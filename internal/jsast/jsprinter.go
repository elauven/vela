package jsast

// jsprinter.go — serializes a JS AST back to source text.
//
// This is the output half of the new AST-based code generator.
// CodeGen builds a JsProgram; this file turns it back into a string.
//
// Two modes (see PrintMode):
//   Pretty   — readable, indented, one statement per line.
//   Minified — no insignificant whitespace; replaces the old
//              char-level minifier.go pass.
//
// Correctness contract: for any AST produced by the parser,
// Print(Parse(src)) must re-parse to a structurally equal tree.
// The round-trip test (jsprinter_roundtrip_test.go) enforces this.
//
// Parenthesization is *derived*, not stored.  The printer walks the
// tree with a "parent precedence" context and inserts parentheses
// only where the child's precedence would otherwise re-associate
// against the parent.  The AST never carries explicit paren nodes.

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// ─────────────────────────────────────────────
//  Public entry points
// ─────────────────────────────────────────────

type PrintMode int

const (
	PrintPretty PrintMode = iota
	PrintMinified
)

// PrintLocation identifies one user-origin AST node at its printed location.
// GeneratedLine is one-based; GeneratedColumn and SourcePos are zero-based.
type PrintLocation struct {
	GeneratedLine   int
	GeneratedColumn int
	SourcePos       int
}

// PrintJS serializes a whole program.
func PrintJS(prog *JsProgram, mode PrintMode) string {
	return PrintJSWithLocations(prog, mode, nil)
}

// PrintJSWithLocations serializes prog and reports every user-origin node at
// the generated position where printing of that node starts. Compiler-created
// nodes are deliberately omitted: they have no source location to map back to.
func PrintJSWithLocations(prog *JsProgram, mode PrintMode, visit func(PrintLocation)) string {
	p := &jsPrinter{mode: mode, line: 1, location: visit}
	p.program(prog)
	return p.sb.String()
}

// PrintExpr serializes a single expression (used by tests and by
// codegen sites that need an expression fragment).
func PrintExpr(e JsExpr, mode PrintMode) string {
	p := &jsPrinter{mode: mode}
	p.expr(e, 0)
	return p.sb.String()
}

// ─────────────────────────────────────────────
//  Printer state
// ─────────────────────────────────────────────

type jsPrinter struct {
	sb       strings.Builder
	mode     PrintMode
	indent   int
	line     int
	column   int
	location func(PrintLocation)
}

func (p *jsPrinter) pretty() bool { return p.mode == PrintPretty }

func (p *jsPrinter) write(s string) {
	p.sb.WriteString(s)
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		s = s[size:]
		if r == '\n' {
			p.line++
			p.column = 0
		} else {
			// Source Map columns use UTF-16 code units.
			p.column++
			if r > 0xffff {
				p.column++
			}
		}
	}
}

func (p *jsPrinter) ch(c byte) {
	p.sb.WriteByte(c)
	if c == '\n' {
		p.line++
		p.column = 0
	} else {
		p.column++
	}
}

func (p *jsPrinter) note(n JsNode) {
	if p.location == nil || n == nil {
		return
	}
	pos, ok := sourceOrigin(n)
	if !ok {
		return
	}
	p.location(PrintLocation{
		GeneratedLine: p.line, GeneratedColumn: p.column, SourcePos: pos,
	})
}

