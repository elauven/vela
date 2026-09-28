package main

import "testing"

func TestSSRHydrationComponentNamesFindsSettledNestedKeyedChild(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let rows = [];</script><template>{#each rows as row (row.id)}{#await row.request}<p>pending</p>{:then value}{#each row.fields as field (field.id)}<Child :title={field.title} />{/each}{/await}{/each}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	components, err := ssrHydrationComponentNames(sfc, analyzeScriptAST(sfc.ScriptAST))
	if err != nil {
		t.Fatal(err)
	}
	if !components["Child"] || len(components) != 1 {
		t.Fatalf("nested settled child import discovery=%#v, want only Child", components)
	}
}
