package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildSSRProjectWritesSeparatedProducts(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(srcRoot, "App.vela")
	if err := os.WriteFile(source, []byte(`<template><main class="card"><h1>Hello</h1></main></template><style>.card { color: red; }</style>`), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	opts := CompileOptions{StatusWriter: io.Discard}
	if errors := buildSSRProject([]string{source}, srcRoot, outRoot, opts, false); errors != 0 {
		t.Fatalf("buildSSRProject errors=%d", errors)
	}
	layout := NewSSRProjectLayout(outRoot)
	client, err := os.ReadFile(layout.clientPath(source, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(client), "function App") {
		t.Fatalf("client output is not CSR JavaScript:\n%s", client)
	}
	server, err := os.ReadFile(layout.serverPath(source, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(server), "func RenderApp") {
		t.Fatalf("server output is not a Go renderer:\n%s", server)
	}
	if err := ValidateGeneratedSSRGo("App.go", server); err != nil {
		t.Fatal(err)
	}
	css, err := os.ReadFile(layout.stylePath(source, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), "color: red") {
		t.Fatalf("missing scoped CSS:\n%s", css)
	}
	if _, err := os.Stat(filepath.Join(layout.ClientDir, "runtime.js")); err != nil {
		t.Fatalf("missing client runtime: %v", err)
	}
}
