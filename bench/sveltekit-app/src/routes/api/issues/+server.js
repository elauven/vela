import { json } from '@sveltejs/kit';
import { sql } from '#lib/server/db.js';
import { STATUS_TITLES, issueList, parseFilters } from '#lib/server/data.js';

export async function GET({ url }) {
  return json(await issueList(parseFilters(url)));
}

export async function POST({ request }) {
  const body = await request.json().catch(() => null);
  const title = typeof body?.title === 'string' ? body.title.trim() : '';
  const status = body?.status ?? 'backlog';
  const priority = body?.priority ?? 0;
  if (!body || !title || !STATUS_TITLES[status] || !Number.isInteger(priority) || priority < 0 || priority > 3) {
    return json({ error: 'invalid issue' }, { status: 400 });
  }
  const created = await sql.begin(async (tx) => {
    const [p] = await tx`UPDATE projects SET next_number = next_number + 1 WHERE id = ${body.projectId} RETURNING next_number - 1 AS number, key`;
    if (!p) return null;
    const [row] = await tx`INSERT INTO issues (project_id, number, title, status, priority, position, created_at, updated_at)
      VALUES (${body.projectId}, ${p.number}, ${title}, ${status}, ${priority},
              (SELECT coalesce(max(position), 0) + 1024 FROM issues WHERE project_id = ${body.projectId} AND status = ${status}), now(), now())
      RETURNING id::int`;
    return { id: row.id, key: `${p.key}-${p.number}` };
  });
  if (!created) return json({ error: 'unknown project' }, { status: 400 });
  return json(created, { status: 201 });
}
