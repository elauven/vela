package main

import (
	"path/filepath"
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
