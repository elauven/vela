# SSR JavaScript-to-Go translation matrix

Status: maintained by hand, 2026-10-03. Normative rules live in
[SSR_TRANSLATION_CONTRACT.md](SSR_TRANSLATION_CONTRACT.md); this matrix is the
per-construct reference that applies them. When a row and the contract
disagree, the contract wins and the row is a bug.

## How to read and maintain this file

**Status**

| Status | Meaning |
| --- | --- |
| `implemented` | Meets the contract and has tracked oracle rows (V-11.1b). |
| `partial` | Exists, but is narrower than the contract or diverges from it. The Notes column or §4 says how. |
| `planned (Sn)` | Decided, not implemented; `Sn` is the delivery slice in contract §13. |
| `never` | Deliberately not translated natively. The Outcome column gives the route. |
| `undecided` | Listed in §5; needs a product decision before its slice. |

**Columns**

- *Outcome*: `native` (server-native), `host` (developer Go, contract §8),
  `client` (`{#client}` block or client-only component, §9), `rejected`
  (build error with diagnostic), `excluded` (not part of the server region).
- *Typed* / *Dynamic*: lowering in the typed and dynamic tiers (T-5.4).
  Helper names for `planned` rows are indicative, not an API commitment.
- *Oracles*: tracked oracle group. For `implemented` rows it must exist; for
  `planned` rows it names the group to add with the implementation. Existing
  groups: `value-text` (`testdata/ssr_value_text_oracle.json`), the expression
  groups `ops-*`, `member-*`, `strings-length`, `arrays-spread`,
  `objects-spread`, and `number-text` (`testdata/ssr_expression_oracle.json`,
  run in both tiers by `TestSSRExpressionOracleBothTiers`), `init-order` and
  `objects-order` (`testdata/ssr_init_oracle.json`, `TestSSRInitOracle`), `attribute`
  (`testdata/ssr_attribute_oracle.json`), `checked-binding`
  (`testdata/ssr_checked_binding_oracle.json`). `go-tests` means behaviour is
  covered only by Go tests, which does not satisfy V-11.1b.

**Maintenance rules**

1. A change to the translator or the Go runtime that alters a row's
   behaviour updates the row in the same commit.
2. A row becomes `implemented` only together with its oracle rows, in both
   tiers where both exist.
3. At slice S5 the built-in rows (§2) move into the Go catalogue table
   (V-11.1a). This file is then generated from it, and a test fails when the
   committed file differs from the generated one.

---

## 1. Language constructs

### 1.1 Values and identifiers

| Construct | Outcome | Typed | Dynamic | Rule | Oracles | Status | Notes |
| --- | --- | --- | --- | --- | --- | --- | --- |
| string literal | native | Go string literal | same | P-5 | value-text | implemented | |
| number literal | native | `float64` constant | same | T-5.3a | number-text | implemented | Spelled as a Go float constant (`0.0`, `255.0` for `0xff`), so it is a `float64` also in `any` contexts. |
| `true` / `false` | native | `bool` | same | — | value-text | implemented | |
| `null` | native | `nil` | same | T-5.3 | value-text | implemented | |
| `undefined` | native | `vela.Undefined()` | same | T-5.3 | value-text | implemented | |
| prop identifier | native | `props.<Field>` | same | T-5.2 | value-text | implemented | |
| `$state` / `$state.deep` / `$bindable` read | native | Go local initialised once | same | T-6.6d | init-order | implemented | Evaluated once in declaration order into `ssrL_<name>`; `$state(prop)` renders, and the block engine's payload carries the computed values (`testdata/ssr_init_oracle.json`). Restoring them on the client is task 041. |
| `$derived` read | native | Go local initialised once, in declaration order | same | T-6.6d | init-order | implemented | As above. A read of a binding declared later is rejected (ReferenceError in JavaScript). |
| top-level `const` / unchanged `let` | native | Go local | same | T-2.1 | init-order | implemented | As above; recorded as `binding` sites in `--ssr-report`. |
| `#each` / `#await` binding | native | lexical Go variable | same | — | value-text | implemented | |
| symbol imported from local `.js` | native or host | generated module package `server/modules/<path>`: `New(ssrJS, ctx)` per render, a `Module` field per export | same | T-7.3a, T-7.3b | `TestSSRModulesMatchNode`, `TestSSRModuleDiagnostics` | implemented | Named imports only. Top-level code may only declare functions and `const` bindings; `let`, other statements, and circular imports make the module's symbols unsupported (host fix suggested). Module values are created per render, never shared. |
| symbol imported from npm | host or client | — | — | T-7.3c | — | never | |
| browser global (`window`, `document`, …) | client | — | — | §3 | — | never | |

### 1.2 Member access

