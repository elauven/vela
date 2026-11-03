package main

// lazy() components (task 071): loading on demand in the browser runtime,
// server rendering of the target, and hydration with preloadHydration().

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const lazyAppSource = `<script>
  import { lazy } from 'vela';
  const Settings = lazy(() => import('./panels/Settings.vela'));
  export let open = false;
  let shown = $state(open);
</script>

<template>
  <main>
    <button class="open" @click={() => (shown = !shown)}>Settings</button>
    {#if shown}
      <vela:component :this={Settings} title="Prefs" />
    {/if}
  </main>
</template>
`

const lazySettingsSource = `<script>
  export let title = 'Settings';
  let clicks = $state(0);
</script>

<template>
  <section class="settings"><h2>{title}</h2><button class="hit" @click={() => clicks++}>{clicks}</button></section>
</template>
`

// lazyViewSource renders any value given as a prop, so the test controls
// the lazy values and their loaders.
const lazyViewSource = `<script>
  export let view = null;
</script>

<template>
  <div class="view"><vela:component :this={view} label="x" /></div>
</template>
`

// lazyRuntimeScript drives the client modules in jsdom. Its first argument
// is the client directory; it prints a JSON list of failures.
const lazyRuntimeScript = `
import { pathToFileURL } from 'node:url';
import { createRequire } from 'node:module';
const [jsdomPath, clientDir, mode, htmlFile] = process.argv.slice(2);
const { JSDOM } = createRequire(import.meta.url)(jsdomPath);
const dom = new JSDOM('<!doctype html><html><body></body></html>', { url: 'https://vela.test/' });
for (const name of ['window', 'document', 'Node', 'Event', 'CustomEvent', 'MutationObserver', 'HTMLElement']) globalThis[name] = dom.window[name] || dom.window;
globalThis.window = dom.window;
const load = (file) => import(pathToFileURL(clientDir + '/' + file).href);
const failures = [];
const expect = (ok, message) => { if (!ok) failures.push(message); };
const settle = async () => { for (let i = 0; i < 20; i++) await new Promise((r) => setTimeout(r, 0)); };
const warnings = [];
console.warn = (...args) => warnings.push(args.join(' '));

if (mode === 'csr') {
  const { lazy } = await load('runtime.js');
  const { View } = await load('View.js');
  const { Settings } = await load('panels/Settings.js');
  const deferred = () => { let resolve, reject; const promise = new Promise((a, b) => { resolve = a; reject = b; }); return { promise, resolve, reject }; };
  const a = deferred(), b = deferred(), c = deferred();
  const lazyA = lazy(() => a.promise, 'test/A');
  const lazyB = lazy(() => b.promise, 'test/B');
  const lazyC = lazy(() => c.promise, 'test/C');
  expect(lazy(() => null, 'test/A') === lazyA, 'one lazy value per ID');
  const target = document.createElement('div');
  document.body.appendChild(target);
  const view = View(target, { view: lazyA });
  const text = () => target.querySelector('.view').textContent;
  expect(text() === '' && !lazyA.loaded, 'an unloaded lazy value renders nothing at first: ' + text());
  a.resolve({ default: Settings });
  await settle();
  expect(lazyA.loaded && text().startsWith('Settings'), 'mounts after the module arrives: ' + text());
  target.querySelector('.hit').dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }));
  await settle();
  view.set({ view: lazyA });
  await settle();
  expect(text() === 'Settings1', 'the same lazy value keeps the child: ' + text());
  // :this changes while B loads: the late module is not mounted.
  view.set({ view: lazyB });
  await settle();
  expect(text() === '', 'switching to an unloaded value removes the old child: ' + text());
  view.set({ view: null });
  b.resolve({ default: Settings });
  await settle();
  expect(text() === '', 'a module arriving after :this changed is not mounted: ' + text());
  view.set({ view: lazyB });
  await settle();
  expect(text() === 'Settings0', 'a loaded lazy value mounts at once: ' + text());
  // A failed load warns with the ID and leaves the range empty.
  view.set({ view: lazyC });
  c.reject(new Error('offline'));
  await settle();
  expect(text() === '', 'a failed load renders nothing: ' + text());
  expect(warnings.some((w) => w.includes('the lazy component test/C failed to load: offline')), 'the failure is reported: ' + JSON.stringify(warnings));
  view.destroy();
  // The compiled App registered its lazy component when its module loaded.
  const { $$lazyById } = await load('runtime.js');
  const { App } = await load('App.js');
  const registered = $$lazyById('panels/Settings');
  expect(registered && !registered.loaded, 'App.js registers panels/Settings when it is evaluated');
  const host = document.createElement('div');
  document.body.appendChild(host);
  const app = App(host, {});
  host.querySelector('.open').dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }));
  await settle();
  expect(host.querySelector('.settings h2')?.textContent === 'Prefs', 'opening the panel loads and mounts it: ' + host.innerHTML);
  app.destroy();
} else {
  const { readFileSync } = await import('node:fs');
  const html = readFileSync(htmlFile, 'utf8');
  const container = document.createElement('div');
  document.body.appendChild(container);
  container.innerHTML = html;
  const before = container.innerHTML.replace(/<script[^]*<\/script>/, '');
  const { hydrate, preloadHydration } = await load('runtime-hydrate.js');
  const { App } = await load('App.js');
  if (mode === 'preload') await preloadHydration(container);
  const reports = [];
  const app = hydrate(App, container, { onMismatch: (report) => reports.push(report) });
  await settle();
  if (mode === 'preload') {
    expect(reports.length === 0, 'no mismatch after preloadHydration: ' + JSON.stringify(reports));
    expect(container.innerHTML === before, 'the server DOM is adopted unchanged:\n' + container.innerHTML + '\n' + before);
  } else {
    expect(reports.some((r) => r.kind === 'structure' && r.message.includes('lazy-not-loaded') && r.message.includes('panels/Settings')), 'lazy-not-loaded is reported: ' + JSON.stringify(reports));
    expect(container.querySelector('.settings h2')?.textContent === 'Prefs', 'repair mounts the component after it loads: ' + container.innerHTML);
  }
  container.querySelector('.settings .hit').dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }));
  await settle();
  expect(container.querySelector('.settings .hit').textContent === '1', 'the hydrated component is interactive');
  app.destroy();
}
process.stdout.write(JSON.stringify(failures));
`