func sourceOrigin(n JsNode) (int, bool) {
	switch x := n.(type) {
	case *JsIdent:
		if x.HasSourceOrigin {
			return x.SourceOrigin, true
		}
	case *JsLiteral:
		if x.HasSourceOrigin {
			return x.SourceOrigin, true
		}
	case *JsTemplateLit:
		if x.HasSourceOrigin {
			return x.SourceOrigin, true
		}
	case *JsArrayExpr:
		if x.HasSourceOrigin {
			return x.SourceOrigin, true
		}
	case *JsObjectExpr:
		if x.HasSourceOrigin {
			return x.SourceOrigin, true
		}
	case *JsFuncExpr:
		if x.HasSourceOrigin {
			return x.SourceOrigin, true
		}
	case *JsArrowFunc:
		if x.HasSourceOrigin {
			return x.SourceOrigin, true
		}
	case *JsUnaryExpr:
		if x.HasSourceOrigin {
			return x.SourceOrigin, true
		}
	case *JsBinaryExpr:
		if x.HasSourceOrigin {
			return x.SourceOrigin, true
		}
	case *JsLogicalExpr:
		if x.HasSourceOrigin {
			return x.SourceOrigin, true
		}
	case *JsAssignExpr:
		if x.HasSourceOrigin {
			return x.SourceOrigin, true
		}
	case *JsMemberExpr:
		if x.HasSourceOrigin {
			return x.SourceOrigin, true
		}
	case *JsCallExpr:
		if x.HasSourceOrigin {
			return x.SourceOrigin, true
		}
	case *JsNewExpr:
		if x.HasSourceOrigin {
			return x.SourceOrigin, true
		}
	case *JsConditionalExpr:
		if x.HasSourceOrigin {
			return x.SourceOrigin, true
		}
	case *JsSeqExpr:
		if x.HasSourceOrigin {
			return x.SourceOrigin, true
		}
	case *JsSpreadExpr:
		if x.HasSourceOrigin {
			return x.SourceOrigin, true
		}
	case *JsAwaitExpr:
		if x.HasSourceOrigin {
			return x.SourceOrigin, true
		}
	case *JsYieldExpr:
		if x.HasSourceOrigin {
			return x.SourceOrigin, true
		}
	case *JsThisExpr:
		if x.HasSourceOrigin {
			return x.SourceOrigin, true
		}
	case *JsSuperExpr:
		if x.HasSourceOrigin {
			return x.SourceOrigin, true
		}
	}
	if n.origin() == OriginUser && n.NodeEnd() > n.NodePos() {
		return n.NodePos(), true
	}
	return 0, false
}

// nl emits a newline + indentation in pretty mode; nothing in
// minified mode.
func (p *jsPrinter) nl() {
	if !p.pretty() {
		return
	}
	p.ch('\n')
	for i := 0; i < p.indent; i++ {
		p.write("  ")
	}
}

// sp emits a single space in pretty mode; nothing in minified mode.
// Used around binary operators, after keywords, etc.  Some spaces
// are *required* even when minified (e.g. between `return` and an
// identifier) — those use reqSp.
func (p *jsPrinter) sp() {
	if p.pretty() {
		p.ch(' ')
	}
}

// reqSp emits a space that is required for correctness even in
// minified mode — e.g. `return x`, `typeof x`, `a in b`.
func (p *jsPrinter) reqSp() { p.ch(' ') }

// ─────────────────────────────────────────────
//  Program / module
// ─────────────────────────────────────────────

func (p *jsPrinter) program(prog *JsProgram) {
	for i, stmt := range prog.Body {
		if i > 0 {
			p.nl()
		}
		p.stmt(stmt)
	}
}

// ─────────────────────────────────────────────
//  Statements
// ─────────────────────────────────────────────

