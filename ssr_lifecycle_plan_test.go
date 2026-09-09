package main

import (
	"strings"
	"testing"
)

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

func TestSSRHydrationPendingAwaitComponentsUsesTypedBranchPlan(t *testing.T) {
	sfc, err := parseSFC(`<template><main>{#await request}{#if shown}<Child /><Child />{/if}{:then value}<p>{value}</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	await := sfc.TemplateAST.Nodes[0].(*ElementNode).Children[0].(*AwaitBlockNode)
	plan := BuildSSRStructuralLifecyclePlan(sfc.TemplateAST.Nodes).Find(SSRLifecyclePlanAwait, await.Span)
	components := ssrHydrationPendingAwaitComponents(plan)
	if len(components) != 2 || components[0].Name != "Child" || components[1].Name != "Child" {
		t.Fatalf("pending components=%#v", components)
	}
	entries := ssrHydrationPendingAwaitComponentPlans(plan)
	if len(entries) != 2 || entries[0].Component != components[0] || entries[0].Plan == nil || entries[0].Plan.InstancePath.Segments[len(entries[0].Plan.InstancePath.Segments)-1].Kind != SSRInstanceComponent {
		t.Fatalf("pending component plans=%#v", entries)
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

func TestSSRStructuralLifecyclePlanRetainsInnerKeyedEachRecipe(t *testing.T) {
	sfc, err := parseSFC(`<template><main>{#each rows as row (row.id)}{#await row.request}<p>Loading</p>{:then value}{#each row.fields as field, index (field.id)}<input bind:value={field.title} />{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	outer := sfc.TemplateAST.Nodes[0].(*ElementNode).Children[0].(*EachBlockNode)
	await := outer.Children[0].(*AwaitBlockNode)
	inner := await.ThenChildren[0].(*EachBlockNode)
	plan := BuildSSRStructuralLifecyclePlan(sfc.TemplateAST.Nodes)
	outerPlan := plan.Find(SSRLifecyclePlanEach, outer.Span)
	innerPlan := plan.Find(SSRLifecyclePlanEach, inner.Span)
	if outerPlan == nil || innerPlan == nil || outerPlan.KeyedEach == nil || innerPlan.KeyedEach == nil {
		t.Fatalf("keyed each plans outer=%#v inner=%#v", outerPlan, innerPlan)
	}
	recipe := innerPlan.KeyedEach
	if recipe.Collection != inner.IterExpr || recipe.Key != inner.KeyAST || recipe.ItemName != "field" || recipe.IndexName != "index" || !recipe.DirectLeafRows {
		t.Fatalf("inner keyed recipe=%#v", recipe)
	}
	if len(recipe.OuterScope.Segments) == 0 || len(recipe.RowScope.Segments) != len(recipe.OuterScope.Segments)+1 {
		t.Fatalf("inner keyed scopes outer=%#v row=%#v", recipe.OuterScope, recipe.RowScope)
	}
	rowSegment := recipe.RowScope.Segments[len(recipe.RowScope.Segments)-1]
	if rowSegment.Kind != SSRInstanceEachRow || rowSegment.Key != inner.KeyAST {
		t.Fatalf("inner keyed row scope=%#v", recipe.RowScope)
	}
	if !ssrInstancePathNeedsRuntimeScope(recipe.OuterScope) || !ssrInstancePathNeedsRuntimeScope(recipe.RowScope) {
		t.Fatalf("nested keyed scopes must retain outer runtime identity: outer=%#v row=%#v", recipe.OuterScope, recipe.RowScope)
	}
	if CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("inner keyed await each must remain capability-gated until its range lowerer is implemented")
	}
}

func TestSSRKeyedEachLifecycleRecipeGatesDirectLeafRows(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   bool
	}{
		{name: "direct dynamic leaves", source: `<template>{#each fields as field (field.id)}<input bind:value={field.title} /><button class:selected={field.selected} style:color={field.color} ={() => field.selected = !field.selected}>{field.title}</button>{/each}</template>`, want: true},
		{name: "else branch", source: `<template>{#each fields as field (field.id)}<p>{field.title}</p>{:else}<p>Empty</p>{/each}</template>`, want: false},
		{name: "nested element", source: `<template>{#each fields as field (field.id)}<p><span>{field.title}</span></p>{/each}</template>`, want: false},
		{name: "structural child", source: `<template>{#each fields as field (field.id)}{#if field.visible}<p>{field.title}</p>{/if}{/each}</template>`, want: false},
		{name: "component child", source: `<template>{#each fields as field (field.id)}<Child :field={field} />{/each}</template>`, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sfc, err := parseSFC(tc.source)
			if err != nil {
				t.Fatal(err)
			}
			each := sfc.TemplateAST.Nodes[0].(*EachBlockNode)
			plan := BuildSSRStructuralLifecyclePlan(sfc.TemplateAST.Nodes).Find(SSRLifecyclePlanEach, each.Span)
			if plan == nil || plan.KeyedEach == nil {
				t.Fatalf("keyed lifecycle plan=%#v", plan)
			}
			if got := plan.KeyedEach.DirectLeafRows; got != tc.want {
				t.Fatalf("DirectLeafRows=%t, want %t", got, tc.want)
			}
		})
	}
}

func TestHydrationEachInstanceScopeFromUsesCachedInnerKey(t *testing.T) {
	sfc, err := parseSFC(`<template>{#each rows as row (row.id)}{#await row.request}{:then value}{#each row.fields as field (field.id)}<p>{field.title}</p>{/each}{/await}{/each}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	outer := sfc.TemplateAST.Nodes[0].(*EachBlockNode)
	await := outer.Children[0].(*AwaitBlockNode)
	inner := await.ThenChildren[0].(*EachBlockNode)
	g := &ASTGen{sfc: sfc}
	expr := g.hydrationEachInstanceScopeFrom(id("$$outerScope"), inner, "$$field", "$$fieldIndex")
	program := &JsProgram{Body: []JsStmt{genConst("scope", expr)}}
	output := PrintJS(program, PrintPretty)
	if !strings.Contains(output, "$$outerScope + '--each-"+itoa(inner.Span.Start)+"-' + String($$field.id)") {
		t.Fatalf("inner scope does not use cached item key AST:\n%s", output)
	}
	if strings.Contains(output, inner.KeyAST.Source) && inner.KeyAST.Source != "field.id" {
		t.Fatalf("inner scope reparsed key source: %s", output)
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
