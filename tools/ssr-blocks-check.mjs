// Checks block-engine server output against the client DOM (task 040).
//
// Usage: node tools/ssr-blocks-check.mjs <jsdom api.js> <client dir> <rendered.json>
//
// rendered.json: [{ "name", "module", "export", "props", "html", "static",
//                   "compare": bool, "entries": [component names in order] }]
// For every case the server HTML is parsed as the body of a document, then:
//  - the markup follows §5.1 of docs/HYDRATION_ARCHITECTURE.md: the content
//    is one root range followed by one payload script; every range opens and
//    closes among the same siblings; state tokens belong to the grammar; no
//    other comments, element markers or instance identifiers appear;
//  - the payload stream has version 2, the root props, and "s" entries for
//    the expected components in render order;
//  - the static rendering equals the comment-free interactive rendering;
//  - when compare is set, the comment-free server DOM equals the DOM the
//    block-engine client module creates for the same props.
// Prints {"ok":bool,"total":n,"failures":[...]}.
import { pathToFileURL } from 'node:url';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

const [jsdomPath, clientDir, renderedPath] = process.argv.slice(2);
const { JSDOM } = await import(pathToFileURL(jsdomPath).href);
const cases = JSON.parse(readFileSync(renderedPath, 'utf8'));

// Root modes of the client manifest (A-8.1): a static root renders plain
// HTML without markers or payload and is not hydrated.
const manifest = JSON.parse(readFileSync(join(clientDir, 'vela.manifest.json'), 'utf8'));
const rootMode = (component) => (manifest.components.find((c) => c.component === component) || {}).mode;

const dom = new JSDOM('<!doctype html><body></body>');
for (const name of ['window', 'document', 'Node', 'HTMLElement', 'CustomEvent', 'Event', 'InputEvent']) {
  globalThis[name] = dom.window[name];
}

const COMMENT = 8;
const ELEMENT = 1;
const stateToken = /^(|\d+|x|e|p|t|c|f)$/;
const stripComments = (html) => html.replace(/<!--[\s\S]*?-->/g, '');

// serialize writes the DOM with form state taken from properties: the
// server writes value, checked, and selected attributes where the client
// sets properties. Adjacent text nodes are merged, as the HTML parser merges
// them; comments are written only when comments is set. The function is
// self-contained so that it also runs inside Chromium.
function serialize(node, comments = false) {
  const formAttrs = ['value', 'checked', 'selected'];
  let out = '';
  let text = null;
  const flush = () => {
    // An empty text run is not present in parsed HTML (C-4.3).
    if (text) out += JSON.stringify(text);
    text = null;
  };
  for (const child of node.childNodes) {
    if (child.nodeType === 3) {
      text = (text ?? '') + child.data;
      continue;
    }
    if (child.nodeType === 8 && comments) {
      flush();
      out += `<!--${child.data}-->`;
      continue;
    }
    if (child.nodeType !== 1) continue;
    flush();
    const tag = child.localName;
    const attrs = [...child.attributes]
      .filter((attr) => !formAttrs.includes(attr.name))
      .map((attr) => `${attr.name}=${JSON.stringify(attr.name === 'style' ? child.style.cssText : attr.value)}`)
      .sort();
    if (tag === 'input') attrs.push(`.value=${JSON.stringify(child.value)}`, `.checked=${child.checked}`);
    if (tag === 'select' || tag === 'textarea') attrs.push(`.value=${JSON.stringify(child.value)}`);
    if (tag === 'option') attrs.push(`.selected=${child.selected}`);
    out += `<${tag}${attrs.length ? ' ' + attrs.join(' ') : ''}>`;
    if (tag !== 'textarea' && tag !== 'script') out += serialize(child, comments);
    out += `</${tag}>`;
  }
  flush();
  return out;
}

// parse parses server HTML as the body of a document, as a browser does.
function parse(html) {
  return new JSDOM(`<!doctype html><html><head></head><body>${html}</body></html>`).window.document.body;
}

// checkRanges validates the ranges among the children of parent and
// recurses into elements. Returns the number of ranges.
function checkRanges(parent, errors, path) {
  let depth = 0;
  let count = 0;
  for (const node of parent.childNodes) {
    if (node.nodeType === COMMENT) {
      const text = node.data;
      if (text.startsWith('[')) {
        if (!stateToken.test(text.slice(1))) errors.push(`${path}: invalid state token <!--${text}-->`);
        depth++;
        count++;
      } else if (text === ']') {
        if (depth === 0) errors.push(`${path}: unmatched <!--]-->`);
        else depth--;
      } else {
        errors.push(`${path}: non-marker comment <!--${text}-->`);
      }
    } else if (node.nodeType === ELEMENT) {
      for (const attr of node.attributes) {
        if (attr.name.startsWith('data-vela')) errors.push(`${path}: instance attribute ${attr.name}`);
      }
      count += checkRanges(node, errors, `${path} > ${node.localName}`);
    }
  }
  if (depth !== 0) errors.push(`${path}: ${depth} unclosed range(s)`);
  return count;
}

