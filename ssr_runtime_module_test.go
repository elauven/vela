package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// copyRuntimeModule copies ssr_runtime into dir with the given module path
// and lets edit change each Go source.
func copyRuntimeModule(t *testing.T, dir, module string, edit func(name, src string) string) {
	t.Helper()
	entries, err := os.ReadDir("ssr_runtime")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join("ssr_runtime", name))
		if err != nil {
			t.Fatal(err)
		}
		src := string(data)
		if name == "go.mod" {
			src = strings.Replace(src, "module "+SSRRuntimeImportPath, "module "+module, 1)
		} else if edit != nil {
			src = edit(name, src)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// runtimeModuleProject writes a one-component SSR project whose go.mod uses
// the runtime module at runtimeDir under the path module.
func runtimeModuleProject(t *testing.T, root, module, runtimeDir string) {
	t.Helper()
	files := map[string]string{
		"go.mod":        "module example.com/rtmod\n\ngo 1.25\n\nrequire " + module + " v0.0.0\n\nreplace " + module + " => " + filepath.ToSlash(runtimeDir) + "\n",
		"vela.ssr.json": `{"module": "example.com/rtmod/out/server", "host": {"module": "example.com/rtmod/ssrhost"}, "runtime": {"module": "` + module + `"}}`,
		"src/Page.vela": "<script>export let n = 2;\nfunction shout(s) { return /x/.test(s) ? s : s.toUpperCase(); }</script><template><p>{n * 3} {shout('ok')}</p></template>",
		"main.go": `package main

import (
	"os"

	c "example.com/rtmod/out/server/components"
)

func main() {
	if err := c.RenderPage(os.Stdout, c.PageProps{Set: map[string]bool{}}, c.PageSlots{}); err != nil {
		panic(err)
	}
}
`,
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// buildRuntimeModuleProject builds the project with scaffolded hosts and an
// implementation of shout.
func buildRuntimeModuleProject(t *testing.T, root string) {
	t.Helper()
	srcRoot := filepath.Join(root, "src")
	opts := CompileOptions{StatusWriter: io.Discard, SSRModule: SSRDefaultModule, SSRScaffoldHosts: true}
	buildSSRProject(CollectVela(srcRoot), srcRoot, filepath.Join(root, "out"), opts, false)
	stub := filepath.Join(root, "ssrhost", "src", "page", "shout_host.go")
	data, err := os.ReadFile(stub)
	if err != nil {
		t.Fatalf("host stub: %v", err)
	}
	if !strings.Contains(string(data), `vela "`+SSRRuntimeImport()+`"`) && !strings.Contains(string(data), "vela.ErrHostNotImplemented") {
		t.Fatalf("stub does not use the runtime module:\n%s", data)
	}
	impl := strings.Replace(string(data), "return nil, vela.ErrHostNotImplemented", "_ = vela.ErrHostNotImplemented\n\treturn strings.ToUpper(s), nil", 1)
	impl = strings.Replace(impl, "import (\n", "import (\n\t\"strings\"\n", 1)
	if err := os.WriteFile(stub, []byte(impl), 0o644); err != nil {
		t.Fatal(err)
	}
	opts.SSRScaffoldHosts = false
	if errors := buildSSRProject(CollectVela(srcRoot), srcRoot, filepath.Join(root, "out"), opts, false); errors != 0 {
		t.Fatalf("SSR build failed with %d error(s)", errors)
	}
}

func goRun(t *testing.T, dir string) (string, error) {
	t.Helper()
	cmd := exec.Command("go", "run", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GONOSUMDB=*", "GOWORK=off")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// R-12.1b: vela.ssr.json "runtime.module" renames the runtime module in the
// generated renderers and in host stubs.
func TestSSRRuntimeModuleOverride(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not available")
	}
	root := t.TempDir()
	module := "example.com/forked/ssr_runtime"
	runtimeDir := filepath.Join(root, "runtime")
	copyRuntimeModule(t, runtimeDir, module, nil)
	runtimeModuleProject(t, root, module, runtimeDir)
	buildRuntimeModuleProject(t, root)
	if SSRRuntimeImport() != SSRRuntimeImportPath {
		t.Fatalf("the override leaked out of the build: %s", SSRRuntimeImport())
	}
	renderer, err := os.ReadFile(filepath.Join(root, "out", "server", "components", "page.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`vela "` + module + `"`, "var _ = vela." + SSRRuntimeContract} {
		if !strings.Contains(string(renderer), want) {
			t.Fatalf("renderer misses %q:\n%s", want, renderer)
		}
	}
	if strings.Contains(string(renderer), SSRRuntimeImportPath) {
		t.Fatalf("renderer still imports the default runtime path:\n%s", renderer)
	}
	stub, _ := os.ReadFile(filepath.Join(root, "ssrhost", "src", "page", "shout_host.go"))
	if !strings.Contains(string(stub), `"`+module+`"`) {
		t.Fatalf("host stub does not import the configured runtime:\n%s", stub)
	}
	out, err := goRun(t, root)
	if err != nil || !strings.Contains(out, ">6 OK</p>") {
		t.Fatalf("render with the renamed runtime: %v\n%s", err, out)
	}
}

// R-12.1d: a runtime without the contract marker of the generated code fails
// go build with an undefined identifier instead of running.
func TestSSRRuntimeContractMismatchFailsBuild(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not available")
	}
	root := t.TempDir()
	runtimeDir := filepath.Join(root, "runtime")
	copyRuntimeModule(t, runtimeDir, SSRRuntimeImportPath, func(name, src string) string {
		if name == "contract.go" {
			return strings.Replace(src, "const "+SSRRuntimeContract+" = true", "const ContractOther = true", 1)
		}
		return src
	})
	runtimeModuleProject(t, root, SSRRuntimeImportPath, runtimeDir)
	buildRuntimeModuleProject(t, root)
	out, err := goRun(t, root)
	if err == nil || !strings.Contains(out, "undefined: vela."+SSRRuntimeContract) {
		t.Fatalf("a runtime of another contract was accepted: %v\n%s", err, out)
	}
}
