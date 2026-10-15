# SSR JavaScript-to-Go translation and host implementation contract

Status: normative, implemented in the beta (2026-10-09). Scope owner: Task
007; semantics in 028 and 043–046, host bindings and client boundaries in
030, delivery in 048 and 049; the beta gate is task 031 ([SSR_GATE.md](SSR_GATE.md)).

A Russian translation is kept in `SSR_TRANSLATION_CONTRACT.ru.md` with the
same rule identifiers. This English text is normative; update both together.

This document is the normative contract for how Vela server rendering turns
render-time JavaScript into Go, and how a developer supplies a Go
implementation for code the compiler does not translate. `SSR.md` is the
product overview. Where this document conflicts with the earlier design
(`archive/SSR_DESIGN.md`), this document wins; the superseded items are
listed in §14.

Every rule has a stable identifier (`T-…`, `H-…`, `D-…`). Tests, diagnostics,
and task evidence should cite these identifiers.

---

## 1. Fixed product decisions

- **P-1. Go only.** Server rendering never embeds, spawns, or calls a
  JavaScript engine. All server behaviour is generated Go plus the Vela Go
  runtime plus developer-owned Go.
- **P-2. Translate what can be made exact; delegate the rest explicitly.**
  Every server-evaluated JavaScript site has exactly one outcome (§4). The
  compiler translates a site only when the Go result is observably identical
  to the JavaScript result over the site's whole input domain. Otherwise the
  developer supplies Go (`server-host`), explicitly defers the site to the
  browser (`client-deferred`), or receives an actionable diagnostic.
- **P-3. No silent divergence.** A successful build implies one of: proven
  semantic parity, a developer-declared host implementation, or a
  developer-declared client boundary. Approximate translation is never an
  implicit outcome.
- **P-4. Go AST only.** Generated Go is built from `go/ast`, printed by
  `go/format`, and checked with `go/types` (existing Phase-0 invariant).
- **P-5. Cached ASTs only.** The translator consumes parser-owned JS ASTs; it
  never re-parses source text.

---

## 2. The server-evaluated region

The translator is responsible only for code that executes during a server
render. Everything else stays JavaScript and is not analysed for SSR.

**T-2.1 Region roots.** A component's server-evaluated region consists of:

1. default values of `export let` props;
2. `$state` / `$state.deep` / `$bindable` initializers;
3. `$derived` expressions;
4. template expressions in text, attributes, `class:` / `style:` directives,
   `{#if}` conditions, `{#each}` iterables and keys, `{#key}` expressions,
   `{#await}` operands, and props passed to child components;
5. plain top-level `const` / unchanged `let` bindings read by 1–4;
6. the transitive closure of functions and imported symbols called from 1–5,
   including functions in local `.js` modules.

**T-2.2 Region exclusions.** The following are never translated, never
type-checked for SSR, and never produce SSR diagnostics: event handlers,
`$effect`, `onMount` / `onDestroy`, binding write-back, `bind:this`, and any
function reachable only from them. A handler that is not in the region cannot
block SSR of its component.

> Consequence: the current static-event-bridge errors
> (`VELA-SSR-EVENT-REACTIVE`) must not be reported for a component whose
> hydration is executable. A component that fails hydration reports its
> hydration reason (§10), not an event-handler reason.

**T-2.3 Region report.** For every region site the compiler records: site ID,
source span, outcome (§4), tier (§5.4), semantic type, and, for a delegated
site, the host symbol or boundary. This record is the single source of truth
for codegen, diagnostics, `--ssr-report` (§10.4), and corpus metrics.

---

## 3. Pipeline

```
JS AST (cached) ──► Region & scope analysis ──► Server IR (typed, per site outcome)
                                                     │
                     Host package analysis (go/types)┘
                                                     ▼
                                           Go AST lowering ──► go/format ──► go/types
```

- **T-3.1** Decisions (outcome, type, tier, host binding) are made once,
  while building the Server IR. Go lowering only consumes IR; it must not
  classify, infer types, or reject constructs on its own.
- **T-3.2** Every IR node retains its source span and region site ID, so a
  runtime error and a diagnostic can both point at the original `.vela` /
  `.js` position.
- **T-3.3** IR construction is deterministic: the same inputs produce
  byte-identical Go output.
- **T-3.4** Host packages are analysed with `go/parser` and `go/types` only.
  The compiler never builds or executes developer Go during compilation.

---

## 4. Site outcomes and resolution order

| Outcome | Server | Client |
| --- | --- | --- |
| `server-native` | Generated Go evaluates the site. | Hydration adopts the result; later updates use the original JS. |
| `server-host` | Generated Go calls a developer Go function. | Same as above. |
| `client-deferred` | Deterministic fallback inside an owned marker range. | Hydrator evaluates the original JS and replaces the range. |
| `unsupported` | Build fails with a positioned diagnostic. | — |

**T-4.1 Resolution order** for a call to a function or imported symbol:

1. a declared host implementation (§8), even if native translation is
   possible — the developer's explicit choice wins;
2. native translation, if every construct in the function body is admitted
   (§6, §7) and its types are provable in some tier (§5);
3. an explicit client boundary enclosing the site (§9);
4. otherwise `unsupported`, with the diagnostic required by §10.

**T-4.2** `client-deferred` is never selected implicitly.

