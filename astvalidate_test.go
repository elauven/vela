package main

import (
	"strings"
	"testing"
)

// astvalidate_test.go — proves the validation passes actually fire
// on malformed ASTs.  A validation pass that never catches anything
// is worthless; these tests deliberately construct broken trees and
// assert each pass reports them.

// hasIssueKind reports whether issues contains one of the given kind.
func hasIssueKind(issues []ValidationIssue, kind string) bool {
	for _, iss := range issues {
		if iss.Kind == kind {
			return true
		}
	}
	return false
}

// TestValidateCleanProgram — a well-formed generated program must
// produce zero issues (no false positives).
func TestValidateCleanProgram(t *testing.T) {
	prog := ParseJS(`
		function f($$v) {
			let x = 1;
			if (x === $$v) return;
			x = $$v;
			return x;
		}
	`)
	if issues := ValidateProgram(prog); len(issues) != 0 {
		t.Fatalf("clean program flagged %d issue(s): %+v", len(issues), issues)
	}
}

// TestValidateEmptyIf — an JsIfStmt with a nil test must be caught.
func TestValidateEmptyIf(t *testing.T) {
	prog := &JsProgram{Body: []JsStmt{
		&JsIfStmt{
			Test: nil, // malformed — no condition
			Then: &JsBlock{},
		},
	}}
	issues := ValidateProgram(prog)
	if !hasIssueKind(issues, "empty-node") {
		t.Fatalf("empty if not caught; got %+v", issues)
	}
}

// TestValidateEmptyCall — a call with a nil callee must be caught.
func TestValidateEmptyCall(t *testing.T) {
	prog := &JsProgram{Body: []JsStmt{
		&JsExprStmt{X: &JsCallExpr{Callee: nil}},
	}}
	issues := ValidateProgram(prog)
	if !hasIssueKind(issues, "empty-node") {
		t.Fatalf("empty call not caught; got %+v", issues)
	}
}

// TestValidateTypedNilOperand — a typed-nil operand (the shape a
// codegen bug actually produces) must be caught, not just a plain
// nil interface.
func TestValidateTypedNilOperand(t *testing.T) {
	prog := &JsProgram{Body: []JsStmt{
		&JsExprStmt{X: &JsBinaryExpr{
			Op: "+",
			L:  (*JsIdent)(nil), // typed nil
			R:  &JsLiteral{Raw: "1"},
		}},
	}}
	issues := ValidateProgram(prog)
	if !hasIssueKind(issues, "empty-node") {
		t.Fatalf("typed-nil operand not caught; got %+v", issues)
	}
}

// TestValidateUndeclaredRef — a read of an undeclared $$-prefixed
// wiring variable must be caught.
func TestValidateUndeclaredRef(t *testing.T) {
	// $$missing is never declared anywhere.
	prog := ParseJS(`
		function f() {
			let x = 1;
			x = $$missing;
		}
	`)
	issues := ValidateProgram(prog)
	if !hasIssueKind(issues, "undeclared-ref") {
		t.Fatalf("undeclared $$ ref not caught; got %+v", issues)
	}
}

// TestValidateDeclaredRefOK — a $$-prefixed variable that IS
// declared must not be flagged.
func TestValidateDeclaredRefOK(t *testing.T) {
	prog := ParseJS(`
		function f() {
			let $$wired = 1;
			let y = $$wired;
			return y;
		}
	`)
	issues := ValidateProgram(prog)
	if hasIssueKind(issues, "undeclared-ref") {
		t.Fatalf("declared $$ var wrongly flagged; got %+v", issues)
	}
}

// TestValidateRuntimeGlobalsOK — runtime params and JS built-ins
// are not undeclared references.
func TestValidateRuntimeGlobalsOK(t *testing.T) {
	prog := ParseJS(`
		function f($$target, $$props) {
			$$target.appendChild(document.createElement('div'));
			const p = Promise.resolve($$props);
			return p;
		}
	`)
	issues := ValidateProgram(prog)
	if hasIssueKind(issues, "undeclared-ref") {
		t.Fatalf("runtime globals wrongly flagged; got %+v", issues)
	}
}