| Construct | Outcome | Typed | Dynamic | Rule | Oracles | Status | Notes |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `obj.name` | native | struct field by JSON name; `*vela.Object` lookup | `ssrJS.Get` | T-5.1a, T-6.5a/b | member-get | implemented | Go structs and maps are read through their JSON view (`json` tags, `omitempty`, `-`, embedded fields, Marshalers). A `null` / `undefined` receiver throws `TypeError` (`*vela.RenderError`). |
| `obj[key]` | native | slice index / field / object lookup | `ssrJS.Index` | T-6.5b | member-computed | implemented | Any receiver and key, with JavaScript property-key conversion. |
| `.length` of string | native | UTF-16 length | `ssrJS.Length` | T-6.3a | strings-length | implemented | `null.length` throws `TypeError`; an object without `length` yields `undefined`. |
| `.length` of array | native | `len(x)` | `ssrJS.Length` | — | strings-length | implemented | |
| `a?.b`, `a?.[k]`, `f?.()` | native | nil/undefined check, then access | `vela.OptionalChain` with lazy rest | T-6.1b | ops-optional | partial | `a?.b` and `a?.[k]` implemented; the rest of the chain is not evaluated after a nullish link. `f?.()` is rejected until calls of values exist (S4). |

### 1.3 Operators

| Construct | Outcome | Typed | Dynamic | Rule | Oracles | Status | Notes |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `+` | native | `float64 +` or string concatenation | `vela.Add` | §6.2 | ops-add | implemented | |
| `-`, `*`, `/`, `%` | native | `float64` ops; `%` as `math.Mod` | `vela.NumberSubtract` … (ToNumber) | §6.2 | ops-arith | implemented | |
| `**` | native | `vela.Pow` | `vela.Pow` | §6.2 | ops-pow | implemented | `1 ** Infinity` and `x ** NaN` are `NaN`, unlike `math.Pow`. |
| unary `-`, `+` | native | negate / `ToNumber` | `vela.NumberNegate`, `vela.Number` | §6.2 | ops-unary | implemented | |
| `!` | native | `!vela.Truthy(x)` | same | §6.2 | ops-unary, value-text | implemented | |
| `typeof` | native | `vela.TypeOf` | `vela.TypeOf` | §6.2 | ops-typeof | implemented | Go values follow the JSON view (`time.Time` is `"string"`). |
| `<`, `<=`, `>`, `>=` | native | `float64` compare; `vela.StringLess` (UTF-16) | `vela.Less` … | §6.2, T-6.3a | ops-relational | implemented | |
| `===`, `!==` | native | `vela.StrictEqual` | same | §6.2 | ops-strict-eq | implemented | |
| `x == null`, `x != null` | native | nil-or-undefined check | `vela.LooseNull` | §6.2 | ops-loose-null | implemented | Also `== undefined`. |
| other `==`, `!=` | rejected | — | — | §6.2 | neg-loose-eq | never | Diagnostic suggests `===`. Current behaviour already matches. |
| `&&`, `\|\|` | native | `vela.AndLazy` / `vela.OrLazy` | same | T-6.1b (R1) | ops-logical | implemented | |
| `??` | native | lazy nullish check | `vela.NullishLazy` | T-6.1b | ops-nullish | implemented | |
| `c ? a : b` | native | `vela.TernaryLazy` | same | T-6.1b (R1) | ops-conditional | implemented | |
| `=`, `+=`, `++`, … on local bindings | native | Go assignment | same | T-6.6b | stmt-assign | planned (S4) | Only inside translated functions. |
| assignment to state, props, module bindings | rejected | — | — | T-6.6a | go-tests | partial | `VELA-SSR-IMPURE` (S1) for assignments and `++` / `--`; covered by a Go test, not an oracle group. |
| comma, `void`, `delete`, `in`, `instanceof` | rejected | — | — | §6.2 | — | never | v1. |

### 1.4 Compound values and functions

| Construct | Outcome | Typed | Dynamic | Rule | Oracles | Status | Notes |
| --- | --- | --- | --- | --- | --- | --- | --- |
| template literal | native | `vela.TemplateString` | same | §6.2 | value-text | implemented | |
| tagged template | rejected | — | — | §6.2 | — | never | v1. |
| array literal | native | `[]any{…}` with holes | same | §6.2 | value-text (`array-hole`) | implemented | |
| array spread `[...a]` | native | `vela.JoinArrays` + `ssrJS.SpreadItems` | same | §6.2 | arrays-spread | implemented | Strings spread by code point; spreading `null` throws `TypeError`. |
| object literal | native | `*vela.Object` | `*vela.Object` | T-5.1c | objects-order | implemented | Insertion order with JavaScript key ordering, observed through the payload (`testdata/ssr_init_oracle.json`). A closed-shape struct lowering is an optimisation not done yet. |
| object spread `{...o}` | native | `(*vela.Object).Spread` | same | T-5.1c | objects-spread | implemented | |
| object methods, getters/setters | rejected | — | — | §6.2 | — | never | v1. |
| destructuring (declarations, parameters, defaults) | native | Go locals | same | §7.1 | stmt-destructure | implemented | Rest elements and computed keys are rejected for now. |
| arrow function as local value, argument, or callback prop | native | `vela.Func` closure | same | §7.1 | fn-closures | implemented | A function value created in a template defers writes to state, props, or module bindings to a run-time `VELA-SSR-IMPURE` error, raised only if the render calls it (an event callback passed as a prop is never called). |
| arrow function as built-in callback | native | `vela.Func` closure called by the built-in | same | T-7.2b | arr-callbacks | implemented | Callback parameters are dynamic; typed element parameters come with the typed variants (T-7.2a). |
| call to local function with one `return` | native | inlined expression | same | T-7.1a | fn-inline | partial | Existing inliner; becomes an optimisation of the next row. |
| call to local multi-statement function | native | `vela.Func` closure `ssrF_<name>` | same | T-7.1a | fn-statements | implemented | Also `let`/`const` arrow bindings and exports of local modules. |
| monomorphised variants | native | `ssrF_<name>__<types>`: parameters of proven type are typed Go locals when the body never assigns them; a result type every `return` proves types the call | one dynamic variant above 4 | T-7.2a | oracle typed tier | implemented | Threshold 4 (decision 1). |
| recursion | native | Go recursion | same | T-7.1b | fn-recursion | implemented | |
| call bound by `//vela:host` | host | `vela.CallHost` (reflection; §8.4 conversions) | same | §8 | host-e2e | partial | Functions of components and of local `.js` modules, and parameterless hosts for module values (§8.7). Covered by Go end-to-end tests (`TestSSRHost*`); the host fixture tooling of V-11.3e is not done. |
| `new Error(m)`, `new TypeError(m)`, `new RangeError(m)` | native | `vela.NewError(kind, m)` | same | §6.2 | errors-new | implemented | |
| other `new`, `this`, `class` | rejected | — | — | §6.2 | — | never | v1. |
| regular expression literal | host | — | — | §3 | — | never | See §3. |

