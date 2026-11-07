package main

import (
	"github.com/elauven/vela/runtime"

	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
)

// TestStarterTemplates creates a project from every template of
// @govela/create (tasks 066, 088), in TypeScript and in JavaScript,
// installs the packages of this repository into it, and runs what its CI
// workflow runs: npm run check (with --types in TypeScript), npm run lint,
// npm test and npm run build.
func TestStarterTemplates(t *testing.T) {
	if goruntime.GOOS == "windows" {
		skipBundlers(t, "the test links the commands of node_modules/.bin, which npm makes shims of on Windows")
	}
	if _, err := exec.LookPath("node"); err != nil {
		skipBundlers(t, "node not available")
	}
	if _, err := exec.LookPath("npm"); err != nil {
		skipBundlers(t, "npm not available")
	}
	for _, dep := range starterDependencies {
		if _, err := os.Stat(filepath.Join("node_modules", filepath.FromSlash(dep), "package.json")); err != nil {
			skipBundlers(t, "%s not installed; run npm ci", dep)
		}
	}
	compilerRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tools := t.TempDir()
	vela := filepath.Join(tools, "vela")
	velaApp := filepath.Join(tools, "vela-app")
	// vela-app is a command of the app module.
	for binary, module := range map[string]string{vela: compilerRoot, velaApp: filepath.Join(compilerRoot, "app")} {
		pkg := "."
		if binary == velaApp {
			pkg = "./cmd/vela-app"
		}
		build := exec.Command("go", "build", "-o", binary, pkg)
		build.Dir = module
		build.Env = append(os.Environ(), "GOWORK=off")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("go build %s: %v\n%s", binary, err, out)
		}
	}

	// The packages as npm publishes them: the files of package.json only.
	packed := t.TempDir()
	for _, dir := range []string{runtimePackageDir, compilerPackageDir, unpluginPackageDir, testingPackageDir, createPackageDir, eslintPluginPackageDir, "packages/app"} {
		pack := exec.Command("npm", "pack", "--silent", "--pack-destination", packed, "./"+dir)
		out, err := pack.Output()
		if err != nil {
			t.Fatalf("npm pack %s: %v\n%s", dir, err, out)
		}
		tarball := filepath.Join(packed, strings.TrimSpace(string(out)))
		into := filepath.Join(packed, filepath.FromSlash(dir))
		if err := os.MkdirAll(into, 0o755); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("tar", "-xzf", tarball, "-C", into, "--strip-components=1").CombinedOutput(); err != nil {
			t.Fatalf("tar %s: %v\n%s", tarball, err, out)
		}
	}

	for _, variant := range []string{"default/ts", "default/js", "ssr/ts", "ssr/js", "app/ts", "app/js"} {
		template, language, _ := strings.Cut(variant, "/")
		t.Run(variant, func(t *testing.T) {
			if template != "default" {
				if _, err := exec.LookPath("go"); err != nil {
					t.Skip("go tool not available")
				}
			}
			dir := filepath.Join(t.TempDir(), "my-app")
			args := []string{filepath.Join(packed, "packages", "create", "bin", "create.js"), dir, "--module", "example.com/site"}
			if template != "default" {
				args = append(args, "--"+template)
			}
			if language == "js" {
				args = append(args, "--js")
			}
			if out, err := exec.Command("node", args...).CombinedOutput(); err != nil {
				t.Fatalf("create: %v\n%s", err, out)
			} else if !strings.Contains(string(out), "Created my-app ("+template+", "+map[string]string{"ts": "TypeScript", "js": "JavaScript"}[language]+")") {
				t.Errorf("create printed:\n%s", out)
			}
			files := []string{"AGENTS.md", ".gitignore", ".github/workflows/ci.yml", "eslint.config.js", "vitest.setup." + language}
			if language == "ts" {
				files = append(files, "tsconfig.json")
			}
			for _, name := range files {
				if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name))); err != nil {
					t.Errorf("no %s: %v", name, err)
				}
			}
			manifest, err := os.ReadFile(filepath.Join(dir, "package.json"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(manifest), "__") || !strings.Contains(string(manifest), `"@govela/runtime": "`+runtime.Version+`"`) {
				t.Errorf("package.json:\n%s", manifest)
			}
			agents, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
			if !strings.Contains(string(agents), "/blob/v"+runtime.Version+"/AGENTS.md") {
				t.Errorf("AGENTS.md does not point to the guide of v%s:\n%s", runtime.Version, agents)
			}
			installStarterPackages(t, dir, packed, compilerRoot, vela, velaApp, template == "app")
			if template != "default" {
				useLocalGoModules(t, dir, compilerRoot)
			}

			// The steps the starter prints, in its order, with go.sum empty
			// and no GOFLAGS, as on a new machine: vela-app gen runs before
			// go mod tidy.
			env := append(os.Environ(), "VELA_BINARY="+vela, "VELA_APP_BINARY="+velaApp, "GOWORK=off", "GOFLAGS=")
			steps := map[string][][]string{
				"ssr": {{"npx", "--no-install", "vela"}, {"go", "mod", "tidy"}},
				"app": {{"npx", "--no-install", "vela-app", "gen"}, {"npx", "--no-install", "vela"}, {"go", "mod", "tidy"}},
			}[template]
			for _, step := range steps {
				cmd := exec.Command(step[0], step[1:]...)
				cmd.Dir = dir
				cmd.Env = env
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%s: %v\n%s", strings.Join(step, " "), err, out)
				}
			}
			for _, script := range []string{"check", "lint", "test", "build"} {
				cmd := exec.Command("npm", "run", "--silent", script)
				cmd.Dir = dir
				cmd.Env = env
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("npm run %s: %v\n%s", script, err, out)
				}
				if script == "test" && !strings.Contains(string(out), "1 passed") {
					t.Errorf("npm test:\n%s", out)
				}

			}
			if language == "ts" {
				checkStarterTypes(t, dir, env, template)
			}
			built := map[string]string{"default": "dist/index.html", "ssr": "build/server", "app": "build/server"}[template]
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(built))); err != nil {
				t.Errorf("the build wrote no %s: %v", built, err)
			}
		})
	}
}