function checkCase(testCase) {
  const errors = [];
  const host = parse(testCase.html);
  const nodes = [...host.childNodes];
  const script = nodes[nodes.length - 1];
  if (!script || script.nodeType !== ELEMENT || script.localName !== 'script' || !script.hasAttribute('data-vela-root')) {
    errors.push('the root range is not followed by its payload script');
    return errors;
  }
  if (host.querySelectorAll('script[data-vela-root]').length !== 1) errors.push('more than one payload script');
  if (nodes[0].nodeType !== COMMENT || nodes[0].data !== '[' || nodes[nodes.length - 2].nodeType !== COMMENT || nodes[nodes.length - 2].data !== ']') {
    errors.push('the root is not one range');
  }
  script.remove();
  checkRanges(host, errors, testCase.name);

  let payload;
  try {
    payload = JSON.parse(script.textContent);
  } catch (error) {
    errors.push(`payload is not JSON: ${error.message}`);
    return errors;
  }
  if (payload.v !== 2) errors.push(`payload version ${payload.v}`);
  for (const [name, value] of Object.entries(testCase.props)) {
    if (JSON.stringify(payload.props?.[name]) !== JSON.stringify(value)) {
      errors.push(`payload prop ${name} = ${JSON.stringify(payload.props?.[name])}`);
    }
  }
  const components = payload.entries.filter((entry) => entry[0] === 's').map((entry) => entry[1]);
  if (testCase.entries && JSON.stringify(components) !== JSON.stringify(testCase.entries)) {
    errors.push(`state entries ${JSON.stringify(components)}, want ${JSON.stringify(testCase.entries)}`);
  }
  if (testCase.states) {
    const states = payload.entries.filter((entry) => entry[0] === "s").map((entry) => entry[2]);
    testCase.states.forEach((want, i) => {
      if (JSON.stringify(states[i]) !== JSON.stringify(want)) errors.push(`state entry ${i} = ${JSON.stringify(states[i])}, want ${JSON.stringify(want)}`);
    });
  }
  if (testCase.awaits) {
    const awaits = payload.entries.filter((entry) => entry[0] === 'a').map((entry) => [entry[1], entry[2].state]);
    if (JSON.stringify(awaits) !== JSON.stringify(testCase.awaits)) errors.push(`await entries ${JSON.stringify(awaits)}`);
  }

  const serverHTML = serialize(host);
  const staticHTML = serialize(parse(testCase.static));
  if (/<!--|<script/.test(testCase.static)) errors.push("static rendering has markers or a payload script");
  if (staticHTML !== serverHTML) errors.push(`static rendering differs:\n  static: ${staticHTML}\n  server: ${serverHTML}`);
  return errors;
}

async function clientHTML(testCase) {
  const host = document.createElement('div');
  document.body.appendChild(host);
  const module = await import(pathToFileURL(join(clientDir, testCase.module)).href);
  const factory = module[testCase.export] || module.default;
  const instance = factory(host, structuredClone(testCase.props));
  // Promises settle before the comparison: the server renders the settled
  // branch of an evaluated {#await} (T-7.4c, T-7.4f).
  await settle();
  const html = serialize(host);
  instance.destroy();
  host.remove();
  return html;
}


// ── Hydration (task 041, Q-10.2 items 1–4) ───────────────────────────────
// For a compare case the server HTML is hydrated with runtime-hydrate.js:
// the result must equal the client DOM, hydration may only insert empty
// text nodes (C-4.3) and remove the payload script (S-6.9), the server
// elements must keep their identity, and every update step must leave the
// hydrated and the created instance equal.
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

function applyStep(host, instance, step) {
  if (step.set) instance.set(structuredClone(step.set));
  // A missing target is skipped; both DOMs are equal before every step.
  const clicked = step.click && host.querySelector(step.click);
  if (clicked) clicked.click();
  const input = step.input && host.querySelector(step.input);
  if (input) {
    input.value = step.value;
    input.dispatchEvent(new Event('input', { bubbles: true }));
  }
}

