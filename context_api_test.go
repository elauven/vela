package main

// The context API (task 090): setContext, getContext and hasContext reach
// components through slots, {#if}, {#each}, {#key}, {#await}, lazy() and
// <vela:component>, on the client, on the server and after hydration; a
// $state.deep object in context stays shared after hydration.

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var contextFixture = map[string]string{
	"App.vela": `<script>
  import { setContext, lazy } from 'vela';
  import { Reader } from './Reader.vela';
  import { Box } from './Box.vela';
  import { Provider } from './Provider.vela';
  import { ModReader } from './ModReader.vela';
  const Late = lazy(() => import('./Late.vela'));
  export let items = ['a', 'b'];
  let theme = $state.deep({ name: 'dark' });
  setContext('theme', theme);
  setContext('label', 'app');
  let show = $state(true);
  let version = $state(0);
  let list = $state(items);
  const request = Promise.resolve(1);
</script>

<template>
  <main>
    <button class="rename" @click={() => (theme.name = 'light')}>rename</button>
    <button class="toggle" @click={() => (show = !show)}>toggle</button>
    <button class="bump" @click={() => (version = version + 1)}>bump</button>
    <button class="add" @click={() => (list = [...list, 'c'])}>add</button>
    <Reader name="plain" />
    {#if show}<Reader name="if" />{/if}
    {#each list as item (item)}<Reader :name={item} />{/each}
    {#key version}<Reader name="key" />{/key}
    {#await request}
      <i>wait</i>
    {:then value}
      <Reader name="await" />
    {/await}
    <Box><Reader name="slot" /></Box>
    <Provider><Box><Reader name="override" /></Box></Provider>
    <vela:component :this={Reader} name="dynamic" />
    <vela:component :this={Late} />
    <ModReader />
  </main>
</template>
`,
	"Reader.vela": `<script>
  import { getContext, hasContext } from 'vela';
  export let name = '';
  let theme = $state.deep(getContext('theme'));
  const label = getContext('label');
  const missing = hasContext('missing');
</script>

<template>
  <span class="r" :data-name={name}>{name}:{theme.name}:{label}:{missing}</span>
</template>
`,
	"Box.vela": `<template><div class="box"><slot /></div></template>
`,
	"Provider.vela": `<script>
  import { setContext } from 'vela';
  function provide(value) {
    setContext('label', value);
  }
  provide('inner');
</script>

<template><section><slot /></section></template>
`,
	"Late.vela": `<script>
  import { Reader } from './Reader.vela';
</script>

<template><Reader name="lazy" /></template>
`,
	"ModReader.vela": `<script>
  import { getTheme } from './theme.js';
  let theme = $state.deep(getTheme());
</script>

<template><b class="m">module:{theme.name}</b></template>
`,
	"theme.js": `import { getContext } from 'vela';

export function getTheme() {
  return getContext('theme');
}
`,
}

const contextReaders = "plain if a b key await slot override dynamic lazy"

// contextRuntimeScript mounts or hydrates App in jsdom and prints a JSON
// list of failures.
const contextRuntimeScript = `
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
const warnings = [];
console.warn = (...args) => warnings.push(args.join(' '));
const target = document.createElement('div');
document.body.appendChild(target);
const { App } = await load('App.js');
let app;
if (mode === 'csr') {
  const { mount } = await load('runtime.js');
  app = mount(App, { target, props: {} });
} else {
  target.innerHTML = readFileSync(htmlFile, 'utf8');
  const { hydrate, preloadHydration } = await load('runtime-hydrate.js');
  await preloadHydration(target);
  const reports = [];
  app = hydrate(App, target, { onMismatch: (report) => reports.push(report) });
  expect(reports.length === 0, 'hydration mismatches: ' + JSON.stringify(reports));
}
await settle();
const texts = () => Object.fromEntries([...target.querySelectorAll('span.r')].map((s) => [s.dataset.name, s.textContent]));
const check = (theme, names, when) => {
  const got = texts();
  for (const name of names.split(' ')) {
    const label = name === 'override' ? 'inner' : 'app';
    expect(got[name] === name + ':' + theme + ':' + label + ':false', when + ': ' + name + ' reads ' + got[name]);
  }
  const module = target.querySelector('b.m')?.textContent;
  expect(module === 'module:' + theme, when + ': the module function reads ' + module);
};
check('dark', process.argv[6], 'first render');
const click = (selector) => target.querySelector(selector).dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }));
click('.rename');
await settle();
check('light', process.argv[6], 'after the shared $state.deep changed');
click('.toggle');
await settle();
click('.toggle');
click('.bump');
click('.add');
await settle();
check('light', process.argv[6] + ' c', 'after remounts');
expect(warnings.length === 0, 'warnings: ' + JSON.stringify(warnings));
app.destroy();
process.stdout.write(JSON.stringify(failures));
`

