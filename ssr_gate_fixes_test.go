package main

// Tests for the defects the SSR beta gate (task 031) found.

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func diagCodes(ds *DiagSet) map[string]int {
	codes := map[string]int{}
	for _, d := range ds.All() {
		codes[d.Code]++
	}
	return codes
}

func TestVelaUnknownImportIsRejected(t *testing.T) {
	script := `import { auth, onMount, writable } from 'vela';`
	ds := NewDiagSet("test.vela", script)
	validateScriptForms(ParseJS(script), ds)
	if codes := diagCodes(ds); codes["VELA-UNKNOWN-IMPORT"] != 1 {
		t.Fatalf("want one VELA-UNKNOWN-IMPORT for auth, got %v", ds.All())
	}
	builtin := `import { $$dispatch } from 'vela';`
	ds = NewDiagSet("test.vela", builtin)
	validateScriptForms(ParseJS(builtin), ds)
	if codes := diagCodes(ds); codes["VELA-BUILTIN-IMPORT"] != 1 || codes["VELA-UNKNOWN-IMPORT"] != 0 {
		t.Fatalf("$$dispatch must stay VELA-BUILTIN-IMPORT, got %v", ds.All())
	}
}

func runTemplateForms(t *testing.T, template string) *DiagSet {
	t.Helper()
	nodes, err := parseTemplate(template)
	if err != nil {
		t.Fatal(err)
	}
	ds := NewDiagSet("test.vela", template)
	validateTemplateForms(nodes, template, ds)
	return ds
}

func TestDuplicateAttributeAndBraces(t *testing.T) {
	ds := runTemplateForms(t, `<a href="x" :href={a} title="x {a}">x</a>`)
	codes := diagCodes(ds)
	if codes["VELA-DUPLICATE-ATTR"] != 1 || codes["VELA-ATTR-BRACES"] != 1 {
		t.Fatalf("want one VELA-DUPLICATE-ATTR and one VELA-ATTR-BRACES, got %v", ds.All())
	}
	// A static class with a dynamic class is merged, not a duplicate.
	ds = runTemplateForms(t, `<span class="badge" :class={a}>x</span>`)
	if codes := diagCodes(ds); codes["VELA-DUPLICATE-ATTR"] != 0 {
		t.Fatalf("class pair must merge, got %v", ds.All())
	}
}

func TestStaticClassMergesWithDynamicClass(t *testing.T) {
	nodes, err := parseTemplate("<span class=\"a  b`\" :class={v ? 'on' : ''}>x</span>")
	if err != nil {
		t.Fatal(err)
	}
	attrs := nodes[0].(*ElementNode).Attrs
	if len(attrs) != 1 || !attrs[0].IsBind || attrs[0].Expr == nil || len(attrs[0].Expr.Errors) != 0 {
		t.Fatalf("want one dynamic class attribute, got %+v", attrs)
	}
	if got, want := attrs[0].Expr.Source, "`a b\\` ${(v ? 'on' : '')}`"; got != want {
		t.Fatalf("merged class source %q, want %q", got, want)
	}
}

func TestSSRComponentPackageWithRelativeProject(t *testing.T) {
	layout := NewSSRProjectLayoutWithModule("out", "example.com/app/server")
	abs, err := filepath.Abs(filepath.Join("testdata", "rel", "src", "editor", "A.vela"))
	if err != nil {
		t.Fatal(err)
	}
	if got := layout.importPath(abs, filepath.Join("testdata", "rel", "src")); got != "example.com/app/server/components/editor" {
		t.Fatalf("import path %q", got)
	}
}

// TestSSRGateFixesRenderLikeClient renders the fixed shapes on the server
// and hydrates them: merged classes, class directives without static
// classes, a static text joined with a dynamic text in parsed HTML, and a
// module function that reads a const declared after it.
func TestSSRGateFixesRenderLikeClient(t *testing.T) {
	files := map[string]string{
		"lib.js": `export function count() { return ITEMS.length; }
export const ITEMS = ['a', 'b'];
`,
		"Fixes.vela": `<script>
  import { count } from './lib.js';
  export let variant = 'v';
  export let label = 'L';
  let on = $state(false);
  let show = $state(true);
</script>
<template>
  <div>
    <span class="badge  x" :class={variant}>a</span>
    <button class:active={on} @click={() => on = !on}>b</button>
    <i class:shown={show}>c</i>
    {#if show}
      <p>
        <b>x</b>
        {on ? 'yes' : label}
      </p>
    {/if}
    <em>{count()}</em>
  </div>
</template>
`,
	}
	cases := []ssrBlocksCase{{Name: "gate fixes", Component: "Fixes", Props: json.RawMessage(`{"variant":"v","label":"L"}`), Entries: []string{"Fixes"}, Compare: true}}
	runSSRBlocksSuite(t, files, cases)
}

