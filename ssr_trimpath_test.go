package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The release binaries are built with -trimpath, so they do not know their
// GOROOT. vela --ssr type-checks the generated Go against the standard
// library and must find it through the go command on PATH, without GOROOT
// in the environment (go test sets it for its children).
func TestTrimpathBinaryTypeChecksSSRWithoutGOROOT(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the compiler")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go on PATH")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "vela")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-trimpath", "-o", binary, ".")
	build.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=", "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build -trimpath: %v\n%s", err, out)
	}

	project := filepath.Join(dir, "site")
	for name, content := range map[string]string{
		"go.mod":           "module example.com/site\n",
		"vela.config.json": `{"project":"src","out":"out","ssr":true,"ssrModule":"example.com/site/out/server"}`,
		"src/App.vela":     "<script>\n  export let name = 'x';\n</script>\n<template><p>{name}</p></template>\n",
	} {
		file := filepath.Join(project, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(binary)
	cmd.Dir = project
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GOROOT=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "1/1 SSR components emitted") {
		t.Fatalf("vela --ssr without GOROOT: %v\n%s", err, out)
	}
}
