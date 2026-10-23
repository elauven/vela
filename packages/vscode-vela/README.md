# Vela for Visual Studio Code

Syntax highlighting and language support for [Vela](https://github.com/elauven/vela)
single-file components (`.vela`), through the Vela language server
`vela-lsp`.

- Diagnostics of the compiler while typing, with the SSR checks of the
  project on save.
- Go to definition, hover and completion of components, props, events,
  directives, blocks and the script's names.
- JavaScript inside `<script>` and template expressions through TypeScript:
  members, types, references, rename, signature help, type errors with
  `// @ts-check`. The extension ships TypeScript 6 for it.

## Requirements

`vela-lsp` of the same version as the project's compiler. The extension uses,
in order:

1. the `vela.server.path` setting;
2. the binary of `@govela/compiler` in the workspace's `node_modules`
   (`npm i -D @govela/compiler`);
3. `vela-lsp` on `PATH` (`go install github.com/elauven/vela/cmd/vela-lsp@latest`).

Node.js on `PATH` runs the TypeScript helper of the server; without it the
JavaScript features are off and the Vela ones remain.

## Settings

- `vela.server.path`: the `vela-lsp` binary.
- `vela.typescript.enable`: the JavaScript features (default on).
- `vela.typescript.tsdk`: another TypeScript 5.x or 6.x (`lib` directory).
- `vela.diagnosticsDelay`: milliseconds between a change and the diagnostics.
- `vela.trace.server`: trace the protocol in the output channel.

The command **Vela: Restart Language Server** restarts `vela-lsp`.

License: MIT.
