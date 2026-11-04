# Vela App

Vela App is a layer above the compiler for multi-page applications. It plays
the part SvelteKit and Nuxt play for their compilers, but the server is
**Go**:

- routes, layouts and error pages come from files in nested directories;
- data is loaded by **Go** functions that sit next to the routes;
- the first request gets server HTML, and the browser hydrates it;
- a navigation replaces only the part of the page that changed, so the
  layouts above it stay alive and keep their state;
- for each navigation the server sends either data (JSON) for components the
  client already has, or the server HTML of the changed part (a fragment).

Status: **experimental**: implemented by tasks 073–078, not released yet (the `app/` module has no tag). This page is the contract (task 073), approved on
2026-11-03. The route scanner and the `vela-app` generator exist (task 074,
see [Generator](#generator)), `app.New` serves documents that the
browser hydrates (task 075, see [Server](#server)), and the client router
navigates with data responses (task 076, see [Navigation](#navigation))
and with server HTML fragments (task 077); form actions, prefetching,
build ids and `vela-app dev` complete it (task 078, see
[Forms, prefetching, deploys and development](#forms-prefetching-deploys-and-development)).
The
rules are numbered so that code, tests and reviews can cite them: `B-`
boundary, `P-` paths and routes, `L-` loaders, `R-` rendering, `N-`
navigation.

```
src/routes/**  ──vela-app gen──►  src/_app/VelaApp.vela, routes.js, client.js
server/routes/**                  internal/appgen/routes_gen.go
                ──vela --ssr───►  out/client/…, out/server/…
                ──go build─────►  your server: mux.Handle("/", app.New(…))
```

## B. Boundary between the app layer and the compiler

- **B-1** The layer lives in this repository but depends on the compiler
  only through its public interfaces, so it can move to its own repository
  unchanged. Those interfaces are:
  - the `vela` CLI and its documented outputs: `vela.manifest.json`, the
    documented Go API of generated renderers ([SSR.md](SSR.md) "Render"),
    and `vela.config.json`;
  - the Go module `github.com/elauven/vela/ssr_runtime`, including
    `ssr_runtime/vite` and `ssr_runtime/pkgpath` (the directory encoding of
    task 069);
  - the public entry points of `@govela/runtime` (`.`, `./blocks`,
    `./hydrate`) and their exported names. Names that start with `$$` are
    internal and are never imported.
- **B-2** The compiler knows nothing about apps. There are no `routes/`
  conventions, no app flags and no app code paths in `internal/*`. When the
  app needs something from the language or the runtime, it is added as a
  general feature in its own task: 069 (Go paths for route directories), 070
  `<vela:component>`, 071 `lazy()`, 072 adopting server HTML.
- **B-3** Layout:

  | Path | Contents |
  | --- | --- |
  | `app/` | Go module `github.com/elauven/vela/app` (its own `go.mod`, listed in `go.work`). Package `app`: `Handler`, `Request`, loaders, responses |
  | `app/cmd/vela-app/` | The generator CLI (`go run github.com/elauven/vela/app/cmd/vela-app`) |
  | `app/internal/gen/` | Route scanner and code generation |
  | `app/internal/protocol/` | Navigation protocol types, shared by the server and the tests |
  | `packages/app/` | npm `@govela/app`: the client router (`@govela/app/client`) |
  | `examples/app/` | A runnable example (from task 074) |

- **B-4** `TestAppLayerIsSeparable` (root module) enforces B-1 and B-2:
  1. `go list -deps -test` of `./app/...` contains no package of the root
     module `github.com/elauven/vela`. Only the standard library,
     `ssr_runtime` and the app's own packages are imported directly;
  2. no package of the root module imports `github.com/elauven/vela/app`;
  3. the sources of `packages/app` import only the public `@govela/runtime`
     entry points (B-1), no `$$` names, and their own files;
  4. app tests run the compiler as a subprocess (the `vela` binary), never
     as a Go package. Rule 1 enforces this, because it also covers the test
     dependencies.
- **B-5** The generator is written in Go and run with `go run` or
  `go install`. App users have Go anyway, so the npm side ships no binary.
- **B-6** Versions. The Go module is tagged `app/vX.Y.Z`. `@govela/app` has
  its own version and is not part of the lockstep version check of the
  compiler packages, just like `vscode-vela`. Both declare the runtime
  contract they support (`ContractV4` from task 070) and the navigation
  protocol version (N-1). See [RELEASING.md](RELEASING.md).

## P. Paths and routes

### Route tree

- **P-1** The routes directory (`src/routes/` by default, configured in
  `vela.app.json` as `"routes"`) holds only Vela files: `Page.vela`,
  `Layout.vela` and `Error.vela`. They are PascalCase, as the compiler
  requires. Any other `.vela` file there is a generator error, so
  components shared between routes live outside that directory. Local
  `.js` modules are allowed.
- **P-2** Directory names:

  | Directory | URL | Go directory (069) |
  | --- | --- | --- |
  | `users` | `/users` | `users` |
  | `[id]` | `/users/42`, `params.id = "42"` | `id.param` |
  | `[[lang]]` | optional segment: `/en/about` and `/about` | `lang.opt` |
  | `[...path]` | rest of the path, possibly empty: `params.path = "a/b"` | `path.rest` |
  | `[id=int]` | like `[id]`, but matches only when the matcher `int` accepts the segment | `id.int.param` |
  | `(admin)` | no URL segment; groups routes under a shared layout | `admin.group` |

- **P-3** Mixed segments such as `[a]-[b]` or `file.[ext]` are not part of
  v1, and the compiler rejects them anyway (069). A name that
  `pkgpath.Encode` cannot encode is a generator error.
- **P-4** `[...rest]` must be the last directory of a path. Only `[[opt]]`
  and `(group)` directories may be nested freely.
- **P-5** Parameter names are JavaScript identifiers that are unique along
  a path. A name repeated along one path is a generator error.

### Matching

- **P-6** The URL path is split at `/` **before** percent-decoding. Each
  segment is then decoded, and matchers and `params` see the decoded value.
  An encoded `%2F` therefore stays inside its segment. A rest parameter is
  the decoded segments joined with `/`. Matching is case-sensitive.
- **P-7** Trailing slash: a path that ends with `/` (other than `/` itself)
  redirects with 308 to the path without it, keeping the query. The client
  router normalises the same way before it matches.
- **P-8** Ranking, applied segment by segment from the left: static, then a
  parameter with a matcher, then a parameter, then an optional segment,
  then rest. The first full match wins. Two candidates of the same rank at
  one level (`[id]` and `[slug]`, or `[a=int]` and `[b=int]`) are a
  generator error. An optional segment is tried first with a value and
  then without one. Groups add no segment. Parameters with different
  matchers at one level are allowed and tried in the order of the matcher
  names.
- **P-9** Two routes that produce the same URL pattern are a generator
  error. This includes patterns produced by groups (`(a)/x` and `(b)/x`)
  and by an optional segment (`[[lang]]/about` and `about`).
- **P-10** A URL matches a **chain**: the root `Layout`, the nested
  `Layout`s (group layouts included) and the `Page`. A directory without a
  `Layout.vela` adds no level, and a directory without a `Page.vela` is not
  a route.
- **P-11** A **segment id** is the `$$id` of the segment's component (070):
  its path relative to the project, without the extension, for example
  `routes/Layout`, `routes/users/Layout` or `routes/users/[id=int]/Page`.
  Segment ids appear in the chain, in the protocol and in the manifest
  lookups of R-9.
- **P-12** A **route id** is the route's directory path relative to the
  routes directory, with groups and brackets kept: `/`, `/users/[id=int]`,
  `/(admin)/settings`. It is unique (P-9) and is sent as `route.id`.

### Matchers

- **P-13** `[name=matcher]` names a matcher. The built-in matchers are part
  of the app layer in Go and in `@govela/app`:

  | Matcher | Accepts |
  | --- | --- |
  | `int` | one or more ASCII digits (`0`, `42`, `007`); no sign |
  | `uuid` | `8-4-4-4-12` hexadecimal digits in either case |
  | `slug` | `[a-z0-9]+` groups joined by single `-` |

- **P-14** A custom matcher `name` needs both halves:
  - Go: the package `server/params/name/` (for example
    `server/params/name/match.go`), with `func Match(segment string) bool`.
    Each matcher is a package of its own, so that every one can declare
    `Match`; a `.go` file directly in `server/params/` is an error;
  - JavaScript: `src/params/name.js`, with `export function match(segment)`
    returning a boolean.

  A missing half or an unknown matcher name is a generator error. The two
  roots are configurable (`"serverParams"`, `"params"`). A custom matcher
  cannot shadow a built-in one. Matcher names are identifiers.
- **P-15** The server is authoritative. The client uses matchers only to
  choose the chain and preload modules before the response arrives. When
  the two disagree, the `route` of the server response wins. One shared
  JSON fixture of URL → route and parameters tests the Go and JavaScript
  matchers.

### Mirrored Go tree

- **P-16** Go code for routes lives in a tree that mirrors the routes tree
  (`server/routes/` by default, `"server"` in `vela.app.json`), using the
  encoded directory names of 069. It is the same pattern as `ssrhost/`. A
  `.go` file inside `src/routes/users/[id]/` would break `go build ./...`
  of the whole module, so the mirror is needed:

  ```
  src/routes/users/[id]/Page.vela    server/routes/users/id.param/load.go
  src/routes/(admin)/Layout.vela     server/routes/admin.group/load.go
  src/routes/users/[id]/             server/routes/users/id.param/actions.go   (078)
  ```

- **P-17** In the mirrored tree, `load.go` and `actions.go` are expected
  only in a directory that matches a route directory. A mirrored directory
  without a route directory is an error (a stale loader), as stale hosts
  are. A `.go` file under the routes directory is an error that names the
  mirrored path to move it to.
- **P-18** `vela-app scaffold [route]` creates the mirrored directory and a
  `load.go` with the `Page`/`Layout` signatures of L-1. It does this for the
  given route, or for every route that has none. It never overwrites a
  file.

## L. Loaders

- **L-1** A loader is an exported function in the mirrored `load.go`. Its
  package is named after the encoded directory with dots replaced by
  underscores (`id_param`):

  ```go
  func Page(ctx context.Context, r *app.Request) (UserView, error)
  func Layout(ctx context.Context, r *app.Request) (Menu, error)
  ```

  `Page` loads data for `Page.vela` and `Layout` for `Layout.vela` in the
  same directory. A loader without a matching component is a generator
  error, and so is a wrong signature (the message prints the expected one).
  A component without a loader gets `data = null`.
- **L-2** The result is any JSON-serialisable Go value. The server
  marshals it with `encoding/json` and unmarshals it into `any`, so the
  `data` prop is the same value on the server as the JSON the client
  receives: objects, arrays, `float64` numbers, strings, booleans and
  `null` (T-5.1 of [SSR_TRANSLATION_CONTRACT.md](SSR_TRANSLATION_CONTRACT.md)).
  `time.Time` therefore arrives as an RFC 3339 string. A result that does
  not marshal is a 500 error.
- **L-3** `*app.Request` gives a loader:
  - `r.Params` (`map[string]string`, the decoded parameters of P-6; an
    absent optional parameter has no entry);
  - `r.Query` (`url.Values`);
  - `r.HTTP` (the `*http.Request`);
  - `r.Route` (the route id of P-12);
  - `r.Head()`, which collects the title and `<meta>` tags (R-6);
  - `r.Header()`, response headers such as `Cache-Control` (private by
    default).
- **L-4** The loaders of one chain run concurrently under the request
  context. `app.Parent[T](r)` waits for the result of the nearest layout
  above with a loader, converted to `T` through JSON. If the client kept
  that layout's segment, its loader runs on demand. A loader that is not
  needed never runs.
- **L-5** Errors are returned as the loader's error:
  - `app.Redirect(code, url)`: 303 or 307 for a document; `redirect` for a
    navigation (N-6);
  - `app.NotFound()`: 404;
  - `app.Error(status, message)`: that status, with a message that is
    shown;
  - any other error, or a panic: 500. The error is logged through
    `Config.Logger`, and in production the page shows only a generic
    message.

  The first error cancels the context of the other loaders of the chain.
- **L-6** Segment keys decide which loaders run again on a navigation. A
  segment's key is derived from its id and the values of its dependencies.
  By default those are the parameters of the segment's own path. A loader
  may declare more:

  ```go
  var Deps = app.Deps{Query: []string{"q"}}
  ```

  `Deps` applies to every loader of its `load.go` package (`Page` and
  `Layout` of one directory). Keys are computed only on the server and are
  opaque to the client (N-3).
  A level whose key changes runs again, and so does every level below it
  (N-4). A layout's data may therefore depend on its parent's data through
  `Parent` without declaring it.
- **L-7** A loader may declare the response mode of its route with
  `const Render = app.RenderAuto` (the default), `app.RenderData`,
  `app.RenderFragment` or `app.RenderDocument` (R-8). Only the `Page`'s
  `load.go` may declare it.

## R. Rendering

- **R-1** Route components get two props from the app. Declaring them is
  optional:
  - `data`: the result of the segment's loader (L-2);
  - `route`: `{ id, url, path, params, query }` for the current
    navigation, where `id` is the route id (P-12), `url` is the path with
    its query, `params` maps parameter names to strings, and `query` maps
    each query name to its first value. The full query is in `url`.

  The page also gets `form`: `null`, or the value of `app.Fail` after a
  failed form action (see [Form actions](#form-actions)). `Error.vela` gets `error`
  (`{ status, message }`) and `route`. No store or context API is needed,
  and the props also work on the server.
- **R-2** The generator writes the app root `src/_app/VelaApp.vela`, which
  `vela` compiles like any other component:
  - it imports the layouts statically and the pages and error pages with
    `lazy()` (071), and keeps a table from segment id to component;
  - its props are `route`, `chain` (`[{ id, key, data, error? }]`, N-5),
    `start`, and the client-only `views`: component values by level number
    (a fragment of R-8) that replace the table's component of that level.
    The server never sets `views`;
  - its template nests `<vela:component>` to the maximum chain depth of the
    project. Level `i` renders `views[start + i]`, or else the component of
    `chain[start + i]`, with its `data` and `route`, and the next level is
    its default slot.

  A level whose component is unchanged gets `set`, so it keeps its state. A
  changed level is replaced. That is all partial navigation needs: there is
  no router-specific runtime code in the components.
- **R-3** An error at level `i` (L-5) renders the nearest `Error.vela` at or
  above level `i` in place of that level and every level below it. The
  levels above it render normally. If there is no `Error.vela`, the app
  uses a built-in error page.
- **R-4** Document shell. A default HTML shell is built in. A project may
  supply `src/document.html` with the placeholders `%vela.head%` and
  `%vela.body%`; each must appear exactly once.
- **R-5** `%vela.head%` receives the merged head (R-6), the client tags of
  `ssr_runtime/vite` for `src/_app/client.js`, modulepreload links for the
  matched lazy pages, and `<meta name="vela-build">` (078).
  `%vela.body%` receives the `VelaApp` root with its payload.
- **R-6** Head: the title of the deepest segment that set one, then
  `Config.Head` defaults. `<meta>` tags from all segments, where a deeper
  segment replaces a tag with the same `name` or `property`. Tags set by
  the app carry `data-vela-head`, so the client can replace them.
- **R-7** The response is buffered rather than streamed, so an error can
  still change the status. A size limit is configurable. Every response
  sends `Vary: X-Vela-Navigate, X-Vela-Have, X-Vela-Build`.
- **R-8** Response modes for one URL:

  | Mode | Response | Client |
  | --- | --- | --- |
  | `document` | full HTML: the document shell, the `VelaApp` root and the payload | `preloadHydration` + `hydrate` |
  | `data` | JSON: `start`, the changed segments with keys and data, and the head | loads the modules of new levels, then calls `set` on the app root |
  | `fragment` | JSON with the HTML of the changed subtree, either an interactive root with its payload or static HTML | `serverComponent` / `staticHTML` at level `start` |

  The first request, and every request without JavaScript, gets
  `document`. The server may always answer `document` or `reload`, for
  example after a build change, a redirect to another chain or a protocol
  mismatch.
- **R-9** Mode choice for `X-Vela-Navigate: auto`:
  1. the route's `Render` constant (L-7), if it is not `auto`;
  2. otherwise `fragment` with `static: true`, if every component of the
     levels from `start` down is a `static` root in `vela.manifest.json`
     (looked up by segment id, P-11), because then no JavaScript is
     needed;
  3. otherwise `data`.

  An explicit `X-Vela-Navigate: data` or `fragment` is honoured, unless the
  route says `RenderDocument`, in which case the answer is `reload`. A
  fragment (from `RenderFragment` or an explicit request) is static when
  every component sent is a static root, and interactive otherwise. "Every
  component sent" includes the error page of an error segment. Without a
  manifest (`Config.Manifest`), `auto` is always `data`.
- **R-10** Fragments render the levels from `start` (task 077):
  - **static**: the server nests the static renderings of the components
    (`vela.Component.RenderDynamic` with `Static: true`; the generated
    `Routes.Components` lists them by segment id), each level the default
    slot of the one above, with `data`, `error` and `route` as props. No
    markers, no payload. The segments carry no `data`: a static level
    holds no data on the client;
  - **interactive**: the server renders the `VelaApp` root with `start`.
    Its `chain` has the levels above `start` without `data`, so the root
    has its own payload and is a complete root for `serverComponent`. The
    segments carry their `data`, so later data navigations know the chain.

  The head, the headers and the error handling are those of `data`. A
  fragment that fails to render answers `reload` (`document`).

## N. Navigation protocol

- **N-1** The protocol is versioned. This page describes version **1**.
  Requests carry the version in `X-Vela-Navigate`, and responses carry it
  in `"v"`. On a version mismatch, the server answers `reload`, and the
  client falls back to `location.assign(url)`.
- **N-2** A navigation request is a GET of the target URL with these
  headers:

  | Header | Value |
  | --- | --- |
  | `X-Vela-Navigate` | `auto;v=1`, `data;v=1` or `fragment;v=1`. Without this header, the request is a document request |
  | `X-Vela-Have` | the keys of the chain the client holds, in level order, separated by `,`; empty after `invalidate()` |
  | `X-Vela-Build` | the build id the client runs |

- **N-3** A key is an opaque token of at most 64 characters from
  `[A-Za-z0-9_-]`. The server computes it from the segment id and the
  dependency values (L-6). The keys of different segment ids differ, so
  the key alone identifies the level. `X-Vela-Have` is untrusted input: it
  is parsed with limits (at most 32 keys and 4 KiB), a malformed value is
  treated as empty, and it only ever lets the server skip work. Data is
  never taken from it.
- **N-4** `start` is the first level whose key differs from the key the
  client holds at that level, or the end of the shorter of the two chains
  if all their common levels match. The server runs the loaders of
  levels `>= start`. It runs loaders above `start` only on demand: for
  `Parent` (L-4), or for a head when that segment sets one (R-6). The
  generator decides the latter from the source: a loader sets the head
  when its body, or a function of its package other than the two loaders,
  calls a method `Head()` (`Level.Head`). A loader that sets the head
  through a function of another package is not detected; its title then
  stays out of the navigation's head.
- **N-5** Response: status 200, `Content-Type: application/json`, and a
  `type` field that says what happened. `build` is always present.

  ```jsonc
  // data
  { "v": 1, "type": "data", "build": "…",
    "route": { "id": "/users/[id=int]", "url": "/users/2?tab=a", "path": "/users/2",
               "params": { "id": "2" }, "query": { "tab": "a" } },
    "start": 2,
    "segments": [ { "id": "routes/users/[id=int]/Page", "key": "…", "data": { … } } ],
    "head": { "title": "User 2", "meta": [ { "name": "description", "content": "…" } ] } }

  // fragment: the data fields, plus the HTML of the levels from start
  // (R-10); the segments of a static fragment have no data
  { …, "type": "fragment", "fragment": { "html": "…", "static": true } }

  // redirect: the client navigates again, at most 10 times in a row
  { "v": 1, "type": "redirect", "build": "…", "location": "/login", "status": 303 }

  // reload: the client does a full load of the URL it asked for
  { "v": 1, "type": "reload", "build": "…", "reason": "build" }
  ```

  - `segments` are the chain levels from `start` to the end, and the client
    keeps the levels before `start`. Each segment has `id`, `key`, and
    either `data` (the loader result, or `null`) or `error`
    (`{ "status": 404, "message": "…" }`). A segment with `error` is the
    `Error.vela` of R-3. It is always the last one, and its `id` is the id
    of that error component. Errors are therefore ordinary data responses,
    not a separate type.
  - `head` is the merged head of the new chain (R-6): the defaults and the
    heads of the levels that set one, kept or not (N-4). The server always
    sends it; a client keeps its current head when it is absent.
  - `reason` is `build`, `protocol` or `document`. It is informative, and
    the client handles every value the same way.
  - A response with an unknown `type` or another `v` is handled as
    `reload`.
  - `{ "v": 1, "type": "form", "build": "…", "status": 422, "form": … }`
    answers a POST whose action failed with `app.Fail`: the client sets
    `form` on the page level and changes nothing else.
- **N-6** Inside the protocol, redirects and errors are JSON with status
  200, so that `fetch` follows them under the router's control. For a
  document request, they are real 3xx and 4xx/5xx responses.
- **N-7** In a document, the `VelaApp` payload carries `route`, `chain` and
  `start: 0`. The `key` values in `chain` are what the client sends in
  `X-Vela-Have` later. After a `data` response, the client merges the
  levels it kept with the received segments and calls
  `set({ route, chain, start: 0 })`.
- **N-8** `internal/protocol` holds the Go types, the header encoding and
  parsing, and the version check. The JSON field names on this page are
  normative. The Go names may change.

## Generator

`vela-app` (task 074) turns the routes tree into sources that `vela --ssr`
and `go build` compile. Run it in the project directory, the one with
`vela.config.json` and `go.mod`:

```sh
go run github.com/elauven/vela/app/cmd/vela-app gen   # once, or before vela
go run github.com/elauven/vela/app/cmd/vela-app watch # next to vela --watch
vela && go build ./...
```

| Command | Effect |
| --- | --- |
| `gen` | Scans the routes tree, the mirrored Go tree and the matchers, and writes the four files below. It writes only files whose content changed, removes stale files it owns, and writes nothing when there are errors (exit status 1) |
| `watch` | Runs `gen` whenever the routes tree, the mirrored Go tree, the matchers or the configuration change. The compiler's own watcher picks up the regenerated `.vela` file |
| `scaffold [route]` | Creates `load.go` with the `Page`/`Layout` signatures of L-1 for one route (its route id, such as `/users/[id=int]`) or for every route directory without Go files (P-18). It never overwrites a file |

`-C dir` runs a command in another directory.

### Configuration

`vela.app.json` is optional. Its fields and their defaults:

| Field | Default | Meaning |
| --- | --- | --- |
| `project` | `"project"` of `vela.config.json`, else `src` | the directory `vela` compiles; component and segment ids are relative to it |
| `routes` | `<project>/routes` | the routes tree |
| `out` | `<project>/_app` | `VelaApp.vela`, `routes.js`, `client.js` |
| `params` | `<project>/params` | JavaScript matchers |
| `server` | `server/routes` | the mirrored Go tree |
| `serverParams` | `server/params` | Go matchers |
| `goPackage` | `internal/appgen` | `routes_gen.go` |
| `goModule` | the module of `go.mod` | import root of the mirrored tree and the matchers |
| `ssrModule` | `"ssrModule"` of `vela.config.json` (required) | import root of the generated renderers |

### Generated files

Every generated file starts with `Code generated by vela-app. DO NOT EDIT.`.
A file without that line is never overwritten or removed.

| File | Contents |
| --- | --- |
| `src/_app/VelaApp.vela` | The app root of R-2: layouts imported statically, pages and error pages with `lazy()`, a table from segment id to component, and one `<vela:component>` per level up to the maximum chain depth, each with `data`, `error` and `route` |
| `src/_app/routes.js` | The client table: the routes with their chains, the patterns in ranking order, the matchers (`@govela/app/match` and the project's own), the lazy components by segment id (`lazy()` returns the value `VelaApp` registered under the same id) and `match(path)` |
| `src/_app/client.js` | The client entry: `start({ VelaApp, routes, root: 'vela-app' })` from `@govela/app/client` (task 076) |
| `internal/appgen/routes_gen.go` | `appgen.Routes() app.Routes`: the same patterns, the chains with their loaders (`app.LoaderOf`), `Deps` and `Render` of the mirrored packages, the matchers, and the `VelaApp` renderer. It compiles against the `app` package only, without reflection |

The patterns are expanded and ranked by the generator: a route with
optional segments has one pattern per combination, and both matchers,
`app.Routes.Match` in Go and `matchPath` of `@govela/app/match`, take the
first pattern that matches the whole path. `app/testdata/routing` (a routes
tree and `cases.json`) and `app/testdata/matchers.json` test the two
matchers against the same cases.

### Diagnostics

Errors are printed as the compiler prints its own:
`file:line:col: [error] [CODE] message`, then `help: …`. An error about a
directory names the directory or the `Page.vela` of the route.

| Code | Rules |
| --- | --- |
| `VELA-APP-CONFIG` | `vela.app.json`, `vela.config.json`, `go.mod`; a missing routes directory; a generated path held by a file of the developer |
| `VELA-APP-DIRECTORY` | a name `pkgpath` cannot encode (P-3) |
| `VELA-APP-FILE` | another `.vela` file (P-1), a `.go` file in the routes tree (P-17) |
| `VELA-APP-REST` | a directory below `[...rest]` (P-4) |
| `VELA-APP-PARAM` | a parameter name repeated along a path (P-5) |
| `VELA-APP-RANK` | two candidates of the same rank at one level (P-8) |
| `VELA-APP-DUPLICATE` | two routes with one URL pattern, also through a group or an optional segment (P-9) |
| `VELA-APP-MATCHER` | an unknown matcher, a missing half, a wrong `Match`, shadowing a built-in (P-13, P-14) |
| `VELA-APP-STALE` | a mirrored Go directory without a route directory (P-17) |
| `VELA-APP-LOADER` | a loader without its component, a wrong signature, `Deps` or `Render` of the wrong kind, `Render` outside a page (L-1, L-6, L-7) |

`examples/app` is a small application: a home page, an `(admin)` group
with a layout, `users` and `users/[id=int]` with layouts and loaders,
`[[lang]]/about`, a custom matcher, a redirect (`/me`), a failing loader
(`/crash`) and an error page. `cmd/server` serves it with the build of
`vite build` or the Vite dev server (`-dev`). `TestExampleAppServes`
checks every route's status and title, and `TestExampleAppHydrates` builds
it with Vite and checks in Chromium that every document hydrates without
a mismatch and preloads its lazy page.

## Server

`app.New` (task 075) serves full documents: with it alone an app is a
multi-page site whose pages are hydrated.

```go
assets, err := vite.Load("dist/.vite/manifest.json", "/") // or vite.Dev(url)
h, err := app.New(app.Config{
    Routes: appgen.Routes(),
    Assets: assets,
    Head:   app.Head{Title: "Site", Meta: []app.Meta{{Name: "description", Content: "…"}}},
})
mux.Handle("/assets/", http.FileServer(http.Dir("dist")))
mux.Handle("/", h)
```

| `Config` field | Meaning |
| --- | --- |
| `Routes` | `appgen.Routes()` |
| `Assets` | the client tags of `ssr_runtime/vite`; without them pages are not hydrated |
| `Document` | the shell of R-4; the built-in one when empty |
| `Head` | the default title and meta tags of R-6 |
| `BuildID` | the build id (`<meta name="vela-build">`, `build` of every JSON response); by default `Assets.BuildID()`: `dev` with `vite.Dev`, a hash of the Vite manifest with `vite.Load` |
| `CrossOrigin` | the CSRF protection of form actions; `http.NewCrossOriginProtection()` when nil, which rejects cross-origin POSTs (`Sec-Fetch-Site`, `Origin`). Add trusted origins to it to allow other sites |
| `Manifest` | the path of `vela.manifest.json` (`out/client/vela.manifest.json`); its static roots choose static fragments (R-9). Without it `auto` navigations get data |
| `Logger` | receives loader and render errors (`slog.Default()` when nil) |
| `Dev` | the error page shows the message of an internal error; otherwise "Internal Error" |
| `MaxBytes` | the size limit of a buffered response (R-7), 8 MiB by default |

A request:

1. Only GET and HEAD are served; other methods get 405 with `Allow`. A path
   with a trailing slash redirects with 308 (P-7); leading slashes collapse,
   so `//host/` cannot redirect to another host.
2. The URL is matched (P-6, P-8). A URL no route matches renders the
   layouts of the routes directory with the root `Error.vela` and 404.
3. The loaders of the chain run concurrently under the request context,
   each with its own `*app.Request`. `app.Parent[T](r)` waits for the
   nearest layout above with a loader. An error at a level cancels the
   levels below it; the levels above it keep running, since they render
   around the error page. The handler returns only after every loader it
   started has returned, so a loader never sees its request reused.
4. The first level (from the top) that failed decides the answer (L-5):
   `app.Redirect` answers with its status and `Location`; `app.NotFound` and
   `app.Error` with their status and message; another error, a panic or a
   result that does not marshal with 500, logged with the route and the
   level (and the stack of a panic). The `Error.vela` of that level
   (`Level.Error`, the nearest at or above it) replaces it and the levels
   below it, as the last chain segment with `error: { status, message }`.
   Without an `Error.vela` the built-in error page answers.
5. Loader results become props through `encoding/json` (L-2), and each
   level gets its key (L-6). The `VelaApp` root is rendered with `route`,
   `chain` and `start: 0` into the shell: the merged head (R-6), the client
   tags, `<link rel="modulepreload">` and stylesheets of the lazy page or
   error page (`vite.Assets.Preload`), and `<meta name="vela-build">`.
6. The response is buffered. It sends `Content-Type`, `Content-Length`,
   `Vary` (R-7) and `Cache-Control`, `private` unless a loader set it.

A loader sets its segment's head and headers through its request:

```go
r.Head().Title = user.Name
r.Head().SetMeta(app.Meta{Name: "description", Content: "…"})
r.CacheControl("public, max-age=60") // r.Header() for other headers
```

A deeper segment replaces a header set above it. On the client,
`start({ VelaApp, routes })` of `@govela/app/client`, which the generated
`client.js` calls, loads the lazy components the payload names
(`preloadHydration`), hydrates the root, dispatches `vela:hydrated` on the
root element and installs the router ([Navigation](#navigation)).

A request with `X-Vela-Navigate` (N-2) is a navigation of the router and
gets JSON with status 200 (N-5, N-6):

1. Another protocol version or an unknown mode answers `reload`
   (`protocol`), and an `X-Vela-Build` other than `Handler.BuildID()`
   answers `reload` (`build`). A route with `RenderDocument` answers
   `reload` (`document`) without running loaders.
2. The keys of the new chain are computed, and `start` is the first level
   whose key differs from `X-Vela-Have` (N-4). A malformed `X-Vela-Have` is
   empty, so every level is sent.
3. The loaders of the levels from `start` run, and so do the loaders above
   it that set the head (`Level.Head`). `Parent` runs any other kept level
   on demand. Kept levels are not sent.
4. A redirect is `{ "type": "redirect" }`, a trailing slash too (308). An
   error is the last segment, with the `Error.vela` id and `error` (R-3);
   when a kept level fails (for `Parent` or its head), `start` moves up to
   it. An error with no `Error.vela` answers `reload` (`document`), so the
   built-in error page loads as a document.
5. `head` is the merged head (N-5). `Cache-Control` and other headers come
   from the loaders that ran; kept levels that did not run add none.

## Navigation

`@govela/app/client` is the client router. The generated `client.js` starts
it; an app imports the rest:

```js
import { goto, invalidate, navigating, beforeNavigate, afterNavigate } from '@govela/app/client';
```

| Export | Meaning |
| --- | --- |
| `goto(url, { replace, state, invalidate })` | navigates like a link; `state` is kept in `history.state.state`; `invalidate` runs every loader again. Resolves when the navigation ends |
| `invalidate()` | loads the current URL again with every loader (empty `X-Vela-Have`), keeping the history entry, the scroll position and the focus |
| `navigating` | a store: `null`, or `{ from, to, type }` while a navigation loads, for loading indicators (`$navigating`) |
| `beforeNavigate(fn)` | `fn({ from, to, type, cancel })` before each navigation; `cancel()` keeps the page. Returns a function that removes `fn` |
| `afterNavigate(fn)` | `fn({ from, to, type })` once the new page shows. Returns a function that removes `fn` |

`from` and `to` are `{ url, route }`, with `url` a `URL` and `route` the route
id; `type` is `link`, `goto`, `popstate` or `invalidate`. Register hooks in
`onMount` and return the remover, so they go away with the component:

```vela
<script>
  import { onMount } from 'vela';
  import { beforeNavigate } from '@govela/app/client';

  let dirty = $state(false);

  function confirmLeave(navigation) {
    if (dirty && !confirm('Leave without saving?')) navigation.cancel();
  }

  onMount(() => {
    return beforeNavigate(confirmLeave);
  });
</script>
```

The router:

- **Links.** One `click` listener on `document` takes a click on an `<a>` of
  the same origin with the primary button and no modifier key, unless the
  link has a `target` other than `_self`, `download`, `rel="external"` or
  `data-vela-reload`, or only changes the `#hash` of the page (the browser
  scrolls). An SVG `<a>` is left to the browser. Without JavaScript every
  link is a document request.
- **Navigation.** A new navigation aborts the one in flight. A URL that no
  client route matches (`routes.js`) loads as a document. Otherwise the
  router sends `X-Vela-Navigate: auto;v=1`, the keys it holds and the build
  id (N-2), and loads the modules of the new chain's lazy pages meanwhile.
  On `data` it loads the modules of the received segments, keeps the levels
  before `start`, calls `set({ route, chain, start: 0 })` on the app root
  (N-7) and replaces the title and the `<meta data-vela-head>` tags. Levels
  whose component is unchanged keep their DOM and state (R-2).
- **Redirects** navigate again, at most 10 times; a redirect to another
  origin or to a URL without a client route loads the document. `reload`,
  another protocol version, a network error and a response that is not the
  protocol load the document (`location.assign`, or `location.reload` for
  back and forward).
- **History.** Each entry has an index in `history.state`. A link or
  `goto` pushes an entry (a link to the current URL replaces it), back and
  forward navigate without pushing. Scroll positions are kept per index
  (also across reloads, in `sessionStorage`), and restored after back and
  forward; other navigations scroll to the top or to the `#hash`.
- **Focus.** After a navigation, focus moves to the first `[autofocus]`
  element the navigation added, or to `<body>`, and a polite live region
  (`#vela-announcer`, outside the app root) announces the new title.
- **Fragments** (R-8, R-10). A static fragment becomes `staticHTML(html)`
  and loads no module. For an interactive fragment the router loads the
  lazy pages inside it (`preloadHydration` on the parsed HTML) and mounts
  `serverComponent(VelaApp, html)`. Either value goes into the `views` of
  level `start` of the instance that owns that level.
- **Nested roots.** An interactive fragment is a `VelaApp` instance that
  renders the levels from its `start`. The router keeps a stack of
  instances: the hydrated root, then one per interactive fragment inside
  the one above. Data from level `j` goes to the innermost instance whose
  `start <= j`; a fragment at level `j` goes into the views of the
  innermost instance whose `start < j` (the root holds every level). The
  instances below the target go with the levels they rendered. Every
  instance that stays gets `set({ route, chain, start, views })`.
- **Static levels.** `X-Vela-Have` stops at the first level of a static
  fragment: those levels hold no data and render as one piece, so the
  server sends them again when anything in them changes.
- **Modules.** `routes.js` gives each route the `render` mode the generator
  read from `const Render = app.RenderX`. The router loads the modules of
  a `data` route while the server answers; for other routes it loads them
  once a `data` response names them, so a static fragment loads none.
- Each navigation dispatches `vela:navigated` on the root element, with
  `{ from, to, type }` as its detail.

## Forms, prefetching, deploys and development

### Form actions

A page takes POSTs through `actions.go` next to its `load.go` in the
mirrored Go tree (`server/routes/users/actions.go` for
`src/routes/users/Page.vela`). The generator finds `func Actions()
app.Actions` like the loaders and rejects it without a `Page.vela` or with
another signature (`VELA-APP-LOADER`).

```go
func Actions() app.Actions {
    return app.Actions{"default": add, "delete": remove}
}

func add(ctx context.Context, r *app.Request) (any, error) {
    var in struct{ Name string `form:"name"` }
    if err := r.Decode(&in); err != nil {
        return nil, err
    }
    if in.Name == "" {
        return nil, app.Fail(422, map[string]string{"error": "A name is required."})
    }
    store.Add(in.Name)
    return nil, nil
}
```

- `POST <page url>` runs `default`; `POST <page url>?/delete` runs
  `delete` (in a form: `action="?/delete"`). An unknown name is 404. Only
  a page with actions takes POST; anything else is 405.
- `r.Form()` parses the body (urlencoded or multipart); `r.Decode(&v)`
  fills a struct from it (the `form` tag, else the `json` tag, else the
  lowercased field name; strings, booleans, numbers and slices), or from a
  JSON body. `app.Parent` works in an action.
- The answer, with the page URL without `?/name`:

  | Action result | Without JavaScript | With JavaScript (router) |
  | --- | --- | --- |
  | success | 303 to the page (post/redirect/get) | data response from level 0: every loader runs again |
  | `app.Fail(status, value)` | the page with `form` = value and that status (400–499) | `{ "type": "form" }`: only the page's `form` changes |
  | `app.Redirect` | that redirect | `{ "type": "redirect" }` |
  | another error | the error page (L-5) | a data response with the error segment |

- POST responses send `Cache-Control: no-store`. Cross-origin POSTs are
  rejected with 403 before the action runs (`Config.CrossOrigin`).
- The router takes `submit` of `<form method="post">` inside the app root,
  of the same origin, without `target` and without `data-vela-reload` (on
  the form or the submit button), and posts the form (`FormData`, with the
  submitter; multipart when `enctype` says so) with `X-Vela-Navigate`.
  `navigating` is `{ type: 'form' }` meanwhile. A success replaces the
  history entry when the page is the current one and pushes it otherwise;
  a failure dispatches `vela:form` on the root. A `form` prop lasts until
  the next navigation. When the router cannot answer (reload, another
  protocol, a network error), the browser posts the form itself.

### Prefetching

`<a data-vela-preload="hover|viewport|off">` (on the link or an ancestor;
`start({ preload })` is the default, `hover`). On hover after 50 ms, on
`touchstart`, or when the link enters the viewport, the router asks for the
navigation's answer and loads the modules it needs. The answer serves one
navigation to the same URL with the same `X-Vela-Have` within 30 s.
`invalidate()` and form actions clear it.

### Deploys

Every document has `<meta name="vela-build">` and every JSON response
`build`. The router sends its build in `X-Vela-Build`; a server of another
build answers `reload`, and the router loads the document. A lazy module
that fails to load during a navigation (a chunk the new build removed) also
loads the document. So a page that stays open across a deploy gets the new
build on its next navigation, not a broken page.

### `vela-app dev`

```sh
vela-app dev                         # in the project directory
vela-app dev -server "go run ./cmd/server -addr :8080 -dev http://localhost:5173" -vite "npx vite"
```

One command runs the loop of [SSR.md](SSR.md) ("Development loop"): it
regenerates the app sources (`watch`, in process), runs `vela --watch`
(`$VELA_BINARY` or `vela`; `-vela` overrides), starts the Go server once
vela has built and restarts it after every good build (`out/.vela-ready`)
and every change of a Go file outside the generated directories, and runs
Vite (`-vite ""` runs none). The output of each process has its prefix
(`[gen]`, `[vela]`, `[server]`, `[vite]`, `[dev]`). Ctrl-C stops every
process; when vela or Vite exits, `dev` stops too; when the server exits,
the next change starts it again.

## Decisions on the open questions of 073

Approved together with the rest of this page on 2026-11-03:

1. `route` carries the route id (`route.id`, P-12). It is cheap, and it lets
   a layout highlight the active section without parsing paths. It does
   not carry a separate matched pattern: the route id is that pattern.
2. v1 has no client-side data cache for back and forward: a history
   navigation asks the server like any other navigation. `X-Vela-Have`
   already skips unchanged levels, and the 30-second prefetch cache of 078
   covers the common "hover, then click" case. A history cache can be added
   later without changing the protocol.

## Not included in v1

Streaming and deferred data, mixed segments (`[a]-[b]`), layout resets
(SvelteKit `+layout@`), i18n routing beyond `[[lang]]`, prerendering to
static files, and endpoints other than loaders and actions (the user's own
Go handlers cover APIs). Hot module replacement belongs to task 008.
