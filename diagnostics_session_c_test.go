package main

import (
	"strings"
	"testing"
)

// runValidateTemplate parses a template-only fragment and runs
// validateTemplate against it.  Returns the diagnostics so tests
// can assert on severity and message.
func runValidateTemplate(t *testing.T, script, template string) *DiagSet {
	t.Helper()
	a := analyzeScript(script)
	nodes, err := parseTemplate(template)
	if err != nil {
		t.Fatalf("template parse failed: %v", err)
	}
	ds := NewDiagSet("test.vela", script+"\n"+template)
	validateTemplate(nodes, a, ds)
	return ds
}

// Session C — an unkeyed {#each} whose row body renders a child
// component triggers an informational hint suggesting the user add
// a key so row identity (and child-component state) is preserved
// across middle insertions and reorderings.
func TestSessionC_UnkeyedEachWithComponentEmitsHint(t *testing.T) {
	script := `import Row from './Row.vela'; let items = [];`
	template := `<ul>{#each items as it}<Row name={it.name} />{/each}</ul>`
	ds := runValidateTemplate(t, script, template)
	hints := 0
	for _, d := range ds.All() {
		if d.Sev == SevHint && strings.Contains(d.Message, "unkeyed {#each") {
			hints++
		}
	}
	if hints != 1 {
		t.Fatalf("expected exactly one Session C hint, got %d\n%v",
			hints, ds.All())
	}
}

// A keyed {#each} over the same component should NOT emit the
// Session C hint — the user already keyed the loop.
func TestSessionC_KeyedEachIsSilent(t *testing.T) {
	script := `import Row from './Row.vela'; let items = [];`
	template := `<ul>{#each items as it (it.id)}<Row name={it.name} />{/each}</ul>`
	ds := runValidateTemplate(t, script, template)
	for _, d := range ds.All() {
		if d.Sev == SevHint && strings.Contains(d.Message, "unkeyed {#each") {
			t.Fatalf("keyed {#each} should not get the Session C hint:\n  %s", d.Message)
		}
	}
}

// An unkeyed {#each} over plain DOM (no components) should also be
// silent — the row-identity concern is specifically about
// child-component state, which a pure-DOM row doesn't have.
func TestSessionC_PureDOMEachIsSilent(t *testing.T) {
	script := `let items = [];`
	template := `<ul>{#each items as it}<li>{it}</li>{/each}</ul>`
	ds := runValidateTemplate(t, script, template)
	for _, d := range ds.All() {
		if d.Sev == SevHint && strings.Contains(d.Message, "unkeyed {#each") {
			t.Fatalf("pure-DOM {#each} should not get the Session C hint:\n  %s", d.Message)
		}
	}
}

// Nested component inside an {#if} inside the row still counts —
// the row owns child-component state via the nested chain.
func TestSessionC_ComponentInIfBranchInRowFiresHint(t *testing.T) {
	script := `import Row from './Row.vela'; let items = [];`
	template := `<ul>{#each items as it}<li>{#if it.show}<Row name={it.name} />{/if}</li>{/each}</ul>`
	ds := runValidateTemplate(t, script, template)
	hits := 0
	for _, d := range ds.All() {
		if d.Sev == SevHint && strings.Contains(d.Message, "unkeyed {#each") {
			hits++
		}
	}
	if hits != 1 {
		t.Fatalf("expected hint for unkeyed each containing nested {#if}-with-component:\n%v", ds.All())
	}
}
