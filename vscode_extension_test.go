package main

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
)

const vscodeExtensionDir = "packages/vscode-vela"

// grammarTokens tokenizes files with the extension's TextMate grammar
// (tools/vela-grammar-check.mjs): per file, the tokens as text and scopes.
func grammarTokens(t *testing.T, files ...string) map[string][][2]string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		skipBundlers(t, "node not available")
	}
	if _, err := os.Stat(filepath.Join("node_modules", "vscode-textmate")); err != nil {
		skipBundlers(t, "vscode-textmate not installed (npm ci)")
	}
	out, err := exec.Command(node, append([]string{"tools/vela-grammar-check.mjs"}, files...)...).Output()
	if err != nil {
		t.Fatalf("grammar check: %v", err)
	}
	var res struct {
		Files map[string][][2]string `json:"files"`
		Error string                 `json:"error"`
	}
	if err := json.Unmarshal(out, &res); err != nil || res.Error != "" {
		t.Fatalf("grammar check: %v %s", err, res.Error)
	}
	return res.Files
}

// TestVelaGrammar tokenizes components with the grammar of the VS Code
// extension, as VS Code does (vscode-textmate, oniguruma): the sections go
// to the JavaScript and CSS grammars, directives, components, blocks and
// expressions get their scopes, and every example's template ends where the
// component's does.
func TestVelaGrammar(t *testing.T) {
	sample := filepath.Join(t.TempDir(), "Sample.vela")
	writeTestFile(t, sample, `<script context="client">
  import { Card } from './Card.vela';
  let count = $state(0);
</script>

<template>
  <!-- {not an expression} -->
  {#each items as item (item.id)}
    <Card :title={item.title} @on:close={() => count++} bind:value={count} />
  {:else}
    <p class:empty={true} :data-n="count + 1" title="a {b}">&amp; {count}</p>
  {/each}
</template>

<style>
  p { color: red; }
</style>
`)
	files := []string{sample}
	examples, err := filepath.Glob(filepath.Join("examples", "*.vela"))
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, examples...)
	files = append(files, CollectVela(filepath.Join("examples", "dashboard"))...)
	tokens := grammarTokens(t, files...)

	has := func(text, scope string) bool {
		for _, tok := range tokens[sample] {
			if strings.TrimSpace(tok[0]) == text && strings.Contains(tok[1], scope) {
				return true
			}
		}
		return false
	}
	for _, want := range [][2]string{
		{"import", "meta.section.script.vela source.js"},
		{"script", "entity.name.tag.script.html"},
		{"context", "entity.other.attribute-name.html"},
		{"\"client\"", "string.quoted.html"},
		{"each", "keyword.control.vela"},
		{"as", "keyword.control.vela"},
		{"item", "meta.block.vela meta.embedded.expression.vela source.js"},
		{"Card", "support.class.component.vela"},
		{":", "keyword.other.directive.vela"},
		{"title", "entity.other.attribute-name.html"},
		{"on:close", "entity.other.attribute-name.html"},
		{"bind:", "keyword.other.directive.vela"},
		{"class:", "keyword.other.directive.vela"},
		{"data-n", "entity.other.attribute-name.html"},
		{"count", "meta.interpolation.vela meta.embedded.expression.vela source.js"},
		{"&amp;", "constant.character.entity.html"},
		{"\"a {b}\"", "string.quoted.html"},
		{"{not an expression}", "comment.block.html"},
		{"p", "entity.name.tag.html"},
		{"color", "meta.section.style.vela source.css"},
	} {
		if !has(want[0], want[1]) {
			t.Errorf("no %q with %s in\n%v", want[0], want[1], tokens[sample])
		}
	}
	for _, file := range files[1:] {
		toks := tokens[file]
		end := -1
		for i, tok := range toks {
			if tok[0] == "template" && strings.HasSuffix(tok[1], "entity.name.tag.template.html") {
				end = i
			}
		}
		if end < 0 {
			t.Errorf("%s: no <template> tag", file)
			continue
		}
		// After </template> the grammar is back at the top level.
		for _, tok := range toks[end+1:] {
			if strings.Contains(tok[1], "meta.template.vela") {
				t.Errorf("%s: the template does not end at </template>: %q %s", file, tok[0], tok[1])
				break
			}
		}
	}
}

