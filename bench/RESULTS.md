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

## Browser

`--mode browser` (`shared/browser-metrics.mjs`), Chromium, median of 5 cold
loads (new context, empty cache). Desktop: no throttling. Mobile: CPU 4×
slower, 1.6 Mbit/s down, 150 ms round trip (Lighthouse's mobile preset).
Both servers send uncompressed HTML; behind a compressing proxy the HTML
sizes, and the mobile times that depend on them, would shrink for both.

| Page | Metric | Vela desktop | SvelteKit desktop | Vela mobile | SvelteKit mobile |
| --- | --- | ---: | ---: | ---: | ---: |
| Board (400 cards) | TTFB ms | 54 | 81 | 54 | 78 |
| | FCP / LCP ms | 168 / 168 | 208 / 208 | 464 / 560 | 464 / 524 |
| | hydrated ms | **194** | 285 | 2420 | 2488 |
| | TBT ms | 20 | 27 | **78** | 174 |
| Render fixture (200 cards) | hydrated ms | **47** | 162 | 1522 | 1688 |
| | TBT ms | 7 | 0 | **36** | 112 |
| Issue list | FCP ms | 48 | 68 | 428 | 444 |
| | hydrated ms | **42** | 100 | **636** | 894 |
| Issue page | FCP ms | 36 | 52 | 412 | 416 |
| | hydrated ms | **30** | 78 | **531** | 851 |
| Dashboard (no JavaScript) | FCP ms | 128 | 128 | 400 | 388 |
| All pages | JavaScript per page | **25–29 KB, 1 file** | 99–101 KB, 13–14 files | | |
| | JS heap after hydration | 2–6 MB | 3–9 MB | | |
| Board | HTML | 358 KB | 307 KB | | |

| Interaction (slowest event, Event Timing API) | Vela desktop | SvelteKit desktop | Vela mobile | SvelteKit mobile |
| --- | ---: | ---: | ---: | ---: |
| Typing in the board filter (200 cards) | 32 ms | 40 ms | 48 ms | 48 ms |
| Drag and drop between columns | 88 ms | 80 ms | 80 ms | 80 ms |
| Status filter on the list until the rows are updated (fetch included) | 44 ms | 50 ms | 292 ms | 288 ms |

- **Hydration** finishes 1.5–3.5 times sooner with Vela on desktop: one
  25–29 KB module against about 100 KB in 13–14 SvelteKit chunks. On the
  mobile profile the board's time is dominated by downloading its HTML, so
  both are close; on the smaller pages Vela is ready 260–320 ms earlier.
- **Main-thread blocking** (TBT) during hydration is lower for Vela on the
  mobile profile (78 against 174 ms on the board). On desktop both are small.
  A first run of this measurement showed the opposite on mobile (86 against
  13 ms), so treat single TBT values as noisy; the hydration and byte counts
  were stable.
- **Interactions** are equal within the timer resolution (8 ms steps): both
  update only the affected DOM.
- **HTML** is 10–17% larger for Vela on list pages (block markers and the
  payload); first paint is still earlier or equal because the server
  answers sooner.

## Saturation curves

`--profile step`: 25 s at 10, 50, 100, 200 and 400 VUs. req/s, p95 and the
slowest request (max) in milliseconds.

| Scenario | CPUs | VUs | Vela req/s | Vela p95 | Vela max | SvelteKit req/s | SvelteKit p95 | SvelteKit max |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `render-200` | 1 | 10 | 405 | 60 | 114 | 203 | 86 | 136 |
| `render-200` | 1 | 50 | 396 | 363 | 525 | 195 | 298 | 1936 |
| `render-200` | 1 | 100 | 397 | 584 | 739 | 196 | 536 | 14158 |
| `render-200` | 1 | 200 | 399 | 1101 | 1308 | 194 | 750 | 55952 |
| `render-200` | 1 | 400 | 404 | 1786 | 1876 | 198 | 911 | 26879 |
| `board` | 1 | 10 | 72 | 205 | 274 | 51 | 285 | 349 |
| `board` | 1 | 50 | 84 | 761 | 978 | 66 | 1042 | 1322 |
| `board` | 1 | 100 | 82 | 1387 | 1555 | 67 | 2013 | 2510 |
| `board` | 1 | 200 | 88 | 2431 | 2771 | 70 | 3899 | 4882 |
| `board` | 1 | 400 | 81 | 5200 | 5454 | 70 | 7313 | 19569 |
| `issue` | 1 | 10 | 2934 | 6 | 15 | 989 | 16 | 59 |
| `issue` | 1 | 50 | 2860 | 24 | 54 | 1166 | 61 | 119 |
| `issue` | 1 | 100 | 2840 | 42 | 84 | 1127 | 121 | 191 |
| `issue` | 1 | 200 | 2809 | 79 | 150 | 1118 | 237 | 331 |
| `issue` | 1 | 400 | 2828 | 154 | 246 | 1073 | 487 | 5336 |
| `api` | 1 | 10 | 614 | 28 | 62 | 550 | 33 | 81 |
| `api` | 1 | 50 | 500 | 149 | 208 | 519 | 179 | 403 |
| `api` | 1 | 100 | 386 | 344 | 394 | 422 | 529 | 1059 |
| `api` | 1 | 200 | 428 | 685 | 851 | 481 | 930 | 2112 |
| `api` | 1 | 400 | 478 | 1229 | 3481 | 544 | 1256 | 2175 |
| `render-200` | 4 | 10 | 987 | 19 | 42 | 509 | 37 | 93 |
| `render-200` | 4 | 50 | 1071 | 110 | 292 | 487 | 168 | 374 |
| `render-200` | 4 | 100 | 1115 | 214 | 645 | 485 | 309 | 649 |
| `render-200` | 4 | 200 | 1138 | 434 | 1283 | 483 | 533 | 3280 |
| `render-200` | 4 | 400 | 1143 | 891 | 3282 | 482 | 915 | 25692 |
| `board` | 4 | 10 | 77 | 162 | 244 | 73 | 192 | 263 |
| `board` | 4 | 50 | 77 | 745 | 833 | 73 | 818 | 1011 |
| `board` | 4 | 100 | 77 | 1347 | 1410 | 74 | 1793 | 2819 |
| `board` | 4 | 200 | 78 | 2650 | 2767 | 74 | 4978 | 10635 |
| `board` | 4 | 400 | 78 | 5182 | 5259 | 74 | 9808 | 18782 |
| `issue` | 4 | 10 | 3397 | 4 | 25 | 2058 | 8 | 43 |
| `issue` | 4 | 50 | 3280 | 20 | 56 | 2079 | 36 | 94 |
| `issue` | 4 | 100 | 3287 | 36 | 98 | 2489 | 62 | 128 |
| `issue` | 4 | 200 | 3276 | 78 | 169 | 2513 | 122 | 239 |
| `issue` | 4 | 400 | 3313 | 149 | 237 | 2592 | 216 | 341 |
| `api` | 4 | 10 | 541 | 33 | 522 | 542 | 35 | 476 |
| `api` | 4 | 50 | 459 | 162 | 262 | 504 | 209 | 395 |
| `api` | 4 | 100 | 378 | 362 | 441 | 407 | 550 | 1522 |
| `api` | 4 | 200 | 370 | 762 | 923 | 429 | 1415 | 5103 |
| `api` | 4 | 400 | 475 | 989 | 1217 | 538 | 1616 | 3229 |

- **Both saturate early.** Each VU waits for its answer before the next
  request (a closed load model), so once the CPU is busy more VUs only add
  latency: throughput is flat from 10 VUs on one CPU. Vela's plateau is 2.0×
  SvelteKit's for rendering and 2.5× for the issue page on one CPU (2.2× and
  1.3× on four).
