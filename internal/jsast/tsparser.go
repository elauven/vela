package jsast

import (
	"fmt"
	"strings"
)

// TypeScript parsing (task 080). The JavaScript parser calls into this file
// only when p.ts is set, so JavaScript parsing is unchanged. See tsast.go for
// the model: types are erased while parsing and recorded beside the tree.

// ParseWithOptions parses a module in the language opts selects and returns
// its tree and diagnostics (sorted by position). With opts.TypeScript the
// tree has no TypeScript in it and prog.TS describes what was erased.
func ParseWithOptions(src string, opts ParseOptions) (*JsProgram, []JSParseDiagnostic) {
	p := newJSParserOpts(src, opts)
	prog := p.parseProgram()
	p.finish()
	return prog, p.Diagnostics
}

// DetectTypeScript reports whether src, which failed to parse as JavaScript,
// parses cleanly as TypeScript, and the offset of its first TypeScript-only
// syntax. The compiler uses it to say "add lang=ts" (VELA-TS-IN-JS) instead
// of a JavaScript syntax error.
func DetectTypeScript(src string) (int, bool) {
	p := newJSParserOpts(src, ParseOptions{TypeScript: true})
	p.parseProgram()
	for _, d := range p.Diagnostics {
		if d.Code != DiagTSNonErasable {
			return 0, false
		}
	}
	if p.firstTS < 0 {
		return 0, false
	}
	return p.firstTS, true
}

func newJSParserOpts(src string, opts ParseOptions) *JSParser {
	p := &JSParser{lex: NewLexer(src), src: src, firstTS: -1}
	if opts.TypeScript {
		p.ts = true
		p.tsInfo = &TSInfo{}
	}
	p.lex.onErr = p.lexReport
	p.cur = p.lex.Next()
	p.peek = p.lex.Next()
	return p
}

// ──────────────────────────────────────────────
//  Speculative parsing
// ──────────────────────────────────────────────
//
// TypeScript's grammar needs look-ahead that the JavaScript parser's
// two-token window cannot give: `f<T>(x)` is a call with a type argument
// while `a < b > (c)` is not, `(a): T => a` is an arrow, `(a)` alone is
// not. try runs a parse function speculatively: while it runs, diagnostics
// are collected instead of reported; when it fails (returns false or reports
// a diagnostic) the parser is put back exactly where it was. When it
// succeeds the parse is kept, so nothing is parsed twice.

type specDiag struct {
	d       JSParseDiagnostic
	lexical bool
}

type parseState struct {
	lex                       Lexer
	parens                    []bool
	cur, peek                 Token
	prevEnd                   int
	noIn, condThen, noCond    bool
	decls, imports, refs, tps int
	erased                    int
	firstTS                   int
}

func (p *JSParser) save() parseState {
	s := parseState{
		lex: *p.lex, parens: append([]bool(nil), p.lex.parens...),
		cur: p.cur, peek: p.peek, prevEnd: p.prevEnd,
		noIn: p.noIn, condThen: p.condThen, noCond: p.noCondType,
		tps: len(p.typeParams), firstTS: p.firstTS,
	}
	if p.tsInfo != nil {
		s.decls, s.imports, s.refs = len(p.tsInfo.Decls), len(p.tsInfo.Imports), len(p.tsInfo.Refs)
		s.erased = len(p.tsInfo.Erased)
	}
	return s
}

func (p *JSParser) restore(s parseState) {
	*p.lex = s.lex
	p.lex.parens = append([]bool(nil), s.parens...)
	p.cur, p.peek, p.prevEnd = s.cur, s.peek, s.prevEnd
	p.noIn, p.condThen, p.noCondType = s.noIn, s.condThen, s.noCond
	p.typeParams = p.typeParams[:s.tps]
	p.firstTS = s.firstTS
	if p.tsInfo != nil {
		p.tsInfo.Decls = p.tsInfo.Decls[:s.decls]
		p.tsInfo.Imports = p.tsInfo.Imports[:s.imports]
		p.tsInfo.Refs = p.tsInfo.Refs[:s.refs]
		p.tsInfo.Erased = p.tsInfo.Erased[:s.erased]
	}
}

// try runs fn speculatively; see above. A lexical error of a token that fn
// did not consume (the look-ahead) does not make it fail: it is reported
// again, for real, when it belongs to the parse that is kept.
func (p *JSParser) try(fn func() bool) bool {
	st := p.save()
	mark := len(p.specDiags)
	p.spec++
	ok := fn()
	p.spec--
	collected := append([]specDiag(nil), p.specDiags[mark:]...)
	p.specDiags = p.specDiags[:mark]
	if ok {
		for _, d := range collected {
			if !d.lexical || d.d.Pos < p.cur.Pos {
				ok = false
				break
			}
		}
	}
	if !ok {
		p.restore(st)
		return false
	}
	for _, d := range collected {
		p.addDiagKind(d.d, true)
	}
	return true
}

// lexReport receives the lexer's errors.
func (p *JSParser) lexReport(pos int, msg string) {
	p.addDiagKind(JSParseDiagnostic{Pos: pos, Message: fmt.Sprintf("line %d: %s", p.lineAt(pos), msg)}, true)
}

// reportCode records a diagnostic with a code.
func (p *JSParser) reportCode(pos int, code DiagCode, msg string) {
	p.addDiag(JSParseDiagnostic{Pos: pos, Message: fmt.Sprintf("line %d: %s", p.lineAt(pos), msg), Code: code})
}

// dropDiagsFrom forgets the diagnostics at or after pos: the lexer's errors
// of a token that is scanned again (rescanRegexAsDivision).
func (p *JSParser) dropDiagsFrom(pos int) {
	if p.spec > 0 {
		kept := p.specDiags[:0]
		for _, d := range p.specDiags {
			if d.d.Pos < pos {
				kept = append(kept, d)
			}
		}
		p.specDiags = kept
		return
	}
	kept := p.Diagnostics[:0]
	for _, d := range p.Diagnostics {
		if d.Pos < pos {
			kept = append(kept, d)
		} else {
			delete(p.seen, d.Pos)
		}
	}
	p.Diagnostics = kept
}

// rescanRegexAsDivision fixes the one place where TypeScript changes how the
// source is tokenized: after a non-null `!`, the closing `>` of type
// arguments or a type after `as`, a `/` divides, but the lexer, which saw an
// operator before it, read a regular expression. The lexer is positioned
// after p.peek, so both tokens are scanned again.
func (p *JSParser) rescanRegexAsDivision() {
	if p.cur.Kind != TokRegex {
		return
	}
	switch p.peek.Kind {
	case TokLParen:
		if n := len(p.lex.parens); n > 0 {
			p.lex.parens = p.lex.parens[:n-1]
		}
	case TokRParen:
		p.lex.parens = append(p.lex.parens, false)
	}
	p.dropDiagsFrom(p.cur.Pos)
	p.lex.pos, p.lex.line, p.lex.afterValue = p.cur.Pos, p.cur.Line, true
	p.lex.prev, p.lex.prev2 = TokIdent, TokIdent
	nl := p.cur.NL
	p.cur = p.lex.Next()
	p.cur.NL = nl
	p.peek = p.lex.Next()
}

// Speculative attempts that fail are remembered by position. Parsing type
// arguments or a function type from a given token depends only on the
// tokens from there on, so an attempt that failed inside a larger failed
// attempt fails again: without the memo `a < a < a …` would parse every
// suffix of the chain once per `<` (quadratic), and nested parentheses in
// invalid types exponentially often.

type failKind uint8

const (
	failTypeArgs failKind = iota
	failFunctionType
)

type failKey struct {
	pos  int
	kind failKind
}

// knownToFail reports, while speculating, that the attempt of kind at pos
// failed before; it reports a diagnostic so the enclosing try fails too.
func (p *JSParser) knownToFail(kind failKind, pos int) bool {
	if p.spec == 0 || !p.failed[failKey{pos, kind}] {
		return false
	}
	p.report(pos, "type syntax expected")
	return true
}

// rememberFailure records that the attempt of kind at pos failed: it
// reported diagnostics after mark while speculating.
func (p *JSParser) rememberFailure(kind failKind, pos, mark int) {
	if p.spec == 0 || len(p.specDiags) <= mark {
		return
	}
	if p.failed == nil {
		p.failed = map[failKey]bool{}
	}
	p.failed[failKey{pos, kind}] = true
}

// ──────────────────────────────────────────────
//  Token helpers
// ──────────────────────────────────────────────

// isWord reports whether the current token is the identifier w (a
// TypeScript contextual keyword such as `type` or `readonly`).
func (p *JSParser) isWord(w string) bool { return p.cur.Kind == TokIdent && p.cur.Val == w }

func isWordTok(t Token, w string) bool { return t.Kind == TokIdent && t.Val == w }

// peek2 returns the token after p.peek without consuming anything.
func (p *JSParser) peek2() Token { return p.lex.clone().Next() }

// peekTokens returns the n tokens after p.peek.
func (p *JSParser) peekTokens(n int) []Token {
	l := p.lex.clone()
	out := make([]Token, n)
	for i := range out {
		out[i] = l.Next()
	}
	return out
}

