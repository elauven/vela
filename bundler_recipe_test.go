package main

import (
	"github.com/elauven/vela/runtime"

	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// bundlerFixture is a temporary application: the compiled output of the
// fixture projects, node_modules with the vendored package and the runtime
// package, and the esbuild of the repository's test tooling.
type bundlerFixture struct {
	t       *testing.T
	root    string
	esbuild string
	node    string
}

func newBundlerFixture(t *testing.T) *bundlerFixture {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		skipBundlers(t, "node not available")
	}
	esbuild, err := filepath.Abs(filepath.Join("node_modules", ".bin", "esbuild"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(esbuild); err != nil {
		skipBundlers(t, "esbuild not installed; run npm ci")
	}
	f := &bundlerFixture{t: t, root: t.TempDir(), esbuild: esbuild, node: node}
	f.copyDir(filepath.Join("testdata", "bundler", "packages", "tiny-fmt"), filepath.Join(f.root, "node_modules", "tiny-fmt"), nil)
	f.copyDir(runtimePackageDir, filepath.Join(f.root, "node_modules", filepath.FromSlash(runtime.PackageName)), nil)
	return f
}

func (f *bundlerFixture) copyDir(from, to string, edit func(name, src string) string) {
	f.t.Helper()
	entries, err := os.ReadDir(from)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := os.MkdirAll(to, 0o755); err != nil {
		f.t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(from, entry.Name()))
		if err != nil {
			f.t.Fatal(err)
		}
		if edit != nil {
			data = []byte(edit(entry.Name(), string(data)))
		}
		if err := os.WriteFile(filepath.Join(to, entry.Name()), data, 0o644); err != nil {
			f.t.Fatal(err)
		}
	}
}

// compile builds a fixture project into the application directory out.
func (f *bundlerFixture) compile(project, out string, bare bool) {
	f.t.Helper()
	srcRoot := filepath.Join("testdata", "bundler", project)
	opts := CompileOptions{StatusWriter: io.Discard, BareRuntimeImports: bare}
	if bare {
		opts.RuntimeImport = runtime.PackageName
	}
	if errors := buildProject(CollectVela(srcRoot), srcRoot, filepath.Join(f.root, out), opts, false); errors != 0 {
		f.t.Fatalf("build of %s failed with %d error(s)", project, errors)
	}
}

// bundle runs esbuild on entry (relative to the application root).
func (f *bundlerFixture) bundle(entry, outfile string, extra ...string) (string, error) {
	f.t.Helper()
	args := append([]string{entry, "--bundle", "--format=esm", "--outfile=" + outfile, "--log-level=error"}, extra...)
	cmd := exec.Command(f.esbuild, args...)
	cmd.Dir = f.root
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (f *bundlerFixture) read(name string) string {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.root, name))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(data)
}

type bundlerPageRun struct {
	Before []string `json:"before"`
	After  []string `json:"after"`
	Logs   []string `json:"logs"`
	Error  string   `json:"error"`
}

// run serves dir with an index page that loads bundle and clicks every
// button once in Chromium.
func (f *bundlerFixture) run(dir, bundle string) bundlerPageRun {
	f.t.Helper()
	page := `<!doctype html><div id="app"></div><div id="other"></div><script type="module" src="./` + bundle + `"></script>`
	if err := os.WriteFile(filepath.Join(f.root, dir, "index.html"), []byte(page), 0o644); err != nil {
		f.t.Fatal(err)
	}
	cmd := exec.Command(f.node, "tools/bundler-recipe-check.mjs", filepath.Join(f.root, dir))
	out, err := cmd.CombinedOutput()
	var result bundlerPageRun
	if err != nil || json.Unmarshal(out, &result) != nil {
		f.t.Fatalf("chromium run: %v\n%s", err, out)
	}
	if result.Error != "" {
		if strings.Contains(result.Error, "Executable doesn't exist") || strings.Contains(result.Error, "browserType.launch") {
			skipBundlers(f.t, "chromium not installed")
		}
		f.t.Fatalf("chromium run: %s\n%v", result.Error, result.Logs)
	}
	return result
}

