package main

import (
	"strings"
	"testing"

	"github.com/elauven/vela/internal/sfcast"
)

// boundary_test.go — {#boundary}…{:catch error, retry}…{/boundary} (task 091)
// in the parser and the browser runtime. The server side is in
// ssr_boundary_test.go.

func TestBoundaryParses(t *testing.T) {
	tmpl, err := ParseTemplate(`{#boundary}<p>a</p>{:catch error, retry}<b>{error.message}</b>{/boundary}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(tmpl) != 1 {
		t.Fatalf("nodes = %d", len(tmpl))
	}
	node, ok := tmpl[0].(*sfcast.BoundaryBlockNode)
	if !ok {
		t.Fatalf("node is %T", tmpl[0])
	}
	if node.ErrorName != "error" || node.RetryName != "retry" || len(node.Children) != 1 || len(node.CatchChildren) != 1 {
		t.Fatalf("node = %+v", node)
	}
	for _, src := range []string{
		`{#boundary}<p>a</p>{:catch}<b>x</b>{/boundary}`,
		`{#boundary}<p>a</p>{:catch e}<b>{e}</b>{/boundary}`,
		`{#boundary}<p>a</p>{:catch}{/boundary}`,
	} {
		if _, err := ParseTemplate(src); err != nil {
			t.Errorf("%s: %v", src, err)
		}
	}
}

func TestBoundaryParseErrors(t *testing.T) {
	for src, want := range map[string]string{
		`{#boundary}<p>a</p>{/boundary}`:                       "needs a {:catch error, retry} branch",
		`{#boundary}<p>a</p>{:catch a, b, c}x{/boundary}`:      "at most two names",
		`{#boundary}<p>a</p>{:catch a.b}x{/boundary}`:          "plain JavaScript identifier",
		`{#boundary}<p>a</p>{:catch e, e}x{/boundary}`:         "binds e twice",
		`{#boundary}<p>a</p>{:catch e}x{/if}`:                  "must end with {/boundary}",
		`{#boundary}<p>a</p>{:else}x{/boundary}`:               "expected {:catch}",
		`{#boundary}<p>a</p>{:catch e}x{:catch f}y{/boundary}`: "must end with {/boundary}",
	} {
		_, err := ParseTemplate(src)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v, want %q", src, err, want)
		}
	}
}

// boundaryChild throws while it initialises when globalThis.__boom is set,
// and while it renders when its prop n is 3.
const boundaryChild = `<script>
  export let n = 0;
  if (globalThis.__boom) throw new Error('boom at init');
  function check(value) {
    if (value === 3) throw new Error('three');
    return value;
  }
</script>
<template><span class="child">{check(n)}</span></template>`

// TestBoundaryCatchesInitAndRetries: a child that throws while it starts
// shows the catch branch, the rest of the page works, retry recreates it.
func TestBoundaryCatchesInitAndRetries(t *testing.T) {
	page := `<script>
  import { Child } from './Child.js';
  globalThis.__boom = true;
  let count = $state(0);
  function fixAndRetry(retry) {
    globalThis.__boom = false;
    retry();
  }
</script>
<template><div>
  <button class="count" @click={() => count++}>{count}</button>
  {#boundary}
    <Child :n={count} />
  {:catch error, retry}
    <p class="failed">{error.message}</p>
    <button class="retry" @click={() => fixAndRetry(retry)}>retry</button>
  {/boundary}
</div></template>`
	assertRuntimeSet(t, "Page", map[string]string{"Page": page, "Child": boundaryChild}, []rtStep{
		{"assertText": "p.failed", "equals": "boom at init"},
		{"assertCount": "span.child", "equals": 0},
		{"click": "button.count"},
		{"assertText": "button.count", "equals": "1"},
		{"click": "button.retry"},
		{"assertCount": "p.failed", "equals": 0},
		{"assertText": "span.child", "equals": "1"},
		{"click": "button.count"},
		{"assertText": "span.child", "equals": "2"},
	})
}

