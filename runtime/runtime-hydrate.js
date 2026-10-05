/**
 * Vela hydration (docs/HYDRATION_ARCHITECTURE.md §4, §6, §8.3).
 *
 * hydrate() adopts server-rendered HTML with the same compiled fragments
 * that create DOM: it runs them with a hydrate cursor, which takes the next
 * server node where the create cursor would make one (C-4.1, C-4.2). The
 * payload stream that follows the root range restores component state in
 * render order (S-6.7, S-6.8). Only builds that hydrate ship this module.
 */

import { $$owner, $$setHydration } from './runtime-blocks.js';

export const MISMATCH = 'VELA-HYDRATION-MISMATCH';

function describe(node) {
  if (!node) return 'the end of the parent';
  if (node.nodeType === 1) return `<${node.localName}>`;
  if (node.nodeType === 3) return `text ${JSON.stringify(node.data)}`;
  if (node.nodeType === 8) return `<!--${node.data}-->`;
  return `node type ${node.nodeType}`;
}

function mismatch(message) {
  const error = new Error(`${MISMATCH}: ${message}`);
  error.code = MISMATCH;
  return error;
}

// hydrateCursor walks server DOM from position pos ({ parent, next,
// stack }), which every cursor of one root shares.
function hydrateCursor(pos, owner, session) {
  const cursor = {
    owner,
    hydrating: true,
    sub(childOwner) {
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
    // (F-2.2). Development value verification is X-7.2.
    init() {},
    listen(target, event, handler, options) {
      target.addEventListener(event, handler, options);
      owner.add(() => target.removeEventListener(event, handler, options));
    },
    block(kind, ...args) {
      return kind(cursor, ...args);
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

function createSession(entries) {
  let index = 0;
  const session = {
    cursors: [],
    after: [],
    entries,
    next(kind) {
      const entry = entries[index++];
      if (!entry || entry[0] !== kind) {
        throw mismatch(`expected a payload "${kind}" entry, found ${entry ? JSON.stringify(entry[0]) : 'the end of the stream'}`);
      }
      return entry;
    },
    // state returns the restored state of a component instance (S-6.4,
    // S-6.8): omitted bindings are left out so that their initializers run.
    state(component) {
      const entry = session.next('s');
      if (entry[1] !== component) throw mismatch(`expected the state of <${component}>, found <${entry[1]}>`);
      const state = Object.assign({}, entry[2]);
      for (const name of (entry[3] && entry[3].omitted) || []) delete state[name];
      return state;
    },
    entry(kind) {
      return session.next(kind)[2];
    },
    done() {
      return index === entries.length;
    },
  };
  return session;
}

// hydrate adopts the root range and payload script that a Go renderer
// wrote into container (A-8.7) and returns the component instance, which
// then behaves exactly like one created by the factory. A mismatch throws
// a VELA-HYDRATION-MISMATCH error after releasing what was adopted (X-7.4);
// the server DOM and payload stay in place.
export function hydrate(Component, container, options = {}) {
  let script = null;
  for (const node of container.childNodes) {
    if (node.nodeType === 1 && node.localName === 'script' && node.hasAttribute('data-vela-root')) script = node;
  }
  if (!script) throw mismatch('the container has no payload script (data-vela-root)');
  const payload = JSON.parse(script.textContent);
  if (payload.v !== 2) throw mismatch(`unsupported payload version ${payload.v}`);

  let first = container.firstChild;
  while (first && first.nodeType === 3 && first.data.trim() === '') first = first.nextSibling;
  const pos = { parent: container, next: first, stack: [] };
  const session = createSession(payload.entries || []);
  const owner = $$owner(null);
  const cursor = hydrateCursor(pos, owner, session);
  const previous = $$setHydration(session);
  let instance = null;
  try {
    cursor.open();
    session.cursors.push(cursor);
    try {
      instance = Component(container, payload.props || {}, options.slots || {});
    } finally {
      session.cursors.pop();
    }
    cursor.close();
    cursor.expect(pos.next === script || (pos.next && pos.next.nodeType === 3 && pos.next.nextSibling === script), `unexpected ${describe(pos.next)} after the root range`);
    cursor.expect(session.done(), 'the payload stream has entries no component consumed');
  } catch (error) {
    // Rollback: release listeners and children without touching the DOM.
    if (instance && instance.abandon) instance.abandon();
    owner.release();
    throw error;
  } finally {
    $$setHydration(previous);
  }
  script.remove();
  for (const callback of session.after) callback();
  return instance;
}
