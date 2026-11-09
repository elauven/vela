/**
 * Vela hydration (docs/HYDRATION_ARCHITECTURE.md §4, §6, §7, §8.3).
 *
 * hydrate() adopts server-rendered HTML with the same compiled fragments
 * that create DOM: it runs them with a hydrate cursor, which takes the next
 * server node where the create cursor would make one (C-4.1, C-4.2). The
 * payload stream that follows the root range restores component state in
 * render order (S-6.7, S-6.8). Only builds that hydrate ship this module.
 *
 * Mismatches (X-7.1) follow a policy (§7.3): `repair` (the development
 * default) remounts the nearest enclosing block with a create cursor, or
 * the whole root (X-7.3), and warns; `fail` releases everything adopted
 * without touching the DOM and throws (X-7.4); production builds repair
 * with one console.error and no value verification.
 */

import { $$contextParent, $$contextRun, $$isDev, $$lazyById, warn } from './runtime.js';
import { $$owner, $$setHydration, $$createCursor, $$listen, $$action, $$queueAction, $$appHead } from './runtime-blocks.js';

export const MISMATCH = 'VELA-HYDRATION-MISMATCH';

// The default verification policy of development builds: repair, fail, or
// off (--hydration-verify, V-11.3c). The compiler rewrites this line.
const VERIFY = 'repair';

function describe(node) {
  if (!node) return 'the end of the parent';
  if (node.nodeType === 1) return `<${node.localName}>`;
  if (node.nodeType === 3) return `text ${JSON.stringify(node.data)}`;
  if (node.nodeType === 8) return `<!--${node.data}-->`;
  return `node type ${node.nodeType}`;
}

function mismatch(message, kind = 'structure') {
  const error = new Error(`${MISMATCH}: ${message}`);
  error.code = MISMATCH;
  error.kind = kind;
  return error;
}

// formatReport writes a report as V-11.3b shows it:
//   [vela] hydration mismatch at src/X.vela:14:9 (site 7, host formatCurrency)
//     server: "a"   client: "b"
function formatReport(report) {
  let text = '[vela] hydration mismatch';
  if (report.source) {
    text += ` at ${report.source} (site ${report.site}${report.host ? `, host ${report.host}` : ''})`;
  }
  if ('server' in report) {
    return `${text}\n  server: ${JSON.stringify(report.server)}   client: ${JSON.stringify(report.client)}`;
  }
  return `${text}: ${report.message.replace(`${MISMATCH}: `, '')}`;
}

function isMismatch(error) {
  return error && error.code === MISMATCH;
}

// A lone surrogate is written to HTML as U+FFFD; it compares equal (T-6.3b).
function htmlText(value) {
  return value.replace(/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/g, '�');
}

// sameAttribute compares an attribute value before and after a client
// write; style values are compared as declarations.
function sameAttribute(name, before, after) {
  if (before === after) return true;
  if (name !== 'style' || before === null || after === null) return false;
  const probe = document.createElement('div');
  probe.setAttribute('style', before);
  const normalized = probe.style.cssText;
  probe.setAttribute('style', after);
  return normalized === probe.style.cssText;
}

