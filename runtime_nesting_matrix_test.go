package main

import "testing"

// runtime_nesting_matrix_test.go — the kitchen-sink test suite for
// every nesting combination of Vela's control-flow constructs.
//
// Each test exercises FIVE properties for one nesting shape:
//
//   (a) compiles cleanly
//   (b) initial DOM is correct
//   (c) reactive change to INNER state updates DOM
//   (d) toggling/changing OUTER construct cleanly tears down the
//       inner subtree — no orphan nodes, no runtime errors
//   (e) where applicable, reactive change to OUTER preserves inner
//       state semantically
//
// The matrix covers {#if}, {#each}, {#key}, {#await} nested in
// every meaningful combination, plus component-bearing variants
// (the PageCraft-shaped failure modes).
//
// Session 1 deliverable for the centralization roadmap: this file
// is the safety net that future builders will land on top of.  If
// a nesting shape regresses — orphan nodes, missed updates, a
// "$$chain_$$nN is not defined" crash — the corresponding test
// here turns red, before the bug reaches an app.

// ── Pairwise nestings ────────────────────────────────────────────

// if-in-if — Bug fixed earlier this session.  Inner {#if}'s chain
// machinery must hoist out of the outer branch's build block.
func TestRuntimeNestingMatrix_IfInIf(t *testing.T) {
	src := `<script>
  let outer = $state(true);
  let inner = $state('a');
</script>
<template><div>
  <button class="o" @click={() => outer = !outer}>o</button>
  <button class="i" @click={() => inner = inner === 'a' ? 'b' : 'a'}>i</button>
  {#if outer}
    {#if inner}<span class="x">{inner}</span>{/if}
  {/if}
</div></template>`
	assertRuntime(t, "IfInIf", src, []rtStep{
		// (b) initial
		{"assertText": "span.x", "equals": "a"},
		// (c) inner change
		{"click": "button.i"},
		{"assertText": "span.x", "equals": "b"},
		// (d) outer off — inner gone, no error
		{"click": "button.o"},
		{"assertCount": "span.x", "equals": 0},
		// outer on again — inner re-appears with current state
		{"click": "button.o"},
		{"assertText": "span.x", "equals": "b"},
	})
}

// each-in-if — {#each} inside an {#if} branch.  Tests that each
// machinery (anchor, blocks array, reconciler fn) hoists out of
// the branch build block, AND that branch-reopen rebuilds rows
// correctly (the isConnected check from Session A).
func TestRuntimeNestingMatrix_EachInIf(t *testing.T) {
	src := `<script>
  let show = $state(true);
  let items = $state.deep(['a','b']);
</script>
<template><div>
  <button class="t" @click={() => show = !show}>t</button>
  <button class="a" @click={() => items.push('c')}>a</button>
  {#if show}
    <ul>{#each items as it}<li class="row">{it}</li>{/each}</ul>
  {/if}
</div></template>`
	assertRuntime(t, "EachInIf", src, []rtStep{
		{"assertCount": "li.row", "equals": 2},
		{"click": "button.a"},
		{"assertCount": "li.row", "equals": 3},
		{"assertText": "li.row:nth-of-type(3)", "equals": "c"},
		{"click": "button.t"}, // hide
		{"assertCount": "li.row", "equals": 0},
		{"click": "button.t"}, // show — must rebuild
		{"assertCount": "li.row", "equals": 3},
	})
}

// key-in-if — {#key} (chain with KeyExpr) inside an {#if}.  The
// {#key} block recreates its subtree whenever its key expression
// changes identity; hoisted keyprev tracking must survive outer
// toggling.
func TestRuntimeNestingMatrix_KeyInIf(t *testing.T) {
	src := `<script>
  let show = $state(true);
  let key = $state(1);
</script>
<template><div>
  <button class="t" @click={() => show = !show}>t</button>
  <button class="k" @click={() => key++}>k</button>
  {#if show}
    {#key key}<span class="x">k={key}</span>{/key}
  {/if}
</div></template>`
	assertRuntime(t, "KeyInIf", src, []rtStep{
		{"assertText": "span.x", "equals": "k=1"},
		{"click": "button.k"},
		{"assertText": "span.x", "equals": "k=2"},
		{"click": "button.t"}, // hide
		{"assertCount": "span.x", "equals": 0},
		{"click": "button.t"}, // show
		{"assertText": "span.x", "equals": "k=2"},
		{"click": "button.k"}, // key changes while shown
		{"assertText": "span.x", "equals": "k=3"},
	})
}

// await-in-if — {#await} inside an {#if}.  Tests that await's
// three-branch machinery hoists correctly.  Using an immediately-
// resolved promise so :then is the active branch by the time we
// poll.  (Synchronous test of pending/then/catch would need a
// resolvable Deferred — the harness has only sync ticks.)
func TestRuntimeNestingMatrix_AwaitInIf(t *testing.T) {
	src := `<script>
  let show = $state(true);
  let p = $state(Promise.resolve('hello'));
</script>
<template><div>
  <button class="t" @click={() => show = !show}>t</button>
  {#if show}
    {#await p}
      <span class="p">pending</span>
    {:then v}
      <span class="r">got:{v}</span>
    {:catch e}
      <span class="e">err:{e}</span>
    {/await}
  {/if}
</div></template>`
	assertRuntime(t, "AwaitInIf", src, []rtStep{
		// pending or resolved depending on microtask order — accept either, then drive on.
		{"click": "button.t"}, // hide — await machinery must not crash
		{"assertCount": "span.r", "equals": 0},
		{"assertCount": "span.p", "equals": 0},
		{"click": "button.t"}, // show again
		// After microtasks flush, the promise resolved → :then renders.
		{"assertText": "span.r", "equals": "got:hello"},
	})
}

