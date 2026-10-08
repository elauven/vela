package jsast

// Negative tests: invalid programs must be *reported*, not silently accepted.
//
// The parser is error-tolerant by design (it always returns a tree), but the
// component compiler consumes Diagnostics, so a program with a syntax error
// that produces no diagnostic reaches codegen and yields wrong output without
// any message to the developer.
//
// Out of scope here (they are "early errors" that need scope/context tracking
// and belong in a separate validation pass): duplicate declarations,
// break/continue outside loops, return outside functions, invalid regex flags,
// strict-mode reserved words, duplicate constructors.

import "testing"

type ctBadCase struct {
	Name string
	Src  string
	Gap  string
}

var ctInvalid = []ctBadCase{
	// unterminated constructs
	{Name: "unterminated string", Src: `x = 'abc`},
	{Name: "unterminated string at newline", Src: "x = 'abc\ny = 1"},
	{Name: "unterminated template", Src: "x = `abc"},
	{Name: "unterminated template hole", Src: "x = `a${b"},
	{Name: "unterminated block comment", Src: `a; /* never closed`},
	{Name: "unterminated regex", Src: `x = /abc`},
	{Name: "illegal character", Src: `a @ b`},
	{Name: "lone hash", Src: `# x`},

	// missing / unbalanced delimiters
	{Name: "unclosed call", Src: `foo(`},
	{Name: "unclosed call with args", Src: `foo(a, b`},
	{Name: "unclosed if condition", Src: `if (a {`},
	{Name: "unclosed block", Src: `{`},
	{Name: "unclosed function body", Src: `function f() {`},
	{Name: "unclosed array", Src: `[1, 2`},
	{Name: "unclosed object", Src: `({a: 1`},
	{Name: "mismatched delimiters", Src: `({a: 1)`},
	{Name: "unclosed class body", Src: `class A {`},
	{Name: "unclosed switch", Src: `switch (x) { case 1:`},
	{Name: "stray closing paren", Src: `a);`},
	{Name: "stray closing brace", Src: `a; }`},
	{Name: "stray closing bracket", Src: `a; ]`},

	// expressions
	{Name: "missing rhs of assignment", Src: `x = ;`},
	{Name: "dangling binary operator", Src: `a +`},
	{Name: "adjacent binary operators", Src: `1 + * 2`},
	{Name: "ternary without colon", Src: `a ? b`},
	{Name: "double comma in call", Src: `f(a,, b)`},
	{Name: "double comma in params", Src: `function f(a,,b) {}`},
	{Name: "arrow without body", Src: `(a, b) =>`},
	{Name: "invalid assignment target: literal", Src: `1 = 2`},
	{Name: "invalid assignment target: call", Src: `f() = 1`},
	{Name: "invalid compound-assignment target", Src: `a + b += 1`},
	{Name: "invalid update operand", Src: `1++`},
	{Name: "invalid update operand (prefix)", Src: `++f()`},
	{Name: "invalid new.meta", Src: `function f() { new.foo }`},
	{Name: "empty parens", Src: `x = ()`},

	// statements
	{Name: "missing semicolon between statements on one line", Src: `a b`},
	{Name: "two declarations on one line", Src: `var x = 1 var y = 2`},
	{Name: "two numbers on one line", Src: `x = 1 2`},
	{Name: "for header missing semicolon", Src: `for (let i = 0; i < 3) {}`},
	{Name: "unclosed for header", Src: `for (;;`},
	{Name: "if without condition parens", Src: `if a {}`},
	{Name: "else without if", Src: `else {}`},
	{Name: "catch without try", Src: `catch (e) {}`},
	{Name: "case outside switch", Src: `case 1:`},
	{Name: "throw + newline", Src: "throw\nnew Error()"},
	{Name: "try without catch/finally", Src: `try {}`},
	{Name: "const without initializer", Src: `const x;`},
	{Name: "var without name", Src: `var ;`},
	{Name: "reserved word as binding", Src: `var class = 1;`},
	{Name: "reserved word as function name", Src: `function if() {}`},
	{Name: "number followed by identifier", Src: `var x = 3in y;`},
	{Name: "number glued to identifier", Src: `x = 3abc;`},
	{Name: "function declaration without name", Src: `function () {}`},
	{Name: "class declaration without name", Src: `class {}`},
	{Name: "with statement", Src: `with (a) { b }`},
	{Name: "class member garbage", Src: `class A { 1 2 }`},
	{Name: "object literal missing colon/value", Src: `({a: })`},

	// modules
	{Name: "import without specifier or source", Src: `import;`},
	{Name: "import missing source", Src: `import {a} from;`},
	{Name: "import missing from", Src: `import {a} 'x';`},
	{Name: "import: default without from", Src: `import a 'x';`},
	{Name: "import missing comma in specifiers", Src: `import {a b} from 'x';`},
	{Name: "import: keyword as default binding", Src: `import from 'x';`},
	{Name: "export without anything", Src: `export`},
	{Name: "export default without value", Src: `export default`},
	{Name: "export * without from", Src: `export * 'x';`},
	{Name: "import: namespace without binding", Src: `import * as 1 from 'x';`},
	{Name: "import: reserved word as local name", Src: `import {a as class} from 'x';`},
	{Name: "import: comma without clause", Src: `import a, 1 from 'x';`},
	{Name: "destructuring: numeric shorthand", Src: `const {1} = o;`},
	{Name: "object literal: string shorthand", Src: `({'a'})`},
	{Name: "malformed unicode escape in identifier", Src: `var \u12 = 1;`},
	{Name: "assignment to optional call chain", Src: `a?.b().c = 1`},
	{Name: "increment of optional chain", Src: `a?.b++`},
	{Name: "regex flags: unknown", Src: `x = /a/x;`},
	{Name: "regex flags: u and v together", Src: `x = /a/uv;`},
	{Name: "await before **", Src: `async function f() { await a ** b }`},
	{Name: "private name outside class member position", Src: `x = #a;`},
	{Name: "import.meta typo", Src: `import.metaa`},
	{Name: "bare import in expression", Src: `x = import;`},
	{Name: "for await without of", Src: `async function f() { for await (x in y) {} }`},
	{Name: "class method modifier without name", Src: `class A { async }} `},
	{Name: "garbage inside switch body", Src: `switch (a) { foo }`},
	{Name: "duplicate default clause in switch", Src: `switch (a) { default: b; default: c }`},
	{Name: "arrow function as unary operand", Src: `!(a) => 1`},
	{Name: "arrow function as binary operand", Src: `x || (a) => 1`},
	{Name: "empty template placeholder", Src: "x = `a${}b`"},

	// Early errors that need scope or context tracking are reported by
	// EarlyErrors (early.go), a separate validation pass.
	{Name: "early: ?? mixed with || without parens", Src: `a ?? b || c`},
	{Name: "early: ?? mixed with && without parens", Src: `a && b ?? c`},
	{Name: "early: unary operand of **", Src: `-a ** b`},
	{Name: "early: assignment to optional chain", Src: `a?.b.c = 1`},
	{Name: "early: break outside loop", Src: `break;`},
	{Name: "early: continue outside loop", Src: `continue;`},
	{Name: "early: undefined label", Src: `while (a) { break nowhere; }`},
	{Name: "early: duplicate let", Src: `let a; let a;`, Gap: "reported by the compiler's scope analysis (VELA-SCOPE-DUPLICATE), not by the parser or EarlyErrors"},
	{Name: "early: duplicate constructor", Src: `class A { constructor() {} constructor() {} }`},
	{Name: "early: super() outside constructor", Src: `function f() { super(); }`},
	{Name: "early: invalid regex flags", Src: `x = /a/gg;`},
	{Name: "early: undeclared private name", Src: `class A { f() { return this.#missing; } }`},
	{Name: "early: invalid regex pattern", Src: `x = /(/;`},
	{Name: "early: return outside function", Src: `return 1;`, Gap: "deliberately not reported: script bodies may be compiled into a function, so a top-level `return` can be legitimate"},
	{Name: "early: new.target outside function", Src: `new.target`},
	{Name: "early: await in non-async function", Src: `function f() { await x; }`},
}

