package app

// The built-in matchers (docs/APP.md P-13). @govela/app/match has the same
// functions; app/testdata/matchers.json tests both.

// MatchInt accepts one or more ASCII digits, without a sign.
func MatchInt(segment string) bool {
	if segment == "" {
		return false
	}
	for i := 0; i < len(segment); i++ {
		if segment[i] < '0' || segment[i] > '9' {
			return false
		}
	}
	return true
}

// MatchUUID accepts 8-4-4-4-12 hexadecimal digits in either case.
func MatchUUID(segment string) bool {
	if len(segment) != 36 {
		return false
	}
	for i := 0; i < len(segment); i++ {
		c := segment[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return false
			}
		}
	}
	return true
}

// MatchSlug accepts groups of [a-z0-9] joined by single hyphens.
func MatchSlug(segment string) bool {
	if segment == "" || segment[0] == '-' || segment[len(segment)-1] == '-' {
		return false
	}
	for i := 0; i < len(segment); i++ {
		c := segment[i]
		switch {
		case c >= 'a' && c <= 'z' || c >= '0' && c <= '9':
		case c == '-' && segment[i-1] != '-':
		default:
			return false
		}
	}
	return true
}

// BuiltinMatchers returns the built-in matchers by name.
func BuiltinMatchers() map[string]func(string) bool {
	return map[string]func(string) bool{"int": MatchInt, "uuid": MatchUUID, "slug": MatchSlug}
}
