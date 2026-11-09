package main

// <vela:window> and <vela:document> (task 095): listeners on window and
// document that leave with the component, and read-only bindings whose
// first client value is the server's and that follow the live value after
// the mount or the hydration commit.

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var globalFixture = map[string]string{
	"App.vela": `<script>
  import { Panel } from './Panel.vela';
  let width = $state(0);
  let scroll = $state(0);
  let online = $state(false);
  let visibility = $state('unknown');
  let keys = $state('');
  let show = $state(true);
  function onKey(event) {
    keys = keys + event.key;
  }
</script>

<template>
  <vela:window @keydown={onKey} bind:innerWidth={width} bind:scrollY={scroll} bind:online={online} />
  <vela:document @visibilitychange={() => (keys = keys + '!')} bind:visibilityState={visibility} />
  <main>
    <p class="values">{width}|{scroll}|{online}|{visibility}|{keys}</p>
    <button class="toggle" @click={() => (show = !show)}>toggle</button>
    {#if show}<Panel />{/if}
  </main>
</template>
`,
	"Panel.vela": `<script>
  let resized = $state(0);
</script>

<template>
  <vela:window @resize={() => resized++} />
  <p class="panel">resized {resized}</p>
</template>
`,
}

const globalRuntimeScript = `
import { pathToFileURL } from 'node:url';
import { createRequire } from 'node:module';
import { readFileSync } from 'node:fs';
const [jsdomPath, clientDir, mode, htmlFile] = process.argv.slice(2);
const { JSDOM } = createRequire(import.meta.url)(jsdomPath);
const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'https://vela.test/' });
Object.defineProperty(globalThis, 'navigator', { configurable: true, value: dom.window.navigator });
for (const name of ['window', 'document', 'Node', 'Event', 'CustomEvent', 'MutationObserver', 'HTMLElement']) globalThis[name] = dom.window[name] || dom.window;
// Net listeners the compiled code put on window and document, counted at
// the DOM (jsdom adds its own on the first user event).
let live = 0;
for (const target of [dom.window, dom.window.document]) {
  const add = target.addEventListener.bind(target);
  const remove = target.removeEventListener.bind(target);
  target.addEventListener = (type, handler, options) => {
    if (!new Error().stack.includes(clientDir)) return add(type, handler, options);
    handler.__velaKeys = handler.__velaKeys || {};
    if (!handler.__velaKeys[type]) { handler.__velaKeys[type] = true; live++; }
    add(type, handler, options);
  };
  target.removeEventListener = (type, handler, options) => {
    if (handler.__velaKeys && handler.__velaKeys[type]) { delete handler.__velaKeys[type]; live--; }
    remove(type, handler, options);
  };
}
const load = (file) => import(pathToFileURL(clientDir + '/' + file).href);
const failures = [];
const expect = (ok, message) => { if (!ok) failures.push(message); };
const settle = async () => { for (let i = 0; i < 20; i++) await new Promise((r) => setTimeout(r, 0)); };
const target = document.createElement('div');
document.body.appendChild(target);
const { App } = await load('App.js');
const { $$debugStats } = await load('runtime-blocks.js');
const before = { ...$$debugStats(), live };
let app;
if (mode === 'csr') {
  const { mount } = await load('runtime.js');
  app = mount(App, { target, props: {} });
} else {
  target.innerHTML = readFileSync(htmlFile, 'utf8');
  expect(target.querySelector('.values').textContent === '0|0|false|unknown|', 'server values: ' + target.querySelector('.values').textContent);
  const { hydrate } = await load('runtime-hydrate.js');
  const reports = [];
  app = hydrate(App, target, { policy: 'fail', onMismatch: (report) => reports.push(report) });
  expect(reports.length === 0, 'hydration mismatches: ' + JSON.stringify(reports));
}
await settle();
const text = (selector) => (target.querySelector(selector) || { textContent: null }).textContent;
const want = (selector, value, when) => expect(text(selector) === value, when + ': ' + selector + ' is ' + JSON.stringify(text(selector)) + ', want ' + JSON.stringify(value));
want('.values', '1024|0|true|' + document.visibilityState + '|', 'after the mount');
dom.window.innerWidth = 500;
dom.window.dispatchEvent(new dom.window.Event('resize'));
dom.window.scrollY = 40;
dom.window.dispatchEvent(new dom.window.Event('scroll'));
Object.defineProperty(dom.window.navigator, 'onLine', { configurable: true, get: () => false });
dom.window.dispatchEvent(new dom.window.Event('offline'));
Object.defineProperty(dom.window.document, 'visibilityState', { configurable: true, get: () => 'hidden' });
document.dispatchEvent(new dom.window.Event('visibilitychange'));
document.body.dispatchEvent(new dom.window.KeyboardEvent('keydown', { key: 'k', bubbles: true }));
await settle();
want('.values', '500|40|false|hidden|!k', 'after the events');
want('.panel', 'resized 1', 'after the resize');
target.querySelector('.toggle').dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }));
await settle();
const withoutPanel = live;
dom.window.dispatchEvent(new dom.window.Event('resize'));
target.querySelector('.toggle').dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }));
await settle();
want('.panel', 'resized 0', 'after the panel came back');
expect(live === withoutPanel + 1, 'the panel adds one window listener: ' + withoutPanel + ' then ' + live);
app.destroy();
expect(live === before.live, 'window and document listeners after destroy: ' + live + ', before the mount ' + before.live);
expect($$debugStats().listeners === before.listeners, 'runtime listeners after destroy: ' + $$debugStats().listeners);
if (mode === 'csr') {
  const { mount } = await load('runtime.js');
  for (let i = 0; i < 1000; i++) mount(App, { target, props: {} }).destroy();
  await settle();
  const after = $$debugStats();
  expect(live === before.live, 'window and document listeners after 1000 cycles: ' + live + ', before ' + before.live);
  expect(after.listeners === before.listeners && after.owners === before.owners, 'runtime stats after 1000 cycles: ' + JSON.stringify(after) + ', before ' + JSON.stringify(before));
}
process.stdout.write(JSON.stringify(failures));
`

