// DOM snapshot check of the block engine against recorded snapshots
// (tasks 039, 042).
//
// Usage:
//   node tools/engine-diff.mjs <jsdom api.js> <build dir> <cases.json> <expected.json>
//   node tools/engine-diff.mjs <jsdom api.js> <build dir> <cases.json> --record <expected.json>
//
// cases.json: [{ "module": "Name.js", "export": "Name", "props": {...},
//                "steps": [{ "set": {...} } | { "click": "css" } |
//                          { "input": "css", "value": "text" } |
//                          { "check": "css", "checked": true } |
//                          { "destroy": true }] }]
// Each case mounts the compiled component into its own host, applies the steps
// and records the comment-free HTML after every step. expected.json holds one
// snapshot list per case; it was recorded from the legacy DOM engine before
// task 042 removed it, so the block engine keeps its observable DOM. The check
// prints {"ok":bool,"total":n,"failures":[...]}.
import { readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';

const [jsdomPath, buildDir, casesPath, ...rest] = process.argv.slice(2);
const record = rest[0] === '--record';
const expectedPath = record ? rest[1] : rest[0];
const { JSDOM } = await import(pathToFileURL(jsdomPath).href);
const cases = JSON.parse(readFileSync(casesPath, 'utf8'));

const dom = new JSDOM('<!doctype html><body></body>');
for (const name of ['window', 'document', 'Node', 'HTMLElement', 'CustomEvent', 'Event', 'InputEvent']) {
  globalThis[name] = dom.window[name];
}
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));
const html = (el) => el.innerHTML.replace(/<!--[\s\S]*?-->/g, '');

async function run(dir, testCase) {
  const host = document.createElement('div');
  document.body.appendChild(host);
  const module = await import(pathToFileURL(join(dir, testCase.module)).href);
  const factory = module[testCase.export] || module.default;
  const instance = factory(host, structuredClone(testCase.props || {}));
  await settle();
  const snapshots = [html(host)];
  for (const step of testCase.steps || []) {
    if (step.set) instance.set(structuredClone(step.set));
    if (step.click) host.querySelector(step.click).click();
    if (step.input) {
      const input = host.querySelector(step.input);
      input.value = step.value;
      input.dispatchEvent(new Event('input', { bubbles: true }));
    }
    if (step.check) {
      const input = host.querySelector(step.check);
      input.checked = step.checked;
      input.dispatchEvent(new Event('change', { bubbles: true }));
    }
    if (step.destroy) instance.destroy();
    await settle();
    snapshots.push(html(host));
  }
  host.remove();
  return snapshots;
}

const caseName = (testCase) => `${testCase.module} ${JSON.stringify(testCase.props || {})}`;

if (record) {
  const recorded = [];
  for (const testCase of cases) {
    recorded.push({ case: caseName(testCase), snapshots: await run(buildDir, testCase) });
  }
  writeFileSync(expectedPath, JSON.stringify(recorded, null, 1) + '\n');
  console.log(JSON.stringify({ ok: true, total: cases.length, recorded: expectedPath }));
} else {
  const expected = JSON.parse(readFileSync(expectedPath, 'utf8'));
  const failures = [];
  if (expected.length !== cases.length) {
    failures.push(`expected.json has ${expected.length} cases, cases.json has ${cases.length}`);
  }
  for (let c = 0; c < cases.length && c < expected.length; c++) {
    const testCase = cases[c];
    const name = caseName(testCase);
    try {
      if (expected[c].case !== name) {
        failures.push(`case ${c}: expected.json records ${expected[c].case}, cases.json has ${name}`);
        continue;
      }
      const want = expected[c].snapshots;
      const got = await run(buildDir, testCase);
      if (want.length !== got.length) {
        failures.push(`${name}: ${got.length} snapshots, expected ${want.length}`);
        continue;
      }
      for (let i = 0; i < want.length; i++) {
        if (want[i] !== got[i]) {
          failures.push(`${name} step ${i}:\n  expected: ${want[i]}\n  actual:   ${got[i]}`);
          break;
        }
      }
    } catch (error) {
      failures.push(`${name}: ${error.stack || error}`);
    }
  }
  console.log(JSON.stringify({ ok: failures.length === 0, total: cases.length, failures }, null, 1));
}