async function hydrateCheck(testCase) {
  const errors = [];
  const module = await import(pathToFileURL(join(clientDir, testCase.module)).href);
  const factory = module[testCase.export] || module.default;
  const { hydrate } = await import(pathToFileURL(join(clientDir, 'runtime-hydrate.js')).href);
  const reference = document.createElement('div');
  document.body.appendChild(reference);
  const created = factory(reference, structuredClone(testCase.props));
  await settle();
  const host = document.createElement('div');
  document.body.appendChild(host);
  host.innerHTML = testCase.html;
  const serverElements = [...host.querySelectorAll('*')].filter((el) => el.localName !== 'script');
  const observer = new dom.window.MutationObserver(() => {});
  observer.observe(host, { subtree: true, childList: true, characterData: true, attributes: true, attributeOldValue: true });
  let instance;
  try {
    instance = hydrate(factory, host);
  } catch (error) {
    observer.disconnect();
    errors.push(`hydrate: ${error.message}`);
    created.destroy();
    host.remove();
    reference.remove();
    return errors;
  }
  for (const record of observer.takeRecords()) {
    const emptyText = record.type === 'childList' && record.removedNodes.length === 0 &&
      [...record.addedNodes].every((node) => node.nodeType === 3 && node.data === '');
    const payload = record.type === 'childList' && record.addedNodes.length === 0 &&
      [...record.removedNodes].every((node) => node.localName === 'script');
    // Development value verification re-runs initial writes; an attribute
    // write of the same value changes nothing.
    const unchanged = record.type === 'attributes' && record.oldValue === record.target.getAttribute(record.attributeName);
    if (!emptyText && !payload && !unchanged) errors.push(`hydration mutated the DOM: ${record.type} on <${record.target.localName || record.target.nodeName}>`);
  }
  observer.disconnect();
  for (const element of serverElements) {
    if (!host.contains(element)) errors.push(`hydration replaced the server <${element.localName}>`);
  }
  if (serialize(host) !== serialize(reference)) {
    errors.push(`hydrated DOM differs from the client DOM:\n  hydrated: ${serialize(host)}\n  client:   ${serialize(reference)}`);
  }
  for (const [index, step] of (testCase.steps || []).entries()) {
    applyStep(host, instance, step);
    applyStep(reference, created, step);
    await settle();
    if (serialize(host) !== serialize(reference)) {
      errors.push(`after step ${index} ${JSON.stringify(step)}:\n  hydrated: ${serialize(host)}\n  client:   ${serialize(reference)}`);
      break;
    }
  }
  instance.destroy();
  created.destroy();
  if (host.querySelector('*')) errors.push(`destroy() left elements: ${serialize(host)}`);
  host.remove();
  reference.remove();
  return errors;
}

