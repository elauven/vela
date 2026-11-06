import { mount, type ComponentEvents } from '@govela/runtime';
import { Button, type ButtonProps } from './Button.vela';
import { Icon } from './Icon.vela';

mount(Button, { target: document.body, props: { label: 'Go', icon: Icon } });
mount(Button, { target: document.body, props: { label: 1 } });
const partial: Partial<ButtonProps> = { /*props*/ };
const at: ComponentEvents<typeof Button>['press']['at'] = 1;
export { partial, at };
