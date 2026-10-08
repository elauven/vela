package jsast

// Feature-coverage matrix for the JS parser.
//
// Every row is one language feature: a source snippet plus the AST the parser
// is expected to produce (see ctDump for the notation).  Rows are grouped by
// the ECMAScript edition that introduced the feature so the test output reads
// as a compatibility table.
//
// Gap: a non-empty Gap marks a KNOWN limitation.  The row is still executed:
//   - fails  → reported as SKIP with the reason (suite stays green),
//   - passes → the test FAILS asking you to delete the Gap marker, so the
//     matrix can never silently go stale.
//
// Run with AST_COMPAT_STRICT=1 to ignore Gap markers and see every failure.

import "testing"

type ctCase struct {
	ID   string
	Ver  string
	Name string
	Src  string
	Want string // expected dump; "" = only require a clean parse
	Gap  string // known limitation (why / what to do)
}

func ctRunCases(t *testing.T, cases []ctCase) {
	t.Helper()
	for _, c := range cases {
		c := c
		t.Run(c.Ver+"/"+c.Name, func(t *testing.T) {
			err := ctCheckValid(c.Src, c.Want)
			ctRecord(ctResult{ID: c.Ver + "/" + c.Name, Group: c.Ver, Name: c.Name, Pass: err == "", Gap: c.Gap, Err: err, Src: c.Src})
			ctVerdict(t, c.Src, c.Gap, err)
		})
	}
}

func ctVerdict(t *testing.T, src, gap, err string) {
	t.Helper()
	switch {
	case ctStrict():
		if err != "" {
			t.Errorf("FAIL (strict mode)\n  src: %q\n    %s", src, err)
		}
	case gap != "" && err != "":
		t.Skipf("KNOWN GAP: %s\n  src: %q\n    %s", gap, src, err)
	case gap != "" && err == "":
		t.Errorf("known gap now passes — remove the Gap marker (%q)\n  src: %q", gap, src)
	case err != "":
		t.Errorf("REGRESSION\n  src: %q\n    %s", src, err)
	}
}

// ──────────────────────────────────────────────
//  ES5 and language basics
// ──────────────────────────────────────────────

