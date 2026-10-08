package jsast

import (
	"fmt"
	"sort"
	"strings"
)

// ──────────────────────────────────────────────
//  Parser
// ──────────────────────────────────────────────

type JSParser struct {
	lex         *Lexer
	cur         Token
	peek        Token
	prevEnd     int // End of last consumed token — used for node.End
	src         string
	Errors      []string
	Diagnostics []JSParseDiagnostic

	noIn bool             // parsing a for(...) initializer: `in` is not a binary operator
	seen map[int]struct{} // positions that already carry a diagnostic

	lines []int // byte offsets of line starts; built lazily, shared with template-hole sub-parsers

	// paren records expressions that were written inside parentheses.  The AST
	// has no parenthesised-expression node, but a few early errors depend on
	// it: `a ?? b || c` is invalid while `a ?? (b || c)` is fine, and
	// `(a?.b).c = 1` is fine while `a?.b.c = 1` is not.
	paren map[JsExpr]bool

	// arrow caches arrowAhead results by the offset of the opening "(" so that
	// deeply nested parentheses are resolved by a single scan (linear, not
	// quadratic).
	arrow map[int]bool
}

// JSParseDiagnostic records one recoverable parser failure with a byte offset
// relative to the parsed JavaScript source. Errors remains for compatibility
// with parser tests and template-expression callers; component compilation
// consumes this structured form through ScriptAST.
type JSParseDiagnostic struct {
	Pos     int
	Message string
}

func ParseJS(src string) *JsProgram {
	prog, _ := ParseJSWithDiagnostics(src)
	return prog
}

// newJSParser creates a parser over src and primes the two-token window.
func newJSParser(src string) *JSParser {
	p := &JSParser{lex: NewLexer(src), src: src}
	p.lex.onErr = p.report
	p.cur = p.lex.Next()
	p.peek = p.lex.Next()
	return p
}

// ParseJSWithDiagnostics parses JavaScript once and returns both its AST and
// structured recoverable parser diagnostics (sorted by position).
//
// The parser never fails: on a syntax error it records a diagnostic, recovers
// and keeps going, so the returned tree is always usable.  A program is valid
// exactly when the returned diagnostics are empty.
func ParseJSWithDiagnostics(src string) (*JsProgram, []JSParseDiagnostic) {
	p := newJSParser(src)
	prog := p.parseProgram()
	p.finish()
	return prog, p.Diagnostics
}

// ParseJSExpr parses src as a single JavaScript expression and
// returns the expression node.  Returns nil if src is empty.
func ParseJSExpr(src string) JsExpr {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	p := newJSParser(src)
	return p.parseExpr()
}

// numberKind classifies a TokNumber's raw value.  ES2020 BigInt
// literals carry a trailing `n` (e.g. `42n`, `0xffn`); the lexer
// preserves the suffix in Val, so we look at the last character.
// All other numerics — integer, float, exponent, hex/octal/binary,
// numeric separators — share Kind "number".
func numberKind(raw string) string {
	if len(raw) > 0 && raw[len(raw)-1] == 'n' {
		return "bigint"
	}
	return "number"
}

// ──────────────────────────────────────────────
//  Diagnostics
// ──────────────────────────────────────────────

// lineAt returns the 1-based line number of byte offset pos.  The line index is
// built once (O(n)) and queried by binary search, so parsing a file with many
// template placeholders stays linear.
func (p *JSParser) lineAt(pos int) int {
	if p.lines == nil {
		p.lines = []int{0}
		for i := 0; i < len(p.src); i++ {
			if p.src[i] == '\n' {
				p.lines = append(p.lines, i+1)
			}
		}
	}
	// number of line starts <= pos
	return sort.Search(len(p.lines), func(i int) bool { return p.lines[i] > pos })
}

// report records a diagnostic.  Only the first diagnostic at a given offset is
// kept, which suppresses most cascades after a single mistake.
func (p *JSParser) report(pos int, msg string) {
	p.addDiag(JSParseDiagnostic{Pos: pos, Message: fmt.Sprintf("line %d: %s", p.lineAt(pos), msg)})
}

func (p *JSParser) addDiag(d JSParseDiagnostic) {
	if p.seen == nil {
		p.seen = map[int]struct{}{}
	}
	if _, dup := p.seen[d.Pos]; dup {
		return
	}
	p.seen[d.Pos] = struct{}{}
	p.Diagnostics = append(p.Diagnostics, d)
}

// finish sorts diagnostics by position (the lexer runs ahead of the parser, so
// they are recorded out of order) and rebuilds Errors from them.
func (p *JSParser) finish() {
	sort.SliceStable(p.Diagnostics, func(i, j int) bool { return p.Diagnostics[i].Pos < p.Diagnostics[j].Pos })
	p.Errors = p.Errors[:0]
	for _, d := range p.Diagnostics {
		p.Errors = append(p.Errors, d.Message)
	}
}

// tokDesc renders a token for "found X" messages.
func tokDesc(t Token) string {
	switch t.Kind {
	case TokEOF:
		return "end of input"
	case TokIllegal:
		return fmt.Sprintf("illegal character %q", t.Val)
	}
	v := t.Val
	if len(v) > 24 {
		v = v[:24] + "…"
	}
	return fmt.Sprintf("'%s'", v)
}

// kindDesc renders an expected token kind.
func kindDesc(k TokKind) string {
	switch k {
	case TokIdent, TokNumber, TokString, TokTemplate, TokRegex, TokPrivateName, TokEOF, TokIllegal:
		return k.String()
	}
	return "'" + k.String() + "'"
}

// ──────────────────────────────────────────────
//  Token helpers
// ──────────────────────────────────────────────

var kwKinds = func() map[TokKind]bool {
	m := map[TokKind]bool{}
	for _, k := range KeywordMap {
		m[k] = true
	}
	return m
}()

// isIdentName reports whether k can be used as an IdentifierName (property
// names, import/export names): identifiers and every keyword.
func isIdentName(k TokKind) bool { return k == TokIdent || kwKinds[k] }

// isBindingKind reports whether k can name a binding / plain identifier
// reference: identifiers and contextual keywords.
func isBindingKind(k TokKind) bool { return k == TokIdent || isContextualIdent(k) }

// isDelimiter reports tokens that end an expression list / member; error
// recovery never consumes them so the enclosing construct can close cleanly.
func isDelimiter(k TokKind) bool {
	switch k {
	case TokRParen, TokRBracket, TokRBrace, TokComma, TokSemi, TokColon, TokAssign, TokLParen, TokEOF:
		return true
	}
	return false
}

func (p *JSParser) advance() Token {
	tok := p.cur
	p.prevEnd = tok.End
	p.cur = p.peek
	p.peek = p.lex.Next()
	return tok
}

func (p *JSParser) eat(k TokKind) bool {
	if p.cur.Kind == k {
		p.advance()
		return true
	}
	return false
}

// expect consumes a token of kind k, or records a diagnostic and returns a
// zero-width token (without consuming anything) so parsing can continue.
func (p *JSParser) expect(k TokKind) Token {
	if p.cur.Kind != k {
		p.report(p.cur.Pos, fmt.Sprintf("expected %s but found %s", kindDesc(k), tokDesc(p.cur)))
		return Token{Kind: k, Pos: p.cur.Pos, End: p.cur.Pos}
	}
	return p.advance()
}

// eatSemi implements automatic semicolon insertion: a statement may end with
// ';', or implicitly before '}', at end of input, or when the next token is on
// a new line.  Anything else is a missing-semicolon error.
func (p *JSParser) eatSemi() {
	if p.eat(TokSemi) {
		return
	}
	if p.cur.Kind == TokRBrace || p.cur.Kind == TokEOF || p.cur.NL {
		return
	}
	p.report(p.cur.Pos, fmt.Sprintf("expected ';' but found %s", tokDesc(p.cur)))
}

// guard guarantees progress in statement-level loops: if nothing was consumed
// since start (error recovery declined to eat a token) skip one token.
func (p *JSParser) guard(start Token) {
	if p.cur == start && p.cur.Kind != TokEOF {
		p.advance()
	}
}

// allowIn re-enables the `in` operator (inside parentheses, brackets, blocks…)
// and returns a function restoring the previous state.
func (p *JSParser) allowIn() func() {
	old := p.noIn
	p.noIn = false
	return func() { p.noIn = old }
}

// curIdent returns the current token's value when it's an identifier or
// a contextual keyword acting as an identifier.
func (p *JSParser) curIdent() (string, bool) {
	if isBindingKind(p.cur.Kind) {
		return p.cur.Val, true
	}
	return "", false
}

func (p *JSParser) curPos() int { return p.cur.Pos }

// end returns p.prevEnd — the end position of the last consumed token.
func (p *JSParser) end() int { return p.prevEnd }

// endAt is like end but never returns less than pos, so nodes built during
// error recovery (where nothing may have been consumed) keep Pos <= End.
func (p *JSParser) endAt(pos int) int {
	if p.prevEnd < pos {
		return pos
	}
	return p.prevEnd
}

// ──────────────────────────────────────────────
//  Program
// ──────────────────────────────────────────────

