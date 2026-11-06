package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Diagnostics of TypeScript sources (task 081): the codes of
// docs/TYPESCRIPT.md, at the exact place in the TypeScript source.

func compileTSDiags(t *testing.T, source string) []*Diagnostic {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "Comp.vela")
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	diags, _ := CompileFile(src, filepath.Join(dir, "out", "Comp.js"), CompileOptions{})
	return diags
}

// at is "line:column" of a diagnostic.
func at(d *Diagnostic) string { return itoa(d.Line) + ":" + itoa(d.Col) }

func TestTypeScriptDiagnostics(t *testing.T) {
	cases := []struct {
		name, source string
		code, pos    string // pos "line:column", 1-based
		sev          DiagSeverity
		message      string // a part of the message
	}{
		{"unknown lang", "<script lang=\"coffee\">\n  x = 1\n</script>\n<template><p></p></template>",
			"VELA-TS-LANG", "1:15", SevError, `"coffee"`},
		{"TypeScript in a JavaScript script", "<script>\n  export let item: string = 'a';\n</script>\n<template><p>{item}</p></template>",
			"VELA-TS-IN-JS", "2:18", SevError, "TypeScript syntax"},
		{"TypeScript in a JavaScript template", "<script>\n  let x = 1;\n</script>\n<template><p>{x as number}</p></template>",
			"VELA-TEMPLATE-AST", "4:15", SevError, `add lang="ts"`},
		{"enum", "<script lang=\"ts\">\n  let a = 1;\n  enum Color { Red }\n</script>\n<template><p>{a}</p></template>",
			"VELA-TS-NON-ERASABLE", "3:3", SevError, "as const"},
		{"const enum", "<script lang=\"ts\">\n  const enum E { A }\n</script>\n<template><p></p></template>",
			"VELA-TS-NON-ERASABLE", "2:3", SevError, "enums generate code"},
		{"parameter property", "<script lang=\"ts\">\n  class A { constructor(private x: number) {} }\n</script>\n<template><p></p></template>",
			"VELA-TS-NON-ERASABLE", "2:25", SevError, "parameter properties"},
		{"value namespace", "<script lang=\"ts\">\n  namespace N { export const a = 1 }\n</script>\n<template><p></p></template>",
			"VELA-TS-NON-ERASABLE", "2:3", SevError, "namespace"},
		{"angle-bracket assertion", "<script lang=\"ts\">\n  let a: unknown = 1;\n  let b = <number>a;\n</script>\n<template><p>{b}</p></template>",
			"VELA-TS-NON-ERASABLE", "3:11", SevError, "x as T"},
		{"TypeScript syntax error", "<script lang=\"ts\">\n  let a: = 1;\n</script>\n<template><p>{a}</p></template>",
			"VELA-JS-SYNTAX", "2:10", SevError, "type expected"},
		{"value import used as a type", "<script lang=\"ts\">\n  import { Item, makeItem } from './types.ts';\n  export let item: Item = makeItem();\n</script>\n<template><p>{item}</p></template>",
			"VELA-TS-TYPE-IMPORT", "2:12", SevWarning, `"Item"`},
		{"default import used as a type", "<script lang=\"ts\">\n  import Store from './store.ts';\n  export let s: Store | null = null;\n</script>\n<template><p>{s}</p></template>",
			"VELA-TS-TYPE-IMPORT", "2:10", SevWarning, `"Store"`},
		{"bind target with as", "<script lang=\"ts\">\n  let x = $state<string>('');\n</script>\n<template><input bind:value={x as string} /></template>",
			"VELA-TEMPLATE-SYNTAX", "4:30", SevError, "bind:value target"},
		{"annotated each binding", "<script lang=\"ts\">\n  let items = $state<{ id: string }[]>([]);\n</script>\n<template>{#each items as item: { id: string }}<p>{item.id}</p>{/each}</template>",
			"VELA-TEMPLATE-AST", "4:18", SevError, "plain JavaScript identifier"},
		{"angle-bracket assertion in template", "<script lang=\"ts\">\n  let x = $state<unknown>('');\n</script>\n<template><p>{<string>x}</p></template>",
			"VELA-TEMPLATE-AST", "4:15", SevError, "x as T"},
		{"invalid generics", "<script lang=\"ts\" generics=\"T extends\">\n  export let items: T[] = [];\n</script>\n<template><p>{items.length}</p></template>",
			"VELA-TS-GENERICS", "1:38", SevError, "type expected"},
		{"empty generics", "<script lang=\"ts\" generics=\"\">\n  let a = 1;\n</script>\n<template><p>{a}</p></template>",
			"VELA-TS-GENERICS", "1:29", SevError, "empty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			diags := compileTSDiags(t, c.source)
			var found *Diagnostic
			for _, d := range diags {
				if d.Code == c.code {
					found = d
					break
				}
			}
			if found == nil {
				var got []string
				for _, d := range diags {
					got = append(got, d.Code+"@"+at(d)+" "+d.Message)
				}
				t.Fatalf("no %s; got %v", c.code, got)
			}
			if at(found) != c.pos || found.Sev != c.sev || !strings.Contains(found.Message, c.message) {
				t.Errorf("got %s %s at %s: %s; want %s at %s containing %q", found.Sev, found.Code, at(found), found.Message, c.sev, c.pos, c.message)
			}
		})
	}
}

