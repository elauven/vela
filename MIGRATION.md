# AST codegen migration history

The migration is complete. Vela compiles every component through direct
`JsProgram` construction in `astgen.go`, validates that AST, and prints it
with `jsprinter.go`. The former string-concatenation generator, its
`--codegen=legacy` flag, and the AST-versus-legacy differential oracle were
retired in Task 005.

The current safety net is behavioural rather than implementation-comparative:
AST validation, source-map tests, the component corpus, jsdom and Chromium
runtime fixtures, nesting-matrix regressions, and fuzzing. Historical details
remain available in Git history.
