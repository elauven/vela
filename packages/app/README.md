# @govela/app

The client router of Vela App, the application layer above the Vela compiler
with a Go server. The contract is [docs/APP.md](../../docs/APP.md).

This package is not published yet. `@govela/app/match` has the built-in
matchers (`int`, `uuid`, `slug`) and `matchPath`, which the route table that
`vela-app gen` writes (`src/_app/routes.js`) uses; the Go package
`github.com/elauven/vela/app` matches with the same rules.

`@govela/app/client`:

- `start({ VelaApp, routes })`, which the generated `client.js` calls,
  hydrates the server document and installs the router: links of the same
  origin navigate inside the app, the server sends the data of the changed
  segments only, and layouts above them keep their DOM and state;
- `goto(url, { replace, state, invalidate })` and `invalidate()`;
- the `navigating` store;
- `beforeNavigate(fn)` and `afterNavigate(fn)`, which return a remover for
  `onMount`.

The rules for links, history, scroll, focus and the head are in the
[Navigation](../../docs/APP.md#navigation) section of APP.md. Types for
editors are in `client.d.ts` and `match.d.ts`.

It imports only the public entry points of `@govela/runtime` (a peer
dependency) and has its own version, independent of the compiler's.
