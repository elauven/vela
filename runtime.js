/**
 * Vela Runtime
 * The only JS that ships to the browser — everything else is compiled away.
 * ~120 lines. No dependencies.
 */

// ─────────────────────────────────────────────
//  Batch update mechanism
//
//  When a store fires (set/update), all subscribers run inside _batchRun().
//  Generated component setters call $$defer(fn) for DOM-touching work.
//  $$defer pushes fn to the shared queue.  Two flush paths drain it:
//
//    1. _batchRun() — when explicit batching is in progress (depth > 0),
//       flush runs synchronously after the outer-most _batchRun returns.
//       Used by store subscribe/notify cycles.
//
//    2. queueMicrotask — when no batch is in progress (depth === 0), the
//       first $$defer schedules a microtask that drains the queue at the
//       end of the current task.  Subsequent synchronous $$defer calls
//       reuse the already-scheduled microtask (the `scheduled` flag),
//       coalescing a chain of state changes into one DOM-update pass.
//
//  Why microtasks instead of synchronous fallback: a single user action
//  often updates several pieces of state in a row (e.g. `loading=true;
//  error=''; data=null` at the start of an async fetch).  Synchronous
//  $$defer would redraw three times, with intermediate visual states
//  flashing.  Microtask coalescing redraws once after all three setters
//  return.
//
//  Both flush paths share the same queue and clear `scheduled` after
//  draining, so only one of them runs per coalesced batch even if
//  _batchRun fires before the microtask gets its turn.
//
//  The context is shared via Symbol.for only for the deprecated legacy
//  generator. AST-generated components import $$defer directly from this
//  module, so they never change behaviour based on global registration order.
// ─────────────────────────────────────────────

const _B = Symbol.for('vela.batch');
if (!globalThis[_B]) {
  globalThis[_B] = { depth: 0, queue: [], scheduled: false };
}

// _flushQueue drains b.queue, running each deferred fn in FIFO order.
// Reentrant pushes (a deferred fn that queues another fn) are handled by
// looping until the queue is empty, so the entire transitive closure of
// updates triggered by one batch flushes in one pass.
function _flushQueue() {
  const b = globalThis[_B];
  b.scheduled = false;
  let firstError;
  while (b.queue.length) {
    const q = b.queue.splice(0);
    for (const f of q) {
      try {
        f();
      } catch (error) {
        if (firstError === undefined) firstError = error;
      }
    }
  }
  if (firstError !== undefined) throw firstError;
}

function _batchRun(fn) {
  const b = globalThis[_B];
  b.depth++;
  try { fn(); }
  finally {
    if (--b.depth === 0 && b.queue.length) {
      _flushQueue();
    }
  }
}

// ─────────────────────────────────────────────
//  Stores
//
// Store ownership is centralised here: subscriptions are synchronous,
// unsubscription is idempotent, and fan-out iterates a snapshot.

function createWritable(value) {
  const subscribers = new Set();

  function notify() {
    let firstError;
    for (const run of [...subscribers]) {
      try {
        run(value);
      } catch (error) {
        if (firstError === undefined) firstError = error;
      }
    }
    if (firstError !== undefined) throw firstError;
  }

  function set(next) {
    if (next === value) return;
    value = next;
    _batchRun(notify);
  }

  return {
    subscribe(run) {
      subscribers.add(run);
      try {
        run(value);
      } catch (error) {
        subscribers.delete(run);
        throw error;
      }
      let active = true;
      return () => {
        if (!active) return;
        active = false;
        subscribers.delete(run);
      };
    },
    set,
    update(fn) {
      set(fn(value));
    },
    get() {
      return value;
    },
  };
}

/** A store whose value can be set from outside. */
export function writable(value) {
  return createWritable(value);
}

/**
 * A read-only store whose start callback runs once for the first subscriber
 * and whose cleanup runs once after the final unsubscribe.
 */
export function readable(value, start) {
  const store = createWritable(value);
  let subscriberCount = 0;
  let stop = null;

  return {
    subscribe(run) {
      const firstSubscriber = subscriberCount === 0;
      if (firstSubscriber && start) {
        const cleanup = start(store.set);
        stop = typeof cleanup === 'function' ? cleanup : null;
      }
      let unsubscribe;
      try {
        unsubscribe = store.subscribe(run);
      } catch (error) {
        if (firstSubscriber && stop) {
          const cleanup = stop;
          stop = null;
          cleanup();
        }
        throw error;
      }
      subscriberCount++;
      let active = true;
      return () => {
        if (!active) return;
        active = false;
        unsubscribe();
        subscriberCount--;
        if (subscriberCount === 0 && stop) {
          const cleanup = stop;
          stop = null;
          cleanup();
        }
      };
    },
    get: store.get,
  };
}

