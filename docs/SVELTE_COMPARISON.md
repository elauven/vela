# Vela and Svelte 5

Vela's template language is close to Svelte's, so this page lists where the
two differ. Svelte 5 reference: 5.x (2025). Vela status:
[FEATURES.md](FEATURES.md).

## In short

- **Same model:** compile-time reactivity, no virtual DOM, scoped CSS,
  single-file components, `{#if}` / `{#each}` / `{#await}` / `{#key}`
  blocks.
- **Different syntax:** `@click` instead of `onclick`, `:attr={…}` for dynamic
  attributes, `export let` props, `$state.deep` for nested mutation.
- **Different scope:** Vela renders on the server in **Go** (Svelte renders in
  Node). The compiler does not bundle; routing, loaders and navigation are
  the separate Vela App layer ([APP.md](APP.md)), with loaders in Go where
  SvelteKit has `load` in JavaScript.
- **Not in Vela:** snippets, most `<svelte:*>` elements, `{@const}`,
  `{@debug}` and `{@render}`, and for now event modifiers (task 101).

## Reactivity

| | Svelte 5 | Vela |
| --- | --- | --- |
| State | `$state(v)` — deep by default; `$state.raw` for shallow | `$state(v)` — shallow (reassignment); `$state.deep(v)` for nested mutation |
| Derived | `$derived(expr)`, `$derived.by(fn)` | `$derived(expr)` only (`VELA-DERIVED-CALLBACK` for a callback) |
| Effects | `$effect`, `$effect.pre`, `$effect.root` | `$effect` with cleanup |
| Lifecycle | `onMount`, `onDestroy` | `onMount`, `onDestroy` from `vela` |
| Props | `let { a, b } = $props()` | `export let a = default` |
| Two-way props | `$bindable()` inside `$props()` | `export let value = $bindable(default)` |
| Stores | `svelte/store` | `vela/store` (`writable`, `readable`, `derived`) |

Vela keeps `$state` shallow so that the common case compiles to direct
assignments without a proxy; opt into the proxy where nested writes are
needed.

## Templates

| | Svelte 5 | Vela |
| --- | --- | --- |
| Text | `{expr}` | `{expr}` |
| Dynamic attribute | `href={url}` | `:href={url}` (`href={url}` accepted) |
| DOM event | `onclick={fn}` | `@click={fn}` |
| Event modifiers | — (Svelte 5 removed them) | — (`VELA-EVENT-MODIFIER`); planned as `@click.prevent`, `@keydown.enter` (task 101) |
| Bindings | `bind:value`, `bind:checked`, `bind:this`, `bind:group`, … | `bind:value`, `bind:checked`, `bind:this` |
| `class:` / `style:` directives | ✓ | ✓ |
| Static `class` with dynamic `class` | `class={[…]}` arrays and objects | `class="a" :class={b}` joined; no object or array form |
| `{#if}`, `{#each}` (keyed, `{:else}`), `{#await}`, `{#key}` | ✓ | ✓ |
| `{@html}` | ✓ | ✓ (not sanitized; also rendered by the Go server and adopted on hydration, where a server/client difference is a mismatch) |
| `{@const}`, `{@debug}`, `{@render}` | ✓ | — (`VELA-UNSUPPORTED-TAG` names the replacement) |
| Snippets | `{#snippet}` | — (use slots) |
| Browser-only content | `{#if browser}` with `$app/environment` | `{#client}…{:else}…{/client}`, `<script context="client">` |

## Components

| | Svelte 5 | Vela |
| --- | --- | --- |
| Import | `import Card from './Card.svelte'` | `import { Card } from './Card.vela'` (default import also works) |
| Child content | snippets, `children` | `<slot>`, named slots, fallback content, `<svelte:fragment slot>` |
| Events to parent | callback props | `$$dispatch(name, detail)` in the child, `@on:name` in the parent |
| Two-way binding | `bind:value` on a `$bindable` prop | the same |
| Instance API | `mount()` / `unmount()` | `Component(target, props, slots)` returns `{ set, destroy }` |

## Special elements and directives

| | Svelte 5 | Vela |
| --- | --- | --- |
| `<svelte:fragment>` | ✓ (legacy) | ✓ |
| `<svelte:component this={C}>` | ✓ (legacy) | `<vela:component :this={C}>`; props are matched by name to the current component |
| `<svelte:boundary>` with a `failed` snippet | ✓ | `{#boundary}…{:catch error, retry}…{/boundary}` (a block, since Vela has no snippets); errors are reported with a `vela:error` event instead of `onerror`; with SSR the server renders the catch branch too |
| `<svelte:element>`, `<svelte:self>` | ✓ | — |
| `<svelte:head>` | ✓ | `<vela:head>` at the top level of a template: title, meta, link and JSON-LD; the deepest component wins a key, also on the server |
| `<svelte:window>`, `<svelte:document>` | ✓ | `<vela:window>` and `<vela:document>`: listeners and read-only bindings (no writable `scrollX`/`scrollY`) |
| `<svelte:body>` | ✓ | — |
| `use:` actions | ✓ | ✓ (`use:name={params}`, Svelte 4 semantics; no `{@attach}`) |
| `transition:`, `in:`, `out:`, `animate:` | ✓ | ✓ from `vela/transition` and `vela/animate`; always local, no `\|global`, no `tick`-only transitions |

## Tooling and server rendering

| | Svelte 5 | Vela |
| --- | --- | --- |
| Compiler | JavaScript (Node) | Go, single binary |
| Language | JavaScript, TypeScript | JavaScript (ES2025 modules) and TypeScript (experimental; erasable syntax only, checked by `vela check --types` as `svelte-check` does) |
| Bundling | Vite plugin | none: plain ES modules for any bundler or an import map |
| SSR | Node, SvelteKit | Go renderers generated from the same components; JavaScript translated to Go or supplied as Go hosts; Vela App for routes, Go loaders, form actions and partial navigation |
| Hydration | ✓ | ✓, with mismatch reports that name the source position |
| Dev server, HMR | Vite | `--watch` only |
| Diagnostics for Svelte habits | — | stable `VELA-*` codes with the Vela replacement |
