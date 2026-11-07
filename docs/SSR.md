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
| `out/server/components/…` | One Go package per source directory: `page.go` (renderer) and `page_props.go` (props struct: regenerated when the props change until you edit it, then yours and never overwritten) |
| `out/server/modules/…` | One Go package per local `.js` or `.ts` module that components import |
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
`undefined: vela.ContractV5` instead of misbehaving.

### Directory names

The source tree may use the directory names of file-based routers. They are
not valid in Go import paths, so the Go side encodes them (the client output
and `vela.manifest.json` keep the source paths):

| Source directory | Go directory | Go package |
| --- | --- | --- |
| `[id]` | `id.param` | `id_param` |
| `[[lang]]` | `lang.opt` | `lang_opt` |
| `[...rest]` | `rest.rest` | `rest_rest` |
| `[id=int]` | `id.int.param` | `id_int_param` |
| `(admin)` | `admin.group` | `admin_group` |
| `user-list`, `sitemap.xml` | unchanged | `user_list`, `sitemap_xml` |

- The names inside brackets and parentheses match `[A-Za-z][A-Za-z0-9_]*`.
- Other names keep letters, digits, `_`, `.` and `-`; they cannot start with
  `-` or `~` or end with `.`. The suffixes `.param`, `.opt`, `.rest` and
  `.group` are reserved, so the encoding is reversible.
- Mixed names (`[a]-[b]`, `file.[ext]`), other characters (`$`, `@`, `{`,
  `!` …), reserved suffixes, and two directories that become the same Go
  directory (compared without case) fail with `VELA-SSR-PACKAGE-PATH`,
  which names the directory and suggests a valid name. Without this check
  one such directory would break `go build ./...` of your whole module.
- Directories starting with `_` or `.` are valid, but `go build ./...` skips
  them.
- A package name gets `_` for `.` and `-`, a `p` before a leading digit and a
  `_` after a Go keyword.
- Go tools read the same table from
  `github.com/elauven/vela/ssr_runtime/pkgpath` (`Encode`, `Decode`,
  `EncodePath`, `DecodePath`, `PackageName`).

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
| `PageComponent` | The component as a `vela.Component` value: `ComponentID()` and `RenderDynamic(ctx, w, props, slots, options)` with props by name; what `<vela:component>` renders |

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
- **Lazy components.** A page that renders `lazy()` components loads their
  modules before it hydrates; otherwise hydration reports
  `lazy-not-loaded` and repairs the range when the module arrives:

  ```js
  import { hydrate, preloadHydration } from '@govela/runtime/hydrate';

  const target = document.getElementById('app');
  await preloadHydration(target);
  hydrate(Page, target);
  ```

  The server renders a lazy component like any other (contract §5.5).
- **HTML after the page loaded.** HTML a Go renderer wrote for a later
  request, such as the changed part of a page, mounts into
  `<vela:component>`: `serverComponent(Page, html)` hydrates a root range
  with its payload, and `staticHTML(html)` inserts the HTML of a static
  root without any instance (HYDRATION_ARCHITECTURE A-8.7a). Pass only
  HTML from your own server.

## Development and production with Vite

The Go server renders the HTML; Vite serves the client in development and
bundles it for production, with the components compiled by
`@govela/unplugin` (docs/GUIDE.md "Bundler plugin"). Run `vela` and Vite
in the same directory, the Vite root: the scope classes of the CSS and the
source positions of hydration reports come from the paths relative to it,
so the server HTML and the client agree.

```json
// vela.config.json
{ "project": "src", "out": "out", "ssr": true, "ssrModule": "example.com/site/out/server" }
```

```js
// vite.config.js
import vela from '@govela/unplugin/vite';

export default {
  plugins: [vela()],
  server: { origin: 'http://localhost:5173' },
  build: { manifest: true, rollupOptions: { input: 'src/client.js' } },
};
```

```js
// src/client.js
import { hydrate } from '@govela/runtime/hydrate';
import App from './App.vela';

hydrate(App, document.getElementById('app'));
```

The Go server takes the tags of the client from the package
`github.com/elauven/vela/ssr_runtime/vite`:

```go
import "github.com/elauven/vela/ssr_runtime/vite"

assets := vite.Dev("http://localhost:5173")                  // development
assets, err := vite.Load("dist/.vite/manifest.json", "/")    // production
tags, err := assets.Tags("src/client.js")                    // into <head>
```

- `Dev` gives the Vite client and the entry module from the dev server,
  which compiles the components on request. The CSS arrives with the
  modules, so the page is unstyled until the client has loaded.
- `Load` reads the manifest of `vite build` and `Tags` gives, per entry,
  the stylesheets, `<link rel="modulepreload">` for the imported chunks and
  the module script, with the hashed file names. `Preload(entries, modules…)` gives the
  stylesheets and modulepreload links of modules the page imports
  dynamically, such as the `lazy()` components it renders
  (`"src/Panel.vela"`), without the files the entries already load; in
  development it gives nothing. Serve `dist/assets/` at
  the `base` you pass (Vite's `base`, `/` by default).

**Development loop.** Three processes:

```sh
vela --watch                     # with vela.config.json: --ssr --project src --out out
watchexec -r -w out/.vela-ready -- go run ./cmd/server
npx vite
```

`vela --ssr --watch` rebuilds the project after every change of a
component, a local module, `vela.ssr.json` or a host file, and removes the
outputs of a removed component. After each build without errors it writes
`out/.vela-ready` (the start time of the build), the signal to restart the
Go server; a build with errors leaves it, so the last good server keeps
running. Every rebuild is a clean build of the project (about 0.1 s for
30 components). Vite reloads the page when a component changes; component
state is not kept.

This setup runs in CI (`TestSSRWithVite`): the Go server with `vite build`
output and with the Vite dev server, hydrated in Chromium without
mismatches. `TestSSRWatchRebuildsLikeACleanBuild` covers the watch mode.

## What runs on the server

The server evaluates what the first render needs: props and their defaults,
`$state` and `$derived` initializers, template expressions, and the
functions and local `.js` and `.ts` modules they call. Event handlers,
`$effect`, `onMount` and `onDestroy` never run on the server and never block
SSR.

TypeScript components and `.ts` modules render exactly as their JavaScript
twins (docs/TYPESCRIPT.md TS-7). The annotation of a prop chooses the Go
type of its field, as JSDoc `@type` does (`export let total: number` is
`Total float64` in `PageProps`); other annotations do not change the
translation.

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
outcome, tier and type, for review or CI, and the Go type of each prop with
where it comes from (`ts`, `jsdoc`, `inferred`).

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
  `ssrhost/src/format/`), with directory names encoded as in "Directory
  names" (`src/routes/[id]/util.js` → `ssrhost/src/routes/id.param/util/`);
  configure the tree in `vela.ssr.json`
  (`"host": {"dir": "ssrhost", "module": "example.com/site/ssrhost"}`).
- The compiler prints the expected file and signature;
  `--ssr-scaffold-hosts` creates stubs and never changes existing files. For
  a function of a `.ts` module or a `lang="ts"` component the signature
  comes from its annotations (`function formatPrice(value: number,
  currency?: string): string` gives `value float64, currency *string)
  (string, error)`), and a host that contradicts them is
  `VELA-SSR-HOST-SIG`.
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
| `VELA-SSR-PACKAGE-PATH` | A source directory name that cannot become a Go package directory ("Directory names") |
| `VELA-OUTPUT-COLLISION` | Two components write one Go file: names become snake_case, so `UserCard.vela` and `User_Card.vela` both write `user_card.go`, and `CardProps.vela` writes the `card_props.go` of `Card.vela` (also reported for client outputs, docs/GUIDE.md) |
| `VELA-HTML-NESTING` | Markup the HTML parser would rearrange (for example `<div>` in `<p>`) |
| `VELA-HYDRATION-MISMATCH` | Runtime: server and client disagree during hydration |

## Not included

Streaming, partial hydration and islands, an HTTP framework or router, hot
module replacement, and a dev server of its own: Vite serves the client
("Development and production with Vite"). Routing, loaders and navigation
are the job of a separate layer above the compiler, Vela App
([APP.md](APP.md)), which also has form actions and `vela-app dev`. Vela does not bundle: the client
entry is built by your own tools ([GUIDE.md](GUIDE.md), "Building an
application").

