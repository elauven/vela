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

### Explicit server await snapshots

The Go renderer does not execute JavaScript promises. A server data loader may,
however, explicitly declare the branch already known at render time through the
generated component-specific options API:

```go
components.RenderPageWithOptionsAt(w, props, slots,
    components.PageRenderOptions{
        Await: map[int]components.PageAwaitSnapshot{
            3: {State: "then"}, // `3` is the compiler-owned await marker ID
        },
    },
    "vela-page-instance",
)
```

`State` is exactly `"pending"`, `"then"`, or `"catch"`. The legacy
`RenderPage` and `RenderPageAt` APIs remain unchanged and always select
`pending`. A selected snapshot is emitted in the component's inert payload as
`await`, so hydration can adopt the same initial branch rather than briefly
rebuild it as pending.

The executable snapshot slice accepts static content plus direct leaf reads of
the declared branch name inside a static element tree: `{value}` / `{error}`
text and ordinary one-way attributes such as `:title={value}`. It also accepts
ordinary DOM events. `Value` and `Error` are explicit strings on
`ComponentAwaitSnapshot`, escaped by the Go renderer and retained in their
canonical marker range by hydration. Initial adoption performs no DOM write;
it attaches each event listener to its acquired element marker and gives the
settled range a reverse-order listener cleanup. Native `bind:value` and
`bind:checked` are also supported when their target is a simple reactive
component state or prop identifier, never the `then` / `catch` binding itself.
Hydration first adopts the control's live browser-owned value, then uses its
guarded normal setter/write path for later state changes; listener and writer
are cleared with the settled range. Same-branch settlement updates only owned
text ranges and changed attributes, retaining the settled element. The dynamic
attribute expression must be the branch binding itself — composed expressions,
directives, and nested structural content still require their
own marker-aware recipes and remain later work.

A direct local `.vela` child component with ordinary one-way props, `@on:*` component events, `bind:value` to a `$bindable` child prop, a static slot fallback, and parent-projected direct text and recursively nested ordinary element slot content, plus top-level `#if` / `#key` / `#each` blocks within the existing static/direct-text subset are now also part of the executable subset. The parent owns markers for its dynamic projected text and acquires those ranges in deterministic slot-name order; the child leaves projected DOM untouched. The parent client imports the child's generated `.hydrate.js` entry under the same local name, passes the existing parent element to it, and receives the child's live instance. The child entry restores its own server props and serializable state before that hand-off, so the parent does not replay initial props or create a CSR anchor. Later parent reactivity forwards only `.set(...)` updates. This is emitted only when every reachable local child has the same executable hydration capability; otherwise the parent remains validation-only. Nested or unsupported structural slot content, other component bindings, and non-local component imports are intentionally excluded.

The first dynamic structural-child lifecycle slice is also executable: a normal `{#if}` with one direct local child branch using only ordinary one-way props and static sibling branches. Hydration adopts the active server child via its `.hydrate.js` entry without DOM recreation; later prop changes call `.set(...)` on that adopted instance. Switching away destroys the child before removing its marker range (which also removes its inert payload), and switching back mounts a new instance through a distinct ordinary `.js` factory import.

`#await` now has the corresponding direct-child-chain slice: SSR adopts the
pending branch's local children with their structural instance namespace.
When the promise settles, the pending handles are destroyed before their
payload scripts and range nodes are removed; the selected `then` or `catch`
branch mounts direct local children through ordinary CSR factories in source
order. This slice accepts only one-way props and static siblings in the three
branches. Slots, component events/bindings, and nested structural component
branches remain later work.