// TestBoundaryCatchesUpdate: a render error after a prop update replaces
// the body; updates of the page go on, retry with a good value restores it.
func TestBoundaryCatchesUpdate(t *testing.T) {
	page := `<script>
  import { Child } from './Child.js';
  let count = $state(1);
  let errors = $state(0);
  function onError() {
    errors++;
  }
</script>
<template><div @vela:error={onError}>
  <button class="count" @click={() => count++}>{count}</button>
  <i class="errors">{errors}</i>
  {#boundary}
    <Child :n={count} />
  {:catch error, retry}
    <p class="failed">{error.message} at {count}</p>
    <button class="retry" @click={retry}>retry</button>
  {/boundary}
</div></template>`
	assertRuntimeSet(t, "Page", map[string]string{"Page": page, "Child": boundaryChild}, []rtStep{
		{"assertText": "span.child", "equals": "1"},
		{"click": "button.count"},
		{"assertText": "span.child", "equals": "2"},
		{"click": "button.count"},
		{"assertCount": "span.child", "equals": 0},
		{"assertText": "p.failed", "equals": "three at 3"},
		{"tick": true},
		{"assertText": "i.errors", "equals": "1"},
		{"click": "button.count"},
		{"assertText": "p.failed", "equals": "three at 4"},
		{"click": "button.retry"},
		{"assertCount": "p.failed", "equals": 0},
		{"assertText": "span.child", "equals": "4"},
	})
}

// TestBoundaryCatchesOwnFlushAndEffects: errors of a child's own update
// (its template after a handler and its $effect) reach the boundary
// through the child's view.
func TestBoundaryCatchesOwnFlushAndEffects(t *testing.T) {
	child := `<script>
  let n = $state(0);
  $effect(() => {
    if (n === 2) throw new Error('effect at ' + n);
  });
  function check(value) {
    if (value === 4) throw new Error('render at ' + value);
    return value;
  }
</script>
<template><button class="inc" @click={() => n++}>{check(n)}</button></template>`
	page := `<script>
  import { Child } from './Child.js';
</script>
<template><div>
  {#boundary}
    <Child />
  {:catch error, retry}
    <p class="failed">{error.message}</p>
    <button class="retry" @click={retry}>retry</button>
  {/boundary}
</div></template>`
	assertRuntimeSet(t, "Page", map[string]string{"Page": page, "Child": child}, []rtStep{
		{"click": "button.inc"},
		{"assertText": "button.inc", "equals": "1"},
		{"click": "button.inc"},
		{"assertText": "p.failed", "equals": "effect at 2"},
		{"assertCount": "button.inc", "equals": 0},
		{"click": "button.retry"},
		{"assertText": "button.inc", "equals": "0"},
	})
}

// TestBoundaryNested: the inner boundary takes the error; an error of its
// catch branch goes to the outer one.
func TestBoundaryNested(t *testing.T) {
	page := `<script>
  import { Child } from './Child.js';
  let n = $state(1);
  let catchFails = $state(false);
  function boom(fails, message) {
    if (fails) throw new Error('catch failed');
    return message;
  }
</script>
<template><div>
  <button class="up" @click={() => n++}>{n}</button>
  <button class="cf" @click={() => catchFails = true}>cf</button>
  {#boundary}
    <b class="outer">outer</b>
    {#boundary}
      <Child :n={n} />
    {:catch error}
      <p class="inner">{boom(catchFails, error.message)}</p>
    {/boundary}
  {:catch error}
    <p class="outer-failed">{error.message}</p>
  {/boundary}
</div></template>`
	assertRuntimeSet(t, "Page", map[string]string{"Page": page, "Child": boundaryChild}, []rtStep{
		{"click": "button.up"},
		{"click": "button.up"},
		{"assertText": "p.inner", "equals": "three"},
		{"assertText": "b.outer", "equals": "outer"},
		{"click": "button.cf"},
		{"assertText": "p.outer-failed", "equals": "catch failed"},
		{"assertCount": "b.outer", "equals": 0},
	})
}

// TestBoundaryWithoutErrorKeepsBody: content without errors renders and
// updates as without the block, and an error outside a boundary is not
// caught.
func TestBoundaryWithoutErrorKeepsBody(t *testing.T) {
	page := `<script>
  let items = $state(['a', 'b']);
</script>
<template><div>
  <button class="add" @click={() => items = [...items, 'c']}>add</button>
  {#boundary}
    {#each items as item (item)}<i class="row">{item}</i>{/each}
  {:catch}
    <p class="failed">failed</p>
  {/boundary}
</div></template>`
	assertRuntime(t, "Page", page, []rtStep{
		{"assertCount": "i.row", "equals": 2},
		{"click": "button.add"},
		{"assertCount": "i.row", "equals": 3},
		{"assertCount": "p.failed", "equals": 0},
		{"remount": true},
		{"assertCount": "i.row", "equals": 2},
	})
}
