// Uploads and downloads. SIZE is the upload size in KB (default 100);
// UPLOADS is the share of uploads (default 0.1), the rest downloads files
// uploaded by this VU. Every upload stays on disk until the next reseed:
// about UPLOADS × req/s × duration × SIZE (≈ 0.4 GB for 30 s at 1400 req/s).
import http from 'k6/http';
import { check } from 'k6';
import { BASE, ISSUES, ORIGIN, options as base, pick } from './lib.js';

export const options = { ...base, discardResponseBodies: false };
const SIZE = Number(__ENV.SIZE || 100) * 1024;
const UPLOADS = Number(__ENV.UPLOADS || 0.1);
const payload = 'x'.repeat(SIZE);
const mine = [];

export default function () {
  if (mine.length === 0 || Math.random() < UPLOADS) {
    const res = http.post(`${BASE}/api/issues/${pick(ISSUES)}/attachments`,
      { file: http.file(payload, 'load.bin', 'application/octet-stream') }, { headers: ORIGIN, tags: { name: 'upload' } });
    if (check(res, { 201: (r) => r.status === 201 })) mine.push(res.json('url'));
    return;
  }
  const res = http.get(BASE + mine[pick(mine.length) - 1], { tags: { name: 'download' }, responseType: 'none' });
  check(res, { 200: (r) => r.status === 200 });
}
