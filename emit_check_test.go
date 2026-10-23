package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The compiler never writes a module it cannot parse back (task 055).
func TestCheckEmittedJSRejectsUnparsableModules(t *testing.T) {
	if err := CheckEmittedJS("export function A() { return 1; }"); err != nil {
		t.Fatalf("valid module rejected: %v", err)
	}
	for _, code := range []string{"const B = class;", "let c=a++b;", "for (await const; x; of[]]) {}"} {
		err := CheckEmittedJS(code)
		if err == nil || !strings.Contains(err.Error(), EmitCheckDiagnosticCode) {
			t.Errorf("%q: err = %v, want %s", code, err, EmitCheckDiagnosticCode)
		}
	}
}

// The character minifier kept `a + +b` and `a - -b` apart only by a space it
// removed, producing `a++b` (task 055).
func TestMinifyKeepsUnarySignsApart(t *testing.T) {
	for src, want := range map[string]string{
		"c = a + +b;": "c=a+ +b;",
		"c = a - -b;": "c=a- -b;",
		"c = a + -b;": "c=a+-b;",
	} {
		if got := Minify(src); got != want {
			t.Errorf("Minify(%q) = %q, want %q", src, got, want)
		}
	}
}

// Syntax that the previous parser accepted but printed wrong, without a
// diagnostic, now compiles to the same program (task 055).
func TestFormerlySilentSyntaxCompiles(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available")
	}
	cases := map[string]string{
		"class expression":   "const B = class extends Object { static k = 1 };",
		"tagged template":    "const tag = (s, ...v) => s.raw.join('|') + v.join(','); const t = tag`a${1}b`;",
		"unicode ident":      "const café = 1; const π = café + 2;",
		"private members":    "class A { #p = 2; static #s = 3; m() { return this.#p + A.#s } static { A.ready = true } }",
		"for await":          "async function f(xs) { let n = 0; for await (const x of xs) n += x; return n }",
		"regex after =":      "const r = /=/.test('=');",
		"dynamic import":     "const load = () => import('./missing.js');",
		"async class method": "class C { async m() { return 1 } *g() { yield 1 } async *ag() {} }",
	}
	for name, script := range cases {
		dir := t.TempDir()
		src := filepath.Join(dir, "App.vela")
		if err := os.WriteFile(src, []byte("<script>\n"+script+"\n</script><template><p>x</p></template>"), 0o644); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(dir, "App.js")
		for _, minify := range []bool{false, true} {
			if _, err := CompileFile(src, out, CompileOptions{Minify: minify}); err != nil {
				t.Errorf("%s (minify=%v): %v", name, minify, err)
				continue
			}
			if msg, err := exec.Command(node, "--check", out).CombinedOutput(); err != nil {
				t.Errorf("%s (minify=%v): emitted module does not load: %s", name, minify, msg)
			}
		}
	}
}

func TestCheckEmittedJSRejectsNestedModuleDeclarations(t *testing.T) {
	if err := CheckEmittedJS("function A() { export const k = 1; }"); err == nil || !strings.Contains(err.Error(), "top level") {
		t.Fatalf("err = %v", err)
	}
}

// A component script exports props only; any other export used to land
// inside the component factory and produce a module that does not load.
func TestComponentScriptRejectsNonPropExports(t *testing.T) {
	for _, line := range []string{
		"export default { a: 1 };",
		"export { a };",
		"export { a as b };",
		"export * from './other.js';",
		"export { x } from './other.js';",
		"export const k = 1;",
		"export function helper() { return 1; }",
		"export class K {}",
		"export let { p } = {};",
	} {
		dir := t.TempDir()
		src := filepath.Join(dir, "App.vela")
		if err := os.WriteFile(src, []byte("<script>\nlet a = 1;\n"+line+"\n</script><template><p>{a}</p></template>"), 0o644); err != nil {
			t.Fatal(err)
		}
		diags, err := CompileFile(src, filepath.Join(dir, "App.js"), CompileOptions{})
		found := false
		for _, d := range diags {
			if d.Code == "VELA-SCRIPT-EXPORT" && d.Line == 3 {
				found = true
			}
		}
		if err == nil || !found {
			t.Errorf("%s: err=%v diags=%v; want VELA-SCRIPT-EXPORT on line 3", line, err, diags)
		}
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "App.vela")
	if err := os.WriteFile(src, []byte("<script>\nexport let title = 'x';\nexport let value = $bindable(1), other = 2;\n</script><template><p>{title}{value}{other}</p></template>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CompileFile(src, filepath.Join(dir, "App.js"), CompileOptions{}); err != nil {
		t.Fatalf("prop exports rejected: %v", err)
	}
}

// Early errors in a component script are positioned script diagnostics
// instead of a module that fails to load (task 055).
func TestComponentScriptReportsEarlyErrors(t *testing.T) {
	for _, line := range []string{
		"function f() { break; }",
		"function f() { continue; }",
		"function f() { while (1) { break nope; } }",
		"class A { constructor() {} constructor() {} }",
		"function f() { super(); }",
		"class A { m() { return this.#nope; } }",
		"const r = /(/;",
		"function f() { await g(); }",
		"const v = await Promise.resolve(1);",
		"function f() { yield 1; }",
	} {
		dir := t.TempDir()
		src := filepath.Join(dir, "App.vela")
		if err := os.WriteFile(src, []byte("<script>\nlet a = 1;\n"+line+"\n</script><template><p>{a}</p></template>"), 0o644); err != nil {
			t.Fatal(err)
		}
		diags, err := CompileFile(src, filepath.Join(dir, "App.js"), CompileOptions{})
		found := false
		for _, d := range diags {
			if d.Code == "VELA-JS-SYNTAX" && d.Line == 3 {
				found = true
			}
		}
		if err == nil || !found {
			t.Errorf("%s: err=%v diags=%v; want VELA-JS-SYNTAX on line 3", line, err, diags)
		}
	}
}

func TestCheckEmittedJSRejectsEarlyErrors(t *testing.T) {
	for _, code := range []string{"function f() { break; }", "export function A() { const v = await x; }"} {
		if err := CheckEmittedJS(code); err == nil {
			t.Errorf("%q accepted", code)
		}
	}
	if err := CheckEmittedJS("const m = await import('./m.js');"); err != nil {
		t.Errorf("top-level await of a module rejected: %v", err)
	}
}
