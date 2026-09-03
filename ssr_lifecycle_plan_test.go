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
}

func hasSSRStructuralOperation(plan *SSRStructuralLifecyclePlan, operation SSRLifecycleOperation) bool {
	for _, current := range plan.Operations {
		if current == operation {
			return true
		}
	}
	return false
}