// TestBundlerRecipeEsbuild is the esbuild recipe of docs/GUIDE.md as a test
// (task 048): compiled output with package imports and bare runtime imports
// bundles with esbuild and runs in Chromium; a production bundle has no
// development code; one runtime instance per page; a runtime of another
// contract fails at bundle time.
func TestBundlerRecipeEsbuild(t *testing.T) {
	f := newBundlerFixture(t)
	f.compile("app", "build", true)
	if main := f.read("build/main.js"); !strings.Contains(main, "from './App.js'") {
		t.Fatalf("entry point still imports the component source:\n%s", main)
	}
	app := f.read("build/App.js")
	for _, want := range []string{"from '" + runtime.PackageName + "'", "from '" + runtime.PackageName + "/blocks'", "from 'tiny-fmt'"} {
		if !strings.Contains(app, want) {
			t.Fatalf("App.js misses %q:\n%s", want, app)
		}
	}
	if _, err := os.Stat(filepath.Join(f.root, "build", "runtime.js")); err == nil {
		t.Fatal("bare runtime imports still staged runtime.js")
	}

	for _, mode := range []string{"development", "production"} {
		out, err := f.bundle("build/main.js", "dist/"+mode+".js", "--define:process.env.NODE_ENV=\""+mode+"\"")
		if err != nil {
			t.Fatalf("%s bundle: %v\n%s", mode, err, out)
		}
		if strings.Contains(f.read("dist/"+mode+".js"), "process.env") {
			t.Errorf("%s bundle still reads process.env", mode)
		}
		// The development check of the runtime is a constant after the
		// bundler's replacement: run code that reads it in Node, where
		// process exists, so only the replacement decides.
		probe := "import { $$isDev } from '" + runtime.PackageName + "';\nconsole.log('dev=' + $$isDev());\n"
		if err := os.WriteFile(filepath.Join(f.root, "probe.js"), []byte(probe), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := f.bundle("probe.js", "dist/probe-"+mode+".mjs", "--define:process.env.NODE_ENV=\""+mode+"\""); err != nil {
			t.Fatalf("%s probe bundle: %v\n%s", mode, err, out)
		}
		probeCode := f.read("dist/probe-" + mode + ".mjs")
		if strings.Contains(probeCode, "process") {
			t.Errorf("%s probe bundle still reads process:\n%s", mode, probeCode)
		}
		cmd := exec.Command(f.node, filepath.Join(f.root, "dist", "probe-"+mode+".mjs"))
		cmd.Env = append(os.Environ(), "NODE_ENV=opposite")
		got, err := cmd.CombinedOutput()
		want := map[string]string{"development": "dev=true", "production": "dev=false"}[mode]
		if err != nil || strings.TrimSpace(string(got)) != want {
			t.Errorf("%s bundle: development check gives %q (%v), want %q", mode, got, err, want)
		}
		result := f.run("dist", mode+".js")
		if len(result.Before) != 1 || result.Before[0] != "COUNT! 07" || result.After[0] != "COUNT! 08" || len(result.Logs) != 0 {
			t.Errorf("%s page: %+v", mode, result)
		}
	}

	// Two compiled directories bundled together share one runtime instance.
	f.compile("second", "build2", true)
	entry := "import App from './build/App.js';\nimport Other from './build2/Other.js';\nApp(document.getElementById('app'), {});\nOther(document.getElementById('other'), {});\n"
	if err := os.WriteFile(filepath.Join(f.root, "both.js"), []byte(entry), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := f.bundle("both.js", "dist/both.js"); err != nil {
		t.Fatalf("bundle of two outputs: %v\n%s", err, out)
	}
	if n := strings.Count(f.read("dist/both.js"), "Symbol.for(\"vela.defer\")"); n != 1 {
		t.Errorf("bundle of two outputs has %d runtime instances, want 1", n)
	}
	if result := f.run("dist", "both.js"); len(result.After) != 2 || result.After[1] != "on" || len(result.Logs) != 0 {
		t.Errorf("two outputs page: %+v", result)
	}

	// A runtime of another contract fails when esbuild links the modules.
	f.copyDir(runtimePackageDir, filepath.Join(f.root, "node_modules", filepath.FromSlash(runtime.PackageName)), func(name, src string) string {
		return strings.ReplaceAll(src, RuntimeContractExport, "$$runtimeContractOther")
	})
	out, err := f.bundle("build/main.js", "dist/mismatch.js")
	if err == nil || !strings.Contains(out, RuntimeContractExport) {
		t.Fatalf("a runtime of another contract bundled: %v\n%s", err, out)
	}
}
