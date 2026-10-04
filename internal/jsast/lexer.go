package jsast

// ──────────────────────────────────────────────
//  Token kinds
// ──────────────────────────────────────────────

type TokKind uint8

const (
	// Literals
	TokIllegal TokKind = iota
	TokEOF
	TokIdent
	TokNumber
	TokString
	TokTemplate // complete backtick literal, raw text preserved
	TokRegex

	// Contextual / true keywords
	TokAs
	TokAsync
	TokAwait
	TokBreak
	TokCase
	TokCatch
	TokClass
	TokConst
	TokContinue
	TokDefault
	TokDelete
	TokDo
	TokElse
	TokExport
	TokExtends
	TokFalse
	TokFinally
	TokFor
	TokFrom
	TokFunction
	TokIf
	TokImport
	TokIn
	TokInstanceof
	TokLet
	TokNew
	TokNull
	TokOf
	TokReturn
	TokStatic
	TokSuper
	TokSwitch
	TokThis
	TokThrow
	TokTrue
	TokTry
	TokTypeof
	TokVar
	TokVoid
	TokWhile
	TokWith
	TokYield

	// Punctuation
	TokLBrace    // {
	TokRBrace    // }
	TokLParen    // (
	TokRParen    // )
	TokLBracket  // [
	TokRBracket  // ]
	TokSemi      // ;
	TokColon     // :
	TokComma     // ,
	TokDot       // .
	TokEllipsis  // ...
	TokQuestion  // ?
	TokOptChain  // ?.
	TokArrow     // =>

	// Assignment operators
	TokAssign        // =
	TokPlusAssign    // +=
	TokMinusAssign   // -=
	TokStarAssign    // *=
	TokSlashAssign   // /=
	TokModAssign     // %=
	TokStarStarAssign // **=
	TokBitAndAssign  // &=
	TokBitOrAssign   // |=
	TokBitXorAssign  // ^=
	TokLShiftAssign  // <<=
	TokRShiftAssign  // >>=
	TokURShiftAssign // >>>=
	TokAndAssign     // &&=
	TokOrAssign      // ||=
	TokNullAssign    // ??=

	// Binary / logical operators
	TokOr        // ||
	TokAnd       // &&
	TokNullCoal  // ??
	TokBitOr     // |
	TokBitXor    // ^
	TokBitAnd    // &
	TokEqEq      // ==
	TokNotEq     // !=
	TokStrictEq  // ===
	TokStrictNeq // !==
	TokLt        // <
	TokGt        // >
	TokLtEq      // <=
	TokGtEq      // >=
	TokLShift    // <<
	TokRShift    // >>
	TokURShift   // >>>
	TokPlus      // +
	TokMinus     // -
	TokStar      // *
	TokStarStar  // **
	TokSlash     // /
	TokMod       // %
	TokBang      // !
	TokTilde     // ~
	TokPlusPlus  // ++
	TokMinusMinus // --
)

var KeywordMap = map[string]TokKind{
	"as": TokAs, "async": TokAsync, "await": TokAwait,
	"break": TokBreak, "case": TokCase, "catch": TokCatch,
	"class": TokClass, "const": TokConst, "continue": TokContinue,
	"default": TokDefault, "delete": TokDelete, "do": TokDo,
	"else": TokElse, "export": TokExport, "extends": TokExtends,
	"false": TokFalse, "finally": TokFinally, "for": TokFor,
	"from": TokFrom, "function": TokFunction, "if": TokIf,
	"import": TokImport, "in": TokIn, "instanceof": TokInstanceof,
	"let": TokLet, "new": TokNew, "null": TokNull, "of": TokOf,
	"return": TokReturn, "static": TokStatic, "super": TokSuper,
	"switch": TokSwitch, "this": TokThis, "throw": TokThrow,
	"true": TokTrue, "try": TokTry, "typeof": TokTypeof,
	"var": TokVar, "void": TokVoid, "while": TokWhile,
	"with": TokWith, "yield": TokYield,
}

// ──────────────────────────────────────────────
//  Token
// ──────────────────────────────────────────────

type Token struct {
	Kind TokKind
	Val  string // raw source text of this token
	Pos  int    // byte offset of first character
	End  int    // byte offset one past last character
	Line int
}

// ──────────────────────────────────────────────
//  Lexer
// ──────────────────────────────────────────────

type Lexer struct {
	src  string
	pos  int
	line int

	// Context for regex disambiguation:
	// true after a token that can end an expression (ident, number, string,
	// ), ], ++, --, true, false, null, template).
	afterValue bool
}

func NewLexer(src string) *Lexer { return &Lexer{src: src, line: 1} }

