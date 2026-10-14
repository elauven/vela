import { error } from '@sveltejs/kit';
import { issue } from '#lib/server/data.js';

export async function load({ params }) {
  const id = Number.parseInt(params.id, 10);
  const data = Number.isNaN(id) ? null : await issue(id);
  if (!data) error(404, 'not found');
  return data;
}
