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
| symbol imported from local `.js` | native or host | generated module package | same | T-7.3a | modules-* | planned (S4) | |
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
| destructuring (declarations, parameters, defaults) | native | Go locals | same | §7.1 | stmt-destructure | planned (S4) | |
| arrow function as built-in callback or local value | native | Go closure or inlined loop | same | T-7.2b | fn-arrow | planned (S4) | Currently rejected. |
| call to local function with one `return` | native | inlined expression | same | T-7.1a | fn-inline | partial | Existing inliner; becomes an optimisation of the next row. |
| call to local multi-statement function | native | generated Go function | same | T-7.1a | fn-statements | planned (S4) | |
| monomorphised variants | native | one Go function per argument type tuple | one dynamic variant above 4 | T-7.2a | fn-variants | planned (S4) | Threshold 4 (decision 1). |
| recursion | native | Go recursion | same | T-7.1b | fn-recursion | planned (S4) | |
| call bound by `//vela:host` | host | `vela.CallHost` (reflection; §8.4 conversions) | same | §8 | host-e2e | partial | Functions of components and of local `.js` modules, and parameterless hosts for module values (§8.7). Covered by Go end-to-end tests (`TestSSRHost*`); the host fixture tooling of V-11.3e is not done. |
| `new Error(m)`, `new TypeError(m)`, `new RangeError(m)` | native | `vela.NewError(kind, m)` | same | §6.2 | errors-new | planned (S4) | |
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
| `async function` / async arrow | native | Go function returning `(T, error)` | T-7.4a | async-* | planned (S4) |
| `await e` | native | take settled value or throw rejection | T-7.4a | async-await | planned (S4) |
| async call without `await` | native | settled `vela.Promise` | T-7.4b | async-promise | planned (S4) |
| `.then`, `.catch`, `.finally` | native | synchronous chaining | T-7.4b | async-chain | planned (S4) |
| `{#await}` with server-evaluated operand | native or host | render `then`/`catch`, snapshot in payload | T-7.4f | await-server | planned (S4) |
| `{#await}` whose operand cannot run on the server | client | wrap the block in `{#client}`; server renders the boundary fallback | T-7.4f, T-9.1b | await-client | planned (S6) |
| `{#await}` with a caller-supplied `RenderOptions.Await` snapshot | native | render the supplied branch; operand not evaluated | T-7.4f | await-snapshot | partial (string-only `Value`/`Error`) |
| top-level `await` in component script | rejected | — | T-7.4d | — | never |

### 1.7 Region boundaries

| Construct | Outcome | Rule | Status | Notes |
| --- | --- | --- | --- | --- |
| event handlers, `$effect`, `onMount`, `onDestroy`, binding write-back | excluded | T-2.2 | planned (S1) | Currently diagnosed through the static event bridge (`VELA-SSR-EVENT-REACTIVE`). |
| `{#client}` main branch | excluded | T-9.1c | planned (S6) | Fallback branch is region code. |
| client-only component (`<script context="client">`) | excluded | T-9.2 | planned (S6) | Its props and slot content are excluded from the parent region. |

---

## 2. Built-ins

All rows are `planned (S5)` unless the Status column says otherwise. The
*Divergence* column lists the known places where the obvious Go call differs
from JavaScript; each item must have an oracle row. Examples marked ✓ were
checked against Node and Go on 2026-10-03.

### 2.1 Conversions and global functions

