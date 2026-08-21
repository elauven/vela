// browser-runtime-harness.mjs — execute compiled Vela components in Chromium.
import { createServer } from "node:http";
import { readFileSync } from "node:fs";
import { resolve, relative, sep } from "node:path";
import { chromium } from "playwright";

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
    const html = "<!doctype html><script type=module>import { " + config.exportName + " } from " + JSON.stringify(entry) + "; window.__velaInstance = " + config.exportName + "(document.body, {}, {});</script>";
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
  if (config.storage) await page.addInitScript((entries) => { for (const [key, value] of Object.entries(entries)) localStorage.setItem(key, value); }, config.storage);
  const failures = [];
  page.on("pageerror", (error) => failures.push("page error: " + String(error.stack || error)));
  page.on("console", (message) => { if (message.type() === "error") failures.push("console error: " + message.text()); });
  page.on("requestfailed", (request) => failures.push("request failed: " + request.url() + " " + (request.failure()?.errorText || "")));
  page.on("response", (response) => { if (response.status() >= 400) failures.push("HTTP " + response.status() + ": " + response.url()); });
  await page.goto(origin + "/");
  await page.waitForFunction(() => window.__velaInstance);

  for (let i = 0; i < (config.steps || []).length; i++) {
    const step = config.steps[i];
    const tag = "step " + i;
    try {
      if (step.assertStorage) {
        const got = await page.evaluate((key) => localStorage.getItem(key), step.assertStorage);
        if (got == null || !got.includes(step.contains ?? "")) failures.push(tag + ": localStorage[" + step.assertStorage + "] = " + JSON.stringify(got));
      } else if (step.assertText) {
        const got = (await page.locator(step.assertText).first().textContent())?.trim() ?? null;
        if (got !== step.equals) failures.push(tag + ": text of " + step.assertText + " = " + JSON.stringify(got) + ", want " + JSON.stringify(step.equals));
      } else if (step.assertCount) {
        const got = await page.locator(step.assertCount).count();
        if (got !== step.equals) failures.push(tag + ": count of " + step.assertCount + " = " + got + ", want " + step.equals);
      } else if (step.assertValue) {
        const got = await page.locator(step.assertValue).first().inputValue();
        if (got !== step.equals) failures.push(tag + ": value of " + step.assertValue + " = " + JSON.stringify(got) + ", want " + JSON.stringify(step.equals));
      } else if (step.click) {
        await page.locator(step.click).first().click();
      } else if (step.dblclick) {
        await page.locator(step.dblclick).first().dblclick();
      } else if (step.input) {
        await page.locator(step.input).first().fill(step.value ?? "");
      } else if (step.change) {
        const locator = page.locator(step.change).first();
        if ("checked" in step) {
          if (step.checked) await locator.check(); else await locator.uncheck();
        } else {
          await locator.selectOption(step.value);
        }
      } else if (step.submit) {
        await page.locator(step.submit).first().dispatchEvent("submit");
      } else if (step.keydown) {
        await page.locator(step.keydown).first().press(step.key);
      } else if (step.blur) {
        await page.locator(step.blur).first().blur();
      } else {
        failures.push(tag + ": unsupported step");
      }
    } catch (error) { failures.push(tag + ": " + String(error.message || error)); }
  }
  try { await page.evaluate(() => window.__velaInstance?.destroy()); } catch (error) { failures.push("destroy failed: " + String(error.stack || error)); }
  done({ ok: failures.length === 0, failures });
} catch (error) { done({ ok: false, failures: [String(error.stack || error)] }); } finally { await browser?.close(); await new Promise((resolveClose) => server.close(resolveClose)); }
