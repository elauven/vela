package main

import (
	"strings"
	"testing"
)

func TestAnalyzeSSRCapabilityMirrorsCurrentHydrationSubset(t *testing.T) {
	for _, source := range []struct {
		code string
		want bool
	}{
		{`<template><main><p>{title}</p></main></template>`, true},
		{`<template><main>{#if visible}<Child :title={title} />{:else}<p>Hidden</p>{/if}</main></template>`, true},
		{`<template><main>{#each rows as row}<Child :row={row} />{/each}</main></template>`, false},
	} {
		sfc, err := parseSFC(source.code)
		if err != nil {
			t.Fatal(err)
		}
		capability := AnalyzeSSRCapability(sfc.TemplateAST.Nodes)
		if got := capability.ExecutableHydration(); got != source.want {
			t.Fatalf("capability executable=%t, want %t for %s", got, source.want, source.code)
		}
		if got := CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes); got != source.want {
			t.Fatalf("CanBuildSSRHydrationClient=%t, want %t for %s", got, source.want, source.code)
		}
	}
}

func TestAnalyzeSSRCapabilityRetainsNestedStructuralPathAndOwnership(t *testing.T) {
	sfc, err := parseSFC(`<template><main>{#if visible}<section>{#each rows as row (row.id)}<Child :row={row} />{/each}</section>{/if}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	capability := AnalyzeSSRCapability(sfc.TemplateAST.Nodes)
	if capability.Hydration != SSRHydrationNeedsLifecycle {
		t.Fatalf("root hydration=%d, want needs lifecycle", capability.Hydration)
	}
	if len(capability.Children) != 1 || capability.Children[0].Marker != SSRMarkerElement {
		t.Fatalf("root element ownership was not retained: %#v", capability.Children)
	}
	ifNode := capability.Children[0].Children[0]
	if ifNode.NodeKind != "if" || ifNode.Marker != SSRMarkerStructuralRange {
		t.Fatalf("if capability=%#v", ifNode)
	}
	eachNode := ifNode.Children[0].Children[0].Children[0]
	if eachNode.NodeKind != "each" || eachNode.Marker != SSRMarkerStructuralRange {
		t.Fatalf("each capability=%#v", eachNode)
	}
	if eachNode.Path != "template[0].children[0].branches[0][0].children[0]" {
		t.Fatalf("each path=%q", eachNode.Path)
	}
	if len(eachNode.Children) != 1 || eachNode.Children[0].Marker != SSRMarkerComponentBoundary {
		t.Fatalf("component ownership=%#v", eachNode.Children)
	}
}

func TestSSRCapabilityReportsMostSpecificHydrationBoundary(t *testing.T) {
	sfc, err := parseSFC(`<template><main><section>{#each rows as row}<Child :row={row} />{/each}</section></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	capability := AnalyzeSSRCapability(sfc.TemplateAST.Nodes)
	issues := capability.HydrationIssues()
	if len(issues) != 1 {
		t.Fatalf("issues=%#v, want one specific issue", issues)
	}
	issue := issues[0]
	if issue.Path != "template[0].children[0].children[0]" || issue.NodeKind != "each" {
		t.Fatalf("issue=%#v", issue)
	}
	if !strings.Contains(issue.Reason, "recursive instance and range lifecycle") {
		t.Fatalf("issue reason=%q", issue.Reason)
	}
	_, err = BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err == nil || !strings.Contains(err.Error(), issue.Path) || !strings.Contains(err.Error(), issue.Reason) {
		t.Fatalf("hydration error=%v, want capability reason for %s", err, issue.Path)
	}
}