### 1.5 Statements

Statements are admitted inside component functions with more than one
statement, translated as Go closures `ssrF_<name>` of type `vela.Func`
(ssr_func.go, task 045). Oracle groups: `fn-statements`, `fn-try`,
`fn-closures`, `fn-recursion`, `stmt-destructure`, `stmt-assign`,
`errors-new`. Each function has one dynamic-tier variant;
monomorphisation (T-7.2a) is not done yet. Status: `implemented` unless
the notes say otherwise.

| Construct | Lowering | Rule | Notes |
| --- | --- | --- | --- |
| `const`, `let`, block scoping | Go locals in nested blocks | §7.1 | Shadowing is renamed deterministically. |
| `if` / `else` | Go `if` on `vela.Truthy` | §7.1 | |
| `for (;;)` | Go `for` | §7.1 | One Go variable per loop: a function created in the body that captures a `let` loop variable is rejected (per-iteration copies are not generated yet). |
| `for…of` over arrays | Go `range` | §7.1 | Holes are visited as `undefined`. |
| `for…of` over strings | iterate code points | §7.1, T-6.3a | Not UTF-16 units. |
| `while`, `do…while` | Go `for` | §7.1 | |
| `break`, `continue` (no labels) | Go `break` / `continue` | §7.1 | |
| `return` | Go `return` | §7.1 | |
| `throw` | `ssrJS.Throw` records `*vela.Thrown`; uncaught, it is a `*vela.RenderError` of the error's name | T-6.5a | `new Error`, `TypeError`, `RangeError` create error objects. |
| `try` / `catch` / `finally` | `vela.Try` with closures completing `(value, returned)` | T-6.5d | `return`/`throw` in `finally` overrides. Host errors are caught as `Error("Internal error")` unless wrapped in `vela.PublicError` (T-7.4h). `break`/`continue` across a `try` is rejected. Go panics are not caught. |
| `switch` | Go `switch` with `vela.StrictEqual` cases; fall-through preserved | §7.1 | |
| nested function declaration | Go closure | §7.1 | Hoisted within its block; arrow and function expressions are `vela.Func` values; recursion is supported. |
| destructuring `const {a, b = 1} = o`, `const [x, y] = a` | `ssrJS.Get` / `ssrJS.Index` | §7.1 | Rest elements and computed keys are rejected for now. |
| assignment to locals and to members of local objects; compound operators, `++`, `--` | Go assignment, `ssrJS.SetMember` | T-6.6b | Writes to state, props, or module bindings are impure (T-6.6a). Growing an array by index assignment throws until arrays get identity (task 046). |
| `var` declarations | rejected | §7.1 | Use `let` or `const`. |
| labels, `for…in`, generators, `for await`, `with`, `eval`, `arguments`, cross-block `var` hoisting | rejected | §7.1 | `never` in v1. |

### 1.6 Asynchronous code