// hydrateCursor walks server DOM from position pos ({ parent, next, stack,
// owners }), which every cursor of one root shares.
// ctx is the context of the position (task 090).
function hydrateCursor(pos, owner, session, ctx) {
  const cursor = {
    owner,
    hydrating: true,
    ctx: ctx || null,
    sub(childOwner, childCtx) {
      pos.owners.push(childOwner);
      return hydrateCursor(pos, childOwner, session, childCtx);
    },
    expect(ok, message) {
      if (!ok) {
        const error = mismatch(message);
        error.site = session.site;
        throw error;
      }
    },
    // at names the next adopted site (V-11.3b): [id, source, host].
    at(site) {
      session.site = site;
    },
    element(tag) {
      const node = pos.next;
      cursor.expect(node && node.nodeType === 1 && node.localName === tag, `expected <${tag}>, found ${describe(node)}`);
      pos.next = node.nextSibling;
      return node;
    },
    // A text run with an empty value is not present in parsed HTML; it is
    // inserted, which is not a mismatch (C-4.3).
    text(value) {
      const node = pos.next;
      const site = session.take();
      if (node && node.nodeType === 3) {
        pos.next = node.nextSibling;
        if (session.verify && node.data !== htmlText(value)) {
          session.value(`text is ${JSON.stringify(node.data)}, the client computes ${JSON.stringify(value)}`, () => {
            node.data = value;
          }, { site, server: node.data, client: value });
        }
        return node;
      }
      cursor.expect(value === '', `expected text ${JSON.stringify(value)}, found ${describe(node)}`);
      const text = document.createTextNode('');
      pos.parent.insertBefore(text, node);
      return text;
    },
    comment() {
      throw new Error('vela: a fragment created an anchor comment while hydrating');
    },
    static(template) {
      const nodes = [];
      const expected = template.content.childNodes;
      for (let i = 0; i < expected.length; i++) {
        const node = pos.next;
        const want = expected[i];
        cursor.expect(node && node.nodeType === want.nodeType && (want.nodeType !== 1 || node.localName === want.localName),
          `expected ${describe(want)}, found ${describe(node)}`);
        nodes.push(node);
        pos.next = node.nextSibling;
      }
      return nodes;
    },
    enter(element) {
      pos.stack.push([pos.parent, pos.next]);
      pos.parent = element;
      pos.next = element.firstChild;
    },
    exit() {
      cursor.expect(pos.next === null, `unexpected ${describe(pos.next)} at the end of <${pos.parent.localName}>`);
      [pos.parent, pos.next] = pos.stack.pop();
    },
    // Initial DOM writes are skipped: the server wrote the same values
    // (F-2.2). Development builds run them and report a changed attribute
    // as a value mismatch (X-7.2).
    init(write) {
      const site = session.take();
      if (!session.observer) return;
      session.observer.takeRecords();
      write();
      for (const record of session.observer.takeRecords()) {
        if (record.type !== 'attributes') continue;
        const now = record.target.getAttribute(record.attributeName);
        if (!sameAttribute(record.attributeName, record.oldValue, now)) {
          session.value(`<${record.target.localName}> ${record.attributeName} is ${JSON.stringify(record.oldValue)}, the client computes ${JSON.stringify(now)}`, null, { site, server: record.oldValue, client: now });
        }
      }
    },
    // adopt takes the live value of a bound form control: a value the user
    // entered before hydration goes into state through the setter (C-4.5).
    adopt(element, property, read, write) {
      const live = element[property];
      let state = read();
      if (property === 'value') state = state == null ? '' : String(state);
      if (live !== state) session.after.push(() => write(live));
    },
    listen(target, event, handler, options) {
      $$listen(owner, target, event, handler, options);
    },
    // action starts use: on the adopted node after the commit (task 093); a
    // rolled-back hydration never starts it. An adoption inside a build
    // (serverComponent() in a new branch) waits for that build's insert.
    action(node, action, read) {
      return $$action(owner, node, action, read, (start) => session.after.push(() => $$queueAction(start)), cursor.ctx);
    },
    // block adopts a block; under repair a mismatch inside it remounts the
    // block from its server range with a create cursor.
    block(kind, ...args) {
      const site = session.take();
      if (session.policy === 'fail') {
        try {
          return kind(cursor, ...args);
        } catch (error) {
          if (isMismatch(error) && !error.site) error.site = site;
          throw error;
        }
      }
      const start = pos.next;
      const mark = pos.owners.length;
      try {
        return kind(cursor, ...args);
      } catch (error) {
        if (!isMismatch(error) || !start || start.nodeType !== 8 || !start.data.startsWith('[') || start.parentNode !== pos.parent) throw error;
        let end = null;
        let depth = 0;
        for (let node = start; node; node = node.nextSibling) {
          if (node.nodeType !== 8) continue;
          if (node.data.startsWith('[')) depth++;
          else if (node.data === ']' && --depth === 0) {
            end = node;
            break;
          }
        }
        if (!end) throw error;
        if (!error.site) error.site = site;
        session.report(error);
        for (const adopted of pos.owners.splice(mark)) adopted.release();
        // Entries of the abandoned range can no longer be matched to
        // instances; later instances initialize as on the client.
        session.lost = true;
        const after = end.nextSibling;
        for (let node = start; node !== after;) {
          const next = node.nextSibling;
          node.remove();
          node = next;
        }
        pos.next = after;
        return kind($$createCursor(pos.parent, after, owner, cursor.ctx), ...args);
      }
    },
    // open consumes a range start marker and returns its state token.
    open() {
      const node = pos.next;
      cursor.expect(node && node.nodeType === 8 && node.data.startsWith('['), `expected a block range, found ${describe(node)}`);
      pos.next = node.nextSibling;
      return node.data.slice(1);
    },
    // close consumes a range end marker, which becomes the block's anchor.
    close() {
      const node = pos.next;
      cursor.expect(node && node.nodeType === 8 && node.data === ']', `expected the end of a block range, found ${describe(node)}`);
      pos.next = node.nextSibling;
      return node;
    },
    // mount adopts a fragment at the current position. Its range is the
    // adopted nodes, or null when it has none.
    // ctx, when given, is the context of the fragment ({#boundary}).
    mount(fragmentFn, scope, fragmentOwner, ctx) {
      const mountOwner = fragmentOwner || $$owner(null);
      pos.owners.push(mountOwner);
      const before = pos.next ? pos.next.previousSibling : pos.parent.lastChild;
      const handle = fragmentFn(hydrateCursor(pos, mountOwner, session, ctx === undefined ? cursor.ctx : ctx), scope);
      const start = before ? before.nextSibling : pos.parent.firstChild;
      const range = start && start !== pos.next
        ? { start, end: pos.next ? pos.next.previousSibling : pos.parent.lastChild }
        : null;
      return { owner: mountOwner, handle, range, scope };
    },
    // checkpoint marks the position inside an open range ({#boundary},
    // task 091). The returned restore() undoes a failed adoption from
    // there: it releases what was adopted since, removes the server nodes
    // up to the range end, which becomes the next node, and gives up the
    // payload stream, whose entries can no longer be matched (as a repair
    // does).
    checkpoint() {
      const parent = pos.parent;
      const depth = pos.stack.length;
      const mark = pos.owners.length;
      const first = pos.next;
      return () => {
        for (const adopted of pos.owners.splice(mark)) adopted.release();
        pos.stack.length = depth;
        pos.parent = parent;
        let node = first;
        for (let nested = 0; node;) {
          if (node.nodeType === 8) {
            if (node.data.startsWith('[')) nested++;
            else if (node.data === ']' && nested-- === 0) break;
          }
          const next = node.nextSibling;
          node.remove();
          node = next;
        }
        pos.next = node;
        session.lost = true;
      };
    },
    // mountRow adopts an #each row inside its own range (M-5.2).
    mountRow(fragmentFn, scope) {
      const start = pos.next;
      const token = cursor.open();
      cursor.expect(token === '', `expected an #each row, found <!--[${token}-->`);
      const mounted = cursor.mount(fragmentFn, scope);
      mounted.range = { start, end: cursor.close() };
      return mounted;
    },
    entry(kind) {
      return session.entry(kind);
    },
    afterCommit(callback) {
      session.after.push(callback);
    },
  };
  return cursor;
}

