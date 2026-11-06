// Runs the extension in VS Code (test/suite.cjs) on a copy of the language
// server fixture with the TypeScript files of testdata/vscode-ts, with
// vela-lsp and vela where @govela/compiler installs them and the runtime
// package.
//
// Usage: node test/run.mjs <vela-lsp binary> <vela binary>   (on Linux CI: xvfb-run -a)
// VS Code is downloaded into .vscode-test; VELA_VSCODE_USER_DIR shortens the
// user data directory, whose socket path must stay under 107 characters.
import { runTests } from '@vscode/test-electron';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const extension = path.dirname(here);
const repo = path.join(extension, '..', '..');
const fixture = path.join(repo, 'testdata', 'lsp', 'app');
const [binary, vela] = process.argv.slice(2);
if (!binary || !vela) throw new Error('usage: node test/run.mjs <vela-lsp binary> <vela binary>');

const workspace = fs.mkdtempSync(path.join(os.tmpdir(), 'vela-vscode-'));
fs.cpSync(fixture, workspace, { recursive: true });
fs.cpSync(path.join(repo, 'testdata', 'vscode-ts'), workspace, { recursive: true });
fs.rmSync(path.join(workspace, 'README.md'), { force: true });
fs.cpSync(path.join(repo, 'packages', 'runtime'), path.join(workspace, 'node_modules', '@govela', 'runtime'), { recursive: true });
const platform = `compiler-${process.platform}-${process.arch}`;
for (const file of [binary, vela]) {
  const target = path.join(workspace, 'node_modules', '@govela', platform, 'bin', path.basename(file));
  fs.mkdirSync(path.dirname(target), { recursive: true });
  fs.copyFileSync(file, target);
  fs.chmodSync(target, 0o755);
}

const userDir = process.env.VELA_VSCODE_USER_DIR ?? fs.mkdtempSync(path.join(os.tmpdir(), 'vu-'));
const result = path.join(workspace, 'result.json');
try {
  await runTests({
    extensionDevelopmentPath: extension,
    extensionTestsPath: path.join(here, 'suite.cjs'),
    extensionTestsEnv: { VELA_RESULT: result },
    launchArgs: [workspace, `--user-data-dir=${userDir}`, '--disable-extensions', '--disable-workspace-trust', '--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage'],
  });
} finally {
  if (fs.existsSync(result)) console.log(fs.readFileSync(result, 'utf8'));
  fs.rmSync(workspace, { recursive: true, force: true });
}
