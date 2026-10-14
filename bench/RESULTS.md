# Results: Vela + Go vs SvelteKit

Measurements of task 058, 2026-10-14; the main table is the third run, after
the runtime fix (`b82dc3c`) and the page-size work of task 059. Method and
contract: [README.md](README.md). Raw k6 summaries: `bench/results/` (not
committed).

## Environment

| | |
| --- | --- |
| Machine | Laptop, Intel Core i7-8550U (4 cores, 8 threads, frequency scaling), 8 GB RAM, Linux 7.0 |
| Pinning | `taskset`: PostgreSQL on logical CPUs 0–1, the application on 2 (1 CPU) or 2–5 (4 CPUs), k6 on 6–7 |
| Database | PostgreSQL 18.6, default settings (`shared_buffers` 128 MB, `max_connections` 100); seed: 20 projects, 50 000 issues, 200 000 comments; reseeded (and uploads cleared) before every application × CPU block |
| Vela | compiler of commit `50dae72` (task 059), Go 1.27.1, `vela --ssr --mode=production`, client bundled with esbuild; `GOMAXPROCS` = CPUs |
| SvelteKit | 3.0.1, Svelte 5.57.2, Vite 8.3.4, adapter-node 6.0.0, Node 24.21; 4 CPUs = 4 `node:cluster` workers |
| Load | k6 1.8.2, 64 VUs, 30 s per scenario after a 10 s warm-up (`node shared/runner.mjs`) |

**Caveats.** One noisy laptop, not a server: logical CPUs share physical
cores (hyper-threading), so the 4-CPU configuration competes with
PostgreSQL and k6, and turbo frequency varies. Two runs of the same
configuration differed by up to ~10%; SvelteKit's numbers agree within that
across all three runs. Treat differences under 15% as noise. A server run
with `docker-compose.yml` is still to do.

## Main table

req/s with p95 latency in milliseconds. **Bold**: more than 15% ahead.

| Scenario | What it measures | Vela 1 CPU | SvelteKit 1 CPU | Vela 4 CPU | SvelteKit 4 CPU |
| --- | --- | ---: | ---: | ---: | ---: |
| `render-50` | SSR of 50 cards, no database | **1357** (113) | 693 (124) | **3186** (49) | 1448 (76) |
| `render-200` | SSR of 200 cards, no database | **434** (447) | 194 (363) | **1091** (144) | 502 (196) |
| `render-500` | SSR of 500 cards, no database | **179** (928) | 87 (737) | **504** (312) | 222 (419) |
| `board` | Board page: database + up to 400 cards | **84** (925) | 65 (1348) | 77 (897) | 73 (1102) |
| `issues` | Issue list page | 402 (225) | 361 (408) | 341 (274) | 341 (457) |
| `issue` | Issue page | **2955** (28) | 1222 (74) | **3346** (24) | 2285 (44) |
| `dashboard` | Aggregates over all issues, static page | 28 | 27 | 28 | 29 |
| `api` | JSON API, 80% reads / 20% writes | 562 (158) | 522 (410) | 510 (174) | 534 (264) |
| `files-100k` | 100 KB files, 10% uploads / 90% downloads | **3751** (32) | 1186 (85) | **3666** (41) | 2395 (50) |
| `journey` | User path with think time | **385** (619) | 283 (560) | 290 (662) | 291 (1125) |

Resources under the same load:

| | Vela | SvelteKit |
| --- | --- | --- |
| Peak RSS, 1 CPU | 24–44 MB | 330–410 MB |
| Peak RSS, 4 CPUs | 29–89 MB | 1.3–1.6 GB |
| CPU at the same load (`issue`, 4 CPUs) | 2.0 cores for 3346 req/s | 3.8 cores for 2285 req/s |

Page sizes (HTML with the hydration payload, bytes):

| Page | Vela before 059 | Vela after 059 | SvelteKit |
| --- | ---: | ---: | ---: |
| Board, 400 cards | 543 697 | 360 209 | 308 878 |
| Render fixture, 500 cards | 707 005 | 472 583 | 404 241 |
| Issue list | 52 926 | 32 957 | 29 386 |
| Issue page | 11 875 | 5 703 | 6 720 |
| Dashboard | 9 356 | 6 348 | 6 538 |

