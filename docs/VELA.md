# Vela compiler reference

How the compiler is organised, what it emits, and how it is tested. For the
authoring syntax read [GUIDE.md](GUIDE.md); for feature status,
[FEATURES.md](FEATURES.md); for server rendering, [SSR.md](SSR.md).

## Repository layout

| Path | Contents |
| --- | --- |
| `*.go` (root, `package main`) | The compiler and the `vela` CLI |
| `internal/jsast/` | JavaScript lexer, parser (ES2025 modules), AST, printer, walker, early errors and AST validation |
| `internal/sfc/` | SFC split, template parser and template AST, template expression parsing |
| `internal/css/` | CSS tokenizer, parser, validation and scoping |
| `internal/diag/` | Diagnostic types (`Diagnostic`, `DiagSet`, JSON form) |
| `internal/srcspan/` | `SectionSpan`, byte ranges in component source |
| `runtime/` | Browser runtime: `runtime.js`, `runtime-blocks.js`, `runtime-hydrate.js` (embedded in the binary) |
| `packages/runtime/` | The npm package `@govela/runtime`, generated from `runtime/` (`runtime_package.go`) |
| `ssr_runtime/` | Go runtime of generated renderers, nested module `github.com/elauven/vela/ssr_runtime` |
| `examples/` | Example components, a dashboard, and the SSR server with hosts (`examples/ssr-host`) |
| `test_projects/`, `testdata/` | Compatibility corpus (applications written by AI models) and fixtures |
| `tools/` | Node harnesses for jsdom/Chromium tests, oracle generators, gate checks |
| `bench/` | Benchmark against SvelteKit on Node and Bun ([bench/README.md](../bench/README.md), [results](../bench/RESULTS.md)) |

`go.work` joins the compiler and `ssr_runtime/` for tests. The compiler
itself does not import the runtime and builds without the workspace
(`GOWORK=off go build .`), as `go install` does. Go 1.25 or later.

## Pipeline

```
.vela ─► parseSFC ─► <script>  ─► jsast parser ─► analyzeScriptAST ─► scope, reactivity,
            │                                       (state, props,     early errors and
            │                                        derived, effects)  Vela diagnostics
            ├──► <template> ─► parseTemplate (elements, blocks, directives,
            │                  cached expression ASTs)
            └──► <style>    ─► internal/css (scoped [vela-xxxxxx] rules)
                                   │
                       ┌───────────┴───────────┐
                       ▼                       ▼
            client: astgen + blocks_codegen   server (--ssr): SSR render plan
            → generated JsProgram             → server IR → Go AST → gofmt
            → astvalidate, rewriter, dce        (ssr_plan, ssr_ir, ssr_func,
            → printer (+ source map, minify)     ssr_module, ssr_go_*)
            → reparse check → Page.js          → page.go, page_props.go
```

- **One AST end to end.** Script and template expressions are parsed once;
  generated code is built as a JavaScript AST (never as strings), validated,
  printed, and reparsed before it is written (`VELA-INTERNAL-EMIT` if the
  printer produced invalid JavaScript).
- **Block engine.** Templates lower to fragment functions over a cursor
  (`$$c.element`, `$$c.text`, `$$c.static`, `$$c.block`, …). The same
  fragments create DOM on the client and adopt server DOM during hydration
  ([HYDRATION_ARCHITECTURE.md](HYDRATION_ARCHITECTURE.md)).
- **Server target.** The render plan is target-neutral. Server-evaluated
  JavaScript goes through the server IR and becomes Go with JavaScript
  semantics or a diagnostic ([SSR_TRANSLATION_CONTRACT.md](SSR_TRANSLATION_CONTRACT.md)).

### Main files

| File | Role |
| --- | --- |
| `main.go` | CLI, file and project builds, runtime staging, watch mode |
| `analyzer.go`, `scope.go`, `reactivity.go`, `unreachable.go`, `diagnostics.go` | Script analysis and diagnostic checks |
| `astgen.go`, `blocks_codegen.go`, `template_codegen_helpers.go` | Client code generation |
| `rewriter.go`, `dce.go`, `minifier.go`, `sourcemap.go` | Rewrites, dead-binding elimination, validation, output |
| `imports.go`, `package_imports.go`, `package_resolve.go` | `.vela`/runtime specifiers, `vela.manifest.json`, `--check-deps` |
| `ssr_project.go`, `ssr_roots.go` | SSR project build, root modes, manifest |
| `ssr_plan.go`, `ssr_region.go`, `ssr_expr.go`, `ssr_ir.go`, `ssr_func.go`, `ssr_module.go`, `ssr_init.go` | Server render plan and translation |
| `ssr_go_ast.go`, `ssr_go_renderer.go`, `ssr_go_blocks.go`, `html_nesting.go` | Go renderer generation, HTML parse stability |
| `ssr_host.go` | Host implementations (`//vela:host`) |
| `ssr_builtins.go`, `ssr_catalogue.go` | Built-in catalogue (matrix §2) |
| `runtime_package.go` | Version, `@govela/runtime` generator |

