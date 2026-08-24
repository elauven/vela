package main

import (
	"fmt"
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

// ParseJSWithDiagnostics parses JavaScript once and returns both its AST and
// structured recoverable parser diagnostics.
func ParseJSWithDiagnostics(src string) (*JsProgram, []JSParseDiagnostic) {
	p := &JSParser{lex: NewLexer(src), src: src}
	p.cur = p.lex.Next()
	p.peek = p.lex.Next()
	p.prevEnd = 0
	return p.parseProgram(), p.Diagnostics
}

// parseJSExpr parses src as a single JavaScript expression and
// returns the expression node.  Used for re-parsing the ${...}
// interpolations of a template literal into structured sub-nodes.
// Returns nil if src is empty or unparseable.
func parseJSExpr(src string) JsExpr {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	p := &JSParser{lex: NewLexer(src), src: src}
	p.cur = p.lex.Next()
	p.peek = p.lex.Next()
	p.prevEnd = 0
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
//  Token helpers
// ──────────────────────────────────────────────

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

func (p *JSParser) expect(k TokKind) Token {
	if p.cur.Kind != k {
		message := fmt.Sprintf("line %d: expected token %d, got %q", p.cur.Line, k, p.cur.Val)
		p.Errors = append(p.Errors, message)
		p.Diagnostics = append(p.Diagnostics, JSParseDiagnostic{Pos: p.cur.Pos, Message: message})
		// error recovery: return zero token so parsing can continue
		return Token{Kind: k, Pos: p.cur.Pos, End: p.cur.Pos}
	}
	return p.advance()
}

func (p *JSParser) eatSemi() {
	p.eat(TokSemi)
	// ASI: no explicit semicolon needed between most statements
}

// curIdent returns the current token's value when it's an identifier or
// a contextual keyword acting as an identifier.
func (p *JSParser) curIdent() (string, bool) {
	if p.cur.Kind == TokIdent {
		return p.cur.Val, true
	}
	if isContextualIdent(p.cur.Kind) {
		return p.cur.Val, true
	}
	return "", false
}

func (p *JSParser) curPos() int { return p.cur.Pos }

// end returns p.prevEnd — the end position of the last consumed token.
func (p *JSParser) end() int { return p.prevEnd }

// ──────────────────────────────────────────────
//  Program
// ──────────────────────────────────────────────

func (p *JSParser) parseProgram() *JsProgram {
	prog := &JsProgram{baseNode: baseNode{Pos: 0}}
	for p.cur.Kind != TokEOF {
		s := p.parseStmt()
		if s != nil {
			prog.Body = append(prog.Body, s)
		}
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
		return p.parseImport()
	case TokExport:
		return p.parseExport()
	case TokFunction:
		return p.parseFuncDecl(false)
	case TokAsync:
		if p.peek.Kind == TokFunction {
			pos := p.curPos() // capture position of 'async' keyword
			p.advance()       // consume 'async'
			fd := p.parseFuncDecl(true)
			// Extend the node's start to include 'async'
			fd.baseStmt.baseNode.Pos = pos
			return fd
		}
		return p.parseExprStmt()
	case TokClass:
		return p.parseClassDecl()
	case TokLet, TokConst, TokVar:
		return p.parseVarDecl(true)
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
	case TokBreak:
		p.advance()
		label := ""
		if p.cur.Kind == TokIdent {
			label = p.advance().Val
		}
		p.eatSemi()
		return &JsBreakStmt{baseStmt: baseStmt{baseNode{Pos: pos, End: p.end()}}, Label: label}
	case TokContinue:
		p.advance()
		label := ""
		if p.cur.Kind == TokIdent {
			label = p.advance().Val
		}
		p.eatSemi()
		return &JsContinueStmt{baseStmt: baseStmt{baseNode{Pos: pos, End: p.end()}}, Label: label}
	case TokLBrace:
		return p.parseBlock()
	case TokSemi:
		p.advance()
		return &JsEmptyStmt{baseStmt: baseStmt{baseNode{Pos: pos, End: p.end()}}}
	case TokIdent:
		// labeled statement: ident ':'
		if p.peek.Kind == TokColon {
			label := p.advance().Val
			p.advance() // :
			body := p.parseStmt()
			return &JsLabeledStmt{baseStmt: baseStmt{baseNode{Pos: pos, End: p.end()}}, Label: label, Body: body}
		}
		return p.parseExprStmt()
	default:
		if isContextualIdent(p.cur.Kind) {
			if p.peek.Kind == TokColon {
				label := p.advance().Val
				p.advance()
				body := p.parseStmt()
				return &JsLabeledStmt{baseStmt: baseStmt{baseNode{Pos: pos, End: p.end()}}, Label: label, Body: body}
			}
		}
		return p.parseExprStmt()
	}
}

// ──────────────────────────────────────────────
//  Import / Export
// ──────────────────────────────────────────────

func (p *JSParser) parseImport() *JsImportDecl {
	pos := p.curPos()
	p.advance() // import
	d := &JsImportDecl{}

	if p.cur.Kind == TokString {
		d.Source = unquoteStr(p.advance().Val)
		p.eatSemi()
		d.Pos, d.End = pos, p.end()
		return d
	}

	if p.cur.Kind == TokStar {
		p.advance() // *
		p.eat(TokAs)
		d.Namespace = p.advance().Val
		p.eat(TokFrom)
		d.Source = unquoteStr(p.expect(TokString).Val)
		p.eatSemi()
		d.Pos, d.End = pos, p.end()
		return d
	}

	// default import?
	if p.cur.Kind == TokIdent && p.peek.Kind != TokLBrace {
		d.DefaultName = p.advance().Val
		if p.eat(TokComma) {
			// default + named: import Def, { A, B } from '...'
		} else {
			// just default
			p.eat(TokFrom)
			d.Source = unquoteStr(p.expect(TokString).Val)
			p.eatSemi()
			d.Pos, d.End = pos, p.end()
			return d
		}
	}

	if p.eat(TokLBrace) {
		for p.cur.Kind != TokRBrace && p.cur.Kind != TokEOF {
			imported := p.advance().Val
			local := imported
			if p.eat(TokAs) {
				local = p.advance().Val
			}
			d.Specifiers = append(d.Specifiers, JsImportSpec{imported, local})
			if !p.eat(TokComma) {
				break
			}
		}
		p.eat(TokRBrace)
	}

	p.eat(TokFrom)
	if p.cur.Kind == TokString {
		d.Source = unquoteStr(p.advance().Val)
	}
	p.eatSemi()
	d.Pos, d.End = pos, p.end()
	return d
}

func (p *JSParser) parseExport() *JsExportDecl {
	pos := p.curPos()
	p.advance()
	d := &JsExportDecl{}
	if p.eat(TokDefault) {
		d.Default = true
	}
	d.Declaration = p.parseStmt()
	d.Pos, d.End = pos, p.end()
	return d
}

// ──────────────────────────────────────────────
//  Declarations
// ──────────────────────────────────────────────

func (p *JSParser) parseFuncDecl(async bool) *JsFuncDecl {
	pos := p.curPos()
	p.advance() // function
	gen := p.eat(TokStar)
	name := ""
	if p.cur.Kind == TokIdent {
		name = p.advance().Val
	}
	params := p.parseParams()
	body := p.parseBlock()
	return &JsFuncDecl{
		baseStmt: baseStmt{baseNode{Pos: pos, End: p.end()}},
		Name:     name, Params: params, Body: body,
		Async: async, Generator: gen,
	}
}

func (p *JSParser) parseClassDecl() *JsClassDecl {
	pos := p.curPos()
	p.advance()
	name := ""
	if p.cur.Kind == TokIdent {
		name = p.advance().Val
	}
	var super JsExpr
	if p.eat(TokExtends) {
		super = p.parseLeftHandSide()
	}
	d := &JsClassDecl{Name: name, SuperClass: super}
	p.eat(TokLBrace)
	for p.cur.Kind != TokRBrace && p.cur.Kind != TokEOF {
		m := p.parseClassMember()
		if m != nil {
			d.Members = append(d.Members, m)
		}
	}
	p.eat(TokRBrace)
	d.Pos, d.End = pos, p.end()
	return d
}

func (p *JSParser) parseClassMember() *JsClassMember {
	pos := p.curPos()
	m := &JsClassMember{Kind: "method"}
	if p.cur.Kind == TokStatic {
		m.Static = true
		p.advance()
	}
	if p.cur.Val == "get" || p.cur.Val == "set" {
		if p.peek.Kind != TokLParen {
			m.Kind = p.advance().Val
		}
	}
	if p.cur.Kind == TokLBracket {
		p.advance()
		m.Key = p.parseAssignExpr()
		p.eat(TokRBracket)
		m.Computed = true
	} else if p.cur.Kind != TokEOF {
		m.Key = p.parsePropKey()
	}
	if p.cur.Kind == TokLParen {
		params := p.parseParams()
		body := p.parseBlock()
		m.Value = &JsFuncExpr{Params: params, Body: body}
	} else if p.eat(TokAssign) {
		m.Kind = "field"
		m.Value = p.parseAssignExpr()
		p.eatSemi()
	} else {
		p.eatSemi()
	}
	m.Pos, m.End = pos, p.end()
	return m
}

// parseVarDecl parses let/const/var declaration.
// consumeSemi=false is used inside for(...) headers.
func (p *JSParser) parseVarDecl(consumeSemi bool) *JsVarDecl {
	pos := p.curPos()
	kind := p.advance().Val
	d := &JsVarDecl{Kind: kind}
	for {
		dp := p.curPos()
		id := p.parseBindingPattern()
		var init JsExpr
		if p.eat(TokAssign) {
			init = p.parseAssignExpr()
		}
		d.Decls = append(d.Decls, &JsVarDeclarator{
			baseNode: baseNode{Pos: dp, End: p.end()},
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

func (p *JSParser) parseBindingPattern() JsExpr {
	switch p.cur.Kind {
	case TokLBracket:
		return p.parseArrayPattern()
	case TokLBrace:
		return p.parseObjectPattern()
	default:
		pos := p.curPos()
		name := p.advance().Val
		e := &JsIdent{baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}}, Name: name}
		return e
	}
}

func (p *JSParser) parseArrayPattern() JsExpr {
	pos := p.curPos()
	p.advance() // [
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
			elems = append(elems, &JsSpreadExpr{baseExpr: baseExpr{baseNode{Pos: ep, End: p.end()}}, X: arg})
			break
		}
		elem := p.parseBindingPattern()
		if p.eat(TokAssign) {
			def := p.parseAssignExpr()
			elem = &JsAssignExpr{baseExpr: baseExpr{baseNode{Pos: elem.nodePos(), End: p.end()}},
				Op: "=", L: elem, R: def}
		}
		elems = append(elems, elem)
		if !p.eat(TokComma) {
			break
		}
	}
	p.eat(TokRBracket)
	return &JsArrayExpr{baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}}, Elems: elems}
}

