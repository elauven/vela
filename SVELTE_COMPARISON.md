# Vela vs Svelte 5 — Syntax & Functionality Comparison

A side-by-side reference for how Vela's surface area aligns with — and
diverges from — Svelte 5 (as of late 2025 / Svelte 5.45.x).

The point of this document isn't to argue Vela should match Svelte
exactly. It's to make the choices explicit so you can decide which
divergences are intentional vs incidental, and what you'd want to
adopt, change, or skip.

---

## TL;DR

- **Reactivity primitives**: Vela has `$state`, `$state.deep`, `$derived`,
  `$effect`, and `$bindable`; props use `export let` rather than `$props()`.
- **Event syntax**: Vela uses `@click=` (Vue-style); Svelte 5 uses
  `onclick=` (HTML-attribute style). Both rejected Svelte 4's `on:click=`.
- **Bindings (`bind:`)**: Same syntax for both. Vela's `bind:this` and
  `bind:value`/`bind:checked` work as expected.
- **Template control flow**: `{#if}`, `{#each}`, `{#await}`, `{#key}`, and
  their branch syntax are supported. Snippets and `{@html}` are not.
- **Slots**: Vela uses `slot="name"` / `<slot>` (Svelte-4 style).
  Svelte 5 deprecated those in favor of **snippets** (`{#snippet}` /
  `{@render}`). This is the biggest single design divergence.
- **Surface area**: Vela covers maybe 60% of what people actually
  use in Svelte. The missing pieces are mostly the long tail (`class:`,
  `style:`, transitions, `<svelte:window>`, etc.), not the
  fundamentals.

---

## Reactivity primitives

### `$state` — reactive state

Same name, same purpose. The crucial difference is **deep reactivity**.

**Svelte 5**: arrays and objects passed to `$state` become deep
proxies. Mutating them in place triggers updates.

```svelte
<script>
  let arr = $state([1, 2, 3]);
  function add() {
    arr.push(4);             // ✓ triggers re-render
  }
  let user = $state({ name: 'A', address: { city: 'X' } });
  function move() {
    user.address.city = 'Y'; // ✓ triggers re-render
  }
</script>
```

**Vela**: `$state` only tracks **assignments to the variable itself**.
Mutating contents won't trigger updates.

```vela
<script>
  let arr = $state([1, 2, 3]);
  function add() {
    arr.push(4);   // ✗ no re-render (compiler doesn't see an assignment)
    arr = [...arr, 4]; // ✓ assignment triggers $$set_arr
  }
</script>
```

This is a real ergonomic gap. To match Svelte 5 you'd need to wrap
state in a `Proxy` at runtime and have the proxy trap `set` and
`deleteProperty` to fire setters. It's doable but a non-trivial
runtime addition.

**Verdict**: same primitive, weaker semantics. Users coming from
Svelte will get surprised by `.push()`/`.splice()` not working.

### `$derived` — computed values

Both compute values that update when dependencies change. Both
auto-track reads of `$state` vars.

**Svelte 5** also has `$derived.by(() => ...)` for multi-line
computations:

```svelte
let total = $derived.by(() => {
  let sum = 0;
  for (const n of numbers) sum += n;
  return sum;
});
```

**Vela** only has the expression form `let x = $derived(expr)`. For
multi-line you'd need to wrap an IIFE.

### `$effect` — side effects

Same idea. Svelte 5 has variants — `$effect.pre` (runs *before* DOM
updates) and `$effect.root` (creates an effect outside a component).

Vela has only the base `$effect(() => {})`. Cleanup is returned-fn
style on both:

```svelte
$effect(() => {
  const id = setInterval(...);
  return () => clearInterval(id);
});
```

Vela follows the same shape, so existing knowledge transfers.

### `$props` — component props

This is where syntax **really** differs.

**Svelte 5**: one destructure call with all props.

```svelte
<script lang="ts">
  let { label, variant = 'primary', onclick } = $props();
</script>
```

**Vela**: per-prop `export let`. This is Svelte-4 syntax kept alive.

```vela
<script>
  export let label;
  export let variant = 'primary';
  export let onclick;
</script>
```

The Svelte 5 form has a few advantages: one type annotation site for
TypeScript, `...rest` works naturally, and renaming uses
destructure (`let { class: klass } = $props()`) instead of
`export { x as class }`. Vela's form is simpler to parse but less
expressive.

### `$bindable` — two-way prop binding

Same purpose: declare that a prop can flow back to the parent.

**Svelte 5**: lives inside the destructure.

```svelte
<script>
  let { value = $bindable('') } = $props();
</script>
```

**Vela**: combined with `export let`.

```vela
<script>
  export let count = $bindable(0);
</script>
```

Mechanics under the hood are similar: parent emits a
`$$onChange_X` callback alongside the prop, child fires it from its
setter. Vela got this working as of the last session.

---

## Event handlers

Both frameworks have moved away from Svelte 4's `on:click=`. They
went in different directions:

| | Svelte 5 | Vela |
|---|---|---|
| Click | `<button onclick={fn}>` | `<button @click={fn}>` |
| Input | `<input oninput={fn}>` | `<input @input={fn}>` |
| Modifier | `onclick={preventDefault(fn)}` (function wrapper) | (no modifier syntax) |
| Custom event | `oncustomevent={fn}` | `@customevent={fn}` |

Svelte 5's choice is "this is just an HTML attribute, treat it like
one." Vela's `@` is a clear marker that's hard to confuse with a
regular attribute. Both readable, just different philosophies.

**Modifier syntax**: Svelte 4 had `on:click|preventDefault={fn}`.
Svelte 5 dropped it in favor of explicit wrapper functions. Vela has
no modifier syntax — same approach.

---

## Bindings (`bind:`)

Same syntax on both:

```svelte
<input bind:value={name} />
<input type="checkbox" bind:checked={agree} />
<input bind:this={inputEl} />
<MyChild bind:count={parentCount} />
```

Vela supports the four most common cases (`bind:value`,
`bind:checked`, `bind:this`, two-way on components). Svelte 5 supports
many more (`bind:group`, `bind:files`, `bind:innerHTML`,
`bind:clientWidth`, etc.). The semantics where they overlap are
identical.

---

## Template control flow

### Both supported, identical syntax

```svelte
{#if cond}
  <p>yes</p>
{:else if other}
  <p>maybe</p>
{:else}
  <p>no</p>
{/if}

{#each items as item, i (item.id)}
  <li>{i}: {item.name}</li>
{/each}
```

### Svelte 5 features Vela does not support

```svelte
{#snippet item(arg)}
  <li>{arg.name}</li>
{/snippet}
{@render item(thing)}

{@html rawString}
{@const local = expr}
{@debug var1, var2}
```

`{#await}` and `{#key}` are commonly used. `{#snippet}` is the big
one — it's Svelte 5's replacement for named slots. `{@html}` is
useful but a security risk (no XSS protection); reasonable to leave
out.

---

## Slots vs Snippets

This is **the** biggest architectural divergence.

### Vela (Svelte-4 style)

```vela
<!-- Card.vela -->
<template>
  <article>
    <slot name="header" />
    <slot />                  <!-- default slot -->
    <slot name="footer" />
  </article>
</template>

<!-- Usage -->
<Card>
  <h1 slot="header">Title</h1>
  <p>body content</p>
  <button slot="footer">OK</button>
</Card>
```

### Svelte 5 (snippets)

```svelte
<!-- Card.svelte -->
<script>
  let { header, children, footer } = $props();
</script>
<article>
  {#if header}{@render header()}{/if}
  {@render children()}
  {#if footer}{@render footer()}{/if}
</article>

<!-- Usage -->
<Card>
  {#snippet header()}<h1>Title</h1>{/snippet}
  <p>body content</p>          <!-- becomes `children` automatically -->
  {#snippet footer()}<button>OK</button>{/snippet}
</Card>
```

**Why snippets are better**: they can take arguments. For lists, this
replaces the awkward "scoped slot" pattern. Snippets are first-class
values — passable as props, declarable inside other markup, etc.

**Why slots are simpler**: less to learn, the named-slot pattern
covers 80% of real use. Vela's current implementation works fine for
those cases.

A future direction: add snippets *in addition to* slots (Svelte 5 also
supports old slot syntax in legacy mode). That preserves your existing
component vocabulary while adding the list-row use case.

---

## Element directives

Vela doesn't implement these. They're real ergonomic wins in Svelte.

```svelte
<!-- Conditional classes -->
<div class:active={isActive} class:large={size > 100}>
<!-- compiles roughly to: class={[isActive && 'active', size > 100 && 'large']} -->

<!-- Conditional inline styles -->
<div style:color={color} style:font-size="{size}px">

<!-- Animations / transitions -->
<div transition:fade={{ duration: 300 }}>
<div in:fly={{ y: 50 }} out:fade>

<!-- Action — fn called with the element after mount -->
<input use:focus />
```

In Vela today, all of these are workable with longer-hand
constructs — `class={cond ? 'active' : ''}` for `class:`,
inline `style="..."` for `style:`, no equivalent for transitions
or actions. The missing one that costs the most is `use:action`
because it's how Svelte users plug in third-party DOM behaviors
(autoresize, tooltips, drag-and-drop libraries) cleanly. Worth
considering.

---

## Special elements

| Svelte 5 | Vela |
|---|---|
| `<svelte:component this={Comp}>` | ✗ no dynamic components |
| `<svelte:self>` | ✗ no recursive self-reference |
| `<svelte:head>` | ✗ |
| `<svelte:window>` / `<svelte:document>` / `<svelte:body>` | ✗ |
| `<svelte:fragment slot="x">` | ✓ supported |
| `<svelte:element this={tag}>` | ✗ no dynamic tag |

`<svelte:component>` and `<svelte:self>` are the two most common.
Dynamic components are useful (think tab switchers, multi-step
forms); self-references enable recursive components like tree views.
Both are mechanically straightforward to add — the parser sees a
tag name expression instead of a static tag.

`<svelte:head>` is the way SSR-friendly title/meta tags get into
`<head>`. Less critical for a client-only compiler.

---

## Component instance methods

