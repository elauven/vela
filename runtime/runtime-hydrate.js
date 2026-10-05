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

import { $$isDev } from './runtime.js';
import { $$owner, $$setHydration, $$createCursor } from './runtime-blocks.js';

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
function hydrateCursor(pos, owner, session) {
  const cursor = {
    owner,
    hydrating: true,
    sub(childOwner) {
      pos.owners.push(childOwner);
      return hydrateCursor(pos, childOwner, session);
    },
    expect(ok, message) {
      if (!ok) throw mismatch(message);
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
      if (node && node.nodeType === 3) {
        pos.next = node.nextSibling;
        if (session.verify && node.data !== htmlText(value)) {
          session.value(`text is ${JSON.stringify(node.data)}, the client computes ${JSON.stringify(value)}`, () => {
            node.data = value;
          });
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
      if (!session.observer) return;
      session.observer.takeRecords();
      write();
      for (const record of session.observer.takeRecords()) {
        if (record.type !== 'attributes') continue;
        const now = record.target.getAttribute(record.attributeName);
        if (!sameAttribute(record.attributeName, record.oldValue, now)) {
          session.value(`<${record.target.localName}> ${record.attributeName} is ${JSON.stringify(record.oldValue)}, the client computes ${JSON.stringify(now)}`, null);
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
      target.addEventListener(event, handler, options);
      owner.add(() => target.removeEventListener(event, handler, options));
    },
    // block adopts a block; under repair a mismatch inside it remounts the
    // block from its server range with a create cursor.
    block(kind, ...args) {
      if (session.policy === 'fail') return kind(cursor, ...args);
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
        return kind($$createCursor(pos.parent, after, owner), ...args);
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
    mount(fragmentFn, scope, fragmentOwner) {
      const mountOwner = fragmentOwner || $$owner(null);
      pos.owners.push(mountOwner);
      const before = pos.next ? pos.next.previousSibling : pos.parent.lastChild;
      const handle = fragmentFn(hydrateCursor(pos, mountOwner, session), scope);
      const start = before ? before.nextSibling : pos.parent.firstChild;
      const range = start && start !== pos.next
        ? { start, end: pos.next ? pos.next.previousSibling : pos.parent.lastChild }
        : null;
      return { owner: mountOwner, handle, range, scope };
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
      if (options.onMismatch) options.onMismatch(report);
      if (policy === 'production') {
        if (!reported) console.error(MISMATCH);
        reported = true;
      } else if (policy === 'repair') {
        console.warn('[Vela]', error.message);
      }
    },
    // value handles a value mismatch: fail throws, repair writes the client
    // value (or keeps the written one) and warns.
    value(message, repair) {
      const error = mismatch(message, 'value');
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
  const payload = JSON.parse(script.textContent);
  if (payload.v !== 2) throw mismatch(`unsupported payload version ${payload.v}`);
  const props = payload.props || {};

  let first = container.firstChild;
  while (first && first.nodeType === 3 && first.data.trim() === '') first = first.nextSibling;
  const pos = { parent: container, next: first, stack: [], owners: [] };
  const session = createSession(payload.entries || [], options, container);
  const owner = $$owner(null);
  pos.owners.push(owner);
  const cursor = hydrateCursor(pos, owner, session);
  const previous = $$setHydration(session);
  let instance = null;
  let failure = null;
  try {
    cursor.open();
    session.cursors.push(cursor);
    try {
      instance = Component(container, props, options.slots || {});
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
  if (failure) {
    // Rollback (X-7.4): release everything adopted without touching the
    // DOM; the server DOM and payload stay for a retry.
    if (instance && instance.abandon) instance.abandon();
    for (let i = pos.owners.length - 1; i >= 0; i--) pos.owners[i].release();
    if (!isMismatch(failure) || session.policy === 'fail') throw failure;
    // A mismatch at the root range remounts the root (X-7.3).
    session.report(failure);
    while (container.firstChild) container.firstChild.remove();
    return Component(container, props, options.slots || {});
  }
  script.remove();
  for (const callback of session.after) callback();
  return instance;
}
