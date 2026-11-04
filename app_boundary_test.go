package main

// TestAppLayerIsSeparable enforces the boundary between the compiler and the
// app layer (docs/APP.md B-1, B-2, B-4): the app module and @govela/app use
// only the public interfaces of the compiler, so they can move to their own
// repository unchanged.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const (
	rootModulePath = "github.com/elauven/vela"
	appModulePath  = rootModulePath + "/app"
	ssrModulePath  = rootModulePath + "/ssr_runtime"
)

// listedPackage is the part of `go list -json` the boundary checks read.
type listedPackage struct {
	ImportPath   string
	Standard     bool
	Module       *struct{ Path string }
	Imports      []string
	TestImports  []string
	XTestImports []string
}

func (p listedPackage) module() string {
	if p.Module == nil {
		return ""
	}
	return p.Module.Path
}

// basePath strips the test variant suffix: "a/b [a/b.test]" → "a/b".
func basePath(importPath string) string {
	if i := strings.Index(importPath, " ["); i >= 0 {
		return importPath[:i]
	}
	return importPath
}

func inModule(importPath, module string) bool {
	return importPath == module || strings.HasPrefix(importPath, module+"/")
}

// goListDeps runs `go list -deps -test -json ./...` in dir. env is added to
// the environment (GOWORK of a probe workspace).
func goListDeps(t *testing.T, dir string, env ...string) []listedPackage {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", "-test", "-json=ImportPath,Standard,Module,Imports,TestImports,XTestImports", "./...")
	cmd.Dir = dir
	// The package's tests set GOFLAGS=-mod=mod (ssr_gomod_env_test.go),
	// which workspace mode rejects.
	// PWD of this process is not dir; go would ignore it and take the
	// real path of the working directory.
	cmd.Env = append(append(os.Environ(), "GOFLAGS=", "PWD="+dir), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list in %s: %v\n%s", dir, err, stderr.String())
	}
	var pkgs []listedPackage
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p listedPackage
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		pkgs = append(pkgs, p)
	}
	return pkgs
}

// appDependencyViolations checks rule 1 of B-4 on the dependencies of the
// app module: no package of the root module, and direct imports of app
// packages only from the standard library, ssr_runtime and the app itself.
func appDependencyViolations(pkgs []listedPackage) []string {
	standard := map[string]bool{}
	for _, p := range pkgs {
		if p.Standard {
			standard[basePath(p.ImportPath)] = true
		}
	}
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, p := range pkgs {
		if p.module() == rootModulePath {
			add(fmt.Sprintf("%s is a package of the compiler module %s", basePath(p.ImportPath), rootModulePath))
		}
		if p.module() != appModulePath {
			continue
		}
		for _, imp := range append(append(append([]string{}, p.Imports...), p.TestImports...), p.XTestImports...) {
			imp = basePath(imp)
			if standard[imp] || imp == "C" || inModule(imp, appModulePath) || inModule(imp, ssrModulePath) {
				continue
			}
			add(fmt.Sprintf("%s imports %s; the app layer imports only the standard library, %s and its own packages", basePath(p.ImportPath), imp, ssrModulePath))
		}
	}
	sort.Strings(out)
	return out
}

// rootImportViolations checks rule 2 of B-4: no package of the root module
// depends on the app module.
func rootImportViolations(pkgs []listedPackage) []string {
	var out []string
	for _, p := range pkgs {
		if inModule(basePath(p.ImportPath), appModulePath) || p.module() == appModulePath {
			out = append(out, fmt.Sprintf("the compiler module depends on %s", basePath(p.ImportPath)))
		}
	}
	sort.Strings(out)
	return out
}

var (
	jsBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	jsLineComment  = regexp.MustCompile(`(?m)^\s*//.*$`)
	// import … from 'x', export … from 'x'; the clause is captured to find $$ names.
	jsFromImport   = regexp.MustCompile(`(?s)\b(?:import|export)\b([^;'"` + "`" + `]*?)\bfrom\s*['"]([^'"]+)['"]`)
	jsBareImport   = regexp.MustCompile(`\bimport\s*['"]([^'"]+)['"]`)
	jsCallImport   = regexp.MustCompile(`\b(?:import|require)\s*\(\s*([^)]*)\)`)
	jsStringArg    = regexp.MustCompile(`^['"]([^'"]+)['"]$`)
	jsInternalName = regexp.MustCompile(`\$\$[A-Za-z_]`)
)

