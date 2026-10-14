import { json } from '@sveltejs/kit';
import { createReadStream } from 'node:fs';
import { stat } from 'node:fs/promises';
import { join } from 'node:path';
import { Readable } from 'node:stream';
import { sql } from '#lib/server/db.js';
import { STORAGE_DIR } from '#lib/server/storage.js';

// Streams an attachment with ETag and single-range support.
export async function GET({ params, request }) {
  const id = Number.parseInt(params.id, 10);
  if (Number.isNaN(id)) return json({ error: 'not found' }, { status: 404 });
  const [row] = await sql`SELECT filename, content_type, sha256, storage_key, created_at FROM attachments WHERE id = ${id}`;
  if (!row) return json({ error: 'not found' }, { status: 404 });
  const path = join(STORAGE_DIR, row.storage_key);
  const info = await stat(path).catch(() => null);
  if (!info) return json({ error: 'not found' }, { status: 404 });
  const etag = `"${row.sha256}"`;
  const headers = {
    'content-type': row.content_type,
    etag,
    'accept-ranges': 'bytes',
    'last-modified': row.created_at.toUTCString(),
    'content-disposition': `attachment; filename="${row.filename.replace(/["\\]/g, '_')}"`,
  };
  if (request.headers.get('if-none-match') === etag) return new Response(null, { status: 304, headers });
  let start = 0;
  let end = info.size - 1;
  let status = 200;
  const range = request.headers.get('range');
  if (range) {
    const m = /^bytes=(\d*)-(\d*)$/.exec(range);
    if (!m || (m[1] === '' && m[2] === '')) return new Response(null, { status: 416, headers: { 'content-range': `bytes */${info.size}` } });
    if (m[1] === '') {
      start = Math.max(0, info.size - Number(m[2]));
    } else {
      start = Number(m[1]);
      if (m[2] !== '') end = Math.min(end, Number(m[2]));
    }
    if (start > end || start >= info.size) return new Response(null, { status: 416, headers: { 'content-range': `bytes */${info.size}` } });
    status = 206;
    headers['content-range'] = `bytes ${start}-${end}/${info.size}`;
  }
  headers['content-length'] = String(end - start + 1);
  return new Response(Readable.toWeb(createReadStream(path, { start, end })), { status, headers });
}
