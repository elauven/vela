package lsp

import (
	"github.com/elauven/vela/runtime"

	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// tsdk is the TypeScript of the repository's test tooling (typescript@6 in
// package.json), installed by npm ci, or VELA_TEST_TSDK; without it or node the JavaScript
// tests skip, unless VELA_REQUIRE_BUNDLERS=1 (CI) makes them fail.
func tsdk(t *testing.T) string {
	t.Helper()
	missing := func(format string, args ...any) {
		if os.Getenv("VELA_REQUIRE_BUNDLERS") == "1" {
			t.Fatalf(format, args...)
		}
		t.Skipf(format, args...)
	}
	if _, err := exec.LookPath("node"); err != nil {
		missing("node not available")
	}
	// VELA_TEST_TSDK runs the tests with another TypeScript.
	lib := os.Getenv("VELA_TEST_TSDK")
	if lib == "" {
		var err error
		if lib, err = filepath.Abs(filepath.Join("..", "..", "node_modules", "typescript", "lib")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(lib, "typescript.js")); err != nil {
		missing("typescript not installed (npm ci): %v", err)
	}
	return lib
}

// startTS starts a server with the TypeScript helper on the fixture and
// opens App.vela with text.
func startTS(t *testing.T, text string) (*client, string, string) {
	t.Helper()
	lib := tsdk(t)
	root := fixture(t)
	c := startClient(t, root)
	c.initializeWith(root, map[string]any{"tsdk": lib})
	uri := pathToURI(filepath.Join(root, "src", "App.vela"))
	c.notify("textDocument/didOpen", map[string]any{"textDocument": TextDocumentItem{URI: uri, LanguageID: "vela", Version: 1, Text: text}})
	return c, root, uri
}

func appSource(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixture(t), "src", "App.vela"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// marked splits a source with one | into the text and the position of |.
func marked(t *testing.T, src string) (string, Position) {
	t.Helper()
	p := at(t, src, "|", 0, 0)
	return strings.Replace(src, "|", "", 1), p
}

func TestTypeScriptCompletion(t *testing.T) {
	src := appSource(t)
	text, inScript := marked(t, strings.Replace(src, "    open = false;", "    open = false;\n    items.|", 1))
	c, _, uri := startTS(t, text)
	items := labels(t, c, uri, inScript)
	for _, want := range []string{"push", "length", "filter"} {
		if _, ok := items[want]; !ok {
			t.Errorf("items.: no %q in %d items", want, len(items))
		}
	}
	if _, ok := items["count"]; ok {
		t.Error("items.: a member completion offers the script's names")
	}
	push := items["push"]
	var resolved CompletionItem
	c.result("completionItem/resolve", push, &resolved)
	if !strings.Contains(resolved.Detail, "push(...items") {
		t.Errorf("resolved push: %+v", resolved)
	}

	// A member of an {#each} item, typed by the array of the $state.deep.
	text, inTemplate := marked(t, strings.Replace(src, ":title={item.title}", ":title={item.|}", 1))
	c.edit(uri, 2, text)
	items = labels(t, c, uri, inTemplate)
	if _, ok := items["title"]; !ok {
		t.Errorf("item.: %v", keys(items))
	}
	if _, ok := items["id"]; !ok {
		t.Errorf("item.: %v", keys(items))
	}

	// Names and globals in an expression, the runes in the script.
	text, inExpr := marked(t, strings.Replace(src, "<p>{count}</p>", "<p>{|}</p>", 1))
	c.edit(uri, 3, text)
	items = labels(t, c, uri, inExpr)
	for _, want := range []string{"count", "items", "Math", "#if"} {
		if _, ok := items[want]; !ok {
			t.Errorf("expression: no %q", want)
		}
	}
	for _, unwanted := range []string{"$$each", "$$render", "$state", "item"} {
		if _, ok := items[unwanted]; ok {
			t.Errorf("expression: unexpected %q", unwanted)
		}
	}
	text, inRunes := marked(t, strings.Replace(src, "  let open", "  $|\n  let open", 1))
	c.edit(uri, 4, text)
	items = labels(t, c, uri, inRunes)
	if item := items["$state"]; item.InsertTextFormat != insertSnippet || items["$derived"].Label == "" {
		t.Errorf("runes: %+v", item)
	}
}

func TestTypeScriptHoverAndDefinition(t *testing.T) {
	src := strings.Replace(appSource(t), "  import { Card } from './Card.vela';", "  import { Card } from './Card.vela';\n  import { format } from './format.js';", 1)
	src = strings.Replace(src, "<p>{count}</p>", `<p>{format(count)}</p>
    <button @click={(e) => e.preventDefault()}>x</button>`, 1)
	c, root, uri := startTS(t, src)
	for _, tc := range []struct {
		name string
		pos  Position
		want []string
	}{
		{"each item", at(t, src, "item.title", 0, 1), []string{"const item: {", "title: string", "each item of items"}},
		{"derived", at(t, src, "(count)", 0, 1), []string{"let count: number", "derived ($derived)"}},
		{"event parameter", at(t, src, "(e) =>", 0, 1), []string{"e: PointerEvent"}},
		{"documented import", at(t, src, "{format(", 0, 2), []string{"format(n: number, unit?: string): string", "Formats a count for display.", "import from './format.js'"}},
		{"component event handler", at(t, src, "close(event)", 0, 7), []string{"event: any"}},
	} {
		got := hoverText(t, c, uri, tc.pos)
		for _, want := range tc.want {
			if !strings.Contains(got, want) {
				t.Errorf("%s: %q misses %q", tc.name, got, want)
			}
		}
	}

	// Into a JavaScript module, and to the declaration of a block name.
	format := location(t, c, uri, at(t, src, "{format(", 0, 2))
	if format.URI != pathToURI(filepath.Join(root, "src", "format.js")) || format.Range.Start.Line != 5 {
		t.Errorf("format: %+v", format)
	}
	item := location(t, c, uri, at(t, src, "item.title", 0, 1))
	if want := at(t, src, "as item", 0, 3); item.URI != uri || item.Range.Start != want {
		t.Errorf("item: %+v, want %+v", item, want)
	}

	var help struct {
		Signatures []struct {
			Label string `json:"label"`
		} `json:"signatures"`
		ActiveParameter int `json:"activeParameter"`
	}
	c.result("textDocument/signatureHelp", params(uri, at(t, src, "{format(count)}", 0, 8)), &help)
	if len(help.Signatures) != 1 || !strings.HasPrefix(help.Signatures[0].Label, "format(n: number") {
		t.Errorf("signature help: %+v", help)
	}
}

func TestTypeScriptReferencesAndRename(t *testing.T) {
	src := appSource(t)
	c, root, uri := startTS(t, src)
	var refs []Location
	c.result("textDocument/references", ReferenceParams{params(uri, at(t, src, "let open", 0, 4))}, &refs)
	var texts []string
	tx := newText(src)
	for _, r := range refs {
		if r.URI != uri {
			t.Errorf("reference in %s", r.URI)
			continue
		}
		texts = append(texts, src[tx.offset(r.Range.Start):tx.offset(r.Range.End)]+"@"+strconv.Itoa(r.Range.Start.Line))
	}
	// The declaration, the write in close() and bind:value in the template.
	if len(refs) != 3 || !strings.Contains(strings.Join(texts, " "), "@15") {
		t.Errorf("references of open: %v", texts)
	}

	var edit struct {
		Changes map[string][]TextEdit `json:"changes"`
	}
	c.result("textDocument/rename", RenameParams{params(uri, at(t, src, "{count}", 0, 1)), "total"}, &edit)
	if edits := edit.Changes[uri]; len(edits) != 2 || len(edit.Changes) != 1 {
		t.Errorf("rename of count: %+v", edit.Changes)
	}

	// A prop is also an attribute name of its parents (task 085): renaming
	// it from the child or from a parent's attribute renames both.
	card := filepath.Join(root, "src", "Card.vela")
	cardURI, cardSrc := c.open(card)
	for _, from := range []struct {
		uri string
		pos Position
	}{{cardURI, at(t, cardSrc, "let title", 0, 4)}, {uri, at(t, src, ":title", 0, 2)}} {
		edit.Changes = nil
		c.result("textDocument/rename", RenameParams{params(from.uri, from.pos), "heading"}, &edit)
		var got []string
		for file, edits := range edit.Changes {
			text := cardSrc
			if file == uri {
				text = src
			}
			ft := newText(text)
			for _, e := range edits {
				got = append(got, filepath.Base(file)+":"+text[ft.offset(e.Range.Start):ft.offset(e.Range.End)]+"→"+e.NewText)
			}
		}
		joined := strings.Join(got, " ")
		// The declaration, {title} of the template, the shorthand of the
		// $$dispatch detail, and :title of App.
		for _, want := range []string{"Card.vela:title→heading", "Card.vela:title→title: heading", "App.vela:title→heading"} {
			if !strings.Contains(joined, want) {
				t.Errorf("rename of the prop from %s misses %s: %s", from.uri, want, joined)
			}
		}
		if len(got) != 4 {
			t.Errorf("rename of the prop: %s", joined)
		}
	}

	// References of the prop: in Card and in App's tag.
	refs = nil
	c.result("textDocument/references", ReferenceParams{params(cardURI, at(t, cardSrc, "let title", 0, 4))}, &refs)
	files := map[string]int{}
	for _, r := range refs {
		files[filepath.Base(r.URI)]++
	}
	if files["Card.vela"] != 3 || files["App.vela"] != 1 {
		t.Errorf("references of the prop: %v", files)
	}
}

// TestTypeScriptDiagnostics: with // @ts-check, TypeScript's semantic
// diagnostics of the script and the template, mapped to the component.
func TestTypeScriptDiagnostics(t *testing.T) {
	src := strings.Replace(appSource(t), "<script>\n", "<script>\n  // @ts-check\n", 1)
	src = strings.Replace(src, "    open = false;", "    open = false;\n    count.toUpperCase();", 1)
	src = strings.Replace(src, "<p>{count}</p>", "<p>{items.size}</p>", 1)
	c, _, uri := startTS(t, src)
	d := c.diagnostics(uri, func(p PublishDiagnosticsParams) bool { return len(p.Diagnostics) > 0 })
	var got []string
	tx := newText(src)
	for _, diag := range d.Diagnostics {
		if diag.Source == "ts" {
			got = append(got, src[tx.offset(diag.Range.Start):tx.offset(diag.Range.End)]+": "+diag.Message)
		}
	}
	joined := strings.Join(got, "\n")
	if len(got) != 2 || !strings.Contains(joined, "toUpperCase: Property 'toUpperCase' does not exist on type 'number'") || !strings.Contains(joined, "size: Property 'size' does not exist") {
		t.Errorf("TypeScript diagnostics:\n%s", joined)
	}

	// Without @ts-check the script is not type checked.
	c.edit(uri, 2, strings.Replace(src, "  // @ts-check\n", "", 1))
	d = c.diagnostics(uri, func(p PublishDiagnosticsParams) bool { return p.Version != nil && *p.Version == 2 })
	for _, diag := range d.Diagnostics {
		if diag.Source == "ts" {
			t.Errorf("unchecked: %+v", diag)
		}
	}
}

// TestTypeScriptMissing: without a TypeScript that has the language
// service API, the server says so and keeps the Vela features.
func TestTypeScriptMissing(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "App.vela"), []byte("<script>let n = $state(1);</script>\n<template><p>{n}</p></template>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A TypeScript 7 package: no language service API.
	fake := filepath.Join(root, "node_modules", "typescript")
	if err := os.MkdirAll(fake, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"package.json": `{"name": "typescript", "version": "7.0.2", "main": "index.js"}`, "index.js": "module.exports = { version: '7.0.2' };"} {
		if err := os.WriteFile(filepath.Join(fake, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("npm_config_prefix", filepath.Join(root, "no-global"))
	c := startClient(t, root)
	c.initializeWith(root, map[string]any{})
	var logged string
	for m := range c.notes {
		if m.Method == "window/logMessage" {
			var p LogMessageParams
			_ = json.Unmarshal(m.Params, &p)
			logged = p.Message
			break
		}
	}
	if !strings.Contains(logged, "JavaScript language features are off") || !strings.Contains(logged, "TypeScript 7.0.2 has no language service API") {
		t.Errorf("log: %q", logged)
	}
	uri, src := c.open(filepath.Join(root, "App.vela"))
	if items := labels(t, c, uri, at(t, src, "{n}", 0, 1)); items["n"].Label == "" {
		t.Errorf("Vela completion without TypeScript: %v", keys(items))
	}
}

// TestTypeScriptDynamicComponent: the :this of <vela:component> is checked
// as a component or nothing (task 070), and an imported component is one.
func TestTypeScriptDynamicComponent(t *testing.T) {
	src := strings.Replace(appSource(t), "<script>\n", "<script>\n  // @ts-check\n", 1)
	src = strings.Replace(src, "<p>{count}</p>", "<p>{count}</p><vela:component :this={open ? Card : null} /><vela:component :this={count} />", 1)
	c, _, uri := startTS(t, src)
	d := c.diagnostics(uri, func(p PublishDiagnosticsParams) bool { return len(p.Diagnostics) > 0 })
	var got []string
	tx := newText(src)
	for _, diag := range d.Diagnostics {
		if diag.Source == "ts" {
			got = append(got, src[tx.offset(diag.Range.Start):tx.offset(diag.Range.End)]+": "+diag.Message)
		}
	}
	if len(got) != 1 || !strings.HasPrefix(got[0], "count: Argument of type 'number' is not assignable") {
		t.Errorf("TypeScript diagnostics:\n%s", strings.Join(got, "\n"))
	}
}

// TestVelaEnvIsTheAmbientTypes: the language server declares the runes and
// the `vela` modules with the ambient types of the runtime package (task
// 082), so the editor and `tsc` with @govela/runtime/ambient agree.
func TestVelaEnvIsTheAmbientTypes(t *testing.T) {
	ambient := runtime.AmbientDeclarations()
	if !strings.HasPrefix(velaEnv, ambient) {
		t.Fatal("velaEnv does not start with runtime.AmbientDeclarations()")
	}
	for _, name := range []string{"declare function $state", "declare module 'vela'", "declare module 'vela/store'", "export function onMount", "export interface Component<"} {
		if !strings.Contains(ambient, name) {
			t.Errorf("the ambient types lack %q", name)
		}
	}
}

// TestTypeScriptComponent: a lang="ts" component is a TypeScript document
// (task 085): its type errors are reported while typing, in the script and
// the template, and hover shows the annotated types.
func TestTypeScriptComponent(t *testing.T) {
	src := `<script lang="ts">
  import { Card } from './Card.vela';

  interface Row { id: number; title: string }

  let rows: Row[] = $state([]);
  let total: number = 'none';
</script>

<template>
  {#each rows as row (row.id)}
    <Card :title={row.id} @on:closed={() => {}} />
  {/each}
</template>
`
	c, root, _ := startTS(t, appSource(t))
	uri := pathToURI(filepath.Join(root, "src", "Typed.vela"))
	c.notify("textDocument/didOpen", map[string]any{"textDocument": TextDocumentItem{URI: uri, LanguageID: "vela", Version: 1, Text: src}})
	d := c.diagnostics(uri, func(p PublishDiagnosticsParams) bool {
		n := 0
		for _, d := range p.Diagnostics {
			if d.Source == "ts" {
				n++
			}
		}
		return n >= 3
	})
	tx := newText(src)
	var got []string
	for _, diag := range d.Diagnostics {
		if diag.Source == "ts" {
			got = append(got, src[tx.offset(diag.Range.Start):tx.offset(diag.Range.End)]+" "+diag.Code)
		}
	}
	joined := strings.Join(got, ", ")
	for _, want := range []string{"total TS2322", "title TS2322", `closed TS2345`} {
		if !strings.Contains(joined, want) {
			t.Errorf("diagnostics %s miss %s", joined, want)
		}
	}
	if h := hoverText(t, c, uri, at(t, src, "row.id}", 0, 1)); !strings.Contains(h, "row: Row") {
		t.Errorf("hover of row: %q", h)
	}
}
