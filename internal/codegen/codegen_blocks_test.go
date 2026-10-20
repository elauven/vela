package codegen

import (
	"strings"
	"testing"
)

func TestKey_RegistersUpdateOnKeyDeps(t *testing.T) {
	// The chain function must re-run when the key expression's deps
	// change — otherwise the cached subtree would persist across
	// what should be re-creations.
	src := `<script>
  let id = $state(1);
</script>
<template>
  {#key id}
    <p>x</p>
  {/key}
</template>`
	out := compileGenerated(t, src)
	// $$set_id should mark and schedule the chain update for the component flush.
	idx := strings.Index(out, "function $$set_id(")
	if idx < 0 {
		t.Fatalf("missing $$set_id:\n%s", out)
	}
	tail := out[idx:]
	endIdx := strings.Index(tail, "\n  }\n")
	if endIdx < 0 {
		endIdx = len(tail)
	}
	setterBody := tail[:endIdx]
	if !strings.Contains(setterBody, "$$dirty_s") || !strings.Contains(setterBody, "$$scheduleFlush()") {
		t.Fatalf("$$set_id does not mark and schedule its structural chain update:\n%s", setterBody)
	}
}
