package main

import (
	"github.com/elauven/vela/runtime"

	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"
)

// skipBundlers skips a test that needs node_modules and Chromium, or fails
// it with VELA_REQUIRE_BUNDLERS=1 (the bundlers job of CI).
func skipBundlers(t *testing.T, format string, args ...any) {
	t.Helper()
	if os.Getenv("VELA_REQUIRE_BUNDLERS") == "1" {
		t.Fatalf(format, args...)
	}
	t.Skipf(format, args...)
}

// pluginApp is the fixture application of the bundler plugin: the sources
// of testdata/bundler/plugin, and node_modules with @govela/unplugin,
// @govela/compiler and @govela/runtime as published, tiny-fmt, and the
// unplugin of the repository's test tooling.
type pluginApp struct {
	t      *testing.T
	root   string
	node   string
	binary string
}

func newPluginApp(t *testing.T) *pluginApp {
	t.Helper()
	return newBundlerApp(t, "plugin")
}

// newBundlerApp is a pluginApp with the sources of testdata/bundler/<fixture>.
func newBundlerApp(t *testing.T, fixture string) *pluginApp {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		skipBundlers(t, "node not available")
	}
	for _, dep := range []string{"vite", "unplugin", "rolldown", "esbuild", "playwright"} {
		if _, err := os.Stat(filepath.Join("node_modules", dep, "package.json")); err != nil {
			skipBundlers(t, "%s not installed; run npm ci", dep)
		}
	}
	a := &pluginApp{t: t, root: t.TempDir(), node: node}
	copyTree(t, filepath.Join("testdata", "bundler", fixture), a.root)
	modules := filepath.Join(a.root, "node_modules")
	copyTree(t, filepath.Join("testdata", "bundler", "packages", "tiny-fmt"), filepath.Join(modules, "tiny-fmt"))
	for dir, name := range map[string]string{runtimePackageDir: runtime.PackageName, compilerPackageDir: CompilerPackageName, unpluginPackageDir: UnpluginPackageName} {
		copyTree(t, filepath.FromSlash(dir), filepath.Join(modules, filepath.FromSlash(name)))
	}
	unplugin, err := filepath.Abs(filepath.Join("node_modules", "unplugin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(unplugin, filepath.Join(modules, "unplugin")); err != nil {
		skipBundlers(t, "symlink to unplugin: %v", err)
	}
	a.binary = filepath.Join(a.root, "vela")
	if goruntime.GOOS == "windows" {
		a.binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", a.binary, ".")
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return a
}

type pluginPageState struct {
	Counter      string `json:"counter"`
	Badge        string `json:"badge"`
	CounterColor string `json:"counterColor"`
	BadgeColor   string `json:"badgeColor"`
	Clicks       string `json:"clicks"`
	Dev          string `json:"dev"`
}

type pluginRun struct {
	Before, After *pluginPageState
	Edited        *pluginPageState
	Logs          []string
	Bundle        *struct {
		Runtimes   int  `json:"runtimes"`
		ProcessEnv bool `json:"processEnv"`
	}
	Entries    map[string]string
	BuildError string `json:"buildError"`
	Error      string
}

// run runs tools/bundler-plugin-check.mjs.
func (a *pluginApp) run(args ...string) pluginRun {
	a.t.Helper()
	var result pluginRun
	a.script("bundler-plugin-check.mjs", &result, args...)
	if result.Error != "" {
		a.t.Fatalf("%v: %s\n%v", args, result.Error, result.Logs)
	}
	return result
}

// script runs tools/<name> <args[0]> <app> <args[1:]> and decodes its JSON
// output into result. The process must end by itself: a compiler service
// the plugin leaves running keeps it alive.
func (a *pluginApp) script(name string, result any, args ...string) {
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, a.node, append([]string{filepath.Join("tools", name), args[0], a.root}, args[1:]...)...)
	cmd.Env = append(os.Environ(), "VELA_BINARY="+a.binary)
	out, err := cmd.Output()
	if ctx.Err() != nil {
		a.t.Fatalf("%s %v did not exit: a compiler service is still running?\n%s", name, args, out)
	}
	var failure struct{ Error string }
	if err != nil || json.Unmarshal(out, result) != nil || json.Unmarshal(out, &failure) != nil {
		a.t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	if strings.Contains(failure.Error, "Executable doesn't exist") || strings.Contains(failure.Error, "browserType.launch") {
		skipBundlers(a.t, "chromium not installed")
	}
}

func (a *pluginApp) checkPage(name, dev string, r pluginRun) {
	a.t.Helper()
	want := func(counter, badge, clicks string) pluginPageState {
		return pluginPageState{Counter: counter, Badge: badge, CounterColor: "rgb(1, 2, 3)", BadgeColor: "rgb(4, 5, 6)", Clicks: clicks, Dev: dev}
	}
	if r.Before == nil || *r.Before != want("07", "b7", "clicks 0") || r.After == nil || *r.After != want("08", "b8", "clicks 1") || len(r.Logs) != 0 {
		a.t.Errorf("%s: before %+v, after %+v, logs %v", name, r.Before, r.After, r.Logs)
	}
	if r.Bundle != nil && (r.Bundle.Runtimes != 1 || r.Bundle.ProcessEnv) {
		a.t.Errorf("%s: bundle has %d runtime instance(s), reads process.env: %v", name, r.Bundle.Runtimes, r.Bundle.ProcessEnv)
	}
}

// TestBundlerPlugin builds the fixture with @govela/unplugin in Vite (dev
// server and builds), esbuild and Rolldown, and runs it in Chromium: the
// components of two directories render and update, their scoped CSS
// applies, `vela` and `vela/store` in the application's modules are the
// runtime package, a bundle has one runtime instance, and the bundler's
// NODE_ENV replacement decides between development and production.
func TestBundlerPlugin(t *testing.T) {
	a := newPluginApp(t)
	for _, mode := range []string{"development", "production"} {
		for _, bundler := range []string{"vite-build", "esbuild", "rolldown"} {
			r := a.run(bundler, mode)
			a.checkPage(bundler+" "+mode, map[string]string{"development": "true", "production": "false"}[mode], r)
			if r.Bundle == nil {
				t.Errorf("%s %s: no bundle information", bundler, mode)
			}
		}
	}

	r := a.run("vite-dev")
	a.checkPage("vite dev", "true", r)
	if r.Edited == nil || r.Edited.BadgeColor != "rgb(7, 8, 9)" {
		t.Errorf("vite dev: an edited <style> did not reach the page: %+v", r.Edited)
	}

	entries := a.run("entries").Entries
	for _, bundler := range unpluginBundlers {
		want := "vela"
		if bundler == "webpack" || bundler == "rspack" {
			want = "apply()"
		}
		if entries[bundler] != want {
			t.Errorf("@govela/unplugin/%s makes %q, want %q", bundler, entries[bundler], want)
		}
	}
}

// TestBundlerPluginReportsDiagnostics: a compile error fails the Vite build
// with the file, the line and the code of the diagnostic. Vite ids use
// forward slashes on every platform.
func TestBundlerPluginReportsDiagnostics(t *testing.T) {
	a := newPluginApp(t)
	writeTestFile(t, filepath.Join(a.root, "src", "App.vela"), checkErrorComponent)
	r := a.run("vite-error")
	for _, want := range []string{"src/App.vela:5:", "VELA-BIND-MEMBER", "Use a one-way binding"} {
		if !strings.Contains(r.BuildError, want) {
			t.Errorf("the build error misses %q:\n%s", want, r.BuildError)
		}
	}
}