/**
 * A read-only value derived from one or more stores. Source subscriptions stay
 * live only while it has subscribers; destroy() is final and idempotent.
 */
export function derived(stores, fn, initial) {
  const single = !Array.isArray(stores);
  const sources = single ? [stores] : stores;
  const output = createWritable(initial);
  let subscriberCount = 0;
  let sourceUnsubs = [];
  let active = false;
  let destroyed = false;

  function start() {
    if (active || destroyed) return;
    active = true;
    const values = new Array(sources.length);
    const inited = new Array(sources.length).fill(false);
    try {
      sourceUnsubs = [];
      for (let index = 0; index < sources.length; index++) {
        const source = sources[index];
        sourceUnsubs.push(source.subscribe(value => {
          values[index] = value;
          inited[index] = true;
          if (inited.every(Boolean)) {
            output.set(single ? fn(values[0]) : fn(values));
          }
        }));
      }
    } catch (error) {
      for (const unsubscribe of sourceUnsubs) unsubscribe();
      sourceUnsubs = [];
      active = false;
      throw error;
    }
  }

  function stop() {
    if (!active) return;
    active = false;
    const unsubs = sourceUnsubs;
    sourceUnsubs = [];
    for (const unsubscribe of unsubs) unsubscribe();
  }

  return {
    subscribe(run) {
      const firstSubscriber = subscriberCount === 0;
      if (firstSubscriber) start();
      let unsubscribe;
      try {
        unsubscribe = output.subscribe(run);
      } catch (error) {
        if (firstSubscriber) stop();
        throw error;
      }
      subscriberCount++;
      let subscribed = true;
      return () => {
        if (!subscribed) return;
        subscribed = false;
        unsubscribe();
        subscriberCount--;
        if (subscriberCount === 0) stop();
      };
    },
    get: output.get,
    destroy() {
      if (destroyed) return;
      destroyed = true;
      stop();
    },
  };
}

// ─────────────────────────────────────────────
//  Component mounting helpers
// ─────────────────────────────────────────────

/**
 * mount(Component, { target, props }) — mounts a compiled Vela component.
 *
 * import { mount } from './runtime.js';
 * import { Counter } from './Counter.js';
 *
 * const app = mount(Counter, { target: document.body, props: { start: 5 } });
 * // later:
 * app.destroy();
 */
export function mount(Component, { target, props = {}, slots = {} } = {}) {
  if (!target) throw new Error('[Vela] mount() requires a target element');
  return Component(target, props, slots);
}

/**
 * tick() — returns a Promise that resolves after the current microtask queue
 * drains. Useful in tests or after imperative state changes.
 */
export function tick() {
  return Promise.resolve();
}

// ─────────────────────────────────────────────
//  Component helper: $$defer
//
//  AST-generated components import this helper explicitly. It is also
//  registered on globalThis for generated modules from the deprecated legacy
//  code generator, which did not emit a runtime import.
//
//  Behavior summary:
//    depth > 0  → push to queue; _batchRun's flush will drain it
//    depth === 0 → push to queue; schedule a microtask flush if not already
//                  scheduled.  Coalesces a chain of state changes into one
//                  redraw at the end of the current task.
//
//  The deprecated generator retains its own synchronous fallback when no
//  runtime module is loaded. That compatibility behaviour does not apply to
//  the AST generator: its named import guarantees this implementation runs.
// ─────────────────────────────────────────────

// _scheduleFlush asks the JS engine to run _flushQueue at the end of the
// current task.  queueMicrotask is the standard API; the Promise-then form
// is a fallback for older runtimes that lack it.  Both are microtask-class:
// they fire before the next macrotask (setTimeout, I/O), so flushed updates
// are visible before any subsequent user interaction.
const _scheduleFlush = (typeof queueMicrotask === 'function')
  ? queueMicrotask
  : (fn) => Promise.resolve().then(fn);

export function $$defer(fn) {
  const b = globalThis[Symbol.for('vela.batch')];
  b.queue.push(fn);
  if (b.depth > 0) {
    // Inside an explicit batch — _batchRun's depth-decrement will flush.
    return;
  }
  if (!b.scheduled) {
    b.scheduled = true;
    _scheduleFlush(_flushQueue);
  }
}
globalThis[Symbol.for('vela.defer')] = $$defer;