// markTS records TypeScript-only syntax at pos (DetectTypeScript).
func (p *JSParser) markTS(pos int) {
	if p.firstTS < 0 || pos < p.firstTS {
		p.firstTS = pos
	}
}

// blank records [pos, end) as TypeScript syntax (TSInfo.Erased).
func (p *JSParser) blank(pos, end int, kind TSErasedKind) {
	if p.tsInfo != nil && end > pos {
		p.tsInfo.Erased = append(p.tsInfo.Erased, TSErased{Pos: pos, End: end, Kind: kind})
	}
}

// blankFrom records the syntax from pos to the end of the last token.
func (p *JSParser) blankFrom(pos int, kind TSErasedKind) { p.blank(pos, p.prevEnd, kind) }

// blankCur records the current token, unless it is inside a type, whose
// range is recorded whole.
func (p *JSParser) blankCur() {
	if p.inType == 0 {
		p.blank(p.cur.Pos, p.cur.End, TSErasedSyntax)
	}
}

// blankMove records that the byte at from moves to to (TSErasedMove).
func (p *JSParser) blankMove(from, to int) {
	if p.tsInfo != nil && from != to {
		p.tsInfo.Erased = append(p.tsInfo.Erased, TSErased{Pos: from, End: to, Kind: TSErasedMove})
	}
}

// eatTypeColon consumes the `:` of an annotation.
func (p *JSParser) eatTypeColon() bool {
	if p.cur.Kind != TokColon {
		return false
	}
	p.markTS(p.cur.Pos)
	p.blankCur()
	p.advance()
	return true
}

// typeEntry starts a type, a type argument or a type parameter list; the
// function it returns ends it and records its range when it is outermost:
//
//	defer p.typeEntry()()
func (p *JSParser) typeEntry() func() {
	start := p.cur.Pos
	p.inType++
	return func() {
		p.inType--
		if p.inType == 0 {
			p.blankFrom(start, TSErasedSyntax)
		}
	}
}

func hasLineBreak(s string) bool { return strings.ContainsAny(s, "\n\r\u2028\u2029") }

// splitPunct maps the remainder of a split `<…` / `>…` token.
var splitPunct = map[string]TokKind{
	"<": TokLt, "<=": TokLtEq, "=": TokAssign,
	">": TokGt, ">>": TokRShift, ">=": TokGtEq, ">>=": TokRShiftAssign,
}

// splitFirst consumes the first character of the current `<<`, `>>`, `>=`,
// … token and leaves the rest as the current token: `Array<Array<T>>`
// closes two lists with one `>>` token.
func (p *JSParser) splitFirst() {
	rest := p.cur.Val[1:]
	p.prevEnd = p.cur.Pos + 1
	p.cur = Token{Kind: splitPunct[rest], Val: rest, Pos: p.cur.Pos + 1, End: p.cur.End, Line: p.cur.Line}
}

func isGtLike(k TokKind) bool {
	switch k {
	case TokGt, TokRShift, TokURShift, TokGtEq, TokRShiftAssign, TokURShiftAssign:
		return true
	}
	return false
}

// eatGt consumes one `>`, splitting a longer token.
func (p *JSParser) eatGt() bool {
	if p.cur.Kind == TokGt {
		p.advance()
		return true
	}
	if isGtLike(p.cur.Kind) {
		if p.cur.Kind == TokURShift || p.cur.Kind == TokURShiftAssign {
			// `>>>` → `>>`, `>>>=` → `>>=`
			rest := p.cur.Val[1:]
			p.prevEnd = p.cur.Pos + 1
			p.cur = Token{Kind: splitPunct[rest], Val: rest, Pos: p.cur.Pos + 1, End: p.cur.End, Line: p.cur.Line}
			return true
		}
		p.splitFirst()
		return true
	}
	return false
}

func (p *JSParser) expectGt() {
	if !p.eatGt() {
		p.report(p.cur.Pos, fmt.Sprintf("expected '>' but found %s", tokDesc(p.cur)))
	}
}

// expectLt consumes one `<`, splitting `<<` and `<=`.
func (p *JSParser) expectLt() {
	switch p.cur.Kind {
	case TokLt:
		p.advance()
	case TokLShift, TokLtEq, TokLShiftAssign:
		if p.cur.Kind == TokLShiftAssign {
			rest := p.cur.Val[1:]
			p.prevEnd = p.cur.Pos + 1
			p.cur = Token{Kind: TokLtEq, Val: rest, Pos: p.cur.Pos + 1, End: p.cur.End, Line: p.cur.Line}
			return
		}
		p.splitFirst()
	default:
		p.report(p.cur.Pos, fmt.Sprintf("expected '<' but found %s", tokDesc(p.cur)))
	}
}

// canFollowModifier reports whether t may follow a modifier keyword such as
// `readonly` or `public`, which makes that keyword a modifier rather than
// a name.
func canFollowModifier(t Token) bool {
	if t.NL {
		return false
	}
	switch t.Kind {
	case TokLBracket, TokLBrace, TokStar, TokEllipsis, TokPrivateName, TokString, TokNumber:
		return true
	}
	return isIdentName(t.Kind)
}

// erase marks s as a statement that exists only in TypeScript: statement
// lists drop it.
func (p *JSParser) erase(s JsStmt) JsStmt {
	if p.erased == nil {
		p.erased = map[JsStmt]bool{}
	}
	p.erased[s] = true
	return s
}

func (p *JSParser) isErased(s JsStmt) bool { return s != nil && p.erased[s] }

func (p *JSParser) erasedStmt(pos int) JsStmt {
	p.blankFrom(pos, TSErasedStatement)
	return p.erase(&JsEmptyStmt{BaseStmt: BaseStmt{BaseNode{Pos: pos, End: p.endAt(pos)}}})
}

// addDecl records a top-level erased declaration.
func (p *JSParser) addDecl(d *TSDecl) {
	if p.tsInfo == nil || p.depth > 0 {
		return
	}
	p.tsInfo.Decls = append(p.tsInfo.Decls, d)
}

func (p *JSParser) lastDecl() *TSDecl {
	if p.tsInfo == nil || len(p.tsInfo.Decls) == 0 {
		return nil
	}
	return p.tsInfo.Decls[len(p.tsInfo.Decls)-1]
}

func (p *JSParser) pushTypeParam(name string) { p.typeParams = append(p.typeParams, name) }

func (p *JSParser) popTypeParams(n int) {
	if n < len(p.typeParams) {
		p.typeParams = p.typeParams[:n]
	}
}

// recordRef records a name used in a type position unless it names a type
// parameter in scope.
func (p *JSParser) recordRef(name string, pos, end int, query bool) {
	if p.tsInfo == nil || name == "" || name == "this" {
		return
	}
	if !query {
		for i := len(p.typeParams) - 1; i >= 0; i-- {
			if p.typeParams[i] == name {
				return
			}
		}
	}
	p.tsInfo.Refs = append(p.tsInfo.Refs, TSRef{Pos: pos, End: end, Name: name, Query: query})
}

// nestedTypes clears the "no conditional type" context of an `extends`
// clause inside brackets; it returns the restore function.
func (p *JSParser) nestedTypes() func() {
	old := p.noCondType
	p.noCondType = false
	return func() { p.noCondType = old }
}

// ──────────────────────────────────────────────
//  Types
// ──────────────────────────────────────────────

var tsKeywordTypes = map[string]bool{
	"any": true, "unknown": true, "never": true, "undefined": true, "boolean": true,
	"number": true, "string": true, "bigint": true, "symbol": true, "object": true,
	"intrinsic": true,
}

func (p *JSParser) typeNode(pos int, kind TSTypeKind) *TSType {
	return &TSType{Pos: pos, End: p.endAt(pos), Kind: kind}
}

// parseType parses a type, including function, constructor and
// conditional types.
func (p *JSParser) parseType() *TSType {
	defer p.typeEntry()()
	pos := p.curPos()
	p.markTS(pos)
	switch {
	case p.cur.Kind == TokLt:
		return p.parseFunctionType(pos, false, false)
	case p.cur.Kind == TokNew:
		p.advance()
		return p.parseFunctionType(pos, true, false)
	case p.isWord("abstract") && p.peek.Kind == TokNew && !p.peek.NL:
		p.advance()
		p.advance()
		return p.parseFunctionType(pos, true, true)
	case p.cur.Kind == TokLParen:
		var ft *TSType
		if p.try(func() bool { ft = p.parseFunctionType(pos, false, false); return ft != nil }) {
			return ft
		}
	}
	n := len(p.typeParams) // `infer X` names are in scope until the conditional ends
	t := p.parseUnionType()
	if !p.noCondType && p.cur.Kind == TokExtends && !p.cur.NL {
		p.advance()
		p.noCondType = true
		ext := p.parseType()
		p.noCondType = false
		p.expect(TokQuestion)
		yes := p.parseType()
		p.expect(TokColon)
		p.popTypeParams(n)
		no := p.parseType()
		c := p.typeNode(pos, TSConditional)
		c.Args = []*TSType{t, ext, yes, no}
		return c
	}
	p.popTypeParams(n)
	return t
}