func (p *JSParser) parseProgram() *JsProgram {
	prog := &JsProgram{BaseNode: BaseNode{Pos: 0}}
	for p.cur.Kind != TokEOF {
		start := p.cur
		s := p.parseStmt()
		if s != nil {
			prog.Body = append(prog.Body, s)
		}
		p.guard(start)
	}
	prog.End = p.prevEnd
	return prog
}

// ──────────────────────────────────────────────
//  Statements
// ──────────────────────────────────────────────

func (p *JSParser) parseStmt() JsStmt {
	pos := p.curPos()
	switch p.cur.Kind {
	case TokImport:
		// import(…) and import.meta are expressions, not declarations.
		if p.peek.Kind != TokLParen && p.peek.Kind != TokDot {
			return p.parseImport()
		}
	case TokExport:
		return p.parseExport()
	case TokFunction:
		return p.parseFuncDecl(false, true)
	case TokAsync:
		if p.peek.Kind == TokFunction && !p.peek.NL {
			return p.parseAsyncFuncDecl(true)
		}
	case TokClass:
		return p.parseClassDecl(true)
	case TokLet:
		// `let` is a declaration only when a binding follows; otherwise it is
		// an ordinary identifier (sloppy mode).
		if isBindingKind(p.peek.Kind) || p.peek.Kind == TokLBracket || p.peek.Kind == TokLBrace ||
			p.peek.Kind == TokYield || p.peek.Kind == TokAwait {
			return p.parseVarDecl(true, false)
		}
	case TokConst, TokVar:
		return p.parseVarDecl(true, false)
	case TokIf:
		return p.parseIf()
	case TokFor:
		return p.parseFor()
	case TokWhile:
		return p.parseWhile()
	case TokDo:
		return p.parseDoWhile()
	case TokReturn:
		return p.parseReturn()
	case TokThrow:
		return p.parseThrow()
	case TokTry:
		return p.parseTry()
	case TokSwitch:
		return p.parseSwitch()
	case TokBreak, TokContinue:
		isBreak := p.cur.Kind == TokBreak
		p.advance()
		label := ""
		if p.cur.Kind == TokIdent && !p.cur.NL {
			label = p.advance().Val
		}
		p.eatSemi()
		if isBreak {
			return &JsBreakStmt{BaseStmt: BaseStmt{BaseNode{Pos: pos, End: p.endAt(pos)}}, Label: label}
		}
		return &JsContinueStmt{BaseStmt: BaseStmt{BaseNode{Pos: pos, End: p.endAt(pos)}}, Label: label}
	case TokDebugger:
		p.advance()
		p.eatSemi()
		return &JsDebuggerStmt{BaseStmt: BaseStmt{BaseNode{Pos: pos, End: p.endAt(pos)}}}
	case TokWith:
		// `with` is illegal in strict mode and in ES modules, which is what
		// component scripts are.  Report it, but still parse the body so the
		// rest of the file is analysed normally.
		p.report(pos, "'with' statements are not supported (they are not allowed in strict mode or modules)")
		p.advance()
		p.expect(TokLParen)
		p.parseExpr()
		p.expect(TokRParen)
		body := p.parseStmt()
		return &JsBlock{BaseStmt: BaseStmt{BaseNode{Pos: pos, End: p.endAt(pos)}}, Body: []JsStmt{body}}
	case TokLBrace:
		return p.parseBlock()
	case TokSemi:
		p.advance()
		return &JsEmptyStmt{BaseStmt: BaseStmt{BaseNode{Pos: pos, End: p.endAt(pos)}}}
	}

	// Labeled statement: identifier ':' (contextual keywords included).
	if _, ok := p.curIdent(); ok && p.peek.Kind == TokColon {
		label := p.advance().Val
		p.advance() // :
		body := p.parseStmt()
		return &JsLabeledStmt{BaseStmt: BaseStmt{BaseNode{Pos: pos, End: p.endAt(pos)}}, Label: label, Body: body}
	}
	return p.parseExprStmt()
}

// ──────────────────────────────────────────────
//  Import / Export
// ──────────────────────────────────────────────

// moduleExportName parses an import/export name: an IdentifierName (any
// identifier or keyword, e.g. `default`) or a string literal (ES2022).
func (p *JSParser) moduleExportName() string {
	switch {
	case p.cur.Kind == TokString:
		return unquoteStr(p.advance().Val)
	case isIdentName(p.cur.Kind):
		return p.advance().Val
	}
	p.report(p.cur.Pos, fmt.Sprintf("module export name expected but found %s", tokDesc(p.cur)))
	return ""
}

// bindingName parses a plain binding identifier and returns its name.
func (p *JSParser) bindingName() string {
	if name, ok := p.curIdent(); ok {
		p.advance()
		return name
	}
	p.report(p.cur.Pos, fmt.Sprintf("identifier expected but found %s", tokDesc(p.cur)))
	if kwKinds[p.cur.Kind] { // recover: a reserved word used as a name
		return p.advance().Val
	}
	return ""
}

func (p *JSParser) parseImport() *JsImportDecl {
	pos := p.curPos()
	p.advance() // import
	d := &JsImportDecl{}

	if p.cur.Kind != TokString { // import 'mod' has no clause
		switch {
		case p.cur.Kind == TokStar:
			p.parseImportNamespace(d)
		case p.cur.Kind == TokLBrace:
			p.parseImportNamed(d)
		case isBindingKind(p.cur.Kind):
			d.DefaultName = p.advance().Val
			if p.eat(TokComma) {
				switch p.cur.Kind {
				case TokStar:
					p.parseImportNamespace(d)
				case TokLBrace:
					p.parseImportNamed(d)
				default:
					p.report(p.cur.Pos, fmt.Sprintf("'*' or '{' expected after ',' but found %s", tokDesc(p.cur)))
				}
			}
		default:
			p.report(p.cur.Pos, fmt.Sprintf("import binding expected but found %s", tokDesc(p.cur)))
		}
		p.expect(TokFrom)
	}
	d.Source = unquoteStr(p.expect(TokString).Val)
	d.Attributes = p.parseImportAttributes()
	p.eatSemi()
	d.Pos, d.End = pos, p.end()
	return d
}

func (p *JSParser) parseImportNamespace(d *JsImportDecl) {
	p.advance() // *
	p.expect(TokAs)
	d.Namespace = p.bindingName()
}

func (p *JSParser) parseImportNamed(d *JsImportDecl) {
	p.expect(TokLBrace)
	for p.cur.Kind != TokRBrace && p.cur.Kind != TokEOF {
		imported := p.moduleExportName()
		local := imported
		if p.eat(TokAs) {
			local = p.bindingName()
		}
		d.Specifiers = append(d.Specifiers, JsImportSpec{imported, local})
		if !p.eat(TokComma) {
			break
		}
	}
	p.expect(TokRBrace)
}

// parseImportAttributes parses an optional `with { type: 'json' }` clause
// (or the legacy `assert { … }` spelling).
func (p *JSParser) parseImportAttributes() []JsImportAttr {
	isClause := p.cur.Kind == TokWith || (p.cur.Kind == TokIdent && p.cur.Val == "assert" && !p.cur.NL)
	if !isClause || p.peek.Kind != TokLBrace {
		return nil
	}
	p.advance() // with | assert
	p.expect(TokLBrace)
	var attrs []JsImportAttr
	for p.cur.Kind != TokRBrace && p.cur.Kind != TokEOF {
		key := p.moduleExportName()
		p.expect(TokColon)
		val := unquoteStr(p.expect(TokString).Val)
		attrs = append(attrs, JsImportAttr{Key: key, Value: val})
		if !p.eat(TokComma) {
			break
		}
	}
	p.expect(TokRBrace)
	return attrs
}

func (p *JSParser) parseExport() *JsExportDecl {
	pos := p.curPos()
	p.advance() // export
	d := &JsExportDecl{}

	switch {
	case p.cur.Kind == TokDefault:
		p.advance()
		d.Default = true
		switch {
		case p.cur.Kind == TokFunction:
			d.Declaration = p.parseFuncDecl(false, false)
		case p.cur.Kind == TokAsync && p.peek.Kind == TokFunction && !p.peek.NL:
			d.Declaration = p.parseAsyncFuncDecl(false)
		case p.cur.Kind == TokClass:
			d.Declaration = p.parseClassDecl(false)
		default:
			// `export default { … }` is an object literal, never a block.
			xpos := p.curPos()
			x := p.parseAssignExpr()
			p.eatSemi()
			d.Declaration = &JsExprStmt{BaseStmt: BaseStmt{BaseNode{Pos: xpos, End: p.endAt(xpos)}}, X: x}
		}

	case p.cur.Kind == TokStar: // export * [as ns] from 'm'
		p.advance()
		d.All = true
		if p.eat(TokAs) {
			d.All = false
			d.Namespace = p.moduleExportName()
		}
		p.expect(TokFrom)
		d.HasSource = true
		d.Source = unquoteStr(p.expect(TokString).Val)
		d.Attributes = p.parseImportAttributes()
		p.eatSemi()

	case p.cur.Kind == TokLBrace: // export { a, b as c } [from 'm']
		p.advance()
		for p.cur.Kind != TokRBrace && p.cur.Kind != TokEOF {
			local := p.moduleExportName()
			exported := local
			if p.eat(TokAs) {
				exported = p.moduleExportName()
			}
			d.Specifiers = append(d.Specifiers, JsExportSpec{Local: local, Exported: exported})
			if !p.eat(TokComma) {
				break
			}
		}
		p.expect(TokRBrace)
		if p.eat(TokFrom) {
			d.HasSource = true
			d.Source = unquoteStr(p.expect(TokString).Val)
			d.Attributes = p.parseImportAttributes()
		}
		p.eatSemi()

	case p.cur.Kind == TokVar || p.cur.Kind == TokLet || p.cur.Kind == TokConst ||
		p.cur.Kind == TokFunction || p.cur.Kind == TokClass ||
		(p.cur.Kind == TokAsync && p.peek.Kind == TokFunction):
		d.Declaration = p.parseStmt()

	default:
		p.report(p.cur.Pos, fmt.Sprintf("declaration, '{', '*' or 'default' expected after 'export' but found %s", tokDesc(p.cur)))
		d.Declaration = &JsEmptyStmt{BaseStmt: BaseStmt{BaseNode{Pos: p.curPos(), End: p.curPos()}}}
	}
	d.Pos, d.End = pos, p.end()
	return d
}

