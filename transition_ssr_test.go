package main

// Transitions under SSR and hydration (task 096): the server writes no
// trace of the directives, hydration plays nothing and reports no
// mismatch, and the adopted elements play their outro and intro later.

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var transitionSSRFixture = map[string]string{
	"App.vela": `<script>
  import { fade } from 'vela/transition';
  import { Row } from './Row.vela';
  let show = $state(true);
  let items = $state(['x', 'y']);
</script>

<template>
  <main>
    <button class="toggle" @click={() => (show = !show)}>toggle</button>
    <button class="drop" @click={() => (items = items.slice(1))}>drop</button>
    {#if show}<p transition:fade={{ duration: 100 }}>note</p>{/if}
    <ul>{#each items as item (item)}<Row :name={item} />{/each}</ul>
  </main>
</template>
`,
	"Row.vela": `<script>
  import { slide } from 'vela/transition';
  export let name = '';
</script>

<template><li out:slide>{name}</li></template>
`,
}

const transitionSSRScript = `
import { pathToFileURL } from 'node:url';
import { createRequire } from 'node:module';
import { readFileSync } from 'node:fs';
const [jsdomPath, clientDir, htmlFile] = process.argv.slice(2);
const { JSDOM } = createRequire(import.meta.url)(jsdomPath);
const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'https://vela.test/' });
for (const name of ['window', 'document', 'Node', 'Event', 'CustomEvent', 'MutationObserver', 'HTMLElement']) globalThis[name] = dom.window[name] || dom.window;
globalThis.getComputedStyle = dom.window.getComputedStyle.bind(dom.window);
const running = new Set();
let started = 0;
dom.window.Element.prototype.animate = function () {
  let done;
  const animation = { finished: new Promise((resolve, reject) => { done = { resolve, reject }; }), currentTime: 0, cancel() { if (running.delete(animation)) done.reject(new Error('cancelled')); }, finish() { if (running.delete(animation)) done.resolve(); } };
  running.add(animation);
  started++;
  return animation;
};
const load = (file) => import(pathToFileURL(clientDir + '/' + file).href);
const failures = [];
const expect = (ok, message) => { if (!ok) failures.push(message); };
const settle = async () => { for (let i = 0; i < 20; i++) await new Promise((r) => setTimeout(r, 0)); };
const target = document.createElement('div');
document.body.appendChild(target);
target.innerHTML = readFileSync(htmlFile, 'utf8');
const { App } = await load('App.js');
const { hydrate } = await load('runtime-hydrate.js');
const { $$debugStats } = await load('runtime-blocks.js');
const reports = [];
const app = hydrate(App, target, { policy: 'fail', onMismatch: (report) => reports.push(report) });
await settle();
expect(reports.length === 0, 'hydration mismatches: ' + JSON.stringify(reports));
expect(started === 0, 'animations during hydration: ' + started);
const count = (selector) => target.querySelectorAll(selector).length;
const click = (selector) => target.querySelector(selector).dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }));
const finish = async () => { for (const animation of [...running]) animation.finish(); await settle(); };
click('.toggle');
click('.drop');
await settle();
expect(started === 2, 'outros of the adopted paragraph and row: ' + started);
expect(count('p') === 1 && count('li') === 2, 'the adopted elements stay for their outro: ' + target.innerHTML);
expect($$debugStats().leaving === 2, 'leaving ranges: ' + $$debugStats().leaving);
await finish();
expect(count('p') === 0 && count('li') === 1, 'after the outros: ' + target.innerHTML);
click('.toggle');
await settle();
expect(started === 3 && count('p') === 1, 'intro after hydration: ' + started);
click('.toggle');
await settle();
app.destroy();
expect(count('p') === 0 && $$debugStats().leaving === 0, 'destroy during an outro: ' + target.innerHTML + ' leaving ' + $$debugStats().leaving);
process.stdout.write(JSON.stringify(failures));
`

func TestTransitionSSRAndHydration(t *testing.T) {
	node, jsdom := requireJsdom(t)
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not available")
	}
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	for name, source := range transitionSSRFixture {
		writeTestFile(t, filepath.Join(srcRoot, name), source)
	}
	outRoot := filepath.Join(root, "out")
	module := "example.com/transitions"
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
	for _, unwanted := range []string{"transition", "fade", "slide", "out:"} {
		if strings.Contains(string(html), unwanted) {
			t.Errorf("server HTML contains %q:\n%s", unwanted, html)
		}
	}
	page := filepath.Join(root, "page.html")
	writeTestFile(t, page, string(html))
	script := filepath.Join(root, "transition-run.mjs")
	writeTestFile(t, script, transitionSSRScript)
	command := exec.Command(node, script, jsdom, clientDir, page)
	var stderr strings.Builder
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		t.Fatalf("%v\n%s%s", err, out, stderr.String())
	}
	var failures []string
	if err := json.Unmarshal(out, &failures); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, failure := range failures {
		t.Error(failure)
	}
}
