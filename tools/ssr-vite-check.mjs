// Runs the server-rendered fixture with Vite for TestSSRWithVite (task 064):
// the Go server renders the page with the tags of ssr_runtime/vite, and the
// client compiled by @govela/unplugin hydrates it in Chromium.
//
// Usage: node tools/ssr-vite-check.mjs <build|dev> <app> <server binary>
//
// build: `vite build` with a manifest, the server serves dist; dev: a Vite
// dev server, the server's tags point to it. Prints {"html": server HTML,
// "before": {...}, "after": {...}, "removed": [...], "logs": [...]} or
// {"error": "..."}; removed lists the elements of the server HTML that the
// client replaced instead of adopting.
import { spawn } from 'node:child_process';
import { createServer as createNetServer } from 'node:net';
import { join } from 'node:path';
import { createInterface } from 'node:readline';
import { pathToFileURL } from 'node:url';
import { chromium } from 'playwright';

const [mode, app, serverBinary] = process.argv.slice(2);
const vela = async () => (await import(pathToFileURL(join(app, 'node_modules', '@govela', 'unplugin', 'vite.js')).href)).default();

function freePort() {
  return new Promise((resolve) => {
    const server = createNetServer().listen(0, '127.0.0.1', () => {
      const { port } = server.address();
      server.close(() => resolve(port));
    });
  });
}

// Starts the Go server; resolves with its URL, the first line it prints.
function startServer(args) {
  const child = spawn(serverBinary, args, { cwd: app, stdio: ['ignore', 'pipe', 'pipe'] });
  let stderr = '';
  child.stderr.on('data', (chunk) => { stderr += chunk; });
  return new Promise((resolve, reject) => {
    createInterface({ input: child.stdout }).once('line', (url) => resolve({ url, stop: () => child.kill() }));
    child.on('exit', (code) => reject(new Error(`server exited (${code}): ${stderr}`)));
  });
}

async function inspect(url) {
  const html = await (await fetch(url)).text();
  const browser = await chromium.launch();
  const logs = [];
  try {
    const page = await browser.newPage();
    page.on('console', (m) => { if (m.type() === 'error' || m.type() === 'warning') logs.push(`${m.type()}: ${m.text()}`); });
    page.on('pageerror', (e) => logs.push(`pageerror: ${e.message}`));
    // An element of the server HTML removed during hydration means the
    // client did not adopt it (a structural mismatch remounts the root);
    // hydrate removes only the payload script.
    await page.addInitScript(() => {
      window.__removed = [];
      new MutationObserver((records) => {
        for (const r of records) {
          for (const n of r.removedNodes) {
            if (n.nodeType === 1 && !n.hasAttribute('data-vela-root')) window.__removed.push(n.outerHTML);
          }
        }
      }).observe(document, { childList: true, subtree: true });
    });
    await page.goto(url);
    await page.waitForFunction(() => document.body.dataset.hydrated === 'true', null, { timeout: 15000 });
    const state = () => page.evaluate(() => {
      const color = (el) => (el ? getComputedStyle(el).color : null);
      const inc = document.querySelector('button.inc');
      const badge = document.querySelector('.badge');
      return { counter: inc?.textContent ?? null, badge: badge?.textContent ?? null, counterColor: color(inc), badgeColor: color(badge) };
    });
    // Vite's dev server injects the CSS with the modules.
    await page.waitForFunction(() => getComputedStyle(document.querySelector('.badge')).color === 'rgb(4, 5, 6)', null, { timeout: 5000 }).catch(() => {});
    const before = await state();
    const removed = await page.evaluate(() => window.__removed);
    await page.click('button.inc');
    await page.waitForTimeout(100);
    return { html, before, after: await state(), removed, logs };
  } finally {
    await browser.close();
  }
}

async function build() {
  const { build: viteBuild } = await import('vite');
  const dist = join(app, 'dist');
  await viteBuild({
    root: app,
    configFile: false,
    logLevel: 'silent',
    plugins: [await vela()],
    build: { outDir: dist, manifest: true, rollupOptions: { input: join(app, 'src', 'client.js') } },
  });
  const server = await startServer(['-dist', dist]);
  try {
    return await inspect(server.url);
  } finally {
    server.stop();
  }
}

async function dev() {
  const { createServer } = await import('vite');
  const port = await freePort();
  const origin = `http://127.0.0.1:${port}`;
  // server.origin: URLs the dev server writes point to it, not to the Go
  // server that serves the page.
  const vite = await createServer({ root: app, configFile: false, logLevel: 'silent', plugins: [await vela()], server: { host: '127.0.0.1', port, strictPort: true, origin } });
  await vite.listen();
  try {
    const server = await startServer(['-dev', origin]);
    try {
      return await inspect(server.url);
    } finally {
      server.stop();
    }
  } finally {
    await vite.close();
  }
}

try {
  const run = { build, dev }[mode];
  if (!run) throw new Error(`unknown mode ${mode}`);
  console.log(JSON.stringify(await run()));
} catch (error) {
  console.log(JSON.stringify({ error: String(error?.stack ?? error) }));
}
