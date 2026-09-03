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
