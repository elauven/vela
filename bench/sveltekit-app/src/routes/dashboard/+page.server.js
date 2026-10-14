import { dashboard } from '#lib/server/data.js';

// A page without interactivity: no client-side rendering, as Vela does for
// static roots (task 058 fairness rules).
export const csr = false;

export const load = () => dashboard();