// parseFunctionType parses `<T>(params) => R` (ctor: after `new`). Inside
// try it returns nil when no `=>` follows the parameters.
func (p *JSParser) parseFunctionType(pos int, ctor, abstract bool) *TSType {
	if p.knownToFail(failFunctionType, pos) {
		return nil
	}
	defer p.rememberFailure(failFunctionType, pos, len(p.specDiags))
	n := len(p.typeParams)
	defer p.popTypeParams(n)
	sig := &TSSignature{}
	if p.cur.Kind == TokLt || p.cur.Kind == TokLShift {
		sig.TypeParams = p.parseTypeParams()
	}
	if p.cur.Kind != TokLParen && p.spec > 0 {
		return nil
	}
	sig.Params, sig.This = p.parseSigParams()
	if p.cur.Kind != TokArrow {
		if p.spec > 0 {
			return nil
		}
		p.report(p.cur.Pos, fmt.Sprintf("expected '=>' but found %s", tokDesc(p.cur)))
	} else {
		p.advance()
	}
	sig.Return = p.parseReturnType()
	kind := TSFunction
	if ctor {
		kind = TSConstructor
	}
	t := p.typeNode(pos, kind)
	t.Sig, t.Abstract = sig, abstract
	return t
}

func (p *JSParser) parseUnionType() *TSType {
	pos := p.curPos()
	p.eat(TokBitOr) // a leading `|`
	first := p.parseIntersectionType()
	if p.cur.Kind != TokBitOr {
		return first
	}
	members := []*TSType{first}
	for p.eat(TokBitOr) {
		members = append(members, p.parseIntersectionType())
	}
	t := p.typeNode(pos, TSUnion)
	t.Args = members
	return t
}

func (p *JSParser) parseIntersectionType() *TSType {
	pos := p.curPos()
	p.eat(TokBitAnd)
	first := p.parseTypeOperator()
	if p.cur.Kind != TokBitAnd {
		return first
	}
	members := []*TSType{first}
	for p.eat(TokBitAnd) {
		members = append(members, p.parseTypeOperator())
	}
	t := p.typeNode(pos, TSIntersection)
	t.Args = members
	return t
}

// typeCanStart reports whether t can begin a type (used to tell the
// operator `keyof T` from a type named `keyof`).
func typeCanStart(t Token) bool {
	switch t.Kind {
	case TokComma, TokRParen, TokRBracket, TokRBrace, TokGt, TokAssign, TokSemi,
		TokBitOr, TokBitAnd, TokColon, TokQuestion, TokEOF, TokDot, TokArrow, TokExtends:
		return false
	}
	return !isGtLike(t.Kind)
}

func (p *JSParser) parseTypeOperator() *TSType {
	pos := p.curPos()
	if p.cur.Kind == TokIdent {
		switch p.cur.Val {
		case "keyof", "unique", "readonly":
			if typeCanStart(p.peek) {
				op := p.advance().Val
				x := p.parseTypeOperator()
				t := p.typeNode(pos, TSOperator)
				t.Name, t.Args = op, []*TSType{x}
				return t
			}
		case "infer":
			if isBindingKind(p.peek.Kind) {
				p.advance()
				name := p.bindingName()
				t := p.typeNode(pos, TSInfer)
				t.Name = name
				p.pushTypeParam(name)
				if p.cur.Kind == TokExtends {
					var c *TSType
					if p.try(func() bool {
						p.advance()
						old := p.noCondType
						p.noCondType = true
						c = p.parseType()
						p.noCondType = old
						return old || p.cur.Kind != TokQuestion
					}) {
						t.Args = []*TSType{c}
					}
				}
				t.End = p.endAt(pos)
				return t
			}
		}
	}
	return p.parsePostfixType()
}

func (p *JSParser) parsePostfixType() *TSType {
	pos := p.curPos()
	t := p.parseNonArrayType()
	for p.cur.Kind == TokLBracket && !p.cur.NL {
		if p.peek.Kind == TokRBracket {
			p.advance()
			p.advance()
			a := p.typeNode(pos, TSArray)
			a.Args = []*TSType{t}
			t = a
			continue
		}
		p.advance()
		restore := p.nestedTypes()
		idx := p.parseType()
		restore()
		p.expect(TokRBracket)
		ix := p.typeNode(pos, TSIndexed)
		ix.Args = []*TSType{t, idx}
		t = ix
	}
	return t
}

func (p *JSParser) parseNonArrayType() *TSType {
	pos := p.curPos()
	switch p.cur.Kind {
	case TokIdent:
		if tsKeywordTypes[p.cur.Val] && p.peek.Kind != TokDot {
			t := p.typeNode(pos, TSKeyword)
			t.Name = p.advance().Val
			t.End = p.end()
			return t
		}
		return p.parseTypeRef()
	case TokVoid, TokNull:
		t := &TSType{Pos: pos, Kind: TSKeyword, Name: p.advance().Val}
		t.End = p.end()
		return t
	case TokThis:
		p.advance()
		return p.typeNode(pos, TSThis)
	case TokTypeof:
		p.advance()
		t := &TSType{Pos: pos, Kind: TSQuery}
		if p.cur.Kind == TokImport {
			t.Args = []*TSType{p.parseImportType(p.curPos())}
			t.End = p.end()
			return t
		}
		npos := p.curPos()
		name, root, rootEnd := p.parseEntityName(true)
		p.recordRef(root, npos, rootEnd, true)
		t.Name = name
		if (p.cur.Kind == TokLt || p.cur.Kind == TokLShift) && !p.cur.NL {
			t.Args = p.parseTypeArgs()
		}
		t.End = p.end()
		return t
	case TokImport:
		return p.parseImportType(pos)
	case TokString, TokNumber, TokTrue, TokFalse:
		t := &TSType{Pos: pos, Kind: TSLiteral, Name: p.advance().Val}
		t.End = p.end()
		return t
	case TokMinus:
		if p.peek.Kind == TokNumber {
			p.advance()
			p.advance()
			t := p.typeNode(pos, TSLiteral)
			t.Name = p.src[pos:t.End]
			return t
		}
	case TokTemplate:
		return p.parseTemplateType()
	case TokLParen:
		p.advance()
		restore := p.nestedTypes()
		inner := p.parseType()
		restore()
		p.expect(TokRParen)
		t := p.typeNode(pos, TSParen)
		t.Args = []*TSType{inner}
		return t
	case TokLBracket:
		return p.parseTupleType()
	case TokLBrace:
		if p.isMappedTypeStart() {
			return p.parseMappedType()
		}
		t := &TSType{Pos: pos, Kind: TSObject}
		t.Members = p.parseTypeMembers()
		t.End = p.endAt(pos)
		return t
	}
	if isBindingKind(p.cur.Kind) {
		return p.parseTypeRef()
	}
	p.report(pos, fmt.Sprintf("type expected but found %s", tokDesc(p.cur)))
	if !isDelimiter(p.cur.Kind) && !isGtLike(p.cur.Kind) && p.cur.Kind != TokRBrace && p.cur.Kind != TokEOF {
		p.advance()
	}
	return &TSType{Pos: pos, End: p.endAt(pos), Kind: TSKeyword, Name: "any"}
}

// parseEntityName parses `a.b.c` (allowThis: may start with `this`) and
// returns the dotted name and the first name with its end.
func (p *JSParser) parseEntityName(allowThis bool) (name, root string, rootEnd int) {
	switch {
	case allowThis && p.cur.Kind == TokThis:
		name = p.advance().Val
	case isBindingKind(p.cur.Kind) || p.cur.Kind == TokYield || p.cur.Kind == TokAwait:
		name = p.advance().Val
	default:
		p.report(p.cur.Pos, fmt.Sprintf("type name expected but found %s", tokDesc(p.cur)))
		return "", "", p.cur.Pos
	}
	root, rootEnd = name, p.end()
	for p.cur.Kind == TokDot {
		p.advance()
		if isIdentName(p.cur.Kind) || p.cur.Kind == TokPrivateName {
			name += "." + p.advance().Val
		} else {
			p.report(p.cur.Pos, fmt.Sprintf("name expected after '.' but found %s", tokDesc(p.cur)))
			break
		}
	}
	return name, root, rootEnd
}

func (p *JSParser) parseTypeRef() *TSType {
	pos := p.curPos()
	name, root, rootEnd := p.parseEntityName(false)
	p.recordRef(root, pos, rootEnd, false)
	t := &TSType{Pos: pos, Kind: TSRefType, Name: name}
	if (p.cur.Kind == TokLt || p.cur.Kind == TokLShift) && !p.cur.NL {
		t.Args = p.parseTypeArgs()
	}
	t.End = p.endAt(pos)
	return t
}

// parseImportType parses `import('m').A.B<T>`.
func (p *JSParser) parseImportType(pos int) *TSType {
	p.advance() // import
	p.expect(TokLParen)
	t := &TSType{Pos: pos, Kind: TSImportType, Name: unquoteStr(p.expect(TokString).Val)}
	if p.eat(TokComma) { // import attributes: import('m', { with: { … } })
		p.parseAssignExpr()
		p.eat(TokComma)
	}
	p.expect(TokRParen)
	for p.cur.Kind == TokDot {
		p.advance()
		if isIdentName(p.cur.Kind) {
			if t.Label != "" {
				t.Label += "."
			}
			t.Label += p.advance().Val
		} else {
			p.report(p.cur.Pos, fmt.Sprintf("name expected after '.' but found %s", tokDesc(p.cur)))
			break
		}
	}
	if (p.cur.Kind == TokLt || p.cur.Kind == TokLShift) && !p.cur.NL {
		t.Args = p.parseTypeArgs()
	}
	t.End = p.endAt(pos)
	return t
}

