package main

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestTypeScriptDocsExamples (task 089): the TypeScript examples of
// AGENTS.md and of the TypeScript chapter of docs/GUIDE.md, as an agent or a
// reader would copy them, build with --ssr without errors and pass
// `vela check --types`.
func TestTypeScriptDocsExamples(t *testing.T) {
	blocks := func(file, from, to string) []string {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		start := strings.Index(text, from)
		if start < 0 {
			t.Fatalf("%s has no %q", file, from)
		}
		text = text[start:]
		if end := strings.Index(text[len(from):], to); end >= 0 {
			text = text[:len(from)+end]
		}
		var out []string
		for _, m := range regexp.MustCompile("(?s)```(?:vela|ts)\n(.*?)```").FindAllStringSubmatch(text, -1) {
			out = append(out, m[1])
		}
		return out
	}
	agents := blocks("AGENTS.md", "## TypeScript", "\n## ")
	guide := blocks(filepath.Join("docs", "GUIDE.md"), "## 12. TypeScript", "\n## ")
	if len(agents) != 1 || len(guide) < 7 {
		t.Fatalf("examples: AGENTS.md %d, GUIDE.md %d", len(agents), len(guide))
	}
	find := func(prefix string) string {
		for _, b := range guide {
			if strings.HasPrefix(b, prefix) {
				return strings.TrimPrefix(b, prefix)
			}
		}
		t.Fatalf("GUIDE.md has no example starting with %q", prefix)
		return ""
	}
	// The parent and the price component of the guide have no file name.
	var parent, price string
	for _, b := range guide {
		switch {
		case strings.Contains(b, "import { Counter }"):
			parent = strings.TrimPrefix(b, "<!-- parent -->\n")
		case strings.Contains(b, "import { format, type Money }"):
			price = b
		}
	}
	files := map[string]string{
		"src/Picker.vela":  agents[0],
		"src/filter.ts":    "export interface Filter {\n  q: string;\n}\n",
		"src/Counter.vela": find("<!-- Counter.vela -->\n"),
		"src/Parent.vela":  parent,
		"src/List.vela":    find("<!-- List.vela -->\n"),
		"src/format.ts":    find("// src/format.ts\n"),
		"src/Price.vela":   price,
		"src/main.ts":      "import { mount } from '@govela/runtime';\nimport { Parent } from './Parent.vela';\n\nmount(Parent, { target: document.body });\n",
		"vela.ssr.json":    `{"module": "example.com/docs/out/server"}`,
	}
	// The fixture's tsconfig.json is the one of the guide (TS-10.2).
	app := typeCheckApp(t, "clean", "typescript")
	if err := os.RemoveAll(filepath.Join(app, "src")); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		file := filepath.Join(app, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	srcRoot := filepath.Join(app, "src")
	var errOut strings.Builder
	opts := CompileOptions{StatusWriter: io.Discard, ErrWriter: &errOut, SSRModule: "example.com/docs/out/server"}
	if errors := BuildSSRProject(CollectVela(srcRoot), srcRoot, filepath.Join(app, "out"), opts, false); errors != 0 {
		t.Fatalf("vela --ssr: %d error(s)\n%s", errors, errOut.String())
	}
	report, code, stderr := runTypeCheck(t, app)
	for _, d := range report.Diagnostics {
		if strings.HasPrefix(d.Code, "TS") || strings.HasPrefix(d.Code, "VELA-TS") {
			t.Errorf("%s %s:%d:%d %s", d.Code, d.File, d.Line, d.Column, d.Message)
		}
	}
	if code != 0 {
		t.Errorf("vela check --types: exit %d %s", code, stderr)
	}
}
