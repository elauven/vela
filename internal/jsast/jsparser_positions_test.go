package jsast

// Source-span tests.  Pos/End are byte offsets into the original source and
// drive diagnostics, source maps and (via ShiftTree) template-hole relocation,
// so a wrong span is a user-visible bug even when the tree shape is right.
//
// Each row lists, for one node type, the source text every occurrence of that
// node type must cover (in pre-order).

import (
	"fmt"
	"testing"
)

func ctFindAll(root JsNode, typeName string) []JsNode {
	var out []JsNode
	var visit func(n JsNode)
	visit = func(n JsNode) {
		if ctTypeName(n) == typeName {
			out = append(out, n)
		}
		for _, c := range ctChildren(n) {
			visit(c)
		}
	}
	visit(root)
	return out
}

type ctPosCase struct {
	Name string
	Src  string
	Type string
	Want []string // source text covered by each node of Type, pre-order
	Gap  string
}

var ctPositions = []ctPosCase{
	{Name: "binary", Src: `a + b`, Type: "JsBinaryExpr", Want: []string{"a + b"}},
	{Name: "call and args", Src: `foo(bar, baz)`, Type: "JsCallExpr", Want: []string{"foo(bar, baz)"}},
	{Name: "identifiers in call", Src: `foo(bar, baz)`, Type: "JsIdent", Want: []string{"foo", "bar", "baz"}},
	{Name: "parenthesised left operand", Src: `(a + b) * c`, Type: "JsBinaryExpr", Want: []string{"(a + b) * c", "a + b"}},
	{Name: "parenthesised right operand", Src: `a * (b + c)`, Type: "JsBinaryExpr", Want: []string{"a * (b + c)", "b + c"}},
	{Name: "parenthesised callee", Src: `(f || g)(x)`, Type: "JsCallExpr", Want: []string{"(f || g)(x)"}},
	{Name: "parenthesised assignment target", Src: `(a) = 1`, Type: "JsAssignExpr", Want: []string{"(a) = 1"}},
	{Name: "parenthesised conditional test", Src: `(a) ? b : c`, Type: "JsConditionalExpr", Want: []string{"(a) ? b : c"}},
	{Name: "async function declaration", Src: `async function f() {}`, Type: "JsFuncDecl", Want: []string{"async function f() {}"}},
	{Name: "async arrow", Src: `async () => 1`, Type: "JsArrowFunc", Want: []string{"async () => 1"}},
	{Name: "async arrow, ident param", Src: `async x => x`, Type: "JsIdent", Want: []string{"x", "x"}},
	{Name: "arrow expression body", Src: `x => x + 1`, Type: "JsArrowFunc", Want: []string{"x => x + 1"}},
	{Name: "class members", Src: `class A { m() {} static n = 1; }`, Type: "JsClassMember", Want: []string{"m() {}", "static n = 1;"}},
	{Name: "class method function value", Src: `class A { m(a) {} }`, Type: "JsFuncExpr", Want: []string{"(a) {}"}},
	{Name: "object method function value", Src: `({ m(a) {} })`, Type: "JsFuncExpr", Want: []string{"(a) {}"}},
	{Name: "object props", Src: `({a, b: 1})`, Type: "JsProp", Want: []string{"a", "b: 1"}},
	{Name: "shorthand key/value are distinct nodes", Src: `({a})`, Type: "JsIdent", Want: []string{"a", "a"}},
	{Name: "export default", Src: `export default 1;`, Type: "JsExportDecl", Want: []string{"export default 1;"}},
	{Name: "optional member", Src: `a?.b`, Type: "JsMemberExpr", Want: []string{"a?.b"}},
	{Name: "new", Src: `new Foo(1)`, Type: "JsNewExpr", Want: []string{"new Foo(1)"}},
	{Name: "new.target identifiers", Src: `function F() { new.target }`, Type: "JsMemberExpr", Want: []string{"new.target"}},
	{Name: "if / else", Src: `if (a) b; else c;`, Type: "JsIfStmt", Want: []string{"if (a) b; else c;"}},
	{Name: "declarators", Src: `let a = 1, b = 2;`, Type: "JsVarDeclarator", Want: []string{"a = 1", "b = 2"}},
	{Name: "declaration", Src: `let a = 1, b = 2;`, Type: "JsVarDecl", Want: []string{"let a = 1, b = 2;"}},
	{Name: "conditional", Src: `x = y ? 1 : 2`, Type: "JsConditionalExpr", Want: []string{"y ? 1 : 2"}},
	{Name: "multi-line member chain", Src: "a\n  .b\n  .c", Type: "JsMemberExpr", Want: []string{"a\n  .b\n  .c", "a\n  .b"}},
	{Name: "block", Src: `function f(a, b) {}`, Type: "JsBlock", Want: []string{"{}"}},
	{Name: "switch cases", Src: `switch (a) { case 1: b; default: c }`, Type: "JsSwitchCase", Want: []string{"case 1: b;", "default: c"}},
	{Name: "labeled", Src: `a: b;`, Type: "JsLabeledStmt", Want: []string{"a: b;"}},
	{Name: "spread", Src: `f(...a)`, Type: "JsSpreadExpr", Want: []string{"...a"}},
	{Name: "sequence", Src: `a, b`, Type: "JsSeqExpr", Want: []string{"a, b"}},
	{Name: "unary / postfix", Src: `!a; b++`, Type: "JsUnaryExpr", Want: []string{"!a", "b++"}},
	{Name: "multi-byte source", Src: `x = 'привет'; y`, Type: "JsIdent", Want: []string{"x", "y"}},
	{Name: "template literal", Src: "x = `a${b}c`", Type: "JsTemplateLit", Want: []string{"`a${b}c`"}},
	{Name: "template hole idents", Src: "x = `a${b}c${ d + e }`", Type: "JsIdent", Want: []string{"x", "b", "d", "e"}},
	{Name: "template hole expression", Src: "x = `a${b}c${ d + e }`", Type: "JsBinaryExpr", Want: []string{"d + e"}},
	{Name: "template hole after multi-byte text", Src: "x = `привет ${имя}` + y", Type: "JsBinaryExpr", Want: []string{"`привет ${имя}` + y"}},
	{Name: "nested template holes", Src: "`a${ f(`b${c}`) }`", Type: "JsIdent", Want: []string{"f", "c"}},
	{Name: "template hole on a later line", Src: "a;\n\n`x\n${ foo(1) }`", Type: "JsCallExpr", Want: []string{"foo(1)"}},
	{Name: "template hole with object shorthand", Src: "`${ f({a}) }`", Type: "JsIdent", Want: []string{"f", "a", "a"}},
}

