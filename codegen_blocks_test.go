package main

// codegen_blocks_test.go — three additions in one batch:
//
//   1. {#key expr} — destroy and rebuild children when expr changes
//      identity.  Modeled as a one-branch IfChainNode with KeyExpr
//      set; codegen emits a cached prev-key and force-destroys the
//      subtree on mismatch.
//
//   2. {#await promise}{:then v}{:catch e}{/await} — async branch
//      switching.  Lowered to a 3-branch synthesized chain gated by
//      $$promState_X (0/1/2), with a driver that attaches .then/.catch
//      to the promise and re-runs the chain on settle.
//
//   3. Multi-root templates — previously the codegen used the first
//      element as $$root and nested siblings inside it.  Now wraps
//      with a display:contents div whenever there's more than one
//      root element.

import (
	"strings"
	"testing"
)

// ─────────────────────────────────────────────
//  Multi-root template wrapper
// ─────────────────────────────────────────────

func TestMultiRoot_WrapsWhenMultipleSiblings(t *testing.T) {
	src := `<script>
  let n = $state(0);
  function inc() { n++; }
  function dec() { n--; }
</script>
<template>
  <button @click={inc}>+</button>
  <button @click={dec}>-</button>
</template>`
	out := compileGenerated(t, src)
	// Root must be a div (the wrapper), not the first button.
	if !strings.Contains(out, "const $$root = document.createElement('div')") {
		t.Fatalf("expected wrapper div for multi-root template; got:\n%s", out)
	}
	// Siblings must be appended to the wrapper, not to each other.
	if !strings.Contains(out, "$$root.style.display = 'contents'") {
		t.Fatalf("wrapper missing display:contents:\n%s", out)
	}
	// And listeners must attach to per-button vars, not $$root.
	if !strings.Contains(out, "$$n0.addEventListener('click', inc)") {
		t.Fatalf("first button listener should attach to $$n0, not $$root:\n%s", out)
	}
}

func TestMultiRoot_NoWrapperForSingleElement(t *testing.T) {
	src := `<script></script>
<template>
  <div>solo</div>
</template>`
	out := compileGenerated(t, src)
	// Single root element should still BE the root — no wrapper.
	if strings.Contains(out, "$$root.style.display = 'contents'") {
		t.Fatalf("single-root template got an unnecessary wrapper:\n%s", out)
	}
	if !strings.Contains(out, "const $$root = document.createElement('div')") {
		t.Fatalf("expected $$root = div:\n%s", out)
	}
}

// ─────────────────────────────────────────────
//  {#key}
// ─────────────────────────────────────────────

func TestKey_EmitsKeyprevCache(t *testing.T) {
	src := `<script>
  let id = $state(1);
</script>
<template>
  {#key id}
    <div>user {id}</div>
  {/key}
</template>`
	out := compileGenerated(t, src)
	if !strings.Contains(out, "_keyprev") {
		t.Fatalf("expected $$chain_*_keyprev cache for {#key}:\n%s", out)
	}
	if !strings.Contains(out, "$$keynew = (id)") {
		t.Fatalf("expected key expression captured into $$keynew:\n%s", out)
	}
}

func TestKey_ForceDestroyOnKeyChange(t *testing.T) {
	src := `<script>
  let id = $state(1);
</script>
<template>
  {#key id}
    <div>x</div>
  {/key}
</template>`
	out := compileGenerated(t, src)
	// Look for the pattern that destroys the cached subtree on
	// key mismatch.  The check is `if (keyprev !== keynew)` followed
	// by removal of all branch element vars.
	if !strings.Contains(out, "!== $$keynew") {
		t.Fatalf("missing key-mismatch check:\n%s", out)
	}
}

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
	// $$set_id should trigger the chain function in its $$defer body.
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
	if !strings.Contains(setterBody, "$$chain_") {
		t.Fatalf("$$set_id doesn't trigger the chain:\n%s", setterBody)
	}
}

// ─────────────────────────────────────────────
//  {#await}
// ─────────────────────────────────────────────

func TestAwait_EmitsThreeBranchChain(t *testing.T) {
	src := `<script>
  let p = $state(Promise.resolve(1));
</script>
<template>
  {#await p}
    pending
  {:then v}
    got: {v}
  {:catch e}
    error: {e}
  {/await}
</template>`
	out := compileGenerated(t, src)
	// Three branches gated by promise state.
	checks := []string{
		"$$promState_",
		"$$awaitVal_",
		"$$awaitErr_",
		"=== 0)",
		"=== 1)",
		"=== 2)",
	}
	for _, c := range checks {
		if !strings.Contains(out, c) {
			t.Errorf("missing %q in await emission:\n%s", c, out)
		}
	}
}

