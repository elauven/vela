// Fragment: the stores of `vela/store` (also exported by `vela` and
// `@govela/runtime`). See core.d.ts.

/** Removes a subscription. Idempotent. */
export type Unsubscriber = () => void;

/** A store that can be read and subscribed to. */
export interface Readable<T> {
  /** Calls run with the current value now and with every later value. */
  subscribe(run: (value: T) => void): Unsubscriber;
  /** The current value. */
  get(): T;
}

/** A store whose value can be set from outside. */
export interface Writable<T> extends Readable<T> {
  set(value: T): void;
  update(fn: (value: T) => T): void;
}

/** A store computed from other stores. */
export interface Derived<T> extends Readable<T> {
  /** Unsubscribes from the sources for good. Idempotent. */
  destroy(): void;
}

/** The values of the stores of `derived()`: one value, or a tuple of them. */
export type StoresValues<S> = S extends Readable<infer T>
  ? T
  : { [K in keyof S]: S[K] extends Readable<infer T> ? T : never };

export function writable<T>(value: T): Writable<T>;
export function writable<T = undefined>(): Writable<T | undefined>;

/**
 * A read-only store. start runs for the first subscriber and may return a
 * cleanup, which runs after the last unsubscribes.
 */
export function readable<T>(value: T, start?: (set: (value: T) => void) => void | (() => void)): Readable<T>;

/**
 * A store computed by fn from one store or a list of stores. Before its
 * first subscriber, get() returns initial.
 */
export function derived<S extends Readable<any> | readonly Readable<any>[], T>(
  stores: S,
  fn: (values: StoresValues<S>) => T,
  initial?: T,
): Derived<T>;
