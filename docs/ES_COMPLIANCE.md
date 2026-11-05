# ECMAScript Compliance Report

The JavaScript layer (`internal/jsast`) is checked by a compatibility suite of
467 cases (task 055). Every case goes through the whole pipeline the compiler
uses, not only the parser:

- **Valid programs** must parse without diagnostics, satisfy the tree
  invariants (positions inside their parent, no shared nodes, no placeholder
  nodes), be fully visited by `WalkNode`, and print back, in pretty and in
  minified mode, to source that parses to the same tree
  (`TestPrinterRoundTripsCompatCorpus`).
- **Invalid programs** must be rejected with a positioned diagnostic, either
  by the parser or by `EarlyErrors`, the early-error pass the compiler runs on
  component scripts, template expressions and emitted modules.

Re-run with:

    go test ./internal/jsast -run 'TestCompat|TestPrinterRoundTrip|TestEarlyErrors' -v

The previous report (87/87, ES5–ES2020) checked parsing only. It did not
notice that class expressions, tagged templates, private members,
`for await`, Unicode identifiers and some regex literals were printed wrong,
which produced broken JavaScript without a diagnostic.

## Summary (2026-10-08)

| Group | Pass | Open | Total |
| --- | ---: | ---: | ---: |
| Errors | 101 | 2 | 103 |
| ES5 | 57 | 0 | 57 |
| ES2015 | 88 | 0 | 88 |
| ES2016 | 3 | 0 | 3 |
| ES2017 | 16 | 0 | 16 |
| ES2018 | 5 | 0 | 5 |
| ES2019 | 2 | 0 | 2 |
| ES2020 | 12 | 0 | 12 |
| ES2021 | 4 | 0 | 4 |
| ES2022 | 13 | 0 | 13 |
| ES2023 | 1 | 0 | 1 |
| ES2024 | 1 | 0 | 1 |
| ES2025 | 4 | 0 | 4 |
| Lexical | 23 | 0 | 23 |
| ASI | 13 | 0 | 13 |
| Proposals | 0 | 7 | 7 |
| Legacy | 2 | 3 | 5 |
| Positions | 40 | 0 | 40 |
| RealWorld | 5 | 0 | 5 |
| Lexer | 65 | 0 | 65 |
| **Total** | **455** | **12** | **467** |

## Open cases

None of them compiles silently: each one either fails with a diagnostic or
is reported by another compiler pass.

| Case | Status |
| --- | --- |
| Duplicate `let` | Reported by the compiler's scope analysis (`VELA-SCOPE-DUPLICATE`), not by the JavaScript layer. |
| `return` outside a function | Not reported: a component script is compiled into a function. |
| Decorators, `accessor` members | Rejected: `@` is an illegal character. |
| `using` / `await using` | Rejected with a syntax diagnostic. |
| `import defer` / `import source` | Rejected with a syntax diagnostic. |
| HTML-like comments (Annex B) | Not recognised; component scripts are modules. |
| `await` / `yield` as identifiers | Always operators; component scripts are modules. |

## TypeScript

With `ParseOptions{TypeScript: true}` (task 080, docs/TYPESCRIPT.md TS-2,
TS-3) the parser accepts the erasable TypeScript syntax and erases it
while it parses: the tree is ordinary JavaScript, and what the types said
is kept beside it (`JsProgram.TS`, the `TS` fields of functions, classes and
members, `JsVarDeclarator.Type`). JavaScript parsing is unchanged: every
TypeScript rule runs only in that mode, and the JavaScript corpus parses
to the same trees with it on.

| Check | Size | Test |
| --- | ---: | --- |
| TypeScript corpus: valid sources erase to the tree of their JavaScript twin, print, and reparse as JavaScript | 182 | `TestTSCorpusValid` |
| Rejected constructs (TS-2.4) and syntax errors are positioned diagnostics | 20 | `TestTSCorpusInvalid` |
| The JavaScript corpus reads the same with TypeScript on | all | `TestTSAcceptsJavaScriptCorpus` |
| Oracle: `ts.transpileModule` (TypeScript 6.0.3, `verbatimModuleSyntax`) erases to the same tree | 1700 sources | `TestTSOracleTranspileModule` |
| The type grammar over declaration files (`lib.dom.d.ts`, `typescript.d.ts`, …) | 11 323 files | `TestTSDeclarationFilesParse` |
| Mutated and truncated sources, pathological shapes, `FuzzTSParse` | 8 000 mutations | `TestTSMutatedInputNeverCrashes`, `TestTSPathologicalShapes` |

The oracle compares the corpus and every `.ts` file under `node_modules`
and `packages/*/node_modules`; `VELA_TS_ORACLE_DIR` adds directories (a
path list). The 1700 sources and 11 323 declaration files above are the
repository plus the third-party packages of another project on the
development machine (tiptap, TanStack Query, inlang, MSAL, kysely). The
oracle needs node and `node_modules/typescript` and is skipped without
them. 74 of those sources use `enum` or other TS-2.4 constructs and are
rejected on purpose.

Speculative parsing (`f<T>(x)` against `a < b > (c)`, `(a): T => a`,
generic arrows) keeps what succeeds and remembers by position what fails,
so `a < a < a …` stays linear (20 000 comparisons in milliseconds).

Re-run with:

    go test ./internal/jsast -run 'TestTS' -v
    VELA_TS_ORACLE_DIR=/path/to/projects go test ./internal/jsast -run TestTSOracle -v

## Component scripts

A component script is checked as the body of its component factory:

- `await` is valid only inside `async` functions (the factory is not async);
  top-level `await` is reported.
- The only export form is a prop, `export let name = value` or
  `export let name = $bindable(value)`. Other exports (`export default`, export
  lists, re-exports, `export const` / `function` / `class`) are reported with
  `VELA-SCRIPT-EXPORT`; move them to a `.js` module.

Server rendering translates a subset of this syntax; see
`docs/SSR_TRANSLATION_MATRIX.md`. Constructs outside it (classes, tagged
templates, dynamic `import()`, `import.meta`) get the regular unsupported
outcome with a host or `{#client}` fix.