| Construct | Outcome | Lowering | Rule | Oracles | Status |
| --- | --- | --- | --- | --- | --- |
| `async function` / async arrow | native | `vela.Func` closure whose body runs to completion and settles a `*vela.Promise` (`ssrJS.Async`) | T-7.4a | `TestSSRAsyncMatchesNode` | implemented |
| `await e` | native | `ssrJS.Await(ctx, e)`: settled value or the rejection thrown; a cancelled `ctx` stops the render uncatchably (T-7.4g) | T-7.4a | `TestSSRAsyncMatchesNode`, `TestAwaitCancelledIsNotCaught` | implemented |
| async call without `await` | native | settled `vela.Promise` (text `[object Promise]`; in `$state` it is omitted from the payload and recomputed by the client) | T-7.4b | async (oracle), `TestPromiseStateIsOmitted` | implemented |
| `.then`, `.catch`, `.finally`, `Promise.resolve`, `reject`, `all`, `allSettled` | native | `ssrJS.Then` / `CatchPromise` / `Finally`, `vela.Resolve` / `Reject`, `ssrJS.All` / `AllSettled`, in array order | T-7.4b | `TestSSRAsyncMatchesNode`, `TestPromiseChain`, `TestPromiseAllOrder` | implemented |
| `{#await}` with server-evaluated operand | native or host | `vela.AwaitSettle`: renders `then`/`catch` with any JSON value, payload snapshot adopted by the client without re-evaluating the operand; `await` sites in `--ssr-report` | T-7.4f | `TestSSRAsyncMatchesNode`, `Loader` hydration cases | implemented |
| `{#await}` whose operand cannot run on the server | client | wrap the block in `{#client}`; server renders the boundary fallback | T-7.4f, T-9.1b | await-client | planned (S6) |
| `{#await}` with a caller-supplied `RenderOptions.Await` snapshot | native | render the supplied branch with any JSON `Value`/`Error`; operand not evaluated | T-7.4f | await-snapshot (`Multi then`, `Multi catch`) | implemented (block engine; the legacy renderer keeps string snapshots) |
| top-level `await` in component script | rejected | — | T-7.4d | — | never |

### 1.7 Region boundaries

| Construct | Outcome | Rule | Status | Notes |
| --- | --- | --- | --- | --- |
| event handlers, `$effect`, `onMount`, `onDestroy`, binding write-back | excluded | T-2.2 | planned (S1) | Currently diagnosed through the static event bridge (`VELA-SSR-EVENT-REACTIVE`). |
| `{#client}` main branch | excluded | T-9.1c | planned (S6) | Fallback branch is region code. |
| client-only component (`<script context="client">`) | excluded | T-9.2 | planned (S6) | Its props and slot content are excluded from the parent region. |

---

## 2. Built-ins

Generated from `ssr_catalogue.go` (V-11.1a); regenerate with
`VELA_UPDATE_MATRIX=1 go test -run TestSSRMatrixBuiltins .`. The
*Divergence* column lists the known places where the obvious Go call
differs from JavaScript; each has rows in the listed oracle groups of
`testdata/ssr_expression_oracle.json`. Examples marked ✓ were checked
against Node and Go on 2026-10-03.

### 2.1 Conversions and global functions

| Built-in | Go helper | Rule | Divergence / required edge cases | Oracles | Status |
| --- | --- | --- | --- | --- | --- |
| `String(x)` | `vela.ToStringValue` | T-6.4a | `String()` is `""`; arrays join; `-0` is `"0"`. | `conv-call` | implemented |
| `Number(x)` | `vela.ToNumber` | §6.2 | `Number("")` is `0`, `Number(" 12 ")` is `12`, `Number("0x1f")` is `31`, `Number("1_0")` is `NaN` ✓; `strconv.ParseFloat` differs on all four. | `conv-call` | implemented |
| `Boolean(x)` | `vela.ToBoolean` | §6.2 |  | `conv-call` | implemented |
| `parseInt(s, radix)` | `vela.ParseInt` | — | Parses a prefix: `parseInt("12px")` is `12` ✓, while `strconv.ParseInt` fails; `0x` prefix with radix 16 or absent; radix outside 2–36 is `NaN`. | `parseInt` | implemented |
| `parseFloat(s)` | `vela.ParseFloat` | — | Prefix parsing; `Infinity` literal. | `parseFloat` | implemented |
| `isNaN`, `isFinite` | `vela.IsNaN`, `vela.IsFinite` | — | Coerce with `ToNumber` first. | `isnan` | implemented |
| `Number.isNaN`, `Number.isFinite`, `Number.isInteger`, `Number.isSafeInteger` | `vela.Number…` | — | No coercion. | `isnan` | implemented |
| `encodeURIComponent`, `encodeURI` | `ssrJS.EncodeURIComponent`, `ssrJS.EncodeURI` | T-6.5a | JS keeps `!*'()` unescaped and encodes space as `%20` ✓; `url.QueryEscape` escapes them and writes `+`, `url.PathEscape` escapes them too. A lone surrogate throws `URIError`. | `uri` | implemented |
| `decodeURIComponent`, `decodeURI` | `ssrJS.DecodeURI…` | T-6.5a | Malformed input throws `URIError`; `decodeURI` keeps escapes of reserved characters. | `uri` | implemented |
| global functions as values (`items.filter(Boolean)`) | `ssrJS.GlobalFunc` | §7.1 |  | `global-values` | implemented |
| `NaN`, `Infinity` | `vela.ToNumber("NaN")` | §6.2 |  | `math-const` | implemented |
| `Array.isArray` | `vela.IsArray` | — |  | `isarray` | implemented |
| `console.log`, `info`, `warn`, `error`, `debug` | `ssrJS.Console` | T-6.6a | Calls go to the observer of `vela.WithConsoleObserver(ctx, f)`; without one they do nothing (decision of §5). | `console` | implemented |