function createSession(entries, options, container) {
  let index = 0;
  const dev = $$isDev();
  const requested = options.policy || VERIFY;
  // off repairs structural mismatches without comparing values.
  const policy = dev ? (requested === 'off' ? 'repair' : requested) : 'production';
  let reported = false;
  const session = {
    cursors: [],
    after: [],
    entries,
    policy,
    lost: false,
    verify: dev && policy !== 'production' && requested !== 'off',
    observer: null,
    site: null,
    // take returns the site named by the last at() and clears it.
    take() {
      const site = session.site;
      session.site = null;
      return site;
    },
    next(kind) {
      if (session.lost) return null;
      const entry = entries[index++];
      if (!entry || entry[0] !== kind) {
        throw mismatch(`expected a payload "${kind}" entry, found ${entry ? JSON.stringify(entry[0]) : 'the end of the stream'}`, 'stream');
      }
      return entry;
    },
    // state returns the restored state of a component instance (S-6.4,
    // S-6.8): omitted bindings are left out so that their initializers run.
    state(component) {
      const entry = session.next('s');
      if (!entry) return null;
      if (entry[1] !== component) throw mismatch(`expected the state of <${component}>, found <${entry[1]}>`, 'stream');
      const state = Object.assign({}, entry[2]);
      for (const name of (entry[3] && entry[3].omitted) || []) delete state[name];
      return state;
    },
    entry(kind) {
      const entry = session.next(kind);
      return entry ? entry[2] : null;
    },
    done() {
      return session.lost || index === entries.length;
    },
    // report passes every mismatch to onMismatch (X-7.5) and logs it.
    report(error) {
      const report = { code: MISMATCH, kind: error.kind || 'structure', message: error.message, policy };
      // Source position, site ID, host symbol, and both values (V-11.3b).
      const site = error.site;
      if (site) Object.assign(report, { site: site[0], source: site[1], host: site[2] || undefined });
      if ('server' in error) Object.assign(report, { server: error.server, client: error.client });
      if (options.onMismatch) options.onMismatch(report);
      if (policy === 'production') {
        if (!reported) console.error(MISMATCH);
        reported = true;
      } else if (policy === 'repair') {
        console.warn(formatReport(report));
      }
    },
    // value handles a value mismatch: fail throws, repair writes the client
    // value (or keeps the written one) and warns.
    value(message, repair, details) {
      const error = mismatch(message, 'value');
      if (details) {
        error.site = details.site;
        error.server = details.server;
        error.client = details.client;
      }
      if (policy === 'fail') throw error;
      session.report(error);
      if (repair) repair();
    },
  };
  // Attribute verification observes the writes of init(); text needs no
  // observer.
  const Observer = session.verify && (globalThis.MutationObserver || (document.defaultView && document.defaultView.MutationObserver));
  if (Observer) {
    session.observer = new Observer(() => {});
    session.observer.observe(container, { subtree: true, attributes: true, attributeOldValue: true });
  }
  return session;
}

