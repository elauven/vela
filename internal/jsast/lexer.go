package jsast

import (
	"fmt"
	"unicode"
	"unicode/utf8"
)

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
	TokLBrace   // {
	TokRBrace   // }
	TokLParen   // (
	TokRParen   // )
	TokLBracket // [
	TokRBracket // ]
	TokSemi     // ;
	TokColon    // :
	TokComma    // ,
	TokDot      // .
	TokEllipsis // ...
	TokQuestion // ?
	TokOptChain // ?.
	TokArrow    // =>

	// Assignment operators
	TokAssign         // =
	TokPlusAssign     // +=
	TokMinusAssign    // -=
	TokStarAssign     // *=
	TokSlashAssign    // /=
	TokModAssign      // %=
	TokStarStarAssign // **=
	TokBitAndAssign   // &=
	TokBitOrAssign    // |=
	TokBitXorAssign   // ^=
	TokLShiftAssign   // <<=
	TokRShiftAssign   // >>=
	TokURShiftAssign  // >>>=
	TokAndAssign      // &&=
	TokOrAssign       // ||=
	TokNullAssign     // ??=

	// Binary / logical operators
	TokOr         // ||
	TokAnd        // &&
	TokNullCoal   // ??
	TokBitOr      // |
	TokBitXor     // ^
	TokBitAnd     // &
	TokEqEq       // ==
	TokNotEq      // !=
	TokStrictEq   // ===
	TokStrictNeq  // !==
	TokLt         // <
	TokGt         // >
	TokLtEq       // <=
	TokGtEq       // >=
	TokLShift     // <<
	TokRShift     // >>
	TokURShift    // >>>
	TokPlus       // +
	TokMinus      // -
	TokStar       // *
	TokStarStar   // **
	TokSlash      // /
	TokMod        // %
	TokBang       // !
	TokTilde      // ~
	TokPlusPlus   // ++
	TokMinusMinus // --

	// Appended after the original kinds so existing numeric values are stable.
	TokPrivateName // #name (class private member / `#x in obj`)
	TokDebugger    // debugger
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
	"with": TokWith, "yield": TokYield, "debugger": TokDebugger,
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

	// NL reports that at least one line terminator (including one inside a
	// preceding multi-line comment) separates this token from the previous
	// one.  The parser uses it for automatic semicolon insertion and for the
	// "no LineTerminator here" restrictions (return, throw, break, continue,
	// yield, postfix ++/--, async).
	NL bool
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
	// ], ++, --, true, false, null, template, and ")" unless it closes the
	// header of an if/while/for/with).
	afterValue bool

	// parens records, for every currently open "(", whether it belongs to the
	// header of if / while / for / with.  After the matching ")" a "/" starts
	// a regex (a statement follows), otherwise it is a division.
	parens      []bool
	prev, prev2 TokKind // the two previously returned token kinds

	nl bool // a line terminator was skipped before the current token

	// onErr, when set, receives lexical errors (unterminated string, illegal
	// character, ...).  Scanning always continues and always makes progress.
	onErr func(pos int, msg string)
}

func NewLexer(src string) *Lexer { return &Lexer{src: src, line: 1} }

func (l *Lexer) errorf(pos int, format string, args ...any) {
	if l.onErr != nil {
		l.onErr(pos, fmt.Sprintf(format, args...))
	}
}

// clone returns an independent copy used for look-ahead.  It is silent: the
// original lexer reports the errors when it reaches the same input.
func (l *Lexer) clone() *Lexer {
	c := *l
	c.parens = append([]bool(nil), l.parens...)
	c.onErr = nil
	return &c
}

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

// lineTerminatorLen returns the byte length of the line terminator at i
// (\n, \r, U+2028, U+2029) or 0.
func (l *Lexer) lineTerminatorLen(i int) int {
	if i >= len(l.src) {
		return 0
	}
	switch l.src[i] {
	case '\n', '\r':
		return 1
	case 0xE2:
		if i+2 < len(l.src) && l.src[i+1] == 0x80 && (l.src[i+2] == 0xA8 || l.src[i+2] == 0xA9) {
			return 3
		}
	}
	return 0
}

