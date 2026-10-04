package main

import (
	"errors"
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
	if !strings.Contains(string(client), "$$addHydrationListener($$transaction,") {
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

func TestCompileSSRProjectFileReportsHydrationBlockerBeforeBridgeError(t *testing.T) {
	root := t.TempDir()
	list := filepath.Join(root, "List.vela")
	listSource := `<script>
  export let items = [];
  let selected = $state(null);
  function pick(item) { selected = item.id; }
</script>
<template>
  <ul>
    {#each items as item (item.id)}
      <li class:active={selected === item.id} @click={() => pick(item)}>{item.name}</li>
    {:else}
      <li>empty</li>
    {/each}
  </ul>
</template>`
	if err := os.WriteFile(list, []byte(listSource), 0o644); err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(root, "Page.vela")
	pageSource := `<script>
  import { List } from './List.vela';
  export let items = [];
  let open = $state(true);
</script>
<template>
  <main>
    <List :items={items} />
    <button @click={() => open = !open}>toggle</button>
  </main>
</template>`
	if err := os.WriteFile(page, []byte(pageSource), 0o644); err != nil {
		t.Fatal(err)
	}
	layout := NewSSRProjectLayout(filepath.Join(root, "out"))

	err := compileSSRProjectFile(list, root, layout)
	var blocked *SSRHydrationBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("list error = %T %v, want SSRHydrationBlockedError", err, err)
	}
	if want := list + ":8:5: <each>"; !strings.Contains(blocked.Reason, want) {
		t.Fatalf("list reason = %q, want position %q", blocked.Reason, want)
	}
	if !strings.HasPrefix(err.Error(), SSRHydrationBlockedDiagnosticCode) {
		t.Fatalf("list error must lead with the hydration reason: %v", err)
	}
	var bridge *SSRStaticEventError
	if !errors.As(err, &bridge) {
		t.Fatalf("list error must keep the bridge error: %v", err)
	}

	err = compileSSRProjectFile(page, root, layout)
	if !errors.As(err, &blocked) || !strings.Contains(blocked.Reason, "child component <List> cannot hydrate") {
		t.Fatalf("page error = %v, want the blocking child component", err)
	}
}
