# SSR delivery specification

Updated: 2026-09-29, following the implementation review through `9c15b0b`.

This is the execution and acceptance specification for Task 007 and Tasks
027–032. `SSR.md` remains the product/architecture design; the historical
checklist in Task 007 is implementation evidence, not the current work queue.
The requirements below supersede historical claims that conflict with the
reviewed behaviour. Task checkboxes describe delivery evidence, not feature
availability by themselves.

## Scope and ownership

Deliver Go SSR and DOM-adopting hydration for Vela's supported templates,
including component graphs, with a bounded, behaviour-tested JS execution
model. Go output remains `go/ast`-generated; expressions use cached parser
ASTs. Preserve current CSR semantics and public defaults. Streaming, islands,
an HTTP framework, bundling, and HMR remain outside this delivery.

| Task | Owned deliverable |
| --- | --- |
| [027](tasks/027-public-ssr-pipeline-executable.md) | Public build/artifact contract, prop presence across component calls, hydration failure rollback |
| [028](tasks/028-baseline-js-to-go-semantics.md) | Lazy expression evaluation, JS/CSR/SSR value semantics, paired semantic oracles |
| [029](tasks/029-recursive-structural-lifecycle.md) | General recursive lifecycle and mandatory positive nesting matrix |
| [030](tasks/030-go-host-bindings-client-boundaries.md) | Typed Go host calls and explicit client fallback/lifecycle boundaries |
| [031](tasks/031-ssr-compatibility-corpus-ci.md) | Fixed project expectations, full compatibility gate and synchronized documentation |
| [032](tasks/032-mandatory-ssr-browser-ci.md) | Early mandatory Chromium CI and detection of skipped/missing test coverage |

## Execution checkpoints

1. **C0 — trustworthy baseline, first priority.** Fix R1–R3 below in 027/028,
   add their failing-before/fixed-after regression fixtures, and complete 032.
   Pass the public CLI/Go/browser fixtures and the full Go suite. Do not expand
   the admitted expression subset or add more special structural recipes
   before this checkpoint. Design/inventory work for 030 can continue.
2. **C1 — complete baseline contracts.** Finish 027/028, including partial
   hydration failure rollback and the named-function baseline. Every newly
   admitted expression must bring a paired oracle and product-path evidence
   in the same change. Existing green tests alone do not prove parity.
3. **C2 — recursive and explicit-boundary implementation.** 029 starts after
   027, 028, and 032 complete. 030 keeps an independent design track; its
   executable lowering starts after C0, using C1 contracts, and composes with
   the 029 lifecycle rather than creating another ownership system. Freeze
   shared contracts before implementing a dependent slice.
4. **C3 — release gate.** 031 completes only after 027–030 and 032. All required
   positive cases must pass; known unsupported cases are a separate negative
   suite. Close 007 only after this checkpoint, not after counting historical
   checkboxes.

`parent` is organizational in taskmd and does not enforce completion. Task
dependencies encode whole-task prerequisites; C0 and the 030 design/lowering
split are explicit partial milestones, not additional taskmd `phase` values.
027, 028, and 032 can progress together. The early CI must not wait for 031.

## Required corrections

### R1 — lazy conditional/logical evaluation (028)

Reproducer: `export let items = [];` and either
`{items.length ? items[0] : 'empty'}` or `{items.length && items[0]}`.
At review, both compile successfully and panic in generated Go for an empty
slice. JS returns `empty` and `0`, respectively. Add the corresponding
`{!items.length || items[0]}` case, whose empty-list result is `true`.

Generate actual conditional control flow or lazy closures through Go AST.
Evaluate the condition/left operand once; evaluate only the selected branch
or required right operand. Preserve operand values, types, and JS truthiness.
Purity is insufficient justification for eager evaluation: a pure expression
may fail. Do not catch and suppress a panic from an unselected branch as the
implementation of short-circuiting.

Test empty/nonempty collections, both ternary branches, nested operators, and
unselected unsafe reads. The renderer must return the expected HTML without
panic. Unsupported selected operations require their documented diagnostic
or error contract. When calls are admitted later, reuse these tests with
evaluation counters and errors to check order and exactly-once execution.

### R2 — omitted child props retain defaults (027)

Reproducer: Child declares `export let title = 'fallback'`; Parent contains
`<Child />`. At review, SSR emits empty text while a direct Go call with an
empty initialized `Props.Set` emits `fallback`.

Carry presence information through compiler-generated child calls. Only an
omitted prop receives its default; explicit empty string, false, zero, and
null retain their meaning for the corresponding admitted prop type. Preserve
the documented compatibility of direct Go calls with nil `Set` and existing
user-owned sidecars; do not overwrite sidecars to make a test pass. Handle
reserved generated-field collisions explicitly.

