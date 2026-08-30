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
	if err := os.WriteFile(source, []byte(`<template><button class="card" @click={() => alert("done")}>Hello</button></template><style>.card { color: red; }</style>`), 0o644); err != nil {
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
	props, err := os.ReadFile(layout.propsPath(source, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(props), "type AppProps struct") {
		t.Fatalf("missing inferred props sidecar:\n%s", props)
	}
	if strings.Contains(string(server), "type AppProps struct") {
		t.Fatalf("renderer unexpectedly owns the props contract:\n%s", server)
	}
	if err := ValidateGeneratedSSRGoPackage(map[string][]byte{"app.go": server, "app_props.go": props}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(server), `data-vela-event=\"`) {
		t.Fatalf("server output misses event marker:\n%s", server)
	}
	bridge, err := os.ReadFile(layout.eventBridgePath(source, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bridge), "addEventListener") {
		t.Fatalf("event bridge is missing native listener:\n%s", bridge)
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

func TestSSRProjectPreservesDeveloperPropsSidecar(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(srcRoot, "App.vela")
	if err := os.WriteFile(source, []byte(`<script>export let title="";</script><template><p>{title}</p></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	layout := NewSSRProjectLayout(filepath.Join(root, "out"))
	if err := compileSSRProjectFile(source, srcRoot, layout); err != nil {
		t.Fatal(err)
	}
	custom := []byte("package components\n\ntype AppProps struct {\n\tTitle string\n\tMarker int\n}\n")
	if err := os.WriteFile(layout.propsPath(source, srcRoot), custom, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := compileSSRProjectFile(source, srcRoot, layout); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(layout.propsPath(source, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(custom) {
		t.Fatalf("user props sidecar was overwritten:\n%s", got)
	}
}

func TestValidateSSRProjectPackagesChecksOneDirectoryAsOnePackage(t *testing.T) {
	root := t.TempDir()
	layout := NewSSRProjectLayout(root)
	if err := os.MkdirAll(layout.ServerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout.ServerDir, "first.go"), []byte("package components\n\ntype Shared struct{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout.ServerDir, "second.go"), []byte("package components\n\ntype Shared struct{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := ValidateSSRProjectPackages(layout)
	if err == nil || !strings.Contains(err.Error(), "Shared redeclared") {
		t.Fatalf("package validation error=%v", err)
	}
}

func TestSSRProjectRendersSamePackageChildComponent(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(srcRoot, "Parent.vela")
	child := filepath.Join(srcRoot, "Child.vela")
	if err := os.WriteFile(parent, []byte(`<script>import { Child } from './Child.vela'; export let title="";</script><template><Child :title={title} /></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte(`<script>export let title="";</script><template><p>{title}</p></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if errors := buildSSRProject([]string{parent, child}, srcRoot, filepath.Join(root, "out"), CompileOptions{StatusWriter: io.Discard}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	server, err := os.ReadFile(NewSSRProjectLayout(filepath.Join(root, "out")).serverPath(parent, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(server), "Child(ChildProps{Title: props.Title}, ChildSlots{}).Render(w)") {
		t.Fatalf("child call was not generated:\n%s", server)
	}
}

func TestGoFileStemUsesLowerSnakeCase(t *testing.T) {
	cases := map[string]string{
		"UserPage":      "user_page",
		"HTTPServer":    "http_server",
		"APIv2Client":   "ap_iv2_client",
		"kebab-case":    "kebab_case",
		"already_snake": "already_snake",
	}
	for input, want := range cases {
		if got := goFileStem(input); got != want {
			t.Errorf("goFileStem(%q)=%q want %q", input, got, want)
		}
	}
}

func TestSSRProjectLayoutMirrorsSnakeCaseGoPath(t *testing.T) {
	layout := NewSSRProjectLayout("out")
	got := filepath.ToSlash(layout.serverPath(filepath.Join("src", "admin", "HTTPServer.vela"), "src"))
	if want := "out/server/components/admin/http_server.go"; got != want {
		t.Fatalf("serverPath=%q want %q", got, want)
	}
}

func TestSSRProjectLayoutUsesSnakeCasePropsPath(t *testing.T) {
	layout := NewSSRProjectLayout("out")
	got := filepath.ToSlash(layout.propsPath(filepath.Join("src", "admin", "HTTPServer.vela"), "src"))
	if want := "out/server/components/admin/http_server_props.go"; got != want {
		t.Fatalf("propsPath=%q want %q", got, want)
	}
}
