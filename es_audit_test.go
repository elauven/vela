package main

// es_audit_test.go — ECMAScript compliance audit for ParseJS.
//
// Each test is a single small snippet exercising one language feature.
// We classify the outcome:
//
//   PASS    — parse succeeded and (where checked) the AST shape is
//             what we expect
//   PARTIAL — parse succeeded but the AST representation is lossy
//             (e.g. parsed as a generic call rather than a dedicated
//             node)
//   FAIL    — ParseJS returned nil or panicked
//
// To run just the audit:    go test -run TestES_Audit -v
// To run the summary:       go test -run TestES_Summary  -v
//
// The summary test prints a Markdown table; the per-feature tests
// give you the granular diagnosis when something fails.

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// ─────────────────────────────────────────────
//  Audit harness
// ─────────────────────────────────────────────

type auditResult struct {
	feature string
	era     string
	src     string
	status  string // "PASS" / "PARTIAL" / "FAIL"
	note    string
}

var (
	auditMu      sync.Mutex
	auditResults []auditResult
)

func record(era, feature, src, status, note string) {
	auditMu.Lock()
	defer auditMu.Unlock()
	auditResults = append(auditResults, auditResult{
		feature: feature, era: era, src: src, status: status, note: note,
	})
}

// tryParse runs ParseJS and recovers from any panic.  Returns
// (prog, ok, panicMsg).  ok is false if parse returned nil or panicked.
func tryParse(src string) (prog *JsProgram, ok bool, panicMsg string) {
	defer func() {
		if r := recover(); r != nil {
			ok = false
			panicMsg = fmt.Sprintf("%v", r)
		}
	}()
	prog = ParseJS(src)
	if prog == nil {
		return nil, false, ""
	}
	return prog, true, ""
}

// audit runs one feature check, calling `verify` on the parsed
// program if parsing succeeded.  verify returns (status, note).
func audit(t *testing.T, era, feature, src string, verify func(*JsProgram) (string, string)) {
	t.Helper()
	prog, ok, panicMsg := tryParse(src)
	if !ok {
		note := "parse returned nil"
		if panicMsg != "" {
			note = "panic: " + panicMsg
		}
		record(era, feature, src, "FAIL", note)
		return
	}
	if verify == nil {
		record(era, feature, src, "PASS", "")
		return
	}
	status, note := verify(prog)
	record(era, feature, src, status, note)
}

// ─────────────────────────────────────────────
//  Verifiers — shared helpers for AST shape checks
// ─────────────────────────────────────────────

// firstStmt returns the first top-level statement, or nil.
func firstStmt(prog *JsProgram) JsStmt {
	if prog == nil || len(prog.Body) == 0 {
		return nil
	}
	return prog.Body[0]
}

// findFirst walks the AST and returns the first node matching `match`.
func findFirst(prog *JsProgram, match func(JsNode) bool) JsNode {
	var found JsNode
	WalkNode(prog, func(n JsNode) bool {
		if found != nil {
			return false
		}
		if match(n) {
			found = n
			return false
		}
		return true
	})
	return found
}

// hasNodeOfType returns true iff a node of the requested concrete
// type appears anywhere in the AST.
func hasNodeOfType[T JsNode](prog *JsProgram) bool {
	return findFirst(prog, func(n JsNode) bool {
		_, ok := n.(T)
		return ok
	}) != nil
}

// ─────────────────────────────────────────────
//  ES5 — variables, statements, regex, getters/setters
// ─────────────────────────────────────────────

