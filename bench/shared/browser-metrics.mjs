#!/usr/bin/env node
// Browser metrics of one benchmark application (task 058), in Chromium.
//
//   node shared/browser-metrics.mjs <base URL> <out.json> [--runs 5] [--profile desktop|mobile]
//
// For every page: --runs cold loads (a new browser context, empty cache) and
// one warm load (the same context again). Measured in the page:
//   ttfb, fcp, lcp, dcl, load      Navigation Timing, Paint Timing, LCP
//   hydrated                       performance.mark('app-hydrated'), set by
//                                  both applications when hydration is done
//   tbt                            long-task time above 50 ms until hydrated
//                                  (or load for a page without JavaScript)
//   htmlBytes, jsBytes             response sizes (decoded bytes)
//   heapMB                         JavaScript heap after hydration
// Interactions, on the render fixture (no database writes) and the list:
//   typeMs     the slowest keyboard interaction while typing in the filter
//   dropMs     the slowest event of a drag and drop between two columns
//   filterMs   choosing a status filter until the rows are updated
// Event durations come from the Event Timing API (the basis of INP).
//
// Profiles: desktop (no throttling) and mobile (CPU 4× slower, 1.6 Mbit/s
// down, 750 kbit/s up, 150 ms round trip, as Lighthouse's mobile preset).

import { chromium } from 'playwright';
import { writeFileSync } from 'node:fs';
import { parseArgs } from 'node:util';

const { values: opts, positionals } = parseArgs({
  allowPositionals: true,
  options: { runs: { type: 'string', default: '5' }, profile: { type: 'string', default: 'desktop' } },
});
const [base, out] = positionals;
if (!base || !out) {
  console.error('usage: browser-metrics.mjs <base URL> <out.json> [--runs N] [--profile desktop|mobile]');
  process.exit(2);
}
const runs = Number(opts.runs);
const mobile = opts.profile === 'mobile';

// js: the page hydrates (the dashboard has no JavaScript in either app).
const PAGES = [
  { name: 'board', path: '/projects/1/board', js: true },
  { name: 'render-200', path: '/render/board?n=200', js: true },
  { name: 'issues', path: '/issues', js: true },
  { name: 'issue', path: '/issues/42', js: true },
  { name: 'dashboard', path: '/dashboard', js: false },
];

// Collects LCP, long tasks and event timings from the start of the page.
const observe = () => {
  const m = (window.__metrics = { lcp: 0, longTasks: [], events: [] });
  new PerformanceObserver((list) => {
    for (const e of list.getEntries()) m.lcp = e.startTime;
  }).observe({ type: 'largest-contentful-paint', buffered: true });
  new PerformanceObserver((list) => {
    for (const e of list.getEntries()) m.longTasks.push([e.startTime, e.duration]);
  }).observe({ type: 'longtask', buffered: true });
  new PerformanceObserver((list) => {
    for (const e of list.getEntries()) m.events.push([e.name, e.duration, e.interactionId || 0, e.startTime]);
  }).observe({ type: 'event', durationThreshold: 16, buffered: true });
};

async function throttle(page) {
  if (!mobile) return null;
  const cdp = await page.context().newCDPSession(page);
  await cdp.send('Network.enable');
  await cdp.send('Network.emulateNetworkConditions', { offline: false, latency: 150, downloadThroughput: (1.6e6 / 8), uploadThroughput: (750e3 / 8) });
  await cdp.send('Emulation.setCPUThrottlingRate', { rate: 4 });
  return cdp;
}

async function heapMB(page) {
  const cdp = await page.context().newCDPSession(page);
  const { usedSize } = await cdp.send('Runtime.getHeapUsage');
  await cdp.detach();
  return usedSize / 1e6;
}

