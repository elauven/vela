import { mount } from '@govela/runtime';
import { App } from './App.vela';
import Card from './Card.vela';

mount(App, { target: document.body });
mount(Card, { target: document.body, props: { heading: 'x' } });