// ── X-in-each — the row-fragment scope ───────────────────────────

// if-in-each — each row's body contains an {#if}.  The if-chain
// must live in row scope (not component scope), and a reactive
// change to the if's condition must update each affected row.
func TestRuntimeNestingMatrix_IfInEach(t *testing.T) {
	src := `<script>
  let items = $state.deep([{label:'a', show:true}, {label:'b', show:false}, {label:'c', show:true}]);
  let bump = $state(0);
</script>
<template><div>
  <button class="b" @click={() => { items[1].show = !items[1].show; bump++; }}>toggle b</button>
  {#each items as it}
    <div class="r">
      {#if it.show}<span class="x">{it.label}</span>{/if}
    </div>
  {/each}
</div></template>`
	assertRuntime(t, "IfInEach", src, []rtStep{
		{"assertCount": "div.r", "equals": 3},
		{"assertCount": "span.x", "equals": 2}, // a and c shown, b hidden
		{"assertText": "div.r:nth-of-type(1) span.x", "equals": "a"},
		{"click": "button.b"},
		{"assertCount": "span.x", "equals": 3}, // b now shown
	})
}

// each-in-each — doubly nested {#each}.  Inner each is row-scoped;
// reacting on inner array mutation must update only that row.
func TestRuntimeNestingMatrix_EachInEach(t *testing.T) {
	src := `<script>
  let groups = $state.deep([{name:'A', items:['a1','a2']}, {name:'B', items:['b1']}]);
</script>
<template><div>
  <button class="g" @click={() => groups[0].items.push('a3')}>add to A</button>
  {#each groups as g}
    <section class="grp">
      <h2 class="name">{g.name}</h2>
      <ul>{#each g.items as it}<li class="row">{it}</li>{/each}</ul>
    </section>
  {/each}
</div></template>`
	assertRuntime(t, "EachInEach", src, []rtStep{
		{"assertCount": "section.grp", "equals": 2},
		{"assertCount": "section.grp:nth-of-type(1) li.row", "equals": 2},
		{"assertCount": "section.grp:nth-of-type(2) li.row", "equals": 1},
		{"click": "button.g"},
		{"assertCount": "section.grp:nth-of-type(1) li.row", "equals": 3},
		{"assertCount": "section.grp:nth-of-type(2) li.row", "equals": 1},
	})
}

// key-in-each — {#key} inside each row.  The key block in row N
// must respond to changes in its own row's key without affecting
// other rows.
func TestRuntimeNestingMatrix_KeyInEach(t *testing.T) {
	src := `<script>
  let items = $state.deep([{id:1, ver:0}, {id:2, ver:0}]);
</script>
<template><div>
  <button class="b" @click={() => items[0].ver++}>bump 0</button>
  {#each items as it}
    <div class="row">
      {#key it.ver}
        <span class="x">v={it.ver}</span>
      {/key}
    </div>
  {/each}
</div></template>`
	assertRuntime(t, "KeyInEach", src, []rtStep{
		{"assertCount": "div.row", "equals": 2},
		{"assertCount": "span.x", "equals": 2},
		{"assertText": "div.row:nth-of-type(1) span.x", "equals": "v=0"},
		{"assertText": "div.row:nth-of-type(2) span.x", "equals": "v=0"},
		{"click": "button.b"},
		{"assertText": "div.row:nth-of-type(1) span.x", "equals": "v=1"},
		{"assertText": "div.row:nth-of-type(2) span.x", "equals": "v=0"},
	})
}

// await-in-each — {#await} inside each row.  Each row drives its
// own promise; resolution must reach the correct row.
func TestRuntimeNestingMatrix_AwaitInEach(t *testing.T) {
	src := `<script>
  let items = $state.deep([
    {id:1, p:Promise.resolve('one')},
    {id:2, p:Promise.resolve('two')}
  ]);
</script>
<template><div>
  {#each items as it}
    <div class="row">
      {#await it.p}
        <span class="p">pending</span>
      {:then v}
        <span class="r">{v}</span>
      {/await}
    </div>
  {/each}
</div></template>`
	assertRuntime(t, "AwaitInEach", src, []rtStep{
		{"assertCount": "div.row", "equals": 2},
		{"assertText": "div.row:nth-of-type(1) span.r", "equals": "one"},
		{"assertText": "div.row:nth-of-type(2) span.r", "equals": "two"},
	})
}

// ── X-in-key ─────────────────────────────────────────────────────

