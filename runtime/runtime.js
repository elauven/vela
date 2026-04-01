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
//  $$defer pushes fn to the queue when depth > 0, or runs it immediately.
//  After all subscribers finish the depth drops to 0 and the queue flushes —
//  by which point every state variable in the notification is current.
//
//  The context is shared via Symbol.for so generated components can access it
//  without needing an explicit import path to the runtime.
// ─────────────────────────────────────────────

const _B = Symbol.for('vela.batch');
if (!globalThis[_B]) {
  globalThis[_B] = { depth: 0, queue: [] };
}

function _batchRun(fn) {
  const b = globalThis[_B];
  b.depth++;
  try { fn(); }
  finally {
    if (--b.depth === 0 && b.queue.length) {
      const q = b.queue.splice(0);
      for (const f of q) f();
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
//  Dev helpers (stripped in production builds)
// ─────────────────────────────────────────────

const DEV = typeof process !== 'undefined'
  ? process.env.NODE_ENV !== 'production'
  : !globalThis.__VELA_PROD__;

export function warn(msg) {
  if (DEV) console.warn('[Vela]', msg);
}
