package main

import (
	"strings"
	"testing"
)

// TestParseClientBoundaries covers the syntax of contract §9: {#client}
// with and without {:else}, and <script context="client">.
func TestParseClientBoundaries(t *testing.T) {
	sfc, err := parseSFC(`<script context="client">export let x = 1;</script><template>{#client}<b>{x}</b>{:else}<i>wait</i>{/client}{#each [1] as n}{#client}<u>{n}</u>{/client}{/each}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !sfc.ClientOnly {
		t.Error(`<script context="client"> must mark the component client-only`)
	}
	first := sfc.TemplateAST.Nodes[0].(*IfChainNode)
	if !first.Client || len(first.Branches) != 2 || first.Branches[0].HasCondition() {
		t.Errorf("{#client}{:else} = %+v", first)
	}
	each := sfc.TemplateAST.Nodes[1].(*EachBlockNode)
	if inner := each.Children[0].(*IfChainNode); !inner.Client || len(inner.Branches) != 1 {
		t.Errorf("{#client} in a row = %+v", inner)
	}
	for _, source := range []string{`<script>let x;</script>`, `<script lang="js">let x;</script>`, `<script data-context="client">let x;</script>`} {
		if plain, _ := parseSFC(source + `<template><p/></template>`); plain.ClientOnly {
			t.Errorf("%s is not client-only", source)
		}
	}
	bad, err := parseSFC(`<template>{#client}<b/>{/if}</template>`)
	if err != nil || len(bad.TemplateAST.Diagnostics) == 0 || !strings.Contains(bad.TemplateAST.Diagnostics[0].Message, "{/client}") {
		t.Errorf("a {#client} closed by {/if} must be a template error, got %v %+v", err, bad.TemplateAST.Diagnostics)
	}
}