const DEV = typeof process !== 'undefined'
  ? process.env.NODE_ENV !== 'production'
  : !globalThis.__VELA_PROD__;

export function warn(msg) {
  if (DEV) console.warn('[Vela]', msg);
}


// ── Compiler runtime helpers ────────────────────────────────────
// These were previously inlined into every generated component.
// A compiled component imports whichever ones it uses, so the
// helper body ships once instead of per file.

// $$insertHTML parses an HTML string into `target`.  A
// DocumentFragment (nodeType 11) has no innerHTML setter, so the
// HTML is routed through a <template>; an element uses the faster
// insertAdjacentHTML.
export function $$insertHTML($$t, $$h) {
  if ($$t.nodeType === 11) {
    const $$tmp = document.createElement('template');
    $$tmp.innerHTML = $$h;
    $$t.appendChild($$tmp.content);
  } else {
    $$t.insertAdjacentHTML('beforeend', $$h);
  }
}

// $$proxy wraps a value in a recursive, WeakMap-cached Proxy so a
// nested mutation fires the same `notify` path as a whole-value
// reassignment.  Backs $state.deep.
//
// Notification is value-aware: assigning an equal value or deleting
// a missing key is a no-op. Native mutating Array methods use one
// local transaction, so their several Proxy traps publish at most
// one component invalidation.
export function $$proxy(val, notify) {
  if (val === null || typeof val !== 'object') return val;
  const cache = new WeakMap();
  const rawByProxy = new WeakMap();
  const arrayMethods = new WeakMap();
  let mutationDepth = 0;
  let mutationPending = false;

  const mutatingArrayMethods = new Set([
    'copyWithin', 'fill', 'pop', 'push', 'reverse', 'shift', 'sort',
    'splice', 'unshift',
  ]);

  const unwrap = (value) => rawByProxy.get(value) || value;
  const changed = (mutation) => {
    if (mutationDepth > 0) {
      mutationPending = true;
      return;
    }
    notify(mutation);
  };
  const runArrayMutation = (target, key, receiver, method, path) => {
    let methods = arrayMethods.get(target);
    if (!methods) {
      methods = new Map();
      arrayMethods.set(target, methods);
    }
    const cached = methods.get(key);
    if (cached && cached.method === method) return cached.wrapped;
    const wrapped = function(...args) {
      // Preserve native extraction/call semantics. Only a method invoked with
      // this proxy as its receiver owns this deep-state notification batch;
      // `const push = list.push; push()` must still throw like Array#push.
      if (this !== receiver) return Reflect.apply(method, this, args.map(unwrap));
      mutationDepth++;
      try {
        return Reflect.apply(method, receiver, args.map(unwrap));
      } finally {
        mutationDepth--;
        if (mutationDepth === 0 && mutationPending) {
          mutationPending = false;
          notify({ kind: 'array', path: path.slice(), method: key });
        }
      }
    };
    methods.set(key, { method, wrapped });
    return wrapped;
  };
  const wrap = (v, path = []) => {
    if (v === null || typeof v !== 'object') return v;
    // Exotic built-ins (Promise, Date, RegExp, Map, Set, WeakMap,
    // WeakSet, Error, ArrayBuffer / TypedArrays) rely on internal
    // slots that Proxy receivers cannot satisfy — wrapping them
    // breaks any method call ("Method Promise.prototype.then
    // called on incompatible receiver").  Return such values
    // unwrapped; deep reactivity for plain objects and arrays is
    // unaffected.
    if (v instanceof Promise) return v;
    if (v instanceof Date) return v;
    if (v instanceof RegExp) return v;
    if (v instanceof Map) return v;
    if (v instanceof Set) return v;
    if (v instanceof WeakMap) return v;
    if (v instanceof WeakSet) return v;
    if (v instanceof Error) return v;
    if (v instanceof ArrayBuffer) return v;
    if (ArrayBuffer.isView && ArrayBuffer.isView(v)) return v;
    let p = cache.get(v);
    if (p) return p;
    p = new Proxy(v, {
      get(t, k, receiver) {
        // Brand: lets a component prop setter recognise a deep
        // ($state.deep) proxy and skip the identity-equality guard
        // that would otherwise suppress a re-render after an
        // in-place mutation (the reference is unchanged but the
        // contents are not).
        if (k === $$PROXY_BRAND) return true;
        const value = Reflect.get(t, k, receiver);
        if (Array.isArray(t) && typeof k === 'string' &&
            mutatingArrayMethods.has(k) && typeof value === 'function') {
          return runArrayMutation(t, k, receiver, value, path);
        }
        return wrap(value, path.concat(k));
      },
      set(t, k, x, receiver) {
        const hadOwn = Object.prototype.hasOwnProperty.call(t, k);
        const before = Reflect.get(t, k, receiver);
        const ok = Reflect.set(t, k, unwrap(x), receiver);
        const hasOwn = Object.prototype.hasOwnProperty.call(t, k);
        if (ok && (hadOwn !== hasOwn ||
            !Object.is(before, Reflect.get(t, k, receiver)))) changed({ kind: 'set', path: path.concat(k) });
        return ok;
      },
      deleteProperty(t, k) {
        if (!Object.prototype.hasOwnProperty.call(t, k)) return true;
        const ok = Reflect.deleteProperty(t, k);
        if (ok) changed({ kind: 'delete', path: path.concat(k) });
        return ok;
      },
    });
    cache.set(v, p);
    rawByProxy.set(p, v);
    return p;
  };
  return wrap(val);
}

