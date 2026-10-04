# Compiler pipeline test matrix

Status: operational baseline for Task 011, updated 2026-08-23. This matrix
links the actual client compiler path to its executable proof. A checkmark
means that focused tests exist; it does not claim an unavailable optional
runtime has run. Browser and jsdom suites deliberately skip when their external
dependencies are missing.

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

## Required suite commands

| Goal | Command | Environment condition |
| --- | --- | --- |
| Compiler, parser, AST, corpus, jsdom/browser tests registered in Go | `go test ./... -count=1` | Node/jsdom and Playwright tests skip if unavailable. |
| Focused target-AST / structural gateway proof | `go test -run "Test(Validate|ASTGen|StructuralString)" -count=1` | None. |
| Source-map proof | `go test -run "Test(BrowserSourceMap|SourceMap)" -count=1` | Browser subset needs Playwright Chromium. |
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

## Remediation order

1. Task 012 eliminates structural executable-JS string construction and
   print/reparse lowering.
2. Task 013 introduces parse-once ScriptAST and AST-native transforms.
3. Task 014 completes template/CSS AST spans, diagnostics, and validation.
4. Only then may Tasks 007 (SSR/hydration) and 008 (HMR/dev server) add a
   second backend or persistent incremental state.
5. Task 015 target profiles and Task 016 CLI trace delivery are useful but
   non-blocking enhancements after their dependencies.