func TestGlobalElementsSSRAndHydration(t *testing.T) {
	node, jsdom := requireJsdom(t)
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not available")
	}
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	for name, source := range globalFixture {
		writeTestFile(t, filepath.Join(srcRoot, name), source)
	}
	outRoot := filepath.Join(root, "out")
	module := "example.com/globals"
	if errors := BuildSSRProject(CollectVela(srcRoot), srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard, SSRModule: module + "/server"}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	clientDir := filepath.Join(outRoot, "client")
	writeTestFile(t, filepath.Join(clientDir, "package.json"), `{"type":"module"}`)
	compilerRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(outRoot, "go.mod"), "module "+module+"\n\ngo 1.25\n\n"+ssrRuntimeRequire(compilerRoot))
	writeTestFile(t, filepath.Join(outRoot, "render", "main.go"), `package main

import (
	"os"

	c "`+module+`/server/components"
)

func main() {
	if err := c.RenderApp(os.Stdout, c.AppProps{Set: map[string]bool{}}, c.AppSlots{}); err != nil {
		panic(err)
	}
}
`)
	render := exec.Command("go", "run", "./render")
	render.Dir = outRoot
	render.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
	var renderErr strings.Builder
	render.Stderr = &renderErr
	html, err := render.Output()
	if err != nil {
		t.Fatalf("render: %v\n%s", err, renderErr.String())
	}
	for _, unwanted := range []string{"vela:", "keydown", "visibilitychange", "innerWidth"} {
		if strings.Contains(string(html), unwanted) {
			t.Errorf("server HTML contains %q:\n%s", unwanted, html)
		}
	}
	page := filepath.Join(root, "page.html")
	writeTestFile(t, page, string(html))
	script := filepath.Join(root, "global-run.mjs")
	writeTestFile(t, script, globalRuntimeScript)
	for _, mode := range []string{"csr", "hydrate"} {
		command := exec.Command(node, script, jsdom, clientDir, mode, page)
		var stderr strings.Builder
		command.Stderr = &stderr
		out, err := command.Output()
		if err != nil {
			t.Fatalf("%s: %v\n%s%s", mode, err, out, stderr.String())
		}
		var failures []string
		if err := json.Unmarshal(out, &failures); err != nil {
			t.Fatalf("%s: %v\n%s", mode, err, out)
		}
		for _, failure := range failures {
			t.Errorf("%s: %s", mode, failure)
		}
	}
}

func TestGlobalElementsDiagnostics(t *testing.T) {
	cases := []struct {
		name, template, script, want string
	}{
		{"nested", `<div><vela:window @resize={f} /></div>`, `function f() {}`, "VELA-GLOBAL-PLACEMENT"},
		{"in block", `{#if a}<vela:document @click={f} />{/if}<p>x</p>`, `let a = $state(true); function f() {}`, "VELA-GLOBAL-PLACEMENT"},
		{"twice", `<vela:window @resize={f} /><vela:window @scroll={f} /><p>x</p>`, `function f() {}`, "at most one <vela:window>"},
		{"content", `<vela:window><p>x</p></vela:window><p>x</p>`, ``, "VELA-GLOBAL-CONTENT"},
		{"attribute", `<vela:window class="a" /><p>x</p>`, ``, "takes only @event listeners"},
		{"unknown binding", `<vela:window bind:visibilityState={v} /><p>x</p>`, `let v = $state('');`, "cannot bind \"visibilityState\""},
		{"plain target", `<vela:window bind:innerWidth={w} /><p>{w}</p>`, `let w = 0;`, "VELA-GLOBAL-BINDING"},
		{"svelte window", `<svelte:window on:resize={f} /><p>x</p>`, `function f() {}`, "VELA-SVELTE-WINDOW"},
		{"svelte document", `<svelte:document /><p>x</p>`, ``, "use <vela:document>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			source := "<script>" + c.script + "</script>\n<template>" + c.template + "</template>\n"
			svc := &CompilerService{Cwd: mustGetwd(t)}
			res := svc.Compile(ServiceRequest{Path: "App.vela", Source: &source})
			var all []string
			for _, d := range res.Diagnostics {
				all = append(all, d.Code+": "+d.Message)
			}
			if !strings.Contains(strings.Join(all, "\n")+res.Error, c.want) {
				t.Fatalf("diagnostics %v (error %q) do not mention %q", all, res.Error, c.want)
			}
		})
	}
}

func TestGlobalElementsOnly(t *testing.T) {
	// A template of only a <vela:window> renders nothing in place.
	src := `<script>
  let n = $state(0);
</script>
<template><vela:window @keydown={() => n++} /></template>`
	source := src
	svc := &CompilerService{Cwd: mustGetwd(t)}
	res := svc.Compile(ServiceRequest{Path: "Only.vela", Source: &source})
	if res.Error != "" {
		t.Fatalf("compile: %s %v", res.Error, res.Diagnostics)
	}
}