// $$PROXY_BRAND is a cross-realm symbol read by $$isProxy.  Using
// Symbol.for keeps it stable across module instances.
const $$PROXY_BRAND = Symbol.for('vela.proxy');

// $$isProxy reports whether v is a deep-reactive proxy produced by
// $$proxy.  A component prop setter uses this to decide whether an
// identity-equal new value might still have mutated in place.
export function $$isProxy(v) {
  return v !== null && typeof v === 'object' && v[$$PROXY_BRAND] === true;
}

// $$dispatch fires a bubbling CustomEvent from a component's mount
// target.  Exported as a (target, …) function; a generated
// component binds it to its own $$target.
export function $$dispatch($$target, name, detail) {
  $$target.dispatchEvent(
    new CustomEvent(name, { detail, bubbles: true }));
}

// $$createHydrationWalker indexes compiler-owned paired SSR markers without
// creating, moving, or deleting DOM nodes. Markers may nest, so validation is
// stack-based rather than a loose lookup by ID; crossed or duplicate ranges
// become actionable errors before a component attaches listeners or setters.
export function $$createHydrationWalker(root, componentId) {
  const label = componentId || 'unknown component';
  const doc = root && (root.ownerDocument || (root.nodeType === 9 ? root : null));
  if (!doc || typeof doc.createTreeWalker !== 'function') {
    throw new Error(`Vela hydration mismatch [${label}]: missing DOM root for marker walk`);
  }
  const walker = doc.createTreeWalker(root, 128); // NodeFilter.SHOW_COMMENT
  const markers = { v: new Map(), e: new Map() };
  const stack = [];
  const ignoredStructuralMarkers = [];
  let node;
  while ((node = walker.nextNode())) {
    const marker = String(node.data || '').trim();
    const match = /^\/?([ve]):([^:]+):(\d+)$/.exec(marker);
    if (!match) continue;
    const kind = match[1];
    if (match[2] !== componentId) continue;
    const id = Number(match[3]);
    const key = kind + ':' + id;
    const closing = marker[0] === '/';
    // Element marker IDs describe template positions, so an #each renders
    // the same inner e-id once per row. They are not global acquisition
    // targets while enclosed by a dynamic v-range; that range's own lowering
    // owns the active nodes. Keep validating its v boundaries while skipping
    // repeated structural element pairs.
    const repeatedStructuralMarker = !closing && stack.some(entry => entry.kind === 'v') && markers[kind].has(id);
    if (ignoredStructuralMarkers.length > 0 || repeatedStructuralMarker) {
      if (closing) {
        const open = ignoredStructuralMarkers.pop();
        if (!open || open.key !== key) {
          throw new Error(`Vela hydration mismatch [${label}]: structural marker end does not match its start`);
        }
      } else {
        ignoredStructuralMarkers.push({ key });
      }
      continue;
    }
    if (!closing) {
      if (markers[kind].has(id) || stack.some(entry => entry.key === key)) {
        throw new Error(`Vela hydration mismatch [${label}]: duplicate start marker <!--${kind}:${label}:${id}-->`);
      }
      stack.push({ kind, id, key, start: node });
      continue;
    }
    const open = stack.pop();
    if (!open) {
      throw new Error(`Vela hydration mismatch [${label}]: end marker <!--/${kind}:${label}:${id}--> has no start marker`);
    }
    if (open.key !== key) {
      throw new Error(`Vela hydration mismatch [${label}]: expected <!--/${open.kind}:${label}:${open.id}--> before <!--/${kind}:${label}:${id}-->`);
    }
    markers[kind].set(id, { start: open.start, end: node });
  }
  if (stack.length) {
    const open = stack[stack.length - 1];
    throw new Error(`Vela hydration mismatch [${label}]: start marker <!--${open.kind}:${label}:${open.id}--> has no end marker`);
  }
  if (ignoredStructuralMarkers.length !== 0) {
    throw new Error(`Vela hydration mismatch [${label}]: structural marker is not balanced`);
  }
  return { root, componentId: label, ranges: markers.v, elements: markers.e };
}

