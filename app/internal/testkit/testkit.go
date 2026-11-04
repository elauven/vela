// Package testkit helps the tests of the app layer run the compiler and the
// go tool on copies of projects. App tests run the compiler as a program,
// never as a Go package (docs/APP.md B-4).
package testkit

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	velaOnce sync.Once
	velaBin  string
	velaErr  error
	velaOut  []byte
)

// RepoRoot is the root of the repository: the directory with go.work.
func RepoRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("testkit: no go.work above the test directory")
		}
		dir = parent
	}
}

// VelaBinary builds the compiler of the repository once per test binary.
func VelaBinary(t testing.TB) string {
	t.Helper()
	velaOnce.Do(func() {
		dir, err := os.MkdirTemp("", "vela-app-test-")
		if err != nil {
			velaErr = err
			return
		}
		velaBin = filepath.Join(dir, "vela")
		if filepath.Separator == '\\' {
			velaBin += ".exe"
		}
		build := exec.Command("go", "build", "-o", velaBin, ".")
		build.Dir = RepoRoot(t)
		build.Env = append(os.Environ(), "GOFLAGS=")
		velaOut, velaErr = build.CombinedOutput()
	})
	if velaErr != nil {
		t.Fatalf("build vela: %v\n%s", velaErr, velaOut)
	}
	return velaBin
}

// CopyProject copies a project into a temporary directory without the
// outputs of vela-app, vela and Vite.
func CopyProject(t testing.TB, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, file)
		if entry.IsDir() {
			switch filepath.ToSlash(rel) {
			case "out", "want", "dist", "node_modules", "src/_app", "internal/appgen":
				return filepath.SkipDir
			}
			return nil
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		WriteFile(t, filepath.Join(dst, rel), string(data))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// WriteFile writes a file, creating its directory.
func WriteFile(t testing.TB, file, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// UseLocalModules points go.mod of a copied project at the app and runtime
// modules of this repository.
func UseLocalModules(t testing.TB, root string) {
	t.Helper()
	repo := RepoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	module := strings.SplitN(string(data), "\n", 2)[0]
	app := filepath.ToSlash(filepath.Join(repo, "app"))
	runtime := filepath.ToSlash(filepath.Join(repo, "ssr_runtime"))
	WriteFile(t, filepath.Join(root, "go.mod"), module+"\n\ngo 1.25.0\n\nrequire (\n\tgithub.com/elauven/vela/app v0.0.0\n\tgithub.com/elauven/vela/ssr_runtime v0.0.0\n)\n\nreplace (\n\tgithub.com/elauven/vela/app => "+app+"\n\tgithub.com/elauven/vela/ssr_runtime => "+runtime+"\n)\n")
	os.Remove(filepath.Join(root, "go.sum"))
}

// Run runs a command in dir outside the workspace and returns its output.
func Run(t testing.TB, dir, name string, args ...string) string {
	t.Helper()
	command := exec.Command(name, args...)
	command.Dir = dir
	command.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

// CompileApp runs vela --ssr on a generated project, then go build and go
// vet. Any compiler error or warning fails the test.
func CompileApp(t testing.TB, root string) {
	t.Helper()
	out := Run(t, root, VelaBinary(t))
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, " x ") || strings.Contains(line, "✗") || strings.Contains(line, "error") || strings.Contains(line, "warn") {
			t.Errorf("vela: %s", line)
		}
	}
	if !strings.Contains(out, "SSR components emitted") {
		t.Fatalf("vela:\n%s", out)
	}
	UseLocalModules(t, root)
	Run(t, root, "go", "build", "./...")
	Run(t, root, "go", "vet", "./...")
}

// RequireGo skips a test that builds Go code in -short mode or without go.
func RequireGo(t testing.TB) {
	t.Helper()
	if testing.Short() {
		t.Skip("builds the compiler and the generated Go code")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not available")
	}
}
