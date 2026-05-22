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

Remaining splice users: `buildEachBlock` (E3.3) and
`buildElementSplice` (E3.4).

## NEXT STEP — E3.3: `{#each}` as struct construction

`{#each}` is the largest remaining conversion. `buildEachBlock`
currently splices `walkEachBlock`, which delegates to `walkEach` +
`fraggen.go`. The generated `{#each}` has five parts:

1. `const $$nN_a = document.createComment('#each')` — anchor.
2. `function $$frag_$$nN(item, $$i)` — the **fragment builder**:
   builds one row's DOM, returns `{el, update(item,$$i), destroy()}`.
3. `let $$nN_bs = []` (+ `const $$nN_km = new Map()` when keyed) —
   block storage.
4. `function $$each_$$nN($$arr)` — the **reconciler**: unkeyed
   destroys-all-then-rebuilds; keyed diffs against `$$nN_km`,
   reusing matched blocks and destroying dropped keys.
5. Wiring: `$$root.appendChild($$nN_a)`, the setter `$$defer`
   re-render, the init call, and the destroy-all in `destroy()`.

### Difficulty split

- **Parts 1, 3, 4, 5 are fixed-shape** — only the key expression
  and frag-fn name vary. These struct-build cleanly. The keyed and
  unkeyed reconciler bodies are two fixed templates.
- **Part 2, the fragment builder, is the hard part** — its body is
  arbitrary recursive row content with full per-element wiring
  (events with row-ref rewriting, binds, class:/style: directives,
  two-way bindings, nested elements, holes, even nested `{#each}`).
  `fraggen.go`'s `FragGen` is effectively a second walker.

### Recommended approach

Two viable paths, pick at session start:

  (a) **Full struct build.** Reimplement `FragGen` as struct
      construction in `astgen`. Largest single task in the
      migration; needs a full fresh context budget. Reuse
      `buildHoleSubtree` (E3.1) for the common hole-bearing-row
      case; the row `update()` fn re-runs the hole writes.
      Per-row event/bind/directive wiring must be rebuilt.

  (b) **Reconciler struct-built, fragment body spliced.** Build
      parts 1/3/4/5 as struct construction; splice ONLY the
      `$$frag_$$nN` function body via `FragGen`. Shrinks the splice
      from the whole block to the row body. Does NOT fully
      decouple — `{#each}` still imports `fraggen.go` — so E4
      (deleting `codegen.go`/`fraggen.go`) cannot complete until
      the fragment body is also converted.

Path (a) is required for the stated goal (complete decoupling).
Path (b) is a valid intermediate if (a) must be split across
sessions: do the reconciler shells first, fragment body second.

### Verification

- Differential harness: the legacy `{#each}` oracle is correct as
  of the E1 `$$root` fix. Unkeyed and keyed fixtures must match
  structurally (or be documented divergences like the E3.1 hole
  branches).
- `node --check` on every `{#each}` shape: unkeyed, keyed,
  indexed, multi-child row (wrapper div), nested.
- E2 viewer (`--emit-gen-ast` + `tools/ast-viewer.html`, diff
  mode) for locating any structural divergence.

## The splice users

`spliceLegacyBlock` runs a legacy emitter in a sub-`CodeGen` and
re-parses its text output into the unified AST. Remaining call
sites after E3.1 + E3.2:

1. `buildModuleSplice` — whole-template fallback (`astCanBuildDirectly`
   false). Currently reached only by the legacy `#if`/`#each`
   attribute-directive syntax on a root element.
2. `buildElementSplice` — a root element whose subtree contains
   slots / components / nested dynamics. (E3.4)
3. `buildEachBlock` — `{#each}`. (E3.3 — next)

`{#if}` / `{#key}` (E3.1) and `{#await}` (E3.2) are struct-built;
their splice calls are gone.

Note: `astCanBuildDirectly == true` does NOT mean "no splice" —
`buildModule` still calls `buildEachBlock` / `buildElementSplice`
internally.

## Verification invariants (every step)

- `go build ./... && go test ./... && go vet ./...` clean.
- `TestASTGenMatchesLegacy` (18 curated fixtures) green.
- `TestASTGenCorpus` (validity) + `TestASTGenCorpusMatchesLegacy`
  (structural equality, minus `knownLegacyBugs`) green.
- Differential gate deterministic across repeated runs.
- Package: copy `*.go *.md *.js Makefile` + `tools/` into a fresh
  tree, write `go.mod` (`module vela` / `go 1.21`), build+test+vet,
  zip to the outputs dir, `present_files`.