// payloadOf returns the payload of the root in container without
// consuming it, or null.
function payloadOf(container) {
  for (const node of container.childNodes) {
    if (node.nodeType === 1 && node.localName === 'script' && node.hasAttribute('data-vela-root')) {
      try {
        return JSON.parse(node.textContent);
      } catch {
        return null;
      }
    }
  }
  return null;
}

// preloadHydration loads the lazy components (lazy(), task 071) that the
// server rendered in container, named by the "d" entries of its payload,
// so that hydrate() can adopt them synchronously (S-6.14). A lazy module
// can register further lazy components when it loads; the entries are
// read again until nothing new loads. A failed load is reported with warn;
// hydrate() then reports lazy-not-loaded and repairs.
export async function preloadHydration(container) {
  const payload = payloadOf(container);
  if (!payload || !Array.isArray(payload.entries)) return;
  const ids = [...new Set(payload.entries.filter((entry) => entry[0] === 'd').map((entry) => entry[2]))];
  const done = new Set();
  for (let progress = true; progress;) {
    progress = false;
    const pending = [];
    for (const id of ids) {
      const value = done.has(id) ? null : $$lazyById(id);
      if (!value) continue;
      done.add(id);
      if (value.loaded) continue;
      progress = true;
      pending.push(value.load().catch((error) => {
        warn(`preloadHydration: the lazy component ${id} failed to load: ${error && error.message ? error.message : error}`);
      }));
    }
    await Promise.all(pending);
  }
}

// readPayload parses a payload script (S-6.9) and checks its version.
function readPayload(script) {
  const payload = JSON.parse(script.textContent);
  if (payload.v !== 4) throw mismatch(`unsupported payload version ${payload.v}`);
  return payload;
}

