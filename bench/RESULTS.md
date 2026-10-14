# Results: Vela + Go vs SvelteKit

First measurements of task 058, 2026-10-14. Method and contract:
[README.md](README.md). Raw k6 summaries: `bench/results/` (not committed).

## Environment

| | |
| --- | --- |
| Machine | Laptop, Intel Core i7-8550U (4 cores, 8 threads, frequency scaling), 8 GB RAM, Linux 7.0 |
| Pinning | `taskset`: PostgreSQL on logical CPUs 0–1, the application on 2 (1 CPU) or 2–5 (4 CPUs), k6 on 6–7 |
| Database | PostgreSQL 18.6, default settings (`shared_buffers` 128 MB, `max_connections` 100); seed: 20 projects, 50 000 issues, 200 000 comments; reseeded before every application × CPU block |
| Vela | compiler of commit `b82dc3c`, Go 1.27.1, `vela --ssr --mode=production`, client bundled with esbuild; `GOMAXPROCS` = CPUs |
| SvelteKit | 3.0.1, Svelte 5.57.2, Vite 8.3.4, adapter-node 6.0.0, Node 24.21; 4 CPUs = 4 `node:cluster` workers |
| Load | k6 1.8.2, 64 VUs, 30 s per scenario after a 10 s warm-up (`node shared/runner.mjs`) |

**Caveats.** One noisy laptop, not a server: logical CPUs share physical
cores (hyper-threading), so the 4-CPU configuration competes with
PostgreSQL and k6, and turbo frequency varies. Two runs of the same
configuration differed by up to ~10%. Treat differences under 15% as
noise. A server run with `docker-compose.yml` is still to do.

## Main table

Vela after the runtime fix (`b82dc3c`, second run) and SvelteKit (second
run). req/s with p95 latency in milliseconds.

| Scenario | What it measures | Vela 1 CPU | SvelteKit 1 CPU | Vela 4 CPU | SvelteKit 4 CPU |
| --- | --- | ---: | ---: | ---: | ---: |
| `render-50` | SSR of 50 cards, no database | **1126** (152) | 675 (131) | **2563** (65) | 1443 (77) |
| `render-200` | SSR of 200 cards, no database | **338** (667) | 190 (405) | **816** (194) | 505 (200) |
| `render-500` | SSR of 500 cards, no database | **149** (1140) | 86 (783) | **402** (371) | 221 (471) |
| `board` | Board page: database + up to 400 cards | 75 (1076) | 65 (1338) | 65 (1106) | 71 (1084) |
| `issues` | Issue list page | 406 (226) | 390 (329) | 348 (273) | 339 (431) |
| `issue` | Issue page | **2782** (29) | 1220 (74) | **3325** (25) | 2411 (44) |
| `dashboard` | Aggregates over all issues, static page | 28 | 26 | 28 | 29 |
| `api` | JSON API, 80% reads / 20% writes | 588 (152) | 571 (448) | 497 (181) | 532 (265) |
| `files-100k` | 100 KB uploads and downloads | **1410** (84) | 931 (104) | **2894** (42) | 1525 (71) |
| `journey` | User path with think time | 333 (723) | 287 (587) | 365 (585) | 280 (1137) |

Resources under the same load:

| | Vela | SvelteKit |
| --- | --- | --- |
| Peak RSS, 1 CPU | 24–60 MB | 330–405 MB |
| Peak RSS, 4 CPUs | 30–141 MB | 1.3–1.5 GB |
| CPU at the same req/s (`issue`, 4 CPUs) | 2.1 cores for 3325 req/s | 3.7 cores for 2411 req/s |

## Reading the numbers

- **Server rendering.** Without the database Vela renders 1.6–1.8 times
  more pages per second than SvelteKit on the same CPU. The median latency
  is lower too, but Vela's p95 on one CPU is higher: 64 requests share one
  `GOMAXPROCS` slot and the garbage collector, while Node serves them
  strictly one after another. With 4 CPUs the tails are equal or better.
- **Pages with the database.** Board, list, dashboard and the API are
  limited by PostgreSQL on two logical CPUs. Both versions reach about the
  same throughput there; the dashboard spends almost all its time in
  aggregate queries (~2.3 s median at 64 VUs for both).
- **Small pages and files.** The issue page and file transfers favour Go
  clearly: 1.4–2.3 times the throughput with less CPU.
- **Memory.** Vela uses about a tenth of SvelteKit's memory, and its
  memory does not grow with worker processes.
- **Bytes on the wire.** Vela's pages are larger (board: 544 KB against
  310 KB) for the reasons in "Findings for Vela" below; SvelteKit sends less
  per page.

## Before and after the runtime fix

The first run showed Vela rendering **slower** than SvelteKit. Profiling
(`go test -bench BenchmarkRenderBoard` in `vela-app/server`) found that 60%
of the render time was the JSON encoding of the hydration payload:
`*vela.Object` was encoded through `json.Marshal` level by level, which
re-validated the nested output again at every level. Commit `b82dc3c`
replaced it with a single-pass encoder that writes the same bytes, plus two
smaller fixes (`Object.Keys` without re-sorting, `WriteString` without a
copy).

| | Before | After |
| --- | ---: | ---: |
| `BenchmarkRenderBoard` (200 cards) | 8.1 ms, 67 000 allocations | 2.2 ms, 12 400 allocations |
| `render-200`, 1 CPU | 110 req/s | 338 req/s |
| `render-200`, 4 CPUs | 290 req/s | 816 req/s |
| `board`, 1 CPU | 41 req/s | 75 req/s |
| `issue`, 1 CPU | 2071 req/s | 2782 req/s |

All compiler suites and the strict browser baseline pass with the new
encoder; a test compares its output with `encoding/json` on thousands of
random values.

## Findings for Vela

Found by this benchmark; not yet fixed (candidates for a follow-up task):

1. **State initialized from a prop is serialized twice.** In
   `let board = $state(columns)` the payload carries `columns` as a root prop
   and `board` as state: 106 KB + 106 KB on the board page.
2. **Empty state entries.** Every child component instance without state
   adds `["s","Card",{}]` to the payload: about 800 entries and 13 KB on the
   board.
3. **Scope attributes without styles.** Every element carries
   `vela-xxxxxx`, the root of a child component twice, even when no
   component has a `<style>`.
4. **Inter-element whitespace.** Vela drops whitespace-only text between
   elements where Svelte keeps one space, so the visible text differs by
   spaces (the parity check ignores whitespace).

Fixed during the task: the linear payload encoder (above), and missing
browser globals (`XMLHttpRequest`, `Intl`, `AbortController`, observers…)
in the compiler's scope analysis.

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
- Fix the Vela findings above and measure again.
