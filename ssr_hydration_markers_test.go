package main

import "testing"

func TestSSRHydrationMarkerIDsFollowSharedRenderPlan(t *testing.T) {
	sfc, err := parseSFC(`<template><main>{title}{#if visible}<p>{title}</p>{/if}{#each rows as row (row.id)}<p>{row.name}</p>{/each}{#await request}<p>Loading</p>{:then result}<p>{result}</p>{:catch error}<p>{error}</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	root := plan.Nodes[0].(*SSRPlanElement)
	text := root.Children[0].(*SSRPlanText)
	ifNode := root.Children[1].(*SSRPlanIf)
	eachNode := root.Children[2].(*SSRPlanEach)
	awaitNode := root.Children[3].(*SSRPlanAwait)
	markers := ssrHydrationMarkerIDs(plan.Nodes)

	if got, ok := markers.Elements[root.Span]; !ok || got != root.HydrationID {
		t.Fatalf("root element marker=%d,%t want %d", got, ok, root.HydrationID)
	}
	if got, ok := markers.textID(&Part{Span: text.Parts[0].Span}); !ok || got != text.Parts[0].DynamicID {
		t.Fatalf("text marker=%d,%t want %d", got, ok, text.Parts[0].DynamicID)
	}
	if got, ok := markers.ifID(ifNode.Span); !ok || got != ifNode.DynamicID {
		t.Fatalf("if marker=%d,%t want %d", got, ok, ifNode.DynamicID)
	}
	if got, ok := markers.eachID(eachNode.Span); !ok || got != eachNode.DynamicID {
		t.Fatalf("each marker=%d,%t want %d", got, ok, eachNode.DynamicID)
	}
	if got, ok := markers.awaitID(awaitNode.Span); !ok || got != awaitNode.DynamicID {
		t.Fatalf("await marker=%d,%t want %d", got, ok, awaitNode.DynamicID)
	}
	if !(text.Parts[0].DynamicID < ifNode.DynamicID && ifNode.DynamicID < eachNode.DynamicID && eachNode.DynamicID < awaitNode.DynamicID) {
		t.Fatalf("unexpected dynamic marker order: text=%d if=%d each=%d await=%d", text.Parts[0].DynamicID, ifNode.DynamicID, eachNode.DynamicID, awaitNode.DynamicID)
	}
}
