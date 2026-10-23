package compiler

import (
	"github.com/elauven/vela/internal/project"

	"os"
	"path/filepath"
	"strings"
	"testing"
)

const analyzedComponent = `<script>
  import { Card } from './Card.vela';
  export let title = 'Untitled';
  export let value = $bindable('');
  let count = $state(0);
  let doubled = $derived(count * 2);
  function inc() { count++; }
</script>

<template>
  <Card :title={title} @click={inc}>{doubled}</Card>
</template>
`

func TestAnalyzeComponent(t *testing.T) {
	a := AnalyzeComponent("src/App.vela", analyzedComponent, CompileOptions{})
	if a.Err != nil || !a.ScriptComplete || !a.TemplateComplete || len(a.Template) == 0 {
		t.Fatalf("analysis %+v", a)
	}
	if len(a.Analysis.Props) != 2 || !a.Analysis.Props[1].Bindable || len(a.Analysis.StateVars) != 1 || len(a.Analysis.DerivedVars) != 1 {
		t.Errorf("props %+v, state %+v, derived %+v", a.Analysis.Props, a.Analysis.StateVars, a.Analysis.DerivedVars)
	}
	count := a.Scope.Bindings["count"]
	if count == nil || len(count.Writes) != 1 || a.SFC.Script[count.Pos:count.End] != "count" {
		t.Errorf("binding count %+v", count)
	}
	if len(a.Imports) != 1 || a.Imports[0].Resolved != filepath.Join("src", "Card.vela") {
		t.Errorf("imports %+v", a.Imports)
	}
}

// TestAnalyzeBrokenComponent: a broken section leaves the analysis of the
// other one, and the script parser's recovery keeps what precedes the error.
func TestAnalyzeBrokenComponent(t *testing.T) {
	for _, c := range []struct {
		name, source            string
		script, template, scope bool
		binding                 string
	}{
		{"script syntax", strings.Replace(analyzedComponent, "function inc() { count++; }", "function inc() { count++;\n  let x = ;", 1), false, true, true, "doubled"},
		{"template syntax", strings.Replace(analyzedComponent, "{doubled}", "{doubled + }", 1), true, false, true, "inc"},
		{"no template", analyzedComponent[:strings.Index(analyzedComponent, "<template>")], true, false, true, "inc"},
		// An unclosed section ends at the next one or at the end of the file.
		{"unclosed script", "<script>\n  let count = $state(0);\n  let n", false, false, true, "count"},
		{"unclosed script before the template", "<script>\n  let count = $state(0);\n<template><p>{count}</p></template>\n", false, true, true, "count"},
		{"unclosed template", "<script>\n  let count = $state(0);\n</script>\n<template><p>{count}</p>\n", true, false, true, "count"},
	} {
		a := AnalyzeComponent("App.vela", c.source, CompileOptions{})
		if a.Err != nil || a.ScriptComplete != c.script || a.TemplateComplete != c.template || (a.Scope != nil) != c.scope {
			t.Errorf("%s: script %v, template %v, scope %v, err %v", c.name, a.ScriptComplete, a.TemplateComplete, a.Scope != nil, a.Err)
			continue
		}
		if c.template && a.Template == nil {
			t.Errorf("%s: no template nodes", c.name)
		}
		if c.binding != "" && a.Scope.Bindings[c.binding] == nil {
			t.Errorf("%s: no binding %s", c.name, c.binding)
		}
	}
}

// TestAnalyzeEveryPrefix analyzes every prefix of the example components,
// as an editor sees a file being typed: the analysis never fails.
func TestAnalyzeEveryPrefix(t *testing.T) {
	var files []string
	for _, root := range []string{"../../examples", "../../test_projects/qpage-builder/src"} {
		files = append(files, project.CollectVela(root)...)
	}
	if len(files) == 0 {
		t.Fatal("no components")
	}
	step := 7
	if testing.Short() {
		step = 61
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		source := string(data)
		for n := 0; n <= len(source); n += step {
			if a := AnalyzeComponent(file, source[:n], CompileOptions{}); a.Err != nil {
				t.Fatalf("%s, %d of %d bytes: %v", file, n, len(source), a.Err)
			}
		}
	}
}

// TestEditsCompileWithoutInternalErrors compiles the example components
// with an unfinished tag, block or directive typed at the start of a line,
// as the language server compiles a buffer being edited: every problem is
// a diagnostic of the source, never an internal compiler error.
func TestEditsCompileWithoutInternalErrors(t *testing.T) {
	files, err := filepath.Glob("../../examples/*.vela")
	if err != nil || len(files) == 0 {
		t.Fatalf("no components: %v", err)
	}
	edits := []string{"<Card", "<div", "<p class=", "<Card :a={1}", "<", "{", "{#if x}", "{:else}", "{/if}", ":title={", "@click={", "bind:"}
	if testing.Short() {
		files = files[:1]
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			t.Parallel()
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			source := string(data)
			// Every third line keeps the test short; the edits are the
			// same at each line of a section.
			for n, line := 0, 0; n < len(source); n++ {
				if source[n] != '\n' {
					continue
				}
				if line++; line%3 != 0 {
					continue
				}
				for _, edit := range edits {
					edited := source[:n+1] + edit + source[n+1:]
					diags, _ := compileComponent(file, "App.js", CompileOptions{}, func() ([]byte, error) { return []byte(edited), nil }, func(*compiledModule) error { return nil })
					for _, d := range diags {
						if strings.Contains(d.Message, "internal compiler error") || strings.Contains(d.Message, EmitCheckDiagnosticCode) {
							t.Fatalf("%q at line %d: %s", edit, line+1, d.Message)
						}
					}
				}
			}
		})
	}
}
