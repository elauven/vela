# Vela Component Guide

A practical guide to writing `.vela` components. This is the
component-author's reference — for compiler internals see `VELA.md`,
for the Svelte 5 feature comparison see `SVELTE_COMPARISON.md`.

A `.vela` file compiles to a self-contained ES module. No virtual
DOM, no runtime template interpretation — the compiler turns your
template into direct DOM construction code and wires reactivity at
build time.

---

## 1. File structure

A component is one `.vela` file with up to three sections:

```
<script>
  // component logic — state, props, functions
</script>

<template>
  <!-- markup -->
</template>

<style>
  /* scoped CSS */
</style>
```

All three are optional, but a component without a `<template>`
renders nothing. Order does not matter. There is exactly one of
each per file.

The compiler emits `<name>.js` — an ES module exporting a factory
function named after the file (`Button.vela` → `export function
Button(...)`).

---

## 2. Runes — reactive primitives

Runes are the `$`-prefixed functions that declare reactive state.
They are compiler intrinsics, not imports — you do not import them.

### `$state` — reactive value

```
<script>
  let count = $state(0);
  let name  = $state('Ada');
</script>
```

Reassigning a `$state` variable re-renders everything that depends
on it. A plain `let` without `$state` is *not* reactive — the
compiler warns if you assign to one and expect an update.

```
let total = $state(0);
total = total + 1;        // ✓ triggers re-render
let plain = 0;
plain = plain + 1;        // ✗ compiler warns: not reactive
```

### `$state.deep` — deep reactive value

Plain `$state` tracks *reassignment*. For objects and arrays whose
*nested* properties you mutate in place, use `$state.deep`:

```
<script>
  let user = $state.deep({ name: 'Ada', tags: [] });
</script>
```

```
user.name = 'Grace';     // ✓ tracked — nested mutation
user.tags.push('admin'); // ✓ tracked
```

`$state.deep` wraps the value in a recursive Proxy. Use plain
`$state` when you only ever replace the whole value — it has no
proxy overhead.

### `$derived` — computed value

A value computed from other reactive state. It recomputes
automatically when any input changes, and is read like a plain
variable:

```
<script>
  let price = $state(10);
  let qty   = $state(3);
  let total = $derived(price * qty);
</script>

<template><p>Total: {total}</p></template>
```

`$derived` expressions must be **pure** — no side effects, no
assignments. The compiler enforces this.

A `$derived` may depend on another `$derived`; the chain updates
transitively.

### `$effect` — side effect

Runs a side effect when its reactive dependencies change. Use it
for things that reach outside the component — DOM measurement,
logging, syncing to storage:

```
<script>
  let query = $state('');
  $effect(() => {
    console.log('query is now', query);
  });
</script>
```

The effect re-runs whenever any reactive value it *reads* changes
— including a `$derived` whose upstream changed. Effects are
batched: multiple state changes in the same tick coalesce into a
single re-run, flushed on a microtask.

An effect with **no** reactive reads runs once on mount and never
again — the compiler warns and suggests `onMount` instead.

To clean up, return a function from the effect:

```
$effect(() => {
  const id = setInterval(tick, 1000);
  return () => clearInterval(id);
});
```

---

## 3. Props

Props are inputs a parent passes to the component. Declare them
with `export let`:

```
<script>
  export let label = 'Click me';   // default value
  export let disabled = false;
</script>

<template>
  <button disabled={disabled}>{label}</button>
</template>
```

The default is used when the parent omits the prop.

### `$bindable` — two-way props

By default a prop flows one way: parent → child. Wrap the default
in `$bindable(...)` to allow the child to write back:

```
<script>
  export let value = $bindable('');
</script>
```

A parent then binds with `bind:` (see §6) and the two stay in
sync.

---

## 4. Lifecycle

`onMount` and `onDestroy` are imported from `vela`:

```
<script>
  import { onMount, onDestroy } from 'vela';

  onMount(() => {
    // runs after the component's DOM is in the document
  });

  onDestroy(() => {
    // runs when the component is torn down
  });
</script>
```

`onMount` may return a cleanup function, which runs on destroy —
equivalent to an `onDestroy`.

---

## 5. Template syntax

### Text interpolation

`{expr}` inserts a JavaScript expression as text:

```
<p>Hello, {name}!</p>
<p>{count} item{count === 1 ? '' : 's'}</p>
```

Expressions may contain anything — comparisons included. A `<`
inside `{...}` is part of the expression, not a tag:

```
<span>{i < limit ? 'under' : 'over'}</span>
```

