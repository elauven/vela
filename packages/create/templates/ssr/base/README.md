# __NAME__

A server-rendered [Vela](https://github.com/elauven/vela) application: the
Go server in `cmd/server` renders the components, Vite with
`@govela/unplugin` builds the client that hydrates them, Vitest with
`@govela/testing` tests them.

```sh
npm install
go mod tidy
```

Development, three processes:

```sh
npm run dev:vela     # vela --watch: Go renderers into out/
npm run dev          # Vite on http://localhost:5173
npm run dev:server   # the Go server on http://127.0.0.1:8080
```

Restart the Go server after a change of a component (`out/.vela-ready`
is written after every successful build).

```sh
npm test             # component tests
npm run check        # compiler and server-rendering diagnostics
npm run lint         # eslint
npm run build        # out/, dist/ and build/server
npm start            # the production server, serving dist/
```