// parseTypeArgs parses `<A, B>`.
func (p *JSParser) parseTypeArgs() []*TSType {
	pos := p.cur.Pos
	if p.knownToFail(failTypeArgs, pos) {
		return nil
	}
	defer p.rememberFailure(failTypeArgs, pos, len(p.specDiags))
	defer p.typeEntry()()
	p.markTS(pos)
	p.expectLt()
	restore := p.nestedTypes()
	defer restore()
	var args []*TSType
	for !isGtLike(p.cur.Kind) && p.cur.Kind != TokEOF {
		args = append(args, p.parseType())
		if !p.eat(TokComma) {
			break
		}
	}
	if len(args) == 0 {
		p.report(p.cur.Pos, "type argument list cannot be empty")
	}
	p.expectGt()
	return args
}

// parseTypeParams parses `<const T extends C = D, in out U>` and puts the
// names in scope; the caller pops them (popTypeParams) where the scope ends.
func (p *JSParser) parseTypeParams() []*TSTypeParam {
	defer p.typeEntry()()
	p.markTS(p.cur.Pos)
	p.expectLt()
	restore := p.nestedTypes()
	defer restore()
	var out []*TSTypeParam
	for !isGtLike(p.cur.Kind) && p.cur.Kind != TokEOF {
		tp := &TSTypeParam{Pos: p.curPos()}
		for {
			switch {
			case p.cur.Kind == TokConst && isBindingKind(p.peek.Kind):
				tp.Const = true
			case p.cur.Kind == TokIn && isBindingKind(p.peek.Kind):
				tp.In = true
			case p.isWord("out") && isBindingKind(p.peek.Kind):
				tp.Out = true
			default:
				goto name
			}
			p.advance()
		}
	name:
		tp.Name = p.bindingName()
		p.pushTypeParam(tp.Name)
		if p.eat(TokExtends) {
			tp.Constraint = p.parseType()
		}
		if p.eat(TokAssign) {
			tp.Default = p.parseType()
		}
		tp.End = p.endAt(tp.Pos)
		out = append(out, tp)
		if !p.eat(TokComma) {
			break
		}
	}
	if len(out) == 0 {
		p.report(p.cur.Pos, "type parameter list cannot be empty")
	}
	p.expectGt()
	return out
}

func (p *JSParser) parseTupleType() *TSType {
	pos := p.curPos()
	p.advance() // [
	restore := p.nestedTypes()
	defer restore()
	var elems []*TSType
	for p.cur.Kind != TokRBracket && p.cur.Kind != TokEOF {
		epos := p.curPos()
		rest := p.eat(TokEllipsis)
		var el *TSType
		if isIdentName(p.cur.Kind) && (p.peek.Kind == TokColon || (p.peek.Kind == TokQuestion && p.peek2().Kind == TokColon)) {
			label := p.advance().Val
			opt := p.eat(TokQuestion)
			p.expect(TokColon)
			ty := p.parseType()
			el = &TSType{Pos: epos, Kind: TSNamedMember, Name: label, Optional: opt, Rest: rest, Args: []*TSType{ty}}
		} else {
			ty := p.parseType()
			switch {
			case rest:
				el = &TSType{Pos: epos, Kind: TSRestType, Args: []*TSType{ty}}
			case p.cur.Kind == TokQuestion:
				p.advance()
				el = &TSType{Pos: epos, Kind: TSOptionalType, Args: []*TSType{ty}}
			default:
				el = ty
			}
		}
		el.End = p.endAt(epos)
		elems = append(elems, el)
		if !p.eat(TokComma) {
			break
		}
	}
	p.expect(TokRBracket)
	t := p.typeNode(pos, TSTuple)
	t.Args = elems
	return t
}

// parseTemplateType parses a template literal type; its holes are types.
func (p *JSParser) parseTemplateType() *TSType {
	pos := p.curPos()
	raw := p.advance().Val
	end := p.end()
	t := &TSType{Pos: pos, End: end, Kind: TSTemplate, Name: raw}
	for _, h := range p.templateHoles(raw, pos, end) {
		sub := p.subParser(h[0], h[1])
		ty := sub.parseType()
		if sub.cur.Kind != TokEOF {
			sub.report(sub.cur.Pos, fmt.Sprintf("unexpected %s in template literal type", tokDesc(sub.cur)))
		}
		p.mergeSub(sub)
		t.Args = append(t.Args, ty)
	}
	return t
}

// templateHoles returns the [start, end) offsets of the ${…} holes of a
// template literal whose raw text is at [pos, end).
func (p *JSParser) templateHoles(raw string, pos, end int) [][2]int {
	var holes [][2]int
	i := 1
	for i < len(raw) {
		c := raw[i]
		switch {
		case c == '\\' && i+1 < len(raw):
			i += 2
		case c == '`':
			return holes
		case c == '$' && i+1 < len(raw) && raw[i+1] == '{':
			holeStart := pos + i + 2
			hl := &Lexer{src: p.src[:end], pos: holeStart, line: p.lineAt(holeStart)}
			closeAt := hl.scanHole()
			if closeAt < 0 {
				closeAt = end
			}
			holes = append(holes, [2]int{holeStart, closeAt})
			i = closeAt - pos + 1
		default:
			i++
		}
	}
	return holes
}

// subParser parses src[start:end] in place, sharing the TypeScript state.
func (p *JSParser) subParser(start, end int) *JSParser {
	src := p.src[:end]
	p.lineAt(0)
	sub := &JSParser{lex: &Lexer{src: src, pos: start, line: p.lineAt(start)}, src: src, lines: p.lines,
		ts: p.ts, tsInfo: p.tsInfo, firstTS: -1, depth: p.depth + 1, ambient: p.ambient,
		typeParams: append([]string(nil), p.typeParams...)}
	sub.cur = sub.lex.Next()
	sub.peek = sub.lex.Next()
	return sub
}

// mergeSub reports the diagnostics of a sub-parser.
func (p *JSParser) mergeSub(sub *JSParser) {
	for _, d := range sub.Diagnostics {
		p.addDiag(d)
	}
	if sub.firstTS >= 0 {
		p.markTS(sub.firstTS)
	}
}

func (p *JSParser) isMappedTypeStart() bool {
	toks := append([]Token{p.peek}, p.peekTokens(3)...)
	i := 0
	switch {
	case toks[0].Kind == TokPlus || toks[0].Kind == TokMinus:
		return isWordTok(toks[1], "readonly")
	case isWordTok(toks[0], "readonly"):
		i = 1
	}
	return toks[i].Kind == TokLBracket && isIdentName(toks[i+1].Kind) && toks[i+2].Kind == TokIn
}

func (p *JSParser) parseMappedType() *TSType {
	pos := p.curPos()
	p.advance() // {
	restore := p.nestedTypes()
	defer restore()
	t := &TSType{Pos: pos, Kind: TSMapped}
	switch {
	case p.cur.Kind == TokPlus || p.cur.Kind == TokMinus:
		sign := p.advance().Val
		p.expectWord("readonly")
		t.Readonly = sign + "readonly"
	case p.isWord("readonly"):
		p.advance()
		t.Readonly = "readonly"
	}
	p.expect(TokLBracket)
	if isIdentName(p.cur.Kind) {
		t.Name = p.advance().Val
	}
	p.expect(TokIn)
	n := len(p.typeParams)
	constraint := p.parseType()
	p.pushTypeParam(t.Name)
	var as, val *TSType
	if p.eat(TokAs) {
		as = p.parseType()
	}
	p.expect(TokRBracket)
	switch {
	case p.cur.Kind == TokPlus || p.cur.Kind == TokMinus:
		sign := p.advance().Val
		p.expect(TokQuestion)
		t.OptionalMod = sign + "?"
	case p.eat(TokQuestion):
		t.OptionalMod = "?"
	}
	if p.eat(TokColon) {
		val = p.parseType()
	}
	p.popTypeParams(n)
	if !p.eat(TokSemi) {
		p.eat(TokComma)
	}
	p.expect(TokRBrace)
	t.Args = []*TSType{constraint, as, val}
	t.End = p.endAt(pos)
	return t
}

func (p *JSParser) expectWord(w string) {
	if p.isWord(w) {
		p.advance()
		return
	}
	p.report(p.cur.Pos, fmt.Sprintf("expected '%s' but found %s", w, tokDesc(p.cur)))
}

// parseTypeMembers parses the `{ … }` of an object type or an interface.
func (p *JSParser) parseTypeMembers() []*TSMember {
	p.expect(TokLBrace)
	restore := p.nestedTypes()
	defer restore()
	var out []*TSMember
	for p.cur.Kind != TokRBrace && p.cur.Kind != TokEOF {
		start := p.cur
		if m := p.parseTypeMember(); m != nil {
			out = append(out, m)
		}
		if !p.eat(TokSemi) && !p.eat(TokComma) && p.cur.Kind != TokRBrace && !p.cur.NL && p.cur != start {
			p.report(p.cur.Pos, fmt.Sprintf("expected ';' but found %s", tokDesc(p.cur)))
		}
		p.guard(start)
	}
	p.expect(TokRBrace)
	return out
}

