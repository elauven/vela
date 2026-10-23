package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"unicode/utf16"
)

// rangeText is the source a machine diagnostic covers: its one-based UTF-16
// columns, the end exclusive.
func rangeText(source string, d MachineDiagnostic) string {
	lines := strings.Split(source, "\n")
	offset := func(line, column int) int {
		at := 0
		for _, l := range lines[:line-1] {
			at += len(l) + 1
		}
		units := utf16.Encode([]rune(lines[line-1]))
		return at + len(string(utf16.Decode(units[:column-1])))
	}
	return source[offset(d.Line, d.Column):offset(d.EndLine, d.EndColumn)]
}

// TestDiagnosticRanges: a diagnostic covers the node it is about when its
// extent is known (a statement, a call, an import, an attribute, a template
// expression) and the token at its position otherwise, for the editor
// (task 065) and SARIF.
func TestDiagnosticRanges(t *testing.T) {
	source := `<script>
  import { $$dispatch } from 'vela';
  let count = $state(0);
  let twice = $derived(count * 2, 1);
  function inc() {
    return;
    if (count) {
      count++;
    }
  }
  undeclaredName = 1;
</script>

<template>
  <button @click.prevent={inc} title="a {b}">{count} 🙂 {missing}</button>
</template>
`
	svc := &CompilerService{Cwd: mustGetwd(t)}
	res := svc.Compile(ServiceRequest{Path: "App.vela", Source: &source})
	want := map[string]string{
		"VELA-BUILTIN-IMPORT":        "import { $$dispatch } from 'vela';",
		"VELA-DERIVED-ARITY":         "$derived(count * 2, 1)",
		"VELA-FLOW-UNREACHABLE":      "if (count) {\n      count++;\n    }",
		"VELA-SCOPE-IMPLICIT-GLOBAL": "undeclaredName",
		"VELA-EVENT-MODIFIER":        "@click.prevent={inc}",
		"VELA-ATTR-BRACES":           `title="a {b}"`,
		// After a character outside the BMP, columns count UTF-16 units.
		"": "missing",
	}
	for _, d := range res.Diagnostics {
		text, ok := want[d.Code]
		if !ok {
			continue
		}
		delete(want, d.Code)
		if got := rangeText(source, d); got != text {
			t.Errorf("%s %q covers %q, want %q", d.Code, d.Message, got, text)
		}
	}
	for code := range want {
		t.Errorf("no %s diagnostic in %+v", code, res.Diagnostics)
	}

	// Parse errors cover the token they stop at.
	for _, c := range []struct{ source, code, text string }{
		{"<script>\n  let a = $state(;\n</script>\n<template><p>{a}</p></template>\n", "VELA-JS-SYNTAX", ";"},
		{"<template><p>x</p></template>\n<style>\n  p { color: red;\n</style>\n", "VELA-CSS-SYNTAX", ""},
	} {
		res := svc.Compile(ServiceRequest{Path: "App.vela", Source: &c.source})
		if len(res.Diagnostics) != 1 || res.Diagnostics[0].Code != c.code {
			t.Fatalf("%q: diagnostics %+v", c.source, res.Diagnostics)
		}
		if got := rangeText(c.source, res.Diagnostics[0]); got != c.text {
			t.Errorf("%s covers %q, want %q", c.code, got, c.text)
		}
	}
}

// TestSSRDiagnosticRanges: diagnostics of the SSR build cover a token too,
// in the JSON and the SARIF of vela check.
func TestSSRDiagnosticRanges(t *testing.T) {
	source := "<script>\n  let now = $state(Date.now());\n</script>\n<template><p>{now}</p></template>\n"
	dir := checkProject(t, map[string]string{"Clock.vela": source})
	_, stdout, _ := runCheck(t, "--project", dir, "--ssr", "--format", "json")
	var doc struct{ Diagnostics []MachineDiagnostic }
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range doc.Diagnostics {
		if strings.HasPrefix(d.Code, "VELA-SSR-") {
			found = true
			if got := rangeText(source, d); got != "Date" {
				t.Errorf("%s covers %q, want Date", d.Code, got)
			}
		}
	}
	if !found {
		t.Fatalf("no SSR diagnostic: %s", stdout)
	}

	_, stdout, _ = runCheck(t, "--project", dir, "--ssr", "--format", "sarif")
	if !regexp.MustCompile(`"endColumn":\s*24`).MatchString(stdout) || !regexp.MustCompile(`"endLine":\s*2\b`).MatchString(stdout) {
		t.Errorf("the SARIF region has no end:\n%s", stdout)
	}
}
