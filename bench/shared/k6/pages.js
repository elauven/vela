// Server-rendered pages with the database. PAGE selects one page
// (board, issues, issue, dashboard) or "mix" (default) for all of them.
import http from 'k6/http';
import { check } from 'k6';
import { BASE, ISSUES, options as base, pick, PROJECTS, tick } from './lib.js';

export const options = base;
const PAGE = __ENV.PAGE || 'mix';
const routes = {
  board: () => `/projects/${pick(PROJECTS)}/board`,
  issues: () => `/issues?project=${pick(PROJECTS)}&sort=${['updated', 'priority', 'number'][pick(3) - 1]}&page=${pick(5)}`,
  issue: () => `/issues/${pick(ISSUES)}`,
  dashboard: () => '/dashboard',
};
const names = Object.keys(routes);

export default function () {
  tick();
  const name = PAGE === 'mix' ? names[pick(names.length) - 1] : PAGE;
  const res = http.get(BASE + routes[name](), { tags: { name } });
  check(res, { 200: (r) => r.status === 200 });
}
