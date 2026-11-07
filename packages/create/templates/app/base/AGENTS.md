# Agent instructions

This is a Vela App: file-based routes in `src/routes`, rendered on the
server by Go and hydrated in the browser. Before you create or edit a
`.vela` component, read the Vela authoring guide of the compiler version
this project uses:

https://github.com/elauven/vela/blob/v__VERSION__/AGENTS.md

and, for routes, loaders and actions:

https://github.com/elauven/vela/blob/v__VERSION__/docs/APP.md

When a compiler diagnostic disagrees with an example, trust the diagnostic.

## This project

- `src/routes/**/Page.vela` are pages, `Layout.vela` wraps them. The Go
  loaders in `server/routes` (the same tree) return each component's
  `data` prop; `npx vela-app scaffold` adds the missing ones.
- `src/_app`, `internal/appgen` and `out/` are generated; never edit them.
- A component at any depth reads the route, the form and the data of the
  page with `getPage()` from `#app/page.js` (call it in the script; keep it
  as `$state.deep(getPage())` to follow navigation) instead of passing
  `route` and `data` down as props.
- Code that runs during the first render is translated to Go: keep it free
  of browser APIs. Treat every `VELA-SSR-*` diagnostic as an error to fix.
- Tests are `*.test.__EXT__` files next to the components, run by Vitest with
  `@govela/testing` (`render`, `fireEvent`).

## Before handing off

```sh
npm run check   # vela-app gen and vela check --ssr (and --types)
npm run lint    # eslint: the code of the components and modules
npm test        # vitest run
npm run build   # vela-app build: the server and the client into build/
```

All of them must pass.
