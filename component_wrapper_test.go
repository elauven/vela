package main

import (
	"strings"
	"testing"
)

// A component invocation is mounted into a wrapper <div>.  That
// wrapper must be display:contents so it generates no layout box —
// otherwise it silently breaks the parent's flex/grid layout and
// any spacing that assumed the component's root was a direct child.
// Regression: components used to render into a bare <div> wrapper.
func compileComponentParent(t *testing.T, astPath bool) string {
	t.Helper()
	src := `<script>
  import Child from './Child.vela';
</script>
<template><section class="row"><Child /></section></template>`
	sfc, err := parseSFC(src)
	if err != nil {
		t.Fatalf("parseSFC: %v", err)
	}
	tmpl, err := parseTemplate(sfc.Template)
	if err != nil {
		t.Fatalf("parseTemplate: %v", err)
	}
	a := analyzeScript(sfc.Script)
	if astPath {
		return generateCodeAST("Parent", sfc, tmpl, a, "vela-test")
	}
	return generateCode("Parent", sfc, tmpl, a, "vela-test")
}

func TestComponentWrapperIsDisplayContents_AST(t *testing.T) {
	out := compileComponentParent(t, true)
	if !strings.Contains(out, "createElement('div')") {
		t.Fatalf("expected a wrapper div for the component:\n%s", out)
	}
	if !strings.Contains(out, ".style.display = 'contents';") {
		t.Fatalf("component wrapper must be display:contents "+
			"(layout-transparent):\n%s", out)
	}
}

func TestComponentWrapperIsDisplayContents_Legacy(t *testing.T) {
	out := compileComponentParent(t, false)
	if !strings.Contains(out, ".style.display = 'contents';") {
		t.Fatalf("legacy component wrapper must be display:contents:\n%s", out)
	}
}
