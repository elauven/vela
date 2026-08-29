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
		"package components", "import \"io\"", "type GreetingProps struct {", "Title  string", "Count  int", "Active bool", "Items  []any", "Config map[string]any",
		"type GreetingSlots struct {", "func RenderGreeting(w io.Writer, props GreetingProps, slots GreetingSlots) error",
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

func TestValidateGeneratedSSRGoRejectsMalformedSource(t *testing.T) {
	if err := ValidateGeneratedSSRGo("broken.go", []byte("package components\nfunc {")); err == nil {
		t.Fatal("malformed generated source did not fail")
	}
}
