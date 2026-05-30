package main

// runtime_behaviour_test.go — E-stabilize 1.
//
// Compiles a .vela source through the AST generator, loads the
// generated JS in a jsdom document via tools/runtime-harness.mjs,
// and asserts on BEHAVIOUR — initial render, reactive updates,
// {#if}/{#each} toggles, component events, teardown.
//
// The differential harness (astgen_diff_test.go) checks syntax and
// structure; it cannot catch a runtime fault — a TDZ error, a dead
// listener, content that fails to re-render.  Every such bug found
// against real projects passed `node --check` and structural
// equality.  This harness closes that gap.
//
// Requires node + jsdom.  When jsdom cannot be located the test
// SKIPS (it does not fail) so the suite stays green in minimal
// environments.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// rtStep is one harness step — see tools/runtime-harness.mjs for the
// recognised shapes.  Marshalled straight to the steps JSON file.
type rtStep map[string]any

// rtResult is the harness's stdout JSON.
type rtResult struct {
	OK       bool     `json:"ok"`
	Failures []string `json:"failures"`
}

// findNodeModules locates a node_modules directory containing jsdom,
// searching the cwd upward plus a couple of common spots.  Returns
// "" when none is found.
func findNodeModules() string {
	candidates := []string{}
	if wd, err := os.Getwd(); err == nil {
		dir := wd
		for i := 0; i < 6; i++ {
			candidates = append(candidates, dir)
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	candidates = append(candidates, "/tmp", os.TempDir())
	for _, base := range candidates {
		if base == "" {
			continue
		}
		if st, err := os.Stat(filepath.Join(base,
			"node_modules", "jsdom")); err == nil && st.IsDir() {
			return base
		}
	}
	return ""
}

// runRuntimeHarness compiles a set of components, writes each to its
// own JS file in a shared dir (so cross-component imports resolve),
// and runs the jsdom harness against the entry component with the
// given steps.  t.Skip on a missing toolchain; t.Fatal on a
// harness/compile error; returns the result to assert on.
//
// components maps component name → .vela source.  entry names the
// component to mount.  Generated imports use './<Name>.js', so each
// component is also written under that exact name.
func runRuntimeHarness(t *testing.T, entry string,
	components map[string]string, steps []rtStep) rtResult {
	t.Helper()

	nodeBin, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available — skipping runtime-behaviour test")
	}
	nodeModules := findNodeModules()
	if nodeModules == "" {
		t.Skip("jsdom not available — skipping runtime-behaviour test")
	}
	harness := "tools/runtime-harness.mjs"
	if _, err := os.Stat(harness); err != nil {
		t.Fatalf("harness script missing: %v", err)
	}
	if _, ok := components[entry]; !ok {
		t.Fatalf("entry component %q not in component set", entry)
	}

	dir := t.TempDir()

	// Generated components import the shared helpers from
	// ./runtime.js, so the runtime must sit alongside them in the
	// temp dir.  Copy the repo's runtime.js in.
	rt, err := os.ReadFile("runtime.js")
	if err != nil {
		t.Fatalf("cannot read runtime.js: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "runtime.js"),
		rt, 0o644); err != nil {
		t.Fatalf("cannot stage runtime.js: %v", err)
	}

	// Compile every component through the AST generator.  Each is
	// written twice: <Name>.js so cross-component imports
	// (import … from './<Name>.js') resolve, and the entry also as
	// <Name>.mjs so node treats the entry as an ES module without
	// needing a package.json.
	var entryPath string
	for name, src := range components {
		sfc, err := parseSFC(src)
		if err != nil {
			t.Fatalf("[%s] parseSFC: %v", name, err)
		}
		tmpl, err := parseTemplate(sfc.Template)
		if err != nil {
			t.Fatalf("[%s] parseTemplate: %v", name, err)
		}
		analysis := analyzeScript(sfc.Script)
		js := generateCodeAST(name, sfc, tmpl, analysis, "vela-000000")

		if err := os.WriteFile(filepath.Join(dir, name+".js"),
			[]byte(js), 0o644); err != nil {
			t.Fatalf("write %s.js: %v", name, err)
		}
		if name == entry {
			entryPath = filepath.Join(dir, name+".mjs")
			if err := os.WriteFile(entryPath,
				[]byte(js), 0o644); err != nil {
				t.Fatalf("write %s.mjs: %v", name, err)
			}
		}
	}

	stepsJSON, err := json.Marshal(steps)
	if err != nil {
		t.Fatalf("marshal steps: %v", err)
	}
	stepsPath := filepath.Join(dir, "steps.json")
	if err := os.WriteFile(stepsPath, stepsJSON, 0o644); err != nil {
		t.Fatalf("write steps: %v", err)
	}

	cmd := exec.Command(nodeBin, harness, entryPath, entry, stepsPath)
	cmd.Env = append(os.Environ(), "VELA_NODE_MODULES="+nodeModules)
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("[%s] harness run failed: %v\n%s", entry, err, stderr)
	}

	var res rtResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("[%s] cannot parse harness output: %v\nraw: %s",
			entry, err, out)
	}
	return res
}

