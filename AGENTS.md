# Vela application authoring guide

Use this guide when creating or editing Vela applications. It is written for
both people and AI agents. It defines the preferred authoring style; it does
not describe compiler internals.

For feature availability and known limits, read `docs/FEATURES.md`. For longer
explanations and examples, read `docs/GUIDE.md`. For server rendering, read
`docs/SSR.md`. If a compiler diagnostic conflicts
with an old example or historical document, trust the diagnostic and the
current feature matrix.

## Component shape

One component is one PascalCase `.vela` file. A component may contain one of
each optional section, in any order:

```vela
<script>
  // imports, props, reactive state, derived values, handlers
</script>

<template>
  <!-- component DOM -->
</template>

<style>
  /* component-scoped CSS */
</style>
```

- Use PascalCase component filenames and tags: `UserCard.vela` and
  `<UserCard />`.
- Import local Vela components by their `.vela` path:
  `import { UserCard } from './UserCard.vela';`.
- Keep application-specific utility functions in ordinary JavaScript modules;
  keep DOM and component state close to the component that owns it.
- Treat `<style>` as scoped to the component. Use `:global(...)` only for
  intentional document-level styling.

## Reactivity rules

```vela
<script>
  export let title = 'Untitled';
  export let value = $bindable('');

  let count = $state(0);
  let items = $state.deep([]);
  let doubled = $derived(count * 2);

  function increment() {
    count++;
  }
</script>
```

- Use `$state(initial)` for values that change by reassignment. A plain `let`
  is not reactive.
- Use `$state.deep(initial)` only when nested object or array mutation is
  required, such as `items.push(item)` or `user.profile.name = name`. Prefer
  plain `$state` for scalar values and immutable replacement of objects/arrays.
- Assign to the state binding when updating shallow state: `items = [...items,
  item]`. Do not mutate a plain `$state` object or array in place and expect a
  rerender.
- Use `$derived(expression)` for pure computed values. Do not assign, mutate
  state, perform I/O, or start asynchronous work inside a derived expression.
- Use `$effect(() => { ... })` only for work outside component rendering:
  browser APIs, subscriptions, timers, logging, and persistence. Return a
  cleanup function whenever the effect creates a subscription, listener, or
  timer.
- For one-time DOM setup and final teardown, import and use lifecycle hooks:

  ```js
  import { onMount, onDestroy } from 'vela';
  ```

- Declare incoming component data with `export let name = defaultValue`.
  Declare a two-way component prop only as
  `export let value = $bindable(defaultValue)`.
- Prefer named functions for non-trivial handlers and effects. Keep short
  inline handlers limited to one obvious state update.

## Template rules

### Values, attributes, and events

```vela
<template>
  <p>Hello, {title}</p>
  <a :href={profileUrl} :aria-label={title}>Profile</a>
  <button :disabled={saving} @click={save}>Save</button>
  <input bind:value={query} />
  <input type="checkbox" bind:checked={enabled} />
</template>
```

- Use `{expression}` for text interpolation.
- Use `:name={expression}` for every new dynamic HTML or component attribute.
  This is the project’s canonical style. Do not mix it with the compatible
  `name={expression}` shorthand in new code.
- Use `@event={handler}` for DOM event listeners. Do not use `onclick=` or
  Svelte 4 `on:event=` syntax.
- Use `bind:value`, `bind:checked`, and `bind:this` only when data must flow
  from the DOM back into component state. Use `:value={value}` for one-way DOM
  writes.
- Use `<Child bind:value={value} />` only when `Child` declares
  `export let value = $bindable(...)`; otherwise pass `:value={value}`.
- Use `class:name={condition}` for a conditional class and
  `style:property={expression}` for a single dynamic inline CSS property.
  Keep structural styling in the component stylesheet.
- A static `class="…"` and one `:class={expr}` on the same element are joined
  into one class value. Do not give any other attribute twice on one element
  (`VELA-DUPLICATE-ATTR`).
- Braces inside a quoted static attribute are literal text. For a computed
  value write `:title={`Hello ${name}`}`, not `title="Hello {name}"`.

### Control flow and lists

```vela
<template>
  {#if loading}
    <p>Loading…</p>
  {:else if error}
    <p class="error">{error.message}</p>
  {:else}
    {#each users as user, index (user.id)}
      <UserRow :user={user} :index={index} />
    {:else}
      <p>No users.</p>
    {/each}
  {/if}
</template>
```

