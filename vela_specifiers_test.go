package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRewriteJSModuleSpecifiers(t *testing.T) {
	cases := map[string]string{
		"import App from './App.vela';":                       "import App from './App.js';",
		`import { A, B } from "../ui/Card.vela"`:              `import { A, B } from "../ui/Card.js"`,
		"import './Side.vela';":                               "import './Side.js';",
		"export { default as Card } from './Card.vela';":      "export { default as Card } from './Card.js';",
		"export * from './All.vela';":                         "export * from './All.js';",
		"const Page = () => import('./Page.vela');":           "const Page = () => import('./Page.js');",
		"import data from './d.json' with { type: 'json' };":  "import data from './d.json' with { type: 'json' };",
		"import { format } from 'date-fns';":                  "import { format } from 'date-fns';",
		"const s = './App.vela'; f('./B.vela');":              "const s = './App.vela'; f('./B.vela');",
		"const t = `import App from './App.vela'`;":           "const t = `import App from './App.vela'`;",
		"// import App from './App.vela'\nlet x = 1 / 2 / 3;": "// import App from './App.vela'\nlet x = 1 / 2 / 3;",
		"const o = { from: './A.vela' };":                     "const o = { from: './A.vela' };",
		"import { writable } from 'vela/store';":              "import { writable } from './runtime.js';",
		"import { tick } from \"vela\";":                      "import { tick } from \"./runtime.js\";",
		"import x from 'velatile';":                           "import x from 'velatile';",
		"import 'pkg/file.vela';":                             "import 'pkg/file.vela';",
	}
	for src, want := range cases {
		if got := rewriteJSModuleSpecifiers(src, "./runtime.js"); got != want {
			t.Errorf("rewriteJSModuleSpecifiers(%q)\n got  %q\n want %q", src, got, want)
		}
	}
}

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
	if errors := buildProject(collectVela(srcRoot), srcRoot, outRoot, CompileOptions{StatusWriter: os.Stderr}, false); errors != 0 {
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