func (p *JSParser) parseObjectPattern() JsExpr {
	pos := p.curPos()
	p.advance() // {
	var props []*JsProp
	for p.cur.Kind != TokRBrace && p.cur.Kind != TokEOF {
		pp := p.curPos()
		if p.cur.Kind == TokEllipsis {
			p.advance()
			arg := p.parseBindingPattern()
			props = append(props, &JsProp{
				baseNode: baseNode{Pos: pp, End: p.end()},
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
			p.eat(TokRBracket)
		} else {
			key = p.parsePropKey()
		}
		var val JsExpr
		shorthand := false
		if p.eat(TokColon) {
			val = p.parseBindingPattern()
		} else {
			shorthand = true
			val = key
		}
		if p.eat(TokAssign) {
			def := p.parseAssignExpr()
			val = &JsAssignExpr{baseExpr: baseExpr{baseNode{Pos: val.nodePos(), End: p.end()}},
				Op: "=", L: val, R: def}
		}
		props = append(props, &JsProp{
			baseNode: baseNode{Pos: pp, End: p.end()},
			Key:      key, Value: val,
			Computed: computed, Shorthand: shorthand, Kind: "init",
		})
		if !p.eat(TokComma) {
			break
		}
	}
	p.eat(TokRBrace)
	return &JsObjectExpr{baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}}, Props: props}
}

