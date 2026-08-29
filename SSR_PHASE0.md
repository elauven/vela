# Vela SSR Phase 0 — locked implementation contract

Status: design input for Task 007. This document narrows `SSR.md` into the first implementable server-rendering checkpoint. It deliberately does not add runtime or code-generation behaviour.

## Boundary

Vela will first ship static SSR. It takes a validated `ComponentAST`, creates a typed server render plan, and emits Go. It must not invoke JavaScript, run `$effect`, attach events, proxy deep state, or create client DOM nodes.

Hydration remains part of Task 007, but it is a later checkpoint built on the same render-plan order. Static SSR must therefore preserve node identity and dynamic-position information even though it does not yet emit markers.

## Renderer API

Go does not permit these two desired forms to share the name `Card`:

```go
func Card(w io.Writer, props CardProps) error
func Card(props CardProps) vela.Renderer
```

The generated API uses distinct names instead:

```go
func RenderCard(w io.Writer, props CardProps, slots CardSlots) error
func Card(props CardProps, slots CardSlots) vela.Renderer
```

`Card` returns a tiny renderer wrapper whose `Render` delegates to `RenderCard`. Direct calls are efficient and make component lowering simple; the wrapper is opt-in for composition. `CardSlots` contains named `vela.Slot` closures, each with the shape `func(io.Writer) error`.

## Go AST output invariant

Every final Go artifact — component file, prop/slot type, statement, expression, import, and string literal — is built with standard-library `go/ast` nodes and positions from `go/token`. `go/format.Node` is the only printer. The test path independently parses the printed file with `go/parser` and type-checks it through `go/types`.

Template text may appear only as the semantic value of an AST string literal. The SSR backend must never assemble executable Go with string concatenation or parse compiler-authored Go snippets. This gives the same structural guarantee that the JavaScript target AST now has.

## Phase-1 expression contract

The server translator consumes cached `TemplateExpr.Root()` JavaScript ASTs; it must never parse expression source text again. The first implementation supports only constructs whose Go semantics and runtime types are known:

| Form | Initial server lowering |
| --- | --- |
| string, number, boolean, null literals | Go literal / `nil` |
| exported prop identifier | `props.<ExportedName>` |
| member and computed index access | Go selector/index after type proof |
| `+`, arithmetic, comparisons, `!`, `&&`, `||`, ternary | direct lowering only where Go types make the operation sound |
| array length | `len(value)` |

Calls, user script helpers, JavaScript methods, object/array construction, assignments, update operators, `new`, `this`, arbitrary template literals, and browser globals are outside Phase 1. They emit a positioned `VELA-SSR-EXPR` error. The future Go escape hatch from `SSR.md` is not syntax yet and therefore must not be accepted or documented as working language syntax.

This strict rule differs intentionally from the future hydration placeholder strategy: a static page has no client to repair missing output.

## Static event bridge

Server rendering cannot execute user event handlers; their closures and reactive DOM update plan live in the client. Phase 1 nevertheless produces a stable, deliberately simple event bridge:

- The render plan assigns every event-bearing element a compiler-owned unique `data-vela-event` key. It does not overwrite an author-provided HTML `id`.
- The generated external client companion module locates that key under the SSR root and uses the platform `addEventListener(eventName, handler)` API. No inline executable handler is placed in server HTML.
- The bridge may activate only a handler proven not to write Vela state, use bindings, or require a template update. Handlers outside that profile receive `VELA-SSR-EVENT-REACTIVE` until hydration owns state restoration and DOM updates.
- Event order and key allocation are part of the render plan and therefore become inputs to the later hydrator; no second ad-hoc DOM traversal is permitted.

This makes normal native event registration an intentional compatibility layer without claiming that a static page can perform reactive UI updates.

## State and lifecycle contract

- Exported props are server inputs. Their initial/default values inform type inference but do not create client-like setters.
- `$state` and `$derived` values are evaluated once only when their initial expression is translatable. `$effect`, event handlers, and bindings do not run server-side.
- Server output escapes text and attributes through the Go runtime. Attribute names remain compiler-validated; raw HTML is not introduced in Phase 1.
- An unsupported dynamic construct fails compilation, not rendering.

## Future hydration wire contract

The server and client will receive their node/dynamic ordering from one typed render plan. When hydration starts, each dynamic range will be delimited by paired comments, for example `<!--v0-->text<!--/v0-->`. The generated page will include a safely escaped inert payload:

```html
<script type="application/json" id="vela-state-<component-id>">{...}</script>
```

The client hydrate entry point reads this payload and uses the same ordered plan to acquire nodes. It may report a mismatch; it must never silently clear and recreate the component tree on first mount.

## Hand-written target: Card

`examples/dashboard/components/Card.vela` establishes the composition target. The first server backend must eventually be capable of producing the following shape (details of inferred property types may improve in Phase 2):

```go
type CardProps struct {
    Title    string
    Subtitle string
    Padding  string
    NoPad    bool
}

type CardSlots struct {
    Default vela.Slot
    Actions vela.Slot
    Footer  vela.Slot
}

func RenderCard(w io.Writer, props CardProps, slots CardSlots) error {
    // Writes escaped static/dynamic markup. Calls slots.Actions only under
    // the title branch; calls slots.Default unconditionally; calls
    // slots.Footer last. The scoped stylesheet is emitted separately.
    return nil
}

func Card(props CardProps, slots CardSlots) vela.Renderer {
    return vela.RendererFunc(func(w io.Writer) error {
        return RenderCard(w, props, slots)
    })
}
```

`Card` deliberately includes slots and nested `#if` blocks, so it is a Phase-2 target, not a claim that Phase 1 already handles all of its source.

## Corpus observations

The read-only inventory covers 31 maintained `.vela` files: all `examples/`, the positive todo compatibility corpus, and `test_projects/qwen-dashboard-vela`. It deliberately excludes local user worktrees. Representative parsed template forms are `{title}`, `{todo.done}`, `{filter === 'all'}`, `{positive ? '▲' : '▼'}`, `{orders.length > 0}`, `{formatCurrency(order.amount)}`, `{newTitle.trim() === ''}`, event callbacks such as `{() => filter = 'all'}`, and keyed blocks such as `{#each orders as order (order.id)}`.

The maintained examples and compatibility projects include all of these classes: simple identifiers, member access, literal and boolean conditions, ternaries, arithmetic, calls into script helpers, chained JavaScript methods, arrow functions, template literals, event/binding assignments, keyed each expressions, and await promises. The initial translator must be driven by the first four classes only; the others are intentionally diagnostic until a separate, tested extension is accepted.

The Phase-1 fixture will use a purpose-built small component rather than an existing app whose expressions exceed that subset. `Card.vela` is retained as the API/composition reference for Phase 2.