func TestES_Audit(t *testing.T) {

	// ===== ES5 baseline =====

	audit(t, "ES5", "var declaration",
		`var x = 1;`,
		func(p *JsProgram) (string, string) {
			d, ok := firstStmt(p).(*JsVarDecl)
			if !ok || d.Kind != "var" {
				return "PARTIAL", "didn't produce JsVarDecl with Kind=var"
			}
			return "PASS", ""
		})

	audit(t, "ES5", "function declaration",
		`function f(a, b) { return a + b; }`,
		func(p *JsProgram) (string, string) {
			d, ok := firstStmt(p).(*JsFuncDecl)
			if !ok {
				return "PARTIAL", "didn't produce JsFuncDecl"
			}
			if d.Name != "f" || len(d.Params) != 2 {
				return "PARTIAL", "name or params wrong"
			}
			return "PASS", ""
		})

	audit(t, "ES5", "function expression",
		`var f = function(a) { return a; };`,
		func(p *JsProgram) (string, string) {
			if !hasNodeOfType[*JsFuncExpr](p) {
				return "PARTIAL", "no JsFuncExpr found"
			}
			return "PASS", ""
		})

	audit(t, "ES5", "named function expression",
		`var f = function fact(n) { return n <= 1 ? 1 : n * fact(n-1); };`,
		func(p *JsProgram) (string, string) {
			fe, ok := findFirst(p, func(n JsNode) bool {
				_, ok := n.(*JsFuncExpr)
				return ok
			}).(*JsFuncExpr)
			if !ok {
				return "FAIL", "no JsFuncExpr"
			}
			if fe.Name != "fact" {
				return "PARTIAL", fmt.Sprintf("name lost: got %q", fe.Name)
			}
			return "PASS", ""
		})

	audit(t, "ES5", "if/else/else-if",
		`if (a) { x; } else if (b) { y; } else { z; }`, nil)

	audit(t, "ES5", "do-while",
		`do { x++; } while (x < 10);`, nil)

	audit(t, "ES5", "switch with fallthrough",
		`switch (x) { case 1: a(); break; case 2: case 3: b(); break; default: c(); }`, nil)

	audit(t, "ES5", "try/catch/finally",
		`try { f(); } catch (e) { g(e); } finally { h(); }`, nil)

	audit(t, "ES5", "throw",
		`throw new Error("oops");`, nil)

	audit(t, "ES5", "for loop, classic three-part",
		`for (var i = 0; i < n; i++) { sum += i; }`, nil)

	audit(t, "ES5", "for-in",
		`for (var k in obj) { use(k); }`, nil)

	audit(t, "ES5", "labeled statement + break label",
		`outer: for (var i = 0; i < n; i++) { for (var j = 0; j < n; j++) { if (j > 5) break outer; } }`,
		func(p *JsProgram) (string, string) {
			brk := findFirst(p, func(n JsNode) bool {
				b, ok := n.(*JsBreakStmt)
				return ok && b.Label == "outer"
			})
			if brk == nil {
				return "PARTIAL", "labeled break not preserved"
			}
			return "PASS", ""
		})

	audit(t, "ES5", "regex literal",
		`var re = /^foo[0-9]+/gi;`,
		func(p *JsProgram) (string, string) {
			lit, _ := findFirst(p, func(n JsNode) bool {
				l, ok := n.(*JsLiteral)
				return ok && l.Kind == "regex"
			}).(*JsLiteral)
			if lit == nil {
				return "FAIL", "regex not recognised as a literal"
			}
			return "PASS", ""
		})

	audit(t, "ES5", "string with all common escapes",
		`var s = "\n\t\r\\\"\u00e9\x41";`, nil)

	audit(t, "ES5", "object literal getter/setter",
		`var o = { get x() { return 1; }, set x(v) { this._x = v; } };`,
		func(p *JsProgram) (string, string) {
			obj, _ := findFirst(p, func(n JsNode) bool {
				_, ok := n.(*JsObjectExpr)
				return ok
			}).(*JsObjectExpr)
			if obj == nil {
				return "FAIL", "no object literal"
			}
			hasGet, hasSet := false, false
			for _, prop := range obj.Props {
				if prop.Kind == "get" {
					hasGet = true
				}
				if prop.Kind == "set" {
					hasSet = true
				}
			}
			if !hasGet || !hasSet {
				return "PARTIAL", fmt.Sprintf("get/set Kind not set (got get=%v set=%v)", hasGet, hasSet)
			}
			return "PASS", ""
		})

	audit(t, "ES5", "trailing comma in object literal",
		`var o = { a: 1, b: 2, };`, nil)

	audit(t, "ES5", "trailing comma in array literal",
		`var a = [1, 2, 3,];`, nil)

	audit(t, "ES5", "comma operator",
		`var x = (a, b, c);`, nil)

	audit(t, "ES5", "typeof / instanceof / in / void / delete",
		`a = typeof x; b = y instanceof Z; c = "x" in obj; d = void 0; delete obj.x;`, nil)

	audit(t, "ES5", "hex / octal-zero / decimal numbers",
		`var a = 0xff, b = 0.5, c = 1e10, d = .5;`, nil)

	// ===== ES2015 (ES6) =====

	audit(t, "ES2015", "let declaration",
		`let x = 1;`,
		func(p *JsProgram) (string, string) {
			d, ok := firstStmt(p).(*JsVarDecl)
			if !ok || d.Kind != "let" {
				return "PARTIAL", "Kind != let"
			}
			return "PASS", ""
		})

	audit(t, "ES2015", "const declaration",
		`const x = 1;`,
		func(p *JsProgram) (string, string) {
			d, ok := firstStmt(p).(*JsVarDecl)
			if !ok || d.Kind != "const" {
				return "PARTIAL", "Kind != const"
			}
			return "PASS", ""
		})

	audit(t, "ES2015", "arrow function — concise body",
		`const f = x => x * 2;`,
		func(p *JsProgram) (string, string) {
			if !hasNodeOfType[*JsArrowFunc](p) {
				return "FAIL", "no JsArrowFunc"
			}
			return "PASS", ""
		})

	audit(t, "ES2015", "arrow function — block body, multiple params",
		`const f = (a, b) => { return a + b; };`, nil)

	audit(t, "ES2015", "arrow function — no params",
		`const f = () => 42;`, nil)

	audit(t, "ES2015", "arrow function — returns object literal",
		`const f = () => ({ x: 1 });`, nil)

	audit(t, "ES2015", "template literal — simple",
		"const s = `hello world`;", nil)

	audit(t, "ES2015", "template literal — with interpolation",
		"const s = `hello ${name}!`;",
		func(p *JsProgram) (string, string) {
			if !hasNodeOfType[*JsTemplateLit](p) {
				return "FAIL", "no JsTemplateLit"
			}
			return "PASS", ""
		})

	audit(t, "ES2015", "tagged template literal",
		"const s = tag`hello ${x}!`;", nil)

	audit(t, "ES2015", "class declaration",
		`class Foo { constructor(x) { this.x = x; } bar() { return this.x; } }`,
		func(p *JsProgram) (string, string) {
			c, ok := firstStmt(p).(*JsClassDecl)
			if !ok {
				return "FAIL", "no JsClassDecl"
			}
			if c.Name != "Foo" || len(c.Members) < 2 {
				return "PARTIAL", "name or members wrong"
			}
			return "PASS", ""
		})

	audit(t, "ES2015", "class with extends",
		`class B extends A { constructor() { super(); } }`,
		func(p *JsProgram) (string, string) {
			c, _ := firstStmt(p).(*JsClassDecl)
			if c == nil || c.SuperClass == nil {
				return "PARTIAL", "SuperClass missing"
			}
			return "PASS", ""
		})

	audit(t, "ES2015", "class with static method",
		`class K { static create() { return new K(); } }`,
		func(p *JsProgram) (string, string) {
			c, _ := firstStmt(p).(*JsClassDecl)
			if c == nil || len(c.Members) == 0 {
				return "FAIL", "no members"
			}
			if !c.Members[0].Static {
				return "PARTIAL", "static flag not set"
			}
			return "PASS", ""
		})

	audit(t, "ES2015", "class with getter/setter",
		`class K { get x() { return this._x; } set x(v) { this._x = v; } }`,
		func(p *JsProgram) (string, string) {
			c, _ := firstStmt(p).(*JsClassDecl)
			if c == nil || len(c.Members) < 2 {
				return "FAIL", "members missing"
			}
			hasGet, hasSet := false, false
			for _, m := range c.Members {
				if m.Kind == "get" {
					hasGet = true
				}
				if m.Kind == "set" {
					hasSet = true
				}
			}
			if !hasGet || !hasSet {
				return "PARTIAL", fmt.Sprintf("Kind not set (get=%v set=%v)", hasGet, hasSet)
			}
			return "PASS", ""
		})

	audit(t, "ES2015", "destructuring — array",
		`const [a, b, c] = arr;`, nil)

	audit(t, "ES2015", "destructuring — array with rest",
		`const [a, ...rest] = arr;`,
		func(p *JsProgram) (string, string) {
			if !hasNodeOfType[*JsSpreadExpr](p) {
				return "PARTIAL", "rest element not represented as spread"
			}
			return "PASS", ""
		})

	audit(t, "ES2015", "destructuring — object",
		`const { a, b } = obj;`, nil)

	audit(t, "ES2015", "destructuring — object with renaming",
		`const { a: x, b: y } = obj;`, nil)

	audit(t, "ES2015", "destructuring — with defaults",
		`const { a = 1, b = 2 } = obj;`, nil)

	audit(t, "ES2015", "destructuring — nested",
		`const { a: { b: { c } } } = obj;`, nil)

	audit(t, "ES2015", "destructuring — in function params",
		`function f({ a, b }, [x, y]) { return a + b + x + y; }`, nil)

	audit(t, "ES2015", "default parameters",
		`function f(a = 1, b = 2) { return a + b; }`, nil)

	audit(t, "ES2015", "rest parameters",
		`function f(...args) { return args.length; }`,
		func(p *JsProgram) (string, string) {
			d, _ := firstStmt(p).(*JsFuncDecl)
			if d == nil {
				return "FAIL", "no func decl"
			}
			if len(d.Params) != 1 {
				return "PARTIAL", "param count"
			}
			if _, ok := d.Params[0].(*JsSpreadExpr); !ok {
				return "PARTIAL", "rest param not a spread"
			}
			return "PASS", ""
		})

	audit(t, "ES2015", "spread in array literal",
		`const a = [1, ...others, 5];`, nil)

	audit(t, "ES2015", "spread in call",
		`f(...args);`, nil)

	audit(t, "ES2015", "for-of",
		`for (const x of iter) { use(x); }`,
		func(p *JsProgram) (string, string) {
			fo, _ := firstStmt(p).(*JsForInStmt)
			if fo == nil {
				return "FAIL", "no JsForInStmt"
			}
			if !fo.IsOf {
				return "PARTIAL", "IsOf flag not set"
			}
			return "PASS", ""
		})

	audit(t, "ES2015", "generator function",
		`function* gen() { yield 1; yield 2; }`,
		func(p *JsProgram) (string, string) {
			d, _ := firstStmt(p).(*JsFuncDecl)
			if d == nil {
				return "FAIL", "no func decl"
			}
			if !d.Generator {
				return "PARTIAL", "Generator flag not set"
			}
			return "PASS", ""
		})

	audit(t, "ES2015", "yield expression",
		`function* g() { yield 42; }`,
		func(p *JsProgram) (string, string) {
			if !hasNodeOfType[*JsYieldExpr](p) {
				return "PARTIAL", "no JsYieldExpr"
			}
			return "PASS", ""
		})

	audit(t, "ES2015", "yield delegating (yield*)",
		`function* g() { yield* other(); }`,
		func(p *JsProgram) (string, string) {
			ye, _ := findFirst(p, func(n JsNode) bool {
				_, ok := n.(*JsYieldExpr)
				return ok
			}).(*JsYieldExpr)
			if ye == nil {
				return "FAIL", "no yield expr"
			}
			if !ye.Delegate {
				return "PARTIAL", "Delegate flag not set"
			}
			return "PASS", ""
		})

	audit(t, "ES2015", "import — named",
		`import { a, b } from "mod";`, nil)

	audit(t, "ES2015", "import — default",
		`import D from "mod";`,
		func(p *JsProgram) (string, string) {
			d, _ := firstStmt(p).(*JsImportDecl)
			if d == nil {
				return "FAIL", "no JsImportDecl"
			}
			if d.DefaultName != "D" {
				return "PARTIAL", "DefaultName not set"
			}
			return "PASS", ""
		})

	audit(t, "ES2015", "import — namespace",
		`import * as Ns from "mod";`,
		func(p *JsProgram) (string, string) {
			d, _ := firstStmt(p).(*JsImportDecl)
			if d == nil {
				return "FAIL", "no JsImportDecl"
			}
			if d.Namespace != "Ns" {
				return "PARTIAL", "Namespace not set"
			}
			return "PASS", ""
		})

	audit(t, "ES2015", "import — side effect only",
		`import "polyfill";`, nil)

	audit(t, "ES2015", "import — mixed default+named",
		`import D, { a, b } from "mod";`, nil)

	audit(t, "ES2015", "import — named with alias",
		`import { foo as bar } from "mod";`,
		func(p *JsProgram) (string, string) {
			d, _ := firstStmt(p).(*JsImportDecl)
			if d == nil || len(d.Specifiers) == 0 {
				return "FAIL", "no specs"
			}
			s := d.Specifiers[0]
			if s.Imported != "foo" || s.Local != "bar" {
				return "PARTIAL", fmt.Sprintf("aliasing wrong: %s as %s", s.Imported, s.Local)
			}
			return "PASS", ""
		})

	audit(t, "ES2015", "export named",
		`export { a, b };`, nil)

	audit(t, "ES2015", "export default function",
		`export default function f() {}`, nil)

	audit(t, "ES2015", "export const",
		`export const x = 1;`, nil)

	audit(t, "ES2015", "computed property name",
		`const o = { [key]: 1, [a+b]: 2 };`, nil)

	audit(t, "ES2015", "object shorthand",
		`const o = { x, y };`,
		func(p *JsProgram) (string, string) {
			obj, _ := findFirst(p, func(n JsNode) bool {
				_, ok := n.(*JsObjectExpr)
				return ok
			}).(*JsObjectExpr)
			if obj == nil || len(obj.Props) == 0 {
				return "FAIL", "no object props"
			}
			if !obj.Props[0].Shorthand {
				return "PARTIAL", "Shorthand flag not set"
			}
			return "PASS", ""
		})

	audit(t, "ES2015", "method shorthand",
		`const o = { greet() { return "hi"; } };`, nil)

	audit(t, "ES2015", "binary number literal",
		`const x = 0b1010;`, nil)

	audit(t, "ES2015", "octal number literal (0o)",
		`const x = 0o755;`, nil)

	// ===== ES2016 =====

	audit(t, "ES2016", "exponentiation operator **",
		`const x = 2 ** 10;`,
		func(p *JsProgram) (string, string) {
			be, _ := findFirst(p, func(n JsNode) bool {
				b, ok := n.(*JsBinaryExpr)
				return ok && b.Op == "**"
			}).(*JsBinaryExpr)
			if be == nil {
				return "PARTIAL", "** not parsed as binary expr"
			}
			return "PASS", ""
		})

	audit(t, "ES2016", "exponentiation assignment **=",
		`x **= 2;`, nil)

	// ===== ES2017 =====

	audit(t, "ES2017", "async function declaration",
		`async function f() { return 1; }`,
		func(p *JsProgram) (string, string) {
			d, _ := firstStmt(p).(*JsFuncDecl)
			if d == nil {
				return "FAIL", "no func decl"
			}
			if !d.Async {
				return "PARTIAL", "Async flag not set"
			}
			return "PASS", ""
		})

	audit(t, "ES2017", "async arrow function",
		`const f = async (x) => { return await g(x); };`,
		func(p *JsProgram) (string, string) {
			af, _ := findFirst(p, func(n JsNode) bool {
				_, ok := n.(*JsArrowFunc)
				return ok
			}).(*JsArrowFunc)
			if af == nil {
				return "FAIL", "no arrow"
			}
			if !af.Async {
				return "PARTIAL", "Async flag not set on arrow"
			}
			return "PASS", ""
		})

	audit(t, "ES2017", "await expression",
		`async function f() { const x = await fetch("/api"); }`,
		func(p *JsProgram) (string, string) {
			if !hasNodeOfType[*JsAwaitExpr](p) {
				return "PARTIAL", "no JsAwaitExpr"
			}
			return "PASS", ""
		})

	audit(t, "ES2017", "trailing comma in function params",
		`function f(a, b,) { return a + b; }`, nil)

	audit(t, "ES2017", "trailing comma in function call",
		`f(1, 2,);`, nil)

	// ===== ES2018 =====

	audit(t, "ES2018", "async generator",
		`async function* gen() { yield 1; }`,
		func(p *JsProgram) (string, string) {
			d, _ := firstStmt(p).(*JsFuncDecl)
			if d == nil {
				return "FAIL", "no decl"
			}
			if !d.Async || !d.Generator {
				return "PARTIAL", fmt.Sprintf("flags: async=%v gen=%v", d.Async, d.Generator)
			}
			return "PASS", ""
		})

	audit(t, "ES2018", "for-await-of",
		`async function f() { for await (const x of iter) { use(x); } }`, nil)

	audit(t, "ES2018", "object spread",
		`const o = { ...other, x: 1 };`,
		func(p *JsProgram) (string, string) {
			obj, _ := findFirst(p, func(n JsNode) bool {
				_, ok := n.(*JsObjectExpr)
				return ok
			}).(*JsObjectExpr)
			if obj == nil {
				return "FAIL", "no object"
			}
			hasSpread := false
			for _, prop := range obj.Props {
				if prop.Spread {
					hasSpread = true
				}
			}
			if !hasSpread {
				return "PARTIAL", "Spread flag not set on prop"
			}
			return "PASS", ""
		})

	audit(t, "ES2018", "object rest in destructuring",
		`const { a, ...rest } = obj;`, nil)

	// ===== ES2019 =====

	audit(t, "ES2019", "optional catch binding",
		`try { f(); } catch { g(); }`,
		func(p *JsProgram) (string, string) {
			ts, _ := firstStmt(p).(*JsTryStmt)
			if ts == nil {
				return "FAIL", "no try"
			}
			if ts.Handler == nil {
				return "PARTIAL", "no handler"
			}
			if ts.CatchParam != nil {
				return "PARTIAL", "CatchParam should be nil"
			}
			return "PASS", ""
		})

	// ===== ES2020 =====

	audit(t, "ES2020", "optional chaining — member",
		`const x = obj?.foo;`,
		func(p *JsProgram) (string, string) {
			me, _ := findFirst(p, func(n JsNode) bool {
				m, ok := n.(*JsMemberExpr)
				return ok && m.Optional
			}).(*JsMemberExpr)
			if me == nil {
				return "PARTIAL", "Optional flag not set on member"
			}
			return "PASS", ""
		})

	audit(t, "ES2020", "optional chaining — call",
		`obj.fn?.();`,
		func(p *JsProgram) (string, string) {
			ce, _ := findFirst(p, func(n JsNode) bool {
				c, ok := n.(*JsCallExpr)
				return ok && c.Optional
			}).(*JsCallExpr)
			if ce == nil {
				return "PARTIAL", "Optional flag not set on call"
			}
			return "PASS", ""
		})

	audit(t, "ES2020", "optional chaining — computed",
		`const x = obj?.[key];`, nil)

	audit(t, "ES2020", "nullish coalescing ??",
		`const x = a ?? b;`,
		func(p *JsProgram) (string, string) {
			be, _ := findFirst(p, func(n JsNode) bool {
				if b, ok := n.(*JsLogicalExpr); ok && b.Op == "??" {
					return true
				}
				if b, ok := n.(*JsBinaryExpr); ok && b.Op == "??" {
					return true
				}
				return false
			}).(JsNode)
			if be == nil {
				return "PARTIAL", "?? not parsed as logical/binary"
			}
			return "PASS", ""
		})

	audit(t, "ES2020", "nullish coalescing assignment ??=",
		`x ??= 5;`, nil)

	audit(t, "ES2020", "logical-or assignment ||=",
		`x ||= 5;`, nil)

	audit(t, "ES2020", "logical-and assignment &&=",
		`x &&= 5;`, nil)

	audit(t, "ES2020", "BigInt literal",
		`const n = 9007199254740993n;`,
		func(p *JsProgram) (string, string) {
			lit, _ := findFirst(p, func(n JsNode) bool {
				l, ok := n.(*JsLiteral)
				if !ok {
					return false
				}
				return strings.HasSuffix(l.Raw, "n")
			}).(*JsLiteral)
			if lit == nil {
				return "FAIL", "BigInt not recognised"
			}
			if lit.Kind != "bigint" {
				return "PARTIAL", fmt.Sprintf("Kind = %q, expected 'bigint'", lit.Kind)
			}
			return "PASS", ""
		})

	audit(t, "ES2020", "dynamic import",
		`const m = await import("./mod");`, nil)

	audit(t, "ES2020", "import.meta",
		`const url = import.meta.url;`, nil)

	audit(t, "ES2020", "export * as",
		`export * as utils from "./utils";`, nil)

	audit(t, "ES2020", "globalThis (identifier)",
		`globalThis.foo = 1;`, nil)

	audit(t, "ES2020", "numeric separators",
		`const big = 1_000_000;`, nil)
}