| Built-in | Go helper | Rule | Divergence / required edge cases | Status |
| --- | --- | --- | --- | --- |
| `String(x)` | `vela.String` | T-6.4a | Text conversion exists for template text; the call form is not admitted. | partial |
| `Number(x)` | `vela.ToNumber` | §6.2 | `Number("")` is `0`, `Number(" 12 ")` is `12`, `Number("0x1f")` is `31`, `Number("1_0")` is `NaN` ✓; `strconv.ParseFloat` differs on all four. | planned (S5) |
| `Boolean(x)` | `vela.Truthy` | §6.2 | | planned (S5) |
| `parseInt(s, radix)` | `vela.ParseInt` | — | Parses a prefix: `parseInt("12px")` is `12` ✓, while `strconv.ParseInt` fails; `0x` prefix with radix 16 or absent. | planned (S5) |
| `parseFloat(s)` | `vela.ParseFloat` | — | Prefix parsing; `Infinity` literal. | planned (S5) |
| `isNaN`, `isFinite` | `vela.IsNaN`, `vela.IsFinite` | — | Coerce with `ToNumber` first. | planned (S5) |
| `Number.isNaN`, `Number.isFinite`, `Number.isInteger`, `Number.isSafeInteger` | `vela.Number…` | — | No coercion. | planned (S5) |
| `encodeURIComponent`, `encodeURI` | `vela.EncodeURIComponent`, `vela.EncodeURI` | — | JS keeps `!*'()` unescaped and encodes space as `%20` ✓; `url.QueryEscape` escapes them and writes `+`, `url.PathEscape` escapes them too. | planned (S5) |
| `decodeURIComponent`, `decodeURI` | `vela.DecodeURI…` | T-6.5a | Malformed input throws `URIError`. | planned (S5) |
| `Array.isArray` | `vela.IsArray` | — | | planned (S5) |

### 2.2 String methods

| Built-in | Go helper | Rule | Divergence / required edge cases | Status |
| --- | --- | --- | --- | --- |
| `length` | `vela.Length` | T-6.3a | UTF-16: `"😀".length` is `2` ✓. | partial (§1.2) |
| `at`, `charAt`, `charCodeAt`, `codePointAt` | `vela.String…` | T-6.3a | UTF-16 indices; negative index for `at`. | planned (S5) |
| `includes`, `startsWith`, `endsWith`, `indexOf`, `lastIndexOf` | `vela.String…` | T-6.3a | Positions in UTF-16 units; `position` argument clamping. | planned (S5) |
| `slice`, `substring` | `vela.StringSlice`, `vela.Substring` | T-6.3a | `substring` swaps reversed arguments; `slice` accepts negatives; cutting a pair yields a lone surrogate (T-6.3b). | planned (S5) |
| `toUpperCase`, `toLowerCase` | `vela.ToUpper`, `vela.ToLower` | T-6.3c | Full SpecialCasing: `"straße".toUpperCase()` is `"STRASSE"` ✓ (Go `STRAßE`); context-sensitive final sigma: `"ΣΑΣ".toLowerCase()` is `"σας"` ✓ (Go `σασ`). | planned (S5) |
| `trim`, `trimStart`, `trimEnd` | `vela.Trim…` | — | JS whitespace set: U+FEFF is trimmed by JS but not by `strings.TrimSpace` ✓. | planned (S5) |
| `padStart`, `padEnd` | `vela.Pad…` | T-6.3a | Target length in UTF-16 units; pad string truncated. | planned (S5) |
| `repeat` | `vela.Repeat` | T-6.5a | Negative or infinite count throws `RangeError`. | planned (S5) |
| `split(separator, limit)` with a string separator | `vela.Split` | T-6.3a | `split("")` splits into UTF-16 units and breaks surrogate pairs: `"😀".split("").length` is `2` ✓. | planned (S5) |
| `replace(string, string)`, `replaceAll(string, string)` | `vela.Replace`, `vela.ReplaceAll` | — | `replace` changes the first match only; replacement patterns `$&`, `$$`, `` $` ``, `$'` apply even with a string pattern: `"a".replace("a", "$&$&")` is `"aa"` ✓. | planned (S5) |
| `replace` / `replaceAll` with a callback | `vela.ReplaceFunc` | T-7.2b | Callback receives match, offset, string. | planned (S5) |
| `concat` | `vela.StringConcat` | — | | planned (S5) |
| spread / `for…of` over a string | code-point iteration | T-6.3a | `[..."😀"].length` is `1` ✓. | planned (S2) |
| `normalize` | — | — | Needs Unicode normalisation tables; see §5. | undecided |
| `localeCompare`, `toLocaleUpperCase`, `toLocaleLowerCase` | host | §3 | Locale-dependent. | never |
| `match`, `matchAll`, `search`, `replace`/`split` with a RegExp | host | §3 | | never |

### 2.3 Array methods

