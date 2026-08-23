package main

import (
	"strings"
	"testing"
)

func TestGeneratedStringLiteralDefersEscapingToPrinter(t *testing.T) {
	value := "quote ' and \" plus slash \\ and newline\nline separator \u2028"
	literal := strLit(value)
	if !literal.Decoded || literal.Value != value || literal.Raw != "" {
		t.Fatalf("generated literal must retain only decoded value: %+v", literal)
	}

	prog := &JsProgram{Body: []JsStmt{genConst("value", literal)}}
	printed := PrintJS(prog, PrintPretty)
	want := "const value = " + encodeGeneratedString(value) + ";"
	if printed != want {
		t.Fatalf("printed literal = %q, want %q", printed, want)
	}
	if reparsed := ParseJS(printed); reparsed == nil || len(reparsed.Body) != 1 {
		t.Fatalf("printer output did not parse as one statement: %q", printed)
	}
}

func TestTypedHTMLFillUsesDecodedDataLiteral(t *testing.T) {
	g := &ASTGen{}
	html := `<p title="a&b">quote ' and "</p>`
	printed := PrintJS(&JsProgram{Body: g.fillHTMLStmts("$$target", html)}, PrintPretty)
	if !strings.Contains(printed, "innerHTML = "+encodeGeneratedString(html)) {
		t.Fatalf("HTML payload was not emitted through decoded literal builder:\n%s", printed)
	}
	if strings.Contains(printed, "&amp;") {
		t.Fatalf("HTML payload was escaped as an attribute value instead of JS data:\n%s", printed)
	}
}

func TestTypedStyleInjectionUsesDecodedCSSLiteral(t *testing.T) {
	g := &ASTGen{sfc: &SFC{Style: `.card::before { content: "'"; }`}, scopeID: "vela-test"}
	printed := PrintJS(&JsProgram{Body: g.buildStyleInjection()}, PrintPretty)
	css := ScopeCSS(g.sfc.Style, g.scopeID)
	if !strings.Contains(printed, "textContent = "+encodeGeneratedString(css)) {
		t.Fatalf("CSS payload was not emitted through decoded literal builder:\n%s", printed)
	}
	if strings.Contains(printed, "jsStmt") {
		t.Fatalf("printer output unexpectedly contains helper text:\n%s", printed)
	}
}
