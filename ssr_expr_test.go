package main

import (
	"strings"
	"testing"
)

func TestTranslateSSRExpressionUsesCachedAST(t *testing.T) {
	sfc, err := parseSFC(`<script>
export let title = "";
export let count = 0;
export let active = false;
export let items = [];
</script><template>{title + "!"}{items.length}{items[0]}{active && count > 0}</template>`)
	if err != nil {
		t.Fatalf("parseSFC: %v", err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatalf("BuildSSRRenderPlan: %v", err)
	}
	text := sfc.TemplateAST.Nodes[0].(*TextNode)
	wants := []string{
		`props.Title + "!"`,
		"vela.Length(props.Items)",
		"props.Items[0]",
		"vela.AndLazy(func() any {\n\treturn props.Active\n}, func() any {\n\treturn props.Count > 0\n})",
	}
	for i, want := range wants {
		expr, err := TranslateSSRExpression(text.Parts[i].Expr, NewSSRExprContext(plan))
		if err != nil {
			t.Fatalf("TranslateSSRExpression part %d: %v", i, err)
		}
		got, err := FormatSSRGoExpr(expr)
		if err != nil {
			t.Fatalf("FormatSSRGoExpr part %d: %v", i, err)
		}
		if got != want {
			t.Errorf("part %d = %q, want %q", i, got, want)
		}
	}
}

func TestTranslateSSRExpressionLowersNullToGoNil(t *testing.T) {
	sfc, err := parseSFC(`<template>{null}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	translated, err := TranslateSSRExpression(expr, SSRExprContext{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := FormatSSRGoExpr(translated)
	if err != nil || got != "nil" {
		t.Fatalf("null translation=%q, err=%v", got, err)
	}
}

func TestTranslateSSRExpressionReportsPositionedUnsupportedForm(t *testing.T) {
	sfc, err := parseSFC(`<template>{format(title)}</template>`)
	if err != nil {
		t.Fatalf("parseSFC: %v", err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatalf("BuildSSRRenderPlan: %v", err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	_, err = TranslateSSRExpression(expr, NewSSRExprContext(plan))
	problem, ok := err.(*SSRExpressionError)
	if !ok {
		t.Fatalf("error = %T %v, want *SSRExpressionError", err, err)
	}
	if problem.Pos != expr.Pos || problem.Error()[:len(SSRExpressionDiagnosticCode)] != SSRExpressionDiagnosticCode {
		t.Fatalf("problem = %#v", problem)
	}
}

func TestTranslateSSRExpressionExplainsLocalReactiveValue(t *testing.T) {
	sfc, err := parseSFC(`<script>let profile = $state.deep({});</script><template>{profile}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	_, err = TranslateSSRExpression(expr, NewSSRExprContext(plan))
	problem, ok := err.(*SSRExpressionError)
	if !ok || problem.Pos != expr.Pos || !strings.Contains(problem.Error(), "local reactive binding \"profile\"") {
		t.Fatalf("error=%T %v", err, err)
	}
}

func TestTranslateSSRExpressionLowersSafeLocalStateInitializer(t *testing.T) {
	sfc, err := parseSFC(`<script>let count = $state(1 + 1);</script><template>{count}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	translated, err := TranslateSSRExpression(expr, NewSSRExprContext(plan))
	if err != nil {
		t.Fatal(err)
	}
	got, err := FormatSSRGoExpr(translated)
	if err != nil || got != "1 + 1" {
		t.Fatalf("state initializer translation=%q, err=%v", got, err)
	}
}

func TestTranslateSSRExpressionLowersSafeLocalDerivedInitializer(t *testing.T) {
	sfc, err := parseSFC(`<script>let count = $state(2); let doubled = $derived(count * 2);</script><template>{doubled}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	translated, err := TranslateSSRExpression(expr, NewSSRExprContext(plan))
	if err != nil {
		t.Fatal(err)
	}
	got, err := FormatSSRGoExpr(translated)
	if err != nil || got != "2 * 2" {
		t.Fatalf("derived initializer translation=%q, err=%v", got, err)
	}
}

func TestTranslateSSRExpressionRejectsCircularLocalDerivedInitializer(t *testing.T) {
	sfc, err := parseSFC(`<script>let first = $derived(second); let second = $derived(first);</script><template>{first}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	_, err = TranslateSSRExpression(expr, NewSSRExprContext(plan))
	if err == nil || !strings.Contains(err.Error(), "circular server initializer") {
		t.Fatalf("circular derived error=%v", err)
	}
}

func TestTranslateSSRExpressionLowersTernaryThroughRuntime(t *testing.T) {
	sfc, err := parseSFC(`<script>export let count = 0;</script><template>{count ? 'many' : 'none'}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	translated, err := TranslateSSRExpression(expr, NewSSRExprContext(plan))
	if err != nil {
		t.Fatal(err)
	}
	got, err := FormatSSRGoExpr(translated)
	want := `vela.TernaryLazy(func() any {
	return props.Count
}, func() any {
	return "many"
}, func() any {
	return "none"
})`
	if err != nil || got != want {
		t.Fatalf("ternary translation=%q, err=%v", got, err)
	}
}

func TestTranslateSSRExpressionLowersLogicalOperandsThroughRuntime(t *testing.T) {
	sfc, err := parseSFC(`<script>export let title = ''; export let active = false;</script><template>{title || 'Untitled'}{active && 'active'}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatal(err)
	}
	text := sfc.TemplateAST.Nodes[0].(*TextNode)
	wants := []string{
		`vela.OrLazy(func() any {
	return props.Title
}, func() any {
	return "Untitled"
})`,
		`vela.AndLazy(func() any {
	return props.Active
}, func() any {
	return "active"
})`,
	}
	for index, want := range wants {
		translated, err := TranslateSSRExpression(text.Parts[index].Expr, NewSSRExprContext(plan))
		if err != nil {
			t.Fatal(err)
		}
		got, err := FormatSSRGoExpr(translated)
		if err != nil || got != want {
			t.Fatalf("logical expression %d=%q, err=%v", index, got, err)
		}
	}
}

func TestTranslateSSRExpressionDecodesSingleQuotedString(t *testing.T) {
	sfc, err := parseSFC(`<template>{'it\'s\nready'}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	translated, err := TranslateSSRExpression(expr, SSRExprContext{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := FormatSSRGoExpr(translated)
	if err != nil || got != `"it's\nready"` {
		t.Fatalf("single quoted translation=%q, err=%v", got, err)
	}
}

func TestTranslateSSRExpressionLowersStaticTemplateLiteral(t *testing.T) {
	sfc, err := parseSFC("<template>{`ready\\n\\`cost: \\$5`}</template>")
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	translated, err := TranslateSSRExpression(expr, SSRExprContext{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := FormatSSRGoExpr(translated)
	if err != nil || got != "\"ready\\n`cost: $5\"" {
		t.Fatalf("static template translation=%q, err=%v", got, err)
	}
}

func TestTranslateSSRExpressionLowersTypedInterpolatedTemplateLiteral(t *testing.T) {
	sfc, err := parseSFC("<script>export let title = ''; </script><template>{`hello ${title}!`}</template>")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	translated, err := TranslateSSRExpression(expr, NewSSRExprContext(plan))
	if err != nil {
		t.Fatal(err)
	}
	got, err := FormatSSRGoExpr(translated)
	if err != nil || got != `vela.TemplateString("hello ", props.Title, "!")` {
		t.Fatalf("interpolated template translation=%q, err=%v", got, err)
	}
}

func TestTranslateSSRExpressionLowersNullishInterpolatedTemplateLiteral(t *testing.T) {
	sfc, err := parseSFC("<script>export let value; </script><template>{`hello ${value}`}</template>")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	translated, err := TranslateSSRExpression(expr, NewSSRExprContext(plan))
	if err != nil {
		t.Fatal(err)
	}
	got, err := FormatSSRGoExpr(translated)
	if err != nil || got != `vela.TemplateString("hello ", props.Value, "")` {
		t.Fatalf("nullish template translation=%q, err=%v", got, err)
	}
}

func TestTranslateSSRExpressionLowersUndefinedAndMissingObjectTemplateValue(t *testing.T) {
	sfc, err := parseSFC("<script>export let profile = {}; </script><template>{`u=${undefined}; m=${profile.missing}`}</template>")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	translated, err := TranslateSSRExpression(expr, NewSSRExprContext(plan))
	if err != nil {
		t.Fatal(err)
	}
	got, err := FormatSSRGoExpr(translated)
	want := `vela.TemplateString("u=", vela.Undefined(), "; m=", vela.Get(props.Profile, "missing"), "")`
	if err != nil || got != want {
		t.Fatalf("undefined template translation=%q, err=%v", got, err)
	}
}

func TestTranslateSSRExpressionLowersArrayLiteralAndHole(t *testing.T) {
	sfc, err := parseSFC(`<template>{[1, , 'three']}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	translated, err := TranslateSSRExpression(expr, SSRExprContext{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := FormatSSRGoExpr(translated)
	if err != nil || got != `[]any{1, nil, "three"}` {
		t.Fatalf("array literal translation=%q, err=%v", got, err)
	}
}

func TestTranslateSSRExpressionRejectsArraySpread(t *testing.T) {
	sfc, err := parseSFC(`<script>export let items = [];</script><template>{[1, ...items]}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	_, err = TranslateSSRExpression(expr, NewSSRExprContext(plan))
	if err == nil || !strings.Contains(err.Error(), "array spread") {
		t.Fatalf("spread error=%v", err)
	}
}

func TestTranslateSSRExpressionLowersPlainObjectLiteral(t *testing.T) {
	sfc, err := parseSFC(`<template>{ { title: 'ready', selected: true } }</template>`)
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	translated, err := TranslateSSRExpression(expr, SSRExprContext{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := FormatSSRGoExpr(translated)
	if err != nil || got != `map[string]any{"title": "ready", "selected": true}` {
		t.Fatalf("object literal translation=%q, err=%v", got, err)
	}
}

func TestTranslateSSRExpressionKeepsLastDuplicateObjectProperty(t *testing.T) {
	sfc, err := parseSFC(`<template>{ { title: 'old', title: 'new' } }</template>`)
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	translated, err := TranslateSSRExpression(expr, SSRExprContext{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := FormatSSRGoExpr(translated)
	if err != nil || got != `map[string]any{"title": "new"}` {
		t.Fatalf("duplicate object literal translation=%q, err=%v", got, err)
	}
}

func TestTranslateSSRExpressionRejectsUnsupportedObjectForms(t *testing.T) {
	for _, source := range []string{
		`<template>{ { ...{} } }</template>`,
		`<script>export let key = '';</script><template>{ { [key]: 1 } }</template>`,
		`<template>{ { __proto__: null } }</template>`,
	} {
		sfc, err := parseSFC(source)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
		if err != nil {
			t.Fatal(err)
		}
		expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
		if _, err := TranslateSSRExpression(expr, NewSSRExprContext(plan)); err == nil {
			t.Fatalf("source %q unexpectedly translated", source)
		}
	}
}

func TestTranslateSSRExpressionLowersObjectPropMember(t *testing.T) {
	sfc, err := parseSFC(`<script>export let profile = {};</script><template>{profile.name}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatal(err)
	}
	text := sfc.TemplateAST.Nodes[0].(*TextNode)
	expr, err := TranslateSSRExpression(text.Parts[0].Expr, NewSSRExprContext(plan))
	if err != nil {
		t.Fatal(err)
	}
	got, err := FormatSSRGoExpr(expr)
	if err != nil {
		t.Fatal(err)
	}
	if want := `vela.Get(props.Profile, "name")`; got != want {
		t.Fatalf("member output=%q want %q", got, want)
	}
}
