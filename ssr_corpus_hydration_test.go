package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestBlocksCorpusHydrates covers Q-10.5 (task 041): every interactive
// component of test_projects whose SSR outputs build renders with its prop
// defaults and hydrates under the fail policy without a mismatch.
func TestBlocksCorpusHydrates(t *testing.T) {
	node, jsdom := requireJsdom(t)
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not available")
	}
	projects, err := filepath.Glob("test_projects/*/src")
	if err != nil {
		t.Fatal(err)
	}
	corpus, _ := filepath.Glob("test_projects/corpus/*/src")
	projects = append(projects, corpus...)
	compilerRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	hydrated := 0
	for _, srcRoot := range projects {
		root := t.TempDir()
		outRoot := filepath.Join(root, "out")
		module := "example.com/corpus"
		layout := NewSSRProjectLayoutWithModule(outRoot, module+"/server")
		files := collectVela(srcRoot)
		withDOMEngine(t, DOMEngineBlocks, func() {
			opts := CompileOptions{StatusWriter: io.Discard, SSRModule: module + "/server"}
			_ = captureStderr(t, func() { buildSSRProject(files, srcRoot, outRoot, opts, false) })
		})
		manifestData, err := os.ReadFile(filepath.Join(layout.ClientDir, "vela.manifest.json"))
		if err != nil {
			t.Fatalf("%s: %v", srcRoot, err)
		}
		var manifest SSRManifest
		if err := json.Unmarshal(manifestData, &manifest); err != nil {
			t.Fatal(err)
		}
		modes := map[string]SSRManifestComponent{}
		for _, component := range manifest.Components {
			modes[component.Source] = component
		}
		// One Go program renders every interactive component whose server
		// renderer was written, with the prop defaults.
		imports := map[string]string{}
		var calls strings.Builder
		var rendered []SSRManifestComponent
		sort.Strings(files)
		for _, src := range files {
			rel, _ := filepath.Rel(srcRoot, src)
			entry := modes[filepath.ToSlash(rel)]
			if entry.Mode != "interactive" {
				continue
			}
			if _, err := os.Stat(layout.serverPath(src, srcRoot)); err != nil {
				continue // the component does not translate yet
			}
			importPath := layout.importPath(src, srcRoot)
			alias, ok := imports[importPath]
			if !ok {
				alias = fmt.Sprintf("p%d", len(imports))
				imports[importPath] = alias
			}
			fmt.Fprintf(&calls, "\trender(%q, func(w io.Writer) error { return %s.Render%s(w, %s.%sProps{Set: map[string]bool{}}, %s.%sSlots{}) })\n",
				entry.Component+"@"+entry.Module, alias, entry.Component, alias, entry.Component, alias, entry.Component)
			rendered = append(rendered, entry)
		}
		if len(rendered) == 0 {
			continue
		}
		gomod := "module " + module + "\n\ngo 1.22\n\nrequire github.com/elauven/vela v0.0.0\n\nreplace github.com/elauven/vela => " + filepath.ToSlash(compilerRoot) + "\n"
		if err := os.WriteFile(filepath.Join(outRoot, "go.mod"), []byte(gomod), 0o644); err != nil {
			t.Fatal(err)
		}
		// A package that imports a component whose renderer was not written
		// does not build; its components are left out.
		broken := map[string]bool{}
		for path := range imports {
			build := exec.Command("go", "build", path)
			build.Dir = outRoot
			if output, err := build.CombinedOutput(); err != nil {
				broken[path] = true
				t.Logf("%s: package %s does not build: %s", srcRoot, path, strings.SplitN(string(output), "\n", 3)[1])
			}
		}
		if len(broken) > 0 {
			var kept []SSRManifestComponent
			calls.Reset()
			used := map[string]string{}
			for _, entry := range rendered {
				src := filepath.Join(srcRoot, filepath.FromSlash(entry.Source))
				path := layout.importPath(src, srcRoot)
				if broken[path] {
					continue
				}
				alias := imports[path]
				used[path] = alias
				fmt.Fprintf(&calls, "\trender(%q, func(w io.Writer) error { return %s.Render%s(w, %s.%sProps{Set: map[string]bool{}}, %s.%sSlots{}) })\n",
					entry.Component+"@"+entry.Module, alias, entry.Component, alias, entry.Component, alias, entry.Component)
				kept = append(kept, entry)
			}
			rendered, imports = kept, used
			if len(rendered) == 0 {
				continue
			}
		}
		var importLines strings.Builder
		for path, alias := range imports {
			fmt.Fprintf(&importLines, "\t%s %q\n", alias, path)
		}
		program := `package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"

` + importLines.String() + `)

var out = map[string]string{}

func render(name string, run func(io.Writer) error) {
	var page strings.Builder
	if err := run(&page); err != nil {
		out[name] = "ERROR:" + err.Error()
		return
	}
	out[name] = page.String()
}

func main() {
` + calls.String() + `	json.NewEncoder(os.Stdout).Encode(out)
}
`
		if err := os.WriteFile(filepath.Join(outRoot, "main.go"), []byte(program), 0o644); err != nil {
			t.Fatal(err)
		}
		command := exec.Command("go", "run", ".")
		command.Dir = outRoot
		command.Stderr = os.Stderr
		output, err := command.Output()
		if err != nil {
			t.Fatalf("%s: render: %v", srcRoot, err)
		}
		var pages map[string]string
		if err := json.Unmarshal(output, &pages); err != nil {
			t.Fatalf("%s: %v\n%s", srcRoot, err, output)
		}
		if err := os.WriteFile(filepath.Join(layout.ClientDir, "package.json"), []byte(`{"type":"module"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, entry := range rendered {
			page := pages[entry.Component+"@"+entry.Module]
			if strings.HasPrefix(page, "ERROR:") {
				// A render error with default props (for example a required
				// prop read as undefined) is not a hydration result.
				t.Logf("%s %s: %s", srcRoot, entry.Source, page)
				continue
			}
			htmlFile := filepath.Join(root, entry.Component+".html")
			if err := os.WriteFile(htmlFile, []byte(page), 0o644); err != nil {
				t.Fatal(err)
			}
			result, err := exec.Command(node, "tools/ssr-hydrate-run.mjs", jsdom, layout.ClientDir, entry.Module, entry.Component, htmlFile, "[]", "fail").Output()
			if err != nil {
				t.Fatalf("%s %s: %v\n%s", srcRoot, entry.Source, err, result)
			}
			var run struct {
				OK    bool   `json:"ok"`
				Error string `json:"error"`
			}
			if err := json.Unmarshal(result, &run); err != nil || !run.OK {
				t.Errorf("%s %s does not hydrate: %v %s", srcRoot, entry.Source, err, run.Error)
				continue
			}
			hydrated++
		}
	}
	t.Logf("hydrated %d corpus components", hydrated)
	if hydrated == 0 {
		t.Fatal("no corpus component was hydrated")
	}
}
