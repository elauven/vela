# Vela + Go vs SvelteKit benchmark

One application, an issue tracker with attachments, written twice:

| Directory | Stack |
| --- | --- |
| [`vela-app/`](vela-app) | Go `net/http` + pgx; pages are Vela components compiled with `vela --ssr`, hydrated in the browser |
| [`sveltekit-app/`](sveltekit-app) | SvelteKit (Svelte 5), `adapter-node`, postgres.js |
| [`shared/`](shared) | Schema, render fixture, stylesheet, k6 scenarios, contract smoke test |
| [`seed/`](seed) | Deterministic data generator |

Both versions implement the contract below with the same database, data and
CSS, so the measurements compare the stacks, not two different apps. Method
and fairness rules: task [058](../tasks/058-bench-vela-vs-sveltekit.md);
results: [`RESULTS.md`](RESULTS.md).

## Quick start (local PostgreSQL)

```sh
cd bench
export DATABASE_URL=postgres://bench:bench@localhost:5432/bench
GOWORK=off go run ./seed               # schema + 50k issues, 200k comments (~10 s)

vela-app/build.sh && APP_DIR=$PWD/vela-app vela-app/bin/server   # http://localhost:8081
sveltekit-app/run.sh                                             # http://localhost:8082

node shared/e2e-check.mjs http://localhost:8081      # browser behaviour (writes to the database)
node shared/parity-check.mjs http://localhost:8081 http://localhost:8082
k6 run -e BASE=http://localhost:8081 shared/k6/journey.js
```

Full measurement on one Linux host (pins PostgreSQL to CPUs 0–1 — start it with
`taskset -c 0,1 pg_ctlcluster 18 main start` — the app to 2–5, k6 to 6–7):

```sh
go build -o bin/seed ./seed && vela-app/build.sh && (cd sveltekit-app && npm ci && npm run build)
node shared/runner.mjs                       # all scenarios, 1 and 4 CPUs, both apps
node shared/runner.mjs --apps vela --cpus 1 --scenarios render-200
```

With Docker (not yet run): `APP_CPUS=1 docker compose up --build -d db seed vela
sveltekit` starts PostgreSQL, seeds it, and runs both applications with the
same CPU and memory limits; `docker compose run --rm k6 run -e
BASE=http://vela:8081 /scripts/render.js` runs a scenario.

## Environment

| Variable | Default | Meaning |
| --- | --- | --- |
| `DATABASE_URL` | `postgres://bench:bench@localhost:5432/bench` | PostgreSQL |
| `PORT` | `8081` (Vela), `8082` (SvelteKit) | HTTP port |
| `DB_POOL` | `20` | Connection pool size |
| `STORAGE_DIR` | `bench/storage` | Uploaded files |

## Contract

Status values and titles: `backlog` Backlog, `todo` To do, `in_progress`
In progress, `done` Done. Priorities: 0 None, 1 Low, 2 High, 3 Urgent.
Dates are formatted on the server in UTC: short `Sep 1`, long `Sep 1, 2026`.
Sizes: `512 B`, `12.3 KB`, `4.0 MB`. The current user is user 1.

### Pages

Every page links `/static/app.css` and renders one root component into
`<div id="app">`; the root contains the navigation bar (Board, Issues,
Dashboard) and `<main>`. The dashboard has no client JavaScript in either
version (a static root in Vela, `csr = false` in SvelteKit). On
`/render/board` card moves stay local (`live: false`), because the fixture is
not in the database.

| Route | Data | Client behaviour |
| --- | --- | --- |
| `GET /` | — | `302` to `/projects/1/board` |
| `GET /projects/:id/board` | `project {id, key, name}`, `columns [{status, title, cards: Card[]}]` with at most 100 cards per column ordered by `position`, `assignees [name]` (sorted, distinct, from the cards) | Text and assignee filters; drag a card to another column: optimistic move, `PATCH /api/issues/:id`, rollback on error |
| `GET /render/board?n=N` | The same component and data shape from `shared/fixtures/board.json`, no database: the first `N` cards (default 200, at most 500) grouped by status in order; project `{id: 0, key: "WEB", name: "Render fixture"}` | Same as the board |
| `GET /issues?project=&status=&q=&sort=&page=` | `projects [{id, key, name}]`, `filters {project, status, q, sort, page}`, `rows Row[]` (50 per page), `total`, `pages` | Changing a filter fetches `/api/issues` and replaces the rows (query input debounced 200 ms), `history.replaceState` |
| `GET /issues/:id` | `issue Issue`, `comments Comment[]` (oldest first), `attachments Attachment[]` | Inline title edit (`PATCH`), status select (`PATCH`), comment form (`POST`), file upload with progress (`POST` multipart) |
| `GET /dashboard` | `totals {issues, open, done, comments}`, `byStatus [{status, title, count, pct}]`, `byPriority [{priority, title, count, pct}]`, `projects [{id, key, name, open, done}]`, `recent [{id, key, title, updated}]` (20 most recently updated) | None (a static page) |

Defaults: `project` 1, `status` all, `q` empty (title contains, case
insensitive), `sort` `updated` (also `priority`, `number`), `page` 1.

```
Card       {id, key, title, status, priority, assignee: User|null, labels: Label[], due, comments, attachments}
User       {name, initials, color}
Label      {name, color}
Row        {id, key, title, status, statusTitle, priority, assignee: User|null, updated, comments}
Issue      {id, key, title, description, status, priority, assignee: User|null, labels: Label[],
            project {id, key, name}, due, created, updated}
Comment    {id, author: User, body, created}
Attachment {id, filename, size, sizeLabel, created, url}
```

`key` is `<project key>-<number>`; `due` is `""` when unset; `comments` and
`attachments` on cards and rows are counts.

### API

| Route | Body | Response |
| --- | --- | --- |
| `GET /api/issues?…` | — | `200 {rows, total, pages}` (same query as the page) |
| `POST /api/issues` | `{projectId, title, status?, priority?}` | `201 {id, key}` |
| `PATCH /api/issues/:id` | any of `{title, status, priority, position}` | `200 {id, title, status, priority, position}`; a status change without `position` moves the issue to the end of the column |
| `POST /api/issues/:id/comments` | `{body}` | `201 Comment` |
| `POST /api/issues/:id/attachments` | multipart, field `file` | `201 Attachment`; streamed to `STORAGE_DIR`, SHA-256 computed while writing |
| `GET /files/:id` | — | The file, streamed, with `Content-Type`, `Content-Length`, `ETag` (SHA-256) and `Range` support |
| `GET /healthz` | — | `200 ok` |

Errors: `400` for invalid input, `404` for unknown ids, JSON `{error}`.
