package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The supplied AI outputs are permanent negative fixtures. Their corrected
// copies are positive fixtures; this test keeps both contracts reproducible.
func TestTodoCorpusCompilation(t *testing.T) {
	good := []string{
		"test_projects/corpus/deepseek-fixed/src/App.vela",
		"test_projects/corpus/gpt-fixed/src/App.vela",
		"test_projects/corpus/qwen-fixed/src/App.vela",
		"test_projects/corpus/qwen-fixed/src/TodoItem.vela",
	}
	for _, src := range good {
		t.Run("positive/"+src, func(t *testing.T) {
			_, err := compileFile(src, filepath.Join(t.TempDir(), "out.js"), CompileOptions{})
			if err != nil {
				t.Fatalf("positive fixture did not compile: %v", err)
			}
		})
	}

	type expectedDiagnostic struct {
		code string
		text string
	}
	bad := []struct {
		src  string
		want []expectedDiagnostic
	}{
		{
			src: "test_projects/deepseek-todo-vela/src/App.vela",
			want: []expectedDiagnostic{
				{"VELA-DERIVED-CALLBACK", "$derived does not accept a callback"},
				{"VELA-EVENT-MODIFIER", "event modifiers are not supported"},
				{"VELA-CLASS-OBJECT", "object-valued :class is not supported"},
				{"VELA-BIND-MEMBER", "bind:checked requires a plain $state"},
			},
		},
		{
			src:  "test_projects/qwen-todo-vela/src/App.vela",
			want: []expectedDiagnostic{{"VELA-DYNAMIC-CLASS", "is not a dynamic attribute"}},
		},
		{
			src: "test_projects/qwen-todo-vela/src/TodoItem.vela",
			want: []expectedDiagnostic{
				{"VELA-BUILTIN-IMPORT", "$$dispatch is a Vela builtin"},
				{"VELA-DYNAMIC-CLASS", "is not a dynamic attribute"},
			},
		},
	}
	for _, fixture := range bad {
		t.Run("negative/"+fixture.src, func(t *testing.T) {
			diags, err := compileFile(fixture.src, filepath.Join(t.TempDir(), "out.js"), CompileOptions{})
			if err == nil {
				t.Fatal("negative fixture compiled; expected actionable diagnostics")
			}
			for _, want := range fixture.want {
				found := false
				for _, diag := range diags {
					if diag.Sev == SevError && diag.Line > 0 && diag.Col > 0 && diag.Code == want.code && strings.Contains(diag.Message, want.text) {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("missing positioned %s diagnostic %q: %#v", want.code, want.text, diags)
				}
			}
		})
	}
}

// Positive corpus fixtures must work through the same project-mode path used
// by applications: all Vela components are mirrored as JS and local JS helper
// modules remain available at their original relative import paths, with
// imports of components pointing to the compiled modules.
func TestPositiveCorpusProjectsBuildAsProjects(t *testing.T) {
	projects := []string{
		"test_projects/corpus/deepseek-fixed/src",
		"test_projects/corpus/gpt-fixed/src",
		"test_projects/corpus/qwen-fixed/src",
		"test_projects/qwen-dashboard-vela/src",
	}
	for _, srcRoot := range projects {
		t.Run(filepath.Base(filepath.Dir(srcRoot)), func(t *testing.T) {
			outRoot := t.TempDir()
			files := collectVela(srcRoot)
			if len(files) == 0 {
				t.Fatal("fixture has no .vela files")
			}
			if errors := buildProject(files, srcRoot, outRoot, CompileOptions{}, false); errors != 0 {
				t.Fatalf("project build failed with %d error(s)", errors)
			}
			for _, src := range files {
				dest := mirrorPath(src, srcRoot, outRoot, ".js")
				code, err := os.ReadFile(dest)
				if err != nil {
					t.Fatalf("missing compiled module %s: %v", dest, err)
				}
				// Site tables of development builds name .vela files; only an
				// import of one is wrong.
				if regexp.MustCompile(`(from|import)\s*\(?\s*['"][^'"]*\.vela['"]`).Match(code) {
					t.Fatalf("compiled module retained a .vela import: %s", dest)
				}
				if nodeAvailable() {
					if err := nodeCheck(string(code)); err != nil {
						t.Fatalf("compiled module %s failed node --check: %v", dest, err)
					}
				}
			}
			for _, module := range collectProjectJSModules(srcRoot) {
				rel, err := filepath.Rel(srcRoot, module)
				if err != nil {
					t.Fatal(err)
				}
				copied := filepath.Join(outRoot, rel)
				got, err := os.ReadFile(copied)
				if err != nil {
					t.Fatalf("missing copied local module %s: %v", copied, err)
				}
				want, err := os.ReadFile(module)
				if err != nil {
					t.Fatal(err)
				}
				// Only compiler specifiers change (rewriteJSModuleSpecifiers).
				if string(got) != RewriteJSModuleSpecifiers(string(want), RuntimeImportFor(filepath.Dir(copied), outRoot)) {
					t.Fatalf("copied module %s differs from source", rel)
				}
			}
		})
	}
}
