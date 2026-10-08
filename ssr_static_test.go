package main

import (
	"strings"
	"testing"
)

func TestBuildSSRGoFileRendersArrayLiteralWithJSJoinSemantics(t *testing.T) {
	sfc, err := parseSFC(`<template><p>{[1, , 3]}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	source, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), `vela.EscapeString(vela.String([]any{1.0, vela.Hole(), 3.0}))`) {
		t.Fatalf("array literal SSR output is not lowered through Vela conversion:\n%s", source)
	}
	if err := ValidateGeneratedSSRGo("Page.go", source); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSSRGoFileRendersPlainObjectLiteral(t *testing.T) {
	sfc, err := parseSFC(`<template><p>{ { title: 'ready' } }</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	source, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), `vela.EscapeString(vela.String(vela.NewObject().Set("title", "ready")))`) {
		t.Fatalf("object literal SSR output is not lowered through Vela conversion:\n%s", source)
	}
	if err := ValidateGeneratedSSRGo("Page.go", source); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSSRGoFileRendersSafeDerivedValue(t *testing.T) {
	sfc, err := parseSFC(`<script>let count = $state(2); let doubled = $derived(count * 2);</script><template><p>{doubled}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	source, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	// The derived value is evaluated once in declaration order (T-6.6d).
	for _, want := range []string{`ssrL_count := 2.0`, `ssrL_doubled := vela.NumberMultiply(ssrL_count, 2.0)`, `vela.EscapeString(vela.String(ssrL_doubled))`} {
		if !strings.Contains(string(source), want) {
			t.Fatalf("derived SSR output misses %q:\n%s", want, source)
		}
	}
	if err := ValidateGeneratedSSRGo("Page.go", source); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSSRGoFileUsesJSTruthinessForTernaryIfCondition(t *testing.T) {
	sfc, err := parseSFC(`<script>export let count = 0;</script><template>{#if count ? 'ready' : ''}<p>ready</p>{:else}<p>empty</p>{/if}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	source, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	want := `if vela.Truthy(vela.String(vela.TernaryLazy(func() any {`
	if !strings.Contains(string(source), want) {
		t.Fatalf("ternary if condition is not lowered through JS truthiness:\n%s", source)
	}
	if err := ValidateGeneratedSSRGo("Page.go", source); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSSRGoFileRendersStaticTemplateLiteral(t *testing.T) {
	sfc, err := parseSFC("<template><p>{`ready\\nnow`}</p></template>")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	source, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), `vela.EscapeString(vela.String("ready\nnow"))`) {
		t.Fatalf("static template literal is not lowered through SSR text conversion:\n%s", source)
	}
	if err := ValidateGeneratedSSRGo("Page.go", source); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSSRGoFileRendersTypedInterpolatedTemplateLiteral(t *testing.T) {
	sfc, err := parseSFC("<script>export let title = ''; </script><template><p>{`hello ${title}!`}</p></template>")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	source, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), `vela.EscapeString(vela.String(vela.TemplateString("hello ", props.Title, "!")))`) {
		t.Fatalf("interpolated template literal is not lowered through SSR conversion:\n%s", source)
	}
	if err := ValidateGeneratedSSRGo("Page.go", source); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSSRGoFileRendersUndefinedTemplateInterpolation(t *testing.T) {
	sfc, err := parseSFC("<template><p>{`value=${undefined}`}</p></template>")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	source, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), `vela.TemplateString("value=", vela.Undefined(), "")`) {
		t.Fatalf("undefined template interpolation is not lowered through its SSR sentinel:\n%s", source)
	}
	if err := ValidateGeneratedSSRGo("Page.go", source); err != nil {
		t.Fatal(err)
	}
}

func TestBuildSSRGoFileLowersLogicalOperandsWithJSResults(t *testing.T) {
	sfc, err := parseSFC(`<script>export let title = '';</script><template><p>{title || 'Untitled'}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	source, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), `vela.EscapeString(vela.String(vela.OrLazy(func() any {`) {
		t.Fatalf("logical SSR expression is not lowered through JS operand semantics:\n%s", source)
	}
	if err := ValidateGeneratedSSRGo("Page.go", source); err != nil {
		t.Fatal(err)
	}
}
