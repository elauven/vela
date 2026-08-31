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
  const ranges = new Map();
  const stack = [];
  let node;
  while ((node = walker.nextNode())) {
    const marker = String(node.data || '').trim();
    const match = /^\/?v:([^:]+):(\d+)$/.exec(marker);
    if (!match) continue;
    if (match[1] !== componentId) continue;
    const id = Number(match[2]);
    const closing = marker[0] === '/';
    if (!closing) {
      if (ranges.has(id) || stack.some(entry => entry.id === id)) {
        throw new Error(`Vela hydration mismatch [${label}]: duplicate start marker <!--v:${label}:${id}-->`);
      }
      stack.push({ id, start: node });
      continue;
    }
    const open = stack.pop();
    if (!open) {
      throw new Error(`Vela hydration mismatch [${label}]: end marker <!--/v:${label}:${id}--> has no start marker`);
    }
    if (open.id !== id) {
      throw new Error(`Vela hydration mismatch [${label}]: expected <!--/v:${label}:${open.id}--> before <!--/v:${label}:${id}-->`);
    }
    ranges.set(id, { start: open.start, end: node });
  }
  if (stack.length) {
    const open = stack[stack.length - 1];
    throw new Error(`Vela hydration mismatch [${label}]: start marker <!--v:${label}:${open.id}--> has no end marker`);
  }
  return { root, componentId: label, ranges };
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
