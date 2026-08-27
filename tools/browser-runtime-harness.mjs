// browser-runtime-harness.mjs — execute compiled Vela components in Chromium.
import { createServer } from "node:http";
import { readFileSync } from "node:fs";
import { resolve, relative, sep } from "node:path";
import { chromium } from "playwright";
import { fixtureStorage, replayFixtureSteps } from './runtime-fixture-dsl.mjs';

const [, , configPath] = process.argv;
function done(value) { process.stdout.write(JSON.stringify(value)); }
if (!configPath) { done({ ok: false, failures: ["usage: browser-runtime-harness.mjs <config.json>"] }); process.exit(0); }
let config;
try { config = JSON.parse(readFileSync(configPath, "utf8")); } catch (error) { done({ ok: false, failures: ["cannot read config: " + error.message] }); process.exit(0); }
const root = resolve(config.rootDir);
const entry = "/" + config.entry;
const server = createServer((req, res) => {
  const pathname = new URL(req.url, "http://vela.test").pathname;
  if (pathname === "/") {
    const html = "<!doctype html><script type=module>import { " + config.exportName + " } from " + JSON.stringify(entry) + "; window.__velaMount = () => " + config.exportName + "(document.body, {}, {}); window.__velaInstance = window.__velaMount();</script>";
    res.writeHead(200, { "content-type": "text/html" }).end(html);
    return;
  }
  const file = resolve(root, "." + pathname);
  if (relative(root, file).startsWith(".." + sep) || file === root) { res.writeHead(403).end(); return; }
  try {
    const body = readFileSync(file);
    const type = pathname.endsWith(".js") ? "text/javascript" : pathname.endsWith(".css") ? "text/css" : "application/octet-stream";
    res.writeHead(200, { "content-type": type }).end(body);
  } catch { res.writeHead(404).end(); }
});
await new Promise((resolveListen) => server.listen(0, "127.0.0.1", resolveListen));
const address = server.address();
const origin = "http://127.0.0.1:" + address.port;
let browser;
try {
  browser = await chromium.launch({ headless: true });
  const page = await browser.newPage();
  page.setDefaultTimeout(5000);
  page.setDefaultNavigationTimeout(5000);
  const storage = Object.assign({}, config.storage || {}, fixtureStorage(config.steps || []));
  if (Object.keys(storage).length) await page.addInitScript((entries) => { for (const [key, value] of Object.entries(entries)) localStorage.setItem(key, value); }, storage);
  const failures = [];
  page.on("pageerror", (error) => failures.push("page error: " + String(error.stack || error)));
  page.on("console", (message) => { if (message.type() === "error") failures.push("console error: " + message.text()); });
  page.on("requestfailed", (request) => failures.push("request failed: " + request.url() + " " + (request.failure()?.errorText || "")));
  page.on("response", (response) => { if (response.status() >= 400) failures.push("HTTP " + response.status() + ": " + response.url()); });
  await page.goto(origin + "/");
  await page.waitForFunction(() => window.__velaInstance);

  const adapter = {
    text: async (selector) => (await page.locator(selector).first().textContent())?.trim() ?? null,
    html: async (selector) => page.locator(selector).first().evaluate(element => element.innerHTML),
    value: async (selector) => page.locator(selector).first().inputValue(),
    storage: (key) => page.evaluate(value => localStorage.getItem(value), key),
    global: (path) => page.evaluate((valuePath) => String(valuePath).split('.').reduce(
      (value, key) => value == null ? undefined : value[key], globalThis), path),
    count: (selector) => page.locator(selector).count(),
    focused: (selector) => page.locator(selector).first().evaluate(element => document.activeElement === element),
    style: (selector, property) => page.locator(selector).first().evaluate(
      (element, name) => getComputedStyle(element).getPropertyValue(name), property),
    focus: (selector) => page.locator(selector).first().focus(),
    click: (selector) => page.locator(selector).first().click(),
    dblclick: (selector) => page.locator(selector).first().dblclick(),
    submit: (selector) => page.locator(selector).first().dispatchEvent('submit'),
    input: (selector, value) => page.locator(selector).first().fill(value),
    change: async (selector, step) => {
      const locator = page.locator(selector).first();
      if ('checked' in step) {
        if (step.checked) await locator.check(); else await locator.uncheck();
      } else {
        await locator.selectOption(step.value);
      }
    },
    blur: (selector) => page.locator(selector).first().blur(),
    keydown: (selector, key) => page.locator(selector).first().press(key),
    remount: () => page.evaluate(() => {
      window.__velaInstance?.destroy();
      document.body.replaceChildren();
      window.__velaInstance = window.__velaMount();
    }),
    settle: async () => {
      for (let turn = 0; turn < 5; turn++) {
        await page.evaluate(() => Promise.resolve());
      }
    },
  };
  failures.push(...await replayFixtureSteps(config.steps || [], adapter));
  try { await page.evaluate(() => window.__velaInstance?.destroy()); } catch (error) { failures.push("destroy failed: " + String(error.stack || error)); }
  done({ ok: failures.length === 0, failures });
} catch (error) { done({ ok: false, failures: [String(error.stack || error)] }); } finally { await browser?.close(); server.closeAllConnections?.(); await new Promise((resolveClose) => server.close(resolveClose)); }
