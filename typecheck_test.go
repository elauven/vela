package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elauven/vela/internal/typecheck"
)

// The type check of a project (task 084, docs/TYPESCRIPT.md TS-1.2,
// TS-1.5): testdata/typecheck/errors has one error per check, each
// reported at its range of the .vela or .ts source; testdata/typecheck/clean
// uses every check correctly and has none. Both run with TypeScript 5.8
// and 6.x.

// typeCheckApp copies a fixture into a temporary directory with
// node_modules: the TypeScript of node_modules/<ts> as `typescript`, and
// the runtime package.
func typeCheckApp(t *testing.T, fixture, ts string) string {
	t.Helper()
	tsDir, err := filepath.Abs(filepath.Join("node_modules", ts))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(tsDir, "lib", "typescript.js")); err != nil {
		skipBundlers(t, "%s not installed; run npm ci", ts)
	}
	app := t.TempDir()
	copyTree(t, filepath.Join("testdata", "typecheck", fixture), app)
	runtimeDir, err := filepath.Abs(runtimePackageDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(app, "node_modules", "@govela"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(tsDir, filepath.Join(app, "node_modules", "typescript")); err != nil {
		skipBundlers(t, "symlink: %v", err)
	}
	if err := os.Symlink(runtimeDir, filepath.Join(app, "node_modules", "@govela", "runtime")); err != nil {
		t.Fatal(err)
	}
	return app
}

type typeCheckReport struct {
	Diagnostics []MachineDiagnostic `json:"diagnostics"`
	Errors      int                 `json:"errors"`
}

func runTypeCheck(t *testing.T, app string, args ...string) (typeCheckReport, int, string) {
	t.Helper()
	t.Chdir(app)
	var stdout, stderr bytes.Buffer
	code := runCheckCommand(append([]string{"--types", "--format=json", "--project", "src"}, args...), &stdout, &stderr)
	var report typeCheckReport
	if code != 2 {
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Fatalf("%v\n%s\n%s", err, stdout.String(), stderr.String())
		}
	}
	return report, code, stderr.String()
}

func TestCheckTypes(t *testing.T) {
	type expected struct{ file, code, at string }
	// at is the text the diagnostic starts at: its first occurrence in the
	// file, or the occurrence after a "…|" prefix.
	want := []expected{
		{"src/main.ts", "TS2322", "label: total"},
		{"src/Errors.vela", "TS2322", "label: string = 42|label"},
		{"src/Errors.vela", "TS2322", "{ count: 'many' }|count"},
		{"src/Errors.vela", "TS2554", "$$dispatch('missing')"},
		{"src/Errors.vela", "TS2339", "name.nope|nope"},
		{"src/Errors.vela", "TS18047", "e.target.value|e.target"},
		{"src/Errors.vela", "TS2339", "e.target.value|value"},
		{"src/Errors.vela", "TS2345", "<Button />|Button"},
		{"src/Errors.vela", "TS2322", "label={count}|label"},
		{"src/Errors.vela", "TS2353", "size=\"big\"|size"},
		{"src/Errors.vela", "TS2322", "kind=\"loud\"|kind"},
		{"src/Errors.vela", "TS2345", "@on:pres=|pres"},
		{"src/Errors.vela", "TS2339", "e.detail.when|when"},
		{"src/Errors.vela", "TS2345", "bind:label|label"},
		{"src/Errors.vela", "TS2345", "slot=\"iconz\"|iconz"},
		{"src/Errors.vela", "TS2353", "[{ name: 'a' }]|name"},
		{"src/Errors.vela", "TS2339", "item.name|name"},
		{"src/Errors.vela", "TS2322", ":color|color"},
		{"src/Errors.vela", "TS2345", "bind:open={open}|open"},
		{"src/Errors.vela", "TS2339", "n.length|length"},
		{"src/Errors.vela", "TS2551", "text.toFixed|toFixed"},
		{"src/Checked.vela", "TS2322", "amount = 'none'|amount"},
		{"src/Checked.vela", "TS2339", "amount.nope|nope"},
	}
	for _, ts := range []string{"typescript-5.8", "typescript"} {
		t.Run(ts, func(t *testing.T) {
			app := typeCheckApp(t, "errors", ts)
			report, code, stderr := runTypeCheck(t, app)
			if code != 1 {
				t.Fatalf("exit code %d, want 1: %s", code, stderr)
			}
			found := map[int]bool{}
			for _, w := range want {
				data, err := os.ReadFile(filepath.Join(app, w.file))
				if err != nil {
					t.Fatal(err)
				}
				src := string(data)
				off := strings.Index(src, w.at)
				if prefix, needle, ok := strings.Cut(w.at, "|"); ok {
					off = strings.Index(src, prefix)
					if off >= 0 {
						off += strings.Index(src[off:], needle)
					}
				}
				if off < 0 {
					t.Fatalf("%q is not in %s", w.at, w.file)
				}
				line := strings.Count(src[:off], "\n") + 1
				col := off - strings.LastIndex(src[:off], "\n")
				hit := -1
				for i, d := range report.Diagnostics {
					if !found[i] && filepath.ToSlash(d.File) == w.file && d.Code == w.code && d.Line == line && d.Column == col {
						hit = i
						break
					}
				}
				if hit < 0 {
					t.Errorf("no %s at %s:%d:%d (%q)", w.code, w.file, line, col, w.at)
					continue
				}
				found[hit] = true
			}
			for i, d := range report.Diagnostics {
				if !found[i] && strings.HasPrefix(d.Code, "TS") {
					// A duplicate attribute is also TS1117; the rest is unexpected.
					if d.Code == "TS1117" {
						continue
					}
					t.Errorf("unexpected %s at %s:%d:%d: %s", d.Code, d.File, d.Line, d.Column, d.Message)
				}
				if strings.Contains(d.Message, "in code Vela generates") {
					t.Errorf("unmapped diagnostic: %+v", d)
				}
			}
		})
	}
}

