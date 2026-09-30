package main

import (
	"strings"
	"testing"
)

func TestBuildSSRGoSkeletonUsesStandardGoAST(t *testing.T) {
	sfc, err := parseSFC(`<script>
export let title = "";
export let count = 0;
export let active = false;
export let items = [];
export let config = {};
</script><template><main><h1>{title}</h1></main></template>`)
	if err != nil {
		t.Fatalf("parseSFC: %v", err)
	}
	plan, err := BuildSSRRenderPlan("Greeting", sfc, "vela-greeting")
	if err != nil {
		t.Fatalf("BuildSSRRenderPlan: %v", err)
	}
	file, err := BuildSSRGoSkeleton(plan, "components")
	if err != nil {
		t.Fatalf("BuildSSRGoSkeleton: %v", err)
	}
	source, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatalf("FormatSSRGoFile: %v", err)
	}
	got := string(source)
	for _, want := range []string{
		"package components", `"io"`, `vela "github.com/elauven/vela/ssr_runtime"`, "type GreetingProps struct {", "Title  string", "Count  int", "Active bool", "Items  []any", "Config map[string]any",
		"type GreetingSlots struct {", "func RenderGreetingAt(w io.Writer, props GreetingProps, slots GreetingSlots, instanceID string) error", "func RenderGreeting(w io.Writer, props GreetingProps, slots GreetingSlots) error",
		"func GreetingAt(props GreetingProps, slots GreetingSlots, instanceID string) vela.Renderer", "func Greeting(props GreetingProps, slots GreetingSlots) vela.Renderer",
		"return vela.RendererFunc(func(w io.Writer) error",
		"return nil",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated Go missing %q:\n%s", want, got)
		}
	}
	if err := ValidateGeneratedSSRGo("Greeting.go", source); err != nil {
		t.Fatalf("ValidateGeneratedSSRGo: %v\n%s", err, source)
	}
}

func TestBuildSSRGoSkeletonRejectsInvalidInputs(t *testing.T) {
	if _, err := BuildSSRGoSkeleton(nil, "components"); err == nil {
		t.Fatal("nil plan did not fail")
	}
	plan := &SSRRenderPlan{ComponentName: "notExported"}
	if _, err := BuildSSRGoSkeleton(plan, "components"); err == nil {
		t.Fatal("unexported component did not fail")
	}
	plan.ComponentName = "Demo"
	if _, err := BuildSSRGoSkeleton(plan, "invalid-package"); err == nil {
		t.Fatal("invalid package did not fail")
	}
}

func TestBuildSSRGoFileUsesInstanceAwareRendererForRepeatedStaticChildren(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let visible = true;</script><template><main>{#if visible}<Child /><Child />{:else}<p>Hidden</p>{/if}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysisAndLinks("Parent", sfc, analysis, "vela-parent", map[string]SSRComponentLink{
		"Child": {LocalName: "Child", GoName: "Child", PackageName: "child", ImportPath: "example.com/project/child"},
	})
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "parent")
	if err != nil {
		t.Fatal(err)
	}
	source, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	got := string(source)
	for _, want := range []string{
		"child.ChildAt(child.ChildProps{Set: map[string]bool{}}, child.ChildSlots{}, instanceID+\"--n0--n0--if0--n0--component-Child\")",
		"child.ChildAt(child.ChildProps{Set: map[string]bool{}}, child.ChildSlots{}, instanceID+\"--n0--n0--if0--n1--component-Child\")",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("repeated child Go renderer misses %q:\\n%s", want, got)
		}
	}
}

func TestBuildSSRGoFileScopesEachAndKeyChildrenByRuntimeIdentity(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let rows = []; export let selected = "a";</script><template><main>{#each rows as row}<Child /><Child />{/each}{#key selected}<Child />{/key}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlanWithAnalysisAndLinks("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent", map[string]SSRComponentLink{
		"Child": {LocalName: "Child", GoName: "Child", PackageName: "child", ImportPath: "example.com/project/child"},
	})
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "parent")
	if err != nil {
		t.Fatal(err)
	}
	source, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	got := string(source)
	for _, want := range []string{
		`vela.String(ssrEachIndex0)`,
		`child.ChildAt(child.ChildProps{Set: map[string]bool{}}, child.ChildSlots{}, instanceID+"--each-`,
		`vela.String(props.Selected)`,
		`instanceID+"--key-`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("runtime child scope misses %q:\\n%s", want, got)
		}
	}
}

func TestValidateGeneratedSSRGoRejectsMalformedSource(t *testing.T) {
	if err := ValidateGeneratedSSRGo("broken.go", []byte("package components\nfunc {")); err == nil {
		t.Fatal("malformed generated source did not fail")
	}
}
