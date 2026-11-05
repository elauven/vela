package jsast

import "sort"

// TypeScript to JavaScript by blanking (task 081).
//
// The compiler needs the JavaScript of a TypeScript source as text as well
// as a tree: template expressions, event handlers and copied modules are
// handled as source text. BlankTypeScript replaces every range the parser
// recorded as TypeScript (TSInfo.Erased) with spaces, so the result has the
// same length and the same lines as the source: every offset, diagnostic
// position and source map location of the TypeScript source is also one of
// the JavaScript. Parsed as JavaScript, the result is the tree
// ParseWithOptions returned, except for the empty statements that keep
// lines apart (TS-2.3).

// BlankTypeScript returns src with the TypeScript syntax recorded in info
// replaced by spaces. Line breaks are kept. info is the TS of the program
// ParseWithOptions returned for src; a nil info returns src.
func BlankTypeScript(src string, info *TSInfo) string {
	if info == nil || len(info.Erased) == 0 {
		return src
	}
	out := []byte(src)
	var stmts []TSErased
	for _, e := range info.Erased {
		switch e.Kind {
		case TSErasedMove:
			continue
		case TSErasedStatement:
			stmts = append(stmts, e)
		}
		for i := e.Pos; i < e.End && i < len(out); i++ {
			if out[i] != '\n' && out[i] != '\r' {
				out[i] = ' '
			}
		}
	}
	// Multi-byte characters inside types became spaces byte by byte; a line
	// separator (U+2028, U+2029) became three spaces, which only matters for
	// line counting of a type that contained one in a string literal.

	sort.SliceStable(stmts, func(i, j int) bool {
		if stmts[i].Pos != stmts[j].Pos {
			return stmts[i].Pos < stmts[j].Pos
		}
		return stmts[i].End > stmts[j].End
	})
	end := -1
	for _, s := range stmts {
		if s.Pos < end || s.Pos >= len(out) {
			continue // inside an erased statement already handled
		}
		end = s.End
		if needsSemicolon(out, s.Pos, s.End) {
			out[s.Pos] = ';'
		}
	}

	for _, e := range info.Erased {
		if e.Kind == TSErasedMove && e.Pos < len(out) && e.End < len(out) && e.Pos >= 0 && e.End >= 0 {
			out[e.End], out[e.Pos] = out[e.Pos], ' '
		}
	}
	return string(out)
}

// needsSemicolon reports whether the erased statement [pos, end) must leave
// a `;`: unless the code before it already ended a statement (start of the
// source, `;`, `{`), the lines around it could join, as in
//
//	let x = a
//	interface I {}
//	(b)
func needsSemicolon(out []byte, pos, end int) bool {
	prev := byte(0)
	for i := pos - 1; i >= 0; i-- {
		if c := out[i]; !isBlankByte(c) {
			prev = c
			break
		}
	}
	switch prev {
	case 0, ';', '{':
		return false
	case '}':
		// After a block, only a following `(`, `[`, … could continue an
		// expression (a function expression called).
		for i := end; i < len(out); i++ {
			if c := out[i]; !isBlankByte(c) {
				switch c {
				case '(', '[', '`', '+', '-', '/', '.', ',', '?', '=', '<', '>', '*', '%', '&', '|', '^', '!':
					return true
				}
				return false
			}
		}
		return false
	}
	return true
}

func isBlankByte(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v' }

// EraseTypeScript parses a TypeScript module and returns its JavaScript as
// text (BlankTypeScript), with the tree and diagnostics of ParseWithOptions.
// With diagnostics the text is src unchanged.
func EraseTypeScript(src string) (string, *JsProgram, []JSParseDiagnostic) {
	prog, diags := ParseWithOptions(src, ParseOptions{TypeScript: true})
	if len(diags) > 0 || prog == nil {
		return src, prog, diags
	}
	return BlankTypeScript(src, prog.TS), prog, nil
}
