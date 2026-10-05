#!/usr/bin/env node
// Hydrates server HTML with a block-engine client module and applies update
// steps (task 041).
//
// Usage: node tools/ssr-hydrate-run.mjs <jsdom api.js> <client dir> <module.js> <export> <html file> [steps json] [policy]
//
// Prints {"ok":bool,"error":string,"snapshots":[html after hydrate, after
// each step]} where the HTML is the container's innerHTML without comments.
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';

const [jsdomPath, clientDir, modulePath, exportName, htmlPath, stepsJSON, policy] = process.argv.slice(2);
const { JSDOM } = await import(pathToFileURL(jsdomPath).href);
const dom = new JSDOM('<!doctype html><html><head></head><body></body></html>');
for (const name of ['window', 'document', 'Node', 'HTMLElement', 'CustomEvent', 'Event', 'InputEvent']) {
  globalThis[name] = dom.window[name];
}
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));
const html = (el) => el.innerHTML.replace(/<!--[\s\S]*?-->/g, '');

const result = { ok: false, snapshots: [] };
try {
  const module = await import(pathToFileURL(join(clientDir, modulePath)).href);
  const factory = module[exportName] || module.default;
  const { hydrate } = await import(pathToFileURL(join(clientDir, 'runtime-hydrate.js')).href);
  const host = document.createElement('div');
  document.body.appendChild(host);
  host.innerHTML = readFileSync(htmlPath, 'utf8');
  const instance = hydrate(factory, host, policy ? { policy } : {});
  result.snapshots.push(html(host));
  for (const step of JSON.parse(stepsJSON || '[]')) {
    if (step.set) instance.set(step.set);
    if (step.click) host.querySelector(step.click).click();
    if (step.input) {
      const input = host.querySelector(step.input);
      input.value = step.value;
      input.dispatchEvent(new Event('input', { bubbles: true }));
    }
    await settle();
    result.snapshots.push(html(host));
  }
  instance.destroy();
  result.ok = true;
} catch (error) {
  result.error = String(error && error.stack || error);
}
console.log(JSON.stringify(result));