// adoptRoot runs a hydration session for Component over the root range
// that starts at first (whitespace before it is skipped) and ends before
// script, its payload, under parent: a container element for hydrate(), the
// range a <vela:component> block mounts into for serverComponent().
// observed is the node whose attribute writes the session verifies. It
// returns { instance, failure, session } and the two ways to finish:
// commit() removes the payload and runs the after-commit callbacks;
// rollback() releases everything adopted without touching the DOM (X-7.4).
function adoptRoot(Component, parent, first, script, payload, options, observed, target, ctx) {
  while (first && first !== script && first.nodeType === 3 && first.data.trim() === '') first = first.nextSibling;
  const pos = { parent, next: first, stack: [], owners: [] };
  const session = createSession(payload.entries || [], options, observed);
  const owner = $$owner(null);
  pos.owners.push(owner);
  const cursor = hydrateCursor(pos, owner, session, ctx);
  const previous = $$setHydration(session);
  let instance = null;
  let failure = null;
  try {
    cursor.open();
    session.cursors.push(cursor);
    try {
      // The root's script runs in a context frame under ctx (task 090).
      instance = $$contextRun(ctx, () => Component(target, payload.props || {}, options.slots || {}));
    } finally {
      session.cursors.pop();
    }
    cursor.close();
    cursor.expect(pos.next === script || (pos.next && pos.next.nodeType === 3 && pos.next.nextSibling === script), `unexpected ${describe(pos.next)} after the root range`);
    cursor.expect(session.done(), 'the payload stream has entries no component consumed');
  } catch (error) {
    failure = error;
  } finally {
    $$setHydration(previous);
    if (session.observer) session.observer.disconnect();
  }
  return {
    instance,
    failure,
    session,
    rollback() {
      if (instance && instance.abandon) instance.abandon();
      for (let i = pos.owners.length - 1; i >= 0; i--) pos.owners[i].release();
    },
    commit() {
      script.remove();
      for (const callback of session.after) callback();
      // The root owner holds what the root cursor adopted; it goes with the
      // instance (Q-10.2 item 5).
      const destroy = instance.destroy;
      instance.destroy = function () {
        destroy.call(this);
        owner.release();
      };
      // A parent block releases a child without removing its DOM through
      // abandon (B-3.4), as <vela:component> does when its value changes.
      const abandon = instance.abandon;
      if (abandon) {
        instance.abandon = function () {
          abandon.call(this);
          owner.release();
        };
      }
      return instance;
    },
  };
}

// hydrate adopts the root range and payload script that a Go renderer
// wrote into container (A-8.7) and returns the component instance, which
// then behaves exactly like one created by the factory.
//
// options: policy ('repair' | 'fail' | 'off'; production builds use their
// own policy), onMismatch(report) (X-7.5), slots.
export function hydrate(Component, container, options = {}) {
  let script = null;
  for (const node of container.childNodes) {
    if (node.nodeType === 1 && node.localName === 'script' && node.hasAttribute('data-vela-root')) script = node;
  }
  if (!script) throw mismatch('the container has no payload script (data-vela-root)');
  const payload = readPayload(script);
  const props = payload.props || {};
  const result = adoptRoot(Component, container, container.firstChild, script, payload, options, container, container, null);
  if (result.failure) {
    result.rollback();
    if (!isMismatch(result.failure) || result.session.policy === 'fail') throw result.failure;
    // A mismatch at the root range remounts the root (X-7.3).
    result.session.report(result.failure);
    while (container.firstChild) container.firstChild.remove();
    return $$contextRun(null, () => Component(container, props, options.slots || {}));
  }
  return result.commit();
}

// ── Server-rendered fragments (task 072) ─────────────────────────────────
// serverComponent and staticHTML turn HTML that arrives after the page
// loaded into component values for <vela:component> (§8.3). The HTML is
// parsed with a <template>, so its scripts never run; the payload script is
// read as JSON only. HTML from the server is trusted like the document:
// never pass HTML from another origin.

function parseHTML(html) {
  const template = document.createElement('template');
  template.innerHTML = html;
  return template.content;
}

// The target of a component factory: a comment anchor the nodes go before
// (how <vela:component> mounts a child), or an element they are appended to.
function placeOf(target, what) {
  if (target && target.nodeType === 8 && target.parentNode) return [target.parentNode, target];
  if (target && (target.nodeType === 1 || target.nodeType === 11)) return [target, null];
  throw new TypeError(`vela: ${what} mounts only after the page hydrated, into <vela:component> or an element`);
}

