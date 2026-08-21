package main

import (
	"encoding/json"
	"strings"
	"testing"
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
	ds := NewDiagSet("App.vela", "<p>🙂 @keydown.enter={save}</p>")
	pos := strings.Index(ds.src, "@keydown")
	ds.ErrorWith(pos, "VELA-EVENT-MODIFIER", "Use @keydown with an explicit event.key check.", "event modifiers are not supported")

	if got, want := ds.All()[0].Col, 7; got != want {
		t.Fatalf("UTF-16 column = %d, want %d", got, want)
	}
}

func TestTemplateFormDiagnosticUsesRepeatedAttributePosition(t *testing.T) {
	template := "<div><button @keydown.enter={first}>first</button><button @keydown.enter={second}>second</button></div>"
	nodes, err := parseTemplate(template)
	if err != nil {
		t.Fatal(err)
	}
	ds := NewDiagSet("App.vela", template)
	validateTemplateForms(nodes, template, ds)

	if got, want := len(ds.Errors()), 2; got != want {
		t.Fatalf("error count = %d, want %d: %#v", got, want, ds.All())
	}
	first := strings.Index(template, "@keydown")
	second := strings.LastIndex(template, "@keydown")
	if got, want := ds.Errors()[0].Col, first+1; got != want {
		t.Errorf("first diagnostic column = %d, want %d", got, want)
	}
	if got, want := ds.Errors()[1].Col, second+1; got != want {
		t.Errorf("second diagnostic column = %d, want %d", got, want)
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
