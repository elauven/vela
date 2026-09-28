package main

import (
	"strings"
	"testing"
)

func TestBuildStaticSSRGoFile(t *testing.T) {
	sfc, err := parseSFC(`<template><main class="app"><h1>Hello</h1><br /></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildStaticSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	src, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "vela.WriteString(w,") || !strings.Contains(string(src), "<main class") || !strings.Contains(string(src), "<br></main>") {
		t.Fatalf("unexpected output:\n%s", src)
	}
	if err := ValidateGeneratedSSRGo("Page.go", src); err != nil {
		t.Fatal(err)
	}
}
func TestBuildStaticSSRGoFileRejectsDynamic(t *testing.T) {
	sfc, _ := parseSFC(`<script>export let title="";</script><template>{title}</template>`)
	plan, _ := BuildSSRRenderPlan("Page", sfc, "vela-page")
	_, err := BuildStaticSSRGoFile(plan, "components")
	if err == nil || !strings.Contains(err.Error(), SSRExpressionDiagnosticCode) {
		t.Fatalf("err=%v", err)
	}
}

func TestBuildStaticSSRGoFileWithTextPropsEscapesValue(t *testing.T) {
	sfc, _ := parseSFC(`<script>export let title="";</script><template>Hello {title}</template>`)
	plan, _ := BuildSSRRenderPlan("Page", sfc, "vela-page")
	file, err := BuildStaticSSRGoFileWithTextProps(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	src, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "vela.EscapeString(vela.String(props.Title))") {
		t.Fatalf("missing escaped prop write:\n%s", src)
	}
	if err := ValidateGeneratedSSRGo("Page.go", src); err != nil {
		t.Fatal(err)
	}
}

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
	if !strings.Contains(string(source), `vela.EscapeString(vela.String([]any{1, nil, 3}))`) {
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
	if !strings.Contains(string(source), `vela.EscapeString(vela.String(map[string]any{"title": "ready"}))`) {
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
	if !strings.Contains(string(source), `vela.EscapeString(vela.String(2*2))`) {
		t.Fatalf("derived SSR output is not lowered from cached AST:\n%s", source)
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
	want := `if vela.Truthy(vela.Ternary(vela.Truthy(props.Count), "ready", "")) {`
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
