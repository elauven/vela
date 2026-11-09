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

// $$runtimeContract1 is the runtime/compiler contract marker (R-12.2d).
// Every generated module that uses the runtime imports it, so a compiler and
// runtime of different contract versions fail at module load. The compiler
// changes the name when generated code needs a new or changed runtime API.
export const $$runtimeContract1 = true;

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
// Select multiple bindings
//
// A multi-select is intentionally modelled as an array of strings. Its DOM
// semantics are membership-based: duplicate values in the model do not create
// duplicate selections, while duplicate option values (if authored) receive
// the same membership. Reading preserves DOM order, which gives application
// code a stable snapshot after a user-driven change.
//
// $$writeSelectValues is guarded at the per-option property level. That makes
// it safe for hydration and for owner-local keyed option reconciliation: a
// retained option is not touched unless its selected state actually changes.

export function $$readSelectValues(select) {
  const values = [];
  for (const option of select.options) {
    if (option.selected) values.push(String(option.value));
  }
  return values;
}

// Hydration compares a multiple select by membership, not by the raw array
// returned from the DOM. Duplicate option values therefore do not replace an
// equivalent application model merely because the document contains the same
// selected value more than once.
export function $$selectValuesEqual(select, values) {
  const expected = new Set(Array.isArray(values) ? values.map(String) : []);
  const actual = new Set();
  for (const option of select.options) {
    if (!option.selected) continue;
    const value = String(option.value);
    if (!expected.has(value)) return false;
    actual.add(value);
  }
  return actual.size === expected.size;
}

export function $$writeSelectValues(select, values) {
  const selected = new Set(Array.isArray(values) ? values.map(String) : []);
  let changed = false;
  for (const option of select.options) {
    const next = selected.has(String(option.value));
    if (option.selected !== next) {
      option.selected = next;
      changed = true;
    }
  }
  return changed;
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
  return $$contextRun(null, () => Component(target, props, slots));
}

// ─────────────────────────────────────────────
//  Context (task 090)
//
//  A context frame belongs to one component instance: { parent, map, open }.
//  The runtime opens a frame around every component factory call
//  ($$contextRun: $$component, mount(), hydrate()); the component's script
//  runs inside it, so setContext and getContext during initialisation find
//  it on top of the stack. $$mountComponent closes the frame before the
//  template renders ($$contextClaim) and hands the context on to the root
//  cursor, which passes it to every block, slot and child component of the
//  template, also those mounted later. A call while the frame on top is
//  closed (the template, onMount, a child's props) throws.
//
//  A root factory called directly (`App(target, props)`) has no frame: its
//  setContext creates a root frame that its $$mountComponent claims. With
//  no frame, getContext finds only that root frame's values. Development
//  builds warn about a root frame nobody claimed: setContext called in a
//  handler or an effect.
//  Context values are not reactive; keys are strings.
// ─────────────────────────────────────────────

const $$contextStack = [];
let $$contextRoot = null;

// $$contextRun runs fn (a component factory call) in a new frame whose
// parent is the context ctx of the mount position (null at a root).
export function $$contextRun(ctx, fn) {
  $$contextStack.push({ parent: ctx || null, map: null, open: true });
  try {
    return fn();
  } finally {
    $$contextStack.pop();
  }
}

// $$contextClaim closes the frame of the component that is mounting its
// template and returns the context its template passes on: the frame when
// the component set a value, else its parent.
export function $$contextClaim() {
  const frame = $$contextStack[$$contextStack.length - 1];
  if (!frame) {
    const root = $$contextRoot;
    $$contextRoot = null;
    if (root) root.open = false;
    return root;
  }
  if (!frame.open) return null;
  frame.open = false;
  return frame.map ? frame : frame.parent;
}

// $$contextParent is the context of the current mount position: the parent
// of the frame on top, for a factory that calls another factory in place.
export function $$contextParent() {
  const frame = $$contextStack[$$contextStack.length - 1];
  return frame ? frame.parent : null;
}

// initFrame returns the frame of the initialising component, or null for a
// directly called root that has set nothing (create: make its root frame).
function initFrame(name, key, create) {
  if (typeof key !== 'string') {
    throw new TypeError(`vela: ${name}() needs a string key, got ${key === null ? 'null' : typeof key}`);
  }
  const frame = $$contextStack[$$contextStack.length - 1];
  if (frame) {
    if (!frame.open) {
      throw new Error(`vela: ${name}() is called outside component initialisation; call it in the component script, or in a function the script calls while the component starts (VELA-CONTEXT-OUTSIDE-INIT)`);
    }
    return frame;
  }
  if (!$$contextRoot && create) {
    const root = { parent: null, map: null, open: true };
    $$contextRoot = root;
    if (DEV) {
      _scheduleFlush(() => {
        if ($$contextRoot !== root) return;
        $$contextRoot = null;
        warn(`${name}() was called outside component initialisation (VELA-CONTEXT-OUTSIDE-INIT); the value reaches no component`);
      });
    }
  }
  return $$contextRoot;
}