Independently selected `then` and `catch` snapshots additionally adopt a
source-ordered direct local child chain with ordinary one-way props, `@on:*`
component events, a `$bindable` component binding of the form
`bind:name={parentState}`, and static siblings. `parentState` must be a simple
reactive parent-state identifier; it is passed as an `$$onChange_name` callback
to the hydrate entry without replaying the server prop into the live child DOM.
Static projected default and named slots are also retained during adoption;
when a later branch transition mounts a fresh CSR child, it receives the same
typed static slot factories. A default or named slot may additionally contain
`{name}` text interpolation, `:attribute={name}` one-way attributes, and
`class:name={value}` / `style:property={value}` directives, and ordinary DOM
`@event` listeners, where each value is a single parent identifier or the
selected `then` / `catch` binding, recursively inside ordinary elements. The
parent owns and acquires the canonical text and element markers, updates them
in place for parent changes or same-branch promise settlement, and clears
their handles before the branch range is removed. Event listeners attach after
child adoption and are removed before branch replacement or destroy; a
subsequent CSR remount builds fresh slot DOM and listener ownership rather than
reusing consumed SSR markers. Native `bind:value` and `bind:checked` are also
accepted on ordinary projected slot elements when their target is a simple
reactive parent state/prop identifier (not the `then`/`catch` binding). The
hydrator first reads the live browser control value, then owns both its setter
writer and listener cleanup within the selected slot range. One direct local
projected child component with ordinary one-way props, one simple `$bindable`
binding to parent `$state`, and `@on:*` component events is also supported.
That direct child may in turn receive fully static default and named slots
(ordinary text and elements with static attributes), which are retained as
part of its server DOM during adoption and recreated by the ordinary CSR slot
factory after a branch remount. The parent imports its named hydrate entry,
gives the binding callback to it before adoption, attaches CustomEvent
listeners afterward, forwards later props with `.set(...)`, and
detaches/destroys the payload-owning instance before the selected range is
removed. Within an ordinary-element tree in those child slots, simple parent
identifier text interpolation, one-way dynamic attributes, and `class:` /
`style:` directives are acquired and refreshed by the selected await-range
owner without replacing child DOM. Ordinary DOM events attach after adoption
and detach before the child or selected range is destroyed. `bind:value` and
checkbox `bind:checked` are supported for a simple reactive parent
state/prop identifier: hydration preserves browser-owned control state, then
installs a guarded writer and listener owned by the selected range. Nested
local child components are also supported: each is adopted through its named
hydrate entry, retains its own inert payload, receives later `.set(...)`
updates, and is destroyed in reverse source order before the enclosing child
or selected range is removed. The first structural extension of this
selected-snapshot slot recipe is a marker-aware ordinary `#if` / `{:else}`
with a simple parent-identifier condition and fully static ordinary-DOM
branches. The selected await-range owner acquires the canonical `v` range and
adopts its existing nodes without an initial write. A condition change removes
only those owned nodes and inserts the selected static branch before the same
end marker, so the enclosing projected child, nested local children, and
unrelated slot siblings retain identity. `{:else if}`, dynamic branch leaves,
branch-owned listeners or bindings, local child lifecycle inside a branch,
`#key`, `#each`, and nested `#await` still require the general structural
range controller.
Each child retains its server DOM and inert payload while the initial promise
is pending. If that promise settles into the same branch, every adopted child
receives `.set(...)` with the client value or error instead of being remounted.
Component-event listeners are attached after child adoption and removed in
reverse source order before branch replacement. Changing to a different branch
destroys every child before its range and payload are removed. Slots and nested
structural content beyond the static slot subset remain future lifecycle work.


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

## SSR v2 completion contract

The first delivery described above proves the renderer, marker protocol, and
DOM-adoption approach. It is not the final definition of SSR support. This
section extends the task from a supported-subset renderer to a compatibility
contract: a CSR-valid Vela project must receive a classified SSR outcome,
not an accidental compile failure because an otherwise supported feature is
nested or used by a child component.

### One execution decision per AST site

The compiler will classify every cached script and template AST site into one
of four explicit modes. The classification is part of analysis and the typed
SSR render plan; neither the Go renderer nor the JS printer rediscovers it
from source text.

| Mode | Server behaviour | Client behaviour | Typical use |
| --- | --- | --- | --- |
| `server-native` | Generated Go evaluates the construct. | Hydration adopts its result and continues normal updates. | Pure expression, supported control flow, portable helper. |
| `server-host` | Generated Go calls a declared user-owned Go binding. | The original JS remains the source of later client updates. | `MakeMarkdown(value)`, domain formatting, authenticated server service. |
| `client-deferred` | The renderer emits a deterministic fallback inside an owned marker range. | The normal hydrator evaluates and replaces or updates that range. | `window`, DOM APIs, browser-only libraries, client async work. |
| `unsupported` | Generation stops with a positioned diagnostic and a reason chain. | No partial artifact is emitted. | Invalid AST, unsafe ambiguous boundary, or a missing required host declaration. |

`unsupported` is therefore not a synonym for "not yet translated to Go".
Non-portable but valid browser code normally has the `client-deferred` path.
An error is reserved for code whose server boundary is ambiguous or whose
developer contract is incomplete.

### Host bindings for portable domain logic

