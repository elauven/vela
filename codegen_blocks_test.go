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

// ─────────────────────────────────────────────
//  {#key}
// ─────────────────────────────────────────────

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

// ─────────────────────────────────────────────
//  {#await}
// ─────────────────────────────────────────────

// ─────────────────────────────────────────────
//  Branch cleanup-closure mechanism
// ─────────────────────────────────────────────

func TestParseTemplate_AwaitInlineBranches(t *testing.T) {
	tests := []struct {
		src       string
		valueName string
		errorName string
	}{
		{"{#await request then result}<p>{result}</p>{/await}", "result", ""},
		{"{#await request catch failure}<p>{failure.message}</p>{/await}", "", "failure"},
	}
	for _, tc := range tests {
		nodes, err := parseTemplate(tc.src)
		if err != nil {
			t.Fatalf("parseTemplate(%q): %v", tc.src, err)
		}
		await, ok := nodes[0].(*AwaitBlockNode)
		if !ok {
			t.Fatalf("got %T, want AwaitBlockNode", nodes[0])
		}
		if await.PromiseExpr != "request" || await.Expr.Pos != len("{#await ") {
			t.Fatalf("promise = %q at %d, want request at %d", await.PromiseExpr, await.Expr.Pos, len("{#await "))
		}
		if len(await.PendingChildren) != 0 || await.ValueName != tc.valueName || await.ErrorName != tc.errorName {
			t.Fatalf("await shape = pending:%d value:%q error:%q", len(await.PendingChildren), await.ValueName, await.ErrorName)
		}
		if tc.valueName != "" && len(await.ThenChildren) != 1 || tc.errorName != "" && len(await.CatchChildren) != 1 {
			t.Fatalf("inline branch body was not assigned to its declared branch")
		}
	}

	nodes, err := parseTemplate("{#await Promise.resolve({ then: 1 }) then value}{value}{/await}")
	if err != nil {
		t.Fatal(err)
	}
	await := nodes[0].(*AwaitBlockNode)
	if await.PromiseExpr != "Promise.resolve({ then: 1 })" {
		t.Fatalf("top-level split corrupted promise expression: %q", await.PromiseExpr)
	}
}

func TestParseTemplate_AwaitBindingDiagnostics(t *testing.T) {
	for _, src := range []string{
		"{#await p then}<p>x</p>{/await}",
		"{#await p catch error.message}<p>x</p>{/await}",
		"{#await p}{:then result.value}<p>x</p>{/await}",
		"{#await p}{:catch}<p>x</p>{/await}",
		"{#await p then value}<p>{value}</p>{:catch error}<p>{error}</p>{/await}",
		"{#await p}{:then value}<p>{value}</p>{:then duplicate}<p>{duplicate}</p>{/await}",
	} {
		if _, err := parseTemplate(src); err == nil {
			t.Errorf("parseTemplate(%q) succeeded, want await diagnostic", src)
		}
	}
}
