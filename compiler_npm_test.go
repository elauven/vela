package main

import (
	"github.com/elauven/vela/runtime"

	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
)

// TestCompilerNpmPackage packs @govela/compiler and the platform package of
// this machine with a freshly built binary, installs both into an
// application without the registry, and runs `npx vela` and the JavaScript
// API there.
func TestCompilerNpmPackage(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available")
	}
	npm, err := exec.LookPath("npm")
	if err != nil {
		t.Skip("npm not available")
	}
	var platform *compilerPlatform
	for i, p := range compilerPlatforms {
		if p.GOOS == goruntime.GOOS && p.GOARCH == goruntime.GOARCH {
			platform = &compilerPlatforms[i]
		}
	}
	if platform == nil {
		t.Skipf("no compiler package for %s/%s", goruntime.GOOS, goruntime.GOARCH)
	}
	root := t.TempDir()
	run := func(dir, name string, args ...string) string {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "npm_config_cache="+filepath.Join(root, "npm-cache"), "VELA_BINARY=")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
		return string(out)
	}

	// The packages as the release workflow publishes them.
	compilerDir, platformDir, tgz := filepath.Join(root, "compiler"), filepath.Join(root, "platform"), filepath.Join(root, "tgz")
	copyTree(t, filepath.FromSlash(compilerPackageDir), compilerDir)
	copyTree(t, filepath.FromSlash(platform.Dir()), platformDir)
	binary := filepath.Join(platformDir, filepath.FromSlash(platform.Binary()))
	// As the release workflow builds it, outside the workspace.
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	if err := os.MkdirAll(tgz, 0o755); err != nil {
		t.Fatal(err)
	}
	run(compilerDir, npm, "pack", "--silent", "--pack-destination", tgz)
	run(platformDir, npm, "pack", "--silent", "--pack-destination", tgz)
	compilerTgz := filepath.Join(tgz, "govela-compiler-"+runtime.Version+".tgz")
	platformTgz := filepath.Join(tgz, "govela-compiler-"+platform.name()+"-"+runtime.Version+".tgz")

	app := filepath.Join(root, "app")
	writeTestFile(t, filepath.Join(app, "package.json"), `{"name": "app", "private": true, "type": "module"}`)
	writeTestFile(t, filepath.Join(app, "src", "App.vela"), "<template><p>hi</p></template>\n<style>p { color: red; }</style>\n")
	writeTestFile(t, filepath.Join(app, "src", "Broken.vela"), checkErrorComponent)
	run(app, npm, "install", "--offline", "--no-audit", "--no-fund", platformTgz, compilerTgz)

	if got := strings.TrimSpace(run(app, "npx", "--no-install", "vela", "--version")); got != "vela "+runtime.Version {
		t.Fatalf("npx vela --version = %q", got)
	}
	var result compileResult
	if err := json.Unmarshal([]byte(run(app, "npx", "--no-install", "vela", "compile", "--path", "src/App.vela")), &result); err != nil || !result.OK {
		t.Fatalf("npx vela compile: %v %+v", err, result)
	}
	cmd := exec.Command("npx", "--no-install", "vela", "check", "--project", "src")
	cmd.Dir = app
	if out, err := cmd.CombinedOutput(); err == nil || cmd.ProcessState.ExitCode() != 1 || !strings.Contains(string(out), "VELA-BIND-MEMBER") {
		t.Fatalf("npx vela check: exit %d, want 1\n%s", cmd.ProcessState.ExitCode(), out)
	}

	// Fake binaries for the handshake checks of the API.
	fakes := filepath.Join(root, "fakes")
	handshake := func(version string, protocol int) string {
		line, _ := json.Marshal(serviceHandshake{Protocol: protocol, CompilerVersion: version, RuntimePackage: runtime.PackageName, RuntimeContract: RuntimeContractExport})
		return "#!/bin/sh\necho '" + string(line) + "'\ncat >/dev/null\n"
	}
	writeTestFile(t, filepath.Join(fakes, "old-version"), handshake("0.0.1", ServiceProtocolVersion))
	writeTestFile(t, filepath.Join(fakes, "new-protocol"), handshake(runtime.Version, 2))
	for _, name := range []string{"old-version", "new-protocol"} {
		if err := os.Chmod(filepath.Join(fakes, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var checks struct {
		Checks map[string]string `json:"checks"`
	}
	out := run(".", node, "tools/compiler-package-check.mjs", app, fakes)
	if err := json.Unmarshal([]byte(out), &checks); err != nil || len(checks.Checks) == 0 {
		t.Fatalf("API checks: %v\n%s", err, out)
	}
	want := []string{"binaryPath", "compile", "service", "missing binary"}
	if goruntime.GOOS != "windows" {
		want = append(want, "version mismatch", "protocol mismatch")
	}
	for _, name := range want {
		if _, ok := checks.Checks[name]; !ok {
			t.Errorf("API check %q did not run", name)
		}
	}
	for name, outcome := range checks.Checks {
		if outcome != "ok" {
			t.Errorf("API %s: %s", name, outcome)
		}
	}

	// Without the platform package the launcher says what is missing.
	bare := filepath.Join(root, "bare")
	writeTestFile(t, filepath.Join(bare, "package.json"), `{"name": "bare", "private": true, "type": "module"}`)
	run(bare, npm, "install", "--offline", "--no-audit", "--no-fund", "--omit=optional", compilerTgz)
	cmd = exec.Command("npx", "--no-install", "vela", "--version")
	cmd.Dir = bare
	cmd.Env = append(os.Environ(), "VELA_BINARY=")
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "@govela/compiler-"+platform.name()+" is not installed") {
		t.Fatalf("launcher without the platform package: %v\n%s", err, out)
	}
}

// copyTree copies the files under from into to.
func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.Walk(from, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.MkdirAll(filepath.Join(to, rel), 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(to, rel), data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}
