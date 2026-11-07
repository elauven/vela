// @govela/runtime: components, mount, lazy and the stores (TS-6.1, TS-6.3).
import {
  mount, lazy, tick, warn, writable, readable, derived, setContext, getContext, hasContext,
  type Component, type ComponentInstance, type ComponentProps, type ComponentEvents, type Lazy,
  type Readable, type Writable, type Derived, type MountOptions,
} from '@govela/runtime';
import { expectTrue, expectType, type Equal } from './expect.ts';

declare const Card: Component<{ title: string; count?: number }, { close: string }, { actions: {} }>;
declare const Optional: Component<{ count?: number }>;
declare const target: HTMLElement;

expectTrue<Equal<ComponentProps<typeof Card>, { title: string; count?: number }>>();
expectTrue<Equal<ComponentEvents<typeof Card>, { close: string }>>();

const card = mount(Card, { target, props: { title: 'a' } });
expectTrue<Equal<typeof card, ComponentInstance<{ title: string; count?: number }>>>();
expectType<Element | null>(card.el);
card.destroy();
card.set?.({ count: 2 });
// @ts-expect-error a prop of the wrong type
card.set?.({ count: 'two' });

mount(Optional, { target });
mount(Optional, { target, props: {} });
// @ts-expect-error required props are required
mount(Card, { target });
// @ts-expect-error a missing required prop
mount(Card, { target, props: {} });
// @ts-expect-error a prop of the wrong type
mount(Card, { target, props: { title: 1 } });
// @ts-expect-error an unknown prop
mount(Card, { target, props: { title: 'a', other: 1 } });
// @ts-expect-error a target is required
mount(Card, { props: { title: 'a' } });
const options: MountOptions<{ count?: number }> = { target };
mount(Optional, options);

const panel = lazy(() => Promise.resolve({ default: Card }));
expectTrue<Equal<typeof panel, Lazy<typeof Card>>>();
expectType<string>(panel.id);
expectType<boolean>(panel.loaded);
expectType<typeof Card | null>(panel.component);
panel.load().then((loaded) => mount(loaded, { target, props: { title: 'b' } }));
lazy(() => Promise.resolve(Card));
// @ts-expect-error a lazy loader returns a module with a component
lazy(() => Promise.resolve({ default: 1 }));
// @ts-expect-error mount needs the loaded component
mount(panel, { target, props: { title: 'a' } });

expectTrue<Equal<ReturnType<typeof tick>, Promise<void>>>();
warn('message');

const count = writable(1);
expectTrue<Equal<typeof count, Writable<number>>>();
count.set(2);
count.update((n) => n + 1);
// @ts-expect-error a store keeps its type
count.set('three');
const unsubscribe: () => void = count.subscribe((n) => expectType<number>(n));
unsubscribe();
expectType<number>(count.get());
const empty = writable<string>();
expectTrue<Equal<typeof empty, Writable<string | undefined>>>();

const clock = readable(0, (set) => {
  const id = setInterval(() => set(Date.now()), 1000);
  return () => clearInterval(id);
});
expectTrue<Equal<typeof clock, Readable<number>>>();
// @ts-expect-error a readable store cannot be set
clock.set(1);

const doubled = derived(count, (n) => n * 2);
expectTrue<Equal<typeof doubled, Derived<number>>>();
doubled.destroy();
const label = derived([count, writable('x')] as const, ([n, s]) => s.repeat(n));
expectTrue<Equal<typeof label, Derived<string>>>();
const total = derived([count, clock], (values) => values.length);
expectType<Derived<number>>(total);
// @ts-expect-error the values of derived() have the stores' types
derived(count, (n: string) => n);

// A component of another module declaration is the same structural type.
const asValue: Component<{ title: string; count?: number }, any, any> = Card;
expectType<Component>(Optional);
export { asValue };

// Context (task 090): string keys, a typed getContext.
expectType<{ name: string }>(setContext('theme', { name: 'dark' }));
expectTrue<Equal<ReturnType<typeof getContext<{ name: string }>>, { name: string }>>();
expectType<unknown>(getContext('theme'));
expectType<boolean>(hasContext('theme'));
// @ts-expect-error keys are strings
setContext(Symbol('theme'), 1);
// @ts-expect-error keys are strings
getContext(1);
