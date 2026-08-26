package main

import (
	"strings"
	"unicode"
)

// ─────────────────────────────────────────────────────────────────────────────
//  CSS Tokenizer
// ─────────────────────────────────────────────────────────────────────────────

type CssTokKind uint8

const (
	CssTokEOF        CssTokKind = iota
	CssTokIdent                 // foo, bar-baz, --custom-prop
	CssTokString                // "…" or '…'
	CssTokHash                  // #foo
	CssTokAt                    // @media, @keyframes, @supports…
	CssTokNumber                // 42, 3.14, 1em, 50%
	CssTokColon                 // :
	CssTokSemi                  // ;
	CssTokComma                 // ,
	CssTokLBrace                // {
	CssTokRBrace                // }
	CssTokLParen                // (
	CssTokRParen                // )
	CssTokLBracket              // [
	CssTokRBracket              // ]
	CssTokDelim                 // any other single char (>, +, ~, *, ., etc.)
	CssTokWhitespace            // runs of space/tab/newline (preserved for selector rebuild)
)

type CssTok struct {
	Kind CssTokKind
	Val  string
}

type CssLexer struct {
	src string
	pos int
}

func (l *CssLexer) ch() byte {
	if l.pos >= len(l.src) {
		return 0
	}
	return l.src[l.pos]
}

func (l *CssLexer) peek(n int) byte {
	p := l.pos + n
	if p >= len(l.src) {
		return 0
	}
	return l.src[p]
}

func (l *CssLexer) adv() byte {
	c := l.src[l.pos]
	l.pos++
	return c
}

func (l *CssLexer) Next() CssTok {
	if l.pos >= len(l.src) {
		return CssTok{Kind: CssTokEOF}
	}

	// ── Comments → skip, return next ──────────────────────────────────────
	if l.ch() == '/' && l.peek(1) == '*' {
		l.pos += 2
		for l.pos < len(l.src) {
			if l.ch() == '*' && l.peek(1) == '/' {
				l.pos += 2
				break
			}
			l.pos++
		}
		return l.Next()
	}

	// ── Whitespace ─────────────────────────────────────────────────────────
	if isWSByte(l.ch()) {
		start := l.pos
		for l.pos < len(l.src) && isWSByte(l.ch()) {
			l.pos++
		}
		return CssTok{CssTokWhitespace, l.src[start:l.pos]}
	}

	// ── Strings ────────────────────────────────────────────────────────────
	if l.ch() == '"' || l.ch() == '\'' {
		q := l.adv()
		start := l.pos - 1
		for l.pos < len(l.src) && l.ch() != q {
			if l.ch() == '\\' {
				l.pos++
			}
			l.pos++
		}
		if l.pos < len(l.src) {
			l.pos++
		}
		return CssTok{CssTokString, l.src[start:l.pos]}
	}

	// ── @ keywords ─────────────────────────────────────────────────────────
	if l.ch() == '@' {
		start := l.pos
		l.pos++
		for l.pos < len(l.src) && isCssIdentChar(l.ch()) {
			l.pos++
		}
		return CssTok{CssTokAt, l.src[start:l.pos]}
	}

	// ── Hash ───────────────────────────────────────────────────────────────
	if l.ch() == '#' {
		start := l.pos
		l.pos++
		for l.pos < len(l.src) && isCssIdentChar(l.ch()) {
			l.pos++
		}
		return CssTok{CssTokHash, l.src[start:l.pos]}
	}

	// ── Identifiers (including custom properties --foo) ────────────────────
	if isCssIdentStart(l.ch()) || (l.ch() == '-' && (isCssIdentStart(l.peek(1)) || l.peek(1) == '-')) {
		start := l.pos
		l.pos++
		for l.pos < len(l.src) && isCssIdentChar(l.ch()) {
			l.pos++
		}
		return CssTok{CssTokIdent, l.src[start:l.pos]}
	}

	// ── Numbers (with optional unit) ───────────────────────────────────────
	if isDigitByte(l.ch()) || (l.ch() == '.' && isDigitByte(l.peek(1))) {
		start := l.pos
		for l.pos < len(l.src) && (isDigitByte(l.ch()) || l.ch() == '.') {
			l.pos++
		}
		// unit / percentage
		for l.pos < len(l.src) && isCssIdentChar(l.ch()) {
			l.pos++
		}
		if l.pos < len(l.src) && l.ch() == '%' {
			l.pos++
		}
		return CssTok{CssTokNumber, l.src[start:l.pos]}
	}

	// ── Single-char tokens ─────────────────────────────────────────────────
	c := l.adv()
	switch c {
	case ':':
		return CssTok{CssTokColon, ":"}
	case ';':
		return CssTok{CssTokSemi, ";"}
	case ',':
		return CssTok{CssTokComma, ","}
	case '{':
		return CssTok{CssTokLBrace, "{"}
	case '}':
		return CssTok{CssTokRBrace, "}"}
	case '(':
		return CssTok{CssTokLParen, "("}
	case ')':
		return CssTok{CssTokRParen, ")"}
	case '[':
		return CssTok{CssTokLBracket, "["}
	case ']':
		return CssTok{CssTokRBracket, "]"}
	}
	return CssTok{CssTokDelim, string(c)}
}

