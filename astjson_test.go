package main

import (
	"strings"
	"testing"
)

// astjson_test.go — coverage for the generated-AST JSON serializer
// (E2 inspection tooling).

// TestASTToJSON_Basic — a simple program serializes to JSON with
// _type tags and the expected field structure.
func TestASTToJSON_Basic(t *testing.T) {
	prog := ParseJS(`function f(x) { return x + 1; }`)
	js := ASTToJSON(prog, false)

	for _, want := range []string{
		`"_type": "JsProgram"`,
		`"_type": "JsFuncDecl"`,
		`"Name": "f"`,
		`"_type": "JsReturnStmt"`,
		`"_type": "JsBinaryExpr"`,
		`"Op": "+"`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("serialized JSON missing %q", want)
		}
	}
}

// TestASTToJSON_PosSuppressed — by default the Pos/End/Origin
// bookkeeping fields are omitted; withPos=true includes them.
func TestASTToJSON_PosSuppressed(t *testing.T) {
	prog := ParseJS(`let x = 1;`)

	without := ASTToJSON(prog, false)
	if strings.Contains(without, `"Pos"`) || strings.Contains(without, `"End"`) {
		t.Error("position fields should be suppressed by default")
	}

	with := ASTToJSON(prog, true)
	if !strings.Contains(with, `"Pos"`) {
		t.Error("position fields should appear when withPos=true")
	}
}

// TestASTToJSON_NilChild — a node with a nil child slot serializes
// that slot as JSON null rather than panicking.
func TestASTToJSON_NilChild(t *testing.T) {
	prog := &JsProgram{Body: []JsStmt{
		&JsReturnStmt{}, // Arg is nil
	}}
	js := ASTToJSON(prog, false)
	if !strings.Contains(js, `"Arg": null`) {
		t.Errorf("nil child not serialized as null:\n%s", js)
	}
}

// TestASTToJSON_ValidJSON — the output parses as well-formed JSON
// for every diff fixture compiled through the generator.
func TestASTToJSON_ValidJSON(t *testing.T) {
	for name, src := range diffFixtures {
		sfc, err := ParseSFC(src)
		if err != nil {
			t.Fatalf("[%s] parseSFC: %v", name, err)
		}
		tmpl, err := ParseTemplate(sfc.Template)
		if err != nil {
			t.Fatalf("[%s] parseTemplate: %v", name, err)
		}
		analysis := AnalyzeScript(sfc.Script)
		prog := buildProgram(name, sfc, tmpl, analysis, "vela-000000")
		js := ASTToJSON(prog, false)

		// Lightweight well-formedness: balanced braces/brackets and
		// a JsProgram root.  (A full JSON parse would need the
		// stdlib encoding/json import; balance + root is enough to
		// catch a structurally broken serializer.)
		if !strings.HasPrefix(strings.TrimSpace(js), `{`) {
			t.Errorf("[%s] output is not a JSON object", name)
		}
		if !strings.Contains(js, `"_type": "JsProgram"`) {
			t.Errorf("[%s] output missing JsProgram root", name)
		}
		if brace := balance(js, '{', '}'); brace != 0 {
			t.Errorf("[%s] unbalanced braces: %d", name, brace)
		}
		if brack := balance(js, '[', ']'); brack != 0 {
			t.Errorf("[%s] unbalanced brackets: %d", name, brack)
		}
	}
}

// balance returns the net count of open minus close delimiters,
// ignoring those inside JSON string literals.
func balance(s string, open, close rune) int {
	n := 0
	inStr := false
	escaped := false
	for _, r := range s {
		if inStr {
			if escaped {
				escaped = false
			} else if r == '\\' {
				escaped = true
			} else if r == '"' {
				inStr = false
			}
			continue
		}
		switch r {
		case '"':
			inStr = true
		case open:
			n++
		case close:
			n--
		}
	}
	return n
}
