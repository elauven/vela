package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
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
	sfc, err := ParseSFC(src)
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
	_, err := ParseSFC(`<script>let x = 1;</script>`)
	if err == nil {
		t.Error("expected error for missing template")
	}
}

// ──────────────────────────────────────────────
//  Template Parser
// ──────────────────────────────────────────────

func TestParseTemplate_SimpleElement(t *testing.T) {
	nodes, err := ParseTemplate(`<div class="foo">hello</div>`)
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
	nodes, err := ParseTemplate(`<p>Hello {name}!</p>`)
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

func TestParseSFCHandlesNonASCIIBytesBeforeSections(t *testing.T) {
	src := "<template><p>" + string([]byte{0xdf}) + "</p></template><style>.card { color: red; }</style>"
	sfc, err := ParseSFC(src)
	if err != nil {
		t.Fatal(err)
	}
	if sfc.Style != ".card { color: red; }" {
		t.Fatalf("style = %q", sfc.Style)
	}
}

func TestParseTemplate_Directives(t *testing.T) {
	nodes, err := ParseTemplate(`<div :class={cls} @click={handler}>x</div>`)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	el, ok := nodes[0].(*ElementNode)
	if !ok {
		t.Fatalf("expected *ElementNode at nodes[0], got %T", nodes[0])
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
	for _, source := range []string{
		`<li #if="show">x</li>`,
		`<li #elseif="show">x</li>`,
		`<li #else>x</li>`,
		`<li #each="items as item">{item}</li>`,
		`<li #key="item.id">x</li>`,
	} {
		if _, err := ParseTemplate(source); err == nil || !strings.Contains(err.Error(), "no longer supported") {
			t.Fatalf("legacy template %q error = %v", source, err)
		}
	}
}

func TestParseTemplate_VoidElement(t *testing.T) {
	nodes, err := ParseTemplate(`<div><br /><input type="text" /></div>`)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	el := nodes[0].(*ElementNode)
	if len(el.Children) != 2 {
		t.Fatalf("expected 2 void children, got %d", len(el.Children))
	}
}

func TestParseTemplate_Nested(t *testing.T) {
	nodes, err := ParseTemplate(`<ul><li>a</li><li>b</li></ul>`)
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
	a := AnalyzeScript(`
let count = $state(0);
const name  = $state('hello');
let active  = $state(false);
`)
	if len(a.StateVars) != 3 {
		t.Fatalf("expected 3 state vars, got %d", len(a.StateVars))
	}
	if a.StateVars[0].Name != "count" || SourceExpr(a.StateVars[0].InitialAST) != "0" {
		t.Errorf("first state var: got %+v", a.StateVars[0])
	}
}

// ──────────────────────────────────────────────
//  AST-based assignment rewriter
// ──────────────────────────────────────────────

// ──────────────────────────────────────────────
//  Code generator — smoke tests
// ──────────────────────────────────────────────

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
		parts := ParseInterpolations(tc.input)
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

func TestAnalyzeScript_EffectDepsInTemplate(t *testing.T) {
	// Effect referencing state via template literal — deps must be found
	a := AnalyzeScript(`
		let count = $state(0);
		$effect(() => {
			document.title = ` + "`" + `Count: ${count}` + "`" + `;
		});
	`)
	if len(a.Effects) == 0 {
		t.Fatal("no effects parsed")
	}
	if !slices.Contains(a.Effects[0].Deps, "count") {
		t.Errorf("effect should depend on count; deps=%v body=%q",
			a.Effects[0].Deps, SourceBlock(a.Effects[0].BodyAST))
	}
}

// ──────────────────────────────────────────────
//  Full pipeline integration
// ──────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
//  Template parser — new directives and component nodes
// ─────────────────────────────────────────────────────────────────────────────

func TestParseTemplate_ElseIfElse(t *testing.T) {
	html := `
		{#if a}<div>A</div>
		{:else if b}<div>B</div>
		{:else}<div>C</div>{/if}
	`
	nodes, err := ParseTemplate(html)
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
	for i, want := range []string{"a", "b"} {
		if got := chains[0].Branches[i].Expr; got == nil || got.Source != want {
			t.Errorf("branch[%d].Expr = %#v, want cached expression %q", i, got, want)
		}
	}

	if chains[0].Branches[2].Cond != "" {
		t.Errorf("branch[2] (else) should have empty Cond, got %q", chains[0].Branches[2].Cond)
	}
}

func TestParseTemplate_Component(t *testing.T) {
	html := `<div><MyButton :label="text" @click="handler" /></div>`
	nodes, err := ParseTemplate(html)
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
	nodes, err := ParseTemplate(`<Container />`)
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
	a := AnalyzeScript(`
		export let name = 'World';
		export let count = 0;
		export let required;
	`)
	if len(a.Props) != 3 {
		t.Fatalf("expected 3 props, got %d", len(a.Props))
	}
	if a.Props[0].Name != "name" || SourceExpr(a.Props[0].DefaultAST) != "'World'" {
		t.Errorf("prop[0]: got name=%q default=%q", a.Props[0].Name, SourceExpr(a.Props[0].DefaultAST))
	}
	if got := PrintNode(a.Props[0].DefaultAST); got != "'World'" {
		t.Errorf("prop[0] default AST: want %q, got %q", "'World'", got)
	}
	if a.Props[2].Name != "required" || a.Props[2].DefaultAST != nil {
		t.Errorf("prop[2] should have empty default: %+v", a.Props[2])
	}
	if a.Props[2].DefaultAST != nil {
		t.Errorf("required prop must not have a default AST: %#v", a.Props[2].DefaultAST)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Codegen — if-chain and component
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
//  Full pipeline — elseif/else + props
// ─────────────────────────────────────────────────────────────────────────────

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
	a := AnalyzeScript(script)
	ds := NewDiagSet("test.vela", script)
	ValidateScript(prog, a, ds)
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
	a := AnalyzeScript(script)
	ds := NewDiagSet("test.vela", script)
	// The effect-no-deps warning moved from ValidateScript into
	// ValidateEffectBody (reactivity.go) so it could carry an
	// accurate source position.
	ValidateEffectBody(prog, a, ds)
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
	nodes, _ := ParseTemplate(sfc.Template)
	a := AnalyzeScript(sfc.Script)
	ds := NewDiagSet("test.vela", sfc.Script)
	ValidateTemplate(nodes, a, ds)
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
	nodes, _ := ParseTemplate(sfc.Template)
	a := AnalyzeScript(sfc.Script)
	ds := NewDiagSet("test.vela", sfc.Script)
	ValidateTemplate(nodes, a, ds)
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
	a := AnalyzeScript(`
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
	if !strings.Contains(SourceBlock(lc.BodyAST), "setInterval") {
		t.Errorf("body should contain setInterval; got: %q", SourceBlock(lc.BodyAST))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  $bindable props
// ─────────────────────────────────────────────────────────────────────────────

func TestAnalyzeScript_Bindable(t *testing.T) {
	a := AnalyzeScript(`export let value = $bindable(0);`)
	if len(a.Props) != 1 {
		t.Fatalf("expected 1 prop, got %d", len(a.Props))
	}
	p := a.Props[0]
	if !p.Bindable {
		t.Error("prop should be marked Bindable")
	}
	if SourceExpr(p.DefaultAST) != "0" {
		t.Errorf("default: want '0', got %q", SourceExpr(p.DefaultAST))
	}
	if got := PrintNode(p.DefaultAST); got != "0" {
		t.Errorf("default AST: want %q, got %q", "0", got)
	}
}

func TestAnalyzeScript_BindableNoDefault(t *testing.T) {
	a := AnalyzeScript(`export let val = $bindable();`)
	if len(a.Props) != 1 {
		t.Fatalf("expected 1 prop, got %d", len(a.Props))
	}
	if !a.Props[0].Bindable {
		t.Error("should be bindable")
	}
	if a.Props[0].DefaultAST != nil {
		t.Errorf("should have empty default, got %q", SourceExpr(a.Props[0].DefaultAST))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Slots
// ─────────────────────────────────────────────────────────────────────────────

func TestParseTemplate_SlotDefault(t *testing.T) {
	nodes, err := ParseTemplate(`<div><slot /></div>`)
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
	nodes, err := ParseTemplate(`<div><slot name="header">Fallback</slot></div>`)
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

// ─────────────────────────────────────────────────────────────────────────────
//  FragGen + keyed #each
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
//  $$dispatch
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
//  Regression tests for runtime bugs
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
//  Boolean attribute binding
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
//  Prop assignment rewriting
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
//  Lifecycle body rewriting
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
//  Full pipeline — UserCard-like with all features
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
//  RHS extraction with parenthesised expressions
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
//  Component directive parsing regression tests
// ─────────────────────────────────────────────────────────────────────────────

func TestParseTemplate_ComponentWithEach(t *testing.T) {
	html := `<div><MyCard #each="items as item" #key="item.id" :title="item.name" /></div>`
	if _, err := ParseTemplate(html); err == nil || !strings.Contains(err.Error(), "#each") {
		t.Fatalf("component legacy #each error = %v", err)
	}
}

func TestParseTemplate_ComponentWithIf(t *testing.T) {
	html := `<div>{#if open}<Modal :title={header} />{/if}</div>`
	nodes, err := ParseTemplate(html)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	div := nodes[0].(*ElementNode)
	chain, ok := div.Children[0].(*IfChainNode)
	if !ok {
		t.Fatalf("expected IfChainNode, got %T", div.Children[0])
	}
	if len(chain.Branches) != 1 || len(chain.Branches[0].Children) != 1 {
		t.Fatalf("if branch children = %+v", chain.Branches)
	}
	if _, ok := chain.Branches[0].Children[0].(*ComponentNode); !ok {
		t.Fatalf("if branch child = %T, want *ComponentNode", chain.Branches[0].Children[0])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Regression: native 'select' event / focus loss bugs
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
//  #if branch children + inline handler rewriting
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
//  Canonical {#if}/{:else if} blocks in nested children
// ─────────────────────────────────────────────────────────────────────────────

func TestParseTemplate_IfElseifGroupedInsideElement(t *testing.T) {
	// A nested standard block must parse into one IfChainNode.
	html := `
		<div class="card">
			{#if !editMode}<div>View mode</div>
			{:else if editMode}<div>Edit mode</div>{/if}
		</div>
	`
	nodes, err := ParseTemplate(html)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	div := nodes[0].(*ElementNode)

	// The standard block owns both branches in one IfChainNode.
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

}

func TestParseTemplate_IfElseGroupedThreeLevelsDeep(t *testing.T) {
	// Verify grouping works at any nesting depth
	html := `<outer><middle>{#if a}<p>A</p>{:else if b}<p>B</p>{:else}<p>C</p>{/if}</middle></outer>`
	nodes, err := ParseTemplate(html)
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

// ─────────────────────────────────────────────────────────────────────────────
//  Prop deps in derived vars + branch component re-render
// ─────────────────────────────────────────────────────────────────────────────

func TestParseTemplate_ComponentIfElseifChainGrouped(t *testing.T) {
	// Standard block syntax owns all component branches in one IfChainNode.
	html := `
		<div>
			{#if route === 'search'}<SearchPage />
			{:else if route === 'favorites'}<FavoritesPage />
			{:else if route === 'user'}<UserPage :login={params.login} />{/if}
		</div>
	`
	nodes, err := ParseTemplate(html)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	div := nodes[0].(*ElementNode)
	if len(div.Children) != 1 {
		t.Fatalf("expected 1 IfChainNode, got %d children: %v", len(div.Children),
			func() []string {
				var ts []string
				for _, c := range div.Children {
					ts = append(ts, fmt.Sprintf("%T", c))
				}
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
	for i, branch := range chain.Branches {
		if i < 2 && (branch.Expr == nil || branch.Expr.Source == "") {
			t.Errorf("branch[%d] is missing its cached condition AST", i)
		}
		if len(branch.Children) != 1 {
			t.Errorf("branch[%d] children = %d, want 1", i, len(branch.Children))
			continue
		}
		if _, ok := branch.Children[0].(*ComponentNode); !ok {
			t.Errorf("branch[%d] child = %T, want component", i, branch.Children[0])
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Effect dep analysis — written vars excluded, cleanup support
// ─────────────────────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
//  Svelte-style {#if} / {:else} / {/if} block syntax
// ─────────────────────────────────────────────────────────────────────────────

func TestSvelteBlock_ParseSimpleIf(t *testing.T) {
	html := `<div>{#if show}<span>yes</span>{/if}</div>`
	nodes, err := ParseTemplate(html)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	div := nodes[0].(*ElementNode)
	if len(div.Children) != 1 {
		t.Fatalf("expected 1 child (IfChainNode), got %d", len(div.Children))
	}
	chain, ok := div.Children[0].(*IfChainNode)
	if !ok {
		t.Fatalf("expected *IfChainNode, got %T", div.Children[0])
	}
	if len(chain.Branches) != 1 {
		t.Errorf("expected 1 branch, got %d", len(chain.Branches))
	}
	if chain.Branches[0].Cond != "show" {
		t.Errorf("cond want 'show', got %q", chain.Branches[0].Cond)
	}
}

func TestSvelteBlock_ParseIfElse(t *testing.T) {
	html := `<div>
		{#if logged}
		  <p>Welcome</p>
		{:else}
		  <p>Login</p>
		{/if}
	</div>`
	nodes, err := ParseTemplate(html)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	div := nodes[0].(*ElementNode)
	chain, ok := div.Children[0].(*IfChainNode)
	if !ok {
		t.Fatalf("expected *IfChainNode, got %T", div.Children[0])
	}
	if len(chain.Branches) != 2 {
		t.Errorf("expected 2 branches, got %d", len(chain.Branches))
	}
	if chain.Branches[0].Cond != "logged" {
		t.Errorf("branch 0 cond want 'logged', got %q", chain.Branches[0].Cond)
	}
	if chain.Branches[1].Cond != "" {
		t.Errorf("else branch should have empty cond, got %q", chain.Branches[1].Cond)
	}
}

func TestSvelteBlock_ParseIfElseIf(t *testing.T) {
	html := `{#if a}<p>A</p>{:else if b}<p>B</p>{:else}<p>C</p>{/if}`
	nodes, err := ParseTemplate(html)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	chain, ok := nodes[0].(*IfChainNode)
	if !ok {
		t.Fatalf("expected *IfChainNode at root, got %T", nodes[0])
	}
	if len(chain.Branches) != 3 {
		t.Fatalf("expected 3 branches, got %d", len(chain.Branches))
	}
	if chain.Branches[0].Cond != "a" {
		t.Errorf("branch 0 cond=%q", chain.Branches[0].Cond)
	}
	if chain.Branches[1].Cond != "b" {
		t.Errorf("branch 1 cond=%q", chain.Branches[1].Cond)
	}
	if chain.Branches[2].Cond != "" {
		t.Errorf("branch 2 (else) cond=%q", chain.Branches[2].Cond)
	}
}

func TestSvelteBlock_ParseIfRootLevel(t *testing.T) {
	// {#if} at template root (no outer element)
	html := `{#if x}<span>X</span>{/if}<p>always</p>`
	nodes, err := ParseTemplate(html)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("expected 2 root nodes, got %d", len(nodes))
	}
	if _, ok := nodes[0].(*IfChainNode); !ok {
		t.Errorf("node[0] want IfChainNode, got %T", nodes[0])
	}
	if _, ok := nodes[1].(*ElementNode); !ok {
		t.Errorf("node[1] want ElementNode, got %T", nodes[1])
	}
}

func TestSvelteBlock_IfBranchChildren(t *testing.T) {
	// Branch children should be parsed as proper nodes
	html := `{#if show}<div class="a"><span>{msg}</span></div>{/if}`
	nodes, err := ParseTemplate(html)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	chain := nodes[0].(*IfChainNode)
	children := chain.Branches[0].Children
	if len(children) != 1 {
		t.Fatalf("expected 1 child in branch, got %d", len(children))
	}
	div, ok := children[0].(*ElementNode)
	if !ok {
		t.Fatalf("branch child should be ElementNode, got %T", children[0])
	}
	if div.Tag != "div" {
		t.Errorf("tag want div, got %q", div.Tag)
	}
	if len(div.Attrs) == 0 || div.Attrs[0].Value != "a" {
		t.Errorf("class attr not preserved")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Svelte-style {#each} block syntax
// ─────────────────────────────────────────────────────────────────────────────

func TestSvelteBlock_ParseEach(t *testing.T) {
	html := `<ul>{#each items as item}<li>{item.name}</li>{/each}</ul>`
	nodes, err := ParseTemplate(html)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	ul := nodes[0].(*ElementNode)
	if len(ul.Children) != 1 {
		t.Fatalf("expected 1 child (EachBlockNode), got %d", len(ul.Children))
	}
	each, ok := ul.Children[0].(*EachBlockNode)
	if !ok {
		t.Fatalf("expected *EachBlockNode, got %T", ul.Children[0])
	}
	if each.EachExpr != "items as item" {
		t.Errorf("EachExpr want 'items as item', got %q", each.EachExpr)
	}
	if each.EachKey != "" {
		t.Errorf("EachKey should be empty, got %q", each.EachKey)
	}
}

func TestSvelteBlock_ParseEachWithKey(t *testing.T) {
	html := `{#each todos as todo (todo.id)}<div>{todo.text}</div>{/each}`
	nodes, err := ParseTemplate(html)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	each, ok := nodes[0].(*EachBlockNode)
	if !ok {
		t.Fatalf("expected *EachBlockNode at root, got %T", nodes[0])
	}
	if each.EachExpr != "todos as todo" {
		t.Errorf("EachExpr=%q", each.EachExpr)
	}
	if each.EachKey != "todo.id" {
		t.Errorf("EachKey want 'todo.id', got %q", each.EachKey)
	}
}

func TestSvelteBlock_RecognizesEmptyTerminalEachKey(t *testing.T) {
	if got := EachTerminalKeyStart("rows as row ()"); got < 0 {
		t.Fatal("empty terminal key was not recognised as a key construct")
	}
	nodes, err := ParseTemplate(`{#each rows as row ()}<p>{row}</p>{/each}`)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	each := nodes[0].(*EachBlockNode)
	if !each.HasKey() || each.KeyAST == nil || each.KeyAST.Source != "" {
		t.Fatalf("empty key did not retain an invalid cached AST: %+v", each)
	}
}

func TestSvelteBlock_ParseEachIterableCallWithoutKey(t *testing.T) {
	nodes, err := ParseTemplate(`{#each feats(plan) as f}<li>{f}</li>{/each}`)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	each, ok := nodes[0].(*EachBlockNode)
	if !ok {
		t.Fatalf("expected *EachBlockNode, got %T", nodes[0])
	}
	if got, want := each.EachExpr, "feats(plan) as f"; got != want {
		t.Errorf("EachExpr = %q, want %q", got, want)
	}
	if each.EachKey != "" {
		t.Errorf("function-call parentheses must not become EachKey: %q", each.EachKey)
	}
	if each.IterExpr == nil || each.IterExpr.Source != "feats(plan)" {
		t.Fatalf("iterable AST source = %#v, want feats(plan)", each.IterExpr)
	}
	if each.ItemName != "f" || each.IndexName != "" {
		t.Fatalf("typed row bindings = (%q, %q), want (f, empty)", each.ItemName, each.IndexName)
	}
}

func TestSvelteBlock_ParseEachHeaderIgnoresJSStringDelimiters(t *testing.T) {
	nodes, err := ParseTemplate(`{#each collect(plan, " as ", { closing: "}" }) as feature (feature.id)}<li>{feature}</li>{/each}`)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	each, ok := nodes[0].(*EachBlockNode)
	if !ok {
		t.Fatalf("expected *EachBlockNode, got %T", nodes[0])
	}
	if got, want := each.IterExpr.Source, `collect(plan, " as ", { closing: "}" })`; got != want {
		t.Fatalf("iterable source = %q, want %q", got, want)
	}
	if got, want := each.ItemName, "feature"; got != want {
		t.Fatalf("item binding = %q, want %q", got, want)
	}
	if got, want := each.EachKey, "feature.id"; got != want {
		t.Fatalf("key = %q, want %q", got, want)
	}
}

func TestSvelteBlock_ReadTagIgnoresClosingBraceInJSString(t *testing.T) {
	nodes, err := ParseTemplate(`{#if message === "}"}<p>{message}</p>{/if}`)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	chain, ok := nodes[0].(*IfChainNode)
	if !ok || len(chain.Branches) != 1 {
		t.Fatalf("expected one if branch, got %#v", nodes)
	}
	if got, want := chain.Branches[0].Expr.Source, `message === "}"`; got != want {
		t.Fatalf("condition source = %q, want %q", got, want)
	}
}

func TestSvelteBlock_ReadTagIgnoresClosingBraceInJSComment(t *testing.T) {
	nodes, err := ParseTemplate("{#if /* } */ visible}<p>{visible}</p>{/if}")
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	chain, ok := nodes[0].(*IfChainNode)
	if !ok || len(chain.Branches) != 1 {
		t.Fatalf("expected one if branch, got %#v", nodes)
	}
	if got, want := chain.Branches[0].Expr.Source, "/* } */ visible"; got != want {
		t.Fatalf("condition source = %q, want %q", got, want)
	}
}

func TestSvelteBlock_ParseEachWithIndex(t *testing.T) {
	html := `{#each items as item, i (item.id)}<p>{i}: {item.name}</p>{/each}`
	nodes, err := ParseTemplate(html)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	each := nodes[0].(*EachBlockNode)
	if each.ItemName != "item" || each.IndexName != "i" {
		t.Fatalf("typed row bindings = (%q, %q), want (item, i)", each.ItemName, each.IndexName)
	}
}

func TestSvelteBlock_OldSyntaxIsRejected(t *testing.T) {
	if _, err := ParseTemplate(`<div #if="show">old style</div>`); err == nil || !strings.Contains(err.Error(), "no longer supported") {
		t.Fatalf("legacy syntax error = %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  svelte:fragment slot syntax
// ─────────────────────────────────────────────────────────────────────────────

func TestSvelteFragment_ParsedAsElementWithTag(t *testing.T) {
	// Inside a component, <svelte:fragment slot="footer"> should be parsed
	html := `<div><Modal><svelte:fragment slot="footer"><button>OK</button><button>Cancel</button></svelte:fragment></Modal></div>`
	nodes, err := ParseTemplate(html)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	div := nodes[0].(*ElementNode)
	modal, ok := div.Children[0].(*ComponentNode)
	if !ok {
		t.Fatalf("expected ComponentNode, got %T", div.Children[0])
	}
	footerSlot, exists := modal.Slots["footer"]
	if !exists {
		t.Fatal("'footer' slot not found")
	}
	// The slot should contain the TWO buttons directly (no svelte:fragment wrapper)
	if len(footerSlot) != 2 {
		t.Errorf("footer slot should have 2 children (buttons), got %d", len(footerSlot))
	}
	for i, node := range footerSlot {
		el, ok := node.(*ElementNode)
		if !ok {
			t.Errorf("slot child %d should be ElementNode, got %T", i, node)
		}
		if el != nil && el.Tag != "button" {
			t.Errorf("slot child %d tag=%q, want button", i, el.Tag)
		}
	}
}

func TestSvelteFragment_NoWrapperInDOM(t *testing.T) {
	// The svelte:fragment itself must NOT appear in the slot — only its children
	html := `<Modal><svelte:fragment slot="header"><h1>Title</h1><span>Sub</span></svelte:fragment></Modal>`
	nodes, err := ParseTemplate(html)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	modal := nodes[0].(*ComponentNode)
	header, exists := modal.Slots["header"]
	if !exists {
		t.Fatal("header slot missing")
	}
	for _, n := range header {
		if el, ok := n.(*ElementNode); ok && el.Tag == "svelte:fragment" {
			t.Errorf("svelte:fragment element must not appear in slot — only its children should")
		}
	}
	if len(header) != 2 {
		t.Errorf("expected 2 header slot children, got %d", len(header))
	}
}

func TestSvelteFragment_DefaultSlotUnchanged(t *testing.T) {
	// Content without slot= goes to default slot as before
	html := `<Modal><p>Body text</p></Modal>`
	nodes, err := ParseTemplate(html)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	modal := nodes[0].(*ComponentNode)
	def, exists := modal.Slots["default"]
	if !exists {
		t.Fatal("default slot missing")
	}
	if len(def) != 1 {
		t.Errorf("expected 1 default slot child, got %d", len(def))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Mixed: Svelte blocks inside HTML elements and vice versa
// ─────────────────────────────────────────────────────────────────────────────

func TestSvelteBlock_NestedInsideHtmlElement(t *testing.T) {
	html := `<section>
		<h1>Title</h1>
		{#if items.length > 0}
			<ul>
				{#each items as item (item.id)}
					<li>{item.name}</li>
				{/each}
			</ul>
		{:else}
			<p>No items</p>
		{/if}
	</section>`
	nodes, err := ParseTemplate(html)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	section := nodes[0].(*ElementNode)
	// section children: h1, IfChainNode
	var foundChain, foundH1 bool
	for _, c := range section.Children {
		switch x := c.(type) {
		case *ElementNode:
			if x.Tag == "h1" {
				foundH1 = true
			}
		case *IfChainNode:
			foundChain = true
			if len(x.Branches) != 2 {
				t.Errorf("if chain: expected 2 branches, got %d", len(x.Branches))
			}
			// First branch should have a ul with an EachBlockNode inside
			ifChildren := x.Branches[0].Children
			var foundUl bool
			for _, child := range ifChildren {
				if el, ok := child.(*ElementNode); ok && el.Tag == "ul" {
					foundUl = true
					var foundEach bool
					for _, uc := range el.Children {
						if _, ok := uc.(*EachBlockNode); ok {
							foundEach = true
						}
					}
					if !foundEach {
						t.Error("ul should contain EachBlockNode")
					}
				}
			}
			if !foundUl {
				t.Error("if branch should contain ul")
			}
		}
	}
	if !foundH1 {
		t.Error("section should contain h1")
	}
	if !foundChain {
		t.Error("section should contain IfChainNode")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Regression harness — compile every real .vela template in testdata/ and
//  verify the generated JS meets three structural invariants.  These are the
//  checks that would have caught most bugs fixed over this project's life:
//    1. No %!(EXTRA markers — fmt.Sprintf arg-count slips
//    2. Output is a valid JS program — parses clean via our own parser
//    3. No duplicate top-level `const` declarations — the handler-name
//       collision class of bug from turn 13
// ─────────────────────────────────────────────────────────────────────────────

// compileVelaString runs the same pipeline as CompileFile but on an in-memory
// source string, returning the generated JS.  Test-only; avoids touching disk.
func compileVelaString(t *testing.T, name, raw string) string {
	t.Helper()
	sfc, err := ParseSFC(raw)
	if err != nil {
		t.Fatalf("%s: parse: %v", name, err)
	}
	tmpl, err := ParseTemplate(sfc.Template)
	if err != nil {
		t.Fatalf("%s: template: %v", name, err)
	}
	analysis := AnalyzeScript(sfc.Script)
	return GenerateCodeAST(name, sfc, tmpl, analysis, "vela-reg")
}

// TestRealTemplates_RuntimeMount runs each compiled template inside a real
// JS engine (Node + jsdom) under realistic props and inside a simulated
// _batchRun, asserting no exceptions are thrown.  This catches an entire
// class of bug that source-only tests miss:
//
//  1. TreeWalker invalidation on multi-hole subtrees — the compiled code
//     parses fine but throws TypeError at runtime when nextNode() returns
//     null after a tree mutation.
//  2. Captured refs not visible from setters — let-hoisting omissions
//     cause ReferenceError when a setter fires, but the compiled code
//     itself parses cleanly.
//  3. Off-by-one errors in chain function teardown that only show up
//     when the branch toggles on→off→on.
//
// The static-validation tests (OutputParses, NoEmptyControlFlowGuards) run
// on every machine; this runtime test requires Node + jsdom and SKIPS
// cleanly if either is unavailable.  When it CAN run, it's the strongest
// guarantee available short of a real browser.
//
// Each template gets a "scenario" — a sequence of set() calls inside a
// batch that drives the component through realistic state transitions.
// The scenarios were chosen to activate every {#if} branch in each
// template so unguarded code paths get exercised.
func TestRealTemplates_RuntimeMount(t *testing.T) {
	// Locate jsdom: try VELA_JSDOM_DIR first, then common dev paths.  Skip
	// if no install is reachable — most CI machines won't have jsdom and
	// shouldn't fail on its absence.
	jsdomDir := findJsdomDir(t)
	if jsdomDir == "" {
		t.Skip("jsdom not available — set VELA_JSDOM_DIR to a directory whose node_modules/jsdom is installed")
	}

	// Per-template scenarios.  Each entry is a sequence of set() calls
	// that drive the component through state transitions.  The scenarios
	// activate every {#if} branch in the template so unguarded code paths
	// are exercised by the runtime mount.
	scenarios := map[string]string{
		// App: toggle authenticated → not → authenticated again.
		"App": `[{"user":{"name":"Alice","avatar":"P"},"isAuthenticated":true},{"isAuthenticated":false},{"user":{"name":"Bob","avatar":"L"},"isAuthenticated":true}]`,

		// Card: cycle through showing the title-with-subtitle path, then
		// title-only path, then no-header path.  Hits the inline {#if subtitle}
		// inside the {#if title} chain — the original Card bug location.
		"Card": `[{"title":"Hello","subtitle":"world"},{"subtitle":""},{"title":""},{"title":"Back"}]`,

		// LoginPage: show error, hide error, show again.
		"LoginPage": `[{"error":"Invalid credentials"},{"error":""},{"error":"Locked out"},{"username":"admin","password":"admin"}]`,

		// Modal: open with size variants.
		"Modal": `[{"open":true,"title":"Hello","size":"md"},{"size":"lg"},{"open":false},{"open":true,"title":"Back"}]`,

		// OrdersPage / UsersPage: no specific state needed — the store-driven
		// each blocks don't fire until the store pushes data.
		"OrdersPage": `[{}]`,
		"UsersPage":  `[{}]`,
	}

	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Skip("testdata/ not populated")
	}

	// Materialize a temp directory with: shim modules at /stores/*.js and
	// /components/*.js (matching what the templates import), plus a runner
	// script that mounts each component.
	tmp := t.TempDir()
	if err := writeRuntimeShims(tmp); err != nil {
		t.Fatalf("writing shims: %v", err)
	}

	// Compile each .vela template into the temp dir.  Keep nested layout
	// the templates expect (App at root, OrdersPage under pages/, etc.).
	templates := map[string]string{} // basename → file path
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".vela") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".vela")
		data, err := os.ReadFile(filepath.Join("testdata", e.Name()))
		if err != nil {
			t.Errorf("%s: read: %v", e.Name(), err)
			continue
		}
		js := compileVelaString(t, name, string(data))
		// OrdersPage and UsersPage import from "../stores" so they need
		// to live under a subdir; everything else at root.
		var dst string
		if name == "OrdersPage" || name == "UsersPage" {
			os.MkdirAll(filepath.Join(tmp, "pages"), 0755)
			dst = filepath.Join(tmp, "pages", name+".js")
		} else {
			dst = filepath.Join(tmp, name+".js")
		}
		if err := os.WriteFile(dst, []byte(js), 0644); err != nil {
			t.Fatalf("%s: write: %v", name, err)
		}
		templates[name] = dst
	}

	// Write the runner inside jsdomDir so its `import 'jsdom'` resolves via
	// Node's normal node_modules lookup (which walks up from the script's
	// location, not from CWD).  Use a unique name to avoid collisions when
	// tests run in parallel; clean up at end.
	runnerName := fmt.Sprintf("vela_runtime_runner_%d.mjs", os.Getpid())
	runnerPath := filepath.Join(jsdomDir, runnerName)
	if err := os.WriteFile(runnerPath, []byte(runtimeRunnerScript), 0644); err != nil {
		t.Fatalf("writing runner: %v", err)
	}
	defer os.Remove(runnerPath)

	// Build the input: one line per template — "name|path|scenarioJSON".
	var inputLines []string
	for name, path := range templates {
		scen, ok := scenarios[name]
		if !ok {
			scen = "[{}]"
		}
		inputLines = append(inputLines, fmt.Sprintf("%s|%s|%s", name, path, scen))
	}
	sort.Strings(inputLines) // deterministic order for log readability

	// Run the runner with CWD = jsdomDir so its `import 'jsdom'` resolves.
	cmd := exec.Command("node", runnerPath)
	cmd.Dir = jsdomDir
	cmd.Stdin = strings.NewReader(strings.Join(inputLines, "\n") + "\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Logf("runner output:\n%s", out)
		t.Fatalf("runner failed: %v", err)
	}

	// Parse output: lines beginning with OK or FAIL.
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "FAIL ") {
			t.Errorf("runtime mount: %s", strings.TrimPrefix(line, "FAIL "))
		} else if !strings.HasPrefix(line, "OK ") {
			// Unexpected line — log for visibility but don't fail the test
			// (could be a benign warning from jsdom).
			t.Logf("runner: %s", line)
		}
	}
}

// findJsdomDir returns a directory whose node_modules/jsdom is installed,
// or "" if none is reachable.  Honors VELA_JSDOM_DIR.
func findJsdomDir(t *testing.T) string {
	if v := os.Getenv("VELA_JSDOM_DIR"); v != "" {
		if _, err := os.Stat(filepath.Join(v, "node_modules", "jsdom")); err == nil {
			return v
		}
		t.Logf("VELA_JSDOM_DIR=%s but no jsdom found there", v)
	}
	// Common dev paths to probe.
	candidates := []string{
		"/home/claude/svelte-compare", // this project's sibling
		filepath.Join(os.Getenv("HOME"), "svelte-compare"),
	}
	for _, dir := range candidates {
		if _, err := os.Stat(filepath.Join(dir, "node_modules", "jsdom")); err == nil {
			return dir
		}
	}
	return ""
}

// writeRuntimeShims materializes the shim modules each template imports.
// These mimic the surface area of the real stores (subscribe + various
// methods) without actually doing anything reactive.  The components mount
// and run their wiring; we don't care about real reactivity for this test.
func writeRuntimeShims(dir string) error {
	mkdir := func(p string) { os.MkdirAll(p, 0755) }
	write := func(p, body string) error {
		return os.WriteFile(p, []byte(body), 0644)
	}
	mkdir(filepath.Join(dir, "stores"))
	mkdir(filepath.Join(dir, "components"))

	if err := write(filepath.Join(dir, "stores", "auth.js"), `
export const auth = { subscribe: () => () => {}, user: null };
export const logout = () => {};
export const login = () => Promise.resolve();
`); err != nil {
		return err
	}
	if err := write(filepath.Join(dir, "stores", "data.js"), `
export const settings = { subscribe: () => () => {} };
export const orders   = { subscribe: () => () => {} };
export const users    = { subscribe: () => () => {} };
export const deleteOrder = () => {};
export const updateOrderStatus = () => {};
export const deleteUser = () => {};
export const ROLES = ['admin', 'editor', 'viewer'];
`); err != nil {
		return err
	}
	// Generic component shim: callable as Component(target, props, slots),
	// returns { destroy, set } compatible with what the real components do.
	componentShim := `
export default function (target, props, slots) {
  const el = document.createElement('div');
  target.appendChild(el);
  return { el, destroy: () => el.remove(), set: () => {} };
};
`
	for _, name := range []string{"Card", "Badge", "Modal", "Toast"} {
		// Named-export shim: matches the real components' export shape.
		body := fmt.Sprintf(`
export const %s = (target, props, slots) => {
  const el = document.createElement('div');
  target.appendChild(el);
  return { el, destroy: () => el.remove(), set: () => {} };
};
`, name)
		if err := write(filepath.Join(dir, "components", name+".js"), body); err != nil {
			return err
		}
	}
	// Top-level page shims for App's imports.
	for _, name := range []string{"LoginPage", "Dashboard"} {
		body := fmt.Sprintf(`
export const %s = (target, props, slots) => {
  const el = document.createElement('div');
  target.appendChild(el);
  return { el, destroy: () => el.remove(), set: () => {} };
};
`, name)
		if err := write(filepath.Join(dir, name+".js"), body); err != nil {
			return err
		}
	}
	_ = componentShim // reserved for future use
	return nil
}

// runtimeRunnerScript is the Node runner.  Reads "name|path|scenarioJSON"
// lines from stdin; for each, sets up jsdom + a simulated _batchRun, mounts
// the component, drives it through the scenario, and reports OK or FAIL.
const runtimeRunnerScript = `
import { JSDOM } from 'jsdom';
import { pathToFileURL } from 'url';
import * as readline from 'readline';

const dom = new JSDOM('<!doctype html><body></body>', { runScripts: 'outside-only' });
globalThis.document    = dom.window.document;
globalThis.window      = dom.window;
globalThis.CustomEvent = dom.window.CustomEvent;
globalThis.Node        = dom.window.Node;
globalThis.NodeFilter  = dom.window.NodeFilter;
globalThis.performance = { now: () => Date.now() };

// Simulated runtime _batchRun: defer DOM-touching work until the batch
// completes, mirroring the real runtime's semantics.  Components call
// $$defer for setter-triggered updates; without batching, intermediate
// states (e.g. isAuthenticated=true while user is still null) would
// crash chain functions that read both.
const _B = Symbol.for('vela.batch');
globalThis[_B] = { depth: 0, queue: [] };
globalThis[Symbol.for('vela.defer')] = (fn) => {
  const b = globalThis[_B];
  if (b.depth > 0) b.queue.push(fn);
  else fn();
};
// Register $$tmpl so compiled code uses the runtime path (not the inline
// fallback).  Mirrors the real runtime exactly — see runtime.js.  Testing
// the runtime helper means future changes that subtly break the bundled
// behavior (e.g. wrong NodeFilter constant, off-by-one anchor index) get
// caught here even though the fallback would still work.
globalThis[Symbol.for('vela.tmpl')] = (parent, html, n) => {
  parent.innerHTML = html;
  const w = document.createTreeWalker(parent, 128);
  const r = new Array(n);
  for (let i = 0; i < n; i++) r[i] = w.nextNode();
  return r;
};
function batchRun(fn) {
  const b = globalThis[_B];
  b.depth++;
  try { fn(); }
  finally {
    if (--b.depth === 0 && b.queue.length) {
      const q = b.queue.splice(0);
      for (const f of q) f();
    }
  }
}

const rl = readline.createInterface({ input: process.stdin });
const lines = [];
for await (const line of rl) lines.push(line);

for (const line of lines) {
  if (!line.trim()) continue;
  const [name, path, scenarioJSON] = line.split('|');
  let scenario;
  try { scenario = JSON.parse(scenarioJSON); }
  catch (e) { console.log('FAIL ' + name + ': bad scenario JSON: ' + e.message); continue; }

  try {
    const mod = await import(pathToFileURL(path).href);
    const Comp = mod[name] || mod.default;
    if (typeof Comp !== 'function') {
      console.log('FAIL ' + name + ': no exported component named "' + name + '"');
      continue;
    }
    const target = dom.window.document.createElement('div');
    dom.window.document.body.appendChild(target);
    const inst = Comp(target, {}, {});
    // Drive through the scenario steps inside batched updates.
    for (const step of scenario) {
      batchRun(() => inst.set(step));
    }
    // Tear down.
    inst.destroy();
    target.remove();
    console.log('OK ' + name);
  } catch (e) {
    const msg = (e && e.message) ? e.message : String(e);
    const stack = (e && e.stack) ? e.stack.split('\\n').slice(0, 3).join(' | ') : '';
    console.log('FAIL ' + name + ': ' + msg + (stack ? ' [' + stack + ']' : ''));
  }
}
`

// TestRealTemplates_NoEmptyControlFlowGuards scans every generated output
// for accidentally-empty control-flow conditions: `if ()`, `while ()`,
// `else if ()`.  These are syntax errors in real JavaScript engines but
// our internal JSParser is too permissive to catch them — it accepts
// empty parenthesized expressions and skips on.
//
// The bug class: when a code-generation site formats `if (%s) ...` with
// an empty branchVar, the result is `if () ...`, which crashes the
// browser at module-load time with `SyntaxError: expected expression,
// got ')'`.  This regression caught a real shipping issue in Card.vela:
// inline-if blocks (raw `if (cond) { ... }` emission for nested if-chains)
// pass an empty branchVar to the Step 3 emitter, which then formatted it
// into the update guard.
//
// Pinning the symptom rather than just the root cause means future
// emission paths can't reintroduce the same shape silently.
func TestRealTemplates_NoEmptyControlFlowGuards(t *testing.T) {
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Skip("testdata/ not populated")
	}
	// These patterns must NEVER appear in generated JS.  Each is a
	// browser-rejected syntax that our internal parser accepts.
	badPatterns := []string{
		"if ()",
		"if()",
		"} else if ()",
		"} else if()",
		"while ()",
		"while()",
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".vela") {
			continue
		}
		path := filepath.Join("testdata", e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: read: %v", e.Name(), err)
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".vela")
		code := compileVelaString(t, name, string(data))
		for _, bad := range badPatterns {
			if strings.Contains(code, bad) {
				// Report enough context to make the bug obvious.
				idx := strings.Index(code, bad)
				start := idx - 60
				if start < 0 {
					start = 0
				}
				end := idx + len(bad) + 60
				if end > len(code) {
					end = len(code)
				}
				t.Errorf("%s: emitted %q (browser-rejected syntax).\n  context: ...%s...",
					e.Name(), bad, code[start:end])
			}
		}
	}
}

// TestRealTemplates_NoSprintfLeakage verifies that no generated output
// contains `%!(EXTRA ` — the format-string footprint of a Sprintf call
// with wrong arg count.  These historically produced outright JS syntax
// errors at runtime (turn 1: nine separate bugs).
func TestRealTemplates_NoSprintfLeakage(t *testing.T) {
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Skip("testdata/ not populated; skipping real-template regression sweep")
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".vela") {
			continue
		}
		path := filepath.Join("testdata", e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: read: %v", e.Name(), err)
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".vela")
		code := compileVelaString(t, name, string(data))
		if strings.Contains(code, "%!(EXTRA") {
			// Find the first offending line for a useful error message.
			idx := strings.Index(code, "%!(EXTRA")
			start := strings.LastIndexByte(code[:idx], '\n') + 1
			end := idx + strings.IndexByte(code[idx:], '\n')
			if end < idx {
				end = len(code)
			}
			t.Errorf("%s: Sprintf arg-count leakage in output:\n    %s",
				e.Name(), code[start:end])
		}
	}
}

// TestRealTemplates_OutputParses verifies that each template's compiled JS
// parses cleanly via our own JS parser.  ParseJS collects errors as it goes
// rather than panicking, so we inspect the returned errors list.  This is
// the single strongest structural check — it catches the entire class of
// "generator emits malformed JS" bugs.
func TestRealTemplates_OutputParses(t *testing.T) {
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Skip("testdata/ not populated")
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".vela") {
			continue
		}
		path := filepath.Join("testdata", e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: read: %v", e.Name(), err)
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".vela")
		code := compileVelaString(t, name, string(data))
		p := parsedJS{Errors: jsParseErrors(code)}
		if len(p.Errors) > 0 {
			t.Errorf("%s: generated JS does not parse cleanly (%d errors)\n  first: %s",
				e.Name(), len(p.Errors), p.Errors[0])
		}
	}
}

// TestRealTemplates_NoDuplicateConsts scans each generated component's
// TOP-LEVEL const bindings (depth 1 — inside the `export function App(...)`
// body but outside any IIFE or nested function) and ensures no name is
// declared twice.  This targets the handler-name collision class (turn 13)
// where two code paths both emit `const $$nX_input_h = ...` into the same
// component body — a hard SyntaxError at JS load time.
//
// We intentionally do NOT check nested depths: two forEach-IIFE bodies
// that each declare a local `const $$c = ...` live in separate scopes but
// happen to share the same brace depth, and distinguishing them requires
// a real scope tree.  The turn-13 bug happened at top-level, and that's
// where this cheap check is precise enough to be useful.
func TestRealTemplates_NoDuplicateConsts(t *testing.T) {
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Skip("testdata/ not populated")
	}
	// JS identifier characters are [A-Za-z0-9_$].  Without including `$` the
	// regex would parse `$$tmpl_$$n9` as just `$$tmpl_` — capturing only the
	// prefix, causing false duplicates when names share that prefix and
	// missing real duplicates that happen to differ only after a $.
	constRe := regexp.MustCompile(`\bconst\s+(\$\$[A-Za-z0-9_$]+)\b`)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".vela") {
			continue
		}
		path := filepath.Join("testdata", e.Name())
		data, _ := os.ReadFile(path)
		name := strings.TrimSuffix(e.Name(), ".vela")
		code := compileVelaString(t, name, string(data))

		matches := constRe.FindAllStringSubmatchIndex(code, -1)
		seen := map[string]int{}
		for _, m := range matches {
			nm := code[m[2]:m[3]]
			// Only flag duplicates at the component function's top level
			// (depth 1): immediately inside `export function App(...) { ... }`.
			if braceDepthAt(code, m[0]) != 1 {
				continue
			}
			seen[nm]++
		}
		for nm, n := range seen {
			if n > 1 {
				t.Errorf("%s: duplicate top-level `const %s` declaration (%d copies) — would be a JS SyntaxError at load time",
					e.Name(), nm, n)
			}
		}
	}
}

// braceDepthAt counts unmatched `{` minus `}` from the start of src up to
// position pos, skipping braces inside strings and comments.  Used by
// TestRealTemplates_NoDuplicateConsts to distinguish nested vs top-level
// scopes without building a full scope tree.
func braceDepthAt(src string, pos int) int {
	if pos > len(src) {
		pos = len(src)
	}
	depth := 0
	i := 0
	for i < pos {
		c := src[i]
		switch c {
		case '{':
			depth++
			i++
		case '}':
			depth--
			i++
		case '"', '\'', '`':
			q := c
			i++
			for i < pos {
				if src[i] == '\\' && i+1 < pos {
					i += 2
					continue
				}
				if src[i] == q {
					i++
					break
				}
				i++
			}
		case '/':
			if i+1 < pos && src[i+1] == '/' {
				for i < pos && src[i] != '\n' {
					i++
				}
			} else if i+1 < pos && src[i+1] == '*' {
				i += 2
				for i+1 < pos && !(src[i] == '*' && src[i+1] == '/') {
					i++
				}
				i += 2
			} else {
				i++
			}
		default:
			i++
		}
	}
	return depth
}

// ─────────────────────────────────────────────────────────────────────────────
//  Targeted unit tests for features added during the compiler's recent
//  fix sessions — A4 (destructuring LHS), A7 (AST-based imports), A8
//  (scope-aware item-ref rewriter), and turn-13's two-way binding.
//
//  These pin the contract of each feature so future refactors can't
//  silently regress them.  The coverage report before these tests showed
//  0% on rewritePatternAssign, emitTwoWayListener, twoWayBindSpec, and
//  RewriteScriptImports — i.e. they shipped with no tests at all.
// ─────────────────────────────────────────────────────────────────────────────

// ── A4: RewriteAssignments destructuring-LHS support ──────────────────────

// ── A8: rewriteItemRef scope tracking ──────────────────────────────────────

// ── Two-way binding (turn 13): emitTwoWayListener + twoWayBindSpec ─────────

// ── A7: AST-based imports ──────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────────────────────
//  Step 3 predicates and template-with-holes builder.
//
//  These tests pin behavioral intent rather than emission shape — the regression
//  harness already verifies that real templates compile to valid JS, but doesn't
//  catch silent definitional changes (e.g. a future refactor that subtly changes
//  what counts as "fully static").  Each test below names ONE behavioral
//  commitment and constructs the minimal fixture that can verify it.
// ─────────────────────────────────────────────────────────────────────────────

func TestValidateDerivedCycles(t *testing.T) {
	src := `
		let first = $derived(second + 1);
		let second = $derived(first + 1);
	`
	analysis := AnalyzeScript(src)
	if len(analysis.DerivedCycles) != 1 {
		t.Fatalf("cycles = %+v, want one cycle", analysis.DerivedCycles)
	}
	ds := NewDiagSet("", src)
	ValidateDerivedCycles(ParseJS(src), analysis, ds)
	if len(ds.Errors()) != 1 || !strings.Contains(ds.Errors()[0].Message, "cyclic $derived dependencies") {
		t.Fatalf("cycle diagnostic = %+v", ds.All())
	}
}

func TestTemplateExpressionsKeepSourcePositions(t *testing.T) {

	parts := ParseInterpolationsAt(`{value} {value}`, 10)
	if len(parts) != 3 || parts[0].Pos != 11 || parts[2].Pos != 19 {
		t.Fatalf("interpolation positions = %+v", parts)
	}
}

// parsedJS carries the recoverable parser messages of generated code.
type parsedJS struct{ Errors []string }

// jsParseErrors parses code with the public parser API and returns its
// recoverable error messages.
func jsParseErrors(code string) []string {
	_, diagnostics := ParseJSWithDiagnostics(code)
	errs := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		errs = append(errs, diagnostic.Message)
	}
	return errs
}
