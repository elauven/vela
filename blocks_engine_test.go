package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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

// buildForJsdom builds a project into a temporary ES module directory.
func buildForJsdom(t *testing.T, srcRoot string) string {
	t.Helper()
	outRoot := t.TempDir()
	if errors := BuildProject(CollectVela(srcRoot), srcRoot, outRoot, CompileOptions{}, false); errors != 0 {
		t.Fatalf("project build failed with %d error(s)", errors)
	}
	if err := os.WriteFile(filepath.Join(outRoot, "package.json"), []byte(`{"type":"module"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return outRoot
}

// TestBlocksEngineMatchesRecordedDOM compiles the components of
// testdata/engine_diff and compares the DOM after every step of
// testdata/engine_diff/cases.json in jsdom with expected.json (task 039). The
// snapshots were recorded from the legacy DOM engine before task 042 removed
// it, so the block engine keeps its observable DOM. The cases do not empty a
// list with {:else}: the legacy engine never rendered it (see
// TestBlocksEngineRendersEachElse).
func TestBlocksEngineMatchesRecordedDOM(t *testing.T) {
	node, jsdom := requireJsdom(t)
	srcRoot := filepath.Join("testdata", "engine_diff")
	blocks := buildForJsdom(t, srcRoot)
	if _, err := os.Stat(filepath.Join(blocks, "runtime-blocks.js")); err != nil {
		t.Fatalf("block engine build did not stage runtime-blocks.js: %v", err)
	}
	out, err := exec.Command(node, "tools/engine-diff.mjs", jsdom, blocks, filepath.Join(srcRoot, "cases.json"), filepath.Join(srcRoot, "expected.json")).CombinedOutput()
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
		t.Fatalf("block engine DOM differs from the recorded snapshots:\n%s", strings.Join(result.Failures, "\n"))
	}
}

// TestBlocksEngineRendersEachElse covers {#each}…{:else}, which the legacy
// CSR engine never rendered.
func TestBlocksEngineRendersEachElse(t *testing.T) {
	for _, header := range []string{"{#each items as item (item.id)}", "{#each items as item}"} {
		src := `<script>let items = $state([]);</script>
<template><div>
  <button class="add" @click={() => items = [{ id: 1 }]}>add</button>
  <button class="clear" @click={() => items = []}>clear</button>
  <ul>` + header + `<li class="row">{item.id}</li>{:else}<li class="empty">nothing</li>{/each}</ul>
</div></template>`
		assertRuntime(t, "EachElse", src, []rtStep{
			{"assertCount": "li.empty", "equals": 1},
			{"click": "button.add"},
			{"assertCount": "li.empty", "equals": 0},
			{"assertCount": "li.row", "equals": 1},
			{"click": "button.clear"},
			{"assertCount": "li.empty", "equals": 1},
			{"assertCount": "li.row", "equals": 0},
		})
	}
}

// TestBlocksEngineUpdatesSlotContentInEachRows covers slot content placed in
// a child component inside an {#each} row: it must follow both component
// state and the row item. The legacy engine updated neither.
func TestBlocksEngineUpdatesSlotContentInEachRows(t *testing.T) {
	components := map[string]string{
		"Box": `<template><div class="box"><slot /></div></template>`,
		"App": `<script>
  import Box from './Box.vela';
  let count = $state(0);
  let rows = $state([{ id: 1, name: 'a' }]);
</script>
<template><div>
  <button class="inc" @click={() => count++}>inc</button>
  <button class="rename" @click={() => rows = [{ id: 1, name: 'b' }]}>rename</button>
  {#each rows as row (row.id)}<div class="row"><Box><span class="slotted">{row.name}:{count}</span></Box></div>{/each}
</div></template>`,
	}
	result := runRuntimeHarness(t, "App", components, []rtStep{
		{"assertText": "span.slotted", "equals": "a:0"},
		{"click": "button.inc"},
		{"assertText": "span.slotted", "equals": "a:1"},
		{"click": "button.rename"},
		{"assertText": "span.slotted", "equals": "b:1"},
	})
	if !result.OK {
		t.Fatalf("slot content in each rows: %v", result.Failures)
	}
}