// checkStarterTypes: npm run check of a TypeScript project checks types,
// and render() checks the props of a test against the component's.
func checkStarterTypes(t *testing.T, dir string, env []string, template string) {
	t.Helper()
	// A prop of the wrong type, and an event detail used as another type
	// (Counter) or a missing required prop (Page).
	source := "import { render } from '@govela/testing';\nimport Counter from './Counter.vela';\n\n" +
		"render(Counter, { props: { step: 'two' } });\n" +
		"render(Counter, { events: { change: (event) => event.detail.toUpperCase() } });\n"
	test := filepath.Join(dir, "src", "Wrong.test.ts")
	if template == "app" {
		source = "import { render } from '@govela/testing';\nimport Page from './Page.vela';\n\n" +
			"render(Page, { props: { data: { greeting: 1 } } });\n" +
			"render(Page, {});\n"
		test = filepath.Join(dir, "src", "routes", "Wrong.test.ts")
	}
	writeTestFile(t, test, source)
	defer os.Remove(test)
	cmd := exec.Command("npm", "run", "--silent", "check")
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err == nil || strings.Count(string(out), "Wrong.test.ts:") < 2 {
		t.Errorf("npm run check of two wrong render() calls: %v\n%s", err, out)
	}
}

// starterDependencies are the packages of the templates that are not of
// this repository; they are linked from its node_modules.
var starterDependencies = []string{"vite", "vitest", "happy-dom", "unplugin", "eslint", "@eslint/js", "globals", "typescript", "typescript-eslint"}

// installStarterPackages stands in for npm install: the packed packages of
// this repository are copied into node_modules, the others linked from the
// repository's node_modules, and the commands linked into node_modules/.bin.
func installStarterPackages(t *testing.T, dir, packed, compilerRoot, vela, velaApp string, app bool) {
	t.Helper()
	modules := filepath.Join(dir, "node_modules")
	packages := map[string]string{
		runtimePackageDir:      runtime.PackageName,
		compilerPackageDir:     CompilerPackageName,
		unpluginPackageDir:     UnpluginPackageName,
		testingPackageDir:      TestingPackageName,
		eslintPluginPackageDir: ESLintPluginPackageName,
	}
	if app {
		packages["packages/app"] = "@govela/app"
	}
	for from, name := range packages {
		copyTree(t, filepath.Join(packed, filepath.FromSlash(from)), filepath.Join(modules, filepath.FromSlash(name)))
	}
	link := func(target, name string) {
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, name); err != nil {
			skipBundlers(t, "symlink: %v", err)
		}
	}
	// The scopes of the dependencies' own dependencies (@eslint/*,
	// @typescript-eslint/*) resolve from the repository's node_modules.
	for _, dep := range append(append([]string{}, starterDependencies...), "@eslint", "@typescript-eslint") {
		if dep == "@eslint/js" {
			continue
		}
		link(filepath.Join(compilerRoot, "node_modules", filepath.FromSlash(dep)), filepath.Join(modules, filepath.FromSlash(dep)))
	}
	bin := filepath.Join(modules, ".bin")
	link(filepath.Join(compilerRoot, "node_modules", "vite", "bin", "vite.js"), filepath.Join(bin, "vite"))
	link(filepath.Join(compilerRoot, "node_modules", "vitest", "vitest.mjs"), filepath.Join(bin, "vitest"))
	link(filepath.Join(compilerRoot, "node_modules", "eslint", "bin", "eslint.js"), filepath.Join(bin, "eslint"))
	link(vela, filepath.Join(bin, "vela"))
	if app {
		link(velaApp, filepath.Join(bin, "vela-app"))
	}
}

// useLocalGoModules points the Go modules of a project at this repository,
// whose versions may not be tagged yet.
func useLocalGoModules(t *testing.T, dir, compilerRoot string) {
	t.Helper()
	path := filepath.Join(dir, "go.mod")
	mod, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	replace := "\nreplace github.com/elauven/vela/ssr_runtime => " + filepath.ToSlash(filepath.Join(compilerRoot, "ssr_runtime")) + "\n" +
		"\nreplace github.com/elauven/vela/app => " + filepath.ToSlash(filepath.Join(compilerRoot, "app")) + "\n"
	writeTestFile(t, path, string(mod)+replace)
	// No go.sum: the steps of the starter create it (go mod tidy).
	os.Remove(filepath.Join(dir, "go.sum"))
}
