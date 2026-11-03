// Six lowercase hexadecimal digits; server/params/hex is the Go half.
export function match(segment) {
  return /^[0-9a-f]{6}$/.test(segment);
}
