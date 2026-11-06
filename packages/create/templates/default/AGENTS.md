# Agent instructions

This is a Vela application. Before you create or edit a `.vela` component,
read the Vela authoring guide of the compiler version this project uses:

https://github.com/elauven/vela/blob/v__VERSION__/AGENTS.md

It defines the component shape, the runes (`$state`, `$derived`,
`$effect`, `$bindable`), the template syntax (`:attr={…}`, `@event={…}`,
`{#if}`, `{#each}`) and what Vela does not support. When a compiler
diagnostic disagrees with an example, trust the diagnostic.

## This project

- Components live in `src/`; `src/main.js` mounts `src/App.vela`.
- Tests are `*.test.js` files next to the components, run by Vitest with
  `@govela/testing` (`render`, `fireEvent`).

## Before handing off

```sh
npm run check   # vela check: compiler diagnostics
npm test        # vitest run
npm run build   # vite build
```

All three must pass.
