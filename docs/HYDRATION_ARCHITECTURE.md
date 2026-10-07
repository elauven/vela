# Block runtime and universal hydration contract

Status: accepted direction, 2026-10-04. Scope owner: Task 007; replaces the
implementation approach of Task 029 and parts of the Task 027 artifact
contract (§11).

A Russian translation is kept in `HYDRATION_ARCHITECTURE.ru.md` with the same
rule identifiers. This English text is normative; update both together.

This document is the normative contract for how Vela builds, updates, and
adopts DOM structure in the browser. It replaces shape-specific hydration
recipes with one mechanism that works for any supported template structure
at any nesting depth. Expression translation and host implementations are
covered by `SSR_TRANSLATION_CONTRACT.md`; this document does not change them.

Rule identifiers: `U-` principles, `F-` fragments, `B-` blocks, `C-` cursor,
`M-` markup, `S-` payload stream, `X-` mismatches and rollback, `A-`
artifacts and API, `Q-` verification, `G-` migration.

---

## 1. Principles

- **U-1.1 One code path.** CSR mounting and hydration execute the same
  generated code. Hydration is a different DOM cursor, not a different
  generator.
- **U-1.2 Composition, not enumeration.** A structure is supported for
  hydration if and only if it is supported for CSR. No rule, predicate, or
  generator branch may depend on which block type contains which.
- **U-1.3 Generic blocks in the runtime.** Structural behaviour (`#if`,
  `#each`, `#key`, `#await`, components, slots, `{#client}`) is implemented
  once in the runtime. The compiler generates only fragments and
  dependency-aware update code.
- **U-1.4 Compile-time reactivity stays.** Setters, dirty flags, and the
  per-component flush keep their current design. Only the structural layer
  changes.
- **U-1.5 Smaller output.** Generated code must not contain per-site copies
  of structural algorithms (reconciliation, branch switching, cleanup).

### 1.1 Why the current model cannot scale

Today each structural block is generated inline as a state machine
(`$$chain_*`, `$$each_*`). The state of a nested block is hoisted to
component scope (`$$n7_a`, `$$n7_km`, `$$n7_bs`), and the enclosing block
creates and destroys the nested one by hand. Every pair "X inside Y" is
therefore glue written by the generator, and hydration had to repeat that
glue per pair: about 85 `buildHydration*` functions in `astgen.go`, shape
predicates in `ssr_capability.go`, and `ssr_nested_keyed_await_adoption.go`.
The number of pairs grows combinatorially with nesting depth.

### 1.2 Terminology

| Term | Meaning |
| --- | --- |
| Fragment | A generated function that builds the body of a component, branch, row, slot, or boundary through a cursor. |
| Block | A runtime object implementing a structural construct (§3). |
| Owner | The block or component instance that releases a resource when it is destroyed. |
| Range | The DOM nodes a block occupies, delimited by a start and an end comment. |
| Cursor | The object that supplies DOM nodes to fragments: created (CSR) or adopted (hydration) (§4). |
| Root | One `hydrate()` or `mount()` call and the tree under it. |
| Stream | The ordered payload of one root (§6). |

---

## 2. Fragments

A fragment is the only kind of structural code the compiler generates.

```js
// {#each items as item (item.id)} <li @click={() => pick(item)}>{item.name}</li> {/each}
function row(c, s) {                     // c: cursor, s: lexical scope of the row
  const li = c.element('li');
  c.listen(li, 'click', () => pick(s.item));
  c.enter(li);
  const t = c.text(`${s.item.name}`);
  c.exit();
  return {
    p(dirty) { if (dirty & ITEM) t.data = `${s.item.name}`; },
  };
}
```

- **F-2.1 Cursor only.** During mount a fragment obtains every DOM node,
  attribute value, listener, binding, and child block through its cursor. It
  never calls `document.createElement`, `appendChild`, `innerHTML`, or
  `addEventListener` directly.
- **F-2.2 Initial values through the cursor.** Initial text, attribute,
  property, class, and style values are passed to the cursor, never assigned
  directly. In create mode the cursor writes them; in hydrate mode it adopts
  the server value and, in development, verifies it (§7.2).
- **F-2.3 Deterministic sequence.** For given inputs, a fragment performs the
  same cursor calls in document order. The order is derived from the shared
  render plan, which also drives the Go renderer.
- **F-2.4 Scope.** Lexical bindings (`#each` item and index, `#await` value
  or error) are passed in a scope object. When a block reuses a fragment with
  new values (an updated row, a settled `then`), it updates the scope and
  calls `p`.