// ─────────────────────────────────────────────────────────────────────────────
//  CSS AST
// ─────────────────────────────────────────────────────────────────────────────

// CssNode is the common interface for all CSS AST nodes.
type CssNode interface{ cssNode() }

// CssStylesheet is the root node.
type CssStylesheet struct {
	Source      string
	Span        SectionSpan
	Rules       []CssNode
	Diagnostics []CssParseDiagnostic
}

type CssParseDiagnostic struct {
	Pos     int
	Message string
}

func (c *CssStylesheet) cssNode() {}

// CssQualifiedRule holds a selector list + a declaration block.
type CssQualifiedRule struct {
	Selectors []*CssSelector // comma-separated list
	Decls     []*CssDecl
}

func (c *CssQualifiedRule) cssNode() {}

// CssAtRule represents @media, @keyframes, @supports, etc.
type CssAtRule struct {
	Name    string    // e.g. "media", "keyframes", "supports"
	Prelude string    // everything between @name and { or ;
	Block   []CssNode // inner rules (nil for statement at-rules ending in ;)
	IsStmt  bool      // true → ends with ; (e.g. @import, @charset)
}

func (c *CssAtRule) cssNode() {}

// CssSelector is a single selector (between commas).
// Parts is the raw token sequence; we keep tokens so we can accurately
// insert the scoping attribute at the right position.
type CssSelector struct {
	Parts []CssTok // raw token stream for this selector
}

// CssDecl is a single property: value declaration.
type CssDecl struct {
	Prop      string
	Value     string
	Important bool
}

// ─────────────────────────────────────────────────────────────────────────────
//  CSS Parser
// ─────────────────────────────────────────────────────────────────────────────

type CssParser struct {
	lex *CssLexer
	cur CssTok
}

func newCssStylesheet(src string, offset int) *CssStylesheet {
	ss := ParseCSS(src)
	ss.Source = src
	ss.Span = SectionSpan{Start: offset, End: offset + len(src)}
	ss.Diagnostics = validateCSSSource(src)
	return ss
}

func ParseCSS(src string) *CssStylesheet {
	p := &CssParser{lex: &CssLexer{src: src}}
	p.advance()
	ss := &CssStylesheet{}
	ss.Rules = p.parseRuleList(false)
	return ss
}

func (p *CssParser) advance() {
	p.cur = p.lex.Next()
}

func (p *CssParser) skipWS() {
	for p.cur.Kind == CssTokWhitespace {
		p.advance()
	}
}

