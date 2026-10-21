// The entry point of the bundler plugin fixture (task 063): components from
// two directories, a package import, a store from vela/store and the
// runtime from vela.
import { $$isDev } from 'vela';
import App from './App.vela';
import { clicks } from './store.js';

// Development or production, as the bundler's NODE_ENV replacement decided.
document.body.dataset.dev = String($$isDev());

App(document.getElementById('app'), {});
clicks.subscribe((n) => { document.getElementById('clicks').textContent = 'clicks ' + n; });
