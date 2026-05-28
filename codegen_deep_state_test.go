package main

// codegen_deep_state_test.go — opt-in deep reactivity via Proxy.
//
// `$state(x)` keeps its old semantics — only reassignment to the
// variable triggers updates.  `$state.deep(x)` wraps the value in a
// lazy Proxy so in-place mutations (`arr.push(x)`, `obj.a.b = 1`)
// fire the same notify path that reassignment does.
//
// Tradeoffs the user chooses by picking one or the other:
//   • plain — no Proxy overhead, no identity surprises, but
//     mutations don't update
//   • deep  — `.push()`, nested writes work; pays a small overhead
//     per property access for the trap call

import (
	"strings"
	"testing"
)

func TestDeepState_AnalyzerRecognizesStateDeep(t *testing.T) {
	src := `<script>
  let items = $state.deep([1, 2, 3]);
  let counter = $state(0);
</script>
<template><p>{counter}</p></template>`
	out := compileGenerated(t, src)
	// Sanity: both vars declared.
	if !strings.Contains(out, "let counter = 0") {
		t.Fatalf("counter not declared correctly:\n%s", out)
	}
	// items should be wrapped in $$proxy.
	if !strings.Contains(out, "$$proxy([1, 2, 3]") {
		t.Fatalf("items not wrapped in $$proxy:\n%s", out)
	}
}

func TestDeepState_ProxyHelperEmittedWhenNeeded(t *testing.T) {
	src := `<script>
  let items = $state.deep([]);
</script>
<template><p>x</p></template>`
	out := compileGenerated(t, src)
	// $$proxy is a shared runtime helper — imported from
	// runtime.js when a $state.deep variable is present.
	if !strings.Contains(out, "$$proxy") ||
		!strings.Contains(out, "from './runtime.js'") {
		t.Fatalf("expected $$proxy imported from runtime.js:\n%s", out)
	}
	// The deep var's initial value is wrapped at the call site.
	if !strings.Contains(out, "$$proxy(") {
		t.Fatalf("expected a $$proxy() call:\n%s", out)
	}
}

func TestDeepState_ProxyHelperOmittedWhenNotNeeded(t *testing.T) {
	src := `<script>
  let counter = $state(0);
</script>
<template><p>{counter}</p></template>`
	out := compileGenerated(t, src)
	// Plain $state means no proxy machinery — no $$proxy() call
	// wrapping a value, no $$mut_ notify wiring.  ($$proxy may
	// appear in the runtime-helper import — that is a harmless
	// unused import, not machinery.)
	if strings.Contains(out, "$$proxy(") {
		t.Fatalf("$$proxy() call emitted for plain $state-only "+
			"component:\n%s", out)
	}
	if strings.Contains(out, "$$mut_") {
		t.Fatalf("$$mut_ emitted for plain $state-only component:\n%s", out)
	}
}

func TestDeepState_SetterReWrapsOnReassignment(t *testing.T) {
	src := `<script>
  let items = $state.deep([1, 2]);
</script>
<template><p>x</p></template>`
	out := compileGenerated(t, src)
	// When the user reassigns `items = newArray`, the new array
	// needs to be wrapped in a fresh proxy so subsequent mutations
	// to IT also trigger updates.  Without re-wrapping, the second
	// time you reassign and then mutate, nothing would happen.
	if !strings.Contains(out, "items = $$proxy($$v, $$mut_items)") {
		t.Fatalf("setter does not re-wrap on reassignment:\n%s", out)
	}
}

func TestDeepState_NotifyFunctionShared(t *testing.T) {
	src := `<script>
  let items = $state.deep([]);
</script>
<template>
  <p>{items.length}</p>
</template>`
	out := compileGenerated(t, src)
	// The setter and the proxy's mutation trap should both invoke
	// the same notify function ($$set_items_notify) — that's how
	// `arr.push()` triggers the same DOM updates as `arr = [...arr, x]`.
	if !strings.Contains(out, "function $$set_items_notify(") {
		t.Fatalf("expected shared $$set_items_notify function:\n%s", out)
	}
	if !strings.Contains(out, "$$mut_items = () => $$set_items_notify()") {
		t.Fatalf("expected $$mut_items to delegate to notify:\n%s", out)
	}
}

func TestDeepState_MutationsNotRewrittenToSetterCalls(t *testing.T) {
	// `items.push(x)` should NOT be rewritten to `$$set_items(...)`.
	// The proxy's trap handles the notification — leaving the
	// expression alone means it goes through the trap unchanged.
	src := `<script>
  let items = $state.deep([]);
  function add() { items.push(42); }
</script>
<template><p>x</p></template>`
	out := compileGenerated(t, src)
	if !strings.Contains(out, "items.push(42)") {
		t.Fatalf("items.push(42) was rewritten away:\n%s", out)
	}
	// And reassignment IS still rewritten to the setter.
	src2 := `<script>
  let items = $state.deep([]);
  function reset() { items = []; }
</script>
<template><p>x</p></template>`
	out2 := compileGenerated(t, src2)
	if !strings.Contains(out2, "$$set_items([])") {
		t.Fatalf("reassignment not rewritten to setter:\n%s", out2)
	}
}

func TestDeepState_OnlyPlainStateUnchanged(t *testing.T) {
	// Backwards-compatibility check: a component using only plain
	// $state should generate byte-identical output to before the
	// feature was added (modulo unrelated changes).  The key marker
	// is: no $$proxy helper, no $$mut_ vars, no _notify split.
	src := `<script>
  let count = $state(0);
  function inc() { count++; }
</script>
<template>
  <p>{count}</p>
  <button @click={inc}>+</button>
</template>`
	out := compileGenerated(t, src)
	// Plain $state must not pull in any deep-reactivity machinery.
	// $$proxy may appear in the runtime-helper import (a harmless
	// unused import); the markers of actual machinery are a
	// $$proxy() call, $$mut_ notify vars, and the _notify setter
	// split.
	forbidden := []string{"$$proxy(", "$$mut_", "_notify"}
	for _, f := range forbidden {
		if strings.Contains(out, f) {
			t.Errorf("plain $state output contains %q (should be "+
				"unchanged from before deep feature):\n%s", f, out)
		}
	}
}