// assertRuntime runs the harness for a single-component test.
func assertRuntime(t *testing.T, name, src string, steps []rtStep) {
	t.Helper()
	assertRuntimeSet(t, name,
		map[string]string{name: src}, steps)
}

// assertRuntimeSet runs the harness for a multi-component test and
// fails on any step failure, printing each one for triage.
func assertRuntimeSet(t *testing.T, entry string,
	components map[string]string, steps []rtStep) {
	t.Helper()
	res := runRuntimeHarness(t, entry, components, steps)
	if !res.OK {
		for _, f := range res.Failures {
			t.Errorf("[%s] %s", entry, f)
		}
	}
}

// ── tests ────────────────────────────────────────────────────────

// TestRuntimeCounter — the canonical reactive case: a click handler
// increments state and the bound text re-renders.
func TestRuntimeCounter(t *testing.T) {
	src := `<script>let count = $state(0);</script>
<template><div><button @click={() => count++}>{count}</button></div></template>`
	assertRuntime(t, "Counter", src, []rtStep{
		{"assertText": "button", "equals": "0"},
		{"click": "button"},
		{"assertText": "button", "equals": "1"},
		{"click": "button"},
		{"assertText": "button", "equals": "2"},
	})
}

// TestRuntimeIfToggle — an {#if} whose condition flips: content must
// appear, disappear, and reappear (the reopen path).
func TestRuntimeIfToggle(t *testing.T) {
	src := `<script>let show = $state(false);</script>
<template><div>
  <button @click={() => show = !show}>toggle</button>
  {#if show}<p class="msg">visible</p>{/if}
</div></template>`
	assertRuntime(t, "IfToggle", src, []rtStep{
		{"assertCount": "p.msg", "equals": 0},
		{"click": "button"},
		{"assertCount": "p.msg", "equals": 1},
		{"assertText": "p.msg", "equals": "visible"},
		{"click": "button"},
		{"assertCount": "p.msg", "equals": 0},
		{"click": "button"}, // reopen — the class of bugs we hit
		{"assertCount": "p.msg", "equals": 1},
		{"assertText": "p.msg", "equals": "visible"},
	})
}

// TestRuntimeEachRender — an {#each} list renders one node per item
// and re-renders when the array changes.
func TestRuntimeEachRender(t *testing.T) {
	src := `<script>
  let items = $state(['a', 'b', 'c']);
</script>
<template><div>
  <button @click={() => items = [...items, 'x']}>add</button>
  <ul>{#each items as it}<li class="row">{it}</li>{/each}</ul>
</div></template>`
	assertRuntime(t, "EachRender", src, []rtStep{
		{"assertCount": "li.row", "equals": 3},
		{"click": "button"},
		{"assertCount": "li.row", "equals": 4},
	})
}

// TestRuntimeSlotComponentEvent — a component placed in another
// component's slot, whose @click mutates parent state.  Exercises
// the captureScope boundary: the slot-content event listener and
// the reactive hole update must both survive into the closure.
// This is the bug class hit against the real Card/Button project.
func TestRuntimeSlotComponentEvent(t *testing.T) {
	box := `<script></script>
<template><section class="box"><slot /></section></template>`
	app := `<script>
  import { Box } from './Box.js';
  let count = $state(0);
</script>
<template><div>
  <p class="out">{count}</p>
  <Box><button class="b" @click={() => count++}>inc</button></Box>
</div></template>`
	assertRuntimeSet(t, "App", map[string]string{
		"App": app,
		"Box": box,
	}, []rtStep{
		{"assertText": "p.out", "equals": "0"},
		{"assertCount": "section.box button.b", "equals": 1},
		{"click": "button.b"},
		{"assertText": "p.out", "equals": "1"},
		{"click": "button.b"},
		{"assertText": "p.out", "equals": "2"},
	})
}