var ctES5 = []ctCase{
	{Ver: "ES5", Name: "var declaration", Src: `var a = 1, b;`, Want: `(program (var (= a 1) b))`},
	{Ver: "ES5", Name: "function declaration", Src: `function f(a, b) { return a + b; }`, Want: `(program (function f (a b) (block (return (+ a b)))))`},
	{Ver: "ES5", Name: "function expression", Src: `var f = function g() {};`, Want: `(program (var (= f (function g () (block)))))`},
	{Ver: "ES5", Name: "IIFE", Src: `(function () {})();`, Want: `(program (expr (call (function () (block)))))`},
	{Ver: "ES5", Name: "if / else", Src: `if (a) b; else c;`, Want: `(program (if a (expr b) (expr c)))`},
	{Ver: "ES5", Name: "else-if chain", Src: `if (a) b; else if (c) d; else e;`, Want: `(program (if a (expr b) (if c (expr d) (expr e))))`},
	{Ver: "ES5", Name: "for loop", Src: `for (var i = 0; i < n; i++) x();`, Want: `(program (for (var (= i 0)) (< i n) (post++ i) (expr (call x))))`},
	{Ver: "ES5", Name: "for loop, empty header", Src: `for (;;) {}`, Want: `(program (for <nil> <nil> <nil> (block)))`},
	{Ver: "ES5", Name: "for loop, comma in header", Src: `for (i = 0, j = 9; i < j; i++, j--) {}`, Want: `(program (for (expr (seq (= i 0) (= j 9))) (< i j) (seq (post++ i) (post-- j)) (block)))`},
	{Ver: "ES5", Name: "for-in with declaration", Src: `for (var k in o) f(k);`, Want: `(program (for-in (var k) o (expr (call f k))))`},
	{Ver: "ES5", Name: "for-in without declaration", Src: `for (k in o) f(k);`, Want: `(program (for-in (expr k) o (expr (call f k))))`},
	{Ver: "ES5", Name: "while", Src: `while (a) b();`, Want: `(program (while a (expr (call b))))`},
	{Ver: "ES5", Name: "do-while", Src: `do x++; while (x < 5)`, Want: `(program (do-while (expr (post++ x)) (< x 5)))`},
	{Ver: "ES5", Name: "switch", Src: `switch (x) { case 1: a(); break; default: b(); }`, Want: `(program (switch x (case 1 (expr (call a)) (break)) (default (expr (call b)))))`},
	{Ver: "ES5", Name: "try / catch / finally", Src: `try { a(); } catch (e) { b(); } finally { c(); }`, Want: `(program (try (block (expr (call a))) (catch e (block (expr (call b)))) (finally (block (expr (call c))))))`},
	{Ver: "ES5", Name: "labeled break", Src: `outer: for (;;) { break outer; }`, Want: `(program (label outer (for <nil> <nil> <nil> (block (break outer)))))`},
	{Ver: "ES5", Name: "labeled continue", Src: `outer: while (a) { continue outer; }`, Want: `(program (label outer (while a (block (continue outer)))))`},
	{Ver: "ES5", Name: "throw", Src: `throw new Error('x');`, Want: `(program (throw (new Error 'x')))`},
	{Ver: "ES5", Name: "empty statement", Src: `;;`, Want: `(program (empty) (empty))`},
	{Ver: "ES5", Name: "debugger statement", Src: `debugger;`, Want: `(program (debugger))`},
	{Ver: "ES5", Name: "directive prologue", Src: `'use strict';`, Want: `(program (expr 'use strict'))`},
	{Ver: "ES5", Name: "operator precedence", Src: `a + b * c - d`, Want: `(program (expr (- (+ a (* b c)) d)))`},
	{Ver: "ES5", Name: "logical precedence", Src: `a || b && c`, Want: `(program (expr (|| a (&& b c))))`},
	{Ver: "ES5", Name: "bitwise and shift precedence", Src: `a | b ^ c & d << 1`, Want: `(program (expr (| a (^ b (& c (<< d 1))))))`},
	{Ver: "ES5", Name: "relational and equality", Src: `a < b == c >= d`, Want: `(program (expr (== (< a b) (>= c d))))`},
	{Ver: "ES5", Name: "in / instanceof", Src: `a in b; c instanceof D`, Want: `(program (expr (in a b)) (expr (instanceof c D)))`},
	{Ver: "ES5", Name: "conditional, right-assoc", Src: `a ? b : c ? d : e`, Want: `(program (expr (? a b (? c d e))))`},
	{Ver: "ES5", Name: "compound assignment", Src: `a += 1; b >>>= 2; c %= 3`, Want: `(program (expr (+= a 1)) (expr (>>>= b 2)) (expr (%= c 3)))`},
	{Ver: "ES5", Name: "chained assignment", Src: `a = b = c`, Want: `(program (expr (= a (= b c))))`},
	{Ver: "ES5", Name: "sequence expression", Src: `a = 1, b = 2;`, Want: `(program (expr (seq (= a 1) (= b 2))))`},
	{Ver: "ES5", Name: "unary operators", Src: `!a; -b; +c; ~d; typeof e; void 0; delete f.g;`, Want: `(program (expr (! a)) (expr (- b)) (expr (+ c)) (expr (~ d)) (expr (typeof e)) (expr (void 0)) (expr (delete (. f g))))`},
	{Ver: "ES5", Name: "prefix / postfix update", Src: `++a; b--;`, Want: `(program (expr (++ a)) (expr (post-- b)))`},
	{Ver: "ES5", Name: "member and call chain", Src: `a.b[c](d).e`, Want: `(program (expr (. (call ([] (. a b) c) d) e)))`},
	{Ver: "ES5", Name: "new with arguments", Src: `new Foo(1, 2)`, Want: `(program (expr (new Foo 1 2)))`},
	{Ver: "ES5", Name: "new without arguments", Src: `new Foo`, Want: `(program (expr (new Foo)))`},
	{Ver: "ES5", Name: "new with member callee", Src: `new a.b.C(1)`, Want: `(program (expr (new (. (. a b) C) 1)))`},
	{Ver: "ES5", Name: "new with computed member callee", Src: `new a[b](1)`, Want: `(program (expr (new ([] a b) 1)))`},
	{Ver: "ES5", Name: "member access on new result", Src: `new Foo().bar()`, Want: `(program (expr (call (. (new Foo) bar))))`},
	{Ver: "ES5", Name: "new with parenthesised call callee", Src: `new (foo())()`, Want: `(program (expr (new (call foo))))`},
	{Ver: "ES5", Name: "regex literal with flags", Src: `var r = /ab+c/gi;`, Want: `(program (var (= r /ab+c/gi)))`},
	{Ver: "ES5", Name: "regex with slash in class", Src: `/[/]/.test(s)`, Want: `(program (expr (call (. /[/]/ test) s)))`},
	{Ver: "ES5", Name: "division is not regex", Src: `x = a / b / c`, Want: `(program (expr (= x (/ (/ a b) c))))`},
	{Ver: "ES5", Name: "regex after return/typeof/!", Src: `function f() { return /a/.test(s) } typeof /b/; !/c/.test(s)`, Want: `(program (function f () (block (return (call (. /a/ test) s)))) (expr (typeof /b/)) (expr (! (call (. /c/ test) s))))`},
	{Ver: "ES5", Name: "object literal keys", Src: `({a: 1, 'b': 2, 3: 4, [k]: 5})`, Want: `(program (expr (obj (prop a 1) (prop 'b' 2) (prop 3 4) (prop [k] 5))))`},
	{Ver: "ES5", Name: "object literal keyword keys", Src: `({ default: 1, new: 2, class: 3 })`, Want: `(program (expr (obj (prop default 1) (prop new 2) (prop class 3))))`},
	{Ver: "ES5", Name: "keyword property access", Src: `a.class.if; p.catch(f).finally(g)`, Want: `(program (expr (. (. a class) if)) (expr (call (. (call (. p catch) f) finally) g)))`},
	{Ver: "ES5", Name: "getter / setter in object", Src: `({ get a() { return 1 }, set a(v) {} })`, Want: `(program (expr (obj (get a (function () (block (return 1)))) (set a (function (v) (block))))))`},
	{Ver: "ES5", Name: "getter/setter named get/set", Src: `({ get: 1, set: 2, get, set })`, Want: `(program (expr (obj (prop get 1) (prop set 2) (short get) (short set))))`},
	{Ver: "ES5", Name: "array literal with holes", Src: `[1, , 2]`, Want: `(program (expr (array 1 <hole> 2)))`},
	{Ver: "ES5", Name: "array trailing comma", Src: `[1, 2,]`, Want: `(program (expr (array 1 2)))`},
	{Ver: "ES5", Name: "number literal forms", Src: `[0x1F, 0b101, 0o17, 1e3, .5, 5.5e-3, 017, 0]`, Want: `(program (expr (array 0x1F 0b101 0o17 1e3 .5 5.5e-3 017 0)))`},
	{Ver: "ES5", Name: "string escapes", Src: `x = 'it\'s' + "a\"b\n";`, Want: `(program (expr (= x (+ 'it\'s' "a\"b\n"))))`},
	{Ver: "ES5", Name: "string line continuation", Src: "x = 'a\\\nb';", Want: "(program (expr (= x 'a\\\nb')))"},
	{Ver: "ES5", Name: "comments", Src: "a // c\n/* d\n e */ b", Want: `(program (expr a) (expr b))`},
	{Ver: "ES5", Name: "function returning object literal", Src: `function f() { return { a: 1 }; }`, Want: `(program (function f () (block (return (obj (prop a 1))))))`},
	{Ver: "ES5", Name: "block statement", Src: `{ a; { b } }`, Want: `(program (block (expr a) (block (expr b))))`},
	{Ver: "ES5", Name: "switch with block cases", Src: `switch (x) { case 1: { a() } case 2: default: b() }`, Want: `(program (switch x (case 1 (block (expr (call a)))) (case 2) (default (expr (call b)))))`},
}

// ──────────────────────────────────────────────
//  ES2015 (ES6)
// ──────────────────────────────────────────────

