# Agent instructions

This is a server-rendered Vela application. Before you create or edit a
`.vela` component, read the Vela authoring guide of the compiler version
this project uses:

https://github.com/elauven/vela/blob/v__VERSION__/AGENTS.md

It defines the component shape, the runes, the template syntax, the rules
of server rendering and what Vela does not support. When a compiler
diagnostic disagrees with an example, trust the diagnostic.

## This project

- Components live in `src/`. `vela` (configured by `vela.config.json`)
  compiles them to Go renderers in `out/server`; `cmd/server` renders
  `App` with them; `src/client.__EXT__` hydrates the HTML in the browser.
- Code that runs during the first render is translated to Go: keep it free
  of browser APIs (`window`, `document`, `Date.now()`). Put browser work in
  handlers, `$effect` or `onMount`. Treat every `VELA-SSR-*` diagnostic as
  an error to fix.
- Tests are `*.test.__EXT__` files next to the components, run by Vitest with
  `@govela/testing` (`render`, `fireEvent`).

## Before handing off

```sh
npm run check   # vela check --ssr: compiler and server-rendering diagnostics (and types)
npm run lint    # eslint: the code of the components and modules
npm test        # vitest run
npm run build   # vela, vite build and go build
```

All of them must pass.