// TestRuntimeIfHole — an {#if} branch carrying a reactive hole: the
// hole must show the current value when the branch (re)builds.
func TestRuntimeIfHole(t *testing.T) {
	src := `<script>
  let show = $state(true);
  let label = $state('first');
</script>
<template><div>
  <button class="t" @click={() => show = !show}>toggle</button>
  {#if show}<p class="msg">value {label}</p>{/if}
</div></template>`
	assertRuntime(t, "IfHole", src, []rtStep{
		{"assertText": "p.msg", "equals": "value first"},
		{"click": "button.t"},
		{"assertCount": "p.msg", "equals": 0},
		{"click": "button.t"},
		{"assertText": "p.msg", "equals": "value first"},
	})
}

// TestRuntimeComponentInIf — a component inside an {#if} branch:
// toggling the branch off then on must destroy and recreate the
// component so its content reappears (the empty-on-reopen bug).
func TestRuntimeComponentInIf(t *testing.T) {
	badge := `<script>
  export let text = '';
</script>
<template><span class="badge">{text}</span></template>`
	app := `<script>
  import { Badge } from './Badge.js';
  let show = $state(false);
</script>
<template><div>
  <button class="t" @click={() => show = !show}>toggle</button>
  {#if show}<Badge text="hello" />{/if}
</div></template>`
	assertRuntimeSet(t, "App", map[string]string{
		"App":   app,
		"Badge": badge,
	}, []rtStep{
		{"assertCount": "span.badge", "equals": 0},
		{"click": "button.t"},
		{"assertCount": "span.badge", "equals": 1},
		{"assertText": "span.badge", "equals": "hello"},
		{"click": "button.t"},
		{"assertCount": "span.badge", "equals": 0},
		{"click": "button.t"}, // reopen — content must reappear
		{"assertCount": "span.badge", "equals": 1},
		{"assertText": "span.badge", "equals": "hello"},
	})
}

// ── combination-matrix runtime tests (E-stabilize 3) ─────────────
// Behavioural checks for the cross-construct nestings — the curated
// corpus confirms they COMPILE; these confirm they WORK.

// TestRuntimeEachNested — a doubly-nested {#each}: each outer row
// renders an inner list, and the totals must be right.
func TestRuntimeEachNested(t *testing.T) {
	src := `<script>
  let rows = $state([[1, 2], [3, 4, 5]]);
</script>
<template><div>
  {#each rows as row}<ul class="r">{#each row as cell}<li class="c">{cell}</li>{/each}</ul>{/each}
</div></template>`
	assertRuntime(t, "EachNested", src, []rtStep{
		{"assertCount": "ul.r", "equals": 2},
		{"assertCount": "li.c", "equals": 5}, // 2 + 3
	})
}

// TestRuntimeEachRowIf — an {#if} inside an {#each} row: every row
// shows its content while the shared flag is true, none when false.
func TestRuntimeEachRowIf(t *testing.T) {
	src := `<script>
  let items = $state([1, 2, 3]);
  let flag = $state(true);
</script>
<template><div>
  <button class="t" @click={() => flag = !flag}>toggle</button>
  <ul>{#each items as n}<li>{#if flag}<span class="v">{n}</span>{/if}</li>{/each}</ul>
</div></template>`
	assertRuntime(t, "EachRowIf", src, []rtStep{
		{"assertCount": "span.v", "equals": 3},
		{"click": "button.t"},
		{"assertCount": "span.v", "equals": 0},
		{"click": "button.t"},
		{"assertCount": "span.v", "equals": 3},
	})
}

