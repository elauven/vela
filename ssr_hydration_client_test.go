package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSSRProjectWritesExecutableHydrationClientForSupportedSubset(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(srcRoot, "Page.vela")
	if err := os.WriteFile(source, []byte(`<script>export let title='server';</script><template><p>{title}</p></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	if errors := buildSSRProject([]string{source}, srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	layout := NewSSRProjectLayout(outRoot)
	clientPath := layout.hydrationClientPath(source, srcRoot)
	client, err := os.ReadFile(clientPath)
	if err != nil {
		t.Fatalf("read hydration client: %v", err)
	}
	if !strings.Contains(string(client), "$$acquireHydrationRange($$walker, 0)") {
		t.Fatalf("hydration client misses text acquire:\n%s", client)
	}
	entry, err := os.ReadFile(layout.hydratePath(source, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(entry), "import Page from './Page.hydrate_client.js';") {
		t.Fatalf("hydrate entry does not invoke the supported client:\n%s", entry)
	}
}

func TestCanBuildSSRHydrationClientSubset(t *testing.T) {
	for _, source := range []struct {
		name string
		code string
		want bool
	}{
		{"root attributes and event", `<template><button class:active={selected} @click={toggle}>Save</button></template>`, true},
		{"direct dynamic text", `<template><p>{title}</p></template>`, true},
		{"nested dynamic text", `<template><main><p>{title}</p></main></template>`, false},
		{"nested dynamic attribute", `<template><main><button :disabled={saving}>Save</button></main></template>`, false},
		{"direct static if", `<template><main>{#if visible}<span>On</span>{:else}<span>Off</span>{/if}</main></template>`, true},
		{"if with dynamic branch", `<template><main>{#if visible}<span>{title}</span>{/if}</main></template>`, false},
		{"direct static each", `<template><main>{#each items as item}<span>Row</span>{/each}</main></template>`, true},
		{"static each else", `<template><main>{#each items as item}<span>Row</span>{:else}<span>Empty</span>{/each}</main></template>`, true},
		{"dynamic text each row", `<template><main>{#each items as item}<span>{item}</span>{/each}</main></template>`, true},
		{"keyed each", `<template><main>{#each items as item (item.id)}<span>Row</span>{/each}</main></template>`, false},
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

func TestSSRHydrationClientAdoptsStaticEachRangeWithoutInitialRender(t *testing.T) {
	sfc, err := parseSFC(`<script>export let items=[];</script><template><main>{#each items as item}<span>Row</span>{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"$$acquireHydrationRange($$walker, 0)",
		"$$adoptHydrationRange($$heach0_range)",
		"function $$heach0_update()",
		"const $$heach0_items = items;",
		"for (const $$item of $$heach0_items)",
		"$$heach0_range.end.before($$frag)",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("hydration each lowering misses %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, "\n  $$heach0_update();\n") || strings.Contains(source, "appendChild($$root)") {
		t.Fatalf("hydration each lowering unexpectedly performs initial DOM work:\n%s", source)
	}
}

func TestSSRHydrationClientBuildsDynamicEachRowsFromAST(t *testing.T) {
	sfc, err := parseSFC(`<script>export let items=[];</script><template><main>{#each items as item}<span class="row">{item}</span>{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"const $$row = document.createElement('span');",
		"$$row.appendChild(document.createTextNode(String($$item)));",
		"$$frag.appendChild($$row);",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("dynamic each row lowering misses %q:\n%s", want, source)
		}
	}
}

func TestSSRHydrationClientAdoptsStaticIfRangeWithoutInitialRender(t *testing.T) {
	sfc, err := parseSFC(`<script>export let visible=false;</script><template><main>{#if visible}<span>On</span>{:else}<span>Off</span>{/if}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"$$acquireHydrationRange($$walker, 0)",
		"$$adoptHydrationRange($$hif0_range)",
		"function $$hif0_update()",
		"$$hif0_nodes = Array.from($$frag.childNodes)",
		"$$hif0_range.end.before($$frag)",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("hydration if lowering misses %q:\n%s", want, source)
		}
	}
	for _, forbidden := range []string{
		"\n  $$hif0_update();\n",
		"appendChild($$root)",
		"document.createElement('main')",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("hydration if lowering unexpectedly performs initial DOM work through %q:\n%s", forbidden, source)
		}
	}
}

func TestBrowserSSRHydrationStaticIfAdoptsThenReplacesRange(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let visible=false; export let items=[];</script><template><main>{#if visible}<span class="state">On</span>{:else}<span class="state">Off</span>{/if}{#each items as item}<span class="row">{item}</span>{:else}<span class="empty">Empty</span>{/each}</main></template>`)
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
	entry, err := BuildSSRHydrateEntryWithClient(plan, "./Page.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":             embeddedRuntime,
		"Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)),
		"Page.hydrate.js":        []byte(PrintJS(entry, PrintPretty)),
		"index.html": []byte(`<!doctype html><body>
<!--e:vela-page:0--><main vela-page=""><!--v:vela-page:0--><!--e:vela-page:2--><span vela-page="" class="state">Off</span><!--/e:vela-page:2--><!--/v:vela-page:0--><!--v:vela-page:1--><!--e:vela-page:3--><span vela-page="" class="row"><!--v:vela-page:2-->1<!--/v:vela-page:2--></span><!--/e:vela-page:3--><!--e:vela-page:3--><span vela-page="" class="row"><!--v:vela-page:2-->2<!--/v:vela-page:2--></span><!--/e:vela-page:3--><!--/v:vela-page:1--></main><!--/e:vela-page:0-->
<script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{"Visible":false,"Items":[1,2]},"state":{},"incomplete":false}</script>
<script type="module">
  import hydratePage from './Page.hydrate.js';
  window.__hydrationObserver = new MutationObserver(() => {});
  window.__hydrationObserver.observe(document.body, { childList: true, subtree: true, attributes: true, characterData: true });
  window.__hydrated = hydratePage(document.body);
  window.__initialMutations = window.__hydrationObserver.takeRecords().length;
</script></body>`),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browserScript = `const fs = require('fs');
const path = require('path');
const http = require('http');
const { chromium } = require('playwright');
const root = process.argv[1];
const server = http.createServer((req, res) => {
  const name = req.url === '/' ? 'index.html' : req.url.slice(1);
  try { const body = fs.readFileSync(path.join(root, name)); res.writeHead(200, {'content-type': name.endsWith('.js') ? 'text/javascript' : 'text/html'}).end(body); }
  catch { res.writeHead(404).end(); }
});
server.listen(0, '127.0.0.1', async () => {
  let browser;
  try {
    browser = await chromium.launch({headless:true});
    const page = await browser.newPage();
    page.setDefaultTimeout(5000);
    const errors = [];
    page.on('pageerror', error => errors.push(String(error.stack || error)));
    page.on('console', message => { if (message.type() === 'error') errors.push(message.text()); });
    await page.goto('http://127.0.0.1:' + server.address().port + '/');
    try { await page.waitForFunction(() => window.__hydrated); }
    catch (error) { throw new Error(String(error.stack || error) + '\\n' + errors.join('\\n')); }
    const initial = await page.evaluate(() => window.__initialMutations);
    await page.evaluate(() => window.__hydrated.component.set({visible:true, items:[1]}));
    await page.waitForFunction(() => document.querySelector('.state')?.textContent === 'On' && document.querySelectorAll('.row').length === 1 && document.querySelector('.row')?.textContent === '1');
    const result = await page.evaluate(() => ({ initial: window.__initialMutations, text: document.querySelector('.state')?.textContent, stateCount: document.querySelectorAll('main > .state').length, rowCount: document.querySelectorAll('main > .row').length, rowText: document.querySelector('.row')?.textContent, emptyCount: document.querySelectorAll('main > .empty').length }));
    if (result.initial !== 0 || result.text !== 'On' || result.stateCount !== 1 || result.rowCount !== 1 || result.rowText !== '1' || result.emptyCount !== 0) throw new Error(JSON.stringify(result));
    process.stdout.write('OK');
  } catch (error) {
    console.error(error && error.stack || error);
    process.exitCode = 1;
  } finally {
    await browser?.close();
    server.close(() => process.exit());
  }
});`
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("browser hydration fixture: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("browser hydration fixture output=%q", got)
	}
}

func TestGeneratedSSRHydrationClientAdoptsElementAndHandlesEvent(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is unavailable")
	}
	sfc, err := parseSFC(`<script>export let title='server'; export let selected=false; function toggle() { selected = !selected; }</script><template><button class:selected={selected} @click={toggle}>{title}</button></template>`)
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
	if !strings.Contains(clientSource, "$$acquireHydrationRange($$walker, 0)") {
		t.Fatalf("hydration client does not acquire existing text marker range:\n%s", clientSource)
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
const textStart = marker('v:vela-page:0');
const text = { nodeType: 3, data: 'server' };
const textEnd = marker('/v:vela-page:0');
start.nextSibling = element;
element.nextSibling = end;
textStart.nextSibling = text;
text.nextSibling = textEnd;
const comments = [start, textStart, textEnd, end];
const payload = { type: 'application/json', textContent: '{"component":"vela-page","props":{"Title":"server","Selected":false},"state":{},"incomplete":false}' };
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
adopted.component.set({ title: 'client' });
await Promise.resolve();
assert(text.data === 'client', 'prop update must write only the adopted text range');
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
