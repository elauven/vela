// Hydration checks shared by tools/ssr-blocks-check.mjs: they use only DOM
// APIs, so the same functions run in jsdom and in a Chromium page (task
// 052, Q-10.2). env.load(path) imports a module of the client directory.
// serialize writes the DOM with form state taken from properties: the
// server writes value, checked, and selected attributes where the client
// sets properties. Adjacent text nodes are merged, as the HTML parser merges
// them; comments are written only when comments is set. The function is
// self-contained so that it also runs inside Chromium.
export function serialize(node, comments = false) {
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


export async function clientHTML(env, testCase) {
  const host = document.createElement('div');
  document.body.appendChild(host);
  const module = await env.load(testCase.module);
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
export const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

export function applyStep(host, instance, step) {
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

export async function hydrateCheck(env, testCase) {
  const errors = [];
  // Q-10.2 item 5: after destroy() no owner, listener, or queued update of
  // the case remains.
  const { $$debugStats } = await env.load('runtime-blocks.js');
  const before = $$debugStats();
  const module = await env.load(testCase.module);
  const factory = module[testCase.export] || module.default;
  const { hydrate } = await env.load('runtime-hydrate.js');
  const reference = document.createElement('div');
  document.body.appendChild(reference);
  const created = factory(reference, structuredClone(testCase.props));
  await settle();
  const host = document.createElement('div');
  document.body.appendChild(host);
  host.innerHTML = testCase.html;
  const serverElements = [...host.querySelectorAll('*')].filter((el) => el.localName !== 'script');
  // {#client}: after the commit a fallback is replaced by the main branch
  // (contract §9.1); only fallback nodes may go, and only new nodes come.
  const fallbackNodes = new Set();
  // server selectors must be present; fallback selectors only may go.
  for (const selector of [...((testCase.boundary && testCase.boundary.server) || []), ...((testCase.boundary && testCase.boundary.fallback) || [])]) {
    for (const element of host.querySelectorAll(selector)) {
      fallbackNodes.add(element);
      for (const node of element.querySelectorAll("*")) fallbackNodes.add(node);
    }
  }
  const walker = document.createTreeWalker(host, 0xFFFFFFFF);
  const serverNodes = new Set();
  for (let node = walker.nextNode(); node; node = walker.nextNode()) serverNodes.add(node);
  const observer = new MutationObserver(() => {});
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
    const boundary = testCase.boundary && record.type === "childList" &&
      [...record.removedNodes].every((node) => fallbackNodes.has(node) || node.nodeType !== 1 && !node.parentNode) &&
      [...record.addedNodes].every((node) => !serverNodes.has(node));
    if (!emptyText && !payload && !unchanged && !boundary) errors.push(`hydration mutated the DOM: ${record.type} on <${record.target.localName || record.target.nodeName}>`);
  }
  observer.disconnect();
  for (const element of serverElements) {
    if (!host.contains(element) && !fallbackNodes.has(element)) errors.push(`hydration replaced the server <${element.localName}>`);
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
  await settle();
  const after = $$debugStats();
  if (after.owners !== before.owners || after.listeners !== before.listeners || after.queued !== 0) {
    errors.push(`destroy() left ${after.owners - before.owners} owners, ${after.listeners - before.listeners} listeners, and ${after.queued} queued updates`);
  }
  host.remove();
  reference.remove();
  return errors;
}

// ── Mismatch suite (task 041, Q-10.3) ────────────────────────────────────
// Each injected mismatch is hydrated under every policy (§7.3): repair and
// production converge to the client DOM (production does not verify
// values), fail throws and leaves the DOM untouched, and a retry after the
// failure yields exactly one live instance.
export async function mismatchCheck(env, testCase, injected) {
  const errors = [];
  const module = await env.load(testCase.module);
  const factory = module[testCase.export] || module.default;
  const { hydrate } = await env.load('runtime-hydrate.js');
  const { $$debugStats } = await env.load('runtime-blocks.js');
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
    const before = $$debugStats();
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
      if (injected.source && policy === 'repair') {
        // V-11.3b: the report names the source position and site ID.
        const sited = reports.find((report) => report.source === injected.source && Number.isInteger(report.site));
        if (!sited) errors.push(`${label(policy)}: no report at ${injected.source}, got ${JSON.stringify(reports.map((r) => [r.source, r.site, r.host]))}`);
        if (globalThis.process?.env?.VELA_PRINT_REPORTS) console.error(JSON.stringify(reports));
      }
      if (verified && !reports.some((report) => report.kind === injected.kind)) errors.push(`${label(policy)}: onMismatch got ${JSON.stringify(reports.map((r) => r.kind))}, want a ${injected.kind} report`);
      if (injected.message && policy === 'repair' && !reports.some((report) => report.message.includes(injected.message))) {
        errors.push(`${label(policy)}: no report says ${JSON.stringify(injected.message)}, got ${JSON.stringify(reports.map((r) => r.message))}`);
      }
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
    // Repair, rollback, and retry leave nothing behind either (Q-10.2).
    await settle();
    const after = $$debugStats();
    if (after.owners !== before.owners || after.listeners !== before.listeners || after.queued !== 0) {
      errors.push(`${label(policy)}: destroy() left ${after.owners - before.owners} owners, ${after.listeners - before.listeners} listeners, and ${after.queued} queued updates`);
    }
    host.remove();
    reference.remove();
  }
  return errors;
}

// ── Fragments (task 072) ─────────────────────────────────────────────────
// The server HTML of a case is mounted into <vela:component> of Adopt.vela
// with serverComponent(): it must adopt without a mismatch report, leave
// the client DOM, stay interactive, and switch to staticHTML(), to an
// ordinary component and to nothing. Injected mismatches repair to the
// client DOM or, under fail, throw. Nothing may stay behind after destroy.
const quiet = (run) => {
  const saved = { warn: console.warn, error: console.error };
  console.warn = () => {};
  console.error = () => {};
  try {
    return run();
  } finally {
    Object.assign(console, saved);
  }
};

export async function fragmentCheck(env, testCase) {
  const errors = [];
  const { $$debugStats } = await env.load('runtime-blocks.js');
  const { serverComponent, staticHTML } = await env.load('runtime-hydrate.js');
  const module = await env.load(testCase.module);
  const factory = module[testCase.export] || module.default;
  const { Adopt } = await env.load('Adopt.js');
  const { DynA } = await env.load('DynA.js');
  const before = $$debugStats();
  const place = () => {
    const element = document.createElement('div');
    document.body.appendChild(element);
    return element;
  };
  const reference = place();
  const created = factory(reference, structuredClone(testCase.props));
  await settle();
  const host = place();
  const reports = [];
  let adopt;
  try {
    adopt = Adopt(host, { view: serverComponent(factory, testCase.html, { onMismatch: (report) => reports.push(report) }) });
  } catch (error) {
    created.destroy();
    host.remove();
    reference.remove();
    return [`serverComponent: ${error.message}`];
  }
  await settle();
  const box = host.querySelector('.adopt');
  if (reports.length) errors.push(`serverComponent reported ${JSON.stringify(reports.map((r) => r.message))}`);
  if (box.querySelector('script')) errors.push('serverComponent left the payload script');
  if (serialize(box) !== serialize(reference)) {
    errors.push(`adopted DOM differs from the client DOM:\n  adopted: ${serialize(box)}\n  client:  ${serialize(reference)}`);
  } else {
    // Events and updates work after the adoption; set steps go to the
    // root instance, which the adopted value hides, so they are skipped.
    for (const [index, step] of (testCase.steps || []).entries()) {
      if (step.set) continue;
      applyStep(box, null, step);
      applyStep(reference, created, step);
      await settle();
      if (serialize(box) !== serialize(reference)) {
        errors.push(`adopted, after step ${index} ${JSON.stringify(step)}:\n  adopted: ${serialize(box)}\n  client:  ${serialize(reference)}`);
        break;
      }
    }
  }
  const staticReference = document.createElement('div');
  staticReference.innerHTML = testCase.static;
  adopt.set({ view: staticHTML(testCase.static) });
  await settle();
  if (serialize(box) !== serialize(staticReference)) errors.push(`staticHTML DOM differs:\n  mounted: ${serialize(box)}\n  static:  ${serialize(staticReference)}`);
  adopt.set({ view: DynA });
  await settle();
  if (!box.querySelector('.a') || box.children.length !== 1) errors.push(`switching to a component: ${serialize(box)}`);
  adopt.set({ view: null });
  await settle();
  if (box.childNodes.length && [...box.childNodes].some((node) => node.nodeType !== 8)) errors.push(`switching to nothing left ${serialize(box)}`);
  try {
    staticHTML(testCase.html)(place());
    errors.push('staticHTML accepted an interactive root');
  } catch (error) {
    if (!/interactive root/.test(error.message)) errors.push(`staticHTML: ${error.message}`);
  }
  document.body.lastChild.remove();
  adopt.destroy();
  created.destroy();
  host.remove();
  reference.remove();

  for (const injected of testCase.mismatches || []) {
    const html = injected.regex ? testCase.html.replace(new RegExp(injected.from), injected.to) : testCase.html.replace(injected.from, injected.to);
    for (const policy of ['repair', 'fail']) {
      const label = `fragment ${injected.name} (${policy})`;
      const expected = place();
      const fresh = factory(expected, structuredClone(testCase.props));
      await settle();
      const target = place();
      const got = [];
      let mounted = null;
      let thrown = null;
      quiet(() => {
        try {
          const view = serverComponent(factory, html, { policy, onMismatch: (report) => got.push(report) });
          // Under fail the value throws while it mounts; it is mounted
          // directly, since a host component cannot release a child that
          // threw in its constructor.
          mounted = policy === 'fail' ? view(target) : Adopt(target, { view });
        } catch (error) {
          thrown = error;
        }
      });
      await settle();
      if (policy === 'fail') {
        if (!thrown || thrown.code !== 'VELA-HYDRATION-MISMATCH') errors.push(`${label}: expected VELA-HYDRATION-MISMATCH, got ${thrown ? thrown.message : 'no error'}`);
      } else if (thrown) {
        errors.push(`${label}: threw ${thrown.message}`);
      } else {
        if (!got.some((report) => report.kind === injected.kind)) errors.push(`${label}: onMismatch got ${JSON.stringify(got.map((r) => r.kind))}, want a ${injected.kind} report`);
        const box = target.querySelector('.adopt');
        if (serialize(box) !== serialize(expected)) errors.push(`${label}: DOM differs from the client:\n  adopted: ${serialize(box)}\n  client:  ${serialize(expected)}`);
      }
      if (mounted) mounted.destroy();
      fresh.destroy();
      target.remove();
      expected.remove();
    }
  }
  await settle();
  const after = $$debugStats();
  if (after.owners !== before.owners || after.listeners !== before.listeners || after.queued !== 0) {
    errors.push(`fragments left ${after.owners - before.owners} owners, ${after.listeners - before.listeners} listeners, and ${after.queued} queued updates`);
  }
  return errors;
}