// TestTypeScriptNoFalseTypeImport: a value import used as a value, as a
// component tag, in a typeof query or only in the template is no
// VELA-TS-TYPE-IMPORT.
func TestTypeScriptNoFalseTypeImport(t *testing.T) {
	source := `<script lang="ts">
  import Card from './Card.vela';
  import { Store, helper, config, fmt } from './lib.ts';
  let s: Store = new Store();
  let c: typeof config = config;
  let h: ReturnType<typeof helper>;
</script>
<template><Card :title={fmt(String(s))} /></template>`
	for _, d := range compileTSDiags(t, source) {
		if d.Code == "VELA-TS-TYPE-IMPORT" {
			t.Errorf("unexpected %s at %s: %s", d.Code, at(d), d.Message)
		}
	}
}

// tsProject writes files (path → content) under a new source root.
func tsProject(t *testing.T, files map[string]string) (srcRoot, outRoot string) {
	t.Helper()
	root := t.TempDir()
	srcRoot, outRoot = filepath.Join(root, "src"), filepath.Join(root, "dist")
	for name, content := range files {
		path := filepath.Join(srcRoot, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return srcRoot, outRoot
}

// buildTSProject builds a project and returns the number of errors and its
// machine diagnostics.
func buildTSProject(t *testing.T, srcRoot, outRoot string, opts CompileOptions) (int, []MachineDiagnostic) {
	t.Helper()
	var buf bytes.Buffer
	opts.DiagnosticsJSON = true
	opts.ErrWriter, opts.StatusWriter = &buf, &bytes.Buffer{}
	errors := BuildProject(CollectVela(srcRoot), srcRoot, outRoot, opts, false)
	var diags []MachineDiagnostic
	for _, line := range strings.Split(buf.String(), "\n") {
		var d MachineDiagnostic
		if strings.HasPrefix(line, "{") && json.Unmarshal([]byte(line), &d) == nil {
			diags = append(diags, d)
		}
	}
	return errors, diags
}

func TestTypeScriptModulesInProject(t *testing.T) {
	srcRoot, outRoot := tsProject(t, map[string]string{
		"App.vela": `<script lang="ts">
  import { format } from './lib/format.ts';
  import type { Fmt } from './lib/format.ts';
  const fmt: Fmt = { upper: true };
  const later = (): Promise<unknown> => import('./lib/extra.ts');
  let label = $derived(format('x', fmt));
</script>
<template><p @click={later}>{label}</p></template>`,
		"lib/format.ts": `// Formatting.
export interface Fmt { upper: boolean }
export type Mode = 'a' | 'b';

export function format(s: string, fmt?: Fmt): string {
  return fmt?.upper ? s.toUpperCase() : s;
}
export const loadExtra = () => import('./extra.ts');
`,
		"lib/extra.ts":      "export { extra } from './generic.mts';\n",
		"lib/generic.mts":   "export const extra = <T,>(x: T): T => x;\n",
		"lib/types.d.ts":    "export interface Shape { area(): number }\n",
		"main.ts":           "import App from './App.vela';\nimport { format, type Fmt } from './lib/format.ts';\nconst f: Fmt = { upper: false };\nconsole.log(format('x', f), App);\n",
		"plain.js":          "export const plain = './x.ts';\n",
		"node_modules/x.ts": "export const ignored: number = 1;\n",
	})
	errors, diags := buildTSProject(t, srcRoot, outRoot, CompileOptions{})
	if errors != 0 || len(diags) != 0 {
		t.Fatalf("%d errors, diagnostics %+v", errors, diags)
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(outRoot, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("missing output: %v", err)
		}
		return string(b)
	}
	format := read("lib/format.js")
	for _, want := range []string{
		"export function format(s        , fmt      )         {", // columns kept
		"import('./extra.js')",
	} {
		if !strings.Contains(format, want) {
			t.Errorf("lib/format.js lacks %q:\n%s", want, format)
		}
	}
	if strings.Count(format, "\n") != 8 {
		t.Errorf("lib/format.js changed its number of lines:\n%s", format)
	}
	if strings.Contains(format, "interface") || strings.Contains(format, ": string") {
		t.Errorf("types left in lib/format.js:\n%s", format)
	}
	if got := read("lib/extra.js"); !strings.Contains(got, "from './generic.js'") {
		t.Errorf("lib/extra.js: %s", got) // .mts compiles to .js too (TS-5.1)
	}
	if got := read("lib/generic.js"); !strings.Contains(got, "export const extra =     (x   )    => x;") {
		t.Errorf("lib/generic.js: %q", got)
	}
	if got := read("main.js"); !strings.Contains(got, "import App from './App.js';") || !strings.Contains(got, "from './lib/format.js'") || strings.Contains(got, ": Fmt") {
		t.Errorf("main.js:\n%s", got)
	}
	if got := read("plain.js"); got != "export const plain = './x.ts';\n" {
		t.Errorf("a string that is no specifier changed: %s", got)
	}
	app := read("App.js")
	for _, want := range []string{"from './lib/format.js'", "import('./lib/extra.js')"} {
		if !strings.Contains(app, want) {
			t.Errorf("App.js lacks %q", want)
		}
	}
	for _, absent := range []string{"lib/types.d.js", "lib/types.d.ts", "node_modules/x.js"} {
		if _, err := os.Stat(filepath.Join(outRoot, absent)); err == nil {
			t.Errorf("%s was written", absent)
		}
	}
	if node, err := exec.LookPath("node"); err == nil {
		for _, name := range []string{"lib/format.js", "main.js", "App.js"} {
			check := filepath.Join(t.TempDir(), "check.mjs")
			_ = os.WriteFile(check, []byte(read(name)), 0o644)
			if out, err := exec.Command(node, "--check", check).CombinedOutput(); err != nil {
				t.Errorf("%s does not parse in node: %v\n%s", name, err, out)
			}
		}
	}

	// With the bundler plugin, .ts specifiers are the bundler's (TS-5.4).
	bundled := filepath.Join(filepath.Dir(outRoot), "bundled")
	if errors, diags := buildTSProject(t, srcRoot, bundled, CompileOptions{SourceComponentImports: true, BareRuntimeImports: true}); errors != 0 {
		t.Fatalf("bundler build: %d errors %+v", errors, diags)
	}
	bundledApp, _ := os.ReadFile(filepath.Join(bundled, "App.js"))
	if !strings.Contains(string(bundledApp), "from './lib/format.ts'") || !strings.Contains(string(bundledApp), "import('./lib/extra.ts')") {
		t.Errorf("bundler build rewrote .ts specifiers:\n%s", bundledApp)
	}
}

func TestTypeScriptModuleDiagnostics(t *testing.T) {
	srcRoot, outRoot := tsProject(t, map[string]string{
		"App.vela":      "<script lang=\"ts\">\n  let a = 1;\n</script>\n<template><p>{a}</p></template>",
		"enum.ts":       "export const a = 1;\nexport enum E { A }\n",
		"reexport.ts":   "interface Props { a: number }\nconst value = 1;\nexport { Props, value };\n",
		"typeimport.ts": "import { Shape } from './shapes.ts';\nexport let s: Shape | null = null;\n",
		"view.tsx":      "export const v = 1;\n",
		"legacy.cts":    "export const c = 1;\n",
		"broken.ts":     "export const b: = 1;\n",
		"shapes.ts":     "export interface Shape { area(): number }\n",
		"types.d.cts":   "export {};\n",
		"jsx/Comp.jsx":  "export const j = 1;\n",
		"twice.js":      "export const t = 1;\n",
		"twice.ts":      "export const t: number = 1;\n",
		"typed.js":      "export const n = 1;\nexport function f(a: number) { return a }\n",
		"loose.js":      "export const broken = ;\n",
	})
	errors, diags := buildTSProject(t, srcRoot, outRoot, CompileOptions{})
	want := map[string]string{ // file → code@line:col
		"enum.ts":       "VELA-TS-NON-ERASABLE@2:8",
		"reexport.ts":   "VELA-TS-TYPE-EXPORT@3:10",
		"typeimport.ts": "VELA-TS-TYPE-IMPORT@1:10",
		"view.tsx":      "VELA-TS-MODULE@1:1",
		"legacy.cts":    "VELA-TS-MODULE@1:1",
		"Comp.jsx":      "VELA-TS-MODULE@1:1",
		"broken.ts":     "VELA-JS-SYNTAX@1:17",
		"twice.ts":      "VELA-TS-MODULE@1:1",
		"typed.js":      "VELA-TS-IN-JS@2:20",
	}
	got := map[string]string{}
	for _, d := range diags {
		got[filepath.Base(d.File)] = d.Code + "@" + itoa(d.Line) + ":" + itoa(d.Column)
	}
	for file, w := range want {
		if got[file] != w {
			t.Errorf("%s: got %q, want %q", file, got[file], w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("diagnostics for %v, want only %v", got, want)
	}
	// The warning of typeimport.ts is no error; loose.js is copied as
	// JavaScript modules always were.
	if errors != 8 {
		t.Errorf("%d errors, want 8", errors)
	}
	for _, name := range []string{"enum.js", "reexport.js", "broken.js"} {
		if _, err := os.Stat(filepath.Join(outRoot, name)); err == nil {
			t.Errorf("%s was written despite errors", name)
		}
	}
	if _, err := os.Stat(filepath.Join(outRoot, "typeimport.js")); err != nil {
		t.Errorf("typeimport.js (warning only) was not written: %v", err)
	}
}

// TestTypeScriptCheck: `vela check` (without --types) reports the syntax
// diagnostics of TypeScript components and modules (task 081).
func TestTypeScriptCheck(t *testing.T) {
	srcRoot, _ := tsProject(t, map[string]string{
		"App.vela": "<script lang=\"ts\">\n  enum E { A }\n</script>\n<template><p></p></template>",
		"util.ts":  "interface P { a: number }\nexport { P };\n",
		"ok.ts":    "export const ok: number = 1;\n",
	})
	diags, checked, err := CheckTarget{SrcRoot: srcRoot}.Run()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, d := range diags {
		got[filepath.Base(d.File)] = d.Code + "@" + at(d)
	}
	want := map[string]string{"App.vela": "VELA-TS-NON-ERASABLE@2:3", "util.ts": "VELA-TS-TYPE-EXPORT@2:10"}
	if checked != 1 || len(got) != len(want) || got["App.vela"] != want["App.vela"] || got["util.ts"] != want["util.ts"] {
		t.Errorf("checked %d, diagnostics %v, want %v", checked, got, want)
	}
}
