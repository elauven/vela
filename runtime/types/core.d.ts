// Fragment: the component API of `vela` and `@govela/runtime` (TS-6.3).
// runtime/embed.go assembles it into runtime.d.ts and ambient.d.ts; the
// declarations carry no `declare`, so they are valid in both.

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
 * id is the component's `$$id`, which the compiler and `vela-app` pass.
 */
export function lazy<C extends Component<any, any, any>>(loader: () => Promise<C | { default: C }>, id?: string): Lazy<C>;

/** Resolves after pending updates were applied to the DOM. */
export function tick(): Promise<void>;

/** Logs a warning in development builds; nothing in production. */
export function warn(message: string): void;
