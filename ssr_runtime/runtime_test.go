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
