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
  Node), and it is a compiler only: no SvelteKit-style framework, router or
  bundler.
- **Not in Vela:** snippets, `{@html}`, actions, transitions, dynamic
  components and most `<svelte:*>` elements.

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
| Event modifiers | — (Svelte 5 removed them) | — (`VELA-EVENT-MODIFIER`) |
| Bindings | `bind:value`, `bind:checked`, `bind:this`, `bind:group`, … | `bind:value`, `bind:checked`, `bind:this` |
| `class:` / `style:` directives | ✓ | ✓ |
| Static `class` with dynamic `class` | `class={[…]}` arrays and objects | `class="a" :class={b}` joined; no object or array form |
| `{#if}`, `{#each}` (keyed, `{:else}`), `{#await}`, `{#key}` | ✓ | ✓ |
| `{@html}`, `{@const}`, `{@debug}`, `{@render}` | ✓ | — |
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
| `<svelte:component>`, `<svelte:element>`, `<svelte:self>` | ✓ | — |
| `<svelte:head>`, `<svelte:window>`, `<svelte:document>`, `<svelte:body>` | ✓ | — |
| `use:` actions | ✓ | — (use `bind:this` with `onMount` or `$effect`) |
| `transition:`, `in:`, `out:`, `animate:` | ✓ | — |

## Tooling and server rendering

| | Svelte 5 | Vela |
| --- | --- | --- |
| Compiler | JavaScript (Node) | Go, single binary |
| Language | JavaScript, TypeScript | JavaScript (ES2025 modules); no TypeScript |
| Bundling | Vite plugin | none: plain ES modules for any bundler or an import map |
| SSR | Node, SvelteKit | Go renderers generated from the same components; JavaScript translated to Go or supplied as Go hosts |
| Hydration | ✓ | ✓, with mismatch reports that name the source position |
| Dev server, HMR | Vite | `--watch` only |
| Diagnostics for Svelte habits | — | stable `VELA-*` codes with the Vela replacement |
