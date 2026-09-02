# Vela SSR — Design Document

This document captures the working design for Vela's server-side
rendering (SSR) capability. It records both the implemented Phase 1/2
contracts and the remaining phases, so implementation cannot drift
from the compiler architecture.

The reader should already be familiar with the current state of the
compiler (see `VELA.md`).

---

## The vision

The end-state goal is **not** "Vela has an SSR mode like Svelte's."
It's "Vela becomes a self-contained Go-native toolchain that doesn't
need Node for anything." SSR is one component of that — the runtime
side — and it should be a Go program because that's what the rest of
the stack will be.

That reframes what the SSR compiler emits. The compiler doesn't
produce a JS function that runs in Node and returns HTML. It
produces a **Go function** that takes typed data and returns HTML.

The developer writes:
1. `.vela` source files (as today)
2. A Go program that imports the generated server code, fetches its
   own data, and wires routes

The compiler bridges the two by generating typed Go renderers from
the same `.vela` source that produces the client JS.

---

## Three meanings of "SSR"

Worth distinguishing because they're not the same product:

**1. Static rendering** — given a component + props, produce an HTML
string. No interactivity, no JS sent to browser. Use case: marketing
pages, blog posts, content that doesn't change.

**2. SSR with hydration** — server produces HTML; same component code
runs on client and "adopts" the existing DOM instead of recreating it.
This is what Svelte/React/Vue mean by SSR.

**3. SSR with streaming / partial hydration / islands** — different
routes/parts render differently. Astro, Next.js App Router. The most
ambitious tier.

Vela targets **#2 (SSR with hydration)** as the eventual goal, built
on top of **#1 (static rendering)** as the foundation. **#3 is
explicitly out of scope** for the foreseeable future — it's a
multi-month effort that's only worth doing for teams the size of
Vercel.

---

## Output structure

For each `Foo.vela`, the compiler emits three groups of output:

```
out/
  styles/
    Foo.css                          per-component scoped CSS

  client/
    Foo.js                           client-side JS (CSR or hydrate mode)

  server/
    components/
      foo.go                         server-side renderer
    types.go                         shared prop structs (cross-component)
      foo_props.go                   inferred prop types (developer-overridable)
```

A future bundler stage (not yet planned in detail) consolidates the
per-component CSS and JS into route-scoped or app-scoped bundles.

### Why split that way

- **CSS lives independently.** The compiler already extracts scoped
  CSS per component; emitting one file each is the natural shape.
  Bundling/minification happens later, in a separate tool that
  doesn't care about Vela's internals.

- **Client JS stays in its own tree.** No mixing with server output.
  The bundler (yours or esbuild-via-Go-API) processes only this
  directory.

- **Server code is a proper Go package.** The developer imports
  `myapp/out/server/components` like any other Go package. `go.mod`
  / `go.sum` integration is straightforward.

- **Runtime helpers live in a separate published Go module**, not
  re-emitted into every project. Something like
  `github.com/your-org/vela-runtime` providing `vela.WriteEscaped`,
  `vela.Each`, `vela.ToString`, `vela.ToJSON`. Same approach on the
  JS side: a separate `vela-runtime.js` the bundler picks up.

### Component composition package contract

Component composition is a project-level operation, not an isolated renderer
operation. Before emitting Go, the SSR project pass must resolve every local
`.vela` import to its canonical source path and link each component tag to
that import. The linker is the only source of truth for a child renderer's Go
package path and exported symbol; template tag text alone is insufficient
because JavaScript imports may be aliased.

The generated server tree preserves source directories. Every directory is a
separate Go package, named in lowercase snake_case; files are likewise
lowercase snake_case. A project supplies one explicit Go module import root
for generated SSR (for example `example.com/site/out/server`). A renderer in
`components/admin` imports a child under `components/ui` through that module
root, never with Go's invalid relative imports. Root components keep package
`components`. The CLI option is `--ssr-module`; its default
`vela/generated/server` is intended for generated-code inspection and tests,
while applications should pass their actual module path.

The compiler validates the linked package graph after all renderer and props
sidecar files have been materialized. Per-file validation remains useful for
standalone renderers, but it cannot prove component calls because the child
types and functions live in another generated package.

Slots follow the same linked render plan. A generated child exposes a typed
`ComponentSlots` struct whose default and named fields are `vela.Slot` values.
The parent creates each closure from its already-planned slot children and
passes the struct to the child renderer. An unfilled `<slot>` renders its
fallback; a filled slot writes exactly once at the child slot location.

---

## The developer experience

What the developer writes:

```vela
<!-- UserPage.vela -->
<script>
  /** @type {{ id: number, name: string, email: string }} */
  export let user;

  let editing = $state(false);
</script>

<template>
  <h1>{user.name}</h1>
  <p>{user.email}</p>
  <button @click={() => editing = !editing}>
    {editing ? 'cancel' : 'edit'}
  </button>
  {#if editing}
    <EditForm bind:user={user} />
  {/if}
</template>
```

What the developer writes in Go:

```go
package main

import (
    "net/http"
    "myapp/out/server/components"
)

func main() {
    http.HandleFunc("/users/", func(w http.ResponseWriter, r *http.Request) {
        user := loadUser(r)  // developer's data layer
        components.UserPage(w, components.UserPageProps{
            User: user,
        })
    })
    http.ListenAndServe(":8080", nil)
}
```

What gets sent to the browser:
1. The pre-rendered HTML for `<UserPage user={Alice}>`
2. A `<script>` tag with the initial state serialized as JSON
3. A `<script src="bundle.js">` for the hydrator

What happens client-side:
1. The hydrator reads the JSON state
2. It walks the existing DOM looking for markers (comment nodes —
   see "Marker scheme" below)
3. It attaches event listeners, sets up reactive setters
4. From that point forward, the component is fully interactive,
   identical to a CSR mount

---

## Component renderer API

A generated Go renderer follows this shape:

```go
package components

import (
    "io"
    "github.com/your-org/vela-runtime"
)

type UserPageProps struct {
    User User
}

func UserPage(w io.Writer, props UserPageProps) error {
    // ... writes HTML directly to w ...
    return nil
}
```

Or, for composability via an interface:

```go
type Renderer interface {
    Render(w io.Writer) error
}

func UserPage(props UserPageProps) Renderer { /* ... */ }

// Usage:
UserPage(UserPageProps{User: user}).Render(w)
```

The interface variant composes more cleanly for nested components
(`Card(CardProps{...}, ButtonChildren(...))`). The direct-write
variant is more efficient but harder to nest. **Open question:**
which to use. Probably both — `Renderer` for composition, with
`Component(w, props)` as a convenience entrypoint that calls
`Component(props).Render(w)`.

---

## The expression sublanguage problem

This is the single hardest design problem. `.vela` files have inline
JS in many positions:

```vela
{name}
{user.profile.name.toUpperCase()}
{#if isAdmin && !loading}
{count + 1}
style:font-size="{size * scale}px"
@click={() => count++}
```

The **client knows what to do with these** — they're JS, they go
into the client output unchanged. The **server needs to evaluate
the same expressions in Go**.

Three options for handling this, with the recommended choice
elaborated:

### Option A: hard error at compile time

Developer must rewrite anything not translatable. Safe but rigid.
Rejected because it makes the templating language too restrictive
to be ergonomic.

### Option B: translate what we can, placeholder the rest

The compiler tries to translate every expression to Go. For ones
it can't translate, it emits a **placeholder** in the server
output (empty text node with a marker) and lets the client fill
in the value during hydration. The page renders with that
position blank for the brief window between first paint and
hydration — a "flash of empty content."

This is what most production SSR systems do for genuinely
client-only state. They don't usually advertise it as such.

### Option C: per-expression Go escape hatch

For critical paths where the placeholder approach is unacceptable,
allow the developer to provide a Go equivalent inline:

```vela
<p>{user.name | go("strings.ToUpper(props.User.Name)")}</p>
```

Awkward syntax, but gives a per-expression escape hatch when needed.

### Recommended approach

**B as default, C as escape hatch.** Most expressions in templates
are simple enough to translate. The few that aren't get a
placeholder and the client takes over within ~50ms. For specific
high-value cases where that delay matters, the developer can
provide a Go form.

### What translates cleanly to Go

The translator handles a defined subset. Initial scope:

| JS construct | Go translation |
|---|---|
| Identifier (`name`) | `props.Name` (case-fold to Go's Export case) |
| Member access (`user.name`) | `props.User.Name` |
| Index (`arr[i]`) | `arr[i]` |
| Arithmetic, comparison, logical, ternary | Direct (with operator quirks) |
| Literals (number, string, bool, null) | Direct |
| `arr.length` | `len(arr)` |
| `String(x)` | `vela.ToString(x)` (runtime helper) |
| `JSON.stringify(x)` | `vela.ToJSON(x)` |
| `arr.map(fn)` / `arr.filter(fn)` | Emit a Go `for` loop |
| `arr.includes(x)` | `vela.Includes(arr, x)` |
| Arrow functions (only inside `.map` / `.filter`) | Inline closure |
| Template literals | `fmt.Sprintf` or string concatenation |
| Object/array literals | Composite literal or struct construction |

### What doesn't translate

- **User-defined helpers in `<script>`.** These are JS, can't run in
  Go. Developer must either avoid using them in interpolations
  (compute in Go and pass as a prop), or use the escape hatch.
- **Closures captured from `<script>` scope.** Same issue.
- **Regex, prototype access, `new SomeJSClass(...)`.** Out of scope.
- **`$state`, `$derived`, `$effect` machinery.** Server-side these
  don't exist — `$state` initial values are inlined as props,
  derived values are computed once, effects don't run.

### Important corollary

**The server backend has no reactive machinery at all.** No
setters, no effects, no DOM updates, no defer queue, no proxy. It
just walks the AST and emits string-building code. The Go backend
is **~1/3 the complexity of the JS backend**, not the same again.

### Current compiler contract before hydration

Until DOM adoption is complete, the compiler applies the following deliberately
strict rules. They prevent the server from advertising interactivity or values
that it cannot reproduce correctly:

- `<script>` is not executed by the generated Go renderer. `$effect` and
  lifecycle callbacks never run on the server.
- Template reads may use exported component props and lexical `#each`/
  `#await` bindings. A local `$state`, `$state.deep`, or `$derived` read fails
  with `VELA-SSR-EXPR`; pass the initial value as an exported prop instead.
- Normal DOM events receive the static event bridge only when an inline arrow
  handler is closed over browser-safe globals. A handler that captures reactive
  state or a component closure fails with `VELA-SSR-EVENT-REACTIVE`.
- `<input bind:value={exportedProp}>`, `<textarea bind:value={exportedProp}>`,
  checkbox `bind:checked={exportedBool}`, and a single-select
  `<select bind:value={exportedProp}>` with explicit static option values have
  safe server representations. The renderer writes escaped `value`, textarea
  text content, bare `checked` only for true (never `checked="false"`), and
  `selected` only on the option whose string value matches the bound prop.
  Hydration attaches the browser listener without rewriting the live control
  on startup. If a user edits a control after SSR paint but before its module
  arrives, post-adoption initialization reads that live value through the
  normal setter rather than restoring the stale server payload.
  `bind:this={reactiveRef}` is omitted from server HTML and assigned only
  after the existing element is adopted; the reference is cleared on destroy.
  `<select multiple>`, component two-way bindings, and other `bind:*` forms
  still fail with `VELA-SSR-EXPR`; their HTML and browser-state semantics have
  not yet been made marker-aware.
- Unsupported JavaScript expressions fail at their template position. The
  future placeholder/Go escape-hatch design is not emitted yet: silently
  omitting server content would create an unbounded hydration mismatch.

The in-progress Phase-3 renderer now appends an inert JSON payload after every
component render range:

```html
<script type="application/json" id="vela-state-vela-abc123">
  {"component":"vela-abc123","props":{...},"state":{...},"incomplete":false}
</script>
```

`props` are marshalled by generated Go at render time. `state` contains only
`$state` / `$state.deep` initializers that are cached JavaScript literal trees:
null, booleans, JSON numbers and strings, arrays, and plain objects without
computed keys, spreads, methods, or shorthand references. The compiler never
evaluates user JavaScript to manufacture this payload. If an initializer is a
call, identifier, member access, or another non-literal form, it is omitted
and the payload carries `"incomplete":true` plus its source name in
`"omitted"`. A future hydrator must refuse incomplete restoration with a
diagnostic; it must not silently treat it as a valid client state.

The payload additionally carries `"deep":[...]` for successfully serialized
`$state.deep` bindings. The adoption entry returns this metadata unchanged;
the forthcoming component lowering will use it to restore those bindings with
the existing `$$proxy` runtime rather than treating them as shallow state.

The browser runtime now exposes the corresponding non-mutating validation
primitives: `$$createHydrationWalker(root, componentID)` indexes and validates
all paired comment markers, `$$acquireHydrationRange(walker, id)` retrieves a
known range, and `$$readHydrationPayload(root, componentID)` validates the
inert JSON payload. Their mismatch errors include the component identifier and
the exact missing, crossed, duplicate, or incomplete input. They are runtime
building blocks only until the generated hydrate entry point wires them into
the client acquire-plan.

Project SSR output now writes a sibling lowercase snake-case module such as
`client/user_page.hydrate.js`. Its exported `hydrateUserPage(root)` is an
adoption preflight: it validates the marker graph and state payload, then
returns `{ root, props, componentProps, state, deep, ranges, elements }`.
`componentProps` combines Vela-named props with compiler-owned
`$$velaState`; it is the exact props shape expected by the SSR client module.
For the first executable subset — one root element, static attributes and
recursively nested text interpolation, ordinary dynamic attributes / `class:` / `style:` directives / DOM events, a static-branch `#if` / `#key` / `#each` at any element depth, an unkeyed
static-row `#each`, and no DOM bindings, plus a `#await` with a static pending branch and static HTML plus dynamic text, attributes, form bindings, `class:` / `style:` directives, DOM events, and nested static-branch `#if` blocks and supported `#each` forms with initial rows, collection updates, and keyed DOM identity and focus preservation during reorder in then/catch branches, whose pending SSR DOM is retained until the client promise settles — project SSR also writes a sibling
`client/user_page.hydrate_client.js` module. The entry invokes that module
with the checked marker walker: it acquires the existing root element, skips
CSR mount and all initial DOM writes, restores serializable state, and attaches
root event listeners. Components outside this deliberately narrow subset keep
the validation-only entry; no file claims reactive hydration before its
marker-aware lowering exists.
Dynamic `#await` branches retain their event and binding teardown as a closure created with the branch itself. This keeps branch-local handler references valid when the promise branch is replaced or the hydrated component is destroyed; detached nodes cannot continue to update disposed state.

A direct local `.vela` child component with ordinary one-way props, `@on:*` component events, `bind:value` to a `$bindable` child prop, a static slot fallback, and parent-projected direct text and recursively nested ordinary element slot content, plus top-level `#if` / `#key` / `#each` blocks within the existing static/direct-text subset are now also part of the executable subset. The parent owns markers for its dynamic projected text and acquires those ranges in deterministic slot-name order; the child leaves projected DOM untouched. The parent client imports the child's generated `.hydrate.js` entry under the same local name, passes the existing parent element to it, and receives the child's live instance. The child entry restores its own server props and serializable state before that hand-off, so the parent does not replay initial props or create a CSR anchor. Later parent reactivity forwards only `.set(...)` updates. This is emitted only when every reachable local child has the same executable hydration capability; otherwise the parent remains validation-only. Nested or unsupported structural slot content, other component bindings, and non-local component imports are intentionally excluded.

The first dynamic structural-child lifecycle slice is also executable: a normal `{#if}` with one direct local child branch using only ordinary one-way props and static sibling branches. Hydration adopts the active server child via its `.hydrate.js` entry without DOM recreation; later prop changes call `.set(...)` on that adopted instance. Switching away destroys the child before removing its marker range (which also removes its inert payload), and switching back mounts a new instance through a distinct ordinary `.js` factory import. Slots, component events, component bindings, nested structural component branches, `#each`, and `#await` child lifecycles remain excluded from this slice.


For a supported direct `{#if}` the active server branch is adopted as the
nodes between its `v` markers. No branch is rebuilt at hydration time. A later
reactive condition change removes and materializes only that marker range;
the root element and unrelated siblings are retained. The entry deliberately
does not eagerly acquire elements inside structural ranges, because inactive
branches do not exist in the server response.

A supported direct `{#key identity}` follows the same no-write adoption rule.
The cached key expression is retained on the client; only when its value
changes is the static range removed and rematerialized. It intentionally has
the normal `#key` lifecycle semantics, unlike keyed `#each`, whose matching
rows retain identity.

The initial unkeyed `#each` slice follows the same rule. It adopts all existing
row nodes in its outer `v` range and does no initial row rendering. When its
collection changes, only that range is replaced. A static `{:else}` fallback
follows the same rule: it is adopted when the initial collection is empty and
materialized only after a later empty update. A row containing one element
with direct text interpolation of the each item is also supported; replacement
rows are assembled with generated `createElement` and `createTextNode` AST
nodes rather than embedding item JavaScript in HTML text.

The executable subset now also covers a **keyed** `#each` with exactly one
ordinary row element containing direct text nodes and no `{:else}`. Initial
hydration maps the existing row elements to the initial iterable through the
cached `KeyAST`, rejecting count and duplicate-key mismatches. A later update
retains and reorders matching elements, creates only new rows, removes only
orphans, and updates direct row text in place. Compiler row-marker comments
are removed only on that first update, never during adoption. Chromium coverage
proves identity and focused-row preservation across a deep field write,
reorder, insertion, and deletion. Nested keyed ranges, dynamic row attributes,
bindings, events, components, and keyed `{:else}` remain outside this subset.
Repeated row `e` / `v` marker IDs are ignored by the entry because they are
template positions, not global element identities.

The Go payload uses exported Go field names (`Title`) because it is marshalled
from the generated props struct. The hydrate entry maps those fields back to
the original Vela prop names (`title`) using `SSRPlanProp` metadata before it
returns `props`; consumers of the entry never need to infer that mapping from
printed Go or JavaScript.

---

## The type system

This is the second major design problem. Go is statically typed;
`.vela` is duck-typed. Some bridge is needed.

### Progression (cheap to expensive)

**1. Inference from initial values.** `$state(0)` → `int`,
`$state('')` → `string`, `$state(false)` → `bool`, `$state([])` →
`[]any`, `$state({name: ''})` → `struct{ Name string }`. The
compiler already extracts these initial expressions and has a JS
AST; classifying literals into Go types is mechanical.

**2. JSDoc annotations.** When inference can't determine a type
(e.g. `export let value` with no initializer, or `$state(loadData())`
where the return type is unknown), the developer writes JSDoc:

```js
/** @type {{ id: number, name: string, email: string }} */
export let user;

/** @type {Array<{id: number, count: number}>} */
let items = $state([]);
```

JSDoc parsing is straightforward — the lexer already discards
comments; it can be extended to recognize structured `@type`
comments and feed them to the type analyzer.

**3. Override via `.d.go`.** The compiler emits a `.d.go` file with
inferred types. The developer can hand-edit this file to refine
types beyond what inference produces. On the next compile, the
compiler reads the `.d.go` and **overlays inferred types only
where `.d.go` doesn't define them**. This gives a "refine after
generation" workflow without making the developer rewrite the
component source.

```go
// UserPage.d.go (generated, may be edited)
package components

type UserPageProps struct {
    // Inferred by the compiler:
    Editing bool

    // Hand-edited refinement:
    User struct {
        ID    int64   `json:"id"`
        Name  string  `json:"name"`
        Email string  `json:"email,omitempty"`
        Avatar *URL   `json:"avatar,omitempty"`
    }
}
```

**4. Runtime reflection (last resort).** If inference fails AND
there's no JSDoc AND `.d.go` doesn't define the prop, the
compiler emits `interface{}` and a **loud warning**. The
developer pays the type-assertion cost in their server code.
This should be rare; the warning should make it visible.

### Why this gradient

Inference is free for the common case. JSDoc is a small cost for
the next-most-common case. `.d.go` overrides handle the
sophisticated cases without forcing the developer to inline Go
syntax into `.vela`. Runtime reflection is a safety net but
**not the default** — Go reflection is slow and ugly; it should
feel like an admission of defeat, not a feature.

### Generated props sidecar

Project SSR output keeps each component's inferred prop struct in a separate,
lowercase snake_case file such as `server/components/user_page_props.go`. The
compiler creates this `*_props.go` file only when it is absent; later builds
read and preserve it byte-for-byte. Developers may refine the inferred Go
fields there (for example replacing `map[string]any` with an application
struct). The companion `user_page.go` renderer deliberately does not declare
the props type, and the compiler type-checks both files as one Go package.

---

## Hydration mechanics

For Phase 3 — covered here so the Phase 1/2 decisions don't
preclude it.

### The marker scheme

Comment nodes are the recommended marker form:

```html
<!--e:vela-page:0--><p>Hello <!--v:vela-page:0-->Alice<!--/v:vela-page:0-->, count = <!--v:vela-page:1-->42<!--/v:vela-page:1--></p><!--/e:vela-page:0-->
```

Each `<!--v:<component-id>:N-->` pair brackets a dynamic position. Each
`<!--e:<component-id>:N-->` pair surrounds one static element, which the
hydrator acquires by tag without creating or replacing it. The client walks
both marker namespaces, then assigns cached element vars (`$$n0`, `$$n1`,
...) through the element ranges.

Why comment nodes instead of `data-vh="N"` attributes:
- Comments are invisible to CSS, accessibility tools, and the DOM
  inspector's element tree
- Comments work anywhere — between text, inside elements, as
  sibling to anything
- Attributes pollute the DOM and only work on elements

Why pairs instead of single markers:
- The end marker tells the hydrator where the dynamic content
  stops, so it can correctly bind the cached var to the right
  range of nodes (especially for `{#if}` branches and `{#each}`
  rows)

### The state injection format

Server emits initial state as JSON in a script tag:

```html
<script type="application/json" id="vela-state-page">
  {"user":{"name":"Alice","id":42},"editing":false}
</script>
```

The hydrator reads it on startup:

```js
const state = JSON.parse(
  document.getElementById('vela-state-page').textContent
);
```

Why this rather than `<script>window.__VELA__ = {...}</script>`:
- The `application/json` MIME type makes it inert if executed
- Cross-origin / CSP friendly (no `script-src 'unsafe-inline'`
  needed for state, only for the hydrator bundle)
- Validates as JSON; the inline-`window.__X__` approach allows
  arbitrary JS injection if developer escapes incorrectly

### The acquire pattern

The current client codegen has lines like:

```js
const $$n0 = document.createElement('div');
$$n0.setAttribute('vela-a3f8e1', '');
$$root.appendChild($$n0);
```

For hydration, these would be replaced by:

```js
const $$n0 = $$acquireElement(walker);  // find next marker, return its element
```

The codegen needs a **mode flag** — CSR or hydrate — and a single
`acquireElement` function that does the right thing for each mode.
The same walk order, the same `$$nN` numbering — only the
construction primitive changes.

This is the refactor that makes hydration work without doubling
the codegen. Worth designing carefully; do it before Phase 3, not
during.

---

## Why this is achievable in a reasonable timeframe

A few things that make this less daunting than it sounds:

**The hard parts are already done.** The parser, analyzer, AST,
expression parser, JS parser, scope analyzer — all reused. The
SSR work is "add a second backend that walks the same AST."

**The Go backend is simpler than the JS one.** No reactivity, no
proxy machinery, no DOM update tracking. Walk + emit strings.
Estimated complexity: **~1/3 of the existing JS codegen**.

**Types are mostly inferable.** The common case is `$state(literal)`,
which is mechanical to translate. JSDoc covers the next tier.
`.d.go` handles the rest. Runtime reflection is the last-resort
safety net.

**One walk drives both backends.** The discipline is "shared AST
walk produces a render plan; both backends consume the plan."
This keeps the two outputs aligned by construction — no
hydration mismatches from drift between independent walks.

---

## What this is NOT

To prevent scope creep:

- **Not a full framework.** Vela compiles components. The
  developer writes the server, routing, data loading, auth, and
  everything else in Go.
- **Not Astro.** No islands, no per-component hydration modes, no
  build-time route partitioning. Every component is either
  CSR-mounted or SSR-then-hydrated; no mixing within a page.
- **Not Next.js.** No filesystem-based routing, no `getServerSideProps`,
  no API routes. The developer wires their own HTTP handlers.
- **Not Templ.** Templ is server-only and not reactive. Vela
  produces the same kind of typed Go renderers Templ does, plus
  a reactive client that can hydrate them.

The pitch is: **"a Go-native fullstack reactive framework."** The
niche is the value, not feature parity with the established
players.

---

## Phased plan

### Phase 0 — design lock-in (one session, mostly conversation)

Before any code:
1. Write down the expression-sublanguage as a formal list.
2. Pick the marker scheme (recommendation: paired comment nodes).
3. Pick the state injection format (recommendation:
   `application/json` script tag).
4. Decide on the runtime-package split (one Go module + one JS
   module, both published separately).
5. Lock the renderer API shape (recommendation: both
   `Component(w, props)` and `Component(props).Render(w)`).
6. **Collect a corpus** of real `{...}` expressions from your
   team's `.vela` files. This is the single most valuable input
   to the translator design — it tells you what the translator
   actually needs to handle vs. what you might over-engineer for.

### Phase 1 — JS→Go translator + minimal Go backend (3-4 sessions)

- Restricted JS expression → Go expression translator.
  Clear error mode for unhandled cases.
- `ssrgen_go.go`: walks the AST, emits a Go file per component.
- Static HTML, attributes, text interpolation.
- `{#if}` and `{#each}` block emitters (the easy ones).
- Scoped CSS extraction to separate `.css` files (already
  scoped — just move the existing extraction to its own file).
- Type inference for `$state(literal)` cases.
- One real test: SSR a multi-component page from the playground,
  get HTML out via `curl`.

End-of-phase deliverable: you can compile a `.vela` project, write
a Go `main.go`, get HTML out. No hydration yet — page is static.

Effort: ~2000 lines of new Go-side codegen + a small runtime
package. ~30-50 new tests.

### Phase 2 — types and ergonomics (2-3 sessions)

- Generated `.d.go` files with inferred prop structs.
- JSDoc `@type` parsing for explicit annotations.
- Override mechanism: inferred types yield to `.d.go` entries.
- Component composition in Go (one component's renderer calls
  another's).
- Slot rendering as Go closures: `func(io.Writer) error`.
- `{#key}` and `{#await}` SSR emitters. (`{#await}` server-side
  either evaluates the promise during render — slowing first byte
  — OR emits the pending state and lets the client take over.
  Recommendation: latter, document explicitly.)
- `class:foo={cond}` and `style:foo={expr}` directives.

End-of-phase deliverable: realistic multi-component pages SSR
cleanly with typed Go props.

Effort: ~1500 lines + further test coverage.

### Phase 3 — hydration (5-8 sessions)

- Refactor client codegen so element construction goes through a
  single `acquireElement` function (CSR mode: creates; hydrate
  mode: walks markers).
- Server emits paired comment-node markers around dynamic
  positions.
- JSON state injection in the server output, JSON state reading
  in the hydrator.
- A "hydrate-mode entry point" generated alongside the existing
  CSR one. Developer picks which to use.
- End-to-end test: server renders page, browser receives, JS
  attaches, app is interactive without re-rendering anything.

End-of-phase deliverable: full SSR + hydration. Production
viable.

Effort: ~2500-3500 lines including the client codegen refactor.
This phase has real architectural risk — hydration mismatches
are notoriously hard to debug. The dual-emission single-walk
discipline is the main safeguard.

### Phase 4 — bundlers and polish (open-ended)

- CSS bundler (merge, minify, optimize).
- JS bundler (or integrate esbuild via its Go API).
- Asset pipeline.
- Source maps.
- Dev-server hot-reload integration.

This is the "make it nice for OSS launch" phase. Doesn't have to
happen in any particular order; each piece is independently
useful.

---

## Open questions to settle before Phase 1

These should be answered in Phase 0:

1. **Renderer API:** `Component(w, props)` direct write, or
   `Component(props).Render(w)` via interface? (Recommendation:
   both — direct as convenience over the interface form.)

2. **Slot syntax in Go:** how does the developer pass slot
   content? Closures taking `io.Writer`? Strings? A
   `Slot` type? (Recommendation: closures; matches Go idiom for
   compositional rendering.)

3. **Error handling:** what happens when a renderer encounters
   invalid data (nil pointer, type mismatch from `interface{}`)?
   Recover and emit error HTML? Propagate as a Go error? (Probably
   propagate — let the developer's HTTP handler decide.)

4. **CSS scoping for SSR:** inline `<style>` tag in every page, or
   separate stylesheet linked via `<link>`? (Probably separate for
   caching; inline for the first-paint critical path. Could be a
   compiler flag.)

5. **Async data:** can a Go renderer call out to data loaders
   mid-render, or must all data be loaded before the render
   starts? (Probably the latter for simplicity. Streaming is a
   Phase 4+ topic.)

---

## Risks to watch

**Translator scope creep.** The temptation will be to handle "just
one more JS construct." Stop. The escape hatch (Option C above)
exists for a reason. Keep the translator small.

**Hydration mismatch debugging.** Once two backends emit
independently-walked structures, mismatches are inevitable
without strict discipline. The single-walk render-plan pattern
is the safety net; if you skip it, expect to spend significant
time on debugging tools instead.

**Documentation cost.** A from-scratch framework has nothing
borrowed from React/Svelte/Vue knowledge transfer. Every concept
needs explaining. Plan for substantial documentation work as part
of the OSS launch — this isn't a phase, it's a continuous tax.

**The Templ comparison.** Templ exists, is well-known in Go
circles, and overlaps with Vela's Phase 1/2 deliverable. The
differentiator is the reactive client. Make sure the OSS pitch
leads with that, not with "another Templ."

---

## Recommended next concrete step

Before writing any SSR code, do **two things in parallel**:

1. **Collect the corpus.** Grep every `{...}` expression out of
   your team's `.vela` files. Categorize them: how many are pure
   ident? Member access? Method calls? Computed properties?
   Function calls into `<script>` helpers? This list tells you
   what the translator must handle vs. what it can defer.

2. **Hand-write `Card.vela`'s desired Go output.** Pick a real
   component (Card.vela is the canonical playground component —
   has slots, scoped CSS, conditionals — a good middle case),
   write what its Go file *should* look like by hand. Iterate
   until the team agrees on the API shape. **Then** build the
   translator that emits that shape.

This is outside-in design — the desired Go API constrains the
translator, rather than the translator (whatever the parser can
do) constraining the API. Outside-in catches API problems before
they're baked into the codegen.

Once those two artifacts exist, Phase 1 has a concrete starting
point and the rest of the plan can proceed with much less
guesswork.

---

## Decisions summary

For quick reference, the recommendations from this design:

| Question | Recommendation |
|---|---|
| What does "SSR" mean for Vela? | Static rendering + hydration. No islands. |
| Server output language | Go |
| Compiler output layout | Three directories: styles/, client/, server/ |
| Component renderer shape | Both `Component(w, props)` and `Component(props).Render(w)` |
| JS expression handling on server | Translator subset (option B) + per-expression escape hatch (C) |
| Type system | Infer → JSDoc → `.d.go` override → reflection fallback |
| Marker scheme | Component-scoped paired comment nodes (`<!--v:scope:0-->...<!--/v:scope:0-->`) |
| State injection | `<script type="application/json">` with JSON payload |
| Runtime helpers | Separate published Go module + JS module |
| Hydration mode switching | Single `acquireElement` function in client codegen |
| `{#await}` server-side | Render pending state; client takes over |
| Scope creep guardrail | Translator is intentionally small; escape hatch covers the rest |
| First concrete step | Corpus collection + hand-written Card.go example |

---

## Opt-in project route manifest

Route discovery is a project metadata pass for SSR. It is not an HTTP server,
JavaScript router, loader, redirect system, or authorization layer. Existing
`vela --project` builds never read routes and keep their current output.

The planned SSR-only option is `--routes=off|auto|manifest`; `off` is the
default. `auto` discovers filesystem pages and merges a manifest when one
exists. `manifest` requires `vela.routes.json`. The compiler never enables
routing only because a directory happens to be named `routes`.

### Filesystem pages

Pages live below `<project>/routes` and use `+page.vela`:

| Source | Route pattern |
| --- | --- |
| `routes/+page.vela` | `/` |
| `routes/about/+page.vela` | `/about` |
| `routes/blog/[slug]/+page.vela` | `/blog/:slug` |
| `routes/files/[...path]/+page.vela` | `/files/*path` |

Static segments are `[A-Za-z0-9_-]+`; parameters are `[name]`, where `name`
matches `[A-Za-z][A-Za-z0-9_]*`; and `[...name]` is a final-only catch-all.
Optional catch-alls, route groups, regexp segments, and encoded segment syntax
are not v1 features. `+layout.vela` is discovered and recorded as an ordered
root-to-leaf layout chain, but v1 does not compose or render layouts implicitly.

### `vela.routes.json`

The optional manifest is relative to the project root and contains only route
metadata:

```json
{
  "version": 1,
  "mode": "extend",
  "routes": [
    {"id":"documentation","path":"/docs/:slug","component":"routes/docs/[slug]/+page.vela"},
    {"id":"home","path":"/","component":"pages/Landing.vela","override":true}
  ]
}
```

`extend` is the default. A manifest route that collides with a discovered path
requires `override: true` and replaces that one entry. Two explicit entries for
one path, or an override without a discovered target, are errors. `replace`
uses only manifest entries. Component paths must remain inside the project and
target `.vela` files. No loader, middleware, auth, environment, or secret
fields exist in v1.

### Artefacts and application contract

Enabled route mode emits `out/routes.json` and optionally
`out/server/routes/routes_gen.go`. They contain only normalized route id,
pattern, ordered params, generated server renderer identity, client entry path,
and layout identity; never `.vela` source, rendered HTML, script values, or
user data. Entries are sorted by normalized pattern then id.

`routes_gen.go` is a standalone metadata package with `Entry`, `Match`,
`Entries`, and a pure `MatchPath(string)` helper. It neither reads HTTP
requests nor calls renderers. The application owns request handling, data,
auth, status codes, and typed props:

```go
match, ok := routes.MatchPath(r.URL.Path)
if !ok { http.NotFound(w, r); return }
switch match.ID {
case "documentation":
    docs.Render(w, docs.DocsProps{Slug: match.Params["slug"]})
}
```

### Diagnostics and fixture matrix

The route pass reports `VELA-ROUTE-DUPLICATE`,
`VELA-ROUTE-INVALID-SEGMENT`, `VELA-ROUTE-CATCHALL-POSITION`,
`VELA-ROUTE-OVERRIDE`, and `VELA-ROUTE-COMPONENT` with the offending
component path segment or manifest location. Its implementation fixtures cover
index, nested static and parameter pages, catch-all, layout chain, manifest
extend/replace/override, duplicates, invalid names, and source positions.

The pass consumes the existing canonical `.vela` list only after component
compilation and generated server package validation. It must not alter template
code generation, component linking, or browser runtime behavior.
