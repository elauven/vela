#!/usr/bin/env node
// Generates the performance chart of the README (docs/assets/ssr-performance.svg)
// from the stable laptop runs in bench/RESULTS.md. After new measurements,
// update the numbers below and run:
//
//   node bench/shared/readme-chart.mjs docs/assets/ssr-performance.svg
//
// Colours are the first three slots of a validated categorical palette
// (colour-blind safe in light and dark); the dark ones apply through
// prefers-color-scheme, as GitHub renders the SVG as an image.
import { writeFileSync } from 'node:fs';
const panels = [
  { title: 'Server-render 200 cards', unit: 'requests / s · higher is better', better: 'high',
    rows: [['Vela', 587, '587'], ['Bun', 160, '160'], ['Node', 111, '111']] },
  { title: 'Issue page from PostgreSQL', unit: 'requests / s · higher is better', better: 'high',
    rows: [['Vela', 3414, '3 414'], ['Bun', 941, '941'], ['Node', 806, '806']] },
  { title: 'JSON API latency, p95', unit: 'ms · lower is better · database-bound', better: 'low',
    rows: [['Vela', 171, '171 ms'], ['Bun', 610, '610 ms'], ['Node', 462, '462 ms']] },
  { title: 'CPU per JSON API request', unit: 'CPU ms · lower is better', better: 'low',
    rows: [['Vela', 0.75, '0.75 ms'], ['Bun', 1.19, '1.19 ms'], ['Node', 1.44, '1.44 ms']] },
  { title: 'Slowest request at 400 users', unit: 'seconds · lower is better', better: 'low',
    rows: [['Vela', 1.0, '1.0 s'], ['Bun', 2.6, '2.6 s'], ['Node', 28.5, '28.5 s']] },
  { title: 'Server memory', unit: 'peak MB · lower is better', better: 'low',
    rows: [['Vela', 38, '38 MB'], ['Bun', 243, '243 MB'], ['Node', 409, '409 MB']] },
];
const W = 880, PW = 410, PH = 150, GAP = 40, X0 = 10, Y0 = 10, LABEL = 52, BARMAX = 240, BH = 20, ROW = 28;
const cls = { Vela: 's1', Node: 's2', Bun: 's3' };
const esc = (t) => t.replace(/&/g, '&amp;').replace(/</g, '&lt;');
let body = '';
panels.forEach((p, i) => {
  const px = X0 + (i % 2) * (PW + GAP), py = Y0 + Math.floor(i / 2) * (PH + 20);
  const max = Math.max(...p.rows.map((r) => r[1]));
  body += `<text class="t1" x="${px}" y="${py + 16}">${esc(p.title)}</text>`;
  body += `<text class="t2" x="${px}" y="${py + 34}">${esc(p.unit)}</text>`;
  const top = py + 50, bx = px + LABEL;
  p.rows.forEach(([name, v, label], j) => {
    const y = top + j * ROW, w = Math.max(6, (v / max) * BARMAX), r = 4;
    body += `<text class="${name === 'Vela' ? 't1' : 't2'} lbl" x="${bx - 8}" y="${y + BH / 2 + 5}" text-anchor="end">${name}</text>`;
    body += `<path class="${cls[name]}" d="M${bx} ${y}h${w - r}a${r} ${r} 0 0 1 ${r} ${r}v${BH - 2 * r}a${r} ${r} 0 0 1 -${r} ${r}h-${w - r}z"/>`;
    let text = label, extra = '';
    if (name === 'Vela') {
      // Against the best of the other two in this panel.
      const others = p.rows.filter((x) => x[0] !== 'Vela');
      const best = others.reduce((a, b) => ((p.better === 'high' ? b[1] > a[1] : b[1] < a[1]) ? b : a));
      const k = p.better === 'high' ? v / best[1] : best[1] / v;
      extra = ` <tspan class="t2 mult">· ${k.toFixed(1)}× ${p.better === 'high' ? best[0] : `less than ${best[0]}`}</tspan>`;
    }
    body += `<text class="${name === 'Vela' ? 't1 val' : 't2 val'}" x="${bx + w + 8}" y="${y + BH / 2 + 5}">${esc(text)}${extra}</text>`;
  });
  body += `<line class="axis" x1="${bx}" x2="${bx}" y1="${top - 4}" y2="${top + 3 * ROW - 4}"/>`;
});
const H = Y0 + Math.ceil(panels.length / 2) * (PH + 20) + 10;
const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${W}" height="${H}" viewBox="0 0 ${W} ${H}" role="img" aria-labelledby="title desc">
<title id="title">Vela SSR performance against SvelteKit on Node and Bun</title>
<desc id="desc">One CPU core, 64 users. Server render of 200 cards: Vela 587, Bun 160, Node 111 requests per second. Issue page from PostgreSQL: Vela 3414, Bun 941, Node 806 requests per second. JSON API p95 latency: Vela 171 ms, Bun 610 ms, Node 462 ms. CPU per JSON API request: Vela 0.75 ms, Bun 1.19 ms, Node 1.44 ms. Slowest request at 400 users: Vela 1.0 s, Bun 2.6 s, Node 28.5 s. Peak server memory: Vela 38 MB, Bun 243 MB, Node 409 MB.</desc>
<style>
  svg { --ink: #0b0b0b; --ink2: #52514e; --rule: #c3c2b7; --s1: #2a78d6; --s2: #eb6834; --s3: #1baf7a; }
  @media (prefers-color-scheme: dark) { svg { --ink: #f0efec; --ink2: #c3c2b7; --rule: #52514e; --s1: #3987e5; --s2: #d95926; --s3: #199e70; } }
  text { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif; fill: var(--ink2); font-size: 12px; }
  .t1 { fill: var(--ink); font-size: 14px; font-weight: 600; }
  .t2 { fill: var(--ink2); }
  .lbl, .val { font-size: 13px; }
  .t1.lbl, .t1.val { font-size: 13px; }
  .s1 { fill: var(--s1); } .s2 { fill: var(--s2); } .s3 { fill: var(--s3); }
  .axis { stroke: var(--rule); stroke-width: 1; }
  .note { font-size: 11px; }
  .mult { font-weight: 400; }
</style>
${body}
<text class="t2 note" x="${X0}" y="${H - 10}">SvelteKit 3 · Svelte 5 on Node 24 and Bun 1.4 against Vela · Go · one CPU core at 1.8 GHz, PostgreSQL 18, 64 users, median of three rounds</text>
</svg>
`;
writeFileSync(process.argv[2], svg);
