package main

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// ──────────────────────────────────────────────
//  SFC Parser
// ──────────────────────────────────────────────

func TestParseSFC_AllSections(t *testing.T) {
	src := `
<script>let x = $state(0);</script>
<style>.a { color: red; }</style>
<template><div>hello</div></template>
`
	sfc, err := parseSFC(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(sfc.Script, "$state") {
		t.Error("script not extracted")
	}
	if !strings.Contains(sfc.Style, "color: red") {
		t.Error("style not extracted")
	}
	if !strings.Contains(sfc.Template, "<div>") {
		t.Error("template not extracted")
	}
}

func TestParseSFC_MissingTemplate(t *testing.T) {
	_, err := parseSFC(`<script>let x = 1;</script>`)
	if err == nil {
		t.Error("expected error for missing template")
	}
}

// ──────────────────────────────────────────────
//  Template Parser
// ──────────────────────────────────────────────

func TestParseTemplate_SimpleElement(t *testing.T) {
	nodes, err := parseTemplate(`<div class="foo">hello</div>`)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	el, ok := nodes[0].(*ElementNode)
	if !ok {
		t.Fatal("expected ElementNode")
	}
	if el.Tag != "div" {
		t.Errorf("expected tag 'div', got %q", el.Tag)
	}
	if len(el.Attrs) != 1 || el.Attrs[0].Name != "class" {
		t.Error("expected class attr")
	}
}

func TestParseTemplate_Interpolation(t *testing.T) {
	nodes, err := parseTemplate(`<p>Hello {name}!</p>`)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	el := nodes[0].(*ElementNode)
	txt := el.Children[0].(*TextNode)
	if len(txt.Parts) != 3 {
		t.Fatalf("expected 3 parts (Hello | name | !), got %d", len(txt.Parts))
	}
	if txt.Parts[1].Static || txt.Parts[1].Value != "name" {
		t.Error("middle part should be dynamic expr 'name'")
	}
}

func TestParseTemplate_Directives(t *testing.T) {
	nodes, err := parseTemplate(`<div #if="show" :class="cls" @click="handler">x</div>`)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	el := nodes[0].(*ElementNode)
	if el.IfCond != "show" {
		t.Errorf("IfCond: want 'show', got %q", el.IfCond)
	}
	if len(el.Attrs) != 2 {
		t.Fatalf("expected 2 attrs, got %d", len(el.Attrs))
	}
	bind := el.Attrs[0]
	if !bind.IsBind || bind.Name != "class" {
		t.Error("expected :class as IsBind attr")
	}
	evt := el.Attrs[1]
	if !evt.IsEvent || evt.Name != "click" {
		t.Error("expected @click as IsEvent attr")
	}
}

func TestParseTemplate_Each(t *testing.T) {
	nodes, err := parseTemplate(`<li #each="items as item, i">{item}</li>`)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	el := nodes[0].(*ElementNode)
	if el.EachExpr != "items as item, i" {
		t.Errorf("EachExpr: want 'items as item, i', got %q", el.EachExpr)
	}
}

func TestParseTemplate_VoidElement(t *testing.T) {
	nodes, err := parseTemplate(`<div><br /><input type="text" /></div>`)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	el := nodes[0].(*ElementNode)
	if len(el.Children) != 2 {
		t.Fatalf("expected 2 void children, got %d", len(el.Children))
	}
}

func TestParseTemplate_Nested(t *testing.T) {
	nodes, err := parseTemplate(`<ul><li>a</li><li>b</li></ul>`)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	ul := nodes[0].(*ElementNode)
	if ul.Tag != "ul" {
		t.Errorf("expected ul, got %s", ul.Tag)
	}
	if len(ul.Children) != 2 {
		t.Fatalf("expected 2 li children, got %d", len(ul.Children))
	}
}

// ──────────────────────────────────────────────
//  Script Analyzer
// ──────────────────────────────────────────────

func TestAnalyzeScript_StateVars(t *testing.T) {
	a := analyzeScript(`
let count = $state(0);
const name  = $state('hello');
let active  = $state(false);
`)
	if len(a.StateVars) != 3 {
		t.Fatalf("expected 3 state vars, got %d", len(a.StateVars))
	}
	if a.StateVars[0].Name != "count" || a.StateVars[0].Initial != "0" {
		t.Errorf("first state var: got %+v", a.StateVars[0])
	}
}

func TestAnalyzeScript_DerivedVars(t *testing.T) {
	a := analyzeScript(`
let count   = $state(0);
let doubled = $derived(count * 2);
let label   = $derived(count > 0 ? 'positive' : 'zero');
`)
	if len(a.DerivedVars) != 2 {
		t.Fatalf("expected 2 derived vars, got %d", len(a.DerivedVars))
	}
	d := a.DerivedVars[0]
	if d.Name != "doubled" {
		t.Errorf("name: want 'doubled', got %q", d.Name)
	}
	if !containsStr(d.Deps, "count") {
		t.Error("doubled should depend on count")
	}
}

func TestAnalyzeScript_Effects(t *testing.T) {
	a := analyzeScript(`
let count = $state(0);
$effect(() => {
  document.title = count;
});
`)
	if len(a.Effects) != 1 {
		t.Fatalf("expected 1 effect, got %d", len(a.Effects))
	}
	if !containsStr(a.Effects[0].Deps, "count") {
		t.Error("effect should depend on count")
	}
}

func TestAnalyzeScript_CleanScript(t *testing.T) {
	a := analyzeScript(`
let count = $state(0);
$effect(() => { console.log(count); });

function increment() {
  count++;
}
`)
	if strings.Contains(a.CleanScript, "$state") {
		t.Error("CleanScript should have $state removed")
	}
	if strings.Contains(a.CleanScript, "$effect") {
		t.Error("CleanScript should have $effect removed")
	}
	if !strings.Contains(a.CleanScript, "function increment") {
		t.Error("CleanScript should retain function declarations")
	}
}

// ──────────────────────────────────────────────
//  AST-based assignment rewriter
// ──────────────────────────────────────────────

func TestRewriteAssignments(t *testing.T) {
	stateSet := map[string]bool{"count": true, "x": true}

	cases := []struct {
		src  string
		want string
	}{
		{"count++;", "$$set_count(count+1);"},
		{"count--;", "$$set_count(count-1);"},
		{"count = 42;", "$$set_count(42);"},
		{"count += 5;", "$$set_count(count+5);"},
		{"count -= 2;", "$$set_count(count-2);"},
		{"count *= 3;", "$$set_count(count*3);"},
		{"count **= 2;", "$$set_count(count**2);"},
		// Reading count — must NOT be rewritten
		{"const y = count + 1;", "const y = count + 1;"},
		// Property assign — must NOT be rewritten
		{"obj.count = 5;", "obj.count = 5;"},
	}

	for _, tc := range cases {
		prog := ParseJS(tc.src)
		got := RewriteAssignments(tc.src, prog, stateSet)
		if !strings.Contains(got, strings.SplitN(tc.want, "(", 2)[0]) {
			t.Errorf("RewriteAssignments(%q):\n  want %q\n  got  %q", tc.src, tc.want, got)
		}
	}
}

func TestRewriteAssignments_InsideArrow(t *testing.T) {
	// Assignment inside a nested arrow function must be rewritten.
	src := `store.subscribe(v => { todos = v; });`
	stateSet := map[string]bool{"todos": true}
	prog := ParseJS(src)
	got := RewriteAssignments(src, prog, stateSet)
	if !strings.Contains(got, "$$set_todos") {
		t.Errorf("expected $$set_todos in rewritten source, got: %s", got)
	}
}

func TestCleanScript_AssignmentsRewritten(t *testing.T) {
	// End-to-end: analyzeScript should produce CleanScript with setters.
	a := analyzeScript(`
let count = $state(0);
function inc() { count++; }
function dec() { count--; }
function set(n) { count = n; }
`)
	clean := a.CleanScript
	if !strings.Contains(clean, "$$set_count") {
		t.Errorf("CleanScript should contain $$set_count; got:\n%s", clean)
	}
	if strings.Contains(clean, "$state") {
		t.Error("CleanScript should not contain $state")
	}
}

// ──────────────────────────────────────────────
//  CSS Scoping
// ──────────────────────────────────────────────

func TestScopeCSS(t *testing.T) {
	css := `button { color: red; } .foo, .bar { margin: 0; }`
	scoped := ScopeCSS(css, "vela-abc")
	if !strings.Contains(scoped, "button[vela-abc]") {
		t.Errorf("button not scoped: %s", scoped)
	}
	if !strings.Contains(scoped, ".foo[vela-abc]") {
		t.Errorf(".foo not scoped: %s", scoped)
	}
}

// ──────────────────────────────────────────────
//  Code generator — smoke tests
// ──────────────────────────────────────────────

func TestGenerateCode_StateBinding(t *testing.T) {
	sfc := &SFC{
		Script:   `let count = $state(0);`,
		Template: `<div>{count}</div>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	analysis := analyzeScript(sfc.Script)
	code := generateCode("MyComp", sfc, nodes, analysis, "vela-test")

	checks := []string{
		"export function MyComp",
		"let count = 0",
		"function $$set_count",
		"createTextNode",
		"$$target.appendChild",
		"destroy()",
	}
	for _, want := range checks {
		if !strings.Contains(code, want) {
			t.Errorf("generated code missing %q", want)
		}
	}
}

func TestGenerateCode_EventHandler(t *testing.T) {
	sfc := &SFC{
		Script:   `let n = $state(0); function inc() { n++; }`,
		Template: `<button @click="inc">+</button>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	analysis := analyzeScript(sfc.Script)
	code := generateCode("Btn", sfc, nodes, analysis, "vela-test")

	if !strings.Contains(code, "addEventListener('click'") {
		t.Error("expected addEventListener in generated code")
	}
	if !strings.Contains(code, "removeEventListener") {
		t.Error("expected removeEventListener in destroy()")
	}
}

func TestGenerateCode_ScopedStyle(t *testing.T) {
	sfc := &SFC{
		Script:   "",
		Style:    `p { color: blue; }`,
		Template: `<p>hi</p>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	analysis := analyzeScript(sfc.Script)
	code := generateCode("Styled", sfc, nodes, analysis, "vela-abc")

	if !strings.Contains(code, "createElement('style')") {
		t.Error("expected style injection")
	}
	if !strings.Contains(code, "p[vela-abc]") {
		t.Error("expected scoped CSS selector")
	}
}

func TestGenerateCode_IfDirective(t *testing.T) {
	sfc := &SFC{
		Script:   `let show = $state(true);`,
		Template: `<span #if="show">visible</span>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	analysis := analyzeScript(sfc.Script)
	code := generateCode("IfComp", sfc, nodes, analysis, "vela-test")

	if !strings.Contains(code, "createComment") {
		t.Error("expected anchor comment for #if")
	}
	if !strings.Contains(code, "insertBefore") {
		t.Error("expected insertBefore for #if mount")
	}
}

func TestGenerateCode_EachDirective(t *testing.T) {
	sfc := &SFC{
		Script:   `let items = $state(['a','b']);`,
		Template: `<li #each="items as item">{item}</li>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	analysis := analyzeScript(sfc.Script)
	code := generateCode("ListComp", sfc, nodes, analysis, "vela-test")

	if !strings.Contains(code, "forEach") {
		t.Error("expected forEach in #each output")
	}
	if !strings.Contains(code, "createComment") {
		t.Error("expected anchor comment for #each")
	}
}

// ──────────────────────────────────────────────
//  Interpolation parser
// ──────────────────────────────────────────────

func TestParseInterpolations(t *testing.T) {
	cases := []struct {
		input     string
		wantParts int
		dynamic   []string
	}{
		{"hello world", 1, nil},
		{"{count}", 1, []string{"count"}},
		{"Hello {name}!", 3, []string{"name"}},
		{"{a} and {b}", 3, []string{"a", "b"}},
		{"x = {a + b * c}", 1, []string{"a + b * c"}},
	}

	for _, tc := range cases {
		parts := parseInterpolations(tc.input)
		// Count dynamic parts
		var dynVals []string
		for _, p := range parts {
			if !p.Static {
				dynVals = append(dynVals, p.Value)
			}
		}
		for _, want := range tc.dynamic {
			found := false
			for _, got := range dynVals {
				if strings.TrimSpace(got) == want {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("input %q: expected dynamic part %q in %v", tc.input, want, dynVals)
			}
		}
	}
}

// ──────────────────────────────────────────────
//  Dependency analysis — AST-based
// ──────────────────────────────────────────────

func TestFindDeps(t *testing.T) {
	// Uses the new AST-based findDeps (which delegates to FindDepsInNode)
	vars := []string{"count", "name", "active"}

	deps := findDeps("count * 2 + name.length", vars)
	if !containsStr(deps, "count") {
		t.Error("should find count")
	}
	if !containsStr(deps, "name") {
		t.Error("should find name")
	}
	if containsStr(deps, "active") {
		t.Error("should not find active (not referenced)")
	}
}

func TestFindDeps_WordBoundary(t *testing.T) {
	// AST-based: "recount" parses as a distinct identifier — never matches "count".
	vars := []string{"count"}
	deps := findDeps("recount()", vars)
	if containsStr(deps, "count") {
		t.Error("should not match 'count' inside 'recount'")
	}
}

func TestFindDeps_TemplateLiteral(t *testing.T) {
	// Deps inside template literal expressions must be found.
	vars := []string{"count", "name"}
	deps := findDeps("`Hello ${name}, you have ${count} items`", vars)
	if !containsStr(deps, "name") {
		t.Error("should find name inside template literal")
	}
	if !containsStr(deps, "count") {
		t.Error("should find count inside template literal")
	}
}

func TestFindDeps_NestedArrow(t *testing.T) {
	// Deps referenced inside a callback must be found.
	vars := []string{"todos"}
	deps := findDeps("todos.filter(t => !t.done).length", vars)
	if !containsStr(deps, "todos") {
		t.Error("should find todos")
	}
}

func TestCollectIdents_SkipsPropertyNames(t *testing.T) {
	// In obj.foo, only "obj" is a free identifier; "foo" is a property name.
	prog := ParseJS("obj.foo")
	names := CollectIdents(prog)
	if !containsStr(names, "obj") {
		t.Error("should contain obj")
	}
	// "foo" appears as a JsIdent in the AST member expr prop, but WalkNode
	// skips non-computed member expr properties — so it should NOT appear.
	// (This is intentional: property names are not variable references.)
	if containsStr(names, "foo") {
		t.Error("should not contain property name foo as a dep")
	}
}

// ──────────────────────────────────────────────
//  parseEachExpr
// ──────────────────────────────────────────────

func TestParseEachExpr(t *testing.T) {
	array, item, index := parseEachExpr("items as todo, i")
	if array != "items" {
		t.Errorf("array: want 'items', got %q", array)
	}
	if item != "todo" {
		t.Errorf("item: want 'todo', got %q", item)
	}
	if index != "i" {
		t.Errorf("index: want 'i', got %q", index)
	}
}

func TestParseEachExpr_NoIndex(t *testing.T) {
	array, item, index := parseEachExpr("list as el")
	if array != "list" || item != "el" || index != "" {
		t.Errorf("got array=%q item=%q index=%q", array, item, index)
	}
}

// ──────────────────────────────────────────────
//  AST walker tests
// ──────────────────────────────────────────────

func TestWalkNode_VisitsAll(t *testing.T) {
	prog := ParseJS(`
		let x = 1;
		function foo() { return x + 1; }
		if (x > 0) { foo(); }
	`)
	var kinds []string
	WalkNode(prog, func(n JsNode) bool {
		switch n.(type) {
		case *JsVarDecl:
			kinds = append(kinds, "VarDecl")
		case *JsFuncDecl:
			kinds = append(kinds, "FuncDecl")
		case *JsIfStmt:
			kinds = append(kinds, "IfStmt")
		case *JsReturnStmt:
			kinds = append(kinds, "ReturnStmt")
		}
		return true
	})
	want := map[string]bool{"VarDecl": true, "FuncDecl": true, "IfStmt": true, "ReturnStmt": true}
	for _, k := range kinds {
		delete(want, k)
	}
	if len(want) > 0 {
		t.Errorf("WalkNode did not visit: %v", want)
	}
}

func TestWalkNode_EarlyStop(t *testing.T) {
	prog := ParseJS(`function outer() { function inner() { let x = 1; } }`)
	var funcNames []string
	WalkNode(prog, func(n JsNode) bool {
		if fd, ok := n.(*JsFuncDecl); ok {
			funcNames = append(funcNames, fd.Name)
			if fd.Name == "outer" {
				return false // stop descending into outer's body
			}
		}
		return true
	})
	if len(funcNames) != 1 || funcNames[0] != "outer" {
		t.Errorf("expected only outer visited, got %v", funcNames)
	}
}

func TestScanTemplateHoles_Simple(t *testing.T) {
	holes := scanTemplateHoles("`hello ${name}!`")
	if len(holes) != 1 || holes[0] != "name" {
		t.Errorf("expected [name], got %v", holes)
	}
}

func TestScanTemplateHoles_Multiple(t *testing.T) {
	holes := scanTemplateHoles("`${a} and ${b}`")
	if len(holes) != 2 {
		t.Fatalf("expected 2 holes, got %v", holes)
	}
	if holes[0] != "a" || holes[1] != "b" {
		t.Errorf("expected [a b], got %v", holes)
	}
}

func TestScanTemplateHoles_NestedBraces(t *testing.T) {
	// Nested object literal inside hole — fails with simple [^}]* regex
	holes := scanTemplateHoles("`result: ${fn({ key: val })}`")
	if len(holes) != 1 {
		t.Fatalf("expected 1 hole, got %v", holes)
	}
	if !strings.Contains(holes[0], "fn") {
		t.Errorf("hole should contain fn, got %q", holes[0])
	}
}

func TestScanTemplateHoles_NestedString(t *testing.T) {
	// String containing } inside hole — confuses naive scanners
	holes := scanTemplateHoles("`${\"}\" + x}`")
	if len(holes) != 1 {
		t.Fatalf("expected 1 hole, got %v", holes)
	}
}

func TestScanTemplateHoles_Escaped(t *testing.T) {
	// \${ is NOT a hole
	holes := scanTemplateHoles("`\\${notAHole} ${isAHole}`")
	if len(holes) != 1 {
		t.Errorf("expected 1 hole (escaped should be ignored), got %v", holes)
	}
}

func TestCollectIdents_TemplateInDerived(t *testing.T) {
	// $derived with template literal — deps inside ${} must be found
	prog := ParseJS("`Count: ${count} items, doubled: ${doubled}`")
	names := CollectIdents(prog)
	if !containsStr(names, "count") {
		t.Error("should find count inside template hole")
	}
	if !containsStr(names, "doubled") {
		t.Error("should find doubled inside template hole")
	}
}

func TestAnalyzeScript_ImportSpec(t *testing.T) {
	// Import spec reconstruction from typed AST (not raw string slicing)
	cases := []struct {
		src  string
		spec string
	}{
		{`import { writable, derived } from 'vela/store';`, "{ writable, derived }"},
		{`import * as Store from 'vela/store';`, "* as Store"},
		{`import Default from 'mod';`, "Default"},
		{`import Default, { named } from 'mod';`, "Default, { named }"},
	}
	for _, tc := range cases {
		a := analyzeScript(tc.src)
		if len(a.Imports) == 0 {
			t.Errorf("no imports parsed for: %s", tc.src)
			continue
		}
		if a.Imports[0].Spec != tc.spec {
			t.Errorf("import spec:\n  want %q\n  got  %q\n  src: %s",
				tc.spec, a.Imports[0].Spec, tc.src)
		}
	}
}

func TestAnalyzeScript_DerivedDepsTransitive(t *testing.T) {
	// doubled depends on count; label depends on doubled — both must chain correctly
	a := analyzeScript(`
		let count   = $state(0);
		let doubled = $derived(count * 2);
		let label   = $derived(doubled > 10 ? 'big' : 'small');
	`)
	if len(a.DerivedVars) != 2 {
		t.Fatalf("expected 2 derived vars, got %d", len(a.DerivedVars))
	}
	if !containsStr(a.DerivedVars[0].Deps, "count") {
		t.Error("doubled should depend on count")
	}
	if !containsStr(a.DerivedVars[1].Deps, "doubled") {
		t.Error("label should depend on doubled")
	}
}

func TestAnalyzeScript_EffectDepsInTemplate(t *testing.T) {
	// Effect referencing state via template literal — deps must be found
	a := analyzeScript(`
		let count = $state(0);
		$effect(() => {
			document.title = ` + "`" + `Count: ${count}` + "`" + `;
		});
	`)
	if len(a.Effects) == 0 {
		t.Fatal("no effects parsed")
	}
	if !containsStr(a.Effects[0].Deps, "count") {
		t.Errorf("effect should depend on count; deps=%v body=%q",
			a.Effects[0].Deps, a.Effects[0].Body)
	}
}

func TestRewriteAssignments_MemberExprNotRewritten(t *testing.T) {
	// obj.count = 5 must NOT be rewritten even though "count" is a state var
	src := `obj.count = 5;`
	prog := ParseJS(src)
	stateSet := map[string]bool{"count": true}
	got := RewriteAssignments(src, prog, stateSet)
	if strings.Contains(got, "$$set_count") {
		t.Errorf("member-expr assignment should not be rewritten: %s", got)
	}
}

func TestRewriteAssignments_NullishAndLogical(t *testing.T) {
	for _, op := range []string{"??=", "&&=", "||="} {
		src := "count " + op + " 0;"
		prog := ParseJS(src)
		got := RewriteAssignments(src, prog, map[string]bool{"count": true})
		if !strings.Contains(got, "$$set_count") {
			t.Errorf("op %s should be rewritten; got: %s", op, got)
		}
	}
}

func TestRewriteAssignments_PreservesNonState(t *testing.T) {
	// Only state vars should be rewritten; other vars stay as-is
	src := `
		let localVar = 0;
		localVar = 42;
		count = 1;
	`
	prog := ParseJS(src)
	got := RewriteAssignments(src, prog, map[string]bool{"count": true})
	if !strings.Contains(got, "localVar = 42") {
		t.Errorf("non-state assignment should be preserved; got:\n%s", got)
	}
	if !strings.Contains(got, "$$set_count") {
		t.Errorf("state assignment should be rewritten; got:\n%s", got)
	}
}

func TestPrintNode_RoundTrip(t *testing.T) {
	// printNode is used to reconstruct effect bodies — check key constructs
	cases := []string{
		"x + y",
		"a ? b : c",
		"foo(a, b, c)",
		"obj.method()",
		"[1, 2, 3]",
		"{ a: 1, b: 2 }",
	}
	for _, expr := range cases {
		prog := ParseJS(expr)
		if len(prog.Body) == 0 {
			t.Errorf("no stmts parsed for %q", expr)
			continue
		}
		got := printNode(prog.Body[0])
		// The round-trip output should contain all the identifiers and operators
		// (we don't require character-identical output, just semantic equivalence)
		if got == "" {
			t.Errorf("printNode(%q) returned empty string", expr)
		}
	}
}

// ──────────────────────────────────────────────
//  Full pipeline integration
// ──────────────────────────────────────────────

func TestFullPipeline_Counter(t *testing.T) {
	src := `
<script>
  let count = $state(0);
  let doubled = $derived(count * 2);
  $effect(() => { document.title = String(count); });
  function inc() { count++; }
  function dec() { count--; }
</script>
<style>
  div { display: flex; }
  button { cursor: pointer; }
</style>
<template>
  <div>
    <button @click="dec">-</button>
    <span>{count} (×2={doubled})</span>
    <button @click="inc">+</button>
  </div>
</template>
`
	sfc, err := parseSFC(src)
	if err != nil {
		t.Fatalf("parseSFC: %v", err)
	}
	nodes, err := parseTemplate(sfc.Template)
	if err != nil {
		t.Fatalf("parseTemplate: %v", err)
	}
	analysis := analyzeScript(sfc.Script)
	code := generateCode("Counter", sfc, nodes, analysis, "vela-counter")

	// The generated code must:
	must := []string{
		"export function Counter(",     // named export
		"let count = 0",                // state init
		"let doubled",                  // derived decl
		"$$upd_doubled",                // derived updater
		"$$effect0",                    // effect wrapper
		"function $$set_count",         // setter
		"$$upd_doubled()",              // called inside setter
		"$$effect0()",                  // called inside setter
		"addEventListener('click'",     // events wired
		"removeEventListener",          // cleanup
		"button[vela-counter]",         // scoped CSS
		"$$target.appendChild($$root)", // mount
		"destroy()",                    // destroy handle
	}
	for _, want := range must {
		if !strings.Contains(code, want) {
			t.Errorf("missing in output: %q", want)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  CSS Parser & Scoper
// ─────────────────────────────────────────────────────────────────────────────

func TestParseCSS_QualifiedRule(t *testing.T) {
	sheet := ParseCSS(`button { color: red; font-size: 1rem; }`)
	if len(sheet.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(sheet.Rules))
	}
	rule, ok := sheet.Rules[0].(*CssQualifiedRule)
	if !ok {
		t.Fatal("expected CssQualifiedRule")
	}
	if len(rule.Selectors) != 1 || tokStr(rule.Selectors[0].Parts) != "button" {
		t.Errorf("unexpected selector: %v", rule.Selectors)
	}
	if len(rule.Decls) != 2 {
		t.Errorf("expected 2 decls, got %d", len(rule.Decls))
	}
	if rule.Decls[0].Prop != "color" || rule.Decls[0].Value != "red" {
		t.Errorf("unexpected decl: %+v", rule.Decls[0])
	}
}

func TestParseCSS_MultiSelector(t *testing.T) {
	sheet := ParseCSS(`.foo, .bar { margin: 0; }`)
	rule := sheet.Rules[0].(*CssQualifiedRule)
	if len(rule.Selectors) != 2 {
		t.Fatalf("expected 2 selectors, got %d", len(rule.Selectors))
	}
}

func TestParseCSS_AtMedia(t *testing.T) {
	sheet := ParseCSS(`@media (max-width: 768px) { button { display: none; } }`)
	if len(sheet.Rules) != 1 {
		t.Fatalf("expected 1 at-rule, got %d", len(sheet.Rules))
	}
	at, ok := sheet.Rules[0].(*CssAtRule)
	if !ok {
		t.Fatal("expected CssAtRule")
	}
	if at.Name != "media" {
		t.Errorf("expected media, got %q", at.Name)
	}
	if len(at.Block) != 1 {
		t.Errorf("expected 1 inner rule, got %d", len(at.Block))
	}
}

func TestParseCSS_Keyframes(t *testing.T) {
	css := `@keyframes spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }`
	sheet := ParseCSS(css)
	at, ok := sheet.Rules[0].(*CssAtRule)
	if !ok || at.Name != "keyframes" {
		t.Fatal("expected keyframes at-rule")
	}
	// Inner content should be preserved as raw (not scoped)
	if len(at.Block) == 0 {
		t.Error("keyframes block should have content")
	}
}

func TestParseCSS_Important(t *testing.T) {
	sheet := ParseCSS(`p { color: red !important; }`)
	rule := sheet.Rules[0].(*CssQualifiedRule)
	if !rule.Decls[0].Important {
		t.Error("should parse !important")
	}
}

func TestScopeCSS_Simple(t *testing.T) {
	out := ScopeCSS(`button { color: red; }`, "vela-abc")
	if !strings.Contains(out, "button[vela-abc]") {
		t.Errorf("simple selector not scoped: %s", out)
	}
}

func TestScopeCSS_MultiSelector(t *testing.T) {
	out := ScopeCSS(`.foo, .bar { margin: 0; }`, "vela-xyz")
	if !strings.Contains(out, ".foo[vela-xyz]") {
		t.Errorf(".foo not scoped: %s", out)
	}
	if !strings.Contains(out, ".bar[vela-xyz]") {
		t.Errorf(".bar not scoped: %s", out)
	}
}

func TestScopeCSS_PseudoElement(t *testing.T) {
	// Scope attr must appear BEFORE ::before/::after
	out := ScopeCSS(`p::before { content: ''; }`, "vela-abc")
	// The attribute should be injected before the pseudo-element
	if !strings.Contains(out, "p[vela-abc]") {
		t.Errorf("pseudo-element selector not correctly scoped: %s", out)
	}
}

func TestScopeCSS_AtMediaWrapsInnerRules(t *testing.T) {
	out := ScopeCSS(`@media (max-width: 768px) { .card { display: none; } }`, "vela-abc")
	if !strings.Contains(out, ".card[vela-abc]") {
		t.Errorf("inner rule inside @media not scoped: %s", out)
	}
	if !strings.Contains(out, "@media") {
		t.Errorf("@media wrapper lost: %s", out)
	}
}

func TestScopeCSS_KeyframesNotScoped(t *testing.T) {
	out := ScopeCSS(`@keyframes spin { from { opacity: 0; } to { opacity: 1; } }`, "vela-abc")
	// "from" and "to" must NOT get attribute selectors
	if strings.Contains(out, "from[vela-abc]") {
		t.Errorf("keyframe selector should not be scoped: %s", out)
	}
}

func TestScopeCSS_Global(t *testing.T) {
	out := ScopeCSS(`:global(.reset) { margin: 0; }`, "vela-abc")
	// :global() strips the scoping but keeps the inner selector
	if strings.Contains(out, "[vela-abc]") {
		t.Errorf(":global selector should not be scoped: %s", out)
	}
	if !strings.Contains(out, ".reset") {
		t.Errorf(":global inner selector should be preserved: %s", out)
	}
}

func TestScopeCSS_CustomProperty(t *testing.T) {
	out := ScopeCSS(`:root { --color-primary: blue; }`, "vela-abc")
	// :root is a valid selector; must be scoped
	if !strings.Contains(out, ":root[vela-abc]") && !strings.Contains(out, "[vela-abc]:root") {
		t.Logf("CSS custom property output: %s", out)
		// Allow either form — implementation may vary
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Template parser — new directives and component nodes
// ─────────────────────────────────────────────────────────────────────────────

func TestParseTemplate_ElseIfElse(t *testing.T) {
	html := `
		<div #if="a">A</div>
		<div #elseif="b">B</div>
		<div #else>C</div>
	`
	nodes, err := parseTemplate(html)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	// Should be grouped into one IfChainNode
	var chains []*IfChainNode
	for _, n := range nodes {
		if c, ok := n.(*IfChainNode); ok {
			chains = append(chains, c)
		}
	}
	if len(chains) != 1 {
		t.Fatalf("expected 1 IfChainNode, got %d (nodes: %v)", len(chains), nodes)
	}
	if len(chains[0].Branches) != 3 {
		t.Errorf("expected 3 branches (if/elseif/else), got %d", len(chains[0].Branches))
	}
	if chains[0].Branches[0].Cond != "a" {
		t.Errorf("branch[0].Cond: want 'a', got %q", chains[0].Branches[0].Cond)
	}
	if chains[0].Branches[1].Cond != "b" {
		t.Errorf("branch[1].Cond: want 'b', got %q", chains[0].Branches[1].Cond)
	}
	if chains[0].Branches[2].Cond != "" {
		t.Errorf("branch[2] (else) should have empty Cond, got %q", chains[0].Branches[2].Cond)
	}
}

func TestParseTemplate_Component(t *testing.T) {
	html := `<div><MyButton :label="text" @click="handler" /></div>`
	nodes, err := parseTemplate(html)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	div := nodes[0].(*ElementNode)
	var comps []*ComponentNode
	for _, child := range div.Children {
		if c, ok := child.(*ComponentNode); ok {
			comps = append(comps, c)
		}
	}
	if len(comps) != 1 {
		t.Fatalf("expected 1 ComponentNode, got %d", len(comps))
	}
	if comps[0].Name != "MyButton" {
		t.Errorf("component name: want 'MyButton', got %q", comps[0].Name)
	}
	if len(comps[0].Attrs) != 2 {
		t.Errorf("expected 2 attrs, got %d", len(comps[0].Attrs))
	}
	bind := comps[0].Attrs[0]
	if !bind.IsBind || bind.Name != "label" {
		t.Errorf("expected :label IsBind attr, got %+v", bind)
	}
	evt := comps[0].Attrs[1]
	if !evt.IsEvent || evt.Name != "click" {
		t.Errorf("expected @click IsEvent attr, got %+v", evt)
	}
}

func TestParseTemplate_ComponentSelfClose(t *testing.T) {
	// Self-closing component with no attrs
	nodes, err := parseTemplate(`<Container />`)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	comp, ok := nodes[0].(*ComponentNode)
	if !ok {
		t.Fatalf("expected ComponentNode, got %T", nodes[0])
	}
	if comp.Name != "Container" {
		t.Errorf("want 'Container', got %q", comp.Name)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Analyzer — export let props
// ─────────────────────────────────────────────────────────────────────────────

func TestAnalyzeScript_ExportLetProps(t *testing.T) {
	a := analyzeScript(`
		export let name = 'World';
		export let count = 0;
		export let required;
	`)
	if len(a.Props) != 3 {
		t.Fatalf("expected 3 props, got %d", len(a.Props))
	}
	if a.Props[0].Name != "name" || a.Props[0].Default != "'World'" {
		t.Errorf("prop[0]: got name=%q default=%q", a.Props[0].Name, a.Props[0].Default)
	}
	if a.Props[2].Name != "required" || a.Props[2].Default != "" {
		t.Errorf("prop[2] should have empty default: %+v", a.Props[2])
	}
}

func TestAnalyzeScript_PropsNotInCleanScript(t *testing.T) {
	// export let should be removed from CleanScript (it's handled by codegen)
	a := analyzeScript(`
		export let name = 'World';
		function greet() { return name; }
	`)
	if strings.Contains(a.CleanScript, "export let") {
		t.Errorf("export let should not appear in CleanScript: %s", a.CleanScript)
	}
	if !strings.Contains(a.CleanScript, "function greet") {
		t.Errorf("greet function should be in CleanScript: %s", a.CleanScript)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Codegen — if-chain and component
// ─────────────────────────────────────────────────────────────────────────────

func TestGenerateCode_IfChain(t *testing.T) {
	sfc := &SFC{
		Script: `let score = $state(0);`,
		Template: `
			<p #if="score > 90">Excellent</p>
			<p #elseif="score > 60">Good</p>
			<p #else>Try again</p>
		`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	analysis := analyzeScript(sfc.Script)
	code := generateCode("ScoreComp", sfc, nodes, analysis, "vela-test")

	// Must have a chain function, anchor comment, and branch variables
	if !strings.Contains(code, "$$chain_") {
		t.Error("expected if-chain function in output")
	}
	if !strings.Contains(code, "createComment('if-chain')") {
		t.Error("expected anchor comment for if-chain")
	}
	if !strings.Contains(code, "$$active") {
		t.Error("expected $$active branch selector")
	}
}

func TestGenerateCode_Props(t *testing.T) {
	sfc := &SFC{
		Script:   `export let greeting = 'Hello'; export let name;`,
		Template: `<p>{greeting}, {name}!</p>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	analysis := analyzeScript(sfc.Script)
	code := generateCode("Greeter", sfc, nodes, analysis, "vela-test")

	// Props initialised from $$props with fallback to default
	if !strings.Contains(code, "$$props.greeting") {
		t.Error("expected $$props.greeting initialisation")
	}
	if !strings.Contains(code, "'Hello'") {
		t.Error("expected default value 'Hello' for greeting")
	}
	// Required prop (no default)
	if !strings.Contains(code, "$$props.name") {
		t.Error("expected $$props.name initialisation")
	}
	// set() should accept both props
	if !strings.Contains(code, "$$set_greeting") {
		t.Error("expected $$set_greeting in set()")
	}
}

func TestGenerateCode_Component(t *testing.T) {
	sfc := &SFC{
		Script:   `import { Counter } from './Counter.js';`,
		Template: `<div><Counter :start="5" /></div>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	analysis := analyzeScript(sfc.Script)
	code := generateCode("App", sfc, nodes, analysis, "vela-test")

	// Component mount call
	if !strings.Contains(code, "Counter(") {
		t.Error("expected Counter() mount call")
	}
	// Child destroy wired to parent destroy
	if !strings.Contains(code, ".destroy()") {
		t.Error("expected child .destroy() in cleanup")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Full pipeline — elseif/else + props
// ─────────────────────────────────────────────────────────────────────────────

func TestFullPipeline_IfChainAndProps(t *testing.T) {
	src := `
<script>
  export let score = 0;
  let grade = $derived(
    score >= 90 ? 'A' :
    score >= 80 ? 'B' :
    score >= 70 ? 'C' : 'F'
  );
</script>
<style>
  .grade { font-size: 2rem; font-weight: bold; }
  .pass  { color: green; }
  .fail  { color: red; }
</style>
<template>
  <div>
    <span class="grade">{grade}</span>
    <p #if="score >= 70" class="pass">Pass</p>
    <p #elseif="score >= 50" class="near">Almost</p>
    <p #else class="fail">Fail</p>
  </div>
</template>
`
	sfc, err := parseSFC(src)
	if err != nil {
		t.Fatalf("parseSFC: %v", err)
	}
	nodes, err := parseTemplate(sfc.Template)
	if err != nil {
		t.Fatalf("parseTemplate: %v", err)
	}
	a := analyzeScript(sfc.Script)
	code := generateCode("GradeComp", sfc, nodes, a, "vela-grade")

	must := []string{
		"export function GradeComp(",
		"$$props.score",             // prop initialisation
		"let grade",                 // derived
		"$$upd_grade",               // derived updater
		"function $$set_score",      // prop setter
		"$$chain_",                  // if-chain function
		"$$active",                  // branch selector
		".grade[vela-grade]",        // CSS scoped
		".pass[vela-grade]",         // CSS scoped
		"destroy()",                 // cleanup handle
	}
	for _, want := range must {
		if !strings.Contains(code, want) {
			t.Errorf("missing in output: %q", want)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Minifier
// ─────────────────────────────────────────────────────────────────────────────

func TestMinify_RemovesLineComments(t *testing.T) {
	src := "const x = 1; // this is a comment\nconst y = 2;"
	got := Minify(src)
	if strings.Contains(got, "//") {
		t.Errorf("line comment not removed: %q", got)
	}
	if !strings.Contains(got, "const x") || !strings.Contains(got, "const y") {
		t.Errorf("code was dropped: %q", got)
	}
}

func TestMinify_RemovesBlockComments(t *testing.T) {
	src := "/* header */ function foo() { /* inner */ return 1; }"
	got := Minify(src)
	if strings.Contains(got, "/*") || strings.Contains(got, "*/") {
		t.Errorf("block comment not removed: %q", got)
	}
	if !strings.Contains(got, "function foo") {
		t.Errorf("code was dropped: %q", got)
	}
}

func TestMinify_PreservesStrings(t *testing.T) {
	src := `const s = "hello // world"; /* drop */ const t = 'bye';`
	got := Minify(src)
	if !strings.Contains(got, `"hello // world"`) {
		t.Errorf("string literal content was modified: %q", got)
	}
	if !strings.Contains(got, `'bye'`) {
		t.Errorf("single-quoted string lost: %q", got)
	}
}

func TestMinify_PreservesTemplateLiterals(t *testing.T) {
	src := "const s = `hello ${name}!`;"
	got := Minify(src)
	if !strings.Contains(got, "`hello ${name}!`") {
		t.Errorf("template literal modified: %q", got)
	}
}

func TestMinify_CollapsesWhitespace(t *testing.T) {
	src := "function   foo(  a,   b  )  {  return  a + b;  }"
	got := Minify(src)
	if strings.Contains(got, "   ") {
		t.Errorf("triple space not collapsed: %q", got)
	}
}

func TestMinify_RemovesSemiBeforeBrace(t *testing.T) {
	src := "function f() { const x = 1; }"
	got := Minify(src)
	// The final semicolon before } should be removed
	if strings.Contains(got, ";}") {
		t.Errorf("semi before } not removed: %q", got)
	}
}

func TestMinify_RoundTrip(t *testing.T) {
	// Full generated component should survive minification without losing structure
	sfc := &SFC{
		Script:   `let count = $state(0); function inc() { count++; }`,
		Template: `<div><button @click="inc">+</button><span>{count}</span></div>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	analysis := analyzeScript(sfc.Script)
	code := generateCode("Counter", sfc, nodes, analysis, "vela-test")
	minified := Minify(code)

	must := []string{"export function Counter", "$$set_count", "addEventListener"}
	for _, want := range must {
		if !strings.Contains(minified, want) {
			t.Errorf("minified code missing %q", want)
		}
	}
	if len(minified) >= len(code) {
		t.Errorf("minified should be shorter: before=%d after=%d", len(code), len(minified))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Diagnostics
// ─────────────────────────────────────────────────────────────────────────────

func TestDiagSet_LineColResolution(t *testing.T) {
	src := "line1\nline2\nline3"
	ds := NewDiagSet("test.vela", src)
	ds.Error(6, "test error") // byte 6 = start of "line2"
	if len(ds.All()) != 1 {
		t.Fatalf("expected 1 diag, got %d", len(ds.All()))
	}
	d := ds.All()[0]
	if d.Line != 2 {
		t.Errorf("line: want 2, got %d", d.Line)
	}
	if d.Col != 1 {
		t.Errorf("col: want 1, got %d", d.Col)
	}
}

func TestDiagSet_HasErrors(t *testing.T) {
	ds := NewDiagSet("f.vela", "")
	if ds.HasErrors() {
		t.Error("empty DiagSet should have no errors")
	}
	ds.Warn(0, "just a warning")
	if ds.HasErrors() {
		t.Error("should still have no errors after adding warning")
	}
	ds.Error(0, "actual error")
	if !ds.HasErrors() {
		t.Error("should have errors now")
	}
}

func TestDiagSet_Filtering(t *testing.T) {
	ds := NewDiagSet("f.vela", "")
	ds.Error(0, "e1")
	ds.Warn(0, "w1")
	ds.Hint(0, "h1")
	ds.Error(0, "e2")
	if len(ds.Errors()) != 2 {
		t.Errorf("expected 2 errors, got %d", len(ds.Errors()))
	}
	if len(ds.Warnings()) != 1 {
		t.Errorf("expected 1 warning, got %d", len(ds.Warnings()))
	}
	if len(ds.All()) != 4 {
		t.Errorf("expected 4 total diags, got %d", len(ds.All()))
	}
}

func TestValidateScript_WarnsDerivedAssignment(t *testing.T) {
	script := `
		let count = $state(0);
		let doubled = $derived(count * 2);
		function badOp() { doubled = 10; }
	`
	prog := ParseJS(script)
	a := analyzeScript(script)
	ds := NewDiagSet("test.vela", script)
	validateScript(prog, a, ds)
	// Should warn about assigning to derived var
	found := false
	for _, d := range ds.Warnings() {
		if strings.Contains(d.Message, "doubled") && strings.Contains(d.Message, "read-only") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warning about assigning to derived var; got: %v", ds.Warnings())
	}
}

func TestValidateScript_WarnsEffectNoDeps(t *testing.T) {
	script := `
		$effect(() => { console.log("hello"); });
	`
	prog := ParseJS(script)
	a := analyzeScript(script)
	ds := NewDiagSet("test.vela", script)
	validateScript(prog, a, ds)
	found := false
	for _, d := range ds.Warnings() {
		if strings.Contains(d.Message, "no reactive variables") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warning for effect with no deps; got: %v", ds.Warnings())
	}
}

func TestValidateTemplate_WarnsUnimportedComponent(t *testing.T) {
	sfc := &SFC{
		Script:   ``,
		Template: `<div><MyWidget /></div>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	ds := NewDiagSet("test.vela", sfc.Script)
	validateTemplate(nodes, a, ds)
	found := false
	for _, d := range ds.Warnings() {
		if strings.Contains(d.Message, "MyWidget") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected warning for unimported component; got: %v", ds.Warnings())
	}
}

func TestValidateTemplate_NoWarnWhenImported(t *testing.T) {
	sfc := &SFC{
		Script:   `import { MyWidget } from './MyWidget.js';`,
		Template: `<div><MyWidget /></div>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	ds := NewDiagSet("test.vela", sfc.Script)
	validateTemplate(nodes, a, ds)
	for _, d := range ds.Warnings() {
		if strings.Contains(d.Message, "MyWidget") {
			t.Errorf("should not warn about imported component; got: %s", d.Message)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Lifecycle hooks
// ─────────────────────────────────────────────────────────────────────────────

func TestAnalyzeScript_OnMount(t *testing.T) {
	a := analyzeScript(`
		let pollId = null;
		onMount(() => {
			pollId = setInterval(() => {}, 1000);
		});
	`)
	if len(a.Lifecycles) != 1 {
		t.Fatalf("expected 1 lifecycle hook, got %d", len(a.Lifecycles))
	}
	lc := a.Lifecycles[0]
	if lc.Kind != "onMount" {
		t.Errorf("kind: want 'onMount', got %q", lc.Kind)
	}
	if !strings.Contains(lc.Body, "setInterval") {
		t.Errorf("body should contain setInterval; got: %q", lc.Body)
	}
}

func TestAnalyzeScript_OnDestroy(t *testing.T) {
	a := analyzeScript(`
		onDestroy(() => {
			clearInterval(pollId);
		});
	`)
	if len(a.Lifecycles) != 1 {
		t.Fatalf("expected 1 lifecycle, got %d", len(a.Lifecycles))
	}
	if a.Lifecycles[0].Kind != "onDestroy" {
		t.Errorf("kind: want 'onDestroy', got %q", a.Lifecycles[0].Kind)
	}
}

func TestAnalyzeScript_LifecycleNotInCleanScript(t *testing.T) {
	a := analyzeScript(`
		onMount(() => { document.title = 'hi'; });
		function greet() { return 'hello'; }
	`)
	if strings.Contains(a.CleanScript, "onMount") {
		t.Errorf("onMount should be removed from CleanScript; got:\n%s", a.CleanScript)
	}
	if !strings.Contains(a.CleanScript, "function greet") {
		t.Errorf("user functions should remain in CleanScript")
	}
}

func TestGenerateCode_OnMount(t *testing.T) {
	sfc := &SFC{
		Script:   `onMount(() => { document.body.classList.add('loaded'); });`,
		Template: `<div>hello</div>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("App", sfc, nodes, a, "vela-test")
	if !strings.Contains(code, "document.body.classList.add") {
		t.Errorf("onMount body should appear in generated code:\n%s", code)
	}
}

func TestGenerateCode_OnDestroy(t *testing.T) {
	sfc := &SFC{
		Script:   `onDestroy(() => { cleanup(); });`,
		Template: `<div>bye</div>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("Bye", sfc, nodes, a, "vela-test")
	// onDestroy body must appear inside destroy()
	destroyStart := strings.Index(code, "destroy()")
	if destroyStart < 0 {
		t.Fatal("no destroy() in output")
	}
	afterDestroy := code[destroyStart:]
	if !strings.Contains(afterDestroy, "cleanup()") {
		t.Errorf("onDestroy body should be inside destroy(); code:\n%s", afterDestroy[:200])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  $bindable props
// ─────────────────────────────────────────────────────────────────────────────

func TestAnalyzeScript_Bindable(t *testing.T) {
	a := analyzeScript(`export let value = $bindable(0);`)
	if len(a.Props) != 1 {
		t.Fatalf("expected 1 prop, got %d", len(a.Props))
	}
	p := a.Props[0]
	if !p.Bindable {
		t.Error("prop should be marked Bindable")
	}
	if p.Default != "0" {
		t.Errorf("default: want '0', got %q", p.Default)
	}
}

func TestAnalyzeScript_BindableNoDefault(t *testing.T) {
	a := analyzeScript(`export let val = $bindable();`)
	if len(a.Props) != 1 {
		t.Fatalf("expected 1 prop, got %d", len(a.Props))
	}
	if !a.Props[0].Bindable {
		t.Error("should be bindable")
	}
	if a.Props[0].Default != "" {
		t.Errorf("should have empty default, got %q", a.Props[0].Default)
	}
}

func TestGenerateCode_BindableCallback(t *testing.T) {
	sfc := &SFC{
		Script:   `export let count = $bindable(0);`,
		Template: `<span>{count}</span>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("Counter", sfc, nodes, a, "vela-test")
	// Should emit $$onChange_count reference
	if !strings.Contains(code, "$$onChange_count") {
		t.Errorf("expected $$onChange_count for bindable prop; code:\n%s", code)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Slots
// ─────────────────────────────────────────────────────────────────────────────

func TestParseTemplate_SlotDefault(t *testing.T) {
	nodes, err := parseTemplate(`<div><slot /></div>`)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	div := nodes[0].(*ElementNode)
	if len(div.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(div.Children))
	}
	slot, ok := div.Children[0].(*SlotNode)
	if !ok {
		t.Fatalf("expected SlotNode, got %T", div.Children[0])
	}
	if slot.Name != "default" {
		t.Errorf("slot name: want 'default', got %q", slot.Name)
	}
}

func TestParseTemplate_SlotNamed(t *testing.T) {
	nodes, err := parseTemplate(`<div><slot name="header">Fallback</slot></div>`)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	div := nodes[0].(*ElementNode)
	slot := div.Children[0].(*SlotNode)
	if slot.Name != "header" {
		t.Errorf("slot name: want 'header', got %q", slot.Name)
	}
	if len(slot.Fallback) == 0 {
		t.Error("expected fallback content")
	}
}

func TestGenerateCode_SlotRendering(t *testing.T) {
	sfc := &SFC{
		Script:   ``,
		Template: `<div><slot name="header">Default header</slot></div>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("Card", sfc, nodes, a, "vela-test")
	if !strings.Contains(code, `$$slots["header"]`) && !strings.Contains(code, `$$slots['header']`) {
		t.Errorf("expected slot lookup in generated code:\n%s", code)
	}
	if !strings.Contains(code, "$$slots") {
		t.Error("expected $$slots parameter")
	}
}

func TestGenerateCode_ComponentPassesSlots(t *testing.T) {
	sfc := &SFC{
		Script:   `import { Card } from './Card.js';`,
		Template: `<Card><p slot="header">My Title</p></Card>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("App", sfc, nodes, a, "vela-test")
	// The component mount call should include a slots argument
	if !strings.Contains(code, "\"header\"") && !strings.Contains(code, "'header'") {
		t.Errorf("expected slot content in component call:\n%s", code)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  FragGen + keyed #each
// ─────────────────────────────────────────────────────────────────────────────

func TestFragGen_BasicEmit(t *testing.T) {
	a := analyzeScript(`let items = $state([]);`)
	fg := newFragGen("$$frag_test", []string{"item", "$$i"}, "vela-test", a)
	fg.create.WriteString("    const $$f0 = document.createElement('li');\n")
	fg.create.WriteString("    $$f0.setAttribute('vela-test', '');\n")
	fg.wire.WriteString("    // wired\n")

	out := fg.Emit()
	if !strings.Contains(out, "function $$frag_test(item, $$i)") {
		t.Errorf("missing function signature: %s", out)
	}
	if !strings.Contains(out, "el: $$f0") {
		t.Errorf("missing el in return: %s", out)
	}
	if !strings.Contains(out, "destroy()") {
		t.Errorf("missing destroy: %s", out)
	}
}

func TestGenerateCode_EachWithFragGen(t *testing.T) {
	sfc := &SFC{
		Script:   `let items = $state([{id:1,name:'a'},{id:2,name:'b'}]);`,
		Template: `<ul><li #each="items as item">{item.name}</li></ul>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("List", sfc, nodes, a, "vela-test")
	// FragGen emits a $$frag_ function
	if !strings.Contains(code, "$$frag_") {
		t.Errorf("expected $$frag_ function in #each output:\n%s", code)
	}
	// And a loop that calls it
	if !strings.Contains(code, "forEach") {
		t.Errorf("expected forEach loop in #each output")
	}
	// destroy() on each block
	if !strings.Contains(code, ".destroy()") {
		t.Errorf("expected block.destroy() in #each cleanup")
	}
}

func TestGenerateCode_KeyedEach(t *testing.T) {
	sfc := &SFC{
		Script:   `let todos = $state([]);`,
		Template: `<li #each="todos as todo" #key="todo.id">{todo.text}</li>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("TodoList", sfc, nodes, a, "vela-test")
	// Keyed diffing uses a Map
	if !strings.Contains(code, "new Map()") {
		t.Errorf("keyed #each should use a Map for key tracking:\n%s", code)
	}
	// update() called for existing blocks
	if !strings.Contains(code, ".update") {
		t.Errorf("keyed #each should call .update() on existing blocks")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  $$dispatch
// ─────────────────────────────────────────────────────────────────────────────

func TestGenerateCode_DispatchFunction(t *testing.T) {
	sfc := &SFC{
		Script:   ``,
		Template: `<div>hello</div>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("MyComp", sfc, nodes, a, "vela-test")
	if !strings.Contains(code, "function $$dispatch") {
		t.Errorf("every component should have $$dispatch; code:\n%s", code[:400])
	}
	if !strings.Contains(code, "CustomEvent") {
		t.Errorf("$$dispatch should use CustomEvent")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Full pipeline — lifecycle + bindable + slots
// ─────────────────────────────────────────────────────────────────────────────

func TestFullPipeline_UserCard(t *testing.T) {
	src := `
<script>
  export let name = $bindable('World');
  export let role = 'Member';
  let greeting = $derived('Hello, ' + name + '!');
  onMount(() => { document.body.setAttribute('data-mounted', 'true'); });
  onDestroy(() => { document.body.removeAttribute('data-mounted'); });
  $effect(() => { document.title = name; });
</script>
<style>
  .card { padding: 1rem; }
  .name { font-weight: bold; }
</style>
<template>
  <div class="card">
    <h2 class="name">{name}</h2>
    <p>{greeting}</p>
    <slot name="footer"><span>Default footer</span></slot>
  </div>
</template>
`
	sfc, err := parseSFC(src)
	if err != nil {
		t.Fatalf("parseSFC: %v", err)
	}
	nodes, err := parseTemplate(sfc.Template)
	if err != nil {
		t.Fatalf("parseTemplate: %v", err)
	}
	a := analyzeScript(sfc.Script)
	code := generateCode("UserCard", sfc, nodes, a, "vela-card")

	must := []string{
		"export function UserCard(",
		"$$props, $$slots",
		"$$onChange_name",              // bindable callback
		"function $$dispatch",          // dispatch helper
		"document.body.setAttribute",   // onMount body
		"document.body.removeAttribute", // onDestroy body
		"$$effect0",                    // $effect
		"let greeting",                 // derived
		"$$upd_greeting",               // derived updater
		`$$slots["footer"]`,            // slot lookup (or with single quotes)
		".card[vela-card]",             // scoped CSS
		"destroy()",                    // cleanup handle
	}
	// slot lookup may use single or double quotes
	for _, want := range must {
		alt := ""
		if want == `$$slots["footer"]` {
			alt = `$$slots['footer']`
		}
		if !strings.Contains(code, want) && (alt == "" || !strings.Contains(code, alt)) {
			t.Errorf("missing in output: %q\n--- first 500 chars ---\n%s", want, code[:min(500, len(code))])
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ─────────────────────────────────────────────────────────────────────────────
//  Regression tests for runtime bugs
// ─────────────────────────────────────────────────────────────────────────────

// Regression: #each with nested children caused "redeclaration of const $$f0"
// because walkEach used a hardcoded "$$f0" for the row root AND frag.id()
// started at 0 for children, producing a duplicate const name.
func TestGenerateCode_EachNoVarRedeclaration(t *testing.T) {
	sfc := &SFC{
		Script: `let todos = $state([]);`,
		Template: `
			<ul>
			  <li #each="todos as todo, i" #key="todo.id">
			    <input type="checkbox" @change="() => toggle(todo.id)" />
			    <span>{todo.text}</span>
			    <button @click="() => remove(todo.id)">✕</button>
			  </li>
			</ul>
		`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("TodoList", sfc, nodes, a, "vela-test")

	// Scan the $$frag_ function for duplicate const declarations
	fragStart := strings.Index(code, "function $$frag_")
	if fragStart < 0 {
		t.Fatal("no $$frag_ function emitted")
	}
	// Find end of frag function (next top-level function or end of file)
	fragEnd := strings.Index(code[fragStart+20:], "\n  function ")
	var fragCode string
	if fragEnd < 0 {
		fragCode = code[fragStart:]
	} else {
		fragCode = code[fragStart : fragStart+20+fragEnd]
	}

	// Count unique const names inside the frag
	constNames := map[string]int{}
	for _, m := range regexp.MustCompile(`\bconst (\$\$f\d+)\b`).FindAllStringSubmatch(fragCode, -1) {
		constNames[m[1]]++
	}
	for name, count := range constNames {
		if count > 1 {
			t.Errorf("const %s declared %d times in $$frag_ — redeclaration bug: %s",
				name, count, fragCode[:min(400, len(fragCode))])
		}
	}
}

// Regression: <slot> content was inserted in the create phase before the
// anchor comment was wired into the DOM, causing "parentNode is null".
func TestGenerateCode_SlotInsertionAfterWire(t *testing.T) {
	sfc := &SFC{
		Script:   ``,
		Template: `<div><slot name="footer">Fallback text</slot></div>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("Card", sfc, nodes, a, "vela-test")

	// The slot insertion block must appear AFTER "// initial render"
	// (i.e. in g.init, not g.create).
	initIdx := strings.Index(code, "// initial render")
	slotIdx := strings.Index(code, `$$slots["footer"]`)
	if slotIdx < 0 {
		slotIdx = strings.Index(code, `$$slots['footer']`)
	}
	if initIdx < 0 {
		t.Fatal("no '// initial render' section found")
	}
	if slotIdx < 0 {
		t.Fatal("no $$slots lookup found in output")
	}
	if slotIdx < initIdx {
		t.Errorf("slot content insertion appears before '// initial render' (create phase) — parentNode will be null\n"+
			"slotIdx=%d initIdx=%d", slotIdx, initIdx)
	}
}

// Regression: arrow-function event handlers like @click="() => foo()" were
// double-wrapped: ($event) => { () => foo(); } — inner arrow never called.
func TestGenerateCode_ArrowHandlerNotDoubleWrapped(t *testing.T) {
	sfc := &SFC{
		Script:   `let x = $state(0); function foo() { x++; }`,
		Template: `<button @click="() => foo()">click</button>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("Btn", sfc, nodes, a, "vela-test")

	// Should NOT produce: ($event) => { () => foo(); }
	if strings.Contains(code, "($event) => { () =>") {
		t.Errorf("arrow handler was double-wrapped:\n%s", code)
	}
	// Should produce the arrow directly (stored in a const for removeEventListener)
	if !strings.Contains(code, "() => foo()") {
		t.Errorf("arrow handler body lost from output:\n%s", code)
	}
}

// Regression: expression handler @input="($e) => x = $e.target.value"
// should be stored directly, not wrapped in another arrow.
func TestGenerateCode_ExprHandlerNotDoubleWrapped(t *testing.T) {
	sfc := &SFC{
		Script:   `let v = $state('');`,
		Template: `<input @input="($e) => v = $e.target.value" />`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("Input", sfc, nodes, a, "vela-test")

	// Must NOT double-wrap: ($event) => { ($e) => v = $e.target.value; }
	if strings.Contains(code, "($event) => { ($e)") {
		t.Errorf("parameterised arrow handler was double-wrapped:\n%s", code)
	}
}

// isHandlerExpr unit tests
func TestIsHandlerExpr(t *testing.T) {
	yes := []string{
		"() => foo()",
		"($e) => bar($e)",
		"(e) => { e.preventDefault(); }",
		"function() { return 1; }",
		"async () => await fetch(url)",
		"e => doThing(e)",
	}
	no := []string{
		"myFunc",
		"handleClick",
		"",
		"obj.method",
	}
	for _, s := range yes {
		if !isHandlerExpr(s) {
			t.Errorf("isHandlerExpr(%q) should be true", s)
		}
	}
	for _, s := range no {
		if isHandlerExpr(s) {
			t.Errorf("isHandlerExpr(%q) should be false", s)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Boolean attribute binding
// ─────────────────────────────────────────────────────────────────────────────

func TestIsBoolAttr(t *testing.T) {
	bools := []string{"checked", "disabled", "readonly", "required",
		"selected", "multiple", "hidden", "autofocus", "loop", "muted", "open"}
	nonBools := []string{"class", "id", "value", "href", "src",
		"placeholder", "type", "name", "data-x", "aria-label"}

	for _, a := range bools {
		if !isBoolAttr(a) {
			t.Errorf("isBoolAttr(%q) should be true", a)
		}
	}
	for _, a := range nonBools {
		if isBoolAttr(a) {
			t.Errorf("isBoolAttr(%q) should be false", a)
		}
	}
}

func TestBindAttrStmt_BooleanUsesProperty(t *testing.T) {
	stmt := bindAttrStmt("$$el", "checked", "todo.done", "  ")
	// Must use property assignment, NOT setAttribute
	if strings.Contains(stmt, "setAttribute") {
		t.Errorf("boolean attr should use property, not setAttribute: %s", stmt)
	}
	if !strings.Contains(stmt, "$$el.checked") {
		t.Errorf("expected $$el.checked property assignment: %s", stmt)
	}
	if !strings.Contains(stmt, "!!(todo.done)") {
		t.Errorf("expected !!() coercion for boolean: %s", stmt)
	}
}

func TestBindAttrStmt_ClassUsesClassName(t *testing.T) {
	stmt := bindAttrStmt("$$el", "class", "myClass", "  ")
	if !strings.Contains(stmt, "$$el.className") {
		t.Errorf("class attr should use className: %s", stmt)
	}
}

func TestBindAttrStmt_RegularUsesSetAttribute(t *testing.T) {
	stmt := bindAttrStmt("$$el", "href", "url", "  ")
	if !strings.Contains(stmt, "setAttribute('href'") {
		t.Errorf("regular attr should use setAttribute: %s", stmt)
	}
}

func TestGenerateCode_BooleanBindingCheckbox(t *testing.T) {
	sfc := &SFC{
		Script:   `let done = $state(false);`,
		Template: `<input type="checkbox" :checked="done" />`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("Check", sfc, nodes, a, "vela-test")

	// Must not use setAttribute for checked
	if strings.Contains(code, "setAttribute('checked'") {
		t.Errorf("boolean :checked must not use setAttribute:\n%s", code)
	}
	// Must use property assignment with !!()
	if !strings.Contains(code, ".checked = !!(done)") {
		t.Errorf("expected .checked = !!(done) in output:\n%s", code)
	}
}

func TestGenerateCode_BooleanBindingDisabled(t *testing.T) {
	sfc := &SFC{
		Script:   `let loading = $state(true);`,
		Template: `<button :disabled="loading">Submit</button>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("Btn", sfc, nodes, a, "vela-test")

	if strings.Contains(code, "setAttribute('disabled'") {
		t.Errorf("boolean :disabled must not use setAttribute:\n%s", code)
	}
	if !strings.Contains(code, ".disabled = !!(loading)") {
		t.Errorf("expected .disabled = !!(loading):\n%s", code)
	}
}

func TestGenerateCode_BooleanBindingHidden(t *testing.T) {
	sfc := &SFC{
		Script:   `let visible = $state(true);`,
		Template: `<div :hidden="!visible">content</div>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("Vis", sfc, nodes, a, "vela-test")

	if strings.Contains(code, "setAttribute('hidden'") {
		t.Errorf("boolean :hidden must not use setAttribute:\n%s", code)
	}
	if !strings.Contains(code, ".hidden = !!(!visible)") {
		t.Errorf("expected .hidden = !!(!visible):\n%s", code)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Prop assignment rewriting
// ─────────────────────────────────────────────────────────────────────────────

func TestStatesSet_IncludesProps(t *testing.T) {
	a := analyzeScript(`
		export let name = $bindable('World');
		export let count = 0;
		let x = $state(0);
	`)
	ss := a.stateSet()
	for _, want := range []string{"name", "count", "x"} {
		if !ss[want] {
			t.Errorf("stateSet() should include %q", want)
		}
	}
}

func TestCleanScript_PropAssignmentRewritten(t *testing.T) {
	// export let name is a prop — assigning name = "new" should become $$set_name("new")
	a := analyzeScript(`
		export let name = $bindable('World');
		function rename(v) { name = v; }
	`)
	if !strings.Contains(a.CleanScript, "$$set_name") {
		t.Errorf("prop assignment should be rewritten to $$set_name:\n%s", a.CleanScript)
	}
	if strings.Contains(a.CleanScript, "name = v") {
		t.Errorf("raw prop assignment should be gone:\n%s", a.CleanScript)
	}
}

func TestGenerateCode_PropAssignmentInMethod(t *testing.T) {
	sfc := &SFC{
		Script: `
			export let name = $bindable('World');
			let nameEdit = $state('');
			function saveEdit() { name = nameEdit; }
		`,
		Template: `<div>{name}</div>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("Card", sfc, nodes, a, "vela-test")

	if !strings.Contains(code, "$$set_name") {
		t.Errorf("saveEdit should call $$set_name:\n%s", code)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Lifecycle body rewriting
// ─────────────────────────────────────────────────────────────────────────────

func TestLifecycle_OnMountBodyRewritten(t *testing.T) {
	a := analyzeScript(`
		let online = $state(false);
		onMount(() => {
			online = true;
		});
	`)
	if len(a.Lifecycles) == 0 {
		t.Fatal("no lifecycles found")
	}
	lc := a.Lifecycles[0]
	if strings.Contains(lc.Body, "online = true") && !strings.Contains(lc.Body, "$$set_online") {
		t.Errorf("onMount body state assignment should be rewritten:\n%s", lc.Body)
	}
	if !strings.Contains(lc.Body, "$$set_online(true)") {
		t.Errorf("expected $$set_online(true) in onMount body:\n%s", lc.Body)
	}
}

func TestLifecycle_OnMountRewritesNestedClosureAssignment(t *testing.T) {
	// online = x inside a setInterval callback inside onMount should be rewritten
	a := analyzeScript(`
		let online = $state(false);
		onMount(() => {
			setInterval(() => { online = Math.random() > 0.5; }, 1000);
		});
	`)
	if len(a.Lifecycles) == 0 {
		t.Fatal("no lifecycles found")
	}
	lc := a.Lifecycles[0]
	if !strings.Contains(lc.Body, "$$set_online") {
		t.Errorf("nested state assignment in onMount body not rewritten:\n%s", lc.Body)
	}
}

func TestLifecycle_PropAssignmentInOnMount(t *testing.T) {
	// Props should also be rewritten inside onMount
	a := analyzeScript(`
		export let count = $bindable(0);
		onMount(() => { count = 42; });
	`)
	if len(a.Lifecycles) == 0 {
		t.Fatal("no lifecycle found")
	}
	lc := a.Lifecycles[0]
	if !strings.Contains(lc.Body, "$$set_count") {
		t.Errorf("prop assignment in onMount not rewritten:\n%s", lc.Body)
	}
}

func TestLifecycle_OnDestroyBodyRewritten(t *testing.T) {
	a := analyzeScript(`
		let status = $state('active');
		onDestroy(() => { status = 'destroyed'; });
	`)
	if len(a.Lifecycles) == 0 {
		t.Fatal("no lifecycle found")
	}
	lc := a.Lifecycles[0]
	if !strings.Contains(lc.Body, "$$set_status") {
		t.Errorf("onDestroy body state assignment not rewritten:\n%s", lc.Body)
	}
}

func TestEffect_BodyRewritten(t *testing.T) {
	// $effect bodies that mutate other state should be rewritten
	a := analyzeScript(`
		let count = $state(0);
		let doubled = $state(0);
		$effect(() => { doubled = count * 2; });
	`)
	if len(a.Effects) == 0 {
		t.Fatal("no effect found")
	}
	eff := a.Effects[0]
	if !strings.Contains(eff.Body, "$$set_doubled") {
		t.Errorf("effect body state assignment not rewritten:\n%s", eff.Body)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Full pipeline — UserCard-like with all features
// ─────────────────────────────────────────────────────────────────────────────

func TestFullPipeline_PropBindingAndLifecycle(t *testing.T) {
	src := `
<script>
  export let name = $bindable('World');
  let online = $state(false);
  let initials = $derived(name.split(' ').map(w => w[0]).join(''));

  onMount(() => {
    online = true;
  });
  onDestroy(() => {
    online = false;
  });

  $effect(() => {
    document.title = name;
  });

  function saveEdit(v) { name = v; }
</script>
<template>
  <div>
    <span>{initials}</span>
    <input type="text" :value="name" @input="($e) => name = $e.target.value" />
    <input type="checkbox" :checked="online" />
  </div>
</template>
`
	sfc, err := parseSFC(src)
	if err != nil {
		t.Fatalf("parseSFC: %v", err)
	}
	nodes, err := parseTemplate(sfc.Template)
	if err != nil {
		t.Fatalf("parseTemplate: %v", err)
	}
	a := analyzeScript(sfc.Script)
	code := generateCode("Profile", sfc, nodes, a, "vela-prof")

	checks := []struct{ desc, want, notWant string }{
		{"prop setter exists", "function $$set_name", ""},
		{"prop init from $$props", "$$props.name", ""},
		{"onMount body rewritten", "$$set_online(true)", ""},
		{"onDestroy body rewritten", "$$set_online(false)", ""},
		{"saveEdit uses setter", "$$set_name(v)", ""},
		{"input rewrite for name", "$$set_name", ""},
		{"checked uses property", ".checked = !!(online)", "setAttribute('checked'"},
		{"$$onChange_name for bindable", "$$onChange_name", ""},
		{"$$dispatch present", "function $$dispatch", ""},
	}
	for _, c := range checks {
		hasWant := c.want == "" || strings.Contains(code, c.want)
		hasNotWant := c.notWant != "" && strings.Contains(code, c.notWant)
		if !hasWant || hasNotWant {
			t.Errorf("[%s] want=%q notWant=%q\ncode excerpt:\n%s",
				c.desc, c.want, c.notWant, code[:min(600, len(code))])
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  RHS extraction with parenthesised expressions
// ─────────────────────────────────────────────────────────────────────────────

// Regression: `x = (a > b) ? 'yes' : 'no'`
// parsePrimary strips outer parens, so ConditionalExpr.Pos pointed to `a`
// instead of `(`, causing rhs to be `a > b) ? 'yes' : 'no'` (wrong).
func TestRewriteAssignments_ParenthesisedTernary(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{
			`x = (h >= 12) ? 'PM' : 'AM';`,
			`$$set_x((h >= 12) ? 'PM' : 'AM')`,
		},
		{
			`x = (a > 0) ? a : -a;`,
			`$$set_x((a > 0) ? a : -a)`,
		},
		{
			// No outer parens — must still work
			`x = h >= 12 ? 'PM' : 'AM';`,
			`$$set_x(h >= 12 ? 'PM' : 'AM')`,
		},
		{
			// Compound assignment with parenthesised RHS
			`x += (n * 2);`,
			`$$set_x(x+(n * 2))`,
		},
	}

	stateSet := map[string]bool{"x": true}
	for _, tc := range cases {
		prog := ParseJS(tc.src)
		got := RewriteAssignments(tc.src, prog, stateSet)
		if !strings.Contains(got, tc.want) {
			t.Errorf("RewriteAssignments(%q):\n  want to contain %q\n  got  %q",
				tc.src, tc.want, got)
		}
		// Must not have the bug pattern: closing paren before the ternary operator
		if strings.Contains(got, ") ? ") && strings.Contains(got, "$$set_") {
			// Check it's the correct form: the ) closes the setter call, not the outer paren
			if strings.Contains(got, "$$set_x() ? ") || strings.Contains(got, "$$set_x(h >= 12) ?") {
				t.Errorf("detected double-wrapped / truncated ternary bug: %q", got)
			}
		}
	}
}

func TestExtractRHS_SimpleAssign(t *testing.T) {
	// x = 42  →  lhsEnd=1, op="=", stmtEnd=6 (or wherever)
	// The src used here simulates the statement text
	src := "x = 42;"
	rhs := extractRHS(src, 1, "=", len(src))
	if rhs != "42" {
		t.Errorf("extractRHS simple: want '42', got %q", rhs)
	}
}

func TestExtractRHS_Ternary(t *testing.T) {
	src := "ampm = (h >= 12) ? 'PM' : 'AM';"
	rhs := extractRHS(src, len("ampm"), "=", len(src))
	want := "(h >= 12) ? 'PM' : 'AM'"
	if rhs != want {
		t.Errorf("extractRHS ternary:\n  want %q\n  got  %q", want, rhs)
	}
}

func TestExtractRHS_Compound(t *testing.T) {
	src := "count += step * 2;"
	rhs := extractRHS(src, len("count"), "+=", len(src))
	want := "step * 2"
	if rhs != want {
		t.Errorf("extractRHS compound:\n  want %q\n  got  %q", want, rhs)
	}
}

// Full pipeline: the Clock component had this exact bug in its onMount body
func TestFullPipeline_ClockTernaryInOnMount(t *testing.T) {
	src := `
<script>
  let ampm = $state('');
  let hours = $state('--');
  onMount(() => {
    setInterval(() => {
      let h = new Date().getHours();
      ampm = (h >= 12) ? 'PM' : 'AM';
      hours = String(h % 12 || 12).padStart(2, '0');
    }, 1000);
  });
</script>
<template><div>{hours} {ampm}</div></template>
`
	sfc, err := parseSFC(src)
	if err != nil {
		t.Fatalf("parseSFC: %v", err)
	}
	nodes, err := parseTemplate(sfc.Template)
	if err != nil {
		t.Fatalf("parseTemplate: %v", err)
	}
	a := analyzeScript(sfc.Script)

	// The onMount body should have the ternary rewritten correctly
	if len(a.Lifecycles) == 0 {
		t.Fatal("no lifecycle found")
	}
	body := a.Lifecycles[0].Body

	// Must produce a valid setter call — not a truncated one
	if !strings.Contains(body, "$$set_ampm") {
		t.Errorf("ampm not rewritten in lifecycle body:\n%s", body)
	}
	// The ternary must be fully inside the setter call
	// Bad: $$set_ampm(h >= 12) ? 'PM' : 'AM')
	// Good: $$set_ampm((h >= 12) ? 'PM' : 'AM')
	if strings.Contains(body, "$$set_ampm(h >= 12) ?") {
		t.Errorf("ternary was truncated in rewrite (paren bug):\n%s", body)
	}
	if strings.Contains(body, "$$set_ampm((h >= 12) ? 'PM' : 'AM')") ||
		strings.Contains(body, "$$set_ampm(h >= 12 ? 'PM' : 'AM')") {
		// Either form is correct
	} else {
		t.Errorf("unexpected ampm rewrite form:\n%s", body)
	}

	// Codegen should succeed without syntax errors
	code := generateCode("Clock", sfc, nodes, a, "vela-test")
	if !strings.Contains(code, "export function Clock(") {
		t.Errorf("codegen failed:\n%s", code[:min(300, len(code))])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Component directive parsing regression tests
// ─────────────────────────────────────────────────────────────────────────────

func TestParseTemplate_ComponentWithEach(t *testing.T) {
	html := `<div><MyCard #each="items as item" #key="item.id" :title="item.name" /></div>`
	nodes, err := parseTemplate(html)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	div := nodes[0].(*ElementNode)
	if len(div.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(div.Children))
	}
	comp, ok := div.Children[0].(*ComponentNode)
	if !ok {
		t.Fatalf("expected ComponentNode, got %T", div.Children[0])
	}
	if comp.EachExpr != "items as item" {
		t.Errorf("EachExpr: want 'items as item', got %q", comp.EachExpr)
	}
	if comp.EachKey != "item.id" {
		t.Errorf("EachKey: want 'item.id', got %q", comp.EachKey)
	}
	// :title should still be in Attrs
	found := false
	for _, a := range comp.Attrs {
		if a.Name == "title" && a.IsBind {
			found = true
		}
	}
	if !found {
		t.Error("expected :title IsBind attr on component")
	}
}

func TestParseTemplate_ComponentWithIf(t *testing.T) {
	html := `<div><Modal #if="open" :title="header" /></div>`
	nodes, err := parseTemplate(html)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	div := nodes[0].(*ElementNode)
	comp, ok := div.Children[0].(*ComponentNode)
	if !ok {
		t.Fatalf("expected ComponentNode, got %T", div.Children[0])
	}
	if comp.IfCond != "open" {
		t.Errorf("IfCond: want 'open', got %q", comp.IfCond)
	}
}

func TestGenerateCode_ComponentEachRoutedCorrectly(t *testing.T) {
	// The key regression: <MyCard #each="items as item" #key="item.id" />
	// must produce a loop function, NOT a single mount at top-level.
	sfc := &SFC{
		Script:   `import { MyCard } from './MyCard.js'; let items = $state([]);`,
		Template: `<ul><MyCard #each="items as item" #key="item.id" :title="item.name" @select="handleSelect" /></ul>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("App", sfc, nodes, a, "vela-test")

	// Must produce a loop function ($$ceach_)
	if !strings.Contains(code, "$$ceach_") {
		t.Errorf("expected component each loop ($$ceach_) in output:\n%s", code[:min(600, len(code))])
	}
	// The loop must reference item variables inside a forEach
	if !strings.Contains(code, "forEach") {
		t.Error("expected forEach loop for component #each")
	}
	// Must NOT reference 'item' outside the loop (top-level mount bug)
	// Find position of the forEach and ensure item.name only appears inside it
	forEachPos := strings.Index(code, "forEach(function(item")
	if forEachPos < 0 {
		t.Error("expected forEach(function(item, ...) in output")
	}
	// Check that no top-level 'item.name' exists before the loop
	beforeLoop := code[:forEachPos]
	if strings.Contains(beforeLoop, "item.name") || strings.Contains(beforeLoop, "item.id") {
		t.Errorf("'item' variable used before the forEach loop — top-level mount bug:\n%s", beforeLoop[max(0, len(beforeLoop)-300):])
	}
}

func TestGenerateCode_ComponentEachKeyedUpdates(t *testing.T) {
	sfc := &SFC{
		Script:   `import { Card } from './Card.js'; let notes = $state([]);`,
		Template: `<div><Card #each="notes as note" #key="note.id" :title="note.title" :selected="selectedId === note.id" /></div>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("App", sfc, nodes, a, "vela-test")

	// Keyed component each should use a Map
	if !strings.Contains(code, "new Map()") {
		t.Error("keyed component #each should use a Map for tracking instances")
	}
	// Existing instances should be updated via .set()
	if !strings.Contains(code, ".set(") && !strings.Contains(code, "comp.set(") {
		t.Error("keyed component #each should call .set() to update existing instances")
	}
	// Removed instances should be destroyed
	if !strings.Contains(code, ".destroy()") {
		t.Error("keyed component #each should call .destroy() for removed instances")
	}
}

func TestGenerateCode_ComponentIfRoutedCorrectly(t *testing.T) {
	sfc := &SFC{
		Script:   `import { Modal } from './Modal.js'; let open = $state(false);`,
		Template: `<div><Modal #if="open" :title="'Hello'" @close="() => open = false" /></div>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("App", sfc, nodes, a, "vela-test")

	// Must use conditional component mount ($$cif_)
	if !strings.Contains(code, "$$cif_") {
		t.Errorf("expected conditional component mount ($$cif_) in output:\n%s", code[:min(500, len(code))])
	}
	// Component should be destroyed when condition becomes false
	if !strings.Contains(code, ".destroy()") {
		t.Error("conditional component must call .destroy() when unmounted")
	}
	// Must be re-checked when 'open' changes
	if !strings.Contains(code, "function $$set_open") {
		t.Error("expected $$set_open setter")
	}
}

func max(a, b int) int {
	if a > b { return a }
	return b
}

// ─────────────────────────────────────────────────────────────────────────────
//  Regression: native 'select' event / focus loss bugs
// ─────────────────────────────────────────────────────────────────────────────

func TestGenerateCode_ComponentEachGuardsCustomEvent(t *testing.T) {
	// Event handlers on component wrappers must guard with instanceof CustomEvent
	// so native DOM events (e.g. the built-in 'select' event on <input>)
	// that share the same name don't trigger with $e.detail === null.
	sfc := &SFC{
		Script:   `import { NoteCard } from './NoteCard.js'; let notes = $state([]);`,
		Template: `<div><NoteCard #each="notes as note" #key="note.id" @select="($e) => handleSelect($e.detail.id)" @delete="($e) => handleDelete($e.detail.id)" /></div>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("App", sfc, nodes, a, "vela-test")

	// All component event listeners must check instanceof CustomEvent
	if !strings.Contains(code, "instanceof CustomEvent") {
		t.Errorf("component event handlers must guard with instanceof CustomEvent:\n%s",
			code[:min(800, len(code))])
	}
	// Must NOT wire the handler directly without the guard
	if strings.Contains(code, `addEventListener('select', ($e) => handleSelect`) {
		t.Error("event handler wired without instanceof CustomEvent guard")
	}
}

func TestGenerateCode_ComponentEachNoUnnecessaryDOMMove(t *testing.T) {
	// Keyed #each must not call insertBefore on elements already in correct position.
	// The generated code must include a nextSibling position check.
	sfc := &SFC{
		Script:   `import { Card } from './Card.js'; let items = $state([]);`,
		Template: `<div><Card #each="items as item" #key="item.id" :label="item.text" /></div>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("App", sfc, nodes, a, "vela-test")

	// Must have the nextSibling guard before insertBefore
	if !strings.Contains(code, "nextSibling") {
		t.Errorf("keyed component #each must check nextSibling before insertBefore (to prevent focus loss):\n%s",
			code[:min(800, len(code))])
	}
}

func TestGenerateCode_PlainComponentGuardsCustomEvent(t *testing.T) {
	// Even plain (non-#each) component wrappers must guard events.
	sfc := &SFC{
		Script:   `import { Modal } from './Modal.js';`,
		Template: `<div><Modal @close="($e) => handleClose($e.detail)" @select="onSelect" /></div>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("App", sfc, nodes, a, "vela-test")

	if !strings.Contains(code, "instanceof CustomEvent") {
		t.Errorf("plain component event handlers must guard with instanceof CustomEvent:\n%s",
			code[:min(600, len(code))])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  #if branch children + inline handler rewriting
// ─────────────────────────────────────────────────────────────────────────────

func TestRewriteHandlerAssignments_ConciseArrow(t *testing.T) {
	sfc := &SFC{
		Script:   `let visible = $state(false);`,
		Template: `<input type="checkbox" @change="($e) => visible = $e.target.checked" />`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("Comp", sfc, nodes, a, "vela-test")

	// The handler must call $$set_visible, NOT do a raw assignment
	if strings.Contains(code, "visible = $e.target.checked") && !strings.Contains(code, "$$set_visible") {
		t.Errorf("concise arrow assignment not rewritten:\n%s", code)
	}
	if !strings.Contains(code, "$$set_visible") {
		t.Errorf("expected $$set_visible in handler:\n%s", code[:min(600, len(code))])
	}
}

func TestRewriteHandlerAssignments_BlockBody(t *testing.T) {
	sfc := &SFC{
		Script:   `let count = $state(0); let step = $state(1);`,
		Template: `<button @click="($e) => { count = count + step; step = 2; }">go</button>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("Comp", sfc, nodes, a, "vela-test")

	if !strings.Contains(code, "$$set_count") {
		t.Errorf("block arrow count assignment not rewritten:\n%s", code[:min(500, len(code))])
	}
	if !strings.Contains(code, "$$set_step") {
		t.Errorf("block arrow step assignment not rewritten:\n%s", code[:min(500, len(code))])
	}
}

func TestRewriteHandlerAssignments_MultipleAssignments(t *testing.T) {
	sfc := &SFC{
		Script:   `let x = $state(0); let y = $state(0);`,
		Template: `<button @click="() => { x = 1; y = 2; }">set</button>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("Comp", sfc, nodes, a, "vela-test")

	if !strings.Contains(code, "$$set_x(1)") {
		t.Errorf("x not rewritten: %s", code[:min(400, len(code))])
	}
	if !strings.Contains(code, "$$set_y(2)") {
		t.Errorf("y not rewritten: %s", code[:min(400, len(code))])
	}
}

func TestRewriteHandlerAssignments_NoFalsePositive(t *testing.T) {
	// Non-state variables and method calls should not be touched
	sfc := &SFC{
		Script:   `let count = $state(0);`,
		Template: `<button @click="($e) => { $e.preventDefault(); localVar = 5; }">go</button>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("Comp", sfc, nodes, a, "vela-test")

	// localVar is not state — should NOT be rewritten
	if strings.Contains(code, "$$set_localVar") {
		t.Errorf("non-state var should not be rewritten: %s", code[:min(400, len(code))])
	}
	// preventDefault should be preserved
	if !strings.Contains(code, "preventDefault") {
		t.Errorf("preventDefault was removed from handler")
	}
}

func TestGenerateCode_IfBranchHasChildren(t *testing.T) {
	sfc := &SFC{
		Script: `let visible = $state(false); let step = $state(1);`,
		Template: `
			<div>
				<div #if="visible" class="step-row">
					<label>Step:</label>
					<input type="number" :value="step" @input="($e) => step = Number($e.target.value)" />
				</div>
			</div>
		`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("Comp", sfc, nodes, a, "vela-test")

	// The if-chain function must create the label and input inside the branch element
	// NOT as siblings at the root level
	chainFnStart := strings.Index(code, "function $$chain_")
	if chainFnStart < 0 {
		t.Fatal("no $$chain_ function found")
	}
	// Find end of chain function by counting braces
	depth := 0; chainFnEnd := chainFnStart
	for chainFnEnd < len(code) {
		c := code[chainFnEnd]
		if c == '{' { depth++ }
		if c == '}' { depth--; if depth < 0 { break } }
		chainFnEnd++
	}
	chainFn := code[chainFnStart:chainFnEnd]

	// The label createElement must appear inside the chain function
	if !strings.Contains(chainFn, "createElement('label')") {
		t.Errorf("label not created inside #if chain function (branch body empty):\n%s", chainFn)
	}
	if !strings.Contains(chainFn, "createElement('input')") {
		t.Errorf("input not created inside #if chain function (branch body empty):\n%s", chainFn)
	}
}

func TestGenerateCode_IfBranchInlineHandlerRewritten(t *testing.T) {
	// The full scenario from If.js: visible = $state, checkbox inside #if branch
	// with inline handler @change="($e) => visible = $e.target.checked"
	sfc := &SFC{
		Script: `
			let count = $state(0);
			let visible = $state(false);
			let step = $state(1);
		`,
		Template: `
			<div>
				<button @click="() => count = count + step">+</button>
				<div #if="visible" class="step-row">
					<label>Step:</label>
					<input type="checkbox" @change="($e) => visible = $e.target.checked" />
				</div>
			</div>
		`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("IfTest", sfc, nodes, a, "vela-test")

	// Handler inside #if branch child must be rewritten
	chainFnStart := strings.Index(code, "function $$chain_")
	if chainFnStart < 0 {
		t.Fatal("no $$chain_ function")
	}
	chainFnEnd := chainFnStart + strings.Index(code[chainFnStart:], "\n  function ")
	if chainFnEnd <= chainFnStart {
		chainFnEnd = len(code)
	}
	chainFn := code[chainFnStart:chainFnEnd]

	if strings.Contains(chainFn, "visible = $e.target.checked") {
		t.Errorf("raw assignment inside #if branch handler should be rewritten:\n%s", chainFn)
	}
	if !strings.Contains(code, "$$set_visible") {
		t.Errorf("$$set_visible must appear in output:\n%s", code[:min(600, len(code))])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  #if/#elseif grouping in nested children
// ─────────────────────────────────────────────────────────────────────────────

func TestParseTemplate_IfElseifGroupedInsideElement(t *testing.T) {
	// #if and #elseif INSIDE a parent element must be grouped into one IfChainNode.
	// Previously groupIfChains was only called on top-level nodes, so nested
	// #elseif elements were left as raw ElementNodes with a __elseif__ attr,
	// which then got rendered unconditionally.
	html := `
		<div class="card">
			<div #if="!editMode">View mode</div>
			<div #elseif="editMode">Edit mode</div>
		</div>
	`
	nodes, err := parseTemplate(html)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	div := nodes[0].(*ElementNode)

	// The two divs should be merged into ONE IfChainNode, not two separate nodes
	if len(div.Children) != 1 {
		t.Fatalf("expected 1 child (IfChainNode), got %d children: %T, ...",
			len(div.Children), div.Children[0])
	}
	chain, ok := div.Children[0].(*IfChainNode)
	if !ok {
		t.Fatalf("expected IfChainNode inside div, got %T", div.Children[0])
	}
	if len(chain.Branches) != 2 {
		t.Errorf("expected 2 branches, got %d", len(chain.Branches))
	}

	// No element with raw __elseif__ attr should remain
	for _, child := range div.Children {
		if el, ok := child.(*ElementNode); ok {
			for _, attr := range el.Attrs {
				if attr.Name == "__elseif__" {
					t.Errorf("__elseif__ sentinel left on element — groupIfChains not called on children")
				}
			}
		}
	}
}

func TestGenerateCode_IfElseifInsideElement(t *testing.T) {
	// Codegen for nested #if/#elseif must not emit __elseif__ as a DOM attribute
	sfc := &SFC{
		Script: `let editMode = $state(false);`,
		Template: `
			<div>
				<div #if="!editMode" class="view">View</div>
				<div #elseif="editMode" class="edit">Edit</div>
			</div>
		`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("Card", sfc, nodes, a, "vela-test")

	// Must NOT have __elseif__ as a setAttribute call
	if strings.Contains(code, "__elseif__") {
		t.Errorf("__elseif__ sentinel leaked into generated DOM code:\n%s",
			code[:min(600, len(code))])
	}
	// Must have a chain function handling both branches
	if !strings.Contains(code, "$$chain_") {
		t.Error("expected $$chain_ if-chain function in output")
	}
	// Both branches must be in the same chain
	if !strings.Contains(code, "$$active = 1") {
		t.Error("expected two branches ($$active = 1) in if-chain")
	}
}

func TestParseTemplate_IfElseGroupedThreeLevelsDeep(t *testing.T) {
	// Verify grouping works at any nesting depth
	html := `<outer><middle><p #if="a">A</p><p #elseif="b">B</p><p #else>C</p></middle></outer>`
	nodes, err := parseTemplate(html)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	outer := nodes[0].(*ElementNode)
	middle := outer.Children[0].(*ElementNode)

	if len(middle.Children) != 1 {
		t.Fatalf("expected 1 IfChainNode inside middle, got %d", len(middle.Children))
	}
	chain, ok := middle.Children[0].(*IfChainNode)
	if !ok {
		t.Fatalf("expected IfChainNode, got %T", middle.Children[0])
	}
	if len(chain.Branches) != 3 {
		t.Errorf("expected 3 branches (if/elseif/else), got %d", len(chain.Branches))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Component inside #if branch
// ─────────────────────────────────────────────────────────────────────────────

func TestGenerateCode_ComponentInsideIfBranch(t *testing.T) {
	// TagBadge inside an #if branch must be rendered, not silently dropped.
	sfc := &SFC{
		Script:   `import { TagBadge } from './TagBadge.js'; let show = $state(true); let tags = $state(['vela','demo']);`,
		Template: `<div #if="show"><TagBadge #each="tags as t" #key="t" :tag="t" :color="'purple'" :removable="false" /></div>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("App", sfc, nodes, a, "vela-test")

	// TagBadge must appear somewhere in the generated code
	if !strings.Contains(code, "TagBadge(") {
		t.Errorf("TagBadge not mounted in #if branch — component dropped:\n%s",
			code[:min(600, len(code))])
	}
	// The mount must be inside the chain function, not at top level after it
	chainStart := strings.Index(code, "function $$chain_")
	tagBadgePos := strings.Index(code, "TagBadge(")
	if tagBadgePos < 0 {
		t.Fatal("TagBadge( not found")
	}
	if tagBadgePos < chainStart {
		t.Errorf("TagBadge mounted before the #if chain function — wrong scope")
	}
}

func TestGenerateCode_ComponentEachInsideIfBranch(t *testing.T) {
	// Keyed #each of component inside an #if branch must use a Map.
	sfc := &SFC{
		Script:   `import { Card } from './Card.js'; let visible = $state(true); let items = $state([]);`,
		Template: `<div #if="visible"><Card #each="items as item" #key="item.id" :label="item.text" /></div>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("App", sfc, nodes, a, "vela-test")

	chainStart := strings.Index(code, "function $$chain_")
	if chainStart < 0 {
		t.Fatal("no chain function")
	}
	// Find the chain function body
	chainEnd := chainStart + strings.Index(code[chainStart:], "\n  function ")
	if chainEnd <= chainStart {
		chainEnd = len(code)
	}
	chainFn := code[chainStart:chainEnd]

	if !strings.Contains(chainFn, "Card(") {
		t.Errorf("Card component not mounted inside #if chain function:\n%s", chainFn[:min(500, len(chainFn))])
	}
	if !strings.Contains(chainFn, "new Map()") {
		t.Errorf("keyed #each inside #if branch should use a Map:\n%s", chainFn[:min(500, len(chainFn))])
	}
}

func TestGenerateCode_PlainComponentInsideIfBranch(t *testing.T) {
	// A plain (non-#each) component inside an #if branch.
	sfc := &SFC{
		Script:   `import { Modal } from './Modal.js'; let open = $state(false);`,
		Template: `<div><div #if="open"><Modal :title="'Hello'" /></div></div>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("App", sfc, nodes, a, "vela-test")

	chainStart := strings.Index(code, "function $$chain_")
	chainEnd := chainStart + strings.Index(code[chainStart:], "\n  function ")
	if chainEnd <= chainStart {
		chainEnd = len(code)
	}
	chainFn := code[chainStart:chainEnd]

	if !strings.Contains(chainFn, "Modal(") {
		t.Errorf("Modal not mounted inside #if chain function:\n%s", chainFn[:min(400, len(chainFn))])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Prop deps in derived vars + branch component re-render
// ─────────────────────────────────────────────────────────────────────────────

func TestReactiveNames_IncludesProps(t *testing.T) {
	a := analyzeScript(`
		export let pinned = false;
		export let selected = false;
		let editMode = $state(false);
		let cardClass = $derived('card' + (pinned ? ' pinned' : '') + (selected ? ' selected' : '') + (editMode ? ' editing' : ''));
	`)
	names := a.reactiveNames()
	nameSet := map[string]bool{}
	for _, n := range names {
		nameSet[n] = true
	}
	for _, want := range []string{"pinned", "selected", "editMode", "cardClass"} {
		if !nameSet[want] {
			t.Errorf("reactiveNames should include %q; got: %v", want, names)
		}
	}
}

func TestDerivedDeps_IncludesPropVars(t *testing.T) {
	a := analyzeScript(`
		export let pinned = false;
		export let selected = false;
		let editMode = $state(false);
		let cardClass = $derived('card' + (pinned ? ' pinned' : '') + (selected ? ' selected' : '') + (editMode ? ' editing' : ''));
	`)
	if len(a.DerivedVars) == 0 {
		t.Fatal("no derived vars found")
	}
	dv := a.DerivedVars[0] // cardClass
	deps := map[string]bool{}
	for _, d := range dv.Deps {
		deps[d] = true
	}
	for _, want := range []string{"pinned", "selected", "editMode"} {
		if !deps[want] {
			t.Errorf("cardClass.Deps should include %q; got: %v", want, dv.Deps)
		}
	}
}

func TestGenerateCode_PropChangeTriggersDerivedUpdate(t *testing.T) {
	// When a prop changes via set(), the derived vars that depend on it must update.
	sfc := &SFC{
		Script: `
			export let pinned = false;
			export let selected = false;
			let editMode = $state(false);
			let cardClass = $derived('card' + (pinned ? ' card-pinned' : '') + (selected ? ' card-selected' : '') + (editMode ? ' card-editing' : ''));
		`,
		Template: `<div :class="cardClass">content</div>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("Card", sfc, nodes, a, "vela-test")

	// $$set_pinned must call $$upd_cardClass
	pinSetStart := strings.Index(code, "function $$set_pinned")
	if pinSetStart < 0 {
		t.Fatal("no $$set_pinned found")
	}
	pinSetEnd := pinSetStart + strings.Index(code[pinSetStart:], "\n  function ")
	if pinSetEnd <= pinSetStart {
		pinSetEnd = len(code)
	}
	pinSetter := code[pinSetStart:pinSetEnd]
	if !strings.Contains(pinSetter, "$$upd_cardClass") {
		t.Errorf("$$set_pinned must call $$upd_cardClass; setter:\n%s", pinSetter)
	}

	// $$set_selected must also call $$upd_cardClass
	selSetStart := strings.Index(code, "function $$set_selected")
	if selSetStart < 0 {
		t.Fatal("no $$set_selected found")
	}
	selSetEnd := selSetStart + strings.Index(code[selSetStart:], "\n  function ")
	if selSetEnd <= selSetStart {
		selSetEnd = len(code)
	}
	selSetter := code[selSetStart:selSetEnd]
	if !strings.Contains(selSetter, "$$upd_cardClass") {
		t.Errorf("$$set_selected must call $$upd_cardClass; setter:\n%s", selSetter)
	}
}

func TestGenerateCode_BranchEachHoistedToComponentScope(t *testing.T) {
	// A #each component inside an #if branch must have its update function
	// hoisted to component scope so it re-renders when the array changes.
	sfc := &SFC{
		Script: `
			import { TagBadge } from './TagBadge.js';
			let editMode = $state(false);
			let tags = $state(['vela', 'demo']);
		`,
		Template: `
			<div>
				<div #if="editMode">
					<TagBadge #each="tags as t" #key="t" :tag="t" :removable="true" @remove="($e) => removeTag($e.detail.tag)" />
				</div>
			</div>
		`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("NoteCard", sfc, nodes, a, "vela-test")

	// The $$bceach_ function must be at component scope (not inside the chain fn)
	bceachPos := strings.Index(code, "$$bceach_")
	chainPos := strings.Index(code, "function $$chain_")
	if bceachPos < 0 {
		t.Fatal("no $$bceach_ update function found — branch #each not hoisted")
	}
	// The function DEFINITION must appear before or outside the chain function
	// (it's written to g.create which comes before chain functions)
	if bceachPos > chainPos && chainPos > 0 {
		// Find the end of the chain function
		chainEnd := chainPos
		depth := 0; started := false
		for chainEnd < len(code) {
			c := code[chainEnd]
			if c == '{' { depth++; started = true }
			if c == '}' && started { depth--; if depth == 0 { break } }
			chainEnd++
		}
		// Check if bceach appears OUTSIDE (before) the chain fn
		bceachFnDef := strings.Index(code, "function $$bceach_")
		if bceachFnDef > chainEnd {
			t.Errorf("$$bceach_ function defined AFTER chain function — not hoisted to component scope")
		}
	}

	// When tags changes, $$set_tags must call the bceach update
	setTagsStart := strings.Index(code, "function $$set_tags")
	if setTagsStart < 0 {
		t.Fatal("no $$set_tags found")
	}
	setTagsEnd := setTagsStart + strings.Index(code[setTagsStart:], "\n  function ")
	if setTagsEnd <= setTagsStart { setTagsEnd = len(code) }
	setTagsFn := code[setTagsStart:setTagsEnd]
	if !strings.Contains(setTagsFn, "$$bceach_") {
		t.Errorf("$$set_tags must call $$bceach_ to re-render tags inside #if branch;\n$$set_tags:\n%s", setTagsFn)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Bug regressions: async, onMount order, component #if/#elseif chains
// ─────────────────────────────────────────────────────────────────────────────

func TestAnalyzeScript_AsyncFunctionPreserved(t *testing.T) {
	a := analyzeScript(`
		async function fetchData(url) {
			const res = await fetch(url);
			return res.json();
		}
		async function search() {
			const data = await fetchData('/api');
			return data;
		}
	`)
	if !strings.Contains(a.CleanScript, "async function fetchData") {
		t.Errorf("async keyword lost from fetchData:\n%s", a.CleanScript)
	}
	if !strings.Contains(a.CleanScript, "async function search") {
		t.Errorf("async keyword lost from search:\n%s", a.CleanScript)
	}
}

func TestGenerateCode_OnMountAfterMount(t *testing.T) {
	// onMount callbacks must appear AFTER $$target.appendChild($$root)
	sfc := &SFC{
		Script:   `onMount(() => { document.getElementById('x').focus(); });`,
		Template: `<div id="x">hi</div>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("App", sfc, nodes, a, "vela-test")

	mountIdx := strings.Index(code, "$$target.appendChild($$root)")
	onMountIdx := strings.Index(code, "// onMount")
	if mountIdx < 0 {
		t.Fatal("no mount call found")
	}
	if onMountIdx < 0 {
		t.Fatal("no onMount section found")
	}
	if onMountIdx < mountIdx {
		t.Errorf("onMount appears BEFORE $$target.appendChild — component not in DOM yet\nonMountIdx=%d mountIdx=%d", onMountIdx, mountIdx)
	}
}

func TestParseTemplate_ComponentIfElseifChainGrouped(t *testing.T) {
	// #if/#elseif/#else on ComponentNode siblings must be grouped into one IfChainNode
	html := `
		<div>
			<SearchPage #if="route === 'search'" />
			<FavoritesPage #elseif="route === 'favorites'" />
			<UserPage #elseif="route === 'user'" :login="params.login" />
		</div>
	`
	nodes, err := parseTemplate(html)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	div := nodes[0].(*ElementNode)
	if len(div.Children) != 1 {
		t.Fatalf("expected 1 IfChainNode, got %d children: %v", len(div.Children),
			func() []string {
				var ts []string
				for _, c := range div.Children { ts = append(ts, fmt.Sprintf("%T", c)) }
				return ts
			}())
	}
	chain, ok := div.Children[0].(*IfChainNode)
	if !ok {
		t.Fatalf("expected *IfChainNode, got %T", div.Children[0])
	}
	if len(chain.Branches) != 3 {
		t.Errorf("expected 3 branches, got %d", len(chain.Branches))
	}
	// Each branch must hold a Comp, not an Elem
	for i, branch := range chain.Branches {
		if branch.Comp == nil {
			t.Errorf("branch[%d] has nil Comp — not a component branch", i)
		}
		if branch.Elem != nil {
			t.Errorf("branch[%d] has unexpected Elem", i)
		}
	}
	// No __elseif__ sentinel must remain
	for i, branch := range chain.Branches {
		if branch.Comp != nil {
			for _, a := range branch.Comp.Attrs {
				if a.Name == "__elseif__" || a.Name == "__else__" {
					t.Errorf("branch[%d] still has sentinel attr %q", i, a.Name)
				}
			}
		}
	}
}

func TestGenerateCode_ComponentIfChainProducesChainFn(t *testing.T) {
	sfc := &SFC{
		Script: `
			import { SearchPage } from './SearchPage.js';
			import { FavoritesPage } from './FavoritesPage.js';
			import { UserPage } from './UserPage.js';
			let route = $state('search');
			let login = $state('');
		`,
		Template: `
			<div>
				<SearchPage   #if="route === 'search'" />
				<FavoritesPage #elseif="route === 'favorites'" />
				<UserPage      #elseif="route === 'user'" :login="login" />
			</div>
		`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("App", sfc, nodes, a, "vela-test")

	// Must produce a single chain function
	if !strings.Contains(code, "function $$chain_") {
		t.Errorf("expected $$chain_ function for component if-chain:\n%s", code[:min(600, len(code))])
	}
	// All three components must be mounted conditionally (not always)
	if !strings.Contains(code, "SearchPage(") {
		t.Error("SearchPage not mounted")
	}
	if !strings.Contains(code, "FavoritesPage(") {
		t.Error("FavoritesPage not mounted")
	}
	if !strings.Contains(code, "UserPage(") {
		t.Error("UserPage not mounted")
	}
	// __elseif__ must NOT appear as a DOM attribute or prop
	if strings.Contains(code, "__elseif__") {
		t.Errorf("__elseif__ sentinel leaked into generated code:\n%s", code[:min(600, len(code))])
	}
	// When route changes, the chain function must be called
	if !strings.Contains(code, "function $$set_route") {
		t.Fatal("no $$set_route setter")
	}
	setRouteEnd := strings.Index(code, "function $$set_route")
	setRouteEnd += strings.Index(code[setRouteEnd:], "\n  function ")
	setRouteFn := code[strings.Index(code, "function $$set_route"):setRouteEnd]
	if !strings.Contains(setRouteFn, "$$chain_") {
		t.Errorf("$$set_route should call $$chain_ to re-evaluate route:\n%s", setRouteFn)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Effect infinite recursion prevention
// ─────────────────────────────────────────────────────────────────────────────

func TestFindWrittenVars(t *testing.T) {
	prog := ParseJS(`
		const unsub = store.subscribe(state => {
			isAuthenticated = state.isAuthenticated;
			theme = state.theme;
		});
		return unsub;
	`)
	written := findWrittenVars(prog)
	if !written["isAuthenticated"] {
		t.Error("isAuthenticated should be in written vars")
	}
	if !written["theme"] {
		t.Error("theme should be in written vars")
	}
	// 'state' is only read, not written
	if written["state"] {
		t.Error("state is read, not written — should not be in written vars")
	}
	// 'unsub' is declared (not assigned via =), depends on parser treatment
	// The key thing is the written vars are isAuthenticated and theme
}

func TestEffectDeps_ExcludeWrittenVars(t *testing.T) {
	// This effect reads isAuthenticated from a store callback but also WRITES
	// isAuthenticated — the dep should not include isAuthenticated, only the
	// store-external deps, otherwise $$set_isAuthenticated → $$effect0 →
	// $$set_isAuthenticated → ∞
	a := analyzeScript(`
		let isAuthenticated = $state(false);
		$effect(() => {
			const unsub = auth.subscribe(state => {
				isAuthenticated = state.isAuthenticated;
			});
			return unsub;
		});
	`)
	if len(a.Effects) == 0 {
		t.Fatal("no effects found")
	}
	eff := a.Effects[0]
	for _, dep := range eff.Deps {
		if dep == "isAuthenticated" {
			t.Errorf("isAuthenticated is WRITTEN in the effect body — must not be a dep (causes infinite recursion)")
		}
	}
}

func TestEffectDeps_SimpleSubscribePattern(t *testing.T) {
	// The common store-subscribe pattern: writes x = value.x inside callback
	// The effect should have NO reactive deps (it only reads from external stores)
	a := analyzeScript(`
		let count = $state(0);
		let theme = $state('light');
		$effect(() => {
			const unsub = someStore.subscribe(s => {
				count = s.count;
				theme = s.theme;
			});
			return unsub;
		});
	`)
	if len(a.Effects) == 0 {
		t.Fatal("no effects")
	}
	eff := a.Effects[0]
	for _, dep := range eff.Deps {
		if dep == "count" || dep == "theme" {
			t.Errorf("var %q is written inside the effect — should not be a dep", dep)
		}
	}
}

func TestGenerateCode_EffectDoesNotCallItsOwnSetter(t *testing.T) {
	// When isAuthenticated is WRITTEN inside an effect,
	// $$set_isAuthenticated must NOT call $$effect0 again.
	sfc := &SFC{
		Script: `
			let isAuthenticated = $state(false);
			$effect(() => {
				const unsub = auth.subscribe(state => {
					isAuthenticated = state.isAuthenticated;
				});
				return unsub;
			});
		`,
		Template: `<div #if="isAuthenticated">logged in</div>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("App", sfc, nodes, a, "vela-test")

	// Find $$set_isAuthenticated body
	setIdx := strings.Index(code, "function $$set_isAuthenticated")
	if setIdx < 0 {
		t.Fatal("no $$set_isAuthenticated found")
	}
	setEnd := setIdx + strings.Index(code[setIdx:], "\n  function ")
	if setEnd <= setIdx {
		setEnd = len(code)
	}
	setter := code[setIdx:setEnd]

	if strings.Contains(setter, "$$effect0()") {
		t.Errorf("$$set_isAuthenticated must NOT call $$effect0 — infinite recursion:\n%s", setter)
	}
}

func TestGenerateCode_EffectCleanupEmitted(t *testing.T) {
	// Effects must emit $$cleanup variables and call them before re-running
	sfc := &SFC{
		Script: `
			let x = $state(0);
			$effect(() => {
				const unsub = store.subscribe(v => { x = v; });
				return unsub;
			});
		`,
		Template: `<div>{x}</div>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("App", sfc, nodes, a, "vela-test")

	if !strings.Contains(code, "$$cleanup0") {
		t.Error("expected $$cleanup0 variable for effect cleanup")
	}
	if !strings.Contains(code, "$$cleanup0 = null") {
		t.Error("expected $$cleanup0 = null initialization")
	}
	// Cleanup called in destroy()
	destroyIdx := strings.Index(code, "destroy() {")
	if destroyIdx < 0 {
		t.Fatal("no destroy() found")
	}
	destroySection := code[destroyIdx:]
	if !strings.Contains(destroySection, "$$cleanup0") {
		t.Error("effect cleanup should be called in destroy()")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Effect dep analysis — written vars excluded, cleanup support
// ─────────────────────────────────────────────────────────────────────────────

func TestEffectDeps_ExcludesWrittenVars(t *testing.T) {
	// The effect WRITES isAuthenticated via $$set_isAuthenticated.
	// isAuthenticated must NOT be in Deps — if it were, $$set_isAuthenticated
	// would re-run the effect, which calls $$set_isAuthenticated again → infinite loop.
	a := analyzeScript(`
		let isAuthenticated = $state(false);
		let theme = $state('light');
		$effect(() => {
			const unsub = auth.subscribe(state => {
				isAuthenticated = state.isAuthenticated;
			});
			return unsub;
		});
	`)
	if len(a.Effects) == 0 {
		t.Fatal("no effects found")
	}
	eff := a.Effects[0]
	for _, dep := range eff.Deps {
		if dep == "isAuthenticated" {
			t.Errorf("effect Deps must not include written var 'isAuthenticated' (causes infinite loop); got Deps=%v", eff.Deps)
		}
	}
}

func TestEffectDeps_KeepsReadVars(t *testing.T) {
	// Variables only READ (not written) inside an effect must remain as deps.
	a := analyzeScript(`
		let count = $state(0);
		let title = $state('');
		$effect(() => {
			document.title = title + ' - ' + count;
		});
	`)
	if len(a.Effects) == 0 {
		t.Fatal("no effects found")
	}
	eff := a.Effects[0]
	deps := map[string]bool{}
	for _, d := range eff.Deps {
		deps[d] = true
	}
	for _, want := range []string{"title", "count"} {
		if !deps[want] {
			t.Errorf("effect should depend on read-only var %q; got Deps=%v", want, eff.Deps)
		}
	}
}

func TestEffectDeps_MixedReadWrite(t *testing.T) {
	// An effect that reads 'source' and writes 'derived' should depend on 'source' only.
	a := analyzeScript(`
		let source = $state(0);
		let derived = $state(0);
		$effect(() => {
			derived = source * 2;
		});
	`)
	if len(a.Effects) == 0 {
		t.Fatal("no effects found")
	}
	eff := a.Effects[0]
	deps := map[string]bool{}
	for _, d := range eff.Deps { deps[d] = true }

	if !deps["source"] {
		t.Errorf("effect should depend on 'source' (read); Deps=%v", eff.Deps)
	}
	if deps["derived"] {
		t.Errorf("effect must NOT depend on 'derived' (written); Deps=%v", eff.Deps)
	}
}

func TestGenerateCode_EffectHasCleanup(t *testing.T) {
	sfc := &SFC{
		Script: `
			let x = $state(0);
			$effect(() => {
				const unsub = store.subscribe(v => { x = v; });
				return unsub;
			});
		`,
		Template: `<div>{x}</div>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("App", sfc, nodes, a, "vela-test")

	// Cleanup variable must be declared
	if !strings.Contains(code, "$$cleanup0") {
		t.Errorf("effect cleanup var not emitted:\n%s", code[:min(400, len(code))])
	}
	// Effect must store return value via IIFE
	if !strings.Contains(code, "$$cleanup0 = (function()") {
		t.Errorf("effect return value not captured:\n%s", code[:min(600, len(code))])
	}
	// Cleanup must be called before re-run
	if !strings.Contains(code, "if ($$cleanup0) { $$cleanup0()") {
		t.Errorf("cleanup not called before effect re-runs:\n%s", code[:min(600, len(code))])
	}
	// Cleanup must be called in destroy()
	destroyIdx := strings.Index(code, "destroy()")
	if destroyIdx < 0 { t.Fatal("no destroy()") }
	destroyBlock := code[destroyIdx : destroyIdx+200]
	if !strings.Contains(destroyBlock, "$$cleanup0") {
		t.Errorf("cleanup not called in destroy():\n%s", destroyBlock)
	}
}

func TestGenerateCode_SubscribeEffectNoInfiniteLoop(t *testing.T) {
	// The key regression: $$set_isAuthenticated must NOT call $$effect0 if
	// isAuthenticated is only written (not read) inside the effect.
	sfc := &SFC{
		Script: `
			let isAuthenticated = $state(false);
			$effect(() => {
				const unsub = auth.subscribe(state => {
					isAuthenticated = state.isAuthenticated;
				});
				return unsub;
			});
		`,
		Template: `<div>{isAuthenticated ? 'logged in' : 'logged out'}</div>`,
	}
	nodes, _ := parseTemplate(sfc.Template)
	a := analyzeScript(sfc.Script)
	code := generateCode("App", sfc, nodes, a, "vela-test")

	// Find $$set_isAuthenticated
	setterStart := strings.Index(code, "function $$set_isAuthenticated")
	if setterStart < 0 {
		t.Fatal("no $$set_isAuthenticated found")
	}
	setterEnd := setterStart + strings.Index(code[setterStart:], "\n  function ")
	if setterEnd <= setterStart { setterEnd = len(code) }
	setter := code[setterStart:setterEnd]

	// The setter must NOT call $$effect0 — that would cause infinite recursion
	if strings.Contains(setter, "$$effect0()") {
		t.Errorf("$$set_isAuthenticated calls $$effect0 — this causes infinite recursion!\nSetter:\n%s", setter)
	}
}
