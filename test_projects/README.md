# AI Todo compatibility corpus

The original `*-todo-vela/src` directories are preserved exactly as supplied.
They are negative fixtures: compilation must finish with actionable diagnostics
and must never panic.

`corpus/*-fixed/src` contains the corresponding canonical Vela applications.
They are positive fixtures and must compile with the default AST generator.

| Original fixture | Expected diagnostics | Fixed corpus |
| --- | --- | --- |
| `deepseek-todo-vela` | `$derived` callback, event modifier, class map, member binding | `corpus/deepseek-fixed` |
| `gpt-todo-vela` | Compiles cleanly apart from non-fatal hints | `corpus/gpt-fixed` |
| `qwen-todo-vela` | builtin dispatch import, invalid class directive spelling | `corpus/qwen-fixed` |

Compile a positive fixture with:

```sh
go run . --project test_projects/corpus/qwen-fixed/src --out /tmp/vela-qwen
```

## SSR and hydration corpus

`qdash` (6 components) and `qpage-builder` (27 components) are CSR-valid
applications used to measure SSR translation and hydration coverage. They
are not negative fixtures: every component is expected to reach an SSR
outcome as the contracts in `docs/SSR_TRANSLATION_CONTRACT.md` and
`docs/HYDRATION_ARCHITECTURE.md` are implemented.

```sh
go run . --ssr --project test_projects/qpage-builder/src --out /tmp/vela-qpage
```