**T-4.3** A site whose value controls structure (`#if`, `#each`, `#key`,
`#await`, component selection) may be `server-native` or `server-host`, but
`client-deferred` only through an explicit structural boundary.

---

## 5. Value model and types

### 5.1 The JSON view rule

**T-5.1** The JavaScript-visible shape of every Go value that enters the
server region (props, host results, host-package constants) is exactly the
value the browser obtains from `JSON.parse(json.Marshal(v))`.

This one rule fixes property names (`json` tags), hidden fields (`json:"-"`),
`omitempty`, `time.Time` (an ISO string), `[]byte` (a base64 string), pointer
`nil` (`null`), and object key order. It also guarantees that the server
evaluates the same data the client receives in the hydration payload.

- **T-5.1a** Member access `user.name` on a Go struct resolves to the field
  whose encoded JSON name is `name`. Go methods are never visible.
- **T-5.1b** A Go value that cannot be JSON-encoded (func, chan, complex,
  cycle) may not enter the region. This is checked with `go/types` for prop
  and host result types and fails the build (`VELA-SSR-HOST-SIG` /
  `VELA-SSR-PROPS`).
- **T-5.1c** Object key enumeration (`Object.keys`, `for…in`, spread, JSON
  output) follows JavaScript order over the JSON view: integer-like keys in
  ascending numeric order, then other keys in JSON text order. Note that
  `encoding/json` writes map keys sorted as strings (`"10"` before `"2"`)
  while JavaScript enumerates `"2"` before `"10"`.

### 5.2 Semantic types

The IR uses JavaScript-level semantic types:

`undefined`, `null`, `boolean`, `number`, `string`, `Array<T>`,
`Object{field: T…}` (closed shape), `Record<T>` (open string-keyed),
`Function(sig)`, unions of these, and `unknown`.

Sources, in priority order: developer prop types in `*_props.go` and host
signatures (Go is authoritative), JSDoc `@type` / `@param` / `@returns`,
literal initializers, flow through declarations and returns, and the result
types of admitted built-ins.

### 5.3 Go representation

| Semantic type | Go representation |
| --- | --- |
| `number` | `float64`, always. Go integer types are accepted at the boundary and widened. |
| `string` | Go `string` holding UTF-8; see §6.3 for UTF-16 operations. |
| `boolean` | `bool` |
| `null` | `nil` |
| `undefined` | `vela.Undefined()` (existing sentinel; distinct from `nil`) |
| `Array<T>` | `[]T` when `T` is precise, else `[]any` |
| `Object{…}` from Go | the developer struct (read through the JSON view) |
| `Object{…}` / `Record` created by translated code | `*vela.Object` (insertion-ordered); never `map[string]any` |
| union / `unknown` | `any` holding a value of the JSON value domain |

**T-5.3a** A `number` stays `float64` inside translated code even when it is
integral. Integer conversion happens only at an explicit boundary (index
access, host parameter of integer type) and is checked (§8.4).

### 5.4 Two tiers, one semantics

- **Typed tier**: types proven precise; lowered to direct Go
  (`a + b` on `float64`, `len(s)` on `[]T`).
- **Dynamic tier**: any type is `unknown` or a union; lowered to runtime
  helpers with full JavaScript semantics over the JSON value domain
  (`vela.Add(a, b)`, `vela.Get(v, "k")`, `vela.Less(a, b)`).

**T-5.4a** Tier selection is an optimisation only. For every admitted
construct, typed and dynamic lowering must produce identical observable
results; oracles (§11) run both tiers.

**T-5.4b** The lack of a precise type is never by itself a reason for
`unsupported`. Errors such as "relational operator requires two known
numbers" are replaced by dynamic-tier lowering.

**T-5.4c** Every dynamic helper is total over the JSON value domain plus
`undefined`: it returns a value or a JavaScript exception (§6.5), and never
panics.

---

## 6. Expression semantics

### 6.1 Evaluation order

- **T-6.1a** Operands, arguments, and object/array elements evaluate left to
  right, each exactly once.
- **T-6.1b** `&&`, `||`, `??`, `?:` and `?.` short-circuit: the unselected
  operand is never evaluated (R1). Recovering from a panic is not an
  implementation of short-circuiting.
- **T-6.1c** A host call executes exactly once per evaluation of its call
  site, at the position JavaScript would call it, and is never hoisted,
  cached across sites, or speculatively executed.

### 6.2 Operators

| Construct | Contract |
| --- | --- |
| `+` | JS `ToPrimitive` / string-or-number rule, both tiers. |
| `- * / % **` | IEEE-754 `float64`; `%` is JS remainder (sign of dividend); `**` follows `Math.pow` edge cases. |
| `< <= > >=` | Number comparison, or UTF-16 code-unit string comparison when both operands are strings, otherwise JS abstract relational comparison. |
| `===` / `!==` | JS strict equality (`NaN !== NaN`, `+0 === -0`); objects compare by identity. |
| `==` / `!=` | Admitted only with a `null` / `undefined` literal operand (`x == null`). All other uses are `unsupported` with a hint to use `===`. |
| `!`, `typeof`, unary `-`/`+` | JS semantics; `typeof` of a Go-originated value follows the JSON view. |
| `?.`, `??` | Admitted in both tiers. |
| template literals | Text-conversion contract (`SSR.md`, "Text conversion"). |
| array/object literal, spread of arrays/objects | Admitted; objects build `*vela.Object` with insertion order. |
| arrow functions | Admitted as callbacks to admitted built-ins and as local values; closures may read captured bindings. |
| `new Error(m)`, `new TypeError(m)`, `new RangeError(m)` | Admitted; produce error objects with `name` and `message`. |
| other `new`, `this`, `class`, getters/setters, `delete`, `in`, `instanceof`, comma, `void`, regex literals, tagged templates | `unsupported` in v1. |

