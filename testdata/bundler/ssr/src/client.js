// The client entry of the server-rendered fixture (task 064): hydrates the
// HTML of the Go renderer with the components the bundler plugin compiled.
import { hydrate } from '@govela/runtime/hydrate';
import App from './App.vela';

hydrate(App, document.getElementById('app'), {
  onMismatch: (report) => console.error('hydration mismatch ' + JSON.stringify(report)),
});
document.body.dataset.hydrated = 'true';
