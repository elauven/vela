# Compiler pipeline test matrix

Status: updated 2026-10-11. This matrix links each stage of the client and
server compiler paths to its executable proof. A checkmark
means that focused tests exist; it does not claim an unavailable optional
runtime has run. Browser and jsdom suites deliberately skip when their external
dependencies are missing.

Unit tests of the layers under `internal/` live with their package: a file
named below is in the repository root or, for those layers, in
`internal/<package>/` (for example `internal/codegen/sourcemap_test.go`,
`internal/analyzer/scope_test.go`). End-to-end, browser, oracle and corpus
tests stay in the root package.

## Pipeline coverage

| Pipeline stage | Unit and invariant coverage | Corpus / generated-JS coverage | jsdom runtime coverage | Chromium coverage | Source-map coverage | Known gap / owner |
| --- | --- | --- | --- | --- | --- | --- |
| File, project, and runtime staging | `todo_corpus_test.go`, `runtime_staging_test.go`, `css_mode_test.go` cover `compileFile`, `buildProject`, copied JS modules, runtime placement, and CSS modes. | Positive AI todo/dashboard projects build through project mode. | Project fixtures exercise staged runtime and imports. | Same project fixtures load as ES modules. | `TestCompileFileEmitsSourceMap` covers file output. | Watch rebuild/remove path has no deterministic integration harness. Defer to Task 008. |
| SFC split | `TestParseSFC_AllSections`, `TestParseSFC_MissingTemplate`. | Every corpus component passes `parseSFC` before compile. | Indirect. | Indirect. | File offsets are consumed by map tests. | Section AST is still text-backed; outer ComponentAST belongs to Tasks 013 and 014. |
| Template parse and forms | `vela_test.go`, `codegen_blocks_test.go`, `diagnostics_*`, and `reactivity_test.go` cover elements, directives, blocks, slots, expressions, scopes, and positioned diagnostics. | `astgen_corpus_test.go`, `todo_corpus_test.go`. | Extensive `runtime_behaviour_test.go` and `runtime_nesting_matrix_test.go`. | Browser todo/dashboard and focused dynamic fixtures. | `sourcemap_test.go`, `browser_sourcemap_test.go` cover handler, each, key, await, slot, and nested origins. | Structural node spans, standalone AST validation, malformed-input fuzzing, and recovery policy are absent. Task 014. |
| CSS parse and scope | `TestParseCSS_*`, `TestScopeCSS_*`, `css_mode_test.go`. | Corpus external/injected CSS build. | Direct-child CSS selector runtime fixture. | Browser direct-child CSS selector fixture. | None; CSS maps are unsupported. | Best-effort parser has no diagnostics/spans or malformed CSS corpus/fuzzing. Task 014. |
| JavaScript parser and printer | `es_audit_test.go`, `scope_test.go`, `jsprinter_roundtrip_test.go`, `astjson_test.go`. | Generated JS is reparsed in fuzz/corpus checks; Node syntax checks run when available. | Indirect via compiled runtime. | Indirect via browser modules. | Printer location tests. | Public `ParseJS` discards parser diagnostics; script is reparsed. Task 013. |
| Semantic analysis and Vela diagnostics | `vela_test.go`, `reactivity_test.go`, `scope_test.go`, `diagnostics_dx_test.go`, `diagnostics_session_c_test.go`. | Positive and negative AI corpus fixtures assert actionable codes/positions. | Runtime fixtures prove reactive consequences. | Browser corpus catches integration mistakes. | Template positions are asserted. | Capability profiles and proven-call diagnostics are unimplemented by policy. Task 015 (optional). |
| Rewriters and optimization | `rewriter_test.go`, `dce_test.go`, reactivity/codegen regression cases. | Minify and corpus compilation execute current text transforms. | Runtime handler/deep-state/nested-each regressions. | Browser todo filters, bindings, persistence. | Source maps cover rewritten handlers; minify cannot combine with maps. | Rewriters/import/DCE still splice source and reparse. Task 013. |
| Target AST construction | `codegen_*_test.go`, `component_wrapper_test.go`, `scheduler_import_test.go`, `codegen_string_boundary_test.go`. | `astgen_corpus_test.go`, `astgen_diff_test.go`, and `FuzzCodegen` assert validated/reparsable generated JS. | `runtime_behaviour_test.go`, nesting matrix. | Browser runtime project corpus. | Generated/template origins covered. | Production uses a tracked structural-string gateway inventory and print/reparse nesting paths. Task 012. |
| Target AST validation | `astvalidate_test.go` exercises empty nodes, undeclared refs, TDZ, duplicates, and unreachable paths. | Every direct AST corpus fixture is validated; fuzz rechecks. | Indirect. | Indirect. | N/A. | Validation needs builder-local invariants and source-ownership rules. Task 012. |
| Runtime behavior | `runtime_contract_test.go`, `runtime_behaviour_test.go`, `runtime_nesting_matrix_test.go`, `runtime_*` regression tests. | Todo/dashboard fixture actions are shared through declarative steps. | Primary DOM/runtime proof; skips without Node/jsdom. | Playwright project corpus and focus/CSS scenarios. | Browser source-map suite separately validates thrown locations. | Broaden real-app corpus as features are added; no SSR/hydration coverage yet. Tasks 003 follow-on / 007. |
| Source maps | `sourcemap_test.go`, `jsprinter_roundtrip_test.go`. | Compile-file external maps plus nested tree origins. | N/A. | Five browser source-map mapping scenarios. | Primary proof. | CSS maps and transformed-script parse-once origins remain pending Tasks 014/013. |
| Trace observability | `compiler_trace_test.go` asserts stage order, failure cutoff, output invariance, aggregate metrics, and source-free JSON. | N/A. | N/A. | N/A. | N/A. | API exists; CLI JSONL sink and watch/parallel delivery are deferred. Task 016 (optional). |
| Block engine and hydration | `runtime_blocks_test.go`, `blocks_engine_test.go`, `runtime_contract_test.go`. | `TestBlocksEngineMatchesRecordedDOM` over recorded corpus DOM. | `TestRuntimeBlocksUnderJsdom`, `TestBlocksCorpusHydrates`. | `TestBlocksSSRMarkupParsesInChromium`, `TestBlocksGenerativeDifferentialInChromium`. | N/A. | — |
| SSR translation (server IR, functions, modules, built-ins) | `ssr_ir`, `ssr_func`, `ssr_module`, `ssr_builtins` tests; `ssr_runtime/...` unit tests. | Expression, init, value-text and checked-binding oracles against Node (`ssr_*_oracle_test.go`); `TestSSRExpressionDifferentialFuzz`. | Indirect. | Indirect. | N/A. | Rows still rejected: tasks 056, 057. |
| SSR project build and Go renderers | `ssr_project_test.go`, `ssr_go_ast_test.go`, `ssr_host_test.go`, `ssr_runtime_module_test.go`, `html_nesting_test.go`. | `TestBlocksSSRMarkupMatchesClientDOM`, `TestBlocksGenerativeDifferential` (CSR = SSR = hydrated DOM). | `ssr-blocks-check.mjs` cases. | `TestSSRHostImplementationEndToEnd`. | N/A. | — |
| SSR beta gate | `TestSSRGateComparison`, `TestSSRGateSummaryIsCurrent`. | `TestSSRGate`: real corpus through the CLI, generated Go and HTTP. | N/A. | `TestSSRGate` (hydration, CSR equality, clicks, teardown). | N/A. | Components needing props are checked with defaults. |
| Bundler delivery | `runtime_package_test.go`, `package_imports_test.go`. | N/A. | N/A. | `TestBundlerRecipeEsbuild`. | N/A. | — |

