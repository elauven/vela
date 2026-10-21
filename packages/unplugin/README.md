# @govela/unplugin

Vela components in Vite, Rollup, Rolldown, esbuild, webpack and Rspack.
The bundler resolves, bundles and serves; the plugin compiles `.vela`
modules through the compiler service of `@govela/compiler`.

```sh
npm i -D @govela/unplugin && npm i @govela/runtime
```

```js
// vite.config.js
import vela from '@govela/unplugin/vite';

export default { plugins: [vela()] };
```

```js
// esbuild
import vela from '@govela/unplugin/esbuild';

await esbuild.build({ entryPoints: ['src/main.js'], bundle: true, outdir: 'dist', plugins: [vela()] });
```

Options: `css` ('external', the default for Vite and esbuild: the CSS of
a component is the module `X.vela?vela&type=style&lang.css` for the
bundler's CSS pipeline; 'inject': the component adds it at runtime, the
default for the other bundlers), `cwd`, `config` (a vela.config.json),
`binary` and `compilerOptions` (file options such as `target`).
Diagnostics are bundler errors and warnings with their location.

Install `@govela/runtime`, `@govela/compiler` and this package with
the same version.

License: MIT.