func (p *JSParser) parseParams() []JsExpr {
	p.eat(TokLParen)
	var params []JsExpr
	for p.cur.Kind != TokRParen && p.cur.Kind != TokEOF {
		if p.cur.Kind == TokEllipsis {
			ep := p.curPos()
			p.advance()
			arg := p.parseBindingPattern()
			params = append(params, &JsSpreadExpr{baseExpr: baseExpr{baseNode{Pos: ep, End: p.end()}}, X: arg})
			break
		}
		param := p.parseBindingPattern()
		if p.eat(TokAssign) {
			def := p.parseAssignExpr()
			param = &JsAssignExpr{baseExpr: baseExpr{baseNode{Pos: param.nodePos(), End: p.end()}},
				Op: "=", L: param, R: def}
		}
		params = append(params, param)
		if !p.eat(TokComma) {
			break
		}
	}
	p.eat(TokRParen)
	return params
}

func (p *JSParser) parseBlock() *JsBlock {
	pos := p.curPos()
	p.eat(TokLBrace)
	b := &JsBlock{}
	for p.cur.Kind != TokRBrace && p.cur.Kind != TokEOF {
		s := p.parseStmt()
		if s != nil {
			b.Body = append(b.Body, s)
		}
	}
	p.eat(TokRBrace)
	b.Pos, b.End = pos, p.end()
	return b
}

// ──────────────────────────────────────────────
//  Control-flow statements
// ──────────────────────────────────────────────

func (p *JSParser) parseIf() *JsIfStmt {
	pos := p.curPos()
	p.advance()
	p.eat(TokLParen)
	test := p.parseExpr()
	p.eat(TokRParen)
	then := p.parseStmt()
	var els JsStmt
	if p.eat(TokElse) {
		els = p.parseStmt()
	}
	return &JsIfStmt{baseStmt: baseStmt{baseNode{Pos: pos, End: p.end()}}, Test: test, Then: then, Else: els}
}

