// Fragment: the lifecycle hooks of `vela`. The compiler wires them into the
// component; the runtime package does not export them. See core.d.ts.

/**
 * Runs fn after the component is mounted. A function it returns runs when
 * the component is destroyed.
 */
export function onMount(fn: () => void | (() => void) | Promise<void>): void;

/** Runs fn when the component is destroyed. */
export function onDestroy(fn: () => void): void;
