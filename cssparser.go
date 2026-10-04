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
	Kind  CssTokKind
	Val   string
	Start int
	End   int
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
			if l.ch() == 42 && l.peek(1) == 47 {
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
		return CssTok{Kind: CssTokWhitespace, Val: l.src[start:l.pos], Start: start, End: l.pos}
	}

	// ── Strings ────────────────────────────────────────────────────────────
	if l.ch() == '"' || l.ch() == '\'' {
		q := l.adv()
		start := l.pos - 1
		for l.pos < len(l.src) && l.ch() != q {
			if l.ch() == 92 && l.pos+1 < len(l.src) {
				l.pos++
			}
			l.pos++
		}
		if l.pos < len(l.src) {
			l.pos++
		}
		return CssTok{Kind: CssTokString, Val: l.src[start:l.pos], Start: start, End: l.pos}
	}

	// ── @ keywords ─────────────────────────────────────────────────────────
	if l.ch() == '@' {
		start := l.pos
		l.pos++
		for l.pos < len(l.src) && isCssIdentChar(l.ch()) {
			l.pos++
		}
		return CssTok{Kind: CssTokAt, Val: l.src[start:l.pos], Start: start, End: l.pos}
	}

	// ── Hash ───────────────────────────────────────────────────────────────
	if l.ch() == '#' {
		start := l.pos
		l.pos++
		for l.pos < len(l.src) && isCssIdentChar(l.ch()) {
			l.pos++
		}
		return CssTok{Kind: CssTokHash, Val: l.src[start:l.pos], Start: start, End: l.pos}
	}

	// ── Identifiers (including custom properties --foo) ────────────────────
	if isCssIdentStart(l.ch()) || (l.ch() == '-' && (isCssIdentStart(l.peek(1)) || l.peek(1) == '-')) {
		start := l.pos
		l.pos++
		for l.pos < len(l.src) && isCssIdentChar(l.ch()) {
			l.pos++
		}
		return CssTok{Kind: CssTokIdent, Val: l.src[start:l.pos], Start: start, End: l.pos}
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
		return CssTok{Kind: CssTokNumber, Val: l.src[start:l.pos], Start: start, End: l.pos}
	}

	// ── Single-char tokens ─────────────────────────────────────────────────
	start := l.pos
	c := l.adv()
	switch c {
	case ':':
		return CssTok{Kind: CssTokColon, Val: ":", Start: start, End: l.pos}
	case ';':
		return CssTok{Kind: CssTokSemi, Val: ";", Start: start, End: l.pos}
	case ',':
		return CssTok{Kind: CssTokComma, Val: ",", Start: start, End: l.pos}
	case '{':
		return CssTok{Kind: CssTokLBrace, Val: "{", Start: start, End: l.pos}
	case '}':
		return CssTok{Kind: CssTokRBrace, Val: "}", Start: start, End: l.pos}
	case '(':
		return CssTok{Kind: CssTokLParen, Val: "(", Start: start, End: l.pos}
	case ')':
		return CssTok{Kind: CssTokRParen, Val: ")", Start: start, End: l.pos}
	case '[':
		return CssTok{Kind: CssTokLBracket, Val: "[", Start: start, End: l.pos}
	case ']':
		return CssTok{Kind: CssTokRBracket, Val: "]", Start: start, End: l.pos}
	}
	return CssTok{Kind: CssTokDelim, Val: string(c), Start: start, End: l.pos}
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
	Span      SectionSpan
	Selectors []*CssSelector // comma-separated list
	Decls     []*CssDecl
}

func (c *CssQualifiedRule) cssNode() {}

// CssAtRule represents @media, @keyframes, @supports, etc.
type CssAtRule struct {
	Span    SectionSpan
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
	Span  SectionSpan
	Parts []CssTok // raw token stream for this selector
}

// CssDecl is a single property: value declaration.
type CssDecl struct {
	Span      SectionSpan
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
	offsetCSSSpans(ss, offset)
	ss.Diagnostics = validateCSSSource(src)
	return ss
}

