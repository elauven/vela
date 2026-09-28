package main

import (
	"strings"
	"testing"
)

func TestHydrationKeyedAwaitNestedComponentScopeUsesInnerKeyedRecipe(t *testing.T) {
	sfc, err := parseSFC(`<template>{#each rows as row (row.id)}{#each row.fields as field (field.id)}<Child :title={field.title} />{/each}{/each}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	outer := sfc.TemplateAST.Nodes[0].(*EachBlockNode)
	nested := outer.Children[0].(*EachBlockNode)
	g := &ASTGen{sfc: sfc, hydrationLifecyclePlan: BuildSSRStructuralLifecyclePlan(sfc.TemplateAST.Nodes)}
	code := PrintJS(&JsProgram{Body: []JsStmt{genConst("scope", g.hydrationKeyedAwaitNestedComponentScope(outer, nested, id("$$outer_scope"), "$$field", "$$field_index"))}}, PrintPretty)
	want := "$$outer_scope + '--each-" + itoa(nested.Span.Start) + "-' + String($$field.id)"
	if !strings.Contains(code, want) {
		t.Fatalf("inner keyed component scope missing %q:\n%s", want, code)
	}
}

func TestHydrationKeyedAwaitNestedDirectComponentRow(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		ok   bool
	}{
		{"one-way props", `<template>{#each fields as field (field.id)}<Child :title={field.title} />{/each}</template>`, true},
		{"slot is deferred", `<template>{#each fields as field (field.id)}<Child><span>slot</span></Child>{/each}</template>`, false},
		{"component event is deferred", `<template>{#each fields as field (field.id)}<Child @on:close={close} />{/each}</template>`, false},
		{"component binding is deferred", `<template>{#each fields as field (field.id)}<Child bind:value={field.value} />{/each}</template>`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sfc, err := parseSFC(tc.src)
			if err != nil {
				t.Fatal(err)
			}
			block := sfc.TemplateAST.Nodes[0].(*EachBlockNode)
			recipe, ok := hydrationKeyedAwaitNestedDirectComponentRow(block)
			if ok != tc.ok {
				t.Fatalf("recognized=%v, want %v", ok, tc.ok)
			}
			if ok && (recipe.Block != block || recipe.Component == nil || recipe.Component.Name != "Child") {
				t.Fatalf("unexpected recipe: %#v", recipe)
			}
		})
	}
}
