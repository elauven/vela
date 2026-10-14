// Shared k6 settings (task 058). Every scenario takes the target from
// BASE and the load shape from PROFILE:
//   step     ramp 10 → 50 → 100 → 200 → 400 VUs, 30 s each (default)
//   soak     VUS (default 100) for DURATION (default 30m)
//   spike    20 VUs, jump to 10× for 30 s, back to 20
//   constant VUS for DURATION (default 1m), for quick comparisons
import exec from 'k6/execution';

export const BASE = __ENV.BASE || 'http://localhost:8081';

export function profile() {
  const vus = Number(__ENV.VUS || 100);
  switch (__ENV.PROFILE || 'step') {
    case 'soak':
      return { vus, duration: __ENV.DURATION || '30m' };
    case 'spike':
      return { stages: [
        { duration: '30s', target: 20 }, { duration: '5s', target: 200 }, { duration: '30s', target: 200 },
        { duration: '5s', target: 20 }, { duration: '60s', target: 20 },
      ] };
    case 'constant':
      return { vus, duration: __ENV.DURATION || '1m' };
    default:
      return { stages: STEPS.flatMap((t) => [{ duration: '5s', target: t }, { duration: '25s', target: t }]) };
  }
}

export const STEPS = [10, 50, 100, 200, 400];

function seconds(text) {
  const m = /^(\d+)(s|m|h)$/.exec(text);
  if (!m) return 60;
  return Number(m[1]) * { s: 1, m: 60, h: 3600 }[m[2]];
}

// Time windows (task 058): every request carries the tag w of its window,
// and a threshold per window makes k6 export the window's metrics in the
// summary, which gives the runner a time series without raw outputs.
//   step   s0…s4: the 25 s hold of each step (the 5 s ramps are "ramp")
//   soak   m0, m1, …: one window per minute
//   spike  t0, t1, …: one window per 5 s
export function windows() {
  switch (__ENV.PROFILE || 'step') {
    case 'step':
      return { names: STEPS.map((_, i) => `s${i}`), of: (t) => (t % 30 >= 5 ? `s${Math.floor(t / 30)}` : 'ramp') };
    case 'soak':
      return { names: Array.from({ length: Math.ceil(seconds(__ENV.DURATION || '30m') / 60) }, (_, i) => `m${i}`), of: (t) => `m${Math.floor(t / 60)}` };
    case 'spike':
      return { names: Array.from({ length: 26 }, (_, i) => `t${i}`), of: (t) => `t${Math.floor(t / 5)}` };
    default:
      return { names: [], of: () => '' };
  }
}

const WINDOWS = windows();

// tick tags the requests of the current iteration with their window; every
// scenario calls it first.
export function tick() {
  if (WINDOWS.names.length) exec.vu.metrics.tags.w = WINDOWS.of(Math.floor(exec.instance.currentTestRunDuration / 1000));
}

const thresholds = { http_req_failed: ['rate<0.01'] };
for (const w of WINDOWS.names) {
  thresholds[`http_req_duration{w:${w}}`] = ['max>=0'];
  thresholds[`http_reqs{w:${w}}`] = ['count>=0'];
  thresholds[`http_req_failed{w:${w}}`] = ['rate>=0'];
}

export const options = {
  ...profile(),
  discardResponseBodies: true,
  summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
  thresholds,
};

// Deterministic-enough ids within the seeded ranges (bench/seed defaults).
export const PROJECTS = Number(__ENV.PROJECTS || 20);
export const ISSUES = Number(__ENV.ISSUES || 50000);
export const pick = (n) => 1 + Math.floor(Math.random() * n);
export const json = { headers: { 'content-type': 'application/json' } };
// Browsers send Origin on form posts; SvelteKit's CSRF check requires it.
export const ORIGIN = { Origin: BASE };