// TestSSRChildFailurePropagates checks that a parent whose child has no Go
// renderer fails with VELA-SSR-CHILD, has no renderer file, and leaves the
// rest of its package buildable.
func TestSSRChildFailurePropagates(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not available")
	}
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	files := map[string]string{
		"Child.vela":  "<script>\n  let t = $state(Date.now());\n</script>\n<template><p>{t}</p></template>\n",
		"Parent.vela": "<script>\n  import Child from './Child.vela';\n</script>\n<template><div><Child /></div></template>\n",
		"Other.vela":  "<template><p>ok</p></template>\n",
	}
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(srcRoot, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	report := filepath.Join(root, "report.json")
	outRoot := filepath.Join(root, "out")
	opts := CompileOptions{StatusWriter: io.Discard, SSRModule: "example.com/child/out/server", SSRReport: report}
	if errors := buildSSRProject(collectVela(srcRoot), srcRoot, outRoot, opts, false); errors == 0 {
		t.Fatal("the build must fail")
	}
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	var parsed ssrGateReport
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	for _, c := range parsed.Components {
		status[c.Source] = c.Status + " " + strings.SplitN(c.Error, ":", 2)[0]
	}
	if status["Parent.vela"] != "error VELA-SSR-CHILD" || status["Other.vela"] != "ok " {
		t.Fatalf("statuses %v", status)
	}
	if _, err := os.Stat(filepath.Join(outRoot, "server", "components", "parent.go")); err == nil {
		t.Fatal("the renderer of the failed parent is still written")
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/child\n\ngo 1.25\n\n"+ssrRuntimeRequire(mustGetwd(t))), 0o644); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "./out/server/...")
	build.Dir = root
	build.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GONOSUMDB=*")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("the remaining package does not build: %v\n%s", err, out)
	}
}

func mustGetwd(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestSSRGateComparison checks that the gate comparison reports every kind
// of regression and keeps improvements apart.
func TestSSRGateComparison(t *testing.T) {
	want := ssrGateProject{ID: "p", Components: map[string]ssrGateComponent{
		"A.vela": {Status: "ok", Mode: "interactive", Render: "ok", Browser: "pass", Sites: map[string]string{"s1": "server-native", "s2": "client"}},
		"B.vela": {Status: "ok", Mode: "static", Render: "ok", Browser: "pass"},
		"C.vela": {Status: "error", Error: "VELA-SSR-EXPR", Sites: map[string]string{"s1": "unsupported"}},
	}}
	clone := func() ssrGateProject {
		got := ssrGateProject{ID: "p", Components: map[string]ssrGateComponent{}}
		for name, c := range want.Components {
			sites := map[string]string{}
			for k, v := range c.Sites {
				sites[k] = v
			}
			c.Sites = sites
			got.Components[name] = c
		}
		return got
	}
	if r, c := compareSSRGateProject(want, clone()); len(r)+len(c) != 0 {
		t.Fatalf("identical projects differ: %v %v", r, c)
	}
	regress := []struct {
		name   string
		change func(p ssrGateProject)
	}{
		{"site downgrade", func(p ssrGateProject) { p.Components["A.vela"].Sites["s1"] = "server-host" }},
		{"new unsupported site", func(p ssrGateProject) { p.Components["A.vela"].Sites["s3"] = "unsupported" }},
		{"removed site", func(p ssrGateProject) { delete(p.Components["A.vela"].Sites, "s2") }},
		{"status ok to error", func(p ssrGateProject) {
			c := p.Components["B.vela"]
			c.Status, c.Error = "error", "VELA-SSR-EXPR"
			p.Components["B.vela"] = c
		}},
		{"other error code", func(p ssrGateProject) {
			c := p.Components["C.vela"]
			c.Error = "VELA-SSR-HOST-MISSING"
			p.Components["C.vela"] = c
		}},
		{"interactive to static", func(p ssrGateProject) {
			c := p.Components["A.vela"]
			c.Mode = "static"
			p.Components["A.vela"] = c
		}},
		{"render fails", func(p ssrGateProject) {
			c := p.Components["B.vela"]
			c.Render, c.Browser = "error", ""
			p.Components["B.vela"] = c
		}},
		{"browser fails", func(p ssrGateProject) {
			c := p.Components["B.vela"]
			c.Browser = "initial: hydrated DOM differs from CSR"
			p.Components["B.vela"] = c
		}},
		{"removed component", func(p ssrGateProject) { delete(p.Components, "B.vela") }},
	}
	for _, tc := range regress {
		got := clone()
		tc.change(got)
		if r, _ := compareSSRGateProject(want, got); len(r) == 0 {
			t.Errorf("%s: no regression reported", tc.name)
		}
	}
	improve := []struct {
		name   string
		change func(p ssrGateProject)
	}{
		{"site upgrade", func(p ssrGateProject) { p.Components["A.vela"].Sites["s2"] = "server-native" }},
		{"error fixed", func(p ssrGateProject) {
			p.Components["C.vela"] = ssrGateComponent{Status: "ok", Mode: "static", Render: "ok", Browser: "pass", Sites: map[string]string{"s1": "server-native"}}
		}},
		{"new component", func(p ssrGateProject) { p.Components["D.vela"] = ssrGateComponent{Status: "ok"} }},
	}
	for _, tc := range improve {
		got := clone()
		tc.change(got)
		r, c := compareSSRGateProject(want, got)
		if len(r) != 0 || len(c) == 0 {
			t.Errorf("%s: regressions %v, changes %v", tc.name, r, c)
		}
	}
}
