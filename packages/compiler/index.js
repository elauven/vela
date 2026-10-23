// The JavaScript API of @govela/compiler: the binary of this platform, and
// the compiler service protocol (docs/TOOLING_PROTOCOL.md) over
// `vela serve --stdio`.
import { spawn } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { createInterface } from 'node:readline';

const require = createRequire(import.meta.url);
const manifest = JSON.parse(readFileSync(new URL('./package.json', import.meta.url), 'utf8'));

/** The version of this package; the binary must have the same version. */
export const version = manifest.version;

/** The service protocol version this package speaks. */
export const protocol = 1;

const platforms = ['linux-x64', 'linux-arm64', 'darwin-x64', 'darwin-arm64', 'win32-x64'];

function velaError(code, message) {
  const error = new Error(message);
  error.code = code;
  return error;
}

// The binary `command` of the installed platform package, or the path in
// the environment variable `env`.
function platformBinary(command, env, module) {
  if (process.env[env]) {
    return process.env[env];
  }
  const platform = `${process.platform}-${process.arch}`;
  const name = `${manifest.name}-${platform}`;
  if (!platforms.includes(platform)) {
    throw velaError('VELA_PLATFORM', `${command}: no prebuilt binary for ${platform}; supported: ${platforms.join(', ')}. Install it with \`go install ${module}@v${version}\` and set ${env} to it.`);
  }
  const file = `bin/${command}${process.platform === 'win32' ? '.exe' : ''}`;
  try {
    return require.resolve(`${name}/${file}`);
  } catch {
    throw velaError('VELA_BINARY', `${command}: the package ${name} is not installed. It is an optional dependency of ${manifest.name}; reinstall without --no-optional / --omit=optional, or set ${env} to a ${command} binary.`);
  }
}

/**
 * The path of the vela binary: VELA_BINARY when it is set, else the binary
 * of the installed platform package.
 */
export function binaryPath() {
  return platformBinary('vela', 'VELA_BINARY', 'github.com/elauven/vela');
}

/**
 * The path of the vela-lsp language server: VELA_LSP_BINARY when it is set,
 * else the binary of the installed platform package. Editors start it with
 * `--stdio`.
 */
export function languageServerPath() {
  return platformBinary('vela-lsp', 'VELA_LSP_BINARY', 'github.com/elauven/vela/cmd/vela-lsp');
}

/**
 * Starts `vela serve --stdio`. Resolves when the compiler has answered the
 * handshake with this package's protocol and version.
 */
export function startService({ cwd, config, args = [], binary } = {}) {
  const command = binary ?? binaryPath();
  const argv = ['serve', '--stdio', '--protocol', String(protocol), ...(config ? ['--config', config] : []), ...args];
  const child = spawn(command, argv, { cwd, stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true });
  const lines = createInterface({ input: child.stdout });
  const pending = new Map();
  let stderr = '';
  let nextId = 1;
  let closed = false;
  child.stderr.on('data', (chunk) => { stderr += chunk; });

  return new Promise((resolve, reject) => {
    let handshake = null;
    const fail = (error) => {
      for (const { reject: rejectRequest } of pending.values()) rejectRequest(error);
      pending.clear();
      if (!handshake) reject(error);
    };
    child.on('error', (error) => fail(velaError('VELA_BINARY', `vela: cannot run ${command}: ${error.message}`)));
    // 'close' comes after the last output line, unlike 'exit'.
    child.on('close', (code, signal) => {
      closed = true;
      if (pending.size > 0 || !handshake) {
        fail(velaError('VELA_EXIT', `vela serve exited (${signal ?? code})${stderr ? `: ${stderr.trim()}` : ''}`));
      }
    });
    lines.on('line', (line) => {
      let message;
      try {
        message = JSON.parse(line);
      } catch {
        fail(velaError('VELA_PROTOCOL', `vela serve wrote a line that is not JSON: ${line}`));
        return;
      }
      if (!handshake) {
        handshake = message;
        if (message.error || message.protocol !== protocol) {
          child.kill();
          reject(velaError('VELA_PROTOCOL', `vela: ${message.error ?? `the compiler speaks protocol ${message.protocol}, ${manifest.name} ${version} speaks ${protocol}`}`));
          return;
        }
        if (message.compilerVersion !== version) {
          child.kill();
          reject(velaError('VELA_VERSION', `vela: the binary ${command} is version ${message.compilerVersion}, ${manifest.name} is ${version}; install matching versions${process.env.VELA_BINARY ? ' or unset VELA_BINARY' : ''}`));
          return;
        }
        resolve(service);
        return;
      }
      const request = pending.get(JSON.stringify(message.id));
      if (request) {
        pending.delete(JSON.stringify(message.id));
        request.resolve(message);
      }
    });

    const service = {
      handshake: () => handshake,
      compile(request) {
        if (closed) {
          return Promise.reject(velaError('VELA_EXIT', 'vela serve is closed'));
        }
        const id = nextId++;
        return new Promise((resolveRequest, rejectRequest) => {
          pending.set(JSON.stringify(id), { resolve: resolveRequest, reject: rejectRequest });
          child.stdin.write(JSON.stringify({ ...request, id }) + '\n');
        });
      },
      close() {
        if (closed) return Promise.resolve();
        return new Promise((resolveClose) => {
          child.once('close', () => resolveClose());
          child.stdin.end();
        });
      },
    };
  });
}

/** Compiles one component with a short-lived service. */
export async function compile(request, serviceOptions) {
  const service = await startService(serviceOptions);
  try {
    return await service.compile(request);
  } finally {
    await service.close();
  }
}
