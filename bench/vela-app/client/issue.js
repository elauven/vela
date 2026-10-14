// Client entry: hydrates the server-rendered IssuePage (copied into out/client).
import { hydrate } from './runtime-hydrate.js';
import { IssuePage } from './pages/IssuePage.js';

hydrate(IssuePage, document.getElementById('app'));