### 6.3 Strings

- **T-6.3a** `length`, indexing, `charAt`, `charCodeAt`, `slice`,
  `substring`, `at`, `padStart`/`padEnd`, `indexOf` positions, and sort order
  use UTF-16 code units. Example: `"😀".length` is 2 in JS, while Go's
  `len` is 4.
- **T-6.3b** A lone surrogate produced by an operation is written to HTML as
  U+FFFD. This is the single declared encoding divergence; it has an oracle
  row, and dev-mode hydration verification (§11.3) treats it as equal.
- **T-6.3c** Case mapping uses full Unicode SpecialCasing, not
  `strings.ToUpper`. Example: `"straße".toUpperCase()` is `"STRASSE"` in JS
  and `"STRAßE"` with `strings.ToUpper`. Locale-sensitive variants
  (`toLocaleUpperCase`) are host-only.

### 6.4 Numbers to text

- **T-6.4a** Number → string follows ECMAScript `Number::toString` (shortest
  round-trip; exponent form from 1e21; `-0` prints as `0`; `NaN`;
  `Infinity`).
- **T-6.4b** Each formatting built-in carries its own JS rounding rule.
  Examples that must not use the obvious Go call:
  `(2.5).toFixed(0)` is `"3"` in JS and `"2"` with
  `strconv.FormatFloat(2.5, 'f', 0, 64)`; `Math.round(-2.5)` is `-2` in JS
  and `-3` with `math.Round`.

### 6.5 Exceptions

- **T-6.5a** An operation that throws in JavaScript (property read on
  `null`/`undefined`, calling a non-function, `throw` in a translated
  function) produces a Go error of type `*vela.RenderError` that carries the
  JS error kind (`TypeError`, `RangeError`, `Error`), the message, and the
  site's source position. The renderer returns that error.
- **T-6.5b** An out-of-range array index or a missing property is not an
  exception: it yields `undefined`, as in JavaScript. Generated code must
  never cause a Go index-out-of-range or nil-map panic.
- **T-6.5c** Output written before an error is not rolled back. Applications
  that need all-or-nothing responses render into a buffer. The existing
  payload preflight (`VELA-SSR-PROPS`) keeps its stronger guarantee. A
  render collects its output in chunks (written directly into a
  `*bytes.Buffer`) and writes what it has when the outermost component
  returns, with or without an error, so the writer receives the same
  bytes in fewer calls.
- **T-6.5d** `try` / `catch` / `finally` follow JavaScript semantics. `catch`
  receives thrown JS values, runtime JS exceptions (T-6.5a), and host errors
  (exposed as described in T-7.4h). `finally` runs after normal completion,
  `return`, and `throw`; a `return` or `throw` inside `finally` overrides the
  pending completion. Go panics are never caught.
- **T-6.5e** Only functions that can throw, directly or transitively, are
  lowered with an `error` result. Throw analysis keeps code that cannot throw
  free of error-checking overhead.

### 6.6 Purity and determinism

- **T-6.6a No external writes.** Region code may not write component state,
  module-level bindings, globals, props, or values it did not create.
  Violations are `unsupported` (`VELA-SSR-IMPURE`).
- **T-6.6b Local mutation is allowed.** A translated function may mutate
  arrays and objects that it created itself during the current invocation
  (for example `const out = []; out.push(x)`), and may reassign its own
  `let` bindings. The compiler proves this by local allocation analysis; it
  does not attempt general escape analysis.
- **T-6.6c Nondeterminism.** `Date.now()`, `new Date()`, `Math.random()`,
  `crypto.*`, `performance.now()`, `Promise.race`, and `Promise.any` are
  never native. In a `$state`
  initializer they may be `server-host` (the value travels in the payload).
  In a `$derived` or template expression they require `client-deferred`,
  because the client recomputes them and would diverge.
- **T-6.6d Initialization order.** Prop defaults, then `$state` initializers,
  then `$derived` values evaluate once, in script declaration order — the
  same order as client initialization. `$state(prop)` and `$derived` over
  props and state are ordinary admitted sites. The hydration payload carries
  the computed initial values, not compile-time constants.

---

## 7. Statements, functions and modules

### 7.1 Admitted statements (v1)

`const` / `let` declarations (including array/object destructuring with
defaults), assignment to local bindings, compound assignment, `++`/`--` on
local bindings, `if`/`else`, `for (;;)`, `for…of` over arrays and strings,
`while`, `do…while`, `break` / `continue` (without labels), `return`,
`throw`, `try` / `catch` / `finally` (T-6.5d), `switch` with `===` matching,
nested function declarations, block scoping, `async` functions and `await`
(§7.4).

Not admitted in v1: labels, `for…in`, generators and async generators,
`for await`, classes, `with`, `eval`, `arguments`, `var` hoisting across
blocks, getters/setters.

