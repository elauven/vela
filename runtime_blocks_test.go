package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestRuntimeBlocksUnderJsdom runs the block runtime unit tests
// (tools/runtime-blocks-test.mjs) against runtime/runtime-blocks.js. jsdom
// comes from the project's npm dependencies; the strict browser runner
// requires this test, so CI cannot skip it.
func TestRuntimeBlocksUnderJsdom(t *testing.T) {
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
	dir := t.TempDir()
	for _, name := range []string{"runtime.js", "runtime-blocks.js"} {
		data, err := os.ReadFile(filepath.Join("runtime", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, "tools/runtime-blocks-test.mjs", dir, jsdom).CombinedOutput()
	if err != nil {
		t.Fatalf("block runtime tests: %v\n%s", err, out)
	}
	var result struct {
		OK       bool     `json:"ok"`
		Total    int      `json:"total"`
		Failures []string `json:"failures"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("invalid block runtime test output: %v\n%s", err, out)
	}
	if result.Total == 0 {
		t.Fatal("block runtime tests selected no cases")
	}
	if !result.OK {
		t.Fatalf("block runtime failures:\n%v", result.Failures)
	}
}
