import { renderFixture } from '#lib/server/data.js';

export function load({ url }) {
  const n = Number.parseInt(url.searchParams.get('n'), 10);
  return { ...renderFixture(Number.isNaN(n) ? 200 : n), live: false };
}