// skipSpace skips whitespace and comments, recording whether a line
// terminator was crossed in l.nl.
func (l *Lexer) skipSpace() {
	// Hashbang: only valid at the very start of the source.
	if l.pos == 0 && len(l.src) >= 2 && l.src[0] == '#' && l.src[1] == '!' {
		for l.pos < len(l.src) && l.lineTerminatorLen(l.pos) == 0 {
			l.pos++
		}
	}
	for l.pos < len(l.src) {
		c := l.ch()
		switch {
		case c == ' ' || c == '\t' || c == '\v' || c == '\f':
			l.pos++
		case c == '\n' || c == '\r':
			l.nl = true
			l.adv()
		case c == '/' && l.peek1() == '/':
			for l.pos < len(l.src) && l.lineTerminatorLen(l.pos) == 0 {
				l.pos++
			}
		case c == '/' && l.peek1() == '*':
			open := l.pos
			l.pos += 2
			closed := false
			for l.pos < len(l.src) {
				if l.ch() == '*' && l.peek1() == '/' {
					l.pos += 2
					closed = true
					break
				}
				if n := l.lineTerminatorLen(l.pos); n > 0 {
					l.nl = true
					if n == 3 {
						l.pos += 3
						continue
					}
				}
				l.adv()
			}
			if !closed {
				l.errorf(open, "unterminated comment")
			}
		case c >= 0x80:
			r, n := utf8.DecodeRuneInString(l.src[l.pos:])
			if n == 3 && (r == '\u2028' || r == '\u2029') {
				l.nl = true
				l.pos += n
			} else if r == '\uFEFF' || unicode.Is(unicode.Zs, r) {
				l.pos += n
			} else {
				return
			}
		default:
			return
		}
	}
}

// Next returns the next token.  Every call consumes at least one byte unless
// the end of input has been reached, so callers cannot loop forever.
func (l *Lexer) Next() Token {
	l.nl = false
	l.skipSpace()
	nl := l.nl
	tok := l.scan()
	tok.NL = nl
	l.prev2, l.prev = l.prev, tok.Kind
	return tok
}

