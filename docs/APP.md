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

Status: **planned**. This page is the contract (task 073). The
implementation follows in tasks 074–078, and until then the `app/` module
and `@govela/app` contain only their skeleton and the protocol types. The
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
  then without one. Groups add no segment.
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
  - Go: `server/params/name.go`, with `func Match(segment string) bool`;
  - JavaScript: `src/params/name.js`, with `export function match(segment)`
    returning a boolean.

  A missing half or an unknown matcher name is a generator error. The two
  roots are configurable (`"serverParams"`, `"params"`). A custom matcher
  cannot shadow a built-in one.
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

  Keys are computed only on the server and are opaque to the client (N-3).
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

  The page also gets `form` (task 078). `Error.vela` gets `error`
  (`{ status, message }`) and `route`. No store or context API is needed,
  and the props also work on the server.
- **R-2** The generator writes the app root `src/_app/VelaApp.vela`, which
  `vela` compiles like any other component:
  - it imports the layouts statically and the pages and error pages with
    `lazy()` (071), and keeps a table from segment id to component;
  - its props are `route`, `chain` (`[{ id, key, data, error? }]`, N-5) and
    `start`; task 077 adds the client-only `views`;
  - its template nests `<vela:component>` to the maximum chain depth of the
    project. Level `i` renders `chain[start + i]` with its `data` and
    `route`, and the next level is its default slot.

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
  route says `RenderDocument`, in which case the answer is `reload`.

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
  `Parent` (L-4), or for a head when that segment sets one (R-6).
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

  // fragment: the data fields, plus
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
  - `head` is absent when no segment at or after `start` set one. The
    client then keeps its current head.
  - `reason` is `build`, `protocol` or `document`. It is informative, and
    the client handles every value the same way.
  - A response with an unknown `type` or another `v` is handled as
    `reload`.
  - Task 078 adds `{ "type": "form", "status": 422, "form": … }` for action
    failures.
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

## Decisions on the open questions of 073

These need review together with the rest of this page before 074 starts:

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