// $$acquireHydrationElement returns the existing element bracketed by one
// compiler-owned element range. It validates the tag rather than attempting a
// repair, so a stale server page cannot be silently adopted as another view.
export function $$acquireHydrationElement(walker, id, tag) {
  if (!walker || !(walker.elements instanceof Map)) {
    throw new Error('Vela hydration mismatch: invalid marker walker');
  }
  const range = walker.elements.get(id);
  if (!range) {
    throw new Error(`Vela hydration mismatch [${walker.componentId}]: missing element range e${id}`);
  }
  let node = range.start.nextSibling;
  while (node && node !== range.end && node.nodeType !== 1) node = node.nextSibling;
  if (!node || node === range.end) {
    throw new Error(`Vela hydration mismatch [${walker.componentId}]: element range e${id} contains no element`);
  }
  const actual = String(node.tagName || '').toLowerCase();
  const expected = String(tag || '').toLowerCase();
  if (actual !== expected) {
    throw new Error(`Vela hydration mismatch [${walker.componentId}]: element range e${id} expected <${expected}> but found <${actual || 'unknown'}>`);
  }
  return node;
}

// $$acquireHydrationRange returns an already indexed SSR range. A generated
// hydrate entry point uses this for dynamic text and structural blocks; it
// never repairs a mismatch by clearing or recreating the component tree.
export function $$acquireHydrationRange(walker, id) {
  if (!walker || !(walker.ranges instanceof Map)) {
    throw new Error('Vela hydration mismatch: invalid marker walker');
  }
  const range = walker.ranges.get(id);
  if (!range) {
    throw new Error(`Vela hydration mismatch [${walker.componentId}]: missing marker range v${id}`);
  }
  return range;
}

// $$adoptHydrationRange snapshots the nodes already rendered between paired
// markers. It performs no writes, so an SSR branch remains untouched until a
// later reactive update explicitly replaces that structural range.
export function $$adoptHydrationRange(range) {
  if (!range || !range.start || !range.end) {
    throw new Error('Vela hydration mismatch: invalid structural range');
  }
  const nodes = [];
  for (let node = range.start.nextSibling; node && node !== range.end; node = node.nextSibling) {
    nodes.push(node);
  }
  if (!range.end.parentNode || (!nodes.length && range.start.nextSibling !== range.end)) {
    throw new Error('Vela hydration mismatch: malformed structural range');
  }
  return nodes;
}

// $$findHydrationNestedRange resolves one repeated structural v-range inside
// an already adopted keyed row. The component-wide walker intentionally does
// not index repeated markers below another v-range, so this helper makes the
// enclosing row the ownership boundary and rejects ambiguous/malformed pairs.
export function $$findHydrationNestedRange(range, componentId, id) {
  if (!range?.start || !range?.end) {
    throw new Error('Vela hydration mismatch: invalid nested structural owner range');
  }
  const marker = String(componentId || '') + ':' + String(id);
  const startMarker = 'v:' + marker;
  const endMarker = '/v:' + marker;
  let start = null;
  for (let node = range.start.nextSibling; node && node !== range.end; node = node.nextSibling) {
    if (node.nodeType !== 8) continue;
    const data = String(node.data || '');
    if (data === startMarker) {
      if (start) throw new Error('Vela hydration mismatch: duplicate nested structural start marker');
      start = node;
      continue;
    }
    if (data === endMarker) {
      if (!start) throw new Error('Vela hydration mismatch: unmatched nested structural end marker');
      return { start, end: node };
    }
  }
  if (start) throw new Error('Vela hydration mismatch: nested structural start marker has no end marker');
  throw new Error(`Vela hydration mismatch: missing nested marker range v${id}`);
}

