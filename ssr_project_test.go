package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSSRArtifactManifestPreservesClientBoundaryMode(t *testing.T) {
	source, err := json.Marshal(SSRArtifactManifest{
		Component: "Page",
		Mode:      SSRArtifactClientBoundary,
		Hydrate:   "Page.hydrate.js",
	})
	if err != nil {
		t.Fatal(err)
	}
	var manifest SSRArtifactManifest
	if err := json.Unmarshal(source, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Mode != SSRArtifactClientBoundary || manifest.Hydrate != "Page.hydrate.js" {
		t.Fatalf("client-boundary manifest=%#v", manifest)
	}
}

func TestSSRProjectRejectsSingleSelectWithKnownNullBinding(t *testing.T) {
	for _, contents := range []string{
		`<script>let choice = $state(null);</script><template><select bind:value={choice}><option value="one">One</option><option value="two">Two</option></select></template>`,
		`<script>let choice = $derived(null);</script><template><select bind:value={choice}><option value="one">One</option><option value="two">Two</option></select></template>`,
	} {
		root := t.TempDir()
		srcRoot := filepath.Join(root, "src")
		if err := os.MkdirAll(srcRoot, 0o755); err != nil {
			t.Fatal(err)
		}
		source := filepath.Join(srcRoot, "App.vela")
		if err := os.WriteFile(source, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
		layout := NewSSRProjectLayout(filepath.Join(root, "out"))
		err := compileSSRProjectFile(source, srcRoot, layout)
		if err == nil || !strings.Contains(err.Error(), SSRExpressionDiagnosticCode) || !strings.Contains(err.Error(), "known null server value") {
			t.Fatalf("single-select null SSR error=%v", err)
		}
		if _, statErr := os.Stat(layout.serverPath(source, srcRoot)); !os.IsNotExist(statErr) {
			t.Fatalf("single-select null SSR unexpectedly wrote server artifact: %v", statErr)
		}
	}
}

func TestSSRProjectRejectsLooseEqualityBeforeWritingServerArtifact(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(srcRoot, "App.vela")
	if err := os.WriteFile(source, []byte(`<template><p>{1 == '1'}</p></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	layout := NewSSRProjectLayout(filepath.Join(root, "out"))
	err := compileSSRProjectFile(source, srcRoot, layout)
	if err == nil || !strings.Contains(err.Error(), SSRExpressionDiagnosticCode) || !strings.Contains(err.Error(), "loose equality") {
		t.Fatalf("loose equality SSR error=%v", err)
	}
	if _, statErr := os.Stat(layout.serverPath(source, srcRoot)); !os.IsNotExist(statErr) {
		t.Fatalf("loose equality unexpectedly wrote server artifact: %v", statErr)
	}
}

func runSSRGeneratedProgram(root string) ([]byte, error) {
	command := exec.Command("go", "run", ".")
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("generated SSR go run: %w\n%s", err, output)
	}
	return output, nil
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
	// The generated sidecar has Set: defaults read it directly (task 060).
	assertServerContains := func(want, absent string) {
		t.Helper()
		server, err := os.ReadFile(layout.serverPath(source, srcRoot))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(server), want) || strings.Contains(string(server), absent) {
			t.Fatalf("renderer: want %q, not %q:\n%s", want, absent, server)
		}
	}
	assertServerContains(`if props.Set != nil && !props.Set["title"] {`, "vela.PropProvided")
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
	}	// A sidecar without Set keeps the reflective compatibility check.
	assertServerContains(`if !vela.PropProvided(props, "title") {`, "props.Set")
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
	if !strings.Contains(string(server), "Child(ChildProps{Title: props.Title, Set: map[string]bool{\"title\": true}}, ChildSlots{}).Render(w)") {
		t.Fatalf("child call was not generated:\n%s", server)
	}
}

func TestSSRProjectInternalChildCallRetainsOmittedPropDefault(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(srcRoot, "Parent.vela")
	child := filepath.Join(srcRoot, "Child.vela")
	if err := os.WriteFile(parent, []byte(`<script>import { Child } from './Child.vela';</script><template><Child /></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte(`<script>export let title = 'fallback';</script><template><p>{title}</p></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	projectModule := "example.com/ssrchilddefault"
	if errors := buildSSRProject([]string{parent, child}, srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard, SSRModule: projectModule + "/server"}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	parentSource, err := os.ReadFile(NewSSRProjectLayoutWithModule(outRoot, projectModule+"/server").serverPath(parent, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(parentSource), "Child(ChildProps{Set: map[string]bool{}}, ChildSlots{}).Render(w)") {
		t.Fatalf("internal omitted prop call misses initialized presence map:\n%s", parentSource)
	}
	compilerRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	module := "module " + projectModule + "\n\ngo 1.25\n\n" + ssrRuntimeRequire(compilerRoot)
	if err := os.WriteFile(filepath.Join(outRoot, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	mainSource := `package main
import (
    "os"
    components "example.com/ssrchilddefault/server/components"
)
func main() {
    if err := components.RenderParent(os.Stdout, components.ParentProps{}, components.ParentSlots{}); err != nil { panic(err) }
}
`
	if err := os.WriteFile(filepath.Join(outRoot, "main.go"), []byte(mainSource), 0o644); err != nil {
		t.Fatal(err)
	}
	output, err := runSSRGeneratedProgram(outRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), ">fallback</p>") {
		t.Fatalf("omitted child prop did not render its default:\n%s", output)
	}
}

func TestSSRProjectInternalChildCallPreservesExplicitZeroValues(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(srcRoot, "Parent.vela")
	child := filepath.Join(srcRoot, "Child.vela")
	if err := os.WriteFile(parent, []byte(`<script>import { Child } from './Child.vela';</script><template><Child :title={''} :enabled={false} :count={0} /></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte(`<script>export let title = 'fallback'; export let enabled = true; export let count = 7;</script><template><p class="title">{title}</p>{#if enabled}<p class="enabled">yes</p>{:else}<p class="enabled">no</p>{/if}<p class="count">{count}</p></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	projectModule := "example.com/ssrchildzeros"
	if errors := buildSSRProject([]string{parent, child}, srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard, SSRModule: projectModule + "/server"}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	parentSource, err := os.ReadFile(NewSSRProjectLayoutWithModule(outRoot, projectModule+"/server").serverPath(parent, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`Title: ""`, `Enabled: false`, `Count: 0`, `Set: map[string]bool{"title": true, "enabled": true, "count": true}`} {
		if !strings.Contains(string(parentSource), want) {
			t.Fatalf("explicit child prop call misses %q:\n%s", want, parentSource)
		}
	}
	compilerRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	module := "module " + projectModule + "\n\ngo 1.25\n\n" + ssrRuntimeRequire(compilerRoot)
	if err := os.WriteFile(filepath.Join(outRoot, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	mainSource := `package main
import (
    "os"
    components "example.com/ssrchildzeros/server/components"
)
func main() {
    if err := components.RenderParent(os.Stdout, components.ParentProps{}, components.ParentSlots{}); err != nil { panic(err) }
}
`
	if err := os.WriteFile(filepath.Join(outRoot, "main.go"), []byte(mainSource), 0o644); err != nil {
		t.Fatal(err)
	}
	output, err := runSSRGeneratedProgram(outRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`>no</p>`, `>0</p>`} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("explicit zero prop semantics miss %q:\n%s", want, output)
		}
	}
	if strings.Contains(string(output), "fallback") || strings.Contains(string(output), ">yes<") {
		t.Fatalf("explicit child zero values were replaced by defaults:\n%s", output)
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
	for _, want := range []string{"ChildProps{Set: map[string]bool{}", "ChildSlots{", "Default: func(w io.Writer) error", "Actions: func(w io.Writer) error"} {
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
		"ui.Child(ui.ChildProps{Title: props.Title, Set: map[string]bool{\"title\": true}}, ui.ChildSlots{",
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
