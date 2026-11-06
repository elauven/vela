// Types of @govela/testing. render() checks the props, events and slots of
// a test against the component's types (Component<P, E, S, B>).
import type { Component, ComponentInstance, Props } from '@govela/runtime';

export { tick } from '@govela/runtime';

/** Content of a slot: an HTML string, a node, or a function returning one. */
export type SlotContent = string | Node | (() => string | Node);

/** Listeners of a component's events, by name, with their `detail` types. */
export type EventListeners<E extends Record<string, any>> = {
  [K in keyof E & string]?: (event: CustomEvent<E[K]>) => void;
};

/** The slots of a component by name; `default` is the default slot. */
export type SlotContents<S extends Record<string, any>> = string extends keyof S
  ? Record<string, SlotContent>
  : { [K in keyof S]?: SlotContent };

/** The options of render(): props are required when the component has required props. */
export type RenderOptions<
  P extends Props = Props,
  E extends Record<string, any> = Record<string, any>,
  S extends Record<string, any> = Record<string, any>,
> = {
  /** Slot contents by name. */
  slots?: SlotContents<S>;
  /** Listeners of the component's events (`$$dispatch`), by name. */
  events?: EventListeners<E>;
  /** The element to mount into; by default a new div in document.body. */
  target?: Element;
} & ({} extends P ? { props?: P } : { props: P });

export interface RenderResult<P extends Props = Props> {
  /** The element the component is mounted into. */
  readonly container: Element;
  /** The mounted component. */
  readonly component: ComponentInstance<P>;
  /** Sets props and waits for the update. */
  rerender(props: Partial<P>): Promise<void>;
  /** Destroys the component and removes the container render() created. */
  unmount(): void;
}

/** Mounts a component for a test. */
export function render<P extends Props, E extends Record<string, any>, S extends Record<string, any>>(
  component: Component<P, E, S, any>,
  ...options: {} extends P ? [options?: RenderOptions<NoInfer<P>, NoInfer<E>, NoInfer<S>>] : [options: RenderOptions<NoInfer<P>, NoInfer<E>, NoInfer<S>>]
): RenderResult<P>;

/** Unmounts every component render() mounted; call it after each test. */
export function cleanup(): void;

export interface FireEvent {
  /** Dispatches event on element and waits for the component to update. */
  (element: Element | null, event: Event): Promise<boolean>;
  click(element: Element | null, init?: MouseEventInit): Promise<boolean>;
  dblClick(element: Element | null, init?: MouseEventInit): Promise<boolean>;
  mouseDown(element: Element | null, init?: MouseEventInit): Promise<boolean>;
  mouseUp(element: Element | null, init?: MouseEventInit): Promise<boolean>;
  keyDown(element: Element | null, init?: KeyboardEventInit): Promise<boolean>;
  keyUp(element: Element | null, init?: KeyboardEventInit): Promise<boolean>;
  focus(element: Element | null, init?: FocusEventInit): Promise<boolean>;
  blur(element: Element | null, init?: FocusEventInit): Promise<boolean>;
  /** Sets the value of an input, select or textarea and dispatches input. */
  input(element: Element | null, value?: string, init?: EventInit): Promise<boolean>;
  /** Sets the value (or checked, for a boolean) and dispatches change. */
  change(element: Element | null, value?: string | boolean, init?: EventInit): Promise<boolean>;
  submit(element: Element | null, init?: EventInit): Promise<boolean>;
}

export const fireEvent: FireEvent;
