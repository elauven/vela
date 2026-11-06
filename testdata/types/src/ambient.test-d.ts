// @govela/runtime/ambient: the runes and the modules `vela` and `vela/store`
// of component scripts (TS-6.2), as they appear in a `.vela` script.
import { onMount, onDestroy, tick, lazy, mount, writable, type Component, type Lazy } from 'vela';
import { writable as storeWritable, derived, type Writable } from 'vela/store';
import { expectTrue, expectType, type Equal } from './expect.ts';

let count = $state(0);
expectTrue<Equal<typeof count, number>>();
let name = $state<string | null>(null);
expectTrue<Equal<typeof name, string | null>>();
let later = $state<number>();
expectTrue<Equal<typeof later, number | undefined>>();
let items = $state.deep<{ id: number }[]>([]);
let none = $state.deep<string[]>();
expectTrue<Equal<typeof none, string[] | undefined>>();
let doubled = $derived(count * 2);
expectTrue<Equal<typeof doubled, number>>();
let value = $bindable('');
expectTrue<Equal<typeof value, string>>();
let optional = $bindable<number>();
expectTrue<Equal<typeof optional, number | undefined>>();
// @ts-expect-error $state keeps its type
count = 'one';

$effect(() => {
  const id = setInterval(() => count++, 1000);
  return () => clearInterval(id);
});
$effect(() => {
  console.log(items.length, name, later, value, optional);
});
// @ts-expect-error an effect returns nothing or a cleanup function
$effect(() => 1);
$$dispatch('select', { id: 1 });
$$dispatch('close');

onMount(() => {
  return () => {};
});
onMount(async () => {
  await tick();
});
onDestroy(() => {});

declare const Panel: Component<{ rows: number[] }>;
const lazyPanel = lazy(() => Promise.resolve({ default: Panel }));
expectType<Lazy<typeof Panel>>(lazyPanel);
mount(Panel, { target: document.body, props: { rows: [] } });

const a = writable(1);
const b = storeWritable(1);
expectTrue<Equal<typeof b, Writable<number>>>();
const sum = derived([a, b] as const, ([x, y]) => x + y);
expectType<number>(sum.get());