// ──────────────────────────────────────────────
//  Declarations
// ──────────────────────────────────────────────

// parseAsyncFuncDecl parses `async function …` with the cursor on `async`.
func (p *JSParser) parseAsyncFuncDecl(requireName bool) *JsFuncDecl {
	pos := p.curPos() // position of 'async'
	p.advance()       // consume 'async'
	fd := p.parseFuncDecl(true, requireName)
	fd.BaseStmt.BaseNode.Pos = pos // the node starts at 'async'
	return fd
}

// parseFuncDecl parses a function declaration with the cursor on `function`.
// requireName is false for `export default function () {}`.
func (p *JSParser) parseFuncDecl(async, requireName bool) *JsFuncDecl {
	pos := p.curPos()
	p.advance() // function
	gen := p.eat(TokStar)
	name := ""
	if n, ok := p.curIdent(); ok {
		name = n
		p.advance()
	} else if requireName {
		p.report(p.cur.Pos, fmt.Sprintf("function name expected but found %s", tokDesc(p.cur)))
		if kwKinds[p.cur.Kind] { // recover: a reserved word used as a name
			name = p.advance().Val
		}
	}
	params := p.parseParams()
	body := p.parseBlock()
	return &JsFuncDecl{
		BaseStmt: BaseStmt{BaseNode{Pos: pos, End: p.endAt(pos)}},
		Name:     name, Params: params, Body: body,
		Async: async, Generator: gen,
	}
}

// parseClassTail parses everything after the `class` keyword.
func (p *JSParser) parseClassTail(requireName bool) (name string, super JsExpr, members []*JsClassMember) {
	p.advance() // class
	if n, ok := p.curIdent(); ok {
		name = n
		p.advance()
	} else if requireName {
		p.report(p.cur.Pos, fmt.Sprintf("class name expected but found %s", tokDesc(p.cur)))
		if kwKinds[p.cur.Kind] && p.cur.Kind != TokExtends {
			name = p.advance().Val
		}
	}
	if p.eat(TokExtends) {
		super = p.parseLeftHandSide()
	}
	p.expect(TokLBrace)
	defer p.allowIn()()
	for p.cur.Kind != TokRBrace && p.cur.Kind != TokEOF {
		start := p.cur
		if m := p.parseClassMember(); m != nil {
			members = append(members, m)
		}
		p.guard(start)
	}
	p.expect(TokRBrace)
	return name, super, members
}

func (p *JSParser) parseClassDecl(requireName bool) *JsClassDecl {
	pos := p.curPos()
	name, super, members := p.parseClassTail(requireName)
	return &JsClassDecl{
		BaseStmt: BaseStmt{BaseNode{Pos: pos, End: p.endAt(pos)}},
		Name:     name, SuperClass: super, Members: members,
	}
}

func (p *JSParser) parseClassMember() *JsClassMember {
	pos := p.curPos()
	if p.eat(TokSemi) { // stray ';' between members
		return nil
	}
	m := &JsClassMember{Kind: "method"}

	// `x` followed by one of these means the keyword-looking token is itself
	// the member name (`static() {}`, `get = 1`, `async;`).
	nameFollows := func() bool {
		switch p.peek.Kind {
		case TokLParen, TokAssign, TokSemi, TokRBrace, TokEOF:
			return true
		}
		return false
	}

	if p.cur.Kind == TokStatic && !nameFollows() {
		m.Static = true
		p.advance()
		if p.cur.Kind == TokLBrace { // static { … }
			m.Kind = "static-block"
			m.Body = p.parseBlock()
			m.Pos, m.End = pos, p.end()
			return m
		}
	}

	isAsync, isGen := false, false
	if p.cur.Kind == TokAsync && !nameFollows() && !p.peek.NL {
		isAsync = true
		p.advance()
	}
	if p.cur.Kind == TokStar {
		isGen = true
		p.advance()
	}
	if !isAsync && !isGen && p.cur.Kind == TokIdent && (p.cur.Val == "get" || p.cur.Val == "set") && !nameFollows() {
		m.Kind = p.advance().Val
	}
	hasModifier := isAsync || isGen || m.Kind != "method"

	switch {
	case p.cur.Kind == TokLBracket:
		p.advance()
		restore := p.allowIn()
		m.Key = p.parseAssignExpr()
		restore()
		p.expect(TokRBracket)
		m.Computed = true
	case p.cur.Kind == TokPrivateName:
		kp := p.curPos()
		name := p.advance().Val
		m.Key = &JsIdent{BaseExpr: BaseExpr{BaseNode{Pos: kp, End: p.endAt(kp)}}, Name: name}
	default:
		m.Key = p.parsePropKey()
	}

	if p.cur.Kind == TokLParen {
		fpos := p.curPos()
		params := p.parseParams()
		body := p.parseBlock()
		m.Value = &JsFuncExpr{
			BaseExpr: BaseExpr{BaseNode{Pos: fpos, End: p.endAt(fpos)}},
			Params:   params, Body: body, Async: isAsync, Generator: isGen,
		}
	} else {
		if hasModifier {
			p.report(p.cur.Pos, fmt.Sprintf("'(' expected after method name but found %s", tokDesc(p.cur)))
		}
		m.Kind = "field"
		if p.eat(TokAssign) {
			m.Value = p.parseAssignExpr()
		}
		p.eatSemi()
	}
	m.Pos, m.End = pos, p.endAt(pos)
	return m
}

// parseVarDecl parses let/const/var declaration.
// consumeSemi=false and inFor=true are used inside for(...) headers.
func (p *JSParser) parseVarDecl(consumeSemi, inFor bool) *JsVarDecl {
	pos := p.curPos()
	kind := p.advance().Val
	d := &JsVarDecl{Kind: kind}
	for {
		dp := p.curPos()
		id := p.parseBindingPattern()
		var init JsExpr
		if p.eat(TokAssign) {
			init = p.parseAssignExpr()
		} else if !inFor {
			if kind == "const" {
				p.report(dp, "missing initializer in const declaration")
			} else if _, isIdent := id.(*JsIdent); !isIdent {
				p.report(dp, "missing initializer in destructuring declaration")
			}
		}
		d.Decls = append(d.Decls, &JsVarDeclarator{
			BaseNode: BaseNode{Pos: dp, End: p.endAt(dp)},
			Id:       id, Init: init,
		})
		if !p.eat(TokComma) {
			break
		}
	}
	if consumeSemi {
		p.eatSemi()
	}
	d.Pos, d.End = pos, p.end()
	return d
}

// emptyIdent is the placeholder binding produced during error recovery.
func emptyIdent(pos int) *JsIdent {
	return &JsIdent{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: pos}}}
}

func (p *JSParser) parseBindingPattern() JsExpr {
	switch p.cur.Kind {
	case TokLBracket:
		return p.parseArrayPattern()
	case TokLBrace:
		return p.parseObjectPattern()
	}
	pos := p.curPos()
	if k := p.cur.Kind; isBindingKind(k) || k == TokYield || k == TokAwait {
		name := p.advance().Val
		return &JsIdent{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}}, Name: name}
	}
	p.report(pos, fmt.Sprintf("binding identifier expected but found %s", tokDesc(p.cur)))
	if kwKinds[p.cur.Kind] { // recover: a reserved word used as a name
		name := p.advance().Val
		return &JsIdent{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}}, Name: name}
	}
	return emptyIdent(pos)
}

func (p *JSParser) parseArrayPattern() JsExpr {
	pos := p.curPos()
	p.advance() // [
	defer p.allowIn()()
	var elems []JsExpr
	for p.cur.Kind != TokRBracket && p.cur.Kind != TokEOF {
		if p.cur.Kind == TokComma {
			elems = append(elems, nil)
			p.advance()
			continue
		}
		if p.cur.Kind == TokEllipsis {
			ep := p.curPos()
			p.advance()
			arg := p.parseBindingPattern()
			elems = append(elems, &JsSpreadExpr{BaseExpr: BaseExpr{BaseNode{Pos: ep, End: p.endAt(ep)}}, X: arg})
			break
		}
		elem := p.parseBindingPattern()
		if p.eat(TokAssign) {
			def := p.parseAssignExpr()
			elem = &JsAssignExpr{BaseExpr: BaseExpr{BaseNode{Pos: elem.NodePos(), End: p.endAt(elem.NodePos())}},
				Op: "=", L: elem, R: def}
		}
		elems = append(elems, elem)
		if !p.eat(TokComma) {
			break
		}
	}
	p.expect(TokRBracket)
	return &JsArrayExpr{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}}, Elems: elems}
}

