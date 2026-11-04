// Client router of Vela App (docs/APP.md). This module may import only the
// public entry points of @govela/runtime and its own files (docs/APP.md B-4,
// TestAppLayerIsSeparable).
import { hydrate, preloadHydration } from '@govela/runtime/hydrate';

// The navigation protocol version (docs/APP.md N-1).
export const protocolVersion = 1;

// Request headers of a navigation (docs/APP.md N-2).
export const headers = Object.freeze({
  navigate: 'X-Vela-Navigate',
  have: 'X-Vela-Have',
  build: 'X-Vela-Build',
});

// start({ VelaApp, routes, root, onMismatch }) hydrates the app root of a
// server document (R-8): it loads the lazy components the payload names,
// then adopts the server DOM. root is the element or its id ('vela-app').
// It resolves with the hydrated instance and then dispatches the event
// 'vela:hydrated' on root. Partial navigation follows in task 076; until
// then links load documents.
export async function start({ VelaApp, routes, root = 'vela-app', onMismatch } = {}) {
  if (typeof VelaApp !== 'function') throw new TypeError('@govela/app/client: start() needs VelaApp');
  const target = typeof root === 'string' ? document.getElementById(root) : root;
  if (!target) throw new Error(`@govela/app/client: no element #${root} in the document`);
  await preloadHydration(target);
  const options = onMismatch ? { onMismatch } : {};
  const app = hydrate(VelaApp, target, options);
  target.dispatchEvent(new CustomEvent('vela:hydrated', { bubbles: true, detail: { app } }));
  return { app, routes };
}
