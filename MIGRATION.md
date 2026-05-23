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

### E4 — remaining cleanup (optional, low priority)

generateCode / codegen.go / fraggen.go are now reachable ONLY via
buildModuleSplice (defensive, never taken) and the deprecated
--codegen=legacy flag. They can be deleted once confidence is high
that buildModuleSplice is truly dead — i.e. after the AST default
has shipped a release cycle, per the original plan. Until then
they are harmless: not on any live path.

## Verification invariants (every step)## Verification invariants (every step)

- `go build ./... && go test ./... && go vet ./...` clean.
- `TestASTGenMatchesLegacy` (18 curated fixtures) green.
- `TestASTGenCorpus` (validity) + `TestASTGenCorpusMatchesLegacy`
  (structural equality, minus `knownLegacyBugs`) green.
- Differential gate deterministic across repeated runs.
- Package: copy `*.go *.md *.js Makefile` + `tools/` into a fresh
  tree, write `go.mod` (`module vela` / `go 1.21`), build+test+vet,
  zip to the outputs dir, `present_files`.
