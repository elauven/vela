package jsast

import (
	"strings"
	"testing"
)

// Blanking (task 081): the JavaScript text of a TypeScript source has the
// same length and line breaks, and parses as JavaScript to the tree the
// TypeScript parse returned. Checked on the corpus here and on every source
// of the transpileModule oracle (ts_oracle_test.go).

// withoutEmptyStatements drops empty statements, which blanking leaves where
// an erased statement stood between lines that could join.
func withoutEmptyStatements(prog *JsProgram) *JsProgram {
	filter := func(list []JsStmt) []JsStmt {
		var out []JsStmt
		for _, s := range list {
			if _, empty := s.(*JsEmptyStmt); !empty {
				out = append(out, s)
			}
		}
		return out
	}
	WalkNode(prog, func(n JsNode) bool {
		switch n := n.(type) {
		case *JsProgram:
			n.Body = filter(n.Body)
		case *JsBlock:
			n.Body = filter(n.Body)
		case *JsSwitchCase:
			n.Body = filter(n.Body)
		}
		return true
	})
	return prog
}

// blankingProblem returns what is wrong with the blanked JavaScript of src,
// or "".
func blankingProblem(src string, prog *JsProgram) string {
	js := BlankTypeScript(src, prog.TS)
	if len(js) != len(src) {
		return "length changed"
	}
	for i := 0; i < len(src); i++ {
		if (src[i] == '\n') != (js[i] == '\n') {
			return "line breaks moved"
		}
	}
	again := ctParse(js)
	if len(again.diags) > 0 {
		return "does not parse as JavaScript: " + again.diags[0].Message + "\n  js: " + ctShorten(js)
	}
	tsTree := ctDump(withoutEmptyStatements(ctParse(PrintJS(prog, PrintPretty)).prog))
	if g := ctDump(withoutEmptyStatements(again.prog)); g != tsTree {
		return "tree differs\n  js:   " + ctShorten(js) + "\n  got:  " + ctShorten(g) + "\n  want: " + ctShorten(tsTree)
	}
	return ""
}

func TestTSBlankingCorpus(t *testing.T) {
	for _, c := range tsValidCases {
		got := tsParse(c.ts)
		if got.panic != nil || len(got.diags) > 0 {
			continue // TestTSCorpusValid reports it
		}
		if p := blankingProblem(c.ts, got.prog); p != "" {
			t.Errorf("%s/%s: %s", c.group, c.name, p)
		}
	}
}

func TestTSBlankingLineBreaks(t *testing.T) {
	cases := []struct{ name, ts, js string }{
		{"statement between lines", "let x = a\ninterface I {}\n(b)", "let x = a\n;             \n(b)"},
		{"statement after semicolon", "let x = a;\ntype T = 1\n(b)", "let x = a;\n          \n(b)"},
		{"statement after block", "if (a) {}\ntype T = 1\nf()", "if (a) {}\n          \nf()"},
		{"function expression then call", "x = function () {}\ntype T = 1\n(b)", "x = function () {}\n;         \n(b)"},
		{"class member", "class A {\n  x = 1\n  declare y: number\n  [k] = 2\n}", "class A {\n  x = 1\n  ;                \n  [k] = 2\n}"},
		{"return type on two lines", "f = (a):\n  T => a", "f = (a  \n  ) => a"},
		{"generic arrow after return", "function f() {\n  return <T,>\n  (x: T) => x\n}", "function f() {\n  return (   \n   x   ) => x\n}"},
		{"type specifier", "import { type A, b } from './m'", "import {         b } from './m'"},
		{"only type specifier", "import { type A } from './m'", "import {        } from './m'"},
		{"export type specifier last", "export { a, type B }", "export { a,        }"},
		{"this parameter", "function f(this: W, a: number) {}", "function f(         a        ) {}"},
		{"declare field", "class A { declare x: T; y = 1 }", "class A {               y = 1 }"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := tsParse(c.ts)
			if len(got.diags) > 0 {
				t.Fatalf("diagnostics: %v", got.diags)
			}
			if js := BlankTypeScript(c.ts, got.prog.TS); js != c.js {
				t.Errorf("blanked\n  got:  %q\n  want: %q", js, c.js)
			}
			if p := blankingProblem(c.ts, got.prog); p != "" {
				t.Error(p)
			}
		})
	}
}

func TestEraseTypeScriptKeepsSourceOnErrors(t *testing.T) {
	src := "enum E { A }"
	js, _, diags := EraseTypeScript(src)
	if len(diags) == 0 || js != src {
		t.Fatalf("want diagnostics and the source back, got %q %v", js, diags)
	}
	js, prog, diags := EraseTypeScript("let x: number = 1")
	if len(diags) > 0 || prog == nil || strings.TrimRight(js, " ") != "let x = 1" && js != "let x         = 1" {
		t.Fatalf("got %q %v", js, diags)
	}
}
