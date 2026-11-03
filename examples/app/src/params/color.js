// The matcher of [hex=color] (docs/APP.md P-14): six hexadecimal digits.
// server/params/color/match.go is its Go half.
export function match(segment) {
  return /^[0-9a-f]{6}$/.test(segment);
}
