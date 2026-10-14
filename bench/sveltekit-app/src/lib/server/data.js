// Queries and view models of bench/README.md; the SQL matches
// vela-app/server/data.go (bigint columns are cast to int because
// postgres.js returns int8 as strings).
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { sql } from './db.js';

export const STATUS_ORDER = ['backlog', 'todo', 'in_progress', 'done'];
export const STATUS_TITLES = { backlog: 'Backlog', todo: 'To do', in_progress: 'In progress', done: 'Done' };
const PRIORITY_TITLES = ['None', 'Low', 'High', 'Urgent'];
const PAGE_SIZE = 50;

const short = new Intl.DateTimeFormat('en-US', { month: 'short', day: 'numeric', timeZone: 'UTC' });
const long = new Intl.DateTimeFormat('en-US', { month: 'short', day: 'numeric', year: 'numeric', timeZone: 'UTC' });
export const shortDate = (d) => short.format(d);
export const longDate = (d) => long.format(d);
const dueLabel = (d) => (d ? shortDate(d) : '');

export function sizeLabel(n) {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

function initials(name) {
  const letters = name.split(/\s+/).filter(Boolean).map((w) => w[0].toUpperCase());
  return letters.length > 2 ? letters[0] + letters[letters.length - 1] : letters.join('');
}

const user = (name, color) => (name == null ? null : { name, initials: initials(name), color });
const labels = (names, colors) => names.map((name, i) => ({ name, color: colors[i] }));

// ── Board ──────────────────────────────────────────────────────────────────

export async function board(projectId) {
  const [project] = await sql`SELECT id, key, name FROM projects WHERE id = ${projectId}`;
  if (!project) return null;
  const rows = await sql`
SELECT i.id::int, p.key || '-' || i.number AS key, i.title, i.status, i.priority, i.due_date, u.name AS uname, u.color AS ucolor,
       (SELECT count(*)::int FROM comments c WHERE c.issue_id = i.id) AS comments,
       (SELECT count(*)::int FROM attachments a WHERE a.issue_id = i.id) AS attachments,
       ARRAY(SELECT l.name FROM issue_labels il JOIN labels l ON l.id = il.label_id WHERE il.issue_id = i.id ORDER BY l.name) AS lnames,
       ARRAY(SELECT l.color FROM issue_labels il JOIN labels l ON l.id = il.label_id WHERE il.issue_id = i.id ORDER BY l.name) AS lcolors
FROM (SELECT *, row_number() OVER (PARTITION BY status ORDER BY position) AS rn
      FROM issues WHERE project_id = ${projectId}) i
JOIN projects p ON p.id = i.project_id
LEFT JOIN users u ON u.id = i.assignee_id
WHERE i.rn <= 100
ORDER BY i.position`;
  const cards = rows.map((r) => ({
    id: r.id, key: r.key, title: r.title, status: r.status, priority: r.priority,
    assignee: user(r.uname, r.ucolor), labels: labels(r.lnames, r.lcolors),
    due: dueLabel(r.due_date), comments: r.comments, attachments: r.attachments,
  }));
  return { project: { id: project.id, key: project.key, name: project.name }, ...groupColumns(cards) };
}

export function groupColumns(cards) {
  const byStatus = {};
  const names = new Set();
  for (const card of cards) {
    (byStatus[card.status] ||= []).push(card);
    if (card.assignee) names.add(card.assignee.name);
  }
  const columns = STATUS_ORDER.map((status) => ({ status, title: STATUS_TITLES[status], cards: byStatus[status] || [] }));
  // Byte order, as Go's sort.Strings.
  const assignees = [...names].sort((a, b) => (a < b ? -1 : a > b ? 1 : 0));
  return { columns, assignees };
}

// Paths are relative to the application directory, as in the Go version.
const fixtureCards = JSON.parse(readFileSync(resolve(process.env.FIXTURE || '../shared/fixtures/board.json'), 'utf8'));

export function renderFixture(n) {
  return { project: { id: 0, key: 'WEB', name: 'Render fixture' }, ...groupColumns(fixtureCards.slice(0, Math.max(0, Math.min(n, fixtureCards.length)))) };
}

// ── Issue list ─────────────────────────────────────────────────────────────

export function parseFilters(url) {
  const q = url.searchParams;
  const project = Number.parseInt(q.get('project'), 10);
  const page = Number.parseInt(q.get('page'), 10);
  const status = q.get('status') || '';
  const sort = q.get('sort');
  return {
    project: String(project > 0 ? project : 1),
    status: STATUS_TITLES[status] ? status : '',
    q: (q.get('q') || '').trim(),
    sort: sort === 'priority' || sort === 'number' ? sort : 'updated',
    page: page > 0 ? page : 1,
  };
}

const LIST_ORDER = {
  updated: 'i.updated_at DESC, i.id DESC',
  priority: 'i.priority DESC, i.updated_at DESC, i.id DESC',
  number: 'i.number ASC',
};

const likePattern = (q) => '%' + q.replace(/[\\%_]/g, (c) => '\\' + c) + '%';

export async function issueList(f) {
  const project = Number(f.project);
  const pattern = likePattern(f.q);
  const where = sql`i.project_id = ${project} AND (${f.status} = '' OR i.status = ${f.status}) AND (${f.q} = '' OR i.title ILIKE ${pattern})`;
  const [{ total }] = await sql`SELECT count(*)::int AS total FROM issues i WHERE ${where}`;
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE));
  const result = await sql`
SELECT i.id::int, p.key || '-' || i.number AS key, i.title, i.status, i.priority, u.name AS uname, u.color AS ucolor, i.updated_at,
       (SELECT count(*)::int FROM comments c WHERE c.issue_id = i.id) AS comments
FROM issues i JOIN projects p ON p.id = i.project_id LEFT JOIN users u ON u.id = i.assignee_id
WHERE ${where} ORDER BY ${sql.unsafe(LIST_ORDER[f.sort])} LIMIT 50 OFFSET ${(f.page - 1) * PAGE_SIZE}`;
  const rows = result.map((r) => ({
    id: r.id, key: r.key, title: r.title, status: r.status, statusTitle: STATUS_TITLES[r.status], priority: r.priority,
    assignee: user(r.uname, r.ucolor), updated: shortDate(r.updated_at), comments: r.comments,
  }));
  return { rows, total, pages };
}

