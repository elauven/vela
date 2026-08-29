package main

import (
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
		"len(props.Items)",
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
