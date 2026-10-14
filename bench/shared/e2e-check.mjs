#!/usr/bin/env node
// Browser check of one benchmark application (task 058): every page loads
// without script errors, and the interactive behaviour of bench/README.md
// works after hydration. Usage: node shared/e2e-check.mjs http://localhost:8081
//
// Writes are made against the seeded database (a moved card, a comment, a
// small attachment); reseed before measuring if exact data matters.

import { chromium } from 'playwright';

const base = process.argv[2] || 'http://localhost:8081';
const browser = await chromium.launch();
const page = await browser.newPage();
const errors = [];
page.on('pageerror', (e) => errors.push(String(e)));
page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });

const results = [];
async function check(name, run) {
  const before = errors.length;
  try {
    await run();
    if (errors.length > before) throw new Error(errors.slice(before).join('; '));
    results.push(`ok    ${name}`);
  } catch (e) {
    results.push(`FAIL  ${name}: ${e.message.split('\n')[0]}`);
  }
}
const settle = () => page.waitForLoadState('networkidle');

await check('board: hydrates, filters, moves a card', async () => {
  await page.goto(`${base}/projects/1/board`);
  await settle();
  const columns = page.locator('.column');
  if ((await columns.count()) !== 4) throw new Error('expected 4 columns');
  const all = await page.locator('.card').count();
  await page.fill('input[type=search]', 'fix');
  await page.waitForFunction((n) => document.querySelectorAll('.card').length < n, all);
  await page.fill('input[type=search]', '');
  await page.waitForFunction((n) => document.querySelectorAll('.card').length === n, all);
  const first = columns.nth(0).locator('.card').first();
  const key = await first.locator('.key').textContent();
  const patch = page.waitForResponse((r) => r.url().includes('/api/issues/') && r.request().method() === 'PATCH');
  await first.dragTo(columns.nth(1));
  const response = await patch;
  if (!response.ok()) throw new Error(`PATCH ${response.status()}`);
  const moved = await columns.nth(1).locator('.key', { hasText: key }).count();
  if (moved !== 1) throw new Error(`${key} not in the second column`);
});

await check('render fixture: hydrates', async () => {
  await page.goto(`${base}/render/board?n=200`);
  await settle();
  const n = await page.locator('.card').count();
  if (n !== 200) throw new Error(`expected 200 cards, got ${n}`);
});

await check('issues: filters fetch new rows', async () => {
  await page.goto(`${base}/issues`);
  await settle();
  const response = page.waitForResponse((r) => r.url().includes('/api/issues?'));
  await page.locator('.toolbar select').nth(1).selectOption('done');
  await response;
  await page.waitForFunction(() => [...document.querySelectorAll('tbody .status')].every((s) => s.textContent === 'Done'));
  if (!page.url().includes('status=done')) throw new Error('URL not updated');
  const next = page.waitForResponse((r) => r.url().includes('page=2'));
  await page.click('.pager button:last-child');
  await next;
  await page.waitForFunction(() => document.querySelector('.pager span').textContent.startsWith('Page 2'));
});

await check('issue: edits title, comments, uploads', async () => {
  await page.goto(`${base}/issues/42`);
  await settle();
  await page.click('h1 .edit');
  const title = `Edited ${Date.now()}`;
  await page.fill('.title-input', title);
  const patch = page.waitForResponse((r) => r.request().method() === 'PATCH');
  await page.press('.title-input', 'Enter');
  if (!(await patch).ok()) throw new Error('title PATCH failed');
  await page.waitForFunction((t) => document.querySelector('h1').textContent.startsWith(t), title);
  const count = await page.locator('.comment').count();
  await page.fill('.comment-form textarea', 'A comment from the browser check');
  await page.click('.comment-form button');
  await page.waitForFunction((n) => document.querySelectorAll('.comment').length === n + 1, count);
  const files = await page.locator('.attachments li').count();
  await page.setInputFiles('input[type=file]', { name: 'note.txt', mimeType: 'text/plain', buffer: Buffer.from('hello from e2e\n') });
  await page.waitForFunction((n) => document.querySelectorAll('.attachments li').length === n + 1, files);
  const href = await page.locator('.attachments li a').last().getAttribute('href');
  const body = await (await page.request.get(base + href)).text();
  if (body !== 'hello from e2e\n') throw new Error('downloaded file differs');
});

await check('dashboard: renders', async () => {
  await page.goto(`${base}/dashboard`);
  await settle();
  if ((await page.locator('.stat').count()) !== 4) throw new Error('expected 4 stats');
});

await browser.close();
console.log(results.join('\n'));
process.exit(results.some((r) => r.startsWith('FAIL')) ? 1 : 0);