function lookup(frame, key) {
  for (let ctx = frame; ctx; ctx = ctx.parent) {
    if (ctx.map && ctx.map.has(key)) return ctx;
  }
  return null;
}

/** setContext(key, value) makes value visible to the component's descendants. */
export function setContext(key, value) {
  const frame = initFrame('setContext', key, true);
  if (!frame.map) frame.map = new Map();
  frame.map.set(key, value);
  return value;
}

/** getContext(key) returns the value the nearest ancestor set for key. */
export function getContext(key) {
  const owner = lookup(initFrame('getContext', key, false), key);
  return owner ? owner.map.get(key) : undefined;
}

/** hasContext(key) reports whether an ancestor set a value for key. */
export function hasContext(key) {
  return lookup(initFrame('hasContext', key, false), key) !== null;
}

/**
 * tick() — returns a Promise that resolves after the current microtask queue
 * drains. Useful in tests or after imperative state changes.
 */
// ── Lazy components (task 071) ─────────────────────────────────
// lazy(() => import('./X.vela')) is a component value for <vela:component>
// whose module loads on demand. The compiler adds the component ID as the
// second argument and also registers every lazy component of a module when
// the module is evaluated, so that preloadHydration() finds them by the IDs
// of the payload's "d" entries. One ID has one lazy value: it is the
// identity <vela:component> compares, before and after loading.
const $$lazyRegistry = new Map();

export function lazy(loader, id) {
  if (id && $$lazyRegistry.has(id)) return $$lazyRegistry.get(id);
  let pending = null;
  const value = {
    $$lazy: true,
    id: id || '',
    loaded: false,
    component: null,
    load() {
      if (value.loaded) return Promise.resolve(value.component);
      if (!pending) {
        pending = Promise.resolve()
          .then(loader)
          .then((module) => {
            const component = typeof module === 'function' ? module : module && module.default;
            if (typeof component !== 'function') {
              throw new TypeError(`vela: lazy component ${value.id || '(unnamed)'}: the module exports no component`);
            }
            value.component = component;
            value.loaded = true;
            return component;
          })
          .catch((error) => {
            // A later load() tries again.
            pending = null;
            throw error;
          });
      }
      return pending;
    },
  };
  if (id) $$lazyRegistry.set(id, value);
  return value;
}

// $$lazyById returns the registered lazy value of a component ID.
export function $$lazyById(id) {
  return $$lazyRegistry.get(id) || null;
}

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


const DEV = process.env.NODE_ENV !== 'production';

export function warn(msg) {
  if (DEV) console.warn('[Vela]', msg);
}

