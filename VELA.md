# Vela Compiler — Reference

A Svelte-like single-file-component (SFC) compiler written in Go. Takes
`.vela` files and emits self-contained ES module JavaScript. Roughly
24,000 lines including tests; ~17,000 lines of compiler proper plus a
small JS runtime.

This document is the working reference. It describes what's in the
repo, how the compilation pipeline is organized, the contract between
compiled components and the runtime, the template syntax that's
currently supported, the testing strategy, and lessons accumulated
from successive feature work. Read top-to-bottom on first contact;
treat as lookup after that.

For a feature-by-feature comparison against Svelte 5, see
`SVELTE_COMPARISON.md`. For the in-progress SSR design, see `SSR.md`.

---

## Top-level layout

```
codegen.go          ~4925 LOC   walks template AST → emits component JS
vela_test.go        ~5435 LOC   integration + runtime tests
parser.go           ~1351 LOC   .vela → SFC{script, style, template AST}
jsparser.go         ~1235 LOC   ES2020 JS parser (analyzer + diagnostics)
scope.go            ~1193 LOC   scope graph, TDZ analysis, hoisting
es_audit_test.go    ~ 875 LOC   ES2020 conformance audit (87 features)
cssparser.go        ~ 724 LOC   <style> → scoped CSS with vela-XXXXXX attrs
lexer.go            ~ 710 LOC   JS lexer (shared by parser + diagnostics)
analyzer.go         ~ 666 LOC   <script> → Analysis (props, state, derived…)
rewriter.go         ~ 624 LOC   identifier rewrites for setter wrapping
fraggen.go          ~ 484 LOC   fragment generator for {#each} rows
reactivity.go       ~ 485 LOC   reactive dependency tracking
main.go             ~ 432 LOC   CLI entry, project mode, watch
diagnostics.go      ~ 402 LOC   structured compile-time messages
dce.go              ~ 381 LOC   dead-code elimination pass
jsast.go            ~ 352 LOC   JS AST node types
ast_json.go         ~ 293 LOC   --ast debug JSON emission
imports.go          ~ 236 LOC   .vela import path rewrite → .js
unreachable.go      ~ 225 LOC   unreachable-branch detection
minifier.go         ~ 196 LOC   single-pass char-level JS minifier
runtime.js          ~ 270 LOC   stores, batch flush, $$defer, $$tmpl, mount

Test files:
  codegen_*_test.go              feature-specific regression tests
  reactivity_test.go             reactive graph tests
  scope_test.go                  scope analyzer tests
  dce_test.go                    dead-code elimination tests
```

**Working tree:** `/home/claude/vela/` during sessions.
**Test pass count:** 352 tests, 0 failures.
**ES2020 conformance:** 87/87 features pass the audit.

---

## Compilation pipeline

```
.vela source
    │
    ▼
parseSFC(src)           → splits into <script>, <style>, <template>
    │
    ├── parseTemplate(template) ─→ Node tree (Element, IfChain, Each,
    │                              Component, Slot, Text, AwaitBlock)
    ├── analyzeScript(script)    ─→ Analysis (StateVars, DerivedVars,
    │                              Effects, Imports, CleanScript)
    └── parseCSS(style, scopeID) ─→ scoped CSS with [vela-XXXXXX] attrs
    │
    ▼
CodeGen.Emit(SFC, Analysis)
    │
    ├── walkChildren / walkNodes recursively descend the tree
    │      ─→ buffers: create, wire, events, init, cleanup, update
    │      ─→ updates map: { reactiveVar → []updateStmt }
    ├── genSetter(name)   one $$set_X per state/prop (bundles updates)
    └── final assembly: imports + scoped <style> + DOM + setters + mount + destroy
    │
    ▼
component .js (ES module)
```

The compiler is **mostly stateless** per component — every component
compiles into a self-contained JS function with no shared global
state besides the runtime symbols.

---

## .vela template syntax — current state

A `.vela` file has three sections in any order, identified by
`<script>`, `<style>`, `<template>` tags. The scanner is
case-insensitive and tolerates attributes on those tags.

### Script section