func (l *Lexer) ch() byte {
	if l.pos >= len(l.src) {
		return 0
	}
	return l.src[l.pos]
}
func (l *Lexer) peek1() byte {
	if l.pos+1 >= len(l.src) {
		return 0
	}
	return l.src[l.pos+1]
}
func (l *Lexer) adv() byte {
	c := l.src[l.pos]
	l.pos++
	if c == '\n' {
		l.line++
	}
	return c
}

func (l *Lexer) skipSpace() {
	for l.pos < len(l.src) {
		c := l.ch()
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			l.adv()
		} else if c == '/' && l.peek1() == '/' {
			for l.pos < len(l.src) && l.ch() != '\n' {
				l.adv()
			}
		} else if c == '/' && l.peek1() == '*' {
			l.adv(); l.adv()
			for l.pos < len(l.src) {
				if l.ch() == '*' && l.peek1() == '/' {
					l.adv(); l.adv()
					break
				}
				l.adv()
			}
		} else {
			break
		}
	}
}

func (l *Lexer) Next() Token {
	l.skipSpace()
	if l.pos >= len(l.src) {
		return Token{Kind: TokEOF, Pos: l.pos, End: l.pos, Line: l.line}
	}

	start := l.pos
	line := l.line
	c := l.ch()

	// ── Numbers ───────────────────────────────
	if isDigit(c) || (c == '.' && isDigit(l.peek1())) {
		return l.scanNumber(start, line)
	}
	// ── Identifiers / keywords ────────────────
	if IsIdentStart(c) {
		return l.scanIdent(start, line)
	}
	// ── Strings ───────────────────────────────
	if c == '"' || c == '\'' {
		return l.scanString(start, line)
	}
	// ── Template literals ─────────────────────
	if c == '`' {
		return l.scanTemplate(start, line)
	}

	l.adv()
	tok := func(k TokKind, end int) Token {
		av := k == TokIdent || k == TokNumber || k == TokString ||
			k == TokTemplate || k == TokRParen || k == TokRBracket ||
			k == TokPlusPlus || k == TokMinusMinus ||
			k == TokTrue || k == TokFalse || k == TokNull || k == TokRegex
		l.afterValue = av
		return Token{Kind: k, Val: l.src[start:end], Pos: start, End: end, Line: line}
	}
	mk := func(k TokKind) Token { return tok(k, l.pos) }

	switch c {
	case '{':
		l.afterValue = false
		return mk(TokLBrace)
	case '}':
		return tok(TokRBrace, l.pos)
	case '(':
		l.afterValue = false
		return mk(TokLParen)
	case ')':
		return tok(TokRParen, l.pos)
	case '[':
		l.afterValue = false
		return mk(TokLBracket)
	case ']':
		return tok(TokRBracket, l.pos)
	case ';':
		l.afterValue = false
		return mk(TokSemi)
	case ':':
		l.afterValue = false
		return mk(TokColon)
	case ',':
		l.afterValue = false
		return mk(TokComma)
	case '~':
		l.afterValue = false
		return mk(TokTilde)

	case '.':
		if l.ch() == '.' && l.peek1() == '.' {
			l.adv(); l.adv()
			l.afterValue = false
			return tok(TokEllipsis, l.pos)
		}
		l.afterValue = false
		return mk(TokDot)

	case '?':
		if l.ch() == '.' {
			l.adv()
			return tok(TokOptChain, l.pos)
		}
		if l.ch() == '?' {
			l.adv()
			if l.ch() == '=' {
				l.adv()
				l.afterValue = false
				return tok(TokNullAssign, l.pos)
			}
			l.afterValue = false
			return tok(TokNullCoal, l.pos)
		}
		l.afterValue = false
		return mk(TokQuestion)

	case '+':
		if l.ch() == '+' {
			l.adv()
			return tok(TokPlusPlus, l.pos)
		}
		if l.ch() == '=' {
			l.adv()
			l.afterValue = false
			return tok(TokPlusAssign, l.pos)
		}
		l.afterValue = false
		return mk(TokPlus)

	case '-':
		if l.ch() == '-' {
			l.adv()
			return tok(TokMinusMinus, l.pos)
		}
		if l.ch() == '=' {
			l.adv()
			l.afterValue = false
			return tok(TokMinusAssign, l.pos)
		}
		l.afterValue = false
		return mk(TokMinus)

	case '*':
		if l.ch() == '*' {
			l.adv()
			if l.ch() == '=' {
				l.adv()
				l.afterValue = false
				return tok(TokStarStarAssign, l.pos)
			}
			l.afterValue = false
			return tok(TokStarStar, l.pos)
		}
		if l.ch() == '=' {
			l.adv()
			l.afterValue = false
			return tok(TokStarAssign, l.pos)
		}
		l.afterValue = false
		return mk(TokStar)

	case '/':
		if l.ch() == '=' {
			l.adv()
			l.afterValue = false
			return tok(TokSlashAssign, l.pos)
		}
		if !l.afterValue {
			return l.scanRegex(start, line)
		}
		l.afterValue = false
		return mk(TokSlash)

	case '%':
		if l.ch() == '=' {
			l.adv()
			l.afterValue = false
			return tok(TokModAssign, l.pos)
		}
		l.afterValue = false
		return mk(TokMod)

	case '&':
		if l.ch() == '&' {
			l.adv()
			if l.ch() == '=' {
				l.adv()
				l.afterValue = false
				return tok(TokAndAssign, l.pos)
			}
			l.afterValue = false
			return tok(TokAnd, l.pos)
		}
		if l.ch() == '=' {
			l.adv()
			l.afterValue = false
			return tok(TokBitAndAssign, l.pos)
		}
		l.afterValue = false
		return mk(TokBitAnd)

	case '|':
		if l.ch() == '|' {
			l.adv()
			if l.ch() == '=' {
				l.adv()
				l.afterValue = false
				return tok(TokOrAssign, l.pos)
			}
			l.afterValue = false
			return tok(TokOr, l.pos)
		}
		if l.ch() == '=' {
			l.adv()
			l.afterValue = false
			return tok(TokBitOrAssign, l.pos)
		}
		l.afterValue = false
		return mk(TokBitOr)

	case '^':
		if l.ch() == '=' {
			l.adv()
			l.afterValue = false
			return tok(TokBitXorAssign, l.pos)
		}
		l.afterValue = false
		return mk(TokBitXor)

	case '!':
		if l.ch() == '=' {
			l.adv()
			if l.ch() == '=' {
				l.adv()
				l.afterValue = false
				return tok(TokStrictNeq, l.pos)
			}
			l.afterValue = false
			return tok(TokNotEq, l.pos)
		}
		l.afterValue = false
		return mk(TokBang)

	case '<':
		if l.ch() == '<' {
			l.adv()
			if l.ch() == '=' {
				l.adv()
				l.afterValue = false
				return tok(TokLShiftAssign, l.pos)
			}
			l.afterValue = false
			return tok(TokLShift, l.pos)
		}
		if l.ch() == '=' {
			l.adv()
			l.afterValue = false
			return tok(TokLtEq, l.pos)
		}
		l.afterValue = false
		return mk(TokLt)

	case '>':
		if l.ch() == '>' {
			l.adv()
			if l.ch() == '>' {
				l.adv()
				if l.ch() == '=' {
					l.adv()
					l.afterValue = false
					return tok(TokURShiftAssign, l.pos)
				}
				l.afterValue = false
				return tok(TokURShift, l.pos)
			}
			if l.ch() == '=' {
				l.adv()
				l.afterValue = false
				return tok(TokRShiftAssign, l.pos)
			}
			l.afterValue = false
			return tok(TokRShift, l.pos)
		}
		if l.ch() == '=' {
			l.adv()
			l.afterValue = false
			return tok(TokGtEq, l.pos)
		}
		l.afterValue = false
		return mk(TokGt)

	case '=':
		if l.ch() == '>' {
			l.adv()
			l.afterValue = false
			return tok(TokArrow, l.pos)
		}
		if l.ch() == '=' {
			l.adv()
			if l.ch() == '=' {
				l.adv()
				l.afterValue = false
				return tok(TokStrictEq, l.pos)
			}
			l.afterValue = false
			return tok(TokEqEq, l.pos)
		}
		l.afterValue = false
		return mk(TokAssign)
	}

	l.afterValue = false
	return tok(TokIllegal, l.pos)
}