### 2.2 String methods

Method calls dispatch on the receiver at run time (`ssrJS.Method`), so the methods shared by strings and arrays (`at`, `includes`, `indexOf`, `lastIndexOf`, `slice`, `concat`) need no type proof.

| Built-in | Go helper | Rule | Divergence / required edge cases | Oracles | Status |
| --- | --- | --- | --- | --- | --- |
| `length` | `vela.Length` | T-6.3a | UTF-16: `"😀".length` is `2` ✓. | `strings-length` | implemented |
| `at`, `charAt`, `charCodeAt`, `codePointAt` | `ssrJS.Method` | T-6.3a | UTF-16 indices; negative index for `at`. | `str-index` | implemented |
| `includes`, `startsWith`, `endsWith`, `indexOf`, `lastIndexOf` | `ssrJS.Method` | T-6.3a | Positions in UTF-16 units; `position` argument clamping. | `str-search` | implemented |
| `slice`, `substring` | `ssrJS.Method` | T-6.3a | `substring` swaps reversed arguments; `slice` accepts negatives; cutting a pair yields a lone surrogate (T-6.3b), kept in WTF-8 so that joining the halves gives the pair back. | `str-slice` | implemented |
| `toUpperCase`, `toLowerCase` | `ssrJS.Method` (`casing_table.go`) | T-6.3c | Full SpecialCasing from a table generated with Node (`tools/gen-casing-table.mjs`): `"straße".toUpperCase()` is `"STRASSE"` ✓ (Go `STRAßE`); context-sensitive final sigma: `"ΣΑΣ".toLowerCase()` is `"σας"` ✓ (Go `σασ`). | `str-case` | implemented |
| `trim`, `trimStart`, `trimEnd` | `ssrJS.Method` | — | JS whitespace set: U+FEFF is trimmed by JS but not by `strings.TrimSpace` ✓. | `str-trim` | implemented |
| `padStart`, `padEnd` | `ssrJS.Method` | T-6.3a | Target length in UTF-16 units; pad string truncated. | `str-pad` | implemented |
| `repeat` | `ssrJS.Method` | T-6.5a | Negative or infinite count throws `RangeError`. | `str-repeat` | implemented |
| `split(separator, limit)` with a string separator | `ssrJS.Method` | T-6.3a | `split("")` splits into UTF-16 units and breaks surrogate pairs: `"😀".split("").length` is `2` ✓. | `str-split` | implemented |
| `replace(string, string)`, `replaceAll(string, string)` | `ssrJS.Method` | — | `replace` changes the first match only; replacement patterns `$&`, `$$`, `` $` ``, `$'` apply even with a string pattern: `"a".replace("a", "$&$&")` is `"aa"` ✓. | `str-replace` | implemented |
| `replace` / `replaceAll` with a callback | `ssrJS.Method` | T-7.2b | Callback receives match, offset, string. | `str-replace` | implemented |
| `concat`, `toString`, `valueOf` | `ssrJS.Method` | — |  | `str-concat`, `num-tostring` | implemented |
| spread / `for…of` over a string | code-point iteration | T-6.3a | `[..."😀"].length` is `1` ✓. | `arrays-spread` | implemented |
| `normalize(form)` | `ssrJS.Method` (`golang.org/x/text/unicode/norm`) | T-6.5a | NFC, NFD, NFKC, NFKD; another form throws `RangeError`. Unicode 17 tables, as in Node 24 (decision of §5). | `str-normalize` | implemented |
| `localeCompare`, `toLocaleUpperCase`, `toLocaleLowerCase` | host | §3 | Locale-dependent. | — | never |
| `match`, `matchAll`, `search`, `replace`/`split` with a RegExp | host | §3 |  | — | never |

### 2.3 Array methods