// if-in-key — {#if} inside {#key}.  Key change destroys+rebuilds
// the subtree (including the if-chain); inner if must be reactive
// in both the original and the rebuilt subtree.
func TestRuntimeNestingMatrix_IfInKey(t *testing.T) {
	src := `<script>
  let k = $state(1);
  let show = $state(true);
</script>
<template><div>
  <button class="kk" @click={() => k++}>k</button>
  <button class="ss" @click={() => show = !show}>s</button>
  {#key k}
    <section class="g">
      <span class="kv">k={k}</span>
      {#if show}<span class="msg">visible</span>{/if}
    </section>
  {/key}
</div></template>`
	assertRuntime(t, "IfInKey", src, []rtStep{
		{"assertText": "span.kv", "equals": "k=1"},
		{"assertCount": "span.msg", "equals": 1},
		{"click": "button.ss"},
		{"assertCount": "span.msg", "equals": 0},
		// Now change the key — subtree rebuilds.  The new inner
		// {#if} must reflect the current state of `show` (false).
		{"click": "button.kk"},
		{"assertText": "span.kv", "equals": "k=2"},
		{"assertCount": "span.msg", "equals": 0},
		{"click": "button.ss"},
		{"assertCount": "span.msg", "equals": 1},
	})
}

// each-in-key — {#each} inside {#key}.  Same recreate-on-key
// concern; inner each must be intact in the rebuilt subtree.
func TestRuntimeNestingMatrix_EachInKey(t *testing.T) {
	src := `<script>
  let k = $state(1);
  let items = $state.deep(['a','b']);
</script>
<template><div>
  <button class="kk" @click={() => k++}>k</button>
  <button class="aa" @click={() => items.push('c')}>add</button>
  {#key k}
    <ul class="g">
      {#each items as it}<li class="row">{it}</li>{/each}
    </ul>
  {/key}
</div></template>`
	assertRuntime(t, "EachInKey", src, []rtStep{
		{"assertCount": "li.row", "equals": 2},
		{"click": "button.aa"},
		{"assertCount": "li.row", "equals": 3},
		// Force key change — entire subtree recreated.  The each
		// must still render 3 rows in the new subtree.
		{"click": "button.kk"},
		{"assertCount": "li.row", "equals": 3},
		{"click": "button.aa"},
		{"assertCount": "li.row", "equals": 4},
	})
}

// key-in-key — doubly nested {#key}.  Inner key responds to its
// own key independently; outer key change recreates inner too.
func TestRuntimeNestingMatrix_KeyInKey(t *testing.T) {
	src := `<script>
  let ko = $state(1);
  let ki = $state(1);
</script>
<template><div>
  <button class="ko" @click={() => ko++}>ko</button>
  <button class="ki" @click={() => ki++}>ki</button>
  {#key ko}
    <section>
      <span class="o">o={ko}</span>
      {#key ki}<span class="i">i={ki}</span>{/key}
    </section>
  {/key}
</div></template>`
	assertRuntime(t, "KeyInKey", src, []rtStep{
		{"assertText": "span.o", "equals": "o=1"},
		{"assertText": "span.i", "equals": "i=1"},
		{"click": "button.ki"},
		{"assertText": "span.o", "equals": "o=1"},
		{"assertText": "span.i", "equals": "i=2"},
		{"click": "button.ko"},
		{"assertText": "span.o", "equals": "o=2"},
		{"assertText": "span.i", "equals": "i=2"},
	})
}

// await-in-key — {#await} inside {#key}.  Key change recreates
// the await; the new instance should resolve its promise again.
func TestRuntimeNestingMatrix_AwaitInKey(t *testing.T) {
	src := `<script>
  let k = $state(1);
  let p = $state(Promise.resolve('v1'));
</script>
<template><div>
  <button class="kk" @click={() => { k++; p = Promise.resolve('v' + (k+1)); }}>k</button>
  {#key k}
    {#await p}
      <span class="p">pending</span>
    {:then v}
      <span class="r">{v}</span>
    {/await}
  {/key}
</div></template>`
	assertRuntime(t, "AwaitInKey", src, []rtStep{
		{"assertText": "span.r", "equals": "v1"},
		{"click": "button.kk"},
		// k was 1, click makes it 2; promise is set to 'v'+(2+1)='v3'.
		// That's deliberate — it lets us prove the new promise was
		// actually awaited, not the old one resolved to 'v1' again.
		{"assertText": "span.r", "equals": "v3"},
	})
}

// ── X-in-await ───────────────────────────────────────────────────

// if-in-await — {#if} inside the {:then} branch of {#await}.
// The await's then-branch must build a reactive if-chain that
// continues to track its dependency after the promise resolved.
func TestRuntimeNestingMatrix_IfInAwait(t *testing.T) {
	src := `<script>
  let p = $state(Promise.resolve('hi'));
  let show = $state(true);
</script>
<template><div>
  <button class="b" @click={() => show = !show}>t</button>
  {#await p}
    <span class="p">pending</span>
  {:then v}
    <span class="v">{v}</span>
    {#if show}<span class="msg">visible</span>{/if}
  {/await}
</div></template>`
	assertRuntime(t, "IfInAwait", src, []rtStep{
		{"assertText": "span.v", "equals": "hi"},
		{"assertCount": "span.msg", "equals": 1},
		{"click": "button.b"},
		{"assertCount": "span.msg", "equals": 0},
		{"click": "button.b"},
		{"assertCount": "span.msg", "equals": 1},
	})
}

