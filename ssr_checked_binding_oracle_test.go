package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type ssrCheckedBindingOracleCase struct {
	Name              string `json:"name"`
	InitialExpression string `json:"initial_expression"`
	InitialChecked    bool   `json:"initial_checked"`
	InitialText       string `json:"initial_text"`
	UpdatedExpression string `json:"updated_expression"`
	UpdatedChecked    bool   `json:"updated_checked"`
	UpdatedText       string `json:"updated_text"`
}

func loadSSRCheckedBindingOracle(t *testing.T) ([]ssrCheckedBindingOracleCase, []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "ssr_checked_binding_oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []ssrCheckedBindingOracleCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("SSR checked-binding oracle must not be empty")
	}
	return cases, raw
}

// TestSSRProjectPublicCheckedBindingOracleHydratesInBrowser consumes the same
// matrix as tools/verify-ssr-checked-binding-oracle.mjs. Every row passes
// through Vela parsing, generated Go HTML, browser parsing, zero-write
// hydration, a programmatic update, and a real change event back into state.
func TestSSRProjectPublicCheckedBindingOracleHydratesInBrowser(t *testing.T) {
	legacySSRProtocolTest(t)
	node := requirePlaywright(t)
	cases, rawCases := loadSSRCheckedBindingOracle(t)

	var source strings.Builder
	source.WriteString("<script>\n")
	for _, tc := range cases {
		source.WriteString("let checked_")
		source.WriteString(tc.Name)
		source.WriteString(" = $state(")
		source.WriteString(tc.InitialExpression)
		source.WriteString(");\n")
	}
	source.WriteString("function update() {\n")
	for _, tc := range cases {
		source.WriteString("checked_")
		source.WriteString(tc.Name)
		source.WriteString(" = ")
		source.WriteString(tc.UpdatedExpression)
		source.WriteString(";\n")
	}
	source.WriteString("}\n</script><template><main>\n")
	for _, tc := range cases {
		source.WriteString(`<input type="checkbox" class="case-`)
		source.WriteString(tc.Name)
		source.WriteString(`" bind:checked={checked_`)
		source.WriteString(tc.Name)
		source.WriteString(`} /><p class="mirror-`)
		source.WriteString(tc.Name)
		source.WriteString(`">{checked_`)
		source.WriteString(tc.Name)
		source.WriteString("}</p>\n")
	}
	source.WriteString(`<button class="update" @click={update}>Update</button></main></template>`)

	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	component := filepath.Join(srcRoot, "Page.vela")
	if err := os.WriteFile(component, []byte(source.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	projectModule := "example.com/ssrcheckedbindingoracle"
	if errors := buildSSRProject([]string{component}, srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard, SSRModule: projectModule + "/server"}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	compilerRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	module := "module " + projectModule + "\n\ngo 1.25\n\nrequire github.com/elauven/vela v0.0.0\n\nreplace github.com/elauven/vela => " + filepath.ToSlash(compilerRoot) + "\n"
	if err := os.WriteFile(filepath.Join(outRoot, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	mainSource := `package main
import (
    "os"
    components "example.com/ssrcheckedbindingoracle/server/components"
)
func main() {
    if err := components.RenderPageAt(os.Stdout, components.PageProps{}, components.PageSlots{}, "vela-checked-binding-oracle"); err != nil { panic(err) }
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
const checkedDescriptor = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "checked");
window.checkedWrites = 0;
Object.defineProperty(HTMLInputElement.prototype, "checked", {
  configurable: true,
  get: checkedDescriptor.get,
  set(value) { window.checkedWrites++; return checkedDescriptor.set.call(this, value); }
});
window.initialMutations = 0;
const observer = new MutationObserver(() => { window.initialMutations++; });
observer.observe(document.body, { childList: true, subtree: true, characterData: true, attributes: true });
window.hydrated = hydratePage(document.body, {}, "vela-checked-binding-oracle");
window.initialMutations = observer.takeRecords().length;
window.initialCheckedWrites = window.checkedWrites;
`
	if err := os.WriteFile(filepath.Join(clientRoot, "entry.js"), []byte(entry), 0o644); err != nil {
		t.Fatal(err)
	}
	page := "<!doctype html><body>" + string(html) + `<script type="module" src="./entry.js"></script></body>`
	if err := os.WriteFile(filepath.Join(clientRoot, "index.html"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}

	const browser = `const fs=require("fs"),path=require("path"),http=require("http"),{chromium}=require("playwright");
const root=process.argv[1],cases=JSON.parse(process.argv[2]);
const server=http.createServer((q,r)=>{const name=q.url==="/"?"index.html":q.url.slice(1);try{const body=fs.readFileSync(path.join(root,name));r.writeHead(200,{"content-type":name.endsWith(".js")?"text/javascript":"text/html; charset=utf-8"}).end(body)}catch{r.writeHead(404).end()}});
server.listen(0,"127.0.0.1",async()=>{let b;const errors=[];try{
  b=await chromium.launch({headless:true});const p=await b.newPage();p.on("pageerror",e=>errors.push(String(e.stack||e)));p.setDefaultTimeout(5000);
  await p.goto("http://127.0.0.1:"+server.address().port+"/");await p.waitForFunction(()=>window.hydrated);
  const read=phase=>p.evaluate(({cases,phase})=>({mode:window.hydrated.mode,mutations:window.initialMutations,initialCheckedWrites:window.initialCheckedWrites,checkedWrites:window.checkedWrites,values:cases.map(tc=>({name:tc.name,checked:document.querySelector(".case-"+tc.name)?.checked,text:document.querySelector(".mirror-"+tc.name)?.textContent,expectedChecked:tc[phase+"_checked"],expectedText:tc[phase+"_text"]}))}),{cases,phase});
  const assertValues=(label,result)=>{for(const value of result.values)if(value.checked!==value.expectedChecked||value.text!==value.expectedText)throw new Error(label+" "+value.name+": "+JSON.stringify(value))};
  let result=await read("initial");
  if(result.mode!=="executable"||result.mutations!==0||result.initialCheckedWrites!==0)throw new Error("checked adoption wrote DOM: "+JSON.stringify(result));
  assertValues("initial",result);
  await p.evaluate(cases=>{window.checkedNodes=Object.fromEntries(cases.map(tc=>[tc.name,document.querySelector(".case-"+tc.name)]));window.checkedWrites=0},cases);
  await p.click(".update");
  await p.waitForFunction(cases=>cases.every(tc=>document.querySelector(".case-"+tc.name)?.checked===tc.updated_checked&&document.querySelector(".mirror-"+tc.name)?.textContent===tc.updated_text),cases);
  result=await read("updated");assertValues("updated",result);
  if(result.checkedWrites!==cases.length)throw new Error("programmatic checked writes="+result.checkedWrites+", want "+cases.length);
  const retained=await p.evaluate(cases=>cases.every(tc=>document.querySelector(".case-"+tc.name)===window.checkedNodes[tc.name]),cases);
  if(!retained)throw new Error("programmatic update replaced adopted checkbox nodes");
  await p.evaluate(cases=>{for(const tc of cases)document.querySelector(".case-"+tc.name).checked=!tc.updated_checked;window.checkedWrites=0;for(const tc of cases)document.querySelector(".case-"+tc.name).dispatchEvent(new Event("change",{bubbles:true}))},cases);
  await p.waitForFunction(cases=>cases.every(tc=>document.querySelector(".mirror-"+tc.name)?.textContent===String(!tc.updated_checked)),cases);
  if((await p.evaluate(()=>window.checkedWrites))!==0)throw new Error("change binding replayed identical checked properties");
  await p.evaluate(()=>window.hydrated.component.destroy());
  const destroyed=await p.evaluate(()=>({main:document.querySelector("main"),payload:document.getElementById("vela-state-vela-checked-binding-oracle")}));
  if(destroyed.main||destroyed.payload)throw new Error("destroy retained checked-binding DOM or payload");
  if(errors.length)throw new Error(errors.join("\n"));process.stdout.write("OK");
}catch(e){console.error(e&&e.stack||e);console.error(errors.join("\n"));process.exitCode=1}finally{await b?.close();server.close(()=>process.exit())}});`
	output, err := exec.Command(node, "-e", browser, clientRoot, string(rawCases)).CombinedOutput()
	if err != nil {
		t.Fatalf("public checked-binding SSR browser fixture: %v\n%s\ncomponent:\n%s", err, output, source.String())
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("public checked-binding SSR browser output=%q", got)
	}
}
