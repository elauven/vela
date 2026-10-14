import { json } from '@sveltejs/kit';
import { sql } from '#lib/server/db.js';
import { STATUS_TITLES } from '#lib/server/data.js';

export async function PATCH({ params, request }) {
  const id = Number.parseInt(params.id, 10);
  if (Number.isNaN(id)) return json({ error: 'not found' }, { status: 404 });
  const body = await request.json().catch(() => null);
  if (!body) return json({ error: 'invalid JSON' }, { status: 400 });
  let title = body.title ?? null;
  if (title !== null) {
    title = String(title).trim();
    if (!title) return json({ error: 'empty title' }, { status: 400 });
  }
  const status = body.status ?? null;
  const priority = body.priority ?? null;
  const position = body.position ?? null;
  if ((status !== null && !STATUS_TITLES[status]) || (priority !== null && !(Number.isInteger(priority) && priority >= 0 && priority <= 3))) {
    return json({ error: 'invalid field' }, { status: 400 });
  }
  const [row] = await sql`
UPDATE issues i SET
  title    = coalesce(${title}, i.title),
  status   = coalesce(${status}, i.status),
  priority = coalesce(${priority}::smallint, i.priority),
  position = CASE WHEN ${position}::double precision IS NOT NULL THEN ${position}::double precision
                  WHEN ${status}::text IS NOT NULL AND ${status} <> i.status THEN
                    (SELECT coalesce(max(o.position), 0) + 1024 FROM issues o WHERE o.project_id = i.project_id AND o.status = ${status})
                  ELSE i.position END,
  updated_at = now()
WHERE i.id = ${id}
RETURNING i.title, i.status, i.priority, i.position`;
  if (!row) return json({ error: 'not found' }, { status: 404 });
  return json({ id, title: row.title, status: row.status, priority: row.priority, position: row.position });
}
