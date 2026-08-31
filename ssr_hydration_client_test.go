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
		{"nested dynamic text", `<template><main><p>{title}</p></main></template>`, true},
		{"nested dynamic attribute", `<template><main><button :disabled={saving}>Save</button></main></template>`, true},
		{"direct static if", `<template><main>{#if visible}<span>On</span>{:else}<span>Off</span>{/if}</main></template>`, true},
		{"nested static if and each", `<template><main><section>{#if visible}<span>On</span>{:else}<span>Off</span>{/if}{#each items as item}<span>{item}</span>{/each}</section></main></template>`, true},
		{"direct static key", `<template><main>{#key identity}<span>Row</span>{/key}</main></template>`, true},
		{"if with dynamic branch", `<template><main>{#if visible}<span>{title}</span>{/if}</main></template>`, false},
		{"direct static each", `<template><main>{#each items as item}<span>Row</span>{/each}</main></template>`, true},
		{"static each else", `<template><main>{#each items as item}<span>Row</span>{:else}<span>Empty</span>{/each}</main></template>`, true},
		{"dynamic text each row", `<template><main>{#each items as item}<span>{item}</span>{/each}</main></template>`, true},
		{"keyed each", `<template><main>{#each items as item (item.id)}<span>Row</span>{/each}</main></template>`, true},
		{"keyed each else", `<template><main>{#each items as item (item.id)}<span>Row</span>{:else}<span>Empty</span>{/each}</main></template>`, false},
		{"each block", `<template>{#each items as item}<p>{item}</p>{/each}</template>`, false},
		{"binding", `<template><input bind:value={query} /></template>`, true},
		{"select binding", `<template><select bind:value={choice}><option value="a">A</option></select></template>`, true},
		{"element reference", `<template><input bind:this={input} /></template>`, true},
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

