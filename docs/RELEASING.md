# Releasing

One repository, three published products, released together with the same
version `vX.Y.Z` while the contract is in `v0` (docs/SSR_TRANSLATION_CONTRACT.md
§12).

| Product | Source | Release |
| --- | --- | --- |
| Compiler `vela` | repository root (module `github.com/elauven/vela`) | tag `vX.Y.Z`; binaries from CI |
| Go SSR runtime | `ssr_runtime/` (nested module `github.com/elauven/vela/ssr_runtime`) | tag `ssr_runtime/vX.Y.Z` (R-12.1c) |
| JavaScript runtime | `runtime/`, generated into `packages/runtime` | npm package `@vela/runtime` of the same version (R-12.2g) |

The module path is not final yet (R-12.1b): it appears only in
`SSRRuntimeImportPath` and in `ssr_runtime/go.mod`.

## Steps

1. All suites pass, including the strict browser baseline:
   `go test ./... ./ssr_runtime/...` and `node tools/run-ssr-browser-baseline.mjs`.
2. Set `Version` in `runtime_package.go` to `X.Y.Z` and regenerate the npm
   package: `VELA_UPDATE_RUNTIME_PACKAGE=1 go test -run TestRuntimePackageIsGenerated .`.
3. If generated code started to depend on a new or changed runtime API since
   the last release, the contract marker changed (`ssr_runtime/contract.go`
   and `SSRRuntimeContract`, R-12.1d).
4. Tag the Go runtime first: `git tag ssr_runtime/vX.Y.Z`.
5. Publish the JavaScript runtime: `npm publish packages/runtime --access public`.
6. Tag the compiler: `git tag vX.Y.Z`; CI builds the binaries.
7. Update `examples/` to require the released runtime instead of the
   repository `replace`.

## Development

The committed `go.work` joins the compiler and the runtime module, so tests
of the compiler use the runtime from the working tree. The compiler itself
does not import the runtime and builds without the workspace
(`GOWORK=off go build .`, checked in CI), which is how `go install` builds it.
