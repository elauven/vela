package main

import (
	"strings"
	"testing"
)

// dce_test.go — tests for EliminateDeadCode and EliminateDeadCodeFixedPoint.
//
// Each test runs the eliminator on a tiny script + template pair and
// asserts that the right bindings are kept or removed.  We also verify
// the resulting script still parses, since DCE that produces broken JS
// is worse than no DCE at all.

func runDCE(t *testing.T, script, template string) string {
	t.Helper()
	a := AnalyzeScript(script)
	out, _ := EliminateDeadCodeFixedPoint(script, a, template)
	// Result must be valid JS.
	if out != "" && ParseJS(out) == nil {
		t.Fatalf("DCE produced unparseable script:\n%s", out)
	}
	return out
}

func assertGone(t *testing.T, out, needle string) {
	t.Helper()
	if strings.Contains(out, needle) {
		t.Fatalf("expected %q removed, got:\n%s", needle, out)
	}
}

func assertKept(t *testing.T, out, needle string) {
	t.Helper()
	if !strings.Contains(out, needle) {
		t.Fatalf("expected %q kept, got:\n%s", needle, out)
	}
}

// ─────────────────────────────────────────────
//  Basic removal
// ─────────────────────────────────────────────

func TestDCERemovesUnusedConst(t *testing.T) {
	out := runDCE(t, `const unused = 5;`, ``)
	assertGone(t, out, "unused")
}

func TestDCERemovesUnusedLet(t *testing.T) {
	out := runDCE(t, `let unused = "x";`, ``)
	assertGone(t, out, "unused")
}

func TestDCERemovesUnusedFunction(t *testing.T) {
	out := runDCE(t, `function unused() { return 1; }`, ``)
	assertGone(t, out, "unused")
}

func TestDCEKeepsUsedBinding(t *testing.T) {
	out := runDCE(t, `const x = 5; console.log(x);`, ``)
	assertKept(t, out, "const x")
}

// ─────────────────────────────────────────────
//  Template-referenced bindings
// ─────────────────────────────────────────────

func TestDCEKeepsTemplateHandler(t *testing.T) {
	out := runDCE(t,
		`function handleClick() { console.log("hi"); }`,
		`<button on:click={handleClick}>x</button>`)
	assertKept(t, out, "handleClick")
}

func TestDCEKeepsTemplateConst(t *testing.T) {
	out := runDCE(t,
		`const TITLE = "Hello";`,
		`<h1>{TITLE}</h1>`)
	assertKept(t, out, "TITLE")
}

func TestDCEIgnoresPartialTemplateMatch(t *testing.T) {
	// "foo" appears only as part of "foobar" — boundary check should
	// reject it, allowing elimination.
	out := runDCE(t,
		`const foo = 1;`,
		`<p>foobar</p>`)
	assertGone(t, out, "const foo")
}

// ─────────────────────────────────────────────
//  Reactivity protection
// ─────────────────────────────────────────────

func TestDCEKeepsStateVar(t *testing.T) {
	// $state binding looks unused to a pure JS scope analyzer because
	// it's referenced from the template via reactive plumbing.
	out := runDCE(t, `let count = $state(0);`, ``)
	assertKept(t, out, "count")
}

func TestDCEKeepsDerivedVar(t *testing.T) {
	out := runDCE(t, `let count = $state(0); let doubled = $derived(count * 2);`, ``)
	assertKept(t, out, "doubled")
}

// ─────────────────────────────────────────────
//  Side-effect protection
// ─────────────────────────────────────────────

func TestDCEKeepsBindingWithCallInit(t *testing.T) {
	out := runDCE(t, `const result = compute();`, ``)
	assertKept(t, out, "result")
}

func TestDCEKeepsBindingWithMemberAccessInit(t *testing.T) {
	// Member access can trigger a getter.
	out := runDCE(t, `const x = obj.foo;`, ``)
	assertKept(t, out, "const x")
}

func TestDCEKeepsBindingWithNewInit(t *testing.T) {
	out := runDCE(t, `const inst = new Thing();`, ``)
	assertKept(t, out, "inst")
}

func TestDCERemovesBindingWithLiteralInit(t *testing.T) {
	out := runDCE(t, `const x = [1, 2, 3];`, ``)
	assertGone(t, out, "const x")
}

func TestDCERemovesBindingWithArrowFnInit(t *testing.T) {
	// Declaring an arrow function has no side effects (calling it
	// would).  Unused → removable.
	out := runDCE(t, `const f = () => doStuff();`, ``)
	assertGone(t, out, "const f")
}

// ─────────────────────────────────────────────
//  Special cases
// ─────────────────────────────────────────────

func TestDCESkipsUnderscoreName(t *testing.T) {
	// Convention: leading underscore = intentionally unused.
	out := runDCE(t, `const _ignore = 1;`, ``)
	assertKept(t, out, "_ignore")
}

func TestDCESkipsExportedConst(t *testing.T) {
	out := runDCE(t, `export const foo = 1;`, ``)
	assertKept(t, out, "foo")
}