func TestSSRHydrationClientAdoptsKeyedEachRowsFromCachedAST(t *testing.T) {
	sfc, err := parseSFC(`<script>export let items=[];</script><template><main>{#each items as item, index (item.id + ':' + index)}<button class="row">{item.label}</button>{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	block := sfc.TemplateAST.Nodes[0].(*ElementNode).Children[0].(*EachBlockNode)
	if block.KeyAST == nil {
		t.Fatal("parser did not cache the keyed each expression")
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"$$adoptHydrationKeyedRows($$heach0_range, $$heach0_items, $$heach0_key, 'button')",
		"const $$heach0_key = ($$item, $$index) => String($$item.id + ':' + $$index);",
		"const $$heach0_create = ($$item, $$index) => {",
		"$$row.appendChild(document.createTextNode(String($$item.label)));",
		"$$row.textContent = String($$item.label);",
		"$$clearHydrationKeyedRowMarkers($$heach0_range);",
		"$$heach0_blocks = $$reconcileHydrationKeyedRows($$heach0_range, $$heach0_blocks, items, $$heach0_key, $$heach0_create, $$heach0_row_update);",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("keyed hydration lowering misses %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, "\n  $$heach0_update();\n") || strings.Contains(source, "appendChild($$root)") {
		t.Fatalf("keyed hydration lowering unexpectedly performs initial DOM work:\n%s", source)
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

func TestSSRHydrationClientAdoptsStaticKeyRangeWithoutInitialRender(t *testing.T) {
	sfc, err := parseSFC(`<script>export let identity='a';</script><template><main>{#key identity}<span class="row">Row</span>{/key}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"$$adoptHydrationRange($$hkey0_range)",
		"let $$hkey0_prev = identity;",
		"function $$hkey0_update()",
		"if ($$key === $$hkey0_prev)",
		"$$hkey0_range.end.before($$frag)",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("hydration key lowering misses %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, "\n  $$hkey0_update();\n") || strings.Contains(source, "appendChild($$root)") {
		t.Fatalf("hydration key lowering unexpectedly performs initial DOM work:\n%s", source)
	}
}

func TestSSRHydrationClientAdoptsNestedTextRangeWithoutInitialRender(t *testing.T) {
	sfc, err := parseSFC(`<script>export let title='server';</script><template><main><section><p class="title">{title}</p></section></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"const $$n1 = $$acquireHydrationElement($$walker, 1, 'section');",
		"const $$n2 = $$acquireHydrationElement($$walker, 2, 'p');",
		"const $$h0_range = $$acquireHydrationRange($$walker, 0);",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("nested text hydration lowering misses %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, "appendChild($$root)") || strings.Contains(source, "document.createElement('section')") {
		t.Fatalf("nested text hydration lowering unexpectedly creates DOM:\n%s", source)
	}
}

func TestSSRHydrationClientDefersNestedAttributeWriteUntilUpdate(t *testing.T) {
	sfc, err := parseSFC(`<script>export let title='server';</script><template><main><p :title={title}>{title}</p></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"const $$n1 = $$acquireHydrationElement($$walker, 1, 'p');",
		"$$n1.setAttribute('title', String(title));",
		"$$writeHydrationText($$h0_range, String(title));",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("nested attribute hydration lowering misses %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, "\n  $$n1.setAttribute('title', String(title));\n") {
		t.Fatalf("nested attribute hydration lowering writes before a reactive update:\n%s", source)
	}
}

func TestSSRHydrationClientAssignsBindThisAfterAdoption(t *testing.T) {
	sfc, err := parseSFC(`<script>let input=$state(null);</script><template><input bind:this={input} /></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"const $$root = $$acquireHydrationElement($$walker, 0, 'input');",
		"$$set_input($$root);",
		"$$set_input(null);",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("bind:this hydration lowering misses %q:\n%s", want, source)
		}
	}
}

func TestBrowserSSRHydrationSelectBindingAdoptsLiveValue(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let choice="a";</script><template><main><select class="choice" bind:value={choice}><option value="a">A</option><option value="b">B</option></select><p class="output">{choice}</p></main></template>`)
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
<!--e:vela-page:0--><main vela-page=""><!--e:vela-page:1--><select vela-page="" class="choice"><!--e:vela-page:2--><option vela-page="" value="a" selected>A</option><!--/e:vela-page:2--><!--e:vela-page:3--><option vela-page="" value="b">B</option><!--/e:vela-page:3--></select><!--/e:vela-page:1--><!--e:vela-page:4--><p vela-page="" class="output"><!--v:vela-page:0-->a<!--/v:vela-page:0--></p><!--/e:vela-page:4--></main><!--/e:vela-page:0-->
<script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{"Choice":"a"},"state":{},"incomplete":false}</script>
<script type="module">
  import hydratePage from "./Page.hydrate.js";
  if (new URLSearchParams(location.search).has("pre")) document.querySelector(".choice").value = "b";
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
	const browserScript = `const fs = require("fs");
const path = require("path");
const http = require("http");
const { chromium } = require("playwright");
const root = process.argv[1];
const server = http.createServer((req, res) => {
  const requestPath = req.url.split("?")[0];
  const name = requestPath === "/" ? "index.html" : requestPath.slice(1);
  try { const body = fs.readFileSync(path.join(root, name)); res.writeHead(200, {"content-type": name.endsWith(".js") ? "text/javascript" : "text/html"}).end(body); }
  catch { res.writeHead(404).end(); }
});
server.listen(0, "127.0.0.1", async () => {
  let browser;
  try {
    browser = await chromium.launch({headless:true});
    const page = await browser.newPage();
    page.setDefaultTimeout(5000);
    await page.goto("http://127.0.0.1:" + server.address().port + "/");
    await page.waitForFunction(() => window.__hydrated);
    await page.evaluate(() => { window.__choice = document.querySelector(".choice"); });
    await page.selectOption(".choice", "b");
    await page.waitForFunction(() => document.querySelector(".output")?.textContent === "b");
    const result = await page.evaluate(() => ({ initial: window.__initialMutations, same: document.querySelector(".choice") === window.__choice, value: document.querySelector(".choice")?.value, output: document.querySelector(".output")?.textContent }));
    if (result.initial !== 0 || !result.same || result.value !== "b" || result.output !== "b") throw new Error(JSON.stringify(result));
    await page.goto("http://127.0.0.1:" + server.address().port + "/?pre=1");
    await page.waitForFunction(() => window.__hydrated && document.querySelector(".output")?.textContent === "b");
    const pre = await page.evaluate(() => ({ value: document.querySelector(".choice")?.value, output: document.querySelector(".output")?.textContent }));
    if (pre.value !== "b" || pre.output !== "b") throw new Error(JSON.stringify(pre));
    process.stdout.write("OK");
  } catch (error) {
    console.error(error && error.stack || error);
    process.exitCode = 1;
  } finally {
    await browser?.close();
    server.close(() => process.exit());
  }
});
`
	run := exec.Command(node, "-e", browserScript, dir)
	output, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("select hydration browser fixture: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("select hydration browser output=%q", got)
	}
}

func TestBrowserSSRHydrationBindingsAdoptLiveControlsThenUpdateState(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let title='server'; export let done=false; export let note='server note';</script><template><main><input class="input" bind:value={title} /><input class="check" type="checkbox" bind:checked={done} /><textarea class="note" bind:value={note} /><p class="output">{title}:{done}</p><p class="note-output">{note}</p></main></template>`)
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
<!--e:vela-page:0--><main vela-page=""><!--e:vela-page:1--><input vela-page="" class="input" value="server"><!--/e:vela-page:1--><!--e:vela-page:2--><input vela-page="" class="check" type="checkbox"><!--/e:vela-page:2--><!--e:vela-page:3--><textarea vela-page="" class="note">server note</textarea><!--/e:vela-page:3--><!--e:vela-page:4--><p vela-page="" class="output"><!--v:vela-page:0-->server<!--/v:vela-page:0-->:<!--v:vela-page:1-->false<!--/v:vela-page:1--></p><!--/e:vela-page:4--><!--e:vela-page:5--><p vela-page="" class="note-output"><!--v:vela-page:2-->server note<!--/v:vela-page:2--></p><!--/e:vela-page:5--></main><!--/e:vela-page:0-->
<script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{"Title":"server","Done":false,"Note":"server note"},"state":{},"incomplete":false}</script>
<script type="module">
  import hydratePage from './Page.hydrate.js';
  if (new URLSearchParams(location.search).has('pre')) document.querySelector('.input').value = 'typed-before-hydration';
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
  const requestPath = req.url.split('?')[0];
  const name = requestPath === '/' ? 'index.html' : requestPath.slice(1);
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
    catch (error) { throw new Error(String(error.stack || error) + '\n' + errors.join('\n')); }
    await page.evaluate(() => { window.__input = document.querySelector('.input'); window.__check = document.querySelector('.check'); window.__note = document.querySelector('.note'); });
    const initial = await page.evaluate(() => window.__initialMutations);
    await page.locator('.input').fill('client');
    await page.waitForFunction(() => document.querySelector('.output')?.textContent === 'client:false');
    await page.locator('.check').check();
    await page.waitForFunction(() => document.querySelector('.output')?.textContent === 'client:true');
    await page.locator('.note').fill('client note');
    await page.waitForFunction(() => document.querySelector('.note-output')?.textContent === 'client note');
    const result = await page.evaluate(() => ({ initial: window.__initialMutations, sameInput: document.querySelector('.input') === window.__input, sameCheck: document.querySelector('.check') === window.__check, sameNote: document.querySelector('.note') === window.__note, value: document.querySelector('.input')?.value, checked: document.querySelector('.check')?.checked, note: document.querySelector('.note')?.value, text: document.querySelector('.output')?.textContent, noteText: document.querySelector('.note-output')?.textContent }));
    if (result.initial !== 0 || !result.sameInput || !result.sameCheck || !result.sameNote || result.value !== 'client' || !result.checked || result.note !== 'client note' || result.text !== 'client:true' || result.noteText !== 'client note') throw new Error(JSON.stringify(result));
    await page.goto('http://127.0.0.1:' + server.address().port + '/?pre=1');
    await page.waitForFunction(() => window.__hydrated && document.querySelector('.output')?.textContent === 'typed-before-hydration:false');
    const preHydration = await page.evaluate(() => ({ value: document.querySelector('.input')?.value, text: document.querySelector('.output')?.textContent }));
    if (preHydration.value !== 'typed-before-hydration' || preHydration.text !== 'typed-before-hydration:false') throw new Error(JSON.stringify(preHydration));
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
		t.Fatalf("browser binding hydration fixture: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("browser binding hydration fixture output=%q", got)
	}
}

func TestBrowserSSRHydrationNestedTextAttributeAndEventAdoptThenUpdate(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let title='server'; export let selected=false; export let color='red'; function change() { title = 'client'; selected = !selected; color = 'blue'; }</script><template><main><section><p class="title" :title={title} class:selected={selected} style:color={color} @click={change}>{title}</p></section></main></template>`)
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
<!--e:vela-page:0--><main vela-page=""><!--e:vela-page:1--><section vela-page=""><!--e:vela-page:2--><p vela-page="" class="title" title="server" style="color: red;"><!--v:vela-page:0-->server<!--/v:vela-page:0--></p><!--/e:vela-page:2--></section><!--/e:vela-page:1--></main><!--/e:vela-page:0-->
<script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{"Title":"server","Selected":false,"Color":"red"},"state":{},"incomplete":false}</script>
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
    catch (error) { throw new Error(String(error.stack || error) + '\n' + errors.join('\n')); }
    await page.evaluate(() => { window.__paragraph = document.querySelector('.title'); });
    const initial = await page.evaluate(() => window.__initialMutations);
    await page.evaluate(() => document.querySelector('.title').click());
    await page.waitForFunction(() => document.querySelector('.title')?.textContent === 'client' && document.querySelector('.title')?.getAttribute('title') === 'client' && document.querySelector('.title')?.classList.contains('selected') && document.querySelector('.title')?.style.color === 'blue');
    const result = await page.evaluate(() => ({ initial: window.__initialMutations, sameParagraph: document.querySelector('.title') === window.__paragraph, text: document.querySelector('.title')?.textContent, title: document.querySelector('.title')?.getAttribute('title'), selected: document.querySelector('.title')?.classList.contains('selected'), color: document.querySelector('.title')?.style.color }));
    if (result.initial !== 0 || !result.sameParagraph || result.text !== 'client' || result.title !== 'client' || !result.selected || result.color !== 'blue') throw new Error(JSON.stringify(result));
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
		t.Fatalf("browser nested text hydration fixture: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("browser nested text hydration fixture output=%q", got)
	}
}