func (p *CssParser) parseRuleList(insideBlock bool) []CssNode {
	var rules []CssNode
	for p.cur.Kind != CssTokEOF {
		if p.cur.Kind == CssTokWhitespace {
			p.advance()
			continue
		}
		if insideBlock && p.cur.Kind == CssTokRBrace {
			break
		}
		if p.cur.Kind == CssTokAt {
			rules = append(rules, p.parseAtRule())
			continue
		}
		rule := p.parseQualifiedRule()
		if rule != nil {
			rules = append(rules, rule)
		}
	}
	return rules
}

func (p *CssParser) parseAtRule() *CssAtRule {
	name := strings.TrimPrefix(p.cur.Val, "@")
	p.advance()

	// Collect prelude tokens until { or ;
	var prelude strings.Builder
	for p.cur.Kind != CssTokEOF && p.cur.Kind != CssTokLBrace && p.cur.Kind != CssTokSemi {
		prelude.WriteString(p.cur.Val)
		p.advance()
	}

	rule := &CssAtRule{
		Name:    name,
		Prelude: strings.TrimSpace(prelude.String()),
	}

	if p.cur.Kind == CssTokSemi {
		rule.IsStmt = true
		p.advance()
		return rule
	}

	// Consume the {
	if p.cur.Kind == CssTokLBrace {
		p.advance()
	}

	// @keyframes / @font-face blocks contain keyframe rules, not qualified rules.
	// We parse them as raw text to avoid scoping their "selectors" (from/to/%).
	switch name {
	case "keyframes", "-webkit-keyframes", "-moz-keyframes":
		rule.Block = p.parseKeyframeBlock()
	default:
		rule.Block = p.parseRuleList(true)
	}

	if p.cur.Kind == CssTokRBrace {
		p.advance()
	}
	return rule
}

// parseKeyframeBlock reads inside @keyframes { … } as a single opaque at-rule
// so we never try to scope "from" or "to" as selectors.
func (p *CssParser) parseKeyframeBlock() []CssNode {
	// We collect the entire content as an opaque raw rule.
	var raw strings.Builder
	depth := 1
	for p.cur.Kind != CssTokEOF && depth > 0 {
		if p.cur.Kind == CssTokLBrace {
			depth++
		} else if p.cur.Kind == CssTokRBrace {
			depth--
			if depth == 0 {
				break
			}
		}
		raw.WriteString(p.cur.Val)
		p.advance()
	}
	// Return as an opaque at-rule with no name (just raw prelude as block text)
	return []CssNode{&CssAtRule{Name: "raw", Prelude: raw.String(), IsStmt: false}}
}

func (p *CssParser) parseQualifiedRule() *CssQualifiedRule {
	// Collect selector tokens until {
	var selectorToks []CssTok
	for p.cur.Kind != CssTokEOF && p.cur.Kind != CssTokLBrace && p.cur.Kind != CssTokRBrace {
		selectorToks = append(selectorToks, p.cur)
		p.advance()
	}

	if p.cur.Kind != CssTokLBrace {
		return nil
	}
	p.advance() // consume {

	rule := &CssQualifiedRule{}
	rule.Selectors = splitSelectors(selectorToks)
	rule.Decls = p.parseDeclBlock()

	if p.cur.Kind == CssTokRBrace {
		p.advance()
	}
	return rule
}

// splitSelectors splits a flat token sequence on CssTokComma, trimming
// leading/trailing whitespace from each resulting selector.
func splitSelectors(toks []CssTok) []*CssSelector {
	var selectors []*CssSelector
	var current []CssTok
	depth := 0

	flush := func() {
		// trim whitespace
		for len(current) > 0 && current[0].Kind == CssTokWhitespace {
			current = current[1:]
		}
		for len(current) > 0 && current[len(current)-1].Kind == CssTokWhitespace {
			current = current[:len(current)-1]
		}
		if len(current) > 0 {
			selectors = append(selectors, &CssSelector{Parts: current})
		}
		current = nil
	}

	for _, t := range toks {
		switch t.Kind {
		case CssTokLParen, CssTokLBracket:
			depth++
			current = append(current, t)
		case CssTokRParen, CssTokRBracket:
			depth--
			current = append(current, t)
		case CssTokComma:
			if depth == 0 {
				flush()
			} else {
				current = append(current, t)
			}
		default:
			current = append(current, t)
		}
	}
	flush()
	return selectors
}

