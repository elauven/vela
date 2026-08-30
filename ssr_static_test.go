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
