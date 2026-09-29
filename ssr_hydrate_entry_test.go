package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildSSRHydrateEntryAdoptsPayloadAndMarkerRanges(t *testing.T) {
	sfc, err := parseSFC(`<script>export let title="";</script><template><main>{title}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntry(plan)
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(entry, PrintPretty)
	for _, want := range []string{
		"import { $$acquireHydrationElement, $$createHydrationWalker, $$readHydrationPayload } from './runtime.js';",
		"export function hydratePage($$root, $$overrides, $$instanceID)",
		"export function Page($$root, $$overrides, $$instanceID)",
		"const $$hydratedInstances = new WeakMap();",
		"const $$scopeID = $$instanceID || 'vela-page';",
		"const $$existing = $$instances.get($$scopeID);",
		"if ($$existing) {",
		"return $$existing;",
		"$$createHydrationWalker($$root, $$scopeID)",
		"$$readHydrationPayload($$root, $$scopeID)",
		"const $$velaProps = { title: $$payload.props.Title };",
		"const $$componentProps = Object.assign({}, { title: $$payload.props.Title }, { $$velaState: $$payload.state, $$velaAwait: $$payload.await || {}, $$velaAwaitAt: $$payload.awaitAt || {} }, $$overrides || {});",
		"const $$hydration = { mode: 'static-only', root: $$root, props: $$velaProps, componentProps: $$componentProps, state: $$payload.state, deep: $$payload.deep || [], ranges: $$walker.ranges, elements: { e0: $$acquireHydrationElement($$walker, 0, 'main') } };",
		"$$instances.set($$scopeID, $$hydration);",
		"return $$hydration;",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("hydrate entry misses %q:\n%s", want, source)
		}
	}
}

func TestBuildSSRClientBoundaryEntryDeclaresNonExecutableMode(t *testing.T) {
	sfc, err := parseSFC(`<template><main>Fallback</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRClientBoundaryEntry(plan)
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(entry, PrintPretty)
	for _, want := range []string{
		"mode: 'client-boundary'",
		"$$createHydrationWalker($$root, $$scopeID)",
		"$$readHydrationPayload($$root, $$scopeID)",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("client-boundary entry misses %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, "const $$component =") {
		t.Fatalf("client-boundary entry must not claim an executable component:\n%s", source)
	}
}

func TestSSRProjectWritesHydrateEntryAlongsideClientModule(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(srcRoot, "App.vela")
	if err := os.WriteFile(source, []byte(`<script>export let title="";</script><template><main>{title}</main></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	if errors := buildSSRProject([]string{source}, srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	layout := NewSSRProjectLayout(outRoot)
	entry, err := os.ReadFile(layout.hydratePath(source, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(entry), "export function hydrateApp") {
		t.Fatalf("missing hydrate entry:\n%s", entry)
	}
}

func TestSSRHydrateEntryReleasesInstancePayloadOnDestroy(t *testing.T) {
	sfc, err := parseSFC(`<template><main>ok</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(entry, PrintPretty)
	for _, want := range []string{
		"const $$hydration = { mode: 'executable'",
		"const $$destroy = $$component.destroy;",
		"$$component.destroy = () => {",
		"$$instances.delete($$scopeID);",
		"$$hydratedInstances.delete($$root);",
		"document.getElementById('vela-state-' + $$scopeID)",
		"$$payloadNode.remove();",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("executable hydrate entry misses %q:\\n%s", want, source)
		}
	}
}

func TestGeneratedSSRHydrateEntryAdoptsExistingServerData(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is unavailable")
	}
	sfc, err := parseSFC(`<script>export let title="";</script><template><main>{title}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntry(plan)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Page.hydrate.js"), []byte(PrintJS(entry, PrintPretty)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "runtime.js"), embeddedRuntime, 0o644); err != nil {
		t.Fatal(err)
	}
	const runner = `import hydratePage from './Page.hydrate.js';
function assert(value, message) { if (!value) throw new Error(message); }
function marker(data) { return { data }; }
const element = { nodeType: 1, tagName: 'MAIN' };
const elementStart = marker('e:vela-page:0');
const elementEnd = marker('/e:vela-page:0');
elementStart.nextSibling = element;
element.nextSibling = elementEnd;
const comments = [elementStart, marker('v:vela-page:0'), marker('/v:vela-page:0'), elementEnd];
const payload = { type: 'application/json', textContent: '{"component":"vela-page","props":{"Title":"server"},"state":{"count":1},"deep":["settings"],"incomplete":false}' };
const document = {
  createTreeWalker() { let index = 0; return { nextNode() { return index < comments.length ? comments[index++] : null; } }; },
  getElementById(id) { return id === 'vela-state-vela-page' ? payload : null; },
};
const root = { ownerDocument: document };
const adopted = hydratePage(root);
const adoptedAgain = hydratePage(root);
assert(adoptedAgain === adopted, 'entry must reuse an already hydrated root and scope');
assert(adopted.mode === 'static-only', 'entry must expose static-only artifact mode');
assert(adopted.root === root, 'entry must retain the server root');
assert(adopted.props.title === 'server', 'entry must map Go payload fields back to Vela props');
assert(adopted.componentProps.title === 'server', 'component props must retain mapped Vela props');
assert(adopted.componentProps.$$velaState.count === 1, 'component props must expose restored state to the SSR client module');
assert(adopted.state.count === 1, 'entry must expose serialised state');
assert(adopted.deep.join(',') === 'settings', 'entry must preserve deep-state metadata');
assert(adopted.ranges.get(0).start === comments[1], 'entry must expose indexed marker ranges');
assert(adopted.elements.e0 === element, 'entry must acquire existing server element without creating it');
console.log('OK');
`
	if err := os.WriteFile(filepath.Join(dir, "run.mjs"), []byte(runner), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(node, "run.mjs")
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("hydrate entry runner: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("hydrate entry output=%q", got)
	}
}
