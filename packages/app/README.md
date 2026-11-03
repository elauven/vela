# @govela/app

The client router of Vela App, the application layer above the Vela compiler
with a Go server. The contract is [docs/APP.md](../../docs/APP.md).

This package is not published yet. `@govela/app/match` has the built-in
matchers (`int`, `uuid`, `slug`) and `matchPath`, which the route table that
`vela-app gen` writes (`src/_app/routes.js`) uses; the Go package
`github.com/elauven/vela/app` matches with the same rules. `start({ VelaApp,
routes })` of `@govela/app/client` hydrates the server document: it loads the
lazy components the page renders, hydrates the app root and dispatches
`vela:hydrated` on the root element. Partial navigation (`goto`,
`navigating`, `beforeNavigate`, `afterNavigate`, `invalidate`) follows in
task 076.

It imports only the public entry points of `@govela/runtime` and has its own
version, independent of the compiler's.