func TestLazyComponents(t *testing.T) {
	node, jsdom := requireJsdom(t)
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not available")
	}
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	for name, source := range map[string]string{"App.vela": lazyAppSource, "View.vela": lazyViewSource, "panels/Settings.vela": lazySettingsSource} {
		writeTestFile(t, filepath.Join(srcRoot, filepath.FromSlash(name)), source)
	}
	outRoot := filepath.Join(root, "out")
	module := "example.com/lazy"
	if errors := BuildSSRProject(CollectVela(srcRoot), srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard, SSRModule: module + "/server"}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	clientDir := filepath.Join(outRoot, "client")
	writeTestFile(t, filepath.Join(clientDir, "package.json"), `{"type":"module"}`)
	app, err := os.ReadFile(filepath.Join(clientDir, "App.js"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"lazy(() => import('./panels/Settings.js'), 'panels/Settings');\n",
		"const Settings = lazy(() => import('./panels/Settings.js'), 'panels/Settings');",
	} {
		if !strings.Contains(string(app), want) {
			t.Errorf("App.js misses %q:\n%s", want, app)
		}
	}
	server, err := os.ReadFile(filepath.Join(outRoot, "server", "components", "app.go"))
	if err != nil || !strings.Contains(string(server), "ssrL_Settings := ssrc_example_com_lazy_server_components_panels.SettingsComponent") {
		t.Errorf("app.go: %v\n%s", err, server)
	}

	// Render the page with the panel open.
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
	if err := c.RenderApp(os.Stdout, c.AppProps{Set: map[string]bool{"open": true}, Open: true}, c.AppSlots{}); err != nil {
		panic(err)
	}
}
`)
	render := exec.Command("go", "run", "./render")
	render.Dir = outRoot
	render.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
	html, err := render.Output()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(string(html), `["d","App#1","panels/Settings"]`) || !strings.Contains(string(html), "<h2>Prefs</h2>") {
		t.Fatalf("server HTML:\n%s", html)
	}
	page := filepath.Join(root, "page.html")
	writeTestFile(t, page, string(html))
	script := filepath.Join(root, "lazy-run.mjs")
	writeTestFile(t, script, lazyRuntimeScript)

	for _, mode := range []string{"csr", "preload", "no-preload"} {
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

func TestLazyDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, source, code string
	}{
		{"not an arrow", `<script>import { lazy } from 'vela'; const X = lazy(load);</script><template><vela:component :this={X} /></template>`, "VELA-LAZY-ARGUMENT"},
		{"variable path", `<script>import { lazy } from 'vela'; const p = './X.vela'; const X = lazy(() => import(p));</script><template><vela:component :this={X} /></template>`, "VELA-LAZY-ARGUMENT"},
		{"not a component", `<script>import { lazy } from 'vela'; const X = lazy(() => import('./x.js'));</script><template><vela:component :this={X} /></template>`, "VELA-LAZY-ARGUMENT"},
		{"tag", `<script>import { lazy } from 'vela'; const X = lazy(() => import('./X.vela'));</script><template><X /></template>`, "VELA-LAZY-TAG"},
	} {
		root := t.TempDir()
		src := filepath.Join(root, "App.vela")
		writeTestFile(t, src, tc.source)
		diags, err := CompileFile(src, filepath.Join(root, "App.js"), CompileOptions{StatusWriter: io.Discard})
		if err == nil {
			t.Errorf("%s: compiled", tc.name)
		}
		found := false
		for _, d := range diags {
			found = found || d.Code == tc.code
		}
		if !found {
			t.Errorf("%s: no %s in %v", tc.name, tc.code, diags)
		}
	}
}
