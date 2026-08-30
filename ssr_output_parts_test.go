package main

import (
	"strings"
	"testing"
)

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
	if !strings.Contains(string(src), "html.EscapeString(fmt.Sprint(props.Title))") || !strings.Contains(string(src), `io.WriteString(w, "<main class=\"app\"><p>Hello ")`) {
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
	if !strings.Contains(string(src), `io.WriteString(w, "<main title=\"")`) || !strings.Contains(string(src), "html.EscapeString(fmt.Sprint(props.Title))") {
		t.Fatalf("unexpected dynamic attribute renderer:\n%s", src)
	}
	if err := ValidateGeneratedSSRGo("Page.go", src); err != nil {
		t.Fatal(err)
	}
}
