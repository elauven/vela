# @govela/create

Creates a Vela application.

```sh
npm create @govela my-app             # Vite, Vitest with @govela/testing
npm create @govela my-app -- --ssr    # a Go server renders, Vite builds the client
npm create @govela my-app -- --app    # a Vela App: routes with Go loaders
```

`--module example.com/my-app` sets the Go module path of `--ssr` and
`--app`. Every project has a component test, `npm run check`
(`vela check`), a GitHub Actions workflow running the check, the tests
and the build, and an `AGENTS.md` pointing to the Vela authoring guide.

License: MIT.
