package lsp

import (
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// A virtual document is the JavaScript TypeScript's language service sees
// for a component (task 065c): the <script> text as written, then the
// template expressions inside a render function whose blocks declare the
// {#each} and {#await} names, then the component export. Text copied from
// the component is mapped in both directions; generated text is not.
//
// It is built lexically, like the cursor context, so a buffer that does
// not parse still has one.

// virtualSuffix names the virtual document of a component: App.vela.js.
const virtualSuffix = ".js"

// segment is text copied from the component: n bytes at src in the
// component and at virt in the virtual document.
type segment struct{ src, virt, n int }

type virtualDoc struct {
	path   string // the component
	source *text  // the component's text
	text   string
	bySrc  []segment
	byVirt []segment
}

func virtualPath(component string) string { return component + virtualSuffix }

// toVirtual maps a component offset; ok is false outside copied text. An
// offset at the end of a segment maps, so a word being typed at the end of
// an expression has a position.
func (v *virtualDoc) toVirtual(off int) (int, bool) {
	i := sort.Search(len(v.bySrc), func(i int) bool { return v.bySrc[i].src+v.bySrc[i].n >= off })
	for ; i < len(v.bySrc) && v.bySrc[i].src <= off; i++ {
		if s := v.bySrc[i]; off <= s.src+s.n {
			return s.virt + off - s.src, true
		}
	}
	return 0, false
}

// toSource maps a virtual offset back to the component.
func (v *virtualDoc) toSource(off int) (int, bool) {
	i := sort.Search(len(v.byVirt), func(i int) bool { return v.byVirt[i].virt+v.byVirt[i].n >= off })
	if i < len(v.byVirt) && v.byVirt[i].virt <= off && off <= v.byVirt[i].virt+v.byVirt[i].n {
		s := v.byVirt[i]
		return s.src + off - s.virt, true
	}
	return 0, false
}

// toSourceRange maps a virtual range; both ends must be in one segment.
func (v *virtualDoc) toSourceRange(start, end int) (int, int, bool) {
	s, ok1 := v.toSource(start)
	e, ok2 := v.toSource(end)
	if !ok1 || !ok2 || e < s {
		return 0, 0, false
	}
	return s, e, true
}

type virtualWriter struct {
	src  string
	b    strings.Builder
	segs []segment
}

func (w *virtualWriter) gen(s string) { w.b.WriteString(s) }

// copy copies src[start:end]; an empty range is still a mapped position,
// where a name is about to be typed.
func (w *virtualWriter) copy(start, end int) {
	if end < start {
		return
	}
	w.segs = append(w.segs, segment{src: start, virt: w.b.Len(), n: end - start})
	w.b.WriteString(w.src[start:end])
}

// copyTrimmed copies src[start:end] without its surrounding white space.
func (w *virtualWriter) copyTrimmed(start, end int) {
	for start < end && isSpaceByte(w.src[start]) {
		start++
	}
	for end > start && isSpaceByte(w.src[end-1]) {
		end--
	}
	w.copy(start, end)
}

func isSpaceByte(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// buildVirtual builds the virtual document of the component at path.
func buildVirtual(path, src string) *virtualDoc {
	w := &virtualWriter{src: src}
	if start, end, ok := sectionBody(src, "script"); ok {
		w.copy(start, end)
	}
	w.gen("\n;\nfunction $$render() {\n")
	if start, end, ok := sectionBody(src, "template"); ok {
		writeTemplate(w, start, end)
	}
	w.gen("}\n")
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if isIdentifier(name) {
		w.gen("export function " + name + "($$target, $$props) {}\nexport default " + name + ";\n")
	}
	v := &virtualDoc{path: path, source: newText(src), text: w.b.String(), byVirt: w.segs}
	v.bySrc = append([]segment(nil), w.segs...)
	sort.SliceStable(v.bySrc, func(i, j int) bool { return v.bySrc[i].src < v.bySrc[j].src })
	return v
}

func isIdentifier(s string) bool {
	if s == "" || s[0] >= '0' && s[0] <= '9' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isIdentByte(s[i]) {
			return false
		}
	}
	return true
}

// templateRegion is a {…} of the template or the quoted value of a dynamic
// attribute: [start, end) is its content.
type templateRegion struct {
	start, end int
	tag, attr  string // the tag and attribute of an attribute value
}

// templateRegions scans the template body [start, end).
func templateRegions(src string, start, end int) []templateRegion {
	var out []templateRegion
	inTag := false
	tag := ""
	attrStart := -1
	for i := start; i < end; i++ {
		c := src[i]
		switch {
		case strings.HasPrefix(src[i:], "<!--"):
			if j := strings.Index(src[i:end], "-->"); j >= 0 {
				i += j + 2
			} else {
				i = end
			}
		case !inTag && c == '<' && i+1 < end && (isIdentByte(src[i+1]) || src[i+1] == '/'):
			inTag, attrStart = true, -1
			tag = tagName(src, i)
			i += len(tag)
		case inTag && c == '>':
			inTag = false
		case inTag && (c == '"' || c == '\''):
			j := strings.IndexByte(src[i+1:end], c)
			if j < 0 {
				j = end - i - 1
			}
			if attrStart >= 0 {
				if name := strings.TrimSpace(strings.TrimSuffix(src[attrStart:i], "=")); isDynamicAttr(name) {
					out = append(out, templateRegion{start: i + 1, end: i + 1 + j, tag: tag, attr: name})
				}
			}
			i += j + 1
			attrStart = -1
		case c == '{':
			close := matchBrace(src, i, end)
			region := templateRegion{start: i + 1, end: close}
			if inTag && attrStart >= 0 {
				region.tag, region.attr = tag, strings.TrimSpace(strings.TrimSuffix(src[attrStart:i], "="))
			}
			if close == end || src[close] != '}' {
				// An expression being typed ends at the next tag or line.
				region.end = i + 1
				for region.end < end && src[region.end] != '<' && src[region.end] != '\n' {
					region.end++
				}
				close = region.end - 1
			}
			out = append(out, region)
			i = close
			if inTag {
				attrStart = -1
			}
		case inTag && isSpaceByte(c), inTag && c == '/':
			attrStart = -1
		case inTag && attrStart < 0:
			attrStart = i
		}
	}
	return out
}

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

// writeTemplate writes the render function body of the template [start, end).
func writeTemplate(w *virtualWriter, start, end int) {
	src := w.src
	type open struct {
		kind         string
		promiseStart int // #await: the promise expression
		promiseEnd   int
	}
	var stack []open
	closeTo := func(kind string) {
		for i := len(stack) - 1; i >= 0; i-- {
			if stack[i].kind == kind {
				for len(stack) > i {
					stack = stack[:len(stack)-1]
					w.gen("}\n")
				}
				return
			}
		}
	}
	top := func() string {
		if len(stack) == 0 {
			return ""
		}
		return stack[len(stack)-1].kind
	}
	for _, r := range templateRegions(src, start, end) {
		content := src[r.start:r.end]
		trimmed := strings.TrimSpace(content)
		switch {
		case r.attr != "":
			writeAttr(w, r)
		case strings.HasPrefix(trimmed, "#"), strings.HasPrefix(trimmed, ":"), strings.HasPrefix(trimmed, "/"):
			at := r.start + strings.Index(content, trimmed)
			keyword := blockKeyword(trimmed)
			rest := at + len(keyword) // the block's expression
			switch keyword {
			case "#if":
				w.gen("if (")
				w.copyTrimmed(rest, r.end)
				w.gen(") {\n")
				stack = append(stack, open{kind: "if"})
			case ":else":
				if strings.HasPrefix(strings.TrimSpace(src[rest:r.end]), "if") {
					cond := rest + strings.Index(src[rest:r.end], "if") + 2
					w.gen("} else if (")
					w.copyTrimmed(cond, r.end)
					w.gen(") {\n")
				} else if top() == "if" {
					w.gen("} else {\n")
				} else if top() != "" {
					w.gen("} {\n")
				}
			case "#each":
				writeEach(w, rest, r.end)
				stack = append(stack, open{kind: "each"})
			case "#await":
				p := strings.TrimSpace(src[rest:r.end])
				pStart := rest + strings.Index(src[rest:r.end], p)
				pEnd := pStart + len(p)
				name, nameStart := "", 0
				for _, branch := range []string{" then ", " catch "} {
					if i := strings.Index(p, branch); i >= 0 {
						name = strings.TrimSpace(p[i+len(branch):])
						nameStart = pStart + i + len(branch) + strings.Index(p[i+len(branch):], name)
						pEnd = pStart + i
						if branch == " catch " {
							name = "catch:" + name
						}
						break
					}
				}
				w.gen("{ (")
				w.copyTrimmed(pStart, pEnd)
				w.gen(");\n")
				stack = append(stack, open{kind: "await", promiseStart: pStart, promiseEnd: pEnd})
				if name != "" {
					writeAwaitName(w, src, strings.TrimPrefix(name, "catch:"), nameStart, strings.HasPrefix(name, "catch:"), pStart, pEnd)
				}
			case ":then", ":catch":
				if top() != "await" {
					continue
				}
				o := stack[len(stack)-1]
				w.gen("} {\n")
				name := strings.TrimSpace(src[rest:r.end])
				if name != "" {
					writeAwaitName(w, src, name, rest+strings.Index(src[rest:r.end], name), keyword == ":catch", o.promiseStart, o.promiseEnd)
				}
			case "#key":
				w.gen("{ (")
				w.copyTrimmed(rest, r.end)
				w.gen(");\n")
				stack = append(stack, open{kind: "key"})
			case "#client":
				w.gen("{\n")
				stack = append(stack, open{kind: "client"})
			case "/if", "/each", "/await", "/key", "/client":
				closeTo(keyword[1:])
			}
		case strings.HasPrefix(trimmed, "@"):
			// {@html …} and other tags Vela does not support.
		default:
			w.gen("(")
			w.copyTrimmed(r.start, r.end)
			w.gen(");\n")
		}
	}
	for range stack {
		w.gen("}\n")
	}
}

// blockKeyword is the #if, :else, /each… at the start of a block tag.
func blockKeyword(tag string) string {
	end := 1
	for end < len(tag) && isIdentByte(tag[end]) {
		end++
	}
	return tag[:end]
}

func writeAwaitName(w *virtualWriter, src, name string, at int, isCatch bool, pStart, pEnd int) {
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
func writeEach(w *virtualWriter, start, end int) {
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
		w.gen("let ")
		w.copyTrimmed(index, end)
		w.gen(" = 0;\n")
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

// writeAttr writes the expression of a dynamic attribute: an element event
// handler gets its event type from $$on, a component's from $$handler.
func writeAttr(w *virtualWriter, r templateRegion) {
	component := r.tag != "" && r.tag[0] >= 'A' && r.tag[0] <= 'Z'
	switch {
	case strings.HasPrefix(r.attr, "@") && !component:
		event := strings.SplitN(r.attr[1:], ".", 2)[0]
		w.gen("$$on(\"" + event + "\", ")
		w.copyTrimmed(r.start, r.end)
		w.gen(");\n")
	case strings.HasPrefix(r.attr, "@"):
		w.gen("$$handler(")
		w.copyTrimmed(r.start, r.end)
		w.gen(");\n")
	default:
		w.gen("(")
		w.copyTrimmed(r.start, r.end)
		w.gen(");\n")
	}
}

// utf16Index is the UTF-16 index of the byte offset off of s, as
// TypeScript counts positions.
func utf16Index(s string, off int) int {
	off = min(max(off, 0), len(s))
	return len(utf16.Encode([]rune(s[:off])))
}

// byteOffset is the byte offset of the UTF-16 index i of s.
func byteOffset(s string, i int) int {
	at, units := 0, 0
	for at < len(s) && units < i {
		r, size := utf8.DecodeRuneInString(s[at:])
		units += utf16.RuneLen(r)
		at += size
	}
	return at
}