func (p *CssParser) parseDeclBlock() []*CssDecl {
	var decls []*CssDecl
	for p.cur.Kind != CssTokEOF && p.cur.Kind != CssTokRBrace {
		p.skipWS()
		if p.cur.Kind == CssTokRBrace || p.cur.Kind == CssTokEOF {
			break
		}

		// Read property name
		if p.cur.Kind != CssTokIdent {
			// Skip unexpected token
			p.advance()
			continue
		}
		prop := p.cur.Val
		p.advance()

		p.skipWS()
		if p.cur.Kind != CssTokColon {
			// Malformed — skip to next semicolon or }
			for p.cur.Kind != CssTokEOF && p.cur.Kind != CssTokSemi && p.cur.Kind != CssTokRBrace {
				p.advance()
			}
			p.eat(CssTokSemi)
			continue
		}
		p.advance() // consume :

		// Read value until ; or }
		var val strings.Builder
		important := false
		for p.cur.Kind != CssTokEOF && p.cur.Kind != CssTokSemi && p.cur.Kind != CssTokRBrace {
			val.WriteString(p.cur.Val)
			p.advance()
		}
		p.eat(CssTokSemi)

		valStr := strings.TrimSpace(val.String())
		if strings.HasSuffix(valStr, "!important") {
			important = true
			valStr = strings.TrimSpace(strings.TrimSuffix(valStr, "!important"))
		}

		decls = append(decls, &CssDecl{
			Prop:      prop,
			Value:     valStr,
			Important: important,
		})
	}
	return decls
}

func (p *CssParser) eat(k CssTokKind) bool {
	if p.cur.Kind == k {
		p.advance()
		return true
	}
	return false
}

// ─────────────────────────────────────────────────────────────────────────────
//  CSS Scope Printer
// ─────────────────────────────────────────────────────────────────────────────

// ScopeCSS parses the CSS, walks the AST, and returns scoped CSS where every
// selector in qualified rules gets the attribute [scopeID] injected at the
// correct position — after the last simple-selector in the rightmost compound
// selector, before any pseudo-elements.
//
// Special cases handled correctly:
//   - @media / @supports wrap inner rules (which are scoped)
//   - @keyframes / raw blocks are left verbatim (never scoped)
//   - :global(sel) removes the scope attribute for sel
//   - ::before / ::after / :hover etc. have the attr injected before them
//   - Combinators (>, +, ~, space) are preserved verbatim
func ScopeCSS(css, scopeID string) string {
	sheet := ParseCSS(css)
	attr := "[" + scopeID + "]"
	var b strings.Builder
	printSheet(sheet.Rules, &b, attr)
	return b.String()
}

func printSheet(rules []CssNode, b *strings.Builder, attr string) {
	for _, rule := range rules {
		printCssNode(rule, b, attr)
	}
}

func printCssNode(node CssNode, b *strings.Builder, attr string) {
	switch r := node.(type) {
	case *CssQualifiedRule:
		// Print scoped selector list
		var selParts []string
		for _, sel := range r.Selectors {
			selParts = append(selParts, scopeSelector(sel.Parts, attr))
		}
		b.WriteString(strings.Join(selParts, ", "))
		b.WriteString(" {\n")
		for _, d := range r.Decls {
			b.WriteString("  ")
			b.WriteString(d.Prop)
			b.WriteString(": ")
			b.WriteString(d.Value)
			if d.Important {
				b.WriteString(" !important")
			}
			b.WriteString(";\n")
		}
		b.WriteString("}\n")

	case *CssAtRule:
		if r.Name == "raw" {
			// Raw keyframe content — emit verbatim
			b.WriteString(r.Prelude)
			return
		}
		b.WriteString("@")
		b.WriteString(r.Name)
		if r.Prelude != "" {
			b.WriteString(" ")
			b.WriteString(r.Prelude)
		}
		if r.IsStmt {
			b.WriteString(";\n")
			return
		}
		b.WriteString(" {\n")
		for _, inner := range r.Block {
			printCssNode(inner, b, attr)
		}
		b.WriteString("}\n")
	}
}

