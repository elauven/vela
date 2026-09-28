package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildSSRStaticEventBridgeUsesNativeListener(t *testing.T) {
	sfc, err := parseSFC(`<template><button @click={() => alert("done")}>Save</button></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := BuildSSRStaticEventBridge(plan)
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(bridge, PrintPretty)
	for _, want := range []string{
		"export function attachSSRStaticEvents(root)",
		`querySelectorAll('[data-vela-event]')`,
		`addEventListener($$entry.event, $$entry.handler)`,
		`'vela-page-e0'`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("bridge is missing %q:\n%s", want, source)
		}
	}
}

func TestBuildSSRStaticEventBridgeRejectsReactiveHandler(t *testing.T) {
	sfc, err := parseSFC(`<script>let count = $state(0);</script><template><button @click={() => count++}>Save</button></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	_, err = BuildSSRStaticEventBridge(plan)
	problem, ok := err.(*SSRStaticEventError)
	if !ok || !strings.Contains(problem.Error(), SSRStaticEventDiagnosticCode) {
		t.Fatalf("error=%T %v", err, err)
	}
}

func TestBuildSSRStaticEventBridgeRejectsClosureHandler(t *testing.T) {
	sfc, err := parseSFC(`<script>function save() {}</script><template><button @click={save}>Save</button></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	_, err = BuildSSRStaticEventBridge(plan)
	if err == nil || !strings.Contains(err.Error(), SSRStaticEventDiagnosticCode) {
		t.Fatalf("err=%v", err)
	}
}

func TestSSRStaticEventBridgeAttachesListenerInNode(t *testing.T) {
	sfc, err := parseSFC(`<template><button @click={() => alert("done")}>Save</button></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := BuildSSRStaticEventBridge(plan)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "Page.ssr-events.mjs")
	if err := os.WriteFile(path, []byte(PrintJS(bridge, PrintPretty)), 0o644); err != nil {
		t.Fatal(err)
	}
	script := `const { attachSSRStaticEvents } = await import("file://" + process.argv[1]);
const calls = [];
const node = {
  getAttribute: () => "vela-page-e0",
  addEventListener: (event, handler) => calls.push([event, typeof handler])
};
attachSSRStaticEvents({ querySelectorAll: () => [node] });
if (JSON.stringify(calls) !== "[[\"click\",\"function\"]]") process.exit(1);
`
	command := exec.Command("node", "--input-type=module", "-e", script, path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("node bridge: %v\n%s", err, output)
	}
}

func TestCompileSSRProjectFileUsesExecutableHydrationForReactiveEvent(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "App.vela")
	content := `<script>export let count = 0; function increment() { count++; }</script><template><button @click={increment}>{count}</button></template>`
	if err := os.WriteFile(source, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	layout := NewSSRProjectLayout(filepath.Join(root, "out"))
	if err := compileSSRProjectFile(source, root, layout); err != nil {
		t.Fatal(err)
	}
	client, err := os.ReadFile(layout.hydrationClientPath(source, root))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(client), "addEventListener('click'") {
		t.Fatalf("executable hydration client misses click listener:\n%s", client)
	}
	if _, err := os.Stat(layout.eventBridgePath(source, root)); !os.IsNotExist(err) {
		t.Fatalf("executable hydration must not emit static event bridge, stat err=%v", err)
	}
}

func TestBuildSSRStaticEventBridgeRejectsLexicalThis(t *testing.T) {
	sfc, err := parseSFC(`<template><button @click={() => this.count++}>Save</button></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	_, err = BuildSSRStaticEventBridge(plan)
	if err == nil || !strings.Contains(err.Error(), SSRStaticEventDiagnosticCode) {
		t.Fatalf("err=%v", err)
	}
}
