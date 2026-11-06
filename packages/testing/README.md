# @govela/testing

Test helpers for Vela components, for Vitest with happy-dom or jsdom.
The components are compiled by `@govela/unplugin`, as in the
application.

```sh
npm i -D @govela/testing @govela/unplugin vitest happy-dom
```

```js
// vite.config.js
import vela from '@govela/unplugin/vite';

export default { plugins: [vela()], test: { environment: 'happy-dom' } };
```

```js
import { afterEach, expect, test } from 'vitest';
import { cleanup, fireEvent, render } from '@govela/testing';
import Counter from './Counter.vela';

afterEach(cleanup);

test('counts', async () => {
  const { container } = render(Counter, { props: { step: 2 } });
  await fireEvent.click(container.querySelector('button'));
  expect(container.textContent).toContain('2');
});
```

- `render(Component, { props, slots, events, target })` mounts the
  component into a new `<div>` of `document.body` and returns
  `{ container, component, rerender(props), unmount() }`. A slot is an
  HTML string, a node or a function returning one; `events` listens to
  the component's `$$dispatch` events.
- `fireEvent(element, event)`, `fireEvent.click(element)`,
  `fireEvent.input(element, value)` and the others dispatch the event
  and wait until the component has updated.
- `cleanup()` unmounts every rendered component; `tick()` waits for
  pending updates.
- In TypeScript, `render` checks the props (and requires the required
  ones), the `detail` of each listener and the slot names against the
  component's type.

License: MIT.