The stable solution for an application helper is a non-generated host package
plus a project manifest that maps a JS-visible symbol to a Go symbol. The
generated renderer imports the declared package through `go/ast`; it never
rewrites or overwrites the developer's implementation.

Proposed manifest shape:

```json
{
  "hostFunctions": {
    "MakeMarkdown": {
      "package": "example.com/site/ssrhost",
      "symbol": "MakeMarkdown"
    }
  }
}
```

For this template:

```vela
<p>{MakeMarkdown(state)}</p>
```

analysis resolves `MakeMarkdown` as `server-host`; generated Go calls
`ssrhost.MakeMarkdown(state)` and propagates its ordinary Go `error`. The
binding signature is checked against inferred/JSDoc prop types where possible,
then falls back to an explicit `any` contract only when the manifest permits
it. The exact manifest filename and signature grammar are Phase 6 design
deliverables, but the user-owned-package and non-destructive rules are fixed.

### Client-deferred boundaries

Leaf expressions can receive a compiler-owned fallback in their existing text
or attribute marker range. For example, a DOM measurement may render an empty
text fallback and be filled by the hydrator.

A client-only value that decides structure is different: the server cannot
safely guess the active `#if`, `#each`, `#await`, key, component choice, or
slot shape. Such a site requires an explicit client boundary with a declared
server fallback. The syntax and exact fallback API will be designed before
implementation, but it is a full-app hydration boundary, not an island: the
parent still owns its markers, lifecycle, payload collection, and teardown.

This distinction prevents both false server HTML and a flash caused by an
implicit empty structural range. A diagnostic must name the controlling
expression and offer the host-binding or client-boundary remedies.

### Recursive template contract

The final renderer must lower these nodes recursively, at any depth and in
any valid combination allowed by the Vela feature matrix:

| Template node | Required SSR/hydration behaviour |
| --- | --- |
| Element, text, dynamic attribute, `class:`, `style:` | Server output plus owned element/text marker; adopt without initial write. |
| DOM event and DOM binding | Emit server representation; attach listener/binding after adoption; clean it on owner destroy. |
| `#if` and `#key` | Adopt the selected range; replace only that range after a dependency change. |
| Unkeyed `#each` | Adopt all rows; replace only the list range when its identity model requires it. |
| Keyed `#each` | Give every row an instance path; retain/reorder matching rows and their children. |
| `#await` | Adopt pending server DOM; own pending/then/catch ranges and their async cleanup. |
| Local component | Adopt or CSR-mount a distinct instance; retain prop, event, binding, slot, style, payload, and destroy ownership. |
| Slot projection | Preserve parent ownership of projected ranges while child owns its fallback and instance lifecycle. |

The implementation vehicle is a typed recursive structural lifecycle plan with
five operations: `adopt`, `mountCSR`, `update`, `destroy`, and
`collectPayload`. It replaces root-only and static-child special cases. A
component instance ID is derived from its structural path, including a stable
key for keyed rows, rather than only its component name. This is essential for
multiple copies of the same child in an `#each`: marker namespaces and payload
lookup must remain unique without duplicate document IDs.

The first Phase 4 checkpoint is present in the compiler: `SSRCapability` now
walks every template node and records a deterministic AST path, execution
mode, marker ownership, lifecycle requirement, and current hydration status.
The existing hydration eligibility API reads this tree, while the old narrow
shape checks remain an internal compatibility adapter until their lifecycle
rules are migrated one by one. This changes no emitted component output yet.
When the current hydrator cannot execute a tree, client generation now reports
the most specific capability boundary with its template path, node kind, and
missing lifecycle rather than a component-wide generic subset failure.
The old policy predicates have also moved out of the hydration generator into
the capability module as a temporary current-subset policy. This leaves one
authoritative analysis boundary while later Phase 4 work replaces individual
shape rules with recursive lifecycle-plan decisions.

The next checkpoint is also now represented in code as
`SSRStructuralLifecyclePlan`. It mirrors the cached template AST without
dropping unsupported-yet descendants and assigns typed ownership operations
and an immutable structural instance recipe to every node. Row recipes retain
the cached `KeyAST`; branch, component, and slot segments distinguish repeated
instances without synthesizing document IDs at analysis time. The plan is
currently analysis-only: it deliberately does not alter emitted HTML or client
code until marker emission and hydration lowering consume the same graph.

