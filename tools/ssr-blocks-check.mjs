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
import { serialize, clientHTML, hydrateCheck, mismatchCheck } from './ssr-hydration-checks.mjs';

const [jsdomPath, clientDir, renderedPath] = process.argv.slice(2);
const { JSDOM } = await import(pathToFileURL(jsdomPath).href);
const cases = JSON.parse(readFileSync(renderedPath, 'utf8'));

// Root modes of the client manifest (A-8.1): a static root renders plain
// HTML without markers or payload and is not hydrated.
const manifest = JSON.parse(readFileSync(join(clientDir, 'vela.manifest.json'), 'utf8'));
const rootMode = (component) => (manifest.components.find((c) => c.component === component) || {}).mode;

const dom = new JSDOM('<!doctype html><body></body>');
for (const name of ['window', 'document', 'Node', 'HTMLElement', 'CustomEvent', 'Event', 'InputEvent', 'MutationObserver']) {
  globalThis[name] = dom.window[name];
}

const COMMENT = 8;
const ELEMENT = 1;
const stateToken = /^(|\d+|x|e|p|t|c|f)$/;
const stripComments = (html) => html.replace(/<!--[\s\S]*?-->/g, '');

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

const env = { load: (path) => import(pathToFileURL(join(clientDir, path)).href) };
const failures = [];
for (const testCase of cases) {
  try {
    if (rootMode(testCase.export) === 'static') {
      const errors = [];
      if (/<!--|<script/.test(testCase.html)) errors.push(`a static root wrote markers or a payload: ${testCase.html}`);
      if (testCase.html !== testCase.static) errors.push('a static root differs from its static rendering');
      const client = testCase.compare ? await clientHTML(env, testCase) : null;
      if (client !== null && serialize(parse(testCase.html)) !== client) errors.push(`static root and client DOM differ:\n  server: ${serialize(parse(testCase.html))}\n  client: ${client}`);
      if (errors.length) failures.push(`${testCase.name}:\n  ${errors.join('\n  ')}`);
      continue;
    }
    const errors = checkCase(testCase);
    if (testCase.compare) {
      const server = parse(testCase.html);
      server.querySelector('script[data-vela-root]').remove();
      const serverHTML = serialize(server);
      const client = await clientHTML(env, testCase);
      if (testCase.boundary) {
        // {#client}: the server writes the fallback, the client the main
        // branch (contract §9.1); hydration must reach the client DOM.
        for (const selector of testCase.boundary.server || []) if (!server.querySelector(selector)) errors.push(`server HTML lacks the fallback ${selector}`);
        for (const selector of testCase.boundary.client || []) if (server.querySelector(selector)) errors.push(`server HTML contains client-only content ${selector}`);
      } else if (serverHTML !== client) errors.push(`server and client DOM differ:\n  server: ${serverHTML}\n  client: ${client}`);
      errors.push(...(await hydrateCheck(env, testCase)));
      for (const injected of testCase.mismatches || []) errors.push(...(await mismatchCheck(env, testCase, injected)));
    }
    if (errors.length) failures.push(`${testCase.name}:\n  ${errors.join('\n  ')}`);
  } catch (error) {
    failures.push(`${testCase.name}: ${error.stack || error}`);
  }
}
let chromiumHydrated = 0;
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
    // Hydration in Chromium (Q-10.2): the same checks as in jsdom, run in a
    // page that loads the client modules from a local server.
    const { createServer } = await import('node:http');
    const types = { '.js': 'text/javascript', '.mjs': 'text/javascript', '.json': 'application/json' };
    const server = createServer((request, response) => {
      const url = new URL(request.url, 'http://localhost');
      let file = null;
      if (url.pathname === '/checks.mjs') file = new URL('./ssr-hydration-checks.mjs', import.meta.url);
      else if (url.pathname.startsWith('/client/')) file = pathToFileURL(join(clientDir, decodeURIComponent(url.pathname.slice('/client/'.length))));
      if (!file) {
        response.writeHead(200, { 'content-type': 'text/html' });
        response.end('<!doctype html><html><head></head><body></body></html>');
        return;
      }
      try {
        const body = readFileSync(file);
        const ext = file.pathname.slice(file.pathname.lastIndexOf('.'));
        response.writeHead(200, { 'content-type': types[ext] || 'application/octet-stream' });
        response.end(body);
      } catch {
        response.writeHead(404);
        response.end();
      }
    });
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
    try {
      const page = await browser.newPage();
      page.on('pageerror', (error) => failures.push(`Chromium page error: ${error.message}`));
      await page.goto(`http://127.0.0.1:${server.address().port}/`);
      const hydrated = cases.filter((testCase) => testCase.compare && rootMode(testCase.export) !== 'static');
      const results = await page.evaluate(async (list) => {
        const checks = await import('/checks.mjs');
        const env = { load: (path) => import(`/client/${path}`) };
        const out = [];
        for (const testCase of list) {
          const errors = [];
          try {
            errors.push(...(await checks.hydrateCheck(env, testCase)));
            for (const injected of testCase.mismatches || []) errors.push(...(await checks.mismatchCheck(env, testCase, injected)));
          } catch (error) {
            errors.push(String(error && error.stack || error));
          }
          out.push([testCase.name, errors]);
        }
        return out;
      }, hydrated);
      chromiumHydrated = results.length;
      for (const [name, errors] of results) {
        if (errors.length) failures.push(`${name} (Chromium hydration):\n  ${errors.join('\n  ')}`);
      }
    } finally {
      server.close();
    }
  } finally {
    await browser.close();
  }
}

console.log(JSON.stringify({ ok: failures.length === 0, total: cases.length, chromiumHydrated, failures }, null, 1));
