package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
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

func TestCheckPackageSpecifier(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"node_modules/exp/package.json":        `{"name":"exp","exports":{".":"./i.js","./sub":"./s.js","./feature/*":"./f/*.js"}}`,
		"node_modules/root-only/package.json":  `{"name":"root-only","exports":"./i.js"}`,
		"node_modules/cond/package.json":       `{"name":"cond","exports":{"import":"./i.mjs","default":"./i.js"}}`,
		"node_modules/plain/package.json":      `{"name":"plain","main":"i.js"}`,
		"node_modules/plain/lib/a.js":          "",
		"node_modules/@scope/pkg/package.json": `{"name":"@scope/pkg"}`,
		"app/src/deep/x.js":                    "",
	})
	from := filepath.Join(root, "app", "src", "deep")
	for spec, want := range map[string]string{
		"exp":            "",
		"exp/sub":        "",
		"exp/feature/x":  "",
		"exp/other":      "not exported",
		"root-only":      "",
		"root-only/x":    "not exported",
		"cond":           "",
		"cond/x":         "not exported",
		"plain":          "",
		"plain/lib/a":    "",
		"plain/lib/a.js": "",
		"plain/missing":  "does not exist",
		"@scope/pkg":     "",
		"absent":         "not installed",
		"node:fs":        "Node.js built-in",
	} {
		got := checkPackageSpecifier(from, spec)
		if (want == "") != (got == "") || (want != "" && !strings.Contains(got, want)) {
			t.Errorf("%s: got %q, want %q", spec, got, want)
		}
	}
	if got := packageName("@scope/pkg/sub/x"); got != "@scope/pkg" {
		t.Errorf("packageName = %q", got)
	}
}

// vela.manifest.json lists the package imports of every module (task 048),
// and --check-deps warns about the ones that do not resolve.
func TestProjectManifestAndDepsCheck(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"node_modules/tiny/package.json": `{"name":"tiny","exports":{".":"./i.js"}}`,
		"src/App.vela":                   "<script>\nimport { a } from 'tiny';\nimport { b } from 'gone';\n</script><template><p>{a}{b}</p></template>",
		"src/lib/util.js":                "import { c } from 'tiny/deep';\nimport { writable } from 'vela/store';\nexport const d = () => import('lazy-pkg');\n",
	})
	srcRoot := filepath.Join(root, "src")
	outRoot := filepath.Join(root, "out")
	var status bytes.Buffer
	stderr := captureStderr(t, func() {
		opts := CompileOptions{StatusWriter: &status, CheckDeps: true}
		if errors := buildProject(collectVela(srcRoot), srcRoot, outRoot, opts, false); errors != 0 {
			t.Fatalf("build failed with %d error(s)", errors)
		}
	})
	data, err := os.ReadFile(filepath.Join(outRoot, "vela.manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest SSRManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	want := `{"modules":[{"module":"App.js","packages":["gone","tiny"]},{"module":"lib/util.js","packages":["lazy-pkg","tiny/deep"]}],"packages":["gone","lazy-pkg","tiny"]}`
	got, _ := json.Marshal(struct {
		Modules  []ManifestModule `json:"modules"`
		Packages []string         `json:"packages"`
	}{manifest.Modules, manifest.Packages})
	if string(got) != want || len(manifest.Components) != 1 || manifest.Components[0].Mode != "" {
		t.Fatalf("manifest:\n%s", data)
	}
	stderr += status.String()
	for _, line := range []string{
		"App.vela:3:1: [warning] [VELA-NPM-UNRESOLVED] package \"gone\" is not installed",
		"lib/util.js:1:19: [warning] [VELA-NPM-UNRESOLVED] subpath \"./deep\" is not exported by package \"tiny\"",
		"lib/util.js:3:31: [warning] [VELA-NPM-UNRESOLVED] package \"lazy-pkg\" is not installed",
	} {
		if !strings.Contains(stderr, line) {
			t.Errorf("--check-deps output misses %q:\n%s", line, stderr)
		}
	}
	if strings.Contains(stderr, "\"tiny\" is not installed") || strings.Contains(stderr, "vela/store") {
		t.Errorf("--check-deps reported a resolvable import:\n%s", stderr)
	}
	// Without --check-deps nothing is reported.
	quiet := captureStderr(t, func() {
		buildProject(collectVela(srcRoot), srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard}, false)
	})
	if strings.Contains(quiet, DepsCheckDiagnosticCode) {
		t.Errorf("dependencies were checked without --check-deps:\n%s", quiet)
	}
}
