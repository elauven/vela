# Releasing

One repository, four published products, released together with the same
version `vX.Y.Z` while the contract is in `v0` (docs/SSR_TRANSLATION_CONTRACT.md
§12).

| Product | Source | Release |
| --- | --- | --- |
| Compiler `vela` and language server `vela-lsp` | repository root and `cmd/vela-lsp` (module `github.com/elauven/vela`) | tag `vX.Y.Z`; `.github/workflows/release.yml` builds the binaries and publishes the GitHub release |
| Go SSR runtime | `ssr_runtime/` (nested module `github.com/elauven/vela/ssr_runtime`) | tag `ssr_runtime/vX.Y.Z` (R-12.1c) |
| JavaScript runtime | `runtime/`, generated into `packages/runtime` | npm package `@govela/runtime` of the same version (R-12.2g), published by the release workflow |
| Compiler for npm | `packages/compiler` (launchers `bin/vela.js` and `bin/vela-lsp.js`, JavaScript API `index.js`) and `packages/compiler-<os>-<cpu>` (`vela` and `vela-lsp` each) | npm packages `@govela/compiler` and `@govela/compiler-linux-x64`, `-linux-arm64`, `-darwin-x64`, `-darwin-arm64`, `-win32-x64` of the same version, published by the release workflow |
| Starter, test helpers and ESLint processor | `packages/create` (command `bin/create.js`, `templates/`), `packages/testing`, `packages/eslint-plugin` (code written by hand, manifests generated) | npm packages `@govela/create`, `@govela/testing` and `@govela/eslint-plugin` of the same version, published by the release workflow after `@govela/compiler` |
| Bundler plugin | `packages/unplugin` (plugin `index.js`, entry points generated) | npm package `@govela/unplugin` of the same version, published by the release workflow after `@govela/compiler` |
| TypeScript server plugin | `packages/typescript-plugin` (plugin `index.js`, manifest generated) | npm package `@govela/typescript-plugin` of the same version, published by the release workflow after `@govela/compiler` |
| App layer (Go) and `vela-app` | `app/` (nested module `github.com/elauven/vela/app`, command `app/cmd/vela-app`) | tag `app/vX.Y.Z`, independent of the compiler version (docs/APP.md B-6); `.github/workflows/release-app.yml` builds `vela-app` for the five platforms and publishes the GitHub release `app/vX.Y.Z` |
| App layer (npm) | `packages/app` (client router, launcher `bin/vela-app.js`) and `packages/app-<os>-<cpu>` (`vela-app`) | npm packages `@govela/app` and `@govela/app-linux-x64`, `-linux-arm64`, `-darwin-x64`, `-darwin-arm64`, `-win32-x64` of the layer's version, published by the app release workflow |
| VS Code extension | `packages/vscode-vela` | its own version (`package.json`), not checked against the tag; CI builds the `.vsix` as an artifact; publishing to the Marketplace or Open VSX is not set up |

All of them are released under the MIT license (`LICENSE`). The nested Go
modules and `packages/app` carry copies in `ssr_runtime/LICENSE`,
`app/LICENSE` and `packages/app/LICENSE` (`TestLicenseCopiesMatch`), and
the npm packages get `LICENSE` and `"license": "MIT"` from the generators.

The manifests of the npm packages are generated with `Version`
(`runtime_package.go`, `compiler_package.go`): `@govela/compiler` depends on
every platform package of exactly its version as an optional dependency, and
npm installs the one whose `os` and `cpu` match. The platform binaries are not
committed; the release workflow builds them into `packages/compiler-*/bin/`.

The module paths are final (R-12.1b): `github.com/elauven/vela` for the
compiler and `github.com/elauven/vela/ssr_runtime` for the runtime. In the
compiler the runtime path appears only in `SSRRuntimeImportPath`.

## Steps

1. All suites pass, including the strict browser baseline:
   `go test ./... ./ssr_runtime/...` and `node tools/run-ssr-browser-baseline.mjs`.
   The TypeScript gate is part of it: the *TypeScript* job of CI on Linux,
   macOS and Windows (the type tests of the packages on TypeScript 5.8 and
   6, `vela check --types` on its fixtures, `examples/` and the examples of
   AGENTS.md and the guide, the TypeScript server plugin) and the twin
   suites of the test job (`TestTypeScriptTwinsCompileAlike`,
   `TestSSRTypeScriptTwins`); the parser's oracle
   (`internal/jsast/ts_oracle_test.go`, with `VELA_TS_ORACLE_DIR` naming
   more real projects) runs before a release that changes
   `internal/jsast`.
