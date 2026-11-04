// Browser check of examples/app for TestExampleAppHydrates (task 075).
//
// Usage: node app/testdata/e2e.mjs <project> <server binary> <cases JSON>
//
// Builds the client with vite build (the project's vite.config.js), starts
// the Go server with the build, and opens every case in Chromium. Prints
// {"pages": [{path, status, title, location, hydrated, sameDOM, removed,
// preloaded, after, logs, diff}]} or {"error": "..."}.
import { spawn } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { createInterface } from 'node:readline';
import { chromium } from 'playwright';
import { build } from 'vite';

const [project, serverBinary, casesJSON] = process.argv.slice(2);
const cases = JSON.parse(casesJSON);

function startServer() {
  const child = spawn(serverBinary, ['-addr', '127.0.0.1:0', '-dist', 'dist'], { cwd: project, stdio: ['ignore', 'pipe', 'pipe'] });
  let stderr = '';
  child.stderr.on('data', (chunk) => { stderr += chunk; });
  return new Promise((resolve, reject) => {
    createInterface({ input: child.stdout }).once('line', (url) => resolve({ url, stop: () => child.kill() }));
    child.on('exit', (code) => reject(new Error(`server exited (${code}): ${stderr}`)));
  });
}

async function check(browser, url, c, files) {
  const response = await fetch(url + c.path, { redirect: 'manual' });
  const result = { path: c.path, status: response.status, location: response.headers.get('location') ?? '', logs: [] };
  if (c.location) return result;
  const serverHTML = await response.text();
  const page = await browser.newPage();
  page.on('console', (m) => {
    if (m.type() !== 'error' && m.type() !== 'warning') return;
    // Chromium reports the status of an error document itself.
    if (m.location().url === url + c.path && m.text().startsWith('Failed to load resource')) return;
    result.logs.push(`${m.type()}: ${m.text()} (${m.location().url})`);
  });
  page.on('pageerror', (e) => result.logs.push(`pageerror: ${e.message}`));
  // An element of the server HTML removed during hydration means the
  // client did not adopt it; hydrate removes only the payload script.
  await page.addInitScript(() => {
    window.__removed = [];
    new MutationObserver((records) => {
      for (const r of records) {
        for (const n of r.removedNodes) {
          if (n.nodeType === 1 && !n.hasAttribute('data-vela-root')) window.__removed.push(n.outerHTML);
        }
      }
    }).observe(document, { childList: true, subtree: true });
    document.addEventListener('vela:hydrated', () => { window.__hydrated = true; });
  });
  try {
    const navigation = await page.goto(url + c.path);
    result.status = navigation.status();
    result.hydrated = await page.waitForFunction(() => window.__hydrated === true, null, { timeout: 15000 }).then(() => true, () => false);
    Object.assign(result, await page.evaluate((html) => {
      // The DOM of the server HTML without the payload script, as the
      // hydrated page must still have it.
      const server = new DOMParser().parseFromString(html, 'text/html').getElementById('vela-app');
      server.querySelector('script[data-vela-root]')?.remove();
      const client = document.getElementById('vela-app').innerHTML;
      let at = 0;
      while (at < client.length && client[at] === server.innerHTML[at]) at++;
      return {
        title: document.title,
        removed: window.__removed,
        sameDOM: client === server.innerHTML,
        diff: client === server.innerHTML ? '' : `server: ${server.innerHTML.slice(Math.max(0, at - 80), at + 80)}\nclient: ${client.slice(Math.max(0, at - 80), at + 80)}`,
        preloads: [...document.querySelectorAll('link[rel=modulepreload]')].map((l) => new URL(l.href).pathname.slice(1)),
      };
    }, serverHTML));
    result.preloaded = result.preloads.map((file) => files[file]).filter(Boolean);
    delete result.preloads;
    if (c.click) {
      await page.click(c.click);
      await page.waitForTimeout(100);
      result.after = await page.textContent(c.click);
    }
  } finally {
    await page.close();
  }
  return result;
}

async function main() {
  await build({ root: project, configFile: join(project, 'vite.config.js'), logLevel: 'silent', build: { outDir: join(project, 'dist') } });
  // The source of each built file, to name the preloaded lazy pages.
  const manifest = JSON.parse(readFileSync(join(project, 'dist', '.vite', 'manifest.json'), 'utf8'));
  const files = Object.fromEntries(Object.entries(manifest).map(([key, chunk]) => [chunk.file, key]));
  const server = await startServer();
  const browser = await chromium.launch();
  try {
    const pages = [];
    for (const c of cases) pages.push(await check(browser, server.url, c, files));
    return { pages };
  } finally {
    await browser.close();
    server.stop();
  }
}

main().then(
  (result) => process.stdout.write(JSON.stringify(result)),
  (error) => process.stdout.write(JSON.stringify({ error: String(error?.stack ?? error) })),
);
