package main

// jsprinter_roundtrip_test.go — the printer's correctness oracle.
//
// For any source the parser accepts, printing the parsed AST and
// re-parsing must yield a structurally equal tree.  This proves the
// printer is lossless and the AST captures everything that matters.
//
// Tested in BOTH pretty and minified modes — minified output that
// re-parses differently would be a token-merging bug.
//
// Structural equality deliberately ignores Pos/End/Origin (those
// change after a reprint) and compares only the semantically
// meaningful shape of the tree.

import (
	"fmt"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────
//  Structural equality
// ─────────────────────────────────────────────

// astEqual reports whether two nodes are structurally equal,
// ignoring source positions and origin markers.
func astEqual(a, b JsNode) bool {
	return nodeSig(a) == nodeSig(b)
}

// nodeSig produces a canonical string signature of a node's
// structure.  Two nodes are structurally equal iff their signatures
// match.  Positions and origin are excluded by construction.
func nodeSig(n JsNode) string {
	var sb strings.Builder
	writeSig(&sb, n)
	return sb.String()
}

// isNilNode detects a typed-nil interface value — a JsExpr/JsStmt
// interface whose dynamic type is a pointer but whose pointer is
// nil.  These arise from optional AST fields (e.g. a var declarator
// with no initializer) and would panic a naive type switch.
func isNilNode(n JsNode) bool {
	switch x := n.(type) {
	case *JsIdent:
		return x == nil
	case *JsLiteral:
		return x == nil
	case *JsTemplateLit:
		return x == nil
	case *JsArrayExpr:
		return x == nil
	case *JsObjectExpr:
		return x == nil
	case *JsFuncExpr:
		return x == nil
	case *JsArrowFunc:
		return x == nil
	case *JsUnaryExpr:
		return x == nil
	case *JsBinaryExpr:
		return x == nil
	case *JsLogicalExpr:
		return x == nil
	case *JsAssignExpr:
		return x == nil
	case *JsMemberExpr:
		return x == nil
	case *JsCallExpr:
		return x == nil
	case *JsNewExpr:
		return x == nil
	case *JsConditionalExpr:
		return x == nil
	case *JsSeqExpr:
		return x == nil
	case *JsSpreadExpr:
		return x == nil
	case *JsAwaitExpr:
		return x == nil
	case *JsYieldExpr:
		return x == nil
	case *JsThisExpr:
		return x == nil
	case *JsSuperExpr:
		return x == nil
	case *JsBlock:
		return x == nil
	}
	return false
}

func writeSig(sb *strings.Builder, n JsNode) {
	if n == nil || isNilNode(n) {
		sb.WriteString("∅")
		return
	}
	switch x := n.(type) {

	case *JsProgram:
		sb.WriteString("Program(")
		for _, s := range x.Body {
			writeSig(sb, s)
			sb.WriteByte(';')
		}
		sb.WriteByte(')')

	case *JsVarDecl:
		sb.WriteString("Var:" + x.Kind + "(")
		for _, d := range x.Decls {
			writeSig(sb, d.Id)
			sb.WriteByte('=')
			writeSig(sb, d.Init)
			sb.WriteByte(',')
		}
		sb.WriteByte(')')

	case *JsFuncDecl:
		fmt.Fprintf(sb, "Func:%s:a%t:g%t(", x.Name, x.Async, x.Generator)
		for _, p := range x.Params {
			writeSig(sb, p)
			sb.WriteByte(',')
		}
		sb.WriteByte('|')
		writeSig(sb, x.Body)
		sb.WriteByte(')')

	case *JsClassDecl:
		sb.WriteString("Class:" + x.Name + "(")
		writeSig(sb, x.SuperClass)
		sb.WriteByte('|')
		for _, m := range x.Members {
			fmt.Fprintf(sb, "M:%s:s%t:c%t[", m.Kind, m.Static, m.Computed)
			writeSig(sb, m.Key)
			sb.WriteByte('=')
			writeSig(sb, m.Value)
			sb.WriteByte(']')
		}
		sb.WriteByte(')')

	case *JsBlock:
		sb.WriteString("Block(")
		for _, s := range x.Body {
			writeSig(sb, s)
			sb.WriteByte(';')
		}
		sb.WriteByte(')')

	case *JsExprStmt:
		sb.WriteString("ExprStmt(")
		writeSig(sb, x.X)
		sb.WriteByte(')')

	case *JsIfStmt:
		sb.WriteString("If(")
		writeSig(sb, x.Test)
		sb.WriteByte('|')
		writeSig(sb, x.Then)
		sb.WriteByte('|')
		writeSig(sb, x.Else)
		sb.WriteByte(')')

	case *JsForStmt:
		sb.WriteString("For(")
		writeSig(sb, x.Init)
		sb.WriteByte('|')
		writeSig(sb, x.Test)
		sb.WriteByte('|')
		writeSig(sb, x.Update)
		sb.WriteByte('|')
		writeSig(sb, x.Body)
		sb.WriteByte(')')

	case *JsForInStmt:
		fmt.Fprintf(sb, "ForIn:of%t(", x.IsOf)
		writeSig(sb, x.Left)
		sb.WriteByte('|')
		writeSig(sb, x.Right)
		sb.WriteByte('|')
		writeSig(sb, x.Body)
		sb.WriteByte(')')

	case *JsWhileStmt:
		sb.WriteString("While(")
		writeSig(sb, x.Test)
		sb.WriteByte('|')
		writeSig(sb, x.Body)
		sb.WriteByte(')')

	case *JsDoWhileStmt:
		sb.WriteString("DoWhile(")
		writeSig(sb, x.Body)
		sb.WriteByte('|')
		writeSig(sb, x.Test)
		sb.WriteByte(')')

	case *JsReturnStmt:
		sb.WriteString("Return(")
		writeSig(sb, x.Arg)
		sb.WriteByte(')')

	case *JsThrowStmt:
		sb.WriteString("Throw(")
		writeSig(sb, x.Arg)
		sb.WriteByte(')')

	case *JsTryStmt:
		sb.WriteString("Try(")
		writeSig(sb, x.Block)
		sb.WriteByte('|')
		writeSig(sb, x.CatchParam)
		sb.WriteByte('|')
		writeSig(sb, x.Handler)
		sb.WriteByte('|')
		writeSig(sb, x.Finalizer)
		sb.WriteByte(')')

	case *JsSwitchStmt:
		sb.WriteString("Switch(")
		writeSig(sb, x.Disc)
		sb.WriteByte('|')
		for _, c := range x.Cases {
			sb.WriteString("Case[")
			writeSig(sb, c.Test)
			sb.WriteByte(':')
			for _, s := range c.Body {
				writeSig(sb, s)
				sb.WriteByte(';')
			}
			sb.WriteByte(']')
		}
		sb.WriteByte(')')

	case *JsBreakStmt:
		sb.WriteString("Break:" + x.Label)

	case *JsContinueStmt:
		sb.WriteString("Continue:" + x.Label)

	case *JsLabeledStmt:
		sb.WriteString("Labeled:" + x.Label + "(")
		writeSig(sb, x.Body)
		sb.WriteByte(')')

	case *JsEmptyStmt:
		sb.WriteString("Empty")

	case *JsDebuggerStmt:
		sb.WriteString("Debugger")

	case *JsImportDecl:
		fmt.Fprintf(sb, "Import:%s:%s:%s(", x.DefaultName, x.Namespace, x.Source)
		for _, s := range x.Specifiers {
			fmt.Fprintf(sb, "%s>%s,", s.Imported, s.Local)
		}
		sb.WriteByte(')')

	case *JsExportDecl:
		fmt.Fprintf(sb, "Export:d%t(", x.Default)
		writeSig(sb, x.Declaration)
		sb.WriteByte(')')

	// ── Expressions ──

	case *JsIdent:
		sb.WriteString("Id:" + x.Name)

	case *JsLiteral:
		sb.WriteString("Lit:" + x.Kind + ":" + normalizeLiteral(x))

	case *JsThisExpr:
		sb.WriteString("This")

	case *JsSuperExpr:
		sb.WriteString("Super")

	case *JsTemplateLit:
		sb.WriteString("Tmpl(")
		for i, q := range x.Quasis {
			sb.WriteString("q:" + q + ";")
			if i < len(x.Exprs) {
				writeSig(sb, x.Exprs[i])
				sb.WriteByte(';')
			}
		}
		sb.WriteByte(')')

	case *JsArrayExpr:
		sb.WriteString("Arr(")
		for _, el := range x.Elems {
			writeSig(sb, el)
			sb.WriteByte(',')
		}
		sb.WriteByte(')')

	case *JsObjectExpr:
		sb.WriteString("Obj(")
		for _, pr := range x.Props {
			fmt.Fprintf(sb, "P:%s:c%t:sh%t:sp%t:m%t[", pr.Kind, pr.Computed, pr.Shorthand, pr.Spread, pr.Method)
			writeSig(sb, pr.Key)
			sb.WriteByte('=')
			writeSig(sb, pr.Value)
			sb.WriteByte(']')
		}
		sb.WriteByte(')')

	case *JsFuncExpr:
		fmt.Fprintf(sb, "FuncExpr:%s:a%t:g%t(", x.Name, x.Async, x.Generator)
		for _, p := range x.Params {
			writeSig(sb, p)
			sb.WriteByte(',')
		}
		sb.WriteByte('|')
		writeSig(sb, x.Body)
		sb.WriteByte(')')

	case *JsArrowFunc:
		fmt.Fprintf(sb, "Arrow:a%t(", x.Async)
		for _, p := range x.Params {
			writeSig(sb, p)
			sb.WriteByte(',')
		}
		sb.WriteByte('|')
		writeSig(sb, x.Body)
		sb.WriteByte(')')

	case *JsUnaryExpr:
		fmt.Fprintf(sb, "Unary:%s:pre%t(", x.Op, x.Prefix)
		writeSig(sb, x.X)
		sb.WriteByte(')')

	case *JsBinaryExpr:
		sb.WriteString("Bin:" + x.Op + "(")
		writeSig(sb, x.L)
		sb.WriteByte('|')
		writeSig(sb, x.R)
		sb.WriteByte(')')

	case *JsLogicalExpr:
		sb.WriteString("Logical:" + x.Op + "(")
		writeSig(sb, x.L)
		sb.WriteByte('|')
		writeSig(sb, x.R)
		sb.WriteByte(')')

	case *JsAssignExpr:
		sb.WriteString("Assign:" + x.Op + "(")
		writeSig(sb, x.L)
		sb.WriteByte('|')
		writeSig(sb, x.R)
		sb.WriteByte(')')

	case *JsMemberExpr:
		fmt.Fprintf(sb, "Member:c%t:o%t(", x.Computed, x.Optional)
		writeSig(sb, x.Obj)
		sb.WriteByte('|')
		writeSig(sb, x.Prop)
		sb.WriteByte(')')

	case *JsCallExpr:
		fmt.Fprintf(sb, "Call:o%t(", x.Optional)
		writeSig(sb, x.Callee)
		sb.WriteByte('|')
		for _, a := range x.Args {
			writeSig(sb, a)
			sb.WriteByte(',')
		}
		sb.WriteByte(')')

	case *JsNewExpr:
		sb.WriteString("New(")
		writeSig(sb, x.Callee)
		sb.WriteByte('|')
		for _, a := range x.Args {
			writeSig(sb, a)
			sb.WriteByte(',')
		}
		sb.WriteByte(')')

	case *JsConditionalExpr:
		sb.WriteString("Cond(")
		writeSig(sb, x.Test)
		sb.WriteByte('|')
		writeSig(sb, x.Then)
		sb.WriteByte('|')
		writeSig(sb, x.Else)
		sb.WriteByte(')')

	case *JsSeqExpr:
		sb.WriteString("Seq(")
		for _, e := range x.Exprs {
			writeSig(sb, e)
			sb.WriteByte(',')
		}
		sb.WriteByte(')')

	case *JsSpreadExpr:
		sb.WriteString("Spread(")
		writeSig(sb, x.X)
		sb.WriteByte(')')

	case *JsAwaitExpr:
		sb.WriteString("Await(")
		writeSig(sb, x.X)
		sb.WriteByte(')')

	case *JsYieldExpr:
		fmt.Fprintf(sb, "Yield:d%t(", x.Delegate)
		writeSig(sb, x.X)
		sb.WriteByte(')')

	default:
		fmt.Fprintf(sb, "UNKNOWN:%T", n)
	}
}

// normalizeLiteral canonicalizes a literal's raw text so that
// cosmetic differences (quote style) don't break round-trip
// equality.  Numbers and regexes are compared verbatim; strings
// are compared by their decoded content modulo quote choice.
func normalizeLiteral(l *JsLiteral) string {
	if l.Kind == "string" {
		// Compare by inner content, ignoring which quote was used.
		inner := l.Raw
		if len(inner) >= 2 {
			inner = inner[1 : len(inner)-1]
		}
		return inner
	}
	return l.Raw
}

// ─────────────────────────────────────────────
//  The round-trip test
// ─────────────────────────────────────────────

func TestPrinterRoundTrip(t *testing.T) {
	cases := []string{
		// literals & identifiers
		`const a = 1;`,
		`let s = "hello";`,
		`let n = null;`,
		`let b = true;`,
		`let f = 3.14;`,
		`let big = 42n;`,
		`let re = /ab+c/gi;`,

		// expressions & precedence
		`let x = a + b * c;`,
		`let x = (a + b) * c;`,
		`let x = a * b + c;`,
		`let x = a - b - c;`,
		`let x = a ** b ** c;`,
		`let x = -a + b;`,
		`let x = a && b || c;`,
		`let x = a ?? b ?? c;`,
		`let x = a ? b : c;`,
		`let x = a ? b ? c : d : e;`,
		`let x = !a;`,
		`let x = typeof a;`,
		`let x = void 0;`,
		`let x = delete a.b;`,
		`let x = a in b;`,
		// in inside a for initializer must stay parenthesised
		`for (let i = ('a' in o) ? 1 : 0; i < 3; i++) {}`,
		`for (let i = 0, j = ('k' in o); i < j; i++) {}`,
		`for (('k' in o); ;) break;`,
		`let x = a instanceof B;`,
		`let x = - -a;`,
		`let x = a, b, c;`,

		// member / call / new
		`foo.bar.baz;`,
		`foo[bar][baz];`,
		`foo?.bar;`,
		`foo?.[bar];`,
		`foo?.();`,
		`fn(a, b, c);`,
		`new Thing(x);`,
		`new Thing;`,
		`obj.method().chain().more();`,

		// functions
		`function f(a, b) { return a + b; }`,
		`async function g() { await x; }`,
		`function* gen() { yield 1; yield* other; }`,
		`const h = (x) => x * 2;`,
		`const h = x => x * 2;`,
		`const h = () => ({ a: 1 });`,
		`const h = async (x) => { return x; };`,

		// objects & arrays
		`let o = { a: 1, b: 2 };`,
		`let o = { a, b, c };`,
		`let o = { [k]: v };`,
		`let o = { ...rest, a: 1 };`,
		`let o = { get x() { return 1; } };`,
		`let o = { m() { return 1; } };`,
		`let o = { async m() { await x; } };`,
		`let o = { *gen() { yield 1; } };`,
		`let o = { m() {}, n: 2, p() {} };`,
		`let a = [1, 2, 3];`,
		`let a = [1, , 3];`,
		`let a = [...xs, 4];`,

		// destructuring
		`const { a, b } = obj;`,
		`const [x, y] = arr;`,
		`const { a, b: c } = obj;`,
		`const [x, ...rest] = arr;`,
		`const { a = 1 } = obj;`,

		// statements
		`if (a) { b(); }`,
		`if (a) { b(); } else { c(); }`,
		`if (a) { b(); } else if (c) { d(); } else { e(); }`,
		`for (let i = 0; i < 10; i++) { f(i); }`,
		`for (const x of xs) { g(x); }`,
		`for (const k in obj) { h(k); }`,
		`while (a) { b(); }`,
		`do { a(); } while (b);`,
		`switch (x) { case 1: a(); break; default: b(); }`,
		`try { a(); } catch (e) { b(); }`,
		`try { a(); } catch { b(); }`,
		`try { a(); } finally { c(); }`,
		`throw new Error("x");`,
		`label: for (;;) { break label; }`,
		`debugger;`,

		// classes
		`class A {}`,
		`class A extends B {}`,
		`class A { m() { return 1; } }`,
		`class A { static s() {} }`,
		`class A { field = 1; }`,
		`class A { get x() { return 1; } set x(v) {} }`,

		// modules
		`import x from 'mod';`,
		`import { a, b } from 'mod';`,
		`import { a as b } from 'mod';`,
		`import * as ns from 'mod';`,
		`import def, { a } from 'mod';`,
		`export default function f() {}`,
		`export const x = 1;`,

		// templates
		"let t = `hello`;",
		"let t = `hello ${name}!`;",
		"let t = `${a} and ${b}`;",
		"let t = `nested ${`inner ${x}`}`;",
		"let t = `obj ${ {a: 1} }`;",

		// assignment operators
		`a = 1;`,
		`a += 1;`,
		`a -= 1;`,
		`a *= 2;`,
		`a ||= b;`,
		`a &&= b;`,
		`a ??= b;`,

		// nested / realistic
		`const result = items.filter(x => x.active).map(x => x.id);`,
		`const f = (a, b) => a > b ? a : b;`,
		`obj.method({ key: value, nested: { deep: true } });`,
	}

	for _, mode := range []PrintMode{PrintPretty, PrintMinified} {
		modeName := "pretty"
		if mode == PrintMinified {
			modeName = "minified"
		}
		for _, src := range cases {
			orig := ParseJS(src)
			printed := PrintJS(orig, mode)
			reparsed := ParseJS(printed)

			origSig := nodeSig(orig)
			reSig := nodeSig(reparsed)

			if origSig != reSig {
				t.Errorf("[%s] round-trip mismatch\n  src:      %s\n  printed:  %s\n  origSig:  %s\n  reSig:    %s",
					modeName, src, printed, origSig, reSig)
			}
		}
	}
}

// TestPrinterRoundTripFixtures round-trips realistic component
// <script> bodies — the kind of code developers actually write —
// exercising the printer on larger, structurally varied inputs
// rather than hand-picked one-liners.
func TestPrinterRoundTripFixtures(t *testing.T) {
	scripts := map[string]string{
		"counter": `
			let count = $state(0);
			let doubled = $derived(count * 2);
			function inc() { count++; }
			function dec() { count--; }
			$effect(() => { document.title = ` + "`Count: ${count}`" + `; });
		`,
		"form": `
			export let onSubmit;
			let username = $state('');
			let password = $state('');
			let loading = $state(false);
			let canSubmit = $derived(
				username.trim().length > 0 && password.length > 0 && !loading
			);
			async function submit() {
				loading = true;
				try {
					const res = await fetch('/login', {
						method: 'POST',
						body: JSON.stringify({ username, password }),
					});
					if (res.ok) { onSubmit(await res.json()); }
				} catch (e) {
					console.error(e);
				} finally {
					loading = false;
				}
			}
		`,
		"list": `
			let items = $state([]);
			let filter = $state('all');
			let visible = $derived(
				filter === 'all'
					? items
					: items.filter(x => x.status === filter)
			);
			function add(text) {
				items = [...items, { id: Date.now(), text, status: 'open' }];
			}
			function toggle(id) {
				items = items.map(x =>
					x.id === id ? { ...x, status: x.status === 'open' ? 'done' : 'open' } : x
				);
			}
		`,
		"destructuring": `
			export let config = {};
			const { width = 100, height = 100, label } = config;
			const [first, second, ...rest] = items;
			function handler({ target: { value } }) {
				return value;
			}
		`,
		"classes": `
			class Store {
				#value = 0;
				constructor(initial) { this.#value = initial; }
				get value() { return this.#value; }
				set value(v) { this.#value = v; }
				static create() { return new Store(0); }
			}
		`,
	}
	for name, src := range scripts {
		for _, mode := range []PrintMode{PrintPretty, PrintMinified} {
			orig := ParseJS(src)
			printed := PrintJS(orig, mode)
			reparsed := ParseJS(printed)
			if nodeSig(orig) != nodeSig(reparsed) {
				t.Errorf("fixture %s round-trip mismatch (mode=%d)\n  printed:\n%s",
					name, mode, printed)
			}
		}
	}
}

func TestPrintJSWithLocationsReportsUserOrigins(t *testing.T) {
	prog := ParseJS("let alpha = beta + 1;")
	var locations []PrintLocation
	printed := PrintJSWithLocations(prog, PrintPretty, func(loc PrintLocation) {
		locations = append(locations, loc)
	})
	if printed != "let alpha = beta + 1;" {
		t.Fatalf("printed = %q", printed)
	}
	if len(locations) == 0 {
		t.Fatal("expected at least one user-origin location")
	}
	first := locations[0]
	if first.GeneratedLine != 1 || first.GeneratedColumn != 0 || first.SourcePos != 0 {
		t.Fatalf("first location = %+v, want generated 1:0 at source 0", first)
	}
}

// TestNumberLiteralMemberAccess: "0.toFixed(1)" is a syntax error in
// JavaScript ("0." is the literal), while the parenthesised and dotted
// spellings are valid and must print as valid JavaScript (task 053).
func TestNumberLiteralMemberAccess(t *testing.T) {
	if _, diagnostics := ParseJSWithDiagnostics("x = 0.toFixed(1);"); len(diagnostics) == 0 {
		t.Error("0.toFixed(1) must be a syntax error")
	}
	for _, src := range []string{"x = (0).toFixed(1);", "x = 0..toFixed(1);", "x = 1.5.toFixed(1);", "x = 3 .toFixed(1);", "x = 1e21.toFixed(2);", "x = 0xff.toString(2);", "x = 1.e2;", "x = 1.;"} {
		program, diagnostics := ParseJSWithDiagnostics(src)
		if len(diagnostics) > 0 {
			t.Errorf("%s: %v", src, diagnostics)
			continue
		}
		for _, mode := range []PrintMode{PrintPretty, PrintMinified} {
			printed := PrintJS(program, mode)
			if _, again := ParseJSWithDiagnostics(printed); len(again) > 0 {
				t.Errorf("%s printed as %q, which does not parse: %v", src, printed, again)
			}
		}
	}
}
