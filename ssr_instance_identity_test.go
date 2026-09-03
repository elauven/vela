package main

import "testing"

func TestSSRComponentInstanceIdentitiesAreStructuralAndOrdered(t *testing.T) {
	sfc, err := parseSFC(`<template><main>{#if visible}<Alpha /><Beta />{:else}<p>Hidden</p>{/if}{#each rows as row (row.id)}<Child :row={row} />{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	identities := BuildSSRComponentInstanceIdentities(sfc.TemplateAST.Nodes)
	if len(identities) != 3 {
		t.Fatalf("identities=%#v", identities)
	}
	if identities[0].ComponentName != "Alpha" || identities[1].ComponentName != "Beta" || identities[2].ComponentName != "Child" {
		t.Fatalf("component identity order=%#v", identities)
	}
	if identities[0].Namespace == identities[1].Namespace || identities[0].RuntimeScoped || identities[1].RuntimeScoped {
		t.Fatalf("static branch identities=%#v", identities[:2])
	}
	if !identities[2].RuntimeScoped || identities[2].Namespace == identities[0].Namespace {
		t.Fatalf("each identity=%#v", identities[2])
	}
}

func TestSSRStaticRepeatedComponentInstanceSuffixesUseStructuralPaths(t *testing.T) {
	sfc, err := parseSFC(`<template><main>{#if visible}<Child /><Child />{:else}<Child />{/if}{#each rows as row (row.id)}<Child :row={row} />{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	suffixes := SSRStaticRepeatedComponentInstanceSuffixes(sfc.TemplateAST.Nodes)
	if len(suffixes) != 3 {
		t.Fatalf("static repeated suffixes=%#v", suffixes)
	}
	seen := map[string]bool{}
	for _, suffix := range suffixes {
		if suffix == "" || seen[suffix] {
			t.Fatalf("suffixes must be non-empty and unique: %#v", suffixes)
		}
		seen[suffix] = true
	}
}

func TestSSRComponentInstanceIdentityIndexAndRuntimeSuffix(t *testing.T) {
	sfc, err := parseSFC(`<template><main>{#each rows as row (row.id)}<Child /><Child />{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	identities := BuildSSRComponentInstanceIdentities(sfc.TemplateAST.Nodes)
	index := SSRComponentInstanceIdentityIndex(sfc.TemplateAST.Nodes)
	if len(identities) != 2 || len(index) != 2 {
		t.Fatalf("identities=%#v index=%#v", identities, index)
	}
	for _, identity := range identities {
		indexed, ok := index[identity.TemplateSpan]
		if !ok || !indexed.RuntimeScoped {
			t.Fatalf("missing runtime identity for %#v", identity)
		}
		if suffix := ssrRuntimeComponentSuffix(indexed); suffix == "" {
			t.Fatalf("empty runtime suffix for %#v", indexed)
		}
	}
}
