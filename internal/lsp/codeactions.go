package lsp

import (
	"strconv"
	"strings"
)

// Quick fixes of the TypeScript diagnostics (task 085): `type` for an
// import used only as a type (VELA-TS-TYPE-IMPORT), and an object `as
// const` for an enum (VELA-TS-NON-ERASABLE).

type CodeActionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
	Context      struct {
		Diagnostics []Diagnostic `json:"diagnostics"`
	} `json:"context"`
}

type CodeAction struct {
	Title       string                           `json:"title"`
	Kind        string                           `json:"kind"`
	Diagnostics []Diagnostic                     `json:"diagnostics,omitempty"`
	IsPreferred bool                             `json:"isPreferred,omitempty"`
	Edit        map[string]map[string][]TextEdit `json:"edit"`
}

func (s *server) codeActions(p CodeActionParams) []CodeAction {
	out := []CodeAction{}
	s.mu.Lock()
	doc := s.docs[p.TextDocument.URI]
	s.mu.Unlock()
	if doc == nil {
		return out
	}
	t := doc.text
	for _, d := range p.Context.Diagnostics {
		off := t.offset(d.Range.Start)
		var title string
		var edits []TextEdit
		switch d.Code {
		case "VELA-TS-TYPE-IMPORT":
			at, ok := typeImportInsertion(t.src, off)
			if !ok {
				continue
			}
			title = "Import it with `type`"
			edits = []TextEdit{{Range: t.rangeOf(at, at), NewText: "type "}}
		case "VELA-TS-NON-ERASABLE":
			start, end, text, ok := enumAsConst(t.src, off)
			if !ok {
				continue
			}
			title = "Replace the enum with an object `as const`"
			edits = []TextEdit{{Range: t.rangeOf(start, end), NewText: text}}
		default:
			continue
		}
		out = append(out, CodeAction{Title: title, Kind: "quickfix", Diagnostics: []Diagnostic{d}, IsPreferred: true,
			Edit: map[string]map[string][]TextEdit{"changes": {p.TextDocument.URI: edits}}})
	}
	return out
}

// typeImportInsertion is where `type ` goes for the import binding at off:
// before its specifier in braces, or after `import` for a default or
// namespace import that is the statement's only binding.
func typeImportInsertion(src string, off int) (int, bool) {
	kw := strings.LastIndex(src[:off], "import")
	if kw < 0 {
		return 0, false
	}
	head := src[kw:off]
	open := strings.LastIndexByte(head, '{')
	if open >= 0 && !strings.Contains(head[open:], "}") {
		// In braces: the specifier starts after the `{` or `,` before it.
		i := off
		for i > kw && src[i-1] != '{' && src[i-1] != ',' {
			i--
		}
		for i < off && isSpaceByte(src[i]) {
			i++
		}
		return i, true
	}
	end := strings.IndexByte(src[off:], ';')
	if end < 0 {
		end = strings.IndexByte(src[off:], '\n')
	}
	if end < 0 {
		end = len(src) - off
	}
	if strings.Contains(src[kw:off+end], "{") || strings.Contains(src[kw:off+end], ",") {
		return 0, false // `import type` cannot hold a default and named bindings
	}
	return kw + len("import "), strings.HasPrefix(src[kw:], "import ")
}

// enumAsConst rewrites the enum declaration starting at off (at `enum` or
// `const enum`) as TS-2.4 says: an object `as const` and a type of its
// values. ok is false for an enum it cannot rewrite (a computed member, an
// automatic value after a non-numeric one).
func enumAsConst(src string, off int) (start, end int, text string, ok bool) {
	start = off
	i := off
	if strings.HasPrefix(src[i:], "const") {
		i = skipSpace(src, i+len("const"))
	}
	if !strings.HasPrefix(src[i:], "enum") {
		return 0, 0, "", false
	}
	i = skipSpace(src, i+len("enum"))
	nameStart := i
	for i < len(src) && isIdentByte(src[i]) {
		i++
	}
	name := src[nameStart:i]
	i = skipSpace(src, i)
	if name == "" || i >= len(src) || src[i] != '{' {
		return 0, 0, "", false
	}
	close := matchingBrace(src, i)
	if close < 0 {
		return 0, 0, "", false
	}
	exported := false
	if before := strings.TrimRight(src[:start], " \t"); strings.HasSuffix(before, "export") {
		exported = true
		start = len(before) - len("export")
	}
	var members []string
	next, auto := 0, true
	for _, part := range splitTopLevel(src[i+1 : close]) {
		part = strings.TrimSpace(stripLineComments(part))
		if part == "" {
			continue
		}
		key, value, hasValue := strings.Cut(part, "=")
		key = strings.TrimSpace(key)
		if strings.HasPrefix(key, "[") {
			return 0, 0, "", false
		}
		if hasValue {
			value = strings.TrimSpace(value)
			if n, err := strconv.Atoi(value); err == nil {
				next, auto = n+1, true
			} else {
				auto = false
			}
		} else {
			if !auto {
				return 0, 0, "", false
			}
			value = strconv.Itoa(next)
			next++
		}
		members = append(members, "  "+key+": "+value+",")
	}
	export := ""
	if exported {
		export = "export "
	}
	// The lines after the first are indented like the enum's.
	lineStart := strings.LastIndexByte(src[:start], '\n') + 1
	indent := src[lineStart:start]
	if strings.TrimSpace(indent) != "" {
		indent = ""
	}
	text = export + "const " + name + " = {\n" + indent + strings.Join(members, "\n"+indent) + "\n" + indent + "} as const;\n" +
		indent + export + "type " + name + " = (typeof " + name + ")[keyof typeof " + name + "];"
	end = close + 1
	if end < len(src) && src[end] == ';' {
		end++
	}
	return start, end, text, true
}

func skipSpace(src string, i int) int {
	for i < len(src) && isSpaceByte(src[i]) {
		i++
	}
	return i
}

func isSpaceByte(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// matchingBrace is the } closing the { at open, skipping strings.
func matchingBrace(src string, open int) int {
	depth := 0
	for i := open; i < len(src); i++ {
		switch c := src[i]; c {
		case '"', '\'', '`':
			for i++; i < len(src) && src[i] != c; i++ {
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
	return -1
}

// splitTopLevel splits at the commas outside brackets and strings.
func splitTopLevel(s string) []string {
	var out []string
	depth, from := 0, 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"', '\'', '`':
			for i++; i < len(s) && s[i] != c; i++ {
				if s[i] == '\\' {
					i++
				}
			}
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[from:i])
				from = i + 1
			}
		}
	}
	return append(out, s[from:])
}

func stripLineComments(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if j := strings.Index(line, "//"); j >= 0 {
			lines[i] = line[:j]
		}
	}
	return strings.Join(lines, "\n")
}
