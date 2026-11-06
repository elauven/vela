package lsp

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestEnumAsConst(t *testing.T) {
	cases := []struct{ src, want string }{
		{"enum Color { Red, Green = 4, Blue }", "const Color = {\n  Red: 0,\n  Green: 4,\n  Blue: 5,\n} as const;\ntype Color = (typeof Color)[keyof typeof Color];"},
		{"export const enum Mode { A = 'a', B = 'b', }", "export const Mode = {\n  A: 'a',\n  B: 'b',\n} as const;\nexport type Mode = (typeof Mode)[keyof typeof Mode];"},
	}
	for _, c := range cases {
		off := strings.Index(c.src, "enum")
		if strings.Contains(c.src, "const enum") {
			off = strings.Index(c.src, "const enum")
		}
		start, end, text, ok := enumAsConst(c.src, off)
		if !ok || text != c.want || c.src[:start]+text+c.src[end:] != c.want {
			t.Errorf("%s:\n%s (%d..%d, %v)", c.src, text, start, end, ok)
		}
	}
	// An automatic value after a string one is an error TypeScript reports.
	if _, _, _, ok := enumAsConst("enum E { A = 'a', B }", 0); ok {
		t.Error("rewrote an enum TypeScript rejects")
	}
}

func TestTypeImportInsertion(t *testing.T) {
	cases := []struct{ src, name, want string }{
		{"import { Props, run } from './a.ts';", "Props", "import { type Props, run } from './a.ts';"},
		{"import { run, Item as Row } from './a.ts';", "Row", "import { run, type Item as Row } from './a.ts';"},
		{"import Config from './c.ts';", "Config", "import type Config from './c.ts';"},
		{"import * as T from './t.ts';", "T", "import type * as T from './t.ts';"},
	}
	for _, c := range cases {
		off := strings.LastIndex(c.src, c.name)
		at, ok := typeImportInsertion(c.src, off)
		if got := c.src[:at] + "type " + c.src[at:]; !ok || got != c.want {
			t.Errorf("%s: %s (%v)", c.src, got, ok)
		}
	}
	if _, ok := typeImportInsertion("import Def, { x } from './a.ts';", len("import ")); ok {
		t.Error("import type of a default with named bindings")
	}
}

// TestCodeActions: the quick fixes of the server for the diagnostics of a
// TypeScript component.
func TestCodeActions(t *testing.T) {
	root := fixture(t)
	c := startClient(t, root)
	c.initializeWith(root, map[string]any{"enable": false})
	fix := func(name, src string) string {
		t.Helper()
		uri := pathToURI(filepath.Join(root, "src", name))
		c.notify("textDocument/didOpen", map[string]any{"textDocument": TextDocumentItem{URI: uri, LanguageID: "vela", Version: 1, Text: src}})
		d := c.diagnostics(uri, func(p PublishDiagnosticsParams) bool { return len(p.Diagnostics) > 0 })
		var actions []CodeAction
		c.result("textDocument/codeAction", CodeActionParams{TextDocument: TextDocumentIdentifier{URI: uri}, Context: struct {
			Diagnostics []Diagnostic `json:"diagnostics"`
		}{d.Diagnostics}}, &actions)
		if len(actions) != 1 {
			t.Fatalf("%s: actions %+v for %+v", name, actions, d.Diagnostics)
		}
		tx := newText(src)
		e := actions[0].Edit["changes"][uri][0]
		return src[:tx.offset(e.Range.Start)] + e.NewText + src[tx.offset(e.Range.End):]
	}
	got := fix("Typed.vela", "<script lang=\"ts\">\n  import { Props } from './types.ts';\n  export let props: Props | null = null;\n</script>\n<template><p>{props}</p></template>\n")
	if !strings.Contains(got, "import { type Props } from './types.ts';") {
		t.Errorf("type import:\n%s", got)
	}
	got = fix("Sized.vela", "<script lang=\"ts\">\n  enum Size { Small, Large }\n  export let size: Size = Size.Small;\n</script>\n<template><p>{size}</p></template>\n")
	if !strings.Contains(got, "  const Size = {\n    Small: 0,\n    Large: 1,\n  } as const;\n  type Size = (typeof Size)[keyof typeof Size];\n  export let size") {
		t.Errorf("enum:\n%s", got)
	}
}
