package main

// Task 069: route-style source directories become valid Go package
// directories ([id] → id.param), so the user's module that contains out/
// and ssrhost/ builds and vets; names that cannot be encoded fail with
// VELA-SSR-PACKAGE-PATH instead of producing Go that breaks the module.

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSSRRouteDirectoriesBuildAsGoPackages(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not available")
	}
	p := &ssrHostProject{t: t, root: t.TempDir()}
	compilerRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	p.write("go.mod", "module example.com/site\n\ngo 1.25\n\n"+ssrRuntimeRequire(compilerRoot))
	p.write("vela.ssr.json", `{"module": "example.com/site/out/server", "host": {"module": "example.com/site/ssrhost"}}`)
	p.write("src/lib/Card.vela", "<script>export let title = '';</script>\n<template><h2>{title}</h2></template>\n")
	// A module under a group directory.
	p.write("src/lib/(admin)/format.js", "export function label(id) { return 'User ' + id; }\n")
	// A module under a parameter directory whose function needs a host.
	p.write("src/routes/users/[id=int]/util.js", "export function stamp(id) { return new Intl.NumberFormat('en').format(id); }\n")
	p.write("src/routes/users/[id=int]/Page.vela", `<script>
  import { Card } from '../../../lib/Card.vela';
  import { label } from '../../../lib/(admin)/format.js';
  import { stamp } from './util.js';
  export let id = '1';
</script>
<template><main><Card :title={label(id)} /><p>{stamp(id)}</p><button @click={() => {}}>x</button></main></template>
`)
	p.write("src/routes/users/[id=int]/posts/Page.vela", "<script>import { Page as User } from '../Page.vela';</script>\n<template><section><User id=\"7\" /></section></template>\n")
	for _, dir := range []string{"[id]", "[[lang]]/about", "docs/[...path]", "(admin)/settings"} {
		p.write("src/routes/"+dir+"/Page.vela", "<script>export let title = 'x';</script>\n<template><p>{title}</p></template>\n")
	}

	// The host is missing: the diagnostic and the scaffold name the encoded
	// host directory.
	errors, stderr := p.build(true)
	if errors == 0 || !strings.Contains(stderr, "ssrhost/src/routes/users/id.int.param/util/stamp_host.go") {
		t.Fatalf("missing host: %d errors\n%s", errors, stderr)
	}
	stub := filepath.Join(p.root, "ssrhost", "src", "routes", "users", "id.int.param", "util", "stamp_host.go")
	data, err := os.ReadFile(stub)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "package util") || !strings.Contains(string(data), "//vela:host stamp") {
		t.Fatalf("stub:\n%s", data)
	}
	p.write("ssrhost/src/routes/users/id.int.param/util/stamp_host.go", `package util

// Stamp implements stamp of src/routes/users/[id=int]/util.js.
//
//vela:host stamp
func Stamp(id string) (string, error) {
	return "#" + id, nil
}
`)
	if errors, stderr := p.build(false); errors != 0 {
		t.Fatalf("build: %d errors\n%s\nstub:\n%s", errors, stderr, data)
	}

	for _, dir := range []string{
		"out/server/components/routes/id.param",
		"out/server/components/routes/lang.opt/about",
		"out/server/components/routes/docs/path.rest",
		"out/server/components/routes/admin.group/settings",
		"out/server/components/routes/users/id.int.param/posts",
		"out/server/modules/lib/admin.group/format",
		"out/server/modules/routes/users/id.int.param/util",
	} {
		if _, err := os.Stat(filepath.Join(p.root, filepath.FromSlash(dir))); err != nil {
			t.Errorf("no package directory %s", dir)
		}
	}
	if page, err := os.ReadFile(filepath.Join(p.root, "out", "server", "components", "routes", "users", "id.int.param", "page.go")); err != nil || !strings.HasPrefix(string(page), "package id_int_param\n") {
		t.Errorf("package clause: %v\n%.80s", err, page)
	}
	// The client output and the manifest keep the source paths.
	if _, err := os.Stat(filepath.Join(p.root, "out", "client", "routes", "users", "[id=int]", "Page.js")); err != nil {
		t.Errorf("client module: %v", err)
	}

	p.write("cmd/render/main.go", `package main

import (
	"fmt"
	"os"

	user "example.com/site/out/server/components/routes/users/id.int.param"
	posts "example.com/site/out/server/components/routes/users/id.int.param/posts"
)

func main() {
	if err := user.RenderPageStatic(os.Stdout, user.PageProps{Set: map[string]bool{"id": true}, Id: "42"}, user.PageSlots{}); err != nil {
		panic(err)
	}
	fmt.Println()
	if err := posts.RenderPageStatic(os.Stdout, posts.PageProps{}, posts.PageSlots{}); err != nil {
		panic(err)
	}
}
`)
	for _, args := range [][]string{{"build", "./..."}, {"vet", "./..."}, {"run", "./cmd/render"}} {
		command := exec.Command("go", args...)
		command.Dir = p.root
		command.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, output)
		}
		if args[0] == "run" {
			want := "<main><h2>User 42</h2><p>#42</p><button>x</button></main>\n<section><main><h2>User 7</h2><p>#7</p><button>x</button></main></section>"
			if string(output) != want {
				t.Errorf("render:\n got %s\nwant %s", output, want)
			}
		}
	}
}

func TestSSRRejectsDirectoriesWithoutGoPackagePath(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	for _, dir := range []string{"[a]-[b]", "$id", "id.param", "Users", "users", "ok"} {
		file := filepath.Join(srcRoot, dir, "Page.vela")
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("<template><p>x</p></template>\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var errors int
	stderr := captureStderr(t, func() {
		errors = BuildSSRProject(CollectVela(srcRoot), srcRoot, filepath.Join(root, "out"), CompileOptions{StatusWriter: io.Discard, SSRModule: SSRDefaultModule}, false)
	})
	if errors != 5 {
		t.Errorf("%d errors, want 5\n%s", errors, stderr)
	}
	for _, want := range []string{
		`[a]-[b]/Page.vela: SSR: VELA-SSR-PACKAGE-PATH: directory "[a]-[b]": mixed segments are not supported`,
		`$id/Page.vela: SSR: VELA-SSR-PACKAGE-PATH: directory "$id": invalid character in a Go import path ("$")`,
		`id.param/Page.vela: SSR: VELA-SSR-PACKAGE-PATH: directory "id.param": reserved suffix of the encoding (".param")`,
		`for example "id"`,
		`VELA-SSR-PACKAGE-PATH: source directories Users and users both become the Go package directory users`,
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr misses %q:\n%s", want, stderr)
		}
	}
	// vela check (SSR) reports the same diagnostics, at the first line of
	// each file.
	diags, _, err := CheckTarget{SrcRoot: srcRoot, SSR: true, Opts: CompileOptions{SSRModule: SSRDefaultModule}}.Run()
	if err != nil {
		t.Fatal(err)
	}
	coded := 0
	for _, d := range diags {
		if d.Code == "VELA-SSR-PACKAGE-PATH" && d.Line == 1 && d.Col == 1 {
			coded++
		}
	}
	if coded != 5 {
		t.Errorf("%d VELA-SSR-PACKAGE-PATH diagnostics at 1:1, want 5", coded)
	}
	// Nothing was generated for the rejected directories.
	entries, _ := os.ReadDir(filepath.Join(root, "out", "server", "components"))
	var dirs []string
	for _, e := range entries {
		dirs = append(dirs, e.Name())
	}
	if strings.Join(dirs, ",") != "ok" {
		t.Errorf("generated package directories %v, want [ok]", dirs)
	}
}
