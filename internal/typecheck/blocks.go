package typecheck

import "strings"

// matchBrace is the index of the } closing the { at open, skipping strings
// and template literals; end when it is not closed.
func matchBrace(src string, open, end int) int {
	depth := 0
	for i := open; i < end; i++ {
		switch c := src[i]; c {
		case '"', '\'', '`':
			for i++; i < end && src[i] != c; i++ {
				if src[i] == '\\' {
					i++
				}
			}
		case '{':
			depth++
		case '}':
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return end
}

// blockKeyword is the #if, :else, /each… at the start of a block tag.
func blockKeyword(tag string) string {
	end := 1
	for end < len(tag) && isIdentByte(tag[end]) {
		end++
	}
	return tag[:end]
}

func writeAwaitName(w *writer, src, name string, at int, isCatch bool, pStart, pEnd int) {
	w.gen("const ")
	w.copy(at, at+len(name))
	if isCatch {
		w.gen(" = $$error;\n")
		return
	}
	w.gen(" = $$awaited(" + src[pStart:pEnd] + ");\n")
}

// writeEach writes the loop of {#each iterable as pattern, index (key)},
// whose header after #each is [start, end).
func writeEach(w *writer, start, end int) {
	src := w.src
	header := src[start:end]
	as := topLevelIndex(header, " as ")
	if as < 0 {
		w.gen("for (const $$item of $$each(")
		w.copyTrimmed(start, end)
		w.gen(")) {\n")
		return
	}
	iterEnd := start + as
	rest := start + as + len(" as ")
	keyStart, keyEnd := -1, -1
	if trimmed := strings.TrimRight(src[rest:end], " \t\r\n"); strings.HasSuffix(trimmed, ")") {
		closeAt := rest + len(trimmed) - 1
		if openAt := matchingParen(src, rest, closeAt); openAt > rest {
			keyStart, keyEnd = openAt+1, closeAt
			end = openAt
		}
	}
	pattern, index := end, -1
	if comma := topLevelLastComma(src[rest:end]); comma >= 0 {
		pattern, index = rest+comma, rest+comma+1
	}
	w.gen("for (const ")
	w.copyTrimmed(rest, pattern)
	w.gen(" of $$each(")
	w.copyTrimmed(start, iterEnd)
	w.gen(")) {\n")
	if index >= 0 {
		// A number the template cannot assign (linters see a constant).
		w.gen("const ")
		w.copyTrimmed(index, end)
		w.gen(" = Number();\n")
	}
	if keyStart >= 0 {
		w.gen("(")
		w.copyTrimmed(keyStart, keyEnd)
		w.gen(");\n")
	}
}

// topLevelIndex is the first index of sep outside brackets, or -1.
func topLevelIndex(s, sep string) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		}
		if depth == 0 && strings.HasPrefix(s[i:], sep) {
			return i
		}
	}
	return -1
}

func topLevelLastComma(s string) int {
	depth, last := 0, -1
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				last = i
			}
		}
	}
	return last
}

// matchingParen is the ( matching the ) at closeAt, searching from start.
func matchingParen(src string, start, closeAt int) int {
	depth := 0
	for i := closeAt; i >= start; i-- {
		switch src[i] {
		case ')':
			depth++
		case '(':
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return -1
}