func (p *JSParser) parseFor() JsStmt {
	pos := p.curPos()
	p.advance()
	p.eat(TokLParen)

	var initStmt JsStmt
	if p.cur.Kind != TokSemi {
		if p.cur.Kind == TokLet || p.cur.Kind == TokConst || p.cur.Kind == TokVar {
			initStmt = p.parseVarDecl(false)
		} else {
			expr := p.parseExpr()
			initStmt = &JsExprStmt{baseStmt: baseStmt{baseNode{Pos: expr.nodePos(), End: p.end()}}, X: expr}
		}
		if p.cur.Kind == TokIn || p.cur.Val == "of" {
			isOf := p.cur.Val == "of"
			p.advance()
			right := p.parseAssignExpr()
			p.eat(TokRParen)
			body := p.parseStmt()
			return &JsForInStmt{
				baseStmt: baseStmt{baseNode{Pos: pos, End: p.end()}},
				Left:     initStmt, Right: right, Body: body, IsOf: isOf,
			}
		}
	}

	p.eat(TokSemi)
	var test JsExpr
	if p.cur.Kind != TokSemi {
		test = p.parseExpr()
	}
	p.eat(TokSemi)
	var update JsExpr
	if p.cur.Kind != TokRParen {
		update = p.parseExpr()
	}
	p.eat(TokRParen)
	body := p.parseStmt()
	return &JsForStmt{
		baseStmt: baseStmt{baseNode{Pos: pos, End: p.end()}},
		Init:     initStmt, Test: test, Update: update, Body: body,
	}
}

func (p *JSParser) parseWhile() *JsWhileStmt {
	pos := p.curPos()
	p.advance()
	p.eat(TokLParen)
	test := p.parseExpr()
	p.eat(TokRParen)
	body := p.parseStmt()
	return &JsWhileStmt{baseStmt: baseStmt{baseNode{Pos: pos, End: p.end()}}, Test: test, Body: body}
}

func (p *JSParser) parseDoWhile() *JsDoWhileStmt {
	pos := p.curPos()
	p.advance()
	body := p.parseStmt()
	p.eat(TokWhile)
	p.eat(TokLParen)
	test := p.parseExpr()
	p.eat(TokRParen)
	p.eatSemi()
	return &JsDoWhileStmt{baseStmt: baseStmt{baseNode{Pos: pos, End: p.end()}}, Body: body, Test: test}
}

func (p *JSParser) parseReturn() *JsReturnStmt {
	pos := p.curPos()
	p.advance()
	var arg JsExpr
	if p.cur.Kind != TokSemi && p.cur.Kind != TokRBrace && p.cur.Kind != TokEOF {
		arg = p.parseExpr()
	}
	p.eatSemi()
	return &JsReturnStmt{baseStmt: baseStmt{baseNode{Pos: pos, End: p.end()}}, Arg: arg}
}

func (p *JSParser) parseThrow() *JsThrowStmt {
	pos := p.curPos()
	p.advance()
	arg := p.parseExpr()
	p.eatSemi()
	return &JsThrowStmt{baseStmt: baseStmt{baseNode{Pos: pos, End: p.end()}}, Arg: arg}
}

func (p *JSParser) parseTry() *JsTryStmt {
	pos := p.curPos()
	p.advance()
	block := p.parseBlock()
	st := &JsTryStmt{Block: block}
	if p.eat(TokCatch) {
		if p.eat(TokLParen) {
			st.CatchParam = p.parseBindingPattern()
			p.eat(TokRParen)
		}
		st.Handler = p.parseBlock()
	}
	if p.eat(TokFinally) {
		st.Finalizer = p.parseBlock()
	}
	st.Pos, st.End = pos, p.end()
	return st
}

func (p *JSParser) parseSwitch() *JsSwitchStmt {
	pos := p.curPos()
	p.advance()
	p.eat(TokLParen)
	disc := p.parseExpr()
	p.eat(TokRParen)
	p.eat(TokLBrace)
	st := &JsSwitchStmt{Disc: disc}
	for p.cur.Kind != TokRBrace && p.cur.Kind != TokEOF {
		cp := p.curPos()
		c := &JsSwitchCase{}
		if p.eat(TokCase) {
			c.Test = p.parseExpr()
		} else {
			p.eat(TokDefault)
		}
		p.eat(TokColon)
		for p.cur.Kind != TokCase && p.cur.Kind != TokDefault &&
			p.cur.Kind != TokRBrace && p.cur.Kind != TokEOF {
			c.Body = append(c.Body, p.parseStmt())
		}
		c.Pos, c.End = cp, p.end()
		st.Cases = append(st.Cases, c)
	}
	p.eat(TokRBrace)
	st.Pos, st.End = pos, p.end()
	return st
}

func (p *JSParser) parseExprStmt() *JsExprStmt {
	pos := p.curPos()
	x := p.parseExpr()
	p.eatSemi()
	return &JsExprStmt{baseStmt: baseStmt{baseNode{Pos: pos, End: p.end()}}, X: x}
}

