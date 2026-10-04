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
	want := "const value = " + EncodeGeneratedString(value) + ";"
	if printed != want {
		t.Fatalf("printed literal = %q, want %q", printed, want)
	}
	if reparsed := ParseJS(printed); reparsed == nil || len(reparsed.Body) != 1 {
		t.Fatalf("printer output did not parse as one statement: %q", printed)
	}
}

func TestGeneratedPrimitiveLiteralBuildersUseTypedValues(t *testing.T) {
	for _, tc := range []struct {
		name, raw, kind string
		literal         *JsLiteral
	}{
		{"true", "true", "boolean", boolLit(true)},
		{"false", "false", "boolean", boolLit(false)},
		{"negative number", "-7", "number", numLit(-7)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.literal.Raw != tc.raw || tc.literal.Kind != tc.kind || tc.literal.Decoded {
				t.Fatalf("typed literal = %+v, want raw=%q kind=%q and undecoded", tc.literal, tc.raw, tc.kind)
			}
			if tc.literal.Origin != OriginGenerated {
				t.Fatalf("typed literal origin = %v, want OriginGenerated", tc.literal.Origin)
			}
		})
	}
}

func TestTypedHTMLFillUsesDecodedDataLiteral(t *testing.T) {
	g := &ASTGen{}
	html := `<p title="a&b">quote ' and "</p>`
	printed := PrintJS(&JsProgram{Body: g.fillHTMLStmts("$$target", html)}, PrintPretty)
	if !strings.Contains(printed, "innerHTML = "+EncodeGeneratedString(html)) {
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
	if !strings.Contains(printed, "textContent = "+EncodeGeneratedString(css)) {
		t.Fatalf("CSS payload was not emitted through decoded literal builder:\n%s", printed)
	}
	if strings.Contains(printed, "jsStmt") {
		t.Fatalf("printer output unexpectedly contains helper text:\n%s", printed)
	}
}
