# AST Codegen Migration — Phase E status & next step

This document tracks the migration of the Vela compiler from the
legacy string-concatenation generator (`codegen.go` / `fraggen.go`)
to the AST-based generator (`astgen.go` + `jsprinter.go`).

## Where things stand

Phases A–D are complete: the AST generator builds a unified,
validated `JsProgram` and is the production default. `generateCode`
(legacy) survives only as an internal subroutine behind the
splices and as the deprecated `--codegen=legacy` escape hatch.

**E1 — done.** Fixed `<style>` injection (was missing from the AST
generator) and the `#each`/`#if`-on-root `$$root` bug (legacy
emitted an undeclared `$$root`).

**E2 — done.** AST inspection tooling: `astjson.go` (reflection-based
`JsProgram` → JSON), `--emit-gen-ast` CLI flag, and
`tools/ast-viewer.html` (collapsible tree, diff mode).

**E3 — in progress.** Convert the remaining splice users to struct
construction so the generator no longer depends on `codegen.go`.

- **E3.1 — done.** `buildIfChain` handles hole-bearing branches
  via the extracted `buildHoleSubtree`. Hole-bearing `{#if}` and
  `{#key}` are struct-built. (The branch wrapper uses `innerHTML`
  rather than legacy's per-element construction — an intentional,
  documented divergence; the form is simpler.)
- **E3.2 — done.** `{#await}` lowers to a struct-built 3-branch
  chain + driver via `buildAwaitBlock`; no splice. Calls the
  E3.1-capable `buildIfChain`.
- **E3.3 — core done.** `{#each}` is struct-built by
  `buildEachBlock` — anchor, fragment builder, keyed/unkeyed
  reconciler, block storage, wiring. No splice for the common
  case. Verified byte-identical to legacy for unkeyed, indexed,
  and keyed rows. Fragment captures use a fragment-local $$fN
  namespace (`withFragmentIDs`); `buildHoleSubtree` gained a
  `collectUpdates` mode (row update() re-runs writes) and a
  fragment-only direct-text-node fast path.
  Remaining gap — E3.3b below.

Remaining splice users: `buildEachBlock` for per-row-dynamic rows
only (E3.3b), and `buildElementSplice` (E3.4).