- **T-7.1a** A helper with several statements is translated as a Go function,
  not inlined. The current single-`return` inliner becomes an optimisation of
  this path.
- **T-7.1b** Recursion is allowed. Termination is the developer's
  responsibility, as in JavaScript.

### 7.2 Functions and specialisation

- **T-7.2a** Each translated JS function produces one Go function per
  distinct tuple of argument semantic types observed at its region call sites
  (monomorphisation), named deterministically
  (`formatCurrency__num`, `formatCurrency__any`). When the number of variants
  for a function would exceed 4, the compiler emits a single dynamic-tier
  variant instead.
- **T-7.2b** Callbacks passed to admitted array built-ins are lowered as Go
  closures or loops; their parameters receive the element type.

### 7.3 Modules

- **T-7.3a** Local `.js` modules imported by region code are translated under
  the same rules. Each module maps to one generated Go package that mirrors
  its path under `server/modules/`. A function used by several components is
  translated once.
- **T-7.3b** Module top-level code runs on the server only to initialise
  `const` bindings with admitted, deterministic initializers. Any other
  top-level side effect makes the module's region symbols `unsupported`
  unless they are host-bound.
- **T-7.3c** Imports of npm packages are never translated. Their region
  symbols are `server-host` or `client-deferred`, or the build fails.

### 7.4 Asynchronous code: synchronous execution model

The server's only job is to return rendered HTML, so asynchronous code runs
in one synchronous pass.

- **T-7.4a Execution model.** An `async` function is lowered to an ordinary
  Go function that returns `(T, error)`. `await e` evaluates `e`; when `e` is
  a promise produced by translated or host code, `await` yields its fulfilled
  value or throws its rejection at that point. Generated code has no
  goroutines, event loop, or microtask queue.
- **T-7.4b Promise values.** Calling an async function without `await`
  produces an already-settled `vela.Promise` (fulfilled value or rejection)
  at the call position. Admitted operations: `.then`, `.catch`, `.finally`,
  `Promise.resolve`, `Promise.reject`, `Promise.all`, and
  `Promise.allSettled`, evaluated in array order.
- **T-7.4c Declared difference.** JavaScript interleaves async functions at
  `await` points; the synchronous model finishes each call in source order.
  Under T-6.6a (no external writes) and H-8.5b (deterministic hosts) the
  results are identical. The only difference is the order in which host
  functions are called, and hosts must not depend on that order. A
  second difference follows from the same model: a `.then`, `.catch`, or
  `.finally` reaction runs at the call that registers it, not in a later
  microtask, so code that reads a variable a reaction writes before it
  awaits the promise sees the write on the server only. Write such code
  with `await`.
- **T-7.4d Not admitted.** `Promise.race` and `Promise.any` (their results
  depend on timing; use a host or a client boundary), `setTimeout`,
  `setInterval`, `queueMicrotask`, `requestAnimationFrame`,
  `new Promise(executor)`, `for await`, async generators, and top-level
  `await` in a component script (the client language does not support it).
- **T-7.4e Data loading.** `fetch` and other browser I/O are never native.
  Server data loading is a host function: the JS `loadUsers()` calls `fetch`
  in the browser, and its Go host reads the database directly. H-8.5a applies
  to the resolved value.
- **T-7.4f `{#await}` blocks.** When the operand is `server-native` or
  `server-host`, the server evaluates it synchronously and renders the `then`
  branch with the value or the `catch` branch with the error, never
  `pending`. The settled snapshot (state plus the JSON view of the value or
  error) is written to the payload. Hydration adopts the settled branch, and
  the client does not re-evaluate the operand for initial hydration; it
  re-evaluates only when the operand's dependencies change. (Required
  behaviour: verify the current client runtime and change it if it
  re-executes the operand.) An operand that cannot be evaluated on the
  server requires the whole `{#await}` block to be inside a `{#client}`
  block; the server then renders that boundary's fallback (T-9.1b). A
  snapshot passed through
  `RenderOptions.Await` overrides evaluation, and the operand is then not
  evaluated. Snapshot values extend from strings to any JSON value that
  satisfies T-5.1b.
- **T-7.4g Cancellation.** Hosts receive the render `ctx`. When `ctx` is
  cancelled, the render returns `ctx.Err()` at the next host call or `await`
  point.
- **T-7.4h Exposing host errors.** A host error reaches JS code (`catch`
  binding, `{#await}` `catch` branch) as an error object with `name`
  `"Error"`. Its `message` is the public message when the error chain
  contains a `vela.PublicError`; otherwise it is the fixed text
  `"Internal error"`, and the original error is passed to the observer set by
  `vela.WithErrorObserver(ctx, …)`. Reason: `catch` branch content and the
  payload are sent to the browser, so internal error text (SQL, file paths)
  must not leak by default. An uncaught host error still fails the render with
  the full error, which stays on the server.

---

## 8. Developer host implementations

### 8.1 Project configuration

`vela.ssr.json` at the project root (optional until a host is needed):

```json
{
  "module": "example.com/site/out/server",
  "host": {
    "dir": "ssrhost",
    "module": "example.com/site/ssrhost"
  },
  "runtime": {
    "module": "example.com/vela/ssr_runtime"
  }
}
```

