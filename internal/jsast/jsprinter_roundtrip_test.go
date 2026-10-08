package jsast

import (
	"strings"
	"testing"
)

// TestPrinterRoundTripsCompatCorpus prints every valid program of the
// compatibility corpus in both modes and parses the result again: the second
// parse must report nothing and produce the same tree (task 055). A node the
// printer drops or prints wrong changes the tree, so a printer gap can no
// longer turn into silently wrong output.
func TestPrinterRoundTripsCompatCorpus(t *testing.T) {
	for _, src := range printerRoundTripSources() {
		first := ctParse(src)
		if first.panic != nil || first.hang || len(first.diags) > 0 {
			continue
		}
		want := ctDump(first.prog)
		for _, mode := range []PrintMode{PrintPretty, PrintMinified} {
			printed := PrintJS(first.prog, mode)
			second := ctParse(printed)
			if second.panic != nil || second.hang || len(second.diags) > 0 {
				t.Errorf("mode %d: printed program does not reparse\n  src:     %q\n  printed: %q\n  diags:   %v", mode, src, printed, second.diags)
				continue
			}
			if got := ctDump(second.prog); got != want {
				t.Errorf("mode %d: printed program changes the tree\n  src:     %q\n  printed: %q\n  want:    %s\n  got:     %s", mode, src, printed, want, got)
			}
		}
	}
}

// printerRoundTripSources is the compatibility corpus plus constructs whose
// printing depends on context (parentheses, statement position, ASI).
func printerRoundTripSources() []string {
	extra := []string{
		"new (f())()",
		"new (a.b())",
		"new (a().b)()",
		"(class {}).name",
		"(function () {})()",
		"(async function () {})()",
		"(() => ({})).x",
		"x = () => ({}).y",
		"x = () => ({ a } = b)",
		"(class {}), 1",
		"({}).toString()",
		"(a, b) => a",
		"async (a) => a",
		"for (const k in (a in b)) ;",
		"let x = (1, 2)",
		"(a ? b : c)()",
		"(-1) ** 2",
		"(await x) ** 2",
		"a ?? (b || c)",
		"(a ?? b) || c",
		"label: { break label }",
		"if (a) b; else c",
		"x = class extends (a, b) {}",
		"x = class extends f() {}",
		"x = class extends a.b {}",
		"x = tag`a${b}c`",
		"x = a.b`c`",
		"x = f()`c`",
		"x = (a?.b)`c`",
		"x = new tag`a`",
		"x = `a${`b${c}`}`",
		"x = a?.[b]?.(c)",
		"x = /re/g.test(s)",
		"x = a / b / c",
		"x = 0..toFixed(1)",
		"x = (0).toFixed(1)",
		"x = 1.5.toFixed(1)",
		"x = - -a",
		"x = + +a",
		"x = -(-1)",
		"x = a++ + +b",
		"x = typeof (() => 1)",
		"x = { 'a-b': 1, 2: 3, [k]: 4, get g() { return 1 }, set s(v) {}, async m() {}, *gen() {}, async *ag() {} }",
		"class A { static async m() {} static *g() {} async *ag() {} static get x() { return 1 } static { init() } #p = 1; static #q; 'quoted'() {} 1() {} [k] = 2 }",
		"export default class {}",
		"export default function () {}",
		"export default async function () {}",
		"export default (a, b)",
		"export default { a: 1 }",
		"export { a, b as c, d as default }",
		"export { default } from 'm'",
		"export { a as 'string name' } from 'm'",
		"export * from 'm'",
		"export * as ns from 'm'",
		"import a, * as ns from 'm'",
		"import a, { b, c as d } from 'm'",
		"import json from './d.json' with { type: 'json' }",
		"export { a } from 'm' with { type: 'json' }",
		"import 'side-effect'",
		"async function f() { for await (const x of xs) ; }",
		"x = import.meta.url",
		"function F() { return new.target }",
		"x = import('./m.js')",
		"x = await import('./m.js', { with: { type: 'json' } })",
		"const café = 1, π = 3.14",
		"x = 1_000_000 + 0xf_f + 1e1_0 + 0b1_0 + .5_5",
		"x = 10n ** 2n",
		"x = a ||= b; y = a &&= b; z = a ??= b",
		"#!/usr/bin/env node\nx = 1",
	}
	return append(ctCorpus(), extra...)
}

func TestPrinterRoundTripSourcesAreValid(t *testing.T) {
	// A typo in the extra list would silently shrink the test.
	for _, src := range printerRoundTripSources()[len(ctCorpus()):] {
		if r := ctParse(src); r.panic != nil || r.hang || len(r.diags) > 0 {
			if !strings.HasPrefix(src, "#!") {
				t.Errorf("extra round-trip source does not parse: %q: %v", src, r.diags)
			}
		}
	}
}

type printerUnknownExpr struct{ BaseExpr }

// The printer fails on a node it cannot print instead of printing nothing.
func TestPrinterRejectsUnknownNodes(t *testing.T) {
	for name, prog := range map[string]*JsProgram{
		"unknown expression": {Body: []JsStmt{&JsExprStmt{X: &printerUnknownExpr{}}}},
		"unparsed token":     {Body: []JsStmt{&JsExprStmt{X: &JsLiteral{Kind: "unknown", Raw: "@"}}}},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: printer did not fail", name)
				}
			}()
			PrintJS(prog, PrintPretty)
		}()
	}
}

func TestLexerRejectsMisplacedNumericSeparators(t *testing.T) {
	for _, src := range []string{"0x_f", "1__0", "1_", "1_.5", "1._5", "1e_1", "0b_1"} {
		if _, diags := ParseJSWithDiagnostics(src); len(diags) == 0 {
			t.Errorf("%q: no diagnostic", src)
		}
	}
	for _, src := range []string{"1_000", "0xf_f", "1e1_0", "0b1_0", ".5_5", "1_0n"} {
		if _, diags := ParseJSWithDiagnostics(src); len(diags) > 0 {
			t.Errorf("%q: %v", src, diags)
		}
	}
}