// TestRuntimeIfBranchEach — an {#each} list inside an {#if} branch:
// the list appears and disappears with the branch, intact on reopen.
func TestRuntimeIfBranchEach(t *testing.T) {
	src := `<script>
  let show = $state(true);
  let items = $state(['x', 'y']);
</script>
<template><div>
  <button class="t" @click={() => show = !show}>toggle</button>
  {#if show}<ul>{#each items as it}<li class="row">{it}</li>{/each}</ul>{/if}
</div></template>`
	assertRuntime(t, "IfBranchEach", src, []rtStep{
		{"assertCount": "li.row", "equals": 2},
		{"click": "button.t"},
		{"assertCount": "li.row", "equals": 0},
		{"click": "button.t"}, // reopen
		{"assertCount": "li.row", "equals": 2},
	})
}

// TestRuntimeEachRowComponent — a component invoked once per
// {#each} row, each receiving a per-row prop.
func TestRuntimeEachRowComponent(t *testing.T) {
	badge := `<script>
  export let text = '';
</script>
<template><span class="badge">{text}</span></template>`
	app := `<script>
  import { Badge } from './Badge.js';
  let items = $state(['a', 'b', 'c']);
</script>
<template><ul>{#each items as it}<li><Badge text={it} /></li>{/each}</ul></template>`
	assertRuntimeSet(t, "App", map[string]string{
		"App":   app,
		"Badge": badge,
	}, []rtStep{
		{"assertCount": "span.badge", "equals": 3},
	})
}

// TestRuntimeEffectReadsDerived — an $effect that reads a $derived
// must re-run when the derived's UPSTREAM $state changes, not only
// when a variable it reads directly changes.  Regression: a typed
// edit to `text` updates `doubled = $derived(text+text)`, and the
// effect that mirrors `doubled` into the DOM must re-run.
func TestRuntimeEffectReadsDerived(t *testing.T) {
	src := `<script>
  let text = $state('a');
  let doubled = $derived(text + text);
  let out = $state(null);
  $effect(() => {
    if (out) out.textContent = doubled;
  });
</script>
<template><div>
  <button @click={() => text = text + 'a'}>add</button>
  <p bind:this={out}></p>
</div></template>`
	assertRuntime(t, "EffectDerived", src, []rtStep{
		{"assertText": "p", "equals": "aa"},
		{"click": "button"},
		{"assertText": "p", "equals": "aaaa"},
		{"click": "button"},
		{"assertText": "p", "equals": "aaaaaa"},
	})
}

// TestRuntimeIfBranchDynamicAttr — a dynamic attribute on an element
// inside an {#if} branch must update when its reactive dependency
// changes, not only on the initial branch build.  Regression: the
// branch's reactive updates (cap.updates) were discarded, so e.g.
// a progress-bar width set via :style only rendered once.
func TestRuntimeIfBranchDynamicAttr(t *testing.T) {
	src := `<script>
  let pct = $state(10);
  let on = $state(true);
</script>
<template><div>
  <button @click={() => pct = pct + 10}>add</button>
  {#if on}<span class="bar" :data-w={pct}>x</span>{/if}
</div></template>`
	assertRuntime(t, "IfBranchAttr", src, []rtStep{
		{"assertHTML": "span.bar", "contains": "x"},
		{"click": "button"},
		// the data-w attribute must reflect the new value
		{"assertCount": "span.bar[data-w='20']", "equals": 1},
		{"click": "button"},
		{"assertCount": "span.bar[data-w='30']", "equals": 1},
	})
}

// TestRuntimeEachInsideIfBranch — a {#each} nested inside an {#if}
// branch (built by the recursive walker) must have its reconciler
// and block-storage HOISTED to component scope, so the setter
// cascade can re-invoke the reconciler when the list changes.
// Regression for "ReferenceError: $$each_$$nN is not defined" —
// the reconciler used to be trapped inside the branch-build block.
func TestRuntimeEachInsideIfBranch(t *testing.T) {
	src := `<script>
  let items = $state([]);
  let show = $state(true);
</script>
<template><div>
  <button class="add" @click={() => items = [...items, items.length]}>add</button>
  {#if show}
    <ul>{#each items as n}<li class="row">{n}</li>{/each}</ul>
  {:else}
    <p>hidden</p>
  {/if}
</div></template>`
	assertRuntime(t, "EachInIf", src, []rtStep{
		{"assertCount": "li.row", "equals": 0},
		{"click": "button.add"},
		{"assertCount": "li.row", "equals": 1},
		{"click": "button.add"},
		{"assertCount": "li.row", "equals": 2},
	})
}