`module` replaces the `--ssr-module` default; the CLI flag remains as an
override. `runtime.module` overrides the Go runtime import path (R-12.1b). `host.dir` is a developer-owned Go source tree. The compiler reads
it but never writes, formats, or deletes an existing file in it (H-8.6).

### 8.2 Discovery and binding

**H-8.2a Mirrored packages.** Host code for a JS source file lives in the
package that mirrors that file's path:

| JS symbol | Host package directory |
| --- | --- |
| export `formatCurrency` of `src/lib/format.js` | `ssrhost/src/lib/format/` |
| function `embedUrl` inside `src/blocks/VideoBlock.vela` | `ssrhost/src/blocks/videoblock/` |

**H-8.2b Explicit directive.** A Go function becomes a host implementation
only through a directive comment that names the JS symbol:

```go
package format

//vela:host formatCurrency
func FormatCurrency(amount float64) (string, error) {
	// ...
}
```

Naming conventions alone never bind a function. This keeps unrelated Go code
in the host tree inert and lets the Go name differ from the JS name.

**H-8.2c Stale bindings.** A `//vela:host` directive whose JS symbol does not
exist in the mirrored source is a build error (`VELA-SSR-HOST-STALE`), so a
JS rename cannot leave a silently unused implementation.

**H-8.2d Duplicate bindings.** Two directives for one JS symbol are a build
error.

**H-8.2e Targets.** In v1 a host binding targets a named function: a
module export, or a top-level `function` declaration / top-level `const`
arrow function in a component script. To delegate an expression that is not
a function call, the developer extracts it into a named function; the
diagnostic for such a site states this.

### 8.3 Signature contract

```
func Name([ctx context.Context,] p1 T1, …, pn Tn) R
func Name([ctx context.Context,] p1 T1, …, pn Tn) (R, error)
```

- **H-8.3a** Parameters match JS parameters by position. A JS call with
  fewer arguments passes `undefined` for the missing ones, so the matching Go
  parameter must be able to receive it (`any`, a pointer, or
  `vela.Optional[T]`); otherwise the call site is a build error.
- **H-8.3b** Admitted Go parameter and result types (boundary types):
  `string`, `bool`, `float64`, signed and unsigned integer types (parameters
  only; checked conversion, §8.4), `[]T`, `map[string]T`, structs and
  pointers to structs that satisfy T-5.1b, `time.Time` (parameter: parsed
  from its ISO string), `any`, `vela.Optional[T]`.
- **H-8.3c** An optional first `context.Context` parameter receives the
  context passed to the renderer. Renderers gain
  `RenderXContext(ctx, w, props, slots)`; the existing entry points remain
  and pass `context.Background()`. This is how locale, user, and request data
  reach formatting hosts without becoming props.
- **H-8.3d** Variadic JS functions bind to a Go variadic final parameter.
- **H-8.3e** The compiler checks every call site against the signature with
  `go/types`. When the argument type is statically known, conversion code is
  generated at compile time. When it is not (dynamic tier), the generated
  code calls a typed runtime converter that returns a `*vela.RenderError`
  (`TypeError`) on mismatch; it never panics.

### 8.4 Conversion rules

| JS value → Go parameter | Rule |
| --- | --- |
| `number` → `float64` | identity |
| `number` → integer type | must be finite, integral, and in range; otherwise `RangeError` |
| `string` → `time.Time` | RFC 3339 parse; failure is `RangeError` |
| `null` → pointer / `any` / `Optional` | `nil` / `nil` / `Optional{Null: true}` |
| `undefined` → `any` / `Optional` | `vela.Undefined()` / `Optional{Absent: true}` |
| object → struct | through the JSON view (`json.Unmarshal` semantics, unknown fields ignored) |
| Go result → JS | through the JSON view (T-5.1) |

### 8.5 Behavioural obligations of the developer

These obligations cannot be fully proved by the compiler. The tooling in §11
detects violations.

- **H-8.5a Parity.** For every input that reaches it, a host function must
  return the value that the JS function returns in the browser. The client
  keeps using the JS implementation for later updates; disagreement shows up
  as a hydration mismatch or as a visual jump after the first update.
- **H-8.5b Determinism.** For the same arguments and context, a host returns
  the same value. Request-scoped data comes from `ctx`, not from mutable
  globals.
- **H-8.5c Errors.** A host reports failure through its `error` result. The
  renderer wraps it as `*vela.HostError{Site, JSName, Err}` and returns it.
  The compiler does not recover panics from developer code.

### 8.6 Scaffolding and non-destructive generation

- **H-8.6a** For each `unsupported` call site whose fix is a host binding,
  `vela --ssr-scaffold-hosts` creates a stub only when the target file does
  not exist:
  `ssrhost/<mirrored path>/<name>_host.go`, containing the directive, the
  signature inferred from the call sites, and a body that returns
  `vela.ErrHostNotImplemented`. Existing files are never modified.
- **H-8.6b** A stub that still returns `vela.ErrHostNotImplemented` fails the
  render with a positioned error, so it cannot ship unnoticed.

### 8.7 Host values that are not functions

Module-level `const` values used by region code (for example a currency
table) are translated when their initializer is admitted. If it is not, the
developer binds a Go function with no parameters, which is called once per
render and cached for that render only.

---

## 9. Client boundaries

### 9.1 Template boundary