```vela
<script>
  // props (compiled to setters + initial value from props arg)
  export let title    = '';
  export let open     = $bindable(false);   // two-way binding from parent

  // state — shallow (assignment-only reactivity)
  let count = $state(0);
  let visible = $state(false);

  // state — deep (Proxy-wrapped, tracks nested mutations)
  let items = $state.deep([{ name: 'apple', count: 1 }]);

  // derived (recomputed when deps change)
  let doubled = $derived(count * 2);
  let canSubmit = $derived(
    username.trim().length > 0 &&
    password.length > 0 &&
    !loading
  );

  // effects (run after initial mount + after each dep change)
  $effect(() => {
    document.title = `Count: ${count}`;
  });

  // lifecycle hooks
  onMount(() => { /* ... */ });
  onDestroy(() => { /* ... */ });

  // event dispatcher
  function close() {
    $$dispatch('close', { reason: 'user' });
  }

  // imports — .vela paths get rewritten to .js
  import { auth } from './stores/auth.js';
  import { Card } from './components/Card.vela';
</script>
```

### Style section

```vela
<style>
  .card { padding: 1rem; }
  .card-title { font-weight: 700; }
</style>
```

Styles are **automatically scoped** by appending a 6-hex-char
attribute selector (`[vela-XXXXXX]`) to each rule and adding the
same attribute to every emitted element in the template.

### Template section — full feature list

```vela
<template>
  <!-- Interpolation -->
  <p>{title}</p>
  <p>{a + b}</p>                        <!-- arbitrary expression -->

  <!-- Conditional rendering -->
  {#if visible}
    <p>Hello</p>
  {:else if other}
    <p>Other</p>
  {:else}
    <p>Nothing</p>
  {/if}

  <!-- List rendering with optional index and key -->
  {#each items as item, i (item.id)}
    <li>{i}: {item.name}</li>
  {:else}
    <li>No items</li>                   <!-- empty-state fallback -->
  {/each}

  <!-- Re-create children when expr changes identity -->
  {#key userId}
    <UserCard id={userId} />
  {/key}

  <!-- Async with pending / resolved / rejected branches -->
  {#await fetchData()}
    loading...
  {:then result}
    <p>{result.name}</p>
  {:catch err}
    <p>error: {err.message}</p>
  {/await}

  <!-- Event handlers -->
  <button @click={handler}>Click</button>
  <input @input={onInput} />

  <!-- One-way attribute binding -->
  <a :href={url}>link</a>
  <button :disabled={!canSubmit}>OK</button>

  <!-- Two-way binding to a DOM input -->
  <input bind:value={username} />
  <input type="checkbox" bind:checked={agree} />

  <!-- Element reference -->
  <input bind:this={inputEl} />

  <!-- Two-way binding to a component prop (declared $bindable in child) -->
  <Counter bind:count={appCount} />

  <!-- Class toggle directive (preserves static class=) -->
  <div class="card" class:active={isActive} class:danger={hasError}>...</div>
  <div class:active>...</div>           <!-- shorthand for class:active={active} -->

  <!-- Inline style directive (hyphenated names, CSS custom properties OK) -->
  <div style:color={theme}>...</div>
  <div style:font-size="{size}px">...</div>
  <div style:--theme-bg={bg}>...</div>

  <!-- Slots -->
  <slot />                              <!-- default slot -->
  <slot name="actions" />               <!-- named slot -->

  <!-- Component with slot fill -->
  <Card title="Settings">
    <p>Body content</p>
    <button slot="actions">Save</button>
  </Card>

  <!-- Component event listener -->
  <Modal :open={isOpen} @on:close={() => isOpen = false} />

  <!-- svelte:fragment for slot content without a wrapper element -->
  <Card>
    <svelte:fragment slot="actions">
      <button>OK</button>
      <button>Cancel</button>
    </svelte:fragment>
  </Card>
</template>
```

### Multi-root templates

Templates with more than one top-level element are wrapped in an
invisible `<div style="display:contents">`. Without this, sibling
elements would be nested inside the first — causing event bubbling
bugs and malformed DOM. A single top-level element remains
unwrapped.

---

## Runtime contract

Each emitted module exports a default function with the component
name:

```js
import { ComponentName } from './ComponentName.js';

const instance = ComponentName(targetEl, props, slots);
instance.set({ propName: newValue });   // push prop updates
instance.destroy();                      // teardown
```

The component function:
1. Creates DOM (`document.createElement`, `createTextNode`,
   `setAttribute`, `appendChild`)
2. Attaches event listeners
3. Runs effects (post-mount)
4. Returns an instance with `.set()` and `.destroy()`

The runtime (`runtime.js`) provides:
- `$$defer(fn)` — batched DOM update queue (microtask-flushed)
- `$$store_subscribe(store, callback)` — store integration
- `$$dispatch(name, detail)` — custom event dispatch
- `$$tmpl(html)` — template element factory (cached)
- `$$insertHTML(target, html)` — innerHTML helper that handles
  DocumentFragment targets correctly

---

## Reactivity model

### Shallow vs deep state

```vela
let counter = $state(0);              // shallow: reassignment only
let items = $state.deep([1, 2, 3]);   // deep: nested mutations tracked
```

**Shallow** (`$state`): the compiler rewrites assignments to the
variable (`counter = 5`, `counter++`) into setter calls
(`$$set_counter(...)`). Property mutations (`obj.a = 1`,
`arr.push(x)`) are invisible to the compiler — they don't trigger
updates.

**Deep** (`$state.deep`): the initial value is wrapped in a `Proxy`
with `set` and `deleteProperty` traps that fire updates on any
mutation. Nested objects are lazily wrapped on access, cached in a
`WeakMap` so identity (`obj.a === obj.a`) is preserved.

The setter for a deep state is split into two functions:
- `$$set_X` — handles reassignment; re-wraps the new value
- `$$set_X_notify` — runs the actual update body (DOM, derived,
  effects); shared between reassignment and proxy traps

Plain `$state` components emit **zero** proxy machinery, so there's
no overhead unless the developer opts in.

### Two-way binding

`bind:value={x}` on an input emits both directions:
- DOM write: `input.value = (x)` on init and on any update to `x`
- Input listener: `input.addEventListener('input', e =>
  $$set_x(e.target.value))`

`<Child bind:foo={y}>` on a component emits:
- Prop push: `{ foo: y }` on initial render and on subsequent
  `child.set()`
- Callback: `$$onChange_foo: ($v) => $$set_y($v)` — the child's
  setter for `foo` calls this when the value changes

To prevent cycles, **every generated setter starts with `if (X ===
$$v) return`**. This short-circuits the ping-pong that would
otherwise occur when parent → child → parent → child fires
indefinitely.

### Derived values

`let x = $derived(expr)` becomes:
- A `let x` declaration at module scope
- An updater function `$$upd_x = () => { x = expr; }`
- Registration as an update dep for every reactive name `expr`
  reads from

Recomputation is recursive: if `derivedA` depends on `derivedB`
which depends on state `s`, then `$$set_s` calls `$$upd_derivedB`
and `$$upd_derivedA` in dependency order.

### Effects

`$effect(() => body)` becomes a function `$$effectN` that:
- Runs once on initial mount
- Re-runs whenever any of its dependencies' setters fire
- Cleanup return is honored: `$effect(() => { const id =
  setInterval(...); return () => clearInterval(id); })`

---

## Block lowering details

### `{#if}` / `{:else if}` / `{:else}`

All branches lowered to a single `IfChainNode` with `Branches[]`.
Compiles to:
- A persistent anchor comment in the DOM
- A `$$chain_X()` function that re-evaluates conditions, mounts
  the active branch, destroys the previously-active one
- Per-branch cached element vars (`$$chain_X_b0`,
  `$$chain_X_b1`, ...) that survive across re-runs
- Reentrancy guard: if `$$chain_X` is called while already
  running, set a `_pending` flag and return; the running call
  loops if needed

### `{#each items as item, i (key)}`

Lowered to an `EachBlockNode`. Compiles to:
- A row factory function (one per `{#each}` block) emitting a
  fragment per item
- An `$$each_X(items)` reconciler that diffs against previous
  rows by key (or by index if no key)
- Keyed: removed/inserted rows; reused rows update in place
- Unkeyed: positional update — row N gets new data, surplus
  rows removed, missing rows added

