package codegen

// codegen_component_bind_test.go — bug 13: <Child bind:x={y}> was
// emitting only the parent→child push (`{ x: y }`), never the
// child→parent callback (`$$onChange_x: ($v) => $$set_y($v)`).
// Plus the necessary cycle-break in setters so the resulting
// round-trip doesn't infinite-loop.

import (
	"strings"
	"testing"
)

// ─────────────────────────────────────────────
//  Callback emission
// ─────────────────────────────────────────────

// ─────────────────────────────────────────────
//  Cycle break in setters
// ─────────────────────────────────────────────

func TestBug13_BindOnComponentEmitsOnChangeCallback(t *testing.T) {
	src := `<script>
  let appCount = $state(5);
</script>
<template>
  <Counter bind:count={appCount} />
</template>`
	out := compileGenerated(t, src)
	// Must emit both the one-way push AND the callback.
	if !strings.Contains(out, "count: appCount") {
		t.Fatalf("expected one-way push `count: appCount`:\n%s", out)
	}
	if !strings.Contains(out, "$$onChange_count: ($$v) => $$set_appCount($$v)") {
		t.Fatalf("expected $$onChange_count callback wired to parent setter:\n%s", out)
	}
}

func TestBug13_PlainColonBindNoCallback(t *testing.T) {
	// `:count={appCount}` is one-way only — no callback expected.
	src := `<script>
  let appCount = $state(5);
</script>
<template>
  <Counter :count={appCount} />
</template>`
	out := compileGenerated(t, src)
	if !strings.Contains(out, "count: appCount") {
		t.Fatalf("expected one-way push:\n%s", out)
	}
	if strings.Contains(out, "$$onChange_count") {
		t.Fatalf("plain `:count=` should NOT emit a callback:\n%s", out)
	}
}

func TestBug13_BindOnNonStateSkipsCallback(t *testing.T) {
	// bind:count={someExpr} where someExpr isn't a $state var has
	// nowhere to write back — we emit the push but skip the callback.
	src := `<script>
  let obj = $state({ count: 5 });
</script>
<template>
  <Counter bind:count={obj.count} />
</template>`
	out := compileGenerated(t, src)
	if !strings.Contains(out, "count: obj.count") {
		t.Fatalf("expected one-way push:\n%s", out)
	}
	// `obj.count` isn't a $state name on its own, so no callback.
	if strings.Contains(out, "$$onChange_count") {
		t.Fatalf("non-state bind target should not get a callback:\n%s", out)
	}
}

func TestBug13_SetterGuardsAgainstNoOpWrites(t *testing.T) {
	src := `<script>
  let x = $state(0);
</script>
<template>
  <p>{x}</p>
</template>`
	out := compileGenerated(t, src)
	// Every generated setter must early-return on same-value writes.
	// Without this, child→parent→child two-way binding would loop.
	// The guard is proxy-aware now (a deep proxy that mutated in
	// place keeps its identity, so it must fall through), but the
	// identity-equality check that breaks the bind loop is still
	// present.
	if !strings.Contains(out, "if (x === $$v && !$$isProxy($$v))") {
		t.Fatalf("expected proxy-aware `if (x === $$v && !$$isProxy($$v)) return` guard in setter:\n%s", out)
	}
}

func TestBug13_SetterGuardForPropAlsoEmitted(t *testing.T) {
	// Props use the same setter shape — they also need the guard,
	// otherwise the bindable round-trip wouldn't terminate.
	src := `<script>
  export let count = $bindable(0);
</script>
<template>
  <p>{count}</p>
</template>`
	out := compileGenerated(t, src)
	if !strings.Contains(out, "if (count === $$v && !$$isProxy($$v))") {
		t.Fatalf("expected proxy-aware guard in $bindable setter:\n%s", out)
	}
	// And the $$onChange_count reader.
	if !strings.Contains(out, "$$props.$$onChange_count") {
		t.Fatalf("expected $$onChange_count read from props:\n%s", out)
	}
}
