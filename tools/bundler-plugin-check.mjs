// Builds or serves the bundler plugin fixture and runs it in Chromium for
// TestBundlerPlugin (task 063).
//
// Usage: node tools/bundler-plugin-check.mjs <vite-dev|vite-build|esbuild|rolldown|vite-error|entries> <app> [development|production]
//
// The app directory has node_modules with @govela/unplugin, @govela/compiler
// (VELA_BINARY names the binary), @govela/runtime and tiny-fmt. Prints
// {"before": {...}, "after": {...}, "logs": [...], "bundle": {...}} after
// clicking the counter once, or {"error": "..."}; vite-error prints the
// build error.
import { createServer } from 'node:http';
import { existsSync, readdirSync, readFileSync, writeFileSync } from 'node:fs';
import { extname, join } from 'node:path';
import { pathToFileURL } from 'node:url';
import { chromium } from 'playwright';

const [mode, app, buildMode = 'development'] = process.argv.slice(2);
const plugin = async (bundler) => (await import(pathToFileURL(join(app, 'node_modules', '@govela', 'unplugin', `${bundler}.js`)).href)).default;
const types = { '.js': 'text/javascript', '.css': 'text/css', '.html': 'text/html' };

function serveDir(root) {
  const server = createServer((req, res) => {
    const file = join(root, req.url === '/' ? 'index.html' : decodeURIComponent(req.url.split('?')[0]));
    try {
      const body = readFileSync(file);
      res.writeHead(200, { 'content-type': types[extname(file)] ?? 'application/octet-stream' });
      res.end(body);
    } catch {
      res.writeHead(404);
      res.end();
    }
  });
  return new Promise((resolve) => server.listen(0, '127.0.0.1', () => resolve({
    url: `http://127.0.0.1:${server.address().port}/`,
    close: () => new Promise((done) => server.close(done)),
  })));
}

// What the page shows: the counter, the badge of the child component, the
// colors of their scoped CSS and the store subscriber.
async function inspect(url, after) {
  const browser = await chromium.launch();
  const logs = [];
  try {
    const page = await browser.newPage();
    page.on('console', (m) => { if (m.type() !== 'debug') logs.push(`${m.type()}: ${m.text()}`); });
    page.on('pageerror', (e) => logs.push(`pageerror: ${e.message}`));
    await page.goto(url);
    await page.waitForSelector('button.inc', { timeout: 10000 });
    const state = () => page.evaluate(() => {
      const color = (el) => (el ? getComputedStyle(el).color : null);
      const inc = document.querySelector('button.inc');
      const badge = document.querySelector('.badge');
      return {
        counter: inc.textContent,
        badge: badge?.textContent ?? null,
        counterColor: color(inc),
        badgeColor: color(badge),
        clicks: document.getElementById('clicks').textContent,
        dev: document.body.dataset.dev,
      };
    });
    const before = await state();
    await page.click('button.inc');
    await page.waitForTimeout(100);
    const result = { before, after: await state(), logs };
    if (after) result.edited = await after(page, state);
    return result;
  } finally {
    await browser.close();
  }
}

// The JavaScript of a build: how many runtime instances it has and whether
// it still reads process.env.
function bundleInfo(dir) {
  let js = '';
  const walk = (d) => {
    for (const entry of readdirSync(d, { withFileTypes: true })) {
      const p = join(d, entry.name);
      if (entry.isDirectory()) walk(p);
      else if (p.endsWith('.js')) js += readFileSync(p, 'utf8');
    }
  };
  walk(dir);
  return {
    runtimes: js.split('vela.defer').length - 1,
    processEnv: js.includes('process.env'),
  };
}

async function viteBuild() {
  const { build } = await import('vite');
  // vite build replaces process.env.NODE_ENV with the NODE_ENV of the
  // process, "production" unless it is set.
  process.env.NODE_ENV = buildMode;
  const outDir = join(app, `dist-vite-${buildMode}`);
  await build({ root: app, configFile: false, logLevel: 'silent', plugins: [(await plugin('vite'))()], build: { outDir } });
  const server = await serveDir(outDir);
  try {
    return { ...(await inspect(server.url)), bundle: bundleInfo(outDir) };
  } finally {
    await server.close();
  }
}

