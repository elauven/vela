package main

import (
	"strings"
	"testing"
)

func TestBuildSSRGoFileAppliesDefaultPropsFromExplicitPresence(t *testing.T) {
	sfc, err := parseSFC(`<script>export let title = "fallback";</script><template><p>{title}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	src, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"map[string]bool `json:\"-\"`",
		`if !vela.PropProvided(props, "title") {`,
		`props.Title = "fallback"`,
	} {
		if !strings.Contains(string(src), want) {
			t.Fatalf("default-prop renderer misses %q:\n%s", want, src)
		}
	}
	if err := ValidateGeneratedSSRGo("Page.go", src); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSSRGoFileWithOutputPartsRendersElementTextProp(t *testing.T) {
	sfc, err := parseSFC(`<script>export let title="";</script><template><main class="app"><p>Hello {title}</p></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	parts, err := BuildSSRRenderOutputParts(plan.Nodes)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 3 || !parts[1].Dynamic() || parts[0].Literal != `<main class="app"><p>Hello ` {
		t.Fatalf("unexpected output parts: %#v", parts)
	}
	file, err := BuildSSRGoFileWithOutputParts(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	src, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "vela.WriteString(w, vela.EscapeString(vela.String(props.Title)))") || !strings.Contains(string(src), `vela.WriteString(w, "<main class=\"app\"><p>Hello ")`) {
		t.Fatalf("unexpected Go renderer:\n%s", src)
	}
	if err := ValidateGeneratedSSRGo("Page.go", src); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSSRGoFileWithOutputPartsRendersDynamicAttribute(t *testing.T) {
	sfc, _ := parseSFC(`<script>export let title="";</script><template><main :title={title}></main></template>`)
	plan, _ := BuildSSRRenderPlan("Page", sfc, "vela-page")
	file, err := BuildSSRGoFileWithOutputParts(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	src, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `vela.WriteString(w, "<main title=\"")`) || !strings.Contains(string(src), "vela.WriteString(w, vela.EscapeString(vela.String(props.Title)))") {
		t.Fatalf("unexpected dynamic attribute renderer:\n%s", src)
	}
	if err := ValidateGeneratedSSRGo("Page.go", src); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSSRGoFileRendersClassAndStyleDirectives(t *testing.T) {
	sfc, err := parseSFC(`<script>export let active=false; export let color="";</script><template><button class="base" class:selected={active} style="width: 1px" style:color={color}>Save</button></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	src, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	output := string(src)
	for _, want := range []string{
		`vela.WriteString(w, "<button class=\"base")`,
		`if props.Active {`,
		`vela.WriteString(w, " selected")`,
		`vela.WriteString(w, "\" style=\"width: 1px;color:")`,
		`vela.EscapeString(vela.String(props.Color))`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("missing %q in SSR renderer:\n%s", want, output)
		}
	}
	if strings.Contains(output, "class:selected") || strings.Contains(output, "style:color") {
		t.Fatalf("SSR directive leaked as an HTML attribute:\n%s", output)
	}
	if err := ValidateGeneratedSSRGo("page.go", src); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSSRGoFileRendersValueBindingForHydration(t *testing.T) {
	sfc, err := parseSFC(`<script>export let title="";</script><template><input bind:value={title} /></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	source, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), `vela.WriteString(w, "<input value=\"")`) || !strings.Contains(string(source), "vela.WriteString(w, vela.EscapeString(vela.String(props.Title)))") {
		t.Fatalf("bind:value SSR output missing initial value:\n%s", source)
	}
	if err := ValidateGeneratedSSRGo("page.go", source); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSSRGoFileRendersTextareaValueBindingAsText(t *testing.T) {
	sfc, err := parseSFC(`<script>export let note="";</script><template><textarea bind:value={note} /></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	source, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source), `textarea value=`) || !strings.Contains(string(source), `vela.WriteString(w, "<textarea>")`) || !strings.Contains(string(source), "vela.WriteString(w, vela.EscapeString(vela.String(props.Note)))") {
		t.Fatalf("textarea bind:value SSR output is not text content:\n%s", source)
	}
	if err := ValidateGeneratedSSRGo("page.go", source); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSSRGoFileRendersSelectValueBindingForHydration(t *testing.T) {
	sfc, err := parseSFC(`<script>export let choice="b";</script><template><select bind:value={choice}><option value="a">A</option><option value="b">B</option></select></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	source, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	output := string(source)
	for _, want := range []string{`vela.WriteString(w, "<select>")`, `if vela.String(props.Choice) == "a" {`, `if vela.String(props.Choice) == "b" {`, `vela.WriteString(w, " selected")`} {
		if !strings.Contains(output, want) {
			t.Fatalf("select bind:value SSR output misses %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, `select value=`) {
		t.Fatalf("select bind:value must select an option rather than render a select value attribute:\n%s", output)
	}
	if err := ValidateGeneratedSSRGo("page.go", source); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSSRGoFileRejectsSelectBindingOptionWithoutStaticValue(t *testing.T) {
	sfc, err := parseSFC(`<script>export let choice="a";</script><template><select bind:value={choice}><option>A</option></select></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	_, err = BuildSSRGoFile(plan, "components")
	if err == nil || !strings.Contains(err.Error(), "requires an explicit static value") {
		t.Fatalf("select binding without a static option value error=%v", err)
	}
}

func TestBuildSSRGoFileRendersMultipleSelectValueBinding(t *testing.T) {
	sfc, err := parseSFC(`<script>export let choice=["a", "c"];</script><template><select multiple bind:value={choice}><option value="a">A</option><option value="b">B</option><option :value={choice[1]}>C</option></select></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	source, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	output := string(source)
	for _, want := range []string{`vela.WriteString(w, "<select multiple>")`, `vela.ContainsString(props.Choice, "a")`, `vela.ContainsString(props.Choice, vela.String(props.Choice[1]))`, `vela.WriteString(w, " selected")`} {
		if !strings.Contains(output, want) {
			t.Fatalf("multiple select SSR output misses %q:\n%s", want, output)
		}
	}
	if err := ValidateGeneratedSSRGo("page.go", source); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSSRGoFileRendersCheckedBindingForHydration(t *testing.T) {
	sfc, err := parseSFC(`<script>export let done=false;</script><template><input type="checkbox" bind:checked={done} /></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	source, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`vela.WriteString(w, "<input type=\"checkbox\"")`,
		"if props.Done {",
		`vela.WriteString(w, " checked")`,
	} {
		if !strings.Contains(string(source), want) {
			t.Fatalf("bind:checked SSR output misses %q:\n%s", want, source)
		}
	}
	if err := ValidateGeneratedSSRGo("page.go", source); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSSRGoFileRejectsUnsafeBindings(t *testing.T) {
	for _, source := range []string{
		`<script>export let files=[];</script><template><input type="file" bind:files={files} /></template>`,
	} {
		sfc, err := parseSFC(source)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
		if err != nil {
			t.Fatal(err)
		}
		_, err = BuildSSRGoFile(plan, "components")
		if err == nil || !strings.Contains(err.Error(), SSRExpressionDiagnosticCode) {
			t.Fatalf("binding error=%v", err)
		}
	}
}

func TestBuildSSRGoFileOmitsBindThisForHydration(t *testing.T) {
	sfc, err := parseSFC(`<script>export let input=null;</script><template><input bind:this={input} /></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	source, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source), "bind:this") {
		t.Fatalf("bind:this leaked into server HTML:\n%s", source)
	}
	if err := ValidateGeneratedSSRGo("page.go", source); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSSRGoFileDoesNotExecuteEffects(t *testing.T) {
	sfc, err := parseSFC(`<script>$effect(() => { alert("unexpected"); });</script><template><p>server only</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	src, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "alert") || !strings.Contains(string(src), "server only") {
		t.Fatalf("effect leaked into server renderer:\n%s", src)
	}
}

func TestBuildSSRGoFileRendersIfBranches(t *testing.T) {
	sfc, err := parseSFC(`<script>export let visible=false; export let title="";</script><template>{#if visible}<p>{title}</p>{:else}<p>hidden</p>{/if}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	src, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "if props.Visible {") || !strings.Contains(string(src), "} else {") || !strings.Contains(string(src), "vela.WriteString(w, vela.EscapeString(vela.String(props.Title)))") {
		t.Fatalf("unexpected if renderer:\n%s", src)
	}
	if err := ValidateGeneratedSSRGo("Page.go", src); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSSRGoFileRendersEachAndEmptyBranch(t *testing.T) {
	sfc, err := parseSFC(`<script>export let items=[];</script><template>{#each items as item, index}<p>{index}:{item}</p>{:else}<p>empty</p>{/each}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	src, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "ssrEach0 := vela.Iter(props.Items)") || !strings.Contains(string(src), "if len(ssrEach0) == 0") || !strings.Contains(string(src), "for ssrEachIndex0, ssrEachItem0 := range ssrEach0") {
		t.Fatalf("unexpected each renderer:\n%s", src)
	}
	if !strings.Contains(string(src), "vela.String(ssrEachItem0)") || !strings.Contains(string(src), "vela.String(ssrEachIndex0)") {
		t.Fatalf("missing each binding output:\n%s", src)
	}
	if err := ValidateGeneratedSSRGo("Page.go", src); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSSRGoFileEmitsStableEventMarker(t *testing.T) {
	sfc, err := parseSFC(`<template><button @click={() => {}}>Save</button></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Events) != 1 || plan.Events[0].EventKey != "vela-page-e0" {
		t.Fatalf("unexpected planned events: %#v", plan.Events)
	}
	parts, err := BuildSSRRenderOutputParts(plan.Nodes)
	if err != nil || len(parts) != 1 || !strings.Contains(parts[0].Literal, `data-vela-event="vela-page-e0"`) {
		t.Fatalf("unexpected event output parts: %#v err=%v", parts, err)
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	src, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `data-vela-event=\"vela-page-e0\"`) {
		t.Fatalf("missing event marker:\n%s", src)
	}
	if err := ValidateGeneratedSSRGo("Page.go", src); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSSRGoFileRendersKeyAndAwaitPending(t *testing.T) {
	source := `<script>export let identity=0; export let request=null;</script><template>{#key identity}<p>key body</p>{/key}{#await request}<p>loading</p>{:then value}<p>{value}</p>{:catch error}<p>{error}</p>{/await}</template>`
	sfc, err := parseSFC(source)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	src, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "key body") || !strings.Contains(string(src), "loading") || !strings.Contains(string(src), `case "then":`) || !strings.Contains(string(src), `ssrAwaitSnapshot1.Value`) {
		t.Fatalf("unexpected static key or await output:\n%s", src)
	}
	if err := ValidateGeneratedSSRGo("page.go", src); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSSRGoFileRendersSettledNestedKeyedAwaitRows(t *testing.T) {
	sfc, err := parseSFC(`<script>export let rows=[];export let request=null;</script><template><main>{#each rows as row (row.id)}{#await request}<p>pending</p>{:then value}{#each row.fields as field (field.id)}<p>{field.title}</p>{/each}{:catch error}{#each row.fields as field (field.id)}<p>{field.title}</p>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	src, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`ssrEach0 := vela.Iter(props.Rows)`,
		`ssrEach1 := vela.Iter(vela.Get(ssrEachItem0, "fields"))`,
		`ssrEach2 := vela.Iter(vela.Get(ssrEachItem0, "fields"))`,
		`vela.Get(ssrEachItem1, "title")`,
		`case "then":`,
		`case "catch":`,
		`"<!--r:"+(instanceID+"--each-`,
	} {
		if !strings.Contains(string(src), want) {
			t.Fatalf("settled nested keyed-await renderer misses %q:\n%s", want, src)
		}
	}
	if err := ValidateGeneratedSSRGo("Page.go", src); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSSRGoFileEmitsPairedMarkersForDynamicRanges(t *testing.T) {
	sfc, err := parseSFC(`<script>export let title=""; export let show=false; export let items=[];</script><template><p>{title}</p>{#if show}<b>yes</b>{/if}{#each items as item}<i>{item}</i>{/each}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	source, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`func RenderPageAt(w io.Writer, props PageProps, slots PageSlots, instanceID string) error`,
		`"<!--v:"+instanceID+":0-->"`,
		`"<!--/v:"+instanceID+":0-->"`,
		`return RenderPageAt(w, props, slots, "vela-page")`,
	} {
		if !strings.Contains(string(source), want) {
			t.Fatalf("generated renderer misses %q:\n%s", want, source)
		}
	}
}

func TestBuildSSRGoFileEmitsPairedMarkersForKeyedEachRows(t *testing.T) {
	sfc, err := parseSFC(`<script>export let items=[];</script><template><main>{#each items as item (item)}<p>{item}</p>{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	source, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"<!--r:"+(instanceID+"--each-`, `"<!--/r:"+(instanceID+"--each-`} {
		if !strings.Contains(string(source), want) {
			t.Fatalf("keyed row marker missing %q:\n%s", want, source)
		}
	}
}

func TestBuildSSRGoFileRejectsAwaitWithoutPending(t *testing.T) {
	sfc, err := parseSFC(`<script>export let request=null;</script><template>{#await request then value}<p>{value}</p>{/await}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	_, err = BuildSSRGoFile(plan, "components")
	if err == nil || !strings.Contains(err.Error(), SSRExpressionDiagnosticCode) {
		t.Fatalf("err=%v", err)
	}
}