func TestContextAPI(t *testing.T) {
	node, jsdom := requireJsdom(t)
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not available")
	}
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	for name, source := range contextFixture {
		writeTestFile(t, filepath.Join(srcRoot, name), source)
	}
	outRoot := filepath.Join(root, "out")
	module := "example.com/contextapi"
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
	for _, name := range strings.Fields(contextReaders) {
		label := "app"
		if name == "override" {
			label = "inner"
		}
		if want := ">" + name + ":dark:" + label + ":false<"; !strings.Contains(string(html), want) {
			t.Errorf("server HTML misses %q", want)
		}
	}
	if !strings.Contains(string(html), "module:dark") {
		t.Errorf("server HTML misses the module reader")
	}
	// The readers take theme from their context, not from the payload.
	if strings.Contains(string(html), `"theme":{"name":"dark"}`) {
		t.Errorf("a context value travels in the payload:\n%s", html)
	}
	if t.Failed() {
		t.Fatalf("server HTML:\n%s", html)
	}
	page := filepath.Join(root, "page.html")
	writeTestFile(t, page, string(html))
	script := filepath.Join(root, "context-run.mjs")
	writeTestFile(t, script, contextRuntimeScript)
	for _, mode := range []string{"csr", "hydrate"} {
		command := exec.Command(node, script, jsdom, clientDir, mode, page, contextReaders)
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

func TestContextOutsideInitDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		errors       int
	}{
		{"script and functions", `<script>
  import { setContext, getContext } from 'vela';
  function provide() { setContext('a', 1); }
  provide();
  const a = getContext('a');
</script><template><p>{a}</p></template>`, 0},
		{"effect", `<script>import { getContext } from 'vela'; $effect(() => { getContext('a'); });</script><template><p /></template>`, 1},
		{"derived", `<script>import { getContext } from 'vela'; let x = $state(0); let d = $derived(getContext('a') + x);</script><template><p>{d}</p></template>`, 1},
		{"lifecycle alias", `<script>import { hasContext as has, onMount as mounted } from 'vela'; mounted(() => { has('a'); });</script><template><p /></template>`, 1},
		{"template and handler", `<script>import { getContext } from 'vela';</script><template><button @click={() => getContext('a')}>{getContext('b')}</button></template>`, 2},
	} {
		root := t.TempDir()
		src := filepath.Join(root, "App.vela")
		writeTestFile(t, src, tc.source)
		diags, _ := CompileFile(src, filepath.Join(root, "App.js"), CompileOptions{StatusWriter: io.Discard})
		found := 0
		for _, d := range diags {
			if d.Code == "VELA-CONTEXT-OUTSIDE-INIT" {
				found++
			}
		}
		if found != tc.errors {
			t.Errorf("%s: %d VELA-CONTEXT-OUTSIDE-INIT, want %d: %v", tc.name, found, tc.errors, diags)
		}
	}
}