// cloneIdent returns an independent copy of e when it is an identifier (the
// value of a shorthand property must not alias its key node).
func cloneIdent(e JsExpr) JsExpr {
	if id, ok := e.(*JsIdent); ok {
		cp := *id
		return &cp
	}
	return e
}

func (p *JSParser) parseObjectPattern() JsExpr {
	pos := p.curPos()
	p.advance() // {
	defer p.allowIn()()
	var props []*JsProp
	for p.cur.Kind != TokRBrace && p.cur.Kind != TokEOF {
		pp := p.curPos()
		if p.cur.Kind == TokEllipsis {
			p.advance()
			arg := p.parseBindingPattern()
			props = append(props, &JsProp{
				BaseNode: BaseNode{Pos: pp, End: p.endAt(pp)},
				Spread:   true,
				Value:    arg,
			})
			break
		}
		computed := false
		var key JsExpr
		if p.cur.Kind == TokLBracket {
			p.advance()
			computed = true
			key = p.parseAssignExpr()
			p.expect(TokRBracket)
		} else {
			key = p.parsePropKey()
		}
		var val JsExpr
		shorthand := false
		if p.eat(TokColon) {
			val = p.parseBindingPattern()
		} else {
			shorthand = true
			if _, ok := key.(*JsIdent); !ok || computed {
				p.report(p.cur.Pos, fmt.Sprintf("':' expected after property name but found %s", tokDesc(p.cur)))
			}
			val = cloneIdent(key)
		}
		if p.eat(TokAssign) {
			def := p.parseAssignExpr()
			val = &JsAssignExpr{BaseExpr: BaseExpr{BaseNode{Pos: val.NodePos(), End: p.endAt(val.NodePos())}},
				Op: "=", L: val, R: def}
		}
		props = append(props, &JsProp{
			BaseNode: BaseNode{Pos: pp, End: p.endAt(pp)},
			Key:      key, Value: val,
			Computed: computed, Shorthand: shorthand, Kind: "init",
		})
		if !p.eat(TokComma) {
			break
		}
	}
	p.expect(TokRBrace)
	return &JsObjectExpr{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}}, Props: props}
}

func (p *JSParser) parseParams() []JsExpr {
	p.expect(TokLParen)
	defer p.allowIn()()
	var params []JsExpr
	for p.cur.Kind != TokRParen && p.cur.Kind != TokEOF {
		if p.cur.Kind == TokEllipsis {
			ep := p.curPos()
			p.advance()
			arg := p.parseBindingPattern()
			params = append(params, &JsSpreadExpr{BaseExpr: BaseExpr{BaseNode{Pos: ep, End: p.endAt(ep)}}, X: arg})
			break
		}
		param := p.parseBindingPattern()
		if p.eat(TokAssign) {
			def := p.parseAssignExpr()
			param = &JsAssignExpr{BaseExpr: BaseExpr{BaseNode{Pos: param.NodePos(), End: p.endAt(param.NodePos())}},
				Op: "=", L: param, R: def}
		}
		params = append(params, param)
		if !p.eat(TokComma) {
			break
		}
	}
	p.expect(TokRParen)
	return params
}

func (p *JSParser) parseBlock() *JsBlock {
	pos := p.curPos()
	p.expect(TokLBrace)
	defer p.allowIn()()
	b := &JsBlock{}
	for p.cur.Kind != TokRBrace && p.cur.Kind != TokEOF {
		start := p.cur
		s := p.parseStmt()
		if s != nil {
			b.Body = append(b.Body, s)
		}
		p.guard(start)
	}
	p.expect(TokRBrace)
	b.Pos, b.End = pos, p.endAt(pos)
	return b
}

// ──────────────────────────────────────────────
//  Control-flow statements
// ──────────────────────────────────────────────

func (p *JSParser) parseIf() *JsIfStmt {
	pos := p.curPos()
	p.advance()
	p.expect(TokLParen)
	test := p.parseExpr()
	p.expect(TokRParen)
	then := p.parseStmt()
	var els JsStmt
	if p.eat(TokElse) {
		els = p.parseStmt()
	}
	return &JsIfStmt{BaseStmt: BaseStmt{BaseNode{Pos: pos, End: p.endAt(pos)}}, Test: test, Then: then, Else: els}
}

func (p *JSParser) parseFor() JsStmt {
	pos := p.curPos()
	p.advance() // for
	isAwait := p.eat(TokAwait)
	p.expect(TokLParen)

	var initStmt JsStmt
	if p.cur.Kind != TokSemi {
		// In a for-header initializer `in` must not be consumed as an operator,
		// otherwise `for (k in o)` would parse `k in o` as one expression.
		old := p.noIn
		p.noIn = true
		if p.cur.Kind == TokConst || p.cur.Kind == TokVar ||
			(p.cur.Kind == TokLet && (isBindingKind(p.peek.Kind) || p.peek.Kind == TokLBracket || p.peek.Kind == TokLBrace)) {
			initStmt = p.parseVarDecl(false, true)
		} else {
			xpos := p.curPos()
			expr := p.parseExpr()
			initStmt = &JsExprStmt{BaseStmt: BaseStmt{BaseNode{Pos: xpos, End: p.endAt(xpos)}}, X: expr}
		}
		p.noIn = old

		if p.cur.Kind == TokIn || p.cur.Kind == TokOf {
			isOf := p.cur.Kind == TokOf
			p.advance()
			var right JsExpr
			if isOf {
				right = p.parseAssignExpr()
			} else {
				right = p.parseExpr()
			}
			p.expect(TokRParen)
			body := p.parseStmt()
			if isAwait && !isOf {
				p.report(pos, "'for await' requires 'of'")
			}
			return &JsForInStmt{
				BaseStmt: BaseStmt{BaseNode{Pos: pos, End: p.endAt(pos)}},
				Left:     initStmt, Right: right, Body: body, IsOf: isOf, Await: isAwait,
			}
		}
	}
	if isAwait {
		p.report(pos, "'for await' requires 'of'")
	}

	p.expect(TokSemi)
	var test JsExpr
	if p.cur.Kind != TokSemi {
		test = p.parseExpr()
	}
	p.expect(TokSemi)
	var update JsExpr
	if p.cur.Kind != TokRParen {
		update = p.parseExpr()
	}
	p.expect(TokRParen)
	body := p.parseStmt()
	return &JsForStmt{
		BaseStmt: BaseStmt{BaseNode{Pos: pos, End: p.endAt(pos)}},
		Init:     initStmt, Test: test, Update: update, Body: body,
	}
}

func (p *JSParser) parseWhile() *JsWhileStmt {
	pos := p.curPos()
	p.advance()
	p.expect(TokLParen)
	test := p.parseExpr()
	p.expect(TokRParen)
	body := p.parseStmt()
	return &JsWhileStmt{BaseStmt: BaseStmt{BaseNode{Pos: pos, End: p.endAt(pos)}}, Test: test, Body: body}
}

func (p *JSParser) parseDoWhile() *JsDoWhileStmt {
	pos := p.curPos()
	p.advance()
	body := p.parseStmt()
	p.expect(TokWhile)
	p.expect(TokLParen)
	test := p.parseExpr()
	p.expect(TokRParen)
	p.eat(TokSemi) // a semicolon after do-while is always optional (spec 12.9.1)
	return &JsDoWhileStmt{BaseStmt: BaseStmt{BaseNode{Pos: pos, End: p.endAt(pos)}}, Body: body, Test: test}
}

func (p *JSParser) parseReturn() *JsReturnStmt {
	pos := p.curPos()
	p.advance()
	var arg JsExpr
	if !p.cur.NL && p.cur.Kind != TokSemi && p.cur.Kind != TokRBrace && p.cur.Kind != TokEOF {
		arg = p.parseExpr()
	}
	p.eatSemi()
	return &JsReturnStmt{BaseStmt: BaseStmt{BaseNode{Pos: pos, End: p.endAt(pos)}}, Arg: arg}
}

func (p *JSParser) parseThrow() *JsThrowStmt {
	pos := p.curPos()
	p.advance()
	if p.cur.NL {
		p.report(p.cur.Pos, "illegal newline after 'throw'")
	}
	arg := p.parseExpr()
	p.eatSemi()
	return &JsThrowStmt{BaseStmt: BaseStmt{BaseNode{Pos: pos, End: p.endAt(pos)}}, Arg: arg}
}

func (p *JSParser) parseTry() *JsTryStmt {
	pos := p.curPos()
	p.advance()
	block := p.parseBlock()
	st := &JsTryStmt{Block: block}
	if p.eat(TokCatch) {
		if p.eat(TokLParen) {
			st.CatchParam = p.parseBindingPattern()
			p.expect(TokRParen)
		}
		st.Handler = p.parseBlock()
	}
	if p.eat(TokFinally) {
		st.Finalizer = p.parseBlock()
	}
	if st.Handler == nil && st.Finalizer == nil {
		p.report(p.cur.Pos, "missing 'catch' or 'finally' after 'try'")
	}
	st.Pos, st.End = pos, p.end()
	return st
}

