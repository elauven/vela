package main

import (
	"github.com/elauven/vela/internal/typecheck"

	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"
)

type eslintResult struct {
	FilePath string `json:"filePath"`
	Messages []struct {
		RuleID    string `json:"ruleId"`
		Message   string `json:"message"`
		Line      int    `json:"line"`
		Column    int    `json:"column"`
		EndLine   int    `json:"endLine"`
		EndColumn int    `json:"endColumn"`
		Fatal     bool   `json:"fatal"`
	} `json:"messages"`
	Output *string `json:"output"`
}

// newESLintProject is a temporary project with ESLint, @eslint/js and the
// packed @govela/compiler and @govela/eslint-plugin in node_modules, and a
// vela binary of this repository.
func newESLintProject(t *testing.T, config string) (root, vela string) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		skipBundlers(t, "node not available")
	}
	if _, err := exec.LookPath("npm"); err != nil {
		skipBundlers(t, "npm not available")
	}
	for _, dep := range []string{"eslint", "@eslint/js"} {
		if _, err := os.Stat(filepath.Join("node_modules", filepath.FromSlash(dep), "package.json")); err != nil {
			skipBundlers(t, "%s not installed; run npm ci", dep)
		}
	}
	compilerRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root = t.TempDir()
	vela = filepath.Join(t.TempDir(), "vela")
	if goruntime.GOOS == "windows" {
		vela += ".exe"
	}
	build := exec.Command("go", "build", "-o", vela, ".")
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	modules := filepath.Join(root, "node_modules")
	for dir, name := range map[string]string{compilerPackageDir: CompilerPackageName, eslintPluginPackageDir: ESLintPluginPackageName} {
		copyTree(t, filepath.FromSlash(dir), filepath.Join(modules, filepath.FromSlash(name)))
	}
	for _, dep := range []string{"eslint", "@eslint", "@typescript-eslint", "typescript-eslint", "typescript"} {
		target := filepath.Join(compilerRoot, "node_modules", filepath.FromSlash(dep))
		if _, err := os.Stat(target); err != nil {
			continue
		}
		if err := os.Symlink(target, filepath.Join(modules, filepath.FromSlash(dep))); err != nil {
			skipBundlers(t, "symlink: %v", err)
		}
	}
	writeTestFile(t, filepath.Join(root, "package.json"), `{"private": true, "type": "module"}`)
	writeTestFile(t, filepath.Join(root, "eslint.config.js"), config)
	return root, vela
}

// runESLint lints the files of root with extra arguments and returns the
// results by file name relative to root.
func runESLint(t *testing.T, root, vela string, args ...string) map[string]eslintResult {
	t.Helper()
	cmd := exec.Command("node", append([]string{filepath.Join("node_modules", "eslint", "bin", "eslint.js"), "--format", "json"}, args...)...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "VELA_BINARY="+vela)
	out, err := cmd.Output()
	if exit, ok := err.(*exec.ExitError); err != nil && (!ok || exit.ExitCode() != 1) {
		t.Fatalf("eslint: %v\n%s\n%s", err, out, exit.Stderr)
	}
	var results []eslintResult
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatalf("eslint output: %v\n%s", err, out)
	}
	byFile := map[string]eslintResult{}
	for _, r := range results {
		rel, _ := filepath.Rel(root, r.FilePath)
		byFile[filepath.ToSlash(rel)] = r
	}
	return byFile
}

// sourceAt is the text of src from the 1-based line and column (UTF-16
// units, as ESLint counts; the test sources are ASCII).
func sourceAt(src string, line, column int) string {
	lines := strings.Split(src, "\n")
	if line < 1 || line > len(lines) || column < 1 || column-1 > len(lines[line-1]) {
		return ""
	}
	return lines[line-1][column-1:]
}

const eslintTestConfig = `import js from '@eslint/js';
import vela from '@govela/eslint-plugin';

export default [
  js.configs.recommended,
  ...vela.configs.recommended,
  {
    files: ['**/*.vela/*.js'],
    languageOptions: { globals: { document: 'readonly', window: 'readonly', setInterval: 'readonly', clearInterval: 'readonly', setTimeout: 'readonly', clearTimeout: 'readonly', console: 'readonly', localStorage: 'readonly', fetch: 'readonly' } },
    rules: { 'prefer-const': 'error' },
  },
];
`

