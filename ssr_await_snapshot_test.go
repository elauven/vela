package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedSSRRendererSelectsAwaitSnapshot(t *testing.T) {
	sfc, err := parseSFC(`<script>export let draft = "server"; export let enabled = false;</script><template><main>{#await request}<p class="pending">Pending</p>{:then value}<p class="settled" :title={value}>Settled</p><input class="draft" bind:value={draft} /><input class="enabled" type="checkbox" bind:checked={enabled} /><button @click={clicked}>Save</button>{:catch error}<p class="failed">Failed</p>{/await}</main></template>`)
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
  err := components.RenderPageWithOptionsAt(os.Stdout, components.PageProps{Draft: "server", Enabled: false}, components.PageSlots{}, components.PageRenderOptions{
    Await: map[int]components.PageAwaitSnapshot{` + itoa(awaitID) + `: {State: "then", Value: "server"}},
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
		`<p class="settled" title="server">Settled</p>`,
		`<input class="draft" value="server">`,
		`<input class="enabled" type="checkbox">`,
		`<button data-vela-event="vela-page-e0">Save</button>`,
		`"await":{"` + itoa(awaitID) + `":{"state":"then","value":"server"}}`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("SSR settled snapshot misses %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Pending") || strings.Contains(got, "Failed") {
		t.Fatalf("SSR settled snapshot rendered another await branch:\n%s", got)
	}
}

func TestGeneratedSSRRendererSelectsAwaitSnapshotChild(t *testing.T) {
	sfc, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request;</script><template><main>{#await request}<p>Pending</p>{:then value}<Child :title={value} /><p>Done</p>{:catch error}<Child :title={error} /><p>Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlanWithAnalysisAndLinks("Parent", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-parent", map[string]SSRComponentLink{
		"Child": {LocalName: "Child", GoName: "Child", PackageName: "child", ImportPath: "example.com/project/child"},
	})
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "parent")
	if err != nil {
		t.Fatal(err)
	}
	source, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`case "then":`,
		`child.ChildAt(child.ChildProps{Title: ssrAwaitSnapshot0.Value}, child.ChildSlots{}, instanceID+"--n0--n0--await-then--n0--component-Child")`,
	} {
		if !strings.Contains(string(source), want) {
			t.Fatalf("SSR snapshot child renderer misses %q:\n%s", want, source)
		}
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

func TestBrowserSSRHydrationAdoptsDynamicAwaitSnapshotLeaves(t *testing.T) {
	node := requirePlaywright(t)
	sfc, err := parseSFC(`<script>export let request; export let draft = "server"; export let enabled = false; function clicked() { window.snapshotClicks++; } function report() { window.snapshotDraft = draft; window.snapshotEnabled = enabled; }</script><template><main>{#await request}<p class="pending">Pending</p>{:then value}<p class="settled" :title={value}>Value: {value}</p><input class="snapshot-draft" bind:value={draft} /><input class="snapshot-enabled" type="checkbox" bind:checked={enabled} /><button class="snapshot-action" @click={clicked}>Increment</button><button class="snapshot-report" @click={report}>Report</button>{:catch error}<p class="failed">Failed: {error}</p>{/await}</main></template>`)
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
	draft := await.ThenChildren[1].(*SSRPlanElement)
	enabled := await.ThenChildren[2].(*SSRPlanElement)
	action := await.ThenChildren[3].(*SSRPlanElement)
	report := await.ThenChildren[4].(*SSRPlanElement)
	html := `<!doctype html><body><!--e:vela-page:` + itoa(main.HydrationID) + `--><main><!--v:vela-page:` + itoa(await.DynamicID) + `--><!--e:vela-page:` + itoa(settled.HydrationID) + `--><p class="settled" title="server">Value: <!--v:vela-page:` + itoa(textID) + `-->server<!--/v:vela-page:` + itoa(textID) + `--></p><!--/e:vela-page:` + itoa(settled.HydrationID) + `--><!--e:vela-page:` + itoa(draft.HydrationID) + `--><input class="snapshot-draft" value="server"><!--/e:vela-page:` + itoa(draft.HydrationID) + `--><!--e:vela-page:` + itoa(enabled.HydrationID) + `--><input class="snapshot-enabled" type="checkbox"><!--/e:vela-page:` + itoa(enabled.HydrationID) + `--><!--e:vela-page:` + itoa(action.HydrationID) + `--><button class="snapshot-action" data-vela-event="vela-page-e0">Increment</button><!--/e:vela-page:` + itoa(action.HydrationID) + `--><!--e:vela-page:` + itoa(report.HydrationID) + `--><button class="snapshot-report" data-vela-event="vela-page-e1">Report</button><!--/e:vela-page:` + itoa(report.HydrationID) + `--><!--/v:vela-page:` + itoa(await.DynamicID) + `--></main><!--/e:vela-page:` + itoa(main.HydrationID) + `--><script type="application/json" id="vela-state-vela-page">{"component":"vela-page","props":{},"state":{},"incomplete":false,"await":{"` + itoa(await.DynamicID) + `":{"state":"then","value":"server"}}}</script><script type="module">import hydratePage from "./Page.hydrate.js";window.snapshotClicks=0;window.request=new Promise(resolve=>{window.resolveRequest=resolve});document.querySelector(".snapshot-draft").value="typed-before-hydration";document.querySelector(".snapshot-enabled").checked=true;window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydratePage(document.body,{request:window.request,draft:"server",enabled:false});window.initial=window.observer.takeRecords().length</script></body>`
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
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{const data=fs.readFileSync(path.join(root,n));return r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(data)}catch{return r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if(await p.evaluate(()=>window.initial)!==0)throw new Error("dynamic settled snapshot hydration mutated DOM");await p.evaluate(()=>{window.settled=document.querySelector(".settled");window.snapshotButton=document.querySelector(".snapshot-action");window.snapshotDraftInput=document.querySelector(".snapshot-draft");window.snapshotEnabledInput=document.querySelector(".snapshot-enabled");document.querySelector(".snapshot-report").click()});await p.waitForFunction(()=>window.snapshotDraft==="typed-before-hydration"&&window.snapshotEnabled===true);await p.evaluate(()=>window.snapshotButton.click());await p.waitForFunction(()=>window.snapshotClicks===1);await p.evaluate(()=>window.resolveRequest("client"));await p.waitForFunction(()=>document.querySelector(".settled")?.textContent==="Value: client"&&document.querySelector(".settled")?.getAttribute("title")==="client");await p.evaluate(()=>window.hydrated.component.set({draft:"external",enabled:false}));await p.waitForFunction(()=>document.querySelector(".snapshot-draft")?.value==="external"&&!document.querySelector(".snapshot-enabled")?.checked);const state=await p.evaluate(()=>({sameElement:document.querySelector(".settled")===window.settled,pending:!!document.querySelector(".pending")}));if(!state.sameElement||state.pending)throw new Error(JSON.stringify(state));await p.evaluate(()=>{window.nextRequest=new Promise(resolve=>{window.resolveNext=resolve});window.hydrated.component.set({request:window.nextRequest})});await p.waitForFunction(()=>document.querySelector(".pending"));await p.evaluate(()=>{window.snapshotDraftInput.value="stale";window.snapshotDraftInput.dispatchEvent(new Event("input",{bubbles:true}));window.snapshotEnabledInput.checked=true;window.snapshotEnabledInput.dispatchEvent(new Event("change",{bubbles:true}))});await p.evaluate(()=>window.resolveNext("again"));await p.waitForFunction(()=>document.querySelector(".snapshot-report"));await p.evaluate(()=>document.querySelector(".snapshot-report").click());await p.waitForFunction(()=>window.snapshotDraft==="external"&&window.snapshotEnabled===false);await p.evaluate(()=>window.hydrated.component.destroy());if(await p.evaluate(()=>document.querySelector("main")))throw new Error("destroy left root");process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("dynamic await snapshot leaf browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(client, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("dynamic await snapshot leaf browser output=%q", got)
	}
}

func TestBrowserSSRHydrationAdoptsAwaitSnapshotChild(t *testing.T) {
	node := requirePlaywright(t)
	childSFC, err := parseSFC(`<script>import { onDestroy } from "vela"; export let title = ""; onDestroy(() => { window.snapshotChildDestroys = (window.snapshotChildDestroys || 0) + 1; });</script><template><p class="snapshot-child">{title}</p></template>`)
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

	parentSFC, err := parseSFC(`<script>import { Child } from "./Child.vela"; export let request;</script><template><main>{#await request}<p class="pending">Pending</p>{:then value}<Child :title={value} /><p class="settled">Done</p>{:catch error}<p class="failed">Failed</p>{/await}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	parentAnalysis := analyzeScriptAST(parentSFC.ScriptAST)
	parentPlan, err := BuildSSRRenderPlanWithAnalysisAndLinks("Parent", parentSFC, parentAnalysis, "vela-parent", map[string]SSRComponentLink{
		"Child": {LocalName: "Child", SourcePath: "Child.vela", GoName: "Child", PackageName: "child", ImportPath: "example.com/project/child"},
	})
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
	awaitNode := main.Children[0].(*AwaitBlockNode)
	awaitID, ok := ssrHydrationMarkerIDs(parentPlan.Nodes).awaitID(awaitNode.Span)
	if !ok {
		t.Fatal("parent await marker is missing")
	}
	childScope := childPlan.ScopeID
	childRoot := childPlan.Nodes[0].(*SSRPlanElement)
	childText := childRoot.Children[0].(*SSRPlanText).Parts[0]
	html := `<!doctype html><body><!--e:vela-parent:` + itoa(parentPlan.Nodes[0].(*SSRPlanElement).HydrationID) + `--><main vela-parent=""><!--v:vela-parent:` + itoa(awaitID) + `--><!--e:` + childScope + `:` + itoa(childRoot.HydrationID) + `--><p vela-child="" class="snapshot-child"><!--v:` + childScope + `:` + itoa(childText.DynamicID) + `-->server<!--/v:` + childScope + `:` + itoa(childText.DynamicID) + `--></p><!--/e:` + childScope + `:` + itoa(childRoot.HydrationID) + `--><script type="application/json" id="vela-state-` + childScope + `">{"component":"` + childScope + `","props":{"Title":"server"},"state":{},"incomplete":false}</script><p vela-parent="" class="settled">Done</p><!--/v:vela-parent:` + itoa(awaitID) + `--></main><!--/e:vela-parent:` + itoa(parentPlan.Nodes[0].(*SSRPlanElement).HydrationID) + `--><script type="application/json" id="vela-state-vela-parent">{"component":"vela-parent","props":{},"state":{},"incomplete":false,"await":{"` + itoa(awaitID) + `":{"state":"then","value":"server"}}}</script><script type="module">import hydrateParent from "./Parent.hydrate.js";window.request=new Promise(resolve=>{window.resolveRequest=resolve});window.observer=new MutationObserver(()=>{});window.observer.observe(document.body,{childList:true,subtree:true,attributes:true,characterData:true});window.hydrated=hydrateParent(document.body,{request:window.request});window.initial=window.observer.takeRecords().length</script></body>`
	dir := t.TempDir()
	for name, data := range map[string][]byte{
		"runtime.js":               embeddedRuntime,
		"Child.js":                 []byte(PrintJS(childCSR, PrintPretty)),
		"Child.hydrate_client.js":  []byte(PrintJS(childClient, PrintPretty)),
		"Child.hydrate.js":         []byte(PrintJS(childEntry, PrintPretty)),
		"Parent.hydrate_client.js": []byte(PrintJS(parentClient, PrintPretty)),
		"Parent.hydrate.js":        []byte(PrintJS(parentEntry, PrintPretty)),
		"index.html":               []byte(html),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],scope=process.argv[2],server=http.createServer((q,r)=>{const n=q.url==="/"?"index.html":q.url.slice(1);try{const data=fs.readFileSync(path.join(root,n));return r.writeHead(200,{"content-type":n.endsWith(".js")?"text/javascript":"text/html"}).end(data)}catch{return r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;try{b=await chromium.launch({headless:true});const p=await b.newPage();p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if(await p.evaluate(()=>window.initial)!==0)throw new Error("snapshot child hydration mutated DOM");await p.evaluate(()=>{window.child=document.querySelector(".snapshot-child");window.resolveRequest("client")});await p.waitForFunction(()=>document.querySelector(".snapshot-child")?.textContent==="client");const same=await p.evaluate(()=>document.querySelector(".snapshot-child")===window.child);if(!same)throw new Error("snapshot child was remounted on same-branch settle");await p.evaluate(()=>{window.nextRequest=new Promise(()=>{});window.hydrated.component.set({request:window.nextRequest})});await p.waitForFunction(()=>document.querySelector(".pending"));const state=await p.evaluate(scope=>({detached:!window.child.isConnected,payload:!document.getElementById("vela-state-"+scope),destroys:window.snapshotChildDestroys}),scope);if(!state.detached||!state.payload||state.destroys!==1)throw new Error(JSON.stringify(state));await p.evaluate(()=>window.hydrated.component.destroy());process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	command := exec.Command(node, "-e", browser, dir, childScope)
	command.Dir = "/projects/compiler"
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("await snapshot child browser fixture: %v\n%s\nclient:\n%s", err, output, PrintJS(parentClient, PrintPretty))
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("await snapshot child browser output=%q", got)
	}
}