// ──────────────────────────────────────────────
//  Expressions — Pratt / TDOP
// ──────────────────────────────────────────────

func (p *JSParser) parseExpr() JsExpr {
	left := p.parseAssignExpr()
	if p.cur.Kind == TokComma {
		exprs := []JsExpr{left}
		for p.eat(TokComma) {
			exprs = append(exprs, p.parseAssignExpr())
		}
		return &JsSeqExpr{
			baseExpr: baseExpr{baseNode{Pos: left.nodePos(), End: p.end()}},
			Exprs:    exprs,
		}
	}
	return left
}

func (p *JSParser) parseAssignExpr() JsExpr {
	// Arrow: ident => body
	if p.cur.Kind == TokIdent && p.peek.Kind == TokArrow {
		pos := p.curPos()
		name := p.advance().Val
		p.advance() // =>
		param := &JsIdent{baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}}, Name: name}
		body := p.parseArrowBody()
		return &JsArrowFunc{
			baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}},
			Params:   []JsExpr{param}, Body: body,
		}
	}
	// async arrow: async ident => body  or  async (params) => body
	if p.cur.Kind == TokAsync {
		if p.peek.Kind == TokIdent {
			pos := p.curPos()
			p.advance() // async
			name := p.advance().Val
			if p.eat(TokArrow) {
				param := &JsIdent{Name: name}
				body := p.parseArrowBody()
				return &JsArrowFunc{
					baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}},
					Params:   []JsExpr{param}, Body: body, Async: true,
				}
			}
			// Not an arrow — fallthrough to ternary with 'async name' as expr
			// Reconstruct: treat as call expr parse
		}
		if p.peek.Kind == TokLParen {
			// Try async (...) => body — need to parse params
			pos := p.curPos()
			p.advance() // async
			if p.cur.Kind == TokLParen {
				params := p.parseParams()
				if p.eat(TokArrow) {
					body := p.parseArrowBody()
					return &JsArrowFunc{
						baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}},
						Params:   params, Body: body, Async: true,
					}
				}
				// Not arrow, parse as call: async(params)
				callee := &JsIdent{baseExpr: baseExpr{baseNode{Pos: pos, End: pos + 5}}, Name: "async"}
				return &JsCallExpr{
					baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}},
					Callee:   callee, Args: params,
				}
			}
		}
	}

	left := p.parseTernary()

	if isAssignOp(p.cur.Kind) {
		pos := left.nodePos()
		op := p.advance().Val
		right := p.parseAssignExpr()
		return &JsAssignExpr{
			baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}},
			Op:       op, L: left, R: right,
		}
	}
	return left
}

func (p *JSParser) parseArrowBody() JsNode {
	if p.cur.Kind == TokLBrace {
		return p.parseBlock()
	}
	return p.parseAssignExpr()
}

// parseTernary handles: expr ? then : else
func (p *JSParser) parseTernary() JsExpr {
	left := p.parseBinary(0)
	if p.cur.Kind == TokQuestion {
		pos := left.nodePos()
		p.advance()
		then := p.parseAssignExpr()
		p.eat(TokColon)
		alt := p.parseAssignExpr()
		return &JsConditionalExpr{
			baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}},
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
	left := p.parseUnary()
	for {
		row, ok := binOpTable[p.cur.Kind]
		if !ok || row.prec <= minPrec {
			break
		}
		pos := left.nodePos()
		p.advance()
		nextPrec := row.prec
		if row.right {
			nextPrec--
		}
		right := p.parseBinary(nextPrec)
		if row.op == "||" || row.op == "&&" || row.op == "??" {
			left = &JsLogicalExpr{
				baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}},
				Op:       row.op, L: left, R: right,
			}
		} else {
			left = &JsBinaryExpr{
				baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}},
				Op:       row.op, L: left, R: right,
			}
		}
	}
	return left
}

func (p *JSParser) parseUnary() JsExpr {
	pos := p.curPos()
	switch p.cur.Kind {
	case TokBang, TokTilde, TokPlus, TokMinus:
		op := p.advance().Val
		x := p.parseUnary()
		return &JsUnaryExpr{baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}}, Op: op, Prefix: true, X: x}
	case TokDelete, TokTypeof, TokVoid:
		op := p.advance().Val
		x := p.parseUnary()
		return &JsUnaryExpr{baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}}, Op: op, Prefix: true, X: x}
	case TokPlusPlus, TokMinusMinus:
		op := p.advance().Val
		x := p.parseUnary()
		return &JsUnaryExpr{baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}}, Op: op, Prefix: true, X: x}
	case TokAwait:
		p.advance()
		x := p.parseUnary()
		return &JsAwaitExpr{baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}}, X: x}
	}
	return p.parsePostfix()
}

