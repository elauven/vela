#!/usr/bin/env node
// The `vela` command of @govela/compiler: runs the binary of this platform
// with the arguments, standard streams and exit code of the command.
import { spawnSync } from 'node:child_process';
import { binaryPath } from '../index.js';

let binary;
try {
  binary = binaryPath();
} catch (error) {
  console.error(error.message);
  process.exit(1);
}
const result = spawnSync(binary, process.argv.slice(2), { stdio: 'inherit', windowsHide: true });
if (result.error) {
  console.error(`vela: cannot run ${binary}: ${result.error.message}`);
  process.exit(1);
}
if (result.signal) {
  process.kill(process.pid, result.signal);
}
process.exit(result.status ?? 1);