- **F-2.5 Update handle.** A fragment returns `{ p(dirty) }`. `p` applies the
  changes selected by the component's dirty mask and forwards it to the child
  blocks the fragment created. A fragment with nothing to update may return
  an empty handle.
- **F-2.6 No structural glue.** A fragment never references the internals of
  another fragment or block. It holds only the handles it created.

---

## 3. Blocks

### 3.1 Protocol

Every block implements:

```ts
interface Block {
  p(dirty: number): void      // update after a component flush
  move(before: Node): void    // move the whole range (used by keyed #each)
  destroy(): void             // release resources and remove the range
  abandon(): void             // release resources, leave the DOM untouched
}
```

A block is created through `c.block(kind, …)` and mounts immediately
through the given cursor.

- **B-3.1 Range.** A block owns a range between a start and an end comment.
  In create mode the cursor creates both comments; in hydrate mode it adopts
  the server markers (§5) as the range comments.
- **B-3.2 Ownership.** A block, listener, binding, child component, or
  deferred task created while a fragment mounts is registered with the
  current owner. Owners form a tree that mirrors the template structure.
- **B-3.3 Destroy.** `destroy()` releases owned resources in reverse
  registration order, then removes the block's range. Descendants inside a
  removed range release their resources but do not remove DOM individually.
- **B-3.4 Abandon.** `abandon()` releases owned resources in reverse
  registration order and invalidates pending callbacks and deferred work, but
  never mutates the DOM. It is the rollback primitive (§7.3).
- **B-3.5 Late work.** Callbacks scheduled by a destroyed or abandoned block
  (promise settlement, deferred mounts, flush work) are inert.
- **B-3.6 Updates after hydration.** After a root commits, every mount
  triggered by an update (a new branch, a new row, a recreated key subtree)
  uses a create cursor anchored inside the block's range.

### 3.2 Block catalogue

| Block | Created by | Behaviour |
| --- | --- | --- |
| `$$if(select, branches)` | `{#if}` / `{:else if}` / `{:else}` | `select()` returns the active branch index or `-1`. A change destroys the current branch and mounts the new one; the same index calls the branch's `p`. |
| `$$each(collection, key, row, fallback)` | `{#each}` with optional key and `{:else}` | Keyed: retain, move, create, and remove rows by key; preserve focus as current CSR does. Unkeyed: update rows in place by index. Each row is a block with its own range. `fallback` is mounted when the collection is empty. |
| `$$key(value, body)` | `{#key}` | Recreates the body when the value changes, using current CSR change semantics. |
| `$$await(operand, pending, then, catch)` | `{#await}` and its shortcut forms | Tracks the current operand; settlement of a stale operand is ignored. Hydration follows §6.4. |
| `$$component(factory, props, slots, events, bindings)` | a child component tag | Creates a child instance with its own owner. Parent updates are forwarded through the child's `set`. Component events and `bind:` to a `$bindable` prop keep their current semantics. |
| `$$slot(name, fallback)` | `<slot>` in a child | Mounts the parent-provided slot fragment, or `fallback` when none is provided. |
| `$$client(main, fallback)` | `{#client}` and client-only components | Contract `SSR_TRANSLATION_CONTRACT.md` §9. In hydrate mode it adopts the fallback range and mounts `main` with a create cursor after the root commits. In CSR it mounts `main` directly. |

- **B-3.7 Slot ownership.** A slot fragment is defined by the parent and
  mounted inside the child. It is owned by the child's `$$slot` block, which
  destroys it, and it receives the parent's dirty updates through a
  subscription that the `$$slot` block registers on the parent instance and
  removes on destroy.
- **B-3.8 Multiple roots.** In the first delivery, components keep their
  current CSR DOM shape, including the `display: contents` wrapper for
  multi-root templates. Whether to drop the wrapper is open (§12).

### 3.3 Generated component shape

- **B-3.9** A component module exports its public factory with the current
  signature (`Component(target, props, slots)` returning `{ el, set,
  destroy }`) and an internal entry used by `$$component` and `hydrate()`
  that mounts the component's root fragment through a given cursor.
- **B-3.10** Element-level features are cursor calls, not blocks: dynamic
  attributes, `class:`, `style:`, DOM events, `bind:value` / `bind:checked` /
  `bind:this`, and select bindings, including `<select multiple>`.

---

## 4. Cursor

### 4.1 API