// $$isDev reports a development build; production builds replace DEV with
// a constant, so development-only code becomes dead code.
export function $$isDev() {
  return DEV;
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
//
// A value that already is a deep-state proxy (a $state.deep object of
// another component, typically read from context, task 090) is shared, not
// wrapped again: notify joins its subscribers, so a change by any holder
// updates every component that declared it as its $state.deep. Shared
// subscribers get no mutation record (their keyed lists diff in full) and
// are held weakly, so destroyed components do not accumulate.
export function $$proxy(val, notify) {
  if (val === null || typeof val !== 'object') return val;
  const share = val[$$PROXY_SHARE];
  if (typeof share === 'function') {
    share(notify);
    return val;
  }
  const cache = new WeakMap();
  // Subscribers that share this state ($$proxy of an existing proxy).
  const shared = new Set();
  const subscribe = (listener) => {
    if (listener === notify) return;
    shared.add(typeof WeakRef === 'function' ? new WeakRef(listener) : { deref: () => listener });
  };
  const publish = (mutation) => {
    notify(mutation);
    if (shared.size === 0) return;
    for (const ref of [...shared]) {
      const listener = ref.deref();
      if (listener) listener();
      else shared.delete(ref);
    }
  };
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
    publish(mutation);
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
          publish({ kind: 'array', path: path.slice(), method: key });
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
        if (k === $$PROXY_SHARE) return subscribe;
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
// $$PROXY_SHARE reads the subscribe function of a deep-state proxy.
const $$PROXY_SHARE = Symbol.for('vela.proxy.share');

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

// ─────────────────────────────────────────────
//  Transitions and animations (task 096)
//
//  `vela/transition` (also `vela/animate` and `vela`): functions for
//  transition:, in:, out: and animate:. A transition is
//  (node, params, { direction }) => { delay, duration, easing, css }, an
//  animation (node, { from, to }, params) => the same; css(t, u) returns
//  the inline CSS at t (0 hidden, 1 shown; u = 1 - t). The runtime plays
//  them with the Web Animations API.
// ─────────────────────────────────────────────

export function linear(t) {
  return t;
}

export function cubicOut(t) {
  const f = t - 1;
  return f * f * f + 1;
}

export function cubicInOut(t) {
  return t < 0.5 ? 4 * t * t * t : 0.5 * Math.pow(2 * t - 2, 3) + 1;
}

function computedStyle(node) {
  return getComputedStyle(node);
}

function baseTransform(style) {
  return style.transform === 'none' || !style.transform ? '' : style.transform + ' ';
}

export function fade(node, { delay = 0, duration = 400, easing = linear } = {}) {
  const opacity = +computedStyle(node).opacity || 1;
  return { delay, duration, easing, css: (t) => `opacity: ${t * opacity}` };
}

export function fly(node, { delay = 0, duration = 400, easing = cubicOut, x = 0, y = 0, opacity = 0 } = {}) {
  const style = computedStyle(node);
  const target = +style.opacity || 1;
  const transform = baseTransform(style);
  const delta = target * (1 - opacity);
  const length = (value) => (typeof value === 'number' ? value + 'px' : value);
  return {
    delay,
    duration,
    easing,
    css: (t, u) => `transform: ${transform}translate(calc(${length(x)} * ${u}), calc(${length(y)} * ${u})); opacity: ${target - delta * u}`,
  };
}

export function scale(node, { delay = 0, duration = 400, easing = cubicOut, start = 0, opacity = 0 } = {}) {
  const style = computedStyle(node);
  const target = +style.opacity || 1;
  const transform = baseTransform(style);
  const scaleDelta = 1 - start;
  const opacityDelta = target * (1 - opacity);
  return {
    delay,
    duration,
    easing,
    css: (t, u) => `transform: ${transform}scale(${1 - scaleDelta * u}); opacity: ${target - opacityDelta * u}`,
  };
}

export function slide(node, { delay = 0, duration = 400, easing = cubicOut, axis = 'y' } = {}) {
  const style = computedStyle(node);
  const opacity = +style.opacity || 1;
  const vertical = axis === 'y';
  const size = vertical ? 'height' : 'width';
  const sides = vertical ? ['top', 'bottom'] : ['left', 'right'];
  const measured = parseFloat(style[size]) || 0;
  const parts = [];
  for (const side of sides) {
    parts.push([`padding-${side}`, parseFloat(style[`padding${side[0].toUpperCase()}${side.slice(1)}`]) || 0]);
    parts.push([`margin-${side}`, parseFloat(style[`margin${side[0].toUpperCase()}${side.slice(1)}`]) || 0]);
    parts.push([`border-${side}-width`, parseFloat(style[`border${side[0].toUpperCase()}${side.slice(1)}Width`]) || 0]);
  }
  return {
    delay,
    duration,
    easing,
    css: (t) =>
      `overflow: hidden; opacity: ${Math.min(t * 20, 1) * opacity}; ${size}: ${t * measured}px; ` +
      parts.map(([name, value]) => `${name}: ${t * value}px`).join('; '),
  };
}

export function flip(node, { from, to }, { delay = 0, duration = (distance) => Math.sqrt(distance) * 120, easing = cubicOut } = {}) {
  const style = computedStyle(node);
  const transform = baseTransform(style);
  const [originX, originY] = (style.transformOrigin || '0 0').split(' ').map(parseFloat);
  const ox = originX || 0;
  const oy = originY || 0;
  const scaleX = to.width ? from.width / to.width : 1;
  const scaleY = to.height ? from.height / to.height : 1;
  const dx = from.left + ox * scaleX - (to.left + ox);
  const dy = from.top + oy * scaleY - (to.top + oy);
  const distance = Math.sqrt(dx * dx + dy * dy);
  return {
    delay,
    duration: typeof duration === 'function' ? duration(distance) : duration,
    easing,
    css: (t, u) => `transform: ${transform}translate(${u * dx}px, ${u * dy}px) scale(${t + u * scaleX}, ${t + u * scaleY})`,
  };
}
