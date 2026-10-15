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
		`if props.Set != nil && !props.Set["title"] {`,
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

func TestBuildSSRGoFileRejectsSingleSelectWithKnownNullBinding(t *testing.T) {
	sfc, err := parseSFC(`<script>let choice = $state(null);</script><template><select bind:value={choice}><option value="one">One</option><option value="two">Two</option></select></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	_, err = BuildSSRGoFile(plan, "components")
	if err == nil || !strings.Contains(err.Error(), "known null server value") {
		t.Fatalf("single-select null SSR error=%v", err)
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
	if !strings.Contains(string(src), "if props.Visible {") || !strings.Contains(string(src), "} else {") || !strings.Contains(string(src), "vela.WriteEscaped(w, vela.String(props.Title))") {
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

// TestBlocksSSRAwaitWithoutPending: the block engine evaluates the operand,
// so a block without a pending branch renders its settled branch (T-7.4f).
func TestBlocksSSRAwaitWithoutPending(t *testing.T) {

	sfc, err := parseSFC(`<script>export let request=null;</script><template>{#await request then value}<p>{value}</p>{/await}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := buildSSRGoRendererWithRegion(plan, "components", "Page.vela", ""); err != nil {
		t.Fatal(err)
	}

}
