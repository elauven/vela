# @govela/app

The client router of Vela App, the application layer above the Vela compiler
with a Go server. The contract is [docs/APP.md](../../docs/APP.md).

It also installs the `vela-app` command, which generates the app sources,
runs the development loop and makes production builds:

```sh
npm install @govela/app @govela/runtime
npx vela-app dev
npx vela-app build
```

`bin/vela-app.js` runs the binary of the optional dependency
`@govela/app-<os>-<cpu>` for this platform (linux x64/arm64, macOS
x64/arm64, Windows x64), or `$VELA_APP_BINARY`. Do not install with
`--omit=optional`. The guide is
[docs/VELA_APP.md](../../docs/VELA_APP.md).

`@govela/app/match` has the built-in
matchers (`int`, `uuid`, `slug`) and `matchPath`, which the route table that
`vela-app gen` writes (`src/_app/routes.js`) uses; the Go package
`github.com/elauven/vela/app` matches with the same rules.

`@govela/app/client`:

- `start({ VelaApp, routes })`, which the generated `client.js` calls,
  hydrates the server document and installs the router: links of the same
  origin navigate inside the app, the server sends the data of the changed
  segments only, and layouts above them keep their DOM and state; a static
  section arrives as server HTML, and a `RenderFragment` page as a server
  rendered root that the router hydrates;
- `goto(url, { replace, state, invalidate })` and `invalidate()`;
- the `navigating` store;
- `beforeNavigate(fn)` and `afterNavigate(fn)`, which return a remover for
  `onMount`;
- POST forms inside the app root are posted with `fetch` to the page's Go
  action; links prefetch on hover (`data-vela-preload`, `start({ preload
  })`).

The rules for links, history, scroll, focus and the head are in the
[Navigation](../../docs/APP.md#navigation) section of APP.md. Types for
editors are in `client.d.ts` and `match.d.ts`.

It imports only the public entry points of `@govela/runtime` (a peer
dependency) and has its own version, independent of the compiler's.