func TestCompatPositions(t *testing.T) {
	for _, c := range ctPositions {
		c := c
		t.Run("Positions/"+c.Name, func(t *testing.T) {
			r := ctParse(c.Src)
			err := ""
			switch {
			case r.panic != nil || r.hang:
				err = fmt.Sprintf("panic=%v hang=%v", r.panic, r.hang)
			default:
				var got []string
				for _, n := range ctFindAll(r.prog, c.Type) {
					p, e := n.NodePos(), n.NodeEnd()
					if p < 0 || e > len(c.Src) || p > e {
						got = append(got, fmt.Sprintf("<invalid [%d,%d)>", p, e))
						continue
					}
					got = append(got, c.Src[p:e])
				}
				if fmt.Sprintf("%q", got) != fmt.Sprintf("%q", c.Want) {
					err = fmt.Sprintf("spans differ for %s\n      got:  %q\n      want: %q", c.Type, got, c.Want)
				}
			}
			ctRecord(ctResult{ID: "Positions/" + c.Name, Group: "Positions", Name: c.Name, Pass: err == "", Gap: c.Gap, Err: err, Src: c.Src})
			ctVerdict(t, c.Src, c.Gap, err)
		})
	}
}

func TestCompatPositionsEdge(t *testing.T) {
	for _, src := range []string{"", " ", "\n\n", "// only a comment", "/* c */"} {
		r := ctParse(src)
		if r.panic != nil || r.hang {
			t.Fatalf("%q: panic=%v hang=%v", src, r.panic, r.hang)
		}
		if n := len(r.prog.Body); n != 0 {
			t.Errorf("%q: %d statements, want 0", src, n)
		}
		if r.prog.Pos != 0 || r.prog.End < 0 || r.prog.End > len(src) {
			t.Errorf("%q: program span [%d,%d)", src, r.prog.Pos, r.prog.End)
		}
	}
}
