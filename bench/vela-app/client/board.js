// Client entry: hydrates the server-rendered BoardPage (copied into out/client).
import { hydrate } from './runtime-hydrate.js';
import { BoardPage } from './pages/BoardPage.js';

hydrate(BoardPage, document.getElementById('app'));
// The end of hydration, for shared/browser-metrics.mjs.
performance.mark('app-hydrated');