func (p *jsPrinter) stmt(s JsStmt) {
	p.note(s)
	switch n := s.(type) {

	case *JsVarDecl:
		p.write(n.Kind)
		p.reqSp()
		for i, d := range n.Decls {
			if i > 0 {
				p.ch(',')
				p.sp()
			}
			p.expr(d.Id, 0)
			if d.Init != nil {
				p.sp()
				p.ch('=')
				p.sp()
				p.expr(d.Init, precAssign)
			}
		}
		p.ch(';')

	case *JsFuncDecl:
		if n.Async {
			p.write("async")
			p.reqSp()
		}
		p.write("function")
		if n.Generator {
			p.ch('*')
		}
		p.reqSp()
		p.write(n.Name)
		p.paramList(n.Params)
		p.sp()
		p.block(n.Body)

	case *JsClassDecl:
		p.write("class")
		p.reqSp()
		p.write(n.Name)
		if n.SuperClass != nil {
			p.reqSp()
			p.write("extends")
			p.reqSp()
			p.expr(n.SuperClass, precLHS)
		}
		p.sp()
		p.classBody(n.Members)

	case *JsBlock:
		p.block(n)

	case *JsExprStmt:
		// A statement beginning with `{` or `function` would be
		// mis-parsed as a block / declaration; wrap in parens.
		if exprStmtNeedsParens(n.X) {
			p.ch('(')
			p.expr(n.X, 0)
			p.ch(')')
		} else {
			p.expr(n.X, 0)
		}
		p.ch(';')

	case *JsIfStmt:
		p.write("if")
		p.sp()
		p.ch('(')
		p.expr(n.Test, 0)
		p.ch(')')
		p.thenBranch(n.Then)
		if n.Else != nil {
			if p.pretty() {
				p.nl()
			}
			p.write("else")
			// `else if` stays on one line; other else bodies get
			// the standard branch treatment.
			if _, isIf := n.Else.(*JsIfStmt); isIf {
				p.reqSp()
				p.stmt(n.Else)
			} else {
				p.thenBranch(n.Else)
			}
		}

	case *JsForStmt:
		p.write("for")
		p.sp()
		p.ch('(')
		if n.Init != nil {
			p.forInit(n.Init)
		}
		p.ch(';')
		if n.Test != nil {
			p.sp()
			p.expr(n.Test, 0)
		}
		p.ch(';')
		if n.Update != nil {
			p.sp()
			p.expr(n.Update, 0)
		}
		p.ch(')')
		p.thenBranch(n.Body)

	case *JsForInStmt:
		p.write("for")
		p.sp()
		p.ch('(')
		p.forInit(n.Left)
		p.reqSp()
		if n.IsOf {
			p.write("of")
		} else {
			p.write("in")
		}
		p.reqSp()
		p.expr(n.Right, precAssign)
		p.ch(')')
		p.thenBranch(n.Body)

	case *JsWhileStmt:
		p.write("while")
		p.sp()
		p.ch('(')
		p.expr(n.Test, 0)
		p.ch(')')
		p.thenBranch(n.Body)

	case *JsDoWhileStmt:
		p.write("do")
		p.thenBranch(n.Body)
		if p.pretty() {
			p.sp()
		}
		p.write("while")
		p.sp()
		p.ch('(')
		p.expr(n.Test, 0)
		p.ch(')')
		p.ch(';')

	case *JsReturnStmt:
		p.write("return")
		if n.Arg != nil {
			p.reqSp()
			p.expr(n.Arg, 0)
		}
		p.ch(';')

	case *JsThrowStmt:
		p.write("throw")
		p.reqSp()
		p.expr(n.Arg, 0)
		p.ch(';')

	case *JsTryStmt:
		p.write("try")
		p.sp()
		p.block(n.Block)
		if n.Handler != nil {
			p.sp()
			p.write("catch")
			if n.CatchParam != nil {
				p.sp()
				p.ch('(')
				p.expr(n.CatchParam, 0)
				p.ch(')')
			}
			p.sp()
			p.block(n.Handler)
		}
		if n.Finalizer != nil {
			p.sp()
			p.write("finally")
			p.sp()
			p.block(n.Finalizer)
		}

	case *JsSwitchStmt:
		p.write("switch")
		p.sp()
		p.ch('(')
		p.expr(n.Disc, 0)
		p.ch(')')
		p.sp()
		p.ch('{')
		p.indent++
		for _, c := range n.Cases {
			p.nl()
			if c.Test != nil {
				p.write("case")
				p.reqSp()
				p.expr(c.Test, 0)
				p.ch(':')
			} else {
				p.write("default:")
			}
			p.indent++
			for _, st := range c.Body {
				p.nl()
				p.stmt(st)
			}
			p.indent--
		}
		p.indent--
		p.nl()
		p.ch('}')

	case *JsBreakStmt:
		p.write("break")
		if n.Label != "" {
			p.reqSp()
			p.write(n.Label)
		}
		p.ch(';')

	case *JsContinueStmt:
		p.write("continue")
		if n.Label != "" {
			p.reqSp()
			p.write(n.Label)
		}
		p.ch(';')

	case *JsLabeledStmt:
		p.write(n.Label)
		p.ch(':')
		p.sp()
		p.stmt(n.Body)

	case *JsEmptyStmt:
		p.ch(';')

	case *JsDebuggerStmt:
		p.write("debugger;")

	case *JsImportDecl:
		p.importDecl(n)

	case *JsExportDecl:
		p.write("export")
		p.reqSp()
		if n.Default {
			p.write("default")
			p.reqSp()
		}
		p.stmt(n.Declaration)

	default:
		// Unknown statement — emit nothing rather than panic.
		// The round-trip test will catch any missing case.
	}
}

// thenBranch prints the body of an if/for/while.  A block body
// stays on the same line after a space; a non-block body goes on
// an indented new line in pretty mode.
func (p *jsPrinter) thenBranch(s JsStmt) {
	if blk, ok := s.(*JsBlock); ok {
		p.sp()
		p.block(blk)
		return
	}
	if p.pretty() {
		p.indent++
		p.nl()
		p.stmt(s)
		p.indent--
	} else {
		p.stmt(s)
	}
}

