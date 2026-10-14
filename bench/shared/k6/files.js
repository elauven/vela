// Uploads and downloads. SIZE is the upload size in KB (default 100);
// 30% uploads, 70% downloads of files uploaded by this VU.
import http from 'k6/http';
import { check } from 'k6';
import { BASE, ISSUES, ORIGIN, options as base, pick } from './lib.js';

export const options = { ...base, discardResponseBodies: false };
const SIZE = Number(__ENV.SIZE || 100) * 1024;
const payload = 'x'.repeat(SIZE);
const mine = [];

export default function () {
  if (mine.length === 0 || Math.random() < 0.3) {
    const res = http.post(`${BASE}/api/issues/${pick(ISSUES)}/attachments`,
      { file: http.file(payload, 'load.bin', 'application/octet-stream') }, { headers: ORIGIN, tags: { name: 'upload' } });
    if (check(res, { 201: (r) => r.status === 201 })) mine.push(res.json('url'));
    return;
  }
  const res = http.get(BASE + mine[pick(mine.length) - 1], { tags: { name: 'download' }, responseType: 'none' });
  check(res, { 200: (r) => r.status === 200 });
}