func TestBrowserSSRHydrationStaticKeyAdoptsThenReplacesRange(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let identity='a';</script><template><main>{#key identity}<span class="row">Row</span>{/key}</main></template>`)
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
<!--e:vela-page:0--><main vela-page=""><!--v:vela-page:0--><!--e:vela-page:1--><span vela-page="" class="row">Row</span><!--/e:vela-page:1--><!--/v:vela-page:0--></main><!--/e:vela-page:0-->
<script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{"Identity":"a"},"state":{},"incomplete":false}</script>
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
    catch (error) { throw new Error(String(error.stack || error) + '\n' + errors.join('\n')); }
    await page.evaluate(() => { window.__initialRow = document.querySelector('.row'); });
    const initial = await page.evaluate(() => window.__initialMutations);
    await page.evaluate(() => window.__hydrated.component.set({identity:'b'}));
    await page.waitForFunction(() => document.querySelector('.row') !== window.__initialRow);
    const result = await page.evaluate(() => ({ initial: window.__initialMutations, count: document.querySelectorAll('.row').length, text: document.querySelector('.row')?.textContent }));
    if (result.initial !== 0 || result.count !== 1 || result.text !== 'Row') throw new Error(JSON.stringify(result));
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
		t.Fatalf("browser key hydration fixture: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("browser key hydration fixture output=%q", got)
	}
}

