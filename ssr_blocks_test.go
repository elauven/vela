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

type ssrBlocksCase struct {
	Name       string            `json:"name"`
	Component  string            `json:"component"`
	Props      json.RawMessage   `json:"props"`
	Options    json.RawMessage   `json:"options,omitempty"`
	Entries    []string          `json:"entries"`
	Awaits     [][]string        `json:"awaits,omitempty"`
	States     []json.RawMessage `json:"states,omitempty"`
	Steps      []json.RawMessage `json:"steps,omitempty"`
	Mismatches []json.RawMessage `json:"mismatches,omitempty"`
	Compare    bool              `json:"compare,omitempty"`
}

// TestBlocksSSRMarkupMatchesClientDOM renders the SSR block corpus with the
// generated Go renderers and checks it with tools/ssr-blocks-check.mjs: the
// marker grammar and payload stream of HYDRATION_ARCHITECTURE §5–§6, the
// static variant, and equality with the DOM the block-engine client module
// creates for the same props (task 040).
func TestBlocksSSRMarkupMatchesClientDOM(t *testing.T) {
	runSSRBlocksCheck(t)
}

// TestBlocksSSRMarkupParsesInChromium adds the Chromium parse of every
// server document: its node sequence, block markers included, must equal
// the parse checked against the client DOM (Q-10.4).
func TestBlocksSSRMarkupParsesInChromium(t *testing.T) {
	node, _ := requireJsdom(t)
	probe := `const fs=require("fs"); const {chromium}=require("playwright"); if (!fs.existsSync(chromium.executablePath())) process.exit(1)`
	if err := exec.Command(node, "-e", probe).Run(); err != nil {
		t.Skip("Playwright Chromium is unavailable; run npx playwright install chromium")
	}
	runSSRBlocksCheck(t, "--chromium")
}