var ctES2015 = []ctCase{
	{Ver: "ES2015", Name: "let / const", Src: `let a = 1; const b = 2;`, Want: `(program (let (= a 1)) (const (= b 2)))`},
	{Ver: "ES2015", Name: "arrow, single param", Src: `x => x + 1`, Want: `(program (expr (=> (x) (+ x 1))))`},
	{Ver: "ES2015", Name: "arrow, multiple params", Src: `(a, b) => a + b`, Want: `(program (expr (=> (a b) (+ a b))))`},
	{Ver: "ES2015", Name: "arrow, no params, block body", Src: `() => {}`, Want: `(program (expr (=> () (block))))`},
	{Ver: "ES2015", Name: "arrow returning object literal", Src: `a => ({ b: a })`, Want: `(program (expr (=> (a) (obj (prop b a)))))`},
	{Ver: "ES2015", Name: "arrow returning arrow", Src: `a => b => c`, Want: `(program (expr (=> (a) (=> (b) c))))`},
	{Ver: "ES2015", Name: "arrow with default params", Src: `(a = 1, b = 2) => a`, Want: `(program (expr (=> ((= a 1) (= b 2)) a)))`},
	{Ver: "ES2015", Name: "arrow with rest param", Src: `(...args) => args`, Want: `(program (expr (=> ((... args)) args)))`},
	{Ver: "ES2015", Name: "arrow with params + rest", Src: `(a, ...rest) => rest`, Want: `(program (expr (=> (a (... rest)) rest)))`},
	{Ver: "ES2015", Name: "arrow with array destructuring param", Src: `([a, b]) => a`, Want: `(program (expr (=> ((array a b)) a)))`},
	{Ver: "ES2015", Name: "arrow with object destructuring param", Src: `({a, b}) => a`, Want: `(program (expr (=> ((obj (short a) (short b))) a)))`},
	{Ver: "ES2015", Name: "arrow with destructured default in param", Src: `({ a = 1, b }) => a`, Want: `(program (expr (=> ((obj (short a = 1) (short b))) a)))`},
	{Ver: "ES2015", Name: "arrow with destructured param = default", Src: `({ a, b } = {}) => a`, Want: `(program (expr (=> ((= (obj (short a) (short b)) (obj))) a)))`},
	{Ver: "ES2015", Name: "arrow in conditional", Src: `x ? (b) => c : d`, Want: `(program (expr (? x (=> (b) c) d)))`},
	{Ver: "ES2015", Name: "arrow with block body ends the statement (ASI before `[`)", Src: "const f = () => {}\n[...a].forEach(g)", Want: `(program (const (= f (=> () (block)))) (expr (call (. (array (... a)) forEach) g)))`},
	{Ver: "ES2015", Name: "arrow with block body ends the statement (ASI before `(`)", Src: "const f = () => {}\n(g)()", Want: `(program (const (= f (=> () (block)))) (expr (call g)))`},
	{Ver: "ES2015", Name: "arrow with expression body continues on the next line", Src: "x = a => a\n(b)", Want: `(program (expr (= x (=> (a) (call a b)))))`},
	{Ver: "ES2015", Name: "async arrow with block body ends the statement", Src: "const f = async () => {}\n[1].map(g)", Want: `(program (const (= f (async=> () (block)))) (expr (call (. (array 1) map) g)))`},
	{Ver: "ES2015", Name: "arrow IIFE", Src: `(() => 1)()`, Want: `(program (expr (call (=> () 1))))`},
	{Ver: "ES2015", Name: "parenthesised expr is not arrow", Src: `(a, b); (c)`, Want: `(program (expr (seq a b)) (expr c))`},
	{Ver: "ES2015", Name: "template literal", Src: "`a${b}c`", Want: `(program (expr (tpl "a" b "c")))`},
	{Ver: "ES2015", Name: "template literal, no holes", Src: "`abc`", Want: `(program (expr (tpl "abc")))`},
	{Ver: "ES2015", Name: "template literal, expression hole", Src: "`${a + 1}`", Want: `(program (expr (tpl "" (+ a 1) "")))`},
	{Ver: "ES2015", Name: "template literal, object in hole", Src: "`${ {a:1}.a }`", Want: `(program (expr (tpl "" (. (obj (prop a 1)) a) "")))`},
	{Ver: "ES2015", Name: "template literal, multi-line", Src: "`a\nb${c}\nd`", Want: `(program (expr (tpl "a\nb" c "\nd")))`},
	{Ver: "ES2015", Name: "template literal, escaped $", Src: "`\\${a}`", Want: `(program (expr (tpl "\\${a}")))`},
	{Ver: "ES2015", Name: "template literal, function call with braces", Src: "`${ f(() => { return 1 }) }`", Want: `(program (expr (tpl "" (call f (=> () (block (return 1)))) "")))`},
	{Ver: "ES2015", Name: "template literal, nested template", Src: "`a${`b${c}`}d`", Want: `(program (expr (tpl "a" (tpl "b" c "") "d")))`},
	{Ver: "ES2015", Name: "template literal, 3-level nesting", Src: "`${`a${`b`}c`}`", Want: `(program (expr (tpl "" (tpl "a" (tpl "b") "c") "")))`},
	{Ver: "ES2015", Name: "template literal, '}' inside string in hole", Src: "`${ '}' }`", Want: `(program (expr (tpl "" '}' "")))`},
	{Ver: "ES2015", Name: "template literal, backtick inside string in hole", Src: "`${ '`' }x`", Want: `(program (expr (tpl "" '` + "`" + `' "x")))`},
	{Ver: "ES2015", Name: "tagged template", Src: "tag`a${b}c`", Want: `(program (expr (tagged tag (tpl "a" b "c"))))`},
	{Ver: "ES2015", Name: "tagged template, member tag", Src: "String.raw`x\\n`", Want: `(program (expr (tagged (. String raw) (tpl "x\\n"))))`},
	{Ver: "ES2015", Name: "tagged template, call result", Src: "f(x)`y`", Want: `(program (expr (tagged (call f x) (tpl "y"))))`},
	{Ver: "ES2015", Name: "object destructuring declaration", Src: `const {a, b: c, ...rest} = o;`, Want: `(program (const (= (obj (short a) (prop b c) (... rest)) o)))`},
	{Ver: "ES2015", Name: "destructuring with computed / string / numeric keys", Src: `const {[k]: v, 'a-b': w, 1: x} = o;`, Want: `(program (const (= (obj (prop [k] v) (prop 'a-b' w) (prop 1 x)) o)))`},
	{Ver: "ES2015", Name: "new with template-tagged member callee", Src: "new a[b]`x`", Want: `(program (expr (new (tagged ([] a b) (tpl "x")))))`},
	{Ver: "ES2015", Name: "array destructuring declaration", Src: `const [x, , y = 1, ...z] = arr;`, Want: `(program (const (= (array x <hole> (= y 1) (... z)) arr)))`},
	{Ver: "ES2015", Name: "nested destructuring with defaults", Src: `const { a: { b = 2 } = {} } = o;`, Want: `(program (const (= (obj (prop a (= (obj (short b = 2)) (obj)))) o)))`},
	{Ver: "ES2015", Name: "array destructuring assignment / swap", Src: `[a, b] = [b, a];`, Want: `(program (expr (= (array a b) (array b a))))`},
	{Ver: "ES2015", Name: "object destructuring assignment", Src: `({a, b} = o);`, Want: `(program (expr (= (obj (short a) (short b)) o)))`},
	{Ver: "ES2015", Name: "object destructuring assignment with default", Src: `({a = 1} = o);`, Want: `(program (expr (= (obj (short a = 1)) o)))`},
	{Ver: "ES2015", Name: "default and destructured parameters", Src: `function f(a = 1, {b, c} = {}, [d] = []) {}`, Want: `(program (function f ((= a 1) (= (obj (short b) (short c)) (obj)) (= (array d) (array))) (block)))`},
	{Ver: "ES2015", Name: "rest parameter", Src: `function f(a, ...rest) {}`, Want: `(program (function f (a (... rest)) (block)))`},
	{Ver: "ES2015", Name: "spread in call / array / object", Src: `f(...a, b); [...a, ...b]; ({...o})`, Want: `(program (expr (call f (... a) b)) (expr (array (... a) (... b))) (expr (obj (... o))))`},
	{Ver: "ES2015", Name: "class declaration", Src: `class A extends B { constructor() { super(); } m() {} static s() {} get g() { return 1 } set g(v) {} }`, Want: `(program (class A < B (method constructor (function () (block (expr (call super))))) (method m (function () (block))) (static method s (function () (block))) (get g (function () (block (return 1)))) (set g (function (v) (block)))))`},
	{Ver: "ES2015", Name: "class, computed method names", Src: `class A { [k]() {} static [Symbol.iterator]() {} }`, Want: `(program (class A (method [k] (function () (block))) (static method [(. Symbol iterator)] (function () (block)))))`},
	{Ver: "ES2015", Name: "class, extends call expression", Src: `class A extends mixin(B, C) {}`, Want: `(program (class A < (call mixin B C)))`},
	{Ver: "ES2015", Name: "class, semicolons between members", Src: `class A { m() {}; n() {}; }`, Want: `(program (class A (method m (function () (block))) (method n (function () (block)))))`},
	{Ver: "ES2015", Name: "class, methods named get/set/keywords", Src: `class A { get() {} set() {} delete() {} static new() {} }`, Want: `(program (class A (method get (function () (block))) (method set (function () (block))) (method delete (function () (block))) (static method new (function () (block)))))`},
	{Ver: "ES2015", Name: "class expression", Src: `const A = class {};`, Want: `(program (const (= A (class-expr))))`},
	{Ver: "ES2015", Name: "named class expression with extends", Src: `const A = class B extends C {};`, Want: `(program (const (= A (class-expr B < C))))`},
	{Ver: "ES2015", Name: "class expression as argument", Src: `f(class { m() {} })`, Want: `(program (expr (call f (class-expr (method m (function () (block)))))))`},
	{Ver: "ES2015", Name: "generator function", Src: `function* g() { yield 1; yield* h(); const x = yield; }`, Want: `(program (function* g () (block (expr (yield 1)) (expr (yield* (call h))) (const (= x (yield))))))`},
	{Ver: "ES2015", Name: "generator expression", Src: `const g = function* () {};`, Want: `(program (const (= g (function* () (block)))))`},
	{Ver: "ES2015", Name: "for-of", Src: `for (const x of xs) f(x);`, Want: `(program (for-of (const x) xs (expr (call f x))))`},
	{Ver: "ES2015", Name: "for-of with destructuring", Src: `for (const [k, v] of m) {}`, Want: `(program (for-of (const (array k v)) m (block)))`},
	{Ver: "ES2015", Name: "for-of without declaration", Src: `for (x of xs) f(x);`, Want: `(program (for-of (expr x) xs (expr (call f x))))`},
	{Ver: "ES2015", Name: "for-in with let", Src: `for (let k in o) {}`, Want: `(program (for-in (let k) o (block)))`},
	{Ver: "ES2015", Name: "shorthand + method + generator props", Src: `({a, b, m() {}, *g() {}, async am() {}, async *ag() {}})`, Want: `(program (expr (obj (short a) (short b) (method m (function () (block))) (method g (function* () (block))) (method am (async function () (block))) (method ag (async function* () (block))))))`},
	{Ver: "ES2015", Name: "computed property key", Src: `({[a + b]: 1})`, Want: `(program (expr (obj (prop [(+ a b)] 1))))`},
	{Ver: "ES2015", Name: "string / number method keys", Src: `({ 'm'() {}, 1() {} })`, Want: `(program (expr (obj (method 'm' (function () (block))) (method 1 (function () (block))))))`},
	{Ver: "ES2015", Name: "new.target", Src: `function F() { new.target; }`, Want: `(program (function F () (block (expr (. new target)))))`},
	{Ver: "ES2015", Name: "super property", Src: `class A extends B { m() { return super.m(); } }`, Want: `(program (class A < B (method m (function () (block (return (call (. super m))))))))`},
	{Ver: "ES2015", Name: "binary / octal literals", Src: `0b11 + 0o7`, Want: `(program (expr (+ 0b11 0o7)))`},

	// modules
	{Ver: "ES2015", Name: "import: side-effect only", Src: `import 'a';`, Want: `(program (import 'a'))`},
	{Ver: "ES2015", Name: "import: default", Src: `import d from 'a';`, Want: `(program (import def:d 'a'))`},
	{Ver: "ES2015", Name: "import: namespace", Src: `import * as ns from 'a';`, Want: `(program (import ns:ns 'a'))`},
	{Ver: "ES2015", Name: "import: named + alias", Src: `import {a, b as c} from 'x';`, Want: `(program (import {a b>c} 'x'))`},
	{Ver: "ES2015", Name: "import: trailing comma, multi-line", Src: "import {\n  a,\n  b,\n} from 'x'", Want: `(program (import {a b} 'x'))`},
	{Ver: "ES2015", Name: "import: default + named", Src: `import d, {a} from 'x';`, Want: `(program (import def:d {a} 'x'))`},
	{Ver: "ES2015", Name: "import: default + namespace", Src: `import d, * as ns from 'x';`, Want: `(program (import def:d ns:ns 'x'))`},
	{Ver: "ES2015", Name: "import: `default as x`", Src: `import { default as x } from 'y';`, Want: `(program (import {default>x} 'y'))`},
	{Ver: "ES2015", Name: "import: contextual-keyword default name", Src: `import of from 'y';`, Want: `(program (import def:of 'y'))`},
	{Ver: "ES2015", Name: "export: const", Src: `export const a = 1;`, Want: `(program (export (const (= a 1))))`},
	{Ver: "ES2015", Name: "export: function", Src: `export function f() {}`, Want: `(program (export (function f () (block))))`},
	{Ver: "ES2015", Name: "export: class", Src: `export class A {}`, Want: `(program (export (class A)))`},
	{Ver: "ES2015", Name: "export default: anonymous function", Src: `export default function () {}`, Want: `(program (export-default (function () (block))))`},
	{Ver: "ES2015", Name: "export default: anonymous class", Src: `export default class {}`, Want: `(program (export-default (class)))`},
	{Ver: "ES2015", Name: "export default: expression", Src: `export default 42;`, Want: `(program (export-default (expr 42)))`},
	{Ver: "ES2015", Name: "export default: object literal", Src: `export default { a: 1, b() {} };`, Want: `(program (export-default (expr (obj (prop a 1) (method b (function () (block)))))))`},
	{Ver: "ES2015", Name: "export default: arrow function", Src: `export default () => {};`, Want: `(program (export-default (expr (=> () (block)))))`},
	{Ver: "ES2015", Name: "export default: async function", Src: `export default async function f() {}`, Want: `(program (export-default (async function f () (block))))`},
	{Ver: "ES2015", Name: "export default: call expression", Src: `export default defineComponent({ name: 'x' });`, Want: `(program (export-default (expr (call defineComponent (obj (prop name 'x'))))))`},
	{Ver: "ES2015", Name: "export: specifier list", Src: `export { a, b as c };`, Want: `(program (export {a b>c}))`},
	{Ver: "ES2015", Name: "export: re-export named", Src: `export { a, b as c } from 'm';`, Want: `(program (export {a b>c} from 'm'))`},
	{Ver: "ES2015", Name: "export: re-export default", Src: `export { default } from 'm'; export { default as x } from 'm';`, Want: `(program (export {default} from 'm') (export {default>x} from 'm'))`},
	{Ver: "ES2015", Name: "export: star", Src: `export * from 'm';`, Want: `(program (export * from 'm'))`},
}

