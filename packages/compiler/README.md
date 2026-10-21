# @govela/compiler

The Vela compiler for JavaScript toolchains: `npm i -D @govela/compiler` installs
the `vela` command with a prebuilt binary for this platform, no Go
toolchain needed. Install `@govela/runtime` of the same version for
bundled applications.

```sh
npx vela --project src --out build --runtime-import=bare
npx vela check --project src
```

## JavaScript API

```js
import { compile, startService } from '@govela/compiler';

const result = await compile({ path: 'src/App.vela' });
// result.ok, result.js, result.css, result.map, result.diagnostics, result.imports

const service = await startService();   // one `vela serve --stdio` process
const a = await service.compile({ path: 'src/App.vela', source, options: { sourcemap: true } });
await service.close();
```

Requests, results and options are those of the compiler service protocol
(`docs/TOOLING_PROTOCOL.md` in the compiler repository). `startService`
rejects a binary that speaks another protocol or has another version.

Supported platforms: linux x64/arm64, macOS x64/arm64, Windows x64. Set
`VELA_BINARY` to the path of a `vela` binary to use another build.

License: MIT.