| Built-in | Go helper | Rule | Divergence / required edge cases | Status |
| --- | --- | --- | --- | --- |
| `length` | `len` / `vela.Length` | — | | partial (§1.2) |
| `at` | `vela.ArrayAt` | — | Negative index. | planned (S5) |
| `includes` | `vela.Includes` | — | SameValueZero: `[NaN].includes(NaN)` is `true` ✓. | planned (S5) |
| `indexOf`, `lastIndexOf` | `vela.IndexOf` | — | Strict equality: `[NaN].indexOf(NaN)` is `-1` ✓. | planned (S5) |
| `join` | `vela.ArrayJoinString` (exists for text) | — | Default separator `,`; `null`/`undefined`/holes become empty. | partial |
| `slice`, `concat` | `vela.ArraySlice`, `vela.ArrayConcat` | — | `concat` flattens array arguments one level. | planned (S5) |
| `map`, `filter`, `forEach`, `some`, `every`, `find`, `findIndex`, `findLast`, `findLastIndex` | loops or `vela.Array…` | T-7.2b | Callbacks receive `(item, index, array)`; `map`/`filter`/`forEach`/`some`/`every` skip holes: `[3,,1].map(x => x*2)` keeps the hole ✓; `find*` visit holes as `undefined`. | planned (S5) |
| `reduce`, `reduceRight` | loop | T-6.5a | Empty array without initial value throws `TypeError`; holes skipped. | planned (S5) |
| `flat(depth)`, `flatMap` | `vela.Flat` | — | Default depth 1; holes removed. | planned (S5) |
| `sort`, `toSorted` | `vela.Sort` | T-6.3a | Stable. Without comparator: compares `String(x)` by UTF-16 units and places `undefined` last: `[10,9,undefined,1].sort()` is `[1,10,9,undefined]` ✓. Comparator result `NaN` counts as `0`. | planned (S5) |
| `reverse`, `toReversed` | `vela.Reverse` | T-6.6b | `reverse` mutates: only on arrays created locally. | planned (S5) |
| `push`, `pop`, `shift`, `unshift`, `splice`, `fill` | `vela.Array…` | T-6.6b | Only on arrays created in the current function invocation; otherwise `VELA-SSR-IMPURE`. | planned (S5) |
| `entries`, `keys`, `values` in `for…of` | index loop | §7.1 | Only as `for…of` sources in v1. | planned (S5) |
| `Array.from(arrayLike, mapFn)` | `vela.ArrayFrom` | — | Strings by code point; `{length: n}`. | planned (S5) |
| `Array.of` | `vela.ArrayOf` | — | | planned (S5) |

### 2.4 Object functions

| Built-in | Go helper | Rule | Divergence / required edge cases | Status |
| --- | --- | --- | --- | --- |
| `Object.keys`, `Object.values`, `Object.entries` | `vela.ObjectKeys` … | T-5.1c | Integer-like keys first, ascending; then insertion or JSON order. `encoding/json` writes `"10"` before `"2"`, JS enumerates `"2"` first ✓. | planned (S5) |
| `Object.fromEntries` | `vela.ObjectFromEntries` | T-5.1c | Later duplicate keys win but keep the first position. | planned (S5) |
| `Object.assign` | `vela.ObjectAssign` | T-6.6b | Target must be created locally. | planned (S5) |
| `Object.hasOwn` | `vela.HasOwn` | — | | planned (S5) |
| `Object.freeze`, `Object.isFrozen` | — | — | See §5. | undecided |

### 2.5 Math

| Built-in | Go helper | Rule | Divergence / required edge cases | Status |
| --- | --- | --- | --- | --- |
| `Math.round` | `vela.MathRound` | T-6.4b | Ties round toward +∞: `Math.round(-2.5)` is `-2` ✓, `math.Round` gives `-3`. `-0` preserved. | planned (S5) |
| `Math.floor`, `Math.ceil`, `Math.trunc`, `Math.abs`, `Math.sign` | `math.*` plus `-0` handling | — | `Math.sign(-0)` is `-0`. | planned (S5) |
| `Math.min`, `Math.max` | `vela.MathMin`, `vela.MathMax` | — | No arguments: `Infinity` / `-Infinity` ✓; any `NaN` gives `NaN`; `-0 < +0` ✓. | planned (S5) |
| `Math.pow`, `**` | `vela.Pow` | — | `Math.pow(1, Infinity)` is `NaN` ✓, `math.Pow` gives `1`; `pow(NaN, 0)` is `1`. | planned (S5) |
| `Math.sqrt`, `Math.cbrt`, `Math.hypot` | `math.*` | — | | planned (S5) |
| `Math.log`, `log2`, `log10`, `exp`, trigonometric functions | `math.*` | — | Results are implementation-approximated; see §5. | undecided |
| `Math.PI`, `Math.E`, other constants | Go constants | — | | planned (S5) |
| `Math.random` | host / client | T-6.6c | | never |

