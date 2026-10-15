# Results: Vela + Go vs SvelteKit

Measurements of task 058, 2026-10-14; the main table is the third run, after
the runtime fix (`b82dc3c`) and the page-size work of task 059. A second
series on a 4-vCPU cloud server, 2026-10-15, and a second one on one CPU
with the runtime work of task 060 are in
[Server run](#server-run-4-vcpu-cloud-server). Method and contract:
[README.md](README.md). Raw k6 summaries: `bench/results/` (not committed;
the server series is in `bench/results/server-2026-10-15/`).

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
across all three runs. Treat differences under 15% as noise. The server
run below confirms the ratios on other hardware.

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

## Server run (4-vCPU cloud server)

2026-10-15, `shared/run-series.sh 1,2`: the same scenarios and profiles on
a cloud server, everything on one host.

| | |
| --- | --- |
| Machine | DigitalOcean Regular droplet: 4 shared vCPUs (KVM, one thread per core), 8 GB RAM, Ubuntu 26.04.1 |
| Pinning | PostgreSQL on CPU 0 (systemd `CPUAffinity`), the application on 1 (1 CPU) or 1–2 (2 CPUs), k6 on 3 |
| Database | PostgreSQL 18.6, `shared_buffers` 1 GB, `max_connections` 200; same seed, reseeded before every block |
| Code | commit `8ad297f`: the Vela runtime of task 059, **before task 060** |
| Load | k6 1.8.2, 64 VUs, 30 s per scenario after a 10 s warm-up |

**Caveats.**
- **Shared vCPUs.** CPU steal (time the hypervisor gave to other guests,
  `vmstat` every 5 s) averaged 12.5% over the series: p50 11%, p90 23%,
  max 32%. By phase: constant 11%, saturation 9%, spike 11%, soak 17%,
  browser 8%. Absolute numbers are noisy and about a third of the laptop's;
  compare the two applications within the series, not with the laptop.
- **Small configurations.** Only 1 and 2 application CPUs fit next to
  PostgreSQL and k6. PostgreSQL has a single vCPU, so every page with the
  database is limited by it (the dashboard runs at 6 req/s in both).
- **TLS to PostgreSQL in Vela only.** pgx defaults to `sslmode=prefer` and
  Ubuntu's PostgreSQL offers TLS, so Vela encrypted its database traffic
  while postgres.js did not. Both now use `sslmode=disable` (`5055499`);
  locally TLS cost Vela about 15% of the CPU on the issue page. Vela's
  numbers on database pages are therefore slightly understated here.

### Constant load

req/s with p95 latency in milliseconds. **Bold**: more than 15% ahead.

| Scenario | Vela 1 CPU | SvelteKit 1 CPU | Vela 2 CPU | SvelteKit 2 CPU |
| --- | ---: | ---: | ---: | ---: |
| `render-50` | **517** (197) | 199 (394) | **960** (147) | 379 (258) |
| `render-200` | **139** (717) | 68 (1055) | **229** (673) | 121 (692) |
| `render-500` | **66** (1526) | 30 (2294) | **116** (1099) | 54 (1470) |
| `board` | 22 (3091) | 23 (4586) | 22 (3175) | 22 (4953) |
| `issues` | 79 (1107) | 86 (1784) | 80 (1098) | 87 (1557) |
| `issue` | **1033** (83) | 336 (312) | **872** (113) | 616 (190) |
| `dashboard` | 6 | 6 | 6 | 6 |
| `api` | 126 (741) | 136 (1832) | 134 (679) | 142 (1547) |
| `files-100k` | **1549** (89) | 409 (251) | **1294** (107) | 842 (148) |
| `journey` | **103** (3069) | 74 (4073) | 80 (3377) | 73 (3783) |

Peak RSS: Vela 26–97 MB in both configurations; SvelteKit 288–397 MB with
one worker and 626–802 MB with two. Cold start: Vela 117–313 ms,
SvelteKit 373–531 ms.

- **Rendering:** Vela serves 1.9–2.6 times SvelteKit's pages per second
  with one or two CPUs, about the ratio on the laptop.
- **Issue page and files:** 3.1× and 3.8× on one CPU. With two CPUs the gap
  narrows (1.4×, 1.5×) and Vela does not get faster. k6 on its single vCPU
  and PostgreSQL on another are the limit here, not the application: Vela
  uses about one of its two cores.
- **Database pages:** equal throughput at PostgreSQL's limit, with 30–60%
  lower p95 for Vela (`api` 741 against 1832 ms, `board` 3.1 against
  4.6 s).

### Saturation curves

`--profile step`, as on the laptop. req/s, p95 and the slowest request
(max) in milliseconds.

| Scenario | CPUs | VUs | Vela req/s | Vela p95 | Vela max | SvelteKit req/s | SvelteKit p95 | SvelteKit max |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `render-200` | 1 | 10 | 150 | 106 | 204 | 70 | 243 | 365 |
| `render-200` | 1 | 50 | 143 | 548 | 1118 | 71 | 757 | 12508 |
| `render-200` | 1 | 100 | 148 | 979 | 2105 | 70 | 1151 | 48964 |
| `render-200` | 1 | 200 | 147 | 1948 | 3687 | 71 | 1485 | 57735 |
| `render-200` | 1 | 400 | 145 | 3578 | 7052 | 66 | 2080 | 30787 |
| `board` | 1 | 10 | 17 | 744 | 851 | 13 | 1030 | 1117 |
| `board` | 1 | 50 | 23 | 2469 | 2714 | 22 | 3860 | 6415 |
| `board` | 1 | 100 | 22 | 5023 | 5219 | 21 | 8270 | 10046 |
| `board` | 1 | 200 | 22 | 9187 | 9935 | 22 | 17639 | 28674 |
| `board` | 1 | 400 | 23 | 17824 | 18278 | 22 | 25786 | 33113 |
| `issue` | 1 | 10 | 786 | 25 | 65 | 271 | 73 | 275 |
| `issue` | 1 | 50 | 638 | 189 | 431 | 451 | 179 | 328 |
| `issue` | 1 | 100 | 948 | 143 | 327 | 462 | 314 | 511 |
| `issue` | 1 | 200 | 999 | 246 | 384 | 454 | 601 | 1140 |
| `issue` | 1 | 400 | 995 | 496 | 776 | 501 | 1010 | 16653 |
| `api` | 1 | 10 | 155 | 106 | 168 | 149 | 118 | 238 |
| `api` | 1 | 50 | 147 | 466 | 669 | 152 | 620 | 1165 |
| `api` | 1 | 100 | 146 | 877 | 1054 | 149 | 1320 | 2020 |
| `api` | 1 | 200 | 131 | 1813 | 1993 | 143 | 2545 | 3834 |
| `api` | 1 | 400 | 122 | 3765 | 3881 | 129 | 5523 | 7914 |
| `render-200` | 2 | 10 | 217 | 99 | 230 | 139 | 124 | 196 |
| `render-200` | 2 | 50 | 267 | 444 | 882 | 146 | 413 | 912 |
| `render-200` | 2 | 100 | 281 | 776 | 1798 | 133 | 920 | 8987 |
| `render-200` | 2 | 200 | 280 | 1514 | 3570 | 133 | 1301 | 54079 |
| `render-200` | 2 | 400 | 295 | 2859 | 6348 | 127 | 1871 | 28353 |
| `board` | 2 | 10 | 20 | 649 | 737 | 19 | 694 | 818 |
| `board` | 2 | 50 | 22 | 2426 | 2651 | 23 | 2787 | 3849 |
| `board` | 2 | 100 | 22 | 4650 | 4967 | 23 | 7594 | 10342 |
| `board` | 2 | 200 | 23 | 9095 | 9324 | 22 | 17391 | 27819 |
| `board` | 2 | 400 | 22 | 18187 | 18506 | 22 | 26913 | 33301 |
| `issue` | 2 | 10 | 805 | 24 | 59 | 465 | 41 | 126 |
| `issue` | 2 | 50 | 929 | 79 | 198 | 579 | 169 | 450 |
| `issue` | 2 | 100 | 972 | 137 | 259 | 811 | 202 | 454 |
| `issue` | 2 | 200 | 939 | 274 | 478 | 867 | 344 | 592 |
| `issue` | 2 | 400 | 1040 | 457 | 637 | 916 | 624 | 951 |
| `api` | 2 | 10 | 154 | 107 | 177 | 152 | 115 | 230 |
| `api` | 2 | 50 | 152 | 456 | 606 | 134 | 709 | 1158 |
| `api` | 2 | 100 | 139 | 901 | 1037 | 136 | 1499 | 2498 |
| `api` | 2 | 200 | 134 | 1787 | 1937 | 128 | 2952 | 4413 |
| `api` | 2 | 400 | 119 | 4646 | 4871 | 126 | 5564 | 8024 |

The laptop's picture again. Throughput is flat from 10 VUs. SvelteKit's
p95 is sometimes lower under overload while it serves half the requests,
and its slowest requests wait up to 58 s (`render-200`) where Vela's
slowest stays at about twice its p95. On `board` at 400 VUs Vela's p95 is 18 s
against 26–27 s.

### Spike

`--profile spike`: 20 VUs, a jump to 200 VUs for 30 s, back to 20.

| Scenario | CPUs | App | p95 before | worst p95 in the spike | req/s in the spike | slowest request | p95 5 s after | errors |
| --- | ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `render-200` | 1 | vela | 197 | 2017 | 147 | 3801 | 218 | 0.0% |
| `issue` | 1 | vela | 37 | 659 | 782 | 820 | 43 | 0.0% |
| `render-200` | 2 | vela | 181 | 1982 | 262 | 3931 | 172 | 0.0% |
| `issue` | 2 | vela | 31 | 323 | 879 | 446 | 49 | 0.0% |
| `render-200` | 1 | sveltekit | 414 | 60000 | 71 | 60011 | 403 | 7.6% |
| `issue` | 1 | sveltekit | 83 | 510 | 563 | 1893 | 84 | 0.0% |
| `render-200` | 2 | sveltekit | 215 | 35049 | 146 | 38226 | 200 | 0.0% |
| `issue` | 2 | sveltekit | 61 | 414 | 1009 | 768 | 83 | 0.0% |

Windows are tagged by the request's start. Both recover within 5 s of the
drop. With one worker, SvelteKit left 7.6% of the requests that started
during the jump unanswered until k6's 60 s timeout. These are the only
failed requests of the series. With two workers its slowest request took
38 s. Vela's slowest took under 4 s.

### Soak

30 minutes of `journey`, 64 VUs, one CPU.

| Minute | Vela req/s | Vela p95 | Vela RSS MB | SvelteKit req/s | SvelteKit p95 | SvelteKit RSS MB |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 108 | 2560 | 61 | 65 | 3996 | 293 |
| 6 | 106 | 2552 | 45 | 65 | 3791 | 305 |
| 11 | 98 | 2682 | 55 | 63 | 3709 | 312 |
| 16 | 100 | 2674 | 58 | 82 | 2803 | 329 |
| 21 | 97 | 2725 | 53 | 72 | 3209 | 331 |
| 26 | 100 | 2596 | 54 | 74 | 3046 | 331 |
| 30 | 86 | 3041 | 56 | 73 | 2911 | 330 |

No errors in either. Over the whole run Vela served 97 req/s and SvelteKit
70, with p95 2.7 s against 3.3 s. Vela's memory stays at 44–61 MB.
SvelteKit's grows from 293 to 334 MB, as on the laptop. The slow drift in
both throughputs follows the steal time, which was highest during the soak.

### Browser

Chromium on the same host, median of 5 cold loads; hydrated = the
`app-hydrated` mark.

| Page | Profile | Vela FCP | SvelteKit FCP | Vela hydrated | SvelteKit hydrated | Vela TBT | SvelteKit TBT |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| board | desktop | 488 | 1052 | 706 | 1401 | 168 | 583 |
| `render-200` | desktop | 348 | 628 | 415 | 956 | 123 | 310 |
| issues | desktop | 288 | 512 | 263 | 656 | 36 | 168 |
| issue | desktop | 212 | 420 | 185 | 609 | 0 | 24 |
| board | mobile | 1544 | 1464 | 3517 | 5360 | 1203 | 2345 |
| `render-200` | mobile | 1072 | 1216 | 2264 | 3961 | 772 | 1202 |
| issues | mobile | 1008 | 1220 | 1267 | 2328 | 437 | 690 |
| issue | mobile | 776 | 1020 | 860 | 1958 | 219 | 391 |

JavaScript: 25–29 KB in one file against 99–101 KB. Vela hydrates 2.0–3.3
times sooner on desktop and 1.5–2.3 times sooner on mobile (CPU 4× slower,
slow 4G), with half the blocking time or less. Interactions are equal within
noise: typing 64 against 80 ms, drag and drop 152 against 144 ms, status
filter 187 against 198 ms on desktop. The static dashboard is the same in
both.

### Build

| | Vela + Go | SvelteKit |
| --- | ---: | ---: |
| Build | vela 1.85 s + esbuild 0.20 s + go build 3.15 s with the build cache (88.1 s without) | vite build 11.03 s |
| Client JavaScript (all pages) | 81 KB, 28 KB gzip, 3 files | 113 KB, 45 KB gzip, 20 files |

The build step of the series first failed: the runner checked application
cores for `--cpus 1,4` also in build mode, which starts no application. It
was fixed and the build measured separately.

### Second server run: one CPU, task 060

The same server, 2026-10-15 12:40–14:06 UTC, `RUNNER_ARGS='--cores-db 0,1
--cores-app 1=2 --cores-k6 3' SOAK_DURATION=15m shared/run-series.sh 1`.

What changed from the first run:
- **Code.** Commit `e83fe98`, with the runtime work of task 060.
- **Database.** PostgreSQL got two vCPUs (0–1); only the 1-CPU
  configuration was measured.
- **TLS.** Both applications connect without it.
- **Soak.** 15 minutes instead of 30.

Raw results are in `bench/results/server-2026-10-15-1cpu/`.

**The host was slower this time.** Steal averaged 18.6% (p90 32%, max
47%) against 12.5% in the first run; during the constant load it was 24%
against 11%. SvelteKit, whose code did not change, rendered 13–22% fewer
pages than in the first run. Absolute numbers of the two runs are
therefore not comparable. The ratios within each run are.

#### Constant load, Vela against SvelteKit

req/s (p95 ms). The last two columns give Vela's lead in req/s, first run
→ this run.

| Scenario | Vela | SvelteKit | Vela ahead, first run | Vela ahead, this run |
| --- | ---: | ---: | ---: | ---: |
| `render-50` | 1131 (97) | 155 (543) | +160% | **+630%** |
| `render-200` | 261 (381) | 53 (1429) | +104% | **+392%** |
| `render-500` | 111 (1171) | 26 (2734) | +120% | **+327%** |
| `board` | 31 (2648) | 19 (4507) | −4% | **+63%** |
| `issues` | 113 (816) | 111 (1027) | −8% | +2% |
| `issue` | 678 (155) | 290 (336) | +207% | +134% |
| `dashboard` | 9 (8423) | 8 (11621) | 0% | +12% |
| `api` | 217 (446) | 192 (706) | −7% | +13% |
| `files-100k` | 1126 (123) | 329 (319) | +279% | +242% |
| `journey` | 130 (2205) | 94 (2420) | +39% | +38% |

Peak RSS: Vela 24–60 MB, SvelteKit 286–393 MB. Cold start 353 against
410 ms.

- **Rendering.** Despite the slower host, Vela renders 68–119% more pages
  than in the first run (`render-200` 139 → 261 req/s) while SvelteKit
  renders 13–22% fewer. This is the effect of task 060 under load:
  `BenchmarkRenderBoard` is 2.8 times faster. Vela now serves 4.3–7.3
  times SvelteKit's pages, with 57–82% lower p95.
- **Database pages.** With two vCPUs PostgreSQL limits less. Both
  applications serve more: `api` 126 → 217 req/s for Vela, 136 → 192 for
  SvelteKit. Vela gains more, from the runtime work and from dropping TLS:
  `board` +63%, `api` +13%, p95 21–41% lower on every database page.
- **Issue page.** Vela served fewer requests than in the first run (678
  against 1033) while using only 0.73 of its core. The application is not
  the limit here. The saturation curve reaches 873 req/s at 400 VUs.
  k6 on one shared vCPU with 24% steal is the likely limit; the data does
  not prove it. The lead stays 2.3×.

#### Saturation curves

| Scenario | CPUs | VUs | Vela req/s | Vela p95 | Vela max | SvelteKit req/s | SvelteKit p95 | SvelteKit max |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| `render-200` | 1 | 10 | 282 | 64 | 144 | 53 | 388 | 966 |
| `render-200` | 1 | 50 | 322 | 233 | 400 | 68 | 803 | 12351 |
| `render-200` | 1 | 100 | 327 | 429 | 832 | 69 | 1170 | 50894 |
| `render-200` | 1 | 200 | 306 | 942 | 1721 | 67 | 1626 | 57856 |
| `render-200` | 1 | 400 | 286 | 1869 | 3713 | 69 | 1772 | 31188 |
| `board` | 1 | 10 | 26 | 563 | 722 | 20 | 740 | 877 |
| `board` | 1 | 50 | 29 | 2275 | 3444 | 27 | 2586 | 3097 |
| `board` | 1 | 100 | 26 | 4549 | 5236 | 28 | 4731 | 5684 |
| `board` | 1 | 200 | 29 | 7584 | 8403 | 28 | 9660 | 15519 |
| `board` | 1 | 400 | 30 | 13769 | 14257 | 25 | 10512 | 40403 |
| `issue` | 1 | 10 | 460 | 42 | 96 | 250 | 75 | 162 |
| `issue` | 1 | 50 | 744 | 112 | 190 | 339 | 238 | 391 |
| `issue` | 1 | 100 | 745 | 207 | 370 | 357 | 429 | 614 |
| `issue` | 1 | 200 | 795 | 390 | 705 | 454 | 614 | 1654 |
| `issue` | 1 | 400 | 873 | 692 | 940 | 435 | 1138 | 20096 |
| `api` | 1 | 10 | 222 | 87 | 228 | 174 | 111 | 283 |
| `api` | 1 | 50 | 233 | 332 | 630 | 246 | 365 | 741 |
| `api` | 1 | 100 | 207 | 689 | 1176 | 257 | 716 | 1601 |
| `api` | 1 | 200 | 191 | 1466 | 1672 | 246 | 1604 | 3446 |
| `api` | 1 | 400 | 195 | 2488 | 2725 | 243 | 3135 | 5109 |

Vela's rendering plateau is 4.1–5.3 times SvelteKit's, the issue page's
1.8–2.2 times. SvelteKit's slowest requests still wait 31–58 s on
`render-200` from 100 VUs on and 40 s on `board` at 400 VUs. Vela's slowest stays at about
twice its p95.

#### Spike

| Scenario | CPUs | App | p95 before | worst p95 in the spike | req/s in the spike | slowest request | p95 5 s after | errors |
| --- | ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `render-200` | 1 | vela | 92 | 852 | 338 | 1631 | 93 | 0.0% |
| `issue` | 1 | vela | 34 | 418 | 1125 | 671 | 52 | 0.0% |
| `render-200` | 1 | sveltekit | 531 | 60001 | 57 | 60017 | 416 | 15.1% |
| `issue` | 1 | sveltekit | 144 | 1434 | 361 | 5783 | 128 | 0.0% |

With one worker, SvelteKit again left the requests that started during
the jump unanswered until k6's 60 s timeout: 15.1% of that window. Vela's
slowest request took 1.6 s. Both recover within 5 s.

#### Soak, browser and build

- **Soak**, 15 minutes of `journey`. Vela 189 req/s, SvelteKit 139
  (+36%), with p95 1.34 against 1.38 s. Memory: Vela 43–47 MB, SvelteKit
  305 → 330 MB. No errors.
- **Browser, time to hydrated.** Vela is ahead by 39–60% on desktop
  (board 599 against 988 ms, issue page 153 against 385 ms) and by 28–57%
  on the mobile profile. Interactions are equal within the timer's 8 ms
  steps.
- **Build.** Vela 1.02 s + esbuild 0.09 s + go build 2.79 s with the cache
  (58.4 s without), against vite build 9.10 s.

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

- **Task 060** (after both series; not yet in the tables above). Profiling
  the server under load (`PPROF_ADDR`) found:
  - template strings formatted through reflection and `fmt.Sprint`;
  - prop defaults checked through reflection;
  - a hash map per object;
  - every HTML write going through two interface calls;
  - the root props encoded twice.

  `BenchmarkRenderBoard` went from 1.76 ms, 9990 allocations and 784 KB to
  0.62 ms, 3044 allocations and 233 KB. A short check at 16 VUs on one CPU
  went from 414 to 965 req/s (`render-200`), 3060 to 3872 (`issue`) and 68
  to 93 (`board`). The second server run measures it under load: Vela's
  rendering went from 2.0–2.6 to 4.3–7.3 times SvelteKit's on one CPU.

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

- A series on dedicated vCPUs (no steal) with more cores, so that k6 and
  PostgreSQL do not limit Vela, and with the laptop table re-measured on
  the code of task 060.
- Report latency with coordinated omission corrected (an open-model k6
  scenario at a fixed arrival rate), so that overload compares what users
  wait for rather than per-request percentiles; Vela's higher single-CPU
  p95 under overload comes from fair CPU sharing (see "Saturation curves").
