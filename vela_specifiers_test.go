package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A plain JavaScript entry point that imports a component must resolve to
// the compiled module after a project build; it used to keep './App.vela',
// which no bundler or browser can load.
func TestProjectJSModulesImportCompiledComponents(t *testing.T) {
	node, jsdom := requireJsdom(t)
	srcRoot := t.TempDir()
	files := map[string]string{
		"App.vela":       "<script>export let name = 'x';</script><template><p>{name}</p></template>",
		"lazy/Page.vela": "<template><p>page</p></template>",
		"main.js":        "import App from './App.vela';\nexport const loadPage = () => import('./lazy/Page.vela');\nexport { App };\n",
	}
	for name, content := range files {
		path := filepath.Join(srcRoot, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	outRoot := t.TempDir()
	if errors := BuildProject(CollectVela(srcRoot), srcRoot, outRoot, CompileOptions{StatusWriter: os.Stderr}, false); errors != 0 {
		t.Fatalf("build failed with %d error(s)", errors)
	}
	main, err := os.ReadFile(filepath.Join(outRoot, "main.js"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "import App from './App.js';\nexport const loadPage = () => import('./lazy/Page.js');\nexport { App };\n"; string(main) != want {
		t.Fatalf("main.js =\n%s\nwant\n%s", main, want)
	}
	if err := os.WriteFile(filepath.Join(outRoot, "package.json"), []byte(`{"type":"module"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Node links the whole static graph and the dynamic import.
	script := "const { JSDOM } = await import(" + strconv.Quote("file://"+filepath.ToSlash(jsdom)) + "); globalThis.document = new JSDOM('').window.document; const m = await import('./main.js'); const p = await m.loadPage(); if (typeof m.App !== 'function' || typeof p.default !== 'function') throw new Error('bad exports'); console.log('ok');"
	cmd := exec.Command(node, "--input-type=module", "-e", script)
	cmd.Dir = outRoot
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "ok") {
		t.Fatalf("node could not load the output graph: %v\n%s", err, out)
	}
}