// $$adoptHydrationKeyedRows maps existing direct row elements to the initial
// iterable. It is intentionally strict: a stale SSR response with a different
// row count or duplicate key is rejected before keyed reconciliation can move
// an unrelated DOM node under the wrong item.
export function $$adoptHydrationKeyedRows(range, iterable, keyOf, tag) {
  if (typeof keyOf !== 'function') {
    throw new Error('Vela hydration mismatch: keyed row adoption requires a key function');
  }
  const values = Array.from(iterable || []);
  const expected = String(tag || '').toLowerCase();
  const rows = [];
  for (let node = range?.start?.nextSibling; node && node !== range.end; node = node.nextSibling) {
    if (node.nodeType !== 1) continue;
	// Ordinary keyed rows supply their element tag. A direct component row has
	// no parent-visible tag in its AST, so an empty expected tag accepts every
	// direct element between the range markers; capability analysis guarantees
	// one component root per row before generated code reaches this helper.
    const tagName = String(node.tagName || '').toLowerCase();
    if (!expected && tagName === 'script' && String(node.id || '').startsWith('vela-state-')) continue;
    if (expected && tagName !== expected) continue;
    rows.push(node);
  }
  if (rows.length !== values.length) {
    throw new Error(`Vela hydration mismatch: keyed row count ${rows.length} does not match iterable length ${values.length}`);
  }
  const blocks = new Map();
  for (let index = 0; index < values.length; index++) {
    const key = keyOf(values[index], index);
    if (blocks.has(key)) {
      throw new Error(`Vela hydration mismatch: duplicate keyed row ${String(key)}`);
    }
    blocks.set(key, rows[index]);
  }
  return blocks;
}

// $$adoptHydrationKeyedRowRanges is the multi-root successor to element-row
// adoption. SSR emits r markers around every keyed iteration, so static
// siblings, several child roots, and payload scripts move as one row without
// a synthetic wrapper element.
export function $$adoptHydrationKeyedRowRanges(range, iterable, keyOf, markerID) {
  if (!range?.start || !range?.end || typeof keyOf !== 'function') throw new Error('Vela hydration mismatch: invalid keyed row range adoption');
  const values = Array.from(iterable || []), rows = [], suffix = ':' + String(markerID);
  let start = null;
  for (let node = range.start.nextSibling; node && node !== range.end; node = node.nextSibling) {
    if (node.nodeType !== 8) continue;
    const data = String(node.data || '');
    if (data.startsWith('r:') && data.endsWith(suffix)) { if (start) throw new Error('Vela hydration mismatch: nested keyed row marker'); start = node; continue; }
    if (data.startsWith('/r:') && data.endsWith(suffix)) { if (!start) throw new Error('Vela hydration mismatch: unmatched keyed row marker'); rows.push({start, end: node}); start = null; }
  }
  if (start) throw new Error('Vela hydration mismatch: unmatched keyed row marker');
  // Older SSR output predates row markers. Keep its single-element contract
  // hydratable while all newly rendered keyed rows take the strict range path.
  if (rows.length === 0) {
    for (let node = range.start.nextSibling; node && node !== range.end; node = node.nextSibling) {
      if (node.nodeType === 1 && !(String(node.tagName || '').toLowerCase() === 'script' && String(node.id || '').startsWith('vela-state-'))) rows.push({start: node, end: node});
    }
  }
  if (rows.length !== values.length) throw new Error(`Vela hydration mismatch: keyed row range count ${rows.length} does not match iterable length ${values.length}`);
  const blocks = new Map();
  for (let index = 0; index < values.length; index++) { const key = keyOf(values[index], index); if (blocks.has(key)) throw new Error(`Vela hydration mismatch: duplicate keyed row ${String(key)}`); blocks.set(key, rows[index]); }
  return blocks;
}

export function $$moveHydrationKeyedRowRange(end, row) {
  if (!end || !row?.start || !row?.end) throw new Error('Vela hydration mismatch: invalid keyed row range');
  const nodes = [];
  for (let node = row.start; node; node = node.nextSibling) { nodes.push(node); if (node === row.end) break; }
  if (nodes[nodes.length - 1] !== row.end) throw new Error('Vela hydration mismatch: detached keyed row range');
  for (const node of nodes) end.before(node);
}

export function $$removeHydrationKeyedRowRange(row) {
  if (!row?.start || !row?.end) throw new Error('Vela hydration mismatch: invalid keyed row range');
  for (let node = row.start; node;) {
    const next = node.nextSibling;
    node.remove();
    if (node === row.end) return;
    node = next;
  }
  throw new Error('Vela hydration mismatch: detached keyed row range');
}

