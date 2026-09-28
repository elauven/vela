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
	hydrationClient, err := os.ReadFile(layout.hydrationClientPath(source, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(hydrationClient), "addEventListener('click'") {
		t.Fatalf("executable hydration client is missing native listener:\n%s", hydrationClient)
	}
	if _, err := os.Stat(layout.eventBridgePath(source, srcRoot)); !os.IsNotExist(err) {
		t.Fatalf("executable hydration unexpectedly emitted static event bridge: %v", err)
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

func TestSSRProjectRendersSamePackageDefaultAndNamedSlots(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(srcRoot, "Parent.vela")
	child := filepath.Join(srcRoot, "Child.vela")
	if err := os.WriteFile(parent, []byte(`<script>import { Child } from './Child.vela';</script><template><Child><p>body</p><svelte:fragment slot="actions"><button>save</button></svelte:fragment></Child></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte(`<template><main><slot>fallback</slot><footer><slot name="actions" /></footer></main></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if errors := buildSSRProject([]string{parent, child}, srcRoot, filepath.Join(root, "out"), CompileOptions{StatusWriter: io.Discard}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	layout := NewSSRProjectLayout(filepath.Join(root, "out"))
	parentSource, err := os.ReadFile(layout.serverPath(parent, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	childSource, err := os.ReadFile(layout.serverPath(child, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ChildSlots{", "Default: func(w io.Writer) error", "Actions: func(w io.Writer) error"} {
		if !strings.Contains(string(parentSource), want) {
			t.Fatalf("parent misses %q:\n%s", want, parentSource)
		}
	}
	for _, want := range []string{"type ChildSlots struct", "Default vela.Slot", "Actions vela.Slot", "slots.Default(w)", "slots.Actions(w)"} {
		if !strings.Contains(string(childSource), want) {
			t.Fatalf("child misses %q:\n%s", want, childSource)
		}
	}
}

func TestSSRProjectRendersCrossPackageChildComponent(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	pagesDir := filepath.Join(srcRoot, "pages")
	uiDir := filepath.Join(srcRoot, "ui")
	if err := os.MkdirAll(pagesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(uiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(pagesDir, "Parent.vela")
	child := filepath.Join(uiDir, "Child.vela")
	if err := os.WriteFile(parent, []byte(`<script>import { Child } from '../ui/Child.vela'; export let title="";</script><template><Child :title={title}><strong>body</strong><svelte:fragment slot="actions"><button>save</button></svelte:fragment></Child></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte(`<script>export let title="";</script><template><article><h1>{title}</h1><slot>fallback</slot><footer><slot name="actions" /></footer></article></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	opts := CompileOptions{StatusWriter: io.Discard, SSRModule: "example.com/site/generated"}
	if errors := buildSSRProject([]string{parent, child}, srcRoot, outRoot, opts, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	layout := NewSSRProjectLayoutWithModule(outRoot, opts.SSRModule)
	parentSource, err := os.ReadFile(layout.serverPath(parent, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	text := string(parentSource)
	for _, want := range []string{
		`ui "example.com/site/generated/components/ui"`,
		"ui.Child(ui.ChildProps{Title: props.Title}, ui.ChildSlots{",
		"Default: func(w io.Writer) error",
		"Actions: func(w io.Writer) error",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("cross-package parent misses %q:\n%s", want, text)
		}
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
