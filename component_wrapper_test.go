package main

import (
	"strings"
	"testing"
)

// Bug 4 — Component wrapper in the DOM tree.
//
// A component invocation USED to be mounted into a wrapper <div>
// styled `display:contents`.  That fixed LAYOUT (the wrapper has
// no layout box) but the <div> was still in the DOM tree, so CSS
// like `.parent > .child` did not match the component's root —
// because the wrapper sat between them.
//
// The fix: replace the wrapper with a Node.COMMENT_NODE anchor.
// Comments are not elements; they are invisible to CSS element
// selectors AND :nth-child counting.  The component's $$root is
// inserted before the anchor, becoming a true direct child of
// the parent.  See MIGRATION.md "Component anchor — no wrapper".
func compileComponentParent(t *testing.T) string {
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
	return generateCodeAST("Parent", sfc, tmpl, a, "vela-test")
}

// Before the bug-4 fix: a component invocation produced a parent-
// side `<div style="display:contents">` wrapper around the child's
// $$root, breaking CSS direct-child selectors like `.parent > .row
// > .child`.
//
// After the fix: the wrapper is replaced by a `<!-- comp -->`
// anchor comment.  Comments are not elements, so `>`, `:nth-child`,
// `:first-child` etc. skip them entirely — the child's $$root
// becomes a true direct child of the parent.  This test pins the
// new shape so a regression returns the wrapper div by accident.
//
// The legacy generator path is unchanged (the differential safety
// net is no longer relied on for component invocation shape — see
// MIGRATION.md).
func TestComponentMountUsesAnchorComment_AST(t *testing.T) {
	out := compileComponentParent(t)
	// New shape: anchor comment, no wrapper div.
	if !strings.Contains(out, "document.createComment('comp')") {
		t.Errorf("expected anchor comment for component mount:\n%s", out)
	}
	if strings.Contains(out, ".style.display = 'contents';") {
		t.Errorf("expected NO display:contents wrapper for component (bug 4):\n%s", out)
	}
	// The child constructor sees a Comment target — verify the
	// per-component mount handles both nodeTypes (set up at
	// component header).  This check anchors the component-side
	// mount branch as part of the same artefact.
	if !strings.Contains(out, "nodeType === 8") {
		t.Errorf("expected component-side mount to detect Comment target:\n%s", out)
	}
}
