import { error } from '@sveltejs/kit';
import { board } from '#lib/server/data.js';

export async function load({ params }) {
  const id = Number.parseInt(params.id, 10);
  const data = Number.isNaN(id) ? null : await board(id);
  if (!data) error(404, 'not found');
  return { ...data, live: true };
}
