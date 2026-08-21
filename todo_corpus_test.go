package main

import (
	"os"
	"path/filepath"
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
			_, err := compileFile(src, filepath.Join(t.TempDir(), "out.js"), CompileOptions{Codegen: "ast"})
			if err != nil {
				t.Fatalf("positive fixture did not compile: %v", err)
			}
		})
	}

	bad := []string{
		"test_projects/deepseek-todo-vela/src/App.vela",
		"test_projects/qwen-todo-vela/src/App.vela",
		"test_projects/qwen-todo-vela/src/TodoItem.vela",
	}
	for _, src := range bad {
		t.Run("negative/"+src, func(t *testing.T) {
			_, err := compileFile(src, filepath.Join(t.TempDir(), "out.js"), CompileOptions{Codegen: "ast"})
			if err == nil {
				t.Fatal("negative fixture compiled; expected actionable diagnostics")
			}
		})
	}
}

// Positive corpus fixtures must work through the same project-mode path used
// by applications: all Vela components are mirrored as JS and local JS helper
// modules remain available at their original relative import paths.
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
			if errors := buildProject(files, srcRoot, outRoot, CompileOptions{Codegen: "ast"}, false); errors != 0 {
				t.Fatalf("project build failed with %d error(s)", errors)
			}
			for _, src := range files {
				dest := mirrorPath(src, srcRoot, outRoot, ".js")
				code, err := os.ReadFile(dest)
				if err != nil {
					t.Fatalf("missing compiled module %s: %v", dest, err)
				}
				if strings.Contains(string(code), ".vela") {
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
				if string(got) != string(want) {
					t.Fatalf("copied module %s differs from source", rel)
				}
			}
		})
	}
}
