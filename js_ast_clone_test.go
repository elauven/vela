package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
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
		prog, diags := ParseJSWithDiagnostics(src)
		if len(diags) > 0 {
			t.Fatalf("%q: %v", src, diags)
		}
		original := map[JsNode]bool{}
		WalkNode(prog, func(n JsNode) bool { original[n] = true; return true })
		clone := &JsProgram{}
		for _, stmt := range prog.Body {
			clone.Body = append(clone.Body, cloneJsStmt(stmt))
		}
		if got, want := PrintJS(clone, PrintMinified), PrintJS(prog, PrintMinified); got != want {
			t.Errorf("%q: clone prints %q, want %q", src, got, want)
		}
		WalkNode(clone, func(n JsNode) bool {
			if original[n] && n != JsNode(clone) {
				t.Errorf("%q: clone shares a %T with the original", src, n)
			}
			return true
		})
	}
}

func TestTemplateExpressionsWithExtendedSyntaxCompile(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available")
	}
	src := "<script>let a = $state(1); const tag = (s, ...v) => s.join('') + v.join('');</script>\n" +
		"<template><p @click={() => { try { a = a + 1 } catch (e) { a = 0 } switch (a) { case 3: a = 1; break; default: } }}>{tag`n${a}`}</p></template>"
	dir := t.TempDir()
	file := filepath.Join(dir, "App.vela")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "App.js")
	if _, err := compileFile(file, out, CompileOptions{}); err != nil {
		t.Fatal(err)
	}
	if msg, err := exec.Command(node, "--check", out).CombinedOutput(); err != nil {
		t.Fatalf("emitted module does not load: %s", msg)
	}
}
