# Server rendering

Vela renders components on the server **in Go**, without a JavaScript
engine. `vela --ssr` compiles every component twice from one source: the
ordinary client module, and a Go renderer that writes the same HTML. The
browser then **hydrates** that HTML: the client module adopts the server DOM
instead of rebuilding it.

```
src/Page.vela ──vela --ssr──► out/client/Page.js       (browser, hydrate)
                         ├──► out/server/components/…  (Go renderer)
                         └──► out/styles/Page.css      (scoped CSS)
```

Your Go program owns everything else: routing, data loading, HTTP. Vela
generates typed functions it can call.

Status: **beta** (experimental contract, `v0`). What is checked on every
change is listed in [SSR_GATE.md](SSR_GATE.md).

| Document | Contents |
| --- | --- |
| This page | What SSR does and how to use it |
| [SSR_TRANSLATION_CONTRACT.md](SSR_TRANSLATION_CONTRACT.md) | Normative rules: which JavaScript becomes Go, host implementations, client boundaries, runtime versioning |
| [SSR_TRANSLATION_MATRIX.md](SSR_TRANSLATION_MATRIX.md) | Per-construct and per-built-in translation status |
| [HYDRATION_ARCHITECTURE.md](HYDRATION_ARCHITECTURE.md) | Block runtime, server markup, payload stream, hydration and mismatch policy |
| [SSR_GATE.md](SSR_GATE.md) | Beta gate: the real corpus through CLI, Go, HTTP and Chromium |
| [`examples/ssr-host`](../examples/ssr-host) | Runnable server with hosts |

## Build

```sh
vela --ssr --project src --out out
```

| Output | Contents |
| --- | --- |
| `out/client/` | Client modules (the same as a CSR build), the runtime, and `vela.manifest.json` (components, root modes, package imports) |
| `out/server/components/…` | One Go package per source directory: `page.go` (renderer) and `page_props.go` (props struct, yours to extend: it is never overwritten) |
| `out/server/modules/…` | One Go package per local `.js` module that components import |
| `out/styles/` | Scoped CSS per component |

The Go packages import the runtime module
`github.com/elauven/vela/ssr_runtime` (alias `vela`). Your module requires
that module, never the compiler:

```
require github.com/elauven/vela/ssr_runtime v0.1.0
```

Set the import path of the generated packages with `--ssr-module` or in
`vela.ssr.json`:

```json
{ "module": "example.com/site/out/server" }
```

A runtime of another contract version fails `go build` with
`undefined: vela.ContractV1` instead of misbehaving.

## Render

```go
import components "example.com/site/out/server/components"

props := components.PageProps{Set: map[string]bool{"title": true}, Title: "Hello"}
err := components.RenderPageContext(r.Context(), w, props, components.PageSlots{})
```

| Function | Writes |
| --- | --- |
| `RenderPage(w, props, slots)` | HTML of the root and its payload script, for hydration |
| `RenderPageContext(ctx, w, props, slots)` | The same; hosts receive `ctx` (request, locale, user) |
| `RenderPageWithOptions(w, props, slots, options)` | The same with settled `{#await}` snapshots |
| `RenderPageStatic(w, props, slots)` | Plain HTML, no markers and no payload |
| `Page(props, slots)` | A `vela.Renderer` value, for composition |

**Props.** `Set` lists the props you supply. With
`Set: map[string]bool{}`, omitted props take their `export let` defaults; a
nil `Set` means "every field is supplied", including zero values.

## Hydrate

The client entry is your own module:

```js
import { hydrate } from './runtime-hydrate.js';   // or '@govela/runtime/hydrate'
import { Page } from './Page.js';

hydrate(Page, document.getElementById('app'), {
  onMismatch: (report) => console.warn(report),
});
```

- The same compiled fragments that build the DOM on the client walk the
  server DOM and attach listeners and updates. Component state is restored
  from the payload script that follows the root.
- **Root modes.** A root whose tree has no events, bindings, effects or
  lifecycle hooks is `static`: it stays plain HTML and needs no JavaScript.
  Other roots are `interactive`. `vela.manifest.json` lists the mode and the
  client module of every component.
- **Mismatch policy** (`--hydration-verify`, `hydrate(…, { policy })`):
  - `repair` (development default): the client value wins and the mismatch is
    reported with its source position, site and host;
  - `fail`: the first mismatch throws and the hydration is rolled back,
    leaving the server DOM in place;
  - `off`: no value comparison.

  Production builds (`--mode=production`) skip value comparison. Under
  `repair` and in production, a structural mismatch remounts the root on the
  client.
