#!/usr/bin/env node
// Parity check of the two benchmark applications (task 058): the same reads
// must give the same API JSON and the same visible page text, and the page
// sizes are recorded. Both apps must use the same database, with no writes
// during the check.
//   node shared/parity-check.mjs http://localhost:8081 http://localhost:8082

import { JSDOM } from 'jsdom';

const [a, b] = process.argv.slice(2);
if (!a || !b) {
  console.error('usage: parity-check.mjs <vela base> <sveltekit base>');
  process.exit(2);
}

const pages = ['/projects/1/board', '/projects/7/board', '/render/board?n=500', '/issues', '/issues?project=3&status=todo&q=fix&sort=priority',
  '/issues?project=5&sort=number&page=4', '/issues/42', '/issues/12345', '/dashboard'];
const apis = ['/api/issues', '/api/issues?project=2&sort=priority&page=3', '/api/issues?project=9&status=done&q=log', '/api/issues?q=zzzz'];

// Visible text with form control values.
function visibleText(html) {
  const { document } = new JSDOM(html).window;
  const root = document.querySelector('#app');
  root.querySelectorAll('script').forEach((s) => s.remove());
  root.querySelectorAll('input, textarea, select').forEach((el) => el.replaceWith(`[${el.value ?? ''}]`));
  // Whitespace is ignored: Vela drops whitespace-only text between elements,
  // Svelte keeps one space.
  return root.textContent.replace(/\s+/g, '');
}

function sortKeys(value) {
  if (Array.isArray(value)) return value.map(sortKeys);
  if (value && typeof value === 'object') return Object.fromEntries(Object.keys(value).sort().map((k) => [k, sortKeys(value[k])]));
  return value;
}

let failures = 0;
const rows = [];
for (const path of pages) {
  const [ra, rb] = await Promise.all([fetch(a + path), fetch(b + path)]);
  const [ha, hb] = await Promise.all([ra.text(), rb.text()]);
  const [ta, tb] = [visibleText(ha), visibleText(hb)];
  const same = ra.status === rb.status && ta === tb;
  if (!same) {
    failures++;
    let i = 0;
    while (i < ta.length && ta[i] === tb[i]) i++;
    console.log(`DIFF  ${path} (status ${ra.status}/${rb.status}) at ${i}\n  vela:      …${ta.slice(Math.max(0, i - 60), i + 60)}\n  sveltekit: …${tb.slice(Math.max(0, i - 60), i + 60)}`);
  }
  rows.push([path, ha.length, hb.length, same ? 'same text' : 'DIFF']);
}
for (const path of apis) {
  const [ja, jb] = await Promise.all([fetch(a + path).then((r) => r.json()), fetch(b + path).then((r) => r.json())]);
  const same = JSON.stringify(sortKeys(ja)) === JSON.stringify(sortKeys(jb));
  if (!same) {
    failures++;
    console.log(`DIFF  ${path}\n  vela:      ${JSON.stringify(sortKeys(ja)).slice(0, 300)}\n  sveltekit: ${JSON.stringify(sortKeys(jb)).slice(0, 300)}`);
  }
  rows.push([path, JSON.stringify(ja).length, JSON.stringify(jb).length, same ? 'same JSON' : 'DIFF']);
}
console.log('\n| Route | Vela bytes | SvelteKit bytes | Result |\n| --- | ---: | ---: | --- |');
for (const r of rows) console.log(`| \`${r[0]}\` | ${r[1]} | ${r[2]} | ${r[3]} |`);
process.exit(failures ? 1 : 0);
