package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func regionForSource(t *testing.T, source string) (*SSRRegion, error) {
	t.Helper()
	sfc, err := ParseSFC(source)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	_, region, err := buildSSRGoRendererWithRegion(plan, "components", "Page.vela", source)
	region.locate(source)
	return region, err
}

// TestSSRRegionReportsEveryFailureWithReasonChain covers D-10.3a/b and
// §10.1: every failed site of a component is reported in one build, a cause
// shared by several sites appears once with its other sites, and each
// diagnostic carries a position, a reason chain, and a fix.
func TestSSRRegionReportsEveryFailureWithReasonChain(t *testing.T) {
	source := `<script>
  export let text = '';
  export let items = [];
  let paras = $derived(text.localeCompare('NFC'));
</script>
<template>
  <section>
    <p>{items.localeCompare()}</p>
    <ul>{#each paras as para}<li>{para}</li>{/each}</ul>
    <ol>{#each paras as para}<li>{para}</li>{/each}</ol>
    <b>{text}</b>
  </section>
</template>`
	region, err := regionForSource(t, source)
	var failure *SSRRegionError
	if !errors.As(err, &failure) {
		t.Fatalf("error = %T %v, want *SSRRegionError", err, err)
	}
	if lines := strings.Split(err.Error(), "\n"); len(lines) != 2 {
		t.Fatalf("one-line errors = %q, want the map call and the shared $derived cause", lines)
	}
	detail := FormatSSRRegionError("Page.vela", failure)
	for _, want := range []string{
		"Page.vela:8:9: [error] VELA-SSR-EXPR",
		"reason: template text",
		"reason: {#each} iterable → $derived \"paras\"",
		"Page.vela:4:24: [error] VELA-SSR-EXPR",
		"also at Page.vela:10:16 ({#each} iterable)",
		"fix: ",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("diagnostic misses %q:\n%s", want, detail)
		}
	}
	native, unsupported := 0, 0
	for _, record := range region.Records {
		switch record.Outcome {
		case SSROutcomeNative:
			native++
		case SSROutcomeUnsupported:
			unsupported++
		}
	}
	// text, and both {#each} rows' {para} plus <b>{text}</b> are native; the
	// map call, both iterables, and the $derived declaration are not.
	if native == 0 || unsupported != 4 {
		t.Fatalf("records: %d native, %d unsupported, want 4 unsupported: %+v", native, unsupported, region.Records)
	}
}

// TestSSRRegionExcludesEventHandlersAndEffects covers T-2.2: handlers,
// effects, lifecycle hooks, and binding write-back are never translated,
// so constructs the server cannot run produce no site and no diagnostic.
func TestSSRRegionExcludesEventHandlersAndEffects(t *testing.T) {
	source := `<script>
  import { onMount } from 'vela';
  export let label = 'go';
  let count = $state(0);
  let input = $state('');
  function load() { return fetch('/api').then(r => r.json()); }
  $effect(() => { document.title = label + count; });
  onMount(() => { load().then(data => { count = data.length; }); });
</script>
<template>
  <div>
    <button @click={() => { count++; load(); }}>{label}</button>
    <input bind:value={input} />
  </div>
</template>`
	region, err := regionForSource(t, source)
	if err != nil {
		t.Fatalf("handlers and effects must not block SSR: %v", err)
	}
	for _, record := range region.Records {
		if record.Required && record.Outcome != SSROutcomeNative {
			t.Fatalf("unexpected failed site %+v", record)
		}
		if strings.Contains(strings.Join(record.Reads, ","), "load") {
			t.Fatalf("a handler-only function entered the region: %+v", record)
		}
	}
}

func TestSSRRegionReportsTemplateWritesAsImpure(t *testing.T) {
	_, err := regionForSource(t, `<script>let count = $state(0);</script>
<template><p>{count++}</p><p>{count = 2}</p></template>`)
	if err == nil || strings.Count(err.Error(), SSRImpureDiagnosticCode) != 2 {
		t.Fatalf("error = %v, want two %s diagnostics", err, SSRImpureDiagnosticCode)
	}
}

// TestSSRReportWritesSiteRecords covers --ssr-report (§10.4): one entry per
// component with its sites, outcomes, tiers, and types, written also when a
// component fails.
func TestSSRReportWritesSiteRecords(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"Good.vela": `<script>export let name = 'x'; let n = $state(2);</script><template><p :title={name}>{name} {n + 1}</p></template>`,
		"Bad.vela":  `<script>export let items = [];</script><template><p>{items.localeCompare()}</p></template>`,
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(srcRoot, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reportPath := filepath.Join(root, "report.json")
	opts := CompileOptions{StatusWriter: io.Discard, SSRModule: "example.com/report/server", SSRReport: reportPath}
	stderr := os.Stderr
	devNull, _ := os.Open(os.DevNull)
	os.Stderr = devNull
	errorsCount := buildSSRProject(collectVela(srcRoot), srcRoot, filepath.Join(root, "out"), opts, false)
	os.Stderr = stderr
	if errorsCount != 1 {
		t.Fatalf("errors = %d, want 1 (Bad.vela)", errorsCount)
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	var report SSRReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Components) != 2 || report.Components[0].Source != "Bad.vela" || report.Components[1].Source != "Good.vela" {
		t.Fatalf("components = %+v", report.Components)
	}
	bad, good := report.Components[0], report.Components[1]
	if bad.Status != "error" || len(bad.Sites) != 2 || bad.Sites[1].Kind != "text" || bad.Sites[1].Outcome != SSROutcomeUnsupported || bad.Sites[1].Diagnostic == nil || bad.Sites[1].Line != 1 {
		t.Fatalf("bad component = %+v", bad)
	}
	kinds := map[string]SSRSiteRecord{}
	for _, site := range good.Sites {
		kinds[site.Kind+":"+site.Name] = site
	}
	if site := kinds["attribute:title"]; site.Outcome != SSROutcomeNative || site.Type != "string" || site.Tier != "typed" {
		t.Fatalf("title attribute site = %+v", site)
	}
	if site := kinds["state:n"]; site.Required || site.Outcome != SSROutcomeNative || site.Type != "number" {
		t.Fatalf("state declaration site = %+v", site)
	}
	if report.Summary.Components != 2 || report.Summary.ComponentsOK != 1 || report.Summary.Outcomes[SSROutcomeUnsupported] != 1 {
		t.Fatalf("summary = %+v", report.Summary)
	}
}
