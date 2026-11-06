// @govela/testing: render Vela components in a test DOM (Vitest with
// happy-dom or jsdom) and act on them (task 066, docs/GUIDE.md "Testing").
// Compile the components with @govela/unplugin, as in the application.
import { mount, tick } from '@govela/runtime';

export { tick };

const rendered = new Set();

// A slot passed to render(): an HTML string, a node, or a function returning
// either. A slot of the runtime is a function returning a DocumentFragment
// with the content; reopened, it returns the same nodes.
function slotOf(content) {
  let nodes = null;
  return () => {
    if (!nodes) {
      const value = typeof content === 'function' ? content() : content;
      const template = document.createElement('template');
      if (typeof value === 'string') template.innerHTML = value;
      else if (value != null) template.content.append(value);
      nodes = [...template.content.childNodes];
    }
    const fragment = document.createDocumentFragment();
    fragment.append(...nodes);
    return fragment;
  };
}

/**
 * Mounts a component into a new container appended to document.body (or
 * into options.target) and returns the container and the instance.
 */
export function render(Component, options = {}) {
  const { props = {}, slots = {}, events = {}, target } = options;
  const container = target ?? document.body.appendChild(document.createElement('div'));
  const runtimeSlots = {};
  for (const [name, content] of Object.entries(slots)) runtimeSlots[name] = slotOf(content);
  // Component events are CustomEvents that bubble from the component's DOM.
  const listeners = [];
  for (const [name, handler] of Object.entries(events)) {
    container.addEventListener(name, handler);
    listeners.push([name, handler]);
  }
  const component = mount(Component, { target: container, props, slots: runtimeSlots });
  let mounted = true;
  const result = {
    container,
    component,
    /** Sets props and waits for the update. */
    async rerender(next) {
      if (!component.set) throw new Error('[Vela] rerender(): the component has no props');
      component.set(next);
      await settle();
    },
    /** Destroys the component and removes the container it created. */
    unmount() {
      if (!mounted) return;
      mounted = false;
      rendered.delete(result);
      component.destroy();
      for (const [name, handler] of listeners) container.removeEventListener(name, handler);
      if (!target) container.remove();
    },
  };
  rendered.add(result);
  return result;
}

/** Unmounts every component render() mounted; call it after each test. */
export function cleanup() {
  for (const result of [...rendered]) result.unmount();
}

// settle waits until the updates scheduled by an event or a state change
// are applied: the runtime flushes them in microtasks.
async function settle() {
  await tick();
  await tick();
}

/**
 * Dispatches event on element and waits for the component to update.
 * fireEvent.click(button), fireEvent.input(input, 'text') and the other
 * helpers create the event.
 */
export async function fireEvent(element, event) {
  if (!element) throw new Error('[Vela] fireEvent(): no element');
  const result = element.dispatchEvent(event);
  await settle();
  return result;
}

const view = () => document.defaultView ?? globalThis;
const mouse = (type) => (element, init = {}) => fireEvent(element, new (view().MouseEvent)(type, { bubbles: true, cancelable: true, ...init }));
const keyboard = (type) => (element, init = {}) => fireEvent(element, new (view().KeyboardEvent)(type, { bubbles: true, cancelable: true, ...init }));
const focus = (type, bubbles) => (element, init = {}) => fireEvent(element, new (view().FocusEvent)(type, { bubbles, ...init }));

fireEvent.click = mouse('click');
fireEvent.dblClick = mouse('dblclick');
fireEvent.mouseDown = mouse('mousedown');
fireEvent.mouseUp = mouse('mouseup');
fireEvent.keyDown = keyboard('keydown');
fireEvent.keyUp = keyboard('keyup');
fireEvent.focus = focus('focus', false);
fireEvent.blur = focus('blur', false);
/** Sets the value of an input, select or textarea and dispatches input. */
fireEvent.input = (element, value, init = {}) => {
  if (value !== undefined) element.value = value;
  return fireEvent(element, new (view().Event)('input', { bubbles: true, ...init }));
};
/** Sets the value (or checked, for a boolean) and dispatches change. */
fireEvent.change = (element, value, init = {}) => {
  if (typeof value === 'boolean') element.checked = value;
  else if (value !== undefined) element.value = value;
  return fireEvent(element, new (view().Event)('change', { bubbles: true, ...init }));
};
fireEvent.submit = (element, init = {}) => fireEvent(element, new (view().Event)('submit', { bubbles: true, cancelable: true, ...init }));