```vela
{#client}
  <Chart :data={stats} />
  <p>Now: {new Date().toLocaleTimeString()}</p>
{:else}
  <div class="chart-skeleton"></div>
{/client}
```

- **T-9.1a Syntax.** `{#client}…{/client}` with an optional `{:else}`
  fallback branch. It is a structural block of the same family as `{#if}`
  and may appear wherever a structural block may appear, including inside
  `#if`, `#each`, `#key`, `#await`, component slots, and another component's
  content.
- **T-9.1b Fallback is optional.** Without `{:else}` (or with an empty one)
  the server renders an empty owned marker range. The compiler never invents
  fallback content.
- **T-9.1c Region.** The fallback is region code and follows every rule in
  this document. The main branch and every expression inside it are excluded
  from the region (T-2.2); they produce no SSR diagnostics and no Go code.
- **T-9.1d Mount timing.** After the owning component's hydration commits,
  the main branch is mounted with the normal CSR code path into the owned
  range, and the fallback nodes are removed. Inside a structural owner
  (for example, an `#each` row) each owner instance mounts its own boundary.
  Deferred strategies (on idle, when visible) are outside v1.
- **T-9.1e Lifecycle.** The boundary belongs to its structural owner: when the
  owner is destroyed, the mounted content (or a pending mount) is destroyed
  with it. A rollback of the owner's hydration transaction cancels the
  pending mount.
- **T-9.1f CSR builds.** In a build without `--ssr`, `{#client}` renders its
  main branch directly and ignores `{:else}`.
- **T-9.1g Payload.** Nothing from the main branch is serialized. Values it
  needs are computed by the client after mounting.

### 9.2 Client-only components

```vela
<script context="client">
  export let data = [];
  // browser-only code
</script>
```

- **T-9.2a** A component whose script is declared `context="client"` is
  client-only. The compiler generates no Go renderer for it.
- **T-9.2b** Every usage of a client-only component is lowered as if it were
  wrapped in `{#client}<X … />{/client}` with an empty fallback. A parent may
  supply a fallback by writing the wrapper explicitly. This is an explicit
  declaration by the component author, so T-4.2 still holds.
- **T-9.2c** Props, events, bindings, and slot content passed to a
  client-only component are evaluated only by the client; they are excluded
  from the parent's region.
- **T-9.2d** In CSR builds a client-only component behaves as an ordinary
  component.

### 9.3 Structural control

- **T-9.3** A value that controls structure (T-4.3) and cannot be evaluated
  on the server must be moved inside a `{#client}` block together with the
  structure it controls. The diagnostic for such a site states this.

---

## 10. Diagnostics

### 10.1 Required content

Every SSR diagnostic includes:

1. the code and the source position of the smallest offending node;
2. a reason chain from the region root to that node, for example
   `$derived "paras" → call "split" → regex literal argument`;
3. at least one concrete fix. For a host fix, the fix gives the exact host
   file path, the directive, and the Go signature inferred from the call
   sites.

Example:

```
src/blocks/BarChart.vela:12:9: [error] VELA-SSR-HOST-MISSING
  formatCurrency (src/lib/format.js:3) uses Intl.NumberFormat, which has no
  server-native translation.
  reason: template text → call formatCurrency → new Intl.NumberFormat
  fix: implement it in Go:
    ssrhost/src/lib/format/format_currency_host.go
      //vela:host formatCurrency
      func FormatCurrency(ctx context.Context, value float64) (string, error)
    or run: vela --ssr --ssr-scaffold-hosts
```

### 10.2 Codes

| Code | Meaning |
| --- | --- |
| `VELA-SSR-EXPR` | Construct outside the admitted language (§6, §7). |
| `VELA-SSR-HOST-MISSING` | A function needs a host implementation and none is declared. |
| `VELA-SSR-HOST-SIG` | A host signature does not match its call sites or uses a non-boundary type. |
| `VELA-SSR-HOST-STALE` | A directive names a missing JS symbol, or a symbol is bound twice. |
| `VELA-SSR-IMPURE` | A region write outside T-6.6b. |
| `VELA-SSR-NONDET` | A nondeterministic API outside the positions T-6.6c allows. |
| `VELA-SSR-HYDRATION` | The template structure is outside the current hydration lifecycle; reported separately from translation. |

### 10.3 Aggregation

- **D-10.3a** Report all region failures of a component in one build, not
  only the first.
- **D-10.3b** Report a function once at its definition, then list its call
  sites. Do not repeat it per call site.
- **D-10.3c** A child component's failure appears once, on the child. The
  parent receives one note pointing to it, not a copy of its errors.

### 10.4 Report

`vela --ssr --ssr-report=<file.json>` writes the T-2.3 records for all
components: outcome, tier, type, and host binding per site, and artifact mode
per component. CI uses this report to track the compatibility corpus; a site
that moves from `server-native` or `server-host` to `unsupported` is a
regression.

---

## 11. Verification

### 11.1 Built-in catalogue

- **V-11.1a** Admitted built-ins are listed in one Go table that is the
  source of truth. Each entry has: JS name and receiver type, outcome, Go
  helper, the tiers it supports, oracle row IDs, and declared divergences.
  `SSR_TRANSLATION_MATRIX.md` is generated from this table. Until the table
  exists (slice S5), the matrix is maintained by hand and every translator
  change updates its rows in the same commit.