Hydration now also derives every acquired marker from the same typed SSR render
plan. `SSRHydrationMarkerIDs` indexes element, text, `#if` / `#key`, `#each`,
and `#await` markers by their source spans. The generator no longer maintains a
private dynamic counter, so marker IDs reserved by a preceding key expression
or inactive structural alternative cannot shift a later client acquisition.

The executable hydration builder now passes `SSRStructuralLifecyclePlan` into
`ASTGen`. Before lowering an acquired element, dynamic text range, structural
range, or local child component, the generator resolves the matching typed
node and checks that it owns the required lifecycle operation. Text-part spans
resolve to their enclosing text node; all structural and component spans stay
exact. This is the first consumer of the lifecycle graph; dispatch still uses
the existing supported subset until the recursive lowerer replaces it.

The supported hydration walker is now plan-driven at its recursive boundary:
it walks paired template AST and lifecycle-plan children, dispatches by
`SSRLifecyclePlanKind`, and rejects a node-kind, child-count, or span mismatch
as an internal generator error. Component slot content is matched to its
typed slot plan in sorted slot-name order. Individual `#if`, `#each`, and
`#await` lowerers still implement the current subset internally; later slices
replace those implementations without reintroducing a second tree traversal.

The existing direct local child in an `#if` branch is also selected from this
plan. Ordinary plan nodes retain their immutable template-AST reference while
synthetic branch nodes remain structural-only; the hydration lowerer and the
distinct CSR-factory import scanner therefore find the component through a
typed branch child, not by independently scanning raw branch arrays. This
preserves the current one-direct-child subset and is the migration seam for
multiple and nested branch children.

That subset now allows static siblings before and after the owned local child
in either `#if` branch. Initial hydration adopts the child while leaving the
server-rendered siblings untouched. When the branch changes, the child is
destroyed before its range is removed; a fresh fragment then emits static
siblings in template order and mounts the child through its ordinary CSR
factory at the same relative position. Chromium coverage proves zero initial
mutations, teardown, remount, and sibling order.

A branch may now contain several direct local children. The hydration client
creates one handle per child, adopts and forwards props independently, destroys
handles in reverse source order before range removal, then recreates static
siblings and CSR children in source order. Repeated static bindings use a
path-derived suffix, so server Go lowering calls `ChildAt` with
`parentInstanceID + suffix` and the hydration client passes that exact value to
the child entry. This gives every child its own marker namespace and payload
script while preserving the legacy marker contract for ordinary singleton
children. Runtime `#each` and `#key` paths remain deliberately excluded: they
need their evaluated row/key identity, not a suffix based on source text.

`SSRComponentInstanceIdentity` now records every component occurrence from the
structural lifecycle graph. Its immutable typed path, not a tag name, is the
source of identity; a diagnostic namespace makes static occurrences visible
to tests and tooling. Recipes crossing `#key` or `#each` are explicitly marked
runtime-scoped, since their final marker/payload namespace must contain the
evaluated key. This is analysis-only until the renderer and hydrate entry
accept an instance namespace; it prevents a later implementation from falling
back to parsing expression text or component-name IDs.

Generated Go renderers now expose `RenderComponentAt(w, props, slots,
instanceID)` and `ComponentAt(props, slots, instanceID)` in addition to the
existing default-scope APIs. Their marker comments, payload `component` field,
and `vela-state-*` script ID use the supplied namespace. Hydrate entries
accept the matching optional third `instanceID` argument and fall back to the
component scope when omitted. Static repeated parent-to-child lowering now
passes structural recipes into these APIs on both targets. The next checkpoint
is runtime identity lowering for `#each` / `#key`, followed by recursive
component lifecycle ownership in nested structural branches.

Go SSR now also lowers runtime structural scope without reparsing JavaScript:
an unkeyed `#each` appends its generated loop index, a keyed `#each` appends
the value of its cached key AST, and `#key` appends its cached key value. A
runtime-scoped child then receives `ComponentAt(..., parentRuntimeScope +
staticPathAfterRuntimeSegment)`. Thus repeated children in different rows or
key values have distinct markers and state payload IDs in server output.
Hydration does not yet adopt child instances inside those row/key ranges; that
remains a Phase 5 lifecycle lowering task, except for the first unkeyed-row
slice: one direct local child with ordinary one-way props can now be adopted
once for every server row using the same index-based namespace as Go SSR. On
a later list update the parent destroys all adopted row children, removes only
the outer list range, and mounts fresh CSR children in source order. This is
deliberately not a keyed reconciler: it makes no identity-retention claim and
does not yet cover row events/bindings, slots, multiple/nested children, or
`#key`-scoped child adoption.