func (p *JSParser) parseSwitch() *JsSwitchStmt {
	pos := p.curPos()
	p.advance()
	p.expect(TokLParen)
	disc := p.parseExpr()
	p.expect(TokRParen)
	p.expect(TokLBrace)
	st := &JsSwitchStmt{Disc: disc}
	hasDefault := false
	for p.cur.Kind != TokRBrace && p.cur.Kind != TokEOF {
		cp := p.curPos()
		c := &JsSwitchCase{}
		if p.eat(TokCase) {
			c.Test = p.parseExpr()
		} else if p.cur.Kind == TokDefault {
			if hasDefault {
				p.report(p.cur.Pos, "more than one 'default' clause in switch statement")
			}
			hasDefault = true
			p.advance()
		} else {
			p.report(p.cur.Pos, fmt.Sprintf("'case' or 'default' expected but found %s", tokDesc(p.cur)))
			p.advance()
			continue
		}
		p.expect(TokColon)
		for p.cur.Kind != TokCase && p.cur.Kind != TokDefault &&
			p.cur.Kind != TokRBrace && p.cur.Kind != TokEOF {
			start := p.cur
			if s := p.parseStmt(); s != nil {
				c.Body = append(c.Body, s)
			}
			p.guard(start)
		}
		c.Pos, c.End = cp, p.endAt(cp)
		st.Cases = append(st.Cases, c)
	}
	p.expect(TokRBrace)
	st.Pos, st.End = pos, p.end()
	return st
}

func (p *JSParser) parseExprStmt() *JsExprStmt {
	pos := p.curPos()
	x := p.parseExpr()
	p.eatSemi()
	return &JsExprStmt{BaseStmt: BaseStmt{BaseNode{Pos: pos, End: p.endAt(pos)}}, X: x}
}

// ──────────────────────────────────────────────
//  Expressions — Pratt / TDOP
// ──────────────────────────────────────────────

func (p *JSParser) parseExpr() JsExpr {
	start := p.curPos()
	left := p.parseAssignExpr()
	if p.cur.Kind == TokComma {
		exprs := []JsExpr{left}
		for p.eat(TokComma) {
			exprs = append(exprs, p.parseAssignExpr())
		}
		return &JsSeqExpr{
			BaseExpr: BaseExpr{BaseNode{Pos: start, End: p.endAt(start)}},
			Exprs:    exprs,
		}
	}
	return left
}

// chainHasOptional reports whether e is (part of) an unparenthesised optional
// chain: `a?.b.c`, `a?.()`.
func (p *JSParser) chainHasOptional(e JsExpr) bool {
	for !p.paren[e] {
		switch t := e.(type) {
		case *JsMemberExpr:
			if t.Optional {
				return true
			}
			e = t.Obj
		case *JsCallExpr:
			if t.Optional {
				return true
			}
			e = t.Callee
		default:
			return false
		}
	}
	return false
}

// isSimpleTarget: identifier or member access that may be assigned to /
// incremented (not a private name on its own, not an optional chain).
func (p *JSParser) isSimpleTarget(e JsExpr) bool {
	switch t := e.(type) {
	case *JsIdent:
		return !strings.HasPrefix(t.Name, "#")
	case *JsMemberExpr:
		return !p.chainHasOptional(t)
	}
	return false
}

func (p *JSParser) parseAssignExpr() JsExpr {
	start := p.curPos()

	// Arrow: ident => body
	if _, ok := p.curIdent(); ok && p.peek.Kind == TokArrow && !p.peek.NL {
		name := p.advance().Val
		param := &JsIdent{BaseExpr: BaseExpr{BaseNode{Pos: start, End: p.endAt(start)}}, Name: name}
		p.advance() // =>
		body := p.parseArrowBody()
		return &JsArrowFunc{
			BaseExpr: BaseExpr{BaseNode{Pos: start, End: p.endAt(start)}},
			Params:   []JsExpr{param}, Body: body,
		}
	}

	// async arrow: `async x => …` or `async (params) => …`.  Anything else
	// starting with `async` (a call `async(a + b)`, a plain identifier) falls
	// through to the ordinary expression path.
	if p.cur.Kind == TokAsync && !p.peek.NL {
		if isBindingKind(p.peek.Kind) && p.lex.clone().Next().Kind == TokArrow {
			p.advance() // async
			ppos := p.curPos()
			name := p.advance().Val
			param := &JsIdent{BaseExpr: BaseExpr{BaseNode{Pos: ppos, End: p.endAt(ppos)}}, Name: name}
			p.expect(TokArrow)
			body := p.parseArrowBody()
			return &JsArrowFunc{
				BaseExpr: BaseExpr{BaseNode{Pos: start, End: p.endAt(start)}},
				Params:   []JsExpr{param}, Body: body, Async: true,
			}
		}
		if p.peek.Kind == TokLParen && p.arrowAhead(p.peek.Pos, false) {
			p.advance() // async
			return p.parseParenArrow(start, true)
		}
	}

	// `(params) => …` — decided by scanning ahead for the matching `)`.  An
	// arrow function is an AssignmentExpression: recognising it here (and not
	// as a primary) is what stops `() => {}` from being continued by a
	// following `[…]` / `(…)` on the next line, which must start a new statement.
	if p.cur.Kind == TokLParen && p.arrowAhead(start, true) {
		return p.parseParenArrow(start, false)
	}

	left := p.parseTernary()

	if isAssignOp(p.cur.Kind) {
		opTok := p.cur
		ok := p.isSimpleTarget(left)
		if opTok.Kind == TokAssign {
			switch left.(type) {
			case *JsObjectExpr, *JsArrayExpr:
				ok = true // destructuring assignment
			}
		}
		if !ok {
			p.report(left.NodePos(), "invalid left-hand side in assignment")
		}
		op := p.advance().Val
		right := p.parseAssignExpr()
		return &JsAssignExpr{
			BaseExpr: BaseExpr{BaseNode{Pos: start, End: p.endAt(start)}},
			Op:       op, L: left, R: right,
		}
	}
	return left
}

// arrowAhead reports whether the parenthesised group whose "(" is at offset
// openPos is followed by `=>`.  The "(" is the current token (parenIsCur) or
// the next one (after `async`).  It scans a copy of the lexer, so nothing is
// consumed; nesting is tracked with a stack because template literals, strings
// and regexes are single tokens.
//
// One scan answers the question for every parenthesis nested inside the group
// too (each is resolved when its ")" and the token after it are seen), and the
// answers are cached, so deeply nested input costs O(n) overall.
func (p *JSParser) arrowAhead(openPos int, parenIsCur bool) bool {
	if v, ok := p.arrow[openPos]; ok {
		return v
	}
	if p.arrow == nil {
		p.arrow = map[int]bool{}
	}
	type open struct {
		pos   int
		paren bool
	}
	l := p.lex.clone()
	stack := []open{{openPos, true}}
	var tok Token
	if parenIsCur {
		tok = p.peek // first token inside the parentheses
	} else {
		tok = l.Next()
	}
	pending := -1 // "(" whose ")" was just read; tok is the token after it
	for {
		if pending >= 0 {
			p.arrow[pending] = tok.Kind == TokArrow && !tok.NL
			pending = -1
			if len(stack) == 0 {
				return p.arrow[openPos]
			}
		}
		switch tok.Kind {
		case TokLParen:
			stack = append(stack, open{tok.Pos, true})
		case TokLBracket, TokLBrace:
			stack = append(stack, open{tok.Pos, false})
		case TokRParen, TokRBracket, TokRBrace:
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if top.paren {
				pending = top.pos
			}
		case TokEOF:
			// Every parenthesis still open has no match, hence is no arrow.
			for _, o := range stack {
				if o.paren {
					p.arrow[o.pos] = false
				}
			}
			return false
		}
		tok = l.Next()
	}
}

// parseParenArrow parses `(params) => body` with the cursor on `(`.
func (p *JSParser) parseParenArrow(pos int, async bool) JsExpr {
	params := p.parseParams()
	p.expect(TokArrow)
	body := p.parseArrowBody()
	return &JsArrowFunc{
		BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}},
		Params:   params, Body: body, Async: async,
	}
}

func (p *JSParser) parseArrowBody() JsNode {
	if p.cur.Kind == TokLBrace {
		return p.parseBlock()
	}
	return p.parseAssignExpr()
}

// parseTernary handles: expr ? then : else
func (p *JSParser) parseTernary() JsExpr {
	start := p.curPos()
	left := p.parseBinary(0)
	if p.cur.Kind == TokQuestion {
		p.advance()
		restore := p.allowIn()
		then := p.parseAssignExpr()
		restore()
		p.expect(TokColon)
		alt := p.parseAssignExpr()
		return &JsConditionalExpr{
			BaseExpr: BaseExpr{BaseNode{Pos: start, End: p.endAt(start)}},
			Test:     left, Then: then, Else: alt,
		}
	}
	return left
}

// Pratt precedence table
type opRow struct {
	prec  int
	right bool
	op    string
}

