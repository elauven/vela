import { expect, test } from 'vitest';
import { fireEvent, render } from '@govela/testing';
import Page from './Page.vela';

test('greets and counts clicks', async () => {
  const { container } = render(Page, { props: { data: { greeting: 'Hello' } } });
  expect(container.querySelector('h1')!.textContent).toBe('Hello');

  const button = container.querySelector('button')!;
  await fireEvent.click(button);

  expect(button.textContent).toBe('Clicked 1 times');
});