### `{#key expr}`

Lowered to a one-branch `IfChainNode` with `KeyExpr` set. The
chain function emits an extra check at the top: compare the
cached prev-key against the current expression value; if
different, force-remove the branch element, null its var, then
fall through to the normal mount path.

### `{#await promise}` / `{:then v}` / `{:catch e}`

Lowered to an `AwaitBlockNode` and codegen-lowered to:
- 3-branch synthesized `IfChainNode` gated by
  `$$promState_X` (0=pending, 1=resolved, 2=rejected)
- Component-scope storage: `$$awaitVal_X`, `$$awaitErr_X`,
  `$$promCell_X` (the current promise reference for staleness
  guard)
- A `$$awaitDrive_X()` function that calls `Promise.resolve(expr)`,
  flips state to 0, runs the chain, then attaches `.then`/`.catch`
- Resolved/rejected callbacks short-circuit if `$$promCell !==
  capturedPromise` (the user reassigned between settle attempts)
- `{:then VALUE}` references to `VALUE` are AST-rewritten to
  `$$awaitVal_X` so the chain codegen doesn't need to know
  about await scope
- The driver is registered as an update for the promise
  expression's deps, so reassigning the promise re-attaches
  handlers

### Slots

Default and named slots are both supported. The child component
emits a `$$slots` parameter and walks it:

```js
function Card(target, props, $$slots) {
  // ...
  if ($$slots['actions']) $$slots['actions']($$slot_actions_target);
  if ($$slots['']) $$slots['']($$default_target);
}
```

The parent emits per-slot factory functions that build the slot
content into a `DocumentFragment` for the child to mount.

---

## Test files

| File | What it covers |
|---|---|
| `vela_test.go` | Integration tests, fixture mounts, end-to-end |
| `es_audit_test.go` | ES2020 feature coverage (87 features) |
| `scope_test.go` | Scope graph, TDZ analysis, hoisting |
| `reactivity_test.go` | Reactive dependency tracking |
| `dce_test.go` | Dead-code elimination |
| `codegen_reactivity_test.go` | Setter emission, derived recomputation |
| `codegen_runtime_test.go` | Runtime helper invariants |
| `codegen_slot_reopen_test.go` | Slot reopen fix (component re-init) |
| `codegen_bindings_test.go` | bind:value, bind:checked, bind:this |
| `codegen_component_bind_test.go` | Two-way component binding |
| `codegen_deep_state_test.go` | `$state.deep` proxy behavior |
| `codegen_directives_test.go` | `class:` and `style:` directives |
| `codegen_blocks_test.go` | `{#key}`, `{#await}`, multi-root wrapper |

All 352 tests pass on every change. `go vet` is clean.

---

## What's NOT in Vela

Useful to know when comparing against Svelte 5 (see
`SVELTE_COMPARISON.md` for full details):

- **No snippets** (`{#snippet}` / `{@render}`) — Svelte 5's slot
  replacement. Vela uses the older named-slot model.
- **No transitions** (`transition:`, `in:`, `out:`) — no animation
  primitives.
- **No `use:action`** — no DOM-action escape hatch for third-party
  libraries.
- **No dynamic components** (`<svelte:component this={Comp}>`).
- **No `<svelte:self>`** — no recursive self-reference.
- **No `<svelte:head>`, `<svelte:window>`, `<svelte:document>`**.
- **No `{@html}`, `{@const}`, `{@debug}`** — no escape hatches for
  raw HTML, scoped constants, or debug breakpoints.
- **No `bind:group` / `bind:files` / `bind:innerHTML`** — only the
  four bindings listed above are supported.
- **No `$state.raw` or `$derived.by`** — Vela has `$state` and
  `$state.deep` (opposite default from Svelte) and only the
  expression form of `$derived`.
- **No `$effect.pre` or `$effect.root`** — only the base `$effect`.
- **No SSR** — client-side only. See `SSR.md` for the design under
  consideration.

---

## Lessons (accumulated across sessions)

