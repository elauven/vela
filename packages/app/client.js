// Client router of Vela App (docs/APP.md). This module may import only the
// public entry points of @govela/runtime and its own files (docs/APP.md B-4,
// TestAppLayerIsSeparable).

// The navigation protocol version (docs/APP.md N-1).
export const protocolVersion = 1;

// Request headers of a navigation (docs/APP.md N-2).
export const headers = Object.freeze({
  navigate: 'X-Vela-Navigate',
  have: 'X-Vela-Have',
  build: 'X-Vela-Build',
});

// start({ VelaApp, routes, target }) hydrates the app root and installs the
// router. It is implemented in task 076.
export function start() {
  throw new Error('@govela/app/client: start() is not implemented yet (task 076)');
}