// scopeSelector inserts attr at the right place within one selector's token list.
//
// Algorithm:
//  1. Walk tokens right-to-left to find the "injection point" — the position
//     just before the first pseudo-element (::before etc.) or at the end of the
//     last compound selector.
//  2. The injection point is after the last identifier/hash/class/attribute
//     token before any pseudo-element, and after all descendant combinators.
//  3. If the selector contains :global(...), skip scoping entirely for that
//     selector (return it verbatim stripped of the :global wrapper).
func scopeSelector(toks []CssTok, attr string) string {
	// First pass: check for :global
	rawStr := tokStr(toks)
	if strings.Contains(rawStr, ":global(") {
		return unwrapGlobal(rawStr)
	}

	// Second pass: skip inherently global selectors (:root, body, html, *)
	// These must never receive a component scope attribute.
	if isGlobalSelector(rawStr) {
		return rawStr
	}

	// Find injection point: scan right-to-left for the position before
	// any trailing pseudo-elements (::after, ::before, :hover, :focus etc.)
	// but after the actual subject token.
	injectAt := len(toks)
	for i := len(toks) - 1; i >= 0; i-- {
		t := toks[i]
		switch t.Kind {
		case CssTokWhitespace:
			continue
		case CssTokColon:
			// This is a pseudo-class/element — injection goes before it
			injectAt = i
			continue
		case CssTokIdent:
			// If preceded by a colon (pseudo), keep scanning left
			if i > 0 && toks[i-1].Kind == CssTokColon {
				injectAt = i - 1
				continue
			}
			// Otherwise this is the subject element — stop here, inject after
			injectAt = i + 1
			goto found
		case CssTokHash, CssTokDelim, CssTokLBracket:
			// Class, id, attribute, universal — inject after
			injectAt = i + 1
			goto found
		case CssTokRBracket:
			// End of attribute selector — inject after
			injectAt = i + 1
			goto found
		default:
			injectAt = i + 1
			goto found
		}
	}
found:

	var b strings.Builder
	for i, t := range toks {
		if i == injectAt {
			b.WriteString(attr)
		}
		b.WriteString(t.Val)
	}
	if injectAt >= len(toks) {
		b.WriteString(attr)
	}
	return b.String()
}

// unwrapGlobal strips :global(...) wrappers from a selector string.
// :global(.foo) → .foo
// .parent :global(.child) → .parent .child
func unwrapGlobal(sel string) string {
	result := sel
	for {
		start := strings.Index(result, ":global(")
		if start < 0 {
			break
		}
		// Find matching close paren
		depth := 1
		i := start + len(":global(")
		for i < len(result) && depth > 0 {
			if result[i] == '(' {
				depth++
			} else if result[i] == ')' {
				depth--
			}
			i++
		}
		inner := result[start+len(":global(") : i-1]
		result = result[:start] + inner + result[i:]
	}
	return result
}

// tokStr concatenates all token values into a string.
func tokStr(toks []CssTok) string {
	var b strings.Builder
	for _, t := range toks {
		b.WriteString(t.Val)
	}
	return b.String()
}

// ─────────────────────────────────────────────────────────────────────────────
//  Character helpers
// ─────────────────────────────────────────────────────────────────────────────

func isWSByte(c byte) bool    { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }
func isDigitByte(c byte) bool { return c >= '0' && c <= '9' }

func isCssIdentStart(c byte) bool {
	return c == '_' || c == '-' || unicode.IsLetter(rune(c))
}

func isCssIdentChar(c byte) bool {
	return isCssIdentStart(c) || isDigitByte(c)
}

// ── Scope helpers injected by patch_cssparser.py ─────────────────────────

