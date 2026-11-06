Fixtures of `vela check --types` (task 084, docs/TYPESCRIPT.md TS-1.2,
TS-1.5), run by `typecheck_test.go` with TypeScript 5.8 and 6.x:

- `errors`: one type error per check of the virtual documents
  (internal/typecheck); the test names the range each is reported at.
- `clean`: the same checks used correctly; no diagnostics.
- `dashboard-ts`: `examples/dashboard` with `<script lang="ts">`, the fixes
  the check asked for (event targets, an untyped `{}` prop, lifecycle hooks
  imported from `vela`, a `bind:` of a member), and `strict: false`.
