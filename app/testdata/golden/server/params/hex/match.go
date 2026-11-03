package hex

func Match(segment string) bool {
	if len(segment) != 6 {
		return false
	}
	for i := 0; i < len(segment); i++ {
		if c := segment[i]; !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