2. Set `Version` in `runtime/embed.go` to `X.Y.Z` and regenerate the npm
   packages: `VELA_UPDATE_RUNTIME_PACKAGE=1 go test -run 'TestRuntimePackageIsGenerated|TestCompilerPackagesAreGenerated' .`.
3. If generated code started to depend on a new or changed runtime API since
   the last release, the contract marker changed (`ssr_runtime/contract.go`
   and `SSRRuntimeContract`, R-12.1d).
4. Tag the Go runtime first: `git tag ssr_runtime/vX.Y.Z`.
5. Tag the compiler: `git tag vX.Y.Z`.
6. Push both tags by name, never `--tags`:
   `git push origin ssr_runtime/vX.Y.Z vX.Y.Z`. The release workflow checks
   that the tag matches `Version` and every npm manifest, runs the Go tests,
   builds `vela` and `vela-lsp` for the five platforms with `SHA256SUMS`, publishes the GitHub release,
   puts the binaries into the platform packages and publishes the npm
   packages: the platform packages, `@govela/runtime`, then
   `@govela/compiler`, with provenance. It needs the repository secret
   `NPM_TOKEN`, an automation token of the npm organization `govela`, which
   owns the scope. A version already on npm is skipped, so a failed run can
   be repeated.
7. Update `examples/` to require the released runtime instead of the
   repository `replace`.

## History

| Tag | Commit | What it is |
| --- | --- | --- |
| `v0.1.0` | 2026-05-20 | The first compiler: JavaScript generated by string concatenation |
| `v0.2.0` | 2026-08-28 | JavaScript generated from an AST, before server rendering |
| `v0.3.0` | | Server rendering to Go and hydration; first release with `ssr_runtime/v0.3.0` and `@govela/runtime` |
| `v0.3.1` | | Fixes of v0.3.0: the Go runtime on arm64 and other fused multiply-add architectures matches JavaScript, JSON of invalid UTF-8 matches every Go version; `ssr_runtime/v0.3.0` is retracted and `@govela/runtime@0.3.0` deprecated |
| `v0.4.0` | | Exact server translation of more JavaScript: rest elements and parameters, computed keys, optional calls, comma and `void`, `in`, `for…in`, labels, a fresh `let` binding per loop iteration, array `entries`/`keys`/`values`, `String.fromCharCode`/`fromCodePoint`/`raw`, `with`, `toSpliced`, `structuredClone`; error objects convert to text as in JavaScript and keep their kind through the `{#await}` payload. Runtime contract `ContractV2`: `ssr_runtime/v0.4.0` and `@govela/runtime@0.4.0` are required together |
| `v0.5.0` | | TypeScript, experimental (docs/TYPESCRIPT.md): `<script lang="ts">` and `.ts` modules compiled by erasure, also with `--ssr`; `vela check --types`, `vela --dts`, declarations in every npm package, TypeScript in `vela-lsp` and the new `@govela/typescript-plugin`. The runtime contract is unchanged |
| `v0.7.0` | | Component context and `#` imports: `setContext`, `getContext` and `hasContext` from `vela` pass values down the tree through slots, blocks, `lazy()` and `<vela:component>`, in the browser and in the Go renderer, with `VELA-CONTEXT-OUTSIDE-INIT`; a `$state.deep` object read from context is shared, also after hydration; `#` specifiers resolve through the `"imports"` of `package.json` (`VELA-SUBPATH-IMPORT`). Runtime contract `ContractV5`: `ssr_runtime/v0.7.0` and `@govela/runtime@0.7.0` are required together |
| `v0.6.0` | | Tooling around the compiler: the new npm packages `@govela/create` (`npm create @govela`: Vite, server-rendered and Vela App starters, TypeScript by default), `@govela/testing` (`render`, `fireEvent` for Vitest) and `@govela/eslint-plugin` (the script and template expressions of components through the new `vela document`); two files whose outputs are one file, also when their names differ only in case, are `VELA-OUTPUT-COLLISION`. The runtime contract is unchanged |

A published tag is never moved: the Go module proxy and checksum database
keep the first content of a version, and npm never reuses a version number.
A broken release is fixed by the next patch version, a `retract` directive in
the affected `go.mod` and `npm deprecate` on the npm version.

