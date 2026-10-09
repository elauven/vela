package main

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// packages/runtime is generated from runtime/; regenerate with
// VELA_UPDATE_RUNTIME_PACKAGE=1 go test -run TestRuntimePackageIsGenerated .
func TestRuntimePackageIsGenerated(t *testing.T) {
	files, err := runtimePackageFiles()
	if err != nil {
		t.Fatal(err)
	}
	update := os.Getenv("VELA_UPDATE_RUNTIME_PACKAGE") == "1"
	var names []string
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(runtimePackageDir, name)
		if update {
			if err := os.MkdirAll(runtimePackageDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, files[name], 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, files[name]) {
			t.Errorf("%s differs from runtime/; run VELA_UPDATE_RUNTIME_PACKAGE=1 go test -run TestRuntimePackageIsGenerated .", path)
		}
	}
	entries, err := os.ReadDir(runtimePackageDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if _, ok := files[entry.Name()]; !ok {
			t.Errorf("%s is not generated; remove it", filepath.Join(runtimePackageDir, entry.Name()))
		}
	}
}