func TestCompatDiagnostics(t *testing.T) {
	for _, c := range ctInvalid {
		c := c
		t.Run("Errors/"+c.Name, func(t *testing.T) {
			err := ctCheckInvalid(c.Src)
			ctRecord(ctResult{ID: "Errors/" + c.Name, Group: "Errors", Name: c.Name, Pass: err == "", Gap: c.Gap, Err: err, Src: c.Src})
			ctVerdict(t, c.Src, c.Gap, err)
		})
	}
}

// Valid programs must not be flagged: guards against the diagnostics above
// turning into false positives.
func TestCompatNoFalsePositives(t *testing.T) {
	valid := []string{
		"a\nb", "a;b", "a\n(b)", "if (a) b\nelse c", "do a(); while (b) c()", "{ a }", "function f() { return }",
		"x = {}\n", "x = function () {}\ny()", "for (;;) break", "label: { break label }",
		"class A { a\n b }", "class A { a = 1; b = 2 }", "class A { static a; static b }", "class A { m() {} n() {} }",
		"a = b ? c : d", "a = (b, c)", "x = a\n? b\n: c", "const {a} = b, [c] = d", "try {} finally {}", "try {} catch {}",
		"switch (a) {}", "switch (a) { default: }", "if (a) ; else ;", "for (const x of y);", "while (0);",
		"export {};", "export default class {}", "export default function () {}", "import {} from 'x'",
		"a\n/b/g", // division per spec
		"f(a, b,)", "function f(a, b,) {}", "[,]", "[,,]", "({})", "({a,})", "x = () => {}\ny = 1",
		"x = a ? (b) : c", "x = a ? (b) => c : d", "async () => {}", "async function f() {}",
		"(a || b) ?? c", "a ?? b ?? c", "a ?? (b && c)", "a && b || c", "a || b || c", "(-a) ** b", "2 ** -a", "a ** b ** c",
		"(a?.b).c = 1", "a.b?.c", "++a.b", "(a?.b).c++", "a?.b.c", "x = /a/dgimsuy", "x = /a/v", "typeof (a?.b)",
		"for (var i = 0, n = a.length; i < n; i++) {}", "for (let [a, b] of c) {}", "for (const {a} of b) {}",
	}
	for _, src := range valid {
		src := src
		t.Run(src, func(t *testing.T) {
			r := ctParse(src)
			if r.panic != nil || r.hang {
				t.Fatalf("panic=%v hang=%v", r.panic, r.hang)
			}
			if len(r.diags) > 0 {
				t.Errorf("false positive: %v", r.diags)
			}
		})
	}
}