// ──────────────────────────────────────────────
//  ES2016 – ES2019
// ──────────────────────────────────────────────

var ctES2016to2019 = []ctCase{
	{Ver: "ES2016", Name: "exponent operator, right-assoc", Src: `a ** b ** c`, Want: `(program (expr (** a (** b c))))`},
	{Ver: "ES2016", Name: "exponent with unary rhs / paren lhs", Src: `2 ** -1; (-a) ** b`, Want: `(program (expr (** 2 (- 1))) (expr (** (- a) b)))`},
	{Ver: "ES2016", Name: "exponent assignment", Src: `a **= 2`, Want: `(program (expr (**= a 2)))`},

	{Ver: "ES2017", Name: "async function declaration", Src: `async function f() { await g(); }`, Want: `(program (async function f () (block (expr (await (call g))))))`},
	{Ver: "ES2017", Name: "async function expression", Src: `const f = async function () {};`, Want: `(program (const (= f (async function () (block)))))`},
	{Ver: "ES2017", Name: "async arrow, ident param", Src: `async x => x`, Want: `(program (expr (async=> (x) x)))`},
	{Ver: "ES2017", Name: "async arrow, parens", Src: `async (a, b) => a`, Want: `(program (expr (async=> (a b) a)))`},
	{Ver: "ES2017", Name: "async arrow, no params, block", Src: `async () => { await x }`, Want: `(program (expr (async=> () (block (expr (await x))))))`},
	{Ver: "ES2017", Name: "async arrow as argument", Src: `foo(async () => { await x })`, Want: `(program (expr (call foo (async=> () (block (expr (await x)))))))`},
	{Ver: "ES2017", Name: "async arrow with rest / destructuring", Src: `async ({a}, ...r) => a`, Want: `(program (expr (async=> ((obj (short a)) (... r)) a)))`},
	{Ver: "ES2017", Name: "async as identifier: assignment and call", Src: `async = 1; async(1)`, Want: `(program (expr (= async 1)) (expr (call async 1)))`},
	{Ver: "ES2017", Name: "async call with non-pattern argument", Src: `async(a + b)`, Want: `(program (expr (call async (+ a b))))`},
	{Ver: "ES2017", Name: "async class methods", Src: `class A { async m() {} static async s() {} async *g() {} }`, Want: `(program (class A (method m (async function () (block))) (static method s (async function () (block))) (method g (async function* () (block)))))`},
	{Ver: "ES2017", Name: "class generator method", Src: `class A { *g() {} static *s() {} }`, Want: `(program (class A (method g (function* () (block))) (static method s (function* () (block)))))`},
	{Ver: "ES2017", Name: "object method named async", Src: `({ async() {} })`, Want: `(program (expr (obj (method async (function () (block))))))`},
	{Ver: "ES2017", Name: "object shorthand named async", Src: `({ async })`, Want: `(program (expr (obj (short async))))`},
	{Ver: "ES2017", Name: "object property named async / static", Src: `({ async: 1, static: 2 })`, Want: `(program (expr (obj (prop async 1) (prop static 2))))`},
	{Ver: "ES2017", Name: "trailing commas in params and args", Src: `function f(a, b,) {} f(1, 2,)`, Want: `(program (function f (a b) (block)) (expr (call f 1 2)))`},
	{Ver: "ES2017", Name: "await in various positions", Src: `async function f() { const x = await a; return (await b).c + await d(); }`, Want: `(program (async function f () (block (const (= x (await a))) (return (+ (. (await b) c) (await (call d)))))))`},

	{Ver: "ES2018", Name: "object spread / rest", Src: `const {a, ...r} = o; const p = {...o, b: 1};`, Want: `(program (const (= (obj (short a) (... r)) o)) (const (= p (obj (... o) (prop b 1)))))`},
	{Ver: "ES2018", Name: "for await…of", Src: `async function f() { for await (const x of y) {} }`, Want: `(program (async function f () (block (for-await-of (const x) y (block)))))`},
	{Ver: "ES2018", Name: "async generator declaration", Src: `async function* g() { yield 1; }`, Want: `(program (async function* g () (block (expr (yield 1)))))`},
	{Ver: "ES2018", Name: "async generator expression", Src: `const g = async function* () {};`, Want: `(program (const (= g (async function* () (block)))))`},
	{Ver: "ES2018", Name: "regex: named groups, lookbehind, unicode props, s flag", Src: `x = /(?<y>\d{4})(?<=a)\p{L}./su;`, Want: `(program (expr (= x /(?<y>\d{4})(?<=a)\p{L}./su)))`},

	{Ver: "ES2019", Name: "optional catch binding", Src: `try { a() } catch { b() }`, Want: `(program (try (block (expr (call a))) (catch (block (expr (call b))))))`},
	{Ver: "ES2019", Name: "U+2028 inside string (JSON superset)", Src: "x = 'a\u2028b'", Want: "(program (expr (= x 'a\u2028b')))"},
}

