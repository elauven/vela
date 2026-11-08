package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestClientOnlyComponentValue: a client-only component (<script
// context="client">) chosen by <vela:component> or loaded with lazy() is
// vela.ClientComponent on the server, named by its component ID relative
// to the project (task 092), so a page that reads browser APIs while it
// starts builds with --ssr and renders an empty client range.
func TestClientOnlyComponentValue(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not available")
	}
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	for name, source := range map[string]string{
		"App.vela": `<script>
  import { lazy } from 'vela';
  import { Clock } from './widgets/Clock.vela';
  const Notes = lazy(() => import('./routes/notes/Page.vela'));
  export let which = 'notes';
</script>
<template><main><vela:component :this={which === 'notes' ? Notes : Clock} /></main></template>`,
		"routes/notes/Page.vela": `<script context="client">
  let width = $state(window.innerWidth);
</script>
<template><p>{width}</p></template>`,
		"widgets/Clock.vela": `<script context="client">
  let now = $state(Date.now());
</script>
<template><time>{now}</time></template>`,
	} {
		writeTestFile(t, filepath.Join(srcRoot, filepath.FromSlash(name)), source)
	}
	outRoot := filepath.Join(root, "out")
	var stderr strings.Builder
	errors := 0
	stderr.WriteString(captureStderr(t, func() {
		errors = BuildSSRProject(CollectVela(srcRoot), srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard, SSRModule: "example.com/client/server"}, false)
	}))
	if errors != 0 || strings.Contains(stderr.String(), "VELA-SSR") {
		t.Fatalf("SSR project errors=%d\n%s", errors, stderr.String())
	}
	server, err := os.ReadFile(filepath.Join(outRoot, "server", "components", "app.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`vela.ClientComponent("routes/notes/Page")`, `vela.ClientComponent("widgets/Clock")`} {
		if !strings.Contains(string(server), want) {
			t.Errorf("app.go misses %s:\n%s", want, server)
		}
	}
}
