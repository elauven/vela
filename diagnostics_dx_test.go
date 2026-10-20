package main

import (
	"strings"
	"testing"
)

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
