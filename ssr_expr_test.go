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
		"props.Active && props.Count > 0",
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

func TestTranslateSSRExpressionLowersTernaryThroughRuntime(t *testing.T) {
	sfc, err := parseSFC(`<script>export let count = 0;</script><template>{count ? "many" : "none"}</template>`)
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
	if err != nil || got != `vela.Ternary(vela.Truthy(props.Count), "many", "none")` {
		t.Fatalf("ternary translation=%q, err=%v", got, err)
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
	if want := `props.Profile["name"]`; got != want {
		t.Fatalf("member output=%q want %q", got, want)
	}
}
