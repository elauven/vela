import { mount } from '@govela/runtime';
import { Button } from './Button.vela';
import { total } from './util.ts';

mount(Button, { target: document.body, props: { label: 'Go' } });
// expect TS2322: the prop has the wrong type
mount(Button, { target: document.body, props: { label: total([1]) } });