The names are indicative; the semantics are normative.

| Call | Create mode | Hydrate mode |
| --- | --- | --- |
| `element(tag)` | create the element and insert it at the current position | take the next node; it must be an element with this tag |
| `text(value)` | create a text node with `value` | take the next node; it must be a text node (see C-4.3) |
| `static(id)` | clone a static subtree from a module-level template | skip the known number of top-level nodes of that subtree |
| `enter(el)` / `exit()` | move into / out of an element's children | same; on `exit` the element must have no remaining unconsumed children |
| `attr`, `prop`, `cls`, `style` | write the initial value | do not write; verify in development (§7.2) |
| `listen(el, event, handler)` | add the listener, register it with the owner | same |
| `bind(el, kind, read, write)` | write the state value to the control | adopt the live control value (C-4.5) |
| `block(kind, …)` | create range comments and mount the block | adopt the range markers and mount the block |

### 4.2 Rules

- **C-4.1 Two implementations, one interface.** Create and hydrate cursors
  are interchangeable for every fragment. The hydrate cursor lives in the
  runtime's hydration module (R-12.2c); CSR-only builds do not ship it.
- **C-4.2 Strict order.** The hydrate cursor consumes the server DOM strictly
  in document order. Any node it does not expect is a mismatch (§7).
- **C-4.3 Text runs.** At compile time every maximal run of adjacent static
  and dynamic text inside one parent becomes one text node whose value is a
  template (as current CSR already does with `` `${name} ` ``). Runs are
  always separated by elements or block markers, so the HTML parser never
  merges them. A run whose value is empty is not present in parsed HTML; the
  hydrate cursor then inserts an empty text node, and this is not a
  mismatch.
- **C-4.4 Mode switch.** A root uses a hydrate cursor only until it commits.
  From then on every mount uses create cursors (B-3.6).
- **C-4.5 Form controls.** Hydrating `bind:value` / `bind:checked` reads the
  live DOM value. If the user changed the control before hydration, the live
  value is written into state through the normal setter. This keeps the
  existing contract in `SSR.md`. `bind:this` is assigned after the element
  is adopted and cleared on destroy.

---

## 5. Markup protocol

The Go renderer writes HTML from the same render plan as the fragments, so
the node sequence of the server HTML equals the cursor sequence.

### 5.1 Grammar

```
range      = "<!--[" state "-->" content "<!--]-->"
state      = ""            ; #each, #key, component, slot, row
           | branch        ; #if: active branch index, decimal; "x" when none
           | "e"           ; #each: the {:else} fallback is rendered
           | "p" | "t" | "c"   ; #await: pending / then / catch
           | "f"           ; $$client: fallback is rendered
```

Example for the template in §2 inside `{#if open}<ul>…</ul>{/if}`:

```html
<!--[0--><ul><!--[--><!--[--><li>Apple</li><!--]--><!--[--><li>Pear</li><!--]--><!--]--></ul><!--]-->
```

- **M-5.1 Blocks only.** Markers appear only at block and row boundaries.
  Elements carry no markers and no instance identifiers; they are found by
  position.
- **M-5.2 Rows.** Each `#each` row is its own range, because a row may have
  several top-level nodes.
- **M-5.3 No data in markers.** Markers carry only the state token. Keys,
  values, and identifiers are never written into markers.
- **M-5.4 Reserved comments.** Comments whose text starts with `[` or `]`
  are reserved for markers. Author HTML comments in templates are not
  emitted in either CSR or SSR output.
- **M-5.5 Text runs.** No separator comments are used; C-4.3 makes them
  unnecessary.
- **M-5.6 Root delimitation.** A root is rendered as one range followed
  immediately by its payload script (§6).

### 5.2 Parse stability

A browser must parse the server HTML into exactly the node sequence the
cursor expects. The compiler guarantees this at compile time.

- **M-5.7 Normalise.** Constructs the parser changes in a fixed way are
  normalised in both outputs: an explicit `<tbody>` is inserted for table
  rows written directly in `<table>`. The leading newline that the parser
  drops after `<pre>` and `<textarea>` is compensated in the Go output.
- **M-5.8 Reject.** Constructs the parser rearranges are compile errors with
  a source position and a fix (`VELA-HTML-NESTING`):
  - text or a block directly inside `<table>`, `<thead>`, `<tbody>`,
    `<tfoot>`, or `<tr>` where it would be foster-parented;
  - block-level elements inside `<p>`;
  - `<a>` inside `<a>`, `<form>` inside `<form>`, `<button>` inside
    `<button>`;
  - elements inside `<option>`, children of void elements.
