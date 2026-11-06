# __NAME__

A [Vela App](https://github.com/elauven/vela/blob/main/docs/VELA_APP.md):
routes in `src/routes`, Go loaders in `server/routes`, the server in
`cmd/server`.

```sh
npm install
npx vela-app gen && npx vela   # once, so that go mod tidy sees the generated packages
go mod tidy
npm run dev                    # http://127.0.0.1:8080
```

```sh
npm test         # component tests
npm run check    # compiler and server-rendering diagnostics
npm run build    # build/: the server binary and the client
```
