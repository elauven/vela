import { json } from '@sveltejs/kit';
import { sql } from '#lib/server/db.js';
import { commentObject } from '#lib/server/data.js';

export async function POST({ params, request }) {
  const id = Number.parseInt(params.id, 10);
  if (Number.isNaN(id)) return json({ error: 'not found' }, { status: 404 });
  const body = await request.json().catch(() => null);
  const text = typeof body?.body === 'string' ? body.body.trim() : '';
  if (!text) return json({ error: 'empty comment' }, { status: 400 });
  const [row] = await sql`
WITH c AS (INSERT INTO comments (issue_id, author_id, body, created_at)
           SELECT id, 1, ${text}, now() FROM issues WHERE id = ${id} RETURNING id, author_id, created_at)
SELECT c.id::int, u.name, u.color, c.created_at FROM c JOIN users u ON u.id = c.author_id`;
  if (!row) return json({ error: 'not found' }, { status: 404 });
  return json(commentObject({ ...row, body: text }), { status: 201 });
}
