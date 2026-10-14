// Pure server rendering, no database: /render/board?n=N (default 200).
import http from 'k6/http';
import { check } from 'k6';
import { BASE, options as base, tick } from './lib.js';

export const options = base;
const N = __ENV.N || 200;

export default function () {
  tick();
  const res = http.get(`${BASE}/render/board?n=${N}`, { tags: { name: 'render' } });
  check(res, { 200: (r) => r.status === 200 });
}
