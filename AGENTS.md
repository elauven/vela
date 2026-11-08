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
- Wrap a part of the page that may fail (a chart, a widget, third-party
  data) in an error boundary, so a failure shows a fallback instead of
  breaking the page:

  ```vela
  {#boundary}
    <Chart :rows={rows} />
  {:catch error, retry}
    <p>{error.message}</p>
    <button @click={retry}>Retry</button>
  {/boundary}
  ```

  It catches rendering, component start and update, `$derived`, `$effect`,
  late `{#await}` branches and `lazy()` loading — not event handlers or
  other async code. Report errors from a `vela:error` listener; on the
  server only a `vela.PublicError` message reaches `error.message`.

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
- Use `<vela:component :this={views[mode]} :rows={rows} />` when the
  component is chosen by a value. `:this` must be dynamic; `null`,
  `undefined` and `false` render nothing. Props the current component does
  not declare are ignored, so keep the props every candidate needs on the
  tag and use `bind:` only for a `$bindable` prop they all declare.
- Load a rarely shown component on demand with
  `const Panel = lazy(() => import('./Panel.vela'))` (literal path, `lazy`
  from `vela`) and render it with `<vela:component :this={Panel} />`. With
  SSR, call `await preloadHydration(target)` before `hydrate()`.
- Import from `vela` only `onMount`, `onDestroy` and runtime exports such as
  `tick`; import stores from `vela/store`. Other names are
  `VELA-UNKNOWN-IMPORT`.
- Set the page title, meta tags, the canonical link and JSON-LD from the
  component that knows them with one `<vela:head>` at the top level of its
  template:

  ```vela
  <vela:head>
    <title>{user.name} — Site</title>
    <meta name="description" :content={user.bio} />
    <link rel="canonical" :href={canonicalUrl} />
  </vela:head>
  ```

  Inside it use only `<title>`, `<meta>`, `<link>`,
  `<script type="application/ld+json">` and `{#if}`
  (`VELA-HEAD-CONTENT`). The deepest component wins a title, a meta `name` or
  `property` and the canonical link; in a Vela App it also overrides
  `r.Head()` of its own level, so keep `r.Head()` for values only Go has.
- Pass values that many levels need (theme, current user, services) through
  context: `setContext('key', value)` in an ancestor's script,
  `getContext('key')` in a descendant's script, with string-literal keys.
  Call them only while the component starts — never in handlers, `$effect`,
  `onMount`, `$derived` or the template (`VELA-CONTEXT-OUTSIDE-INIT`).
  Context is not reactive: put a `$state.deep` object in it and read it as
  `let theme = $state.deep(getContext('theme'))` to follow changes.
- In a Vela App, read the route, form and data of the page below the route
  components with `let page = $state.deep(getPage())`, importing
  `getPage` from `'#app/page.js'`, instead of passing `route` and `data`
  down as props.
- Import deep modules through `package.json` `"imports"` (`#lib/*`) only
  with string targets inside the package; components keep relative
  `.vela` imports (`VELA-SUBPATH-IMPORT`).

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

## TypeScript

TypeScript is experimental (`docs/TYPESCRIPT.md`). JavaScript stays
first-class; use TypeScript when the project is TypeScript (it has a
`tsconfig.json` with `"types": ["@govela/runtime/ambient"]`, or
`"typescript": true` in `vela.app.json`).

```vela
<script lang="ts" generics="T extends { id: string }">
  import { onMount } from 'vela';
  import type { Filter } from './filter.ts';

  interface $$Events {
    select: { item: T };
  }

  export let items: T[];
  export let filter: Filter | null = null;
  export let query: string = $bindable('');

  let selected = $state<T | null>(null);
  let visible = $derived(items.filter((item) => item.id.includes(query)));

  function choose(item: T): void {
    selected = item;
    $$dispatch('select', { item });
  }
</script>

<template>
  {#each visible as item (item.id)}
    <button @click={(event: MouseEvent) => choose(item)}>{item.id}</button>
  {/each}
</template>
```

- Write `<script lang="ts">`. Import local modules as `./util.ts` and
  components as `./Card.vela`.
- Use only erasable TypeScript: annotations, `interface`, `type`,
  `as`, `satisfies`, `!`, generics, `import type`. Do not write `enum`
  (use an object `as const` and `type E = (typeof E)[keyof typeof E]`),
  namespaces with values, parameter properties, `import x = require()` or
  `<T>x` casts: they are `VELA-TS-NON-ERASABLE`.
- Import types with `import type { X }` or `{ type X }`; export them with
  `export type`. A value import used only as a type is
  `VELA-TS-TYPE-IMPORT`.
- Annotate props: `export let title: string = ''` (optional for the
  parent), `export let id: number` (required),
  `export let value: string = $bindable('')`. Give runes type arguments
  when the initial value does not say the type: `$state<User | null>(null)`.
- Declare component events in `interface $$Events` (names to `detail`
  types) and named slots in `interface $$Slots`; `$$dispatch` is then
  checked.
- A generic component declares its type parameters with
  `generics="T extends …"` on `<script>`.
- Template expressions may use `as`, `!` and annotated arrow parameters;
  `{#each}` and `{#await}` names take no annotations, and `bind:` targets
  stay plain names.
- In a TypeScript Vela App, type route components with their generated
  `./$types`: `import type { PageProps } from './$types'`, then
  `export let data: PageProps['data']`, `export let route: PageProps['route']`.
  Change the Go loader, not the types, when the data changes.
- Run `vela check --project src --types` (it needs TypeScript 5.8 or 6.x
  in `node_modules`). The build never checks types: a type error does not
  fail `vela`, so the check must pass separately.

## Unsupported syntax and assumptions

Do not generate or rely on the following in Vela applications:

- TypeScript that generates code (`enum`, value namespaces, parameter
  properties), `.tsx`/`.jsx` modules, or JSX.
- Svelte 5 `$props()`, snippets, or `{@render}`.
- `use:` actions, `transition:`, `in:`, or `out:` directives.
- `<svelte:component>` (use `<vela:component :this={…}>`), `<svelte:head>`
  (use `<vela:head>`), `<svelte:element>`, `<svelte:self>`, `<svelte:window>`,
  `<svelte:document>`, or `<svelte:body>`.
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
7. In a TypeScript project, `vela check --project src --types` passes.
8. Run `go test ./...` after changing compiler sources, runtime fixtures, or
   examples that exercise compiler behaviour.
