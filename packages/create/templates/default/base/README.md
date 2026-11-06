# __NAME__

A [Vela](https://github.com/elauven/vela) application: Vite with
`@govela/unplugin`, Vitest with `@govela/testing`, ESLint with
`@govela/eslint-plugin`.

```sh
npm install
npm run dev     # http://localhost:5173
npm test        # component tests
npm run check   # compiler diagnostics
npm run lint    # eslint
npm run build   # production build into dist/
```

`.github/workflows/ci.yml` runs the check, the linter, the tests and the build on every
push.
