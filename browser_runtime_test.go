package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type browserRuntimeConfig struct {
	RootDir    string            `json:"rootDir"`
	Entry      string            `json:"entry"`
	ExportName string            `json:"exportName"`
	Steps      []rtStep          `json:"steps"`
	Storage    map[string]string `json:"storage,omitempty"`
}

type browserRuntimeResult struct {
	OK       bool     `json:"ok"`
	Failures []string `json:"failures"`
}

func runBrowserProjectWithStorage(t *testing.T, srcRoot, entry string, storage map[string]string, steps []rtStep) {
	t.Helper()
	node := requirePlaywright(t)
	outRoot := t.TempDir()
	files := collectVela(srcRoot)
	if len(files) == 0 {
		t.Fatalf("project %s has no .vela files", srcRoot)
	}
	if errors := buildProject(files, srcRoot, outRoot, CompileOptions{Codegen: "ast"}, false); errors != 0 {
		t.Fatalf("project build failed with %d error(s)", errors)
	}
	config := browserRuntimeConfig{RootDir: outRoot, Entry: entry, ExportName: componentName(entry), Steps: steps, Storage: storage}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(outRoot, "browser-runtime.json")
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(node, "tools/browser-runtime-harness.mjs", configPath).CombinedOutput()
	if err != nil {
		t.Fatalf("browser runtime harness: %v\n%s", err, output)
	}
	var result browserRuntimeResult
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("invalid browser runtime output: %v\n%s", err, output)
	}
	if !result.OK {
		t.Fatalf("browser runtime failures: %v", result.Failures)
	}
}

func runBrowserProject(t *testing.T, srcRoot, entry string, steps []rtStep) {
	t.Helper()
	runBrowserProjectWithStorage(t, srcRoot, entry, nil, steps)
}

func TestBrowserRuntimeTodoCorpusQwen(t *testing.T) {
	runBrowserProject(t, "test_projects/corpus/qwen-fixed/src", "App.js", []rtStep{
		{"assertCount": ".todo-item", "equals": 0},
		{"input": "input.new-title-input", "value": "write browser tests"},
		{"submit": "form.add-form"},
		{"assertCount": ".todo-item", "equals": 1},
		{"change": "input.toggle-checkbox", "checked": true},
		{"assertCount": ".todo-item.done", "equals": 1},
		{"click": "button.filter-button:nth-child(2)"},
		{"assertCount": ".todo-item", "equals": 0},
		{"click": "button.filter-button:nth-child(3)"},
		{"assertCount": ".todo-item.done", "equals": 1},
		{"dblclick": ".todo-title"},
		{"input": "input.edit-input", "value": "edited in chromium"},
		{"keydown": "input.edit-input", "key": "Enter"},
		{"assertText": ".todo-title", "equals": "edited in chromium"},
		{"click": "button.clear-button"},
		{"assertCount": ".todo-item", "equals": 0},
	})
}

func TestBrowserRuntimeTodoCorpusDeepSeek(t *testing.T) {
	runBrowserProjectWithStorage(t, "test_projects/corpus/deepseek-fixed/src", "App.js", map[string]string{
		"vela-todo-tasks": `[{"id":41,"text":"Stored task","done":false}]`,
	}, []rtStep{
		{"assertCount": ".task-item", "equals": 1},
		{"assertText": ".task-item span", "equals": "Stored task"},
		{"input": ".add-form input", "value": "browser deep state"},
		{"keydown": ".add-form input", "key": "Enter"},
		{"assertCount": ".task-item", "equals": 2},
		{"assertStorage": "vela-todo-tasks", "contains": "browser deep state"},
		{"change": ".task-item input[type=checkbox]", "checked": true},
		{"click": ".filters button:nth-child(3)"},
		{"assertCount": ".task-item", "equals": 1},
	})
}

func TestBrowserRuntimeTodoCorpusGPT(t *testing.T) {
	runBrowserProject(t, "test_projects/corpus/gpt-fixed/src", "App.js", []rtStep{
		{"assertCount": ".todo-item", "equals": 2},
		{"click": "button.remove"},
		{"assertCount": ".todo-item", "equals": 1},
		{"change": "input[type=checkbox]", "checked": true},
		{"assertCount": ".todo-item.completed", "equals": 1},
		{"click": "button.clear"},
		{"assertCount": ".todo-item", "equals": 0},
	})
}
