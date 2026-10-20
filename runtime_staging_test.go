package main

import (
	"github.com/elauven/vela/runtime"

	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	files := CollectVela(srcRoot)
	if errors := buildProject(files, srcRoot, outRoot, CompileOptions{}, false); errors != 0 {
		t.Fatalf("project build failed with %d error(s)", errors)
	}
	got, err := os.ReadFile(filepath.Join(outRoot, "runtime.js"))
	if err != nil {
		t.Fatalf("missing staged runtime at the output root: %v", err)
	}
	if !bytes.Equal(got, runtime.Core) {
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
		if got := RuntimeImportFor(dir, root); got != want {
			t.Fatalf("runtimeImportFor(%q) = %q, want %q", dir, got, want)
		}
	}
}

func TestProductionRuntimeDisablesDevelopmentChecks(t *testing.T) {
	if !bytes.Contains(runtime.Core, []byte(runtime.DevelopmentFlag)) {
		t.Fatal("core runtime no longer contains the development flag the production variant replaces")
	}
	production := runtimeSource(true)
	if bytes.Contains(production, []byte(runtime.DevelopmentFlag)) || !bytes.Contains(production, []byte("const DEV = false;")) {
		t.Fatal("production runtime keeps the development flag")
	}
	if !bytes.Equal(runtimeSource(false), runtime.Core) {
		t.Fatal("development runtime differs from the embedded runtime")
	}
}

func TestStaleRuntimeContractFailsAtModuleLoad(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available")
	}
	srcRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcRoot, "App.vela"), []byte("<script>let n = "+string(rune(36))+"state(0);</script><template><p>{n}</p></template>"), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := t.TempDir()
	if errors := buildProject(CollectVela(srcRoot), srcRoot, outRoot, CompileOptions{}, false); errors != 0 {
		t.Fatalf("project build failed with %d error(s)", errors)
	}
	if err := os.WriteFile(filepath.Join(outRoot, "package.json"), []byte(`{"type":"module"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	load := func() ([]byte, error) {
		cmd := exec.Command(node, "--input-type=module", "-e", "await import('./App.js'); console.log('loaded');")
		cmd.Dir = outRoot
		return cmd.CombinedOutput()
	}
	if out, err := load(); err != nil {
		t.Fatalf("module with the current runtime failed to load: %v\n%s", err, out)
	}
	stale := bytes.Replace(runtime.Core, []byte("export const "+RuntimeContractExport), []byte("export const $$runtimeContract0"), 1)
	if err := os.WriteFile(filepath.Join(outRoot, "runtime.js"), stale, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := load()
	if err == nil || !bytes.Contains(out, []byte(RuntimeContractExport)) {
		t.Fatalf("module loaded with a runtime of another contract version: %v\n%s", err, out)
	}
}

func TestCollectVelaAcceptsDotRoot(t *testing.T) {
	dir := t.TempDir()
	for _, path := range []string{"App.vela", "nested/Child.vela", ".hidden/Skip.vela", "node_modules/pkg/Skip.vela"} {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("<template><p>x</p></template>"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(previous) }()
	files := CollectVela(".")
	if len(files) != 2 {
		t.Fatalf("collectVela(\".\") = %v, want App.vela and nested/Child.vela", files)
	}
}

// TestHydrateRuntimeCarriesTheVerificationPolicy covers --hydration-verify
// (V-11.3c): the staged runtime-hydrate.js has the chosen default.
func TestHydrateRuntimeCarriesTheVerificationPolicy(t *testing.T) {
	for verify, want := range map[string]string{"": "repair", "repair": "repair", "fail": "fail", "off": "off"} {
		dir := t.TempDir()

		if err := stageRuntime(dir, CompileOptions{HydrationVerify: verify}); err != nil {
			t.Fatal(err)
		}

		data, err := os.ReadFile(filepath.Join(dir, "runtime-hydrate.js"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "const VERIFY = '"+want+"';") {
			t.Errorf("--hydration-verify=%q: staged runtime lacks VERIFY %q", verify, want)
		}
	}
}

// TestListedFilesGetTheRuntime runs `vela --out dir File.vela`: the module
// imports ./runtime.js, so the runtime must be written next to it.
func TestListedFilesGetTheRuntime(t *testing.T) {
	out := t.TempDir()
	cmd := exec.Command("go", "run", ".", "--out", out, filepath.Join("examples", "Counter.vela"))
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if data, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("vela: %v\n%s", err, data)
	}
	for _, name := range []string{"Counter.js", "runtime.js", "runtime-blocks.js", "runtime-hydrate.js"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Errorf("missing %s", name)
		}
	}
}
