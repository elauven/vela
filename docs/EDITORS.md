# Editor support

`vela-lsp` is the Vela language server: the Language Server Protocol over
stdin and stdout, for every editor with an LSP client. It shares the compile
pipeline of `vela` (`internal/compiler`), so its diagnostics are the ones the
build reports.

## Installing

- With npm: `npm i -D @govela/compiler` installs `vela-lsp` beside `vela`
  (`node_modules/.bin/vela-lsp`, or `languageServerPath()` of the package
  API for editor extensions).
- With Go: `go install github.com/elauven/vela/cmd/vela-lsp@latest`.

The server takes no arguments; `--stdio`, which many clients pass, is
accepted. `vela-lsp --version` prints the version.

## What it does

- **Diagnostics** of the open buffer while typing, 150 ms after the last
  change: every check of the build (template, script, reactivity, imports),
  with the range the diagnostic is about. When `vela.config.json` enables
  SSR (`"ssr": true` with a `project`), saving a component also runs the
  SSR checks of the whole project from the saved files and reports the
  `VELA-SSR-*` diagnostics on every component, open or not.
- **Go to definition**: a component tag or import to its `.vela` file (an
  import of `./X.js` goes to `X.vela` beside it); a prop or event on a
  component tag (`:title`, `bind:value`, `@on:close`) to the declaration or
  the `$$dispatch` call in the child; a name in a template expression or
  the script to its declaration; an `{#each}` item to the block header.
- **Hover**: the declaration and kind of a name — prop, bindable prop,
  `$state`, `$state.deep`, `$derived`, function, import; the props and
  events of a component tag.
- **Completion**: the props, bindable props and events of the child on a
  component tag; directives (`:`, `@event`, `bind:`, `class:`, `style:`)
  on an element; imported components after `<`; block tags after `{#`,
  `{:` and `{/`; the script's names and the enclosing `{#each}` names in
  expressions; the runes in `<script>`.

While a section does not parse, the server keeps the names of its last
version that did, so completion and hovers keep working on a line being
typed.

The server resolves `vela.config.json`, scope classes and diagnostic paths
from the workspace root, as `vela` run in that directory does: it changes
its working directory to the root the client gives in `initialize`.

`initializationOptions.diagnosticsDelay` sets the delay of diagnostics in
milliseconds.

## JavaScript inside components

The language features of JavaScript itself (types of browser and package
APIs, members, signatures, rename) come from TypeScript's language service
over a virtual document of each component; that part is task 065c. Until
then `vela-lsp` knows the names a component declares and the runes, and the
editor's grammar highlights the script.

## Neovim

```lua
vim.filetype.add({ extension = { vela = 'vela' } })
vim.lsp.config('vela', {
  cmd = { 'vela-lsp' },
  filetypes = { 'vela' },
  root_markers = { 'vela.config.json', 'package.json', '.git' },
})
vim.lsp.enable('vela')
```

## Helix

`languages.toml`:

```toml
[language-server.vela-lsp]
command = "vela-lsp"

[[language]]
name = "vela"
scope = "source.vela"
file-types = ["vela"]
roots = ["vela.config.json", "package.json"]
language-servers = ["vela-lsp"]
```

## Other editors

Any LSP client works with the command `vela-lsp` for `*.vela` files and
the workspace root as the root URI. The VS Code extension and the setup of
Zed and Sublime Text are task 065d.
