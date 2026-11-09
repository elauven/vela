package main

// use: under SSR and hydration (task 093): the server writes no trace of
// the directive and never translates the action; after hydration the
// actions start on the adopted nodes once the commit removed the payload,
// with no mismatch, and update and destroy as on the client.

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var actionFixture = map[string]string{
	"App.vela": `<script>
  import { Row } from './Row.vela';
  import { tip, log } from './tip.js';
  let show = $state(true);
  let label = $state('a');
  let items = $state(['x', 'y']);
  function focus(node) {
    log('focus:' + node.localName + ':' + committed());
  }
  function committed() {
    return node().isConnected && !document.querySelector('script[data-vela-root]');
  }
  function node() {
    return document.querySelector('main');
  }
</script>

<template>
  <main use:focus>
    <button class="toggle" @click={() => (show = !show)}>toggle</button>
    <button class="label" @click={() => (label = label + 'a')}>label</button>
    <button class="drop" @click={() => (items = items.slice(1))}>drop</button>
    {#if show}<p use:tip={label}>{label}</p>{/if}
    {#each items as item (item)}<Row :name={item} />{/each}
  </main>
</template>
`,
	"Row.vela": `<script>
  import { tip } from './tip.js';
  export let name = '';
</script>

<template><span use:tip={name}>{name}</span></template>
`,
	"tip.js": `export function log(entry) {
  globalThis.__log = (globalThis.__log ? globalThis.__log + ' ' : '') + entry;
}

export function tip(node, text) {
  log('start:' + node.localName + ':' + text + ':' + (node.isConnected && !document.querySelector('script[data-vela-root]')));
  return {
    update(next) {
      log('update:' + next);
    },
    destroy() {
      log('destroy:' + text);
    },
  };
}
`,
}

const actionRuntimeScript = `
import { pathToFileURL } from 'node:url';
import { createRequire } from 'node:module';
import { readFileSync } from 'node:fs';
const [jsdomPath, clientDir, mode, htmlFile] = process.argv.slice(2);
const { JSDOM } = createRequire(import.meta.url)(jsdomPath);
const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'https://vela.test/' });
for (const name of ['window', 'document', 'Node', 'Event', 'CustomEvent', 'MutationObserver', 'HTMLElement']) globalThis[name] = dom.window[name] || dom.window;
const load = (file) => import(pathToFileURL(clientDir + '/' + file).href);
const failures = [];
const expect = (ok, message) => { if (!ok) failures.push(message); };
const settle = async () => { for (let i = 0; i < 20; i++) await new Promise((r) => setTimeout(r, 0)); };
const target = document.createElement('div');
document.body.appendChild(target);
const { App } = await load('App.js');
let app;
if (mode === 'csr') {
  const { mount } = await load('runtime.js');
  app = mount(App, { target, props: {} });
} else {
  target.innerHTML = readFileSync(htmlFile, 'utf8');
  const { hydrate } = await load('runtime-hydrate.js');
  const reports = [];
  app = hydrate(App, target, { policy: 'fail', onMismatch: (report) => reports.push(report) });
  expect(reports.length === 0, 'hydration mismatches: ' + JSON.stringify(reports));
}
const { $$debugStats } = await load('runtime-blocks.js');
await settle();
const log = () => (globalThis.__log || '').split(' ').sort().join(' ');
const want = (entries, when) => {
  const sorted = entries.split(' ').sort().join(' ');
  expect(log() === sorted, when + ': log is ' + JSON.stringify(globalThis.__log) + ', want (in any order) ' + JSON.stringify(entries));
};
const start = 'focus:main:true start:p:a:true start:span:x:true start:span:y:true';
want(start, 'first render');
expect($$debugStats().actions === 3, 'live actions after the first render: ' + $$debugStats().actions);
const click = (selector) => target.querySelector(selector).dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }));
click('.label');
await settle();
want(start + ' update:aa', 'after the parameter changed');
click('.toggle');
click('.drop');
await settle();
want(start + ' update:aa destroy:a destroy:x', 'after the branch and the row went away');
click('.toggle');
await settle();
want(start + ' update:aa destroy:a destroy:x start:p:aa:true', 'after the branch came back');
app.destroy();
want(start + ' update:aa destroy:a destroy:x start:p:aa:true destroy:aa destroy:y', 'after destroy');
expect($$debugStats().actions === 0, 'live actions after destroy: ' + $$debugStats().actions);
process.stdout.write(JSON.stringify(failures));
`

func TestActionSSRAndHydration(t *testing.T) {
	node, jsdom := requireJsdom(t)
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not available")
	}
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	for name, source := range actionFixture {
		writeTestFile(t, filepath.Join(srcRoot, name), source)
	}
	outRoot := filepath.Join(root, "out")
	module := "example.com/actions"
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
	for _, unwanted := range []string{"use:", "tip", "focus"} {
		if strings.Contains(string(html), unwanted) {
			t.Errorf("server HTML contains %q:\n%s", unwanted, html)
		}
	}
	page := filepath.Join(root, "page.html")
	writeTestFile(t, page, string(html))
	script := filepath.Join(root, "action-run.mjs")
	writeTestFile(t, script, actionRuntimeScript)
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