// isGlobalSelector returns true for selectors that must never be scoped.
func isGlobalSelector(sel string) bool {
	s := strings.TrimSpace(sel)
	if s == "" || strings.HasPrefix(s, "@") {
		return true
	}
	for _, pfx := range []string{":root", ":root ", ":root:", ":root.", ":root#", ":root[", ":root>", ":root+", ":root~"} {
		if s == pfx || (len(pfx) > 5 && strings.HasPrefix(s, pfx)) {
			return true
		}
	}
	if s == ":root" {
		return true
	}
	end := strings.IndexAny(s, " >+~.#[:(")
	first := s
	if end >= 0 {
		first = s[:end]
	}
	return first == "html" || first == "body" || first == "*"
}

// insertScopeAttr inserts [id] before the first : pseudo, respecting ( ) [ ].
func insertScopeAttr(sel, id string) string {
	attr := "[" + id + "]"
	d, b := 0, 0
	for i, c := range sel {
		switch c {
		case '(':
			d++
		case ')':
			d--
		case '[':
			b++
		case ']':
			b--
		case ':':
			if d == 0 && b == 0 {
				return sel[:i] + attr + sel[i:]
			}
		}
	}
	return sel + attr
}

// scopeOneSel scopes a single selector token, handling :global() and globals.
func scopeOneSel(sel, scopeID string) string {
	s := strings.TrimSpace(sel)
	if strings.HasPrefix(s, "@") {
		return s
	}
	if strings.HasPrefix(s, ":global(") && strings.HasSuffix(s, ")") {
		return strings.TrimSpace(s[8 : len(s)-1])
	}
	if isGlobalSelector(s) {
		return s
	}
	if !strings.Contains(s, ":global(") {
		return insertScopeAttr(s, scopeID)
	}
	// Inline :global() — scope parts outside it
	var out strings.Builder
	rem := s
	for {
		idx := strings.Index(rem, ":global(")
		if idx < 0 {
			if t := strings.TrimSpace(rem); t != "" {
				if isGlobalSelector(t) {
					out.WriteString(t)
				} else {
					out.WriteString(insertScopeAttr(t, scopeID))
				}
			}
			break
		}
		if before := strings.TrimSpace(rem[:idx]); before != "" {
			if isGlobalSelector(before) {
				out.WriteString(before)
			} else {
				out.WriteString(insertScopeAttr(before, scopeID))
			}
			out.WriteString(" ")
		}
		rest := rem[idx+8:]
		depth := 1
		i := 0
		for i < len(rest) {
			if rest[i] == '(' {
				depth++
			} else if rest[i] == ')' {
				depth--
				if depth == 0 {
					break
				}
			}
			i++
		}
		out.WriteString(rest[:i])
		rem = rest[i+1:]
	}
	return strings.TrimSpace(out.String())
}

// validateCSSSource performs deterministic lexical validation before CSS
// scoping. The structural parser remains permissive for editor tooling, while
// compiler mode consumes these diagnostics and rejects malformed stylesheets.
func validateCSSSource(src string) []CssParseDiagnostic {
	var out []CssParseDiagnostic
	depth := 0
	quote := byte(0)
	for i := 0; i < len(src); i++ {
		c := src[i]
		if quote != 0 {
			if c == 92 && i+1 < len(src) {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == 47 && i+1 < len(src) && src[i+1] == 42 {
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return append(out, CssParseDiagnostic{Pos: i, Message: "unterminated CSS comment"})
			}
			i += end + 3
			continue
		}
		if c == 39 || c == 34 {
			quote = c
			continue
		}
		switch c {
		case 123:
			depth++
		case 125:
			if depth == 0 {
				out = append(out, CssParseDiagnostic{Pos: i, Message: "unexpected closing CSS brace"})
			} else {
				depth--
			}
		}
	}
	if quote != 0 {
		out = append(out, CssParseDiagnostic{Pos: len(src), Message: "unterminated CSS string"})
	}
	if depth != 0 {
		out = append(out, CssParseDiagnostic{Pos: len(src), Message: "unterminated CSS block"})
	}
	return out
}