- **M-5.9 Text-only elements.** Inside `<textarea>`, `<title>`, `<script>`,
  and `<style>` only one text run is allowed and no blocks; markers cannot
  exist there. `<textarea>` content is handled as a form value (C-4.5).
- **M-5.10 Whitespace.** Whitespace handling is decided once, in the render
  plan. Go output and fragments use the decided text runs and never trim or
  collapse whitespace independently.

---

## 6. Payload stream

### 6.1 Format

One script per interactive root, immediately after the root range:

```html
<script type="application/json" data-vela-root>
{"v":2,
 "props":{"title":"Team","users":[{"id":1,"name":"Ann"},{"id":2,"name":"Bob"}]},
 "entries":[
   ["s","Page",{"open":true}],
   ["s","Header",{}],
   ["s","UserCard",{"expanded":false}],
   ["s","UserCard",{"expanded":false}],
   ["a","Page#3",{"state":"t","value":{"total":2}}]
 ]}
</script>
```

- **S-6.1 One stream per root.** No per-component scripts, no instance
  identifiers.
- **S-6.2 Root props.** `props` holds the root component's declared props
  after defaults (existing allow-list and serialisation rules, including the
  distinct representation of `undefined`).
- **S-6.3 Child props are not serialised.** The client computes them by
  running the parent's code.
- **S-6.4 Entries.** In render order:
  - `["s", component, state]` — written when a component instance starts
    rendering: the initial values of its `$state`, `$state.deep`, and
    `$bindable` bindings. Every rendered instance has an entry, even an empty
    one, so that order is verifiable.
  - `["a", site, snapshot]` — written when a server-evaluated `{#await}`
    renders (T-7.4f). `site` is the compile-time await site identifier.
- **S-6.5 Nothing for client-only code.** The main branch of `{#client}` and
  client-only components produce no entries.
- **S-6.6 Encoding.** Values follow the JSON view rule (T-5.1). The script
  content is escaped so that it cannot close the script element or open a
  comment (`<`, `>`, `&`, U+2028, U+2029 escaped).

### 6.2 Consumption

- **S-6.7 Order, not identity.** The client consumes entries in the order in
  which the cursor reaches the corresponding component or await block. The
  component name or site in each entry must match what the client expects;
  otherwise it is a mismatch.
- **S-6.8 Restored state.** In hydrate mode a component takes its `$state`
  initial values from its entry, and its `$state` initializer expressions
  are **not evaluated**. This is what prevents duplicate data loading and
  repeated host-equivalent work on the client. `$derived` values and prop
  defaults are computed normally.
- **S-6.9 Removal.** The payload script is removed after the root commits.

### 6.3 Keyed lists

- **S-6.10** A hydrating keyed `$$each` computes keys from the restored
  collection and adopts row ranges in order. A row count different from the
  collection length is a mismatch. Duplicate keys behave as in CSR.

### 6.4 Await blocks

- **S-6.11** A hydrating `$$await` reads its `a` entry. For `t` and `c` it
  adopts the settled branch with the serialised value or error and does
  **not** evaluate the operand. The operand is evaluated again only when its
  dependencies change. For `p` (rendered only when a caller supplied a
  pending snapshot through `RenderOptions.Await`), it adopts the pending
  branch and evaluates the operand after the root commits.

---

## 7. Mismatches, verification, and rollback

### 7.1 Detection

- **X-7.1** The hydrate cursor checks, at no extra cost: node kind and tag,
  marker grammar, the `#if` branch index against `select()`, the `#each` row
  count, the `#await` state against the entry, unconsumed children on
  `exit`, and the kind and name of each stream entry.

### 7.2 Value verification

- **X-7.2** In development builds the cursor also compares the initial text,
  attribute, property, class, and style values computed by the client with
  the adopted server values (V-11.3b). Production builds contain no value
  verification code.

### 7.3 Policies

| Situation | `repair` (development default) | `fail` (CI, tests) | Production |
| --- | --- | --- | --- |
| Structural mismatch | The nearest enclosing block abandons its server range and remounts with a create cursor; console warning with source position. | Abandon the whole root (B-3.4) and throw `VELA-HYDRATION-MISMATCH`. | Same as `repair`, with a single `console.error` carrying the code and no source position. |
| Value mismatch | Write the client value; console warning. | As above. | Not checked. |

