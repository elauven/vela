# @govela/typescript-plugin

`.vela` components in TypeScript's language server: a `.ts` module that
imports `./Card.vela` sees the component's props, events, slots and
bindable props, in any editor that runs `tsserver`.

```sh
npm i -D @govela/typescript-plugin @govela/compiler
```

```json
{ "compilerOptions": { "plugins": [{ "name": "@govela/typescript-plugin" }] } }
```

VS Code loads it with the Vela extension. The types are the declarations
of `vela --dts`, read through `vela compile --dts` of `@govela/compiler`
(option `binary` or `VELA_BINARY` for another build). `tsc` does not run
plugins; `vela check --types` checks a project from the command line.

License: MIT.