| Built-in | Go helper | Rule | Divergence / required edge cases | Oracles | Status |
| --- | --- | --- | --- | --- | --- |
| `length` | `vela.Length` | — |  | `strings-length` | implemented |
| holes `[1, , 3]` | `vela.Hole()` | §6.2 | A hole reads as `undefined`; `map` keeps it, `filter`/`forEach`/`some`/`every`/`reduce`/`flat` skip it, `find*` visit it as `undefined`. | `holes`, `arr-callbacks` | implemented |
| `at` | `ssrJS.Method` | — | Negative index. | `arr-index` | implemented |
| `includes` | `ssrJS.Method` | — | SameValueZero: `[NaN].includes(NaN)` is `true` ✓. | `arr-index` | implemented |
| `indexOf`, `lastIndexOf` | `ssrJS.Method` | — | Strict equality: `[NaN].indexOf(NaN)` is `-1` ✓. | `arr-index` | implemented |
| `join` | `ssrJS.Method` | — | Default separator `,`; `null`/`undefined`/holes become empty. | `arr-join` | implemented |
| `slice`, `concat` | `ssrJS.Method` | — | `concat` flattens array arguments one level. | `arr-slice` | implemented |
| `map`, `filter`, `forEach`, `some`, `every`, `find`, `findIndex`, `findLast`, `findLastIndex` | `ssrJS.Method` with `vela.Func` callbacks | T-7.2b | Callbacks receive `(item, index, array)`; `map`/`filter`/`forEach`/`some`/`every` skip holes: `[3,,1].map(x => x*2)` keeps the hole ✓; `find*` visit holes as `undefined`. | `arr-callbacks` | implemented |
| `reduce`, `reduceRight` | `ssrJS.Method` | T-6.5a | Empty array without initial value throws `TypeError`; holes skipped. | `arr-reduce` | implemented |
| `flat(depth)`, `flatMap` | `ssrJS.Method` | — | Default depth 1; holes removed. | `arr-flat` | implemented |
| `sort`, `toSorted` | `ssrJS.Method`, `ssrJS.MutateArray` | T-6.3a, T-6.6b | Stable. Without comparator: compares `String(x)` by UTF-16 units and places `undefined` last, holes after it: `[10,9,undefined,1].sort()` is `[1,10,9,undefined]` ✓. Comparator result `NaN` counts as `0`. `sort` only on arrays the function created. | `arr-sort`, `arr-mutate` | implemented |
| `reverse`, `toReversed` | `ssrJS.Method`, `ssrJS.MutateArray` | T-6.6b | `reverse` mutates: only on arrays the function created. | `arr-reverse`, `arr-mutate` | implemented |
| `push`, `pop`, `shift`, `unshift`, `splice`, `fill` | `ssrJS.MutateArray` | T-6.6b | As a statement, a `const`/`let` initializer, or the right-hand side of an assignment, on a local array the function created; methods that change the length also need the array not to be copied into another variable, literal, or call argument. Otherwise `VELA-SSR-IMPURE`. | `arr-mutate` | implemented |
| `entries`, `keys`, `values` in `for…of` | index loop | §7.1 | Only as `for…of` sources in v1. | — | planned (S5) |
| `Array.from(arrayLike, mapFn)` | `ssrJS.ArrayFrom` | — | Strings by code point; `{length: n}`; holes become `undefined`. | `arr-from` | implemented |
| `Array.of` | `vela.ArrayOf` | — |  | `arr-from` | implemented |

### 2.4 Object functions

| Built-in | Go helper | Rule | Divergence / required edge cases | Oracles | Status |
| --- | --- | --- | --- | --- | --- |
| `Object.keys`, `Object.values`, `Object.entries` | `ssrJS.ObjectKeys` … | T-5.1c | Integer-like keys first, ascending; then insertion or JSON order. `encoding/json` writes `"10"` before `"2"`, JS enumerates `"2"` first ✓. A Go map has no insertion order: its keys follow JSON order. | `obj-keys` | implemented |
| `Object.fromEntries` | `ssrJS.ObjectFromEntries` | T-5.1c | Later duplicate keys win but keep the first position. | `obj-from` | implemented |
| `Object.assign` | `ssrJS.ObjectAssign` | T-6.6b | The target must be an object literal: `Object.assign({}, a, b)`. | `obj-assign` | implemented |
| `Object.hasOwn` | `ssrJS.ObjectHasOwn` | — |  | `obj-hasown` | implemented |
| `Object.freeze`, `Object.isFrozen` | — | — | See §5. | — | undecided |

### 2.5 Math

| Built-in | Go helper | Rule | Divergence / required edge cases | Oracles | Status |
| --- | --- | --- | --- | --- | --- |
| `Math.round` | `vela.MathRound` | T-6.4b | Ties round toward +∞: `Math.round(-2.5)` is `-2` ✓, `math.Round` gives `-3`. `-0` preserved; `Math.round(0.49999999999999994)` is `0`. | `math-round` | implemented |
| `Math.floor`, `Math.ceil`, `Math.trunc`, `Math.abs`, `Math.sign` | `vela.Math…` | — | `Math.sign(-0)` is `-0`. | `math-basic` | implemented |
| `Math.min`, `Math.max` | `vela.MathMin`, `vela.MathMax` | — | No arguments: `Infinity` / `-Infinity` ✓; any `NaN` gives `NaN`; `-0 < +0` ✓. | `math-minmax`, `spread-args` | implemented |
| `Math.pow`, `**` | `vela.Pow` | — | `Math.pow(1, Infinity)` is `NaN` ✓, `math.Pow` gives `1`; `pow(NaN, 0)` is `1`. Results are correctly rounded (integer exponents exactly, others in 256-bit arithmetic); `math.Pow` differs from V8 in about 40% of non-integer cases. V8 uses the platform libm, which is one unit in the last place off in rare cases next to a rounding boundary, such as `pow(1e21, 10)` (`TestFdlibmMatchesNode`, `testdata/math_fixture.json`). | `math-pow` | implemented |
| `Math.sqrt`, `Math.cbrt`, `Math.hypot` | `vela.Math…` | — | `Math.hypot(NaN, Infinity)` is `Infinity`; `hypot` uses V8's scaled, compensated sum; `cbrt` is the FreeBSD algorithm V8 uses and matches it bit for bit, where `math.Cbrt` differs in about 10% of cases. | `math-pow` | implemented |
| `Math.log`, `log2`, `log10`, `log1p`, `exp`, `expm1`, `sin`, `cos`, `tan`, `asin`, `acos`, `atan`, `atan2`, `sinh`, `cosh`, `tanh`, `asinh`, `acosh`, `atanh` | `vela.Math…` (`fdlibm_math.go`) | — | Ports of the fdlibm functions V8 uses, bit for bit with V8 (`TestTranscendentalMatchesNode`, `testdata/math_fixture.json`), including V8's `exp(1) = E` special case. Go's `math` differs: wrong results for logarithms of subnormal numbers and `log2` near 1, up to 11 ulp for large trigonometric arguments (decision of §5). | `math-transcendental` | implemented |
| `Math.fround`, `Math.clz32`, `Math.imul` | `vela.Math…` | — |  | `math-exact` | implemented |
| `Math.PI`, `Math.E`, other constants | Go constants | — |  | `math-const` | implemented |
| `Math.random` | host / client | T-6.6c |  | — | never |