// isIndexSignature reports `[name: …` or `[name, …` at the current `[`.
func (p *JSParser) isIndexSignature() bool {
	if !isIdentName(p.peek.Kind) {
		return false
	}
	k := p.peek2().Kind
	return k == TokColon || k == TokComma
}

func (p *JSParser) parseIndexSignature(pos int) *TSMember {
	p.advance() // [
	ppos := p.curPos()
	name := p.advance().Val
	p.expect(TokColon)
	kt := p.parseType()
	p.expect(TokRBracket)
	m := &TSMember{Pos: pos, Kind: TSIndexSig, Sig: &TSSignature{Params: []*TSParam{{Pos: ppos, End: kt.End, Name: name, Type: kt}}}}
	if p.eat(TokColon) {
		m.Type = p.parseType()
	}
	m.End = p.endAt(pos)
	return m
}

func (p *JSParser) parseTypeMember() *TSMember {
	pos := p.curPos()
	m := &TSMember{Pos: pos}
	switch {
	case p.cur.Kind == TokLParen || p.cur.Kind == TokLt:
		m.Kind, m.Sig = TSCallSig, p.parseMemberSig()
		m.End = p.endAt(pos)
		return m
	case p.cur.Kind == TokNew && (p.peek.Kind == TokLParen || p.peek.Kind == TokLt):
		p.advance()
		m.Kind, m.Sig = TSConstructSig, p.parseMemberSig()
		m.End = p.endAt(pos)
		return m
	}
	if p.isWord("readonly") && canFollowModifier(p.peek) {
		p.advance()
		m.Readonly = true
	}
	if p.cur.Kind == TokLBracket && p.isIndexSignature() {
		im := p.parseIndexSignature(pos)
		im.Readonly = m.Readonly
		return im
	}
	accessor := TSMethodSig
	if (p.isWord("get") || p.isWord("set")) && canFollowModifier(p.peek) {
		accessor = TSGetSig
		if p.advance().Val == "set" {
			accessor = TSSetSig
		}
	}
	p.parseTypeMemberName(m)
	m.Optional = p.eat(TokQuestion)
	if p.cur.Kind == TokLParen || p.cur.Kind == TokLt {
		m.Kind, m.Sig = accessor, p.parseMemberSig()
	} else {
		if accessor != TSMethodSig {
			p.report(p.cur.Pos, fmt.Sprintf("'(' expected after accessor name but found %s", tokDesc(p.cur)))
		}
		m.Kind = TSPropertySig
		if p.eat(TokColon) {
			m.Type = p.parseType()
		}
	}
	m.End = p.endAt(pos)
	return m
}

// parseTypeMemberName parses the name of a type member into m.
func (p *JSParser) parseTypeMemberName(m *TSMember) {
	switch {
	case p.cur.Kind == TokString:
		m.Name = unquoteStr(p.advance().Val)
	case p.cur.Kind == TokNumber:
		m.Name = p.advance().Val
	case p.cur.Kind == TokLBracket:
		p.advance()
		epos := p.curPos()
		restore := p.allowIn()
		p.parseAssignExpr()
		restore()
		m.Name, m.Computed = p.src[epos:p.endAt(epos)], true
		p.expect(TokRBracket)
	case isIdentName(p.cur.Kind):
		m.Name = p.advance().Val
	default:
		p.report(p.cur.Pos, fmt.Sprintf("property name expected but found %s", tokDesc(p.cur)))
		if !isDelimiter(p.cur.Kind) && p.cur.Kind != TokRBrace {
			p.advance()
		}
	}
}

// parseMemberSig parses `<T>(params): R` of a method, call or construct
// signature.
func (p *JSParser) parseMemberSig() *TSSignature {
	n := len(p.typeParams)
	defer p.popTypeParams(n)
	sig := &TSSignature{}
	if p.cur.Kind == TokLt || p.cur.Kind == TokLShift {
		sig.TypeParams = p.parseTypeParams()
	}
	sig.Params, sig.This = p.parseSigParams()
	if p.eat(TokColon) {
		sig.Return = p.parseReturnType()
	}
	return sig
}

// parseSigParams parses the parameter list of a signature without a body;
// an explicit `this` parameter is returned apart.
func (p *JSParser) parseSigParams() ([]*TSParam, *TSType) {
	p.expect(TokLParen)
	restore := p.nestedTypes()
	defer restore()
	var params []*TSParam
	var this *TSType
	for p.cur.Kind != TokRParen && p.cur.Kind != TokEOF {
		pp := &TSParam{Pos: p.curPos()}
		p.skipParamModifiers(false)
		pp.Rest = p.eat(TokEllipsis)
		isThis := false
		switch p.cur.Kind {
		case TokThis:
			p.advance()
			isThis = true
		case TokLBracket, TokLBrace:
			p.parseBindingPattern()
		default:
			if id, ok := p.parseBindingPattern().(*JsIdent); ok {
				pp.Name = id.Name
			}
		}
		pp.Optional = p.eat(TokQuestion)
		if p.eat(TokColon) {
			pp.Type = p.parseType()
		}
		if p.cur.Kind == TokAssign {
			p.report(p.cur.Pos, "a parameter initializer is only allowed in a function implementation")
			p.advance()
			p.parseAssignExpr()
		}
		pp.End = p.endAt(pp.Pos)
		if isThis && this == nil && len(params) == 0 && !pp.Rest {
			this = pp.Type
		} else {
			if isThis {
				pp.Name = "this"
			}
			params = append(params, pp)
		}
		if !p.eat(TokComma) {
			break
		}
	}
	p.expect(TokRParen)
	return params, this
}

// skipParamModifiers consumes the modifiers of a parameter property
// (`private x`, `readonly y`), which generate code: in a function
// implementation they are VELA-TS-NON-ERASABLE.
func (p *JSParser) skipParamModifiers(report bool) {
	reported := false
	for p.cur.Kind == TokIdent && !p.peek.NL {
		switch p.cur.Val {
		case "public", "private", "protected", "readonly", "override":
		default:
			return
		}
		k := p.peek.Kind
		if !(isBindingKind(k) || k == TokLBracket || k == TokLBrace || k == TokYield || k == TokAwait || k == TokThis) {
			return
		}
		if report && !reported {
			p.reportCode(p.cur.Pos, DiagTSNonErasable,
				"parameter properties generate code: declare the field in the class and assign it in the constructor")
			reported = true
		}
		p.markTS(p.cur.Pos)
		p.blankCur()
		p.advance()
	}
}

// parseReturnType parses a return type, which may be a type predicate
// (`x is T`, `asserts x is T`, `asserts this`).
func (p *JSParser) parseReturnType() *TSType {
	defer p.typeEntry()()
	pos := p.curPos()
	p.markTS(pos)
	// A return type may be a conditional type even inside the `extends`
	// clause of one (TypeScript's parseReturnType: allowConditionalTypesAnd).
	restore := p.nestedTypes()
	defer restore()
	if p.isWord("asserts") && !p.peek.NL && (isBindingKind(p.peek.Kind) || p.peek.Kind == TokThis) {
		p.advance()
		t := &TSType{Pos: pos, Kind: TSPredicate, Asserts: true, Name: p.advance().Val}
		if p.isWord("is") && !p.cur.NL {
			p.advance()
			t.Args = []*TSType{p.parseType()}
		}
		t.End = p.endAt(pos)
		return t
	}
	if (isBindingKind(p.cur.Kind) || p.cur.Kind == TokThis) && isWordTok(p.peek, "is") && !p.peek.NL {
		t := &TSType{Pos: pos, Kind: TSPredicate, Name: p.advance().Val}
		p.advance() // is
		t.Args = []*TSType{p.parseType()}
		t.End = p.endAt(pos)
		return t
	}
	return p.parseType()
}

// ──────────────────────────────────────────────
//  Expressions
// ──────────────────────────────────────────────

// isStartOfExpression reports whether the current token can begin an
// expression (for the rule after type arguments in an expression).
func (p *JSParser) isStartOfExpression() bool {
	switch p.cur.Kind {
	case TokIdent, TokNumber, TokString, TokTemplate, TokRegex, TokPrivateName,
		TokLParen, TokLBracket, TokLBrace, TokFunction, TokClass, TokNew,
		TokSlash, TokSlashAssign, TokPlus, TokMinus, TokBang, TokTilde, TokPlusPlus, TokMinusMinus,
		TokTypeof, TokVoid, TokDelete, TokAwait, TokYield, TokThis, TokSuper, TokNull,
		TokTrue, TokFalse, TokImport, TokLt:
		return true
	}
	return isContextualIdent(p.cur.Kind)
}

