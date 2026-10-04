// Differential check of the two CSR DOM engines (task 039).
//
// Usage: node tools/engine-diff.mjs <jsdom api.js> <legacy dir> <blocks dir> <cases.json>
//
// cases.json: [{ "module": "Name.js", "export": "Name", "props": {...},
//                "steps": [{ "set": {...} } | { "click": "css" } |
//                          { "input": "css", "value": "text" } |
//                          { "check": "css", "checked": true } |
//                          { "destroy": true }] }]
// Each case mounts the component compiled by both engines into its own jsdom
// document, applies the same steps and compares the comment-free HTML after
// every step. Prints {"ok":bool,"failures":[...]}.
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';

const [jsdomPath, legacyDir, blocksDir, casesPath] = process.argv.slice(2);
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

const failures = [];
for (const testCase of cases) {
  const name = `${testCase.module} ${JSON.stringify(testCase.props || {})}`;
  try {
    const legacy = await run(legacyDir, testCase);
    const blocks = await run(blocksDir, testCase);
    for (let i = 0; i < legacy.length; i++) {
      if (legacy[i] !== blocks[i]) {
        failures.push(`${name} step ${i}:\n  legacy: ${legacy[i]}\n  blocks: ${blocks[i]}`);
        break;
      }
    }
  } catch (error) {
    failures.push(`${name}: ${error.stack || error}`);
  }
}
console.log(JSON.stringify({ ok: failures.length === 0, total: cases.length, failures }, null, 1));
