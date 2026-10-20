# SSR beta gate

<!-- Generated from testdata/ssr_gate/expected.json by TestSSRGate; do not edit. -->

`TestSSRGate` (task 031) builds every project below with the real CLI
(`vela --ssr --ssr-report`), compiles and runs the generated Go as an HTTP
server, and checks every rendered component in Chromium: hydration without
DOM changes or mismatch reports, the hydrated DOM equal to a client-only
mount, the same DOM after each click on its buttons and checkboxes, and no
owners, listeners or queued work left after `destroy()`. Components are
rendered with their default props. Any difference from the recorded
outcomes fails CI; a regression can only be recorded with a decision in
`testdata/ssr_gate/DECISIONS.md`.

## Positive corpus

| Project | Components | Server renderer | Browser checks pass | Not checked in the browser | Client only | SSR diagnostics |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| examples | 24 | 14 | 14 | 0 | 0 | VELA-SSR-EXPR ×9, VELA-SSR-HOST-MISSING ×1 |
| ssr-host | 1 | 1 | 1 | 0 | 0 |  |
| qdash | 6 | 2 | 2 | 0 | 0 | VELA-SSR-HOST-MISSING ×4 |
| qpage-builder | 27 | 19 | 19 | 0 | 0 | VELA-SSR-CHILD ×2, VELA-SSR-EXPR ×3, VELA-SSR-HOST-MISSING ×3 |
| qwen-dashboard | 4 | 1 | 1 | 0 | 0 | VELA-SSR-HOST-MISSING ×3 |
| gpt-todo | 1 | 1 | 1 | 0 | 0 |  |
| deepseek-fixed | 1 | 1 | 1 | 0 | 0 |  |
| gpt-fixed | 1 | 1 | 1 | 0 | 0 |  |
| qwen-fixed | 2 | 1 | 0 | 1 | 0 | VELA-SSR-HOST-MISSING ×1 |
| **total** | 67 | 41 | 40 | 1 | 0 | VELA-SSR-CHILD ×2, VELA-SSR-EXPR ×12, VELA-SSR-HOST-MISSING ×12 |

A component is not checked in the browser when its default props make
the render throw (as the client would), or when its Go package cannot
build. SSR diagnostics are the outcomes of `--ssr-report`; each one names
the site and the host or `{#client}` route that would fix it.

## Site outcomes

| Outcome | Sites |
| --- | ---: |
| server-native | 788 |
| server-host | 2 |
| unsupported | 111 |

## Negative corpus

These projects use syntax outside the supported template contract. The
build must fail with each listed diagnostic; rejecting them does not count
as positive coverage.

| Project | File | Diagnostic |
| --- | --- | --- |
| deepseek-todo | App.vela | VELA-DERIVED-CALLBACK |
| deepseek-todo | App.vela | VELA-EVENT-MODIFIER |
| deepseek-todo | App.vela | VELA-CLASS-OBJECT |
| deepseek-todo | App.vela | VELA-BIND-MEMBER |
| qwen-todo | App.vela | VELA-DYNAMIC-CLASS |
| qwen-todo | TodoItem.vela | VELA-BUILTIN-IMPORT |
| qwen-todo | TodoItem.vela | VELA-DYNAMIC-CLASS |

Totals: 67 positive components in 9 projects (40 checked in the browser); 2 negative projects with 7 required diagnostics.