// runtimeEntryPoints are the public specifiers of @govela/runtime, read from
// the exports of packages/runtime/package.json.
func runtimeEntryPoints(t *testing.T) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("packages", "runtime", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Name    string
		Exports map[string]any
	}
	if err := json.Unmarshal(b, &manifest); err != nil {
		t.Fatal(err)
	}
	entries := map[string]bool{}
	for key := range manifest.Exports {
		entries[path.Join(manifest.Name, key)] = true
	}
	if !entries["@govela/runtime"] || !entries["@govela/runtime/hydrate"] {
		t.Fatalf("unexpected exports of @govela/runtime: %v", entries)
	}
	return entries
}

// jsImportViolations checks rule 3 of B-4 on the sources in dir: only the
// public runtime entry points, no $$ names, and relative imports that stay
// inside dir.
func jsImportViolations(dir string, runtime map[string]bool) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(file string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(file) {
		case ".js", ".mjs", ".cjs", ".ts", ".mts":
		default:
			return nil
		}
		b, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, file)
		rel = filepath.ToSlash(rel)
		src := jsLineComment.ReplaceAllString(jsBlockComment.ReplaceAllString(string(b), ""), "")
		check := func(spec string) {
			switch {
			case strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../"):
				target := path.Join(path.Dir(rel), spec)
				if target == ".." || strings.HasPrefix(target, "../") || strings.HasPrefix(target, "node_modules/") {
					out = append(out, fmt.Sprintf("%s imports %q outside the package", rel, spec))
				}
			case runtime[spec]:
			default:
				out = append(out, fmt.Sprintf("%s imports %q; only the public entry points of @govela/runtime and the package's own files are allowed", rel, spec))
			}
		}
		for _, m := range jsFromImport.FindAllStringSubmatch(src, -1) {
			check(m[2])
		}
		for _, m := range jsBareImport.FindAllStringSubmatch(src, -1) {
			check(m[1])
		}
		for _, m := range jsCallImport.FindAllStringSubmatch(src, -1) {
			arg := strings.TrimSpace(m[1])
			if s := jsStringArg.FindStringSubmatch(arg); s != nil {
				check(s[1])
			} else {
				out = append(out, fmt.Sprintf("%s has a dynamic import of %s, which cannot be checked", rel, arg))
			}
		}
		if name := jsInternalName.FindString(src); name != "" {
			out = append(out, fmt.Sprintf("%s uses the internal runtime name %s…; names starting with $$ are not public", rel, name))
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

func TestAppLayerIsSeparable(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list over both modules")
	}
	t.Run("app imports no compiler package", func(t *testing.T) {
		pkgs := goListDeps(t, "app")
		if len(pkgs) == 0 {
			t.Fatal("go list found no packages in app/")
		}
		for _, v := range appDependencyViolations(pkgs) {
			t.Error(v)
		}
	})
	t.Run("compiler imports no app package", func(t *testing.T) {
		for _, v := range rootImportViolations(goListDeps(t, ".")) {
			t.Error(v)
		}
	})
	t.Run("packages/app imports only public runtime entry points", func(t *testing.T) {
		violations, err := jsImportViolations(filepath.Join("packages", "app"), runtimeEntryPoints(t))
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range violations {
			t.Error(v)
		}
	})
}