## Required suite commands

| Goal | Command | Environment condition |
| --- | --- | --- |
| Compiler, parser, AST, corpus, jsdom/browser tests registered in Go | `go test ./... ./ssr_runtime/... -count=1` | Node/jsdom and Playwright tests skip if unavailable. |
| Strict browser baseline (required tests, skips fail) | `node tools/run-ssr-browser-baseline.mjs` | Node, jsdom, Playwright Chromium. |
| SSR beta gate | `go test -run TestSSRGate -count=1 .` | Node, Playwright Chromium, Go. |
| Focused target-AST / structural gateway proof | `go test -run "Test(Validate|ASTGen|StructuralString)" -count=1 ./...` | None. |
| Source-map proof | `go test -run "Test(BrowserSourceMap|SourceMap)" -count=1 ./...` | Browser subset needs Playwright Chromium. |
| Browser application compatibility | `go test -run "TestBrowserRuntime" -count=1` | Node, Playwright package, Chromium. |
| Generator robustness | `go test -run "^$" -fuzz=FuzzCodegen -fuzztime=30s` | Run manually/CI time budget. |
| Task registry | `taskmd validate` | None. |

## Test policy for new compiler work

1. A parser/analyzer/rewriter change needs a focused negative test with
   component-relative diagnostics and a positive corpus or generated-AST case.
2. A target-codegen change needs a builder/validator test, corpus compilation,
   and a runtime fixture whenever it changes DOM order, cleanup, event
   dispatch, or reactivity.
3. A new source origin or reused user subtree needs both a source-map unit
   assertion and a browser stack-frame assertion when it can throw at runtime.
4. A structural/nested-block fix needs a declarative jsdom fixture first and a
   Chromium counterpart when browser behavior (focus, module loading, storage,
   CSS, event dispatch) is material.
5. Optional tool dependencies may cause a skip locally, but release/merge CI
   must run the corresponding environment rather than accepting the skip as
   proof.

## Open work

Post-beta translation slices are tasks 056 (exact-semantics constructs) and
057 (Date, Map/Set, `this`, array identity); the dev server and HMR are task
008; moving compiler layers out of `package main` is task 054.