export async function projects() {
  return sql`SELECT id, key, name FROM projects ORDER BY id`;
}

// ── Issue page ─────────────────────────────────────────────────────────────

export const commentObject = (r) => ({ id: r.id, author: user(r.name, r.color), body: r.body, created: longDate(r.created_at) });

export const attachmentObject = (r) => ({
  id: r.id, filename: r.filename, size: r.size, sizeLabel: sizeLabel(r.size), created: longDate(r.created_at), url: `/files/${r.id}`,
});

export async function issue(id) {
  const [r] = await sql`
SELECT i.number, i.title, i.description, i.status, i.priority, i.due_date, i.created_at, i.updated_at, u.name AS uname, u.color AS ucolor,
       p.id AS pid, p.key AS pkey, p.name AS pname,
       ARRAY(SELECT l.name FROM issue_labels il JOIN labels l ON l.id = il.label_id WHERE il.issue_id = i.id ORDER BY l.name) AS lnames,
       ARRAY(SELECT l.color FROM issue_labels il JOIN labels l ON l.id = il.label_id WHERE il.issue_id = i.id ORDER BY l.name) AS lcolors
FROM issues i JOIN projects p ON p.id = i.project_id LEFT JOIN users u ON u.id = i.assignee_id
WHERE i.id = ${id}`;
  if (!r) return null;
  const comments = await sql`SELECT c.id::int, u.name, u.color, c.body, c.created_at FROM comments c JOIN users u ON u.id = c.author_id
                             WHERE c.issue_id = ${id} ORDER BY c.created_at, c.id`;
  const attachments = await sql`SELECT id::int, filename, size::int, created_at FROM attachments WHERE issue_id = ${id} ORDER BY created_at, id`;
  return {
    issue: {
      id, key: `${r.pkey}-${r.number}`, title: r.title, description: r.description, status: r.status, priority: r.priority,
      assignee: user(r.uname, r.ucolor), labels: labels(r.lnames, r.lcolors), project: { id: r.pid, key: r.pkey, name: r.pname },
      due: dueLabel(r.due_date), created: longDate(r.created_at), updated: longDate(r.updated_at),
    },
    comments: comments.map(commentObject),
    attachments: attachments.map(attachmentObject),
  };
}

// ── Dashboard ──────────────────────────────────────────────────────────────

const pct = (n, total) => (total === 0 ? 0 : Math.round((n * 100) / total));

export async function dashboard() {
  const [t] = await sql`SELECT count(*)::int AS issues, (count(*) FILTER (WHERE status <> 'done'))::int AS open,
                        (count(*) FILTER (WHERE status = 'done'))::int AS done, (SELECT count(*)::int FROM comments) AS comments FROM issues`;
  const statusCounts = Object.fromEntries((await sql`SELECT status, count(*)::int AS n FROM issues GROUP BY status`).map((r) => [r.status, r.n]));
  const priorityCounts = [0, 0, 0, 0];
  for (const r of await sql`SELECT priority, count(*)::int AS n FROM issues GROUP BY priority`) priorityCounts[r.priority] = r.n;
  const projectRows = await sql`SELECT p.id, p.key, p.name, (count(*) FILTER (WHERE i.status <> 'done'))::int AS open,
                                (count(*) FILTER (WHERE i.status = 'done'))::int AS done
                                FROM projects p LEFT JOIN issues i ON i.project_id = p.id GROUP BY p.id ORDER BY p.id`;
  const recent = await sql`SELECT i.id::int, p.key || '-' || i.number AS key, i.title, i.updated_at FROM issues i JOIN projects p ON p.id = i.project_id
                           ORDER BY i.updated_at DESC, i.id DESC LIMIT 20`;
  return {
    totals: t,
    byStatus: STATUS_ORDER.map((s) => ({ status: s, title: STATUS_TITLES[s], count: statusCounts[s] || 0, pct: pct(statusCounts[s] || 0, t.issues) })),
    byPriority: [3, 2, 1, 0].map((p) => ({ priority: p, title: PRIORITY_TITLES[p], count: priorityCounts[p], pct: pct(priorityCounts[p], t.issues) })),
    projects: projectRows.map((r) => ({ id: r.id, key: r.key, name: r.name, open: r.open, done: r.done })),
    recent: recent.map((r) => ({ id: r.id, key: r.key, title: r.title, updated: shortDate(r.updated_at) })),
  };
}