// forInit prints the init clause of a for/for-in header — either a
// var declaration (without trailing semicolon) or an expression.
func (p *jsPrinter) forInit(s JsStmt) {
	switch n := s.(type) {
	case *JsVarDecl:
		p.write(n.Kind)
		p.reqSp()
		for i, d := range n.Decls {
			if i > 0 {
				p.ch(',')
				p.sp()
			}
			p.expr(d.Id, 0)
			if d.Init != nil {
				p.sp()
				p.ch('=')
				p.sp()
				p.expr(d.Init, precAssign)
			}
		}
	case *JsExprStmt:
		p.expr(n.X, 0)
	default:
		p.stmt(s)
	}
}

func (p *jsPrinter) block(b *JsBlock) {
	if b == nil {
		p.write("{}")
		return
	}
	if len(b.Body) == 0 {
		p.write("{}")
		return
	}
	p.ch('{')
	p.indent++
	for _, s := range b.Body {
		p.nl()
		p.stmt(s)
	}
	p.indent--
	p.nl()
	p.ch('}')
}

func (p *jsPrinter) classBody(members []*JsClassMember) {
	if len(members) == 0 {
		p.write("{}")
		return
	}
	p.ch('{')
	p.indent++
	for _, m := range members {
		p.nl()
		if m.Static {
			p.write("static")
			p.reqSp()
		}
		switch m.Kind {
		case "get", "set":
			p.write(m.Kind)
			p.reqSp()
		}
		if m.Computed {
			p.ch('[')
			p.expr(m.Key, precAssign)
			p.ch(']')
		} else {
			p.expr(m.Key, precLHS)
		}
		if m.Kind == "field" {
			if m.Value != nil {
				p.sp()
				p.ch('=')
				p.sp()
				p.expr(m.Value, precAssign)
			}
			p.ch(';')
		} else {
			// method / get / set — Value is a JsFuncExpr
			if fn, ok := m.Value.(*JsFuncExpr); ok {
				p.paramList(fn.Params)
				p.sp()
				p.block(fn.Body)
			}
		}
	}
	p.indent--
	p.nl()
	p.ch('}')
}

func (p *jsPrinter) importDecl(n *JsImportDecl) {
	p.write("import")
	hasClause := n.DefaultName != "" || n.Namespace != "" || len(n.Specifiers) > 0
	if hasClause {
		p.reqSp()
		wrote := false
		if n.DefaultName != "" {
			p.write(n.DefaultName)
			wrote = true
		}
		if n.Namespace != "" {
			if wrote {
				p.ch(',')
				p.sp()
			}
			p.write("* as ")
			p.write(n.Namespace)
			wrote = true
		}
		if len(n.Specifiers) > 0 {
			if wrote {
				p.ch(',')
				p.sp()
			}
			p.ch('{')
			p.sp()
			for i, sp := range n.Specifiers {
				if i > 0 {
					p.ch(',')
					p.sp()
				}
				p.write(sp.Imported)
				if sp.Local != "" && sp.Local != sp.Imported {
					p.write(" as ")
					p.write(sp.Local)
				}
			}
			p.sp()
			p.ch('}')
		}
		p.reqSp()
		p.write("from")
		p.reqSp()
	}
	p.ch('\'')
	p.write(n.Source)
	p.ch('\'')
	p.ch(';')
}

// ─────────────────────────────────────────────
//  Expressions
// ─────────────────────────────────────────────

// Precedence levels — higher binds tighter.  These mirror the JS
// grammar and the parser's binOpTable.  `parentPrec` passed into
// expr() is the minimum precedence the context can hold without
// needing parens; if a child's precedence is lower, parenthesize.
const (
	precSeq     = 0  // comma
	precAssign  = 1  // = += etc, yield, arrow
	precCond    = 2  // ?:
	precNull    = 3  // ??
	precOr      = 4  // ||
	precAnd     = 5  // &&
	precBitOr   = 6  // |
	precBitXor  = 7  // ^
	precBitAnd  = 8  // &
	precEq      = 9  // == != === !==
	precRel     = 10 // < > <= >= in instanceof
	precShift   = 11 // << >> >>>
	precAdd     = 12 // + -
	precMul     = 13 // * / %
	precExp     = 14 // **
	precUnary   = 15 // ! ~ + - typeof void delete await
	precPostfix = 16 // x++ x--
	precLHS     = 17 // call, member, new
	precPrimary = 18 // literals, identifiers, parenthesized
)

