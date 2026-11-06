package ssr

import (
	"testing"

	"github.com/elauven/vela/internal/analyzer"
	"github.com/elauven/vela/internal/jsast"
	"github.com/elauven/vela/internal/sfcast"
)

func TestSSRTypeScriptPropTypes(t *testing.T) {
	src := `
  interface User { name: string }
  type Mode = 'a' | 'b';
  type Rows = Array<{ id: number }>;
  export let title: string = 'x';
  export let count: number;
  export let open: boolean = false;
  export let mode: Mode = 'a';
  export let tags: string[] = [];
  export let rows: Rows = [];
  export let user: User | null = null;
  export let data: Record<string, number> = {};
  export let shape: { x: number } = { x: 1 };
  export let maybe: string | undefined;
  export let mixed: string | number = 1;
  export let loose = 'inferred';
  export let none = null;
`
	script := sfcast.NewScriptASTLang(src, 0, true)
	if len(script.Diagnostics) > 0 {
		t.Fatal(script.Diagnostics)
	}
	a := analyzer.AnalyzeScriptAST(script)
	want := map[string][2]string{
		"title": {"string", "ts"}, "count": {"float64", "ts"}, "open": {"bool", "ts"}, "mode": {"string", "ts"},
		"tags": {"[]any", "ts"}, "rows": {"[]any", "ts"}, "user": {"any", "ts"}, "data": {"map[string]any", "ts"},
		"shape": {"map[string]any", "ts"}, "maybe": {"any", "ts"}, "mixed": {"any", "ts"},
		"loose": {"string", "inferred"}, "none": {"any", ""},
	}
	for _, p := range a.Props {
		typ, source := ssrPropType(script, p.Name, p.DefaultAST)
		if got := [2]string{typ.goName(), source}; got != want[p.Name] {
			t.Errorf("%s: %v, want %v", p.Name, got, want[p.Name])
		}
	}
	// JSDoc of a JavaScript component.
	js := sfcast.NewScriptAST("/** @type {number} */\nexport let n = 1;\n", 0)
	if typ, source := ssrPropType(js, "n", nil); typ != SSRTypeFloat || source != "jsdoc" {
		t.Errorf("jsdoc: %v %s", typ, source)
	}
}

func TestSSRTypeScriptHostTypes(t *testing.T) {
	prog, diags := jsast.ParseWithOptions(`export function f(a: number, b: string[], c: Record<string, boolean>, d?: string, e: 'x' | 'y', g: Date, h: number | undefined): Promise<string> { return null!; }`, jsast.ParseOptions{TypeScript: true})
	if len(diags) > 0 {
		t.Fatal(diags)
	}
	fn := prog.Body[0].(*jsast.JsExportDecl).Declaration.(*jsast.JsFuncDecl)
	want := []string{"float64", "[]string", "map[string]bool", "string", "string", "", "*float64"}
	for i, p := range fn.TS.Params {
		if got := ssrTSGoType(p.Type, 0); got != want[i] {
			t.Errorf("parameter %d: %q, want %q", i, got, want[i])
		}
	}
	if got := ssrTSGoType(fn.TS.Return, 0); got != "" {
		t.Errorf("a Promise result maps to %q", got)
	}
	if ssrZeroOfResult("func F(ctx context.Context) (float64, error)") != "0" || ssrZeroOfResult("func F() (any, error)") != "nil" {
		t.Error("zero values of host results")
	}
}
