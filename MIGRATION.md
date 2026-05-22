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

## NEXT STEP — E3.3b: per-row dynamic attributes in {#each}

`buildEachBlock`'s direct path covers hole-bearing-subtree rows.
`astEachRowOK` still routes rows with per-row **events, binds,
class:/style: directives, or two-way bindings** to
`buildModuleSplice`. Legacy `walkEach` handles these inside the
fragment via `FragGen`:

- events — with `rewriteItemRef` so the handler closes over the
  row's mutable $$item box, not the stale forEach parameter
- binds / class: / style: — emitted into the fragment's init
  (run once) and update() (re-run per reconcile)
- two-way bindings — plus an addEventListener wiring

To close this: extend `buildEachBlock`'s fragment builder to walk
the row element's attributes (mirroring `walkEach` lines ~2050+),
emitting per-row event/bind/directive statements into `fragBody`
(events) and `updateBody` (binds/directives). Then widen
`astEachRowOK` to admit them. Reuse `rewriteHandler` /
`rewriteItemRef` for handler rewriting.

After E3.3b, `{#each}` no longer needs the splice at all.

## NEXT STEP — E3.4: buildElementSplice (slots / components)

`buildElementSplice` splices a root element whose subtree contains
slots or component invocations. Converting it to struct
construction is the last splice removal before E4. Component
invocation (props object, `$$onChange` for bindable props, the
child-component mount) and `<slot>` projection both need
struct-built equivalents. Scope this as its own session — like
E3.3, it is a subsystem, not a single function.

## Verification invariants (every step)

- `go build ./... && go test ./... && go vet ./...` clean.
- `TestASTGenMatchesLegacy` (18 curated fixtures) green.
- `TestASTGenCorpus` (validity) + `TestASTGenCorpusMatchesLegacy`
  (structural equality, minus `knownLegacyBugs`) green.
- Differential gate deterministic across repeated runs.
- Package: copy `*.go *.md *.js Makefile` + `tools/` into a fresh
  tree, write `go.mod` (`module vela` / `go 1.21`), build+test+vet,
  zip to the outputs dir, `present_files`.