`v0.1.0` and `v0.2.0` mark earlier stages of the history; they have no Go
runtime tag, no npm package and no release binaries.

## The app layer

The app layer (docs/APP.md) is versioned on its own: the compiler's lockstep
does not apply to it. Its Go module, `Version` in `app/app.go`, the npm
packages and the `vela-app` binaries always carry the same version.

`@govela/app` runs `vela-app` the way `@govela/compiler` runs `vela`: its
launcher `bin/vela-app.js` runs the binary of the installed platform
package, an optional dependency of exactly its version, or
`$VELA_APP_BINARY`. The platform manifests are generated with `Version`
(`app/npm_package_test.go`); the binaries are not committed, the release
workflow builds them into `packages/app-*/bin/`.

A release of the app layer:

1. `cd app && go test ./...` and the app CI job pass. `app/go.mod` requires
   a tagged `ssr_runtime` with everything the layer imports, and has no
   `replace` line: `go install …@vX.Y.Z` refuses a module with one. When the
   layer needs runtime code that is not tagged yet, `app/go.mod` replaces the
   runtime with `../ssr_runtime` during development; release the runtime
   (the compiler steps above) first, then require it and drop the line.
2. Set `Version` in `app/app.go` and `version` in `packages/app/package.json`
   to `X.Y.Z`, and the versions of its `optionalDependencies`
   `@govela/app-*` too; regenerate the platform packages:
   `cd app && VELA_UPDATE_APP_PACKAGES=1 go test -run TestAppPackagesAreGenerated .`.
   `TestAppPackageRunsVelaApp` checks that they all agree.
3. If the layer needs a newer runtime, raise `peerDependencies` of
   `packages/app/package.json` and the `require` of `app/go.mod`; the
   release notes state the minimum `vela`, `ssr_runtime` and
   `@govela/runtime` versions.
4. `git tag app/vX.Y.Z` and `git push origin app/vX.Y.Z`. The workflow
   `.github/workflows/release-app.yml` checks that the tag matches
   `Version` and every manifest, tests the module outside the workspace
   (as `go install` builds it), builds `vela-app` for linux, darwin
   (amd64, arm64) and windows (amd64) with `SHA256SUMS`, publishes the
   GitHub release `app/vX.Y.Z`, puts the binaries into the platform
   packages and publishes them, then `@govela/app`, with provenance and the
   same `NPM_TOKEN`. A version already on npm is skipped, so a failed run
   can be repeated.

The tag pattern of the compiler's workflow, `v*`, does not match
`app/v*`, and each workflow checks only its own packages.

`@govela/create` (released with the compiler) installs the app layer of
`govela.app` in its manifest, the version of `app/app.go` when it was
generated. When a compiler release raises it to an app version that is not
published yet, `npm create @govela -- --app` makes projects whose
`npm install` fails (`ETARGET @govela/app`) until the app release is done:
release the app layer right after the compiler, or keep `govela.app` at a
published version.

`RuntimeContract` in `app/app.go` and `govela.runtimeContract` in
`packages/app/package.json` name the runtime contract the release needs.

### History of the app layer

| Tag | What it is |
| --- | --- |
| `app/v0.1.0`, `app/v0.1.1` | The first app layer (tasks 073–078) and `@govela/app@0.1.1`, the client router only; `vela-app` only through `go install` / `go run` |
| `app/v0.1.2` | `vela-app` in the GitHub release and in npm (`@govela/app` with the platform packages); `vela-app build` |
| `app/v0.3.0` | `getPage()` from the generated `#app/page.js` (`vela-app scaffold` adds `#app/*` to `package.json`); guards in `guard.go` before every loader and action, `app.Local`, `r.Cookie`/`r.SetCookie`, `Config.OnError`, `app.WriteRedirect`/`app.WriteError`, `X-Vela-Purpose` and `"cache": "once"` in protocol version 1; needs `vela`, `ssr_runtime` and `@govela/runtime` `0.7.0` (`ContractV5`) |
| `app/v0.2.0` | TypeScript Vela Apps: `"typescript": true`, `./$types` of every route from the Go loaders (`app/typegen`); `@govela/app` declarations need `@govela/runtime` `0.5.0` |

## Development

The committed `go.work` joins the compiler, the runtime module and the app
module, so tests use the runtime from the working tree. The compiler itself
does not import the runtime and builds without the workspace
(`GOWORK=off go build .`, checked in CI), which is how `go install` builds it.
