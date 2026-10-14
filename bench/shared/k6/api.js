// JSON API without HTML: 80% list reads, 10% status moves, 10% comments.
import http from 'k6/http';
import { check } from 'k6';
import { BASE, ISSUES, json, options as base, pick, PROJECTS, tick } from './lib.js';

export const options = base;
const STATUSES = ['backlog', 'todo', 'in_progress', 'done'];

export default function () {
  tick();
  const roll = Math.random();
  let res;
  if (roll < 0.8) {
    res = http.get(`${BASE}/api/issues?project=${pick(PROJECTS)}&q=${['fix', 'add', 'log', ''][pick(4) - 1]}&page=${pick(3)}`, { tags: { name: 'list' } });
  } else if (roll < 0.9) {
    res = http.patch(`${BASE}/api/issues/${pick(ISSUES)}`, JSON.stringify({ status: STATUSES[pick(4) - 1] }), { ...json, tags: { name: 'move' } });
  } else {
    res = http.post(`${BASE}/api/issues/${pick(ISSUES)}/comments`, JSON.stringify({ body: 'Load test comment' }), { ...json, tags: { name: 'comment' } });
  }
  check(res, { ok: (r) => r.status === 200 || r.status === 201 });
}
