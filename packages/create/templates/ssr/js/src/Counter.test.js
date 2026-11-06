import { expect, test } from 'vitest';
import { fireEvent, render } from '@govela/testing';
import Counter from './Counter.vela';

test('counts by its step and reports the count', async () => {
  const counts = [];
  const { container } = render(Counter, {
    props: { step: 2 },
    slots: { default: 'Clicks' },
    events: { change: (event) => counts.push(event.detail) },
  });
  const button = container.querySelector('button');
  expect(button.textContent).toBe('Clicks: 0');

  await fireEvent.click(button);
  await fireEvent.click(button);

  expect(button.textContent).toBe('Clicks: 4');
  expect(counts).toEqual([2, 4]);
});
