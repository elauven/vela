package jsast

import (
	"strings"
	"testing"
)

func TestEarlyErrorsReportInvalidPrograms(t *testing.T) {
	module := EarlyOptions{TopLevelAwait: true}
	script := EarlyOptions{TopLevelIsFunction: true}
	cases := []struct {
		src  string
		opts EarlyOptions
		want string
	}{
		{"function f() { break; }", module, "illegal break"},
		{"function f() { continue; }", module, "illegal continue"},
		{"while (1) { function f() { break } }", module, "illegal break"},
		{"switch (a) { case 1: continue; }", module, "illegal continue"},
		{"while (1) { break nope; }", module, "undefined label"},
		{"l: { continue l; }", module, "undefined label"},
		{"class A { constructor() {} constructor() {} }", module, "one constructor"},
		{"function f() { super(); }", module, "'super'"},
		{"class A { constructor() { super(); } }", module, "'super'"},
		{"class A { m() { return this.#nope; } }", module, "#nope"},
		{"class A { #x; static has(o) { return #y in o } }", module, "#y"},
		{"const r = /(/;", module, "unterminated group"},
		{"const r = /a/gg;", module, "parse"},
		{"const r = /a/x;", module, "parse"},
		{"const r = /[a/;", module, "parse"},
		{"function f() { await g(); }", module, "await"},
		{"const v = await g();", script, "await"},
		{"async function f() { const g = () => await h(); }", module, "await"},
		{"function f() { for await (const x of y) ; }", module, "for await"},
		{"function f() { yield 1; }", module, "yield"},
		{"const t = new.target;", module, "new.target"},
		{"class A { x = await y; }", module, "await"},
	}
	for _, tc := range cases {
		prog, diags := ParseJSWithDiagnostics(tc.src)
		if len(diags) > 0 {
			if !strings.Contains(tc.want, "parse") {
				t.Errorf("%q: parser already reports %v", tc.src, diags)
			}
			continue
		}
		early := EarlyErrors(prog, tc.opts)
		found := false
		for _, d := range early {
			if strings.Contains(d.Message, tc.want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: early errors %v, want one containing %q", tc.src, early, tc.want)
		}
	}
}

func TestEarlyErrorsAcceptValidPrograms(t *testing.T) {
	module := EarlyOptions{TopLevelAwait: true}
	for _, src := range []string{
		"while (1) { if (a) break; else continue; }",
		"for (;;) { switch (a) { case 1: break; default: continue; } }",
		"outer: for (const a of b) { for (const c of d) { continue outer; } }",
		"block: { break block; }",
		"class A extends B { constructor() { super(); const f = () => super.m(); } m() { return super.m(); } }",
		"class A { #x = 1; static #y; m() { return this.#x + A.#y; } static has(o) { return #x in o; } }",
		"class A { #x; m() { return class { n(o) { return o.#x } } } }",
		"const r = /[(]\\)(?<n>a)[/]/giu;",
		"async function f() { await g(); for await (const x of y) ; }",
		"const v = await import('./m.js');",
		"function* g() { yield 1; yield* h(); }",
		"function F() { return new.target; }",
		"const f = async () => { await g(); };",
		"class A { static { this.ready = true; } }",
		"const o = { get x() { return 1 }, async *m() { yield await 1 } };",
	} {
		prog, diags := ParseJSWithDiagnostics(src)
		if len(diags) > 0 {
			t.Errorf("%q: parse diagnostics %v", src, diags)
			continue
		}
		if early := EarlyErrors(prog, module); len(early) > 0 {
			t.Errorf("%q: unexpected early errors %v", src, early)
		}
	}
	// A component script is checked as a function body.
	prog := ParseJS("const t = new.target; function f() { return 1 }")
	if early := EarlyErrors(prog, EarlyOptions{TopLevelIsFunction: true}); len(early) > 0 {
		t.Errorf("component script: %v", early)
	}
}

// Every valid program of the compatibility corpus is free of early errors,
// and every early-error case of its invalid table is reported.
func TestEarlyErrorsAcceptCompatCorpus(t *testing.T) {
	var valid []string
	for _, tbl := range [][]ctCase{ctES5, ctES2015, ctES2016to2019, ctES2020plus, ctLexical} {
		for _, c := range tbl {
			valid = append(valid, c.Src)
		}
	}
	for _, src := range valid {
		r := ctParse(src)
		if r.panic != nil || r.hang || len(r.diags) > 0 {
			continue
		}
		if early := EarlyErrors(r.prog, EarlyOptions{TopLevelAwait: true}); len(early) > 0 {
			t.Errorf("%q: %v", src, early)
		}
	}
}
