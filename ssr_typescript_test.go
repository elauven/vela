package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TypeScript twins of the SSR corpus (task 086, docs/TYPESCRIPT.md TS-7.1,
// TS-7.2): every component of the block corpus as a lang="ts" component,
// with the props whose default is a literal annotated, and its .js module
// as a .ts one (still imported as ./loader.js, TS-5.3). Both projects are
// built with --ssr, rendered by their Go renderers with the cases of the
// corpus, and compared: the HTML with its payload, the static HTML, and the
// client modules (but for the site table, which holds source columns) are
// byte-identical, so hydration is the JavaScript corpus's.

var ssrTwinLiteralProp = regexp.MustCompile(`(?m)^(\s*export let \w+)( = (?:\$bindable\()?('[^']*'|-?[0-9][0-9.e+-]*|true|false)\)?;)`)

// ssrTypeScriptTwin turns a corpus component into its TypeScript twin.
func ssrTypeScriptTwin(src string) string {
	src = strings.Replace(src, "<script>", `<script lang="ts">`, 1)
	src = strings.Replace(src, "<script context=\"client\">", `<script context="client" lang="ts">`, 1)
	return ssrTwinLiteralProp.ReplaceAllStringFunc(src, func(decl string) string {
		m := ssrTwinLiteralProp.FindStringSubmatch(decl)
		typ := "number"
		switch {
		case strings.HasPrefix(m[3], "'"):
			typ = "string"
		case m[3] == "true" || m[3] == "false":
			typ = "boolean"
		}
		return m[1] + ": " + typ + m[2]
	})
}

func TestSSRTypeScriptTwins(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not available")
	}
	files := map[string]string{}
	for _, pattern := range []string{"testdata/engine_diff/*.vela", "testdata/ssr_blocks/*.vela", "testdata/ssr_blocks/*.js"} {
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
	twins := map[string]string{}
	annotated := 0
	for name, src := range files {
		switch filepath.Ext(name) {
		case ".js":
			twins[strings.TrimSuffix(name, ".js")+".ts"] = strings.Replace(src, "export async function load(id)", "export async function load(id: number): Promise<{ id: number; label: string }>", 1)
		default:
			twin := ssrTypeScriptTwin(src)
			annotated += strings.Count(twin, ": string") + strings.Count(twin, ": number") + strings.Count(twin, ": boolean")
			twins[name] = twin
		}
	}
	if annotated < 20 || !strings.Contains(twins["loader.ts"], "id: number") {
		t.Fatalf("the twins annotate %d props", annotated)
	}
	var cases []ssrBlocksCase
	data, err := os.ReadFile(filepath.Join("testdata", "ssr_blocks", "cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}

	// Both builds use one path: the CSS scope of a component is derived
	// from it.
	root := t.TempDir()
	build := func(files map[string]string) (string, map[string][2]string) {
		for _, dir := range []string{"src", "out"} {
			if err := os.RemoveAll(filepath.Join(root, dir)); err != nil {
				t.Fatal(err)
			}
		}
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
		opts := CompileOptions{StatusWriter: io.Discard, SSRModule: "example.com/ssrblocks/server"}
		if errors := BuildSSRProject(CollectVela(srcRoot), srcRoot, outRoot, opts, false); errors != 0 {
			t.Fatalf("SSR project errors=%d", errors)
		}
		return outRoot, renderSSRBlocksCases(t, outRoot, "example.com/ssrblocks", cases)
	}
	jsOut, jsHTML := build(files)
	jsClient := t.TempDir()
	if err := os.CopyFS(jsClient, os.DirFS(filepath.Join(jsOut, "client"))); err != nil {
		t.Fatal(err)
	}
	tsOut, tsHTML := build(twins)
	for _, c := range cases {
		if jsHTML[c.Name] != tsHTML[c.Name] {
			t.Errorf("%s: the TypeScript twin renders\n%s\nnot\n%s", c.Name, tsHTML[c.Name], jsHTML[c.Name])
		}
	}
	sites := regexp.MustCompile(`(?s)\$\$sites = \[.*?\];`)
	clients, err := filepath.Glob(filepath.Join(jsClient, "*.js"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range clients {
		js, _ := os.ReadFile(file)
		ts, err := os.ReadFile(filepath.Join(tsOut, "client", filepath.Base(file)))
		if err != nil {
			t.Errorf("the TypeScript build has no %s", filepath.Base(file))
			continue
		}
		if a, b := sites.ReplaceAllString(string(js), ""), sites.ReplaceAllString(string(ts), ""); a != b && filepath.Base(file) != "loader.js" {
			t.Errorf("%s differs:\n%s", filepath.Base(file), ts)
		}
	}
}