// $$replaceHydrationKeyedRowContents replaces only the nodes enclosed by an
// existing keyed row boundary. The boundary itself remains stable so the
// reconciler can still reorder the retained key without remounting it.
export function $$replaceHydrationKeyedRowContents(row, fragment) {
  if (!row?.start || !row?.end || !row.end.parentNode) {
    throw new Error("Vela hydration mismatch: invalid keyed row replacement");
  }
  for (let node = row.start.nextSibling; node && node !== row.end;) {
    const next = node.nextSibling;
    node.remove();
    node = next;
  }
  if (row.start.nextSibling !== row.end) {
    throw new Error("Vela hydration mismatch: detached keyed row range");
  }
  row.end.before(fragment);
}

// $$reconcileHydrationKeyedRowRanges is the range-aware counterpart of the
// legacy element reconciler. `create` returns {start,end}; range boundaries
// travel with every child root and inert payload script as one keyed row.
export function $$reconcileHydrationKeyedRowRanges(range, blocks, iterable, keyOf, create, update) {
  if (!range?.end || !(blocks instanceof Map) || typeof keyOf !== 'function' || typeof create !== 'function') throw new Error('Vela hydration mismatch: invalid keyed row range reconciliation inputs');
  const next = new Map(); let index = 0;
  for (const item of iterable || []) {
    const key = keyOf(item, index);
    if (next.has(key)) throw new Error(`Vela keyed update has duplicate key ${String(key)}`);
    let row = blocks.get(key);
    if (row) { if (typeof update === 'function') update(row, item, index); }
    else row = create(item, index);
    $$moveHydrationKeyedRowRange(range.end, row);
    next.set(key, row); index++;
  }
  for (const [key, row] of blocks) if (!next.has(key)) { if (typeof row?.controller?.destroy === 'function') row.controller.destroy(); $$removeHydrationKeyedRowRange(row); }
  return next;
}

// $$clearHydrationKeyedRowMarkers removes only direct compiler element
// markers around adopted keyed rows. It is called immediately before the
// first keyed update, never during hydration, so initial SSR DOM remains
// untouched. Nested `v` markers belong to row text and are deliberately not
// traversed here.
export function $$clearHydrationKeyedRowMarkers(range) {
  if (!range?.start || !range?.end) {
    throw new Error('Vela hydration mismatch: invalid keyed row range');
  }
  for (let node = range.start.nextSibling; node && node !== range.end;) {
    const next = node.nextSibling;
    if (node.nodeType === 8 && /^(?:e:|\/e:)/.test(String(node.data || ''))) node.remove();
    node = next;
  }
}

// $$reconcileHydrationKeyedRows preserves existing row elements by key and
// places them in iterable order immediately before the structural end marker.
// Callers provide typed generated create/update callbacks; this runtime never
// evaluates template source or manufactures HTML from user expressions.
export function $$reconcileHydrationKeyedRows(range, blocks, iterable, keyOf, create, update) {
  if (!range?.end || !(blocks instanceof Map) || typeof keyOf !== 'function' || typeof create !== 'function') {
    throw new Error('Vela hydration mismatch: invalid keyed row reconciliation inputs');
  }
  // Moving an already-focused element within a parent makes Chromium clear
  // document.activeElement even though the keyed row itself is retained.
  // Preserve that native interaction state only for a retained old row; a
  // removed row must never be focused again after reconciliation.
  const doc = range.end.ownerDocument;
  const active = doc && doc.activeElement;
  const restoreFocus = active && Array.from(blocks.values()).some(row => row === active || row?.contains?.(active));
  const next = new Map();
  let index = 0;
  for (const item of iterable || []) {
    const key = keyOf(item, index);
    if (next.has(key)) throw new Error(`Vela keyed update has duplicate key ${String(key)}`);
    let row = blocks.get(key);
    if (row) {
      if (typeof update === 'function') update(row, item, index);
    } else {
      row = create(item, index);
    }
    if (!row || row.nodeType !== 1) throw new Error(`Vela keyed update cannot materialize key ${String(key)}`);
    range.end.before(row);
    next.set(key, row);
    index++;
  }
  for (const [key, row] of blocks) {
    if (!next.has(key)) row.remove();
  }
  if (restoreFocus && active.isConnected && typeof active.focus === 'function') active.focus();
  return next;
}