// serverComponent(Component, html, options) is a component value whose
// first mount adopts html: the root range and payload script that
// RenderXContext of Component wrote. The nodes are inserted where the value
// mounts and hydrated like hydrate() does, with the same options (policy,
// onMismatch). Under repair a mismatch removes them and creates Component
// with the props of the payload; under fail it throws. Props come from the
// payload; the props the parent passes are applied with set() after the
// commit. A later mount of the same value adopts a fresh copy of html.
export function serverComponent(Component, html, options = {}) {
  if (typeof Component !== 'function') throw new TypeError('vela: serverComponent() needs a component factory');
  if (typeof html !== 'string') throw new TypeError('vela: serverComponent() needs the HTML as a string');
  function Adopted(target, props, slots) {
    // Adopted runs as the factory of its mount position; Component inherits
    // the context of that position (task 090).
    const ctx = $$contextParent();
    const [parent, before] = placeOf(target, 'serverComponent()');
    const content = parseHTML(html);
    let script = null;
    for (const node of content.childNodes) {
      if (node.nodeType === 1 && node.localName === 'script' && node.hasAttribute('data-vela-root')) script = node;
    }
    if (!script) throw mismatch('serverComponent(): the HTML has no payload script (data-vela-root)');
    const payload = readPayload(script);
    const nodes = [...content.childNodes];
    parent.insertBefore(content, before);
    const own = Object.assign({}, options, slots && Object.keys(slots).length ? { slots } : {});
    const result = adoptRoot(Component, parent, nodes[0], script, payload, own, parent, target, ctx);
    if (result.failure) {
      result.rollback();
      for (const node of nodes) node.remove();
      if (!isMismatch(result.failure) || result.session.policy === 'fail') throw result.failure;
      result.session.report(result.failure);
      const created = $$contextRun(ctx, () => Component(target, payload.props || {}, slots || {}));
      if (props && created.set && Object.keys(props).length) created.set(props);
      return created;
    }
    const instance = result.commit();
    const destroy = instance.destroy;
    instance.destroy = function () {
      destroy.call(this);
      // The root range markers and whitespace around it.
      for (const node of nodes) if (node.parentNode) node.remove();
    };
    if (props && instance.set && Object.keys(props).length) instance.set(props);
    return instance;
  }
  Adopted.$$props = Component.$$props;
  Adopted.$$adopted = Component;
  return Adopted;
}

// hasMarkers reports range markers or a payload script: the HTML of an
// interactive root.
function hasMarkers(content) {
  const walker = document.createTreeWalker(content, 0x80 | 0x1);
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    if (node.nodeType === 8 && (node.data.startsWith('[') || node.data === ']')) return true;
    if (node.nodeType === 1 && node.localName === 'script' && node.hasAttribute('data-vela-root')) return true;
  }
  return false;
}

// staticHTML(html) is a component value that inserts html and creates no
// instance: it has no state, set() and updates do nothing, and destroy
// removes the nodes. HTML with range markers or a payload script is an
// interactive root; development builds reject it.
export function staticHTML(html) {
  if (typeof html !== 'string') throw new TypeError('vela: staticHTML() needs the HTML as a string');
  function Static(target) {
    const [parent, before] = placeOf(target, 'staticHTML()');
    const content = parseHTML(html);
    if ($$isDev() && hasMarkers(content)) {
      throw new TypeError('vela: staticHTML() got an interactive root (range markers or a payload script); use serverComponent()');
    }
    const nodes = [...content.childNodes];
    parent.insertBefore(content, before);
    const single = nodes.length === 1 && nodes[0].nodeType === 1 ? nodes[0] : null;
    return {
      el: single,
      destroy() {
        for (const node of nodes) if (node.parentNode) node.remove();
      },
      abandon() {},
    };
  }
  Static.$$props = {};
  return Static;
}

// replaceAppHead replaces the head tags of the application (Vela App: the
// title and meta tags of r.Head() and Config.Head) with tags, a list of
// { node, depth }: depth is the depth of the component the value belongs
// to, 0 for the root. A tag of a component's <vela:head> with the same
// title, meta name or property, or canonical link wins when its component
// is deeper or as deep (task 091); the components' tags are not touched.
export function replaceAppHead(tags) {
  $$appHead(tags.map((tag) => ({ node: tag.node, depth: tag.depth || 0 })));
}