### 2.6 Number methods

| Built-in | Go helper | Rule | Divergence / required edge cases | Status |
| --- | --- | --- | --- | --- |
| `toString()` (radix 10), text conversion | `vela.String` | T-6.4a | Exponent from 1e21; `-0` prints `0`. | partial (text conversion exists) |
| `toFixed(d)` | `vela.ToFixed` | T-6.4b | Exact ties round up: `(2.5).toFixed(0)` is `"3"`, `strconv` gives `"2"`; non-ties agree (`(8.345).toFixed(2)` is `"8.35"` in both ✓); values ≥ 1e21 use `toString` (`"1e+21"` ✓); `d > 100` throws `RangeError`. | planned (S5) |
| `toString(radix)`, `toPrecision`, `toExponential` | — | — | See §5. | undecided |
| `Number.MAX_SAFE_INTEGER`, `MIN_SAFE_INTEGER`, `EPSILON`, `MAX_VALUE`, `MIN_VALUE` | Go constants | — | | planned (S5) |
| `toLocaleString` | host | §3 | | never |

### 2.7 JSON

| Built-in | Go helper | Rule | Divergence / required edge cases | Status |
| --- | --- | --- | --- | --- |
| `JSON.stringify(v)`, `JSON.stringify(v, null, indent)` | `vela.JSONStringify` | T-5.1c, T-6.4a | JS does not escape `<`, `>`, `&` (Go does by default; use `SetEscapeHTML(false)`); `undefined` members omitted, `NaN`/`Infinity` become `null`, `-0` becomes `0` ✓; key order per T-5.1c. | planned (S5) |
| `JSON.stringify` with replacer, `toJSON` methods | — | — | Not in v1. | never |
| `JSON.parse(s)` | `vela.JSONParse` | T-6.5a | Result in the JSON value domain with `*vela.Object`; invalid input throws `SyntaxError`. | planned (S5) |

### 2.8 Promise

| Built-in | Go helper | Rule | Notes | Status |
| --- | --- | --- | --- | --- |
| `Promise.resolve`, `Promise.reject` | `vela.Promise…` | T-7.4b | | planned (S4) |
| `Promise.all` | `vela.PromiseAll` | T-7.4b | Array order; first rejection wins. | planned (S4) |
| `Promise.allSettled` | `vela.PromiseAllSettled` | T-7.4b | Result objects `{status, value}` / `{status, reason}`. | planned (S4) |
| `Promise.race`, `Promise.any`, `new Promise(executor)` | host / client | T-7.4d | Depends on timing. | never |

### 2.9 Error objects

| Built-in | Go helper | Rule | Notes | Status |
| --- | --- | --- | --- | --- |
| `err.message`, `err.name` | fields of `vela.ErrorValue` | T-6.5d, T-7.4h | Host errors expose `"Internal error"` unless `vela.PublicError`. | planned (S4) |
| `String(err)` | `vela.String` | — | `"Error: message"`. | planned (S4) |
| `err.stack` | — | — | | never |

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
| `console.*` in region code | drop on the server; forward to an observer in `ctx`; reject | S4 |
| `Map` / `Set` native support | host-only in v1 (current), native after beta | after beta |
| Transcendental `Math` functions (`log`, `exp`, `sin`, …) | require exact V8 results (needs own implementations); accept Go `math` with a 1-ulp tolerance in oracles; host-only | S5 |
| `String.prototype.normalize` | add `golang.org/x/text` as a runtime dependency; host-only | S5 |
| Computed object keys `{[k]: v}` | admit in S2; keep rejected in v1 | S2 |
| `Number.prototype.toString(radix)`, `toPrecision`, `toExponential` | include in beta; after beta | S5 |
| `Object.freeze` / `isFrozen` | no-op freeze with `isFrozen` tracking; reject | S5 |