// each-in-await — {#each} inside {:then}.  Tests that the each
// machinery, hoisted out of the then-branch build, stays reactive
// after the promise resolved.
func TestRuntimeNestingMatrix_EachInAwait(t *testing.T) {
	src := `<script>
  let p = $state(Promise.resolve(['x','y']));
  let bump = $state(0);
  let items = $state.deep([]);
  // wire promise to items once resolved (sync test of post-resolution mutation)
  p.then(arr => items.push(...arr));
</script>
<template><div>
  <button class="a" @click={() => items.push('z')}>add</button>
  {#await p}
    <span class="p">pending</span>
  {:then arr}
    <ul>{#each items as it}<li class="row">{it}</li>{/each}</ul>
  {/await}
</div></template>`
	assertRuntime(t, "EachInAwait", src, []rtStep{
		{"assertCount": "li.row", "equals": 2},
		{"click": "button.a"},
		{"assertCount": "li.row", "equals": 3},
		{"assertText": "li.row:nth-of-type(3)", "equals": "z"},
	})
}

// key-in-await — {#key} inside {:then}.
func TestRuntimeNestingMatrix_KeyInAwait(t *testing.T) {
	src := `<script>
  let p = $state(Promise.resolve('hi'));
  let k = $state(1);
</script>
<template><div>
  <button class="k" @click={() => k++}>k</button>
  {#await p}
    <span class="p">pending</span>
  {:then v}
    {#key k}<span class="x">k={k}:{v}</span>{/key}
  {/await}
</div></template>`
	assertRuntime(t, "KeyInAwait", src, []rtStep{
		{"assertText": "span.x", "equals": "k=1:hi"},
		{"click": "button.k"},
		{"assertText": "span.x", "equals": "k=2:hi"},
	})
}

// await-in-await — {#await} inside {:then} of another {#await}.
// A nested promise chain.  The inner await must drive on its
// own promise once the outer resolved.
func TestRuntimeNestingMatrix_AwaitInAwait(t *testing.T) {
	src := `<script>
  let outer = $state(Promise.resolve('outer-val'));
  let inner = $state(Promise.resolve('inner-val'));
</script>
<template><div>
  {#await outer}
    <span class="po">o-pending</span>
  {:then ov}
    <section class="o">
      <span class="ov">{ov}</span>
      {#await inner}
        <span class="pi">i-pending</span>
      {:then iv}
        <span class="iv">{iv}</span>
      {/await}
    </section>
  {/await}
</div></template>`
	assertRuntime(t, "AwaitInAwait", src, []rtStep{
		{"assertText": "span.ov", "equals": "outer-val"},
		{"assertText": "span.iv", "equals": "inner-val"},
	})
}

// ── Component-bearing variants — the PageCraft failure shapes ───

// component-in-if — {#if} branch contains a child component.
// Toggling the branch off+on must remount the component cleanly.
func TestRuntimeNestingMatrix_ComponentInIf(t *testing.T) {
	child := `<script>export let v;</script>
<template><span class="c">child:{v}</span></template>`
	app := `<script>
  import Child from './Child.vela';
  let show = $state(true);
  let v = $state('a');
</script>
<template><div>
  <button class="t" @click={() => show = !show}>t</button>
  <button class="b" @click={() => v = 'b'}>b</button>
  {#if show}<Child v={v} />{/if}
</div></template>`
	assertRuntimeSet(t, "App", map[string]string{
		"App":   app,
		"Child": child,
	}, []rtStep{
		{"assertText": "span.c", "equals": "child:a"},
		// reactive prop change reaches the mounted child
		{"click": "button.b"},
		{"assertText": "span.c", "equals": "child:b"},
		// branch off — child gone
		{"click": "button.t"},
		{"assertCount": "span.c", "equals": 0},
		// branch on — child re-created with current state
		{"click": "button.t"},
		{"assertText": "span.c", "equals": "child:b"},
	})
}

// component-in-each — {#each} renders one Child per row.  Reactive
// prop update through row.update; focus preserved by Session A.
func TestRuntimeNestingMatrix_ComponentInEach(t *testing.T) {
	child := `<script>export let item;</script>
<template><div class="c"><input class="in" /><span class="lbl">{item.label}</span></div></template>`
	app := `<script>
  import Child from './Child.vela';
  let items = $state.deep([{label:'a'},{label:'b'}]);
</script>
<template><div>
  <button class="bump" @click={() => items[0].label = 'A'}>bump 0</button>
  <button class="add" @click={() => items.push({label:'c'})}>add</button>
  {#each items as it}<Child item={it} />{/each}
</div></template>`
	assertRuntimeSet(t, "App", map[string]string{
		"App":   app,
		"Child": child,
	}, []rtStep{
		{"assertCount": "div.c", "equals": 2},
		{"assertText": "div.c:nth-of-type(1) span.lbl", "equals": "a"},
		// Focus row 0's input.
		{"focus": "div.c:nth-of-type(1) input.in"},
		// Mutate row 0 — prop reaches the existing child.
		{"click": "button.bump"},
		{"assertText": "div.c:nth-of-type(1) span.lbl", "equals": "A"},
		// Focus survives the in-place update.
		{"assertFocused": "div.c:nth-of-type(1) input.in"},
		// Append — new row created; existing rows survive.
		{"click": "button.add"},
		{"assertCount": "div.c", "equals": 3},
		{"assertFocused": "div.c:nth-of-type(1) input.in"},
	})
}