1. **Brace-unwrap discipline.** Attribute values arrive from the
   parser still wrapped in `{...}`. Every site that uses the value
   as a JS expression must call `unwrapBraces()` first. Forgetting
   this produced multiple subtle bugs (the broken `bind:value`
   listener gating, the static-attr-leak in branch elements).
   `attrExprToJS` centralizes the handling for the three shapes:
   pure expression, plain string, interpolated string.

2. **Multi-root structural bug.** For a long time, templates with
   multiple sibling root elements were nesting the siblings inside
   the first one — leading to malformed DOM and event bubbling
   bugs. Now wrapped in `display:contents` div when needed. Found
   only when testing `{#await}`, but affected any multi-root
   component.

3. **Setter cycle break.** Two-way component binding (parent ↔
   child) infinite-loops without an idempotency check. Every
   generated setter starts with `if (X === $$v) return`. Also a
   free perf win — redundant DOM updates skipped.

4. **`isFullyStatic` gates flattening.** An element with ANY
   dynamic attribute (event, bind, two-way, bind:this, class
   directive, style directive) must not be flattened into a static
   HTML string. Adding a new directive type means adding it to this
   predicate, or the element silently gets baked into HTML as a
   literal attribute.

5. **Proxy cache identity.** A naive deep-state Proxy implementation
   creates a new wrapper on every property access, breaking
   `obj.a === obj.a` equality. The `WeakMap`-cached `wrap()`
   helper inside `$$proxy` preserves identity.

6. **The dual-emission walks.** Five sites in `codegen.go` build
   component-prop objects: top-level component, if-branch
   component, each-row component, template-with-holes component,
   chain-branch component. They all duplicate the same pattern.
   The `componentPropsEntries` helper centralizes this; future
   changes should go through it.

7. **Runtime testing catches what static testing misses.**
   `node --check` validates syntax but not semantics. End-to-end
   jsdom mount tests caught multiple bugs that compiled and parsed
   cleanly (init ordering, TreeWalker invalidation, slot reopen,
   two-way binding direction).

---

## CLI

```
vela [flags] FILE.vela [FILE.vela ...]

Flags:
  --out DIR         output directory (default: ./out)
  --project DIR     compile all .vela files in a project tree
  --watch           recompile on file changes
  --minify          run the minifier pass
  --ast             emit JSON AST alongside JS output
  --css MODE        CSS delivery: 'inject' (default, CSS-in-JS) or
                    'external' (scoped CSS written to sibling .css files)
```

Output for `Card.vela`:
```
out/
  Card.js
  Card.css            (if <style> section present)
```

---

## Glossary

- **SFC** — single-file component (the `.vela` file with three
  sections).
- **Anchor** — `document.createComment(...)` node used as a marker
  for where dynamic content should insert.
- **Chain function** — `$$chain_$$nN()`, the recompute-and-reconcile
  function for an `{#if}` block. Always reentrant-guarded.
- **Branch var** — `$$chain_$$nN_bI` — the live DOM fragment for
  branch I of chain N. `null` when the branch isn't active.
- **Reactive setter** — `$$set_X($$v)`, generated by `genSetter` for
  each state and prop. Updates the variable, fires `$$upd_*` for
  downstream derived, schedules DOM updates via `$$defer`.
- **`$$defer`** — pushes a fn to the runtime queue. Inside a batch:
  fn runs when batch unwinds. Outside: fn runs at end of current
  microtask.
- **`g.create`** — code that creates DOM nodes.
- **`g.init`** — code that sets initial dynamic content. Runs
  AFTER derived initializers, so it can safely read derived
  values.
- **`g.cleanup`** — destroy-side code: removeEventListener, effect
  cleanups.
- **`g.updates[depName]`** — list of update statements to run when
  `depName` changes. Collected during the tree walk, emitted into
  setters by `genSetter`.
- **Deep state** — a `$state` variable declared via `$state.deep(...)`
  whose initial value is wrapped in a recursive Proxy. Mutations to
  any property fire the same notify path as reassignment.
- **Driver** — for `{#await}` blocks, the function that attaches
  `.then`/`.catch` to the promise and re-runs the chain on settle.
- **Staleness guard** — for `{#await}` blocks, the
  `if (promCell !== capturedPromise) return` check in the
  resolved/rejected callbacks. Prevents an old in-flight promise
  from clobbering a freshly-reassigned one.
