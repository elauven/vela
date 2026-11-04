#!/usr/bin/env node
// The `vela-app` command of @govela/app: runs the vela-app binary of this
// platform (the optional dependency @govela/app-<os>-<cpu>), or
// $VELA_APP_BINARY, with the arguments, standard streams and exit code of
// the command.
import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const manifest = JSON.parse(readFileSync(new URL('../package.json', import.meta.url), 'utf8'));
const platforms = ['linux-x64', 'linux-arm64', 'darwin-x64', 'darwin-arm64', 'win32-x64'];

function binaryPath() {
  if (process.env.VELA_APP_BINARY) {
    return process.env.VELA_APP_BINARY;
  }
  const platform = `${process.platform}-${process.arch}`;
  const name = `${manifest.name}-${platform}`;
  if (!platforms.includes(platform)) {
    throw new Error(`vela-app: no prebuilt binary for ${platform}; supported: ${platforms.join(', ')}. Install it with \`go install github.com/elauven/vela/app/cmd/vela-app@v${manifest.version}\` and set VELA_APP_BINARY to it.`);
  }
  const file = `bin/vela-app${process.platform === 'win32' ? '.exe' : ''}`;
  try {
    return require.resolve(`${name}/${file}`);
  } catch {
    throw new Error(`vela-app: the package ${name} is not installed. It is an optional dependency of ${manifest.name}; reinstall without --no-optional / --omit=optional, or set VELA_APP_BINARY to a vela-app binary.`);
  }
}

let binary;
try {
  binary = binaryPath();
} catch (error) {
  console.error(error.message);
  process.exit(1);
}
const result = spawnSync(binary, process.argv.slice(2), { stdio: 'inherit', windowsHide: true });
if (result.error) {
  console.error(`vela-app: cannot run ${binary}: ${result.error.message}`);
  process.exit(1);
}
if (result.signal) {
  process.kill(process.pid, result.signal);
}
process.exit(result.status ?? 1);
