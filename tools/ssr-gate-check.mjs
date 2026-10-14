// Browser part of the SSR beta gate (task 031, TestSSRGate).
//
// Usage: node tools/ssr-gate-check.mjs <base URL> <components JSON>
// components: [{ "id": "dir/App.vela", "mode": "interactive" | "static" }]
//
// For every component the Go server of the project renders /c/<id>: the
// server HTML in #ssr, an empty #csr, and /gate.js, which hydrates #ssr (an
// interactive root) under the fail policy and mounts the same component in
// #csr from scratch. This script then checks, in real Chromium:
//   - the page renders (HTTP 200) and the scripts raise no error;
//   - hydration reports no mismatch, writes no DOM and keeps every server
//     element (identity);
//   - the hydrated DOM equals the CSR DOM, and stays equal after clicking
//     every button, checkbox and radio input in both, in the same order;
//   - destroying both instances returns the runtime's owner, listener and
//     queue counters to their values before hydration.
// It prints one JSON array of results; "result" is "pass" or the first
// failure.
import { chromium } from 'playwright';

const [base, componentsJSON] = process.argv.slice(2);
const components = JSON.parse(componentsJSON);
const maxClicks = 12;

const browser = await chromium.launch();
const results = [];
try {
  for (const component of components) {
    results.push(await check(component));
  }
} finally {
  await browser.close();
}
console.log(JSON.stringify(results));

async function check({ id, mode }) {
  const page = await browser.newPage();
  const pageErrors = [];
  page.on('pageerror', (e) => pageErrors.push(String(e.message)));
  const fail = (result, extra = {}) => ({ id, result, pageErrors, ...extra });
  try {
    const response = await page.goto(base + '/c/' + encodeURIComponent(id));
    if (!response || response.status() !== 200) {
      const body = response ? (await response.text()).slice(0, 300) : '';
      return fail('render: HTTP ' + (response && response.status()) + ' ' + body);
    }
    await page.waitForFunction(() => window.__gate && window.__gate.done, null, { timeout: 10000 });
    const state = await page.evaluate(() => ({
      errors: window.__gate.errors,
      reports: window.__gate.reports,
      mutations: window.__gate.mutations,
      identity: (window.__gate.elements || []).every((el) => el.isConnected),
      hydrated: !!window.__gate.ssrInstance,
    }));
    if (state.errors.length) return fail('script error: ' + state.errors[0]);
    if (state.reports.length) return fail('hydration mismatch: ' + state.reports[0]);
    if (mode === 'interactive' && !state.hydrated) return fail('interactive root was not hydrated');
    if (state.mutations) return fail('hydration wrote ' + state.mutations + ' DOM mutations');
    if (!state.identity) return fail('hydration replaced server elements');
    // Semantic comparison: attributes as a sorted set, style as the browser
    // normalises it, form controls by their value/checked/selected
    // properties (the server writes attributes, the client properties),
    // comments and the payload script ignored.
    const compare = (label) => page.evaluate((label) => {
      const formAttr = { value: true, checked: true, selected: true };
      const describe = (node) => {
        if (node.nodeType === 3) return node.data;
        if (node.nodeType !== 1) return '';
        if (node.localName === 'script' && node.hasAttribute('data-vela-root')) return '';
        const control = /^(input|textarea|select|option)$/.test(node.localName);
        const attrs = [...node.attributes]
          .filter((a) => !(control && formAttr[a.name]))
          .map((a) => a.name === 'style' ? 'style=' + node.style.cssText : a.name + '=' + a.value)
          .sort();
        if (control) {
          if ('value' in node && node.localName !== 'option') attrs.push('.value=' + node.value);
          if (node.type === 'checkbox' || node.type === 'radio') attrs.push('.checked=' + node.checked);
          if (node.localName === 'option') attrs.push('.selected=' + node.selected);
        }
        return '<' + node.localName + (attrs.length ? ' ' + attrs.join(' ') : '') + '>' + children(node) + '</' + node.localName + '>';
      };
      // Adjacent text nodes are joined (comments, such as block anchors, do
      // not separate them), empty ones dropped, and whitespace runs compared
      // as the browser renders them: parsed HTML and client-built DOM split
      // text differently without a visible difference.
      const children = (parent) => {
        const out = [];
        let text = null;
        const flushText = () => {
          if (text) out.push(JSON.stringify(text.replace(/[ \t\n\f\r]+/g, ' ')));
          text = null;
        };
        for (const child of parent.childNodes) {
          if (child.nodeType === 3) {
            text = (text ?? '') + child.data;
            continue;
          }
          if (child.nodeType === 8) continue;
          flushText();
          const d = describe(child);
          if (d) out.push(d);
        }
        flushText();
        return out.join('');
      };
      const tree = (id) => children(document.getElementById(id));
      const ssr = tree('ssr');
      const csr = tree('csr');
      if (ssr === csr) return '';
      let at = 0;
      while (at < ssr.length && ssr[at] === csr[at]) at++;
      const from = Math.max(0, at - 80);
      return label + ': hydrated DOM differs from CSR at ' + at + '\n  ssr: …' + ssr.slice(from, at + 80) + '\n  csr: …' + csr.slice(from, at + 80);
    }, label);
    let problem = await compare('initial');
    if (problem) return fail(problem);
    let clicks = 0;
    if (mode === 'interactive') {
      const selector = 'button, input[type=checkbox], input[type=radio]';
      const count = await page.evaluate((s) => Math.min(
        document.querySelectorAll('#ssr ' + s.split(', ').join(', #ssr ')).length,
        document.querySelectorAll('#csr ' + s.split(', ').join(', #csr ')).length), selector);
      for (let i = 0; i < Math.min(count, maxClicks); i++) {
        await page.evaluate(({ s, i }) => {
          for (const root of ['ssr', 'csr']) {
            const els = document.getElementById(root).querySelectorAll(s);
            if (els[i] && !els[i].disabled) els[i].click();
          }
        }, { s: selector, i });
        await page.waitForTimeout(30);
        problem = await compare('after click ' + (i + 1));
        if (problem) return fail(problem, { clicks: i + 1 });
        clicks = i + 1;
      }
    }
    const destroyed = await page.evaluate(async () => {
      const g = window.__gate;
      try {
        if (g.ssrInstance) g.ssrInstance.destroy();
        if (g.csrInstance) g.csrInstance.destroy();
      } catch (e) {
        return 'destroy threw: ' + e;
      }
      await new Promise((r) => setTimeout(r, 0));
      const after = g.stats();
      const before = g.baseline;
      if (after.owners !== before.owners || after.listeners !== before.listeners || after.queued !== 0) {
        return 'destroy left ' + JSON.stringify(after) + ', before ' + JSON.stringify(before);
      }
      return '';
    });
    if (destroyed) return fail(destroyed, { clicks });
    return { id, result: 'pass', clicks, pageErrors };
  } catch (error) {
    return fail('runner: ' + String(error && error.message || error));
  } finally {
    await page.close();
  }
}