var binOpTable = map[TokKind]opRow{
	TokOr:         {1, false, "||"},
	TokAnd:        {2, false, "&&"},
	TokNullCoal:   {3, false, "??"},
	TokBitOr:      {4, false, "|"},
	TokBitXor:     {5, false, "^"},
	TokBitAnd:     {6, false, "&"},
	TokEqEq:       {7, false, "=="},
	TokNotEq:      {7, false, "!="},
	TokStrictEq:   {7, false, "==="},
	TokStrictNeq:  {7, false, "!=="},
	TokLt:         {8, false, "<"},
	TokGt:         {8, false, ">"},
	TokLtEq:       {8, false, "<="},
	TokGtEq:       {8, false, ">="},
	TokIn:         {8, false, "in"},
	TokInstanceof: {8, false, "instanceof"},
	TokLShift:     {9, false, "<<"},
	TokRShift:     {9, false, ">>"},
	TokURShift:    {9, false, ">>>"},
	TokPlus:       {10, false, "+"},
	TokMinus:      {10, false, "-"},
	TokStar:       {11, false, "*"},
	TokSlash:      {11, false, "/"},
	TokMod:        {11, false, "%"},
	TokStarStar:   {12, true, "**"},
}

func (p *JSParser) parseBinary(minPrec int) JsExpr {
	start := p.curPos()
	left := p.parseUnary()
	for {
		row, ok := binOpTable[p.cur.Kind]
		if !ok || row.prec <= minPrec || (p.noIn && p.cur.Kind == TokIn) {
			break
		}
		opPos := p.cur.Pos
		p.advance()
		nextPrec := row.prec
		if row.right {
			nextPrec--
		}
		right := p.parseBinary(nextPrec)
		p.checkBinaryOperands(opPos, row.op, left, right)
		if row.op == "||" || row.op == "&&" || row.op == "??" {
			left = &JsLogicalExpr{
				BaseExpr: BaseExpr{BaseNode{Pos: start, End: p.endAt(start)}},
				Op:       row.op, L: left, R: right,
			}
		} else {
			left = &JsBinaryExpr{
				BaseExpr: BaseExpr{BaseNode{Pos: start, End: p.endAt(start)}},
				Op:       row.op, L: left, R: right,
			}
		}
	}
	return left
}

// checkBinaryOperands reports the two early errors that concern operand shape:
// mixing `??` with `||` / `&&` without parentheses, and an unparenthesised
// unary operator on the left of `**`.
func (p *JSParser) checkBinaryOperands(opPos int, op string, left, right JsExpr) {
	logical := func(e JsExpr) (string, bool) {
		if l, ok := e.(*JsLogicalExpr); ok && !p.paren[e] {
			return l.Op, true
		}
		return "", false
	}
	switch op {
	case "??":
		for _, e := range []JsExpr{left, right} {
			if o, ok := logical(e); ok && (o == "||" || o == "&&") {
				p.report(opPos, "'??' cannot be mixed with '||' or '&&' without parentheses")
				return
			}
		}
	case "||", "&&":
		for _, e := range []JsExpr{left, right} {
			if o, ok := logical(e); ok && o == "??" {
				p.report(opPos, "'??' cannot be mixed with '||' or '&&' without parentheses")
				return
			}
		}
	case "**":
		if p.paren[left] {
			return
		}
		switch t := left.(type) {
		case *JsUnaryExpr:
			if t.Prefix && t.Op != "++" && t.Op != "--" {
				p.report(opPos, "unary operator before '**' must be parenthesized")
			}
		case *JsAwaitExpr:
			p.report(opPos, "'await' before '**' must be parenthesized")
		}
	}
}

func (p *JSParser) parseUnary() JsExpr {
	pos := p.curPos()
	switch p.cur.Kind {
	case TokBang, TokTilde, TokPlus, TokMinus, TokDelete, TokTypeof, TokVoid:
		op := p.advance().Val
		x := p.parseUnary()
		return &JsUnaryExpr{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}}, Op: op, Prefix: true, X: x}
	case TokPlusPlus, TokMinusMinus:
		op := p.advance().Val
		x := p.parseUnary()
		if !p.isSimpleTarget(x) {
			p.report(x.NodePos(), "invalid operand for prefix "+op)
		}
		return &JsUnaryExpr{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}}, Op: op, Prefix: true, X: x}
	case TokAwait:
		p.advance()
		x := p.parseUnary()
		return &JsAwaitExpr{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}}, X: x}
	}
	return p.parsePostfix()
}

func (p *JSParser) parsePostfix() JsExpr {
	start := p.curPos()
	x := p.parseLeftHandSide()
	// No line terminator is allowed before a postfix operator: `a\n++b`
	// is `a; ++b`.
	if (p.cur.Kind == TokPlusPlus || p.cur.Kind == TokMinusMinus) && !p.cur.NL {
		if !p.isSimpleTarget(x) {
			p.report(x.NodePos(), "invalid operand for postfix "+p.cur.Val)
		}
		op := p.advance().Val
		return &JsUnaryExpr{
			BaseExpr: BaseExpr{BaseNode{Pos: start, End: p.endAt(start)}},
			Op:       op, Prefix: false, X: x,
		}
	}
	return x
}

// parseMemberName parses the property after `.` / `?.`: any IdentifierName or
// a private name.
func (p *JSParser) parseMemberName() *JsIdent {
	pos := p.curPos()
	if isIdentName(p.cur.Kind) || p.cur.Kind == TokPrivateName {
		name := p.advance().Val
		return &JsIdent{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}}, Name: name}
	}
	p.report(pos, fmt.Sprintf("property name expected but found %s", tokDesc(p.cur)))
	if !isDelimiter(p.cur.Kind) {
		p.advance()
	}
	return emptyIdent(pos)
}

// parseTemplateTail parses a template literal token as the quasi of a tagged
// template whose tag is expr.
func (p *JSParser) parseTaggedTemplate(start int, tag JsExpr) JsExpr {
	tpos := p.curPos()
	raw := p.advance().Val
	quasi := p.structureTemplate(raw, tpos, p.end())
	return &JsTaggedTemplate{
		BaseExpr: BaseExpr{BaseNode{Pos: start, End: p.endAt(start)}},
		Tag:      tag, Quasi: quasi,
	}
}

// parseLeftHandSide handles calls, member access, tagged templates, new
// expressions.
func (p *JSParser) parseLeftHandSide() JsExpr {
	start := p.curPos()
	expr := p.parseNewOrPrimary()
	for {
		switch p.cur.Kind {
		case TokLParen:
			args := p.parseArgs()
			expr = &JsCallExpr{
				BaseExpr: BaseExpr{BaseNode{Pos: start, End: p.endAt(start)}},
				Callee:   expr, Args: args,
			}
		case TokDot:
			p.advance()
			prop := p.parseMemberName()
			if id, ok := expr.(*JsIdent); ok && id.Name == "import" && prop.Name != "meta" {
				p.report(prop.Pos, "only 'import.meta' is a valid meta property here")
			}
			expr = &JsMemberExpr{
				BaseExpr: BaseExpr{BaseNode{Pos: start, End: p.endAt(start)}},
				Obj:      expr, Prop: prop, Computed: false,
			}
		case TokLBracket:
			p.advance()
			restore := p.allowIn()
			prop := p.parseExpr()
			restore()
			p.expect(TokRBracket)
			expr = &JsMemberExpr{
				BaseExpr: BaseExpr{BaseNode{Pos: start, End: p.endAt(start)}},
				Obj:      expr, Prop: prop, Computed: true,
			}
		case TokTemplate:
			expr = p.parseTaggedTemplate(start, expr)
		case TokOptChain:
			p.advance()
			if p.cur.Kind == TokLParen {
				args := p.parseArgs()
				expr = &JsCallExpr{
					BaseExpr: BaseExpr{BaseNode{Pos: start, End: p.endAt(start)}},
					Callee:   expr, Args: args, Optional: true,
				}
			} else if p.cur.Kind == TokLBracket {
				p.advance()
				restore := p.allowIn()
				prop := p.parseExpr()
				restore()
				p.expect(TokRBracket)
				expr = &JsMemberExpr{
					BaseExpr: BaseExpr{BaseNode{Pos: start, End: p.endAt(start)}},
					Obj:      expr, Prop: prop, Computed: true, Optional: true,
				}
			} else {
				prop := p.parseMemberName()
				expr = &JsMemberExpr{
					BaseExpr: BaseExpr{BaseNode{Pos: start, End: p.endAt(start)}},
					Obj:      expr, Prop: prop, Computed: false, Optional: true,
				}
			}
		default:
			return expr
		}
	}
}

func (p *JSParser) parseNewOrPrimary() JsExpr {
	if p.cur.Kind != TokNew {
		return p.parsePrimary()
	}
	pos := p.curPos()
	newEnd := p.cur.End
	p.advance()
	// new.target
	if p.cur.Kind == TokDot {
		p.advance()
		tpos := p.curPos()
		if p.cur.Val != "target" {
			p.report(tpos, fmt.Sprintf("'new.target' expected but found %s", tokDesc(p.cur)))
		}
		name := ""
		if isIdentName(p.cur.Kind) {
			name = p.advance().Val
		}
		return &JsMemberExpr{
			BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}},
			Obj:      &JsIdent{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: newEnd}}, Name: "new"},
			Prop:     &JsIdent{BaseExpr: BaseExpr{BaseNode{Pos: tpos, End: p.endAt(tpos)}}, Name: name},
		}
	}
	callee := p.parseNewCallee()
	var args []JsExpr
	if p.cur.Kind == TokLParen {
		args = p.parseArgs()
	}
	return &JsNewExpr{
		BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}},
		Callee:   callee, Args: args,
	}
}

