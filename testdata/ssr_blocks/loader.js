export async function load(id) {
  if (id < 0) throw new RangeError('negative ' + id);
  const doubled = await Promise.resolve(id * 2);
  return { id, label: 'item-' + doubled };
}