// ──────────────────────────────────────────────
//  ES2020 – ES2025
// ──────────────────────────────────────────────

var ctES2020plus = []ctCase{
	{Ver: "ES2020", Name: "optional member / index / call", Src: `a?.b; a?.[b]; a?.()`, Want: `(program (expr (?. a b)) (expr (?[] a b)) (expr (?call a)))`},
	{Ver: "ES2020", Name: "optional chain continues", Src: `a?.b.c(d)?.e`, Want: `(program (expr (?. (call (. (?. a b) c) d) e)))`},
	{Ver: "ES2020", Name: "optional chain vs ternary with .5", Src: `a?.5:1`, Want: `(program (expr (? a .5 1)))`},
	{Ver: "ES2020", Name: "nullish coalescing", Src: `a ?? b`, Want: `(program (expr (?? a b)))`},
	{Ver: "ES2020", Name: "nullish with parenthesised logical", Src: `a ?? (b || c)`, Want: `(program (expr (?? a (|| b c))))`},
	{Ver: "ES2020", Name: "BigInt literals", Src: `10n; 0xFFn`, Want: `(program (expr 10n) (expr 0xFFn))`},
	{Ver: "ES2020", Name: "dynamic import", Src: `import('./a.js')`, Want: `(program (expr (call import './a.js')))`},
	{Ver: "ES2020", Name: "dynamic import with await", Src: `const m = await import('x');`, Want: `(program (const (= m (await (call import 'x')))))`},
	{Ver: "ES2020", Name: "dynamic import chained", Src: `const C = () => import(p).then(m => m.default);`, Want: `(program (const (= C (=> () (call (. (call import p) then) (=> (m) (. m default)))))))`},
	{Ver: "ES2020", Name: "import.meta", Src: `import.meta.url`, Want: `(program (expr (. (. import meta) url)))`},
	{Ver: "ES2020", Name: "export * as ns", Src: `export * as ns from 'm';`, Want: `(program (export *>ns from 'm'))`},
	{Ver: "ES2020", Name: "globalThis", Src: `globalThis.x = 1`, Want: `(program (expr (= (. globalThis x) 1)))`},

	{Ver: "ES2021", Name: "logical assignment", Src: `a ||= b; a &&= b; a ??= b`, Want: `(program (expr (||= a b)) (expr (&&= a b)) (expr (??= a b)))`},
	{Ver: "ES2021", Name: "numeric separators (integers)", Src: `1_000_000; 0xFF_FF; 0b1010_0001; 1_0n`, Want: `(program (expr 1_000_000) (expr 0xFF_FF) (expr 0b1010_0001) (expr 1_0n))`},
	{Ver: "ES2021", Name: "numeric separators (fraction)", Src: `3.14_15; 1_0.0_1`, Want: `(program (expr 3.14_15) (expr 1_0.0_1))`},
	{Ver: "ES2021", Name: "numeric separators (exponent)", Src: `1e1_0`, Want: `(program (expr 1e1_0))`},

	{Ver: "ES2022", Name: "class fields", Src: `class A { x = 1; static z = 2; static w = f(); }`, Want: `(program (class A (field x 1) (static field z 2) (static field w (call f))))`},
	{Ver: "ES2022", Name: "class field without initializer", Src: `class A { y; static w; }`, Want: `(program (class A (field y) (static field w)))`},
	{Ver: "ES2022", Name: "class fields separated by ASI", Src: "class A {\n  x = 1\n  y = 2\n  m() {}\n}", Want: `(program (class A (field x 1) (field y 2) (method m (function () (block)))))`},
	{Ver: "ES2022", Name: "class field with arrow function", Src: `class A { h = () => this.x; }`, Want: `(program (class A (field h (=> () (. this x)))))`},
	{Ver: "ES2022", Name: "class fields named get / set / static", Src: `class A { get = 1; set = 2; }`, Want: `(program (class A (field get 1) (field set 2)))`},
	{Ver: "ES2022", Name: "private field and access", Src: `class A { #x = 1; f() { return this.#x; } }`, Want: `(program (class A (field #x 1) (method f (function () (block (return (. this #x)))))))`},
	{Ver: "ES2022", Name: "private methods / accessors / static", Src: `class A { #m() {} get #g() { return 1 } static #s = 2; f() { this.#m(); } }`, Want: `(program (class A (method #m (function () (block))) (get #g (function () (block (return 1)))) (static field #s 2) (method f (function () (block (expr (call (. this #m))))))))`},
	{Ver: "ES2022", Name: "`#x in obj` brand check", Src: `class A { #x; static has(o) { return #x in o; } }`, Want: `(program (class A (field #x) (static method has (function (o) (block (return (in #x o)))))))`},
	{Ver: "ES2022", Name: "optional chain on private member", Src: `class A { #x; f(o) { return o?.#x; } }`, Want: `(program (class A (field #x) (method f (function (o) (block (return (?. o #x)))))))`},
	{Ver: "ES2022", Name: "static initialisation block", Src: `class A { static { init(); } }`, Want: `(program (class A (static-block (block (expr (call init))))))`},
	{Ver: "ES2022", Name: "top-level await", Src: `await x; const y = await z;`, Want: `(program (expr (await x)) (const (= y (await z))))`},
	{Ver: "ES2022", Name: "top-level for await", Src: `for await (const x of y) {}`, Want: `(program (for-await-of (const x) y (block)))`},
	{Ver: "ES2022", Name: "regex `d` flag", Src: `/a(b)/d.exec(s)`, Want: `(program (expr (call (. /a(b)/d exec) s)))`},

	{Ver: "ES2023", Name: "hashbang", Src: "#!/usr/bin/env node\nx", Want: `(program (expr x))`},

	{Ver: "ES2024", Name: "regex `v` flag", Src: `/[\p{L}--[a-z]]/v.test(s)`, Want: `(program (expr (call (. /[\p{L}--[a-z]]/v test) s)))`},

	{Ver: "ES2025", Name: "import attributes (with)", Src: `import data from './d.json' with { type: 'json' };`, Want: `(program (import def:data './d.json' with{type=json}))`},
	{Ver: "ES2025", Name: "export … from with attributes", Src: `export { default } from './d.json' with { type: 'json' };`, Want: `(program (export {default} from './d.json' with{type=json}))`},
	{Ver: "ES2025", Name: "dynamic import with options", Src: `import('./d.json', { with: { type: 'json' } })`, Want: `(program (expr (call import './d.json' (obj (prop with (obj (prop type 'json')))))))`},
	{Ver: "ES2025", Name: "regex modifiers", Src: `/(?i:a)b/.test(s)`, Want: `(program (expr (call (. /(?i:a)b/ test) s)))`},
}

