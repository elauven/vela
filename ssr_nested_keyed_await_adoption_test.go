package main

import "testing"

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