// TestSubpathImports: a # import resolves through the "imports" of the
// nearest package.json in every build; the JavaScript keeps it, the
// manifest does not list it as a package, and the server translates the
// module. A conditional entry, a package target or a target outside the
// package is VELA-SUBPATH-IMPORT.
func TestSubpathImports(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "package.json"), `{
  "imports": {
    "#lib/*": "./src/lib/*",
    "#exact": "./src/lib/fmt.js",
    "#cond": { "browser": "./src/lib/fmt.js" },
    "#list": ["./src/lib/fmt.js"],
    "#pkg": "tiny-fmt",
    "#out": "./src/../../elsewhere.js",
    "#card": "./src/Card.vela"
  }
}
`)
	writeTestFile(t, filepath.Join(root, "src", "lib", "fmt.js"), "export function shout(s) {\n  return s + '!';\n}\n")
	writeTestFile(t, filepath.Join(root, "src", "lib", "use.js"), "import { shout } from '#exact';\n\nexport function twice(s) {\n  return shout(shout(s));\n}\n")
	app := `<script>
  import { shout } from '#lib/fmt.js';
  import { twice } from '#lib/use.js';
  export let name = 'a';
</script>

<template><p>{shout(name)} {twice(name)}</p></template>
`
	writeTestFile(t, filepath.Join(root, "src", "App.vela"), app)
	srcRoot := filepath.Join(root, "src")
	out := filepath.Join(root, "out")
	if errors := BuildSSRProject(CollectVela(srcRoot), srcRoot, out, CompileOptions{StatusWriter: io.Discard, SSRModule: "example.com/sub/server"}, false); errors != 0 {
		t.Fatalf("SSR build errors=%d", errors)
	}
	client, err := os.ReadFile(filepath.Join(out, "client", "App.js"))
	if err != nil || !strings.Contains(string(client), "from '#lib/fmt.js'") {
		t.Errorf("App.js keeps the specifier: %v\n%s", err, client)
	}
	manifest, _ := os.ReadFile(filepath.Join(out, "client", "vela.manifest.json"))
	if strings.Contains(string(manifest), "#lib") || strings.Contains(string(manifest), "#exact") {
		t.Errorf("the manifest lists a # import as a package:\n%s", manifest)
	}
	server, _ := os.ReadFile(filepath.Join(out, "server", "components", "app.go"))
	if !strings.Contains(string(server), ".Shout(") || !strings.Contains(string(server), ".Twice(") {
		t.Errorf("app.go does not call the translated modules:\n%s", server)
	}
	if _, err := os.Stat(filepath.Join(out, "server", "modules", "lib", "use", "use.go")); err != nil {
		t.Errorf("use.js is not translated: %v", err)
	}

	for spec, want := range map[string]string{
		"#cond":    "conditional",
		"#list":    "an array of fallbacks",
		"#pkg":     "outside the package",
		"#out":     "outside the package",
		"#none":    "no entry",
		"#card":    "import components by their relative .vela path",
		"#lib/x/y": "",
	} {
		source := strings.Replace(app, "'#lib/fmt.js'", "'"+spec+"'", 1)
		writeTestFile(t, filepath.Join(root, "src", "App.vela"), source)
		diags, err := CompileFile(filepath.Join(root, "src", "App.vela"), filepath.Join(root, "dist", "App.js"), CompileOptions{StatusWriter: io.Discard})
		var messages []string
		for _, d := range diags {
			if d.Code == "VELA-SUBPATH-IMPORT" {
				messages = append(messages, d.Message)
			}
		}
		if want == "" {
			// #lib/x/y resolves (the file need not exist for the client).
			if len(messages) != 0 {
				t.Errorf("%s: %v", spec, messages)
			}
			continue
		}
		if err == nil || len(messages) != 1 || !strings.Contains(messages[0], want) || !strings.Contains(messages[0], spec) {
			t.Errorf("%s: err %v, diagnostics %q, want one naming %q", spec, err, messages, want)
		}
	}
}