// TestRuntimeHoleInIfBranchWithNestedEach — a hole ({def.x}) inside
// an {#if} branch that ALSO contains a nested {#each} forces the
// recursive-walker branch path.  The hole's text-node var must be
// hoisted to component scope so the setter cascade can update it.
// Regression for "ReferenceError: $$nN is not defined" in a
// prop-setter (Inspector.set_block in the PageCraft app): buildChildren
// hoisted hole vars for slot scope but not for {#if}-branch scope.
func TestRuntimeHoleInIfBranchWithNestedEach(t *testing.T) {
	src := `<script>
  let sel = $state(null);
  let label = $derived(sel ? sel.name : '');
</script>
<template><div>
  <button class="pick" @click={() => sel = { name: 'chosen', tags: ['a','b'] }}>pick</button>
  {#if sel}
    <div class="info">
      <span class="lbl">{label}</span>
      <ul>{#each sel.tags as t}<li class="tag">{t}</li>{/each}</ul>
    </div>
  {:else}
    <p>none</p>
  {/if}
</div></template>`
	assertRuntime(t, "HoleInIfEach", src, []rtStep{
		{"assertCount": "span.lbl", "equals": 0},
		{"click": "button.pick"},
		// the hole reading the $derived must populate
		{"assertText": "span.lbl", "equals": "chosen"},
		// and the nested {#each} must render
		{"assertCount": "li.tag", "equals": 2},
	})
}

// TestRuntimeIfToggleOffWithNullDerived — when an {#if}'s condition
// flips off, the chain teardown must run BEFORE the branch's
// interior updates, so an interior expression that reads a now-null
// derived is never evaluated.  Regression for "TypeError: def is
// null" in the PageCraft Inspector: set_block ran $$each(def.fields)
// before the {#if block} chain could unmount the branch.
func TestRuntimeIfToggleOffWithNullDerived(t *testing.T) {
	src := `<script>
  let sel = $state({ name: 'a', fields: ['x', 'y'] });
  let def = $derived(sel ? sel : null);
</script>
<template><div>
  <button class="clear" @click={() => sel = null}>clear</button>
  {#if sel}
    <div class="info">
      <span class="nm">{def.name}</span>
      <ul>{#each def.fields as f}<li class="f">{f}</li>{/each}</ul>
    </div>
  {/if}
</div></template>`
	assertRuntime(t, "IfOffNullDerived", src, []rtStep{
		{"assertText": "span.nm", "equals": "a"},
		{"assertCount": "li.f", "equals": 2},
		// Clearing sel makes def null AND removes the {#if} branch.
		// The teardown must precede the interior updates — no throw.
		{"click": "button.clear"},
		{"assertCount": "span.nm", "equals": 0},
		{"assertCount": "li.f", "equals": 0},
	})
}

// TestRuntimeDeepMutationRerendersEach — a $state.deep list rendered
// with {#each}; a button mutates a nested property IN PLACE (the
// reference is unchanged).  This must re-render the affected row and
// must NOT leave a duplicate node behind.  Two bugs combined to
// break this in the PageCraft app (editing a block field):
//   1. the setter's identity guard (X === $$v) suppressed the
//      re-render because the deep proxy keeps its reference across
//      an in-place mutation — fixed by the proxy-aware guard
//      (X === $$v && !$$isProxy($$v));
//   2. the {#each}-inside-{#if} branch handle captured the each
//      rows, so on re-render the chain re-attached stale originals
//      alongside the rebuilt rows (duplicated DOM) — fixed by
//      emitting the each's initial call after the branch snapshot.
func TestRuntimeDeepMutationRerendersEach(t *testing.T) {
	src := `<script>
  let items = $state.deep([{ id: 1, label: 'a' }, { id: 2, label: 'b' }]);
  let n = $state(0);
  function bump() {
    n++;
    items[0].label = 'EDITED' + n;   // in-place nested mutation
  }
</script>
<template><div>
  <button class="b" @click={bump}>bump</button>
  {#if items.length}
    <ul>{#each items as it}<li class="row">{it.label}</li>{/each}</ul>
  {/if}
</div></template>`
	assertRuntime(t, "DeepMutEach", src, []rtStep{
		{"assertCount": "li.row", "equals": 2},
		{"assertText": "li.row", "equals": "a"}, // first row
		{"click": "button.b"},
		// re-rendered in place — and crucially still exactly 2 rows,
		// not 3+ (no duplicated/stale node left behind).
		{"assertCount": "li.row", "equals": 2},
		{"assertText": "li.row", "equals": "EDITED1"},
		{"click": "button.b"},
		{"assertCount": "li.row", "equals": 2},
		{"assertText": "li.row", "equals": "EDITED2"},
	})
}