## Reading the numbers

- **Server rendering.** Without the database Vela renders 2.0–2.2 times
  more pages per second than SvelteKit on the same CPU, with lower median
  latency. On one CPU Vela's p95 is still a little higher (447 against
  363 ms at 200 cards): 64 requests share one `GOMAXPROCS` slot and the
  garbage collector, while Node serves them one after another. With 4 CPUs
  Vela's tails are lower.
- **Pages with the database.** List, dashboard and the API are limited by
  PostgreSQL on two logical CPUs; both versions reach about the same
  throughput, with steadier tails for Vela. The board page, which also
  renders up to 400 cards, is ahead for Vela on one CPU.
- **Small pages and files.** The issue page and file transfers favour Go
  clearly: 1.5–3.2 times the throughput with half the CPU.
- **Memory.** Vela uses about a tenth of SvelteKit's memory on one CPU and a
  twentieth with four, because Go serves all cores from one process.
- **Bytes on the wire.** After task 059 Vela's pages are 12–17% larger than
  SvelteKit's for lists and smaller for the issue page and the dashboard.
  The remaining difference on lists is the block markers and the payload,
  which carries the root props (the list data) once.

## History of the Vela numbers

| | First run | Runtime fix (`b82dc3c`) | Task 059 (`50dae72`) |
| --- | ---: | ---: | ---: |
| `BenchmarkRenderBoard` (200 cards) | 8.1 ms, 67 000 allocations | 2.2 ms, 12 400 allocations | — |
| `render-200`, 1 CPU | 110 req/s | 338 req/s | 434 req/s |
| `render-200`, 4 CPUs | 290 req/s | 816 req/s | 1091 req/s |
| `board`, 1 CPU | 41 req/s | 75 req/s | 84 req/s |
| `issue`, 1 CPU | 2071 req/s | 2782 req/s | 2955 req/s |
| Board page | 544 KB | 544 KB | 360 KB |

- **Runtime fix.** The first run showed Vela rendering slower than
  SvelteKit. Profiling (`go test -bench BenchmarkRenderBoard` in
  `vela-app/server`) found that 60% of the render time was the JSON
  encoding of the hydration payload: `*vela.Object` was encoded through
  `json.Marshal` level by level, re-validating the nested output at every
  level. A single-pass encoder that writes the same bytes replaced it, with
  `Object.Keys` without re-sorting and `WriteString` without a copy.
- **Task 059** removed what the benchmark found in the page:
  - state initialized from a prop or a literal is computed by the client
    instead of travelling in the payload a second time;
  - components without such state write no payload entry;
  - components without styles write no scope attribute;
  - whitespace follows HTML: one space between sibling elements, as Svelte
    keeps it. The parity check now finds the same visible text.

Also fixed on the way: missing browser globals (`XMLHttpRequest`, `Intl`,
`AbortController`, observers…) in the compiler's scope analysis.

## Notes on SvelteKit 3

- Configuration moved into the Vite plugin: `sveltekit({ adapter: … })`;
  `svelte.config.js` is rejected.
- `$lib` is removed; `#lib` is a Node subpath import declared in
  `package.json` (`"imports": {"#lib/*": "./src/lib/*"}`).
- There is no `ORIGIN` variable: the origin is `paths.origin` at build time.
  Without it adapter-node assumes `https` and its CSRF check rejects
  multipart uploads; k6 sends an `Origin` header as browsers do.
- adapter-node limits request bodies to 512 KB by default
  (`BODY_SIZE_LIMIT`).
- Node 24 names the main thread `MainThread`, so `pgrep -x node` does not
  find a running server.
- One run showed 6% failed requests on `issues` with 4 workers; it did not
  reproduce in a second run or in isolation.

## Next

- A run on a dedicated server with `docker compose` (both configurations).
- Saturation curves with `PROFILE=step`, a 30-minute soak, and a spike.
- Browser metrics (TTFB, LCP, hydration time, INP) with Playwright.
- Profile the remaining single-CPU tail latency of Vela (garbage collector
  and scheduling with 64 concurrent requests on one `GOMAXPROCS`).
