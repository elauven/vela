// Client entry: hydrates the server-rendered IssuePage (copied into out/client).
import { hydrate } from './runtime-hydrate.js';
import { IssuePage } from './pages/IssuePage.js';

hydrate(IssuePage, document.getElementById('app'));
// The end of hydration, for shared/browser-metrics.mjs.
performance.mark('app-hydrated');