// TestRuntimeTextareaChildInterpolation — <textarea>{value}</textarea>
// uses the textarea's raw-text content as its value.  The compiler
// must write it to the .value PROPERTY, not via the innerHTML +
// TreeWalker hole mechanism (a <textarea> is a CDATA element, so
// innerHTML='<!--$-->' stores literal text and the hole anchor is
// null → "TypeError: $$nN_a is null").
func TestRuntimeTextareaChildInterpolation(t *testing.T) {
	src := `<script>
  let msg = $state('hello');
</script>
<template><div>
  <button class="b" @click={() => msg = 'changed'}>go</button>
  <textarea class="ta">{msg}</textarea>
</template></div>`
	assertRuntime(t, "TextareaChild", src, []rtStep{
		{"assertValue": "textarea.ta", "equals": "hello"},
		{"click": "button.b"},
		{"assertValue": "textarea.ta", "equals": "changed"},
	})
}

// TestRuntimeTextareaChildInIfBranch — the same, but nested inside
// an {#if} branch (the PageCraft Field shape), which builds via the
// recursive element walker rather than the root path.
func TestRuntimeTextareaChildInIfBranch(t *testing.T) {
	src := `<script>
  let show = $state(true);
  let msg = $state('a');
</script>
<template><div>
  <button class="t" @click={() => msg = 'b'}>edit</button>
  {#if show}
    <textarea class="ta">{msg}</textarea>
  {/if}
</div></template>`
	assertRuntime(t, "TextareaInIf", src, []rtStep{
		{"assertValue": "textarea.ta", "equals": "a"},
		{"click": "button.t"},
		{"assertValue": "textarea.ta", "equals": "b"},
	})
}

// TestRuntimeUnkeyedEachPreservesFocus — Session A keystone.
//
// A parent renders an unkeyed {#each} of child components, each
// containing an <input>.  We focus one of the inputs, then fire a
// reactive change that re-runs the each WITH AN UNCHANGED ARRAY
// reference and length.  The expected behaviour: the row's update
// closure propagates new props to the existing child via
// inst.set(propsObj), the DOM nodes are reused, and the focused
// input retains focus.  Before Session A the each would destroy
// and rebuild every row on any such re-render — replacing the
// <input> and dropping focus.
func TestRuntimeUnkeyedEachPreservesFocus(t *testing.T) {
	row := `<script>
  export let item;
  export let stamp = 0;
</script>
<template><div class="r">
  <input class="in" />
  <span class="lbl">{item.label}|{stamp}</span>
</div></template>`
	app := `<script>
  import Row from './Row.vela';
  let items = $state([{id:1, label:'a'}, {id:2, label:'b'}]);
  let stamp = $state(0);
</script>
<template><div>
  <button class="b" @click={() => stamp++}>bump</button>
  {#each items as it}<Row item={it} stamp={stamp} />{/each}
</div></template>`
	assertRuntimeSet(t, "App", map[string]string{
		"App": app,
		"Row": row,
	}, []rtStep{
		{"assertCount": "div.r", "equals": 2},
		{"assertText": "div.r .lbl", "equals": "a|0"},
		// Focus an input — bumping stamp re-runs the each.
		{"focus": "div.r:nth-of-type(1) input.in"},
		{"click": "button.b"},
		// The label updated, proving props propagated…
		{"assertText": "div.r .lbl", "equals": "a|1"},
		// …AND focus survived the re-render (the row was NOT torn
		// down and rebuilt).  This is the keystone Session A
		// assertion — it fails on the old destroy-and-rebuild
		// path because the focused <input> would have been
		// replaced with a freshly-mounted one.
		{"assertFocused": "div.r:nth-of-type(1) input.in"},
	})
}

