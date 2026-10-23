/**
 * Formats a count for display.
 * @param {number} n
 * @param {string} [unit]
 */
export function format(n, unit = 'items') {
  return `${n} ${unit}`;
}