// canFollowTypeArgs decides whether `<…>` just parsed after an expression
// are type arguments (TypeScript 4.7 rule): before `(` or a template they
// are; before `<`, `>`, `+`, `-` they are not; otherwise they are when the
// next token cannot continue an expression.
func (p *JSParser) canFollowTypeArgs() bool {
	switch p.cur.Kind {
	case TokLParen, TokTemplate:
		return true
	case TokLt, TokGt, TokPlus, TokMinus:
		return false
	}
	if _, isBinary := binOpTable[p.cur.Kind]; isBinary || p.cur.NL || p.cur.Kind == TokAs || p.isWord("satisfies") {
		return true
	}
	return !p.isStartOfExpression()
}

// tryTypeArgsInExpr parses type arguments after expr when they are type
// arguments (canFollowTypeArgs), and returns the call or tagged template
// they introduce, or expr itself for an instantiation expression.
func (p *JSParser) tryTypeArgsInExpr(start int, expr JsExpr) (JsExpr, bool) {
	ok := p.try(func() bool {
		p.parseTypeArgs()
		p.rescanRegexAsDivision()
		return p.canFollowTypeArgs()
	})
	if !ok {
		return expr, false
	}
	switch p.cur.Kind {
	case TokLParen:
		args := p.parseArgs()
		return &JsCallExpr{BaseExpr: BaseExpr{BaseNode{Pos: start, End: p.endAt(start)}}, Callee: expr, Args: args}, true
	case TokTemplate:
		return p.parseTaggedTemplate(start, expr), true
	}
	return expr, true
}

// tryTypedArrow parses an arrow function whose head needs TypeScript:
// generic `<T>(a: T) => …` (generic) or `(a): R => …`. It returns nil when
// the tokens are not such an arrow; nothing is consumed then.
func (p *JSParser) tryTypedArrow(start int, async, generic bool) JsExpr {
	st := p.save()
	n := len(p.typeParams)
	var params []JsExpr
	var sig *TSSignature
	if !p.try(func() bool {
		var tps []*TSTypeParam
		if generic {
			tpStart := p.cur.Pos
			tps = p.parseTypeParams()
			if p.cur.Kind == TokLParen && hasLineBreak(p.src[tpStart:p.cur.Pos]) {
				// `return <T>\n(x: T) => x`: the `(` takes the place of `<`.
				p.blankMove(p.cur.Pos, tpStart)
			}
		}
		if p.cur.Kind != TokLParen {
			return false
		}
		params, sig = p.parseParams()
		sig.TypeParams = tps
		closeParen := p.prevEnd - 1
		if p.eatTypeColon() {
			sig.Return = p.parseReturnType()
			if p.cur.Kind == TokArrow && hasLineBreak(p.src[closeParen:p.cur.Pos]) {
				// `(a):\n T => a`: the `)` moves after the type, next to `=>`.
				p.blankMove(closeParen, p.prevEnd-1)
			}
		} else if !generic {
			return false
		}
		return p.cur.Kind == TokArrow && !p.cur.NL
	}) {
		return nil
	}
	var body JsNode
	if p.condThen && sig.Return != nil {
		// `a ? (b): c => d : e` — the arrow interpretation holds only when
		// the conditional's `:` follows the arrow.
		if !p.try(func() bool {
			p.advance() // =>
			body = p.parseArrowBody()
			return p.cur.Kind == TokColon
		}) {
			p.restore(st)
			return nil
		}
	} else {
		p.advance() // =>
		body = p.parseArrowBody()
	}
	p.popTypeParams(n)
	p.markTS(start)
	return &JsArrowFunc{
		BaseExpr: BaseExpr{BaseNode{Pos: start, End: p.endAt(start)}},
		Params:   params, Body: body, Async: async, TS: sig,
	}
}

// ──────────────────────────────────────────────
//  Statements
// ──────────────────────────────────────────────

// parseTSStmt parses a statement that starts with TypeScript syntax; ok is
// false when the current token does not start one.
func (p *JSParser) parseTSStmt() (JsStmt, bool) {
	pos := p.curPos()
	sameLineName := !p.peek.NL && isBindingKind(p.peek.Kind)
	switch {
	case p.isWord("type") && sameLineName:
		return p.parseTypeAlias(pos, false), true
	case p.isWord("interface") && sameLineName:
		return p.parseInterface(pos, false), true
	case p.isWord("declare") && !p.peek.NL && p.declarationFollows(p.peek):
		return p.parseDeclare(pos), true
	case p.isWord("abstract") && p.peek.Kind == TokClass && !p.peek.NL:
		p.markTS(pos)
		p.blankCur()
		p.advance()
		p.abstractClass = true
		c := p.parseClassDecl(true)
		c.Pos = pos
		return c, true
	case (p.isWord("namespace") || p.isWord("module")) && !p.peek.NL &&
		(isBindingKind(p.peek.Kind) || (p.isWord("module") && p.peek.Kind == TokString)):
		return p.parseNamespace(pos, false), true
	case p.isWord("global") && p.ambient > 0 && p.peek.Kind == TokLBrace:
		return p.parseNamespace(pos, true), true
	case p.isWord("enum") && sameLineName:
		return p.parseEnum(pos), true
	case p.cur.Kind == TokConst && isWordTok(p.peek, "enum") && !p.peek.NL:
		p.advance()
		return p.parseEnum(pos), true
	}
	return nil, false
}

// declarationFollows reports whether t starts a declaration after
// `declare`.
func (p *JSParser) declarationFollows(t Token) bool {
	switch t.Kind {
	case TokVar, TokLet, TokConst, TokFunction, TokClass, TokAsync:
		return true
	case TokIdent:
		switch t.Val {
		case "type", "interface", "enum", "namespace", "module", "global", "abstract":
			return true
		}
	}
	return false
}

func (p *JSParser) parseTypeAlias(pos int, declare bool) JsStmt {
	p.markTS(pos)
	p.advance() // type
	d := &TSDecl{Pos: pos, Kind: TSDeclTypeAlias, Declare: declare}
	d.Name = p.bindingName()
	n := len(p.typeParams)
	if p.cur.Kind == TokLt || p.cur.Kind == TokLShift {
		d.TypeParams = p.parseTypeParams()
	}
	p.expect(TokAssign)
	d.Type = p.parseType()
	p.popTypeParams(n)
	p.eatSemi()
	d.End = p.endAt(pos)
	p.addDecl(d)
	return p.erasedStmt(pos)
}

func (p *JSParser) parseInterface(pos int, declare bool) JsStmt {
	p.markTS(pos)
	p.advance() // interface
	d := &TSDecl{Pos: pos, Kind: TSDeclInterface, Declare: declare}
	d.Name = p.bindingName()
	n := len(p.typeParams)
	if p.cur.Kind == TokLt || p.cur.Kind == TokLShift {
		d.TypeParams = p.parseTypeParams()
	}
	if p.eat(TokExtends) {
		for {
			d.Extends = append(d.Extends, p.parseTypeRef())
			if !p.eat(TokComma) {
				break
			}
		}
	}
	bpos := p.curPos()
	members := p.parseTypeMembers()
	d.Type = &TSType{Pos: bpos, End: p.endAt(bpos), Kind: TSObject, Members: members}
	p.popTypeParams(n)
	d.End = p.endAt(pos)
	p.addDecl(d)
	return p.erasedStmt(pos)
}

// parseDeclare parses `declare …`: an ambient declaration, erased.
func (p *JSParser) parseDeclare(pos int) JsStmt {
	p.markTS(pos)
	p.advance() // declare
	p.ambient++
	defer func() { p.ambient-- }()
	switch {
	case p.cur.Kind == TokVar || p.cur.Kind == TokLet || p.cur.Kind == TokConst && !isWordTok(p.peek, "enum"):
		v := p.parseVarDecl(true, false)
		for _, decl := range v.Decls {
			d := &TSDecl{Pos: pos, End: p.endAt(pos), Kind: TSDeclVar, Declare: true, Type: decl.Type}
			if id, ok := decl.Id.(*JsIdent); ok {
				d.Name = id.Name
			}
			p.addDecl(d)
		}
	case p.cur.Kind == TokFunction || p.cur.Kind == TokAsync:
		p.parseStmt() // recorded by parseFuncDecl
		if d := p.lastDecl(); d != nil && d.Kind == TSDeclFunction {
			d.Pos, d.Declare = pos, true
		}
	case p.cur.Kind == TokClass || p.isWord("abstract"):
		if p.isWord("abstract") {
			p.advance()
		}
		c := p.parseClassDecl(true)
		p.addDecl(&TSDecl{Pos: pos, End: p.endAt(pos), Kind: TSDeclClass, Name: c.Name, Declare: true})
	case p.isWord("enum") || p.cur.Kind == TokConst:
		if p.cur.Kind == TokConst {
			p.advance()
		}
		p.parseEnum(pos)
	case p.isWord("namespace") || p.isWord("module") || p.isWord("global"):
		p.parseNamespace(pos, true)
	case p.isWord("type"):
		p.parseTypeAlias(pos, true)
	case p.isWord("interface"):
		p.parseInterface(pos, true)
	default:
		p.report(p.cur.Pos, fmt.Sprintf("declaration expected after 'declare' but found %s", tokDesc(p.cur)))
	}
	return p.erasedStmt(pos)
}