// $$reconcileHydrationKeyedElements is the marker-free counterpart of the
// row reconciler for a typed element collection already owned by one retained
// parent. It never evaluates template source: callers provide cached-AST
// key/create/update callbacks. Appending a retained element moves it into the
// requested order without replacing its identity.
export function $$reconcileHydrationKeyedElements(parent, blocks, iterable, keyOf, create, update) {
  if (!parent || parent.nodeType !== 1 || !(blocks instanceof Map) ||
      typeof keyOf !== 'function' || typeof create !== 'function') {
    throw new Error('Vela hydration mismatch: invalid keyed element reconciliation inputs');
  }
  const next = new Map();
  let index = 0;
  for (const item of iterable || []) {
    const key = keyOf(item, index);
    if (next.has(key)) throw new Error(`Vela keyed update has duplicate key ${String(key)}`);
    let element = blocks.get(key);
    if (element) {
      if (typeof update === 'function') update(element, item, index);
    } else {
      element = create(item, index);
    }
    if (!element || element.nodeType !== 1) {
      throw new Error(`Vela keyed update cannot materialize key ${String(key)}`);
    }
    parent.append(element);
    next.set(key, element);
    index++;
  }
  for (const [key, element] of blocks) {
    if (!next.has(key)) element.remove();
  }
  return next;
}

// $$writeHydrationText updates exactly one text range after hydration. The
// initial server text is left untouched; an empty server value gets a text
// node only when a later reactive update first becomes non-empty.
export function $$writeHydrationText(range, value) {
  if (!range || !range.start || !range.end) {
    throw new Error('Vela hydration mismatch: invalid text range');
  }
  const next = String(value);
  const first = range.start.nextSibling;
  if (first === range.end) {
    if (next === '') return;
    const parent = range.end.parentNode;
    const doc = range.end.ownerDocument || (parent && parent.ownerDocument);
    if (!parent || !doc || typeof doc.createTextNode !== 'function') {
      throw new Error('Vela hydration mismatch: cannot materialize text range');
    }
    parent.insertBefore(doc.createTextNode(next), range.end);
    return;
  }
  if (first.nodeType !== 3 || first.nextSibling !== range.end) {
    throw new Error('Vela hydration mismatch: text range contains unexpected DOM nodes');
  }
  if (first.data !== next) first.data = next;
}

// $$readHydrationPayload reads the inert server payload and verifies that it
// belongs to this component. Incomplete payloads are explicitly rejected: a
// future hydrator must not silently recreate state that SSR could not encode.
export function $$readHydrationPayload(root, componentId) {
  const label = componentId || 'unknown component';
  const doc = root && (root.ownerDocument || (root.nodeType === 9 ? root : null));
  const payloadNode = doc && typeof doc.getElementById === 'function'
    ? doc.getElementById(`vela-state-${componentId}`) : null;
  if (!payloadNode) {
    throw new Error(`Vela hydration mismatch [${label}]: missing state payload`);
  }
  if (payloadNode.type !== 'application/json') {
    throw new Error(`Vela hydration mismatch [${label}]: state payload is not application/json`);
  }
  let payload;
  try {
    payload = JSON.parse(payloadNode.textContent || '');
  } catch {
    throw new Error(`Vela hydration mismatch [${label}]: state payload is not valid JSON`);
  }
  if (!payload || payload.component !== componentId) {
    throw new Error(`Vela hydration mismatch [${label}]: state payload belongs to another component`);
  }
  if (payload.incomplete) {
    const omitted = Array.isArray(payload.omitted) && payload.omitted.length
      ? `; omitted state: ${payload.omitted.join(', ')}` : '';
    throw new Error(`Vela hydration mismatch [${label}]: state payload is incomplete${omitted}`);
  }
  return payload;
}

// $$eachRowsForMutations returns the distinct row indexes that can be safely
// refreshed without a full keyed reconciliation. null is the conservative
// fallback: structural array changes, key writes, unknown records, and empty
// batches must retain the normal keyed diff.
export function $$eachRowsForMutations(mutations, keyField) {
  if (!Array.isArray(mutations) || mutations.length === 0) return null;
  const rows = new Set();
  for (const mutation of mutations) {
    if (!mutation || (mutation.kind !== "set" && mutation.kind !== "delete") ||
        !Array.isArray(mutation.path) || mutation.path.length < 2) return null;
    const [rawIndex, field] = mutation.path;
    const index = typeof rawIndex === "string" ? Number(rawIndex) : rawIndex;
    if (!Number.isInteger(index) || index < 0 || field === keyField) return null;
    rows.add(index);
  }
  return [...rows];
}