// binPrec maps a binary/logical operator to its precedence level.
func binPrec(op string) int {
	switch op {
	case "??":
		return precNull
	case "||":
		return precOr
	case "&&":
		return precAnd
	case "|":
		return precBitOr
	case "^":
		return precBitXor
	case "&":
		return precBitAnd
	case "==", "!=", "===", "!==":
		return precEq
	case "<", ">", "<=", ">=", "in", "instanceof":
		return precRel
	case "<<", ">>", ">>>":
		return precShift
	case "+", "-":
		return precAdd
	case "*", "/", "%":
		return precMul
	case "**":
		return precExp
	}
	return precPrimary
}

// expr prints e.  parentPrec is the minimum precedence the
// surrounding context tolerates; if e's own precedence is lower,
// it is wrapped in parentheses.
func (p *jsPrinter) expr(e JsExpr, parentPrec int) {
	p.note(e)
	switch n := e.(type) {

	case *JsIdent:
		p.write(n.Name)

	case *JsLiteral:
		if n.Decoded && n.Kind == "string" {
			p.write(EncodeGeneratedString(n.Value))
		} else {
			p.write(n.Raw)
		}

	case *JsThisExpr:
		p.write("this")

	case *JsSuperExpr:
		p.write("super")

	case *JsTemplateLit:
		p.ch('`')
		for i, q := range n.Quasis {
			p.write(q)
			if i < len(n.Exprs) {
				p.write("${")
				p.expr(n.Exprs[i], 0)
				p.ch('}')
			}
		}
		p.ch('`')

	case *JsArrayExpr:
		p.ch('[')
		for i, el := range n.Elems {
			if i > 0 {
				p.ch(',')
				p.sp()
			}
			if el != nil {
				p.expr(el, precAssign)
			}
		}
		p.ch(']')

	case *JsObjectExpr:
		p.objectExpr(n)

	case *JsFuncExpr:
		paren := parentPrec >= precLHS
		if paren {
			p.ch('(')
		}
		if n.Async {
			p.write("async")
			p.reqSp()
		}
		p.write("function")
		if n.Generator {
			p.ch('*')
		}
		if n.Name != "" {
			p.reqSp()
			p.write(n.Name)
		}
		p.paramList(n.Params)
		p.sp()
		p.block(n.Body)
		if paren {
			p.ch(')')
		}

	case *JsArrowFunc:
		paren := parentPrec > precAssign
		if paren {
			p.ch('(')
		}
		if n.Async {
			p.write("async")
			p.reqSp()
		}
		p.paramList(n.Params)
		p.sp()
		p.write("=>")
		p.sp()
		switch body := n.Body.(type) {
		case *JsBlock:
			p.block(body)
		case JsExpr:
			// A concise-body arrow returning an object literal needs
			// the object wrapped so `{` isn't read as a block.
			if _, isObj := body.(*JsObjectExpr); isObj {
				p.ch('(')
				p.expr(body, precAssign)
				p.ch(')')
			} else {
				p.expr(body, precAssign)
			}
		}
		if paren {
			p.ch(')')
		}

	case *JsUnaryExpr:
		paren := parentPrec > precUnary
		if paren {
			p.ch('(')
		}
		if n.Prefix {
			p.write(n.Op)
			// Word operators (typeof, void, delete) and repeated
			// symbol operators need a separator.
			if isWordOp(n.Op) {
				p.reqSp()
			} else if needsUnarySpace(n.Op, n.X) {
				p.ch(' ')
			}
			p.expr(n.X, precUnary)
		} else {
			// postfix ++ / --
			p.expr(n.X, precPostfix)
			p.write(n.Op)
		}
		if paren {
			p.ch(')')
		}

	case *JsBinaryExpr:
		prec := binPrec(n.Op)
		paren := prec < parentPrec
		if paren {
			p.ch('(')
		}
		p.expr(n.L, prec)
		// Word operators (in, instanceof) require surrounding spaces.
		if isWordOp(n.Op) {
			p.reqSp()
			p.write(n.Op)
			p.reqSp()
		} else {
			p.sp()
			p.write(n.Op)
			p.sp()
		}
		// Right side binds one level tighter (left-associative).
		p.expr(n.R, prec+1)
		if paren {
			p.ch(')')
		}

	case *JsLogicalExpr:
		prec := binPrec(n.Op)
		paren := prec < parentPrec
		if paren {
			p.ch('(')
		}
		p.expr(n.L, prec)
		p.sp()
		p.write(n.Op)
		p.sp()
		p.expr(n.R, prec+1)
		if paren {
			p.ch(')')
		}

	case *JsAssignExpr:
		paren := parentPrec > precAssign
		if paren {
			p.ch('(')
		}
		p.expr(n.L, precLHS)
		p.sp()
		p.write(n.Op)
		p.sp()
		p.expr(n.R, precAssign)
		if paren {
			p.ch(')')
		}

	case *JsConditionalExpr:
		paren := parentPrec > precCond
		if paren {
			p.ch('(')
		}
		p.expr(n.Test, precCond+1)
		p.sp()
		p.ch('?')
		p.sp()
		p.expr(n.Then, precAssign)
		p.sp()
		p.ch(':')
		p.sp()
		p.expr(n.Else, precAssign)
		if paren {
			p.ch(')')
		}

	case *JsSeqExpr:
		paren := parentPrec > precSeq
		if paren {
			p.ch('(')
		}
		for i, x := range n.Exprs {
			if i > 0 {
				p.ch(',')
				p.sp()
			}
			p.expr(x, precAssign)
		}
		if paren {
			p.ch(')')
		}

	case *JsMemberExpr:
		if literal, ok := n.Obj.(*JsLiteral); ok && !n.Computed && !n.Optional && decimalIntegerLiteral(literal) {
			// "0.toFixed" would lex as the literal "0." followed by a name.
			p.ch('(')
			p.expr(n.Obj, 0)
			p.ch(')')
		} else {
			p.expr(n.Obj, precLHS)
		}
		if n.Computed {
			if n.Optional {
				p.write("?.")
			}
			p.ch('[')
			p.expr(n.Prop, 0)
			p.ch(']')
		} else {
			if n.Optional {
				p.write("?.")
			} else {
				p.ch('.')
			}
			p.expr(n.Prop, precPrimary)
		}

	case *JsCallExpr:
		p.expr(n.Callee, precLHS)
		if n.Optional {
			p.write("?.")
		}
		p.argList(n.Args)

	case *JsNewExpr:
		paren := parentPrec > precLHS
		if paren {
			p.ch('(')
		}
		p.write("new")
		p.reqSp()
		p.expr(n.Callee, precLHS)
		p.argList(n.Args)
		if paren {
			p.ch(')')
		}

	case *JsSpreadExpr:
		p.write("...")
		p.expr(n.X, precAssign)

	case *JsAwaitExpr:
		paren := parentPrec > precUnary
		if paren {
			p.ch('(')
		}
		p.write("await")
		p.reqSp()
		p.expr(n.X, precUnary)
		if paren {
			p.ch(')')
		}

	case *JsYieldExpr:
		paren := parentPrec > precAssign
		if paren {
			p.ch('(')
		}
		p.write("yield")
		if n.Delegate {
			p.ch('*')
		}
		if n.X != nil {
			p.reqSp()
			p.expr(n.X, precAssign)
		}
		if paren {
			p.ch(')')
		}

	default:
		// Unknown expression — emit nothing.  Round-trip test
		// catches missing cases.
	}
}

