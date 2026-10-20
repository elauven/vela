package main

import (
	"testing"
)

func TestGeneratedStringLiteralDefersEscapingToPrinter(t *testing.T) {
	value := "quote ' and \" plus slash \\ and newline\nline separator \u2028"
	literal := GenStrLit(value)
	if !literal.Decoded || literal.Value != value || literal.Raw != "" {
		t.Fatalf("generated literal must retain only decoded value: %+v", literal)
	}

	prog := &JsProgram{Body: []JsStmt{GenConst("value", literal)}}
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
		{"true", "true", "boolean", GenBoolLit(true)},
		{"false", "false", "boolean", GenBoolLit(false)},
		{"negative number", "-7", "number", GenNumLit(-7)},
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