// ──────────────────────────────────────────────
//  Lexical grammar, regex/division and ASI
// ──────────────────────────────────────────────

var ctLexical = []ctCase{
	{Ver: "Lexical", Name: "unicode identifiers", Src: `const café = 1; const π = 3.14;`, Want: `(program (const (= café 1)) (const (= π 3.14)))`},
	{Ver: "Lexical", Name: "cyrillic identifiers", Src: `const переменная = 2; функция(переменная);`, Want: `(program (const (= переменная 2)) (expr (call функция переменная)))`},
	{Ver: "Lexical", Name: "unicode in strings and comments", Src: "x = 'привет 🌍' // комментарий 🌍\ny = 1", Want: "(program (expr (= x 'привет 🌍')) (expr (= y 1)))"},
	{Ver: "Lexical", Name: "unicode in template text", Src: "`привет ${name}!`", Want: `(program (expr (tpl "привет " name "!")))`},
	{Ver: "Lexical", Name: "$ and _ identifiers", Src: `$a = _b + $_c`, Want: `(program (expr (= $a (+ _b $_c))))`},
	{Ver: "Lexical", Name: "unicode code-point escape in identifier", Src: `var \u{61}bc = 1;`, Want: `(program (var (= \u{61}bc 1)))`},
	{Ver: "Lexical", Name: "line comment ends at U+2028", Src: "a // c\u2028b", Want: `(program (expr a) (expr b))`},
	{Ver: "Lexical", Name: "unicode escape in identifier", Src: `var \u0061bc = 1;`, Want: `(program (var (= \u0061bc 1)))`},
	{Ver: "Lexical", Name: "BOM at start", Src: "\uFEFFa;", Want: `(program (expr a))`},
	{Ver: "Lexical", Name: "NBSP as whitespace", Src: "a\u00A0+\u00A0b", Want: `(program (expr (+ a b)))`},
	{Ver: "Lexical", Name: "form feed / vertical tab whitespace", Src: "a\f+\vb", Want: `(program (expr (+ a b)))`},
	{Ver: "Lexical", Name: "CRLF line endings", Src: "a\r\nb\r\n", Want: `(program (expr a) (expr b))`},
	{Ver: "Lexical", Name: "regex: `/=/` literal", Src: `s.split(/=/)`, Want: `(program (expr (call (. s split) /=/)))`},
	{Ver: "Lexical", Name: "regex: after `)` of if/while", Src: `if (x) /re/.test(y)`, Want: `(program (if x (expr (call (. /re/ test) y))))`},
	{Ver: "Lexical", Name: "regex: after block `}`", Src: "{}\n/re/.test(s)", Want: `(program (block) (expr (call (. /re/ test) s)))`},
	{Ver: "Lexical", Name: "regex: after function-body `}`", Src: "function f() { return a }\n/re/.test(s)", Want: `(program (function f () (block (return a))) (expr (call (. /re/ test) s)))`},
	{Ver: "Lexical", Name: "division after ++ / ) / ]", Src: `a++ / 2; (a) / 2; b[0] / 2`, Want: `(program (expr (/ (post++ a) 2)) (expr (/ a 2)) (expr (/ ([] b 0) 2)))`},
	{Ver: "Lexical", Name: "regex after => and in args", Src: `f(x => /a/.test(x), /b/g)`, Want: `(program (expr (call f (=> (x) (call (. /a/ test) x)) /b/g)))`},
	{Ver: "Lexical", Name: "number: trailing dot", Src: `x = 5.;`, Want: `(program (expr (= x 5.)))`},
	{Ver: "Lexical", Name: "number: member access on literal", Src: `1..toString()`, Want: `(program (expr (call (. 1. toString))))`},
	{Ver: "Lexical", Name: "keyword-named identifiers as members", Src: `a.of.from.as.static.let`, Want: `(program (expr (. (. (. (. (. a of) from) as) static) let)))`},
	{Ver: "Lexical", Name: "contextual keywords as variables", Src: `var of = 1, from = 2, as = 3; of + from + as`, Want: `(program (var (= of 1) (= from 2) (= as 3)) (expr (+ (+ of from) as)))`},
	{Ver: "Lexical", Name: "contextual keyword function name", Src: `function from() {} function of() {}`, Want: `(program (function from () (block)) (function of () (block)))`},

	// ASI — automatic semicolon insertion
	{Ver: "ASI", Name: "newline separates statements", Src: "a = 1\nb = 2\n", Want: `(program (expr (= a 1)) (expr (= b 2)))`},
	{Ver: "ASI", Name: "no ASI before `(`", Src: "x\n(y)", Want: `(program (expr (call x y)))`},
	{Ver: "ASI", Name: "no ASI before `[`", Src: "x\n[1]", Want: `(program (expr ([] x 1)))`},
	{Ver: "ASI", Name: "leading semicolon guard", Src: "var a = 1\n;[1, 2].forEach(f)", Want: `(program (var (= a 1)) (expr (call (. (array 1 2) forEach) f)))`},
	{Ver: "ASI", Name: "return + newline", Src: "function f() {\n  return\n  1\n}", Want: `(program (function f () (block (return) (expr 1))))`},
	{Ver: "ASI", Name: "continue label + newline", Src: "a: for (;;) { continue\n a }", Want: `(program (label a (for <nil> <nil> <nil> (block (continue) (expr a)))))`},
	{Ver: "ASI", Name: "postfix ++ needs same line", Src: "a\n++b", Want: `(program (expr a) (expr (++ b)))`},
	{Ver: "ASI", Name: "yield + newline", Src: "function* g() { yield\n 1 }", Want: `(program (function* g () (block (expr (yield)) (expr 1))))`},
	{Ver: "ASI", Name: "async + newline + function", Src: "async\nfunction f() {}", Want: `(program (expr async) (function f () (block)))`},
	{Ver: "ASI", Name: "U+2028 is a line terminator", Src: "a\u2028b", Want: `(program (expr a) (expr b))`},
	{Ver: "ASI", Name: "statement ends at `}` without semicolon", Src: `function f() { return 1 }`, Want: `(program (function f () (block (return 1))))`},
	{Ver: "ASI", Name: "do-while may omit semicolon", Src: `do a(); while (b) c()`, Want: `(program (do-while (expr (call a)) b) (expr (call c)))`},
	{Ver: "ASI", Name: "ASI via multi-line comment", Src: "a /* x\n */ b", Want: `(program (expr a) (expr b))`},
}