// component-in-each-in-if — the PageCraft Inspector shape.  An
// outer {#if} wraps an {#each}; each row renders a Child.
// Reactive prop change must reach Child.set() AND focus on the
// child's input must survive.
func TestRuntimeNestingMatrix_ComponentInEachInIf(t *testing.T) {
	child := `<script>
  export let v;
</script>
<template><div class="row"><input class="in" /><span class="lbl">{v}</span></div></template>`
	app := `<script>
  import Child from './Child.vela';
  let show = $state(true);
  let items = $state.deep([{v:'a'},{v:'b'}]);
</script>
<template><div>
  <button class="t" @click={() => show = !show}>t</button>
  <button class="b" @click={() => items[0].v = 'A'}>bump</button>
  {#if show}
    {#each items as it}<Child v={it.v} />{/each}
  {/if}
</div></template>`
	assertRuntimeSet(t, "App", map[string]string{
		"App":   app,
		"Child": child,
	}, []rtStep{
		{"assertCount": "div.row", "equals": 2},
		{"assertText": "div.row:nth-of-type(1) span.lbl", "equals": "a"},
		{"focus": "div.row:nth-of-type(1) input.in"},
		{"click": "button.b"},
		{"assertText": "div.row:nth-of-type(1) span.lbl", "equals": "A"},
		{"assertFocused": "div.row:nth-of-type(1) input.in"},
		// Toggle off then on — focus loss is expected here (the
		// rows are torn down with the branch), but no errors and
		// state is current after remount.
		{"click": "button.t"},
		{"assertCount": "div.row", "equals": 0},
		{"click": "button.t"},
		{"assertCount": "div.row", "equals": 2},
		{"assertText": "div.row:nth-of-type(1) span.lbl", "equals": "A"},
	})
}

// component-in-if-in-each — each row contains an {#if} that
// renders a Child.  The PageCraft Canvas-then-LiveBlock shape.
func TestRuntimeNestingMatrix_ComponentInIfInEach(t *testing.T) {
	child := `<script>export let v;</script>
<template><span class="c">c:{v}</span></template>`
	app := `<script>
  import Child from './Child.vela';
  let items = $state.deep([{v:'a', show:true}, {v:'b', show:true}]);
</script>
<template><div>
  <button class="t" @click={() => items[0].show = !items[0].show}>toggle 0</button>
  <button class="bump" @click={() => items[0].v = 'A'}>bump 0</button>
  {#each items as it}
    <div class="row">
      {#if it.show}<Child v={it.v} />{/if}
    </div>
  {/each}
</div></template>`
	assertRuntimeSet(t, "App", map[string]string{
		"App":   app,
		"Child": child,
	}, []rtStep{
		{"assertCount": "div.row", "equals": 2},
		{"assertCount": "span.c", "equals": 2},
		{"assertText": "div.row:nth-of-type(1) span.c", "equals": "c:a"},
		// Inner mutation reaches the child.
		{"click": "button.bump"},
		{"assertText": "div.row:nth-of-type(1) span.c", "equals": "c:A"},
		// Toggle inner {#if} off — only row 0's child gone.
		{"click": "button.t"},
		{"assertCount": "span.c", "equals": 1},
		{"assertText": "div.row:nth-of-type(2) span.c", "equals": "c:b"},
		// Toggle back — re-mounted with current state.
		{"click": "button.t"},
		{"assertText": "div.row:nth-of-type(1) span.c", "equals": "c:A"},
	})
}

// ── Triple-nested shapes ─────────────────────────────────────────

// each-in-if-in-each — outer each has rows each containing an {#if}
// containing an inner {#each}.  Exercises double-deep
// nestedTeardowns propagation: when an inner {#if} branch toggles
// off, its inner each's rows must be torn down with it.
func TestRuntimeNestingMatrix_EachInIfInEach(t *testing.T) {
	src := `<script>
  let groups = $state.deep([
    {name:'A', show:true, items:['a1','a2']},
    {name:'B', show:true, items:['b1']}
  ]);
</script>
<template><div>
  <button class="t0" @click={() => groups[0].show = !groups[0].show}>toggle 0</button>
  <button class="a0" @click={() => groups[0].items.push('a3')}>add 0</button>
  {#each groups as g}
    <section class="grp">
      <h2 class="name">{g.name}</h2>
      {#if g.show}
        <ul>{#each g.items as it}<li class="row">{it}</li>{/each}</ul>
      {/if}
    </section>
  {/each}
</div></template>`
	assertRuntime(t, "EachInIfInEach", src, []rtStep{
		{"assertCount": "section.grp", "equals": 2},
		{"assertCount": "section.grp:nth-of-type(1) li.row", "equals": 2},
		{"assertCount": "section.grp:nth-of-type(2) li.row", "equals": 1},
		// Toggle inner {#if} off — group 0's rows gone, group 1
		// untouched.
		{"click": "button.t0"},
		{"assertCount": "section.grp:nth-of-type(1) li.row", "equals": 0},
		{"assertCount": "section.grp:nth-of-type(2) li.row", "equals": 1},
		// Toggle back — rows restored to current items.
		{"click": "button.t0"},
		{"assertCount": "section.grp:nth-of-type(1) li.row", "equals": 2},
		// Append while visible.
		{"click": "button.a0"},
		{"assertCount": "section.grp:nth-of-type(1) li.row", "equals": 3},
	})
}