// parseNamespace parses `namespace A.B { … }`, `module 'm' { … }` and
// `global { … }`. A namespace that is not ambient and holds values
// generates code: VELA-TS-NON-ERASABLE.
func (p *JSParser) parseNamespace(pos int, declare bool) JsStmt {
	p.markTS(pos)
	kw := p.advance().Val
	d := &TSDecl{Pos: pos, Kind: TSDeclNamespace, Declare: declare}
	switch {
	case kw == "global":
		d.Kind, d.Name = TSDeclModule, "global"
	case p.cur.Kind == TokString:
		d.Kind, d.Name = TSDeclModule, unquoteStr(p.advance().Val)
		if !declare && p.ambient == 0 {
			p.report(pos, "a module with a quoted name must be declared with 'declare'")
		}
	default:
		d.Name, _, _ = p.parseEntityName(false)
	}
	ambient := declare || p.ambient > 0
	if ambient {
		p.ambient++
	}
	values := false
	if p.cur.Kind == TokLBrace {
		p.advance()
		p.depth++
		for p.cur.Kind != TokRBrace && p.cur.Kind != TokEOF {
			start := p.cur
			s := p.parseStmt()
			if s != nil && !p.isErased(s) {
				if _, empty := s.(*JsEmptyStmt); !empty {
					values = true
				}
			}
			p.guard(start)
		}
		p.depth--
		p.expect(TokRBrace)
	} else {
		p.eatSemi() // declare module 'm';
	}
	if ambient {
		p.ambient--
	}
	if values && !ambient {
		p.reportCode(pos, DiagTSNonErasable, "a namespace with values generates code: use a module instead")
	}
	d.End = p.endAt(pos)
	p.addDecl(d)
	return p.erasedStmt(pos)
}

// parseEnum parses `enum E { … }` (the cursor on `enum`; pos at `const` for
// `const enum`). Outside an ambient context it is VELA-TS-NON-ERASABLE.
func (p *JSParser) parseEnum(pos int) JsStmt {
	p.markTS(pos)
	p.advance() // enum
	name := p.bindingName()
	p.expect(TokLBrace)
	restore := p.allowIn()
	for p.cur.Kind != TokRBrace && p.cur.Kind != TokEOF {
		start := p.cur
		switch {
		case p.cur.Kind == TokString || isIdentName(p.cur.Kind) || p.cur.Kind == TokNumber:
			p.advance()
		case p.cur.Kind == TokLBracket:
			p.advance()
			p.parseAssignExpr()
			p.expect(TokRBracket)
		default:
			p.report(p.cur.Pos, fmt.Sprintf("enum member name expected but found %s", tokDesc(p.cur)))
		}
		if p.eat(TokAssign) {
			p.parseAssignExpr()
		}
		p.guard(start)
		if !p.eat(TokComma) {
			break
		}
	}
	restore()
	p.expect(TokRBrace)
	if p.ambient > 0 {
		p.addDecl(&TSDecl{Pos: pos, End: p.endAt(pos), Kind: TSDeclEnum, Name: name, Declare: true})
	} else {
		p.reportCode(pos, DiagTSNonErasable,
			"enums generate code: use an object `as const` and `type "+name+" = (typeof "+name+")[keyof typeof "+name+"]`")
	}
	return p.erasedStmt(pos)
}

// typeImportFollows reports whether the `type` at the cursor (after
// `import` or `export`) makes the declaration type-only, as opposed to
// naming a binding `type`.
func (p *JSParser) typeImportFollows() bool {
	switch p.peek.Kind {
	case TokLBrace, TokStar:
		return true
	case TokFrom:
		// `import type from 'm'` imports a default named `type`;
		// `import type from from 'm'` is a type-only import of `from`.
		return p.peek2().Kind == TokFrom
	}
	return isBindingKind(p.peek.Kind)
}

// parseSpecifier parses one specifier of an import or export list with
// TypeScript's inline `type` modifier. For an import, first is the
// imported name and second the local; for an export, first is the local
// and second the exported name.
func (p *JSParser) parseSpecifier(isImport bool) (first, second string, typeOnly bool) {
	nameAfterAs := func() string {
		if isImport {
			return p.bindingName()
		}
		return p.moduleExportName()
	}
	first = p.moduleExportName()
	if p.ts && first == "type" && p.prevTokWasIdent() {
		switch {
		case p.cur.Kind == TokAs:
			p.advance() // first `as`
			switch {
			case p.cur.Kind == TokAs:
				p.advance() // second `as`
				if isIdentName(p.cur.Kind) || p.cur.Kind == TokString {
					// { type as as x }: type-only `as`, renamed x
					return "as", nameAfterAs(), true
				}
				// { type as as }: `type` renamed `as`
				return "type", "as", false
			case isIdentName(p.cur.Kind) || p.cur.Kind == TokString:
				// { type as x }: `type` renamed x
				return "type", nameAfterAs(), false
			}
			// { type as }: type-only `as`
			return "as", "as", true
		case isIdentName(p.cur.Kind) || p.cur.Kind == TokString:
			typeOnly = true
			first = p.moduleExportName()
		}
	}
	second = first
	if p.eat(TokAs) {
		second = nameAfterAs()
	}
	return first, second, typeOnly
}

// prevTokWasIdent reports whether the token just consumed was a plain
// identifier (not a string literal spelling "type").
func (p *JSParser) prevTokWasIdent() bool {
	return p.prevEnd >= 4 && p.src[p.prevEnd-4:p.prevEnd] == "type" &&
		(p.prevEnd-5 < 0 || (p.src[p.prevEnd-5] != '"' && p.src[p.prevEnd-5] != '\''))
}

// parseImportTS parses the TypeScript forms of an import declaration after
// `import`: `import type …` and `import x = …`. ok is false for an
// ordinary import, which the caller parses.
func (p *JSParser) parseImportTS(pos int) (JsStmt, bool) {
	typeOnly := false
	if p.isWord("type") && p.typeImportFollows() {
		p.markTS(p.cur.Pos)
		p.advance()
		typeOnly = true
	}
	if isBindingKind(p.cur.Kind) && p.peek.Kind == TokAssign {
		p.markTS(pos)
		if p.ambient == 0 {
			p.reportCode(pos, DiagTSNonErasable, "'import x = …' generates code: use an ES import")
		}
		p.advance()
		p.advance()
		p.parseLeftHandSide()
		p.eatSemi()
		return p.erasedStmt(pos), true
	}
	if !typeOnly {
		return nil, false
	}
	d := &JsImportDecl{}
	p.parseImportClause(d)
	p.expect(TokFrom)
	d.Source = unquoteStr(p.expect(TokString).Val)
	p.parseImportAttributes()
	p.eatSemi()
	if d.DefaultName != "" && (d.Namespace != "" || len(d.Specifiers) > 0) {
		p.report(pos, "a type-only import can specify a default import or named bindings, but not both")
	}
	add := func(local, imported string) {
		if p.tsInfo != nil {
			p.tsInfo.Imports = append(p.tsInfo.Imports, TSTypeImport{Pos: pos, End: p.endAt(pos),
				Local: local, Imported: imported, Source: d.Source, Statement: true})
		}
	}
	if d.DefaultName != "" {
		add(d.DefaultName, "default")
	}
	if d.Namespace != "" {
		add(d.Namespace, "*")
	}
	for _, s := range d.Specifiers {
		add(s.Local, s.Imported)
	}
	return p.erasedStmt(pos), true
}

// parseExportTS parses the TypeScript forms of an export declaration after
// `export`. ok is false for an ordinary export.
func (p *JSParser) parseExportTS(pos int) (JsStmt, bool) {
	switch {
	case p.cur.Kind == TokAssign: // export = x (fine in an ambient module)
		p.markTS(pos)
		if p.ambient == 0 {
			p.reportCode(pos, DiagTSNonErasable, "'export =' generates code: use 'export default'")
		}
		p.advance()
		p.parseAssignExpr()
		p.eatSemi()
		return p.erasedStmt(pos), true
	case p.cur.Kind == TokImport && isBindingKind(p.peek.Kind) && p.peek2().Kind == TokAssign:
		p.markTS(pos)
		if p.ambient == 0 {
			p.reportCode(pos, DiagTSNonErasable, "'export import x = …' generates code: use an ES import and export")
		}
		p.advance()
		p.advance()
		p.advance()
		p.parseLeftHandSide()
		p.eatSemi()
		return p.erasedStmt(pos), true
	case p.cur.Kind == TokAs && isWordTok(p.peek, "namespace"): // export as namespace N
		p.markTS(pos)
		p.advance()
		p.advance()
		d := &TSDecl{Pos: pos, Kind: TSDeclExport, Name: p.bindingName(), Exported: true}
		p.eatSemi()
		d.End = p.endAt(pos)
		p.addDecl(d)
		return p.erasedStmt(pos), true
	case p.isWord("type") && (p.peek.Kind == TokLBrace || p.peek.Kind == TokStar):
		p.markTS(p.cur.Pos)
		p.advance() // type
		d := &TSDecl{Pos: pos, Kind: TSDeclExport, Exported: true}
		if p.eat(TokStar) {
			if p.eat(TokAs) {
				d.Names = []TSExportSpec{{Local: "*", Exported: p.moduleExportName()}}
			} else {
				d.Names = []TSExportSpec{{Local: "*", Exported: "*"}}
			}
			p.expect(TokFrom)
			d.Source = unquoteStr(p.expect(TokString).Val)
		} else {
			p.advance() // {
			for p.cur.Kind != TokRBrace && p.cur.Kind != TokEOF {
				local, exported, _ := p.parseSpecifier(false)
				d.Names = append(d.Names, TSExportSpec{Local: local, Exported: exported})
				if !p.eat(TokComma) {
					break
				}
			}
			p.expect(TokRBrace)
			if p.eat(TokFrom) {
				d.Source = unquoteStr(p.expect(TokString).Val)
			}
		}
		p.parseImportAttributes()
		p.eatSemi()
		d.End = p.endAt(pos)
		p.addDecl(d)
		return p.erasedStmt(pos), true
	case p.cur.Kind == TokDefault && (isWordTok(p.peek, "interface") || (isWordTok(p.peek, "abstract") && p.peek2().Kind == TokClass)):
		p.advance() // default
		s := p.parseStmt()
		if p.isErased(s) {
			if d := p.lastDecl(); d != nil {
				d.Exported, d.Default, d.Pos = true, true, pos
			}
			return p.erasedStmt(pos), true
		}
		return &JsExportDecl{BaseStmt: BaseStmt{BaseNode{Pos: pos, End: p.endAt(pos)}}, Default: true, Declaration: s}, true
	}
	if p.cur.Kind == TokIdent || (p.cur.Kind == TokConst && isWordTok(p.peek, "enum")) {
		if s, ok := p.parseTSStmt(); ok {
			if p.isErased(s) {
				if d := p.lastDecl(); d != nil && d.Pos >= pos {
					d.Exported, d.Pos = true, pos
				}
				return p.erasedStmt(pos), true
			}
			return &JsExportDecl{BaseStmt: BaseStmt{BaseNode{Pos: pos, End: p.endAt(pos)}}, Declaration: s}, true
		}
	}
	return nil, false
}