- **Form controls.** If the user edits an input before hydration, the live
  value is written into state through the normal setter. `bind:this` is
  assigned after the element is adopted.

## What runs on the server

The server evaluates what the first render needs: props and their defaults,
`$state` and `$derived` initializers, template expressions, and the
functions and local `.js` modules they call. Event handlers, `$effect`,
`onMount` and `onDestroy` never run on the server and never block SSR.

Each such place is a **site** with one outcome:

| Outcome | Meaning |
| --- | --- |
| `server-native` | Translated to Go with JavaScript semantics (UTF-16 strings, `float64` numbers, JS rounding and text conversion) |
| `server-host` | A Go function you wrote stands in for a JavaScript function |
| `unsupported` | The build fails with a diagnostic that names the site and the fix |

There is no approximate translation: code that cannot be exact is rejected.
The admitted language (statements, closures, `async`/`await` with a
synchronous model, built-ins) is in the contract and the matrix.

`vela --ssr --ssr-report=report.json` writes one record per site with its
outcome, tier and type, for review or CI.

### Hosts

When a function cannot be translated (`Intl`, regular expressions,
`localStorage`), write it in Go once:

```go
// ssrhost/src/format/format_price_host.go
package format

//vela:host formatPrice
func FormatPrice(ctx context.Context, value float64, currency string) (string, error) { … }
```

- The host package mirrors the JavaScript file (`src/format.js` →
  `ssrhost/src/format/`); configure the tree in `vela.ssr.json`
  (`"host": {"dir": "ssrhost", "module": "example.com/site/ssrhost"}`).
- The compiler prints the expected file and signature;
  `--ssr-scaffold-hosts` creates stubs and never changes existing files.
- The server calls the host; the browser keeps the JavaScript function, and
  development hydration reports a host that disagrees with it.

Full rules: contract §8.

### Browser-only content

```vela
{#client}
  <Chart :data={stats} />
{:else}
  <div class="chart-skeleton"></div>
{/client}
```

`{#client}` renders its fallback on the server and mounts its content after
hydration. A component declared `<script context="client">` is always
rendered that way. In a CSR build both behave as ordinary content. A value
that controls structure and cannot be computed on the server must move into
`{#client}` with that structure; the diagnostic says so.

### `{#await}`

An `{#await}` operand the server can evaluate settles during the render:
the server writes the `then` or `catch` branch and the client adopts it
without re-running the promise. `RenderPageWithOptions` can pass a settled
snapshot instead. An operand only the browser can compute belongs in
`{#client}`.

## Text conversion

Server text follows the client's conversion in each context:

| Context | `null` | `undefined` / missing member | Arrays |
| --- | --- | --- | --- |
| `{value}` text | `null` | `undefined` | comma join |
| `:attribute={value}` | `"null"` | `"undefined"` | comma join |
| Boolean attributes, `class:` conditions | falsy | falsy | truthy, including `[]` |
| Template literal | `null` | `undefined` | JavaScript text conversion |
| Array join member | empty | empty | nested arrays join |
| Form-control value | DOM property semantics | DOM property semantics | binding-specific |

## Diagnostics

| Code | Cause |
| --- | --- |
| `VELA-SSR-EXPR` | An expression or built-in the server cannot evaluate exactly |
| `VELA-SSR-HOST-MISSING` | A function needs a host; the message shows the file and signature |
| `VELA-SSR-HOST-STALE`, `VELA-SSR-HOST-SIG` | A host directive names a function that no longer exists, or its Go signature does not fit |
| `VELA-SSR-PROPS` | A prop value that cannot be sent to the client in the payload |
| `VELA-SSR-IMPURE` | A write to props, state or module values during the render |
| `VELA-SSR-CHILD` | A child component has no server renderer |
| `VELA-HTML-NESTING` | Markup the HTML parser would rearrange (for example `<div>` in `<p>`) |
| `VELA-HYDRATION-MISMATCH` | Runtime: server and client disagree during hydration |

## Not included

Streaming, partial hydration and islands, an HTTP framework or router, and a
dev server. Vela does not bundle: the client entry is built by your own
tools ([GUIDE.md](GUIDE.md), "Building an application").