// ─────────────────────────────────────────────
//  Summary report
//
//  Run last (Go runs tests in source order within a file).
//  Prints a Markdown table grouped by era with PASS/PARTIAL/FAIL
//  counts and a per-feature breakdown.
// ─────────────────────────────────────────────

func TestES_Summary(t *testing.T) {
	auditMu.Lock()
	defer auditMu.Unlock()

	if len(auditResults) == 0 {
		t.Skip("no audit results — run TestES_Audit first")
	}

	// Group by era preserving insertion order.
	type bucket struct {
		era  string
		rows []auditResult
	}
	var order []string
	seen := map[string]int{}
	var groups []*bucket
	for _, r := range auditResults {
		idx, ok := seen[r.era]
		if !ok {
			seen[r.era] = len(groups)
			idx = len(groups)
			groups = append(groups, &bucket{era: r.era})
			order = append(order, r.era)
		}
		groups[idx].rows = append(groups[idx].rows, r)
	}

	_ = order

	var totalP, totalT, totalF int

	for _, g := range groups {
		t.Logf("\n## %s\n", g.era)
		var p, par, f int
		t.Logf("| Status | Feature | Note |")
		t.Logf("|--------|---------|------|")
		for _, r := range g.rows {
			switch r.status {
			case "PASS":
				p++
			case "PARTIAL":
				par++
			case "FAIL":
				f++
			}
			emoji := map[string]string{
				"PASS":    "✅",
				"PARTIAL": "⚠️",
				"FAIL":    "❌",
			}[r.status]
			note := r.note
			if note == "" {
				note = "—"
			}
			t.Logf("| %s %s | %s | %s |", emoji, r.status, r.feature, note)
		}
		t.Logf("\n**%s subtotal**: %d pass, %d partial, %d fail (%d total)\n",
			g.era, p, par, f, p+par+f)
		totalP += p
		totalT += par
		totalF += f
	}

	t.Logf("\n## Overall: %d pass, %d partial, %d fail (%d total)\n",
		totalP, totalT, totalF, totalP+totalT+totalF)

	// Don't actually fail the test for partials — this is a report,
	// not an assertion.  Only fail if there are unexpected panics
	// (note: "panic: ...") since those are bugs.
	for _, r := range auditResults {
		if strings.HasPrefix(r.note, "panic:") {
			t.Errorf("PANIC in %s/%s: %s\n  src: %s", r.era, r.feature, r.note, r.src)
		}
	}
}
