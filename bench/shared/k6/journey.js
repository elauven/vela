// A user journey: board → filter list → issue page → comment → small upload
// → download, with think time. Also fetches the page's static assets, as a
// browser with a warm cache would not; set ASSETS=0 to skip them.
import http from 'k6/http';
import { check, group, sleep } from 'k6';
import { BASE, ISSUES, ORIGIN, PROJECTS, json, options as base, pick } from './lib.js';

export const options = { ...base, discardResponseBodies: false };
const THINK = Number(__ENV.THINK || 1);
const ASSETS = __ENV.ASSETS !== '0';
const assetPattern = /(?:src|href)="(\/[^"]+\.(?:js|css))"/g;

function get(path, name) {
  const res = http.get(BASE + path, { tags: { name } });
  check(res, { [`${name} 200`]: (r) => r.status === 200 });
  if (ASSETS && res.body) {
    const assets = [...String(res.body).matchAll(assetPattern)].map((m) => ['GET', BASE + m[1], null, { tags: { name: 'asset' } }]);
    if (assets.length) http.batch(assets);
  }
  return res;
}

export default function () {
  const project = pick(PROJECTS);
  group('board', () => get(`/projects/${project}/board`, 'board'));
  sleep(THINK);
  group('list', () => {
    get(`/issues?project=${project}`, 'issues');
    http.get(`${BASE}/api/issues?project=${project}&status=todo&q=fix`, { tags: { name: 'api-list' } });
  });
  sleep(THINK);
  const id = pick(ISSUES);
  group('issue', () => get(`/issues/${id}`, 'issue'));
  sleep(THINK);
  group('write', () => {
    const c = http.post(`${BASE}/api/issues/${id}/comments`, JSON.stringify({ body: 'Journey comment' }), { ...json, tags: { name: 'comment' } });
    check(c, { 'comment 201': (r) => r.status === 201 });
    const u = http.post(`${BASE}/api/issues/${id}/attachments`, { file: http.file('j'.repeat(20 * 1024), 'journey.txt', 'text/plain') }, { headers: ORIGIN, tags: { name: 'upload' } });
    if (check(u, { 'upload 201': (r) => r.status === 201 })) {
      http.get(BASE + u.json('url'), { tags: { name: 'download' } });
    }
  });
  sleep(THINK);
}