func TestAwait_RewritesValueBinding(t *testing.T) {
	// {:then v} → references to `v` inside the then branch must
	// resolve to $$awaitVal_X at runtime.
	src := `<script>
  let p = $state(Promise.resolve('hi'));
</script>
<template>
  {#await p}
    pending
  {:then val}
    <p>{val}</p>
  {/await}
</template>`
	out := compileGenerated(t, src)
	if !strings.Contains(out, "$$awaitVal_") {
		t.Fatalf("missing value storage var:\n%s", out)
	}
	// The text in the then branch should read from $$awaitVal_, not val.
	// (The codegen rewrites `val` → `$$awaitVal_X` before walking.)
	if strings.Contains(out, "String(val)") {
		t.Fatalf("then-binding 'val' not rewritten to storage var:\n%s", out)
	}
	if !strings.Contains(out, "String($$awaitVal_") {
		t.Fatalf("expected String($$awaitVal_...) in then branch:\n%s", out)
	}
}

func TestAwait_RewritesErrorBinding(t *testing.T) {
	src := `<script>
  let p = $state(Promise.reject('oops'));
</script>
<template>
  {#await p}
    pending
  {:catch err}
    <p>{err}</p>
  {/await}
</template>`
	out := compileGenerated(t, src)
	if strings.Contains(out, "String(err)") {
		t.Fatalf("catch-binding 'err' not rewritten:\n%s", out)
	}
	if !strings.Contains(out, "String($$awaitErr_") {
		t.Fatalf("expected String($$awaitErr_...) in catch branch:\n%s", out)
	}
}

func TestAwait_DriverWithStalenessGuard(t *testing.T) {
	src := `<script>
  let p = $state(Promise.resolve(1));
</script>
<template>
  {#await p}p{/await}
</template>`
	out := compileGenerated(t, src)
	// The driver function must reattach handlers on each call AND
	// guard them against settling for a stale promise reference.
	if !strings.Contains(out, "function $$awaitDrive_") {
		t.Fatalf("missing driver function:\n%s", out)
	}
	if !strings.Contains(out, "$$promCell_") {
		t.Fatalf("missing $$promCell staleness cell:\n%s", out)
	}
	// Resolved/rejected callbacks must check promCell !== p and bail.
	if !strings.Contains(out, "$$promCell_") || !strings.Contains(out, "!== $$p") {
		t.Fatalf("missing staleness guard in handler:\n%s", out)
	}
}

func TestAwait_DriverReRunsOnPromiseChange(t *testing.T) {
	// When the user reassigns the promise, the driver must rerun to
	// see the new value.  This is wired by registering the driver
	// as an update for the promise expression's deps.
	src := `<script>
  let p = $state(Promise.resolve(1));
  function reload() { p = Promise.resolve(2); }
</script>
<template>
  {#await p}p{/await}
</template>`
	out := compileGenerated(t, src)
	// $$set_p's $$defer body should include a call to the driver.
	idx := strings.Index(out, "function $$set_p(")
	if idx < 0 {
		t.Fatalf("missing $$set_p:\n%s", out)
	}
	tail := out[idx:]
	end := strings.Index(tail, "\n  }\n")
	if end < 0 {
		end = len(tail)
	}
	setter := tail[:end]
	if !strings.Contains(setter, "$$awaitDrive_") {
		t.Fatalf("$$set_p doesn't call the driver:\n%s", setter)
	}
}

func TestAwait_OptionalCatchOmitsBranch(t *testing.T) {
	// {#await p}{:then v}...{/await} — no catch — should still work.
	src := `<script>
  let p = $state(Promise.resolve(1));
</script>
<template>
  {#await p}
    pending
  {:then v}
    got: {v}
  {/await}
</template>`
	out := compileGenerated(t, src)
	// Only branches 0 (pending) and 1 (then) exist.
	if !strings.Contains(out, "=== 0)") {
		t.Errorf("missing pending branch:\n%s", out)
	}
	if !strings.Contains(out, "=== 1)") {
		t.Errorf("missing then branch:\n%s", out)
	}
	if strings.Contains(out, "=== 2)") {
		t.Errorf("unexpected catch branch:\n%s", out)
	}
}