async function viteDev() {
  const { createServer: createViteServer } = await import('vite');
  const server = await createViteServer({ root: app, configFile: false, logLevel: 'silent', plugins: [(await plugin('vite'))()], server: { port: 0, host: '127.0.0.1' } });
  await server.listen();
  // An edited <style> reaches the page: the component and its CSS module
  // are both invalidated.
  const badge = join(app, 'src', 'widgets', 'Badge.vela');
  const edit = async (page, state) => {
    const reloaded = page.waitForEvent('load', { timeout: 10000 }).catch(() => null);
    writeFileSync(badge, readFileSync(badge, 'utf8').replace('rgb(4, 5, 6)', 'rgb(7, 8, 9)'));
    await reloaded;
    await page.waitForFunction(() => getComputedStyle(document.querySelector('.badge')).color === 'rgb(7, 8, 9)', null, { timeout: 10000 }).catch(() => null);
    return state();
  };
  try {
    return await inspect(server.resolvedUrls.local[0], edit);
  } finally {
    await server.close();
  }
}

async function esbuildBuild() {
  const esbuild = await import('esbuild');
  const outdir = join(app, `dist-esbuild-${buildMode}`);
  // esbuild defines process.env.NODE_ENV as "production" with --minify,
  // else as "development".
  await esbuild.build({
    entryPoints: [join(app, 'src', 'main.js')],
    bundle: true,
    format: 'esm',
    outdir,
    minify: buildMode === 'production',
    logLevel: 'silent',
    absWorkingDir: app,
    plugins: [(await plugin('esbuild'))()],
  });
  const css = existsSync(join(outdir, 'main.css')) ? '<link rel="stylesheet" href="/main.css">' : '';
  writeFileSync(join(outdir, 'index.html'), `<!doctype html>${css}<div id="app"></div><p id="clicks"></p><script type="module" src="/main.js"></script>`);
  const server = await serveDir(outdir);
  try {
    return { ...(await inspect(server.url)), bundle: bundleInfo(outdir) };
  } finally {
    await server.close();
  }
}

// Rolldown has no CSS pipeline of its own: the plugin injects the CSS.
async function rolldownBuild() {
  const { rolldown } = await import('rolldown');
  const dir = join(app, `dist-rolldown-${buildMode}`);
  const bundle = await rolldown({
    input: join(app, 'src', 'main.js'),
    cwd: app,
    logLevel: 'silent',
    transform: { define: { 'process.env.NODE_ENV': JSON.stringify(buildMode) } },
    plugins: [(await plugin('rolldown'))()],
  });
  await bundle.write({ dir, format: 'es', minify: buildMode === 'production' });
  await bundle.close();
  writeFileSync(join(dir, 'index.html'), '<!doctype html><div id="app"></div><p id="clicks"></p><script type="module" src="/main.js"></script>');
  const server = await serveDir(dir);
  try {
    return { ...(await inspect(server.url)), bundle: bundleInfo(dir) };
  } finally {
    await server.close();
  }
}

async function viteError() {
  const { build } = await import('vite');
  try {
    await build({ root: app, configFile: false, logLevel: 'silent', plugins: [(await plugin('vite'))()], build: { outDir: join(app, 'dist-error') } });
  } catch (error) {
    return { buildError: String(error.message), id: error.id ?? null, loc: error.loc ?? null };
  }
  return { error: 'the build of a broken component succeeded' };
}

// Every entry point loads and makes a plugin, also for the bundlers the
// repository does not install.
async function entries() {
  const names = {};
  for (const bundler of ['vite', 'rollup', 'rolldown', 'esbuild', 'webpack', 'rspack']) {
    const made = (await plugin(bundler))();
    names[bundler] = made.name ?? (typeof made.apply === 'function' ? 'apply()' : typeof made);
  }
  return { entries: names };
}

const run = { 'vite-dev': viteDev, 'vite-build': viteBuild, esbuild: esbuildBuild, rolldown: rolldownBuild, 'vite-error': viteError, entries }[mode];
try {
  if (!run) throw new Error(`unknown mode ${mode}`);
  console.log(JSON.stringify(await run()));
} catch (error) {
  console.log(JSON.stringify({ error: String(error?.stack ?? error) }));
}
// No process.exit: a compiler service left running keeps the process alive.
