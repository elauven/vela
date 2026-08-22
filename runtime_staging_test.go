package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildProjectStagesCanonicalRuntimeForEveryComponentDirectory(t *testing.T) {
	srcRoot := t.TempDir()
	outRoot := t.TempDir()
	sources := map[string]string{
		"App.vela":          "<template><p>root</p></template>",
		"nested/Child.vela": "<script>let data = " + string(rune(36)) + "state.deep({ value: 1 });</script><template><p>{data.value}</p></template>",
	}
	for path, source := range sources {
		full := filepath.Join(srcRoot, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	files := collectVela(srcRoot)
	if errors := buildProject(files, srcRoot, outRoot, CompileOptions{}, false); errors != 0 {
		t.Fatalf("project build failed with %d error(s)", errors)
	}
	for _, dir := range []string{outRoot, filepath.Join(outRoot, "nested")} {
		got, err := os.ReadFile(filepath.Join(dir, "runtime.js"))
		if err != nil {
			t.Fatalf("missing staged runtime in %s: %v", dir, err)
		}
		if !bytes.Equal(got, embeddedRuntime) {
			t.Fatalf("staged runtime in %s differs from canonical embedded source", dir)
		}
	}
	code, err := os.ReadFile(filepath.Join(outRoot, "nested", "Child.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(code, []byte("from './runtime.js'")) {
		t.Fatalf("nested component did not retain the sibling runtime import:\n%s", code)
	}
}