func runSSRBlocksCheck(t *testing.T, args ...string) {
	t.Helper()
	// List.vela needs callback translation (task 044). Host.vela passes an
	// untyped row field to a typed child prop (vela.AssignProp); Shell.vela is
	// Host with a typed row expression.
	files := map[string]string{}
	for _, pattern := range []string{"testdata/engine_diff/*.vela", "testdata/ssr_blocks/*.vela"} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range matches {
			if filepath.Base(match) == "List.vela" {
				continue
			}
			data, err := os.ReadFile(match)
			if err != nil {
				t.Fatal(err)
			}
			files[filepath.Base(match)] = string(data)
		}
	}
	var cases []ssrBlocksCase
	data, err := os.ReadFile(filepath.Join("testdata", "ssr_blocks", "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	runSSRBlocksSuite(t, files, cases, args...)
}

// runSSRBlocksSuite builds files with the block engine, renders every case
// with the generated Go renderers, and checks it with
// tools/ssr-blocks-check.mjs: markup, stream, static variant, client DOM,
// hydration, update steps, and injected mismatches.
func runSSRBlocksSuite(t *testing.T, files map[string]string, cases []ssrBlocksCase, args ...string) {
	t.Helper()
	node, jsdom := requireJsdom(t)
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not available")
	}
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(srcRoot, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	outRoot := filepath.Join(root, "out")
	projectModule := "example.com/ssrblocks"
	withDOMEngine(t, DOMEngineBlocks, func() {
		opts := CompileOptions{StatusWriter: io.Discard, SSRModule: projectModule + "/server"}
		if errors := buildSSRProject(collectVela(srcRoot), srcRoot, outRoot, opts, false); errors != 0 {
			t.Fatalf("SSR project errors=%d", errors)
		}
	})
	for _, legacy := range []string{"App.hydrate.js", "Host.ssr.json", "Host.hydrate.js"} {
		if _, err := os.Stat(filepath.Join(outRoot, "client", legacy)); err == nil {
			t.Fatalf("block engine wrote legacy hydration artifact %s", legacy)
		}
	}
	rendered := renderSSRBlocksCases(t, outRoot, projectModule, cases)

	checks := make([]map[string]any, 0, len(cases))
	for _, c := range cases {
		var props map[string]any
		if err := json.Unmarshal(c.Props, &props); err != nil {
			t.Fatal(err)
		}
		check := map[string]any{
			"name": c.Name, "module": c.Component + ".js", "export": c.Component, "props": props,
			"html": rendered[c.Name][0], "static": rendered[c.Name][1],
			"entries": c.Entries, "compare": c.Compare,
		}
		if c.Awaits != nil {
			check["awaits"] = c.Awaits
		}
		if c.States != nil {
			check["states"] = c.States
		}
		if c.Steps != nil {
			check["steps"] = c.Steps
		}
		if c.Mismatches != nil {
			check["mismatches"] = c.Mismatches
		}
		checks = append(checks, check)
	}
	checksPath := filepath.Join(root, "rendered.json")
	encoded, err := json.Marshal(checks)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(checksPath, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	clientDir := filepath.Join(outRoot, "client")
	if err := os.WriteFile(filepath.Join(clientDir, "package.json"), []byte(`{"type":"module"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, append([]string{"tools/ssr-blocks-check.mjs", jsdom, clientDir, checksPath}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("ssr blocks check: %v\n%s", err, out)
	}
	var result struct {
		OK       bool     `json:"ok"`
		Total    int      `json:"total"`
		Failures []string `json:"failures"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("invalid check output: %v\n%s", err, out)
	}
	if result.Total != len(cases) {
		t.Fatalf("checked %d of %d cases", result.Total, len(cases))
	}
	if !result.OK {
		t.Fatalf("server markup check failed:\n%s", strings.Join(result.Failures, "\n"))
	}
}

// renderSSRBlocksCases runs one Go program that renders every case with
// RenderXWithOptions and RenderXStatic. It returns [interactive, static]
// HTML by case name.
func renderSSRBlocksCases(t *testing.T, outRoot, projectModule string, cases []ssrBlocksCase) map[string][2]string {
	t.Helper()
	compilerRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	module := "module " + projectModule + "\n\ngo 1.22\n\nrequire github.com/elauven/vela v0.0.0\n\nreplace github.com/elauven/vela => " + filepath.ToSlash(compilerRoot) + "\n"
	if err := os.WriteFile(filepath.Join(outRoot, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	var calls strings.Builder
	for _, c := range cases {
		var props map[string]any
		if err := json.Unmarshal(c.Props, &props); err != nil {
			t.Fatal(err)
		}
		keys := make([]string, 0, len(props))
		for key := range props {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		options := string(c.Options)
		if options == "" {
			options = "{}"
		}
		fmt.Fprintf(&calls, "\tout[%q] = render(%q, %q, %#v, c.Render%sWithOptions, c.Render%sStatic)\n",
			c.Name, string(c.Props), options, keys, c.Component, c.Component)
	}
	main := `package main

import (
	"encoding/json"
	"io"
	"os"
	"reflect"
	"strings"

	vela "github.com/elauven/vela/ssr_runtime"
	c "` + projectModule + `/server/components"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func render[P, S, O any](propsJSON, optionsJSON string, keys []string, withOptions func(io.Writer, P, S, O) error, static func(io.Writer, P, S) error) [2]string {
	var props P
	must(json.Unmarshal([]byte(propsJSON), &props))
	set := map[string]bool{}
	for _, key := range keys {
		set[key] = true
	}
	reflect.ValueOf(&props).Elem().FieldByName("Set").Set(reflect.ValueOf(set))
	var options O
	must(json.Unmarshal([]byte(optionsJSON), &options))
	var slots S
	var interactive, plain strings.Builder
	must(withOptions(&interactive, props, slots, options))
	must(withOptions(vela.StaticWriter(&plain), props, slots, options))
	if optionsJSON == "{}" {
		var static2 strings.Builder
		must(static(&static2, props, slots))
		if static2.String() != plain.String() {
			panic("RenderXStatic differs from a static writer render")
		}
	}
	return [2]string{interactive.String(), plain.String()}
}

func main() {
	out := map[string][2]string{}
` + calls.String() + `	must(json.NewEncoder(os.Stdout).Encode(out))
}
`
	if err := os.WriteFile(filepath.Join(outRoot, "main.go"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "run", ".")
	command.Dir = outRoot
	command.Stderr = os.Stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("render corpus: %v", err)
	}
	var rendered map[string][2]string
	if err := json.Unmarshal(output, &rendered); err != nil {
		t.Fatalf("invalid render output: %v\n%s", err, output)
	}
	return rendered
}
