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
  if (JSON.stringify(components) !== JSON.stringify(testCase.entries)) {
    errors.push(`state entries ${JSON.stringify(components)}, want ${JSON.stringify(testCase.entries)}`);
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
  const html = serialize(host);
  instance.destroy();
  host.remove();
  return html;
}

const failures = [];
for (const testCase of cases) {
  try {
    const errors = checkCase(testCase);
    if (testCase.compare) {
      const server = parse(testCase.html);
      server.querySelector('script[data-vela-root]').remove();
      const serverHTML = serialize(server);
      const client = await clientHTML(testCase);
      if (serverHTML !== client) errors.push(`server and client DOM differ:\n  server: ${serverHTML}\n  client: ${client}`);
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
