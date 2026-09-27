package ssr_runtime

import (
	"io"
	"strings"
	"testing"
)

func TestRendererFuncAndWriteString(t *testing.T) {
	renderer := RendererFunc(func(writer io.Writer) error { return WriteString(writer, "rendered") })
	var out strings.Builder
	if err := renderer.Render(&out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "rendered" {
		t.Fatalf("output=%q", out.String())
	}
}

func TestStringAndEscapeString(t *testing.T) {
	if got := String(42); got != "42" {
		t.Fatalf("String=%q", got)
	}
	if got := EscapeString(`<tag attr="x">`); got != "&lt;tag attr=&#34;x&#34;&gt;" {
		t.Fatalf("EscapeString=%q", got)
	}
}

func TestContainsStringUsesSelectMultipleMembership(t *testing.T) {
	if !ContainsString([]any{"one", 2, true}, "2") {
		t.Fatal("multi-select membership must use browser-visible string values")
	}
	if ContainsString([]string{"one", "two"}, "three") {
		t.Fatal("multi-select membership must reject absent values")
	}
	if ContainsString("two", "two") {
		t.Fatal("a scalar select model must not masquerade as a multi-select array")
	}
}

func TestIterPreservesArrayOrderAndRejectsNonArrays(t *testing.T) {
	if got := Iter([]string{"one", "two"}); len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("Iter slice = %#v", got)
	}
	if got := Iter([2]int{3, 4}); len(got) != 2 || got[0] != 3 || got[1] != 4 {
		t.Fatalf("Iter array = %#v", got)
	}
	if got := Iter(map[string]any{"id": "row"}); got != nil {
		t.Fatalf("Iter object = %#v, want nil", got)
	}
}

func TestGet(t *testing.T) {
	if got := Get(map[string]any{"title": "ready"}, "title"); got != "ready" {
		t.Fatalf("Get map value = %#v", got)
	}
	if got := Get(map[string]any{}, "missing"); got != nil {
		t.Fatalf("Get missing value = %#v", got)
	}
	if got := Get("not-an-object", "title"); got != nil {
		t.Fatalf("Get scalar value = %#v", got)
	}
}