// TestRuntimeUnkeyedEachAppendInPlace — Session B.
//
// Pushing to a $state.deep array (mutation, same reference) appends
// ONE row without disturbing the existing ones.  We focus an input
// in the first existing row beforehand; after the append, that
// input still has focus AND the appended row is present.  Before
// Session B, growing the array via in-place mutation also went
// through destroy+rebuild.
func TestRuntimeUnkeyedEachAppendInPlace(t *testing.T) {
	src := `<script>
  let items = $state.deep([{label:'a'}, {label:'b'}]);
</script>
<template><div>
  <button class="add" @click={() => items.push({label:'c'})}>add</button>
  {#each items as it}<div class="r"><input class="in" /><span class="lbl">{it.label}</span></div>{/each}
</div></template>`
	assertRuntime(t, "EachAppend", src, []rtStep{
		{"assertCount": "div.r", "equals": 2},
		{"focus": "div.r:nth-of-type(1) input.in"},
		{"click": "button.add"},
		{"assertCount": "div.r", "equals": 3},
		{"assertText": "div.r:nth-of-type(3) .lbl", "equals": "c"},
		// Focus on the row that existed before the append survives.
		{"assertFocused": "div.r:nth-of-type(1) input.in"},
	})
}

// TestRuntimeUnkeyedEachRemoveInPlace — Session B.
//
// Pop from a $state.deep array (mutation, same reference) removes
// the LAST row.  The remaining rows are not destroyed; focus in a
// surviving row is preserved.
func TestRuntimeUnkeyedEachRemoveInPlace(t *testing.T) {
	src := `<script>
  let items = $state.deep([{label:'a'}, {label:'b'}, {label:'c'}]);
</script>
<template><div>
  <button class="pop" @click={() => items.pop()}>pop</button>
  {#each items as it}<div class="r"><input class="in" /><span class="lbl">{it.label}</span></div>{/each}
</div></template>`
	assertRuntime(t, "EachRemove", src, []rtStep{
		{"assertCount": "div.r", "equals": 3},
		{"focus": "div.r:nth-of-type(1) input.in"},
		{"click": "button.pop"},
		{"assertCount": "div.r", "equals": 2},
		{"assertText": "div.r:nth-of-type(2) .lbl", "equals": "b"},
		{"assertFocused": "div.r:nth-of-type(1) input.in"},
	})
}

// TestRuntimeUnkeyedEachAppendRemoveRoundTrip — Session B.
//
// Push then pop: array returns to original length with original
// contents.  All rows alive throughout.  This is the "the fast
// path doesn't leak" check — surplus destroy and tail-append
// must both behave correctly.
func TestRuntimeUnkeyedEachAppendRemoveRoundTrip(t *testing.T) {
	src := `<script>
  let items = $state.deep(['a','b']);
</script>
<template><div>
  <button class="add" @click={() => items.push('c')}>add</button>
  <button class="pop" @click={() => items.pop()}>pop</button>
  {#each items as it}<li class="row">{it}</li>{/each}
</div></template>`
	assertRuntime(t, "EachRoundTrip", src, []rtStep{
		{"assertCount": "li.row", "equals": 2},
		{"click": "button.add"},
		{"assertCount": "li.row", "equals": 3},
		{"assertText": "li.row:nth-of-type(3)", "equals": "c"},
		{"click": "button.pop"},
		{"assertCount": "li.row", "equals": 2},
		{"assertText": "li.row:nth-of-type(2)", "equals": "b"},
		{"click": "button.pop"},
		{"assertCount": "li.row", "equals": 1},
		{"click": "button.pop"},
		{"assertCount": "li.row", "equals": 0},
		{"click": "button.add"},
		{"assertCount": "li.row", "equals": 1},
		{"assertText": "li.row", "equals": "c"},
	})
}

