package diag

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestDiagnosticStringRendersCodeAndSuggestion(t *testing.T) {
	ds := NewDiagSet("App.vela", "<template><button @keydown.enter={save} /></template>")
	ds.ErrorWith(18, "VELA-EVENT-MODIFIER", "Use @keydown with an explicit event.key check.", "event modifiers are not supported")
	diag := ds.All()[0]
	if diag.Code != "VELA-EVENT-MODIFIER" || diag.Suggestion == "" {
		t.Fatalf("structured diagnostic = %#v", diag)
	}
	out := diag.String()
	for _, want := range []string{"[VELA-EVENT-MODIFIER]", "help: Use @keydown"} {
		if !strings.Contains(out, want) {
			t.Fatalf("diagnostic output missing %q:\n%s", want, out)
		}
	}
}

func TestDiagnosticUsesUTF16Column(t *testing.T) {
	src := "<p>🙂 @keydown.enter={save}</p>"
	ds := NewDiagSet("App.vela", src)
	pos := strings.Index(src, "@keydown")
	ds.ErrorWith(pos, "VELA-EVENT-MODIFIER", "Use @keydown with an explicit event.key check.", "event modifiers are not supported")

	if got, want := ds.All()[0].Col, 7; got != want {
		t.Fatalf("UTF-16 column = %d, want %d", got, want)
	}
}

func TestDiagnosticsJSONIsCompactAndMachineReadable(t *testing.T) {
	ds := NewDiagSet("App.vela", "<template />")
	ds.ErrorWith(0, "VELA-EVENT-MODIFIER", "Use @keydown with an explicit event.key check.", "event modifiers are not supported")
	data, err := DiagnosticsJSON(ds.All())
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0]["severity"] != "error" || got[0]["code"] != "VELA-EVENT-MODIFIER" ||
		got[0]["column"] != float64(1) || got[0]["suggestion"] == "" {
		t.Fatalf("machine diagnostics = %s", data)
	}
}

func TestDiagnosticRangeCoversTheToken(t *testing.T) {
	src := "<p>🙂 {#each items as item}<Card.Body x='a b' :y={f({z: 1})}/>\n"
	for _, c := range []struct{ at, want string }{
		{"items", "items"},
		{"{#each", "{#each items as item}"},
		{"<Card", "<Card.Body"},
		{"'a b'", "'a b'"},
		{"{f(", "{f({z: 1})}"},
		{"/>", "/"},
		{"\n", ""},
	} {
		ds := NewDiagSet("App.vela", src)
		ds.Error(strings.Index(src, c.at), "x")
		d := ds.All()[0]
		line, col := d.End()
		units := utf16.Encode([]rune(d.Source))
		if line != d.Line || col < d.Col || col-1 > len(units) {
			t.Fatalf("%q: end %d:%d of %d:%d", c.at, line, col, d.Line, d.Col)
		}
		if got := string(utf16.Decode(units[d.Col-1 : col-1])); got != c.want {
			t.Errorf("%q covers %q, want %q", c.at, got, c.want)
		}
	}
}

func TestReportCoversTheRange(t *testing.T) {
	src := "<script>\nlet a = f(\n  1);\n</script>"
	ds := NewDiagSet("App.vela", src)
	ds.PushOffset(len("<script>"))
	ds.Report(SevWarning, len("\nlet a = "), len("\nlet a = f(\n  1)"), "VELA-X", "", "call %d", 1)
	ds.PopOffset()
	m := ds.All()[0].Machine()
	if m.Severity != "warning" || m.Message != "call 1" || m.Line != 2 || m.Column != 9 || m.EndLine != 3 || m.EndColumn != 5 {
		t.Errorf("range %+v", m)
	}
	// Without a position the range is empty.
	d := &Diagnostic{File: "App.vela", Line: 1, Col: 1}
	if line, col := d.End(); line != 1 || col != 1 {
		t.Errorf("end %d:%d", line, col)
	}
}
