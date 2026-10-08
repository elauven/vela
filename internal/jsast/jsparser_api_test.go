package jsast

// Public entry points and small helpers.

import (
	"strings"
	"testing"
)

func TestCompatParseJS(t *testing.T) {
	prog := ParseJS("a = 1; b()")
	if got, want := ctDump(prog), "(program (expr (= a 1)) (expr (call b)))"; got != want {
		t.Errorf("ParseJS: got %s, want %s", got, want)
	}
	// ParseJS tolerates invalid input and still returns a tree.
	if p := ParseJS("a = "); p == nil {
		t.Error("ParseJS returned nil for invalid input")
	}
}

func TestCompatParseJSWithDiagnostics(t *testing.T) {
	_, diags := ParseJSWithDiagnostics("let a = 1;\nlet b = ;\nfoo(\n")
	if len(diags) < 2 {
		t.Fatalf("want >= 2 diagnostics, got %v", diags)
	}
	for i := 1; i < len(diags); i++ {
		if diags[i].Pos < diags[i-1].Pos {
			t.Errorf("diagnostics not sorted by position: %v", diags)
		}
	}
	if !strings.HasPrefix(diags[0].Message, "line 2:") {
		t.Errorf("first diagnostic should be on line 2, got %q", diags[0].Message)
	}
	if diags[0].Pos < len("let a = 1;\n") {
		t.Errorf("first diagnostic points before line 2: %+v", diags[0])
	}
	if _, d := ParseJSWithDiagnostics("let ok = 1;\n"); len(d) != 0 {
		t.Errorf("valid program reported %v", d)
	}
}

func TestCompatDiagnosticLineNumbers(t *testing.T) {
	// A missing token deep inside a template placeholder on a later line must
	// be reported with the right line and an absolute offset.
	src := "a;\nb;\n`x\n${ foo( }`"
	_, diags := ParseJSWithDiagnostics(src)
	if len(diags) == 0 {
		t.Fatal("expected a diagnostic")
	}
	if !strings.HasPrefix(diags[0].Message, "line 4:") {
		t.Errorf("got %q, want a line-4 diagnostic", diags[0].Message)
	}
	if diags[0].Pos < strings.Index(src, "${") || diags[0].Pos > len(src) {
		t.Errorf("offset %d is not inside the placeholder", diags[0].Pos)
	}
}

func TestCompatParseJSExpr(t *testing.T) {
	if e := ParseJSExpr("a + b * 2"); ctDump(e) != "(+ a (* b 2))" {
		t.Errorf("got %s", ctDump(e))
	}
	if e := ParseJSExpr("   \n "); e != nil {
		t.Errorf("blank source should give nil, got %s", ctDump(e))
	}
}

func TestCompatTokKindString(t *testing.T) {
	for word, k := range KeywordMap {
		if k.String() != word {
			t.Errorf("kind of %q prints as %q", word, k.String())
		}
	}
	for k, want := range punctText {
		if k.String() != want {
			t.Errorf("punctuator %q prints as %q", want, k.String())
		}
	}
	for _, k := range []TokKind{TokIllegal, TokEOF, TokIdent, TokNumber, TokString, TokTemplate, TokRegex, TokPrivateName} {
		if s := k.String(); s == "" || strings.HasPrefix(s, "token(") {
			t.Errorf("kind %d has no description: %q", k, s)
		}
	}
	if s := TokKind(250).String(); !strings.HasPrefix(s, "token(") {
		t.Errorf("unknown kind prints as %q", s)
	}
}

// Diagnostics must tell the developer what was expected and what was found.
func TestCompatDiagnosticMessages(t *testing.T) {
	cases := []struct{ src, want string }{
		{"foo(", "expected ')' but found end of input"},
		{"if a {}", "expected '(' but found 'a'"},
		{"a b", "expected ';' but found 'b'"},
		{"x = ;", "expression expected but found ';'"},
		{"x = 'abc", "unterminated string literal"},
		{"a @ b", "unexpected character"},
		{"const x;", "missing initializer in const declaration"},
		{"a ?? b || c", "'??' cannot be mixed"},
		{"x = /a/gg", "invalid regular expression flags"},
	}
	for _, c := range cases {
		_, diags := ParseJSWithDiagnostics(c.src)
		found := false
		for _, d := range diags {
			if strings.Contains(d.Message, c.want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: want a diagnostic containing %q, got %v", c.src, c.want, diags)
		}
	}
}

// One mistake should not fan out into a wall of errors: every offset is
// reported at most once.
func TestCompatDiagnosticsAreDeduplicated(t *testing.T) {
	_, diags := ParseJSWithDiagnostics("foo(a,, b,, c")
	seen := map[int]bool{}
	for _, d := range diags {
		if seen[d.Pos] {
			t.Errorf("offset %d reported twice: %v", d.Pos, diags)
		}
		seen[d.Pos] = true
	}
}

// Shorthand properties must not alias key and value (walkers would visit the
// same node twice).
func TestCompatShorthandNodesAreDistinct(t *testing.T) {
	for _, src := range []string{`({a})`, `({a = 1} = o)`, `const {a} = o`, `const {a = 1} = o`, `({a}) => a`} {
		for _, n := range ctFindAll(ParseJS(src), "JsProp") {
			s, _ := ctStruct(n)
			k, v := ctField(s, "Key"), ctField(s, "Value")
			if !ctBool(s, "Shorthand") {
				continue
			}
			if v.Interface() == k.Interface() {
				t.Errorf("%s: shorthand property shares Key and Value node", src)
			}
		}
	}
}
