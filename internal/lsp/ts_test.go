package lsp

import (
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

	// A prop is also an attribute name of its parents.
	card := filepath.Join(root, "src", "Card.vela")
	cardURI, cardSrc := c.open(card)
	m := c.call("textDocument/rename", RenameParams{params(cardURI, at(t, cardSrc, "let title", 0, 4)), "heading"})
	if m.Error == nil || !strings.Contains(m.Error.Message, "prop") {
		t.Errorf("rename of a prop: %+v", m)
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
