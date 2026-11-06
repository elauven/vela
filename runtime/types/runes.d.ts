// The runes of component scripts (TS-6.2). The compiler replaces them; they
// exist only in `.vela` scripts.

/** Reactive state: assignments update the template. */
declare function $state<T>(initial: T): T;
/** Reactive state that is undefined until assigned. */
declare function $state<T>(): T | undefined;
declare namespace $state {
  /** Reactive state whose nested objects and arrays are observed too. */
  function deep<T>(initial: T): T;
  function deep<T>(): T | undefined;
}

/** A value computed from reactive state; the expression must be pure. */
declare function $derived<T>(expression: T): T;

/**
 * Runs fn after mount and again when the state it read changes. A function
 * it returns runs before the next run and on destroy.
 */
declare function $effect(fn: () => void | (() => void)): void;

/** A prop the parent may bind with `bind:`; fallback is its default. */
declare function $bindable<T>(fallback: T): T;
declare function $bindable<T>(): T | undefined;

/**
 * Dispatches the component event name to the parent's `@on:name` handler,
 * as a CustomEvent with detail.
 */
declare function $$dispatch(name: string, detail?: any): void;
