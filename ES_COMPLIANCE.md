# ECMAScript Compliance Report

This report is produced by `es_audit_test.go`, which exercises 87
language features across ES5 through ES2020.  Each row reports
whether ParseJS:

- **PASS** — parsed correctly *and* produced the expected AST shape
- **PARTIAL** — parsed without error but the AST representation is
  lossy (e.g. wrong Kind tag, missing flag)
- **FAIL** — returned nil or panicked

To re-run after changing the parser:

    go test -run TestES_Audit -v       # per-feature detail
    go test -run TestES_Summary -v     # grouped summary

## Overall: 87 pass, 0 partial, 0 fail

| Era    | Pass | Partial | Fail | Total |
|--------|-----:|--------:|-----:|------:|
| ES5    |   20 |       0 |    0 |    20 |
| ES2015 |   42 |       0 |    0 |    42 |
| ES2016 |    2 |       0 |    0 |     2 |
| ES2017 |    5 |       0 |    0 |     5 |
| ES2018 |    4 |       0 |    0 |     4 |
| ES2019 |    1 |       0 |    0 |     1 |
| ES2020 |   13 |       0 |    0 |    13 |

## Detail by era

### ES5

| Status | Feature | Note |
|--------|---------|------|
| ✅ PASS | var declaration | — |
| ✅ PASS | function declaration | — |
| ✅ PASS | function expression | — |
| ✅ PASS | named function expression | — |
| ✅ PASS | if/else/else-if | — |
| ✅ PASS | do-while | — |
| ✅ PASS | switch with fallthrough | — |
| ✅ PASS | try/catch/finally | — |
| ✅ PASS | throw | — |
| ✅ PASS | for loop, classic three-part | — |
| ✅ PASS | for-in | — |
| ✅ PASS | labeled statement + break label | — |
| ✅ PASS | regex literal | — |
| ✅ PASS | string with all common escapes | — |
| ✅ PASS | object literal getter/setter | — |
| ✅ PASS | trailing comma in object literal | — |
| ✅ PASS | trailing comma in array literal | — |
| ✅ PASS | comma operator | — |
| ✅ PASS | typeof / instanceof / in / void / delete | — |
| ✅ PASS | hex / octal-zero / decimal numbers | — |
### ES5

| Status | Feature | Note |
|--------|---------|------|
| ✅ PASS | let declaration | — |
| ✅ PASS | const declaration | — |
| ✅ PASS | arrow function — concise body | — |
| ✅ PASS | arrow function — block body, multiple params | — |
| ✅ PASS | arrow function — no params | — |
| ✅ PASS | arrow function — returns object literal | — |
| ✅ PASS | template literal — simple | — |
| ✅ PASS | template literal — with interpolation | — |
| ✅ PASS | tagged template literal | — |
| ✅ PASS | class declaration | — |
| ✅ PASS | class with extends | — |
| ✅ PASS | class with static method | — |
| ✅ PASS | class with getter/setter | — |
| ✅ PASS | destructuring — array | — |
| ✅ PASS | destructuring — array with rest | — |
| ✅ PASS | destructuring — object | — |
| ✅ PASS | destructuring — object with renaming | — |
| ✅ PASS | destructuring — with defaults | — |
| ✅ PASS | destructuring — nested | — |
| ✅ PASS | destructuring — in function params | — |
| ✅ PASS | default parameters | — |
| ✅ PASS | rest parameters | — |
| ✅ PASS | spread in array literal | — |
| ✅ PASS | spread in call | — |
| ✅ PASS | for-of | — |
| ✅ PASS | generator function | — |
| ✅ PASS | yield expression | — |
| ✅ PASS | yield delegating (yield*) | — |
| ✅ PASS | import — named | — |
| ✅ PASS | import — default | — |
| ✅ PASS | import — namespace | — |
| ✅ PASS | import — side effect only | — |
| ✅ PASS | import — mixed default+named | — |
| ✅ PASS | import — named with alias | — |
| ✅ PASS | export named | — |
| ✅ PASS | export default function | — |
| ✅ PASS | export const | — |
| ✅ PASS | computed property name | — |
| ✅ PASS | object shorthand | — |
| ✅ PASS | method shorthand | — |
| ✅ PASS | binary number literal | — |
| ✅ PASS | octal number literal (0o) | — |
### ES5

| Status | Feature | Note |
|--------|---------|------|
| ✅ PASS | exponentiation operator ** | — |
| ✅ PASS | exponentiation assignment **= | — |
### ES5

| Status | Feature | Note |
|--------|---------|------|
| ✅ PASS | async function declaration | — |
| ✅ PASS | async arrow function | — |
| ✅ PASS | await expression | — |
| ✅ PASS | trailing comma in function params | — |
| ✅ PASS | trailing comma in function call | — |
### ES5

| Status | Feature | Note |
|--------|---------|------|
| ✅ PASS | async generator | — |
| ✅ PASS | for-await-of | — |
| ✅ PASS | object spread | — |
| ✅ PASS | object rest in destructuring | — |
### ES5

| Status | Feature | Note |
|--------|---------|------|
| ✅ PASS | optional catch binding | — |
### ES5

| Status | Feature | Note |
|--------|---------|------|
| ✅ PASS | optional chaining — member | — |
| ✅ PASS | optional chaining — call | — |
| ✅ PASS | optional chaining — computed | — |
| ✅ PASS | nullish coalescing ?? | — |
| ✅ PASS | nullish coalescing assignment ??= | — |
| ✅ PASS | logical-or assignment ||= | — |
| ✅ PASS | logical-and assignment &&= | — |
| ✅ PASS | BigInt literal | — |
| ✅ PASS | dynamic import | — |
| ✅ PASS | import.meta | — |
| ✅ PASS | export * as | — |
| ✅ PASS | globalThis (identifier) | — |
| ✅ PASS | numeric separators | — |

## Notes

- The audit covers what the parser *accepts*.  It doesn't check
  whether the generated output runs correctly — that's separate.
- ES2021+ features (`String.replaceAll`, logical assignment, etc.) aren't
  in this report by design; the brief asked for ES2020 baseline.
  Most of them are runtime-library additions and don't affect parsing.
