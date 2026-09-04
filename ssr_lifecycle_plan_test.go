package main

import "testing"

func TestSSRStructuralLifecyclePlanRetainsNestedInstanceRecipe(t *testing.T) {
	sfc, err := parseSFC(`<template><main>{#if visible}<section>{#each rows as row (row.id)}<Child :row={row} />{/each}</section>{:else}<p>Empty</p>{/if}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan := BuildSSRStructuralLifecyclePlan(sfc.TemplateAST.Nodes)
	if len(plan.Children) != 1 || plan.Children[0].Kind != SSRLifecyclePlanElement {
		t.Fatalf("root plan=%#v", plan.Children)
	}
	ifPlan := plan.Children[0].Children[0]
	if ifPlan.Kind != SSRLifecyclePlanIf || !hasSSRStructuralOperation(ifPlan, SSRLifecycleCollectPayload) {
		t.Fatalf("if lifecycle=%#v", ifPlan)
	}
	eachPlan := ifPlan.Children[0].Children[0].Children[0]
	if eachPlan.Kind != SSRLifecyclePlanEach || !hasSSRStructuralOperation(eachPlan, SSRLifecycleMountCSR) {
		t.Fatalf("each lifecycle=%#v", eachPlan)
	}
	componentPlan := eachPlan.Children[0]
	if componentPlan.Kind != SSRLifecyclePlanComponent {
		t.Fatalf("component lifecycle=%#v", componentPlan)
	}
	segments := componentPlan.InstancePath.Segments
	if len(segments) < 3 {
		t.Fatalf("instance recipe=%#v", segments)
	}
	componentSegment := segments[len(segments)-1]
	eachSegment := findSSRInstanceSegment(segments, SSRInstanceEachRow)
	if eachSegment == nil || eachSegment.Key == nil || componentSegment.Kind != SSRInstanceComponent || componentSegment.Name != "Child" {
		t.Fatalf("instance recipe=%#v", segments)
	}
}

func findSSRInstanceSegment(segments []SSRInstanceSegment, kind SSRInstanceSegmentKind) *SSRInstanceSegment {
	for index := len(segments) - 1; index >= 0; index-- {
		if segments[index].Kind == kind {
			return &segments[index]
		}
	}
	return nil
}

func TestSSRStructuralLifecyclePlanRetainsAwaitBranches(t *testing.T) {
	sfc, err := parseSFC(`<template><main>{#await request}<Child />{:then value}<p>{value}</p>{:catch error}<p>{error}</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan := BuildSSRStructuralLifecyclePlan(sfc.TemplateAST.Nodes)
	awaitPlan := plan.Children[0].Children[0]
	if awaitPlan.Kind != SSRLifecyclePlanAwait || len(awaitPlan.Children) != 3 {
		t.Fatalf("await lifecycle=%#v", awaitPlan)
	}
	for index, branch := range awaitPlan.Children {
		if branch.Kind != SSRLifecyclePlanAwaitBranch || branch.InstancePath.Segments[len(branch.InstancePath.Segments)-1].Kind != SSRInstanceAwaitBranch || !hasSSRStructuralOperation(branch, SSRLifecycleDestroy) {
			t.Fatalf("await branch %d=%#v", index, branch)
		}
	}
	pending := awaitPlan.AwaitBranch(0)
	if pending == nil || pending != awaitPlan.Children[0] || len(pending.Children) != 1 || pending.Children[0].Kind != SSRLifecyclePlanComponent {
		t.Fatalf("typed pending branch=%#v", pending)
	}
	if awaitPlan.AwaitBranch(3) != nil {
		t.Fatal("out-of-range await branch must not produce a lifecycle node")
	}
}

func TestSSRStructuralLifecyclePlanFindsDynamicTextOwner(t *testing.T) {
	sfc, err := parseSFC(`<template><main><p>Hello, {name}</p></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan := BuildSSRStructuralLifecyclePlan(sfc.TemplateAST.Nodes)
	text := sfc.TemplateAST.Nodes[0].(*ElementNode).Children[0].(*ElementNode).Children[0].(*TextNode)
	part := text.Parts[1]
	owner := plan.Find(SSRLifecyclePlanText, part.Span)
	if owner == nil || owner.Span != text.Span || !owner.hasOperation(SSRLifecycleUpdate) {
		t.Fatalf("text lifecycle owner=%#v", owner)
	}
}

func TestSSRStructuralLifecyclePlanFindsDirectIfComponent(t *testing.T) {
	sfc, err := parseSFC(`<template><main>{#if visible}<Child />{:else}<p>Hidden</p>{/if}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan := BuildSSRStructuralLifecyclePlan(sfc.TemplateAST.Nodes)
	ifPlan := plan.Find(SSRLifecyclePlanIf, sfc.TemplateAST.Nodes[0].(*ElementNode).Children[0].(*IfChainNode).Span)
	components, index, ok := ssrHydrationDirectIfComponents(ifPlan)
	if !ok || len(components) != 1 || components[0].Name != "Child" || index != 0 {
		t.Fatalf("direct if components=%#v index=%d ok=%t", components, index, ok)
	}
}

func hasSSRStructuralOperation(plan *SSRStructuralLifecyclePlan, operation SSRLifecycleOperation) bool {
	for _, current := range plan.Operations {
		if current == operation {
			return true
		}
	}
	return false
}
