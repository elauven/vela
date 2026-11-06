// Declarations of compiled components (task 083, TS-4, TS-6.5): ui-lib is
// testdata/dts/lib built with `vela --dts`, its .ts module declared by
// `tsc --emitDeclarationOnly`, as docs/GUIDE.md tells library authors.
import {
  mount,
  type ComponentBindings, type ComponentEvents, type ComponentProps, type ComponentSlots,
} from '@govela/runtime';
import { Button, type ButtonProps } from 'ui-lib/Button.js';
import List from 'ui-lib/List.js';
import { Badge } from 'ui-lib/Badge.js';
import { Loose } from 'ui-lib/Loose.js';
import { expectTrue, type Equal } from './expect.ts';

declare const target: HTMLElement;

// A TypeScript component: annotations, defaults, $bindable, $$Events.
mount(Button, { target, props: { label: 'Go' } });
mount(Button, { target, props: { label: 'Go', kind: 'primary', format: { upper: true }, pressed: true } });
// @ts-expect-error a required prop
mount(Button, { target, props: {} });
// @ts-expect-error a prop of the wrong type
mount(Button, { target, props: { label: 1 } });
// @ts-expect-error an unknown prop
mount(Button, { target, props: { label: 'Go', size: 2 } });
// @ts-expect-error not a Kind
mount(Button, { target, props: { label: 'Go', kind: 'big' } });
// @ts-expect-error the imported type travels with the component
mount(Button, { target, props: { label: 'Go', format: { upper: 'yes' } } });
expectTrue<Equal<ComponentProps<typeof Button>, ButtonProps>>();
expectTrue<Equal<ComponentEvents<typeof Button>, { press: { at: number } }>>();
expectTrue<Equal<ComponentBindings<typeof Button>, 'pressed'>>();
expectTrue<Equal<keyof ComponentSlots<typeof Button>, 'default'>>();

// A generic component: mount() checks the props against the constraint of
// T (TypeScript instantiates a generic signature it infers from with its
// constraints); a parent's template infers T (084).
mount(List, { target, props: { items: [{ id: 'a' }], selected: { id: 'a' } } });
expectTrue<Equal<ComponentProps<typeof List>, { items: { id: string }[]; selected?: { id: string } | null }>>();
// @ts-expect-error items need an id
mount(List, { target, props: { items: [{ name: 'A' }] } });
// @ts-expect-error items are required
mount(List, { target });
expectTrue<Equal<keyof ComponentSlots<typeof List>, 'empty'>>();

// A JavaScript component typed by JSDoc.
mount(Badge, { target, props: { text: 'new', tone: 'warn' } });
// @ts-expect-error @type {string}
mount(Badge, { target, props: { text: 1 } });
// @ts-expect-error @type {'info' | 'warn'}
mount(Badge, { target, props: { text: 'new', tone: 'error' } });
expectTrue<Equal<ComponentEvents<typeof Badge>, { dismiss: { text: string } }>>();

// A JavaScript component without annotations is loosely typed.
mount(Loose, { target, props: { value: { anything: true }, items: [1, 'two'] } });
// @ts-expect-error the literal default types the prop
mount(Loose, { target, props: { value: 1, open: 'yes' } });
expectTrue<Equal<ComponentEvents<typeof Loose>, Record<string, any>>>();
expectTrue<Equal<keyof ComponentSlots<typeof Loose>, 'footer'>>();