- Use block syntax for conditional rendering: `{#if}`, `{:else if}`, `{:else}`,
  and `{/if}`.
- Use `{#each collection as item, index (item.id)}` when list item identity
  matters. Keys must be stable, unique, and derived from the item’s identity;
  never use a changing index as the key for reorderable data.
- Supply `{:else}` for an intentional empty state.
- Use `{#key identity}...{/key}` only when subtree state must be deliberately
  discarded and recreated after an identity change.
- Use the multiline await form only:

  ```vela
  {#await request}
    <p>Loading…</p>
  {:then result}
    <Result :value={result} />
  {:catch error}
    <p>{error.message}</p>
  {/await}
  ```

  The opening tag also supports one terminal branch without a pending state:

  ```vela
  {#await request then result}
    <Result :value={result} />
  {/await}

  {#await request catch error}
    <p>{error.message}</p>
  {/await}
  ```

### Components and slots

```vela
<template>
  <Card :title={title} @on:close={close}>
    <p>Body content</p>
    <svelte:fragment slot="actions">
      <button @click={save}>Save</button>
      <button @click={close}>Cancel</button>
    </svelte:fragment>
  </Card>
</template>
```

- Pass child inputs as props; do not manipulate a child’s DOM from the parent.
- Use default slots for primary content and named slots for fixed component
  extension points. Use `<svelte:fragment slot="name">` for several nodes in
  a named slot without adding a wrapper element.
- Use component events for child-to-parent notifications: the child calls the
  built-in `$$dispatch('close', detail)` (never imported), the parent listens
  with `@on:close={handler}` and reads `event.detail`. Keep event names and
  payloads small and explicit.
- Import from `vela` only `onMount`, `onDestroy` and runtime exports such as
  `tick`; import stores from `vela/store`. Other names are
  `VELA-UNKNOWN-IMPORT`.

## Server rendering

`vela --ssr` also generates a Go renderer for every component; the browser
hydrates the server HTML. Write components so the first render can be
computed on the server:

- Props, `$state`/`$derived` initializers, template expressions and the
  functions and local `.js` modules they call run on the server, translated
  to Go. Keep them free of browser APIs (`window`, `document`,
  `localStorage`, `Date.now()`, `Intl`).
- Put browser work in handlers, `$effect`, or `onMount`; these never run on
  the server.
- For content only the browser can compute, use
  `{#client}…{:else}fallback{/client}`, or declare the whole component
  `<script context="client">`.
- When a function must run on the server but cannot be translated, add a Go
  host (`//vela:host name` in the mirrored `ssrhost/` package). Follow the
  file and signature the diagnostic prints; do not change the JavaScript to
  silence it.
- Treat every `VELA-SSR-*` diagnostic as a build error to fix, not to
  work around. `--ssr-report` lists the outcome of every site.

## Unsupported syntax and assumptions

Do not generate or rely on the following in Vela applications:

- TypeScript or `<script lang="ts">`.
- Svelte 5 `$props()`, snippets, or `{@render}`.
- `use:` actions, `transition:`, `in:`, or `out:` directives.
- `<svelte:component>`, `<svelte:element>`, `<svelte:self>`,
  `<svelte:head>`, `<svelte:window>`, `<svelte:document>`, or `<svelte:body>`.
- `bind:group`, `bind:files`, dimension bindings, or `bind:innerHTML`.
- `{@html}`, `{@const}`, `{@debug}`, or event modifiers such as
  `@click.prevent` (call `event.preventDefault()` in the handler).
- Streaming SSR, islands or partial hydration, CSS source maps, or HMR.
  JavaScript source maps (`--sourcemap`) cannot be combined with `--minify`.
  `--watch` recompiles files but does not preserve component state in the
  browser.

## Completion checklist

Before handing off a Vela component or application:

1. Every value whose change must update the template is `$state`, `$state.deep`,
   a prop, or `$derived`.
2. Every `$derived` is pure; every resource-owning `$effect` or lifecycle hook
   has cleanup.
3. Dynamic attributes use `:attribute={expression}`; events use
   `@event={handler}`.
4. Reorderable lists use stable keys, and component two-way bindings target a
   `$bindable` child prop.
5. The component uses only features marked supported in `docs/FEATURES.md`.
6. For server-rendered applications, `vela --ssr` builds without `VELA-SSR-*`
   errors, and browser-only code is in handlers, effects, lifecycle hooks or
   `{#client}`.
7. Run `go test ./...` after changing compiler sources, runtime fixtures, or
   examples that exercise compiler behaviour.