- **X-7.3 Root mismatch.** A mismatch at the root range itself remounts the
  whole root. This is the only case in which hydration clears the
  container.
- **X-7.4 Rollback.** `fail` and any thrown error during hydration call
  `abandon()` on the root owner tree. Server DOM and payload remain for a
  corrected retry. Independently hydrated roots are unaffected. A retry
  creates exactly one live instance. This satisfies the Task 027 rollback
  contract generically.
- **X-7.5 Reporting hook.** `hydrate()` accepts an `onMismatch(report)`
  option that receives every mismatch in every mode. A report has `code`,
  `kind`, `message`, and `policy`; in development builds also `site`,
  `source` (`file:line:column`), `host` when the site calls a host-bound
  function, and `server` and `client` for value mismatches (V-11.3b). The
  compiled fragments name each adopted text, attribute, and block site with
  `$$c.at($$sites[n])` from a per-module site table that `--minify`
  builds omit.

---

## 8. Roots, artifacts, and API

### 8.1 Root modes

- **A-8.1 Two modes, per root.** A component used as a root renders in one
  of two modes:
  - `interactive`: markers, payload stream, and a client module;
  - `static`: plain HTML without markers or payload, and no JavaScript.
- **A-8.2 Static detection.** A component is `static` as a root when its
  whole component tree contains no DOM events, no `bind:` directives
  (including `bind:this`), no `$effect`, no lifecycle hooks, no `{#client}`
  block, and no client-only component. Without these nothing can change
  after rendering. `{#await}` with server-evaluated operands is allowed.
- **A-8.3 Static rendering of interactive components.** Every generated
  renderer also exposes a static variant that writes plain HTML without
  markers or payload even for an `interactive` component. Use cases: e-mail,
  feeds, pre-rendered fragments.
- **A-8.4 No per-component modes.** The per-component artifact modes
  `executable`, `static-only`, and `client-boundary` are removed. Every
  component that compiles for CSR hydrates, subject to the translation
  outcomes of `SSR_TRANSLATION_CONTRACT.md`.

### 8.2 Go API

```go
func RenderPage(w io.Writer, props PageProps, slots PageSlots) error
func RenderPageContext(ctx context.Context, w io.Writer, props PageProps, slots PageSlots) error
func RenderPageStatic(ctx context.Context, w io.Writer, props PageProps, slots PageSlots) error
func RenderPageWithOptions(ctx context.Context, w io.Writer, props PageProps, slots PageSlots, options PageRenderOptions) error
```

- **A-8.5** `RenderX` / `RenderXContext` write the root in its mode (A-8.1).
  `RenderXStatic` implements A-8.3. `RenderXWithOptions` keeps
  `Await` overrides (T-7.4f), keyed by await site identifier of the root
  component.
- **A-8.6** Instance-identifier APIs (`RenderXAt`, `XAt`, the `instanceID`
  parameter) are removed; instances are not identified in markup or payload.

### 8.3 Client API and outputs

```js
import { hydrate, mount } from 'vela/runtime';
import Page from './Page.js';

hydrate(Page, document.getElementById('app'), { onMismatch: report => … });
```

- **A-8.7** `hydrate(Component, container, options)` expects exactly one root
  range and its payload script inside `container`. Several roots on a page
  use several containers.
- **A-8.8** Each component compiles to one client module. The separate
  `X.hydrate.js`, `X.hydrate_client.js`, and per-component `X.ssr.json` files
  are removed. One `vela.manifest.json` lists, for every component, its root
  mode and client module.

---

## 9. Interaction with other contracts

- `{#client}` and client-only components (T-9.1, T-9.2) are the `$$client`
  block (§3.2).
- Server-evaluated `{#await}` (T-7.4f) uses `a` entries (§6.4).
- Development verification and its policies (V-11.3) are implemented by the
  hydrate cursor (§7).
- The runtime module split (R-12.2a, R-12.2c): blocks and the create cursor
  are in the core and DOM modules; the hydrate cursor and `hydrate()` are in
  the hydration module; value verification is in the development module.
- The render plan remains the single source of node order for the Go
  renderer and the fragments (F-2.3, M-5.10).

---

## 10. Verification

- **Q-10.1 CSR gate.** All existing CSR suites (`runtime_behaviour`,
  `runtime_nesting_matrix`, codegen tests, browser runtime tests) pass with
  the block engine without changing their expectations, except expectations
  about generated code text, which are replaced by behaviour checks.
