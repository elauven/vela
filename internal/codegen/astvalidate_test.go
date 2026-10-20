package codegen

import (
	"testing"

	"github.com/elauven/vela/internal/analyzer"
	"github.com/elauven/vela/internal/jsast"
	"github.com/elauven/vela/internal/sfcast"
)

func TestNestedEachBranchUsesFragmentLocalHoists(t *testing.T) {
	src := `<script>
  let groups = $state([]);
</script>
<template>
  {#each groups as group}
    {#if group.kind === 'first'}
      {#each group.items as item}<button>{item}</button>{/each}
    {:else}
      {#each group.items as item}<input :value={item} />{/each}
    {/if}
  {/each}
</template>`
	sfc, err := sfcast.ParseSFC(src)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := sfcast.ParseTemplate(sfc.Template)
	if err != nil {
		t.Fatal(err)
	}
	prog := BuildProgramUnvalidated("NestedEach", sfc, nodes, analyzer.AnalyzeScriptAST(sfc.ScriptAST), "vela-test", false)
	for _, issue := range jsast.ValidateProgram(prog) {
		if issue.Kind == "duplicate-decl" {
			t.Fatalf("nested each fragment leaked duplicate declaration: %s", issue.Msg)
		}
	}
}
