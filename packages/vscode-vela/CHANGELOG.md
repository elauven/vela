# Changelog

## Unreleased

- `<vela:component>` and other `vela:` tags are highlighted as components.
- `<script lang="ts">` is highlighted as TypeScript.
- `{#boundary}…{:catch error, retry}…{/boundary}` is highlighted as a block,
  and `vela-lsp` completes it; `vela-lsp` completes and describes `<vela:head>`.
- `{@html markup}` is highlighted as a tag with a JavaScript expression;
  `vela-lsp` completes and describes it.
- `@govela/typescript-plugin` is registered with VS Code's TypeScript: `.ts`
  modules that import components see their types.

## 0.1.0

- Grammar of `.vela` components with embedded JavaScript and CSS, language
  configuration.
- Client of `vela-lsp`, with TypeScript 6 for the JavaScript features.