- **Q-10.2 Generative differential test.** A grammar-based generator
  produces templates from every block, element-level feature, component,
  slot, and `{#client}`, nested up to a configurable depth, plus random data.
  For each case the strict browser runner checks:
  1. the CSR DOM equals the DOM produced by Go SSR followed by `hydrate()`
     (normalised structure and text);
  2. hydration performs no DOM mutations other than the empty-text inserts
     of C-4.3 (observed with `MutationObserver`);
  3. adopted nodes keep identity;
  4. after each step of a random update sequence applied to both sides, the
     DOMs are equal again;
  5. after `destroy()` no listeners, payload scripts, or pending callbacks
     remain.
  Seeds are recorded; every failure becomes a fixed regression case.
- **Q-10.3 Mismatch suite.** Injected mismatches of each kind in X-7.1 under
  `repair`, `fail`, and production builds produce the behaviour in §7.3,
  including rollback and retry.
- **Q-10.4 Parse stability suite.** Every normalisation and rejection in
  §5.2 has a positive or negative case.
- **Q-10.5 Corpus.** Components of `test_projects` that pass translation
  hydrate without mismatch.
- **Q-10.6 Size report.** Total generated client bytes for `test_projects`
  and `examples` are reported for the legacy and block engines. A report,
  not a gate.

---

## 11. Migration

- **G-11.1 Flag.** `--dom-engine=legacy|blocks` selects the engine for CSR
  code generation, Go markup, payload, and hydration together. The default
  stays `legacy` until gate H3 passes.
- **G-11.2 Recipe freeze.** From the acceptance of this contract no new
  shape-specific hydration recipe is added to the legacy engine.

| Phase | Content | Exit gate |
| --- | --- | --- |
| H0 | This contract; generator harness skeleton | Contract accepted |
| H1 | Runtime blocks, create cursor, fragment code generation; CSR only | Q-10.1 under `--dom-engine=blocks`; size report |
| H2 | Go renderer markup (§5) and payload stream (§6) from the render plan; parse-stability checks | Generated HTML parses in Chromium to the expected node sequence for the generator corpus; Q-10.4 |
| H3 | Hydrate cursor, `hydrate()`, mismatch policies, root modes and API (§8) | Q-10.2, Q-10.3, Q-10.5; strict runner required IDs migrated; default switches to `blocks` |
| H4 | Remove the legacy engine | Legacy code and tests deleted (G-11.3) |

- **G-11.3 Removal list (H4).** `ssr_capability.go` shape predicates (only
  translation outcomes and parse-stability checks remain); `buildHydration*`
  and `hydrationAwaitSnapshot*` functions in `astgen.go`;
  `ssr_nested_keyed_await_adoption.go`; `DOMAcquirePlan`
  (`dom_acquire_plan.go`); inline `$$chain_*` / `$$each_*` generation; the
  `e:` / `v:` marker emitters; per-instance payload code; the runtime
  functions `$$createHydrationWalker`, `$$acquireHydration*`,
  `$$adoptHydrationKeyed*`, `$$reconcileHydrationKeyed*`, and related
  helpers; `.hydrate.js` / `.hydrate_client.js` emitters.
- **G-11.4 Tasks.** Task 029 is re-scoped to phases H1–H3 of this contract;
  its positive matrix becomes the generator corpus. Task 027's hydrate-entry
  and per-component payload contracts are superseded by §6 and §8; its
  rollback contract is met by X-7.4. Task 031 runs on the block engine. Task
  023 (incremental row updates) becomes an internal concern of `$$each`.

### 11.1 Superseded

- Element markers `<!--e:id:N-->` and text-range markers `<!--v:id:N-->`.
- Per-instance payload scripts `vela-state-<id>` and instance identifiers.
- `RenderXAt`, `XAt`, and `instanceID` parameters.
- Per-component artifact modes `executable`, `static-only`,
  `client-boundary` and their hydrate entries.
- `SSRCapability` shape analysis as a hydration gate.
- `RenderOptions.Await` keyed by marker number (now keyed by await site).

---

## 12. Open questions

1. Whether components drop the `display: contents` wrapper for multi-root
   templates once they are ranges (B-3.8). It changes CSR DOM shape and CSS
   selectors, so it is a separate decision after H3.
2. Generator depth and case count for CI and for nightly runs (Q-10.2).
3. Shape of the mismatch report passed to `onMismatch` (X-7.5).
4. Whether `RenderOptions.Await` can address await sites inside child
   components, and how (A-8.5).