func (p *JSParser) parsePostfix() JsExpr {
	x := p.parseLeftHandSide()
	if p.cur.Kind == TokPlusPlus || p.cur.Kind == TokMinusMinus {
		op := p.advance().Val
		return &JsUnaryExpr{
			baseExpr: baseExpr{baseNode{Pos: x.nodePos(), End: p.end()}},
			Op:       op, Prefix: false, X: x,
		}
	}
	return x
}

// parseLeftHandSide handles calls, member access, new expressions.
func (p *JSParser) parseLeftHandSide() JsExpr {
	expr := p.parseNewOrPrimary()
	for {
		pos := expr.nodePos()
		switch p.cur.Kind {
		case TokLParen:
			args := p.parseArgs()
			expr = &JsCallExpr{
				baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}},
				Callee:   expr, Args: args,
			}
		case TokDot:
			p.advance()
			propPos := p.curPos()
			name := p.advance().Val
			prop := &JsIdent{baseExpr: baseExpr{baseNode{Pos: propPos, End: p.end()}}, Name: name}
			expr = &JsMemberExpr{
				baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}},
				Obj:      expr, Prop: prop, Computed: false,
			}
		case TokLBracket:
			p.advance()
			prop := p.parseExpr()
			p.eat(TokRBracket)
			expr = &JsMemberExpr{
				baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}},
				Obj:      expr, Prop: prop, Computed: true,
			}
		case TokOptChain:
			p.advance()
			if p.cur.Kind == TokLParen {
				args := p.parseArgs()
				expr = &JsCallExpr{
					baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}},
					Callee:   expr, Args: args, Optional: true,
				}
			} else if p.cur.Kind == TokLBracket {
				p.advance()
				prop := p.parseExpr()
				p.eat(TokRBracket)
				expr = &JsMemberExpr{
					baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}},
					Obj:      expr, Prop: prop, Computed: true, Optional: true,
				}
			} else {
				propPos := p.curPos()
				name := p.advance().Val
				prop := &JsIdent{baseExpr: baseExpr{baseNode{Pos: propPos, End: p.end()}}, Name: name}
				expr = &JsMemberExpr{
					baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}},
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
	p.advance()
	// new.target
	if p.cur.Kind == TokDot {
		p.advance()
		p.advance() // target
		return &JsMemberExpr{
			baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}},
			Obj:      &JsIdent{Name: "new"},
			Prop:     &JsIdent{Name: "target"},
		}
	}
	callee := p.parseNewOrPrimary()
	var args []JsExpr
	if p.cur.Kind == TokLParen {
		args = p.parseArgs()
	}
	return &JsNewExpr{
		baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}},
		Callee:   callee, Args: args,
	}
}

func (p *JSParser) parseArgs() []JsExpr {
	p.eat(TokLParen)
	var args []JsExpr
	for p.cur.Kind != TokRParen && p.cur.Kind != TokEOF {
		if p.cur.Kind == TokEllipsis {
			ep := p.curPos()
			p.advance()
			x := p.parseAssignExpr()
			args = append(args, &JsSpreadExpr{baseExpr: baseExpr{baseNode{Pos: ep, End: p.end()}}, X: x})
		} else {
			args = append(args, p.parseAssignExpr())
		}
		if !p.eat(TokComma) {
			break
		}
	}
	p.eat(TokRParen)
	return args
}