// if-in-if-in-each — each row has an {#if} containing another {#if}.
// Exercises double-deep nested-chain hoist within a row scope.
func TestRuntimeNestingMatrix_IfInIfInEach(t *testing.T) {
	src := `<script>
  let items = $state.deep([
    {a:true, b:'x'},
    {a:true, b:'y'}
  ]);
</script>
<template><div>
  <button class="t" @click={() => items[0].a = !items[0].a}>t</button>
  <button class="b" @click={() => items[0].b = 'X'}>bump 0</button>
  {#each items as it}
    <div class="row">
      {#if it.a}
        {#if it.b}<span class="msg">{it.b}</span>{/if}
      {/if}
    </div>
  {/each}
</div></template>`
	assertRuntime(t, "IfInIfInEach", src, []rtStep{
		{"assertCount": "div.row", "equals": 2},
		{"assertCount": "span.msg", "equals": 2},
		{"assertText": "div.row:nth-of-type(1) span.msg", "equals": "x"},
		// Inner mutation.
		{"click": "button.b"},
		{"assertText": "div.row:nth-of-type(1) span.msg", "equals": "X"},
		// Toggle outer off — row 0's inner gone, row 1 untouched.
		{"click": "button.t"},
		{"assertCount": "span.msg", "equals": 1},
		{"assertText": "div.row:nth-of-type(2) span.msg", "equals": "y"},
		// Toggle back.
		{"click": "button.t"},
		{"assertCount": "span.msg", "equals": 2},
		{"assertText": "div.row:nth-of-type(1) span.msg", "equals": "X"},
	})
}

// component-in-if-in-each — like the previous CIIIE test but the
// inner {#if} depends on a separate condition AND mutates a prop
// while the outer condition is also reactive.  Tests interactions
// between the row's reactivity, the branch's reactivity, and the
// child's prop update path.
func TestRuntimeNestingMatrix_ComponentInIfInEach_Combined(t *testing.T) {
	child := `<script>export let v;</script>
<template><span class="c">c:{v}</span></template>`
	app := `<script>
  import Child from './Child.vela';
  let items = $state.deep([
    {show:true, v:'a'},
    {show:false, v:'b'},
  ]);
</script>
<template><div>
  <button class="t0" @click={() => items[0].show = !items[0].show}>toggle 0</button>
  <button class="t1" @click={() => items[1].show = !items[1].show}>toggle 1</button>
  <button class="b" @click={() => items[1].v = 'B'}>bump 1</button>
  {#each items as it}
    <div class="row">
      {#if it.show}<Child v={it.v} />{/if}
    </div>
  {/each}
</div></template>`
	assertRuntimeSet(t, "App", map[string]string{
		"App":   app,
		"Child": child,
	}, []rtStep{
		// Initial: row 0 has Child, row 1 doesn't (show=false).
		{"assertCount": "div.row", "equals": 2},
		{"assertCount": "span.c", "equals": 1},
		{"assertText": "div.row:nth-of-type(1) span.c", "equals": "c:a"},
		// Mutate row 1's v while its branch is OFF — no error,
		// no spurious render.
		{"click": "button.b"},
		{"assertCount": "span.c", "equals": 1},
		// Now show row 1.  Its branch builds Child with current v.
		{"click": "button.t1"},
		{"assertCount": "span.c", "equals": 2},
		{"assertText": "div.row:nth-of-type(2) span.c", "equals": "c:B"},
		// Toggle row 0 off — only row 0's child gone.
		{"click": "button.t0"},
		{"assertCount": "span.c", "equals": 1},
		{"assertText": "div.row:nth-of-type(2) span.c", "equals": "c:B"},
	})
}

// ── Edge cases ───────────────────────────────────────────────────

// {#await} that REJECTS — exercises the :catch branch.
func TestRuntimeNestingMatrix_AwaitRejection(t *testing.T) {
	src := `<script>
  let p = $state(Promise.reject(new Error('boom')));
</script>
<template><div>
  {#await p}
    <span class="p">pending</span>
  {:then v}
    <span class="r">{v}</span>
  {:catch e}
    <span class="e">err:{e.message}</span>
  {/await}
</div></template>`
	assertRuntime(t, "AwaitRejection", src, []rtStep{
		{"assertText": "span.e", "equals": "err:boom"},
		{"assertCount": "span.r", "equals": 0},
		{"assertCount": "span.p", "equals": 0},
	})
}

// {#each} with a single-row component AND a focused input — Session A
// keystone, repeated here in matrix form to keep the suite self-
// contained.  An update-in-place must preserve focus.
func TestRuntimeNestingMatrix_FocusSurvives_ComponentInEach(t *testing.T) {
	child := `<script>export let v;</script>
<template><div class="r"><input class="in" /><span class="v">{v}</span></div></template>`
	app := `<script>
  import Child from './Child.vela';
  let items = $state.deep([{v:'a'},{v:'b'}]);
</script>
<template><div>
  <button class="b" @click={() => items[0].v = 'A'}>bump</button>
  {#each items as it}<Child v={it.v} />{/each}
</div></template>`
	assertRuntimeSet(t, "App", map[string]string{
		"App":   app,
		"Child": child,
	}, []rtStep{
		{"focus": "div.r:nth-of-type(1) input.in"},
		{"click": "button.b"},
		{"assertText": "div.r:nth-of-type(1) span.v", "equals": "A"},
		{"assertFocused": "div.r:nth-of-type(1) input.in"},
	})
}