func (l *Lexer) scan() Token {
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
	if l.identLenAt(l.pos, true) > 0 {
		return l.scanIdent(start, line, false)
	}
	// ── Private names (#x) ────────────────────
	if c == '#' && l.identLenAt(l.pos+1, true) > 0 {
		l.pos++
		return l.scanIdent(start, line, true)
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
		av := k == TokIdent || k == TokPrivateName || k == TokNumber || k == TokString ||
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
		// `if (`, `while (`, `for (`, `for await (`, `with (` — but not a method
		// that merely has such a name: `obj.with(x) / 2` is a division.
		ctrl := (l.prev == TokIf || l.prev == TokWhile || l.prev == TokFor || l.prev == TokWith) &&
			l.prev2 != TokDot && l.prev2 != TokOptChain
		if l.prev == TokAwait && l.prev2 == TokFor {
			ctrl = true
		}
		l.parens = append(l.parens, ctrl)
		l.afterValue = false
		return mk(TokLParen)
	case ')':
		ctrl := false
		if n := len(l.parens); n > 0 {
			ctrl = l.parens[n-1]
			l.parens = l.parens[:n-1]
		}
		t := tok(TokRParen, l.pos)
		l.afterValue = !ctrl
		return t
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
			l.adv()
			l.adv()
			l.afterValue = false
			return tok(TokEllipsis, l.pos)
		}
		l.afterValue = false
		return mk(TokDot)

	case '?':
		if l.ch() == '.' && !isDigit(l.peek1()) {
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
		if !l.afterValue {
			return l.scanRegex(start, line)
		}
		if l.ch() == '=' {
			l.adv()
			l.afterValue = false
			return tok(TokSlashAssign, l.pos)
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
	for l.pos < len(l.src) && !utf8.RuneStart(l.src[l.pos]) {
		l.pos++ // swallow the rest of a multi-byte character
	}
	t := tok(TokIllegal, l.pos)
	l.errorf(start, "unexpected character %q", t.Val)
	return t
}

func (l *Lexer) scanDigits(valid func(byte) bool) {
	prevDigit := false
	for valid(l.ch()) || l.ch() == '_' {
		if l.ch() == '_' {
			// A numeric separator joins two digits: not after a prefix or
			// a dot, not doubled, not trailing (`0x_f`, `1__0`, `1_`).
			if !prevDigit || !valid(l.peek1()) {
				l.errorf(l.pos, "numeric separator must be between digits")
			}
			prevDigit = false
		} else {
			prevDigit = true
		}
		l.adv()
	}
}

func (l *Lexer) scanNumber(start, line int) Token {
	isBig := true
	c, n := l.ch(), l.peek1()
	switch {
	case c == '0' && (n == 'x' || n == 'X'):
		l.pos += 2
		l.scanDigits(isHexDigit)
		if l.pos == start+2 {
			l.errorf(start, "hexadecimal digit expected")
		}
	case c == '0' && (n == 'o' || n == 'O'):
		l.pos += 2
		l.scanDigits(func(b byte) bool { return b >= '0' && b <= '7' })
		if l.pos == start+2 {
			l.errorf(start, "octal digit expected")
		}
	case c == '0' && (n == 'b' || n == 'B'):
		l.pos += 2
		l.scanDigits(func(b byte) bool { return b == '0' || b == '1' })
		if l.pos == start+2 {
			l.errorf(start, "binary digit expected")
		}
	default:
		// decimal (also legacy octal such as 017): digits [. digits] [exp]
		l.scanDigits(isDigit)
		if l.ch() == '.' {
			// "1." and "1..toString()" are valid: the dot belongs to the number.
			l.adv()
			l.scanDigits(isDigit)
			isBig = false
		}
		if e := l.ch(); e == 'e' || e == 'E' {
			i := l.pos + 1
			if i < len(l.src) && (l.src[i] == '+' || l.src[i] == '-') {
				i++
			}
			if i < len(l.src) && isDigit(l.src[i]) {
				l.pos = i
				l.scanDigits(isDigit)
				isBig = false
			}
		}
	}
	if isBig && l.ch() == 'n' {
		l.adv()
	}
	// An identifier may not start immediately after a numeric literal.
	if l.identLenAt(l.pos, true) > 0 {
		l.errorf(start, "identifier starts immediately after numeric literal")
	}
	l.afterValue = true
	return Token{Kind: TokNumber, Val: l.src[start:l.pos], Pos: start, End: l.pos, Line: line}
}

// identLenAt returns the byte length of the identifier character at offset i
// (0 if there is none).  start selects ID_Start rules, otherwise ID_Continue.
// \uXXXX and \u{X…} escapes count as identifier characters.
func (l *Lexer) identLenAt(i int, start bool) int {
	if i >= len(l.src) {
		return 0
	}
	c := l.src[i]
	if c < utf8.RuneSelf {
		if IsIdentStart(c) || (!start && isDigit(c)) {
			return 1
		}
		if c == '\\' && i+1 < len(l.src) && l.src[i+1] == 'u' {
			return l.unicodeEscapeLen(i)
		}
		return 0
	}
	r, n := utf8.DecodeRuneInString(l.src[i:])
	if r == utf8.RuneError && n <= 1 {
		return 0
	}
	if unicode.IsLetter(r) || unicode.Is(unicode.Nl, r) || unicode.Is(unicode.Other_ID_Start, r) {
		return n
	}
	if !start && (unicode.In(r, unicode.Mn, unicode.Mc, unicode.Nd, unicode.Pc) ||
		unicode.Is(unicode.Other_ID_Continue, r) || r == '\u200C' || r == '\u200D') {
		return n
	}
	return 0
}

// unicodeEscapeLen measures a \uXXXX or \u{X…} escape starting at i.
func (l *Lexer) unicodeEscapeLen(i int) int {
	j := i + 2
	if j < len(l.src) && l.src[j] == '{' {
		k := j + 1
		for k < len(l.src) && isHexDigit(l.src[k]) {
			k++
		}
		if k > j+1 && k < len(l.src) && l.src[k] == '}' {
			return k + 1 - i
		}
		return 0
	}
	if j+4 <= len(l.src) && isHexDigit(l.src[j]) && isHexDigit(l.src[j+1]) && isHexDigit(l.src[j+2]) && isHexDigit(l.src[j+3]) {
		return 6
	}
	return 0
}

// scanIdent scans an identifier, keyword or (private == true, l.pos just past
// the '#') private name.
func (l *Lexer) scanIdent(start, line int, private bool) Token {
	escaped := false
	for {
		n := l.identLenAt(l.pos, false)
		if n == 0 {
			break
		}
		if l.src[l.pos] == '\\' {
			escaped = true
		}
		l.pos += n
	}
	val := l.src[start:l.pos]
	if private {
		l.afterValue = true
		return Token{Kind: TokPrivateName, Val: val, Pos: start, End: l.pos, Line: line}
	}
	kind := TokIdent
	if k, ok := KeywordMap[val]; ok && !escaped {
		kind = k
	}
	isVal := kind == TokIdent || kind == TokTrue || kind == TokFalse ||
		kind == TokNull || kind == TokThis || kind == TokSuper
	l.afterValue = isVal
	return Token{Kind: kind, Val: val, Pos: start, End: l.pos, Line: line}
}

func (l *Lexer) scanString(start, line int) Token {
	q := l.adv()
	closed := false
	for l.pos < len(l.src) {
		c := l.ch()
		if c == '\n' || c == '\r' {
			break // unescaped line terminator: unterminated
		}
		l.adv()
		if c == '\\' {
			if l.pos < len(l.src) {
				if l.ch() == '\r' && l.peek1() == '\n' {
					l.pos++ // \r\n line continuation
				}
				l.adv()
			}
		} else if c == q {
			closed = true
			break
		}
	}
	if !closed {
		l.errorf(start, "unterminated string literal")
	}
	l.afterValue = true
	return Token{Kind: TokString, Val: l.src[start:l.pos], Pos: start, End: l.pos, Line: line}
}

// scanHole is called with l.pos just after "${".  It consumes tokens up to and
// including the matching "}" and returns that brace's offset (-1 if the input
// ended first).  Using the real tokenizer means strings, comments, regexes and
// nested templates inside the hole are skipped correctly.
func (l *Lexer) scanHole() int {
	savedAV, savedParens, savedPrev, savedPrev2 := l.afterValue, l.parens, l.prev, l.prev2
	l.afterValue, l.parens, l.prev, l.prev2 = false, nil, TokIllegal, TokIllegal
	defer func() { l.afterValue, l.parens, l.prev, l.prev2 = savedAV, savedParens, savedPrev, savedPrev2 }()
	depth := 1
	for {
		t := l.Next()
		switch t.Kind {
		case TokLBrace:
			depth++
		case TokRBrace:
			depth--
			if depth == 0 {
				return t.Pos
			}
		case TokEOF:
			return -1
		}
	}
}

// scanTemplate consumes an entire template literal including ${...} holes.
func (l *Lexer) scanTemplate(start, line int) Token {
	l.adv() // consume `
	closed := false
scan:
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
				if l.scanHole() < 0 {
					break scan // unterminated hole: input is exhausted
				}
			}
		case '`':
			closed = true
			break scan
		}
	}
	if !closed {
		l.errorf(start, "unterminated template literal")
	}
	l.afterValue = true
	return Token{Kind: TokTemplate, Val: l.src[start:l.pos], Pos: start, End: l.pos, Line: line}
}