- **p95 is not the whole story under overload.** At 400 VUs on one CPU
  SvelteKit's p95 for `render-200` is lower (911 ms against 1786 ms) while
  it serves half the requests. Go shares the CPU fairly among all requests,
  so every request takes about VUs ÷ throughput (≈1 s here, by Little's law)
  and the slowest one is 1.9 s. Node serves some connections ahead of others:
  most requests are quick, a few wait up to 27 s, and the VUs stuck on them
  send few requests, so per-request percentiles hide them (coordinated
  omission). The max column shows it.
- **Database-bound pages** (`board`, `api`) are flat for both at the
  database's limit, with lower latency for Vela.

## Spike

`--profile spike`: 20 VUs, a jump to 200 VUs for 30 s, back to 20.

| Scenario | CPUs | App | p95 before | worst p95 in the spike | req/s in the spike | slowest request | p95 5 s after | errors |
| --- | ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `render-200` | 1 | vela | 114 | 1160 | 397 | 1257 | 103 | 0.0% |
| `issue` | 1 | vela | 10 | 81 | 2776 | 179 | 11 | 0.0% |
| `render-200` | 4 | vela | 38 | 451 | 1130 | 1637 | 40 | 0.0% |
| `issue` | 4 | vela | 8 | 84 | 3290 | 150 | 9 | 0.0% |
| `render-200` | 1 | sveltekit | 133 | 626 | 192 | 42955 | 142 | 0.0% |
| `issue` | 1 | sveltekit | 27 | 215 | 1376 | 283 | 34 | 0.0% |
| `render-200` | 4 | sveltekit | 66 | 563 | 479 | 5590 | 77 | 0.0% |
| `issue` | 4 | sveltekit | 13 | 126 | 2572 | 225 | 16 | 0.0% |

Both recover within the first 5 s window after the load drops, without
errors. As in the saturation curves, Vela's per-request p95 in the spike is
higher on one CPU while it serves twice the requests, and SvelteKit's
slowest request waits tens of seconds (43 s for `render-200` on one CPU).

## Soak

`--profile soak --duration 30m`, `journey` (pages, list API, comment,
upload, download, 0.2 s think time), 64 VUs, one CPU.

| Minute | Vela req/s | Vela p95 | Vela RSS MB | SvelteKit req/s | SvelteKit p95 | SvelteKit RSS MB |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 402 | 582 | 47 | 282 | 661 | 325 |
| 6 | 426 | 536 | 46 | 284 | 565 | 350 |
| 11 | 389 | 569 | 48 | 281 | 624 | 357 |
| 16 | 400 | 583 | 49 | 283 | 549 | 358 |
| 21 | 407 | 573 | 51 | 278 | 582 | 360 |
| 26 | 393 | 569 | 45 | 276 | 576 | 364 |
| 30 | 394 | 599 | 50 | 262 | 603 | 362 |

No errors in either. Throughput and latency stay level for both over 30
minutes. Vela's memory is flat (45–51 MB); SvelteKit's grows from 325 to
366 MB over the run, flattening towards the end.

## Build and artifacts

`--mode build`.

| | Vela + Go | SvelteKit |
| --- | ---: | ---: |
| Build | vela 0.22 s + esbuild 0.02 s + go build 0.75 s with the build cache (12.8 s without) | vite build 2.42 s |
| Server artifact | 16.2 MB binary, no runtime needed | 1.6 MB build/ + 0.4 MB production node_modules + Node.js |
| Client JavaScript (all pages) | 81 KB, 28 KB gzip, 3 files | 113 KB, 45 KB gzip, 20 files |
| Cold start (process start to the first answer) | 106–161 ms | 121–228 ms |

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
- Report latency with coordinated omission corrected (an open-model k6
  scenario at a fixed arrival rate), so that overload compares what users
  wait for rather than per-request percentiles; Vela's higher single-CPU
  p95 under overload comes from fair CPU sharing (see "Saturation curves").