### Attributes

Static attributes are plain HTML. A `{expr}` value makes an
attribute dynamic — it updates when the expression's dependencies
change:

```
<a href="/about">About</a>             <!-- static -->
<img src={imageUrl} alt={caption} />    <!-- dynamic -->
<button disabled={isBusy}>Save</button>
```

---

## 6. Directives

Directives are special attributes with a prefix.

### `@event` — event handlers

`@` binds a DOM event listener:

```
<button @click={() => count++}>+</button>
<form @submit={onSubmit}>...</form>
<input @input={(e) => query = e.target.value} />
```

The value is any expression evaluating to a function.

### `bind:` — two-way binding

`bind:value` keeps a form element and a state variable in sync —
it writes the variable into the DOM *and* updates the variable on
user input:

```
<script>let text = $state('');</script>
<template>
  <input bind:value={text} />
  <p>You typed: {text}</p>
</template>
```

### `bind:this` — element reference

`bind:this` writes the live DOM element into a variable once it
exists — for imperative access:

```
<script>
  let canvas = $state(null);
  onMount(() => { canvas.getContext('2d'); });
</script>
<template>
  <canvas bind:this={canvas}></canvas>
</template>
```

### `class:` — conditional class

`class:name={cond}` toggles a CSS class based on a condition:

```
<div class:active={isOpen}>...</div>
<li class:done={task.complete}>...</li>
```

Shorthand: `class:active` (no value) is equivalent to
`class:active={active}` — it uses the same-named variable.

```
<div class:selected>...</div>   <!-- uses the `selected` variable -->
```

### `style:` — inline style property

`style:prop={expr}` sets one inline CSS property:

```
<div style:color={textColor}>...</div>
<div style:width={percent + '%'}></div>
```

Hyphenated CSS names map to camelCase automatically — `style:font-size`
sets `.style.fontSize`. The same no-value shorthand applies.

---

## 7. Block constructs

Blocks are `{#...}` … `{/...}` control structures in the template.

### `{#if}` — conditional rendering

```
<template>
  {#if loggedIn}
    <p>Welcome back.</p>
  {:else if guest}
    <p>Browsing as guest.</p>
  {:else}
    <button @click={login}>Log in</button>
  {/if}
</template>
```

`{:else if}` and `{:else}` are optional. The block's content is
inserted as direct children of its parent — no wrapper element is
added, so `parent > child` CSS selectors work as written.

### `{#each}` — list rendering

```
<template>
  <ul>
    {#each items as item}
      <li>{item.name}</li>
    {/each}
  </ul>
</template>
```

With the index:

```
{#each items as item, i}
  <li>{i + 1}. {item.name}</li>
{/each}
```

Each row's element is a direct child of the list parent — no
wrapper. The list reconciles in place when `items` changes.

**Reconciliation.** When `items` is mutated in place (e.g. a
`$state.deep` array that you `.push`/`.pop`/`.splice`), the
reconciler reuses existing rows: the overlapping prefix is updated
in place via the row's `update` closure (which also calls `.set()`
on any child component in the row), surplus rows are destroyed
from the tail, and new rows are appended for any growth. DOM
identity, input focus, scroll position, native selection, and IME
composition all survive these updates. A fresh array reference
(`items = items.filter(…)`) falls back to a full rebuild.

**Keyed each.** The unkeyed reconciler is *positional* (index-based):
a middle insert or delete shifts every subsequent row's content,
so the row that was previously showing item *B* now shows item
*C*. The DOM nodes are reused, but the row-to-item mapping changes
silently. For lists where row identity matters — especially lists
of child components carrying their own state, or lists with
animation/transition — add a key:

```
{#each items as item (item.id)}
  <ItemRow item={item} />
{/each}
```

With a key, the reconciler matches rows by identity, not position:
inserting `X` between `B` and `C` keeps `B` and `C` themselves
unchanged and creates one new row for `X`. The compiler emits a
hint when it sees an unkeyed `{#each}` over a row that contains a
child component — that's the shape where the positional default
most often surprises.

### `{#key}` — keyed re-creation

`{#key expr}` destroys and rebuilds its content whenever `expr`
changes — useful to force a fresh subtree (resetting animations,
re-running mount logic):

```
{#key currentUserId}
  <UserProfile id={currentUserId} />
{/key}
```

### `{#await}` — promises

Renders different content for the pending / resolved / rejected
states of a promise:

```
{#await loadData()}
  <p>Loading…</p>
{:then data}
  <p>Got {data.length} rows.</p>
{:catch err}
  <p>Failed: {err.message}</p>
{/await}
```