Both produce a similar runtime interface. Vela's emitted shape:

```js
const instance = MyComponent($target, props, slots);
instance.set({ propName: newValue });  // push prop updates
instance.destroy();                      // teardown
```

Svelte 5 component instances expose props and bound state directly
on the instance via getters; there's no `.set()` method. Vela's
`.set()` is closer to Svelte 4's API. Practically equivalent.

---

## Things in Vela that Svelte doesn't have

A few places where Vela actually has *more* than Svelte:

- **`$$defer`** — the deferred-render mechanism for batching DOM
  updates within a microtask. Svelte has its own scheduler but
  it's not user-visible the same way.
- **`bind:` on plain `{value}` braced attributes** — Vela's parser
  auto-detects braced values on plain HTML elements as dynamic
  bindings (recent fix). Svelte requires explicit `bind:`.
- **Compiler-time scope analyzer + DCE pass** — Vela emits TDZ-aware
  scope graph diagnostics and (with `--minify`) eliminates dead
  branches. Svelte does some of this but Vela's is more aggressive.

---

## Things to be careful about (where Vela might surprise Svelte users)

1. **`arr.push(x)` doesn't work**. Use `arr = [...arr, x]`. This is
   the biggest "wait, why isn't it updating?" moment.
2. **`@click` not `onclick`**. If muscle memory says `onclick={fn}`
   it'll be parsed as a static string attribute.
3. **`@on:input` is wrong**. The `on:` prefix is Svelte 4 baggage —
   in Vela just write `@input`. Mixing Svelte 4's `on:` with Vela's
   `@` produces `@on:input` which the compiler treats as listening
   for an event literally named `on:input`.
4. **Slots, not snippets**. Want to pass a parameterized row
   renderer? You'll have to refactor — Vela doesn't have snippets.
5. **No `class:active={cond}` directive**. Use ternaries.
6. **No `use:action`**. Wire DOM behaviors via `bind:this={el}` and
   `$effect(() => { ... })`.
7. **Component props are `export let`**, not destructured `$props()`.

---

## Summary table

| Feature | Svelte 5 | Vela |
|---|---|---|
| `$state` | ✅ deep reactive | ⚠️ shallow (assignment-only) |
| `$derived` | ✅ + `.by()` | ✅ expression form only |
| `$effect` | ✅ + `.pre`, `.root` | ✅ base form only |
| `$props()` destructure | ✅ | ❌ uses `export let` |
| `$bindable` | ✅ in `$props()` | ✅ on `export let` |
| Event handlers | `onclick={fn}` | `@click={fn}` |
| `bind:value` / `:checked` / `:this` | ✅ | ✅ |
| `bind:` on components (two-way) | ✅ | ✅ |
| `bind:group` / `:files` / `:innerHTML` | ✅ | ❌ |
| `{#if}` / `{#each}` / `{:else}` | ✅ | ✅ |
| `{#await}` | ✅ | ✅ |
| `{#key}` | ✅ | ✅ |
| `{#snippet}` + `{@render}` | ✅ | ❌ |
| Named slots (`<slot name>`) | legacy | ✅ |
| `{@html}` / `{@const}` / `{@debug}` | ✅ | ❌ |
| `class:foo={cond}` | ✅ | ✅ |
| `style:foo={x}` | ✅ | ✅ |
| `transition:` / `in:` / `out:` | ✅ | ❌ |
| `use:action` | ✅ | ❌ |
| `<svelte:component>` (dynamic) | ✅ | ❌ |
| `<svelte:self>` (recursion) | ✅ | ❌ |
| `<svelte:head>` | ✅ | ❌ |
| `<svelte:window>` / `:document` / `:body` | ✅ | ❌ |
| `<svelte:element>` (dynamic tag) | ✅ | ❌ |
| `<svelte:fragment>` | ✅ | ✅ |
| Scoped CSS | ✅ | ✅ |
| Component `.set()` | (Svelte 4 era) | ✅ |
| Compile-time DCE | partial | ✅ (`--minify`) |

---

## What's worth adopting next

Ranked by user-visible impact relative to implementation cost, my
suggestions:

1. **`{#await}`** — common pattern, parser-only addition + a small
   runtime helper. Low cost, high payoff.
2. **`class:foo={cond}`** and **`style:foo={x}`** — both compile to
   conditional `setAttribute` / `style.setProperty` calls. Easy.
3. **`use:action`** — opens the door to a whole ecosystem of DOM
   utility libraries. Needs an analyzer pass to capture deps.
4. **`{#key}`** — small parser addition; codegen is "destroy and
   recreate the branch when the key changes."
5. **Deep reactivity via Proxy** — bigger lift but closes the
   `arr.push()` surprise. Worth doing once and getting right.
6. **Dynamic components** (`<svelte:component>`) — straightforward
   parser change; codegen reads the tag name from an expression.
7. **Snippets** — the big one. Allows passing row renderers and
   replaces named slots. Significant codegen work but matches where
   Svelte is heading.

Things I'd deprioritize: transitions (large runtime cost), the
`<svelte:*>` family (each is a different thing), `bind:group`/`:files`
(edge cases). They're nice when you need them but most apps don't.
