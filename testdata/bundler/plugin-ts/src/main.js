// The TypeScript twin of the bundler plugin fixture (task 081): TypeScript
// components and a .ts store module, which is the bundler's (TS-5.4).
import { $$isDev } from 'vela';
import App from './App.vela';
import { clicks } from './store.ts';

// Development or production, as the bundler's NODE_ENV replacement decided.
document.body.dataset.dev = String($$isDev());

App(document.getElementById('app'), {});
clicks.subscribe((n) => { document.getElementById('clicks').textContent = 'clicks ' + n; });
