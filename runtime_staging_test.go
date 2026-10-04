package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// A browser treats every runtime URL as a separate module instance, so a
// project must stage exactly one runtime and every generated module must
// import that file, whatever directory it lives in.
func TestBuildProjectStagesOneRuntimeAtTheOutputRoot(t *testing.T) {
	srcRoot := t.TempDir()
	outRoot := t.TempDir()
	sources := map[string]string{
		"App.vela":               "<template><p>root</p></template>",
		"nested/Child.vela":      "<script>let data = " + string(rune(36)) + "state.deep({ value: 1 });</script><template><p>{data.value}</p></template>",
		"nested/deep/Grand.vela": "<script>let n = " + string(rune(36)) + "state(0);</script><template><button @click={() => n++}>{n}</button></template>",
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
	got, err := os.ReadFile(filepath.Join(outRoot, "runtime.js"))
	if err != nil {
		t.Fatalf("missing staged runtime at the output root: %v", err)
	}
	if !bytes.Equal(got, embeddedRuntime) {
		t.Fatal("staged runtime differs from the canonical embedded source")
	}
	for _, dir := range []string{filepath.Join(outRoot, "nested"), filepath.Join(outRoot, "nested", "deep")} {
		if _, err := os.Stat(filepath.Join(dir, "runtime.js")); !os.IsNotExist(err) {
			t.Fatalf("unexpected runtime copy in %s (err=%v)", dir, err)
		}
	}
	for path, want := range map[string]string{
		"nested/Child.js":      "from '../runtime.js'",
		"nested/deep/Grand.js": "from '../../runtime.js'",
	} {
		code, err := os.ReadFile(filepath.Join(outRoot, path))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(code, []byte(want)) {
			t.Fatalf("%s does not import the root runtime (%s):\n%s", path, want, code)
		}
		if bytes.Contains(code, []byte("from './runtime.js'")) {
			t.Fatalf("%s still imports a sibling runtime copy:\n%s", path, code)
		}
	}
}

func TestRuntimeImportFor(t *testing.T) {
	root := filepath.Join("out", "client")
	for dir, want := range map[string]string{
		root:                          "./runtime.js",
		filepath.Join(root, "a"):      "../runtime.js",
		filepath.Join(root, "a", "b"): "../../runtime.js",
	} {
		if got := runtimeImportFor(dir, root); got != want {
			t.Fatalf("runtimeImportFor(%q) = %q, want %q", dir, got, want)
		}
	}
}