Cover same-package and cross-package children, partial props, slots, nested
structural positions, and direct Go entrypoints. Compare CSR and server HTML,
resolved payload, initial hydration, subsequent parent updates, and CSR
remount. Tests must detect a missing presence map in an internal child call.

### R3 — one value-to-text contract (028)

Reproducer: `export let value = null;` and `<p>{value}</p>`. Chromium review
observed CSR `null`, initial SSR empty text, and `null` after changing the
hydrated value away from null and back.

Preserve current CSR text semantics: standalone null/undefined use their JS
string conversion (`null`/`undefined`). Array joining is a different context:
holes/null/undefined entries join as empty fields. Keep undefined distinct
from null in supported expression and payload paths; a JSON object `{}` must
not accidentally become the representation of a missing JS value.

Document conversion separately for text, ordinary attributes, boolean
attributes, template literals, array joining, and form-control properties.
Do not change established CSR behaviour merely to match the current Go helper.
Add a table shared by JS and Go oracle tests and by selected browser fixtures:
null, undefined, missing object members, empty string, false, zero, Unicode
including astral characters, arrays/holes/nested arrays, and plain objects.
Extend numeric/coercion cases before admitting the corresponding operations.

Compare CSR DOM, Go-rendered DOM before hydration, zero-write adoption,
post-update DOM, and remount. Assert both actual content and node identity;
zero DOM mutations alone can preserve an incorrect server value forever.

## Public hydration failure contract (027)

Keep the early invalid-version/retry test, but do not count it as proof of
rollback after acquisition has started. Add a fixture with a valid initial
node/child and registered listener, followed by a mismatched node/child that
fails hydration. Repeat with a structural owner.

On failure, unwind resources created by that attempt in reverse ownership
order: listeners, binding callbacks, child handles, scheduled work, and cache
entries. Retain server DOM and payload needed for a corrected retry; do not
clear/remount the whole subtree. Preserve independently hydrated instances.
After correction, retry must create exactly one live instance and one event
delivery. Detached/failed handles and late callbacks must be inert. Finish
with ordinary destroy and assert payload/cache/listener cleanup. Existing
destroy logic that removes adopted DOM cannot blindly serve as rollback.

## Coverage and classification contract

- Freeze a versioned mandatory positive matrix from supported template forms,
  independently of what the current capability classifier accepts. Generate
  all ordered pairs of if/key/keyed-each/unkeyed-each/await, branch variants,
  and selected depth-3+ component/slot combinations. Use bounded generation
  for tests without imposing that bound on implementation recursion.
- Record zero unexpected unsupported/static-only outcomes for mandatory
  positive cases. Keep intentionally unsupported syntax in a separate named
  negative suite with expected diagnostic code and AST/source path. A useful
  diagnostic does not satisfy a required positive case.
- Each project fixture fixes expected artifact mode, execution boundaries,
  initial content, interactions, identity, and teardown. An interactive
  example downgraded to static-only is a failure, even with a valid manifest.
  Do not automatically rewrite expectations from the current classifier.
- Distinguish component artifact mode from per-site execution category:
  server-native, server-host, client-deferred, unsupported. A host call can
  coexist with an executable component. A reserved client-boundary label is
  not proof that its deferred lifecycle runs.

## Verification and reporting

032 establishes mandatory browser tooling now and grows the selected suite as
fixes land. Missing Node/Playwright/Chromium, launch failure, zero selected
tests, and skipped required browser/oracle cases must fail the dedicated gate.
Optional local suites may retain their documented skip behaviour. CI must
exercise the mandatory path, including a negative check of missing tooling.

`verify` blocks in the task cards provide runnable baseline checks and manual
acceptance assertions. Their presence does not mean the missing oracle or
strict runner has already been implemented. Add the strict command delivered
by 032 to those blocks when available. A green generic Go suite with skipped
browser tests does not complete a checkpoint.

Run verification from the repository root with
`taskmd verify <id> --timeout 900` (use `--dry-run` to inspect commands).
The installed CLI defaults to 60 seconds per command, shorter than the full
Go/browser suite. `assert` steps are displayed for manual evaluation and do
not affect the CLI exit code: record their evidence and do not mark a task
complete based on exit code alone. The final automated coverage/mode checks
must be executable assertions in the strict runner, not only manual prose.

For every correction, retain a tracked fixture, its expected result, and a
before/after failure record. At handoff record revision, commands, pass/fail/
skip counts, expected artifact modes, and open blockers in the task. After
compiler/runtime changes run `go test ./...`; a shared verified revision can
serve several cards without rerunning the same suite per card. Never reuse
that evidence after a code change. Run `taskmd validate` after task edits.

Review baseline: `taskmd validate` and `go test ./...` passed on 2026-09-29
through `9c15b0b` (Go suite approximately 358 seconds), while independent
reproducers demonstrated R1–R3. This is evidence of missing coverage, not
acceptance of these behaviours. Temporary review examples are not the
deliverable; recreate them as tracked regression fixtures during implementation.