func (p *jsPrinter) objectExpr(n *JsObjectExpr) {
	if len(n.Props) == 0 {
		p.write("{}")
		return
	}
	p.ch('{')
	p.sp()
	for i, pr := range n.Props {
		if i > 0 {
			p.ch(',')
			p.sp()
		}
		if pr.Spread {
			p.write("...")
			p.expr(pr.Value, precAssign)
			continue
		}
		switch pr.Kind {
		case "get", "set":
			p.write(pr.Kind)
			p.reqSp()
			p.propKey(pr)
			if fn, ok := pr.Value.(*JsFuncExpr); ok {
				p.paramList(fn.Params)
				p.sp()
				p.block(fn.Body)
			}
			continue
		}
		if pr.Method {
			// Object-method shorthand: `key(params) { body }`.
			if fn, ok := pr.Value.(*JsFuncExpr); ok {
				if fn.Async {
					p.write("async")
					p.reqSp()
				}
				if fn.Generator {
					p.ch('*')
				}
				p.propKey(pr)
				p.paramList(fn.Params)
				p.sp()
				p.block(fn.Body)
				continue
			}
		}
		if pr.Shorthand {
			// A shorthand prop normally prints just the key.  But a
			// destructuring shorthand with a default — `{ a = 1 }` —
			// stores the default as a JsAssignExpr in Value; that
			// must be printed so the default survives the round trip.
			if asn, ok := pr.Value.(*JsAssignExpr); ok {
				p.expr(asn, precAssign)
			} else {
				p.propKey(pr)
			}
			continue
		}
		p.propKey(pr)
		p.ch(':')
		p.sp()
		p.expr(pr.Value, precAssign)
	}
	p.sp()
	p.ch('}')
}