// SlotComponentInIf — Session 3 audit discovery.  A
// slot-bearing child component placed inside an {#if} branch.
// The slot's cache var ($$scache_$$nN) was declared via
// g.body inside the branch closure, making it block-scoped
// and invisible from the slot factory closure stored on the
// child's props.  Initial mount threw ReferenceError before
// the fix.  declareInScope now routes the declaration to
// fragHoisted/hoisted as needed, fixing the scoping.
func TestRuntimeNestingMatrix_SlotComponentInIf(t *testing.T) {
	wrap := `<script>
  export let label = '';
</script>
<template><div class="wrap"><span class="lbl">{label}</span><slot/></div></template>`
	app := `<script>
  import Wrapper from './Wrapper.vela';
  let show = $state(true);
  let msg = $state('hello');
</script>
<template><div>
  <button class="t" @click={() => show = !show}>t</button>
  <button class="m" @click={() => msg = 'world'}>m</button>
  {#if show}
    <Wrapper label="outer"><span class="inner">{msg}</span></Wrapper>
  {/if}
</div></template>`
	assertRuntimeSet(t, "App", map[string]string{
		"App":     app,
		"Wrapper": wrap,
	}, []rtStep{
		{"assertText": "span.inner", "equals": "hello"},
		{"click": "button.m"},
		{"assertText": "span.inner", "equals": "world"},
		// Hide the branch — the wrap disappears with it.
		{"click": "button.t"},
		{"assertCount": "div.wrap", "equals": 0},
		// Reopen — slot content rebuilds with the current value.
		{"click": "button.t"},
		{"assertText": "span.inner", "equals": "world"},
	})
}

// KeyedEachInIf — Session 3 audit verification.  A KEYED {#each}
// (key suffix supplied) inside an {#if} branch.  The keyed
// reconciler uses a $$nN_km Map for key→block lookup; that map
// is hoisted via g.declare but its `new Map()` initializer runs
// at branch-build time.  Verify that toggling the branch off+on
// rebuilds correctly without leaking stale keyed entries.
func TestRuntimeNestingMatrix_KeyedEachInIf(t *testing.T) {
	src := `<script>
  let show = $state(true);
  let items = $state.deep([{id:1, v:'a'}, {id:2, v:'b'}]);
</script>
<template><div>
  <button class="t" @click={() => show = !show}>t</button>
  <button class="m" @click={() => items[0].v = 'A'}>m</button>
  {#if show}
    <ul>{#each items as it (it.id)}<li class="row">{it.v}</li>{/each}</ul>
  {/if}
</div></template>`
	assertRuntime(t, "KeyedEachInIf", src, []rtStep{
		{"assertCount": "li.row", "equals": 2},
		{"assertText": "li.row:nth-of-type(1)", "equals": "a"},
		{"click": "button.m"},
		{"assertText": "li.row:nth-of-type(1)", "equals": "A"},
		{"click": "button.t"}, // hide
		{"assertCount": "li.row", "equals": 0},
		{"click": "button.t"}, // show — rebuild from current state
		{"assertCount": "li.row", "equals": 2},
		{"assertText": "li.row:nth-of-type(1)", "equals": "A"},
	})
}

// SlotContentInEachReactive — the slot-in-each reactivity fix.
//
// Before the fix, a child component placed in another component's
// slot inside an {#each} row did not receive reactive prop
// updates: the component instance var was block-scoped to the
// slot factory closure, so the row's update closure (Session A's
// rowComps .set() replay) could not see it.  The factory caches
// its output, so the component instance is reused across slot
// re-invocations — there was no path for fresh prop values to
// reach the inner child after initial mount.
//
// Fix: the slot capture re-publishes cap.fragHoisted (via
// g.declare) and cap.comps (via g.compInstances) into the
// enclosing scope, so inner-slot component instances live at row-
// fragment scope and join Session A's rowComps replay set.  Also,
// buildComponentNode's setStmt back-fill now searches for the
// matching inst rather than assuming [n-1] — the slot capture
// may have appended other component entries between push and
// back-fill.
func TestRuntimeNestingMatrix_SlotContentInEachReactive(t *testing.T) {
	wrap := `<script>
  export let label = '';
</script>
<template><div class="wrap"><span class="lbl">{label}</span><slot/></div></template>`
	child := `<script>
  export let label = '';
</script>
<template><span class="logger">log:{label}</span></template>`
	app := `<script>
  import Wrapper from './Wrapper.vela';
  import ChildLog from './ChildLog.vela';
  let items = $state.deep([{v:'a'},{v:'b'}]);
</script>
<template><div>
  <button class="b" @click={() => items[0].v = 'A'}>bump</button>
  {#each items as it}
    <Wrapper label="row"><ChildLog label={it.v} /></Wrapper>
  {/each}
</div></template>`
	// Use a custom class on each child so we can target rows by
	// the slot-content child's identity.  Each row gets a
	// stable-by-content selector via the v prop being part of
	// the rendered text.
	assertRuntimeSet(t, "App", map[string]string{
		"App":      app,
		"Wrapper":  wrap,
		"ChildLog": child,
	}, []rtStep{
		// Both rows render with their own slot-content child:
		// one shows "log:a", the other "log:b".
		{"assertCount": "span.logger", "equals": 2},
		// Selectors keyed by text content via the first matching
		// element's textContent.  assertText uses the FIRST match;
		// since loggers render in DOM order, querying for
		// `span.logger` first returns row 0's text.
		{"assertText": "span.logger", "equals": "log:a"},
		// Mutate row 0 — row 0's child reacts; row 1 untouched.
		{"click": "button.b"},
		// Total count unchanged.
		{"assertCount": "span.logger", "equals": 2},
		// Row 0 now reads "log:A" (the reactive update reached
		// the slot-content child).  Before the fix, this still
		// read "log:a".
		{"assertText": "span.logger", "equals": "log:A"},
	})
}

