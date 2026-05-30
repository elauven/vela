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
