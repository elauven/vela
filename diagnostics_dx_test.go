package main

import (
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