func (l *Lexer) scanNumber(start, line int) Token {
	c := l.ch()
	if c == '0' {
		l.adv()
		switch l.ch() {
		case 'x', 'X':
			l.adv()
			for isHexDigit(l.ch()) || l.ch() == '_' {
				l.adv()
			}
		case 'o', 'O':
			l.adv()
			for (l.ch() >= '0' && l.ch() <= '7') || l.ch() == '_' {
				l.adv()
			}
		case 'b', 'B':
			l.adv()
			for l.ch() == '0' || l.ch() == '1' || l.ch() == '_' {
				l.adv()
			}
		default:
			for isDigit(l.ch()) || l.ch() == '_' {
				l.adv()
			}
			l.scanDecimalTail()
		}
	} else if c == '.' {
		l.adv()
		for isDigit(l.ch()) {
			l.adv()
		}
		l.scanExp()
	} else {
		for isDigit(l.ch()) || l.ch() == '_' {
			l.adv()
		}
		l.scanDecimalTail()
	}
	if l.ch() == 'n' {
		l.adv()
	}
	l.afterValue = true
	return Token{Kind: TokNumber, Val: l.src[start:l.pos], Pos: start, End: l.pos, Line: line}
}

func (l *Lexer) scanDecimalTail() {
	if l.ch() == '.' && isDigit(l.peek1()) {
		l.adv()
		for isDigit(l.ch()) {
			l.adv()
		}
		l.scanExp()
	} else {
		l.scanExp()
	}
}

