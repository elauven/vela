// Runs a bundled page in Chromium for TestBundlerRecipeEsbuild (task 048).
//
// Usage: node tools/bundler-recipe-check.mjs <dir with index.html and app.js>
// Clicks every button once and prints {"before": [...], "after": [...], "logs": [...]}.
import { createServer } from 'node:http';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { chromium } from 'playwright';

const root = process.argv[2];
const server = createServer((req, res) => {
  const file = join(root, req.url === '/' ? 'index.html' : req.url);
  try {
    const body = readFileSync(file);
    res.writeHead(200, { 'content-type': file.endsWith('.js') ? 'text/javascript' : 'text/html' });
    res.end(body);
  } catch {
    res.writeHead(404);
    res.end();
  }
});
server.listen(0, '127.0.0.1', async () => {
  const browser = await chromium.launch();
  const logs = [];
  try {
    const page = await browser.newPage();
    page.on('console', (m) => logs.push(m.type() + ': ' + m.text()));
    page.on('pageerror', (e) => logs.push('pageerror: ' + e.message));
    await page.goto('http://127.0.0.1:' + server.address().port + '/');
    await page.waitForSelector('button', { timeout: 5000 });
    const texts = () => page.$$eval('button', (els) => els.map((el) => el.textContent));
    const before = await texts();
    for (const button of await page.$$('button')) await button.click();
    await page.waitForTimeout(100);
    console.log(JSON.stringify({ before, after: await texts(), logs }));
  } catch (error) {
    console.log(JSON.stringify({ error: String(error), logs }));
  } finally {
    await browser.close();
    server.close();
  }
});
