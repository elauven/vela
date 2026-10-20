package main

import (
	"strings"
	"testing"
)

// scope_test.go — exercise the scope graph + diagnostics emitted by
// BuildScopes and DetectUnreachable.  Each test runs a tiny script
// through the JS parser, builds scopes / runs flow analysis, and
// asserts that the expected diagnostic message appears (or doesn't).

func runUnreachable(t *testing.T, src string) *DiagSet {
	t.Helper()
	prog := ParseJS(src)
	if prog == nil {
		t.Fatalf("ParseJS returned nil for: %q", src)
	}
	ds := NewDiagSet("test.js", src)
	DetectUnreachable(prog, ds)
	return ds
}

// expect asserts that at least one diagnostic message contains `sub`.
func expect(t *testing.T, ds *DiagSet, sub string) {
	t.Helper()
	for _, d := range ds.All() {
		if strings.Contains(d.Message, sub) {
			return
		}
	}
	var got []string
	for _, d := range ds.All() {
		got = append(got, d.Message)
	}
	t.Fatalf("expected a diagnostic containing %q, got: %v", sub, got)
}

// expectNone asserts that no diagnostic mentions `sub`.
func expectNone(t *testing.T, ds *DiagSet, sub string) {
	t.Helper()
	for _, d := range ds.All() {
		if strings.Contains(d.Message, sub) {
			t.Fatalf("unexpected diagnostic %q", d.Message)
		}
	}
}

// ─────────────────────────────────────────────
//  Undeclared variables
// ─────────────────────────────────────────────

// ─────────────────────────────────────────────
//  Declarations + scoping
// ─────────────────────────────────────────────

// ─────────────────────────────────────────────
//  TDZ
// ─────────────────────────────────────────────

// ─────────────────────────────────────────────
//  const / import immutability
// ─────────────────────────────────────────────

// ─────────────────────────────────────────────
//  Redeclaration
// ─────────────────────────────────────────────

// ─────────────────────────────────────────────
//  Update expressions
// ─────────────────────────────────────────────

func TestUnreachableDiagnosticHasStableCode(t *testing.T) {
	ds := runUnreachable(t, `function f() { return 1; console.log('dead'); }`)
	for _, diag := range ds.All() {
		if diag.Code == "VELA-FLOW-UNREACHABLE" {
			return
		}
	}
	t.Fatalf("diagnostics = %+v, want VELA-FLOW-UNREACHABLE", ds.All())
}

// ─────────────────────────────────────────────
//  Member expressions: property names aren't variables
// ─────────────────────────────────────────────

// ─────────────────────────────────────────────
//  Object literal shorthand
// ─────────────────────────────────────────────

// ─────────────────────────────────────────────
//  Catch binding
// ─────────────────────────────────────────────

// ─────────────────────────────────────────────
//  Unreachable code
// ─────────────────────────────────────────────

func TestUnreachableAfterReturn(t *testing.T) {
	src := `function f() { return 1; console.log("dead"); }`
	ds := runUnreachable(t, src)
	expect(t, ds, "unreachable code")
}

func TestUnreachableAfterThrow(t *testing.T) {
	src := `function f() { throw new Error(); let x = 1; }`
	ds := runUnreachable(t, src)
	expect(t, ds, "unreachable code")
}

func TestUnreachableAfterIfElseBothReturn(t *testing.T) {
	src := `function f(c) {
		if (c) { return 1; } else { return 2; }
		console.log("dead");
	}`
	ds := runUnreachable(t, src)
	expect(t, ds, "unreachable code")
}

func TestReachableAfterIfWithNoElse(t *testing.T) {
	src := `function f(c) {
		if (c) { return 1; }
		console.log("alive");
	}`
	ds := runUnreachable(t, src)
	expectNone(t, ds, "unreachable")
}

func TestReachableAfterIfElseOneReturns(t *testing.T) {
	src := `function f(c) {
		if (c) { return 1; } else { let x = 2; }
		console.log("alive");
	}`
	ds := runUnreachable(t, src)
	expectNone(t, ds, "unreachable")
}

func TestUnreachableAfterBreak(t *testing.T) {
	src := `for (let i = 0; i < 10; i++) {
		break;
		console.log("dead");
	}`
	ds := runUnreachable(t, src)
	expect(t, ds, "unreachable code")
}

func TestNestedFunctionFlowIsIndependent(t *testing.T) {
	// The inner function body has its own flow — code after the outer
	// return is unreachable, but the inner function being declared
	// later should not itself be flagged twice.
	src := `function f() {
		return 1;
		function g() { return 2; }
	}`
	ds := runUnreachable(t, src)
	// Exactly one "unreachable" warning, on the function declaration.
	count := 0
	for _, d := range ds.All() {
		if strings.Contains(d.Message, "unreachable") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 unreachable warning, got %d", count)
	}
}

func TestTryFinallyThatReturnsTerminates(t *testing.T) {
	src := `function f() {
		try { return 1; } finally { return 2; }
		console.log("dead");
	}`
	ds := runUnreachable(t, src)
	expect(t, ds, "unreachable code")
}