// parseNewCallee parses the callee of `new`: a primary (or nested `new`)
// followed by member accesses only — `new a.b.C(x)` constructs `a.b.C`, while
// the argument list belongs to the `new`.
func (p *JSParser) parseNewCallee() JsExpr {
	start := p.curPos()
	expr := p.parseNewOrPrimary()
	for {
		switch p.cur.Kind {
		case TokDot:
			p.advance()
			prop := p.parseMemberName()
			expr = &JsMemberExpr{
				BaseExpr: BaseExpr{BaseNode{Pos: start, End: p.endAt(start)}},
				Obj:      expr, Prop: prop,
			}
		case TokLBracket:
			p.advance()
			restore := p.allowIn()
			prop := p.parseExpr()
			restore()
			p.expect(TokRBracket)
			expr = &JsMemberExpr{
				BaseExpr: BaseExpr{BaseNode{Pos: start, End: p.endAt(start)}},
				Obj:      expr, Prop: prop, Computed: true,
			}
		case TokTemplate:
			expr = p.parseTaggedTemplate(start, expr)
		default:
			return expr
		}
	}
}

func (p *JSParser) parseArgs() []JsExpr {
	p.expect(TokLParen)
	defer p.allowIn()()
	var args []JsExpr
	for p.cur.Kind != TokRParen && p.cur.Kind != TokEOF {
		if p.cur.Kind == TokEllipsis {
			ep := p.curPos()
			p.advance()
			x := p.parseAssignExpr()
			args = append(args, &JsSpreadExpr{BaseExpr: BaseExpr{BaseNode{Pos: ep, End: p.endAt(ep)}}, X: x})
		} else {
			args = append(args, p.parseAssignExpr())
		}
		if !p.eat(TokComma) {
			break
		}
	}
	p.expect(TokRParen)
	return args
}

// placeholder builds the "unknown" literal used where an expression was
// required but the token cannot start one.  A diagnostic is always recorded.
func (p *JSParser) placeholder(pos int) JsExpr {
	p.report(pos, fmt.Sprintf("expression expected but found %s", tokDesc(p.cur)))
	tok := p.cur
	// Delimiters are left for the enclosing construct to consume.
	if !isDelimiter(tok.Kind) && tok.Kind != TokRBrace {
		p.advance()
		return &JsLiteral{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}}, Raw: tok.Val, Kind: "unknown"}
	}
	return &JsLiteral{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: pos}}, Kind: "unknown"}
}

func (p *JSParser) parsePrimary() JsExpr {
	pos := p.curPos()

	switch p.cur.Kind {
	case TokIdent:
		name := p.advance().Val
		return &JsIdent{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}}, Name: name}

	case TokPrivateName:
		// Only valid as the left operand of `#x in obj`.
		name := p.advance().Val
		if p.cur.Kind != TokIn {
			p.report(pos, "private name is only allowed as a member name or before 'in'")
		}
		return &JsIdent{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}}, Name: name}

	case TokNumber:
		raw := p.advance().Val
		return &JsLiteral{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}}, Raw: raw, Kind: numberKind(raw)}

	case TokString:
		raw := p.advance().Val
		return &JsLiteral{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}}, Raw: raw, Kind: "string"}

	case TokTemplate:
		raw := p.advance().Val
		return p.structureTemplate(raw, pos, p.end())

	case TokRegex:
		raw := p.advance().Val
		p.checkRegexFlags(pos, raw)
		return &JsLiteral{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}}, Raw: raw, Kind: "regex"}

	case TokTrue, TokFalse:
		raw := p.advance().Val
		return &JsLiteral{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}}, Raw: raw, Kind: "boolean"}

	case TokNull:
		p.advance()
		return &JsLiteral{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}}, Raw: "null", Kind: "null"}

	case TokThis:
		p.advance()
		return &JsThisExpr{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}}}

	case TokSuper:
		p.advance()
		return &JsSuperExpr{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}}}

	case TokImport:
		// import(…) and import.meta — modelled as a call / member access on
		// the identifier `import` (see JsIdent).
		p.advance()
		if p.cur.Kind != TokLParen && p.cur.Kind != TokDot {
			p.report(pos, "'import' must be followed by '(' or '.meta' in an expression")
		}
		return &JsIdent{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}}, Name: "import"}

	case TokFunction:
		return p.parseFuncExpr(pos, false)

	case TokClass:
		name, super, members := p.parseClassTail(false)
		return &JsClassExpr{
			BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}},
			Name:     name, SuperClass: super, Members: members,
		}

	case TokAsync:
		// async function expression; `async` alone is an identifier
		// (async arrows are handled in parseAssignExpr).
		if p.peek.Kind == TokFunction && !p.peek.NL {
			p.advance() // async
			return p.parseFuncExpr(pos, true)
		}
		name := p.advance().Val
		return &JsIdent{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}}, Name: name}

	case TokLParen:
		// Arrow functions are handled in parseAssignExpr; one that reaches
		// this point is an operand (`!(a) => b`, `x || (a) => b`), which is a
		// syntax error.  Parse it anyway so recovery stays tidy.
		if p.arrowAhead(pos, true) {
			p.report(pos, "arrow function is not allowed here (parenthesise it)")
			return p.parseParenArrow(pos, false)
		}
		p.advance()
		if p.cur.Kind == TokRParen {
			p.report(p.cur.Pos, "expression expected inside parentheses")
			p.advance()
			return &JsLiteral{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}}, Raw: "()", Kind: "unknown"}
		}
		restore := p.allowIn()
		expr := p.parseExpr()
		restore()
		p.expect(TokRParen)
		if p.paren == nil {
			p.paren = map[JsExpr]bool{}
		}
		p.paren[expr] = true
		return expr

	case TokLBracket:
		return p.parseArrayLit()

	case TokLBrace:
		return p.parseObjectLit()

	case TokYield:
		p.advance()
		delegate := false
		if !p.cur.NL {
			delegate = p.eat(TokStar)
		}
		var x JsExpr
		if !p.cur.NL {
			switch p.cur.Kind {
			case TokSemi, TokRBrace, TokRParen, TokRBracket, TokComma, TokColon, TokEOF:
			default:
				x = p.parseAssignExpr()
			}
		}
		return &JsYieldExpr{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}}, X: x, Delegate: delegate}
	}

	// Contextual keywords can appear as identifiers in expression position.
	if isContextualIdent(p.cur.Kind) {
		name := p.advance().Val
		return &JsIdent{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}}, Name: name}
	}
	return p.placeholder(pos)
}

// checkRegexFlags validates the flag letters of a regex literal (the pattern
// itself is not parsed).
func (p *JSParser) checkRegexFlags(pos int, raw string) {
	i := strings.LastIndexByte(raw, '/')
	if i <= 0 {
		return // unterminated: already reported by the lexer
	}
	flags := raw[i+1:]
	seen := map[rune]bool{}
	for _, f := range flags {
		if !strings.ContainsRune("dgimsuvy", f) || seen[f] {
			p.report(pos+i+1, fmt.Sprintf("invalid regular expression flags %q", flags))
			return
		}
		seen[f] = true
	}
	if seen['u'] && seen['v'] {
		p.report(pos+i+1, "regular expression flags 'u' and 'v' cannot be combined")
	}
}

// parseFuncExpr parses a function expression with the cursor on `function`;
// pos is where the node starts (the `async` keyword when async).
func (p *JSParser) parseFuncExpr(pos int, async bool) JsExpr {
	p.advance() // function
	gen := p.eat(TokStar)
	name := ""
	if n, ok := p.curIdent(); ok {
		name = n
		p.advance()
	}
	params := p.parseParams()
	body := p.parseBlock()
	return &JsFuncExpr{
		BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}},
		Name:     name, Params: params, Body: body, Async: async, Generator: gen,
	}
}

func (p *JSParser) parseArrayLit() *JsArrayExpr {
	pos := p.curPos()
	p.advance()
	defer p.allowIn()()
	var elems []JsExpr
	for p.cur.Kind != TokRBracket && p.cur.Kind != TokEOF {
		if p.cur.Kind == TokComma {
			elems = append(elems, nil)
			p.advance()
			continue
		}
		if p.cur.Kind == TokEllipsis {
			ep := p.curPos()
			p.advance()
			x := p.parseAssignExpr()
			elems = append(elems, &JsSpreadExpr{BaseExpr: BaseExpr{BaseNode{Pos: ep, End: p.endAt(ep)}}, X: x})
		} else {
			elems = append(elems, p.parseAssignExpr())
		}
		if !p.eat(TokComma) {
			break
		}
	}
	p.expect(TokRBracket)
	return &JsArrayExpr{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}}, Elems: elems}
}