// ──────────────────────────────────────────────
//  Proposals and legacy syntax (not part of the supported language yet)
// ──────────────────────────────────────────────

var ctBeyond = []ctCase{
	{Ver: "Proposals", Name: "decorator on class", Src: `@sealed class A {}`, Gap: "decorators are not implemented (`@` is an illegal character); needs JsDecorator nodes on classes/members"},
	{Ver: "Proposals", Name: "decorator on member", Src: `class A { @log m() {} @observable x = 1 }`, Gap: "decorators are not implemented"},
	{Ver: "Proposals", Name: "using declaration", Src: `{ using res = getResource(); }`, Want: `(program (block (using (= res (call getResource)))))`, Gap: "explicit resource management: `using` / `await using` declarations not implemented"},
	{Ver: "Proposals", Name: "await using declaration", Src: `async function f() { await using res = getResource(); }`, Want: `(program (async function f () (block (await using (= res (call getResource))))))`, Gap: "explicit resource management not implemented"},
	{Ver: "Proposals", Name: "import defer", Src: `import defer * as ns from './x.js';`, Gap: "phase imports (`import defer`) not implemented"},
	{Ver: "Proposals", Name: "import source (phase imports)", Src: `import source mod from './x.wasm';`, Gap: "phase imports (`import source`) not implemented"},
	{Ver: "Proposals", Name: "class accessor keyword", Src: `class A { accessor x = 1; static accessor y; }`, Want: `(program (class A (accessor field x 1) (static accessor field y)))`, Gap: "`accessor` class members (decorators proposal) not implemented"},
	{Ver: "Legacy", Name: "HTML-like comment (Annex B, scripts only)", Src: "x = 1 <!-- comment\ny = 2", Want: `(program (expr (= x 1)) (expr (= y 2)))`, Gap: "`<!--` / `-->` comments (legacy, non-module scripts only) are not recognised; deliberately low priority"},
	{Ver: "Legacy", Name: "await as identifier (non-module script)", Src: `var await = 1; await + 1`, Want: `(program (var (= await 1)) (expr (+ await 1)))`, Gap: "`await` is always an operator (component scripts are modules)"},
	{Ver: "Legacy", Name: "yield as identifier (non-generator, sloppy)", Src: `var yield = 1; yield + 1`, Want: `(program (var (= yield 1)) (expr (+ yield 1)))`, Gap: "`yield` is always an operator, even outside generators"},
	{Ver: "Legacy", Name: "function declaration in if (Annex B)", Src: `if (a) function f() {}`, Want: `(program (if a (function f () (block))))`},
	{Ver: "Legacy", Name: "labelled function (Annex B)", Src: `l: function f() {}`, Want: `(program (label l (function f () (block))))`},
}