// TestVSCodeExtension packages the extension with vsce (npm ci in
// packages/vscode-vela) and runs its smoke test: the .vsix holds the
// client, the grammar and TypeScript 6 but not the build tools, and
// vela-lsp, found in a workspace's node_modules as the extension finds it,
// answers a member completion with the TypeScript of the .vsix.
func TestVSCodeExtension(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		skipBundlers(t, "node not available")
	}
	vsce := filepath.Join(vscodeExtensionDir, "node_modules", "@vscode", "vsce", "vsce")
	if _, err := os.Stat(vsce); err != nil {
		skipBundlers(t, "vsce not installed (npm ci in %s)", vscodeExtensionDir)
	}
	root := t.TempDir()
	vsix := filepath.Join(root, "vela.vsix")
	pack := exec.Command(node, filepath.Join("node_modules", "@vscode", "vsce", "vsce"), "package", "--out", vsix)
	pack.Dir = vscodeExtensionDir
	if out, err := pack.CombinedOutput(); err != nil {
		t.Fatalf("vsce package: %v\n%s", err, out)
	}

	// The .vsix is a zip of extension/…
	ext := filepath.Join(root, "unpacked")
	archive, err := zip.OpenReader(vsix)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range archive.File {
		names[f.Name] = true
		rel, ok := strings.CutPrefix(f.Name, "extension/")
		if !ok {
			continue
		}
		dest := filepath.Join(ext, filepath.FromSlash(rel))
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, dest, string(data))
	}
	archive.Close()
	for _, want := range []string{"package.json", "extension.js", "server.js", "language-configuration.json", "syntaxes/vela.tmLanguage.json", "readme.md", "LICENSE.txt", "changelog.md", "node_modules/vscode-languageclient/package.json", "node_modules/typescript/lib/typescript.js", "node_modules/typescript/lib/lib.dom.d.ts"} {
		if !names["extension/"+want] {
			t.Errorf("the .vsix misses %s", want)
		}
	}
	for name := range names {
		for _, unwanted := range []string{"node_modules/@vscode/vsce/", "node_modules/typescript/lib/tsserver.js", "package-lock.json"} {
			if strings.Contains(name, unwanted) {
				t.Errorf("the .vsix holds %s", name)
			}
		}
	}

	// A workspace with vela-lsp where @govela/compiler installs it, and no
	// TypeScript: the extension's is the one to use.
	workspace := filepath.Join(root, "workspace")
	copyTree(t, filepath.Join("testdata", "lsp", "app"), workspace)
	platform := "compiler-" + map[string]string{"windows": "win32"}[goruntime.GOOS]
	if platform == "compiler-" {
		platform += goruntime.GOOS
	}
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[goruntime.GOARCH]
	binary := filepath.Join(workspace, "node_modules", "@govela", platform+"-"+arch, "bin", "vela-lsp")
	if goruntime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "./cmd/vela-lsp")
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	out, err := exec.Command(node, "tools/vscode-extension-check.mjs", ext, workspace).Output()
	if err != nil {
		t.Fatalf("extension check: %v", err)
	}
	var res struct {
		Server     string   `json:"server"`
		TypeScript string   `json:"typescript"`
		Completion []string `json:"completion"`
		Exit       *int     `json:"exit"`
		Error      string   `json:"error"`
	}
	if err := json.Unmarshal(out, &res); err != nil || res.Error != "" {
		t.Fatalf("extension check: %v %s\n%s", err, res.Error, out)
	}
	if res.Server != binary {
		t.Errorf("the extension found %s, want %s", res.Server, binary)
	}
	if !strings.Contains(res.TypeScript, "TypeScript 6.") {
		t.Errorf("TypeScript: %q", res.TypeScript)
	}
	if !strings.Contains(strings.Join(res.Completion, " "), "push") {
		t.Errorf("items.: %v", res.Completion)
	}
	if res.Exit == nil || *res.Exit != 0 {
		t.Errorf("exit code %v", res.Exit)
	}
}
