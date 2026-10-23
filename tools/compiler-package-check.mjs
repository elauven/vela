// Exercises the JavaScript API of an installed @govela/compiler for
// TestCompilerNpmPackage (task 062).
//
// Usage: node tools/compiler-package-check.mjs <application dir> <fake binaries dir>
// The application has the package installed and src/App.vela, src/Broken.vela.
// Prints {"checks": {name: "ok" | error message}}.
import { createRequire } from 'node:module';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';

const [app, fakes] = process.argv.slice(2);
const require = createRequire(join(app, 'package.json'));
const api = await import(pathToFileURL(require.resolve('@govela/compiler')).href);
const checks = {};

async function check(name, run) {
  try {
    await run();
    checks[name] = 'ok';
  } catch (error) {
    checks[name] = String(error?.stack ?? error);
  }
}

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

await check('binaryPath', async () => {
  const path = api.binaryPath();
  assert(path.includes(`compiler-${process.platform}-${process.arch}`), `binary ${path}`);
});

await check('languageServerPath', async () => {
  const path = api.languageServerPath();
  assert(path.includes(`compiler-${process.platform}-${process.arch}`) && /vela-lsp(\.exe)?$/.test(path), `language server ${path}`);
});

await check('compile', async () => {
  const result = await api.compile({ path: 'src/App.vela' }, { cwd: app });
  assert(result.ok && result.js.includes('@govela/runtime') && result.css.includes('color'), JSON.stringify(result));
});

await check('service', async () => {
  const service = await api.startService({ cwd: app });
  assert(service.handshake().compilerVersion === api.version, JSON.stringify(service.handshake()));
  const results = await Promise.all([
    service.compile({ path: 'src/App.vela', options: { sourcemap: true } }),
    service.compile({ path: 'src/Broken.vela' }),
    service.compile({ path: 'src/Inline.vela', source: '<template><b>x</b></template>', options: { css: 'inject' } }),
  ]);
  await service.close();
  assert(results[0].ok && results[0].map?.version === 3, 'source map: ' + JSON.stringify(results[0]));
  assert(!results[1].ok && results[1].diagnostics[0]?.code === 'VELA-BIND-MEMBER', 'broken: ' + JSON.stringify(results[1]));
  assert(results[2].ok && results[2].js.includes("element('b'"), 'inline: ' + JSON.stringify(results[2]));
  let closed = false;
  await service.compile({ path: 'src/App.vela' }).catch((error) => { closed = error.code === 'VELA_EXIT'; });
  assert(closed, 'a closed service rejects requests');
});

async function rejects(name, binary, code, text) {
  await check(name, async () => {
    let error;
    await api.startService({ cwd: app, binary }).then((s) => s.close(), (e) => { error = e; });
    assert(error?.code === code && error.message.includes(text), `error ${error?.code}: ${error?.message}`);
  });
}

if (process.platform !== 'win32') {
  await rejects('version mismatch', join(fakes, 'old-version'), 'VELA_VERSION', 'is version 0.0.1');
  await rejects('protocol mismatch', join(fakes, 'new-protocol'), 'VELA_PROTOCOL', 'protocol 2');
}
await rejects('missing binary', join(fakes, 'missing'), 'VELA_BINARY', 'cannot run');

console.log(JSON.stringify({ checks }));