// TestRuntimeUnkeyedEachInBranchAppend — Session B + branch interplay.
//
// Reopening a previously-hidden {#if} branch resets _arr tracking
// (via the isConnected fallback), so the next append must work
// correctly after that reset.  Sequence: visible(2) → hide → show →
// append → 3 rows.
func TestRuntimeUnkeyedEachInBranchAppend(t *testing.T) {
	src := `<script>
  let show = $state(true);
  let items = $state.deep(['a','b']);
</script>
<template><div>
  <button class="t" @click={() => show = !show}>toggle</button>
  <button class="add" @click={() => items.push('c')}>add</button>
  {#if show}<ul>{#each items as it}<li class="row">{it}</li>{/each}</ul>{/if}
</div></template>`
	assertRuntime(t, "EachInBranchAppend", src, []rtStep{
		{"assertCount": "li.row", "equals": 2},
		{"click": "button.t"}, // hide
		{"assertCount": "li.row", "equals": 0},
		{"click": "button.t"}, // show
		{"assertCount": "li.row", "equals": 2},
		{"click": "button.add"},
		{"assertCount": "li.row", "equals": 3},
		{"assertText": "li.row:nth-of-type(3)", "equals": "c"},
	})
}

// TestRuntimeNestedIfChainReactive — nested {#if} inside an {#if}
// branch must reactively update on inner-state change.  The bug
// before this fix: the inner chain function was declared inside
// the outer branch's build BLOCK (block-scoped), so the
// component-scope setter cascade could not reach it
// (`$$chain_$$nN is not defined`).  The fix hoists nested-chain
// machinery (anchor, branch handles, reentrancy flags, captured
// node decls, the chain function itself) to component scope.
func TestRuntimeNestedIfChainReactive(t *testing.T) {
	src := `<script>
  let outer = $state(true);
  let inner = $state('first');
</script>
<template><div>
  <button class="b" @click={() => inner = 'second'}>change</button>
  {#if outer}
    {#if inner}<span class="msg">{inner}</span>{/if}
  {/if}
</div></template>`
	assertRuntime(t, "NestedIf", src, []rtStep{
		{"assertText": "span.msg", "equals": "first"},
		{"click": "button.b"},
		{"assertText": "span.msg", "equals": "second"},
	})
}

// TestRuntimeNestedChainElseFallingWhenOuterInactive — pattern
// from PageCraft's LiveBlock: an outer chain selects on block
// type; each outer branch contains an inner chain whose {:else}
// arm always matches (e.g. `{#if level==='h1'}...{:else if
// level==='h2'}...{:else}<h3>...{/if}`).  When the active outer
// branch is NOT the one containing the inner chain, the cascade
// still calls EVERY hoisted inner chain — and the {:else}-falling
// arm would try to attach via its anchor.  Without the anchor
// guard the attach crashed (`Cannot read properties of null
// (reading 'before')`).  With the guard, the chain function
// returns early when its anchor is null and the active outer
// branch's nested chain runs normally.
func TestRuntimeNestedChainElseFallingWhenOuterInactive(t *testing.T) {
	src := `<script>
  let which = $state('a');
  let v = $state('hi');
</script>
<template><div>
  <button class="ba" @click={() => which = 'a'}>a</button>
  <button class="bb" @click={() => which = 'b'}>b</button>
  <button class="vc" @click={() => v = 'changed'}>changed</button>
  {#if which === 'a'}
    {#if v === 'never'}<p class="x">unreachable</p>{:else}<p class="y">A:{v}</p>{/if}
  {:else}
    {#if v === 'never'}<p class="x">unreachable</p>{:else}<p class="z">B:{v}</p>{/if}
  {/if}
</div></template>`
	assertRuntime(t, "NestedElseFalling", src, []rtStep{
		{"assertText": "p.y", "equals": "A:hi"},
		{"assertCount": "p.z", "equals": 0},
		// Click that mutates v — the OTHER outer branch's nested
		// chain is also wired to v (its {:else} always falls),
		// and the cascade calls it; without the guard, that call
		// crashes on a null anchor.
		{"click": "button.vc"},
		{"assertText": "p.y", "equals": "A:changed"},
		// Switch to outer branch b; its nested chain now has a
		// real anchor and the {:else} branch renders.
		{"click": "button.bb"},
		{"assertCount": "p.y", "equals": 0},
		{"assertText": "p.z", "equals": "B:changed"},
	})
}