func TestBrowserSSRHydrationNestedStaticBlocksAdoptThenReplaceRanges(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let visible=false; export let items=[];</script><template><main><section>{#if visible}<span class="state">On</span>{:else}<span class="state">Off</span>{/if}{#each items as item}<span class="row">{item}</span>{:else}<span class="empty">Empty</span>{/each}</section></main></template>`)
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
<!--e:vela-page:0--><main vela-page=""><!--e:vela-page:1--><section vela-page=""><!--v:vela-page:0--><!--e:vela-page:3--><span vela-page="" class="state">Off</span><!--/e:vela-page:3--><!--/v:vela-page:0--><!--v:vela-page:1--><!--e:vela-page:4--><span vela-page="" class="row"><!--v:vela-page:2-->1<!--/v:vela-page:2--></span><!--/e:vela-page:4--><!--e:vela-page:4--><span vela-page="" class="row"><!--v:vela-page:2-->2<!--/v:vela-page:2--></span><!--/e:vela-page:4--><!--/v:vela-page:1--></section><!--/e:vela-page:1--></main><!--/e:vela-page:0-->
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
    const result = await page.evaluate(() => ({ initial: window.__initialMutations, text: document.querySelector('.state')?.textContent, stateCount: document.querySelectorAll('main section > .state').length, rowCount: document.querySelectorAll('main section > .row').length, rowText: document.querySelector('.row')?.textContent, emptyCount: document.querySelectorAll('main section > .empty').length }));
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

func TestBrowserSSRHydrationKeyedEachRetainsFocusedRowAcrossDeepReconcile(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>let items=$state.deep([{id:'a',label:'Alpha'},{id:'b',label:'Beta'}]); function updateRows() { items[1].label = 'Beta*'; items = [items[1], {id:'c',label:'Gamma'}]; }</script><template><main @click={updateRows}>{#each items as item (item.id)}<button class="row">{item.label}</button>{/each}</main></template>`)
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
<!--e:vela-page:0--><main vela-page=""><!--v:vela-page:0--><!--e:vela-page:1--><button vela-page="" class="row"><!--v:vela-page:2-->Alpha<!--/v:vela-page:2--></button><!--/e:vela-page:1--><!--e:vela-page:1--><button vela-page="" class="row"><!--v:vela-page:2-->Beta<!--/v:vela-page:2--></button><!--/e:vela-page:1--><!--/v:vela-page:0--></main><!--/e:vela-page:0-->
<script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{"items":[{"id":"a","label":"Alpha"},{"id":"b","label":"Beta"}]},"deep":["items"],"incomplete":false}</script>
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
    catch (error) { throw new Error(String(error.stack || error) + '\n' + errors.join('\n')); }
    await page.evaluate(() => {
      window.__focusedRow = document.querySelectorAll('.row')[1];
      window.__focusedRow.focus();
	  window.__wasFocused = document.activeElement === window.__focusedRow;
    });
    const initial = await page.evaluate(() => window.__initialMutations);
    await page.evaluate(() => document.querySelector('main').click());
    await page.waitForFunction(() => Array.from(document.querySelectorAll('.row')).map(row => row.textContent).join('|') === 'Beta*|Gamma');
    const result = await page.evaluate(() => {
      const rows = document.querySelectorAll('.row');
      return {
        initial: window.__initialMutations,
		wasFocused: window.__wasFocused,
        rowCount: rows.length,
        firstRetained: rows[0] === window.__focusedRow,
        focused: document.activeElement === window.__focusedRow,
        text: Array.from(rows).map(row => row.textContent).join('|'),
      };
    });
	if (result.initial !== 0 || !result.wasFocused || result.rowCount !== 2 || !result.firstRetained || !result.focused || result.text !== 'Beta*|Gamma') throw new Error(JSON.stringify(result));
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
		t.Fatalf("browser keyed hydration fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("browser keyed hydration fixture output=%q", got)
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
