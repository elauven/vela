package jsast

// Lexer tests: token stream for representative inputs.
//
// A token is rendered by ctTokRepr:
//
//	identifier → id:NAME     number → num:TEXT   string → str:TEXT
//	template   → tpl:TEXT    regex  → re:TEXT    illegal → ILLEGAL:TEXT
//	everything else (keywords, punctuation, operators) → its source text
//
// which keeps the expectations readable and independent of TokKind numbering.

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func ctTokRepr(t Token) string {
	switch t.Kind {
	case TokIdent:
		return "id:" + t.Val
	case TokNumber:
		return "num:" + t.Val
	case TokString:
		return "str:" + t.Val
	case TokTemplate:
		return "tpl:" + t.Val
	case TokRegex:
		return "re:" + t.Val
	case TokIllegal:
		return "ILLEGAL:" + t.Val
	}
	return t.Val
}

// ctLex tokenises src (with a guard against non-terminating lexers).
func ctLex(src string) (toks []Token, hang bool) {
	l := NewLexer(src)
	for i := 0; i < 100000; i++ {
		tk := l.Next()
		if tk.Kind == TokEOF {
			return toks, false
		}
		toks = append(toks, tk)
	}
	return toks, true
}

func ctLexRepr(src string) string {
	toks, hang := ctLex(src)
	if hang {
		return "<lexer did not terminate>"
	}
	parts := make([]string, len(toks))
	for i, t := range toks {
		parts[i] = ctTokRepr(t)
	}
	return strings.Join(parts, " ")
}

// ctTokNL reads Token.NL ("a line terminator precedes this token") when the
// field exists; older revisions of the lexer do not track it.
func ctTokNL(t Token) (val, supported bool) {
	f := reflect.ValueOf(t).FieldByName("NL")
	if !f.IsValid() {
		return false, false
	}
	return f.Bool(), true
}

type ctLexCase struct {
	Name string
	Src  string
	Want string
	Gap  string
}

func ctRunLexCases(t *testing.T, group string, cases []ctLexCase) {
	t.Helper()
	for _, c := range cases {
		c := c
		t.Run(group+"/"+c.Name, func(t *testing.T) {
			got := ctLexRepr(c.Src)
			err := ""
			if got != c.Want {
				err = fmt.Sprintf("tokens differ\n      got:  %s\n      want: %s", got, c.Want)
			}
			ctRecord(ctResult{ID: "Lexer/" + group + "/" + c.Name, Group: "Lexer", Name: group + "/" + c.Name, Pass: err == "", Gap: c.Gap, Err: err, Src: c.Src})
			ctVerdict(t, c.Src, c.Gap, err)
		})
	}
}

