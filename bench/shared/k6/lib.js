// Shared k6 settings (task 058). Every scenario takes the target from
// BASE and the load shape from PROFILE:
//   step     ramp 10 → 50 → 100 → 200 → 400 VUs, 30 s each (default)
//   soak     VUS (default 100) for DURATION (default 30m)
//   spike    20 VUs, jump to 10× for 30 s, back to 20
//   constant VUS for DURATION (default 1m), for quick comparisons
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
      return { stages: [10, 50, 100, 200, 400].flatMap((t) => [{ duration: '5s', target: t }, { duration: '25s', target: t }]) };
  }
}

export const options = {
  ...profile(),
  discardResponseBodies: true,
  summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'max'],
  thresholds: { http_req_failed: ['rate<0.01'] },
};

// Deterministic-enough ids within the seeded ranges (bench/seed defaults).
export const PROJECTS = Number(__ENV.PROJECTS || 20);
export const ISSUES = Number(__ENV.ISSUES || 50000);
export const pick = (n) => 1 + Math.floor(Math.random() * n);
export const json = { headers: { 'content-type': 'application/json' } };
// Browsers send Origin on form posts; SvelteKit's CSRF check requires it.
export const ORIGIN = { Origin: BASE };
