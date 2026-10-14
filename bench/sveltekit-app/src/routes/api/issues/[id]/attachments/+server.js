import { json } from '@sveltejs/kit';
import Busboy from 'busboy';
import { createHash, randomBytes } from 'node:crypto';
import { createWriteStream } from 'node:fs';
import { unlink } from 'node:fs/promises';
import { join } from 'node:path';
import { Readable } from 'node:stream';
import { pipeline } from 'node:stream/promises';
import { sql } from '#lib/server/db.js';
import { attachmentObject } from '#lib/server/data.js';
import { STORAGE_DIR } from '#lib/server/storage.js';

const MAX_UPLOAD = 100 * 1024 * 1024;

// Streams the "file" part to STORAGE_DIR, hashing while it writes.
function receive(request) {
  return new Promise((resolve, reject) => {
    const bb = Busboy({ headers: { 'content-type': request.headers.get('content-type') }, limits: { fileSize: MAX_UPLOAD, files: 1 } });
    let handled = false;
    bb.on('file', (name, stream, info) => {
      if (name !== 'file' || handled) {
        stream.resume();
        return;
      }
      handled = true;
      const key = randomBytes(16).toString('hex');
      const path = join(STORAGE_DIR, key);
      const hash = createHash('sha256');
      let size = 0;
      stream.on('data', (chunk) => {
        hash.update(chunk);
        size += chunk.length;
      });
      pipeline(stream, createWriteStream(path))
        .then(() => resolve({ key, path, size, sha256: hash.digest('hex'), filename: info.filename || 'file', contentType: info.mimeType || 'application/octet-stream' }))
        .catch(reject);
    });
    bb.on('close', () => {
      if (!handled) resolve(null);
    });
    bb.on('error', reject);
    Readable.fromWeb(request.body).pipe(bb);
  });
}

export async function POST({ params, request }) {
  const id = Number.parseInt(params.id, 10);
  if (Number.isNaN(id)) return json({ error: 'not found' }, { status: 404 });
  const [exists] = await sql`SELECT 1 FROM issues WHERE id = ${id}`;
  if (!exists) return json({ error: 'not found' }, { status: 404 });
  if (!(request.headers.get('content-type') || '').startsWith('multipart/form-data')) {
    return json({ error: 'multipart body expected' }, { status: 400 });
  }
  let file;
  try {
    file = await receive(request);
  } catch {
    return json({ error: 'upload failed' }, { status: 400 });
  }
  if (!file) return json({ error: 'field "file" missing' }, { status: 400 });
  const filename = file.filename.replaceAll('\\', '/').split('/').pop() || 'file';
  try {
    const [row] = await sql`INSERT INTO attachments (issue_id, filename, content_type, size, sha256, storage_key, created_at)
      VALUES (${id}, ${filename}, ${file.contentType}, ${file.size}, ${file.sha256}, ${file.key}, now()) RETURNING id::int, created_at`;
    return json(attachmentObject({ id: row.id, filename, size: file.size, created_at: row.created_at }), { status: 201 });
  } catch (err) {
    await unlink(file.path).catch(() => {});
    throw err;
  }
}