`{:then}` and `{:catch}` are optional.

---

## 8. Components

### Importing and using

Import another `.vela` component and use it as an element with a
capitalized tag:

```
<script>
  import Button from './Button.vela';
</script>

<template>
  <Button label="Save" @click={onSave} />
</template>
```

Pass props as attributes. Pass dynamic props with `{expr}`. Wire
a child's events with `@`. For a `$bindable` child prop, bind with
`bind:`:

```
<Modal bind:open={modalOpen} />
```

### Slots

A component can accept child content via `<slot>`:

```
<!-- Card.vela -->
<template>
  <div class="card">
    <slot></slot>
  </div>
</template>
```

```
<!-- usage -->
<Card>
  <p>This goes into the slot.</p>
</Card>
```

### Custom events

A component dispatches events to its parent with `$$dispatch`:

```
<!-- Toast.vela -->
<script>
  function close() {
    $$dispatch('close', { reason: 'user' });
  }
</script>
```

```
<!-- parent -->
<Toast @close={(e) => console.log(e.detail.reason)} />
```

The event bubbles as a `CustomEvent`; the payload is in
`event.detail`.

---

## 9. Scoped styles

CSS in a `<style>` block is **scoped to the component** — a
generated attribute is added to the component's elements and to
every selector, so styles never leak:

```
<template><p class="msg">Hi</p></template>
<style>
  .msg { color: rebeccapurple; }
</style>
```

Only this component's `.msg` elements are affected.

### CSS delivery

By default the scoped CSS is injected at runtime (CSS-in-JS). The
compiler's `--css` flag chooses delivery:

- `--css=inject` (default) — CSS travels inside the JS, injected
  into `document.head` on mount.
- `--css=external` — the scoped CSS is written to a sibling
  `<name>.css` file; you link it yourself or bundle it.

Scoping is identical either way.

---

## 10. A complete example

```
<script>
  import { onMount } from 'vela';

  export let title = 'Counter';

  let count = $state(0);
  let doubled = $derived(count * 2);

  $effect(() => {
    document.title = `${title}: ${count}`;
  });

  onMount(() => console.log('mounted'));

  function reset() { count = 0; }
</script>

<template>
  <section class="counter">
    <h2>{title}</h2>
    <p class:zero={count === 0}>
      {count} (doubled: {doubled})
    </p>
    <button @click={() => count++}>+</button>
    <button @click={reset} disabled={count === 0}>Reset</button>

    {#if count > 10}
      <p class="warn">That's a lot.</p>
    {/if}
  </section>
</template>

<style>
  .counter { padding: 1rem; }
  .zero    { opacity: 0.5; }
  .warn    { color: crimson; }
</style>
```

---

## 11. Quick reference

| Syntax | Meaning |
|---|---|
| `$state(v)` | reactive value |
| `$state.deep(v)` | deep-reactive object/array |
| `$derived(expr)` | computed value |
| `$effect(() => …)` | side effect on dependency change |
| `export let x = d` | prop with default `d` |
| `export let x = $bindable(d)` | two-way prop |
| `{expr}` | text / attribute interpolation |
| `@event={fn}` | event listener |
| `bind:value={x}` | two-way form binding |
| `bind:this={x}` | element reference |
| `class:name={cond}` | conditional class |
| `style:prop={expr}` | inline style property |
| `{#if}` `{:else if}` `{:else}` `{/if}` | conditional |
| `{#each list as item, i}` `{/each}` | list |
| `{#key expr}` `{/key}` | keyed re-creation |
| `{#await p}` `{:then v}` `{:catch e}` `{/await}` | promise states, including `{#await p then v}` and `{#await p catch e}` shortcuts |
| `<slot>` | child-content placeholder |
| `$$dispatch(name, detail)` | emit a custom event |
| `onMount` / `onDestroy` | lifecycle (import from `vela`) |

## Source maps

Source maps are experimental. Use `vela --sourcemap Component.vela` to emit an external v3 JavaScript source map alongside AST-generated output. Script code and stable template expressions map to their original SFC locations, including repeated expressions at different offsets. Source maps cannot be combined with `--minify` or `--codegen=legacy`; CSS source maps are not emitted.


### Browser verification

The optional Chromium source-map suite uses Playwright. Install its local test dependency and browser once:

```sh
npm install --save-dev playwright
npx playwright install chromium
go test ./... -run TestBrowserSourceMap
```

The suite skips with this installation instruction when Node, Playwright, or its Chromium binary is unavailable.
