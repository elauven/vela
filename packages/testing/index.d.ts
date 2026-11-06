// Types of @govela/testing.
import type { Component, ComponentInstance, Props } from '@govela/runtime';

export { tick } from '@govela/runtime';

/** Content of a slot: an HTML string, a node, or a function returning one. */
export type SlotContent = string | Node | (() => string | Node);

export interface RenderOptions<P extends Props = Props> {
  /** The props of the component. */
  props?: P;
  /** Slot contents by name; `default` is the default slot. */
  slots?: Record<string, SlotContent>;
  /** Listeners of the component's events (`$$dispatch`), by name. */
  events?: Record<string, (event: CustomEvent) => void>;
  /** The element to mount into; by default a new div in document.body. */
  target?: Element;
}

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
export function render<P extends Props>(component: Component<P, any, any, any>, options?: RenderOptions<P>): RenderResult<P>;

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