// TestESLintPlugin (task 067): the processor lints the script and the
// template expressions of components and reports at their places in the
// .vela files; nothing is reported about the generated text.
func TestESLintPlugin(t *testing.T) {
	root, vela := newESLintProject(t, eslintTestConfig)
	examples, err := filepath.Glob(filepath.Join("examples", "*.vela"))
	if err != nil || len(examples) == 0 {
		t.Fatalf("examples: %v", err)
	}
	sources := map[string]string{}
	for _, file := range examples {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		sources["src/"+filepath.Base(file)] = string(data)
	}
	sources["src/Bad.vela"] = `<script>
  let unused = $state(0);
  let shown = $state(1);
  let items = $state([]);
  let fixed = 2;
  $effect(() => console.log(fixed));
</script>

<template>
  <p :title={shown}>{shown} {missing}</p>
  {#each items as item, i (item.id)}
    <span @click={() => item.go(nope)}>{item.name}</span>
  {/each}
  {#await items then value}
    <i>{value}</i>
  {/await}
</template>
`
	for name, src := range sources {
		writeTestFile(t, filepath.Join(root, filepath.FromSlash(name)), src)
	}

	results := runESLint(t, root, vela, "src")
	quoted := regexp.MustCompile(`^'([^']+)'`)
	for name, src := range sources {
		r, ok := results[name]
		if !ok {
			t.Errorf("%s was not linted", name)
			continue
		}
		for _, m := range r.Messages {
			if m.Fatal {
				t.Errorf("%s:%d:%d: parse error %s", name, m.Line, m.Column, m.Message)
				continue
			}
			// The name a message is about is at its position in the source.
			if q := quoted.FindStringSubmatch(m.Message); q != nil && !strings.HasPrefix(sourceAt(src, m.Line, m.Column), q[1]) {
				t.Errorf("%s:%d:%d: %s (%s) is not at %q", name, m.Line, m.Column, m.Message, m.RuleID, q[1])
			}
			if strings.Contains(m.Message, "$$") {
				t.Errorf("%s:%d:%d: a message about generated text: %s", name, m.Line, m.Column, m.Message)
			}
		}
	}

	var got []string
	for _, m := range results["src/Bad.vela"].Messages {
		got = append(got, strings.Join([]string{m.RuleID, quoted.FindStringSubmatch(m.Message)[1], strconv.Itoa(m.Line), strconv.Itoa(m.Column)}, " "))
	}
	sort.Strings(got)
	// prefer-const also applies to runes: `const items = $state([])` is
	// a component the compiler accepts (and hints at).
	want := []string{
		"no-undef missing 10 30",
		"no-undef nope 12 33",
		"no-unused-vars i 11 25",
		"no-unused-vars unused 2 7",
		"prefer-const fixed 5 7",
		"prefer-const items 4 7",
		"prefer-const shown 3 7",
		"prefer-const unused 2 7",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("Bad.vela:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// A fix inside the component's own text applies.
	fixed := runESLint(t, root, vela, "--fix-dry-run", "src/Bad.vela")["src/Bad.vela"]
	if fixed.Output == nil || !strings.Contains(*fixed.Output, "const fixed = 2;") || !strings.Contains(*fixed.Output, "{#each items as item, i (item.id)}") {
		t.Errorf("--fix output: %v", fixed.Output)
		if fixed.Output != nil {
			t.Log(*fixed.Output)
		}
	}
}

// TestDocumentCommand: `vela document` prints the virtual document with
// segments in UTF-16 units that map copied text back to the component.
func TestDocumentCommand(t *testing.T) {
	src := "<script>\n  // é😀\n  let name = $state('x');\n</script>\n<template><p>{name}</p></template>\n"
	path := filepath.Join(t.TempDir(), "A.vela")
	writeTestFile(t, path, src)
	var stdout, stderr strings.Builder
	if code := runDocumentCommand([]string{"--path", path}, nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	var doc DocumentResult
	if err := json.Unmarshal([]byte(stdout.String()), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Path != typecheck.VirtualPath(path, false) || doc.TypeScript {
		t.Errorf("path %s, typescript %v", doc.Path, doc.TypeScript)
	}
	source, text := utf16.Encode([]rune(src)), utf16.Encode([]rune(doc.Text))
	names := 0
	for _, s := range doc.Segments {
		a, b := string(utf16.Decode(source[s[0]:s[0]+s[2]])), string(utf16.Decode(text[s[1]:s[1]+s[2]]))
		if a != b {
			t.Errorf("segment %v: %q in the component, %q in the document", s, a, b)
		}
		if a == "name" {
			names++
		}
	}
	if names == 0 || len(doc.Segments) < 2 {
		t.Errorf("segments %v of\n%s", doc.Segments, doc.Text)
	}
}

const eslintTypeScriptConfig = `import { defineConfig } from 'eslint/config';
import js from '@eslint/js';
import tseslint from 'typescript-eslint';
import vela from '@govela/eslint-plugin';

export default defineConfig(
  js.configs.recommended,
  tseslint.configs.recommendedTypeChecked,
  { languageOptions: { parserOptions: { projectService: true, tsconfigRootDir: import.meta.dirname } } },
  vela.configs.recommended,
  // The virtual files of components are not on disk: no type information.
  { files: ['**/*.vela/*.ts', '**/*.vela/*.js', '*.config.js'], extends: [tseslint.configs.disableTypeChecked] },
);
`

// TestESLintPluginTypeScript (task 088): a TypeScript component is linted
// by typescript-eslint as a .ts virtual file, its template expressions
// included; type-aware rules run on the .ts modules, and are disabled for
// the components.
func TestESLintPluginTypeScript(t *testing.T) {
	if _, err := os.Stat(filepath.Join("node_modules", "typescript-eslint", "package.json")); err != nil {
		skipBundlers(t, "typescript-eslint not installed; run npm ci")
	}
	root, vela := newESLintProject(t, eslintTypeScriptConfig)
	component := `<script lang="ts">
  interface Item { id: string; label: string }

  export let items: Item[] = [];
  let unused: number = $state(0);
  let selected = $state<Item | null>(null);

  function pick(item: Item): void {
    selected = item;
  }
</script>

<template>
  {#each items as item (item.id)}
    <button @click={() => pick(item as any)}>{item.label}</button>
  {/each}
  <p>{selected?.label}</p>
</template>
`
	writeTestFile(t, filepath.Join(root, "src", "List.vela"), component)
	// A floating promise: a type-aware rule, reported in the .ts module.
	writeTestFile(t, filepath.Join(root, "src", "util.ts"), "export async function load(): Promise<number> {\n  return 1;\n}\n\nload();\n")
	writeTestFile(t, filepath.Join(root, "tsconfig.json"), `{"compilerOptions": {"strict": true, "module": "ESNext", "moduleResolution": "bundler", "target": "ES2022", "noEmit": true}, "include": ["src"]}`)

	results := runESLint(t, root, vela, "src")
	var got []string
	for _, name := range []string{"src/List.vela", "src/util.ts"} {
		for _, m := range results[name].Messages {
			if m.Fatal {
				t.Errorf("%s:%d:%d: parse error %s", name, m.Line, m.Column, m.Message)
			}
			got = append(got, name+" "+m.RuleID+" "+strconv.Itoa(m.Line)+":"+strconv.Itoa(m.Column))
		}
	}
	sort.Strings(got)
	want := []string{
		"src/List.vela @typescript-eslint/no-explicit-any 15:40",
		"src/List.vela @typescript-eslint/no-unused-vars 5:7",
		"src/List.vela prefer-const 5:7",
		"src/util.ts @typescript-eslint/no-floating-promises 5:1",
		"src/util.ts @typescript-eslint/require-await 1:8",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("messages:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !strings.HasPrefix(sourceAt(component, 15, 40), "any") {
		t.Errorf("15:40 is %q", sourceAt(component, 15, 40))
	}
}
