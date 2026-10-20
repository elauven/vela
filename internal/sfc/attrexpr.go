package sfc

import "strings"

// AttrExprToJS converts an attribute-value string into a JS
// expression that produces its runtime value.  Three input shapes:
//
//   - `{expr}`               → expr                 (pure JS expression)
//   - `prefix{x}suffix`      → `prefix${x}suffix`   (template literal)
//   - `text`                 → 'text'               (plain string literal)
//
// The middle case is what makes `style:font-size="{size}px"` work:
// the user wants the string "16px" at runtime, not the JS expression
// `{size}px` (which is a syntax error).  Without this conversion every
// unit-suffixed style directive would fail.
//
// Brace-matching honors string literals so `{f("}")}` doesn't end
// at the `}` inside the string.
func AttrExprToJS(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "''"
	}
	// Pure-expression shape: `{...}` and nothing outside the
	// matching braces.
	if strings.HasPrefix(value, "{") && strings.HasSuffix(value, "}") {
		inner := UnwrapBraces(value)
		if inner != value {
			return inner
		}
	}
	// No interpolation at all → plain quoted string.
	if !strings.Contains(value, "{") {
		// Escape single quotes and backslashes.
		s := strings.ReplaceAll(value, "\\", "\\\\")
		s = strings.ReplaceAll(s, "'", "\\'")
		return "'" + s + "'"
	}
	// Mixed: build a template literal.
	var b strings.Builder
	b.WriteByte('`')
	i := 0
	for i < len(value) {
		if value[i] == '{' {
			// Find the matching `}` honoring nested braces & strings.
			depth := 1
			j := i + 1
			for j < len(value) && depth > 0 {
				switch value[j] {
				case '"', '\'':
					q := value[j]
					j++
					for j < len(value) && value[j] != q {
						if value[j] == '\\' && j+1 < len(value) {
							j += 2
							continue
						}
						j++
					}
				case '{':
					depth++
				case '}':
					depth--
				}
				if depth > 0 {
					j++
				}
			}
			b.WriteString("${")
			b.WriteString(value[i+1 : j])
			b.WriteByte('}')
			i = j + 1
			continue
		}
		// Literal char — escape template-literal specials.
		c := value[i]
		switch c {
		case '`':
			b.WriteString("\\`")
		case '\\':
			b.WriteString("\\\\")
		case '$':
			if i+1 < len(value) && value[i+1] == '{' {
				b.WriteString("\\$")
			} else {
				b.WriteByte('$')
			}
		default:
			b.WriteByte(c)
		}
		i++
	}
	b.WriteByte('`')
	return b.String()
}

// UnwrapBraces strips a single layer of outer `{...}` from an attribute
// value when the value is being interpolated as a JavaScript expression.
//
// Why this exists:  the template parser preserves the source `{...}`
// wrapper in Attr.Value (so `:disabled={true}` arrives as the literal
// string `"{true}"`).  Codegen sites that interpolate Attr.Value into
// a JS expression context — props objects, event handler bodies, set()
// calls — must remove that wrapper, or the output becomes either a
// syntax error ("disabled: {true}" → {true} is a block) or wrong
// semantics ("value: {username}" → shorthand object literal instead of
// the variable's value).
//
// Only the outermost matched pair is stripped.  Nested braces inside
// (e.g. `{ {x: 1} }`) are preserved because the inner braces are part
// of the user expression.  If the value doesn't start with `{` it's
// returned verbatim — this covers both quoted attributes and bare
// identifiers.
func UnwrapBraces(s string) string {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '{' || s[len(s)-1] != '}' {
		return s
	}
	// Confirm the outer braces are a matched pair (not e.g. "{a} + {b}").
	// Walk the string tracking brace depth, skipping over string and
	// template literals.  If depth hits zero before the final char, the
	// outer braces aren't a pair and we leave the value alone.
	depth := 0
	i := 0
	for i < len(s) {
		c := s[i]
		switch c {
		case '"', '\'':
			// Skip the string literal.
			q := c
			i++
			for i < len(s) && s[i] != q {
				if s[i] == '\\' && i+1 < len(s) {
					i += 2
					continue
				}
				i++
			}
			i++ // closing quote
		case '`':
			// Skip the template literal.  We don't descend into ${} for
			// brace matching — any { inside a template literal is content,
			// not structure.
			i++
			for i < len(s) && s[i] != '`' {
				if s[i] == '\\' && i+1 < len(s) {
					i += 2
					continue
				}
				i++
			}
			i++
		case '{':
			depth++
			i++
		case '}':
			depth--
			if depth == 0 && i < len(s)-1 {
				// Closed the outer braces before the end → not a pair.
				return s
			}
			i++
		default:
			i++
		}
	}
	return strings.TrimSpace(s[1 : len(s)-1])
}