// ── Mismatch suite (task 041, Q-10.3) ────────────────────────────────────
// Each injected mismatch is hydrated under every policy (§7.3): repair and
// production converge to the client DOM (production does not verify
// values), fail throws and leaves the DOM untouched, and a retry after the
// failure yields exactly one live instance.
async function mismatchCheck(testCase, injected) {
  const errors = [];
  const module = await import(pathToFileURL(join(clientDir, testCase.module)).href);
  const factory = module[testCase.export] || module.default;
  const { hydrate } = await import(pathToFileURL(join(clientDir, 'runtime-hydrate.js')).href);
  const html = injected.regex ? testCase.html.replace(new RegExp(injected.from), injected.to) : testCase.html.replace(injected.from, injected.to);
  if (html === testCase.html) return [`${injected.name}: the injection does not apply`];
  const label = (policy) => `${injected.name} (${policy})`;
  const silenced = (run) => {
    const saved = { warn: console.warn, error: console.error };
    const counts = { warn: 0, error: 0 };
    console.warn = () => { counts.warn++; };
    console.error = () => { counts.error++; };
    try {
      return [run(), counts];
    } finally {
      Object.assign(console, saved);
    }
  };
  for (const policy of ['repair', 'fail', 'production']) {
    const reference = document.createElement('div');
    document.body.appendChild(reference);
    const created = factory(reference, structuredClone(testCase.props));
    await settle();
    const host = document.createElement('div');
    document.body.appendChild(host);
    host.innerHTML = html;
    const parsed = host.innerHTML;
    const reports = [];
    let instance = null;
    let thrown = null;
    const [, counts] = silenced(() => {
      try {
        instance = hydrate(factory, host, { policy, onMismatch: (report) => reports.push(report) });
      } catch (error) {
        thrown = error;
      }
    });
    const valueOnly = injected.kind === 'value';
    if (policy === 'fail') {
      if (!thrown || thrown.code !== 'VELA-HYDRATION-MISMATCH') errors.push(`${label(policy)}: expected VELA-HYDRATION-MISMATCH, got ${thrown ? thrown.message : 'no error'}`);
      if (host.innerHTML !== parsed) errors.push(`${label(policy)}: the rollback changed the DOM`);
      silenced(() => {
        try {
          instance = hydrate(factory, host, { policy: 'repair' });
        } catch (error) {
          errors.push(`${label(policy)}: retry failed: ${error.message}`);
        }
      });
    } else {
      if (thrown) errors.push(`${label(policy)}: threw ${thrown.message}`);
      const verified = !(policy === 'production' && valueOnly);
      if (verified && !reports.some((report) => report.kind === injected.kind)) errors.push(`${label(policy)}: onMismatch got ${JSON.stringify(reports.map((r) => r.kind))}, want a ${injected.kind} report`);
      if (!verified && reports.length) errors.push(`${label(policy)}: production verified a value`);
      if (policy === 'repair' && counts.warn === 0) errors.push(`${label(policy)}: no console warning`);
      if (policy === 'production' && verified && counts.error !== 1) errors.push(`${label(policy)}: console.error was called ${counts.error} times, want 1`);
    }
    // A repaired range rebuilds its blocks, whose promises settle later.
    await settle();
    if (instance && !(policy === 'production' && valueOnly)) {
      if (serialize(host) !== serialize(reference)) {
        errors.push(`${label(policy)}: DOM differs from the client:\n  hydrated: ${serialize(host)}\n  client:   ${serialize(reference)}`);
      } else if (injected.step) {
        applyStep(host, instance, injected.step);
        applyStep(reference, created, injected.step);
        await settle();
        if (serialize(host) !== serialize(reference)) errors.push(`${label(policy)}: after ${JSON.stringify(injected.step)}:\n  hydrated: ${serialize(host)}\n  client:   ${serialize(reference)}`);
      }
    }
    if (instance) instance.destroy();
    created.destroy();
    host.remove();
    reference.remove();
  }
  return errors;
}
const failures = [];
for (const testCase of cases) {
  try {
    if (rootMode(testCase.export) === 'static') {
      const errors = [];
      if (/<!--|<script/.test(testCase.html)) errors.push(`a static root wrote markers or a payload: ${testCase.html}`);
      if (testCase.html !== testCase.static) errors.push('a static root differs from its static rendering');
      const client = testCase.compare ? await clientHTML(testCase) : null;
      if (client !== null && serialize(parse(testCase.html)) !== client) errors.push(`static root and client DOM differ:\n  server: ${serialize(parse(testCase.html))}\n  client: ${client}`);
      if (errors.length) failures.push(`${testCase.name}:\n  ${errors.join('\n  ')}`);
      continue;
    }
    const errors = checkCase(testCase);
    if (testCase.compare) {
      const server = parse(testCase.html);
      server.querySelector('script[data-vela-root]').remove();
      const serverHTML = serialize(server);
      const client = await clientHTML(testCase);
      if (serverHTML !== client) errors.push(`server and client DOM differ:\n  server: ${serverHTML}\n  client: ${client}`);
      errors.push(...(await hydrateCheck(testCase)));
      for (const injected of testCase.mismatches || []) errors.push(...(await mismatchCheck(testCase, injected)));
    }
    if (errors.length) failures.push(`${testCase.name}:\n  ${errors.join('\n  ')}`);
  } catch (error) {
    failures.push(`${testCase.name}: ${error.stack || error}`);
  }
}
// With --chromium, Chromium parses every server document; its node
// sequence, markers included, must equal the jsdom parse checked above.
if (process.argv.includes('--chromium')) {
  const { chromium } = await import('playwright');
  const browser = await chromium.launch({ headless: true });
  try {
    const page = await browser.newPage();
    for (const testCase of cases) {
      for (const [kind, html] of [['interactive', testCase.html], ['static', testCase.static]]) {
        await page.setContent(`<!doctype html><html><head></head><body>${html}</body></html>`);
        const parsed = await page.evaluate((source) => (0, eval)(`(${source})`)(document.body, true), serialize.toString());
        const expected = serialize(parse(html), true);
        if (parsed !== expected) failures.push(`${testCase.name} (${kind}): Chromium parse differs:\n  chromium: ${parsed}\n  jsdom:    ${expected}`);
      }
    }
  } finally {
    await browser.close();
  }
}

console.log(JSON.stringify({ ok: failures.length === 0, total: cases.length, failures }, null, 1));
