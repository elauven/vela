import { mount } from '@govela/runtime';
import { Typed } from './Typed.vela';

const target = document.body;
mount(Typed, { target, props: { heading: 1 } });