// ──────────────────────────────────────────────
//  Classes
// ──────────────────────────────────────────────

// parseClassHeritageTS parses the type arguments of the superclass and the
// `implements` clause.
func (p *JSParser) parseClassHeritageTS(info *TSClass, hasSuper bool) {
	if hasSuper && (p.cur.Kind == TokLt || p.cur.Kind == TokLShift) {
		info.SuperTypeArgs = p.parseTypeArgs()
	}
	if p.isWord("implements") {
		start := p.cur.Pos
		p.markTS(start)
		p.advance()
		for {
			info.Implements = append(info.Implements, p.parseTypeRef())
			if !p.eat(TokComma) {
				break
			}
		}
		p.blankFrom(start, TSErasedSyntax)
	}
}

// parseClassMemberTS is parseClassMember for TypeScript: modifiers, index
// signatures, optional and definite members, annotations, overloads and
// abstract or declared members (which have no JavaScript and are dropped).
func (p *JSParser) parseClassMemberTS() *JsClassMember {
	pos := p.curPos()
	if p.eat(TokSemi) {
		return nil
	}
	m := &JsClassMember{Kind: "method"}
	info := &TSClassMember{}
	abstract, declare := false, false

	nameFollows := func() bool {
		switch p.peek.Kind {
		case TokLParen, TokAssign, TokSemi, TokRBrace, TokEOF, TokColon, TokQuestion, TokBang, TokLt:
			return true
		}
		return false
	}

	for {
		if p.cur.Kind == TokStatic && !nameFollows() && !m.Static {
			p.advance()
			m.Static = true
			if p.cur.Kind == TokLBrace { // static { … }
				m.Kind = "static-block"
				m.Body = p.parseBlock()
				m.Pos, m.End = pos, p.end()
				return m
			}
			continue
		}
		if p.cur.Kind == TokIdent && canFollowModifier(p.peek) {
			switch p.cur.Val {
			case "public", "private", "protected":
				info.Access = p.cur.Val
			case "readonly":
				info.Readonly = true
			case "override":
				info.Override = true
			case "abstract":
				abstract = true
			case "declare":
				declare = true
			default:
				goto member
			}
			p.markTS(p.cur.Pos)
			p.blankCur()
			p.advance()
			continue
		}
		break
	}
member:
	if p.cur.Kind == TokLBracket && p.isIndexSignature() {
		p.markTS(pos)
		p.parseIndexSignature(pos)
		p.eatSemi()
		p.blankFrom(pos, TSErasedStatement)
		return nil
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
	if p.cur.Kind == TokQuestion {
		p.markTS(p.cur.Pos)
		p.blankCur()
		p.advance()
		info.Optional = true
	} else if p.cur.Kind == TokBang && !p.cur.NL {
		p.markTS(p.cur.Pos)
		p.blankCur()
		p.advance()
		info.Definite = true
	}

	if p.cur.Kind == TokLParen || p.cur.Kind == TokLt || p.cur.Kind == TokLShift {
		fpos := p.curPos()
		fn, hasBody := p.parseFunctionRest(fpos, isAsync, isGen, true)
		if !hasBody {
			// An overload, an abstract method or a method of a declared
			// class: no JavaScript.
			p.eatSemi()
			p.blankFrom(pos, TSErasedStatement)
			return nil
		}
		if abstract {
			p.report(pos, "an abstract method cannot have an implementation")
		}
		m.Value = fn
	} else {
		if hasModifier {
			p.report(p.cur.Pos, fmt.Sprintf("'(' expected after method name but found %s", tokDesc(p.cur)))
		}
		m.Kind = "field"
		if p.eatTypeColon() {
			info.Type = p.parseType()
		}
		if p.eat(TokAssign) {
			m.Value = p.parseAssignExpr()
		}
		p.eatSemi()
		if declare || abstract {
			// `declare x: T` and abstract properties declare no field.
			p.blankFrom(pos, TSErasedStatement)
			return nil
		}
	}
	m.TS = info
	m.Pos, m.End = pos, p.endAt(pos)
	return m
}

// parseFunctionRest parses `<T>(params): R { body }` of a method or
// function expression starting at fpos. Without a body (allowed only when
// noBody is set: overloads and ambient members) it returns nil, false.
func (p *JSParser) parseFunctionRest(fpos int, async, gen, noBody bool) (*JsFuncExpr, bool) {
	n := len(p.typeParams)
	defer p.popTypeParams(n)
	var tps []*TSTypeParam
	if p.ts && (p.cur.Kind == TokLt || p.cur.Kind == TokLShift) {
		tps = p.parseTypeParams()
	}
	params, sig := p.parseParams()
	if p.ts {
		sig.TypeParams = tps
		if p.eatTypeColon() {
			sig.Return = p.parseReturnType()
		}
		if noBody && p.cur.Kind != TokLBrace {
			p.lastSig = sig
			return nil, false
		}
		if p.ambient > 0 {
			p.report(p.cur.Pos, "an implementation cannot be declared in an ambient context")
		}
	}
	body := p.parseBlock()
	return &JsFuncExpr{
		BaseExpr: BaseExpr{BaseNode{Pos: fpos, End: p.endAt(fpos)}},
		Params:   params, Body: body, Async: async, Generator: gen, TS: sig,
	}, true
}

// parseFuncDeclTS parses the rest of a function declaration after its
// name. Without a body it is an overload signature or an ambient function:
// erased and recorded.
func (p *JSParser) parseFuncDeclTS(pos int, name string, async, gen bool) *JsFuncDecl {
	fn, hasBody := p.parseFunctionRest(p.curPos(), async, gen, true)
	if !hasBody {
		p.eatSemi()
		fd := &JsFuncDecl{BaseStmt: BaseStmt{BaseNode{Pos: pos, End: p.endAt(pos)}}, Name: name, Async: async, Generator: gen,
			Body: &JsBlock{BaseStmt: BaseStmt{BaseNode{Pos: pos, End: p.endAt(pos)}}}}
		p.markTS(pos)
		p.blankFrom(pos, TSErasedStatement)
		p.addDecl(&TSDecl{Pos: pos, End: p.endAt(pos), Kind: TSDeclFunction, Name: name, Declare: p.ambient > 0, Sig: p.lastSig})
		p.erase(fd)
		return fd
	}
	return &JsFuncDecl{
		BaseStmt: BaseStmt{BaseNode{Pos: pos, End: p.endAt(pos)}},
		Name:     name, Params: fn.Params, Body: fn.Body,
		Async: async, Generator: gen, TS: fn.TS,
	}
}

// recordTypeSpecifiers lists the `type` specifiers of an import, which
// parseImportNamed left out of the declaration.
func (p *JSParser) recordTypeSpecifiers(d *JsImportDecl, pos int) {
	for _, s := range p.typeSpecs {
		p.tsInfo.Imports = append(p.tsInfo.Imports, TSTypeImport{Pos: pos, End: d.End, Local: s.Local, Imported: s.Imported, Source: d.Source})
	}
	p.typeSpecs = p.typeSpecs[:0]
}

// ──────────────────────────────────────────────
//  Helpers for callers
// ──────────────────────────────────────────────

// stmtListAppend appends s to a statement list unless it was erased.
func (p *JSParser) stmtListAppend(list []JsStmt, s JsStmt) []JsStmt {
	if s == nil || p.isErased(s) {
		return list
	}
	return append(list, s)
}
