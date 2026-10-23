package compiler

import (
	"strings"
	"unicode"
)

// ─────────────────────────────────────────────────────────────────────────────
//  Minifier — post-process generated JS for production builds
//
//  Strategy: single-pass character-level scan, skipping string/template
//  literals and comments. Compresses:
//    1. Line comments  (// …)          → removed
//    2. Block comments (/* … */)       → removed
//    3. Runs of whitespace             → single space (or nothing at operator boundaries)
//    4. Unnecessary semicolons before }
//    5. Leading/trailing whitespace per line
//
//  We deliberately do NOT rename variables — the $$-prefixed internal names are
//  already short, and renaming user identifiers risks breaking dynamic access.
// ─────────────────────────────────────────────────────────────────────────────

// Minify takes the generated JS source and returns a compact version.
func Minify(src string) string {
	var b strings.Builder
	b.Grow(len(src))

	i := 0
	n := len(src)
	prevWasSpace := false
	prevTok := byte(0) // last non-space, non-comment byte written

	needSpace := func(prev, next byte) bool {
		// `a + +b` and `a - -b` must not merge into `++` / `--` (task 055).
		if (prev == '+' || prev == '-') && prev == next {
			return true
		}
		// No space needed adjacent to these chars
		noSpaceSet := "{}()[];,.:!~<>=+-*/%&|^?`\n"
		return !strings.ContainsRune(noSpaceSet, rune(prev)) &&
			!strings.ContainsRune(noSpaceSet, rune(next))
	}

	write := func(c byte) {
		if c == ' ' || c == '\t' || c == '\r' {
			if !prevWasSpace {
				// Defer actual write until we see the next non-space char
				prevWasSpace = true
			}
			return
		}
		if c == '\n' {
			// Treat newline like space
			if !prevWasSpace {
				prevWasSpace = true
			}
			return
		}
		if prevWasSpace {
			// Flush the deferred space only if it's syntactically needed
			if prevTok != 0 && needSpace(prevTok, c) {
				b.WriteByte(' ')
			}
			prevWasSpace = false
		}
		b.WriteByte(c)
		prevTok = c
	}

	for i < n {
		c := src[i]

		// ── String literals (preserve verbatim) ────────────────────────────
		if c == '"' || c == '\'' {
			if prevWasSpace && prevTok != 0 && needSpace(prevTok, c) {
				b.WriteByte(' ')
			}
			prevWasSpace = false
			start := i
			q := c
			i++
			for i < n {
				ch := src[i]
				if ch == '\\' {
					i += 2
					continue
				}
				if ch == q {
					i++
					break
				}
				i++
			}
			raw := src[start:i]
			b.WriteString(raw)
			prevTok = raw[len(raw)-1]
			continue
		}

		// ── Template literals (preserve verbatim including ${…}) ──────────
		if c == '`' {
			if prevWasSpace && prevTok != 0 && needSpace(prevTok, c) {
				b.WriteByte(' ')
			}
			prevWasSpace = false
			start := i
			i++ // skip opening `
			depth := 0
			for i < n {
				ch := src[i]
				if ch == '\\' {
					i += 2
					continue
				}
				if ch == '$' && i+1 < n && src[i+1] == '{' {
					depth++
					i += 2
					continue
				}
				if ch == '{' && depth > 0 {
					depth++
				} else if ch == '}' && depth > 0 {
					depth--
				} else if ch == '`' && depth == 0 {
					i++
					break
				}
				i++
			}
			raw := src[start:i]
			b.WriteString(raw)
			prevTok = '`'
			continue
		}

		// ── Line comments ─────────────────────────────────────────────────
		if c == '/' && i+1 < n && src[i+1] == '/' {
			for i < n && src[i] != '\n' {
				i++
			}
			prevWasSpace = true
			continue
		}

		// ── Block comments ────────────────────────────────────────────────
		if c == '/' && i+1 < n && src[i+1] == '*' {
			i += 2
			for i < n {
				if src[i] == '*' && i+1 < n && src[i+1] == '/' {
					i += 2
					break
				}
				i++
			}
			prevWasSpace = true
			continue
		}

		write(c)
		i++
	}

	result := b.String()

	// ── Post-pass: remove ";" immediately before "}" ───────────────────────
	result = removeSemiBeforeRBrace(result)

	return result
}

// removeSemiBeforeRBrace collapses patterns like "; }" → " }" in one pass.
func removeSemiBeforeRBrace(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	i := 0
	n := len(src)
	for i < n {
		c := src[i]
		if c == ';' {
			// Peek ahead for optional space + }
			j := i + 1
			for j < n && (src[j] == ' ' || src[j] == '\t') {
				j++
			}
			if j < n && src[j] == '}' {
				// Skip the semicolon; keep the space and }
				i++
				continue
			}
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

// isIdentChar reports whether c can appear in a JS identifier.
func isIdentChar(c byte) bool {
	return c == '_' || c == '$' || unicode.IsLetter(rune(c)) || (c >= '0' && c <= '9')
}