func TestCompatLexerOperators(t *testing.T) {
	// Every operator / punctuator, preceded by an identifier so `/` and `/=`
	// are lexed as operators rather than regex starts.
	ops := []string{
		"{", "}", "(", ")", "[", "]", ";", ":", ",", ".", "...", "?", "?.", "=>",
		"=", "+=", "-=", "*=", "/=", "%=", "**=", "&=", "|=", "^=", "<<=", ">>=", ">>>=", "&&=", "||=", "??=",
		"||", "&&", "??", "|", "^", "&", "==", "!=", "===", "!==", "<", ">", "<=", ">=",
		"<<", ">>", ">>>", "+", "-", "*", "**", "/", "%", "!", "~", "++", "--",
	}
	for _, op := range ops {
		op := op
		t.Run("op "+op, func(t *testing.T) {
			got := ctLexRepr("a " + op)
			if want := "id:a " + op; got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestCompatLexerKeywords(t *testing.T) {
	for word, kind := range KeywordMap {
		word, kind := word, kind
		t.Run(word, func(t *testing.T) {
			toks, _ := ctLex(word)
			if len(toks) != 1 || toks[0].Kind != kind || toks[0].Val != word {
				t.Errorf("%q lexed as %+v, want single token kind %d", word, toks, kind)
			}
		})
	}
	// Identifiers that merely start with / contain a keyword stay identifiers.
	for _, id := range []string{"format", "iffy", "asyncFoo", "letter", "newValue", "of_", "$in", "_do", "returned"} {
		if got := ctLexRepr(id); got != "id:"+id {
			t.Errorf("%q → %q, want identifier", id, got)
		}
	}
}

func TestCompatLexerTokens(t *testing.T) {
	ctRunLexCases(t, "basic", []ctLexCase{
		{Name: "arithmetic", Src: `a+b*c`, Want: `id:a + id:b * id:c`},
		{Name: "increments", Src: `a++ + ++b`, Want: `id:a ++ + ++ id:b`},
		{Name: "ushift assign", Src: `a>>>=b`, Want: `id:a >>>= id:b`},
		{Name: "arrow", Src: `a=>b`, Want: `id:a => id:b`},
		{Name: "optional chain", Src: `a?.b?.[0]?.()`, Want: `id:a ?. id:b ?. [ num:0 ] ?. ( )`},
		{Name: "nullish assign", Src: `a??=b`, Want: `id:a ??= id:b`},
		{Name: "spread", Src: `[...a]`, Want: `[ ... id:a ]`},
		{Name: "comments are skipped", Src: "a/*x*/+/**/b//c\n+d", Want: `id:a + id:b + id:d`},
		{Name: "unterminated block comment ends input", Src: "a /* x", Want: `id:a`},
		{Name: "illegal characters", Src: `a @ b`, Want: `id:a ILLEGAL:@ id:b`},
	})
	ctRunLexCases(t, "numbers", []ctLexCase{
		{Name: "decimal / hex / bin / octal", Src: `12 0x1f 0b11 0o7`, Want: `num:12 num:0x1f num:0b11 num:0o7`},
		{Name: "fraction / exponent", Src: `.5 1.5 1e3 1E-3 2.5e+2`, Want: `num:.5 num:1.5 num:1e3 num:1E-3 num:2.5e+2`},
		{Name: "bigint", Src: `1n 0xffn`, Want: `num:1n num:0xffn`},
		{Name: "separators in integer part", Src: `1_000 0xFF_FF`, Want: `num:1_000 num:0xFF_FF`},
		{Name: "separators in fraction", Src: `3.14_15`, Want: `num:3.14_15`},
		{Name: "trailing dot", Src: `5.`, Want: `num:5.`},
		{Name: "member access on literal", Src: `1..toString()`, Want: `num:1. . id:toString ( )`},
		{Name: "dot then digit is fraction", Src: `a.b .5`, Want: `id:a . id:b num:.5`},
	})
	ctRunLexCases(t, "strings", []ctLexCase{
		{Name: "single and double quotes", Src: `'a' "b"`, Want: `str:'a' str:"b"`},
		{Name: "escaped quote", Src: `'a\'b' "c\"d"`, Want: `str:'a\'b' str:"c\"d"`},
		{Name: "line continuation", Src: "'a\\\nb'", Want: "str:'a\\\nb'"},
		{Name: "unicode escapes", Src: `'\u{1F600}\u00e9\x41'`, Want: `str:'\u{1F600}\u00e9\x41'`},
		{Name: "other quote inside", Src: `"it's" 'say "hi"'`, Want: `str:"it's" str:'say "hi"'`},
	})
	ctRunLexCases(t, "templates", []ctLexCase{
		{Name: "no holes", Src: "`abc`", Want: "tpl:`abc`"},
		{Name: "hole", Src: "`a${b}c`", Want: "tpl:`a${b}c`"},
		{Name: "object literal in hole", Src: "`${ {a:1}.a }` x", Want: "tpl:`${ {a:1}.a }` id:x"},
		{Name: "nested template", Src: "`a${`b${c}`}d` x", Want: "tpl:`a${`b${c}`}d` id:x"},
		{Name: "escaped backtick", Src: "`a\\`b` x", Want: "tpl:`a\\`b` id:x"},
		{Name: "escaped dollar", Src: "`\\${a}` x", Want: "tpl:`\\${a}` id:x"},
		{Name: "lone dollar", Src: "`$a $ {b}` x", Want: "tpl:`$a $ {b}` id:x"},
		{Name: "closing brace inside string in hole", Src: "`${ '}' }` x", Want: "tpl:`${ '}' }` id:x"},
		{Name: "backtick inside string in hole", Src: "`${ '`' }` x", Want: "tpl:`${ '`' }` id:x"},
		{Name: "comment with brace in hole", Src: "`${ a /* } */ }` x", Want: "tpl:`${ a /* } */ }` id:x"},
		{Name: "regex with brace in hole", Src: "`${ /}/.test(a) }` x", Want: "tpl:`${ /}/.test(a) }` id:x"},
		{Name: "division after template", Src: "`a` / 2", Want: "tpl:`a` / num:2"},
	})
	ctRunLexCases(t, "regex-vs-division", []ctLexCase{
		{Name: "after identifier is division", Src: `a / b / c`, Want: `id:a / id:b / id:c`},
		{Name: "after number is division", Src: `1 / 2`, Want: `num:1 / num:2`},
		{Name: "after ) is division", Src: `(a) / 2`, Want: `( id:a ) / num:2`},
		{Name: "after ] is division", Src: `a[0] / 2`, Want: `id:a [ num:0 ] / num:2`},
		{Name: "after ++ is division", Src: `a++ / 2`, Want: `id:a ++ / num:2`},
		{Name: "after operator is regex", Src: `x = /a/g`, Want: `id:x = re:/a/g`},
		{Name: "after ( is regex", Src: `f(/a/)`, Want: `id:f ( re:/a/ )`},
		{Name: "after return is regex", Src: `return /a/`, Want: `return re:/a/`},
		{Name: "after => is regex", Src: `x => /a/`, Want: `id:x => re:/a/`},
		{Name: "class with slash", Src: `x = /[/]/`, Want: `id:x = re:/[/]/`},
		{Name: "escaped slash", Src: `x = /a\/b/g`, Want: `id:x = re:/a\/b/g`},
		{Name: "regex starting with =", Src: `x = /=/g`, Want: `id:x = re:/=/g`},
		{Name: "regex arg starting with =", Src: `s.split(/=/)`, Want: `id:s . id:split ( re:/=/ )`},
		{Name: "after `)` of if header", Src: `if (x) /re/.test(y)`, Want: `if ( id:x ) re:/re/ . id:test ( id:y )`},
		{Name: "after `)` of while header", Src: `while (x) /re/g.exec(y)`, Want: `while ( id:x ) re:/re/g . id:exec ( id:y )`},
		{Name: "after empty block", Src: "{}\n/re/.test(s)", Want: `{ } re:/re/ . id:test ( id:s )`},
		{Name: "after block ending in identifier", Src: "{ a }\n/re/.test(s)", Want: `{ id:a } re:/re/ . id:test ( id:s )`},
		{Name: "division after method named like a keyword", Src: `obj.with(x) / 2; a.if(b) / 2; s.for(k) / 2`, Want: `id:obj . with ( id:x ) / num:2 ; id:a . if ( id:b ) / num:2 ; id:s . for ( id:k ) / num:2`},
		{Name: "regex after for-await header", Src: `for await (x of y) /re/.test(x)`, Want: `for await ( id:x of id:y ) re:/re/ . id:test ( id:x )`},
		{Name: "division after parenthesised call in header-like position", Src: `foo(x) / 2`, Want: `id:foo ( id:x ) / num:2`},
	})
	ctRunLexCases(t, "unicode", []ctLexCase{
		{Name: "latin extended identifier", Src: `café = 1`, Want: `id:café = num:1`},
		{Name: "greek identifier", Src: `π + 1`, Want: `id:π + num:1`},
		{Name: "cyrillic identifier", Src: `имя = 1`, Want: `id:имя = num:1`},
		{Name: "NBSP is whitespace", Src: "a\u00A0b", Want: `id:a id:b`},
		{Name: "BOM is whitespace", Src: "\uFEFFa", Want: `id:a`},
		{Name: "\\f and \\v are whitespace", Src: "a\f\vb", Want: `id:a id:b`},
		{Name: "hashbang line", Src: "#!/usr/bin/env node\na", Want: `id:a`},
	})
	ctRunLexCases(t, "private-names", []ctLexCase{
		{Name: "member access", Src: `this.#x`, Want: `this . #x`},
		{Name: "brand check", Src: `#x in o`, Want: `#x in id:o`},
		{Name: "class field", Src: `#x = 1`, Want: `#x = num:1`},
	})
}

func TestCompatLexerPositions(t *testing.T) {
	toks, _ := ctLex("abc + 12\n  'x'")
	type span struct{ pos, end, line int }
	want := []span{{0, 3, 1}, {4, 5, 1}, {6, 8, 1}, {11, 14, 2}}
	if len(toks) != len(want) {
		t.Fatalf("got %d tokens, want %d", len(toks), len(want))
	}
	for i, w := range want {
		g := toks[i]
		if g.Pos != w.pos || g.End != w.end || g.Line != w.line {
			t.Errorf("token %d %q: got [%d,%d) line %d, want [%d,%d) line %d", i, g.Val, g.Pos, g.End, g.Line, w.pos, w.end, w.line)
		}
	}
}

func TestCompatLexerLineTracking(t *testing.T) {
	src := "a\n`x\ny`\n/* c\n */ b"
	toks, _ := ctLex(src)
	wantLines := []int{1, 2, 5}
	if len(toks) != len(wantLines) {
		t.Fatalf("got %d tokens (%s)", len(toks), ctLexRepr(src))
	}
	for i, w := range wantLines {
		if toks[i].Line != w {
			t.Errorf("token %d (%q) on line %d, want %d", i, toks[i].Val, toks[i].Line, w)
		}
	}
}

func TestCompatLexerEOFIsSticky(t *testing.T) {
	l := NewLexer("a")
	l.Next()
	for i := 0; i < 3; i++ {
		if k := l.Next().Kind; k != TokEOF {
			t.Fatalf("call %d after end returned kind %d, want EOF", i, k)
		}
	}
}

func TestCompatLexerNewlineFlag(t *testing.T) {
	// Token.NL is what the parser needs to implement ASI (return/throw/break/
	// continue/yield/postfix-++/async restrictions).
	cases := []struct {
		src  string
		want []bool // NL for each token
	}{
		{"a b", []bool{false, false}},
		{"a\nb", []bool{false, true}},
		{"a\r\nb", []bool{false, true}},
		{"a /* x */ b", []bool{false, false}},
		{"a /* x\n */ b", []bool{false, true}},
		{"a // c\nb", []bool{false, true}},
		{"a\u2028b", []bool{false, true}},
		{"a\n\n  b  c", []bool{false, true, false}},
	}
	for _, c := range cases {
		toks, _ := ctLex(c.src)
		if len(toks) != len(c.want) {
			t.Errorf("%q: %d tokens (%s), want %d", c.src, len(toks), ctLexRepr(c.src), len(c.want))
			continue
		}
		for i, w := range c.want {
			got, ok := ctTokNL(toks[i])
			if !ok {
				t.Skip("KNOWN GAP: Token has no NL field (no line-terminator tracking)")
			}
			if got != w {
				t.Errorf("%q token %d (%q): NL=%v, want %v", c.src, i, toks[i].Val, got, w)
			}
		}
	}
}
