package main

import (
	"strings"
	"testing"
)

func TestDOMAcquirePlanTracksStructuralElementsInTemplateOrder(t *testing.T) {
	sfc, err := parseSFC(`<template><main><button @click={save}>Save</button></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	_, plan := buildProgramWithDOMAcquirePlan("App", sfc, sfc.TemplateAST.Nodes, analysis, "vela-test", false, DOMAcquireCreate)
	if got, want := plan.Elements, []DOMAcquireElement{{Variable: "$$root", Tag: "main", HydrationID: 0}, {Variable: "$$n1", Tag: "button", HydrationID: 1}}; !sameDOMAcquireElements(got, want) {
		t.Fatalf("acquire plan=%#v want %#v", got, want)
	}
}

func TestDOMAcquireHydrateModeUsesAcquirePrimitive(t *testing.T) {
	sfc, err := parseSFC(`<template><main><p>Hello</p></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	prog, plan := buildProgramWithDOMAcquirePlan("App", sfc, sfc.TemplateAST.Nodes, analysis, "vela-test", false, DOMAcquireHydrate)
	if plan.Mode != DOMAcquireHydrate || len(plan.Elements) != 2 {
		t.Fatalf("hydrate plan=%#v", plan)
	}
	source := PrintJS(prog, PrintPretty)
	if !strings.Contains(source, `$$acquireHydrationElement($$walker, 0, 'main')`) {
		t.Fatalf("hydrate mode did not use acquire primitive:\n%s", source)
	}
}

func sameDOMAcquireElements(got, want []DOMAcquireElement) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