// SlotContentInEachInIf — combined: the slot-in-each pattern
// wrapped in an {#if}.  Verifies that toggling the outer branch
// off+on rebuilds the rows cleanly AND inner-slot reactivity
// survives across the toggle.
func TestRuntimeNestingMatrix_SlotContentInEachInIf(t *testing.T) {
	wrap := `<script>
  export let label = '';
</script>
<template><div class="wrap"><span class="lbl">{label}</span><slot/></div></template>`
	child := `<script>
  export let label = '';
</script>
<template><span class="logger">log:{label}</span></template>`
	app := `<script>
  import Wrapper from './Wrapper.vela';
  import ChildLog from './ChildLog.vela';
  let items = $state.deep([{v:'a'},{v:'b'}]);
  let show = $state(true);
  let n = $state(0);
</script>
<template><div>
  <button class="b" @click={() => { n++; items[0].v = 'v' + n; }}>bump</button>
  <button class="t" @click={() => show=!show}>toggle</button>
  {#if show}
    {#each items as it}
      <Wrapper label="row"><ChildLog label={it.v} /></Wrapper>
    {/each}
  {/if}
</div></template>`
	assertRuntimeSet(t, "App", map[string]string{
		"App":      app,
		"Wrapper":  wrap,
		"ChildLog": child,
	}, []rtStep{
		{"assertCount": "span.logger", "equals": 2},
		{"click": "button.b"},
		{"assertText": "div.wrap:nth-of-type(1) span.logger", "equals": "log:v1"},
		// Hide + show — rebuilds with current state.
		{"click": "button.t"},
		{"assertCount": "span.logger", "equals": 0},
		{"click": "button.t"},
		{"assertText": "div.wrap:nth-of-type(1) span.logger", "equals": "log:v1"},
		// Reactivity still alive after the toggle.
		{"click": "button.b"},
		{"assertText": "div.wrap:nth-of-type(1) span.logger", "equals": "log:v2"},
	})
}

// ComponentDirectChildSelector — bug 4 verification.
//
// Before the fix: a <Child/> inside a parent was mounted into a
// `<div style="display:contents">` wrapper.  display:contents
// makes the wrapper layout-transparent but the <div> remains in
// the DOM tree — so CSS like `.parent > .child` does not match
// the component's root.  CSS specificity selectors that depend
// on direct-child relationships broke silently.
//
// After: the wrapper is replaced by a `<!-- comp -->` anchor.
// Comments are not elements; `>` and :nth-child skip them.  The
// component's $$root is inserted before the anchor, becoming a
// true direct child of the parent.
//
// This test asserts at the JS-runtime level that querySelector
// finds the child's root via a direct-child selector, AND that
// document-order siblings still come out in the right order
// (verifying the anchor-comment positioning works).
func TestRuntimeNestingMatrix_ComponentDirectChildSelector(t *testing.T) {
	child := `<template><b class="grand">grand</b></template>`
	app := `<script>
  import Child from './Child.vela';
</script>
<template>
  <div class="parent">
    <span class="sib1">a</span>
    <Child />
    <span class="sib2">b</span>
  </div>
</template>`
	assertRuntimeSet(t, "App", map[string]string{
		"App":   app,
		"Child": child,
	}, []rtStep{
		// The component's <b> is a TRUE direct child of .parent —
		// before the fix this returned null because of the wrapper.
		{"assertCount": "div.parent > b.grand", "equals": 1},
		// Siblings still flank the component in document order.
		// Use child-position checks via :nth-child counting:
		// .parent has 3 element children — sib1 first, b in the
		// middle, sib2 last.
		{"assertText": "div.parent > :first-child", "equals": "a"},
		{"assertText": "div.parent > :last-child", "equals": "b"},
		// And the component's b is the second element child.
		{"assertText": "div.parent > :nth-child(2)", "equals": "grand"},
	})
}

// ComponentDispatchedEventReachesParentHandler — verify the
// @event listener on a component still fires when the component
// dispatches its CustomEvent.  Before bug-4, the listener was on
// the wrapper div; the component dispatched on $$target (== the
// wrapper) and the listener caught it.  After bug-4, the
// listener is on inst.el (the component's $$root); the component
// dispatches on $$root.  Same effect, different mechanism.
func TestRuntimeNestingMatrix_ComponentEventStillFires(t *testing.T) {
	child := `<script>
  import { $$dispatch } from 'vela';
  export let n = 0;
  function bump() { $$dispatch('ping', { n }); }
</script>
<template><button class="fire" @click={bump}>fire</button></template>`
	app := `<script>
  import Child from './Child.vela';
  let count = $state(0);
</script>
<template>
  <div>
    <Child n={3} @ping={(e) => count = count + e.detail.n} />
    <span class="c">{count}</span>
  </div>
</template>`
	assertRuntimeSet(t, "App", map[string]string{
		"App":   app,
		"Child": child,
	}, []rtStep{
		{"assertText": "span.c", "equals": "0"},
		{"click": "button.fire"},
		{"assertText": "span.c", "equals": "3"},
		{"click": "button.fire"},
		{"assertText": "span.c", "equals": "6"},
	})
}
