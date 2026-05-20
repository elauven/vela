package main

import (
	"strings"
	"testing"
)

// reactivity_test.go — tests for the reactivity-aware diagnostics in
// reactivity.go.  Each test runs one of the validators against a tiny
// hand-rolled script + analysis and asserts the expected diagnostic.

func runDerivedPurity(t *testing.T, script string) *DiagSet {
	t.Helper()
	prog := ParseJS(script)
	if prog == nil {
		t.Fatalf("parse failed: %q", script)
	}
	a := analyzeScript(script)
	ds := NewDiagSet("test.vela", script)
	validateDerivedPurity(prog, a, ds)
	return ds
}

func runEffectBody(t *testing.T, script string) *DiagSet {
	t.Helper()
	prog := ParseJS(script)
	if prog == nil {
		t.Fatalf("parse failed: %q", script)
	}
	a := analyzeScript(script)
	ds := NewDiagSet("test.vela", script)
	validateEffectBody(prog, a, ds)
	return ds
}

func runTemplateRefs(t *testing.T, script, template string) *DiagSet {
	t.Helper()
	prog := ParseJS(script)
	if prog == nil {
		t.Fatalf("script parse failed: %q", script)
	}
	a := analyzeScript(script)
	ds := NewDiagSet("test.vela", script+"\n"+template)
	scope := BuildScopes(prog, NewDiagSet("", script))
	nodes, err := parseTemplate(template)
	if err != nil {
		t.Fatalf("template parse failed: %v", err)
	}
	validateTemplateRefs(nodes, a, scope, template, ds)
	return ds
}

// ─────────────────────────────────────────────
//  $derived purity
// ─────────────────────────────────────────────

func TestDerivedPure_NoWarnings(t *testing.T) {
	ds := runDerivedPurity(t, `
		let count = $state(0);
		let doubled = $derived(count * 2);
	`)
	if len(ds.All()) != 0 {
		t.Fatalf("expected no diagnostics, got: %v", ds.All())
	}
}

func TestDerivedAssignsStateVar(t *testing.T) {
	ds := runDerivedPurity(t, `
		let count = $state(0);
		let bad = $derived((count = 5));
	`)
	expect(t, ds, `writes to $state variable "count"`)
	expect(t, ds, "infinite reactive loop")
}

func TestDerivedAssignsOtherVar(t *testing.T) {
	ds := runDerivedPurity(t, `
		let x = 1;
		let bad = $derived((x = 5, x * 2));
	`)
	expect(t, ds, `writes to "x"`)
	expectNone(t, ds, "infinite reactive loop")
}

func TestDerivedIncrementsStateVar(t *testing.T) {
	ds := runDerivedPurity(t, `
		let count = $state(0);
		let bad = $derived(count++);
	`)
	expect(t, ds, `mutates $state variable "count"`)
}

func TestDerivedAwaitIsWarned(t *testing.T) {
	ds := runDerivedPurity(t, `
		let result = $derived(await fetch('/api'));
	`)
	expect(t, ds, "uses await")
}

func TestDerivedMemberAssignmentIsWarned(t *testing.T) {
	ds := runDerivedPurity(t, `
		let obj = {};
		let bad = $derived((obj.x = 1, obj.x));
	`)
	expect(t, ds, "contains an assignment")
}

func TestDerivedFunctionCallIsSilent(t *testing.T) {
	// We don't try to flag arbitrary function calls — that would
	// require call-graph analysis.  Documented behavior.
	ds := runDerivedPurity(t, `
		function compute(n) { return n * 2; }
		let count = $state(0);
		let doubled = $derived(compute(count));
	`)
	if len(ds.All()) != 0 {
		t.Fatalf("expected no diagnostics on plain function call, got: %v", ds.All())
	}
}

// ─────────────────────────────────────────────
//  $effect body
// ─────────────────────────────────────────────

func TestEffectNoDeps_Warns(t *testing.T) {
	ds := runEffectBody(t, `
		$effect(() => { console.log("hi"); });
	`)
	expect(t, ds, "no reactive variables")
}

func TestEffectWithStateDep_Silent(t *testing.T) {
	ds := runEffectBody(t, `
		let count = $state(0);
		$effect(() => { console.log(count); });
	`)
	expectNone(t, ds, "no reactive variables")
}

// ─────────────────────────────────────────────
//  Template ref resolution
// ─────────────────────────────────────────────

func TestTemplateRefsUndeclared(t *testing.T) {
	ds := runTemplateRefs(t,
		`let count = $state(0);`,
		`<p>{nonexistent}</p>`)
	expect(t, ds, `template references "nonexistent"`)
}

func TestTemplateRefsKnownStateVar(t *testing.T) {
	ds := runTemplateRefs(t,
		`let count = $state(0);`,
		`<p>{count}</p>`)
	expectNone(t, ds, "template references")
}

func TestTemplateRefsKnownDerived(t *testing.T) {
	ds := runTemplateRefs(t,
		`let count = $state(0); let doubled = $derived(count * 2);`,
		`<p>{doubled}</p>`)
	expectNone(t, ds, "template references")
}

func TestTemplateRefsKnownFunction(t *testing.T) {
	ds := runTemplateRefs(t,
		`function handleClick() {}`,
		`<button on:click={handleClick}>x</button>`)
	expectNone(t, ds, "template references")
}

func TestTemplateRefsMemberAccessIsResolved(t *testing.T) {
	// `user.name` — `user` is a reference, `name` is a property name.
	ds := runTemplateRefs(t,
		`let user = $state({ name: "alice" });`,
		`<p>{user.name}</p>`)
	expectNone(t, ds, "template references")
}

func TestTemplateRefsUnknownObjectIsWarned(t *testing.T) {
	ds := runTemplateRefs(t,
		``,
		`<p>{unknownObj.name}</p>`)
	expect(t, ds, `template references "unknownObj"`)
}

func TestTemplateRefsBuiltinsAreSilent(t *testing.T) {
	ds := runTemplateRefs(t,
		``,
		`<p>{Math.PI}</p>`)
	expectNone(t, ds, "template references")
}

func TestTemplateRefsIfCond(t *testing.T) {
	// Using attribute-style #if so the parser produces an
	// ElementNode with IfCond set rather than an {#if} block.
	ds := runTemplateRefs(t,
		``,
		`<p #if="nope">x</p>`)
	expect(t, ds, `template references "nope"`)
}

func TestTemplateRefsEachIter(t *testing.T) {
	ds := runTemplateRefs(t,
		``,
		`<p #each="nope as item">{item}</p>`)
	expect(t, ds, `template references "nope"`)
}

func TestTemplateRefsDeduplicatesPerExpression(t *testing.T) {
	// `{foo + foo + foo}` — only one warning for foo, not three.
	ds := runTemplateRefs(t,
		``,
		`<p>{foo + foo + foo}</p>`)
	count := 0
	for _, d := range ds.All() {
		if strings.Contains(d.Message, `"foo"`) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 warning for foo, got %d", count)
	}
}
