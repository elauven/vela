package lsp

import "strings"

// contextKind is what the cursor is in. The scan is lexical, so it works
// on a buffer that does not parse.
type contextKind int

const (
	inNothing  contextKind = iota // outside the sections, or in <style>
	inScript                      // the <script> body
	inText                        // template text
	inTagName                     // after < of a tag
	inAttrName                    // between the attributes of a tag
	inAttrText                    // a quoted static attribute value
	inExpr                        // a {…} expression, in text or an attribute
	inBlock                       // after {# {: or {/ of a block tag
)

type cursorContext struct {
	kind contextKind
	// tag is the name of the tag the cursor is in (inTagName, inAttrName,
	// inAttrText, inExpr of an attribute).
	tag string
	// wordStart starts the word being typed: the tag name, the attribute
	// name with its sigil, the block keyword with its {# prefix, the
	// identifier of an expression.
	wordStart int
}

// section bounds of a .vela source: the body of the first <tag …> up to its
// closing tag, the next section or the end of the file (as sfcast reads an
// unclosed section).
func sectionBody(src, tag string) (start, end int, ok bool) {
	low := strings.ToLower(src)
	open := strings.Index(low, "<"+tag)
	if open < 0 {
		return 0, 0, false
	}
	gt := strings.IndexByte(src[open:], '>')
	if gt < 0 {
		return 0, 0, false
	}
	start = open + gt + 1
	end = len(src)
	if i := strings.Index(low[start:], "</"+tag+">"); i >= 0 {
		end = start + i
	} else {
		for _, next := range []string{"<script", "<style", "<template"} {
			if i := strings.Index(low[start:], next); i >= 0 && start+i < end {
				end = start + i
			}
		}
	}
	return start, end, true
}

func contextAt(src string, off int) cursorContext {
	if start, end, ok := sectionBody(src, "script"); ok && off >= start && off <= end {
		ws, _ := wordAt(src, off)
		return cursorContext{kind: inScript, wordStart: ws}
	}
	start, end, ok := sectionBody(src, "template")
	if !ok || off < start || off > end {
		return cursorContext{kind: inNothing}
	}
	return scanTemplate(src, start, off)
}

// scanTemplate follows the template from start to off.
func scanTemplate(src string, start, off int) cursorContext {
	var (
		inTag, inComment bool
		tag              string
		tagStart         int
		quote, exprQuote byte
		depth, exprStart int
		attrStart        int
		exprInTag        bool
		quoteDynamic     bool
	)
	for i := start; i < off; i++ {
		c := src[i]
		switch {
		case depth > 0:
			switch {
			case exprQuote != 0:
				if c == '\\' {
					i++
				} else if c == exprQuote {
					exprQuote = 0
				}
			case c == '"' || c == '\'' || c == '`':
				exprQuote = c
			case c == '{':
				depth++
			case c == '}':
				depth--
				if depth == 0 && exprInTag {
					attrStart = -1
				}
			}
		case inComment:
			if strings.HasPrefix(src[i:], "-->") {
				inComment = false
				i += 2
			}
		case inTag:
			space := c == ' ' || c == '\t' || c == '\n' || c == '\r'
			if tag == "" {
				if !space && c != '/' && c != '>' {
					continue // the tag name
				}
				tag = src[tagStart+1 : i]
			}
			switch {
			case quote != 0:
				if c == quote {
					quote, attrStart = 0, -1
				}
			case c == '"' || c == '\'':
				quote = c
				quoteDynamic = false
				if attrStart >= 0 {
					quoteDynamic = isDynamicAttr(strings.TrimSuffix(strings.TrimSpace(src[attrStart:i]), "="))
				}
			case c == '{':
				depth, exprStart, exprInTag = 1, i, true
			case c == '>':
				inTag = false
			case space || c == '/':
				attrStart = -1
			case attrStart < 0:
				attrStart = i
			}
		default:
			switch {
			case strings.HasPrefix(src[i:], "<!--"):
				inComment = true
				i += 3
			case c == '<' && (i+1 == off || isIdentByte(src[i+1]) || src[i+1] == '/'):
				inTag, tag, tagStart, attrStart, quote = true, "", i, -1, 0
			case c == '{':
				depth, exprStart, exprInTag = 1, i, false
			}
		}
	}
	switch {
	case depth > 0:
		if !exprInTag && exprStart+1 < len(src) && strings.IndexByte("#:/", src[exprStart+1]) >= 0 && !strings.ContainsAny(src[exprStart+1:off], " \t\n") {
			return cursorContext{kind: inBlock, wordStart: exprStart}
		}
		ws, _ := wordAt(src, off)
		ctx := cursorContext{kind: inExpr, wordStart: ws}
		if exprInTag {
			ctx.tag = tagName(src, tagStart)
		}
		return ctx
	case inComment:
		return cursorContext{kind: inNothing}
	case inTag && tag == "":
		return cursorContext{kind: inTagName, tag: src[tagStart+1 : off], wordStart: tagStart + 1}
	case inTag && quote != 0 && quoteDynamic:
		// :name="expression", the quoted form the compiler also reads.
		ws, _ := wordAt(src, off)
		return cursorContext{kind: inExpr, tag: tag, wordStart: ws}
	case inTag && quote != 0:
		return cursorContext{kind: inAttrText, tag: tag}
	case inTag:
		ws := off
		if attrStart >= 0 {
			ws = attrStart
		}
		return cursorContext{kind: inAttrName, tag: tag, wordStart: ws}
	}
	ws, _ := wordAt(src, off)
	return cursorContext{kind: inText, wordStart: ws}
}

// tagName is the name of the tag opened at the < at start.
func tagName(src string, start int) string {
	end := start + 1
	for end < len(src) && (isIdentByte(src[end]) || src[end] == ':' || src[end] == '-' || src[end] == '.') {
		end++
	}
	return src[start+1 : end]
}

// isDynamicAttr reports an attribute whose value is an expression.
func isDynamicAttr(name string) bool {
	for _, prefix := range []string{":", "@", "bind:", "class:", "style:"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
