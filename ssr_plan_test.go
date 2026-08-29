package main

import (
	"strings"
	"testing"
)

func TestBuildSSRRenderPlanUsesCachedExpressionsAndStableEventKeys(t *testing.T) {
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
	if button.EventKey != "vela-demo-e0" {
		t.Fatalf("button event key = %q", button.EventKey)
	}
	if len(plan.Events) != 2 {
		t.Fatalf("event count = %d, want 2", len(plan.Events))
	}
	for i, event := range plan.Events {
		if event.EventKey != button.EventKey {
			t.Fatalf("event %d key = %q, want %q", i, event.EventKey, button.EventKey)
		}
	}
	sourceButton, ok := sfc.TemplateAST.Nodes[0].(*ElementNode)
	if !ok {
		t.Fatalf("source node = %T, want *ElementNode", sfc.TemplateAST.Nodes[0])
	}
	if plan.Events[0].Handler != sourceButton.Attrs[0].Expr || plan.Events[1].Handler != sourceButton.Attrs[1].Expr {
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
		SSRDynamicAttribute,
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

func TestBuildSSRRenderPlanDoesNotTreatComponentEventsAsDOMEvents(t *testing.T) {
	sfc, err := parseSFC(`<template><Child @on:close={close} :title={title} /></template>`)
	if err != nil {
		t.Fatalf("parseSFC: %v", err)
	}
	plan, err := BuildSSRRenderPlan("Parent", sfc, "vela-parent")
	if err != nil {
		t.Fatalf("BuildSSRRenderPlan: %v", err)
	}
	if len(plan.Events) != 0 {
		t.Fatalf("component event became a DOM event: %#v", plan.Events)
	}
	component, ok := plan.Nodes[0].(*SSRPlanComponent)
	if !ok {
		t.Fatalf("plan node = %T, want *SSRPlanComponent", plan.Nodes[0])
	}
	if len(component.Attrs) != 2 || component.Attrs[0].Role != SSRAttrEvent || component.Attrs[1].Role != SSRAttrDynamic {
		t.Fatalf("component attrs = %#v", component.Attrs)
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