### 2.6 Number methods

| Built-in | Go helper | Rule | Divergence / required edge cases | Oracles | Status |
| --- | --- | --- | --- | --- | --- |
| `toString()` (radix 10), text conversion | `ssrJS.Method` | T-6.4a | Exponent from 1e21; `-0` prints `0`. | `num-tostring` | implemented |
| `toFixed(d)` | `ssrJS.Method` | T-6.4b | Exact ties round up: `(2.5).toFixed(0)` is `"3"`, `strconv` gives `"2"`; non-ties agree (`(8.345).toFixed(2)` is `"8.35"` in both ✓); values ≥ 1e21 use `toString` (`"1e+21"` ✓); `(-0.0001).toFixed(2)` is `"-0.00"`; `d > 100` throws `RangeError`. | `num-tofixed` | implemented |
| `toString(radix)` | `ssrJS.Method` | T-6.4a | Port of V8's `DoubleToRadixCString`: fraction digits up to the precision of the double, rounded to even (`TestTranscendentalMatchesNode` compares 1500 values); radix outside 2–36 throws `RangeError` (decision of §5). | `num-radix` | implemented |
| `toPrecision(p)`, `toExponential(f)` | `ssrJS.Method` | T-6.4b | Exact decimal rounding of the binary value, ties up: `(2.5).toPrecision(1)` is `"3"`; `toExponential()` uses the shortest digits; out-of-range arguments throw `RangeError` (decision of §5). | `num-precision`, `num-exponential` | implemented |
| `Number.MAX_SAFE_INTEGER`, `MIN_SAFE_INTEGER`, `EPSILON`, `MAX_VALUE`, `MIN_VALUE`, `NaN`, `POSITIVE_INFINITY`, `NEGATIVE_INFINITY` | Go constants | — |  | `math-const` | implemented |
| `toLocaleString` | host | §3 |  | — | never |

### 2.7 JSON

| Built-in | Go helper | Rule | Divergence / required edge cases | Oracles | Status |
| --- | --- | --- | --- | --- | --- |
| `JSON.stringify(v)`, `JSON.stringify(v, null, indent)` | `ssrJS.JSONStringify` | T-5.1c, T-6.4a | JS does not escape `<`, `>`, `&`; `undefined` members and functions omitted, `NaN`/`Infinity` become `null`, `-0` becomes `0` ✓; lone surrogates are `\u` escapes; key order per T-5.1c; cycles throw `TypeError`. | `json-stringify` | implemented |
| `JSON.stringify` with replacer, `toJSON` methods | — | — | Not in v1. | — | never |
| `JSON.parse(s)` | `ssrJS.JSONParse` | T-6.5a | Result in the JSON value domain with `*vela.Object`; invalid input throws `SyntaxError` (message text differs from V8). | `json-parse` | implemented |

### 2.8 Promise

| Built-in | Go helper | Rule | Divergence / required edge cases | Oracles | Status |
| --- | --- | --- | --- | --- | --- |
| `Promise.resolve`, `Promise.reject`, `Promise.all`, `Promise.allSettled`, `.then`, `.catch`, `.finally` | `vela.Resolve`, `vela.Reject`, `ssrJS.All`, `ssrJS.AllSettled`, `ssrJS.Then` … | T-7.4b | See §1.6. Array order; first rejection wins; `allSettled` gives `{status, value}` / `{status, reason}`. | `async` | implemented |
| `Promise.race`, `Promise.any`, `new Promise(executor)` | host / client | T-7.4d | Depends on timing. | — | never |

### 2.9 Error objects

| Built-in | Go helper | Rule | Divergence / required edge cases | Oracles | Status |
| --- | --- | --- | --- | --- | --- |
| `err.message`, `err.name`, `new Error(m)`, `new TypeError(m)`, `new RangeError(m)` | `vela.NewError` | T-6.5d, T-7.4h | Host errors expose `"Internal error"` unless `vela.PublicError`. | `errors-new`, `fn-try` | implemented |
| `String(err)` | `vela.String` | — | `"[object Object]"` on the server; JavaScript gives `"Error: message"`. | — | planned (S5) |
| `err.stack` | — | — |  | — | never |
---

