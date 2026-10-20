package codegen

import (
	"testing"

	"github.com/elauven/vela/internal/jsast"
)

// Template expressions are cloned before they are rewritten; a node the
// clone does not know used to be an internal compiler error (task 055).
func TestCloneCoversEveryStatementAndExpression(t *testing.T) {
	sources := []string{
		"class A extends B { #p = 1; static s = 2; static { init() } get v() { return this.#p } async *m() { yield 1 } }",
		"const C = class { k = 1 }; const t = tag`a${b}c`;",
		"try { a() } catch (e) { b(e) } finally { c() }",
		"switch (x) { case 1: f(); break; default: g() }",
		"outer: for (const a of b) { continue outer }",
		"debugger;",
		"async function f() { for await (const x of y) ; return import.meta.url }",
	}
	for _, src := range sources {
		prog, diags := jsast.ParseJSWithDiagnostics(src)
		if len(diags) > 0 {
			t.Fatalf("%q: %v", src, diags)
		}
		original := map[jsast.JsNode]bool{}
		jsast.WalkNode(prog, func(n jsast.JsNode) bool { original[n] = true; return true })
		clone := &jsast.JsProgram{}
		for _, stmt := range prog.Body {
			clone.Body = append(clone.Body, cloneJsStmt(stmt))
		}
		if got, want := jsast.PrintJS(clone, jsast.PrintMinified), jsast.PrintJS(prog, jsast.PrintMinified); got != want {
			t.Errorf("%q: clone prints %q, want %q", src, got, want)
		}
		jsast.WalkNode(clone, func(n jsast.JsNode) bool {
			if original[n] && n != jsast.JsNode(clone) {
				t.Errorf("%q: clone shares a %T with the original", src, n)
			}
			return true
		})
	}
}