func (p *JSParser) parseObjectLit() *JsObjectExpr {
	pos := p.curPos()
	p.advance()
	defer p.allowIn()()
	var props []*JsProp
	for p.cur.Kind != TokRBrace && p.cur.Kind != TokEOF {
		props = append(props, p.parseObjectProp())
		if !p.eat(TokComma) {
			break
		}
	}
	p.expect(TokRBrace)
	return &JsObjectExpr{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}}, Props: props}
}

func (p *JSParser) parseObjectProp() *JsProp {
	pp := p.curPos()
	if p.cur.Kind == TokEllipsis {
		p.advance()
		x := p.parseAssignExpr()
		return &JsProp{
			BaseNode: BaseNode{Pos: pp, End: p.endAt(pp)},
			Spread:   true, Value: x, Kind: "init",
		}
	}

	kind := "init"
	isAsync, isGen := false, false

	// A modifier-looking token followed by one of these is itself the key:
	// `{ async() {} }`, `{ get: 1 }`, `{ async }`, `{ get = 1 }` (pattern).
	nameFollows := func() bool {
		switch p.peek.Kind {
		case TokColon, TokComma, TokRBrace, TokLParen, TokAssign:
			return true
		}
		return false
	}
	if p.cur.Kind == TokAsync && !nameFollows() && !p.peek.NL {
		isAsync = true
		p.advance()
	}
	if p.cur.Kind == TokStar {
		isGen = true
		p.advance()
	}
	if !isAsync && !isGen && p.cur.Kind == TokIdent && (p.cur.Val == "get" || p.cur.Val == "set") && !nameFollows() {
		kind = p.advance().Val
	}
	hasModifier := isAsync || isGen || kind != "init"

	computed := false
	var key JsExpr
	if p.cur.Kind == TokLBracket {
		p.advance()
		computed = true
		restore := p.allowIn()
		key = p.parseAssignExpr()
		restore()
		p.expect(TokRBracket)
	} else {
		key = p.parsePropKey()
	}

	shorthand, isMethod := false, false
	var val JsExpr
	switch {
	case p.cur.Kind == TokLParen:
		fpos := p.curPos()
		params := p.parseParams()
		body := p.parseBlock()
		val = &JsFuncExpr{
			BaseExpr: BaseExpr{BaseNode{Pos: fpos, End: p.endAt(fpos)}},
			Params:   params, Body: body, Async: isAsync, Generator: isGen,
		}
		// get/set carry their own Kind; only plain methods get the Method flag.
		isMethod = kind == "init"
	case !hasModifier && p.eat(TokColon):
		val = p.parseAssignExpr()
	default:
		if hasModifier {
			p.report(p.cur.Pos, fmt.Sprintf("'(' expected after method name but found %s", tokDesc(p.cur)))
		}
		if _, isIdent := key.(*JsIdent); !isIdent || computed {
			p.report(p.cur.Pos, fmt.Sprintf("':' expected after property name but found %s", tokDesc(p.cur)))
		}
		shorthand = true
		val = cloneIdent(key)
		// `{ a = 1 }` — shorthand with default (valid when the literal is
		// later reinterpreted as a pattern: `({a = 1} = o)`, `({a = 1}) => a`).
		if !hasModifier && p.eat(TokAssign) {
			def := p.parseAssignExpr()
			val = &JsAssignExpr{
				BaseExpr: BaseExpr{BaseNode{Pos: val.NodePos(), End: p.endAt(val.NodePos())}},
				Op:       "=", L: val, R: def,
			}
		}
	}

	return &JsProp{
		BaseNode: BaseNode{Pos: pp, End: p.endAt(pp)},
		Key:      key, Value: val,
		Computed: computed, Shorthand: shorthand, Method: isMethod, Kind: kind,
	}
}

// parsePropKey parses a property name: identifier / keyword, string or number.
// Computed keys and private names are handled by the callers.
func (p *JSParser) parsePropKey() JsExpr {
	pos := p.curPos()
	switch {
	case p.cur.Kind == TokString:
		raw := p.advance().Val
		return &JsLiteral{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}}, Raw: raw, Kind: "string"}
	case p.cur.Kind == TokNumber:
		raw := p.advance().Val
		return &JsLiteral{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}}, Raw: raw, Kind: numberKind(raw)}
	case isIdentName(p.cur.Kind):
		name := p.advance().Val
		return &JsIdent{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: p.endAt(pos)}}, Name: name}
	}
	p.report(pos, fmt.Sprintf("property name expected but found %s", tokDesc(p.cur)))
	if !isDelimiter(p.cur.Kind) {
		p.advance()
	}
	return emptyIdent(pos)
}

// ──────────────────────────────────────────────
//  Helpers
// ──────────────────────────────────────────────

// seqToParams converts a comma-separated sequence expression into a param list.
// (No longer used by the parser — arrow parameters are parsed as binding
// patterns after a look-ahead — kept for callers elsewhere in the package.)
func seqToParams(expr JsExpr) []JsExpr {
	if seq, ok := expr.(*JsSeqExpr); ok {
		return seq.Exprs
	}
	return []JsExpr{expr}
}

// unquoteStr strips surrounding quotes/backticks from a JS string token.
func unquoteStr(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'' || s[0] == '`') {
		return s[1 : len(s)-1]
	}
	return s
}

// structureTemplate splits the raw source text of a template literal
// (including the enclosing backticks) into its alternating literal
// chunks and ${...} interpolation expressions.
//
// Each hole is located with the real tokenizer (Lexer.scanHole), so braces,
// backticks and quotes inside strings, comments, regexes and nested templates
// cannot end it early, and is then parsed *in place* — a sub-parser is
// started at the hole's absolute offset, so every node already carries
// coordinates in the original source and no relocation pass is needed.
//
// pos/end are the absolute offsets of raw within p.src.  A malformed template
// (no closing backtick, unclosed ${) has already been reported by the lexer;
// whatever was scanned becomes the final quasi.
func (p *JSParser) structureTemplate(raw string, pos, end int) *JsTemplateLit {
	lit := &JsTemplateLit{BaseExpr: BaseExpr{BaseNode{Pos: pos, End: end}}}

	var quasi strings.Builder
	i := 0
	if len(raw) > 0 && raw[0] == '`' {
		i = 1
	}
scan:
	for i < len(raw) {
		c := raw[i]
		switch {
		case c == '\\' && i+1 < len(raw):
			// Escape: copy the backslash and the next char verbatim.
			quasi.WriteByte(c)
			quasi.WriteByte(raw[i+1])
			i += 2
		case c == '`':
			break scan // closing backtick
		case c == '$' && i+1 < len(raw) && raw[i+1] == '{':
			lit.Quasis = append(lit.Quasis, quasi.String())
			quasi.Reset()
			holeStart := pos + i + 2
			hl := &Lexer{src: p.src[:end], pos: holeStart, line: p.lineAt(holeStart)}
			closeAt := hl.scanHole()
			if closeAt < 0 { // unterminated: the hole runs to the end
				closeAt = end
			}
			lit.Exprs = append(lit.Exprs, p.parseTemplateHole(holeStart, closeAt))
			i = closeAt - pos + 1
		default:
			quasi.WriteByte(c)
			i++
		}
	}
	// Trailing quasi (always present — maintains the N+1 invariant).
	lit.Quasis = append(lit.Quasis, quasi.String())
	return lit
}

// parseTemplateHole parses src[start:end] (the text of one ${…}) as an
// expression, in place.
func (p *JSParser) parseTemplateHole(start, end int) JsExpr {
	if strings.TrimSpace(p.src[start:end]) == "" {
		p.report(start, "expression expected in template placeholder")
		// Degrade: empty interpolation → empty-string literal.
		return &JsLiteral{BaseExpr: BaseExpr{BaseNode{Pos: start, End: start}}, Raw: "''", Kind: "string"}
	}
	src := p.src[:end]
	p.lineAt(0) // make sure the shared line index exists
	sub := &JSParser{lex: &Lexer{src: src, pos: start, line: p.lineAt(start)}, src: src, lines: p.lines}
	sub.cur = sub.lex.Next()
	sub.peek = sub.lex.Next()
	expr := sub.parseExpr()
	if sub.cur.Kind != TokEOF {
		sub.report(sub.cur.Pos, fmt.Sprintf("unexpected %s in template expression", tokDesc(sub.cur)))
	}
	for _, d := range sub.Diagnostics {
		p.addDiag(d)
	}
	return expr
}

// ShiftTree moves every node in a sub-tree by delta bytes, so a
// tree parsed from a detached substring lines up with the original
// source coordinates.  Skips synthetic nodes (Pos == 0 && End == 0) —
// they have no source span to shift.
//
// The parser itself no longer needs this (template holes are parsed in
// place); it is kept for other callers.
func ShiftTree(root JsNode, delta int) {
	if delta == 0 || root == nil {
		return
	}
	seen := map[JsNode]bool{}
	WalkNode(root, func(n JsNode) bool {
		if seen[n] { // a node reachable twice must only be shifted once
			return true
		}
		seen[n] = true
		if s, ok := n.(posShiftable); ok {
			if n.NodePos() != 0 || n.NodeEnd() != 0 {
				s.shiftPos(delta)
			}
		}
		return true
	})
}
