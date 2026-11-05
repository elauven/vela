// Assertions of the type tests.

/** True when A and B are the same type. */
export type Equal<A, B> = (<T>() => T extends A ? 1 : 2) extends <T>() => T extends B ? 1 : 2 ? true : false;

/** Compiles only when T is true: expectTrue<Equal<A, B>>(). */
export function expectTrue<T extends true>(): void {}

/** Compiles only when value has type T (or a subtype). */
export function expectType<T>(value: T): T {
  return value;
}
