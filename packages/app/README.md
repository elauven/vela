# @govela/app

The client router of Vela App, the application layer above the Vela compiler
with a Go server. The contract is [docs/APP.md](../../docs/APP.md).

This package is a skeleton (task 073) and is not published yet. The router
(`start`, `goto`, `navigating`, `beforeNavigate`, `afterNavigate`,
`invalidate`) follows in task 076.

It imports only the public entry points of `@govela/runtime` and has its own
version, independent of the compiler's.