func TestCompatFeatures(t *testing.T) {
	ctRunCases(t, ctES5)
	ctRunCases(t, ctES2015)
	ctRunCases(t, ctES2016to2019)
	ctRunCases(t, ctES2020plus)
	ctRunCases(t, ctLexical)
	ctRunCases(t, ctBeyond)
}

// BigInt literals get their own Kind so consumers can tell them apart.
func TestCompatLiteralKinds(t *testing.T) {
	cases := map[string]string{
		`1`: "number", `0xFF`: "number", `1_000`: "number", `.5`: "number", `1e3`: "number",
		`10n`: "bigint", `0xFFn`: "bigint", `'s'`: "string", `"s"`: "string", `/r/g`: "regex",
		`true`: "boolean", `false`: "boolean", `null`: "null",
	}
	for src, want := range cases {
		r := ctParse(src)
		lits := ctFindAll(r.prog, "JsLiteral")
		if len(lits) != 1 {
			t.Errorf("%q: %d literals", src, len(lits))
			continue
		}
		s, _ := ctStruct(lits[0])
		if got := ctStr(s, "Kind"); got != want {
			t.Errorf("%q: Kind=%q, want %q", src, got, want)
		}
	}
}

// Template literal invariant documented on JsTemplateLit: len(Quasis) == len(Exprs)+1.
func TestCompatTemplateQuasiInvariant(t *testing.T) {
	for _, src := range []string{"``", "`a`", "`${a}`", "`a${b}c${d}e`", "`${a}${b}`", "`${}`", "`a${`b${c}`}d`"} {
		r := ctParse(src)
		for _, n := range ctFindAll(r.prog, "JsTemplateLit") {
			s, _ := ctStruct(n)
			q, e := ctField(s, "Quasis").Len(), ctField(s, "Exprs").Len()
			if q != e+1 {
				t.Errorf("%q: len(Quasis)=%d, len(Exprs)=%d (want Quasis = Exprs+1)", src, q, e)
			}
		}
	}
}