## Output

### Client module

```js
import { Counter } from './Counter.js';   // also the default export

const app = Counter(target, props, slots); // mounts into target
app.set({ step: 2 });                      // prop updates
app.destroy();                             // teardown, listeners removed
```

Each module imports the runtime contract marker (`$$runtimeContract1`); a
runtime from another compiler version fails at module load or bundle time.
Scoped CSS is injected by the module (`--css=inject`) or written next to it
(`--css=external`).

### Build products

| Build | Writes |
| --- | --- |
| `vela --out dist A.vela B.vela` | `A.js`, `B.js` and the runtime in `dist/` |
| `vela --project src --out dist` | the tree of `src/` as `.js`, copied `.js` modules, the runtime at the root, `vela.manifest.json` |
| `vela --ssr --project src --out out` | `out/client/` (the above), `out/server/` (Go), `out/styles/` |
| `--runtime-import=bare` | no runtime copy; modules import `@govela/runtime` |

## CLI

```
vela [flags] <file.vela>...     compile listed files into --out
vela --project <dir> [flags]    compile a source tree
```

| Flag | Default | Purpose |
| --- | --- | --- |
| `--out` | `dist` | Output directory |
| `--project` | | Source directory; mirrors the tree into `--out` |
| `--watch` | off | Recompile on change (no HMR) |
| `--mode` | `development` | `production` disables development checks in the runtime |
| `--minify` | off | Minify output and drop dead bindings |
| `--sourcemap` | off | External v3 source maps (not with `--minify`) |
| `--css` | `inject` | `inject` or `external` |
| `--runtime-import` | `relative` | `relative` (runtime copied) or `bare` (`@govela/runtime`) |
| `--check-deps` | off | Warn about package imports missing from `node_modules` |
| `--target` | `permissive` | Host globals profile: `permissive`, `browser`, `worker`, `node`, `ssr` |
| `--strict` | off | Proven unresolved reads are errors |
| `--unavailable-capabilities` | | Reject direct host capabilities, e.g. `fetch,indexedDB` |
| `--ssr` | off | Also generate Go renderers |
| `--ssr-module` | `vela/generated/server` | Go import root of generated packages (or `vela.ssr.json`) |
| `--ssr-report` | | Per-site SSR report (JSON) |
| `--ssr-scaffold-hosts` | off | Create stubs for missing hosts |
| `--hydration-verify` | `repair` | Development mismatch policy: `repair`, `fail`, `off` |
| `--ast`, `--emit-gen-ast` | off | Input and generated AST dumps |
| `--trace` | | `CompilationTraceV1` JSONL (`-` for stdout) |
| `--version` | | Print the compiler version |

## Testing

| Command | Covers |
| --- | --- |
| `go test ./... ./ssr_runtime/...` | Everything; jsdom and Chromium suites skip without Node, jsdom or Playwright |
| `node tools/run-ssr-browser-baseline.mjs` | Strict browser baseline: the required tests must run, a skip fails |
| `go test -run TestSSRGate .` | SSR beta gate over the corpus ([SSR_GATE.md](SSR_GATE.md)) |
| `VELA_GEN_SEEDS=300 go test -run TestBlocksGenerativeDifferential .` | Generative CSR/SSR/hydration differential test |
| `go test ./internal/jsast` | Parser, printer round trip, early errors ([ES_COMPLIANCE.md](ES_COMPLIANCE.md)) |

Setup for browser tests: `npm ci && npx playwright install chromium`.
Regenerating checked-in outputs: `VELA_UPDATE_RUNTIME_PACKAGE=1` (npm
package), `VELA_SSR_GATE_UPDATE=1` (gate expectations), `go generate .`
(`*_compat.go` aliases). The test inventory is in
[COMPILER_TEST_MATRIX.md](COMPILER_TEST_MATRIX.md).
