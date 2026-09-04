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
	if !strings.Contains(string(entry), "import $$client from './Page.hydrate_client.js';") {
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
		{"if with direct child lifecycle", `<template><main>{#if visible}<Child :title={title} />{:else}<span>Off</span>{/if}</main></template>`, true},
		{"if child lifecycle rejects component event", `<template><main>{#if visible}<Child @on:close={close} />{:else}<span>Off</span>{/if}</main></template>`, false},
		{"nested static if and each", `<template><main><section>{#if visible}<span>On</span>{:else}<span>Off</span>{/if}{#each items as item}<span>{item}</span>{/each}</section></main></template>`, true},
		{"direct static key", `<template><main>{#key identity}<span>Row</span>{/key}</main></template>`, true},
		{"if with dynamic branch", `<template><main>{#if visible}<span>{title}</span>{/if}</main></template>`, false},
		{"direct static each", `<template><main>{#each items as item}<span>Row</span>{/each}</main></template>`, true},
		{"static each else", `<template><main>{#each items as item}<span>Row</span>{:else}<span>Empty</span>{/each}</main></template>`, true},
		{"dynamic text each row", `<template><main>{#each items as item}<span>{item}</span>{/each}</main></template>`, true},
		{"keyed each", `<template><main>{#each items as item (item.id)}<span>Row</span>{/each}</main></template>`, true},
		{"keyed each else", `<template><main>{#each items as item (item.id)}<span>Row</span>{:else}<span>Empty</span>{/each}</main></template>`, false},
		{"static await", `<template><main>{#await request}<span>Loading</span>{:then value}<span>Done</span>{:catch error}<span>Failed</span>{/await}</main></template>`, true},
		{"await with dynamic resolution", `<template><main>{#await request}<span>Loading</span>{:then value}<span>{value}</span>{/await}</main></template>`, true},
		{"await with dynamic event", `<template><main>{#await request}<span>Loading</span>{:then value}<button @click={save}>{value}</button>{/await}</main></template>`, true},
		{"await with static each", `<template><main>{#await request}<span>Loading</span>{:then value}{#each items as item}<span>{item}</span>{/each}{/await}</main></template>`, true},
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

func TestSSRHydrationClientAdoptsPendingAwaitRangeWithoutInitialRender(t *testing.T) {
	sfc, err := parseSFC(`<script>export let request;</script><template><main>{#await request}<span>Loading</span>{:then value}<span>{value}</span>{:catch error}<span>{error}</span>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Page", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"const $$hawait0_range = $$acquireHydrationRange($$walker, 0);",
		"let $$hawait0_nodes = $$adoptHydrationRange($$hawait0_range);",
		"function $$hawait0_render()",
		"function $$hawait0_drive()",
		"Promise.resolve(request)",
		"String($$hawait0_value)",
		"String($$hawait0_error)",
		"$$hawait0_range.end.before($$frag)",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("hydration await lowering misses %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, "appendChild($$root)") || strings.Contains(source, "document.createElement('main')") {
		t.Fatalf("hydration await lowering unexpectedly creates initial DOM:\n%s", source)
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

func TestBrowserSSRHydrationAwaitRetainsPendingThenReplacesRange(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>let resolveRequest, rejectRequest; let draft = $state("server"); let visible = $state(false); let items = $state(["A"]); let keyed = $state([{ id: "a", label: "A" }, { id: "b", label: "B" }]); function createRequest() { return new Promise((resolve, reject) => { resolveRequest = resolve; rejectRequest = reject; window.__resolveRequest = resolve; window.__rejectRequest = reject; }); } let request = $state(createRequest()); function restartRequest() { request = createRequest(); } window.__restartRequest = restartRequest; function save() { window.__saved = (window.__saved || 0) + 1; } function toggle() { visible = !visible; } function addItem() { items = [...items, "B"]; } function reorder() { keyed = [keyed[1], keyed[0]]; }</script><template><main>{#await request}<span class="pending">Loading</span>{:then value}<button class="done" :title={value} @click={save}>{value}</button><input class="draft" bind:value={draft} /><p class="draft-output">{draft}</p><button class="toggle" @click={toggle}>Toggle</button>{#if visible}<p class="branch">On</p>{:else}<p class="branch">Off</p>{/if}<button class="add" @click={addItem}>Add</button>{#each items as item}<p class="row">{item}</p>{/each}<button class="reorder" @click={reorder}>Reorder</button>{#each keyed as item (item.id)}<button class="keyed-row">{item.label}</button>{/each}{:catch error}<span class="failed">{error}</span>{/await}</main></template>`)
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
<!--e:vela-page:0--><main vela-page=""><!--v:vela-page:0--><span vela-page="" class="pending">Loading</span><!--/v:vela-page:0--></main><!--/e:vela-page:0-->
<script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"incomplete":false}</script>
<script type="module">
  import hydratePage from "./Page.hydrate.js";
  window.__hydrationObserver = new MutationObserver(() => {});
  window.__hydrationObserver.observe(document.body, { childList: true, subtree: true, attributes: true, characterData: true });
  window.__rootBefore = document.querySelector("main");
  if (new URLSearchParams(location.search).has("mismatch")) {
    const mismatched = document.createElement("section");
    document.querySelector("main").replaceWith(mismatched);
  }
  if (new URLSearchParams(location.search).has("payload")) {
    document.getElementById("vela-state-vela-page").textContent = JSON.stringify({ component: "other-page", props: {}, state: {}, incomplete: false });
  }

  window.__hydrationObserver.takeRecords();
  try {
    window.__hydrated = hydratePage(document.body);
  } catch (error) {
    window.__hydrationError = String(error && error.message || error);
  }
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
    page.on("pageerror", error => console.error(error.stack || error));
    page.setDefaultTimeout(5000);
    await page.goto("http://127.0.0.1:" + server.address().port + "/");
    await page.waitForFunction(() => window.__hydrated && window.__resolveRequest);
    const initial = await page.evaluate(() => ({ mutations: window.__initialMutations, pending: !!document.querySelector(".pending"), same: document.querySelector("main") === window.__rootBefore }));
    if (initial.mutations !== 0 || !initial.pending || !initial.same) throw new Error(JSON.stringify(initial));
    await page.evaluate(() => window.__resolveRequest("ready"));
    await page.waitForFunction(() => document.querySelector(".done"));
    const settled = await page.evaluate(() => ({ pending: !!document.querySelector(".pending"), done: document.querySelector(".done")?.textContent, title: document.querySelector(".done")?.getAttribute("title"), same: document.querySelector("main") === window.__rootBefore }));
    if (settled.pending || settled.done !== "ready" || settled.title !== "ready" || !settled.same) throw new Error(JSON.stringify(settled));
    const draft = await page.evaluate(() => ({ value: document.querySelector(".draft")?.value, output: document.querySelector(".draft-output")?.textContent }));
    if (draft.value !== "server" || draft.output !== "server") throw new Error(JSON.stringify(draft));
    await page.fill(".draft", "typed");
    await page.waitForFunction(() => document.querySelector(".draft-output")?.textContent === "typed");
    if (await page.locator(".branch").textContent() !== "Off") throw new Error("nested await if did not mount its initial branch");
    await page.click(".toggle");
    await page.waitForFunction(() => document.querySelector(".branch")?.textContent === "On");
    if (await page.locator(".row").count() !== 1 || await page.locator(".row").textContent() !== "A") throw new Error("nested await each did not mount its initial row");
    await page.click(".add");
    await page.waitForFunction(() => document.querySelectorAll(".row").length === 2);
    const keyedBefore = await page.evaluate(() => { const row = document.querySelector(".keyed-row"); row.focus(); window.__keyedA = row; return row.textContent; });
    if (keyedBefore !== "A") throw new Error("keyed each initial row missing");
    await page.evaluate(() => document.querySelector(".reorder").dispatchEvent(new MouseEvent("click", { bubbles: true })));
    await page.waitForFunction(() => document.querySelectorAll(".keyed-row")[0]?.textContent === "B");
    const keyedAfter = await page.evaluate(() => ({ same: document.querySelectorAll(".keyed-row")[1] === window.__keyedA, focused: document.activeElement === window.__keyedA }));
    if (!keyedAfter.same || !keyedAfter.focused) throw new Error(JSON.stringify(keyedAfter));
    await page.click(".done");
    const saved = await page.evaluate(() => window.__saved);
    if (saved !== 1) throw new Error("await branch event did not run: " + saved);
    await page.evaluate(() => { window.__doneBeforeRestart = document.querySelector(".done"); window.__restartRequest(); });
    await page.waitForFunction(() => document.querySelector(".pending"));
    await page.evaluate(() => window.__doneBeforeRestart.dispatchEvent(new MouseEvent("click", { bubbles: true })));
    const restarted = await page.evaluate(() => ({ oldDoneConnected: window.__doneBeforeRestart.isConnected, saved: window.__saved }));
    if (restarted.oldDoneConnected || restarted.saved !== 1) throw new Error("await branch replacement leaked listener: " + JSON.stringify(restarted));
    await page.evaluate(() => window.__resolveRequest("again"));
    await page.waitForFunction(() => document.querySelector(".done")?.textContent === "again");

    await page.evaluate(() => {
      window.__doneBeforeDestroy = document.querySelector(".done");
      window.__hydrated.component.destroy();
      window.__doneBeforeDestroy.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    });
    const destroyed = await page.evaluate(() => ({ rootConnected: window.__rootBefore.isConnected, doneConnected: window.__doneBeforeDestroy.isConnected, saved: window.__saved }));
    await page.goto("http://127.0.0.1:" + server.address().port + "/?mismatch=1");
    await page.waitForFunction(() => typeof window.__hydrationError === "string");
    const mismatch = await page.evaluate(() => ({ error: window.__hydrationError, mutations: window.__initialMutations, hydrated: !!window.__hydrated, tag: document.querySelector("section")?.tagName }));
    if (!mismatch.error.includes("Vela hydration mismatch [vela-page]: element range e0 expected <main> but found <section>") || mismatch.mutations !== 0 || mismatch.hydrated || mismatch.tag !== "SECTION") throw new Error("unexpected hydration mismatch result: " + JSON.stringify(mismatch));

    if (destroyed.rootConnected || destroyed.doneConnected || destroyed.saved !== 1) throw new Error("hydration destroy leaked DOM or listener: " + JSON.stringify(destroyed));

    await page.goto("http://127.0.0.1:" + server.address().port + "/?payload=1");
    await page.waitForFunction(() => typeof window.__hydrationError === "string");
    const payloadMismatch = await page.evaluate(() => ({ error: window.__hydrationError, mutations: window.__initialMutations, hydrated: !!window.__hydrated, pending: !!document.querySelector(".pending") }));
    if (!payloadMismatch.error.includes("Vela hydration mismatch [vela-page]: state payload belongs to another component") || payloadMismatch.mutations !== 0 || payloadMismatch.hydrated || !payloadMismatch.pending) throw new Error("unexpected payload mismatch result: " + JSON.stringify(payloadMismatch));

    await page.goto("http://127.0.0.1:" + server.address().port + "/?reject=1");
    await page.waitForFunction(() => window.__hydrated && window.__rejectRequest);
    const rejectedInitial = await page.evaluate(() => ({ mutations: window.__initialMutations, pending: !!document.querySelector(".pending"), same: document.querySelector("main") === window.__rootBefore }));
    if (rejectedInitial.mutations !== 0 || !rejectedInitial.pending || !rejectedInitial.same) throw new Error(JSON.stringify(rejectedInitial));
    await page.evaluate(() => window.__rejectRequest("broken"));
    await page.waitForFunction(() => document.querySelector(".failed")?.textContent === "broken");
    const rejected = await page.evaluate(() => ({ pending: !!document.querySelector(".pending"), failed: document.querySelector(".failed")?.textContent, same: document.querySelector("main") === window.__rootBefore }));
    if (rejected.pending || rejected.failed !== "broken" || !rejected.same) throw new Error(JSON.stringify(rejected));
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
		t.Fatalf("browser await hydration fixture failed: %v\n%s", err, output)
	}
	if strings.TrimSpace(string(output)) != "OK" {
		t.Fatalf("browser await hydration fixture output=%q", output)
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

func TestSSRHydrationClientAdoptsLocalChildComponent(t *testing.T) {
	sfc, err := parseSFC(`<script>
		import { Child } from "./Child.vela";
		export let title = "server";
	</script><template><main><Child :title={title} /></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		`import { Child } from './Child.hydrate.js';`,
		`Child($$root).component`,
		`.set({ title: title })`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("child hydration lowering misses %q:\n%s", want, source)
		}
	}
	if strings.Contains(source, `createComment("comp")`) {
		t.Fatalf("child hydration must not create a CSR component anchor:\n%s", source)
	}
}

func TestSSRHydrationStructuralChildImportsKeepCSRFactoryDistinct(t *testing.T) {
	sfc, err := parseSFC(`<script>
		import { Child } from "./Child.vela";
		let visible = $state(true);
	</script><template><main>{#if visible}<Child />{:else}<p>Hidden</p>{/if}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	aliases := ssrHydrationStructuralComponentAliases(sfc.TemplateAST.Nodes)
	if got, want := aliases["Child"], "$$csr_Child"; got != want {
		t.Fatalf("CSR child alias=%q, want %q", got, want)
	}
	g := &ASTGen{
		analysis:               analyzeScriptAST(sfc.ScriptAST),
		domAcquireMode:         DOMAcquireHydrate,
		hydrationComponents:    map[string]bool{"Child": true},
		hydrationCSRComponents: aliases,
	}
	source := PrintJS(&JsProgram{Body: g.buildImportDecls()}, PrintPretty)
	for _, want := range []string{
		"import { Child } from \x27./Child.hydrate.js\x27;",
		"import { Child as $$csr_Child } from \x27./Child.js\x27;",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("structural child imports miss %q:\n%s", want, source)
		}
	}
}

func TestSSRHydrationIfChildAdoptsThenRemountsWithCSRFactory(t *testing.T) {
	sfc, err := parseSFC(`<script>
		import { Child } from "./Child.vela";
		export let visible = true;
		export let title = "server";
	</script><template><main>{#if visible}<Child :title={title} />{:else}<p>Hidden</p>{/if}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"import { Child } from \x27./Child.hydrate.js\x27;",
		"import { Child as $$csr_Child } from \x27./Child.js\x27;",
		"let $$hif0_component = null;",
		"$$hif0_component = Child($$root).component;",
		"$$hif0_component.set({ title: title });",
		"$$hif0_component = $$csr_Child($$frag, { title: title }, {});",
		"$$hif0_component.destroy();",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("structural child lifecycle misses %q:\\n%s", want, source)
		}
	}
	destroyAt := strings.Index(source, "$$hif0_component.destroy();")
	removeAt := strings.Index(source, "$$node.remove();")
	if destroyAt < 0 || removeAt < 0 || destroyAt > removeAt {
		t.Fatalf("structural child must destroy before removing its SSR range:\\n%s", source)
	}
}

func TestSSRHydrationIfChildRemountsStaticSiblingsInTemplateOrder(t *testing.T) {
	sfc, err := parseSFC(`<script>
		import { Child } from "./Child.vela";
		export let visible = true;
		export let title = "server";
	</script><template><main>{#if visible}<p class="before">Before</p><Child :title={title} /><p class="after">After</p>{:else}<p>Hidden</p>{/if}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("static siblings around direct #if child must be hydration-eligible")
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	before := strings.Index(source, "Before")
	mount := strings.Index(source, "$$hif0_component = $$csr_Child")
	after := strings.Index(source, "After")
	if before < 0 || mount < 0 || after < 0 || !(before < mount && mount < after) {
		t.Fatalf("static sibling remount order is not template order:\n%s", source)
	}
}

func TestSSRHydrationIfBranchOwnsMultipleDistinctChildHandles(t *testing.T) {
	sfc, err := parseSFC(`<script>
		import { Alpha } from "./Alpha.vela";
		import { Beta } from "./Beta.vela";
		export let visible = true;
		export let title = "server";
	</script><template><main>{#if visible}<p>Before</p><Alpha :title={title} /><Beta :title={title} /><p>After</p>{:else}<p>Hidden</p>{/if}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("multiple distinct direct #if children must be hydration-eligible")
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"let $$hif0_component_0 = null;",
		"let $$hif0_component_1 = null;",
		"$$hif0_component_0 = Alpha($$root).component;",
		"$$hif0_component_1 = Beta($$root).component;",
		"$$hif0_component_0 = $$csr_Alpha($$frag, { title: title }, {});",
		"$$hif0_component_1 = $$csr_Beta($$frag, { title: title }, {});",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("multi-child lifecycle misses %q:\n%s", want, source)
		}
	}
	if first, second := strings.Index(source, "$$hif0_component_1.destroy();"), strings.Index(source, "$$hif0_component_0.destroy();"); first < 0 || second < 0 || first > second {
		t.Fatalf("multi-child handles must destroy in reverse order:\n%s", source)
	}
}

func TestSSRHydrationIfBranchNamespacesRepeatedChildBinding(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let visible = true;</script><template><main>{#if visible}<Child /><Child />{:else}<p>Hidden</p>{/if}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("repeated structural child bindings have static path-scoped payload namespaces")
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"const $$instanceID = $$hydrate.instanceID || 'vela-parent';",
		"Child($$root, null, $$instanceID + '--n0--n0--if0--n0--component-Child').component",
		"Child($$root, null, $$instanceID + '--n0--n0--if0--n1--component-Child').component",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("repeated child hydration misses %q:\\n%s", want, source)
		}
	}
}

func TestSSRHydrationUnkeyedEachAdoptsAndRebuildsDirectChildRows(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let rows = []; </script><template><main>{#each rows as row, index}<Child :value={row.value} :index={index} />{:else}<p>Empty</p>{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("unkeyed direct child rows must be hydration-eligible")
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"let $$heach0_components = [];",
		"Child($$root, null, $$instanceID + '--each-",
		"$$heach0_components.push($$component);",
		"$$csr_Child($$frag, { value: $$item.value, index: $$heach0_index }, {});",
		"$$component.destroy();",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("unkeyed child each lifecycle misses %q:\\n%s", want, source)
		}
	}
}

func TestSSRHydrationKeyedEachRetainsDirectChildInstances(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let rows = []; </script><template><main>{#each rows as row (row.id)}<Child :value={row.value} />{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("keyed direct child rows must be hydration-eligible")
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"let $$heach0_components = new Map();",
		"$$heach0_components.set($$key, Child($$root, null, $$instanceID + '--each-",
		"$$heach0_components.get($$heach0_key($$item, $$index))",
		"$$component.set({ value: $$item.value });",
		"$$heach0_components.delete($$key);",
		"$$csr_Child($$frag, { value: $$item.value }, {});",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("keyed child each lifecycle misses %q:\\n%s", want, source)
		}
	}
}

func TestSSRHydrationKeyBlockAdoptsAndRemountsDirectChild(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let version = "a"; export let value = "server";</script><template><main>{#key version}<Child :value={value} />{/key}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		t.Fatal("direct child in #key must be hydration-eligible")
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		"let $$hkey0_component = null;",
		"Child($$root, null, $$instanceID + '--key-",
		"$$hkey0_component.set({ value: value });",
		"$$hkey0_component.destroy();",
		"$$csr_Child($$frag, { value: value }, {});",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("key child lifecycle misses %q:\\n%s", want, source)
		}
	}
}

func TestBrowserSSRHydrationKeyedEachRetainsDirectChildInstances(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>export let id = ""; export let value = "";</script><template><p class="child" :data-id={id}>{value}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	childCSR, _ := buildProgramWithDOMAcquirePlan("Child", childSFC, childSFC.TemplateAST.Nodes, childAnalysis, "vela-child", false, DOMAcquireCreate)

	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let rows = [];</script><template><main>{#each rows as row (row.id)}<Child :id={row.id} :value={row.value} />{/each}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	main := parentSFC.TemplateAST.Nodes[0].(*ElementNode)
	block := main.Children[0].(*EachBlockNode)
	component := block.Children[0].(*ComponentNode)
	identity := parentPlan.ComponentInstanceIdentities[component.Span]
	suffix := ssrRuntimeComponentSuffix(identity)
	eachID, ok := ssrHydrationMarkerIDs(parentPlan.Nodes).eachID(block.Span)
	if !ok {
		t.Fatal("parent each marker is missing")
	}
	childScope := func(key string) string {
		return "vela-parent--each-" + itoa(block.Span.Start) + "-" + key + "--" + suffix
	}
	childHTML := func(key, value string) string {
		scope := childScope(key)
		return "<!--e:" + scope + ":0--><p vela-child=\"\" class=\"child\" data-id=\"" + key + "\"><!--v:" + scope + ":0-->" + value + "<!--/v:" + scope + ":0--></p><!--/e:" + scope + ":0--><script type=\"application/json\" id=\"vela-state-" + scope + "\">{\"component\":\"" + scope + "\",\"props\":{\"Id\":\"" + key + "\",\"Value\":\"" + value + "\"},\"state\":{},\"incomplete\":false}</script>"
	}
	serverHTML := "<!doctype html><body><!--e:vela-parent:0--><main vela-parent=\"\"><!--v:vela-parent:" + itoa(eachID) + "-->" + childHTML("a", "Alpha") + childHTML("b", "Beta") + "<!--/v:vela-parent:" + itoa(eachID) + "--></main><!--/e:vela-parent:0--><script type=\"application/json\" id=\"vela-state-vela-parent\">{\"component\":\"vela-parent\",\"props\":{\"Rows\":[{\"id\":\"a\",\"value\":\"Alpha\"},{\"id\":\"b\",\"value\":\"Beta\"}]},\"state\":{},\"incomplete\":false}</script><script type=\"module\">import hydrateParent from './Parent.hydrate.js'; window.__observer = new MutationObserver(() => {}); window.__observer.observe(document.body, {childList:true,subtree:true,attributes:true,characterData:true}); window.__hydrated = hydrateParent(document.body); window.__initialMutations = window.__observer.takeRecords().length;</script></body>"
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":               embeddedRuntime,
		"Child.js":                 []byte(PrintJS(childCSR, PrintPretty)),
		"Child.hydrate_client.js":  []byte(PrintJS(childClient, PrintPretty)),
		"Child.hydrate.js":         []byte(PrintJS(childEntry, PrintPretty)),
		"Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)),
		"Parent.hydrate.js":        []byte(PrintJS(parentEntry, PrintPretty)),
		"index.html":               []byte(serverHTML),
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
    await page.goto('http://127.0.0.1:' + server.address().port + '/');
    await page.waitForFunction(() => window.__hydrated);
    await page.evaluate(() => {
      window.__a = document.querySelector('.child[data-id="a"]');
      window.__b = document.querySelector('.child[data-id="b"]');
      window.__hydrated.component.set({rows:[{id:'b',value:'Beta*'},{id:'a',value:'Alpha*'},{id:'c',value:'Gamma'}]});
    });
    await page.waitForFunction(() => Array.from(document.querySelectorAll('.child')).map(node => node.dataset.id + ':' + node.textContent).join('|') === 'b:Beta*|a:Alpha*|c:Gamma');
    await page.evaluate(() => { window.__retained = {
      initial: window.__initialMutations,
      a: document.querySelector('.child[data-id="a"]') === window.__a,
      b: document.querySelector('.child[data-id="b"]') === window.__b,
    }; });
    await page.evaluate(() => window.__hydrated.component.set({rows:[{id:'b',value:'Beta final'}]}));
    await page.waitForFunction(() => document.querySelectorAll('.child').length === 1 && document.querySelector('.child')?.textContent === 'Beta final');
    const result = await page.evaluate(() => ({
      initial: window.__retained.initial,
      a: window.__retained.a,
      b: window.__retained.b,
      payloadA: !!document.getElementById('vela-state-' + window.__aScope),
      payloadB: !!document.getElementById('vela-state-' + window.__bScope),
    }));
    if (result.initial !== 0 || !result.a || !result.b || result.payloadA || !result.payloadB) throw new Error(JSON.stringify(result));
    process.stdout.write('OK');
  } catch (error) {
    console.error(error && error.stack || error);
    process.exitCode = 1;
  } finally {
    await browser?.close();
    server.close(() => process.exit());
  }
});`
	// Pass the actual server namespaces through globals instead of duplicating
	// their structural-path recipe inside the browser script.
	serverHTML = strings.Replace(serverHTML, "window.__initialMutations", "window.__aScope='"+childScope("a")+"'; window.__bScope='"+childScope("b")+"'; window.__initialMutations", 1)
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(serverHTML), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(node, "-e", browserScript, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("keyed child browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("keyed child browser fixture output=%q", got)
	}
}

func TestSSRProjectHydrationClientImportsLocalChildEntry(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(srcRoot, "Child.vela")
	if err := os.WriteFile(child, []byte(`<script>export let title = "";</script><template><p>{title}</p></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(srcRoot, "Parent.vela")
	if err := os.WriteFile(parent, []byte(`<script>import { Child } from "./Child.vela"; export let title = "server";</script><template><main><Child :title={title} /></main></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	if errors := buildSSRProject([]string{child, parent}, srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	layout := NewSSRProjectLayout(outRoot)
	client, err := os.ReadFile(layout.hydrationClientPath(parent, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`Child.hydrate.js`,
		`Child($$root).component`,
	} {
		if !strings.Contains(string(client), want) {
			t.Fatalf("parent hydration client misses %q:\n%s", want, client)
		}
	}
	childEntry, err := os.ReadFile(layout.hydratePath(child, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(childEntry), `export function Child($$root, $$overrides, $$instanceID)`) {
		t.Fatalf("child hydrate entry misses component alias:\n%s", childEntry)
	}
}

func TestSSRProjectSkipsParentHydrationClientForUnsupportedChild(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(srcRoot, "Child.vela")
	if err := os.WriteFile(child, []byte(`<script>export let visible = false; export let title = "";</script><template><p>{#if visible}<span>{title}</span>{/if}</p></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(srcRoot, "Parent.vela")
	if err := os.WriteFile(parent, []byte(`<script>import { Child } from "./Child.vela"; export let title = "server";</script><template><main><Child :title={title} /></main></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	if errors := buildSSRProject([]string{child, parent}, srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	layout := NewSSRProjectLayout(outRoot)
	if _, err := os.Stat(layout.hydrationClientPath(parent, srcRoot)); !os.IsNotExist(err) {
		t.Fatalf("parent hydration client for unsupported child: %v", err)
	}
	entry, err := os.ReadFile(layout.hydratePath(parent, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(entry), `.hydrate_client.js`) {
		t.Fatalf("parent validation entry must not import a missing child client:\n%s", entry)
	}
}

func TestBrowserSSRHydrationAdoptsLocalChildThenUpdatesProps(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>export let title = "";</script><template><p class="child">{title}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let title = "server";</script><template><main><Child :title={title} /></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":               embeddedRuntime,
		"Child.hydrate_client.js":  []byte(PrintJS(childClient, PrintPretty)),
		"Child.hydrate.js":         []byte(PrintJS(childEntry, PrintPretty)),
		"Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)),
		"Parent.hydrate.js":        []byte(PrintJS(parentEntry, PrintPretty)),
		"index.html": []byte(`<!doctype html><body>
<!--e:vela-parent:0--><main vela-parent=""><!--e:vela-child:0--><p vela-child="" class="child"><!--v:vela-child:0-->server<!--/v:vela-child:0--></p><!--/e:vela-child:0--><script type="application/json" id="vela-state-vela-child">{"component":"vela-child","props":{"Title":"server"},"state":{},"incomplete":false}</script></main><!--/e:vela-parent:0-->
<script type="application/json" id="vela-state-vela-parent">{"component":"vela-parent","props":{"Title":"server"},"state":{},"incomplete":false}</script>
<script type="module">
  import hydrateParent from "./Parent.hydrate.js";
  window.__hydrationObserver = new MutationObserver(() => {});
  window.__hydrationObserver.observe(document.body, { childList: true, subtree: true, attributes: true, characterData: true });
  window.__hydrated = hydrateParent(document.body);
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
  const name = req.url === "/" ? "index.html" : req.url.slice(1);
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
    await page.evaluate(() => { window.__child = document.querySelector(".child"); window.__hydrated.component.set({ title: "client" }); });
    await page.waitForFunction(() => document.querySelector(".child")?.textContent === "client");
    const result = await page.evaluate(() => ({ initial: window.__initialMutations, same: document.querySelector(".child") === window.__child, text: document.querySelector(".child")?.textContent }));
    if (result.initial !== 0 || !result.same || result.text !== "client") throw new Error(JSON.stringify(result));
    process.stdout.write("OK");
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
		t.Fatalf("child hydration browser fixture: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("child hydration browser output=%q", got)
	}
}

func TestBrowserSSRHydrationIfChildAdoptsDestroysAndCSRRemounts(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>
		import { onDestroy } from "vela";
		export let title = "";
		onDestroy(() => { window.__childDestroyCount = (window.__childDestroyCount || 0) + 1; });
	</script><template><p class="child">{title}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	childCSR, _ := buildProgramWithDOMAcquirePlan("Child", childSFC, childSFC.TemplateAST.Nodes, analyzeScriptAST(childSFC.ScriptAST), "vela-child", false, DOMAcquireCreate)

	parentSFC, err := parseSFC(`<script>
		import { Child } from "./Child.vela";
		export let visible = true;
		export let title = "server";
	</script><template><main>{#if visible}<p class="before">Before</p><Child :title={title} /><p class="after">After</p>{:else}<p class="hidden">Hidden</p>{/if}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":               embeddedRuntime,
		"Child.js":                 []byte(PrintJS(childCSR, PrintPretty)),
		"Child.hydrate_client.js":  []byte(PrintJS(childClient, PrintPretty)),
		"Child.hydrate.js":         []byte(PrintJS(childEntry, PrintPretty)),
		"Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)),
		"Parent.hydrate.js":        []byte(PrintJS(parentEntry, PrintPretty)),
		"index.html": []byte(`<!doctype html><body>
<!--e:vela-parent:0--><main vela-parent=""><!--v:vela-parent:0--><p vela-parent="" class="before">Before</p><!--e:vela-child:0--><p vela-child="" class="child"><!--v:vela-child:0-->server<!--/v:vela-child:0--></p><!--/e:vela-child:0--><script type="application/json" id="vela-state-vela-child">{"component":"vela-child","props":{"Title":"server"},"state":{},"incomplete":false}</script><p vela-parent="" class="after">After</p><!--/v:vela-parent:0--></main><!--/e:vela-parent:0-->
<script type="application/json" id="vela-state-vela-parent">{"component":"vela-parent","props":{"Visible":true,"Title":"server"},"state":{},"incomplete":false}</script>
<script type="module">
  import hydrateParent from './Parent.hydrate.js';
  window.__hydrationObserver = new MutationObserver(() => {});
  window.__hydrationObserver.observe(document.body, { childList: true, subtree: true, attributes: true, characterData: true });
  window.__hydrated = hydrateParent(document.body);
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
    await page.goto('http://127.0.0.1:' + server.address().port + '/');
    await page.waitForFunction(() => window.__hydrated);
    await page.evaluate(() => { window.__firstChild = document.querySelector('.child'); window.__hydrated.component.set({title:'client'}); });
    await page.waitForFunction(() => document.querySelector('.child')?.textContent === 'client');
    await page.evaluate(() => window.__hydrated.component.set({visible:false}));
    await page.waitForFunction(() => document.querySelector('.hidden'));
    await page.evaluate(() => { window.__payloadRemoved = !document.getElementById('vela-state-vela-child'); window.__hydrated.component.set({visible:true}); });
    await page.waitForFunction(() => document.querySelector('.child')?.textContent === 'client' && document.querySelector('.before') && document.querySelector('.after'));
    await page.evaluate(() => window.__hydrated.component.set({title:'remounted'}));
    await page.waitForFunction(() => document.querySelector('.child')?.textContent === 'remounted');
    const result = await page.evaluate(() => ({
      initial: window.__initialMutations,
      firstDetached: !window.__firstChild.isConnected,
      remounted: document.querySelector('.child') !== window.__firstChild,
      payloadRemoved: window.__payloadRemoved,
      destroyed: window.__childDestroyCount,
      text: document.querySelector('.child')?.textContent,
      order: Array.from(document.querySelector('main').children).map((node) => node.className).join(','),
    }));
    if (result.initial !== 0 || !result.firstDetached || !result.remounted || !result.payloadRemoved || result.destroyed !== 1 || result.text !== 'remounted' || result.order !== 'before,child,after') throw new Error(JSON.stringify(result));
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
		t.Fatalf("structural child hydration browser fixture: %v\\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("structural child hydration browser output=%q", got)
	}
}

func TestSSRHydrationClientPassesNestedElementToLocalChild(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela";</script><template><main><section><Child /></section></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	if !strings.Contains(source, `Child($$n1).component`) {
		t.Fatalf("nested child hydration must receive adopted parent element:\n%s", source)
	}
}

func TestSSRProjectHydratesNestedLocalComponentGraph(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	grandchild := filepath.Join(srcRoot, "Grandchild.vela")
	if err := os.WriteFile(grandchild, []byte(`<script>export let title = "";</script><template><span class="grandchild">{title}</span></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(srcRoot, "Child.vela")
	if err := os.WriteFile(child, []byte(`<script>import { Grandchild } from "./Grandchild.vela"; export let title = "";</script><template><section><Grandchild :title={title} /></section></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(srcRoot, "Parent.vela")
	if err := os.WriteFile(parent, []byte(`<script>import { Child } from "./Child.vela"; export let title = "server";</script><template><main><Child :title={title} /></main></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	if errors := buildSSRProject([]string{grandchild, child, parent}, srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	layout := NewSSRProjectLayout(outRoot)
	childClient, err := os.ReadFile(layout.hydrationClientPath(child, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(childClient), `Grandchild.hydrate.js`) {
		t.Fatalf("nested child hydration client misses grandchild entry:\n%s", childClient)
	}
}

func TestSSRHydrationClientAttachesComponentEventsAfterChildAdoption(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; let count = $state(0);</script><template><main><Child @on:close={count++} /><p>{count}</p></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	child := strings.Index(source, `Child($$root).component`)
	listener := strings.Index(source, `.addEventListener(`)
	if child < 0 || listener < 0 || listener < child {
		t.Fatalf("component event listener must follow child adoption:\n%s", source)
	}
	if !strings.Contains(source, `instanceof CustomEvent`) {
		t.Fatalf("component event listener must retain CustomEvent guard:\n%s", source)
	}
}

func TestBrowserSSRHydrationForwardsLocalChildComponentEvent(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<template><button class="child" @click={() => $$dispatch("close")}>close</button></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; let count = $state(0);</script><template><main><Child @on:close={count++} /><p class="count">{count}</p></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":               embeddedRuntime,
		"Child.hydrate_client.js":  []byte(PrintJS(childClient, PrintPretty)),
		"Child.hydrate.js":         []byte(PrintJS(childEntry, PrintPretty)),
		"Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)),
		"Parent.hydrate.js":        []byte(PrintJS(parentEntry, PrintPretty)),
		"index.html": []byte(`<!doctype html><body>
<!--e:vela-parent:0--><main vela-parent=""><!--e:vela-child:0--><button vela-child="" class="child">close</button><!--/e:vela-child:0--><script type="application/json" id="vela-state-vela-child">{"component":"vela-child","props":{},"state":{},"incomplete":false}</script><!--e:vela-parent:1--><p vela-parent="" class="count"><!--v:vela-parent:0-->0<!--/v:vela-parent:0--></p><!--/e:vela-parent:1--></main><!--/e:vela-parent:0-->
<script type="application/json" id="vela-state-vela-parent">{"component":"vela-parent","props":{},"state":{"count":0},"incomplete":false}</script>
<script type="module">
  import hydrateParent from "./Parent.hydrate.js";
  window.__hydrationObserver = new MutationObserver(() => {});
  window.__hydrationObserver.observe(document.body, { childList: true, subtree: true, attributes: true, characterData: true });
  window.__hydrated = hydrateParent(document.body);
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
  const name = req.url === "/" ? "index.html" : req.url.slice(1);
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
    await page.evaluate(() => { window.__child = document.querySelector(".child"); });
    await page.locator(".child").click();
    await page.waitForFunction(() => document.querySelector(".count")?.textContent === "1");
    const result = await page.evaluate(() => ({ initial: window.__initialMutations, same: document.querySelector(".child") === window.__child, count: document.querySelector(".count")?.textContent }));
    if (result.initial !== 0 || !result.same || result.count !== "1") throw new Error(JSON.stringify(result));
    process.stdout.write("OK");
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
		t.Fatalf("component event hydration browser fixture: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("component event hydration browser output=%q", got)
	}
}

func TestSSRHydrationClientPassesBindableCallbackBeforeChildAdoption(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let value = "server";</script><template><main><Child bind:value={value} /><p>{value}</p></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	for _, want := range []string{
		`Child($$root, { $$onChange_value: ($$v) => $$set_value($$v) }).component`,
		`.set({ value: value, $$onChange_value:`,
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("bindable child hydration lowering misses %q:\n%s", want, source)
		}
	}
}

func TestBrowserSSRHydrationBindsLocalChildValueWithoutInitialRewrite(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>export let value = $bindable("");</script><template><input class="child" bind:value={value} /></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let value = "server";</script><template><main><Child bind:value={value} /><p class="value">{value}</p></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":               embeddedRuntime,
		"Child.hydrate_client.js":  []byte(PrintJS(childClient, PrintPretty)),
		"Child.hydrate.js":         []byte(PrintJS(childEntry, PrintPretty)),
		"Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)),
		"Parent.hydrate.js":        []byte(PrintJS(parentEntry, PrintPretty)),
		"index.html": []byte(`<!doctype html><body>
<!--e:vela-parent:0--><main vela-parent=""><!--e:vela-child:0--><input vela-child="" class="child" value="server"><!--/e:vela-child:0--><script type="application/json" id="vela-state-vela-child">{"component":"vela-child","props":{"Value":"server"},"state":{},"incomplete":false}</script><!--e:vela-parent:1--><p vela-parent="" class="value"><!--v:vela-parent:0-->server<!--/v:vela-parent:0--></p><!--/e:vela-parent:1--></main><!--/e:vela-parent:0-->
<script type="application/json" id="vela-state-vela-parent">{"component":"vela-parent","props":{"Value":"server"},"state":{},"incomplete":false}</script>
<script type="module">
  import hydrateParent from "./Parent.hydrate.js";
  window.__hydrationObserver = new MutationObserver(() => {});
  window.__hydrationObserver.observe(document.body, { childList: true, subtree: true, attributes: true, characterData: true });
  window.__hydrated = hydrateParent(document.body);
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
  const name = req.url === "/" ? "index.html" : req.url.slice(1);
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
    await page.evaluate(() => { window.__input = document.querySelector(".child"); });
    await page.locator(".child").fill("client");
    await page.waitForFunction(() => document.querySelector(".value")?.textContent === "client");
    const result = await page.evaluate(() => ({ initial: window.__initialMutations, same: document.querySelector(".child") === window.__input, value: document.querySelector(".child")?.value, text: document.querySelector(".value")?.textContent }));
    if (result.initial !== 0 || !result.same || result.value !== "client" || result.text !== "client") throw new Error(JSON.stringify(result));
    process.stdout.write("OK");
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
		t.Fatalf("bindable child hydration browser fixture: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("bindable child hydration browser output=%q", got)
	}
}

func TestSSRProjectBuildsBindableLocalChildHydration(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(srcRoot, "Child.vela")
	if err := os.WriteFile(child, []byte(`<script>export let value = $bindable("");</script><template><input bind:value={value} /></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(srcRoot, "Parent.vela")
	if err := os.WriteFile(parent, []byte(`<script>import { Child } from "./Child.vela"; export let value = "server";</script><template><main><Child bind:value={value} /><p>{value}</p></main></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	if errors := buildSSRProject([]string{child, parent}, srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	layout := NewSSRProjectLayout(outRoot)
	client, err := os.ReadFile(layout.hydrationClientPath(parent, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(client), `$$onChange_value`) {
		t.Fatalf("parent hydration client misses bindable callback:\n%s", client)
	}
}

func TestSSRHydrationClientAdoptsStaticLocalComponentSlots(t *testing.T) {
	child, err := parseSFC(`<template><section><slot><p>fallback</p></slot></section></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !CanBuildSSRHydrationClient(child.TemplateAST.Nodes) {
		t.Fatal("static child slot should be hydration-capable")
	}
	parent, err := parseSFC(`<script>import { Child } from "./Child.vela";</script><template><main><Child><p class="slotted">body</p></Child></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	program, err := BuildSSRHydrationClientProgram("Parent", parent, analyzeScriptAST(parent.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	if !strings.Contains(source, `Child($$root).component`) || strings.Contains(source, `createComment("comp")`) {
		t.Fatalf("static slot child must adopt existing DOM without CSR anchor:\n%s", source)
	}
}

func TestSSRProjectBuildsStaticLocalComponentSlotsForHydration(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(srcRoot, "Child.vela")
	if err := os.WriteFile(child, []byte(`<template><section><slot><p>fallback</p></slot></section></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(srcRoot, "Parent.vela")
	if err := os.WriteFile(parent, []byte(`<script>import { Child } from "./Child.vela";</script><template><main><Child><p class="slotted">body</p></Child></main></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	if errors := buildSSRProject([]string{child, parent}, srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	layout := NewSSRProjectLayout(outRoot)
	if _, err := os.Stat(layout.hydrationClientPath(parent, srcRoot)); err != nil {
		t.Fatalf("parent static-slot hydration client: %v", err)
	}
}

func TestBrowserSSRHydrationAdoptsStructuralAndReactiveLocalComponentSlotContent(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<template><section class="child"><slot><p>fallback</p></slot></section></template>`)
	if err != nil {
		t.Fatal(err)
	}
	childAnalysis := analyzeScriptAST(childSFC.ScriptAST)
	childPlan, err := BuildSSRRenderPlanWithAnalysis("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childClient, err := BuildSSRHydrationClientProgram("Child", childSFC, childAnalysis, "vela-child")
	if err != nil {
		t.Fatal(err)
	}
	childEntry, err := BuildSSRHydrateEntryWithClient(childPlan, "./Child.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let visible = true; export let title = "server"; export let selected = false; function toggle() { selected = !selected; }</script><template><main><Child>{#if visible}<p class="slot-state">shown</p>{:else}<p class="slot-state">hidden</p>{/if}<button class="slotted" :title={title} class:active={selected} @click={toggle}>{title}</button></Child></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysis("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentClient, err := BuildSSRHydrationClientProgram("Parent", parentSFC, parentAnalysis, "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	parentEntry, err := BuildSSRHydrateEntryWithClient(parentPlan, "./Parent.hydrate_client.js")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":               embeddedRuntime,
		"Child.hydrate_client.js":  []byte(PrintJS(childClient, PrintPretty)),
		"Child.hydrate.js":         []byte(PrintJS(childEntry, PrintPretty)),
		"Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)),
		"Parent.hydrate.js":        []byte(PrintJS(parentEntry, PrintPretty)),
		"index.html": []byte(`<!doctype html><body>
<!--e:vela-parent:0--><main vela-parent=""><!--e:vela-child:0--><section vela-child="" class="child"><!--v:vela-parent:0--><!--e:vela-parent:1--><p vela-parent="" class="slot-state">shown</p><!--/e:vela-parent:1--><!--/v:vela-parent:0--><!--e:vela-parent:3--><button vela-parent="" class="slotted" title="server"><!--v:vela-parent:1-->server<!--/v:vela-parent:1--></button><!--/e:vela-parent:3--></section><!--/e:vela-child:0--><script type="application/json" id="vela-state-vela-child">{"component":"vela-child","props":{},"state":{},"incomplete":false}</script></main><!--/e:vela-parent:0-->
<script type="application/json" id="vela-state-vela-parent">{"component":"vela-parent","props":{"Visible":true,"Title":"server","Selected":false},"state":{},"incomplete":false}</script>
<script type="module">
  import hydrateParent from "./Parent.hydrate.js";
  window.__hydrationObserver = new MutationObserver(() => {});
  window.__hydrationObserver.observe(document.body, { childList: true, subtree: true, attributes: true, characterData: true });
  window.__slot = document.querySelector(".slotted");
  window.__hydrated = hydrateParent(document.body);
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
  const name = req.url === "/" ? "index.html" : req.url.slice(1);
  try { const body = fs.readFileSync(path.join(root, name)); res.writeHead(200, {"content-type": name.endsWith(".js") ? "text/javascript" : "text/html"}).end(body); }
  catch { res.writeHead(404).end(); }
});
server.listen(0, "127.0.0.1", async () => {
  let browser;
  try {
    browser = await chromium.launch({headless:true});
    const page = await browser.newPage();
    const errors = [];
    page.on("pageerror", error => errors.push(String(error.stack || error)));
    page.on("console", message => { if (message.type() === "error") errors.push(message.text()); });
    page.setDefaultTimeout(5000);
    await page.goto("http://127.0.0.1:" + server.address().port + "/");
    try { await page.waitForFunction(() => window.__hydrated); }
    catch (error) { throw new Error(String(error.stack || error) + "\n" + errors.join("\n")); }
    const initial = await page.evaluate(() => window.__initialMutations);
    await page.evaluate(() => window.__hydrated.component.set({title: "client"}));
    await page.waitForFunction(() => document.querySelector(".slotted")?.textContent === "client" && document.querySelector(".slotted")?.getAttribute("title") === "client");
    await page.evaluate(() => document.querySelector(".slotted").click());
    await page.waitForFunction(() => document.querySelector(".slotted")?.classList.contains("active"));
    await page.evaluate(() => window.__hydrated.component.set({visible: false}));
    await page.waitForFunction(() => document.querySelector(".slot-state")?.textContent === "hidden");
    const result = await page.evaluate((initial) => ({ initial, same: document.querySelector(".slotted") === window.__slot, text: document.querySelector(".slotted")?.textContent, title: document.querySelector(".slotted")?.getAttribute("title"), active: document.querySelector(".slotted")?.classList.contains("active"), state: document.querySelector(".slot-state")?.textContent }), initial);
    if (result.initial !== 0 || !result.same || result.text !== "client" || result.title !== "client" || !result.active || result.state !== "hidden") throw new Error(JSON.stringify(result));
    process.stdout.write("OK");
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
		t.Fatalf("reactive slot element hydration browser fixture: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("reactive slot element hydration browser output=%q", got)
	}
}

func TestSSRHydrationClientAdoptsDynamicLocalComponentSlotText(t *testing.T) {
	child, err := parseSFC(`<template><section><slot><p>fallback</p></slot></section></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let title = "server";</script><template><main><Child>{title}</Child></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !CanBuildSSRHydrationClient(child.TemplateAST.Nodes) || !CanBuildSSRHydrationClient(parent.TemplateAST.Nodes) {
		t.Fatal("dynamic text slot should be hydration-capable")
	}
	program, err := BuildSSRHydrationClientProgram("Parent", parent, analyzeScriptAST(parent.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	if !strings.Contains(source, `Child($$root).component`) || !strings.Contains(source, `$$acquireHydrationRange($$walker, 0)`) || !strings.Contains(source, `$$writeHydrationText`) {
		t.Fatalf("dynamic parent slot must acquire and update its own text range:\n%s", source)
	}
}

func TestSSRHydrationClientAdoptsReactiveLocalComponentSlotElement(t *testing.T) {
	parent, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let title = "server"; let active = $state(false); function toggle() { active = !active; }</script><template><main><Child><button :title={title} class:active={active} @click={toggle}>{title}</button></Child></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !CanBuildSSRHydrationClient(parent.TemplateAST.Nodes) {
		t.Fatal("reactive ordinary element slot should be hydration-capable")
	}
	program, err := BuildSSRHydrationClientProgram("Parent", parent, analyzeScriptAST(parent.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	if !strings.Contains(source, `$$acquireHydrationElement($$walker, 1, 'button')`) || !strings.Contains(source, `addEventListener('click'`) || !strings.Contains(source, `$$writeHydrationText`) {
		t.Fatalf("reactive slot element must be acquired and retain its parent updates/listener:\n%s", source)
	}
}

func TestSSRHydrationRejectsAwaitLocalComponentSlotContent(t *testing.T) {
	parent, err := parseSFC(`<script>import { Child } from "./Child.vela";</script><template><main><Child>{#await pending}<p>body</p>{/await}</Child></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if CanBuildSSRHydrationClient(parent.TemplateAST.Nodes) {
		t.Fatal("await slot content must stay outside the hydration subset")
	}
}

func TestSSRHydrationClientUsesCanonicalElementMarkersAfterStructuralSlot(t *testing.T) {
	parent, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let visible = true; export let title = "server"; function save() { title = "client"; }</script><template><main><Child>{#if visible}<p>branch</p>{/if}<button @click={save}>{title}</button></Child></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	if !CanBuildSSRHydrationClient(parent.TemplateAST.Nodes) {
		t.Fatal("static structural slot followed by reactive sibling should be hydration-capable")
	}
	program, err := BuildSSRHydrationClientProgram("Parent", parent, analyzeScriptAST(parent.ScriptAST), "vela-parent")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(program, PrintPretty)
	if !strings.Contains(source, `$$acquireHydrationElement($$walker, 2, 'button')`) || strings.Contains(source, `$$acquireHydrationElement($$walker, 1, 'button')`) {
		t.Fatalf("reactive sibling must keep the SSR-plan marker after a structural slot range:\n%s", source)
	}
}
