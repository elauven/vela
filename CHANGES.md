# Changes from baseline

This document summarizes the additions made on top of the original
upload.  Everything below is additive — no existing behavior was
removed, only extended (with one tiny deletion: a duplicated
`$effect` warning that moved to a better home).

## New files

| File                  | Lines | Purpose                                                                                   |
| --------------------- | -----:| ----------------------------------------------------------------------------------------- |
| `scope.go`            |  1193 | Scope graph and binding resolver; the substrate every other new pass uses.                |
| `unreachable.go`      |   225 | Statement-level flow analysis flagging code after `return`/`throw`/`break`/`continue`.    |
| `dce.go`              |   381 | Dead-binding elimination (string splicing, fixed-point, reactivity-aware).                |
| `reactivity.go`       |   485 | `$derived` purity, `$effect` dependency, and template-reference diagnostics.              |
| `scope_test.go`       |   362 | 27 tests for scope + unreachable.                                                         |
| `dce_test.go`         |   301 | 28 tests for DCE.                                                                         |
| `reactivity_test.go`  |   224 | 19 tests for the new reactivity diagnostics.                                              |

## Modified files

### `parser.go`
- `SFC` gained `ScriptOffset`, `StyleOffset`, `TemplateOffset` fields
  recording each section's absolute byte offset in the source file.
- `parseSFC` computes those offsets, accounting for the leading
  whitespace it trims off the inner text.

### `diagnostics.go`
- `DiagSet` gained `PushOffset`/`PopOffset` (a stack) so passes that
  emit positions relative to a section can be translated to
  file-relative line/column transparently.
- `validateScript`'s `assignment to non-existent state var` check was
  refined to fire only on *declared-but-not-reactive* names, ceding
  the truly-undeclared case to the scope analyzer to avoid double
  diagnostics.
- The `$state never updated` hint now points at the variable
  declaration instead of position 0 (script start).
- The `$effect with no reactive deps` warning was removed from here
  and rebuilt in `reactivity.go` with accurate source positions.

### `main.go`
- `compileFile` now runs, in order:
  1. `BuildScopes` (scope graph + undeclared/TDZ/const-write
     diagnostics)
  2. `DetectUnreachable` (flow analysis)
  3. `validateScript` (Vela-specific script checks; original)
  4. `validateDerivedPurity` (state writes inside `$derived`)
  5. `validateEffectBody` (`$effect` with no deps)
  6. `validateTemplate` (component import checks; original)
  7. `validateTemplateRefs` (template expressions reference
     undeclared names)
- All script passes are wrapped in
  `ds.PushOffset(sfc.ScriptOffset)`; template passes use the
  template offset.
- When `--minify` is on, `EliminateDeadCodeFixedPoint` runs between
  diagnostics and code generation.

### `vela_test.go`
- One test moved from calling `validateScript` to calling the new
  `validateEffectBody`, matching the warning's new home.

## What the new diagnostics catch

These all fire with accurate file-relative line/column positions:

| Diagnostic                                                          | Severity | Source             |
| ------------------------------------------------------------------- | -------- | ------------------ |
| Use of undeclared variable                                          | warning  | `scope.go`         |
| Assignment to undeclared variable (creates implicit global)         | warning  | `scope.go`         |
| TDZ — accessing `let`/`const`/`class` before its declaration        | error    | `scope.go`         |
| Assignment to `const`                                               | error    | `scope.go`         |
| Assignment to `import` binding                                      | error    | `scope.go`         |
| Redeclaration of `let`/`const`/`class`                              | error    | `scope.go`         |
| Code after `return`/`throw`/`break`/`continue`                      | warning  | `unreachable.go`   |
| `$derived` expression writes to a `$state` variable                 | error    | `reactivity.go`    |
| `$derived` expression writes to another binding                     | warning  | `reactivity.go`    |
| `$derived` expression contains `await`                              | warning  | `reactivity.go`    |
| `$effect` references no reactive variables                          | warning  | `reactivity.go`    |
| Template expression references undeclared name                      | warning  | `reactivity.go`    |
| `$state` variable never updated                                     | hint     | `diagnostics.go`   |

## What `--minify` now does

Beyond the existing whitespace minification, it also removes:

- Top-level `let`/`const` bindings with zero references and pure initializers
- Function declarations that are never referenced
- Cascading: if removing `f` makes `x` unreferenced, `x` goes too (fixed-point, capped at 8 rounds)

Protected from elimination:
- Anything `$state` / `$derived` / `$props` / `$bindable`
- Exported names
- Names appearing in the template
- Names starting with `_`
- Bindings with side-effectful initializers (calls, member access, etc.)
- `var` declarations and multi-declarator forms (conservative)

## Test counts

- **Baseline**: 200 tests
- **Final**: 291 tests, 0 failing
- New: 27 (scope/unreachable) + 28 (DCE) + 19 (reactivity) = 74 tests
- 17 baseline tests touched the validators that were extended; all
  still pass

## Known limitations (documented in code)

1. `let x = x;` — runtime TDZ that the static analyzer doesn't
   catch because it uses source position as a proxy for "is
   initialized."  V8 will throw `ReferenceError` at runtime.
2. Template position tracking is approximate — the warning lands at
   the right *file* and right *section* but the column within a
   complex expression may be slightly off because the template AST
   stores expression text as strings rather than parsed positions.
3. DCE doesn't touch `var` or multi-declarator forms (intentional
   conservatism; gains are small, risk is high).
4. DCE doesn't prune unused imports — a separate pass would handle
   this on top of the same `Binding.Refs` infrastructure.
