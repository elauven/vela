// Client entry: hydrates the server-rendered IssuesPage (copied into out/client).
import { hydrate } from './runtime-hydrate.js';
import { IssuesPage } from './pages/IssuesPage.js';

hydrate(IssuesPage, document.getElementById('app'));
