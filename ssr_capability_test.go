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
		{`<template><main>{#each rows as row}<Child :row={row} />{/each}</main></template>`, true},
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

func TestSSRCapabilityKeepsNestedMultiSelectClosedUntilGoSSRSelectionExists(t *testing.T) {
	sfc, err := parseSFC(`<template><main>{#each rows as row (row.id)}{#await request}<p>Pending</p>{:then value}{#each row.fields as field (field.id)}<select multiple bind:value={field.choice}><optgroup label="Current">{#each field.current as option (option.id)}<option :value={option.value}>{option.label}</option>{/each}</optgroup><optgroup label="History">{#each field.history as option (option.id)}<option :value={option.value}>{option.label}</option>{/each}</optgroup></select>{/each}{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("multi-select must remain outside executable SSR capability until Go emits selected membership")
	}
}

func TestSSRCapabilityAdmitsUnkeyedEachNestedSharedAwaitChild(t *testing.T) {
	for _, tc := range []struct {
		code string
		want bool
	}{
		{`<template><main>{#each rows as row}{#await request}<Child :title={row.title} />{:then value}<p>Done</p>{:catch error}<p>Failed</p>{/await}{/each}</main></template>`, true},
		{`<template><main>{#each rows as row}{#await row.request}<Child :title={row.title} />{:then value}<p>Done</p>{/await}{/each}</main></template>`, false},
	} {
		sfc, err := parseSFC(tc.code)
		if err != nil {
			t.Fatal(err)
		}
		if got := CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes); got != tc.want {
			t.Fatalf("CanBuildSSRHydrationClient=%t, want %t for %s", got, tc.want, tc.code)
		}
	}
}

func TestSSRHydrationClientBuildsUnkeyedEachNestedSharedAwaitChild(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request; export let rows = [];</script><template><main>{#each rows as row}{#await request}<Child :title={row.title} />{:then value}<p>Done</p>{:catch error}<p>Failed</p>{/await}{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	code := PrintJS(program, PrintPretty)
	for _, fragment := range []string{"queueMicrotask", "$$heach", "$$acquireHydrationRange", "$$csr_Child"} {
		if !strings.Contains(code, fragment) {
			t.Fatalf("hydration client misses %q:\n%s", fragment, code)
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
	sfc, err := parseSFC(`<template><main><section>{#each rows as row}<Child :row={row}><p>slot</p></Child>{/each}</section></main></template>`)
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

func TestSSRCapabilityAdmitsPendingAwaitNestedIfChildLifecycle(t *testing.T) {
	sfc, err := parseSFC(`<template><main>{#await request}{#if shown}<Child :title={title} />{:else}<p>Waiting</p>{/if}{:then value}<p>{value}</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	capability := AnalyzeSSRCapability(sfc.TemplateAST.Nodes)
	if !capability.ExecutableHydration() {
		t.Fatalf("pending nested child capability issues=%#v", capability.HydrationIssues())
	}
}