- **V-11.1b** A built-in is admitted only together with its oracle rows and a
  dynamic-tier implementation (T-5.4a).

### 11.2 Oracles and differential testing

- **V-11.2a** Each operator and built-in has tracked oracle rows evaluated by
  Node and by generated Go in both tiers. The existing
  `testdata/ssr_*_oracle.json` format is extended rather than replaced.
- **V-11.2b** Differential fuzzing: a generator produces random values from
  the JSON value domain plus `undefined`, and random admitted expressions over
  them. The test evaluates them in Node and in generated Go and compares
  results and error kinds. Any mismatch becomes a fixed oracle row.
- **V-11.2c** Required edge rows for every numeric and string built-in:
  `NaN`, `±Infinity`, `-0`, `0.5`/`2.5`/`-2.5` rounding ties, integers above
  2^53, empty string, astral characters, lone surrogates, `null`,
  `undefined`, empty array, holes.

### 11.3 Host parity tooling

- **V-11.3a Build mode.** The CLI has an explicit build mode:
  `--mode=development|production`, default `development`. The mode is
  independent of `--minify`. Production builds contain no verification code.
- **V-11.3b Hydration verification.** In development builds the hydrator
  evaluates each adopted text and attribute site with its client JS and
  compares the result with the server DOM. A mismatch report contains the
  source position, site ID, host symbol when there is one, and both values:

  ```
  [vela] hydration mismatch at src/OrdersTable.vela:14:9 (site 7, host formatCurrency)
    server: "$1,234.5"   client: "$1,234.50"
  ```

  Structural sites (selected `#if` branch, `#each` row count, `#await` state)
  are not compared in v1.
- **V-11.3c Verification policy.** `--hydration-verify=repair|fail|off`;
  the default in development is `repair`.
  - `repair`: report a console warning, write the client value into the DOM,
    and continue hydration. For everyday development.
  - `fail`: report the mismatch, roll back the hydration transaction (the
    existing rollback contract), and throw an error with code
    `VELA-HYDRATION-MISMATCH`. For CI and tests; the strict browser runner
    uses this policy.
  - `off`: no comparison.

  Combining `--hydration-verify` with `--mode=production` is a CLI error.
- **V-11.3d Encoding divergence.** A value that differs only by the U+FFFD
  replacement of T-6.3b counts as equal.
- **V-11.3e Host fixtures (optional).** A developer may add
  `ssrhost/<path>/<name>_cases.json` with input/output rows. The compiler
  generates a Go test and a Node test from that file, so the same rows check
  the host implementation and the JS implementation.

---

## 12. Runtimes

### 12.1 Module and versioning

- **R-12.1a Nested module.** The Go runtime is a nested Go module in the
  compiler repository: directory `ssr_runtime/` with its own `go.mod`.
  Package name stays `ssr_runtime`. Generated code and documentation import
  it under the conventional alias `vela`; that is why examples in this
  document write `vela.PublicError`.
- **R-12.1b Module path.** The runtime module is
  `github.com/elauven/vela/ssr_runtime` (fixed 2026-10-11). In the compiler
  it appears in exactly one place (the `SSRRuntimeImportPath` constant) and
  can be overridden per project in `vela.ssr.json`
  (`"runtime": {"module": "…"}`) for a fork.
- **R-12.1c Versions.** Releases are tagged `ssr_runtime/v0.x.y`. While in
  `v0`, incompatible changes are allowed. The compiler module does not
  require the runtime module: only its tests import it, through the
  committed `go.work`, so `go install` of the compiler needs no runtime
  version and no `replace`. Implemented in task 049 (2026-10-09).
- **R-12.1d Contract check.** The runtime exports a contract marker
  (currently `ContractV1`, `ssr_runtime/contract.go`; the compiler names it in
  `SSRRuntimeContract`), and every generated file references the marker it
  was generated for (`var _ = vela.ContractV1`). A compiler/runtime mismatch
  then fails `go build` with an undefined-identifier error instead of
  misbehaving at runtime. The marker changes whenever generated code starts
  to depend on a new or changed runtime API.

### 12.2 JavaScript runtime delivery

- **R-12.2a Source of truth.** The JavaScript runtime lives in the `runtime/`
  directory of the repository. It replaces the former root `runtime.js` and its
  `runtime/runtime.js` re-export; no other copy is edited by hand. The compiler
  embeds it from `runtime/`. The runtime is split into modules by area: core
  reactivity and scheduling, DOM, hydration, and development checks.
- **R-12.2b One instance per page.** All generated modules of one build must
  import the same runtime module URL. A copy per output directory is not
  allowed: each copy is downloaded and evaluated separately, and copies
  behave as one runtime only while every piece of shared runtime state lives
  on `globalThis` (today the batch queue and the proxy brand use
  `Symbol.for`). One module URL makes module identity the guarantee instead.
- **R-12.2c Build variants.** `--mode=production` omits the
  development-check module (V-11.3); builds without `--ssr` omit the
  hydration module.
- **R-12.2d Client contract check.** Generated modules import a named
  contract export from the runtime (for example `$$contractV3`). A
  version mismatch then fails at module load with a missing-export error, the
  same principle as R-12.1d.
- **R-12.2e Stage 1 (now).** The compiler writes the runtime once at the root
  of the client output and generated modules import it through computed
  relative paths.
