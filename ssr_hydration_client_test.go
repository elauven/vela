package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanBuildSSRHydrationClientSubset(t *testing.T) {
	for _, source := range []struct {
		name string
		code string
		want bool
	}{
		{"root attributes and event", `<template><button class:active={selected} @click={toggle}>Save</button></template>`, true},
		{"dynamic text", `<template><p>{title}</p></template>`, false},
		{"nested dynamic attribute", `<template><main><button :disabled={saving}>Save</button></main></template>`, false},
		{"each block", `<template>{#each items as item}<p>{item}</p>{/each}</template>`, false},
		{"binding", `<template><input bind:value={query} /></template>`, false},
	} {
		t.Run(source.name, func(t *testing.T) {
			sfc, err := parseSFC(source.code)
			if err != nil {
				t.Fatal(err)
			}
			if got := CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes); got != source.want {
				t.Fatalf("CanBuildSSRHydrationClient()=%t, want %t", got, source.want)
			}
		})
	}
}

func TestGeneratedSSRHydrationClientAdoptsElementAndHandlesEvent(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is unavailable")
	}
	sfc, err := parseSFC(`<script>export let selected=false; function toggle() { selected = !selected; }</script><template><button class:selected={selected} @click={toggle}>Toggle</button></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	clientSource := PrintJS(client, PrintPretty)
	for _, forbidden := range []string{
		"document.createElement('button')",
		"appendChild($$root)",
		"$$root.setAttribute('vela-page', '')",
		"$$root.classList.toggle('selected', selected);",
	} {
		if strings.Contains(clientSource, forbidden) {
			t.Fatalf("hydration client unexpectedly writes initial DOM through %q:\n%s", forbidden, clientSource)
		}
	}
	if !strings.Contains(clientSource, "$$acquireHydrationElement($$walker, 0, 'button')") {
		t.Fatalf("hydration client does not acquire existing root element:\n%s", clientSource)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"package.json":           []byte(`{"type":"module"}`),
		"runtime.js":             embeddedRuntime,
		"Page.hydrate_client.js": []byte(clientSource),
		"Page.hydrate.js":        []byte(PrintJS(entry, PrintPretty)),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const runner = `import hydratePage from './Page.hydrate.js';
function assert(value, message) { if (!value) throw new Error(message); }
function marker(data) { return { data }; }
const listeners = new Map();
const toggles = [];
const element = {
  nodeType: 1, tagName: 'BUTTON',
  addEventListener(name, handler) { listeners.set(name, handler); },
  removeEventListener(name) { listeners.delete(name); },
  classList: { toggle(name, value) { toggles.push([name, value]); } },
  remove() {},
};
const start = marker('e:vela-page:0');
const end = marker('/e:vela-page:0');
start.nextSibling = element;
element.nextSibling = end;
const comments = [start, end];
const payload = { type: 'application/json', textContent: '{"component":"vela-page","props":{"Selected":false},"state":{},"incomplete":false}' };
const document = {
  createTreeWalker() { let index = 0; return { nextNode() { return index < comments.length ? comments[index++] : null; } }; },
  getElementById(id) { return id === 'vela-state-vela-page' ? payload : null; },
};
globalThis.document = document;
const root = { ownerDocument: document };
const adopted = hydratePage(root);
assert(adopted.component.el === element, 'factory must retain existing element');
assert(listeners.has('click'), 'factory must attach click listener');
listeners.get('click')({ target: element });
await Promise.resolve();
assert(toggles.length === 1 && toggles[0][0] === 'selected' && toggles[0][1] === true, 'click must update only the dynamic class');
console.log('OK');
`
	if err := os.WriteFile(filepath.Join(dir, "run.mjs"), []byte(runner), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(node, "run.mjs")
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("hydration client runner: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("hydration client output=%q", got)
	}
}
