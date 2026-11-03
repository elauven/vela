package ssr

import (
	"strings"
	"testing"

	"github.com/elauven/vela/internal/analyzer"
	"github.com/elauven/vela/internal/sfcast"
)

// TestBuildSSRGoFileForDynamicComponents checks the Go of task 070: the
// XComponent adapter of every renderer, imported components as values, and
// the <vela:component> site.
func TestBuildSSRGoFileForDynamicComponents(t *testing.T) {
	source := `<script>
  import { Chart } from './Chart.vela';
  import { Table } from './views/Table.vela';
  export let mode = 'chart';
  export let rows = [];
  const views = { chart: Chart, table: Table };
</script>
<template><main><vela:component :this={views[mode]} :rows={rows} title="T" @on:select={() => {}}><p>slot</p></vela:component></main></template>`
	sfc, err := sfcast.ParseSFC(source)
	if err != nil {
		t.Fatal(err)
	}
	links := map[string]SSRComponentLink{
		"Chart": {LocalName: "Chart", PackageName: "components", GoName: "Chart", ImportPath: "example.com/site/server/components"},
		"Table": {LocalName: "Table", PackageName: "views", GoName: "Table", ImportPath: "example.com/site/server/components/views"},
	}
	plan, err := BuildSSRRenderPlanWithAnalysisAndLinks("Page", sfc, analyzer.AnalyzeScriptAST(sfc.ScriptAST), "vela-page", links)
	if err != nil {
		t.Fatal(err)
	}
	plan.ComponentID = "routes/Page"
	file, region, err := buildSSRGoRendererWithRegion(plan, "components", "src/routes/Page.vela", source)
	if err != nil {
		t.Fatal(err)
	}
	src, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		// Imported components as values: same package bare, another one imported.
		`ssrc_example_com_site_server_components_views "example.com/site/server/components/views"`,
		`.Set("chart", ChartComponent).Set("table", ssrc_example_com_site_server_components_views.TableComponent)`,
		// The site: :this first, props only for a component, events dropped.
		`ssrDynamic0 := any(`,
		`if _, ok := ssrDynamic0.(vela.Component); ok {`,
		`ssrDynamicProps0 = map[string]any{"rows": props.Rows, "title": "T"}`,
		`vela.RenderDynamicComponent(w, "Page#0", "routes/Page.vela:8:40", ssrDynamic0, ssrDynamicProps0, map[string]func(io.Writer) error{"default": func(w io.Writer) error {`,
		// The adapter.
		`func (ssrComponentPage) ComponentID() string {
	return "routes/Page"
}`,
		`if value, ok := props["mode"]; ok {`,
		`if err := vela.AssignProp(&p.Mode, value, "Page.mode"); err != nil {`,
		`p.Set["mode"] = true`,
		`return RenderPage(vela.DynamicWriter(w, ctx, options), p, PageSlots{})`,
		`var PageComponent vela.Component = ssrComponentPage{}`,
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("generated Go misses %q", want)
		}
	}
	if t.Failed() {
		t.Logf("generated:\n%s", src)
	}
	kinds := map[string]bool{}
	for _, record := range region.Records {
		kinds[record.Kind+":"+record.Type] = true
	}
	if !kinds["dynamic-component:unknown"] {
		t.Errorf("no dynamic-component site record: %v", kinds)
	}
}

func TestSSRComponentValueErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		link SSRComponentLink
		want string
	}{
		"client only": {SSRComponentLink{LocalName: "Map", ClientOnly: true, GoName: "Map"}, `component "Map" is client-only and has no server renderer`},
		"unlinked":    {SSRComponentLink{LocalName: "Map"}, `component "Map" is not linked to a generated Go renderer`},
	} {
		source := `<script>import { Map } from './Map.vela'; const v = Map;</script><template><vela:component :this={v} /></template>`
		sfc, err := sfcast.ParseSFC(source)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := BuildSSRRenderPlanWithAnalysisAndLinks("Page", sfc, analyzer.AnalyzeScriptAST(sfc.ScriptAST), "vela-page", map[string]SSRComponentLink{"Map": tc.link})
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = buildSSRGoRendererWithRegion(plan, "components", "src/Page.vela", source)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %v, want %q", name, err, tc.want)
		}
	}
}
