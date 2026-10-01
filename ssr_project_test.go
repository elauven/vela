package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildSSRProjectWritesSeparatedProducts(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(srcRoot, "App.vela")
	if err := os.WriteFile(source, []byte(`<template><button class="card" @click={() => alert("done")}>Hello</button></template><style>.card { color: red; }</style>`), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	opts := CompileOptions{StatusWriter: io.Discard}
	if errors := buildSSRProject([]string{source}, srcRoot, outRoot, opts, false); errors != 0 {
		t.Fatalf("buildSSRProject errors=%d", errors)
	}
	layout := NewSSRProjectLayout(outRoot)
	client, err := os.ReadFile(layout.clientPath(source, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(client), "function App") {
		t.Fatalf("client output is not CSR JavaScript:\n%s", client)
	}
	server, err := os.ReadFile(layout.serverPath(source, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(server), "func RenderApp") {
		t.Fatalf("server output is not a Go renderer:\n%s", server)
	}
	props, err := os.ReadFile(layout.propsPath(source, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(props), "type AppProps struct") {
		t.Fatalf("missing inferred props sidecar:\n%s", props)
	}
	if strings.Contains(string(server), "type AppProps struct") {
		t.Fatalf("renderer unexpectedly owns the props contract:\n%s", server)
	}
	if err := ValidateGeneratedSSRGoPackage(map[string][]byte{"app.go": server, "app_props.go": props}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(server), `data-vela-event=\"`) {
		t.Fatalf("server output misses event marker:\n%s", server)
	}
	hydrationClient, err := os.ReadFile(layout.hydrationClientPath(source, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(hydrationClient), "addEventListener('click'") {
		t.Fatalf("executable hydration client is missing native listener:\n%s", hydrationClient)
	}
	if _, err := os.Stat(layout.eventBridgePath(source, srcRoot)); !os.IsNotExist(err) {
		t.Fatalf("executable hydration unexpectedly emitted static event bridge: %v", err)
	}
	manifestSource, err := os.ReadFile(layout.manifestPath(source, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	var manifest SSRArtifactManifest
	if err := json.Unmarshal(manifestSource, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Mode != SSRArtifactExecutable || manifest.HydrationClient != "App.hydrate_client.js" || manifest.StaticEventBridge != "" {
		t.Fatalf("unexpected executable manifest: %#v", manifest)
	}
	hydrateSource, err := os.ReadFile(layout.hydratePath(source, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(hydrateSource), "mode: 'executable'") {
		t.Fatalf("executable hydrate entry does not expose its artifact mode:\n%s", hydrateSource)
	}
	css, err := os.ReadFile(layout.stylePath(source, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), "color: red") {
		t.Fatalf("missing scoped CSS:\n%s", css)
	}
	if _, err := os.Stat(filepath.Join(layout.ClientDir, "runtime.js")); err != nil {
		t.Fatalf("missing client runtime: %v", err)
	}
}

func TestSSRCLIWritesDeclaredArtifactMode(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcRoot, "App.vela"), []byte(`<template><main>server</main></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	command := exec.Command("go", "run", ".", "--project", srcRoot, "--out", outRoot, "--ssr", "--ssr-module", "example.com/ssrcli/server")
	command.Dir = "."
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("vela --ssr failed: %v\n%s", err, output)
	}
	manifestSource, err := os.ReadFile(NewSSRProjectLayoutWithModule(outRoot, "example.com/ssrcli/server").manifestPath(filepath.Join(srcRoot, "App.vela"), srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	var manifest SSRArtifactManifest
	if err := json.Unmarshal(manifestSource, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Mode != SSRArtifactExecutable || manifest.Hydrate != "App.hydrate.js" {
		t.Fatalf("CLI emitted unexpected SSR manifest: %#v", manifest)
	}
}

func TestSSRArtifactManifestPreservesClientBoundaryMode(t *testing.T) {
	source, err := json.Marshal(SSRArtifactManifest{
		Component: "Page",
		Mode:      SSRArtifactClientBoundary,
		Hydrate:   "Page.hydrate.js",
	})
	if err != nil {
		t.Fatal(err)
	}
	var manifest SSRArtifactManifest
	if err := json.Unmarshal(source, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Mode != SSRArtifactClientBoundary || manifest.Hydrate != "Page.hydrate.js" {
		t.Fatalf("client-boundary manifest=%#v", manifest)
	}
}

func TestSSRProjectWritesStaticOnlyArtifactManifest(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(srcRoot, "Static.vela")
	if err := os.WriteFile(source, []byte(`<template><p>one</p><p>two</p></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	layout := NewSSRProjectLayout(filepath.Join(root, "out"))
	if err := compileSSRProjectFile(source, srcRoot, layout); err != nil {
		t.Fatal(err)
	}
	manifestSource, err := os.ReadFile(layout.manifestPath(source, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	var manifest SSRArtifactManifest
	if err := json.Unmarshal(manifestSource, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Mode != SSRArtifactStaticOnly || manifest.HydrationClient != "" {
		t.Fatalf("unexpected static-only manifest: %#v", manifest)
	}
	hydrateSource, err := os.ReadFile(layout.hydratePath(source, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(hydrateSource), "mode: 'static-only'") {
		t.Fatalf("static-only hydrate entry does not expose its artifact mode:\n%s", hydrateSource)
	}
}

func TestSSRProjectRejectsIncompleteExecutableHydrationState(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(srcRoot, "App.vela")
	contents := `<script>let count = $state(1 + 1);</script><template><button @click={() => count++}>{count}</button></template>`
	if err := os.WriteFile(source, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	layout := NewSSRProjectLayout(filepath.Join(root, "out"))
	err := compileSSRProjectFile(source, srcRoot, layout)
	if err == nil || !strings.Contains(err.Error(), SSRStateDiagnosticCode) || !strings.Contains(err.Error(), "count") {
		t.Fatalf("incomplete executable state error=%v", err)
	}
	if _, statErr := os.Stat(layout.serverPath(source, srcRoot)); !os.IsNotExist(statErr) {
		t.Fatalf("incomplete executable state unexpectedly wrote server artifact: %v", statErr)
	}
}

func TestSSRProjectGeneratedServerRunsFromPublicArtifacts(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(srcRoot, "App.vela")
	contents := `<script>export let title = 'server'; let count = $state(0); function increment() { count++; }</script><template><button @click={increment}>{title}: {count}</button></template>`
	if err := os.WriteFile(source, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	projectModule := "example.com/ssrproduct"
	serverModule := projectModule + "/server"
	opts := CompileOptions{StatusWriter: io.Discard, SSRModule: serverModule}
	if errors := buildSSRProject([]string{source}, srcRoot, outRoot, opts, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	compilerRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	module := "module " + projectModule + "\n\ngo 1.22\n\nrequire github.com/elauven/vela v0.0.0\n\nreplace github.com/elauven/vela => " + filepath.ToSlash(compilerRoot) + "\n"
	if err := os.WriteFile(filepath.Join(outRoot, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	mainSource := `package main
import (
    "os"
    components "example.com/ssrproduct/server/components"
)
func main() {
    if err := components.RenderAppAt(os.Stdout, components.AppProps{Title: "world"}, components.AppSlots{}, "vela-app-instance"); err != nil {
        panic(err)
    }
}
`
	if err := os.WriteFile(filepath.Join(outRoot, "main.go"), []byte(mainSource), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "run", ".")
	command.Dir = outRoot
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("public SSR go run: %v\n%s", err, output)
	}
	result := string(output)
	for _, want := range []string{`<button data-vela-event="vela-`, `>world<!--/v:vela-app-instance:0-->`, `"version":1`, `"count":0`, `"incomplete":false`} {
		if !strings.Contains(result, want) {
			t.Fatalf("public SSR output misses %q:\n%s", want, result)
		}
	}
}

func TestSSRProjectPublicArtifactsHydrateAndUpdateInBrowser(t *testing.T) {
	node := requirePlaywright(t)
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(srcRoot, "App.vela")
	contents := "<script>let count = $state(0); function increment() { count++; }</script><template><button class=\"increment\" @click={increment}>{count}</button></template>"
	if err := os.WriteFile(source, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	projectModule := "example.com/ssrbrowser"
	if errors := buildSSRProject([]string{source}, srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard, SSRModule: projectModule + "/server"}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	compilerRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	module := "module " + projectModule + "\n\ngo 1.22\n\nrequire github.com/elauven/vela v0.0.0\n\nreplace github.com/elauven/vela => " + filepath.ToSlash(compilerRoot) + "\n"
	if err := os.WriteFile(filepath.Join(outRoot, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	mainSource := `package main
import (
    "os"
    components "example.com/ssrbrowser/server/components"
)
func main() {
    if err := components.RenderAppAt(os.Stdout, components.AppProps{}, components.AppSlots{}, "vela-app-instance"); err != nil {
        panic(err)
    }
}
`
	if err := os.WriteFile(filepath.Join(outRoot, "main.go"), []byte(mainSource), 0o644); err != nil {
		t.Fatal(err)
	}
	html, err := runSSRGeneratedProgram(outRoot)
	if err != nil {
		t.Fatal(err)
	}
	clientRoot := NewSSRProjectLayout(outRoot).ClientDir
	entry := `import hydrateApp from "./App.hydrate.js";
const payloadNode = document.getElementById("vela-state-vela-app-instance");
const serverPayload = payloadNode.textContent;
payloadNode.textContent = serverPayload.replace('"version":1', '"version":2');
try { hydrateApp(document.body, {}, "vela-app-instance"); } catch (error) { window.hydrationFailure = String(error.message || error); }
payloadNode.textContent = serverPayload;
window.initialWrites = 0;
const observer = new MutationObserver(() => { window.initialWrites++; });
observer.observe(document.body, { childList: true, subtree: true, characterData: true, attributes: true });
window.hydrated = hydrateApp(document.body, {}, "vela-app-instance");
window.rehydrated = hydrateApp(document.body, {}, "vela-app-instance");
window.initialWrites = observer.takeRecords().length;
`
	if err := os.WriteFile(filepath.Join(clientRoot, "entry.js"), []byte(entry), 0o644); err != nil {
		t.Fatal(err)
	}
	page := "<!doctype html><body>" + string(html) + `<script type="module" src="./entry.js"></script></body>`
	if err := os.WriteFile(filepath.Join(clientRoot, "index.html"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	browser := `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1];const server=http.createServer((q,r)=>{const name=q.url==="/"?"index.html":q.url.slice(1);try{const body=fs.readFileSync(path.join(root,name));r.writeHead(200,{"content-type":name.endsWith(".js")?"text/javascript":"text/html"}).end(body)}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;const errors=[];try{b=await chromium.launch({headless:true});const p=await b.newPage();p.on("pageerror",e=>errors.push(String(e.stack||e)));p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);if(!(await p.evaluate(()=>window.hydrationFailure||"")).includes("unsupported state payload version 2"))throw new Error("initial hydration failure was not classified");if(await p.evaluate(()=>window.hydrated!==window.rehydrated))throw new Error("repeated hydration created a second instance");if(await p.evaluate(()=>window.hydrated.mode)!=="executable")throw new Error("hydrate entry mode mismatch");if(await p.evaluate(()=>window.initialWrites)!==0)throw new Error("hydration mutated server DOM");if(await p.textContent(".increment")!=="0")throw new Error("server state text mismatch");await p.click(".increment");await p.waitForFunction(()=>document.querySelector(".increment")?.textContent==="1");await p.evaluate(()=>window.hydrated.component.destroy());if(await p.$(".increment"))throw new Error("destroy retained component DOM");if(errors.length)throw new Error(errors.join("\n"));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	output, err := exec.Command(node, "-e", browser, clientRoot).CombinedOutput()
	if err != nil {
		t.Fatalf("public SSR browser fixture: %v\n%s", err, output)
	}
}

func TestSSRProjectPublicNullTextHydratesAndUpdatesInBrowser(t *testing.T) {
	node := requirePlaywright(t)
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(srcRoot, "App.vela")
	if err := os.WriteFile(source, []byte(`<script>export let value = null;</script><template><p class="value">{value}</p></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	projectModule := "example.com/ssrnulltext"
	if errors := buildSSRProject([]string{source}, srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard, SSRModule: projectModule + "/server"}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	compilerRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	module := "module " + projectModule + "\n\ngo 1.22\n\nrequire github.com/elauven/vela v0.0.0\n\nreplace github.com/elauven/vela => " + filepath.ToSlash(compilerRoot) + "\n"
	if err := os.WriteFile(filepath.Join(outRoot, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	mainSource := `package main
import (
    "os"
    components "example.com/ssrnulltext/server/components"
)
func main() {
    props := components.AppProps{Set: map[string]bool{}}
    if err := components.RenderAppAt(os.Stdout, props, components.AppSlots{}, "vela-null-text"); err != nil { panic(err) }
}
`
	if err := os.WriteFile(filepath.Join(outRoot, "main.go"), []byte(mainSource), 0o644); err != nil {
		t.Fatal(err)
	}
	html, err := runSSRGeneratedProgram(outRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), `>null<!--/v:`) || !strings.Contains(string(html), `"Value":null`) {
		t.Fatalf("server null output/payload is not CSR-compatible:\n%s", html)
	}
	clientRoot := NewSSRProjectLayout(outRoot).ClientDir
	entry := `import hydrateApp from "./App.hydrate.js";
window.serverText = document.querySelector(".value").textContent;
window.initialWrites = 0;
const observer = new MutationObserver(() => { window.initialWrites++; });
observer.observe(document.body, { childList: true, subtree: true, characterData: true, attributes: true });
window.hydrated = hydrateApp(document.body, {}, "vela-null-text");
window.initialWrites = observer.takeRecords().length;
`
	if err := os.WriteFile(filepath.Join(clientRoot, "entry.js"), []byte(entry), 0o644); err != nil {
		t.Fatal(err)
	}
	page := "<!doctype html><body>" + string(html) + `<script type="module" src="./entry.js"></script></body>`
	if err := os.WriteFile(filepath.Join(clientRoot, "index.html"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	browser := `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1];const server=http.createServer((q,r)=>{const name=q.url==="/"?"index.html":q.url.slice(1);try{const body=fs.readFileSync(path.join(root,name));r.writeHead(200,{"content-type":name.endsWith(".js")?"text/javascript":"text/html"}).end(body)}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;const errors=[];try{b=await chromium.launch({headless:true});const p=await b.newPage();p.on("pageerror",e=>errors.push(String(e.stack||e)));p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);const initial=await p.evaluate(()=>({server:window.serverText,text:document.querySelector(".value")?.textContent,writes:window.initialWrites,mode:window.hydrated.mode}));if(initial.server!=="null"||initial.text!=="null"||initial.writes!==0||initial.mode!=="executable")throw new Error("bad null adoption: "+JSON.stringify(initial));await p.evaluate(()=>{window.node=document.querySelector(".value");window.hydrated.component.set({value:"ready"})});await p.waitForFunction(()=>document.querySelector(".value")?.textContent==="ready");await p.evaluate(()=>window.hydrated.component.set({value:null}));await p.waitForFunction(()=>document.querySelector(".value")?.textContent==="null");if(!(await p.evaluate(()=>document.querySelector(".value")===window.node)))throw new Error("null update recreated retained text node");await p.evaluate(()=>window.hydrated.component.destroy());if(await p.$(".value"))throw new Error("destroy retained null-text DOM");if(errors.length)throw new Error(errors.join("\n"));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	output, err := exec.Command(node, "-e", browser, clientRoot).CombinedOutput()
	if err != nil {
		t.Fatalf("public null-text SSR browser fixture: %v\n%s", err, output)
	}
}

func TestSSRProjectPublicValueTextOracleHydratesInBrowser(t *testing.T) {
	node := requirePlaywright(t)
	cases := loadSSRValueTextOracle(t)
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	var source strings.Builder
	source.WriteString(`<script>let status = $state('server'); function update() { status = 'updated'; }</script><template><main>`)
	for _, tc := range cases {
		source.WriteString(`<p class="case-`)
		source.WriteString(tc.Name)
		source.WriteString(`">{`)
		source.WriteString(tc.Expression)
		source.WriteString(`}</p>`)
	}
	source.WriteString(`<button class="update" @click={update}>Update</button><p class="status">{status}</p></main></template>`)
	component := filepath.Join(srcRoot, "Page.vela")
	if err := os.WriteFile(component, []byte(source.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	projectModule := "example.com/ssrvaluetextbrowser"
	if errors := buildSSRProject([]string{component}, srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard, SSRModule: projectModule + "/server"}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	compilerRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	module := "module " + projectModule + "\n\ngo 1.22\n\nrequire github.com/elauven/vela v0.0.0\n\nreplace github.com/elauven/vela => " + filepath.ToSlash(compilerRoot) + "\n"
	if err := os.WriteFile(filepath.Join(outRoot, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	mainSource := `package main
import (
    "os"
    components "example.com/ssrvaluetextbrowser/server/components"
)
func main() {
    if err := components.RenderPageAt(os.Stdout, components.PageProps{}, components.PageSlots{}, "vela-value-text"); err != nil { panic(err) }
}
`
	if err := os.WriteFile(filepath.Join(outRoot, "main.go"), []byte(mainSource), 0o644); err != nil {
		t.Fatal(err)
	}
	html, err := runSSRGeneratedProgram(outRoot)
	if err != nil {
		t.Fatal(err)
	}
	clientRoot := NewSSRProjectLayout(outRoot).ClientDir
	entry := `import hydratePage from "./Page.hydrate.js";
window.initialWrites = 0;
const observer = new MutationObserver(() => { window.initialWrites++; });
observer.observe(document.body, { childList: true, subtree: true, characterData: true, attributes: true });
window.hydrated = hydratePage(document.body, {}, "vela-value-text");
window.initialWrites = observer.takeRecords().length;
`
	if err := os.WriteFile(filepath.Join(clientRoot, "entry.js"), []byte(entry), 0o644); err != nil {
		t.Fatal(err)
	}
	page := "<!doctype html><body>" + string(html) + `<script type="module" src="./entry.js"></script></body>`
	if err := os.WriteFile(filepath.Join(clientRoot, "index.html"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	expected, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	browser := `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],expected=JSON.parse(process.argv[2]);const server=http.createServer((q,r)=>{const name=q.url==="/"?"index.html":q.url.slice(1);try{const body=fs.readFileSync(path.join(root,name));r.writeHead(200,{"content-type":name.endsWith(".js")?"text/javascript":"text/html; charset=utf-8"}).end(body)}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;const errors=[];try{b=await chromium.launch({headless:true});const p=await b.newPage();p.on("pageerror",e=>errors.push(String(e.stack||e)));p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);const initial=await p.evaluate(expected=>({mode:window.hydrated.mode,writes:window.initialWrites,status:document.querySelector(".status")?.textContent,values:Object.fromEntries(expected.map(({name})=>[name,document.querySelector(".case-"+name)?.textContent]))}),expected);if(initial.mode!=="executable"||initial.writes!==0||initial.status!=="server")throw new Error("bad value-text adoption: "+JSON.stringify(initial));for(const {name,expected:value} of expected)if(initial.values[name]!==value)throw new Error("bad value-text case "+name+": "+JSON.stringify(initial.values[name])+" want "+JSON.stringify(value));await p.evaluate(expected=>{window.oracleNodes=Object.fromEntries(expected.map(({name})=>[name,document.querySelector(".case-"+name)]))},expected);await p.click(".update");await p.waitForFunction(()=>document.querySelector(".status")?.textContent==="updated");const retained=await p.evaluate(expected=>expected.every(({name})=>document.querySelector(".case-"+name)===window.oracleNodes[name]),expected);if(!retained)throw new Error("independent update recreated value-text nodes");await p.evaluate(()=>window.hydrated.component.destroy());if(await p.$("main"))throw new Error("destroy retained value-text DOM");if(errors.length)throw new Error(errors.join("\n"));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	output, err := exec.Command(node, "-e", browser, clientRoot, string(expected)).CombinedOutput()
	if err != nil {
		t.Fatalf("public value-text SSR browser fixture: %v\n%s", err, output)
	}
}

func TestSSRProjectPublicAttributeOracleHydratesInBrowser(t *testing.T) {
	node := requirePlaywright(t)
	rawCases, err := os.ReadFile(filepath.Join("testdata", "ssr_attribute_oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name       string `json:"name"`
		Kind       string `json:"kind"`
		Attribute  string `json:"attribute"`
		Expression string `json:"expression"`
	}
	if err := json.Unmarshal(rawCases, &cases); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	var source strings.Builder
	source.WriteString(`<script>let title = $state('before'); let disabled = $state(false); function update() { title = 'after'; disabled = true; }</script><template><main>`)
	for _, tc := range cases {
		source.WriteString(`<button class="case-`)
		source.WriteString(tc.Name)
		source.WriteString(`" :`)
		source.WriteString(tc.Attribute)
		source.WriteString(`={`)
		source.WriteString(tc.Expression)
		source.WriteString(`}>`)
		source.WriteString(tc.Name)
		source.WriteString(`</button>`)
	}
	source.WriteString(`<button class="dynamic" :title={title} :disabled={disabled} @click={update}>Update</button></main></template>`)
	component := filepath.Join(srcRoot, "Page.vela")
	if err := os.WriteFile(component, []byte(source.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	projectModule := "example.com/ssrattributebrowser"
	if errors := buildSSRProject([]string{component}, srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard, SSRModule: projectModule + "/server"}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	compilerRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	module := "module " + projectModule + "\n\ngo 1.22\n\nrequire github.com/elauven/vela v0.0.0\n\nreplace github.com/elauven/vela => " + filepath.ToSlash(compilerRoot) + "\n"
	if err := os.WriteFile(filepath.Join(outRoot, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	mainSource := `package main
import (
    "os"
    components "example.com/ssrattributebrowser/server/components"
)
func main() {
    if err := components.RenderPageAt(os.Stdout, components.PageProps{}, components.PageSlots{}, "vela-attribute-oracle"); err != nil { panic(err) }
}
`
	if err := os.WriteFile(filepath.Join(outRoot, "main.go"), []byte(mainSource), 0o644); err != nil {
		t.Fatal(err)
	}
	html, err := runSSRGeneratedProgram(outRoot)
	if err != nil {
		t.Fatal(err)
	}
	clientRoot := NewSSRProjectLayout(outRoot).ClientDir
	entry := `import hydratePage from "./Page.hydrate.js";
window.initialWrites = 0;
const observer = new MutationObserver(() => { window.initialWrites++; });
observer.observe(document.body, { childList: true, subtree: true, characterData: true, attributes: true });
window.hydrated = hydratePage(document.body, {}, "vela-attribute-oracle");
window.initialWrites = observer.takeRecords().length;
`
	if err := os.WriteFile(filepath.Join(clientRoot, "entry.js"), []byte(entry), 0o644); err != nil {
		t.Fatal(err)
	}
	page := "<!doctype html><body>" + string(html) + `<script type="module" src="./entry.js"></script></body>`
	if err := os.WriteFile(filepath.Join(clientRoot, "index.html"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	browser := `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1],cases=JSON.parse(process.argv[2]);const server=http.createServer((q,r)=>{const name=q.url==="/"?"index.html":q.url.slice(1);try{const body=fs.readFileSync(path.join(root,name));r.writeHead(200,{"content-type":name.endsWith(".js")?"text/javascript":"text/html; charset=utf-8"}).end(body)}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;const errors=[];try{b=await chromium.launch({headless:true});const p=await b.newPage();p.on("pageerror",e=>errors.push(String(e.stack||e)));p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);const initial=await p.evaluate(cases=>({mode:window.hydrated.mode,writes:window.initialWrites,dynamic:{title:document.querySelector(".dynamic")?.getAttribute("title"),disabled:document.querySelector(".dynamic")?.disabled},cases:cases.map(tc=>{const node=document.querySelector(".case-"+tc.name);return {name:tc.name,value:tc.kind==="ordinary"?node?.getAttribute(tc.attribute):node?.hasAttribute(tc.attribute)}})}),cases);if(initial.mode!=="executable"||initial.writes!==0||initial.dynamic.title!=="before"||initial.dynamic.disabled)throw new Error("bad attribute adoption: "+JSON.stringify(initial));for(const tc of cases){const actual=initial.cases.find(value=>value.name===tc.name)?.value;if(actual!==tc.expected)throw new Error("bad attribute case "+tc.name+": "+JSON.stringify(actual)+" want "+JSON.stringify(tc.expected))}await p.evaluate(cases=>{window.attributeNodes=Object.fromEntries(cases.map(tc=>[tc.name,document.querySelector(".case-"+tc.name)]));window.dynamicNode=document.querySelector(".dynamic")},cases);await p.click(".dynamic");await p.waitForFunction(()=>document.querySelector(".dynamic")?.getAttribute("title")==="after"&&document.querySelector(".dynamic")?.disabled);const retained=await p.evaluate(cases=>document.querySelector(".dynamic")===window.dynamicNode&&cases.every(tc=>document.querySelector(".case-"+tc.name)===window.attributeNodes[tc.name]),cases);if(!retained)throw new Error("attribute update recreated adopted nodes");await p.evaluate(()=>window.hydrated.component.destroy());if(await p.$("main"))throw new Error("destroy retained attribute DOM");if(errors.length)throw new Error(errors.join("\n"));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	output, err := exec.Command(node, "-e", browser, clientRoot, string(rawCases)).CombinedOutput()
	if err != nil {
		t.Fatalf("public attribute SSR browser fixture: %v\n%s", err, output)
	}
}

func TestSSRProjectPublicChildPropPresenceHydratesUpdatesAndRemountsInBrowser(t *testing.T) {
	node := requirePlaywright(t)
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(srcRoot, "Parent.vela")
	child := filepath.Join(srcRoot, "Child.vela")
	if err := os.WriteFile(parent, []byte(`<script>
  import { Child } from './Child.vela';
  let title = $state('first');
  let shown = $state(true);
  function update() { title = 'updated'; }
  function toggle() { shown = !shown; }
</script>
<template><main>{#if shown}<Child />{/if}<Child :title={title} /><button class="update" @click={update}>Update</button><button class="toggle" @click={toggle}>Toggle</button></main></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte(`<script>export let title = 'fallback';</script><template><p class="child" :data-title={title}>{title}</p></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	projectModule := "example.com/ssrchildbrowser"
	if errors := buildSSRProject([]string{parent, child}, srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard, SSRModule: projectModule + "/server"}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	compilerRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	module := "module " + projectModule + "\n\ngo 1.22\n\nrequire github.com/elauven/vela v0.0.0\n\nreplace github.com/elauven/vela => " + filepath.ToSlash(compilerRoot) + "\n"
	if err := os.WriteFile(filepath.Join(outRoot, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	mainSource := `package main
import (
    "os"
    components "example.com/ssrchildbrowser/server/components"
)
func main() {
    if err := components.RenderParentAt(os.Stdout, components.ParentProps{}, components.ParentSlots{}, "vela-child-props"); err != nil { panic(err) }
}
`
	if err := os.WriteFile(filepath.Join(outRoot, "main.go"), []byte(mainSource), 0o644); err != nil {
		t.Fatal(err)
	}
	html, err := runSSRGeneratedProgram(outRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), `data-title="fallback"`) || !strings.Contains(string(html), `data-title="first"`) {
		t.Fatalf("server child prop values do not preserve omission and explicit value:\n%s", html)
	}
	clientRoot := NewSSRProjectLayout(outRoot).ClientDir
	entry := `import hydrateParent from "./Parent.hydrate.js";
window.initialWrites = 0;
const observer = new MutationObserver(() => { window.initialWrites++; });
observer.observe(document.body, { childList: true, subtree: true, characterData: true, attributes: true });
window.hydrated = hydrateParent(document.body, {}, "vela-child-props");
window.initialWrites = observer.takeRecords().length;
`
	if err := os.WriteFile(filepath.Join(clientRoot, "entry.js"), []byte(entry), 0o644); err != nil {
		t.Fatal(err)
	}
	page := "<!doctype html><body>" + string(html) + `<script type="module" src="./entry.js"></script></body>`
	if err := os.WriteFile(filepath.Join(clientRoot, "index.html"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	browser := `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1];const server=http.createServer((q,r)=>{const name=q.url==="/"?"index.html":q.url.slice(1);try{const body=fs.readFileSync(path.join(root,name));r.writeHead(200,{"content-type":name.endsWith(".js")?"text/javascript":"text/html"}).end(body)}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;const errors=[];try{b=await chromium.launch({headless:true});const p=await b.newPage();p.on("pageerror",e=>errors.push(String(e.stack||e)));p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);const initial=await p.evaluate(()=>({mode:window.hydrated.mode,writes:window.initialWrites,values:[...document.querySelectorAll(".child")].map(node=>node.textContent)}));if(initial.mode!=="executable"||initial.writes!==0||initial.values.join("|")!=="fallback|first")throw new Error("bad child prop adoption: "+JSON.stringify(initial));await p.evaluate(()=>{window.fallbackNode=document.querySelector('[data-title="fallback"]');window.explicitNode=document.querySelector('[data-title="first"]')});await p.click(".update");await p.waitForFunction(()=>[...document.querySelectorAll(".child")].map(node=>node.textContent).join("|")==="fallback|updated");if(!(await p.evaluate(()=>document.querySelector('[data-title="fallback"]')===window.fallbackNode)))throw new Error("parent update recreated omitted-prop child");if(!(await p.evaluate(()=>document.querySelector('[data-title="updated"]')===window.explicitNode)))throw new Error("parent update recreated explicit-prop child");await p.click(".toggle");await p.waitForFunction(()=>document.querySelectorAll(".child").length===1);if(await p.evaluate(()=>window.fallbackNode.isConnected))throw new Error("conditional omitted-prop child was not removed");await p.click(".toggle");await p.waitForFunction(()=>[...document.querySelectorAll(".child")].map(node=>node.textContent).join("|")==="fallback|updated");if(await p.evaluate(()=>document.querySelector('[data-title="fallback"]')===window.fallbackNode))throw new Error("conditional child remount reused destroyed DOM");if(!(await p.evaluate(()=>document.querySelector('[data-title="updated"]')===window.explicitNode)))throw new Error("sibling child changed during conditional remount");await p.evaluate(()=>window.hydrated.component.destroy());if(await p.$(".child"))throw new Error("destroy retained child DOM");if(errors.length)throw new Error(errors.join("\n"));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	output, err := exec.Command(node, "-e", browser, clientRoot).CombinedOutput()
	if err != nil {
		t.Fatalf("public child prop SSR browser fixture: %v\n%s", err, output)
	}
}

func TestSSRProjectPublicStaticOnlyArtifactsValidateInBrowser(t *testing.T) {
	node := requirePlaywright(t)
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(srcRoot, "Static.vela")
	contents := `<template><p class="first">one</p><p class="second">two</p></template>`
	if err := os.WriteFile(source, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	projectModule := "example.com/ssrstatic"
	if errors := buildSSRProject([]string{source}, srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard, SSRModule: projectModule + "/server"}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	compilerRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	module := "module " + projectModule + "\n\ngo 1.22\n\nrequire github.com/elauven/vela v0.0.0\n\nreplace github.com/elauven/vela => " + filepath.ToSlash(compilerRoot) + "\n"
	if err := os.WriteFile(filepath.Join(outRoot, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	mainSource := `package main
import (
    "os"
    components "example.com/ssrstatic/server/components"
)
func main() {
    if err := components.RenderStaticAt(os.Stdout, components.StaticProps{}, components.StaticSlots{}, "vela-static-instance"); err != nil {
        panic(err)
    }
}
`
	if err := os.WriteFile(filepath.Join(outRoot, "main.go"), []byte(mainSource), 0o644); err != nil {
		t.Fatal(err)
	}
	html, err := runSSRGeneratedProgram(outRoot)
	if err != nil {
		t.Fatal(err)
	}
	clientRoot := NewSSRProjectLayout(outRoot).ClientDir
	entry := `import hydrateStatic from "./Static.hydrate.js";
window.initialWrites = 0;
const observer = new MutationObserver(() => { window.initialWrites++; });
observer.observe(document.body, { childList: true, subtree: true, characterData: true, attributes: true });
window.hydrated = hydrateStatic(document.body, {}, "vela-static-instance");
window.initialWrites = observer.takeRecords().length;
`
	if err := os.WriteFile(filepath.Join(clientRoot, "entry.js"), []byte(entry), 0o644); err != nil {
		t.Fatal(err)
	}
	page := "<!doctype html><body>" + string(html) + `<script type="module" src="./entry.js"></script></body>`
	if err := os.WriteFile(filepath.Join(clientRoot, "index.html"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	browser := `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1];const server=http.createServer((q,r)=>{const name=q.url==="/"?"index.html":q.url.slice(1);try{const body=fs.readFileSync(path.join(root,name));r.writeHead(200,{"content-type":name.endsWith(".js")?"text/javascript":"text/html"}).end(body)}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;const errors=[];try{b=await chromium.launch({headless:true});const p=await b.newPage();p.on("pageerror",e=>errors.push(String(e.stack||e)));p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);const result=await p.evaluate(()=>({mode:window.hydrated.mode,writes:window.initialWrites,hasComponent:Object.hasOwn(window.hydrated,"component"),first:document.querySelector(".first")?.textContent,second:document.querySelector(".second")?.textContent}));if(result.mode!=="static-only"||result.writes!==0||result.hasComponent||result.first!=="one"||result.second!=="two")throw new Error("unexpected static-only hydration result: "+JSON.stringify(result));if(errors.length)throw new Error(errors.join("\n"));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	output, err := exec.Command(node, "-e", browser, clientRoot).CombinedOutput()
	if err != nil {
		t.Fatalf("public static-only SSR browser fixture: %v\n%s", err, output)
	}
}

func TestSSRProjectPublicTextareaBindingHydratesInBrowser(t *testing.T) {
	node := requirePlaywright(t)
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(srcRoot, "Form.vela")
	contents := `<script>let value = $state('server');</script><template><main><textarea class="field" bind:value={value}></textarea><p class="mirror">{value}</p></main></template>`
	if err := os.WriteFile(source, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	projectModule := "example.com/ssrtextarea"
	if errors := buildSSRProject([]string{source}, srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard, SSRModule: projectModule + "/server"}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	compilerRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	module := "module " + projectModule + "\n\ngo 1.22\n\nrequire github.com/elauven/vela v0.0.0\n\nreplace github.com/elauven/vela => " + filepath.ToSlash(compilerRoot) + "\n"
	if err := os.WriteFile(filepath.Join(outRoot, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	mainSource := `package main
import (
    "os"
    components "example.com/ssrtextarea/server/components"
)
func main() {
    if err := components.RenderFormAt(os.Stdout, components.FormProps{}, components.FormSlots{}, "vela-form-instance"); err != nil {
        panic(err)
    }
}
`
	if err := os.WriteFile(filepath.Join(outRoot, "main.go"), []byte(mainSource), 0o644); err != nil {
		t.Fatal(err)
	}
	html, err := runSSRGeneratedProgram(outRoot)
	if err != nil {
		t.Fatal(err)
	}
	clientRoot := NewSSRProjectLayout(outRoot).ClientDir
	entry := `import hydrateForm from "./Form.hydrate.js";
window.initialWrites = 0;
const observer = new MutationObserver(() => { window.initialWrites++; });
observer.observe(document.body, { childList: true, subtree: true, characterData: true, attributes: true });
window.hydrated = hydrateForm(document.body, {}, "vela-form-instance");
window.initialWrites = observer.takeRecords().length;
`
	if err := os.WriteFile(filepath.Join(clientRoot, "entry.js"), []byte(entry), 0o644); err != nil {
		t.Fatal(err)
	}
	page := "<!doctype html><body>" + string(html) + `<script type="module" src="./entry.js"></script></body>`
	if err := os.WriteFile(filepath.Join(clientRoot, "index.html"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	browser := `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1];const server=http.createServer((q,r)=>{const name=q.url==="/"?"index.html":q.url.slice(1);try{const body=fs.readFileSync(path.join(root,name));r.writeHead(200,{"content-type":name.endsWith(".js")?"text/javascript":"text/html"}).end(body)}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;const errors=[];try{b=await chromium.launch({headless:true});const p=await b.newPage();p.on("pageerror",e=>errors.push(String(e.stack||e)));p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);const initial=await p.evaluate(()=>({mode:window.hydrated.mode,writes:window.initialWrites,value:document.querySelector(".field")?.value,mirror:document.querySelector(".mirror")?.textContent}));if(initial.mode!=="executable"||initial.writes!==0||initial.value!=="server"||initial.mirror!=="server")throw new Error("unexpected textarea adoption: "+JSON.stringify(initial));await p.fill(".field","typed");await p.waitForFunction(()=>document.querySelector(".mirror")?.textContent==="typed");if(errors.length)throw new Error(errors.join("\n"));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	output, err := exec.Command(node, "-e", browser, clientRoot).CombinedOutput()
	if err != nil {
		t.Fatalf("public textarea SSR browser fixture: %v\n%s", err, output)
	}
}

func TestSSRProjectPublicNullFormBindingsMatchBrowserProperties(t *testing.T) {
	node := requirePlaywright(t)
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(srcRoot, "Form.vela")
	contents := `<script>let inputValue = $state(null); let textareaValue = $state(null); function update() { inputValue = 'input'; textareaValue = 'textarea'; }</script><template><main><input class="input" bind:value={inputValue} /><textarea class="textarea" bind:value={textareaValue}></textarea><button class="update" @click={update}>Update</button></main></template>`
	if err := os.WriteFile(source, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	projectModule := "example.com/ssrnullform"
	if errors := buildSSRProject([]string{source}, srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard, SSRModule: projectModule + "/server"}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	compilerRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	module := "module " + projectModule + "\n\ngo 1.22\n\nrequire github.com/elauven/vela v0.0.0\n\nreplace github.com/elauven/vela => " + filepath.ToSlash(compilerRoot) + "\n"
	if err := os.WriteFile(filepath.Join(outRoot, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	mainSource := `package main
import (
    "os"
    components "example.com/ssrnullform/server/components"
)
func main() {
    if err := components.RenderFormAt(os.Stdout, components.FormProps{}, components.FormSlots{}, "vela-null-form"); err != nil { panic(err) }
}
`
	if err := os.WriteFile(filepath.Join(outRoot, "main.go"), []byte(mainSource), 0o644); err != nil {
		t.Fatal(err)
	}
	html, err := runSSRGeneratedProgram(outRoot)
	if err != nil {
		t.Fatal(err)
	}
	clientRoot := NewSSRProjectLayout(outRoot).ClientDir
	entry := `import hydrateForm from "./Form.hydrate.js";
window.initialWrites = 0;
const observer = new MutationObserver(() => { window.initialWrites++; });
observer.observe(document.body, { childList: true, subtree: true, characterData: true, attributes: true });
window.hydrated = hydrateForm(document.body, {}, "vela-null-form");
window.initialWrites = observer.takeRecords().length;
`
	if err := os.WriteFile(filepath.Join(clientRoot, "entry.js"), []byte(entry), 0o644); err != nil {
		t.Fatal(err)
	}
	page := "<!doctype html><body>" + string(html) + `<script type="module" src="./entry.js"></script></body>`
	if err := os.WriteFile(filepath.Join(clientRoot, "index.html"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	browser := `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1];const server=http.createServer((q,r)=>{const name=q.url==="/"?"index.html":q.url.slice(1);try{const body=fs.readFileSync(path.join(root,name));r.writeHead(200,{"content-type":name.endsWith(".js")?"text/javascript":"text/html; charset=utf-8"}).end(body)}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;const errors=[];try{b=await chromium.launch({headless:true});const p=await b.newPage();p.on("pageerror",e=>errors.push(String(e.stack||e)));p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);const initial=await p.evaluate(()=>({mode:window.hydrated.mode,writes:window.initialWrites,input:document.querySelector(".input")?.value,textarea:document.querySelector(".textarea")?.value}));if(initial.mode!=="executable"||initial.writes!==0||initial.input!==""||initial.textarea!=="")throw new Error("null form adoption differs from browser properties: "+JSON.stringify(initial));await p.evaluate(()=>{window.input=document.querySelector(".input");window.textarea=document.querySelector(".textarea")});await p.click(".update");await p.waitForFunction(()=>document.querySelector(".input")?.value==="input"&&document.querySelector(".textarea")?.value==="textarea");if(!(await p.evaluate(()=>document.querySelector(".input")===window.input&&document.querySelector(".textarea")===window.textarea)))throw new Error("form update recreated adopted controls");await p.evaluate(()=>window.hydrated.component.destroy());if(await p.$("main"))throw new Error("destroy retained form DOM");if(errors.length)throw new Error(errors.join("\n"));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	output, err := exec.Command(node, "-e", browser, clientRoot).CombinedOutput()
	if err != nil {
		t.Fatalf("public null form SSR browser fixture: %v\n%s", err, output)
	}
}

func TestSSRProjectPublicTableAndSelectHydrateInBrowser(t *testing.T) {
	node := requirePlaywright(t)
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(srcRoot, "Report.vela")
	contents := `<script>let selected = $state('two');</script><template><main><table class="report"><tr><td>server row</td></tr></table><select class="field" bind:value={selected}><option value="one">One</option><option value="two">Two</option></select><p class="mirror">{selected}</p></main></template>`
	if err := os.WriteFile(source, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	projectModule := "example.com/ssrtable"
	if errors := buildSSRProject([]string{source}, srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard, SSRModule: projectModule + "/server"}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	compilerRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	module := "module " + projectModule + "\n\ngo 1.22\n\nrequire github.com/elauven/vela v0.0.0\n\nreplace github.com/elauven/vela => " + filepath.ToSlash(compilerRoot) + "\n"
	if err := os.WriteFile(filepath.Join(outRoot, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	mainSource := `package main
import (
    "os"
    components "example.com/ssrtable/server/components"
)
func main() {
    if err := components.RenderReportAt(os.Stdout, components.ReportProps{}, components.ReportSlots{}, "vela-report-instance"); err != nil {
        panic(err)
    }
}
`
	if err := os.WriteFile(filepath.Join(outRoot, "main.go"), []byte(mainSource), 0o644); err != nil {
		t.Fatal(err)
	}
	html, err := runSSRGeneratedProgram(outRoot)
	if err != nil {
		t.Fatal(err)
	}
	clientRoot := NewSSRProjectLayout(outRoot).ClientDir
	entry := `import hydrateReport from "./Report.hydrate.js";
window.initialWrites = 0;
const observer = new MutationObserver(() => { window.initialWrites++; });
observer.observe(document.body, { childList: true, subtree: true, characterData: true, attributes: true });
window.hydrated = hydrateReport(document.body, {}, "vela-report-instance");
window.initialWrites = observer.takeRecords().length;
`
	if err := os.WriteFile(filepath.Join(clientRoot, "entry.js"), []byte(entry), 0o644); err != nil {
		t.Fatal(err)
	}
	page := "<!doctype html><body>" + string(html) + `<script type="module" src="./entry.js"></script></body>`
	if err := os.WriteFile(filepath.Join(clientRoot, "index.html"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	browser := `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");const root=process.argv[1];const server=http.createServer((q,r)=>{const name=q.url==="/"?"index.html":q.url.slice(1);try{const body=fs.readFileSync(path.join(root,name));r.writeHead(200,{"content-type":name.endsWith(".js")?"text/javascript":"text/html"}).end(body)}catch{r.writeHead(404).end()}});server.listen(0,"127.0.0.1",async()=>{let b;const errors=[];try{b=await chromium.launch({headless:true});const p=await b.newPage();p.on("pageerror",e=>errors.push(String(e.stack||e)));p.setDefaultTimeout(5000);await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);const initial=await p.evaluate(()=>({mode:window.hydrated.mode,writes:window.initialWrites,tbody:document.querySelectorAll(".report > tbody").length,value:document.querySelector(".field")?.value,mirror:document.querySelector(".mirror")?.textContent}));if(initial.mode!=="executable"||initial.writes!==0||initial.tbody!==1||initial.value!=="two"||initial.mirror!=="two")throw new Error("unexpected table/select adoption: "+JSON.stringify(initial));await p.selectOption(".field","one");await p.waitForFunction(()=>document.querySelector(".mirror")?.textContent==="one");if(errors.length)throw new Error(errors.join("\n"));process.stdout.write("OK")}catch(e){console.error(e&&e.stack||e);console.error(errors.join("\n"));process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	output, err := exec.Command(node, "-e", browser, clientRoot).CombinedOutput()
	if err != nil {
		t.Fatalf("public table/select SSR browser fixture: %v\n%s", err, output)
	}
}

func runSSRGeneratedProgram(root string) ([]byte, error) {
	command := exec.Command("go", "run", ".")
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("generated SSR go run: %w\n%s", err, output)
	}
	return output, nil
}

func TestSSRProjectPreservesDeveloperPropsSidecar(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(srcRoot, "App.vela")
	if err := os.WriteFile(source, []byte(`<script>export let title="";</script><template><p>{title}</p></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	layout := NewSSRProjectLayout(filepath.Join(root, "out"))
	if err := compileSSRProjectFile(source, srcRoot, layout); err != nil {
		t.Fatal(err)
	}
	custom := []byte("package components\n\ntype AppProps struct {\n\tTitle string\n\tMarker int\n}\n")
	if err := os.WriteFile(layout.propsPath(source, srcRoot), custom, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := compileSSRProjectFile(source, srcRoot, layout); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(layout.propsPath(source, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(custom) {
		t.Fatalf("user props sidecar was overwritten:\n%s", got)
	}
}

func TestValidateSSRProjectPackagesChecksOneDirectoryAsOnePackage(t *testing.T) {
	root := t.TempDir()
	layout := NewSSRProjectLayout(root)
	if err := os.MkdirAll(layout.ServerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout.ServerDir, "first.go"), []byte("package components\n\ntype Shared struct{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout.ServerDir, "second.go"), []byte("package components\n\ntype Shared struct{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := ValidateSSRProjectPackages(layout)
	if err == nil || !strings.Contains(err.Error(), "Shared redeclared") {
		t.Fatalf("package validation error=%v", err)
	}
}

func TestSSRProjectRendersSamePackageChildComponent(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(srcRoot, "Parent.vela")
	child := filepath.Join(srcRoot, "Child.vela")
	if err := os.WriteFile(parent, []byte(`<script>import { Child } from './Child.vela'; export let title="";</script><template><Child :title={title} /></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte(`<script>export let title="";</script><template><p>{title}</p></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if errors := buildSSRProject([]string{parent, child}, srcRoot, filepath.Join(root, "out"), CompileOptions{StatusWriter: io.Discard}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	server, err := os.ReadFile(NewSSRProjectLayout(filepath.Join(root, "out")).serverPath(parent, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(server), "Child(ChildProps{Title: props.Title, Set: map[string]bool{\"title\": true}}, ChildSlots{}).Render(w)") {
		t.Fatalf("child call was not generated:\n%s", server)
	}
}

func TestSSRProjectInternalChildCallRetainsOmittedPropDefault(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(srcRoot, "Parent.vela")
	child := filepath.Join(srcRoot, "Child.vela")
	if err := os.WriteFile(parent, []byte(`<script>import { Child } from './Child.vela';</script><template><Child /></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte(`<script>export let title = 'fallback';</script><template><p>{title}</p></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	projectModule := "example.com/ssrchilddefault"
	if errors := buildSSRProject([]string{parent, child}, srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard, SSRModule: projectModule + "/server"}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	parentSource, err := os.ReadFile(NewSSRProjectLayoutWithModule(outRoot, projectModule+"/server").serverPath(parent, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(parentSource), "Child(ChildProps{Set: map[string]bool{}}, ChildSlots{}).Render(w)") {
		t.Fatalf("internal omitted prop call misses initialized presence map:\n%s", parentSource)
	}
	compilerRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	module := "module " + projectModule + "\n\ngo 1.22\n\nrequire github.com/elauven/vela v0.0.0\n\nreplace github.com/elauven/vela => " + filepath.ToSlash(compilerRoot) + "\n"
	if err := os.WriteFile(filepath.Join(outRoot, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	mainSource := `package main
import (
    "os"
    components "example.com/ssrchilddefault/server/components"
)
func main() {
    if err := components.RenderParentAt(os.Stdout, components.ParentProps{}, components.ParentSlots{}, "parent"); err != nil { panic(err) }
}
`
	if err := os.WriteFile(filepath.Join(outRoot, "main.go"), []byte(mainSource), 0o644); err != nil {
		t.Fatal(err)
	}
	output, err := runSSRGeneratedProgram(outRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), ">fallback<!--/v:") {
		t.Fatalf("omitted child prop did not render its default:\n%s", output)
	}
}

func TestSSRProjectInternalChildCallPreservesExplicitZeroValues(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(srcRoot, "Parent.vela")
	child := filepath.Join(srcRoot, "Child.vela")
	if err := os.WriteFile(parent, []byte(`<script>import { Child } from './Child.vela';</script><template><Child :title={''} :enabled={false} :count={0} /></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte(`<script>export let title = 'fallback'; export let enabled = true; export let count = 7;</script><template><p class="title">{title}</p>{#if enabled}<p class="enabled">yes</p>{:else}<p class="enabled">no</p>{/if}<p class="count">{count}</p></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	projectModule := "example.com/ssrchildzeros"
	if errors := buildSSRProject([]string{parent, child}, srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard, SSRModule: projectModule + "/server"}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	parentSource, err := os.ReadFile(NewSSRProjectLayoutWithModule(outRoot, projectModule+"/server").serverPath(parent, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`Title: ""`, `Enabled: false`, `Count: 0`, `Set: map[string]bool{"title": true, "enabled": true, "count": true}`} {
		if !strings.Contains(string(parentSource), want) {
			t.Fatalf("explicit child prop call misses %q:\n%s", want, parentSource)
		}
	}
	compilerRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	module := "module " + projectModule + "\n\ngo 1.22\n\nrequire github.com/elauven/vela v0.0.0\n\nreplace github.com/elauven/vela => " + filepath.ToSlash(compilerRoot) + "\n"
	if err := os.WriteFile(filepath.Join(outRoot, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	mainSource := `package main
import (
    "os"
    components "example.com/ssrchildzeros/server/components"
)
func main() {
    if err := components.RenderParentAt(os.Stdout, components.ParentProps{}, components.ParentSlots{}, "parent"); err != nil { panic(err) }
}
`
	if err := os.WriteFile(filepath.Join(outRoot, "main.go"), []byte(mainSource), 0o644); err != nil {
		t.Fatal(err)
	}
	output, err := runSSRGeneratedProgram(outRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`class="title"><!--v:`, `class="enabled">no</p>`, `class="count"><!--v:`, `-->0<!--/v:`} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("explicit zero prop semantics miss %q:\n%s", want, output)
		}
	}
	if strings.Contains(string(output), "fallback") || strings.Contains(string(output), ">yes<") {
		t.Fatalf("explicit child zero values were replaced by defaults:\n%s", output)
	}
}

func TestSSRProjectRendersSamePackageDefaultAndNamedSlots(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(srcRoot, "Parent.vela")
	child := filepath.Join(srcRoot, "Child.vela")
	if err := os.WriteFile(parent, []byte(`<script>import { Child } from './Child.vela';</script><template><Child><p>body</p><svelte:fragment slot="actions"><button>save</button></svelte:fragment></Child></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte(`<template><main><slot>fallback</slot><footer><slot name="actions" /></footer></main></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if errors := buildSSRProject([]string{parent, child}, srcRoot, filepath.Join(root, "out"), CompileOptions{StatusWriter: io.Discard}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	layout := NewSSRProjectLayout(filepath.Join(root, "out"))
	parentSource, err := os.ReadFile(layout.serverPath(parent, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	childSource, err := os.ReadFile(layout.serverPath(child, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ChildProps{Set: map[string]bool{}", "ChildSlots{", "Default: func(w io.Writer) error", "Actions: func(w io.Writer) error"} {
		if !strings.Contains(string(parentSource), want) {
			t.Fatalf("parent misses %q:\n%s", want, parentSource)
		}
	}
	for _, want := range []string{"type ChildSlots struct", "Default vela.Slot", "Actions vela.Slot", "slots.Default(w)", "slots.Actions(w)"} {
		if !strings.Contains(string(childSource), want) {
			t.Fatalf("child misses %q:\n%s", want, childSource)
		}
	}
}

func TestSSRProjectRendersCrossPackageChildComponent(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	pagesDir := filepath.Join(srcRoot, "pages")
	uiDir := filepath.Join(srcRoot, "ui")
	if err := os.MkdirAll(pagesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(uiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(pagesDir, "Parent.vela")
	child := filepath.Join(uiDir, "Child.vela")
	if err := os.WriteFile(parent, []byte(`<script>import { Child } from '../ui/Child.vela'; export let title="";</script><template><Child :title={title}><strong>body</strong><svelte:fragment slot="actions"><button>save</button></svelte:fragment></Child></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte(`<script>export let title="";</script><template><article><h1>{title}</h1><slot>fallback</slot><footer><slot name="actions" /></footer></article></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	opts := CompileOptions{StatusWriter: io.Discard, SSRModule: "example.com/site/generated"}
	if errors := buildSSRProject([]string{parent, child}, srcRoot, outRoot, opts, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	layout := NewSSRProjectLayoutWithModule(outRoot, opts.SSRModule)
	parentSource, err := os.ReadFile(layout.serverPath(parent, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	text := string(parentSource)
	for _, want := range []string{
		`ui "example.com/site/generated/components/ui"`,
		"ui.Child(ui.ChildProps{Title: props.Title, Set: map[string]bool{\"title\": true}}, ui.ChildSlots{",
		"Default: func(w io.Writer) error",
		"Actions: func(w io.Writer) error",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("cross-package parent misses %q:\n%s", want, text)
		}
	}
}

func TestGoFileStemUsesLowerSnakeCase(t *testing.T) {
	cases := map[string]string{
		"UserPage":      "user_page",
		"HTTPServer":    "http_server",
		"APIv2Client":   "ap_iv2_client",
		"kebab-case":    "kebab_case",
		"already_snake": "already_snake",
	}
	for input, want := range cases {
		if got := goFileStem(input); got != want {
			t.Errorf("goFileStem(%q)=%q want %q", input, got, want)
		}
	}
}

func TestSSRProjectLayoutMirrorsSnakeCaseGoPath(t *testing.T) {
	layout := NewSSRProjectLayout("out")
	got := filepath.ToSlash(layout.serverPath(filepath.Join("src", "admin", "HTTPServer.vela"), "src"))
	if want := "out/server/components/admin/http_server.go"; got != want {
		t.Fatalf("serverPath=%q want %q", got, want)
	}
}

func TestSSRProjectLayoutUsesSnakeCasePropsPath(t *testing.T) {
	layout := NewSSRProjectLayout("out")
	got := filepath.ToSlash(layout.propsPath(filepath.Join("src", "admin", "HTTPServer.vela"), "src"))
	if want := "out/server/components/admin/http_server_props.go"; got != want {
		t.Fatalf("propsPath=%q want %q", got, want)
	}
}
