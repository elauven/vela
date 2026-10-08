package main

import (
	"strings"
	"testing"
)

func TestBuildSSRRenderPlanUsesCachedExpressions(t *testing.T) {
	src := `<script>
export let title = "";
export let active = false;
export let items = [];
</script><style>.box { color: red; }</style><template><button @click={track} @focus={observe}>{title}</button>{#if active}<p :title={title}>{message}</p>{/if}{#each items as item (item.id)}<span>{item.label}</span>{/each}</template>`
	sfc, err := parseSFC(src)
	if err != nil {
		t.Fatalf("parseSFC: %v", err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatalf("BuildSSRRenderPlan: %v", err)
	}
	if plan.ComponentName != "Demo" || plan.ScopeID != "vela-demo" {
		t.Fatalf("plan identity = %#v", plan)
	}
	if !strings.Contains(plan.ScopedCSS, "vela-demo") {
		t.Fatalf("scoped CSS is not preserved: %q", plan.ScopedCSS)
	}
	if len(plan.Nodes) != 3 {
		t.Fatalf("plan node count = %d, want 3", len(plan.Nodes))
	}

	button, ok := plan.Nodes[0].(*SSRPlanElement)
	if !ok {
		t.Fatalf("first plan node = %T, want *SSRPlanElement", plan.Nodes[0])
	}
	sourceButton, ok := sfc.TemplateAST.Nodes[0].(*ElementNode)
	if !ok {
		t.Fatalf("source node = %T, want *ElementNode", sfc.TemplateAST.Nodes[0])
	}
	if len(button.Attrs) != 2 || button.Attrs[0].Role != SSRAttrEvent || button.Attrs[0].Expr != sourceButton.Attrs[0].Expr || button.Attrs[1].Expr != sourceButton.Attrs[1].Expr {
		t.Fatal("event handlers were not preserved as cached template expression ASTs")
	}
	text := button.Children[0].(*SSRPlanText)
	if text.Parts[0].Expr != sourceButton.Children[0].(*TextNode).Parts[0].Expr {
		t.Fatal("text expression was reparsed or replaced")
	}

	ifNode, ok := plan.Nodes[1].(*SSRPlanIf)
	if !ok || len(ifNode.Branches) != 1 || ifNode.Branches[0].Condition == nil {
		t.Fatalf("if plan = %#v", plan.Nodes[1])
	}
	each, ok := plan.Nodes[2].(*SSRPlanEach)
	if !ok || each.Iterable == nil || each.Key == nil || each.ItemName != "item" {
		t.Fatalf("each plan = %#v", plan.Nodes[2])
	}

	wantKinds := []SSRDynamicKind{
		SSRDynamicText,
		SSRDynamicIf,
		SSRDynamicText,
		SSRDynamicEach,
		SSRDynamicEachKey,
		SSRDynamicText,
	}
	if len(plan.Dynamics) != len(wantKinds) {
		t.Fatalf("dynamic site count = %d, want %d: %#v", len(plan.Dynamics), len(wantKinds), plan.Dynamics)
	}
	for i, want := range wantKinds {
		if got := plan.Dynamics[i]; got.ID != i || got.Kind != want || got.Expr == nil {
			t.Fatalf("dynamic site %d = %#v, want id=%d kind=%d with expression", i, got, i, want)
		}
	}
}

func TestBuildSSRRenderPlanRejectsMissingInputs(t *testing.T) {
	if _, err := BuildSSRRenderPlan("Demo", nil, ""); err == nil {
		t.Fatal("missing SFC did not fail")
	}
	sfc, err := parseSFC(`<template><p>ok</p></template>`)
	if err != nil {
		t.Fatalf("parseSFC: %v", err)
	}
	if _, err := BuildSSRRenderPlan("", sfc, ""); err == nil {
		t.Fatal("missing component name did not fail")
	}
}

func TestBuildSSRRenderPlanUsesTypedJSDocProps(t *testing.T) {
	sfc, err := parseSFC(`<script>
/** @type {string} */
export let title = null;
/** @type {number[]} */
export let values = null;
/** @type {Record<string, unknown>} */
export let config = null;
const ignored = "/** @type {boolean} */ export let fake = null;";
</script><template>{title}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	if len(sfc.ScriptAST.DocComments) != 3 {
		t.Fatalf("JSDoc metadata=%#v", sfc.ScriptAST.DocComments)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]SSRValueType{}
	for _, prop := range plan.Props {
		got[prop.SourceName] = prop.Type
	}
	wants := map[string]SSRValueType{
		"title":  SSRTypeString,
		"values": SSRTypeSlice,
		"config": SSRTypeObject,
	}
	for name, want := range wants {
		if got[name] != want {
			t.Errorf("prop %q type=%d want %d", name, got[name], want)
		}
	}
}

func TestBuildSSRRenderPlanInfersNumericExpressionPropTypes(t *testing.T) {
	sfc, err := parseSFC(`<script>
export let negative = -1;
export let ratio = 1 / 2;
export let label = 'item ' + 2;
</script><template>{negative}{ratio}{label}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]SSRValueType{}
	for _, prop := range plan.Props {
		got[prop.SourceName] = prop.Type
	}
	for name, want := range map[string]SSRValueType{
		"negative": SSRTypeFloat,
		"ratio":    SSRTypeFloat,
		"label":    SSRTypeString,
	} {
		if got[name] != want {
			t.Errorf("prop %q type=%d want %d", name, got[name], want)
		}
	}
}

func TestBuildSSRRenderPlanRejectsPresenceFieldPropCollision(t *testing.T) {
	sfc, err := parseSFC(`<script>export let set = false;</script><template>{set}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err == nil || !strings.Contains(err.Error(), `prop "set" maps to reserved generated presence field "Set"`) {
		t.Fatalf("reserved prop collision error=%v", err)
	}
}