// TestValidateUnreachable — a statement after an unconditional
// return must be caught.
func TestValidateUnreachable(t *testing.T) {
	prog := ParseJS(`
		function f() {
			let x = 1;
			return x;
			x = 2;
		}
	`)
	issues := ValidateProgram(prog)
	if !hasIssueKind(issues, "unreachable") {
		t.Fatalf("unreachable statement not caught; got %+v", issues)
	}
}

// TestValidateEveryFixtureClean — the real proof: every diff
// fixture, compiled by the AST generator, must pass validation
// with zero issues.  generateCodeAST already panics on a non-empty
// result, so reaching here at all is the assertion — but we re-run
// ValidateProgram explicitly for a clearer failure message.
func TestValidateEveryFixtureClean(t *testing.T) {
	for name, src := range diffFixtures {
		sfc, err := parseSFC(src)
		if err != nil {
			t.Fatalf("[%s] parseSFC: %v", name, err)
		}
		tmpl, err := parseTemplate(sfc.Template)
		if err != nil {
			t.Fatalf("[%s] parseTemplate: %v", name, err)
		}
		analysis := analyzeScript(sfc.Script)
		if !astCanBuildDirectly(tmpl, analysis) {
			continue // falls back to legacy — not AST-validated
		}
		g := &ASTGen{name: name, scopeID: "vela-000000",
			analysis: analysis, sfc: sfc, fragCounter: -1}
		prog := g.buildModule(tmpl)
		if issues := ValidateProgram(prog); len(issues) > 0 {
			var b strings.Builder
			for _, iss := range issues {
				b.WriteString("\n  [" + iss.Kind + "] " + iss.Msg)
			}
			t.Errorf("[%s] AST failed validation:%s", name, b.String())
		}
	}
}

// TestCaptureScopePanicSafety — captureScope swaps the generator's
// emission buffers out for the duration of fn().  If fn() panics,
// the buffers must still be restored (the fuzzer and test suite
// recover from panics and keep compiling — a corrupted buffer
// would silently poison every later compilation).
func TestCaptureScopePanicSafety(t *testing.T) {
	g := &ASTGen{fragCounter: -1}
	sentinel := []JsStmt{&JsEmptyStmt{baseStmt: baseStmt{genNode()}}}
	g.body = sentinel

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected the injected panic to propagate")
			}
		}()
		g.captureScope(func() {
			g.body = []JsStmt{&JsEmptyStmt{baseStmt: baseStmt{genNode()}}}
			panic("boom")
		})
	}()

	// After the recovered panic, g.body must be the original
	// sentinel — not the inner buffer, not nil.
	if len(g.body) != 1 || g.body[0] != sentinel[0] {
		t.Fatalf("captureScope did not restore g.body after a panic; "+
			"got %d stmt(s)", len(g.body))
	}
}

// TestWithFragmentIDsPanicSafety — withFragmentIDs likewise must
// restore the fragment counter even when fn() panics.
func TestWithFragmentIDsPanicSafety(t *testing.T) {
	g := &ASTGen{fragCounter: -1}
	func() {
		defer func() { recover() }()
		g.withFragmentIDs(func() { panic("boom") })
	}()
	if g.fragCounter != -1 {
		t.Fatalf("withFragmentIDs left fragCounter = %d, want -1",
			g.fragCounter)
	}
}

func TestValidateGeneratedTDZ(t *testing.T) {
	prog := ParseJS(`
		export default function component() {
			const value = $$later;
			let $$later = 1;
			return value;
		}
	`)
	issues := ValidateProgram(prog)
	if !hasIssueKind(issues, "tdz-ref") {
		t.Fatalf("generated TDZ reference not caught; got %+v", issues)
	}
}

func TestValidateGeneratedDuplicateDeclaration(t *testing.T) {
	prog := ParseJS(`
		function component() {
			let $$node = null;
			const $$node = document.createElement("div");
		}
	`)
	issues := ValidateProgram(prog)
	if !hasIssueKind(issues, "duplicate-decl") {
		t.Fatalf("generated duplicate declaration not caught; got %+v", issues)
	}
}

func TestValidateGeneratedFunctionHoisting(t *testing.T) {
	prog := ParseJS(`
		function component() {
			$$render();
			function $$render() {}
		}
	`)
	issues := ValidateProgram(prog)
	if hasIssueKind(issues, "tdz-ref") {
		t.Fatalf("hoisted generated function wrongly flagged; got %+v", issues)
	}
}
