package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedSSRRendererSelectsStaticAwaitSnapshot(t *testing.T) {
	sfc, err := parseSFC(`<template><main>{#await request}<p class="pending">Pending</p>{:then value}<p class="settled">Settled</p>{:catch error}<p class="failed">Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	renderer, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateGeneratedSSRGo("page.go", renderer); err != nil {
		t.Fatalf("ValidateGeneratedSSRGo: %v\n%s", err, renderer)
	}
	main := sfc.TemplateAST.Nodes[0].(*ElementNode)
	await := main.Children[0].(*AwaitBlockNode)
	awaitID, ok := ssrHydrationMarkerIDs(plan.Nodes).awaitID(await.Span)
	if !ok {
		t.Fatal("await marker is missing")
	}

	root := t.TempDir()
	components := filepath.Join(root, "components")
	if err := os.MkdirAll(components, 0o755); err != nil {
		t.Fatal(err)
	}
	compilerRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	module := "module example.com/awaitsnapshot\n\ngo 1.22\n\nrequire github.com/elauven/vela v0.0.0\n\nreplace github.com/elauven/vela => " + filepath.ToSlash(compilerRoot) + "\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(components, "page.go"), renderer, 0o644); err != nil {
		t.Fatal(err)
	}
	mainSource := `package main
import (
  "os"
  "example.com/awaitsnapshot/components"
)
func main() {
  err := components.RenderPageWithOptionsAt(os.Stdout, components.PageProps{}, components.PageSlots{}, components.PageRenderOptions{
    Await: map[int]components.PageAwaitSnapshot{` + itoa(awaitID) + `: {State: "then"}},
  }, "vela-page-instance")
  if err != nil { panic(err) }
}
`
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(mainSource), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "run", ".")
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go run: %v\n%s", err, output)
	}
	got := string(output)
	for _, want := range []string{
		`<p class="settled">Settled</p>`,
		`"await":{"` + itoa(awaitID) + `":{"state":"then"}}`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("SSR settled snapshot misses %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Pending") || strings.Contains(got, "Failed") {
		t.Fatalf("SSR settled snapshot rendered another await branch:\n%s", got)
	}
}

func TestSSRHydrationStaticAwaitSnapshotKeepsSettledRange(t *testing.T) {
	sfc, err := parseSFC(`<script>export let request;</script><template><main>{#await request}<p>Pending</p>{:then value}<p>Settled</p>{:catch error}<p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	client, err := BuildSSRHydrationClientProgram("Page", sfc, analysis, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(client, PrintPretty)
	for _, want := range []string{
		"const $$hawait0_snapshot = $$props.$$velaAwait && $$props.$$velaAwait[0];",
		"$$hawait0_snapshot.state === 'then'",
		"let $$hawait0_initial = true;",
		"if ($$hawait0_initial)",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("hydration await snapshot lowering misses %q:\n%s", want, source)
		}
	}
}

func TestBrowserSSRHydrationAdoptsStaticAwaitSnapshot(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let request;</script><template><main>{#await request}<p class="pending">Pending</p>{:then value}<p class="settled">Settled</p>{:catch error}<p class="failed">Failed</p>{/await}</main></template>`)
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
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	settled := await.ThenChildren[0].(*SSRPlanElement)
	html := `<!doctype html><body><!--e:vela-page:` + itoa(main.HydrationID) + `--><main><!--v:vela-page:` + itoa(await.DynamicID) + `--><!--e:vela-page:` + itoa(settled.HydrationID) + `--><p class="settled">Settled</p><!--/e:vela-page:` + itoa(settled.HydrationID) + `--><!--/v:vela-page:` + itoa(await.DynamicID) + `--></main><!--/e:vela-page:` + itoa(main.HydrationID) + `--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"incomplete":false,"await":{"` + itoa(await.DynamicID) + `":{"state":"then"}}}</script><script type="module">import hydratePage from "./Page.hydrate.js";window.resolveRequest=null;window.request=new Promise(resolve=>{window.resolveRequest=resolve});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydratePage(document.body,{request:window.request});window.initial=window.observer.takeRecords().length</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":             embeddedRuntime,
		"Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)),
		"Page.hydrate.js":        []byte(PrintJS(entry, PrintPretty)),
		"index.html":             []byte(html),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{const data=fs.readFileSync(path.join(root,n));return r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(data)}catch{return r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if(await p.evaluate(()=>window.initial)!==0)throw new Error("settled snapshot hydration mutated DOM");await p.evaluate(()=>{window.initialNode=document.querySelector(".settled");window.resolveRequest("same")});await p.waitForFunction(()=>document.querySelector(".settled")===window.initialNode);await p.evaluate(()=>window.hydrated.component.set({request:Promise.reject("failed")}));await p.waitForFunction(()=>document.querySelector(".failed"));const state=await p.evaluate(()=>({settledDetached:!window.initialNode.isConnected,pending:!!document.querySelector(".pending")}));if(!state.settledDetached||state.pending)throw new Error(JSON.stringify(state));await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("static await snapshot browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("static await snapshot browser output=%q", got)
	}
}

func TestBrowserSSRHydrationAdoptsDynamicAwaitSnapshotText(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let request;</script><template><main>{#await request}<p class="pending">Pending</p>{:then value}<p class="settled">Value: {value}</p>{:catch error}<p class="failed">Failed: {error}</p>{/await}</main></template>`)
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
	main := plan.Nodes[0].(*SSRPlanElement)
	await := main.Children[0].(*SSRPlanAwait)
	settled := await.ThenChildren[0].(*SSRPlanElement)
	text := settled.Children[0].(*SSRPlanText)
	textID := text.Parts[1].DynamicID
	html := `<!doctype html><body><!--e:vela-page:` + itoa(main.HydrationID) + `--><main><!--v:vela-page:` + itoa(await.DynamicID) + `--><!--e:vela-page:` + itoa(settled.HydrationID) + `--><p class="settled">Value: <!--v:vela-page:` + itoa(textID) + `-->server<!--/v:vela-page:` + itoa(textID) + `--></p><!--/e:vela-page:` + itoa(settled.HydrationID) + `--><!--/v:vela-page:` + itoa(await.DynamicID) + `--></main><!--/e:vela-page:` + itoa(main.HydrationID) + `--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"incomplete":false,"await":{"` + itoa(await.DynamicID) + `":{"state":"then","value":"server"}}}</script><script type="module">import hydratePage from "./Page.hydrate.js";window.request=new Promise(resolve=>{window.resolveRequest=resolve});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydratePage(document.body,{request:window.request});window.initial=window.observer.takeRecords().length</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":             embeddedRuntime,
		"Page.hydrate_client.js": []byte(PrintJS(client, PrintPretty)),
		"Page.hydrate.js":        []byte(PrintJS(entry, PrintPretty)),
		"index.html":             []byte(html),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{const data=fs.readFileSync(path.join(root,n));return r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(data)}catch{return r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if(await p.evaluate(()=>window.initial)!==0)throw new Error("dynamic settled snapshot hydration mutated DOM");await p.evaluate(()=>{window.settled=document.querySelector(".settled");window.resolveRequest("client")});await p.waitForFunction(()=>document.querySelector(".settled")?.textContent==="Value: client");const state=await p.evaluate(()=>({sameElement:document.querySelector(".settled")===window.settled,pending:!!document.querySelector(".pending")}));if(!state.sameElement||state.pending)throw new Error(JSON.stringify(state));await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("dynamic await snapshot browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("dynamic await snapshot browser output=%q", got)
	}
}