// TestAppLayerBoundaryDetectsViolations is the test of the test: forbidden
// imports added to copies of the app module and of packages/app are found.
func TestAppLayerBoundaryDetectsViolations(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list over a probe workspace")
	}
	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("Go import of the compiler", func(t *testing.T) {
		// On macOS the temporary directory is behind a symlink
		// (/var -> /private/var): go resolves the working directory to the
		// real path, and ./app of the go.work must name the same one.
		work, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		copyTree(t, filepath.Join(root, "app"), filepath.Join(work, "app"))
		// app/go.mod may replace the runtime with ../ssr_runtime, which is
		// not next to the copy.
		goMod := filepath.Join(work, "app", "go.mod")
		data, err := os.ReadFile(goMod)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, goMod, strings.ReplaceAll(string(data), "=> ../ssr_runtime", "=> "+filepath.ToSlash(filepath.Join(root, "ssr_runtime"))))
		writeFile(t, filepath.Join(work, "app", "internal", "probe", "probe.go"),
			"package probe\n\nimport \"github.com/elauven/vela/runtime\"\n\nvar _ = runtime.Version\n")
		writeFile(t, filepath.Join(work, "go.work"), fmt.Sprintf("go 1.25.0\n\nuse (\n\t%q\n\t%q\n\t./app\n)\n",
			filepath.ToSlash(root), filepath.ToSlash(filepath.Join(root, "ssr_runtime"))))
		if sum, err := os.ReadFile(filepath.Join(root, "go.work.sum")); err == nil {
			writeFile(t, filepath.Join(work, "go.work.sum"), string(sum))
		}
		violations := appDependencyViolations(goListDeps(t, filepath.Join(work, "app"), "GOWORK="+filepath.Join(work, "go.work")))
		want := []string{
			"github.com/elauven/vela/app/internal/probe imports github.com/elauven/vela/runtime",
			"github.com/elauven/vela/runtime is a package of the compiler module",
		}
		for _, w := range want {
			if !containsPrefix(violations, w) {
				t.Errorf("no violation %q in %q", w, violations)
			}
		}
	})

	t.Run("third-party Go import", func(t *testing.T) {
		pkgs := []listedPackage{
			{ImportPath: "fmt", Standard: true},
			{ImportPath: appModulePath + "/x", Module: &struct{ Path string }{appModulePath},
				Imports: []string{"fmt", ssrModulePath + "/vite", "golang.org/x/text/language"}},
		}
		if v := appDependencyViolations(pkgs); len(v) != 1 || !strings.Contains(v[0], "imports golang.org/x/text/language") {
			t.Errorf("violations %q", v)
		}
	})

	t.Run("compiler import of the app", func(t *testing.T) {
		pkgs := []listedPackage{
			{ImportPath: rootModulePath, Module: &struct{ Path string }{rootModulePath}, Imports: []string{appModulePath}},
			{ImportPath: appModulePath, Module: &struct{ Path string }{appModulePath}},
		}
		if v := rootImportViolations(pkgs); len(v) != 1 || !strings.Contains(v[0], appModulePath) {
			t.Errorf("violations %q", v)
		}
	})

	t.Run("JavaScript imports", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "app")
		copyTree(t, filepath.Join(root, "packages", "app"), dir)
		writeFile(t, filepath.Join(dir, "probe.js"), strings.Join([]string{
			`import { hydrate } from '@govela/runtime/hydrate';`,
			`import { ok } from './client.js';`,
			`// import { commented } from 'commented-out';`,
			`import { $$proxy } from '@govela/runtime';`,
			`import { x } from '../runtime/runtime.js';`,
			`import 'side-effect';`,
			`export { y } from "lodash";`,
			`const m = await import('@govela/runtime/internal');`,
			`const n = await import(name);`,
		}, "\n"))
		violations, err := jsImportViolations(dir, runtimeEntryPoints(t))
		if err != nil {
			t.Fatal(err)
		}
		want := []string{
			`probe.js imports "../runtime/runtime.js" outside the package`,
			`probe.js imports "side-effect"`,
			`probe.js imports "lodash"`,
			`probe.js imports "@govela/runtime/internal"`,
			`probe.js has a dynamic import of name`,
			`probe.js uses the internal runtime name $$p`,
		}
		for _, w := range want {
			if !containsPrefix(violations, w) {
				t.Errorf("no violation %q in %q", w, violations)
			}
		}
		if len(violations) != len(want) {
			t.Errorf("%d violations, want %d: %q", len(violations), len(want), violations)
		}
	})
}

func containsPrefix(list []string, prefix string) bool {
	for _, s := range list {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

func writeFile(t *testing.T, file, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
