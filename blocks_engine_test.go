package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// withDOMEngine runs fn with the given DOM engine selected. No test in this
// package runs in parallel, so switching the process-wide engine is safe.
func withDOMEngine(t *testing.T, engine DOMEngine, fn func()) {
	t.Helper()
	previous := activeDOMEngine
	activeDOMEngine = engine
	defer func() { activeDOMEngine = previous }()
	fn()
}

func requireJsdom(t *testing.T) (string, string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available")
	}
	jsdom, err := filepath.Abs(filepath.Join("node_modules", "jsdom", "lib", "api.js"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(jsdom); err != nil {
		t.Skip("jsdom not installed; run npm ci")
	}
	return node, jsdom
}

func buildWithEngine(t *testing.T, engine DOMEngine, srcRoot string) string {
	t.Helper()
	outRoot := t.TempDir()
	withDOMEngine(t, engine, func() {
		if errors := buildProject(collectVela(srcRoot), srcRoot, outRoot, CompileOptions{}, false); errors != 0 {
			t.Fatalf("project build failed with %d error(s)", errors)
		}
	})
	if err := os.WriteFile(filepath.Join(outRoot, "package.json"), []byte(`{"type":"module"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return outRoot
}

// TestBlocksEngineMatchesLegacyEngine compiles the components of
// testdata/engine_diff with both engines and compares the DOM after every
// step of testdata/engine_diff/cases.json in jsdom (task 039). The cases do
// not empty a list with {:else}: the legacy engine never renders it (see
// TestBlocksEngineRendersEachElse).
func TestBlocksEngineMatchesLegacyEngine(t *testing.T) {
	node, jsdom := requireJsdom(t)
	srcRoot := filepath.Join("testdata", "engine_diff")
	legacy := buildWithEngine(t, DOMEngineLegacy, srcRoot)
	blocks := buildWithEngine(t, DOMEngineBlocks, srcRoot)
	if _, err := os.Stat(filepath.Join(blocks, "runtime-blocks.js")); err != nil {
		t.Fatalf("block engine build did not stage runtime-blocks.js: %v", err)
	}
	out, err := exec.Command(node, "tools/engine-diff.mjs", jsdom, legacy, blocks, filepath.Join(srcRoot, "cases.json")).CombinedOutput()
	if err != nil {
		t.Fatalf("engine diff: %v\n%s", err, out)
	}
	var result struct {
		OK       bool     `json:"ok"`
		Total    int      `json:"total"`
		Failures []string `json:"failures"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("invalid engine diff output: %v\n%s", err, out)
	}
	if result.Total == 0 {
		t.Fatal("engine diff selected no cases")
	}
	if !result.OK {
		t.Fatalf("engines differ:\n%s", strings.Join(result.Failures, "\n"))
	}
}

// TestBlocksEngineRendersEachElse covers {#each}…{:else}: the legacy CSR
// engine never renders the fallback, the block engine does.
func TestBlocksEngineRendersEachElse(t *testing.T) {
	for _, header := range []string{"{#each items as item (item.id)}", "{#each items as item}"} {
		src := `<script>let items = $state([]);</script>
<template><div>
  <button class="add" @click={() => items = [{ id: 1 }]}>add</button>
  <button class="clear" @click={() => items = []}>clear</button>
  <ul>` + header + `<li class="row">{item.id}</li>{:else}<li class="empty">nothing</li>{/each}</ul>
</div></template>`
		withDOMEngine(t, DOMEngineBlocks, func() {
			assertRuntime(t, "EachElse", src, []rtStep{
				{"assertCount": "li.empty", "equals": 1},
				{"click": "button.add"},
				{"assertCount": "li.empty", "equals": 0},
				{"assertCount": "li.row", "equals": 1},
				{"click": "button.clear"},
				{"assertCount": "li.empty", "equals": 1},
				{"assertCount": "li.row", "equals": 0},
			})
		})
	}
}