// TestCheckTypesClean: projects without type errors have none. clean uses
// every check; dashboard-ts is examples/dashboard moved to lang="ts" (with
// the fixes the check asked for), without strict, as a project is before
// it adds annotations.
func TestCheckTypesClean(t *testing.T) {
	for _, fixture := range []string{"clean", "dashboard-ts"} {
		for _, ts := range []string{"typescript-5.8", "typescript"} {
			t.Run(fixture+"/"+ts, func(t *testing.T) {
				app := typeCheckApp(t, fixture, ts)
				report, code, stderr := runTypeCheck(t, app)
				if code != 0 || report.Errors != 0 {
					t.Errorf("exit code %d, %d error(s): %s %+v", code, report.Errors, stderr, report.Diagnostics)
				}
				for _, d := range report.Diagnostics {
					// The compiler's own warnings of the example stay; type
					// diagnostics are what this test is about.
					typed := strings.HasPrefix(d.Code, "TS") || strings.HasPrefix(d.Code, "VELA-TS")
					if typed && !(fixture == "dashboard-ts" && d.Code == "VELA-TS-CONFIG" && strings.Contains(d.Message, "strict")) {
						t.Errorf("%s at %s:%d:%d: %s", d.Code, d.File, d.Line, d.Column, d.Message)
					}
				}
			})
		}
	}
}

