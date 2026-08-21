// browser-sourcemap-harness.mjs — verify a compiled Vela source map against
// coordinates reported by a real Chromium runtime error.
import { createServer } from "node:http";
import { readFileSync } from "node:fs";
import { resolve, relative, sep } from "node:path";
import { chromium } from "playwright";
import { SourceMapConsumer } from "source-map-js";

const [, , configPath] = process.argv;
function done(value) { process.stdout.write(JSON.stringify(value)); }
if (!configPath) { done({ ok: false, error: "usage: browser-sourcemap-harness.mjs <config.json>" }); process.exit(0); }
let config;
try { config = JSON.parse(readFileSync(configPath, "utf8")); } catch (error) { done({ ok: false, error: "cannot read config: " + error.message }); process.exit(0); }
const root = resolve(config.rootDir);
const requests = [];
const server = createServer((req, res) => {
  const pathname = new URL(req.url, "http://vela.test").pathname;
  const file = resolve(root, "." + pathname);
  if (relative(root, file).startsWith(".." + sep) || file === root) { res.writeHead(403).end(); return; }
  try {
    const body = readFileSync(file);
    requests.push(pathname);
    const type = pathname.endsWith(".map") ? "application/json" : pathname.endsWith(".js") ? "text/javascript" : "text/html";
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
  const session = await page.context().newCDPSession(page);
  await session.send("Debugger.enable");
  let parsed;
  session.on("Debugger.scriptParsed", (event) => { if (event.url === origin + "/" + config.entry) parsed = event; });
  const errors = [];
  page.on("pageerror", (error) => errors.push(String(error.stack || error)));
  await page.goto(origin + "/index.html");
  await page.waitForSelector(config.selector);
  const mapResponse = await page.evaluate(async (mapPath) => { const response = await fetch(mapPath); return { status: response.status, text: await response.text() }; }, "/" + config.map);
  if (mapResponse.status !== 200) throw new Error("browser could not fetch source map: HTTP " + mapResponse.status);
  await page.click(config.selector);
  await page.waitForTimeout(20);
  if (errors.length === 0) throw new Error("expected controlled browser error was not reported");
  if (!parsed || !parsed.sourceMapURL) throw new Error("Chromium did not report sourceMapURL for " + config.entry);
  if (!requests.includes("/" + config.map)) throw new Error("source map was not served to the browser");
  const stack = errors.join("\n");
  if (!stack.includes("/" + config.entry)) throw new Error("browser stack has no generated frame for " + config.entry + ": " + stack);
  const frame = stack.match(/:(\\d+):(\\d+)/);
  if (!frame) throw new Error("browser stack has no line/column frame: " + stack);
  const consumer = new SourceMapConsumer(JSON.parse(mapResponse.text));
  const original = consumer.originalPositionFor({ line: Number(frame[1]), column: Number(frame[2]) - 1, bias: SourceMapConsumer.GREATEST_LOWER_BOUND });
  consumer.destroy?.();
  const result = { ok: original.source === config.expected.source && original.line === config.expected.line && original.column === config.expected.column, parsedSourceMapURL: parsed.sourceMapURL, generated: { line: Number(frame[1]), column: Number(frame[2]) - 1 }, original, expected: config.expected };
  if (!result.ok) result.error = "unexpected mapped source location";
  done(result);
} catch (error) { done({ ok: false, error: error && error.stack ? error.stack : String(error) }); } finally { await browser?.close(); await new Promise((resolveClose) => server.close(resolveClose)); }
