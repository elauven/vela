// Package color is the matcher of [hex=color]; src/params/color.js is its
// JavaScript half.
package color

// Match accepts six lowercase hexadecimal digits.
func Match(segment string) bool {
	if len(segment) != 6 {
		return false
	}
	for i := 0; i < len(segment); i++ {
		c := segment[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