func (p *JSParser) parsePrimary() JsExpr {
	pos := p.curPos()

	switch p.cur.Kind {
	case TokIdent:
		name := p.advance().Val
		return &JsIdent{baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}}, Name: name}

	case TokNumber:
		raw := p.advance().Val
		return &JsLiteral{baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}}, Raw: raw, Kind: numberKind(raw)}

	case TokString:
		raw := p.advance().Val
		return &JsLiteral{baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}}, Raw: raw, Kind: "string"}

	case TokTemplate:
		raw := p.advance().Val
		return p.structureTemplate(raw, pos, p.end())

	case TokRegex:
		raw := p.advance().Val
		return &JsLiteral{baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}}, Raw: raw, Kind: "regex"}

	case TokTrue, TokFalse:
		raw := p.advance().Val
		return &JsLiteral{baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}}, Raw: raw, Kind: "boolean"}

	case TokNull:
		p.advance()
		return &JsLiteral{baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}}, Raw: "null", Kind: "null"}

	case TokThis:
		p.advance()
		return &JsThisExpr{baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}}}

	case TokSuper:
		p.advance()
		return &JsSuperExpr{baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}}}

	case TokFunction:
		p.advance()
		gen := p.eat(TokStar)
		name := ""
		if p.cur.Kind == TokIdent {
			name = p.advance().Val
		}
		params := p.parseParams()
		body := p.parseBlock()
		return &JsFuncExpr{
			baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}},
			Name:     name, Params: params, Body: body, Generator: gen,
		}

	case TokAsync:
		// async function expr  or  async arrow
		if p.peek.Kind == TokFunction {
			p.advance()
			p.advance()
			name := ""
			if p.cur.Kind == TokIdent {
				name = p.advance().Val
			}
			params := p.parseParams()
			body := p.parseBlock()
			return &JsFuncExpr{
				baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}},
				Name:     name, Params: params, Body: body, Async: true,
			}
		}
		// async ident => or async (...)  => handled in parseAssignExpr
		name := p.advance().Val
		return &JsIdent{baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}}, Name: name}

	case TokLParen:
		p.advance()
		// Empty parens: () => body
		if p.cur.Kind == TokRParen {
			p.advance()
			if p.eat(TokArrow) {
				body := p.parseArrowBody()
				return &JsArrowFunc{
					baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}},
					Params:   []JsExpr{}, Body: body,
				}
			}
			// () as expression — unusual but handle gracefully
			return &JsLiteral{baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}}, Raw: "()", Kind: "null"}
		}
		expr := p.parseExpr()
		p.eat(TokRParen)
		// Arrow: (expr) =>  where expr is actually params
		if p.eat(TokArrow) {
			params := seqToParams(expr)
			body := p.parseArrowBody()
			return &JsArrowFunc{
				baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}},
				Params:   params, Body: body,
			}
		}
		return expr

	case TokLBracket:
		return p.parseArrayLit()

	case TokLBrace:
		return p.parseObjectLit()

	case TokYield:
		p.advance()
		delegate := p.eat(TokStar)
		var x JsExpr
		if p.cur.Kind != TokSemi && p.cur.Kind != TokRBrace && p.cur.Kind != TokEOF {
			x = p.parseAssignExpr()
		}
		return &JsYieldExpr{baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}}, X: x, Delegate: delegate}

	default:
		// Contextual keywords can appear as identifiers in expression position
		if isContextualIdent(p.cur.Kind) {
			name := p.advance().Val
			return &JsIdent{baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}}, Name: name}
		}
		// Unknown token: advance and return a placeholder
		tok := p.advance()
		return &JsLiteral{baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}}, Raw: tok.Val, Kind: "unknown"}
	}
}

func (p *JSParser) parseArrayLit() *JsArrayExpr {
	pos := p.curPos()
	p.advance()
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
			elems = append(elems, &JsSpreadExpr{baseExpr: baseExpr{baseNode{Pos: ep, End: p.end()}}, X: x})
		} else {
			elems = append(elems, p.parseAssignExpr())
		}
		if !p.eat(TokComma) {
			break
		}
	}
	p.eat(TokRBracket)
	return &JsArrayExpr{baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}}, Elems: elems}
}

func (p *JSParser) parseObjectLit() *JsObjectExpr {
	pos := p.curPos()
	p.advance()
	var props []*JsProp
	for p.cur.Kind != TokRBrace && p.cur.Kind != TokEOF {
		pp := p.curPos()
		if p.cur.Kind == TokEllipsis {
			p.advance()
			x := p.parseAssignExpr()
			props = append(props, &JsProp{
				baseNode: baseNode{Pos: pp, End: p.end()},
				Spread:   true, Value: x, Kind: "init",
			})
			p.eat(TokComma)
			continue
		}

		kind := "init"
		isAsync := false
		isGen := false

		if p.cur.Val == "async" && p.peek.Kind != TokColon && p.peek.Kind != TokComma {
			isAsync = true
			p.advance()
		}
		if p.cur.Kind == TokStar {
			isGen = true
			p.advance()
		}
		_ = isGen
		if (p.cur.Val == "get" || p.cur.Val == "set") && p.peek.Kind != TokColon &&
			p.peek.Kind != TokComma && p.peek.Kind != TokRBrace && p.peek.Kind != TokLParen {
			kind = p.advance().Val
		}
		_ = isAsync

		computed := false
		var key JsExpr
		if p.cur.Kind == TokLBracket {
			p.advance()
			computed = true
			key = p.parseAssignExpr()
			p.eat(TokRBracket)
		} else {
			key = p.parsePropKey()
		}

		shorthand := false
		isMethod := false
		var val JsExpr

		if p.cur.Kind == TokLParen {
			params := p.parseParams()
			body := p.parseBlock()
			val = &JsFuncExpr{Params: params, Body: body, Async: isAsync, Generator: isGen}
			// Method shorthand — but get/set already carry their own
			// Kind, so only plain methods get the Method flag.
			if kind == "init" {
				isMethod = true
			}
		} else if p.eat(TokColon) {
			val = p.parseAssignExpr()
		} else {
			shorthand = true
			val = key
		}

		props = append(props, &JsProp{
			baseNode: baseNode{Pos: pp, End: p.end()},
			Key:      key, Value: val,
			Computed: computed, Shorthand: shorthand, Method: isMethod, Kind: kind,
		})
		if !p.eat(TokComma) {
			break
		}
	}
	p.eat(TokRBrace)
	return &JsObjectExpr{baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}}, Props: props}
}