func (l *Lexer) scanRegex(start, line int) Token {
	inClass := false
	closed := false
loop:
	for l.pos < len(l.src) {
		if l.lineTerminatorLen(l.pos) > 0 {
			break // regex literals cannot span lines
		}
		c := l.adv()
		switch c {
		case '\\':
			if l.pos < len(l.src) && l.lineTerminatorLen(l.pos) == 0 {
				l.adv()
			}
		case '[':
			inClass = true
		case ']':
			inClass = false
		case '/':
			if !inClass {
				// consume flags
				for l.identLenAt(l.pos, false) > 0 {
					l.pos += l.identLenAt(l.pos, false)
				}
				closed = true
				break loop
			}
		}
	}
	if !closed {
		l.errorf(start, "unterminated regular expression literal")
	}
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

// String returns the source text of a keyword / punctuator kind (or a short
// description for literal kinds); used in diagnostics.
func (k TokKind) String() string {
	switch k {
	case TokIllegal:
		return "illegal character"
	case TokEOF:
		return "end of input"
	case TokIdent:
		return "identifier"
	case TokNumber:
		return "number"
	case TokString:
		return "string"
	case TokTemplate:
		return "template literal"
	case TokRegex:
		return "regular expression"
	case TokPrivateName:
		return "private name"
	}
	for w, kw := range KeywordMap {
		if kw == k {
			return w
		}
	}
	if s, ok := punctText[k]; ok {
		return s
	}
	return fmt.Sprintf("token(%d)", uint8(k))
}

var punctText = map[TokKind]string{
	TokLBrace: "{", TokRBrace: "}", TokLParen: "(", TokRParen: ")", TokLBracket: "[", TokRBracket: "]",
	TokSemi: ";", TokColon: ":", TokComma: ",", TokDot: ".", TokEllipsis: "...", TokQuestion: "?",
	TokOptChain: "?.", TokArrow: "=>", TokAssign: "=",
}
