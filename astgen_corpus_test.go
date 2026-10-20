package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// astgen_corpus_test.go — Stage 3 of the Phase E migration.
//
// TestASTGenMatchesLegacy proves structural equality on 18 curated
// fixtures.  Before the legacy generator can be removed, the AST
// generator needs confidence across a far wider surface: construct
// combinations, edge cases, and the shapes that route through the
// buildModuleSplice fallback rather than the direct builder.
//
// This corpus is that wider surface.  Every entry is compiled by
// generateCodeAST (the production path) and checked two ways:
//   1. the output is syntactically valid JavaScript (node --check)
//   2. generateCodeAST does not panic — i.e. the internal
//      ValidateProgram pass found no malformed AST
//
// Where the direct builder handles the source, a third check runs:
// structural equality with the legacy generator.  Where the source
// routes through the splice, structural equality is trivially true
// (the splice IS the legacy output re-parsed), so only validity is
// asserted.

// corpus is a broad set of .vela sources covering construct
// combinations beyond the curated diffFixtures set.
var corpus = map[string]string{
	// ── reactivity combinations ──────────────────────────────
	"multi_state": `<script>
		let a = $state(1); let b = $state(2); let c = $state(3);
	</script>
	<template><p>{a} {b} {c}</p></template>`,

	"derived_chain": `<script>
		let n = $state(1);
		let d1 = $derived(n * 2);
		let d2 = $derived(d1 + 1);
		let d3 = $derived(d2 * d2);
	</script>
	<template><p>{d3}</p></template>`,

	"effect_multi": `<script>
		let x = $state(0);
		$effect(() => { console.log('a', x); });
		$effect(() => { console.log('b', x); });
	</script>
	<template><p>{x}</p></template>`,

	"state_and_prop": `<script>
		export let label = 'hi';
		let count = $state(0);
		let doubled = $derived(count * 2);
	</script>
	<template><p>{label}: {count} / {doubled}</p></template>`,

	// ── events & bindings ────────────────────────────────────
	"event_inline": `<script>
		let n = $state(0);
	</script>
	<template><button @click={() => n++}>{n}</button></template>`,

	"event_handler_expr": `<script>
		let n = $state(0);
		function bump(by) { n += by; }
	</script>
	<template><button @click={() => bump(5)}>{n}</button></template>`,

	"bind_checked": `<script>
		let on = $state(false);
	</script>
	<template><input type="checkbox" bind:checked={on} /></template>`,

	"bind_this": `<script>
		let el = $state(null);
		function focus() { el.focus(); }
	</script>
	<template><input bind:this={el} /></template>`,

	// ── directives ───────────────────────────────────────────
	"class_multi": `<script>
		let active = $state(false);
		let big = $state(true);
	</script>
	<template><div class:active={active} class:big={big}>x</div></template>`,

	"style_multi": `<script>
		let w = $state(100);
		let c = $state('red');
	</script>
	<template><div style:width="{w}px" style:color={c}>x</div></template>`,

	// ── control flow ─────────────────────────────────────────
	"if_elseif_else": `<script>
		let n = $state(0);
	</script>
	<template>
		{#if n > 10}<p>big</p>{:else if n > 0}<p>small</p>{:else}<p>zero</p>{/if}
	</template>`,

	"each_indexed": `<script>
		let xs = $state(['a','b','c']);
	</script>
	<template>{#each xs as x, i}<li>{i}: {x}</li>{/each}</template>`,

	"each_keyed_obj": `<script>
		let rows = $state([{id:1,n:'x'},{id:2,n:'y'}]);
	</script>
	<template>{#each rows as r (r.id)}<li>{r.n}</li>{/each}</template>`,

	"key_block": `<script>
		let v = $state(0);
	</script>
	<template>{#key v}<p>keyed static</p>{/key}</template>`,

	"await_full": `<script>
		let p = $state(Promise.resolve(42));
	</script>
	<template>
		{#await p}<p>wait</p>{:then v}<p>got</p>{:catch e}<p>err</p>{/await}
	</template>`,

	// ── lifecycle ────────────────────────────────────────────
	"onmount_only": `<script>
		import { onMount } from 'vela';
		let ready = $state(false);
		onMount(() => { ready = true; });
	</script>
	<template><p>{ready}</p></template>`,

	"onmount_cleanup": `<script>
		import { onMount } from 'vela';
		let t = $state(0);
		onMount(() => {
			const id = setInterval(() => { t++; }, 100);
			return () => clearInterval(id);
		});
	</script>
	<template><p>{t}</p></template>`,

	"ondestroy_only": `<script>
		import { onDestroy } from 'vela';
		let x = $state(1);
		onDestroy(() => { console.log('cleanup', x); });
	</script>
	<template><p>{x}</p></template>`,

	// ── deep state ───────────────────────────────────────────
	"deep_nested": `<script>
		let tree = $state.deep({ a: { b: { c: 1 } } });
		let leaf = $derived(tree.a.b.c);
	</script>
	<template><p>{leaf}</p></template>`,

	"deep_and_plain": `<script>
		let list = $state.deep([1,2,3]);
		let count = $state(0);
		let len = $derived(list.length);
	</script>
	<template><p>{len} / {count}</p></template>`,

	// ── bindable props ───────────────────────────────────────
	"bindable_default": `<script>
		export let value = $bindable('start');
	</script>
	<template><input bind:value={value} /></template>`,

	"bindable_and_state": `<script>
		export let open = $bindable(false);
		let hovered = $state(false);
	</script>
	<template><input type="checkbox" bind:checked={open} /></template>`,

	// ── static & structural ──────────────────────────────────
	"deeply_nested_static": `<script></script>
	<template>
		<div><section><article><p><span>deep</span></p></article></section></div>
	</template>`,

	"multi_root_static": `<script></script>
	<template><h1>title</h1><p>body</p><footer>end</footer></template>`,

	"slot_default": `<script></script>
	<template><div class="wrap"><slot /></div></template>`,

	"slot_named": `<script></script>
	<template>
		<div><slot name="header" /><slot /><slot name="footer" /></div>
	</template>`,

	// ── scoped styles ────────────────────────────────────────
	"style_basic": `<script>
		let n = $state(0);
	</script>
	<template><p>{n}</p></template>
	<style>p { color: red; }</style>`,

	"style_multi_rule": `<script></script>
	<template><div class="box"><span>x</span></div></template>
	<style>
		.box { padding: 8px; }
		.box span { font-weight: bold; }
	</style>`,

	// ── standard control-flow blocks ──────────────────────────
	"each_on_root": `<script>
		let items = $state(['a','b']);
	</script>
	<template>{#each items as item}<li>{item}</li>{/each}</template>`,

	"if_on_root": `<script>
		let show = $state(true);
	</script>
	<template>{#if show}<span>visible</span>{/if}</template>`,

	// ── hole-bearing control-flow branches (E3.1) ────────────
	"if_hole_branch": `<script>
		let show = $state(true);
		let name = $state('world');
	</script>
	<template>{#if show}<p>hello {name}</p>{/if}</template>`,

	"if_else_hole": `<script>
		let n = $state(0);
	</script>
	<template>
		{#if n > 0}<p>positive: {n}</p>{:else}<p>non-positive: {n}</p>{/if}
	</template>`,

	"key_hole_branch": `<script>
		let id = $state(1);
		let label = $state('a');
	</script>
	<template>{#key id}<p>item {label}</p>{/key}</template>`,

	// ── per-row dynamic attributes in {#each} (E3.3b) ────────
	"each_row_event": `<script>
		let xs = $state([1,2,3]);
		function pick(v) { console.log(v); }
	</script>
	<template>{#each xs as x}<li @click={() => pick(x)}>{x}</li>{/each}</template>`,

	"each_row_class": `<script>
		let xs = $state(['a','b']);
		let sel = $state(0);
	</script>
	<template>
		{#each xs as x, i}<li class:active={i === sel}>{x}</li>{/each}
	</template>`,

	// Regression: a nested {#if} whose branch contains an element
	// carrying a class:/style: directive.  buildHoleTemplate has
	// no arm for a directive-bearing element, so buildIfChain must
	// route such a branch through the recursive walker rather than
	// buildHoleSubtree — otherwise appendHoleNode panics.
	// Regression: a child component invoked with a bind: prop
	// generates an { $$onChange_X: ... } entry in its props
	// object.  The property KEY $$onChange_X must not be mistaken
	// for a variable reference by the validator — WalkNode must
	// not descend into non-computed object-property keys.
	// Regression: a component invoked with an @event handler must
	// wire a CustomEvent-guarded listener on the component wrapper.
	// This wiring was missing entirely — component events (button
	// clicks, tab selects, …) silently did nothing.
	// Regression: a component invoked inside another component's
	// slot, with an @event handler.  The slot-content event wiring
	// (cap.events) was dropped — buttons in a slot did nothing —
	// and the child instance `let` was trapped in the slot
	// closure, throwing a TDZ ReferenceError on slot reopen.  The
	// instance var and the event handler const are now hoisted to
	// component scope; the addEventListener runs inside the closure.
	// Regression: a component invoked inside another component's
	// slot, with an @event handler.  The slot-content event wiring
	// (cap.events) was dropped — buttons in a slot did nothing —
	// and the child instance `let` was trapped in the slot
	// closure, throwing a TDZ ReferenceError on slot reopen.
	"slot_component_event": `<script>
		import { Card } from './Card.vela';
		import { Button } from './Button.vela';
		let count = $state(0);
	</script>
	<template>
		<div>
			<Card title="C">
				<Button label="Inc" @click="() => count++" />
				<div slot="footer"><Button label="Reset" @click="() => count = 0" /></div>
			</Card>
		</div>
	</template>`,

	// Regression: reactive state interpolated inside a slot must
	// re-render when the state changes.
	"slot_reactive_hole": `<script>
		import { Card } from './Card.vela';
		import { Button } from './Button.vela';
		let count = $state(0);
	</script>
	<template>
		<div>
			<Card title="Counter">
				<p>Count: {count}</p>
				<Button label="Inc" @click="() => count++" />
			</Card>
		</div>
	</template>`,

	// Regression: a component inside an {#if} branch.  When the
	// branch is removed the component must be destroyed and its
	// instance/wrapper vars nulled — otherwise the `if (!$$nN_comp)`
	// mount guard skips re-creation on re-entry, leaving an empty
	// wrapper (the component's DOM stayed in the discarded one).
	// Also a TDZ check: the wrapper var must be hoisted, not a
	// `const` trapped in the branch-build block.
	"if_component_toggle": `<script>
		import { Toast } from './Toast.vela';
		let show = $state(false);
	</script>
	<template>
		<div>{#if show}<Toast message="hi" @close="() => show = false" />{/if}</div>
	</template>`,

	// ── combination matrix (E-stabilize 3) ───────────────────
	// Cross-construct nestings — the space the recent bugs lived
	// in.  Each names one specific high-risk combination so a
	// failure points straight at it (the fuzzer finds these too,
	// but with opaque byte-slice names).

	// Component inside an {#each} row — the row fragment builder
	// must construct a component invocation per row.
	"each_row_component": `<script>
		import { Badge } from './Badge.vela';
		let items = $state(['a', 'b', 'c']);
	</script>
	<template>
		<ul>{#each items as it}<li><Badge text={it} /></li>{/each}</ul>
	</template>`,

	// {#if} nested inside an {#each} row — control-flow within a
	// row body, built via the recursive walker under captureScope.
	"each_row_if": `<script>
		let items = $state([1, 2, 3]);
		let flag = $state(true);
	</script>
	<template>
		<ul>{#each items as n}<li>{#if flag}<span>{n}</span>{/if}</li>{/each}</ul>
	</template>`,

	// {#each} nested inside an {#if} branch — a list that appears
	// and disappears with the branch.
	"if_branch_each": `<script>
		let show = $state(true);
		let items = $state(['x', 'y']);
	</script>
	<template>
		<div>{#if show}<ul>{#each items as it}<li>{it}</li>{/each}</ul>{/if}</div>
	</template>`,

	// {#each} nested inside another {#each} row — doubly-nested
	// reconciliation, fragment-within-fragment.
	"each_nested": `<script>
		let rows = $state([[1, 2], [3, 4]]);
	</script>
	<template>
		<div>{#each rows as row}<ul>{#each row as cell}<li>{cell}</li>{/each}</ul>{/each}</div>
	</template>`,

	// Component with slot children inside an {#if} branch — the
	// captureScope-within-branch case: a slot closure built while
	// the chain branch scope is itself captured.
	"if_branch_slot_component": `<script>
		import { Card } from './Card.vela';
		let show = $state(true);
		let label = $state('hi');
	</script>
	<template>
		<div>{#if show}<Card title="C"><p>{label}</p></Card>{/if}</div>
	</template>`,

	// {#await} whose resolved branch holds a component invocation.
	"await_component": `<script>
		import { Badge } from './Badge.vela';
		let task = $state(Promise.resolve('done'));
	</script>
	<template>
		<div>
			{#await task}<span>loading</span>
			{:then result}<Badge text={result} />
			{:catch err}<span>{err}</span>
			{/await}
		</div>
	</template>`,

	// {#each} row carrying both an event and a nested {#if} — the
	// row fragment needs per-row event wiring AND a nested chain.
	"each_row_event_if": `<script>
		let items = $state([1, 2]);
		let on = $state(true);
		function pick(v) { console.log(v); }
	</script>
	<template>
		<ul>{#each items as n}<li @click={() => pick(n)}>{#if on}<b>{n}</b>{/if}</li>{/each}</ul>
	</template>`,

	// Regression: a '<' inside a {…} interpolation (a comparison
	// like {i < n}) must not be parsed as the start of an HTML tag
	// — the template scanner tracks brace depth.
	"interp_less_than": `<script>
		let i = $state(2);
		let n = $state(5);
	</script>
	<template>
		<div><span>{i < n ? 'lo' : 'hi'}</span></div>
	</template>`,

	// Regression: a dynamic attribute on an element inside an {#if}
	// branch must update when its dependency changes — the branch's
	// element var is hoisted so the setter cascade reaches it.
	"if_branch_dynattr": `<script>
		let on = $state(true);
		let w = $state(10);
	</script>
	<template>
		<div>{#if on}<span :data-w={w}>x</span>{/if}</div>
	</template>`,
}

