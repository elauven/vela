// Hydrates the HTML the Go server rendered.
import { hydrate } from '@govela/runtime/hydrate';
import App from './App.vela';

hydrate(App, document.getElementById('app')!);
