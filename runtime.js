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
//  The context is shared via Symbol.for so generated components can access it
//  without needing an explicit import path to the runtime.
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
  while (b.queue.length) {
    const q = b.queue.splice(0);
    for (const f of q) f();
  }
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
// ─────────────────────────────────────────────

/**
 * writable(initial) — a store whose value can be set from outside.
 *
 * const count = writable(0);
 * count.subscribe(v => console.log(v)); // → 0
 * count.set(1);                         // → 1
 * count.update(n => n + 1);             // → 2
 */
export function writable(value) {
  let subs = [];

  function notify() {
    for (const fn of subs) fn(value);
  }

  return {
    subscribe(fn) {
      subs.push(fn);
      fn(value);
      return () => {
        subs = subs.filter(s => s !== fn);
      };
    },
    set(v) {
      if (v !== value) {
        value = v;
        _batchRun(() => notify());
      }
    },
    update(fn) {
      const next = fn(value);
      if (next !== value) {
        value = next;
        _batchRun(() => notify());
      }
    },
    get() {
      return value;
    },
  };
}

/**
 * readable(initial, start?) — a store whose value can only be read externally.
 * `start` receives a `set` function and may return a cleanup callback.
 *
 * const time = readable(new Date(), set => {
 *   const id = setInterval(() => set(new Date()), 1000);
 *   return () => clearInterval(id);
 * });
 */
export function readable(value, start) {
  const store = writable(value);
  let stop;

  return {
    subscribe(fn) {
      if (subs(store) === 0 && start) {
        stop = start(store.set) || (() => {});
      }
      const unsub = store.subscribe(fn);
      return () => {
        unsub();
        if (subs(store) === 0 && stop) {
          stop();
          stop = null;
        }
      };
    },
    get: store.get,
  };
}

function subs(store) {
  // Count active subscribers — not exposed externally
  let count = 0;
  const unsub = store.subscribe(() => count++);
  unsub();
  return count - 1; // subtract the probe itself
}

/**
 * derived(stores, fn) — a read-only store computed from one or more stores.
 *
 * const doubled = derived(count, n => n * 2);
 * const sum     = derived([a, b], ([a, b]) => a + b);
 */
export function derived(stores, fn, initial) {
  const single = !Array.isArray(stores);
  const sources = single ? [stores] : stores;
  const store = writable(initial);

  let values = new Array(sources.length).fill(undefined);
  let inited = new Array(sources.length).fill(false);
  let ready = false;

  const unsubs = sources.map((src, i) =>
    src.subscribe(v => {
      values[i] = v;
      inited[i] = true;
      ready = inited.every(Boolean);
      if (ready) {
        store.set(single ? fn(values[0]) : fn(values));
      }
    })
  );

  return {
    subscribe: store.subscribe,
    get: store.get,
    destroy() {
      unsubs.forEach(u => u());
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
//  Registered on globalThis via Symbol so compiled components can use it
//  without an explicit import path (components don't import runtime directly —
//  they import stores, which import runtime, which registers $$defer first).
//
//  Behavior summary:
//    depth > 0  → push to queue; _batchRun's flush will drain it
//    depth === 0 → push to queue; schedule a microtask flush if not already
//                  scheduled.  Coalesces a chain of state changes into one
//                  redraw at the end of the current task.
//
//  The fallback inside a compiled component is `?? (fn => fn())`, used only
//  when the component runs before runtime.js has loaded.  That synchronous
//  fallback isn't great but is the only option without runtime present.
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
  if (!b) {
    // Runtime not loaded yet — last-resort synchronous fallback.  Should
    // not happen in normal use; would mean a compiled component ran before
    // runtime.js evaluated.
    fn();
    return;
  }
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
