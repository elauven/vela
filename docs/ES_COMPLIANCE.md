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