- **R-12.2f Stage 2 (before beta; rewritten 2026-10-09).** Vela is a
  compiler: its output is plain ES modules for the application's own
  toolchain (a bundler, an import map, a server), and it never resolves,
  copies, hashes or bundles application dependencies. Package imports are
  emitted as written. The runtime is also the npm package `@vela/runtime`,
  generated from `runtime/` into `packages/runtime` (drift test), with the
  compiler's version and the exports `.`, `./blocks` and `./hydrate`; its
  development check is folded by a bundler's `process.env.NODE_ENV`
  replacement.
  `--runtime-import=relative|bare` selects the form of runtime imports:
  `relative` (default) stages the runtime beside the output as in stage 1;
  `bare` imports the package and stages nothing, so a bundler resolves one
  runtime instance from `node_modules` (R-12.2b) and the contract marker
  (R-12.2d) fails at bundle time on a version mismatch. The project manifest
  lists the package imports of every generated module. The former plan of
  a runtime copy in `ssr_runtime` with `AssetsHandler()` / `HeadTags()` is
  withdrawn: serving and hashing belong to the application.
- **R-12.2g Stage 3 (publication).** The npm package is published with each
  compiler release, together with the Go runtime module (R-12.1c).

### 12.3 API additions

Added to the Go runtime package:

- `vela.Object` — insertion-ordered object with JS key enumeration order.
- `vela.Optional[T]` — host parameter type that keeps absent, `null`, and
  value apart.
- `vela.RenderError`, `vela.HostError`, `vela.ErrHostNotImplemented`.
- `vela.PublicError` and `vela.WithErrorObserver` (T-7.4h).
- `vela.Promise` — settled promise value for the synchronous model (T-7.4b).
- Dynamic-tier helpers covering every admitted operator and built-in,
  replacing type-specific helpers such as `NumberMultiply` where those
  currently take `any`.
- `RenderXContext` entry points (H-8.3c).

---

## 13. Delivery slices

Each slice ends with `go test ./...`, the strict browser runner, and an
updated `--ssr-report` for `test_projects`.

1. **IR and region.** Server IR, region analysis (T-2), site report
   (T-2.3, §10.4), and the event-handler exclusion. Behaviour parity with the
   current translator; the corpus report becomes the progress metric.
2. **Dynamic tier and initialisation.** T-5.4 and T-6.6d: `$state(prop)`,
   `$derived` chains, computed payload values, `*vela.Object`.
3. **Host bindings.** §8 complete with scaffolding, diagnostics (§10), and
   `RenderXContext`.
4. **Statements and modules.** §7: multi-statement helpers, `try` /
   `catch` / `finally`, `async` / `await` with the synchronous model and
   server-evaluated `{#await}` (§7.4), and local `.js` modules.
5. **Built-in catalogue.** §11.1 in frequency order from the corpus:
   strings, arrays, `Math`, `Number` formatting, `JSON.stringify`.
6. **Client boundaries.** §9: `{#client}` and client-only components.

Independent of the slices and needed before beta: build mode and
hydration verification (V-11.3a–d), the nested runtime module with the
contract check (§12.1), and JavaScript runtime stages 1 and 2 (§12.2).

---

## 14. Superseded proposals

- The inline Go escape hatch `{expr | go("…")}` of the earlier design (Option C) is
  withdrawn. Go code lives only in the host tree.
- The JSON `hostFunctions` manifest of the earlier design is replaced by `//vela:host`
  directives in Go (H-8.2b). Only the project-level paths stay in
  `vela.ssr.json`.
- The single-`return` restriction on local formatters becomes an
  optimisation (T-7.1a).
- `SSRTypeInt` as a translated value type is replaced by `number` →
  `float64`. Integer Go types remain valid for developer props and host
  parameters.
- Object literals lowered to `map[string]any` are replaced by
  `*vela.Object` (T-5.1c).
- The earlier design's capability row "Promise, async/await, timers, fetch —
  `client-deferred` by default" is replaced by §7.4: async code in the region
  runs synchronously on the server; timers and browser I/O are never native.
- "`{#await}` server-side renders pending" is replaced by T-7.4f for
  server-evaluated operands. String-only `AwaitSnapshot.Value` / `Error`
  become JSON values.

---

## 15. Decisions

Resolved on 2026-10-03:

1. Monomorphisation threshold is 4 variants for beta (T-7.2a); to be
   revisited with beta feedback.
2. `try` / `catch` / `finally` and `async` / `await` are in v1, using the
   synchronous execution model (T-6.5d, §7.4).
3. Client boundaries use the `{#client}…{:else}…{/client}` block; the
   fallback is optional; content mounts right after the owner's hydration;
   whole components can be declared client-only (§9).
4. Explicit `--mode=development|production`; hydration verification is on
   by default in development, with selectable `repair` / `fail` policies;
   structural sites are not compared in v1 (V-11.3a–d).
5. The Go runtime is a nested module `ssr_runtime/` with package name
   `ssr_runtime`, `v0.x` tags, and a compile-time contract check; the
   public module path stays configurable until the repository name is
   chosen (§12.1).
6. The JavaScript runtime moves to `runtime/` and is delivered in three
   stages: one relative copy now, bare specifier with import map and
   Go-served assets before beta, an npm package at publication (§12.2).

No decisions are open.
