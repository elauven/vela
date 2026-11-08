package main

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSSRHead covers the server side of <vela:head> (task 091): the tags go
// to the head collector of the render context, not into the body; the
// deeper component wins a key; a JSON-LD script cannot close its element;
// and the browser adopts the server's tags without duplicates, updates them
// and removes them with their component.
func TestSSRHead(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not available")
	}
	p := newSSRHostProject(t)
	p.write("src/format.js", "export const unused = 1;\n")
	p.write("src/Product.vela", `<script>
  export let name = '';
</script>
<template>
  <vela:head>
    <title>{name} — Shop</title>
    <meta name="description" :content={'About ' + name} />
    <script type="application/ld+json">{JSON.stringify({ name, note: '</script>' })}</script>
  </vela:head>
  <b class="name">{name}</b>
</template>
`)
	p.write("src/App.vela", `<script>
  import { Product } from './Product.vela';
  export let name = '';
  let show = $state(true);
</script>
<template>
  <vela:head>
    <title>Shop</title>
    <meta name="description" content="Everything" />
    <link rel="canonical" href="https://shop.example/" />
  </vela:head>
  <div class="app">
    <button class="toggle" @click={() => show = !show}>t</button>
    {#if show}<Product :name={name} />{/if}
  </div>
</template>
`)
	if errors, stderr := p.build(false); errors != 0 {
		t.Fatalf("build: %d errors\n%s", errors, stderr)
	}
	p.write("cmd/render/main.go", `package main

import (
	"context"
	"fmt"
	"strings"

	vela "github.com/elauven/vela/ssr_runtime"
	c "example.com/dash/out/server/components"
)

func main() {
	var head vela.Head
	var body strings.Builder
	ctx := vela.WithHead(context.Background(), &head)
	err := c.RenderAppContext(ctx, &body, c.AppProps{Name: "Lamp", Set: map[string]bool{"name": true}}, c.AppSlots{})
	fmt.Printf("HEAD=%s\nBODY=%s\nERR=%v\n", head.HTML(), body.String(), err)
	var plain strings.Builder
	c.RenderApp(&plain, c.AppProps{Name: "Lamp", Set: map[string]bool{"name": true}}, c.AppSlots{})
	fmt.Printf("PLAIN=%s\n", plain.String())
}
`)
	command := exec.Command("go", "run", "./cmd/render")
	command.Dir = p.root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go run: %v\n%s", err, output)
	}
	out := string(output)
	field := func(name string) string {
		start := strings.Index(out, name+"=")
		if start < 0 {
			t.Fatalf("no %s in\n%s", name, out)
		}
		rest := out[start+len(name)+1:]
		return rest[:strings.IndexByte(rest, '\n')]
	}
	head, body, plain := field("HEAD"), field("BODY"), field("PLAIN")
	if field("ERR") != "<nil>" {
		t.Fatalf("render: %s", out)
	}
	want := `<link rel="canonical" href="https://shop.example/" data-vela-head="App#2">` +
		`<title data-vela-head="Product#0">Lamp — Shop</title>` +
		`<meta name="description" content="About Lamp" data-vela-head="Product#1">` +
		`<script type="application/ld+json" data-vela-head="Product#2">{"name":"Lamp","note":"\u003c/script>"}</script>`
	if head != want {
		t.Errorf("head:\n got %s\nwant %s", head, want)
	}
	for _, html := range []string{body, plain} {
		if strings.Contains(html, "<title") || strings.Contains(html, "<meta") || strings.Contains(html, "data-vela-head") {
			t.Errorf("head tags in the body: %s", html)
		}
	}
	if !strings.Contains(body, `<div class="app">`) || !strings.Contains(body, `<b class="name">Lamp</b>`) {
		t.Errorf("body: %s", body)
	}

	// Hydration adopts the server's tags; the toggle removes and restores
	// the product's tags and brings the root's back meanwhile.
	node, jsdom := requireJsdom(t)
	clientDir := filepath.Join(p.root, "out", "client")
	p.write("out/client/package.json", `{"type":"module"}`)
	p.write("head-check.mjs", headCheckScript)
	command = exec.Command(node, filepath.Join(p.root, "head-check.mjs"), jsdom, clientDir, head, body)
	result, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("head check: %v\n%s", err, result)
	}
	var check struct {
		Adopted   bool     `json:"adopted"`
		Snapshots []string `json:"snapshots"`
		Error     string   `json:"error"`
	}
	if err := json.Unmarshal(result, &check); err != nil || check.Error != "" {
		t.Fatalf("head check: %v %s", err, result)
	}
	if !check.Adopted {
		t.Errorf("hydration recreated the server's head tags")
	}
	wantSnapshots := []string{
		"Lamp — Shop|About Lamp|https://shop.example/|1",
		"Shop|Everything|https://shop.example/",
		"Lamp — Shop|About Lamp|https://shop.example/|1",
		"||",
	}
	if strings.Join(check.Snapshots, "\n") != strings.Join(wantSnapshots, "\n") {
		t.Errorf("head after steps:\n%s\nwant\n%s", strings.Join(check.Snapshots, "\n"), strings.Join(wantSnapshots, "\n"))
	}
}

// headCheckScript hydrates App over the server head and body and prints
// the head after each step: title|description|canonical|JSON-LD scripts.
const headCheckScript = `import { join } from 'node:path';
import { pathToFileURL } from 'node:url';
const [jsdomPath, clientDir, head, body] = process.argv.slice(2);
const { JSDOM } = await import(pathToFileURL(jsdomPath).href);
const dom = new JSDOM('<!doctype html><html><head>' + head + '</head><body><div id="root">' + body + '</div></body></html>');
for (const name of ['window', 'document', 'Node', 'HTMLElement', 'CustomEvent', 'Event', 'InputEvent', 'MutationObserver']) globalThis[name] = dom.window[name];
const settle = () => new Promise((resolve) => setTimeout(resolve, 0));
const out = { snapshots: [] };
try {
  const server = [...document.head.children];
  const { App } = await import(pathToFileURL(join(clientDir, 'App.js')).href);
  const { hydrate } = await import(pathToFileURL(join(clientDir, 'runtime-hydrate.js')).href);
  const snap = () => {
    const titles = document.head.querySelectorAll('title');
    if (titles.length > 1) throw new Error('duplicate titles: ' + document.head.innerHTML);
    const description = document.head.querySelectorAll('meta[name="description"]');
    if (description.length > 1) throw new Error('duplicate descriptions: ' + document.head.innerHTML);
    const canonical = document.head.querySelector('link[rel="canonical"]');
    out.snapshots.push([document.title, description[0] ? description[0].getAttribute('content') : '', canonical ? canonical.getAttribute('href') : '', document.head.querySelectorAll('script[type="application/ld+json"]').length].join('|').replace(/\|0$/, ''));
  };
  const app = hydrate(App, document.getElementById('root'));
  out.adopted = server.every((node) => node.isConnected);
  snap();
  document.querySelector('button.toggle').click();
  await settle();
  snap();
  document.querySelector('button.toggle').click();
  await settle();
  snap();
  app.destroy();
  snap();
} catch (error) {
  out.error = String(error && error.stack || error);
}
console.log(JSON.stringify(out));
`
