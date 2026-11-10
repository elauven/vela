<div align="center">

# Vela

**Single-file components compiled to vanilla JavaScript — and rendered on the server in Go.**

[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go 1.25+](https://img.shields.io/badge/go-1.25%2B-00ADD8.svg)](go.mod)
[![Status: beta](https://img.shields.io/badge/status-beta-orange.svg)](docs/FEATURES.md)

[Guide](docs/GUIDE.md) · [Features](docs/FEATURES.md) · [Server rendering](docs/SSR.md) · [Examples](examples/)

</div>

---

Vela is a compiler written in Go. You write components in HTML, CSS and the
JavaScript you already know; Vela turns each one into a small ES module that
updates the DOM directly — no virtual DOM, no diffing. With `--ssr` the same
components also become **Go renderers**, so a Go server can send real HTML
that the browser then hydrates. No Node.js on the server, no embedded
JavaScript engine.

```svelte
<script>
  export let title = 'Today';
  let todos = $state([]);
  let draft = $state('');
  let left = $derived(todos.filter((t) => !t.done).length);

  function add() {
    todos = [...todos, { id: Date.now(), text: draft, done: false }];
    draft = '';
  }
</script>

<template>
  <section>
    <h2>{title} <small>{left} left</small></h2>
    <input bind:value={draft} @keydown={(e) => e.key === 'Enter' && add()} />
    <ul>
      {#each todos as todo (todo.id)}
        <li class:done={todo.done}>{todo.text}</li>
      {:else}
        <li class="empty">Nothing to do.</li>
      {/each}
    </ul>
  </section>
</template>

<style>
  .done { text-decoration: line-through; }
</style>
```

## Why Vela

- **Direct DOM updates.** Dependencies are resolved at compile time; a state
  change updates exactly the text nodes, attributes and blocks that read it,
  batched into one microtask.
- **Small runtime.** About 6 KB min+gzip for client rendering, plus about
  3 KB for hydration.
- **Server rendering in Go.** Render-time JavaScript is translated to Go with
  JavaScript semantics (UTF-16 strings, number formatting, `Math` bit for
  bit). What cannot be translated exactly is a build error with a fix — write
  that function in Go once, or mark the content browser-only.
- **A compiler, not a framework.** The output is plain ES modules. Serve it
  as is, bundle it, or compile inside Vite, Rollup, Rolldown, esbuild,
  webpack or Rspack with the plugin `@govela/unplugin`.
- **Diagnostics that teach.** Errors carry a stable `VELA-*` code, the
  source position and the Vela way to write it — useful for people and for
  AI agents ([AGENTS.md](AGENTS.md)).

## Quick start

A new application in TypeScript, with Vite, a component test, ESLint and
a CI workflow:

```sh
npm create @govela my-app             # -- --ssr: a Go server, -- --app: a Vela App, -- --js: JavaScript
cd my-app && npm install && npm run dev
```

The compiler alone:

```sh
go install github.com/elauven/vela@latest   # or, in a JavaScript project:
npm i -D @govela/compiler                   # then npx vela …

vela --project src --out build          # compile every component in src/
vela --project src --out build --watch  # recompile on change
vela check --project src                # diagnostics only, for CI
```

With Vite (`npm i -D @govela/unplugin && npm i @govela/runtime`):

```js
// vite.config.js
import vela from '@govela/unplugin/vite';
export default { plugins: [vela()] };
```

For a multi-page application with a Go server, routes and loaders
(Vela App), `npm i @govela/app` also installs the `vela-app` command:
`npx vela-app dev` for development, `npx vela-app build` for a directory to
deploy ([VELA_APP.md](docs/VELA_APP.md)).

Mount a component from any page:

```html
<div id="app"></div>
<script type="module">
  import { Todos } from './build/Todos.js';

  const app = Todos(document.getElementById('app'), { title: 'Work' });
  app.set({ title: 'Home' });   // update props
  // app.destroy();             // tear down
</script>
```

The runtime is written next to the output, so a static file server is
enough. For production bundles see
[Building an application](docs/GUIDE.md#building-an-application).

## Server rendering

```sh
vela --ssr --project src --out out --ssr-module example.com/app/out/server
```

```go
import components "example.com/app/out/server/components"

func page(w http.ResponseWriter, r *http.Request) {
	props := components.TodosProps{Set: map[string]bool{}}
	components.RenderTodosContext(r.Context(), w, props, components.TodosSlots{})
}
```

```js
import { hydrate } from './out/client/runtime-hydrate.js';
import { Todos } from './out/client/Todos.js';

hydrate(Todos, document.getElementById('app'));
```

Event handlers, effects and `onMount` stay in the browser; `{#client}`
marks content only the browser can render. The generated Go needs only the
runtime module `github.com/elauven/vela/ssr_runtime`.
[`examples/ssr-host`](examples/ssr-host) is a complete server, and
[docs/SSR.md](docs/SSR.md) explains the model.

## Performance

Vela renders on the server in plain Go, with no JavaScript engine in the
request path. The same tracker app, built with Vela and with SvelteKit (run
on Node and on Bun):

![Vela SSR against SvelteKit on Node and Bun](docs/assets/ssr-performance.svg)

Pages limited by the database serve about as many requests in all three;
Vela answers them sooner and with less CPU, as the JSON API shows. Method,
every scenario and the tables: [bench/RESULTS.md](bench/RESULTS.md).

## Language at a glance

| Syntax | Meaning |
| --- | --- |
| `let n = $state(0)` | Reactive value (`$state.deep` for nested mutation) |
| `let d = $derived(n * 2)` | Computed value |
| `$effect(() => …)` | Side effect with cleanup |
| `export let title = ''` | Prop (`$bindable(…)` for two-way) |
| `{expr}` | Text |
| `:href={url}` | Dynamic attribute |
| `@click={fn}` | Event listener |
| `bind:value={x}` | Two-way binding |
| `class:on={cond}`, `style:color={c}` | Class and style directives |
| `{#if}` `{#each … (key)}` `{#await}` `{#key}` | Blocks |
| `<slot>`, `<slot name="…">` | Slots |
| `{#client}…{/client}` | Browser-only content in SSR |
| `{#boundary}…{:catch error, retry}…{/boundary}` | Error boundary |
| `use:tooltip={opts}`, `transition:fade`, `animate:flip` | Actions, transitions and animations |
| `<vela:head>`, `<vela:window>`, `<vela:component :this={C}>` | Document head, window listeners, dynamic components |
| `setContext` / `getContext` | Values passed down the component tree |

The full syntax is in the [Guide](docs/GUIDE.md); differences from Svelte 5
are in [SVELTE_COMPARISON.md](docs/SVELTE_COMPARISON.md).

## Status

Vela is in **beta**. The client compiler is stable for the features marked
so in [FEATURES.md](docs/FEATURES.md). Server rendering and hydration and the
Vela App layer are beta: complete for their documented scope, and their
contract changes before `v1` only with a note in
[RELEASING.md](docs/RELEASING.md) and a new runtime contract or protocol
version; every change is checked against a corpus of real applications in
Chromium ([SSR_GATE.md](docs/SSR_GATE.md)). TypeScript is experimental: `<script lang="ts">`, `.ts` modules, `vela check --types`, declarations and route types ([TYPESCRIPT.md](docs/TYPESCRIPT.md)). Not included yet:
event modifiers (task 101), HMR (task 008), a static export of Vela Apps
(task 098), streaming SSR and snippets.

## Documentation

| Document | Contents |
| --- | --- |
| [GUIDE.md](docs/GUIDE.md) | Writing components and building applications |
| [FEATURES.md](docs/FEATURES.md) | What is implemented, and its limits |
| [AGENTS.md](AGENTS.md) | Authoring rules for people and AI agents |
| [EDITORS.md](docs/EDITORS.md) | The language server `vela-lsp` and editor setup |
| [SSR.md](docs/SSR.md) | Server rendering and hydration |
| [APP.md](docs/APP.md) | Vela App: routes, Go loaders and partial navigation (beta: the contract, the `vela-app` generator, server documents, data and fragment navigation, form actions, guards, prerendering and caching, `vela-app dev` and `vela-app build`) |
| [VELA_APP.md](docs/VELA_APP.md) | Working with `vela-app`: install, a new project, development, production build and deploy |
| [SSR_TRANSLATION_CONTRACT.md](docs/SSR_TRANSLATION_CONTRACT.md) | JavaScript-to-Go rules and Go hosts ([ru](docs/SSR_TRANSLATION_CONTRACT.ru.md)) |
| [SSR_TRANSLATION_MATRIX.md](docs/SSR_TRANSLATION_MATRIX.md) | Translation status per construct and built-in |
| [HYDRATION_ARCHITECTURE.md](docs/HYDRATION_ARCHITECTURE.md) | Block runtime and hydration ([ru](docs/HYDRATION_ARCHITECTURE.ru.md)) |
| [VELA.md](docs/VELA.md) | Compiler internals, CLI, tests |
| [ES_COMPLIANCE.md](docs/ES_COMPLIANCE.md) | JavaScript syntax coverage |
| [TYPESCRIPT.md](docs/TYPESCRIPT.md) | TypeScript (experimental): the contract, the checker, editors, SSR and route types |
| [RELEASING.md](docs/RELEASING.md) | Versions and the release procedure |

## Contributing

```sh
go test ./... ./ssr_runtime/...          # all suites
npm ci && npx playwright install chromium
node tools/run-ssr-browser-baseline.mjs  # strict browser baseline
```

## License

[MIT](LICENSE). The Go SSR runtime (`ssr_runtime/`) and the npm package
`@govela/runtime` are released under the same license.

---

*This is an attempt to rethink what SSR could be like*
