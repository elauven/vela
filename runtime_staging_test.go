package main

import (
	"strings"
	"bytes"
	"os"
	"os/exec"
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

func TestRuntimeModulesSplitHydrationFromCore(t *testing.T) {
	core := runtimeExportNames(embeddedRuntime)
	if len(hydrationRuntimeExports) == 0 {
		t.Fatal("hydration runtime exports nothing")
	}
	for name := range hydrationRuntimeExports {
		if core[name] {
			t.Fatalf("%s is exported by both runtime modules", name)
		}
	}
	if !core[runtimeContractExport] {
		t.Fatalf("core runtime does not export the contract marker %s", runtimeContractExport)
	}
	if !bytes.Contains(embeddedHydrationRuntime, []byte("import { "+runtimeContractExport+" } from './runtime.js';")) {
		t.Fatal("hydration runtime does not check the runtime contract")
	}
}

func TestBuildProjectStagesHydrationRuntimeOnlyForHydrationBuilds(t *testing.T) {
	srcRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcRoot, "App.vela"), []byte("<script>let n = "+string(rune(36))+"state(0);</script><template><button @click={() => n++}>{n}</button></template>"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := collectVela(srcRoot)
	for _, hydration := range []bool{false, true} {
		outRoot := t.TempDir()
		if errors := buildProject(files, srcRoot, outRoot, CompileOptions{HydrationSupport: hydration}, false); errors != 0 {
			t.Fatalf("project build failed with %d error(s)", errors)
		}
		_, err := os.Stat(filepath.Join(outRoot, "runtime-hydration.js"))
		if hydration && err != nil {
			t.Fatalf("hydration build did not stage runtime-hydration.js: %v", err)
		}
		if !hydration && !os.IsNotExist(err) {
			t.Fatalf("CSR build staged runtime-hydration.js (err=%v)", err)
		}
		code, err := os.ReadFile(filepath.Join(outRoot, "App.js"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(code, []byte(runtimeContractExport)) || !bytes.Contains(code, []byte("from './runtime.js'")) {
			t.Fatalf("generated module does not import the runtime contract:\n%s", code)
		}
	}
}

func TestProductionRuntimeDisablesDevelopmentChecks(t *testing.T) {
	if !bytes.Contains(embeddedRuntime, []byte(runtimeDevelopmentFlag)) {
		t.Fatal("core runtime no longer contains the development flag the production variant replaces")
	}
	production := runtimeSource(true)
	if bytes.Contains(production, []byte(runtimeDevelopmentFlag)) || !bytes.Contains(production, []byte("const DEV = false;")) {
		t.Fatal("production runtime keeps the development flag")
	}
	if !bytes.Equal(runtimeSource(false), embeddedRuntime) {
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
	if errors := buildProject(collectVela(srcRoot), srcRoot, outRoot, CompileOptions{}, false); errors != 0 {
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
	stale := bytes.Replace(embeddedRuntime, []byte("export const "+runtimeContractExport), []byte("export const $$runtimeContract0"), 1)
	if err := os.WriteFile(filepath.Join(outRoot, "runtime.js"), stale, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := load()
	if err == nil || !bytes.Contains(out, []byte(runtimeContractExport)) {
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
	files := collectVela(".")
	if len(files) != 2 {
		t.Fatalf("collectVela(\".\") = %v, want App.vela and nested/Child.vela", files)
	}
}

// TestHydrateRuntimeCarriesTheVerificationPolicy covers --hydration-verify
// (V-11.3c): the staged runtime-hydrate.js has the chosen default.
func TestHydrateRuntimeCarriesTheVerificationPolicy(t *testing.T) {
	for verify, want := range map[string]string{"": "repair", "repair": "repair", "fail": "fail", "off": "off"} {
		dir := t.TempDir()
		withDOMEngine(t, DOMEngineBlocks, func() {
			if err := stageRuntime(dir, CompileOptions{HydrationVerify: verify}); err != nil {
				t.Fatal(err)
			}
		})
		data, err := os.ReadFile(filepath.Join(dir, "runtime-hydrate.js"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "const VERIFY = '"+want+"';") {
			t.Errorf("--hydration-verify=%q: staged runtime lacks VERIFY %q", verify, want)
		}
	}
}