func TestDCESkipsExportedFunction(t *testing.T) {
	out := runDCE(t, `export function foo() {}`, ``)
	assertKept(t, out, "foo")
}

func TestDCESkipsVar(t *testing.T) {
	// var bindings hoist in ways that make textual removal risky.
	out := runDCE(t, `var x = 1;`, ``)
	assertKept(t, out, "var x")
}

func TestDCESkipsMultiDeclarator(t *testing.T) {
	out := runDCE(t, `let a = 1, b = 2;`, ``)
	assertKept(t, out, "let a")
	assertKept(t, out, "b = 2")
}

func TestDCESkipsDestructuring(t *testing.T) {
	out := runDCE(t, `const { x } = something();`, ``)
	assertKept(t, out, "const { x }")
}

// ─────────────────────────────────────────────
//  Recursion and cascading
// ─────────────────────────────────────────────

func TestDCEKeepsRecursiveButOtherwiseUnused(t *testing.T) {
	// f calls itself; the self-call counts as a ref, so we
	// conservatively keep it.  This is a documented limitation:
	// detecting "truly dead despite self-reference" would need
	// reachability analysis from exported entry points.
	out := runDCE(t,
		`function f(n) { return n <= 1 ? 1 : n * f(n - 1); }`, ``)
	assertKept(t, out, "function f")
}

func TestDCECascadingRemoval(t *testing.T) {
	// f references x.  Nothing references f.  Round 1: f removed.
	// Round 2: x now has 0 refs, removed too.
	script := `
		let x = 1;
		function f() { return x; }
	`
	out := runDCE(t, script, ``)
	assertGone(t, out, "function f")
	assertGone(t, out, "let x")
}

func TestDCECascadingStopsAtTemplate(t *testing.T) {
	// f is referenced by template → kept.  x is referenced by f → kept.
	script := `
		let x = 1;
		function f() { return x; }
	`
	out := runDCE(t, script, `<button on:click={f}>x</button>`)
	assertKept(t, out, "function f")
	assertKept(t, out, "let x")
}

// ─────────────────────────────────────────────
//  Nested decls (inside function bodies)
// ─────────────────────────────────────────────

func TestDCEInsideFunctionBody(t *testing.T) {
	// Unused `tmp` inside a function should be removable.  The outer
	// function is kept because the template references it.  The
	// template also literally contains the text "tmp", which would
	// otherwise protect a top-level binding — but `tmp` is inside a
	// function and not visible to the template, so the protection
	// shouldn't fire.
	script := `function f() { let tmp = 1; return 42; }`
	out := runDCE(t, script, `<p>tmp</p><button on:click={f}>x</button>`)
	assertGone(t, out, "let tmp")
	assertKept(t, out, "function f")
}

// ─────────────────────────────────────────────
//  Idempotence
// ─────────────────────────────────────────────

func TestDCEIsIdempotent(t *testing.T) {
	// Running twice should produce identical output.
	script := `
		const unused = 1;
		const used = 2;
		console.log(used);
	`
	a := AnalyzeScript(script)
	out1, _ := EliminateDeadCodeFixedPoint(script, a, "")

	a2 := AnalyzeScript(out1)
	out2, _ := EliminateDeadCodeFixedPoint(out1, a2, "")

	if out1 != out2 {
		t.Fatalf("DCE not idempotent:\nfirst pass:\n%s\nsecond pass:\n%s", out1, out2)
	}
}

func TestDCENoChangeReportsFalse(t *testing.T) {
	// A script with nothing to remove should return changed=false.
	a := AnalyzeScript(`const x = 1; console.log(x);`)
	_, changed := EliminateDeadCode(`const x = 1; console.log(x);`, a, "")
	if changed {
		t.Fatalf("expected changed=false")
	}
}

// ─────────────────────────────────────────────
//  Purity check unit tests
// ─────────────────────────────────────────────

func TestIsPureExprLiteral(t *testing.T) {
	check := func(src string, want bool) {
		t.Helper()
		prog := ParseJS("const x = " + src + ";")
		if prog == nil {
			t.Fatalf("parse failed for: %s", src)
		}
		decl := prog.Body[0].(*JsVarDecl)
		got := isPureExpr(decl.Decls[0].Init)
		if got != want {
			t.Errorf("isPureExpr(%q) = %v, want %v", src, got, want)
		}
	}

	check(`1`, true)
	check(`"hi"`, true)
	check(`true`, true)
	check(`null`, true)
	check(`[1, 2, 3]`, true)
	check(`{a: 1, b: 2}`, true)
	check(`x`, true)
	check(`-x`, true)
	check(`a + b`, true)
	check(`a && b`, true)
	check(`x ? a : b`, true)
	check(`() => x + 1`, true)

	check(`f()`, false)
	check(`new Thing()`, false)
	check(`obj.x`, false)
	check(`x = 1`, false)
	check(`[...arr]`, false)
	check(`{...obj}`, false)
	check(`await p`, false)
	check(`delete obj.x`, false)
	check(`void 0`, false)
}