- **E3.3b — done.** Per-row events, binds, and class:/style:
  directives in {#each} are struct-built (`buildEachRowAttrs`),
  with row-item references rewritten via `rewriteItemRef`.
  `each_row_class` matches legacy byte-for-byte; `each_row_event`
  is a documented divergence (cleaner handler hoisting). Only
  {#each} rows with NESTED CONTROL-FLOW (an {#if}/{#each} inside a
  row) still route to `buildModuleSplice` — a recursion case.

Remaining splice users: `buildEachBlock` for nested-control-flow
rows only, and `buildElementSplice` (E3.4).

## E3.4 + E3.5 — recursive walker — DONE

The recursive element walker (`buildNodes`/`buildElement`/
`buildChildren`/`buildChildRun`) is built and wired. The AST
generator constructs arbitrarily nested dynamic content — nested
elements with events/binds/directives, components, slots — without
splicing. `buildElementSplice` is deleted.

E3.5 closed the root-directive seams: `#if`/`#each` on a root
element now build directly (`if_on_root` via element-branch
normalization in `buildIfChain`; `each_on_root` reframed to an
EachBlockNode through `buildChainRootDOM`). `stripBranchDirectives`
clears a consumed `#if`/`#each` off a branch element.

### Remaining splice seams (2)

1. `buildComponentNode` splices ONLY when a component has slot
   children — slot-closure construction is not yet struct-built.
   Components without slotted children are fully struct-built.
2. `buildModuleSplice` is now reached by exactly ONE shape:
   an `{#each}` row whose body contains nested control-flow
   (`{#if}`/`{#each}` inside a row). Everything else is
   struct-built.

## Post-E-stabilize fix — {#if} no longer wraps in a <div>

An {#if}/{#key}/{#await} branch was built as a wrapper <div>
holding the branch content — so `<section>{#if x}<p/>{/if}</section>`
produced `<section><div><p/></div></section>`.  The stray <div>
broke parent>child CSS selectors, flex/grid child layout, and
:first-child, and made the rendered DOM not match the template.

The wrapper existed because the chain function needs a single
handle to remove/re-insert a branch as a unit.  Fix: build each
branch into a DocumentFragment so its top-level nodes become
DIRECT children of the parent.  Because a fragment empties on
insertion, the branch's top-level nodes are captured into an array
(bv = [...frag.childNodes]) — that array is the branch handle:
removal iterates it calling .remove(); re-attach splices the nodes
back before the anchor comment.

Supporting changes: fillHTMLStmts populates a target via a
<template> element (a DocumentFragment has no innerHTML setter) —
gated by a new fragTarget param on buildHoleSubtree so plain
element targets keep the simpler innerHTML path; initial hole
writes were split into initText that runs after the node array is
captured (they are bv-guarded).  $$insertHTML already handled
fragments (nodeType 11).

Legacy still emits the wrapper; conditional/keyblock/await are now
in knownDiff + knownLegacyBugs as intentional divergences.
Verified by all 8 runtime-behaviour tests (toggle, reopen, hole
branches, nested {#if}-in-{#each}, component-in-{#if}).

## Post-E-stabilize fix — template-literal interpolation positions

A `.vela` whose <script> had a template literal with an
interpolated identifier (e.g. `HTTP ${response.status}`) reported a
bogus TDZ error: "cannot access X before initialization", with the
error position pointing at an unrelated early line (a comment).

Root cause: structureTemplate parses each ${…} interpolation from a
DETACHED substring via parseJSExpr — so every node inside got a Pos
relative to the substring start (0), not an offset into the real
source.  The TDZ check (id.Pos < bind.Pos) then saw the
interpolated ident's Pos (~0) as preceding its own `const`
declaration and flagged a false violation.

Fix: a shiftTree(root, delta) pass (jsparser.go) walks the parsed
sub-expression via WalkNode and shifts every node's Pos/End onto
the original source coordinates (delta = template Pos + 1 + body
offset of "${").  shiftPos is a new method on *baseNode;
posShiftable the interface.  Synthetic nodes (Pos==0 && End==0) are
skipped.  Genuine TDZ violations inside template literals are still
caught — only the coordinate drift is corrected.

Regression: TestTemplateLiteralInterpolationPosition (scope_test).

## Post-E3.6 fix — component lifecycle in {#if} branches

Two bugs with a component placed inside an {#if} branch (Toast /
Modal — both are {#if open}<div/>{/if}, and Demo invokes them
inside its own {#if}):

1. **Second open showed empty content.** A component's mount is
   guarded `if (!$$nN_comp) $$nN_comp = Comp(...)`.  The instance
   var is hoisted (persists across branch toggles), so on branch
   re-entry the guard skipped re-creation — but the branch DOM,
   including the component wrapper, had been rebuilt fresh and was
   empty.  Fixed: buildIfChain now records each branch's component
   instances (capturedScope.comps) and, on branch removal, emits
   `$$nN_comp.destroy(); $$nN_comp = null; $$nN_comp_wrap = null`
   (destroyComps).  Nulling the instance makes the mount guard
   fire again on re-entry — mirrors legacy walkComponentIfChain.

2. **TDZ on reopen — $$nN_comp_wrap.** The wrapper element was a
   `const` in the branch-build / slot-closure body, invisible on
   rebuild and unassignable by the removal cleanup.  Fixed: the
   wrapper var, like the instance var, is now always hoisted
   (`let … = null` declaration in g.hoisted; assignment in g.body).

Regression: if_component_toggle.

## Post-E3.6 fix — slot content didn't re-render

A component event handler in a slot ran (after the earlier fix)
but reactive state changed in it never re-rendered the slot's
interpolations — e.g. {count} inside a <Card> slot stayed at 0.

Root cause: buildSlotClosures built slot content under
captureScope and discarded cap.updates — so the slot's hole
updates ($$nN.data = …) never joined the setter cascade. The
setter changed the variable; no DOM write replayed.

Two-part fix:
  - cap.updates from slot content are merged into g.updates so
    the setter cascade replays them.
  - slot-content hole-node vars must then be reachable from the
    cascade (component scope) — buildChildren builds slot content
    with hoist=true, routing the `let $$nN = null` decls to
    g.hoisted.  Each hole update is self-guarded (`if ($$nN) …`,
    the new $$self guard sentinel) so a cascade replay before the
    slot first opens is a safe no-op.

Regression: slot_reactive_hole.

## Post-E3.6 fix — component events + TDZ in slots

Two bugs with components placed inside another component's slot:

1. **Slot-content events dropped.** buildSlotClosures used only
   cap.body and cap.initBody from the captured scope — cap.events
   was discarded, so a component @click inside a slot wired no
   listener. Fixed: cap.events fold into the closure's init text;
   cap.cleanup goes to component-scope teardown.

2. **TDZ on slot reopen.** A child-component instance var
   (`let $$nN_comp`) and the component-event handler const were
   emitted into the captured slot-closure scope — so they lived in
   the closure's first-open path only. The reopen path references
   them without re-declaring → ReferenceError "can't access
   lexical declaration before initialization". Fixed: a new
   ASTGen.hoisted buffer, which captureScope deliberately does NOT
   redirect, carries these declarations to component scope.
   buildModule emits g.hoisted just before g.body.

Regression: slot_component_event.

## Post-E3.6 fix — component @event handlers were dropped

buildComponentNode built the props object and slot closures but
never wired @event attributes — so component events (button
clicks, tab @select, modal @close, …) silently did nothing, no
error. Fixed: buildComponentEvents wires each @event as a
CustomEvent-guarded listener on the component wrapper div, with a
matching removeEventListener in teardown — identical to legacy
walkComponent's event forwarding. Regression: component_event.

## Post-E3.6 fix — WalkNode object-key false positive

The validator's checkUndeclaredRefs flagged $$onChange_open (a
bindable-prop callback) as an undeclared variable. Root cause:
WalkNode descended into non-computed object-property KEYS as if
they were references — so the property key in
`{ $$onChange_open: ... }` was walked as an ident read. Fixed:
WalkNode now skips non-computed property keys entirely (shorthand
and keyed alike — a key is a name literal, like a member's .Prop).
Shorthand references are unaffected: prop.Value holds the ident.
Regression: component_bind_prop in the corpus.

## Post-E3.6 fix — buildIfChain branch gating

A nested {#if} reached via the recursive walker dispatched
straight to buildIfChain, which then called buildHoleSubtree for
any non-static branch WITHOUT checking astHoleSubtreeOK. A branch
containing a directive-bearing element (class:/style:) made
buildHoleTemplate panic — appendHoleNode has no arm for such an
element. Fixed: buildIfChain's branch-plan loop now routes a
branch that is neither allStatic nor astHoleSubtreeOK through the
recursive walker (captureScope + buildChildRun), the same
mechanism {#each} nested rows use. Regression: if_branch_classdir
in the corpus.

## E3.6 — DONE — Phase 3 complete

The recursive walker now emits into local scope via captureScope,
and both remaining seams are closed:

- **{#each} nested-control-flow rows** — the fragment builder uses
  captureScope + the recursive walker for rows containing
  {#if}/{#each}/{#await}. Doubly-nested {#each} builds directly.
- **Component slot children** — buildSlotClosures builds each
  slot as a struct-built fragment-factory closure (first-open
  builds + caches children; reopen re-adopts them). The 4280slots
  object is passed to the component constructor.

spliceLegacyBlock is DELETED — it has no callers.

buildModuleSplice is RETAINED as a defensive fallback only: the
recursive walker handles every construct, astCanBuildDirectly
returns true for all well-formed input, so it is never reached in
practice. It is kept so any unanticipated shape degrades to valid
output rather than a hard failure.

### Two documented divergences

- slots / component — the recursive walker emits in document
  order; legacy uses create/wire buffer partitioning. Both valid,
  same DOM. Listed in knownDiff / knownLegacyBugs, covered by the
  corpus validity check.

### E4 — remaining cleanup (POSTPONED)

generateCode / codegen.go / fraggen.go are reachable ONLY via
buildModuleSplice (defensive, never taken) and the deprecated
--codegen=legacy flag. They are NOT deleted yet — deliberately.
The last several real-world bugs were diagnosed by comparing AST
output against legacy in the differential harness; legacy is the
oracle. E4 waits until the AST generator has run a real project
for a stretch with no new bugs. It sits on no live path and costs
nothing to keep.

## NEXT PHASE — E-stabilize (testing)

The recent bug run (slot/component lifecycle, captureScope
boundary) shared a trait: every failure was a RUNTIME error — TDZ,
empty content, dead listener — that both `node --check` (syntax)
and structural-equality (TestASTGenMatchesLegacy) passed. The
harness validates syntax and structure, not BEHAVIOUR. Closing
that gap is the focus.

### E-stabilize 1 — runtime-assertion harness (highest leverage)

Build a jsdom-based test harness that COMPILES a .vela component,
loads the generated JS, mounts it into a jsdom document, and
asserts on behaviour:
  - initial render produces the expected DOM
  - a state change updates the DOM
  - an {#if}/{#each} toggle adds/removes content
  - re-toggling re-renders correctly (catches the reopen bugs)
  - component events fire; teardown runs
jsdom + node are available. This harness would have caught all
five recent bugs at test time. Start here.

### E-stabilize 1 — runtime-assertion harness — DONE

tools/runtime-harness.mjs + runtime_behaviour_test.go: compiles a
.vela component (or a set, for cross-component imports), mounts it
in a jsdom document, replays click/assert steps, checks behaviour
— initial render, reactive updates, {#if}/{#each} toggles + reopen,
component events, teardown.  Skips cleanly when node/jsdom are
absent.

Already caught a real bug on its first multi-component test: a
slot-content element's removeEventListener was placed in the host
component's destroy() (component scope) while the element var was
closure-local — a ReferenceError in destroy().  Fixed:
emitEventHandler omits component-scope event cleanup for slot
content (those listeners die with their nodes when the slot
rebuilds / the component tears down).

This is exactly the gap the harness was built to close — a
runtime fault that node --check and structural equality both pass.

### E-stabilize 2 — differential fuzzing — DONE

codegen_fuzz_test.go: FuzzCodegen drives a bytes-seeded generator
that builds syntactically valid .vela templates by recursively
composing elements, {#if}, {#each}, holes, inline @click, class:/
style: directives, and component invocations (with prop, @event,
and default + named slot children).  Each generated source is
compiled and checked: no panic, ValidateProgram clean, printed JS
re-parses.  Inputs the generator makes unparseable are skipped
(not codegen bugs).  The generator only emits valid Vela — it
targets codegen robustness, not the parser.

A seed corpus runs on every `go test`; `make fuzz` (FUZZTIME=…)
runs the explorer.  ~65k executions across two runs, 430+ distinct
interesting inputs, zero crashes / validation failures — the
{#if}/{#each}/component/slot combination space the recent bugs
lived in is now systematically covered.

### E-stabilize 3 — corpus matrix expansion

Generate randomized .vela inputs — nested {#if}/{#each}/component
/slot combinations — and assert: AST output validates (no
ValidateProgram issue), node --check passes, and (where the shape
is legacy-supported) structural equality holds. The bug class
"construct X inside captured scope Y" is combinatorial; fuzzing
explores it systematically. The legacy oracle is still here to
check against.

### E-stabilize 3 — corpus matrix expansion

Systematically cover the combination matrix the recent bugs lived
in: component in an {#each} row, slot inside {#each}, {#await}
with component branches, deeply-nested mixtures. The ad-hoc
regressions added so far (if_branch_classdir, slot_reactive_hole,
if_component_toggle, …) are good but not systematic.

### E-stabilize 3 — corpus matrix expansion — DONE

Seven named cross-construct corpus entries — the combination space
the recent bugs lived in, each named so a failure points straight
at it: each_row_component, each_row_if, if_branch_each,
each_nested, if_branch_slot_component, await_component,
each_row_event_if.  Plus four jsdom runtime-behaviour tests for
the riskiest (EachNested, EachRowIf, IfBranchEach,
EachRowComponent).

The runtime harness caught two real bugs the fuzzer's
codegen-robustness check passed clean:

1. **Component per {#each} row built only once.** buildComponentNode
   hoisted the instance var to component scope — but a fragment
   runs once PER ROW, so all rows shared one instance and the
   `if (!$$nN_comp)` guard skipped rows 2..n.  Fixed: in fragment
   scope (fragCounter >= 0) the wrapper + instance vars are
   fragment-local `const`s, one per row.

2. **Nested {#if} in an {#each} row ignored component state.** The
   per-row {#if}'s chain function is fragment-local — the
   component setter cannot call it.  A change to a flag the row
   body reads did nothing.  Fixed: buildEachBlock collects the
   component-state deps a nested-control-flow row body reads
   (cap.updates keys) and registers the whole-{#each} re-render
   against each — changing such a dep rebuilds every row,
   re-evaluating its nested chains.

Confirms the breadth/depth split: the fuzzer says "it compiles",
the jsdom harness says "it works" — both are needed.

## Post-stabilize fix — '<' in interpolation + {#if}-branch dynamic attrs

Two unrelated bugs found in real components:

1. **`<` inside a {…} interpolation parsed as a tag.** A comparison
   like `{i < n}` in template text made the scanner stop at the
   `<` and try to parse an HTML tag → "empty tag name".  Fixed:
   parser.parseText tracks brace depth — a `<` at depth > 0 is
   part of the interpolation expression, not markup.

2. **Dynamic attribute in an {#if} branch never updated.** An
   element inside an {#if} branch built by the recursive walker
   had its node var emitted as a `const` trapped in the branch-
   build block, and its reactive attribute updates (cap.updates)
   discarded.  So e.g. a progress bar's :style only rendered once.
   Fixed: (a) buildIfChain merges the recursive-walker branch's
   cap.updates into the setter cascade, branch-var-guarded; (b) a
   new inBranchScope flag makes buildElement hoist the element var
   (`let X = null;`) so the cascade can reach it — mirrors the
   inSlotScope mechanism.

Regressions: interp_less_than, if_branch_dynattr (corpus);
TestRuntimeIfBranchDynamicAttr (runtime harness).

## Post-stabilize fix — $effect reading a $derived not re-run

An $effect that reads a $derived did not re-run when the derived's
UPSTREAM $state changed — only when a variable the effect read
DIRECTLY changed.  E.g. `previewHtml = $derived(marked(markdownText))`
and `$effect(() => previewRef.innerHTML = previewHtml)`: typing in
the textarea updated previewHtml but the effect never fired, so the
preview only refreshed on an unrelated re-render.

Root cause: buildSetterCascade step 3 re-ran an effect only when
eff.Deps contained `name` (the directly-set var).  Step 1 already
computes the full transitive `dirty` set (name + every derived it
dirties); step 3 must check effects against `dirty`, not just
`name`.  Fixed: an effect re-runs when any of its deps is in
`dirty`.

Regression: TestRuntimeEffectReadsDerived (runtime harness).

## Shared runtime helpers — DONE

$$dispatch, $$insertHTML and $$proxy were inlined into every
component.  They now live in runtime.js as exports; a component
imports whichever it uses.

  - AST generator: finalizeRuntimeHelpers scans the built program
    and prepends `import { … } from './runtime.js'` for only the
    referenced helpers, plus a thin `const $$dispatch = (name,
    detail) => $$rt_dispatch($$target, …)` binding (the runtime
    export takes target as its first arg).
  - Legacy generator: imports all three unconditionally (it is
    deprecated — not worth usage-tracking).
  - finalizeRuntimeHelpers is idempotent: the splice path
    re-parses legacy output that already carries the import, and
    is detected + skipped.
  - The differential tests gained stripRuntimeHelperWiring, which
    normalizes the helper import + binding away before structural
    comparison (legacy emits unconditionally, AST usage-tracks — a
    systematic, not per-fixture, difference).
  - The runtime harness now drains microtasks after mount and
    after each click: components import runtime.js, which loads
    the real batching $$defer, so assertions must wait for the
    microtask flush.  (This surfaced as a false IfToggle failure —
    a harness gap, not a compiler bug.)

## Component anchor — no wrapper (bug 4)

The last open bug from the backlog.  Before this fix, a child
component invocation produced a parent-side wrapper:

  <div class="parent">
    <div style="display:contents" vela-PARENT>      ← the wrapper
      <button class="my-component">…</button>       ← child $$root
    </div>
  </div>

`display:contents` made the wrapper layout-transparent, but the
`<div>` was still in the DOM tree.  Any CSS rule using direct-
child selectors broke silently:

  .parent > .my-component { … }    /* never matched */
  .parent > :nth-child(2) { … }    /* counted the wrapper */

The wrapper had three concrete roles, all of which had to be
re-homed to remove it:

1. Stable insertion anchor — a fixed position in the parent's
   DOM that the parent owned, before any component was mounted.
2. CustomEvent listener target — the parent's `@event` attrs
   added listeners on the wrapper; the child dispatched on
   $$target (= the wrapper) and the event was caught there.
3. CSS scope-id carrier — the wrapper held the parent's scope
   attribute so "the component" was identifiable as part of the
   parent's scope.  (Mostly cosmetic; user CSS targeting
   components never resolved through it anyway.)

### Fix

Wrapper replaced by a single Node.COMMENT_NODE anchor:

  parent.appendChild(document.createComment('comp'));
  inst = Child(anchor, props, slots);

Three coordinated changes carry the new contract:

**Component-side mount accepts an anchor.**  The generated mount
line now branches on `$$target.nodeType`:

  if ($$target.nodeType === 8) {                   // Comment
    $$target.parentNode.insertBefore($$root, $$target);
  } else {
    $$target.appendChild($$root);
  }

When the target is a Comment, the component inserts $$root BEFORE
the anchor — at the document-order position the caller set up.
When the target is an Element (e.g. the document root passed in
by user code: `App(document.getElementById('root'))`), it still
appendChild's.  The compiler-emitted call sites only ever pass
a Comment now; the Element branch is for user-supplied targets.

**$$dispatch fires on $$root, not $$target.**  The shim used to
capture $$target:

  const $$dispatch = (name, detail) =>
    $$rt_dispatch($$target, name, detail);

With $$target now being the parent's anchor (which lives in the
parent's DOM), dispatching there would fire events that bubble
up the parent's tree — sibling components could see each other's
events.  Switching to $$root keeps events local to the component:

  const $$dispatch = (name, detail) =>
    $$rt_dispatch($$root, name, detail);

The arrow reads $$root lazily; by the time any user handler
invokes $$dispatch (always after mount), $$root is initialized.

**Component @event listeners attach on inst.el after mount.**
The listener target moved from `wrap` to `inst.el` (the
component's $$root).  This required moving the listener-attach
from g.events (runs BEFORE g.initBody) into g.initBody AFTER
the mount call — inst.el doesn't exist until the constructor
returns.

The component instance now exposes its $$root via the `el`
field:

  return { el: $$root, destroy() { … }, set(props) { … } };

Caller-side, `inst.el.addEventListener(name, hv)` runs right
after the mount inside g.initBody.  In captured scopes (an
{#if} branch, an {#each} row, a slot factory), this lands in
plans[i].initText / fragBody / the slot factory body — all
after the mount call.  Branch teardown's `cap.cleanup` closure
captures the `inst` reference; `inst.el.removeEventListener`
fires at the right time.

### Cost: AST↔legacy differential is now disabled

The mount line changed for EVERY component the AST generator
produces — even templates with no child components.  The
differential test compared AST output to the legacy generator
character-by-character; with the universal divergence, it
flagged every corpus entry as a failure.

The differential test was useful when AST was claiming to be
"the same as legacy, just structurally cleaner".  With this
fix, AST is intentionally BETTER.  The differential's job is
done.  Both differential tests (TestASTGenCorpusMatchesLegacy
and TestASTGenMatchesLegacy) now skip with a clear pointer to
this section.  When E4 (delete the legacy generator) lands,
the tests vanish entirely.

The matrix safety net (29 nesting tests + this fix's 2 new
tests) plus the behavioural codegen tests plus the fuzzer are
the replacement safety net.

### Multi-root components

A component with multiple root elements wraps its OWN siblings
in a `display:contents` div internally.  That wrapper is the
component AUTHOR's choice (they wrote a multi-root template),
not the parent's.  After this fix:

  <parent>
    <componentMultiRootWrapper style="display:contents">
      <…siblings…>
    </componentMultiRootWrapper>
  </parent>

The `.parent > .sibling` selector STILL doesn't match because
of the component's internal wrapper.  This is an honest
trade-off: a multi-root component is by nature a "group of
elements with one logical name", and the wrapper is the
mechanism it uses to be one DOM node from the parent's
perspective.  Eliminating that wrapper would require sibling-
range tracking (open/close anchor pairs) — a much larger
refactor with its own correctness concerns.  Left as future
work.

### Regression tests

- TestRuntimeNestingMatrix_ComponentDirectChildSelector: a
  parent with siblings flanking a `<Child/>` — direct-child
  CSS selectors find the child's root, nth-child counts work,
  document order is preserved.
- TestRuntimeNestingMatrix_ComponentEventStillFires: a child
  dispatches a CustomEvent, the parent's `@event` handler
  catches it — verifies the new dispatch-on-$$root + listen-on-
  inst.el path.
- Updated TestComponentMountUsesAnchorComment_AST (renamed
  from the now-incorrect TestComponentWrapperIsDisplayContents
  _AST): pins the anchor-comment shape and the nodeType===8
  branch in the generated mount.

### Gates

Full suite (~607 tests, less 2 differential families now
skipped) green twice (in-tree + fresh-extract); vet clean;
fuzzer clean × 2 passes.  PageCraft e2e: focus preserved,
canvas updates, zero errors — the most important real-world
regression check.

### Backlog after this fix

All four bugs from the post-arc backlog are fixed.  Open items
are now features and architecture work — none "broken", all
"could be better":
  5. {#if}-component-preserve-state (mirror of Session A).
  6. Vite plugin / dev-server integration.
  7. Multi-component integration fixture as permanent test.
  8. E4 — delete the legacy generator (~5000 LOC; the
     differential tests already skip, so this is now
     unblocked).
  9. Consolidate branch teardown — make the closure the sole
     path, retire destroyComps/nestedTeardowns redundancy.

## Aliased intrinsics now resolve correctly

Documented in MIGRATION.md as a deferred issue since the early
post-Session work, fixed now as backlog item 3.

### Pre-fix shape

  import { onMount as init } from 'vela';
  init(() => { … });

The import statement was correctly stripped (the imported name
matched a Vela compiler intrinsic), but `extractLifecycle`
matched callees by literal name only — `init` did not equal
`onMount`, so the call site survived as a reference to an
undeclared local.  Module load threw "init is not defined".

The asymmetry was the bug: import-stripping recognised the
intrinsic by ORIGINAL name (matching `onMount`), but the call
recogniser used the LOCAL name.

### Fix

Analysis grows a small map IntrinsicAliases[localName] →
intrinsicName.  Populated during import processing, only for
imports whose source is 'vela' or 'vela/store' (a user's own
function called `onMount` from their own module remains a user
function, isolated from the compiler's intrinsic logic).
extractLifecycle takes the map as a parameter and resolves the
callee name through it before the switch — so `init(() => …)`
sees `name == "onMount"` and emits as a lifecycle hook.

The kind that gets recorded on LifecycleHook is the resolved
intrinsic name (`"onMount"` / `"onDestroy"`), not the alias —
codegen continues to switch on `lc.Kind == "onMount"` without
needing to know about aliasing.

### Adjacent behaviour, NOT changed by this fix

A direct `onMount(...)` callee with no preceding import is
still recognised as an intrinsic (the historical "intrinsic by
convention" behaviour).  Likewise, `import { onMount } from
'./other.js'; onMount(...)` is STILL hijacked by the compiler
— the direct-name match in extractLifecycle does not consult
the import source.  That's a pre-existing asymmetry, separate
from the alias case; addressing it would be a behaviour
change for users (however unlikely they are to actually have
their own `onMount` from a non-Vela module).

### Regression tests

- TestAliasedOnMount_Recognized: `init(...)` removed from
  output, mount wiring present.
- TestAliasedOnDestroy_Recognized: `teardown(...)` removed,
  destroy() body emitted.
- TestAliasedBothIntrinsics_BothRecognized: both work in one
  script.
- TestAnalyzer_IntrinsicAliases_Populated: the map gets the
  expected entries.
- TestAnalyzer_IntrinsicAliases_NotPopulatedForOtherSource:
  imports from non-Vela sources do NOT pollute the map.

### Gates

Full suite green twice (in-tree + fresh-extract); vet clean;
differential AST↔legacy in sync; fuzzer clean × 2 passes.
PageCraft e2e clean.

## Branch cleanup statements now actually fire

Documented during Session 3 as a watch-point, fixed as a follow-up
preventive cleanup of the post-arc backlog (item 2).

### The pre-fix shape

captureScope collected `cap.cleanup` — statements builders had
emitted via g.cleanup while building the branch body
(removeEventListener for @click handlers and bindings,
inst.destroy() for components, blocks.forEach(destroy) for
nested {#each}).  buildIfChain forwarded buildText, events,
initBody, comps, fragHoisted, updates, and nestedTeardowns out
of the capture — but cleanup was silently dropped.

In practice the current emitters were all harmless: they
reference targets reclaimed when the branch's DOM is removed
(listeners on branch-local elements, components also tracked
via plans[i].comps + destroyComps, each rows tracked via
nestedTeardowns).  The drop was a real leak waiting on a future
builder that emits cleanup with reach OUTSIDE the branch's DOM
— a window/document listener, a setTimeout/setInterval, an
external subscription returning an unsubscribe function.

### The fix — per-branch cleanup closure

Each branch declares a `_bN_cleanup` slot alongside its `_bN`
handle.  At build time (inside `if (!bv) { … }`), if the
branch's cap.cleanup is non-empty, the build stores a closure
into the slot:

  _bN_cleanup = () => {
    /* every statement from cap.cleanup */
  };

The closure captures the build-block locals — `const handler =
…`, `const $$nN = createElement(...)` — so cleanup statements
that reference those names work even after the build block
exits.  At branch teardown (inside the per-branch
`if ($$active !== i && bv) { … }`), the chain invokes the
closure FIRST (before removing branch nodes — order matters for
cleanups that read element state), then nulls the slot:

  if (_bN_cleanup) { _bN_cleanup(); _bN_cleanup = null; }

Branches with no cleanup leave the slot at null forever — the
`if (_bN_cleanup)` teardown guard makes that a free no-op.

### Idempotence with existing teardown mechanisms

The closure may now `inst.destroy()` for components that are
ALSO destroyed by `destroyComps(plans[i].comps)`, or call
`blocks.forEach(destroy)` for each blocks ALSO torn down via
nestedTeardowns.  This is intentionally redundant and safe:
- destroyComps nulls the inst variable after destroy; a second
  `if (inst) inst.destroy()` becomes a no-op.
- blocks gets reset to `[]` by nestedTeardowns; a second
  forEach iterates an empty array.
- removeEventListener on an already-removed element is a no-op.

Removing the redundancy would require either replacing
destroyComps + nestedTeardowns with the closure mechanism
(invasive) or filtering cap.cleanup statement-by-statement
(brittle).  Both are out of scope for a preventive fix.  The
clean refactor — make the closure the sole teardown path,
delete destroyComps — would be its own session.

### Regression tests

- TestIfBranchCleanupClosure: an @click handler inside `{#if}`
  emits a cleanup slot, a closure assignment in the build path,
  the closure invocation in the teardown, and a
  removeEventListener inside the closure body.
- TestIfBranchCleanupClosure_NoCleanup_NoOverhead: a branch with
  no cleanup gets the slot declaration (one `let` per branch,
  unconditional) but no closure assignment — the teardown's
  `if (_cleanup)` guard makes it a free no-op.

### Observable test outside the suite

Spying on addEventListener/removeEventListener via prototype
patches under jsdom — the BindIf scenario (a bind:value inside
`{#if}`) now shows symmetric add/remove pairs as the branch
mounts and unmounts.  Same for an @click handler inside the
branch (ClickIf scenario).

### Gates

Full suite green twice (in-tree + fresh-extract); vet clean;
differential AST↔legacy in sync; fuzzer clean × 2 passes.
PageCraft e2e: editing the heading preserves focus AND updates
the canvas; zero errors.

## Slot content inside {#each} rows is now reactive

Discovered during Session 3's audit, fixed as the first item of
the post-arc backlog.

Symptom: a child component placed inside another component's
slot inside an {#each} row did not receive reactive prop updates.

  <Wrapper><ChildLog label={it.v} /></Wrapper>

Mutating items[0].v left ChildLog rendering the original value.
No runtime errors — just silently stale UI.

### Root cause: three bugs stacked

1. **Slot-internal component vars block-scoped.**  Build
   ComponentNode's three-arm switch routed the `let $$f4_comp
   = null;` declaration to g.body when inFragment was true.
   But when we are inside a slot factory closure, g.body IS
   that closure — making the var local to a single slot
   invocation.  The fix: combine inBranchScope and
   inSlotScope into "captured" (g.inCapturedScope()) for the
   component-decl routing.  Slot-internal components now
   hoist to g.fragHoisted along with the branch case.

2. **Slot capture consumed cap.fragHoisted into the slot factory
   body.**  Even with (1), the fragHoisted entries still ended
   up trapped in the factory.  The slot factory caches its
   output, so the component instance was reused across slot
   invocations — there was no path for fresh prop values to
   reach the inner child after initial mount.  The fix: the
   slot capture now BUBBLES cap.fragHoisted through g.declare
   to the enclosing scope (row fragment, or component scope
   when no fragment encloses).  All fragHoisted entries are
   safe to host at row scope — chain functions reading row-
   local `it` still see it via closure; component instance
   vars become reachable from the row's update closure.

3. **buildComponentNode's setStmt back-fill assumed [n-1].**
   With (1) and (2), Wrapper's slot capture now appends ChildLog
   to g.compInstances during Wrapper's emission.  When the
   back-fill `g.compInstances[n-1].setStmt = jsStmt(...)` runs
   for Wrapper, n-1 points at ChildLog (the slot's later
   append) — and Wrapper's setStmt was silently dropped.  The
   fix: search backwards for the matching `inst` and write
   there.  Order-independent and resilient to whatever
   intermediate appends a captured scope contributes.

### How it now works

For `<Wrapper><Child v={it.v}/></Wrapper>` inside an {#each}:

  - Wrapper's compInstance lands in g.compInstances (row capture).
  - The slot capture (built inside Wrapper's emission) runs the
    recursive walker; ChildLog's compInstance lands in the
    SLOT capture's g.compInstances, with its setStmt
    `if ($$child) $$child.set({v: it.v})`.
  - On slot capture exit, ChildLog's compInstance bubbles up
    to the OUTER g.compInstances (Wrapper's outer scope is the
    row capture).  ChildLog's wrap+inst vars bubble through
    cap.fragHoisted → g.declare → g.fragHoisted (row scope).
  - Wrapper's setStmt back-fills correctly (searching back
    rather than using [n-1]).
  - Row capture closes: rowComps now contains BOTH Wrapper
    and ChildLog with valid setStmts.
  - Row update closure replays .set() for both on each row tick.

### Regression tests

- TestRuntimeNestingMatrix_SlotContentInEachReactive: the
  basic case — component in a slot in an each, reactive prop
  update reaches the slot-content child.
- TestRuntimeNestingMatrix_SlotContentInEachInIf: combined
  with an outer {#if} toggle — verifies the toggle off+on
  rebuilds cleanly AND inner-slot reactivity survives.

Matrix now 29 cases.

### Gates

Full suite green twice (in-tree + fresh-extract); vet clean;
differential AST↔legacy in sync; fuzzer clean × 2 passes.
PageCraft e2e: editing the heading still preserves focus AND
updates the canvas; zero errors.

### Why this is more than just "another nesting bug"

The fix touched three places — but they trace to one missing
principle: **a captured scope that re-publishes some of its
emissions must re-publish all of them consistently**.  The if-
chain's branch capture established that pattern in Sessions
1–3 (forwarding fragHoisted, comps, nestedTeardowns).  The slot
capture forwarded comps but not fragHoisted — half the
machinery — which silently broke this case.

The next preventive step would be to look for any other
captureScope call sites where cap.fragHoisted is being dropped.
Currently there are two: the if-chain branch capture (now
forwards correctly) and the slot capture (this fix).  Both
should now propagate consistently.  Future captureScope users
should adopt the same pattern unless they have an explicit
reason to be a scope boundary (e.g. an event handler closure).

## Session 3 of 3 — audit pass

Final session: hunt for any `let X = null;` / `function Y(){}` /
`const Z = …;` declarations still going into g.body that should
have gone through the centralised routing introduced in Session 2.
Five hits remained after Session 2; four were correct on
inspection, one was a real bug.

### Hits audited

1-2. **buildEachBlock, lines 2812 / 2814** — `const keymap` and
   `let blocks` in the `else` arm of `if (captured) { … } else
   { … }`.  The `else` path runs when NOT in a captured scope,
   so g.body IS the right scope.  **Correct, no change.**

3-4. **buildComponentNode, lines 3885 / 3886** — `let wrap` and
   `let inst` in the `case inFragment:` arm of the three-arm
   switch (inFragment, not branch-scope).  g.body IS the row
   fragment closure; each row gets its own var naturally.
   **Correct, deliberate.**

5. **buildComponentNode slot factory, line 4066** — `let cacheVar
   = null;` for the slot's previously-built nodes.  Read from the
   slot factory closure stored on the child component's props,
   invoked from inside the child's own component body — i.e.
   from OUTSIDE the current emission scope.  When the slot-
   bearing component is itself inside an {#if} or {#each}
   branch, a plain `let` in g.body becomes block-scoped to that
   branch and invisible from the factory.  **REAL BUG.**

   Repro: `{#if show}<Wrapper><span>{msg}</span></Wrapper>{/if}`
   — initial mount threw `ReferenceError: $$scache_$$nN is not
   defined`.  Fix: route the declaration through declareInScope
   so the var escapes the branch closure to the right enclosing
   scope (fragment if inside an {#each}, component otherwise).

   Regression test:
   TestRuntimeNestingMatrix_SlotComponentInIf — slot-bearing
   child component inside an {#if}, edit + hide + reopen cycle.

### Adjacent finding (documented, not a bug)

The if-chain's branch capture drops cap.cleanup (it forwards
buildText, events, comps, fragHoisted, nestedTeardowns up to
the outer scope, but not cleanup).  This means a
`removeEventListener` emitted inside a branch (e.g. from a
bind:value on an input inside `{#if}`) is silently lost.

In practice this is not a leak: the input element is destroyed
with the branch and GC reclaims its listeners.  But it would be
a hazard if a future builder emitted a cleanup statement that
must run on branch unmount (e.g. tearing down a long-lived
subscription).  Worth noting for future contributors.

### Coverage additions

- `TestRuntimeNestingMatrix_SlotComponentInIf` — the bug above.
- `TestRuntimeNestingMatrix_KeyedEachInIf` — confirms the keyed
  reconciler's `keymap = new Map()` initialiser correctly resets
  on branch reopen.

Matrix is now 27 cases.

### Three-arm switch on buildComponentNode — reaffirmed correct

The three destinations are genuinely different (fragHoisted /
g.body / g.hoisted) and the routing decision matches no other
site.  Generalising it into a new method would add a knob without
simplifying any caller.  The comment block was already cleaned up
in Session 2.  No change.

### Gates

Matrix (27 tests): green twice.  Full suite (~597 tests): green
twice.  vet: clean.  Differential AST↔legacy: in sync.  Fuzzer:
clean across two passes.  PageCraft e2e: editing heading
preserves focus AND updates canvas; nested `{#if p.eyebrow}`
reactively shows/hides; zero errors.

### Honest take on the three-session arc

Session 1 (the matrix) caught six real bugs that had been silently
present in the codebase.  Session 2 (the centralization) was a
net-negative LOC refactor that the matrix caught nothing in.
Session 3 (this audit) caught one additional real bug that the
matrix had not exercised yet.  That bug was a "slot factory's
cache var declared in wrong scope" — exactly the shape Session 2's
declareInScope was designed to handle.

The right summary: Session 1 found the existing bugs, Session 2
made the architecture express the routing rule once, Session 3
verified there were no remaining instances of the pattern (and
found one).  Future builders emitting declarations into closures
should reach for g.declare / g.declareInScope first and consult
the comment headers there.

## Session 2 of 3 — centralize branch-scope routing

Session 1's matrix surfaced the same architectural pattern over and
over: a builder emits `let X = null;` / `function Y(){}` into g.body,
which is correct at component scope but wrong inside a captured
branch closure (where g.body is the outer branch's build block).
Three builders had each learned this lesson independently, ending
up with three near-identical 6-line hoist helpers:

  hoistEach  (in buildEachBlock)
  hoistAwait (in buildAwaitBlock)
  hoistTarget (in buildIfChain)

each computing the same `if g.fragCounter >= 0 { fragHoisted }
else { hoisted }` routing.  A fourth builder (e.g. when something
new is added) would forget.

Session 2 consolidates the routing into three methods on ASTGen:

  g.declare(stmt)        — pick fragHoisted or hoisted based on
                           fragCounter (the scope rule).
  g.declareInScope(stmt) — escape via declare when in a captured
                           scope, else emit to g.body.  This is
                           the body of every builder's per-call
                           `decl` closure.
  g.inCapturedScope()    — `g.inBranchScope || g.inSlotScope`
                           predicate; replaces four open-coded
                           copies.

All four builders migrated:
- buildEachBlock: hoistEach removed; six call sites now use
  g.declare directly.
- buildAwaitBlock: hoistAwait + declAwait collapsed; the `decl
  Await` closure now delegates to g.declareInScope.
- buildIfChain: hoistTarget + decl collapsed similarly.
- buildComponentNode: stale double-comment block cleaned up; the
  three-arm scope decision (fragHoisted / g.body / g.hoisted)
  KEPT as-is because its three destinations are genuinely
  different — the routing decision matches no other site.

### Net effect

~50 lines of duplicated routing logic deleted.  Every builder that
needs to declare into "the right scope" goes through one place.
The next builder to land doesn't have to reinvent the rule.

### Why this matters preventively

Of the six bugs Session 1 surfaced, four were the "builder forgot
to honor inBranchScope" pattern.  After Session 2, the routing is
expressed once, named (g.declare / g.declareInScope / g.in
CapturedScope), and discoverable — a new builder authoring code
will encounter the method before they write the open-coded
if/else themselves.

### Gates after the refactor

Matrix safety net (25 tests): green twice.  Full suite (~595
tests): green.  vet: clean.  Differential AST↔legacy: in sync.
Fuzzer: clean across two passes.  PageCraft e2e: editing the
heading preserves focus AND updates the canvas; nested
{#if p.eyebrow} reactively shows/hides; zero errors.

Session 3 (delete remaining workarounds and any straggling
g.body-instead-of-g.declare paths) is now the final step.

## Session 1 of 3 — nesting matrix as a safety net

The first of three sessions to harden Vela's control-flow nesting.
Goal: a comprehensive test suite that covers every meaningful
combination of {#if}, {#each}, {#key}, {#await} and
<Component>, so future regressions surface at the test level
rather than in user apps.  This delivered runtime_nesting_matrix
_test.go (25 cases) and, in the process, surfaced six real bugs
that were silently lurking in the codebase.  All fixed before the
session closed.

### Bugs Session 1 surfaced

1. **{#await} machinery did not hoist when nested in a branch.**
   The state vars ($$promState, $$awaitVal, $$awaitErr, $$promCell)
   and the driver function were emitted to g.body unconditionally;
   inside an outer branch's build closure they became block-scoped
   and invisible to the component-scope cascade (ReferenceError).
   Fix: same nestedHoist pattern as buildIfChain — route to
   g.hoisted (or g.fragHoisted, see below) when inBranchScope or
   inSlotScope.

2. **$$proxy wrapped exotic built-ins.**  $state.deep wrapped
   every nested object — including Promise, Date, RegExp, Map,
   Set, WeakMap, WeakSet, Error, ArrayBuffer and typed arrays.
   These rely on internal slots a Proxy receiver cannot satisfy,
   so any method call on the wrapped value threw "Method
   <X>.prototype.<m> called on incompatible receiver" — e.g.
   `await it.p` on a $state.deep array of Promises blew up at
   .then().  This is a real production hazard, not just a test
   issue.  Fix: $$proxy now passes these built-ins through
   unwrapped.  Deep reactivity for plain objects and arrays is
   unaffected.

3. **{#each} rows orphaned when an outer {#if} branch tore
   down.**  After the earlier Session A "branch handle does not
   capture each rows" fix, the outer chain only captured the
   each's anchor in its branch handle; the rows lived before that
   anchor and were left in the DOM when the outer branch
   detached.  Symptom: hiding then showing again left duplicate
   rows.  Fix: buildEachBlock now emits a teardown snippet into
   the existing g.nestedTeardowns channel — destroy every row,
   clear the bookkeeping, null the anchor.  The outer chain's
   per-branch teardown reads new entries (snapshot-length pattern
   tdMark = len(g.nestedTeardowns)) and emits them.  Doubly-
   nested propagates naturally.

4. **Component instance vars block-scoped inside
   {#if}-in-{#each}.**  buildComponentNode emitted
   `let $$compN = null;` into g.body — fine at the row-fragment
   top level, but inside a branch closure it became block-scoped
   and code outside the branch (the chain function's teardown
   clause, the row's update closure) could not see it.  Hoisting
   to component scope was wrong too: every row would share one
   instance.  Fix: a new fragHoisted channel on capturedScope and
   ASTGen lifts declarations to the nearest ENCLOSING row-fragment
   scope.  Each row gets its own var, and the chain function /
   row update inside that fragment can reach it.  buildIfChain
   re-emits cap.fragHoisted from its branch capture so the
   channel propagates through nesting; the slot factory
   consumes it at its closure top.

5. **Nested chain functions hoisted to row-fragment scope
   were not called on outer-chain re-attach or row-update.**
   The chain function lived in fragment scope (correct — it
   reads row-local vars), but the outer chain's emission called
   nested chains ONLY in the fresh-build branch ("if (!bv)").
   On the re-attach path or on a row update where the active
   branch did not change, inner chains never re-ran, so an inner
   reactive value (e.g. it.b inside `{#if it.a}{#if it.b}…`)
   never updated.  Fix: move plans[i].initText (which carries
   the initial chain-call AND interior hole writes) out of the
   `if (!bv)` block so it runs on every active-branch tick.
   Each statement is bv-guarded and chain calls are no-ops when
   their anchor is null, so it is safe to run unconditionally.

6. **Branch-local component instances not replayed in the
   enclosing row's update.**  Component instances built inside
   an {#if} inside a row were tracked in plans[i].comps for
   branch teardown, but were NOT re-published to g.compInstances.
   Session A's row-update path (which replays each component's
   .set(propsObj)) therefore could not see them — a reactive
   prop change on the enclosing row did not reach the child.
   Fix: after capturing a branch's scope, re-append cap.comps to
   g.compInstances so the outer row's compInstances collection
   includes them.  The setStmt's `if (inst) inst.set(...)` guard
   keeps the replay safe when the branch is currently inactive.

### Infrastructure added

- **fragHoisted channel** — a third hoist destination, sibling to
  g.body and g.hoisted, carrying declarations that must escape
  the current branch closure but stop at the nearest enclosing
  row-fragment scope.  Threaded through capturedScope so it
  propagates across arbitrary nesting depths.  buildEachBlock,
  buildIfChain, buildAwaitBlock, and buildComponentNode all
  route to it when their nestedHoist+inFragment predicate fires.

- **hoistTarget / hoistAwait / hoistEach** — small per-builder
  helpers that abstract the routing decision (fragment vs
  component scope) so each builder reads its own intent
  declaratively.  Session 2's centralization will consolidate
  these into captureScope; this is the surface to refactor.

- **assertFocused harness assertion + focus action** — were
  added in Session A; Session 1's
  TestRuntimeNestingMatrix_FocusSurvives_ComponentInEach exercises
  them in the matrix.

### Matrix coverage

25 cases, organised as:
- All 16 pairwise nestings of {if, each, key, await} × {in-if,
  in-each, in-key, in-await}
- 4 component-bearing variants (component-in-if, in-each, in-if-
  in-each, in-each-in-if)
- 3 triple-nested shapes (each-in-if-in-each, if-in-if-in-each,
  component-in-if-in-each combined-mutation)
- 2 edge cases ({#await} rejection, focus-survives-component-in-
  each)

Each case asserts (where applicable): initial DOM correct;
reactive change to INNER state updates DOM; toggling OUTER
construct cleanly tears down inner; no orphan nodes; no runtime
errors.

PageCraft e2e — editing the heading preserves focus AND updates
the canvas, zero errors.

### What Session 1 implies for Sessions 2 and 3

The density of bugs (six) caught by this matrix strongly argues
the architectural cleanup is now closer to NECESSARY than nice-
to-have.  The code now carries three nearly-identical hoist
helpers and a fragHoisted channel that future builders must
remember to consume.  Session 2 will centralize that routing in
captureScope itself — the right level to consolidate it, since
that is where the scope semantics change.  Session 3 will delete
the per-builder workarounds once 2 lands.

## Post-stabilize fix — nested {#if} chains were not reactive

Reported symptom: in LiveBlock, content wrapped in {#if} did not
update on state change — e.g. {#if p.eyebrow}<span>{p.eyebrow}</span>
{/if} inside the outer {#if block.type==='hero'} branch never
re-rendered when the eyebrow field was edited.  Two distinct bugs
combined.

1. **The AST generator emitted nested chain machinery
   block-scoped.**  When buildIfChain ran inside another branch's
   build closure (the recursive walker calls buildIfChain on
   nested {#if} blocks via captureScope), the chain's
   declarations — anchor `const _a = …`, branch handle vars `let
   _bN = null`, reentrancy flags, captured node decls, and the
   `function chainFn() {…}` itself — landed in g.body, i.e. the
   outer branch's build BLOCK.  That made the chain function
   invisible from component scope: a setter cascade firing from
   $$set_X would call `$$chain_$$nM()` and get "ReferenceError:
   $$chain_$$nM is not defined" (a hard crash).

   Fix: when `g.inBranchScope || g.inSlotScope`, route all of
   that machinery to g.hoisted (component scope) — the anchor
   is split into `let _a = null;` (hoisted) plus `_a = …;` (body),
   the branch handle/flag/keyprev vars go to hoisted, captured
   node decls (sub.decls) go to hoisted, and the chain function
   declaration itself goes to hoisted.  The initial chain call
   stays in initText so it runs after the outer branch attaches.

2. **LiveBlock was on the legacy generator, where (1)'s fix
   doesn't apply.**  astChainRootOK rejected the LiveBlock shape
   because its outer branches contain nested {#if} blocks —
   astHoleSubtreeOK returns false for any branch whose subtree
   has nested control flow.  The fall-through ran the legacy
   generator, which emits nested {#if} as a one-shot inline `if
   (cond) { … }` with no reactive wiring at all (a longstanding
   bug — the conditional ran once at initial build and never
   again).

   Fix: widen astChainRootOK to also accept branches that need
   the recursive walker (nested control flow without broken
   await shorthand).  buildIfChain already handles those via
   buildChildRun + captureScope, so the gate was simply stale.

A third, distinct issue surfaced once LiveBlock was on the AST
path: the cascade fires every hoisted nested chain on every dep
change, including chains whose OUTER branch is currently
inactive.  Those chains have no anchor (the outer branch hasn't
built it) and the {:else} arm of the nested chain would still
try to mount, crashing on `null.before(…)`.  Two layered fixes:

3. **Anchor-null guard on hoisted chain functions.**  Emit
   `if (!<anchor>) return;` at the top of any chain whose
   declaration was hoisted.  The cascade's calls from inactive
   outer branches become safe no-ops; legitimate calls (from
   inside the outer branch build, or via cascade after the
   outer mounted) proceed as before.  Root-scope chains keep
   `const _a = …` and never need the guard.

4. **Outer-branch teardown tears down its nested chains.**
   When the outer chain switches branches, its bv-snapshot only
   captured the nested chain's anchor — the nested chain's
   dynamically-attached content (inserted via `_a.before(...)`
   after the snapshot) would be orphaned in the DOM and the
   `if (!_a) return;` guard would prevent any future rebuild.
   Now each nested chain emits a teardown snippet (remove each
   branch's nodes, null bookkeeping, null its anchor) to a new
   g.nestedTeardowns channel.  The OUTER chain's per-branch
   teardown reads the entries appended during that branch's
   build (snapshot-length pattern: tdMark = len(g.nestedTeardowns)
   before captureScope; new entries belong to this branch).
   Doubly-nested chains propagate naturally — each level appends
   its own teardown.

End-to-end with PageCraft LiveBlock:
  - editing eyebrow updates the <span>: PASS
  - clearing eyebrow hides the <span> ({#if p.eyebrow} → false): PASS
  - repopulating eyebrow shows the <span> again: PASS
  - switching between block types (hero → heading) keeps DOM
    counts stable, no orphan nodes: PASS
  - focus on the heading input survives the edit: PASS
  - zero runtime errors throughout

Two regression tests:
  - TestRuntimeNestedIfChainReactive: minimal nested {#if}
    reactive update.
  - TestRuntimeNestedChainElseFallingWhenOuterInactive:
    sibling outer branches each containing a nested chain whose
    {:else} arm always matches.  Exercises the cascade calling
    inactive-outer's nested chain (guard) AND outer-branch
    switching to ensure the previous nested chain is torn down
    (teardown propagation).

## Session C — keyed-{#each} hint + guide

The unkeyed reconciler is *positional*: a middle insert or delete
shifts every subsequent row's content, so the row that previously
showed item B silently becomes the row showing item C — DOM
identity is preserved but the row-to-item mapping is not.  This
surprise is most visible when each row carries child-component
state (an <input> with focus, a controlled child, animations).

The compiler now emits an informational hint when an {#each} is
unkeyed AND its row body contains a child component (directly or
nested inside an {#if} / {#each} / {#await} within the row).  The
hint names the each expression and tells the author how to add a
key:

  unkeyed {#each items as it} renders a child component per row;
  consider keying the loop (e.g. add `(item.id)` after `as item`)
  so middle insertions and reorderings preserve each row's
  identity and component state

It is silent for the safe shapes — keyed each, and unkeyed each
over plain DOM rows.  The hint uses SevHint (informational), not
SevWarning, so it never blocks compilation; users can ignore it
when the trade-off is intentional (e.g. lists where positional
update is exactly what's wanted).

GUIDE.md's {#each} section was rewritten to explain reconciliation
behavior, the positional semantics of unkeyed, and when to add a
key.  Four diagnostic tests cover the hint conditions:

  - TestSessionC_UnkeyedEachWithComponentEmitsHint
  - TestSessionC_KeyedEachIsSilent
  - TestSessionC_PureDOMEachIsSilent
  - TestSessionC_ComponentInIfBranchInRowFiresHint

PageCraft now compiles with two such hints (Canvas's blocks-each,
Inspector's fields-each) — exactly the two unkeyed-each-with-
component sites in the app.

## Session B — {#each} reconciles append and remove without full rebuild

Session A made same-length updates in-place.  Session B extends
that to length changes via positional reconciliation:

  same length         → update each row in place
  grown (oldLen < N)  → update overlap in place; append new tail
  shrunk (oldLen > N) → update overlap in place; destroy surplus tail

DOM identity, input focus, scroll position, and IME composition
survive all three.  The fall-through (different array reference,
or rows detached after an {#if} unmount) still does destroy +
rebuild.

The new shape:

  if (sameRef && stillLive) {
    const common = Math.min(blocks.length, arr.length);
    for (let i = 0; i < common; i++)
      if (blocks[i].update) blocks[i].update(arr[i], i);
    if (blocks.length > arr.length) {
      for (let i = arr.length; i < blocks.length; i++) blocks[i].destroy();
      blocks.length = arr.length;
    } else if (blocks.length < arr.length) {
      for (let i = common; i < arr.length; i++) {
        const blk = frag(arr[i], i);
        anchor.before(blk.el);
        blocks.push(blk);
      }
    }
    return;
  }
  // fallback: full destroy + rebuild …

This is the standard positional-unkeyed semantics: middle inserts
shift subsequent rows' content (covered by Session C's hint).

Runtime regression tests:

  - TestRuntimeUnkeyedEachAppendInPlace      — push, focus survives
  - TestRuntimeUnkeyedEachRemoveInPlace      — pop, focus survives
  - TestRuntimeUnkeyedEachAppendRemoveRoundTrip — push/pop cycles
  - TestRuntimeUnkeyedEachInBranchAppend     — append after branch reopen

End-to-end with PageCraft: editing the heading preserves the
focused input AND the canvas re-renders; adding a block grows
the list 6→7 cleanly; zero errors.

## Session A — {#each} updates rows in place; focus is preserved

Reported symptom: typing in an Inspector field lost focus on every
keystroke.  Root cause: editing a field fired a deep-state notify
that re-ran the Inspector's unkeyed {#each def.fields}; the
reconciler destroyed every row and rebuilt — including the <input>
the user was typing in.  Even with the array reference and length
unchanged, the unkeyed path always tore everything down.

The proper architectural fix (which is what other frameworks do)
is to UPDATE existing rows in place rather than re-rendering when
only attribute/text data changes.  Three coordinated changes:

1. **The row's update closure now propagates props to existing
   child components.**  compInstance carries a setStmt field
   (`if (inst) inst.set(propsObj);`).  The fragment builder
   collects child instances built within the row and replays each
   setStmt in updateBody.  Before: the row's update only re-ran
   per-row directive/bind statements and the hole writes — it
   never called .set() on already-mounted child components, so
   the path was unusable for component-bearing rows.  After: the
   row's update is structurally equivalent to its mount.

2. **The unkeyed reconciler short-circuits when safe.**  When the
   array reference is unchanged AND the length is unchanged AND
   the existing rows are still in the document tree, the each
   walks each block and calls $$blk.update(item, i) instead of
   destroying and rebuilding.  DOM nodes (and therefore focus,
   scroll position, native selection, IME composition) are
   preserved; child component instances are reused.  Length /
   reference / detachment changes fall through to the destroy +
   rebuild path, which is still required when items are appended
   or removed.

3. **DOM-liveness via .isConnected, not .parentNode.**  The first
   draft of the liveness check used `rows[0].el.parentNode`, but
   an enclosing {#if} branch that's been unmounted detaches the
   branch's root (e.g. the <ul> that hosts the rows) — the rows'
   .parentNode still points at the (detached) <ul>, so a
   .parentNode check spuriously passed and the fast path called
   .update on orphaned blocks (zero rendered rows on branch
   reopen).  .isConnected reports document-tree connectedness,
   which is the property we actually need.

Verified end-to-end with PageCraft: editing the heading field now
preserves the input node (same node before/after), preserves
activeElement, and the canvas re-renders to the new value with
zero errors.  Two regression tests:

  - TestRuntimeUnkeyedEachPreservesFocus — Session A keystone:
    focus an input inside an {#each}-rendered child component,
    fire a reactive update, assert focus is still on the same
    input node afterward.
  - TestRuntimeIfBranchEach already covered the {#if}-wraps-
    {#each} branch-reopen case (the .isConnected fix was needed
    to keep it green).

Three knownLegacyBugs entries (each_indexed, each_on_root,
each_row_class) plus one knownDiff entry (each) document the
intentional divergence — the AST generator's unkeyed each is now
strictly better than the legacy generator's; legacy still
destroys and rebuilds.

A new harness assertion `assertFocused` and action `focus` were
added to tools/runtime-harness.mjs.

Sessions B (unkeyed update-in-place beyond the same-length case
— partial updates when items are appended/removed) and C
(encourage keyed {#each} in the guide, optionally analyzer-warn
for unkeyed lists containing components) remain.

## Post-stabilize fix — raw-text elements (<textarea>/<title>) crashed

`<textarea>{value}</textarea>` crashed with "TypeError: $$nN_a is
null".  textarea (and title) are raw-text / CDATA elements: their
body is NOT parsed as HTML.  The compiler's hole mechanism sets
innerHTML = '<!--$-->' and TreeWalks for that comment, but in a
textarea the placeholder is stored as literal text, so the
TreeWalker finds no comment node and the hole anchor is null —
`$$nN_a.parentNode` then throws.

Fix: an interpolated raw-text body is compiled to a reactive DOM
PROPERTY write (textarea → .value, title → .textContent) instead
of the innerHTML+TreeWalker hole path — which is also the
semantically correct mapping.  Three code paths needed it:
  - buildHolesDOM (root element),
  - buildChildren (nested element, incl. inside an {#if} branch —
    the reactive write is branch-var-guarded),
  - astHoleSubtreeOK now returns false for a raw-text element with
    an interpolated body, so it routes to the recursive walker
    rather than the hole-template (innerHTML) path that cannot
    express it.
Static raw-text bodies are likewise written as the property.
A new harness assertion (assertValue, in tools/runtime-harness.mjs)
checks the .value property.

Regression: TestRuntimeTextareaChildInterpolation,
TestRuntimeTextareaChildInIfBranch.

Note: this fixes the `<textarea>{value}</textarea>` child form.
The attribute form `<textarea value={value}>` already worked and
is unchanged.  A separate, larger issue remains DEFERRED: editing
a field re-runs the Inspector's unkeyed {#each def.fields}, which
destroys+rebuilds every Field and drops input focus.  The correct
fix (keyed reconciliation, or an update-in-place path that
propagates props to existing child components via .set()) is its
own change — an attempted reference-equality fast path was reverted
because the unkeyed row update does not currently re-set child
component props, which would have shown stale content.

## Post-stabilize fix — component wrapper broke layout (display:contents)

A component invocation (`<Canvas />`) mounts the child into a
wrapper `<div>` that the parent owns (it is the stable insertion
point and the event target the parent listens on for the child's
dispatched CustomEvents).  That bare `<div>` generates a layout
box, so it silently broke the parent's flex/grid layout and any
spacing/selector that assumed the component's root was a direct
child — an unobvious wrapper between, say, `<section>` and the
component's `<p>`.

Fix: the wrapper is now `display:contents`, so it generates no box
— the child's content participates in the PARENT's layout exactly
as if the wrapper were not there.  display has no effect on event
propagation, so the parent's CustomEvent listeners on the wrapper
still fire (verified: PageCraft block-select, which bubbles a
`select` event through the wrapper, still works).  This matches
the precedent already set by the multi-root component wrapper,
which used display:contents for the same reason.  Applied in both
generators; the four legacy component-wrapper sites and the AST
buildComponentNode all emit it.

Known residue: the wrapper element still exists in the DOM tree,
so a strict direct-child CSS selector (`section > p`) still sees
the wrapper.  Eliminating the element entirely (anchor-comment
mount + dispatch on the child root) is a larger change to the
event-target wiring, deferred; display:contents fixes the layout
breakage that was reported.

Regression: TestComponentWrapperIsDisplayContents_AST / _Legacy.

## Post-stabilize fix — deep-mutation re-render + duplicated each rows

Reported symptom: editing a block field in the PageCraft Inspector
mutated the data but Canvas did not re-render (only reorder/select
forced an update).  Two distinct bugs combined.

1. **Identity guard suppressed deep-mutation re-render.**  $state.deep
   yields a Proxy.  An in-place nested mutation (b.props.x = v) leaves
   the proxy REFERENCE unchanged, so a child prop setter's guard
   `if (blocks === $$v) return` early-returned and skipped the
   re-render.  Fix: proxies now carry a brand (Symbol.for('vela.proxy'))
   and runtime exports $$isProxy; the guard became
   `if (blocks === $$v && !$$isProxy($$v)) return` — an identity-equal
   proxy falls through to the cascade, primitives/plain objects keep
   the fast skip.  Applied to both the plain setter (buildOneSetter)
   and the deep setter (buildDeepSetters), and to the legacy
   generator, so all paths agree.  The differential normalizer
   (stripRuntimeHelperWiring) and two Bug13 guard-assertion tests
   were updated to the new form.

2. **Duplicated DOM rows after the re-render.**  Fixing (1) exposed
   this: a re-render left BOTH the old and new row in the DOM.  The
   recursive-walker branch path in buildIfChain folded cap.initBody
   — which includes an {#each}'s initial reconciler call — into
   buildText, BEFORE the branch handle snapshot
   `bv = [...frag.childNodes]`.  So the each-managed rows were
   captured into the branch handle; when the {#each} reconciler later
   replaced those rows, the chain's re-attach path spliced the stale
   originals back in.  Fix: cap.initBody is emitted as initText —
   after the snapshot and the anchor's DOM attach — so the branch
   handle holds only its own stable nodes (its anchor) and the rows
   are owned solely by the {#each} reconciler.

Verified end-to-end: editing a deep-prop field updates the canvas
and the node count stays correct (no duplicate); full edit → add →
deselect sequence runs clean under jsdom.

Regression: TestRuntimeDeepMutationRerendersEach.

## Post-stabilize fix — {#if} teardown must precede interior updates

"TypeError: def is null" in the PageCraft Inspector on deselect.
A prop setter's cascade ran a branch's INTERIOR updates before
the {#if} chain re-render that would unmount that branch.  When
`block` became null, `def = $derived(block ? … : null)` became
null too, but the cascade ran `$$each_$$nN(def.fields)` (guarded
by the still-truthy `$$chain_$$n2_b0`) BEFORE `$$chain_$$n2()` had
a chance to tear the branch down and null that guard — so it read
def.fields on a null def and threw.

Root cause: chain re-renders were registered via addUpdate and so
were ordered AFTER the branch-interior updates in the cascade (the
interior updates were registered first, during branch building).

Fix: a new addStructuralUpdate / g.structuralUpdates channel.
buildIfChain registers the chain re-render there (at component
scope), and buildSetterCascade emits all structural updates BEFORE
the ordinary interior updates within the $$defer block.  So a
branch toggled off tears down first; its now-stale interior
updates then no-op against the nulled guard.

Crucially, this is component-scope only: a chain built INSIDE an
{#each} row fragment (fragCounter >= 0) still registers as an
ordinary update, because the row's chain fn is fragment-local and
the dependency must instead surface through the {#each}'s
rowBodyDeps to re-run the whole list.  (Conflating the two briefly
regressed TestRuntimeEachRowIf — caught by the suite.)

Regression: TestRuntimeIfToggleOffWithNullDerived; full PageCraft
select→deselect verified end-to-end under jsdom.

## Post-stabilize fix — hole vars trapped in {#if} branch (buildChildren)

Third instance of the same root cause, a third builder path.  A
hole ({def.icon}, {label}) inside an element inside an {#if}
branch — where the branch also contains nested control flow,
forcing the recursive-walker path — had its captured text-node
var emitted as a `const` trapped in the branch-build block.  The
prop setter cascade (Inspector.set_block in the PageCraft app)
referenced it at component scope → "ReferenceError: $$nN is not
defined".

buildChildren's hole handling hoisted captured vars for
inSlotScope but NOT inBranchScope.  Fixed: it now hoists for
either (decls → g.hoisted, guarded by the per-node $$self ref).
This completes the branch-scope hoisting trio — element vars
(buildElement), {#each} machinery (buildEachBlock), and now hole
vars (buildChildren) all hoist correctly when built inside a
captured branch scope.

Verified end-to-end: the full PageCraft app mounts, addBlock and
block-select (Inspector.set_block, the failing path) run with no
error under jsdom.

Regression: TestRuntimeHoleInIfBranchWithNestedEach.

## Post-stabilize fix — {#each} in {#if} branch + component default export

Two bugs found running a real multi-component app (PageCraft).

1. **{#each} nested in an {#if} branch — "ReferenceError:
   $$each_$$nN is not defined".**  When a {#each} is built inside
   an {#if} branch via the recursive walker, buildEachBlock emitted
   its machinery into g.body — which captureScope traps inside the
   branch-build block.  The reconciler ($$each_$$nN), block storage
   ($$nN_bs), anchor, keymap, and row fragment builder were all
   block-local.  But the setter cascade (which the earlier
   branch-update fix correctly routes to component scope) calls the
   reconciler — so a list mutation hit a function that was out of
   scope.  Fixed: when inBranchScope/inSlotScope, buildEachBlock
   hoists the persistent machinery to component scope (g.hoisted),
   mirroring buildElement's element-var hoist; only the initial
   reconciler call stays in the branch body.

2. **Components had no default export.**  A component compiles to
   `export function Name`, but every multi-component project writes
   `import Name from './Name.vela'` (a default import, matching
   Svelte).  Default-import-of-named-export fails at module load:
   "does not provide an export named 'default'".  This meant NO
   multi-component Vela app worked at runtime — it surfaced only
   now, with the first real multi-component project.  Fixed: both
   generators emit `export default Name;` after the named export,
   so default and named imports both resolve.

Verified end-to-end: the full PageCraft app mounts and an
addBlock click (App → set_blocks → the hoisted reconciler, the
exact failing path) runs with no error under jsdom.

Regressions: TestRuntimeEachInsideIfBranch (runtime harness),
TestComponentHasDefaultExport.

## Post-stabilize fix — phantom onMount/onDestroy runtime import

A real user-reported runtime error:
"The requested module 'runtime.js' doesn't provide an export named:
'onMount'".  onMount and onDestroy are compiler INTRINSICS — the
analyzer extracts them and the codegen wires the callbacks
directly into the component's mount path and destroy() body.  They
have no runtime existence.  But the import-rewriter passed the
NAMES through anyway: `import { onMount } from 'vela'` became
`import { onMount } from './runtime.js'`, a phantom import that
failed at module load.  Why it was not caught earlier: no runtime
test exercised onMount/onDestroy, and node --check accepted the
output (the import line is syntactically valid).

Fixed: stripVelaIntrinsics filters compiler-intrinsic names out of
a 'vela' import specifier; an import that contained only
intrinsics is dropped entirely.  Both the AST generator
(astgen.go) and the legacy generator (codegen.go) call the helper.
Mixed imports keep their real runtime names — `import { onMount,
derived } from 'vela'` emits `import { derived } from './runtime.js'`.

Aliased intrinsics — `import { onMount as init } from 'vela'` —
remain a known limitation: the analyzer recognises only the
literal names, so the user-side `init(...)` call is not rewritten
and becomes undefined at runtime.  No real component uses this
form; a diagnostic can be added if it ever comes up.

Regressions: 5 new TestLifecycle_*/TestStripVelaIntrinsics tests,
plus end-to-end verification (compiled module loads, mount and
destroy actually run under jsdom).

## Post-stabilize fix — analyzer false positives ({#each} vars, $$dispatch)

Two classes of bogus warnings, both noise that eroded trust in
real diagnostics:

1. **{#each} row variables flagged "no such name is declared".**
   validateTemplateRefs called splitEachExpr (which returns the
   iterable expression AND the row-variable names) but discarded
   the names, then walked the loop body with an unchanged `known`
   set — so `{item}`/`{i}` inside `{#each items as item, i}` never
   resolved.  Fixed: addEachBindings registers the row variables
   into `known` for the children walk, then withdraws exactly
   those it added (no leak to siblings, no clobber of same-named
   outer bindings).  identsIn extracts identifiers from each
   post-"as" token so destructuring patterns (`as {name, id}`)
   resolve too.  Both the EachBlockNode and the
   ElementNode-with-EachExpr cases were fixed.

2. **$$dispatch reported "is not defined".**  The code generator
   wires $$dispatch into every component scope, but the scope
   analyzer's builtin list did not include it.  Added.

The iterable expression is still validated; a genuinely
undeclared iterable, loop-body name, or row variable referenced
outside the loop is still reported.  Stepper.vela went from 6
warnings to 0; the warnings now remaining on real components
(App, Markdown) are all legitimate (non-$state assignment,
deps-less $effect).

Regressions: 7 TestTemplateRefsEach* tests, TestDispatchBuiltinResolves.

## Post-stabilize fix — export let not visible to earlier closures

The scope analyzer's pre-declaration pass (preDeclareLexicalStmt)
handled JsVarDecl and JsClassDecl but not JsExportDecl.  An
`export let` prop is a JsExportDecl wrapping a JsVarDecl, so the
prop name was never pre-declared into module scope.  A closure
that referenced the prop while appearing EARLIER in source order
— most commonly an $effect declared above the `export let` line —
then reported a bogus "X is not defined" warning.  The generated
code was always correct; only the diagnostic was wrong.

Fixed: preDeclareLexicalStmt unwraps JsExportDecl and pre-declares
the inner let/const/class.  This clears the long-standing "known
noise" $effect/prop false positives (e.g. Stepper.vela dropped
from 9 warnings to 6 — the remaining 6 are unrelated {#each}
row-var and $$dispatch-builtin items).

Regression: TestExportLetVisibleToEarlierClosure.

## Documentation — GUIDE.md

Added GUIDE.md, a component-author's reference (distinct from
VELA.md, the compiler-internals reference): file structure, the
four runes, props and $bindable, lifecycle, template syntax,
directives, block constructs, components and slots, scoped styles
and --css, a complete worked example, and a quick-reference table.
Every code sample was compile-checked.

## Post-stabilize fix — captureScope panic safety

captureScope and withFragmentIDs swap generator state (emission
buffers, the fragment counter) out for the duration of a callback,
then restore it.  The restore ran as plain trailing statements —
so a panic inside the callback skipped it, leaving the generator's
buffers swapped out.  The fuzzer and the test suite both recover
from panics and keep compiling, so a panic mid-capture would
silently poison every later compilation with another component's
partial state, masking the original failure.

Fixed: restoration moved into a defer in both helpers.  The two
scope-flag save/restore sites (inSlotScope, inBranchScope) were
likewise moved inside their captureScope closures with their own
defer.  No behaviour change on the success path.

Regressions: TestCaptureScopePanicSafety,
TestWithFragmentIDsPanicSafety.

## CSS output mode — DONE

The CSS a component's <style> block produces can now be delivered
two ways, chosen by the --css flag:

  - inject (default) — the component creates a <style> element at
    runtime and appends it to document.head (CSS-in-JS, the
    long-standing behaviour).
  - external — the scoped stylesheet is written to a sibling
    <name>.css file; the component emits NO runtime injection (no
    <style> element, no document.head append, no $$style cleanup
    in destroy()).  The consumer links the .css.

Scoping (ScopeCSS — the [scopeID] attribute rewrite) happens
identically in both modes; only delivery differs.  A component
without a <style> block produces no .css in either mode.

Implementation: CompileOptions.CSSExternal (set by --css=external);
ASTGen.cssExternal threaded via buildProgramCSS / generateCodeASTCSS
(generateCodeAST keeps its historical signature → inject mode, so
existing callers and tests are unaffected); buildStyleInjection and
the destroy()-time $$style cleanup are suppressed when cssExternal;
ExtractScopedCSS returns the scoped stylesheet; compileFile writes
the sibling .css.  Project mode mirrors the .css into the output
tree alongside the .js automatically (the path derives from dest).

Regressions: css_mode_test.go (5 tests).

## FUTURE ROADMAP (post-stabilization, not scheduled)

One idea remains:

1. **Generated-AST JSON export.** The compiler already emits the
   PARSED ast (--emit-gen-ast → astjson.go). Extend the same
   JSON-export treatment so downstream tooling can analyse the
   generated JS AST too.

## KNOWN ISSUES (unrelated to the migration)

- (fixed) Makefile referenced `runtime/runtime.js`; the file is at
  the repo root as `runtime.js`.  Paths corrected.

## Verification invariants (every step)

- `go build ./... && go test ./... && go vet ./...` clean.
- `TestASTGenMatchesLegacy` (18 curated fixtures) green.
- `TestASTGenCorpus` (validity) + `TestASTGenCorpusMatchesLegacy`
  (structural equality, minus `knownLegacyBugs`) green.
- Differential gate deterministic across repeated runs.
- Package: copy `*.go *.md *.js Makefile` + `tools/` into a fresh
  tree, write `go.mod` (`module vela` / `go 1.21`), build+test+vet,
  zip to the outputs dir, `present_files`.