func (p *jsPrinter) propKey(pr *JsProp) {
	if pr.Computed {
		p.ch('[')
		p.expr(pr.Key, precAssign)
		p.ch(']')
		return
	}
	p.expr(pr.Key, precPrimary)
}

func (p *jsPrinter) paramList(params []JsExpr) {
	p.ch('(')
	for i, par := range params {
		if i > 0 {
			p.ch(',')
			p.sp()
		}
		p.expr(par, precAssign)
	}
	p.ch(')')
}

func (p *jsPrinter) argList(args []JsExpr) {
	p.ch('(')
	for i, a := range args {
		if i > 0 {
			p.ch(',')
			p.sp()
		}
		p.expr(a, precAssign)
	}
	p.ch(')')
}

// ─────────────────────────────────────────────
//  Helpers
// ─────────────────────────────────────────────

// isWordOp reports whether op is an alphabetic operator that needs
// whitespace separators on both sides even in minified output.
func isWordOp(op string) bool {
	switch op {
	case "typeof", "void", "delete", "in", "instanceof", "await", "new":
		return true
	}
	return false
}

// needsUnarySpace reports whether a prefix symbol operator needs a
// space before its operand to avoid token merging — e.g. `- -x`
// must not become `--x`, `+ +x` must not become `++x`.
func needsUnarySpace(op string, operand JsExpr) bool {
	if op != "+" && op != "-" {
		return false
	}
	// If the operand starts with the same sign or a matching
	// unary, a separating space is required.
	switch u := operand.(type) {
	case *JsUnaryExpr:
		if u.Prefix && (u.Op == op || u.Op == op+op) {
			return true
		}
	case *JsLiteral:
		// `- -1` style — operand is a negative numeric literal.
		if len(u.Raw) > 0 && (u.Raw[0] == '+' || u.Raw[0] == '-') {
			return true
		}
	}
	return false
}

// exprStmtNeedsParens reports whether an expression used as a
// statement would be mis-parsed if printed bare — i.e. it begins
// with `{` (read as a block) or `function`/`class` (read as a
// declaration).
func exprStmtNeedsParens(e JsExpr) bool {
	switch leftmost(e).(type) {
	case *JsObjectExpr:
		return true
	case *JsFuncExpr:
		return true
	}
	return false
}

// leftmost returns the deepest left-edge expression — the node that
// will be printed first.  Used to detect statement-position
// ambiguity through call/member chains (e.g. `{a:1}.x` still starts
// with `{`).
func leftmost(e JsExpr) JsExpr {
	for {
		switch n := e.(type) {
		case *JsMemberExpr:
			e = n.Obj
		case *JsCallExpr:
			e = n.Callee
		case *JsBinaryExpr:
			e = n.L
		case *JsLogicalExpr:
			e = n.L
		case *JsAssignExpr:
			e = n.L
		case *JsConditionalExpr:
			e = n.Test
		case *JsSeqExpr:
			if len(n.Exprs) == 0 {
				return e
			}
			e = n.Exprs[0]
		default:
			return e
		}
	}
}

// EncodeGeneratedString serializes a decoded compiler-generated string as a
// single-quoted JavaScript string literal. The printer owns quote and control
// character escaping; parser-owned literals retain their original Raw spelling.
func EncodeGeneratedString(value string) string {
	quoted := strconv.Quote(value)
	content := quoted[1 : len(quoted)-1]
	content = strings.ReplaceAll(content, "'", "\\'")
	return "'" + content + "'"
}

// decimalIntegerLiteral reports a number literal spelled with decimal
// digits only, which a following dot would extend.
func decimalIntegerLiteral(literal *JsLiteral) bool {
	if literal.Kind != "number" || literal.Raw == "" {
		return false
	}
	for i := 0; i < len(literal.Raw); i++ {
		if c := literal.Raw[i]; (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}