func TestCheckTypesConfig(t *testing.T) {
	app := typeCheckApp(t, "clean", "typescript")
	// Without tsconfig.json the check uses the configuration of TS-10.2 and
	// says so.
	if err := os.Remove(filepath.Join(app, "tsconfig.json")); err != nil {
		t.Fatal(err)
	}
	report, code, stderr := runTypeCheck(t, app)
	if code != 0 || len(report.Diagnostics) == 0 || !hasCode(report.Diagnostics, "VELA-TS-CONFIG") {
		t.Errorf("without tsconfig.json: exit %d %s %+v", code, stderr, report.Diagnostics)
	}
	// A setting that contradicts TS-10.2 is a warning.
	config := `{"compilerOptions": {"strict": true, "module": "ESNext", "moduleResolution": "bundler", "target": "ES2022", "allowImportingTsExtensions": true, "noEmit": true, "isolatedModules": true, "erasableSyntaxOnly": true, "types": ["@govela/runtime/ambient"]}, "include": ["src"]}`
	if err := os.WriteFile(filepath.Join(app, "tsconfig.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	report, _, _ = runTypeCheck(t, app)
	var warned bool
	for _, d := range report.Diagnostics {
		warned = warned || d.Code == "VELA-TS-CONFIG" && strings.Contains(d.Message, "verbatimModuleSyntax")
	}
	if !warned {
		t.Errorf("no warning about verbatimModuleSyntax: %+v", report.Diagnostics)
	}

	// --types needs --project; without TypeScript it fails with the reason.
	var stdout, errOut bytes.Buffer
	if code := runCheckCommand([]string{"--types", "src/App.vela"}, &stdout, &errOut); code != 2 || !strings.Contains(errOut.String(), "--types requires --project") {
		t.Errorf("--types without --project: %d %s", code, errOut.String())
	}
	if err := os.Remove(filepath.Join(app, "node_modules", "typescript")); err != nil {
		t.Fatal(err)
	}
	// Only node on PATH: no npm, so no global TypeScript either.
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available")
	}
	bin := t.TempDir()
	if err := os.Symlink(node, filepath.Join(bin, "node")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	errOut.Reset()
	if code := runCheckCommand([]string{"--types", "--project", "src", "--tsdk", filepath.Join(app, "missing")}, &stdout, &errOut); code != 2 || !strings.Contains(errOut.String(), "TypeScript") {
		t.Errorf("without TypeScript: %d %s", code, errOut.String())
	}
}

func hasCode(diags []MachineDiagnostic, code string) bool {
	for _, d := range diags {
		if d.Code == code {
			return true
		}
	}
	return false
}

// BenchmarkCheckTypes checks a project of 300 TypeScript components: the
// first check, then a check after one component changed, which reuses the
// helper's program (`vela check --types --watch`).
//
//	go test -run '^$' -bench CheckTypes -benchtime 1x .
func BenchmarkCheckTypes(b *testing.B) {
	app := b.TempDir()
	tsDir, err := filepath.Abs(filepath.Join("node_modules", "typescript"))
	if err != nil {
		b.Fatal(err)
	}
	if _, err := os.Stat(tsDir); err != nil {
		b.Skip("typescript not installed")
	}
	runtimeDir, _ := filepath.Abs(runtimePackageDir)
	must := func(err error) {
		if err != nil {
			b.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(app, "node_modules", "@govela"), 0o755))
	must(os.Symlink(tsDir, filepath.Join(app, "node_modules", "typescript")))
	must(os.Symlink(runtimeDir, filepath.Join(app, "node_modules", "@govela", "runtime")))
	config, err := os.ReadFile(filepath.Join("testdata", "typecheck", "clean", "tsconfig.json"))
	must(err)
	must(os.WriteFile(filepath.Join(app, "tsconfig.json"), config, 0o644))
	src := filepath.Join(app, "src")
	must(os.MkdirAll(src, 0o755))
	const n = 300
	component := func(i int, extra string) string {
		child := ""
		use := ""
		if i+1 < n {
			child = fmt.Sprintf("  import { Item%d } from './Item%d.vela';\n", i+1, i+1)
			use = fmt.Sprintf("<Item%d :label={label} :count={count + 1} @on:pick={(e) => pick(e.detail.id)} />", i+1)
		}
		return fmt.Sprintf(`<script lang="ts">
%s  interface $$Events { pick: { id: number } }
  export let label: string;
  export let count: number = 0;
  let items = $state<{ id: number; name: string }[]>([]);
  function pick(id: number) { $$dispatch('pick', { id }); %s}
</script>

<template>
  <p>{label.toUpperCase()} {count.toFixed()}</p>
  {#each items as item (item.id)}<button @click={() => pick(item.id)}>{item.name}</button>{/each}
  %s
</template>
`, child, extra, use)
	}
	for i := 0; i < n; i++ {
		must(os.WriteFile(filepath.Join(src, fmt.Sprintf("Item%d.vela", i)), []byte(component(i, "")), 0o644))
	}
	b.Chdir(app)
	for range b.N {
		start := time.Now()
		checker, err := typecheck.NewChecker(".", "src", typecheck.Options{})
		must(err)
		diags, err := checker.Check()
		must(err)
		if len(diags) > 0 {
			b.Fatalf("%d diagnostics: %v", len(diags), diags[0])
		}
		cold := time.Since(start)
		must(os.WriteFile(filepath.Join(src, "Item7.vela"), []byte(component(7, "items = [];")), 0o644))
		start = time.Now()
		_, err = checker.Check()
		must(err)
		warm := time.Since(start)
		checker.Close()
		b.ReportMetric(cold.Seconds(), "s/first-check")
		b.ReportMetric(warm.Seconds(), "s/recheck")
	}
}

// TestTypeScriptPlugin: in TypeScript's language service with
// @govela/typescript-plugin, a .ts module sees the types of the components
// it imports (task 085, TS-6.4): a wrong prop is an error, the props
// complete, the events have their detail.
func TestTypeScriptPlugin(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "vela")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	for _, ts := range []string{"typescript-5.8", "typescript"} {
		t.Run(ts, func(t *testing.T) {
			app := typeCheckApp(t, "../typescript-plugin", ts)
			out, err := exec.Command("node", "tools/typescript-plugin-check.mjs", filepath.Join(app, "node_modules", "typescript"), app, binary).Output()
			if err != nil {
				t.Fatalf("plugin check: %v\n%s", err, out)
			}
			var res struct {
				Diagnostics []struct {
					Code    int    `json:"code"`
					At      string `json:"at"`
					Message string `json:"message"`
				} `json:"diagnostics"`
				QuickInfo  string   `json:"quickInfo"`
				Completion []string `json:"completion"`
				Definition []string `json:"definition"`
			}
			if err := json.Unmarshal(out, &res); err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if len(res.Diagnostics) != 1 || res.Diagnostics[0].Code != 2322 || res.Diagnostics[0].At != "label" {
				t.Errorf("diagnostics: %+v", res.Diagnostics)
			}
			if !strings.Contains(res.QuickInfo, "Button") || !strings.Contains(res.QuickInfo, "ButtonProps") {
				t.Errorf("quick info: %q", res.QuickInfo)
			}
			if c := strings.Join(res.Completion, " "); !strings.Contains(c, "label") || !strings.Contains(c, "kind") || !strings.Contains(c, "icon") {
				t.Errorf("completion: %v", res.Completion)
			}
			if len(res.Definition) != 1 || filepath.Base(res.Definition[0]) != "Button.vela" {
				t.Errorf("definition: %v", res.Definition)
			}
		})
	}
}
