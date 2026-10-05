package main

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type ssrInitOracleRow struct {
	Group      string                     `json:"group"`
	Name       string                     `json:"name"`
	Props      map[string]json.RawMessage `json:"props"`
	Decls      [][3]string                `json:"decls"`
	Expression string                     `json:"expression"`
	Expected   string                     `json:"expected"`
	State      string                     `json:"state"`
}

// TestSSRInitOracle runs testdata/ssr_init_oracle.json (groups init-order
// and objects-order, task 044). Node recomputes every row; the generated Go
// renderer must write the same template text and the same JSON in the "s"
// payload entry: initialization in declaration order (T-6.6d) and
// JavaScript object key order (T-5.1c).
func TestSSRInitOracle(t *testing.T) {
	node, _ := requireJsdom(t)
	if out, err := exec.Command(node, "tools/verify-ssr-init-oracle.mjs").CombinedOutput(); err != nil {
		t.Fatalf("JavaScript half: %v\n%s", err, out)
	}
	data, err := os.ReadFile(filepath.Join("testdata", "ssr_init_oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rows []ssrInitOracleRow
	if err := json.Unmarshal(data, &rows); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	var calls strings.Builder
	for i, row := range rows {
		var script strings.Builder
		for _, decl := range row.Decls {
			kind, name, init := decl[0], decl[1], decl[2]
			switch kind {
			case "prop":
				fmt.Fprintf(&script, "export let %s = %s;\n", name, init)
			case "bindable":
				fmt.Fprintf(&script, "export let %s = $bindable(%s);\n", name, init)
			case "state":
				fmt.Fprintf(&script, "let %s = $state(%s);\n", name, init)
			case "deep":
				fmt.Fprintf(&script, "let %s = $state.deep(%s);\n", name, init)
			case "derived":
				fmt.Fprintf(&script, "let %s = $derived(%s);\n", name, init)
			case "const":
				fmt.Fprintf(&script, "const %s = %s;\n", name, init)
			default:
				t.Fatalf("%s: unknown declaration kind %q", row.Name, kind)
			}
		}
		component := fmt.Sprintf("Init%d", i)
		source := "<script>\n" + script.String() + "</script>\n<template><p>{" + row.Expression + "}</p></template>\n"
		if err := os.WriteFile(filepath.Join(srcRoot, component+".vela"), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		props, _ := json.Marshal(row.Props)
		if row.Props == nil {
			props = []byte("{}")
		}
		fmt.Fprintf(&calls, "\tout[%q] = render(%q, c.Render%s)\n", component, string(props), component)
	}
	outRoot := filepath.Join(root, "out")
	projectModule := "example.com/ssrinitoracle"
	withDOMEngine(t, DOMEngineBlocks, func() {
		opts := CompileOptions{StatusWriter: io.Discard, SSRModule: projectModule + "/server"}
		if errors := buildSSRProject(collectVela(srcRoot), srcRoot, outRoot, opts, false); errors != 0 {
			t.Fatalf("SSR project errors=%d", errors)
		}
	})
	rendered := runSSROracleProgram(t, outRoot, projectModule, calls.String())
	paragraph := regexp.MustCompile(`<p[^>]*>(.*?)</p>`)
	payload := regexp.MustCompile(`<script type="application/json" data-vela-root>(.*)</script>`)
	for i, row := range rows {
		got := rendered[fmt.Sprintf("Init%d", i)]
		text := paragraph.FindStringSubmatch(strings.ReplaceAll(got, "<!--", "\x00"))
		script := payload.FindStringSubmatch(got)
		if text == nil || script == nil {
			t.Errorf("%s: unexpected output %q", row.Name, got)
			continue
		}
		if value := html.UnescapeString(strings.Split(text[1], "\x00")[0]); value != row.Expected {
			t.Errorf("%s: text = %q, want %q", row.Name, value, row.Expected)
		}
		var stream struct {
			Entries [][]json.RawMessage `json:"entries"`
		}
		if err := json.Unmarshal([]byte(script[1]), &stream); err != nil || len(stream.Entries) == 0 || len(stream.Entries[0]) < 3 {
			t.Errorf("%s: payload %q: %v", row.Name, script[1], err)
			continue
		}
		if state := string(stream.Entries[0][2]); state != row.State {
			t.Errorf("%s: state entry = %s, want %s", row.Name, state, row.State)
		}
	}
}

// runSSROracleProgram writes go.mod and a main that sets props from JSON
// (an absent prop keeps its default; "__undefined__" is undefined), runs
// the calls, and returns the output or "THROW:<kind>" per component.
func runSSROracleProgram(t *testing.T, outRoot, projectModule, calls string) map[string]string {
	t.Helper()
	compilerRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	module := "module " + projectModule + "\n\ngo 1.22\n\nrequire github.com/elauven/vela v0.0.0\n\nreplace github.com/elauven/vela => " + filepath.ToSlash(compilerRoot) + "\n"
	if err := os.WriteFile(filepath.Join(outRoot, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	main := `package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"

	vela "github.com/elauven/vela/ssr_runtime"
	c "` + projectModule + `/server/components"
)

func render[P, S any](values string, run func(io.Writer, P, S) error) string {
	var raw map[string]any
	if err := json.Unmarshal([]byte(values), &raw); err != nil {
		panic(err)
	}
	var props P
	target := reflect.ValueOf(&props).Elem()
	set := map[string]bool{}
	for name, value := range raw {
		field := target.FieldByNameFunc(func(field string) bool { return strings.EqualFold(field, name) })
		if value == "__undefined__" {
			value = vela.Undefined()
		}
		if value != nil {
			field.Set(reflect.ValueOf(value).Convert(field.Type()))
		}
		set[name] = true
	}
	target.FieldByName("Set").Set(reflect.ValueOf(set))
	var slots S
	var out strings.Builder
	if err := run(&out, props, slots); err != nil {
		var thrown *vela.RenderError
		if errors.As(err, &thrown) {
			return "THROW:" + thrown.Kind
		}
		return "ERROR:" + err.Error()
	}
	return out.String()
}

func main() {
	out := map[string]string{}
` + calls + `	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		panic(err)
	}
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
		t.Fatalf("render oracle: %v", err)
	}
	var rendered map[string]string
	if err := json.Unmarshal(output, &rendered); err != nil {
		t.Fatalf("invalid output: %v\n%s", err, output)
	}
	return rendered
}