## 3. Never native

These are not translated. The Route column says what the developer does
instead. All are `never` and produce a diagnostic that names the route.

| API | Route | Rule | Reason |
| --- | --- | --- | --- |
| `Intl.*` (`NumberFormat`, `DateTimeFormat`, `PluralRules`, `RelativeTimeFormat`, `Collator`, `ListFormat`, `Segmenter`) | host, locale from `ctx` | H-8.3c | Locale data and ICU behaviour. |
| `toLocaleString`, `toLocaleDateString`, `toLocaleTimeString`, `localeCompare` | host | H-8.3c | Locale-dependent. |
| `Date` (constructor, `Date.now`, `Date.parse`, all methods) | host in `$state` initializers; `{#client}` elsewhere | T-6.6c | Nondeterministic and time-zone dependent. |
| RegExp literals and constructor; `match`, `matchAll`, `search`, `test`, `exec`, RegExp `replace`/`split` | host | §6.2 | Go `regexp` is RE2: no backreferences or lookaround, different semantics. |
| `Math.random`, `crypto.*`, `performance.now` | host in `$state` initializers; `{#client}` elsewhere | T-6.6c | Nondeterministic. |
| `fetch`, `XMLHttpRequest`, `WebSocket`, `EventSource` | host (server data loading) | T-7.4e | Browser I/O. |
| `setTimeout`, `setInterval`, `queueMicrotask`, `requestAnimationFrame`, `requestIdleCallback` | `{#client}` or event handlers | T-7.4d | Timing. |
| `window`, `document`, `navigator`, `location`, `history`, `screen`, `matchMedia` | `{#client}` | §9 | Browser state. |
| `localStorage`, `sessionStorage`, `indexedDB`, `document.cookie` | `{#client}`; request cookies through host and `ctx` | §9, H-8.3c | Browser state. |
| `IntersectionObserver`, `ResizeObserver`, `MutationObserver`, `Worker` | `{#client}` | §9 | Browser APIs. |
| `Promise.race`, `Promise.any`, `new Promise(executor)` | host or `{#client}` | T-7.4d | Timing. |
| `Map`, `Set`, `WeakMap`, `WeakSet` | host | — | Not in v1; see §5. |
| `BigInt`, `Symbol`, typed arrays, `ArrayBuffer`, `DataView` | host | — | Not in v1. |
| `Proxy`, `Reflect`, `eval`, `Function` constructor, dynamic `import()`, `with` | rejected | §6.2 | No faithful translation. |
| imports from npm packages | host or `{#client}` | T-7.3c | |
| `class`, `this`, generators | rejected | §6.2, §7.1 | Not in v1. |

---

## 4. Known divergences in the current implementation

Observed in the code on 2026-10-03; status on 2026-10-05 after task 044.

1. **`vela.Get`** read only a few map types. Fixed: the JSON view of Go
   values, and `TypeError` on a nullish receiver (`member-get`).
2. **`vela.Length`** returned `0` for `nil`. Fixed: `ssrJS.Length` throws
   `TypeError`; objects without `length` yield `undefined`
   (`strings-length`).
3. **Object literals** lost insertion order. Fixed: `*vela.Object`.
4. **Number literals** became `int` in `any` contexts and number props were
   `int`. Fixed: float constants and `float64` props (`number-text`).
   Preserved `*_props.go` sidecars keep their declared types.
5. **Operators without proven types** were rejected. Fixed: dynamic tier
   (`ops-*`).
6. **Local initializers were re-inlined at every read.** Fixed: evaluated
   once in declaration order.
7. **`??`** was rejected. Fixed (`ops-nullish`).
8. **Hydration payload state** was a compile-time constant. Fixed for the
   block engine (computed `s` entries). The legacy engine keeps the
   constant until it is removed (task 042).

---

## 5. Undecided rows

Each needs a product decision before the slice that implements it.

| Item | Options | Needed by |
| --- | --- | --- |
| `console.*` in region code | **decided 2026-10-07:** forward to the observer of `vela.WithConsoleObserver(ctx, f)`; nothing without one | S5 (done) |
| `Map` / `Set` native support | host-only in v1 (current), native after beta | after beta |
| Transcendental `Math` functions (`log`, `exp`, `sin`, …) | **decided 2026-10-07:** exact V8 results, ports of the fdlibm functions V8 uses (Go `math` was measured beyond 1 ulp and wrong for subnormal logarithms) | S5 (done) |
| `String.prototype.normalize` | **decided 2026-10-07:** `golang.org/x/text` v0.41.0 (Unicode 17); the module requires Go 1.25 | S5 (done) |
| Computed object keys `{[k]: v}` | admit in S2; keep rejected in v1 | S2 |
| `Number.prototype.toString(radix)`, `toPrecision`, `toExponential` | **decided 2026-10-07:** included in the beta | S5 (done) |
| `Object.freeze` / `isFrozen` | no-op freeze with `isFrozen` tracking; reject | S5 |