async function load(page, path, js) {
  await page.goto(base + path, { waitUntil: 'load' });
  // The hydration mark of a page with JavaScript; at most 20 s. (SvelteKit
  // starts from an inline script, so the page markup cannot tell.)
  if (js) await page.waitForFunction(() => performance.getEntriesByName('app-hydrated').length > 0, null, { timeout: 20000 });
  await page.waitForTimeout(300); // late LCP and long-task entries
  const metrics = await page.evaluate(() => {
    const nav = performance.getEntriesByType('navigation')[0];
    const fcp = performance.getEntriesByName('first-contentful-paint')[0]?.startTime || 0;
    const hydrated = performance.getEntriesByName('app-hydrated')[0]?.startTime || 0;
    const until = hydrated || nav.loadEventEnd;
    const tbt = window.__metrics.longTasks.filter(([start]) => start < until).reduce((sum, [, d]) => sum + Math.max(0, d - 50), 0);
    const js = performance.getEntriesByType('resource').filter((r) => r.name.endsWith('.js') || r.initiatorType === 'script');
    return {
      ttfb: nav.responseStart - nav.startTime, fcp, lcp: window.__metrics.lcp, dcl: nav.domContentLoadedEventEnd, load: nav.loadEventEnd,
      hydrated, tbt, htmlBytes: nav.decodedBodySize, jsBytes: js.reduce((s, r) => s + (r.decodedBodySize || 0), 0), jsFiles: js.length,
    };
  });
  metrics.heapMB = await heapMB(page);
  return metrics;
}

const slowest = (events, names) => Math.max(0, ...events.filter(([name]) => names.includes(name)).map(([, d]) => d));

async function interactions(page) {
  const result = {};
  await page.goto(`${base}/render/board?n=200`, { waitUntil: 'load' });
  await page.waitForFunction(() => performance.getEntriesByName('app-hydrated').length > 0, null, { timeout: 20000 });
  await page.evaluate(() => (window.__metrics.events = []));
  await page.click('input[type=search]');
  await page.keyboard.type('fix the', { delay: 80 });
  await page.waitForTimeout(300);
  result.typeMs = slowest(await page.evaluate(() => window.__metrics.events), ['keydown', 'keypress', 'keyup', 'input', 'beforeinput']);
  await page.fill('input[type=search]', '');
  await page.waitForTimeout(300);
  await page.evaluate(() => (window.__metrics.events = []));
  const columns = page.locator('.column');
  await columns.nth(0).locator('.card').first().dragTo(columns.nth(1));
  await page.waitForTimeout(300);
  result.dropMs = slowest(await page.evaluate(() => window.__metrics.events), ['drop', 'dragend', 'dragstart', 'dragover', 'pointerup', 'pointerdown', 'mouseup', 'mousedown']);

  await page.goto(`${base}/issues`, { waitUntil: 'load' });
  await page.waitForFunction(() => performance.getEntriesByName('app-hydrated').length > 0, null, { timeout: 20000 });
  const started = Date.now();
  await page.locator('.toolbar select').nth(1).selectOption('done');
  await page.waitForFunction(() => {
    const cells = [...document.querySelectorAll('tbody .status')];
    return cells.length > 0 && cells.every((c) => c.textContent === 'Done');
  }, null, { timeout: 20000 });
  result.filterMs = Date.now() - started;
  return result;
}

const median = (xs) => {
  const s = [...xs].sort((a, b) => a - b);
  return s.length ? s[Math.floor(s.length / 2)] : 0;
};

const browser = await chromium.launch();
const results = { base, profile: opts.profile, runs, pages: {}, interactions: [] };
try {
  for (const p of PAGES) {
    const cold = [];
    let warm = null;
    for (let i = 0; i < runs; i++) {
      const context = await browser.newContext();
      await context.addInitScript(observe);
      const page = await context.newPage();
      await throttle(page);
      cold.push(await load(page, p.path, p.js));
      if (i === 0) warm = await load(page, p.path, p.js);
      await context.close();
    }
    const keys = Object.keys(cold[0]);
    results.pages[p.name] = { cold: Object.fromEntries(keys.map((k) => [k, median(cold.map((m) => m[k]))])), warm };
  }
  for (let i = 0; i < runs; i++) {
    const context = await browser.newContext();
    await context.addInitScript(observe);
    const page = await context.newPage();
    await throttle(page);
    results.interactions.push(await interactions(page));
    await context.close();
  }
  results.interactionsMedian = Object.fromEntries(Object.keys(results.interactions[0]).map((k) => [k, median(results.interactions.map((r) => r[k]))]));
} finally {
  await browser.close();
}
writeFileSync(out, JSON.stringify(results, null, 2));
console.log(JSON.stringify({ pages: Object.fromEntries(Object.entries(results.pages).map(([k, v]) => [k, v.cold])), interactions: results.interactionsMedian }));
