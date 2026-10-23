// The smoke test of the packaged VS Code extension (TestVSCodeExtension):
// starts vela-lsp as the extension does — the binary it finds in the
// workspace and its initializationOptions with the TypeScript in the .vsix —
// and runs a session: initialize, a member completion that needs TypeScript,
// shutdown, exit.
//
// Usage: node tools/vscode-extension-check.mjs <unpacked extension dir> <workspace>
// Prints {"server": path, "typescript": message, "completion": [labels], "exit": code}
// or {"error": "..."}.
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

const [extension, workspace] = process.argv.slice(2);
const require = createRequire(import.meta.url);
const { serverCommand, initializationOptions } = require(path.join(extension, 'server.js'));

function session(command) {
  const child = spawn(command, ['--stdio'], { cwd: workspace, stdio: ['pipe', 'pipe', 'inherit'] });
  let buffer = Buffer.alloc(0);
  const waiting = [];
  const messages = [];
  child.stdout.on('data', (chunk) => {
    buffer = Buffer.concat([buffer, chunk]);
    for (;;) {
      const header = buffer.indexOf('\r\n\r\n');
      if (header < 0) return;
      const length = Number(/Content-Length: (\d+)/i.exec(buffer.subarray(0, header).toString())[1]);
      if (buffer.length < header + 4 + length) return;
      const message = JSON.parse(buffer.subarray(header + 4, header + 4 + length).toString());
      buffer = buffer.subarray(header + 4 + length);
      messages.push(message);
      for (const w of [...waiting]) {
        if (w.match(message)) {
          waiting.splice(waiting.indexOf(w), 1);
          w.resolve(message);
        }
      }
    }
  });
  const send = (message) => {
    const body = JSON.stringify({ jsonrpc: '2.0', ...message });
    child.stdin.write(`Content-Length: ${Buffer.byteLength(body)}\r\n\r\n${body}`);
  };
  const wait = (match, what) => new Promise((resolve, reject) => {
    const found = messages.find(match);
    if (found) return resolve(found);
    waiting.push({ match, resolve });
    setTimeout(() => reject(new Error(`timeout waiting for ${what}`)), 30000).unref();
  });
  let id = 0;
  const request = (method, params) => {
    const n = ++id;
    send({ id: n, method, params });
    return wait((m) => m.id === n, method);
  };
  const exited = new Promise((resolve) => child.on('exit', (code) => resolve(code)));
  return { send, wait, request, exited, child };
}

async function main() {
  const command = serverCommand('', [workspace]);
  const options = initializationOptions(extension, { diagnosticsDelay: 0, typescriptEnable: true, tsdk: '' });
  if (!fs.existsSync(path.join(options.typescript.tsdk, 'typescript.js'))) throw new Error(`no TypeScript in the extension: ${options.typescript.tsdk}`);
  const s = session(command);
  const rootUri = pathToFileURL(workspace).href;
  const init = await s.request('initialize', { processId: process.pid, rootUri, capabilities: {}, initializationOptions: options });
  if (init.error) throw new Error(JSON.stringify(init.error));
  s.send({ method: 'initialized', params: {} });
  const typescript = (await s.wait((m) => m.method === 'window/logMessage' && /TypeScript|JavaScript/.test(m.params.message), 'the TypeScript log')).params.message;

  const file = path.join(workspace, 'src', 'App.vela');
  const marked = fs.readFileSync(file, 'utf8').replace('    open = false;', '    open = false;\n    items.|');
  const offset = marked.indexOf('|');
  const before = marked.slice(0, offset).split('\n');
  const uri = pathToFileURL(file).href;
  s.send({ method: 'textDocument/didOpen', params: { textDocument: { uri, languageId: 'vela', version: 1, text: marked.replace('|', '') } } });
  const completion = await s.request('textDocument/completion', { textDocument: { uri }, position: { line: before.length - 1, character: before.at(-1).length } });
  const labels = (completion.result?.items ?? []).map((item) => item.label);

  await s.request('shutdown', null);
  s.send({ method: 'exit' });
  s.child.stdin.end();
  return { server: command, typescript, completion: labels, exit: await s.exited };
}

try {
  console.log(JSON.stringify(await main()));
} catch (error) {
  console.log(JSON.stringify({ error: String(error?.stack ?? error) }));
  process.exit(0);
}
