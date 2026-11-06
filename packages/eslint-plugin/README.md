# @govela/eslint-plugin

ESLint for Vela components. The processor lints the `<script>` and the
template expressions of a `.vela` file as one program, the virtual
document of `vela document`: a variable used only in the template is
used, an undefined name in `{expression}` is reported at its place, and
the names of `{#each}` and `{#await}` are declared. Messages about
generated text are dropped; fixes inside the component's own text apply.

```sh
npm i -D @govela/eslint-plugin eslint @eslint/js globals
```

```js
// eslint.config.js
import js from '@eslint/js';
import globals from 'globals';
import vela from '@govela/eslint-plugin';

export default [
  js.configs.recommended,
  ...vela.configs.recommended,
  // The code of the components: the virtual files App.vela/0_App.vela.js.
  { files: ['**/*.vela/*.js'], languageOptions: { globals: globals.browser } },
];
```

`configs.recommended` sets the processor for `**/*.vela` and declares
`$state`, `$derived`, `$effect`, `$bindable` and `$$dispatch`. The
template markup is not linted. The compiler binary is the one of
`@govela/compiler` (or `VELA_BINARY`).

License: MIT.