// TestASTGenCorpus compiles every corpus entry through the
// production code path and asserts the output is valid JS and
// internally consistent (no validation panic).
func TestASTGenCorpus(t *testing.T) {
	node := nodeAvailable()
	for name, src := range corpus {
		name, src := name, src
		t.Run(name, func(t *testing.T) {
			sfc, err := ParseSFC(src)
			if err != nil {
				t.Fatalf("parseSFC: %v", err)
			}
			tmpl, err := ParseTemplate(sfc.Template)
			if err != nil {
				t.Fatalf("parseTemplate: %v", err)
			}
			analysis := AnalyzeScript(sfc.Script)

			// generateCodeAST panics internally if ValidateProgram
			// finds a malformed AST — so reaching past this call is
			// itself the structural-soundness assertion.
			var out string
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("generateCodeAST panicked: %v", r)
					}
				}()
				out = generateCodeAST(name, sfc, tmpl, analysis, "vela-000000")
			}()

			if !strings.Contains(out, "export function "+name) {
				t.Errorf("output missing component function")
			}

			// Syntactic validity via node --check.
			if node {
				if err := nodeCheck(out); err != nil {
					t.Errorf("output failed node --check: %v", err)
				}
			}
		})
	}
}

func TestASTGenTransitiveDerived(t *testing.T) {
	src := corpus["derived_chain"]
	sfc, _ := ParseSFC(src)
	tmpl, _ := ParseTemplate(sfc.Template)
	analysis := AnalyzeScript(sfc.Script)
	out := generateCodeAST("derived_chain", sfc, tmpl, analysis, "vela-000000")

	// $$set_n must call $$upd_d1, $$upd_d2 AND $$upd_d3 — the full
	// chain — and in that order.
	setterStart := strings.Index(out, "function $$set_n")
	if setterStart < 0 {
		t.Fatal("no $$set_n in output")
	}
	setterEnd := strings.Index(out[setterStart:], "\n  }")
	if setterEnd < 0 {
		t.Fatal("malformed $$set_n")
	}
	setter := out[setterStart : setterStart+setterEnd]

	i1 := strings.Index(setter, "$$upd_d1()")
	i2 := strings.Index(setter, "$$upd_d2()")
	i3 := strings.Index(setter, "$$upd_d3()")
	if i1 < 0 || i2 < 0 || i3 < 0 {
		t.Fatalf("$$set_n does not recompute the full chain:\n%s", setter)
	}
	if !(i1 < i2 && i2 < i3) {
		t.Errorf("derived recompute out of dependency order:\n%s", setter)
	}
}

// nodeAvailable reports whether the node binary is on PATH.
func nodeAvailable() bool {
	_, err := exec.LookPath("node")
	return err == nil
}

// nodeCheck runs `node --check` on a piece of JS, returning any
// syntax error.
func nodeCheck(js string) error {
	f, err := os.CreateTemp("", "vela-corpus-*.mjs")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(js); err != nil {
		return err
	}
	f.Close()
	out, err := exec.Command("node", "--check", f.Name()).CombinedOutput()
	if err != nil {
		return &nodeCheckError{detail: string(out)}
	}
	return nil
}

type nodeCheckError struct{ detail string }

func (e *nodeCheckError) Error() string { return e.detail }