// offsetCSSSpans translates parser-local CSS spans into the full component
// source coordinate space owned by CssStylesheet.Span.
func offsetCSSSpans(sheet *CssStylesheet, offset int) {
	if sheet == nil || offset == 0 {
		return
	}
	shift := func(span *SectionSpan) {
		span.Start += offset
		span.End += offset
	}
	var walk func([]CssNode)
	walk = func(nodes []CssNode) {
		for _, node := range nodes {
			switch rule := node.(type) {
			case *CssQualifiedRule:
				shift(&rule.Span)
				for _, selector := range rule.Selectors {
					if selector == nil {
						continue
					}
					shift(&selector.Span)
					for i := range selector.Parts {
						selector.Parts[i].Start += offset
						selector.Parts[i].End += offset
					}
				}
				for _, decl := range rule.Decls {
					if decl != nil {
						shift(&decl.Span)
					}
				}
			case *CssAtRule:
				shift(&rule.Span)
				walk(rule.Block)
			}
		}
	}
	walk(sheet.Rules)
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
		if p.cur.Kind == CssTokRBrace {
			if insideBlock {
				break
			}
			// The lexical validator reports a top-level closing brace. Consume
			// it here so the permissive editor parser always makes progress.
			p.advance()
			continue
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
	start := p.cur.Start
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
		rule.Span = SectionSpan{Start: start, End: p.cur.End}
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

	end := p.cur.Start
	if p.cur.Kind == CssTokRBrace {
		end = p.cur.End
		p.advance()
	}
	rule.Span = SectionSpan{Start: start, End: end}
	return rule
}

// parseKeyframeBlock reads inside @keyframes { … } as a single opaque at-rule
// so we never try to scope "from" or "to" as selectors.
func (p *CssParser) parseKeyframeBlock() []CssNode {
	// We collect the entire content as an opaque raw rule. It remains typed
	// and positioned even though its declaration grammar is intentionally not
	// interpreted by the scoping pass.
	start := p.cur.Start
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
	// Return as an opaque at-rule with no name (just raw prelude as block text).
	// The closing brace belongs to the parent @keyframes rule.
	return []CssNode{&CssAtRule{
		Span:    SectionSpan{Start: start, End: p.cur.Start},
		Name:    "raw",
		Prelude: raw.String(),
		IsStmt:  false,
	}}
}

func (p *CssParser) parseQualifiedRule() *CssQualifiedRule {
	start := p.cur.Start
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

	rule := &CssQualifiedRule{Span: SectionSpan{Start: start}}
	rule.Selectors = splitSelectors(selectorToks)
	rule.Decls = p.parseDeclBlock()

	if p.cur.Kind == CssTokRBrace {
		rule.Span.End = p.cur.End
		p.advance()
	} else {
		rule.Span.End = p.cur.Start
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
			selectors = append(selectors, &CssSelector{Span: SectionSpan{Start: current[0].Start, End: current[len(current)-1].End}, Parts: current})
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
		start := p.cur.Start
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
		end := p.cur.Start
		if p.cur.Kind == CssTokSemi {
			end = p.cur.End
		}
		p.eat(CssTokSemi)

		valStr := strings.TrimSpace(val.String())
		if strings.HasSuffix(valStr, "!important") {
			important = true
			valStr = strings.TrimSpace(strings.TrimSuffix(valStr, "!important"))
		}

		decls = append(decls, &CssDecl{
			Span:      SectionSpan{Start: start, End: end},
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
	return ScopeCssStylesheet(ParseCSS(css), scopeID)
}

// ScopeCssStylesheet prints an already parsed stylesheet. Component codegen
// uses this path so CSS scoping does not re-interpret the section source.
func ScopeCssStylesheet(sheet *CssStylesheet, scopeID string) string {
	if sheet == nil {
		return ""
	}
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

// ── Scope helpers ─────────────────────────────────────────────────────────

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

// ValidateCssAST checks the typed stylesheet before CSS scoping. It complements
// lexical parser diagnostics and protects codegen from incomplete AST shapes.
func ValidateCssAST(sheet *CssStylesheet) []CssParseDiagnostic {
	var issues []CssParseDiagnostic
	add := func(pos int, message string) {
		issues = append(issues, CssParseDiagnostic{Pos: pos, Message: message})
	}
	if sheet == nil {
		add(0, "missing stylesheet AST")
		return issues
	}
	if sheet.Span.Start < 0 || sheet.Span.End < sheet.Span.Start || sheet.Span.End-sheet.Span.Start != len(sheet.Source) {
		add(0, "stylesheet source span does not match its owned source")
	}
	contains := func(span SectionSpan) bool {
		return span.Start >= sheet.Span.Start && span.End >= span.Start && span.End <= sheet.Span.End
	}
	checkSpan := func(span SectionSpan, label string) {
		if span.Start == 0 && span.End == 0 {
			return
		}
		if !contains(span) {
			add(0, label+" span escapes stylesheet source")
		}
	}
	var walk func([]CssNode)
	walk = func(nodes []CssNode) {
		for _, node := range nodes {
			if node == nil {
				add(0, "stylesheet contains a nil rule")
				continue
			}
			switch rule := node.(type) {
			case *CssQualifiedRule:
				checkSpan(rule.Span, "qualified rule")
				if len(rule.Selectors) == 0 {
					add(0, "qualified rule has no selectors")
				}
				for _, selector := range rule.Selectors {
					if selector == nil || len(selector.Parts) == 0 {
						add(0, "qualified rule contains an empty selector")
					} else {
						checkSpan(selector.Span, "selector")
						for _, token := range selector.Parts {
							span := SectionSpan{Start: token.Start, End: token.End}
							if token.End <= token.Start {
								add(0, "selector contains a token with an empty span")
								continue
							}
							checkSpan(span, "selector token")
							if selector.Span != (SectionSpan{}) && (span.Start < selector.Span.Start || span.End > selector.Span.End) {
								add(0, "selector token span escapes selector")
							}
						}
					}
				}
				for _, decl := range rule.Decls {
					if decl == nil || strings.TrimSpace(decl.Prop) == "" {
						add(0, "qualified rule contains an invalid declaration")
					} else {
						checkSpan(decl.Span, "declaration")
					}
				}
			case *CssAtRule:
				checkSpan(rule.Span, "at-rule")
				if strings.TrimSpace(rule.Name) == "" {
					add(0, "at-rule has an empty name")
				}
				if rule.IsStmt && len(rule.Block) != 0 {
					add(0, "statement at-rule cannot contain a block")
				}
				walk(rule.Block)
			default:
				add(0, "stylesheet contains an unsupported rule kind")
			}
		}
	}
	walk(sheet.Rules)
	return issues
}
