// Generated from runtime/types/ of the Vela compiler repository; do not edit.
// The ambient types of Vela component scripts: list "@govela/runtime/ambient"
// in the `types` of tsconfig.json.

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

declare module 'vela/store' {
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
}

declare module 'vela' {
  /** Props of a component: an object of named values. */
  export type Props = Record<string, any>;

  /**
   * The slots a parent passes to a component. Their values are produced by
   * the compiler from the parent's template.
   */
  export type Slots = Record<string, unknown>;

  /** A mounted component. */
  export interface ComponentInstance<P extends Props = Props> {
    /** The root element; null when the template's root is not one element. */
    readonly el: Element | null;
    /** Removes the component's DOM and stops its effects. Idempotent. */
    destroy(): void;
    /** Sets props; a component without props has no set(). */
    set?(props: Partial<P>): void;
  }

  /**
   * A component: the export of a compiled `.vela` module, or a value made by
   * `serverComponent()` or `staticHTML()`. P are its props, E maps the names
   * of the events it dispatches to their `detail`, S names its slots, B are
   * the names of its `$bindable` props.
   */
  export interface Component<P extends Props = Props, E extends Record<string, any> = Record<string, any>, S extends Record<string, any> = Record<string, any>, B extends string = string> {
    (target: Element | DocumentFragment | Comment, props?: P, slots?: Slots): ComponentInstance<P>;
    /** Type-level only: the events of the component; absent at run time. */
    readonly $$events_def?: E;
    /** Type-level only: the slots of the component; absent at run time. */
    readonly $$slot_def?: S;
    /** Type-level only: the props a parent may bind; absent at run time. */
    readonly $$bindings_def?: B;
  }

  /** The props of a component type. */
  export type ComponentProps<C> = C extends Component<infer P, any, any> ? P : never;

  /** The events of a component type: event names to their `detail`. */
  export type ComponentEvents<C> = C extends Component<any, infer E, any> ? E : never;

  /** The slots of a component type. */
  export type ComponentSlots<C> = C extends Component<any, any, infer S> ? S : never;

  /** The names of the props of a component type that a parent may bind. */
  export type ComponentBindings<C> = C extends Component<any, any, any, infer B> ? B : never;

  /**
   * A component that loads on demand: `lazy(() => import('./Panel.vela'))`.
   * `<vela:component :this>` accepts it; `mount()` and `hydrate()` need the
   * loaded component (`await panel.load()`).
   */
  export interface Lazy<C extends Component<any, any, any> = Component> {
    readonly id: string;
    readonly loaded: boolean;
    /** The component once loaded, else null. */
    readonly component: C | null;
    /** Loads the module; a failed load can be tried again. */
    load(): Promise<C>;
  }

  /** The options of `mount()`: props are required when the component has required props. */
  export type MountOptions<P extends Props = Props> = {
    target: Element | DocumentFragment;
    slots?: Slots;
  } & ({} extends P ? { props?: P } : { props: P });

  /** Mounts a component into target. The props are checked against the component's. */
  export function mount<P extends Props>(component: Component<P, any, any>, options: MountOptions<NoInfer<P>>): ComponentInstance<P>;

  /**
   * A component whose module loads when it is first shown, for
   * `<vela:component :this>`. The loader must be `() => import('<literal .vela path>')`.
   */
  export function lazy<C extends Component<any, any, any>>(loader: () => Promise<C | { default: C }>): Lazy<C>;

  /** Resolves after pending updates were applied to the DOM. */
  export function tick(): Promise<void>;

  /** Logs a warning in development builds; nothing in production. */
  export function warn(message: string): void;

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

  /**
   * Runs fn after the component is mounted. A function it returns runs when
   * the component is destroyed.
   */
  export function onMount(fn: () => void | (() => void) | Promise<void>): void;

  /** Runs fn when the component is destroyed. */
  export function onDestroy(fn: () => void): void;
}
