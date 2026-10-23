#!/usr/bin/env node
// The `vela-lsp` command of @govela/compiler: runs the language server of
// this platform with the arguments, standard streams and exit code of the
// command. Editor extensions can start languageServerPath() directly.
import { spawnSync } from 'node:child_process';
import { languageServerPath } from '../index.js';

let binary;
try {
  binary = languageServerPath();
} catch (error) {
  console.error(error.message);
  process.exit(1);
}
const result = spawnSync(binary, process.argv.slice(2), { stdio: 'inherit', windowsHide: true });
if (result.error) {
  console.error(`vela-lsp: cannot run ${binary}: ${result.error.message}`);
  process.exit(1);
}
if (result.signal) {
  process.kill(process.pid, result.signal);
}
process.exit(result.status ?? 1);