The next keyed-row slice is also executable for one direct local child with
ordinary one-way props and no `{:else}`. The existing runtime still owns
`Map<key, rowElement>` reconciliation. Generated hydration code owns a paired
`Map<key, childInstance>`: retained keys preserve their DOM and receive
`.set(...)`; deleted keys call `destroy()` before the runtime removes their
row; and a create callback mounts only a new CSR child. The component row has
no parent-visible root-tag declaration, so keyed adoption accepts direct
elements while excluding only `vela-state-*` payload scripts. Every executable
hydrate entry also wraps `component.destroy()` to remove that instance's inert
payload script, including when a structural parent owns teardown.

Chromium coverage now verifies this lifecycle end-to-end: initial keyed child
adoption produces zero mutations, reordering retains the exact hydrated child
root nodes, a retained child receives a prop update, a new key creates only
one CSR child, and destroying an orphan removes its `vela-state-*` payload.

This remains intentionally narrower than final keyed component parity: slots,
component events/bindings and nested structural content are still Phase 5 work.
Direct source-ordered child chains and static siblings in a keyed row are
already adopted through paired row-range markers and covered by Chromium
identity, reorder, update, insertion, deletion, and payload-cleanup fixtures.

A `{#key}` block now supports static siblings and multiple direct local child
components with ordinary one-way props. Each hydrate entry receives the same
evaluated key namespace as Go SSR plus its structural path. Changing the key
destroys owned children in reverse order, removes their payloads through the
common destroy wrapper, and rebuilds the complete branch in template order;
changing a one-way prop without changing the key retains every child and
forwards `.set(...)`. Slots, component events/bindings, and nested structural
content inside that keyed branch remain outside this slice.

Chromium coverage verifies that a keyed chain adopts without initial DOM writes,
forwards ordinary prop changes while retaining both child roots, and replaces
the complete component/static-sibling branch only when the key changes.

### JavaScript and Browser API compatibility matrix

`ECMASCRIPT.md` is the inventory of standard objects. Phase 6 converts that
inventory into a Vela-specific, test-backed matrix. Categories are not judged
by syntax alone: a construct is server-native only when its observable Vela
semantics can be reproduced in Go.

| Category | Initial preferred mode | Notes |
| --- | --- | --- |
| Literals, operators, property/index reads, conditionals | `server-native` | Existing expression translator foundation. |
| Pure declarations, named/arrow functions, lexical closures | `server-native` when capture graph is representable | Generated Go closures preserve only documented value semantics. |
| Arrays, objects, iteration, map/filter/reduce-like transforms | `server-native` or `server-host` | Equality, ordering, holes, and mutation semantics require dedicated tests. |
| String, JSON, Math, URI helpers, selected `Intl` formatting | `server-native` where a Go-equivalent is specified | Locale/time-zone differences must be explicit. |
| Date, RegExp, BigInt, Map/Set, typed arrays | Start as `server-host` | Promote only after compatibility fixtures establish semantics. |
| Imports and application helpers | `server-host` for declared pure Go equivalents | Unmapped imports are not silently executed by Go. |
| Promise, async/await, timers, fetch | `client-deferred` by default | Server data loading is a separate explicit API, not accidental JS execution. |
| `window`, `document`, DOM events, storage, history, location, media, canvas | `client-deferred` | Browser state has no faithful server equivalent. |
| Workers, WebSocket, WebRTC, Service Worker, observers | `client-deferred` | May have separate server services, but not JS-to-Go translation. |
| `eval`, dynamic import with unknown target, Proxy/Reflect metaprogramming | `unsupported` or explicit boundary | No implicit server execution or partial emulation. |

The matrix must separately cover language syntax, built-ins, imports,
asynchronous primitives, Browser APIs, type assumptions, execution mode,
fallback semantics, and a fixture link. Unsupported behaviour is a published
contract, not a gap hidden in a code generator.

### Release proof

Completion requires a generated nesting corpus and real applications compiled
in both CSR and SSR modes. For every supported combination, Chromium tests
must prove initial marker adoption, correct reactive updates, instance/row
identity where retention is promised, listener and payload cleanup, and no
unrelated DOM writes. Generated Go must continue to parse, type-check, and
execute fixtures. `vela ssr --check` will expose all server-host and
client-deferred decisions with the full component path before artifact output.

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
