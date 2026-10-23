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
- **Completion** (with the JavaScript names of TypeScript, below): the
  props, bindable props and events of the child on a
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

The language features of JavaScript itself come from TypeScript's language
service, the way the Svelte and Vue language servers provide them. For each
component `vela-lsp` builds a virtual JavaScript document: the `<script>`
as written, then the template's expressions inside a render function whose
blocks declare the `{#each}` and `{#await}` names, with declarations of the
runes and of `vela` / `vela/store`. A small helper run with `node`
(embedded in `vela-lsp`) gives it to TypeScript, and the answers are mapped
back to the component:

- completion of members, globals, browser and package APIs, in the script
  and in template expressions (`{item.` lists the fields of the `{#each}`
  item), with signatures and documentation;
- hover with the type, merged with the Vela kind (`let count: number` —
  derived); an event handler's parameter has its event type
  (`@click={(e) => …}` gives `e: PointerEvent`);
- go to definition into JavaScript modules and packages, references,
  rename, signature help;
- type errors with `// @ts-check` at the top of the script or `checkJs` in
  `jsconfig.json`; without them the script is not type checked, and syntax
  errors come from the compiler.

TypeScript is not bundled. The server uses, in order:

1. `initializationOptions.typescript.tsdk`: a TypeScript the editor
   provides (its `lib` directory or package directory; the VS Code
   extension passes its own);
2. `typescript` in the workspace's `node_modules`;
3. the global `typescript` (`npm root -g`).

It needs the language service API of TypeScript 5.x or 6.x: TypeScript 7,
the native compiler, has none yet. Install `npm i -D typescript@6` when the
workspace has none or has 7. Without node or a usable TypeScript the server
logs why once and keeps the Vela features. `typescript.enable: false` in
`initializationOptions` turns the JavaScript features off.

`jsconfig.json` or `tsconfig.json` in the workspace root sets the compiler
options; without one the project is JavaScript (ES modules, the DOM, not
strict).

Not supported yet: renaming a prop or a component import (their attribute
and tag names are not JavaScript; the server refuses rather than leave them
behind), and navigation from a `.js` file into a component, which belongs
to the editor's own TypeScript server.

## VS Code

The extension in `packages/vscode-vela` registers the `vela` language: a
TextMate grammar (`<script>` as JavaScript, `<style>` as CSS, the template
with its tags, components, directives, blocks and expressions), the
language configuration (comments, brackets, folding of blocks), and the
client of `vela-lsp`. It ships TypeScript 6 for the JavaScript features.

It starts the `vela.server.path` setting, else the `vela-lsp` of
`@govela/compiler` in the workspace's `node_modules`, else `vela-lsp` on
`PATH`. The other settings: `vela.typescript.enable`, `vela.typescript.tsdk`,
`vela.diagnosticsDelay`, `vela.trace.server`; the command *Vela: Restart
Language Server*.

The extension is not on the Marketplace yet. CI builds it (the
`vscode-vela` artifact of the *VS Code extension* job), or locally:

```sh
cd packages/vscode-vela
npm ci
npx vsce package          # vela-0.1.0.vsix
code --install-extension vela-0.1.0.vsix
```

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

Neovim and Helix highlight with tree-sitter grammars; there is none for
Vela yet, so `.vela` files get the language server's features without
highlighting (`vim.treesitter.language.register('html', 'vela')` gives an
approximation in Neovim).

## Sublime Text

With the [LSP](https://packagecontrol.io/packages/LSP) package, in
*Preferences: LSP Settings*:

```json
{
  "clients": {
    "vela": {
      "enabled": true,
      "command": ["vela-lsp"],
      "selector": "source.vela"
    }
  }
}
```

The selector needs the grammar: Sublime Text reads TextMate grammars as
property lists, so convert the extension's into your `Packages/User`
directory:

```sh
python3 -c 'import json, plistlib, sys; plistlib.dump(json.load(open(sys.argv[1])), open(sys.argv[2], "wb"))' \
  packages/vscode-vela/syntaxes/vela.tmLanguage.json "Packages/User/Vela.tmLanguage"
```

The embedded scopes are `source.js` and `source.css`, which Sublime's own
JavaScript and CSS syntaxes provide. This setup is not tested in CI.

## Zed

Zed adds languages through extensions with a tree-sitter grammar; Vela has
neither yet, so Zed is not supported.

## Other editors

Any LSP client works with the command `vela-lsp` for `*.vela` files and the
workspace root as the root URI.