func (l *Lexer) scanExp() {
	if l.ch() == 'e' || l.ch() == 'E' {
		l.adv()
		if l.ch() == '+' || l.ch() == '-' {
			l.adv()
		}
		for isDigit(l.ch()) {
			l.adv()
		}
	}
}

func (l *Lexer) scanIdent(start, line int) Token {
	for IsIdentCont(l.ch()) {
		l.adv()
	}
	val := l.src[start:l.pos]
	kind := TokIdent
	if k, ok := KeywordMap[val]; ok {
		kind = k
	}
	isVal := kind == TokIdent || kind == TokTrue || kind == TokFalse ||
		kind == TokNull || kind == TokThis || kind == TokSuper
	l.afterValue = isVal
	return Token{Kind: kind, Val: val, Pos: start, End: l.pos, Line: line}
}

func (l *Lexer) scanString(start, line int) Token {
	q := l.adv()
	for l.pos < len(l.src) {
		c := l.adv()
		if c == '\\' {
			if l.pos < len(l.src) {
				l.adv()
			}
		} else if c == q {
			break
		}
	}
	l.afterValue = true
	return Token{Kind: TokString, Val: l.src[start:l.pos], Pos: start, End: l.pos, Line: line}
}

// scanTemplate consumes an entire template literal including ${...} holes.
// It tracks brace depth to handle nested braces inside expressions.
func (l *Lexer) scanTemplate(start, line int) Token {
	l.adv() // consume `
	depth := 0
	for l.pos < len(l.src) {
		c := l.adv()
		switch c {
		case '\\':
			if l.pos < len(l.src) {
				l.adv()
			}
		case '$':
			if l.ch() == '{' {
				l.adv()
				depth++
			}
		case '{':
			if depth > 0 {
				depth++
			}
		case '}':
			if depth > 0 {
				depth--
			}
		case '`':
			if depth == 0 {
				l.afterValue = true
				return Token{Kind: TokTemplate, Val: l.src[start:l.pos], Pos: start, End: l.pos, Line: line}
			}
		}
	}
	l.afterValue = true
	return Token{Kind: TokTemplate, Val: l.src[start:l.pos], Pos: start, End: l.pos, Line: line}
}

func (l *Lexer) scanRegex(start, line int) Token {
	inClass := false
	for l.pos < len(l.src) {
		c := l.adv()
		switch c {
		case '\\':
			if l.pos < len(l.src) {
				l.adv()
			}
		case '[':
			inClass = true
		case ']':
			inClass = false
		case '/':
			if !inClass {
				// consume flags
				for IsIdentCont(l.ch()) {
					l.adv()
				}
				l.afterValue = true
				return Token{Kind: TokRegex, Val: l.src[start:l.pos], Pos: start, End: l.pos, Line: line}
			}
		case '\n':
			goto done
		}
	}
done:
	l.afterValue = true
	return Token{Kind: TokRegex, Val: l.src[start:l.pos], Pos: start, End: l.pos, Line: line}
}

// ──────────────────────────────────────────────
//  Character helpers
// ──────────────────────────────────────────────

func isDigit(c byte) bool    { return c >= '0' && c <= '9' }
func isHexDigit(c byte) bool { return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') }
func IsIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func IsIdentCont(c byte) bool { return IsIdentStart(c) || isDigit(c) }

// isAssignOp reports whether k is any assignment operator.
func isAssignOp(k TokKind) bool {
	switch k {
	case TokAssign, TokPlusAssign, TokMinusAssign, TokStarAssign,
		TokSlashAssign, TokModAssign, TokStarStarAssign,
		TokBitAndAssign, TokBitOrAssign, TokBitXorAssign,
		TokLShiftAssign, TokRShiftAssign, TokURShiftAssign,
		TokAndAssign, TokOrAssign, TokNullAssign:
		return true
	}
	return false
}

// isContextualIdent returns true for keywords that are valid as identifiers
// in expression position (contextual keywords).
func isContextualIdent(k TokKind) bool {
	switch k {
	case TokAs, TokAsync, TokFrom, TokOf, TokStatic, TokLet:
		return true
	}
	return false
}
