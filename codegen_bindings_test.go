package main

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

func TestBug11_BindValueEmitsListener(t *testing.T) {
	src := `<script>
  let name = $state('');
</script>
<template>
  <input bind:value={name} />
</template>`
	out := compileGenerated(t, src)
	if !strings.Contains(out, "addEventListener('input'") {
		t.Fatalf("expected input listener for bind:value; got:\n%s", out)
	}
	if !strings.Contains(out, "$$set_name($e.target.value)") {
		t.Fatalf("expected $$set_name($e.target.value) in handler; got:\n%s", out)
	}
}

func TestBug11_BindCheckedEmitsListener(t *testing.T) {
	src := `<script>
  let agree = $state(false);
</script>
<template>
  <input type="checkbox" bind:checked={agree} />
</template>`
	out := compileGenerated(t, src)
	if !strings.Contains(out, "addEventListener('change'") {
		t.Fatalf("expected change listener for bind:checked; got:\n%s", out)
	}
	if !strings.Contains(out, "$$set_agree($e.target.checked)") {
		t.Fatalf("expected $$set_agree($e.target.checked); got:\n%s", out)
	}
}

func TestBug11_BindSelectEmitsChangeListener(t *testing.T) {
	src := `<script>
  let pick = $state('a');
</script>
<template>
  <select bind:value={pick}><option>a</option></select>
</template>`
	out := compileGenerated(t, src)
	if !strings.Contains(out, "addEventListener('change'") {
		t.Fatalf("expected change listener on <select> for bind:value; got:\n%s", out)
	}
}

// ─────────────────────────────────────────────
//  Bug 12 — bind:this
// ─────────────────────────────────────────────

func TestBug12_BindThisOnStateRefUsesSetter(t *testing.T) {
	src := `<script>
  let el = $state(null);
</script>
<template>
  <div bind:this={el}>x</div>
</template>`
	out := compileGenerated(t, src)
	if !strings.Contains(out, "$$set_el($$root)") &&
		!strings.Contains(out, "$$set_el($$n") {
		t.Fatalf("expected $$set_el(elem) for bind:this; got:\n%s", out)
	}
	if !strings.Contains(out, "$$set_el(null)") {
		t.Fatalf("expected $$set_el(null) on destroy; got:\n%s", out)
	}
}

func TestBug12_BindThisOnLocalUsesPlainAssignment(t *testing.T) {
	src := `<script>
  let myRef;
</script>
<template>
  <div bind:this={myRef}>x</div>
</template>`
	out := compileGenerated(t, src)
	if !strings.Contains(out, "myRef = $$root") &&
		!strings.Contains(out, "myRef = $$n") {
		t.Fatalf("expected plain assignment for non-state bind:this; got:\n%s", out)
	}
	if !strings.Contains(out, "myRef = null") {
		t.Fatalf("expected myRef = null on destroy; got:\n%s", out)
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
