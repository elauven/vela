import { issueList, parseFilters, projects } from '#lib/server/data.js';

export async function load({ url }) {
  const filters = parseFilters(url);
  const [list, all] = await Promise.all([issueList(filters), projects()]);
  return { projects: all.map((p) => ({ id: p.id, key: p.key, name: p.name })), filters, ...list };
}
