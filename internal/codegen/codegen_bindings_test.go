package codegen

// codegen_bindings_test.go — bind:value + bind:checked + bind:this.
//
// Two unrelated regressions / features land here:
//
//   • Bug 11 — bind:value was emitting the DOM write but skipping the
//     listener, so user input never wrote back to state.  The
//     listener-emission gate did an exact-match `isStateName(attr.Value)`
//     against a value that still carried its outer `{…}` wrapper,
//     so the check always returned false.
//
//   • Bug 12 / feature — bind:this={ref} writes the live element into
//     the user's variable.  Works on top-level elements and inside
//     `{#if}` branches.  Skipped inside `{#each}` rows (the ref would
//     be overwritten on every iteration).

import (
	"strings"
	"testing"
)

// ─────────────────────────────────────────────
//  Bug 11 — bind:value listener
// ─────────────────────────────────────────────

// ─────────────────────────────────────────────
//  Bug 12 — bind:this
// ─────────────────────────────────────────────

func TestBindMultipleSelectUsesArrayRuntimeHelpers(t *testing.T) {
	src := `<script>
  let picks = $state(['a']);
</script>
<template>
  <select multiple bind:value={picks}><option value="a">A</option><option value="b">B</option></select>
</template>`
	out := compileGenerated(t, src)
	for _, want := range []string{"$$readSelectValues($e.target)", "$$writeSelectValues", "from './runtime.js'"} {
		if !strings.Contains(out, want) {
			t.Fatalf("multiple select lowering misses %q:\n%s", want, out)
		}
	}
}

func TestBug12_BindThisInsideIfBranchWorks(t *testing.T) {
	src := `<script>
  let show = $state(false);
  let el = $state(null);
</script>
<template>
  {#if show}
    <section bind:this={el}>x</section>
  {/if}
</template>`
	out := compileGenerated(t, src)
	// The bind must reach $$set_el in the chain-build path, not
	// fall through to setAttribute('this', '{el}').
	if strings.Contains(out, "setAttribute('this'") {
		t.Fatalf("bind:this leaked as setAttribute('this',...); got:\n%s", out)
	}
	if !strings.Contains(out, "$$set_el(") {
		t.Fatalf("expected $$set_el(...) call inside branch; got:\n%s", out)
	}
}

func TestBug12_BindThisMarkedDynamic(t *testing.T) {
	// An element with bind:this must NOT be flattened into static
	// HTML (otherwise the binding never fires).
	src := `<script>
  let el = $state(null);
</script>
<template>
  <div><span bind:this={el}>nested</span></div>
</template>`
	out := compileGenerated(t, src)
	if strings.Contains(out, `this="{el}"`) {
		t.Fatalf("bind:this got serialized into static HTML; got:\n%s", out)
	}
	if !strings.Contains(out, "$$set_el(") {
		t.Fatalf("expected $$set_el call; got:\n%s", out)
	}
}