func (p *JSParser) parsePropKey() JsExpr {
	pos := p.curPos()
	switch p.cur.Kind {
	case TokString:
		raw := p.advance().Val
		return &JsLiteral{baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}}, Raw: raw, Kind: "string"}
	case TokNumber:
		raw := p.advance().Val
		return &JsLiteral{baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}}, Raw: raw, Kind: numberKind(raw)}
	default:
		name := p.advance().Val
		return &JsIdent{baseExpr: baseExpr{baseNode{Pos: pos, End: p.end()}}, Name: name}
	}
}

// ──────────────────────────────────────────────
//  Helpers
// ──────────────────────────────────────────────

// seqToParams converts a comma-separated sequence expression into a param list.
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
// Each interpolation is recursively parsed with a fresh JSParser so
// the resulting JsTemplateLit carries real sub-expression nodes.
// Brace depth is tracked so a `}` inside a nested object literal or
// nested template doesn't prematurely close the hole.
//
// If raw is malformed (no closing backtick, unbalanced ${}), the
// function degrades gracefully: whatever text was accumulated
// becomes a final quasi.  The parser is lenient by design.
func (p *JSParser) structureTemplate(raw string, pos, end int) *JsTemplateLit {
	lit := &JsTemplateLit{baseExpr: baseExpr{baseNode{Pos: pos, End: end}}}

	// Strip the surrounding backticks if present.
	body := raw
	if len(body) >= 2 && body[0] == '`' {
		body = body[1 : len(body)-1]
	}

	var quasi strings.Builder
	i := 0
	for i < len(body) {
		c := body[i]
		// Escape: copy the backslash and the next char verbatim.
		if c == '\\' && i+1 < len(body) {
			quasi.WriteByte(c)
			quasi.WriteByte(body[i+1])
			i += 2
			continue
		}
		// Start of an interpolation hole.
		if c == '$' && i+1 < len(body) && body[i+1] == '{' {
			// Close the current quasi.
			lit.Quasis = append(lit.Quasis, quasi.String())
			quasi.Reset()
			// Scan to the matching '}', tracking nested braces and
			// nested template literals (whose own ${} must not be
			// counted against this hole).
			depth := 1
			j := i + 2
			for j < len(body) && depth > 0 {
				switch body[j] {
				case '\\':
					j++ // skip escaped char
				case '{':
					depth++
				case '}':
					depth--
					if depth == 0 {
						goto closed
					}
				case '`':
					// Skip a nested template literal wholesale.
					j++
					for j < len(body) && body[j] != '`' {
						if body[j] == '\\' {
							j++
						}
						j++
					}
				case '"', '\'':
					q := body[j]
					j++
					for j < len(body) && body[j] != q {
						if body[j] == '\\' {
							j++
						}
						j++
					}
				}
				j++
			}
		closed:
			exprSrc := ""
			if j <= len(body) {
				// body[i+2 : j] is the expression text (j points at '}').
				hi := j
				if hi > len(body) {
					hi = len(body)
				}
				exprSrc = body[i+2 : hi]
			}
			// Recursively parse the interpolation as an expression.
			expr := parseJSExpr(exprSrc)
			if expr == nil {
				// Degrade: empty interpolation → empty-string literal.
				expr = &JsLiteral{
					baseExpr: baseExpr{baseNode{Pos: pos, End: end}},
					Raw:      "''", Kind: "string",
				}
			} else {
				// parseJSExpr parsed exprSrc as a detached string —
				// every node's Pos/End is relative to exprSrc's
				// start (0).  Shift the whole sub-tree back onto
				// the original source coordinates so downstream
				// position-sensitive passes (TDZ scope analysis,
				// diagnostics) see correct locations.  The hole
				// expression begins at the template's Pos, past the
				// opening backtick, plus the body offset of "${".
				delta := pos + 1 + (i + 2)
				shiftTree(expr, delta)
			}
			lit.Exprs = append(lit.Exprs, expr)
			i = j + 1
			continue
		}
		quasi.WriteByte(c)
		i++
	}
	// Trailing quasi (always present — maintains the N+1 invariant).
	lit.Quasis = append(lit.Quasis, quasi.String())
	return lit
}

// shiftTree moves every node in a sub-tree by delta bytes, so a
// tree parsed from a detached substring lines up with the original
// source coordinates.  Skips synthetic nodes (Pos == 0 && End == 0)
// — they have no source span to shift.
func shiftTree(root JsNode, delta int) {
	if delta == 0 || root == nil {
		return
	}
	WalkNode(root, func(n JsNode) bool {
		if s, ok := n.(posShiftable); ok {
			if n.nodePos() != 0 || n.nodeEnd() != 0 {
				s.shiftPos(delta)
			}
		}
		return true
	})
}
