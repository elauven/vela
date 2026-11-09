package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elauven/vela/internal/sfcast"
)

// compileDiagnostics compiles source as App.vela and returns the compile
// error and the diagnostics as text (code and message).
func compileDiagnostics(t *testing.T, source string) (error, string) {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join(root, "App.vela")
	writeTestFile(t, src, source)
	diags, err := CompileFile(src, filepath.Join(root, "App.js"), CompileOptions{StatusWriter: io.Discard})
	var b strings.Builder
	for _, d := range diags {
		fmt.Fprintf(&b, "%v %s: %s\n", d.Sev, d.Code, d.Message)
	}
	return err, b.String()
}

// head_test.go — <vela:head> (task 091) in the compiler and the browser
// runtime. The server and the router are covered by ssr_head_test.go and
// the app tests.

func TestHeadDiagnostics(t *testing.T) {
	for src, want := range map[string]string{
		`<template><div><vela:head><title>x</title></vela:head></div></template>`:                             "VELA-HEAD-PLACEMENT",
		`<template>{#if a}<vela:head><title>x</title></vela:head>{/if}</template>`:                            "VELA-HEAD-PLACEMENT",
		`<template><vela:head><title>x</title></vela:head><vela:head><title>y</title></vela:head></template>`: "at most one <vela:head>",
		`<template><vela:head><div>x</div></vela:head></template>`:                                            "<div> is not allowed in <vela:head>",
		`<template><vela:head>text</vela:head></template>`:                                                    "text in <vela:head> belongs in <title>",
		`<template><vela:head><script>alert(1)</script></vela:head></template>`:                               `type="application/ld+json"`,
		`<template><vela:head><meta @click={f} /></vela:head></template>`:                                     "events, bindings",
		`<template><vela:head>{#each xs as x}<meta :name={x} />{/each}</vela:head></template>`:                "{#each} is not allowed",
		`<template><vela:head><title><b>x</b></title></vela:head></template>`:                                 "<title> in <vela:head> holds only text",
		`<template><svelte:head><title>x</title></svelte:head></template>`:                                    "VELA-SVELTE-HEAD",
		`<template><vela:body /></template>`:                                                                  "VELA-UNKNOWN-SPECIAL-ELEMENT",
	} {
		_, diags := compileDiagnostics(t, "<script>let a = $state(1); let xs = $state([]); function f() {}</script>"+src)
		if !strings.Contains(diags, want) {
			t.Errorf("%s: diagnostics %q, want %q", src, diags, want)
		}
	}
	_, diags := compileDiagnostics(t, `<script>let a = $state(1);</script><template>
  <vela:head>
    <title>{a} — Site</title>
    {#if a > 1}<meta name="description" :content={String(a)} />{:else}<link rel="canonical" href="/x" />{/if}
    <script type="application/ld+json">{JSON.stringify({ a })}</script>
  </vela:head>
  <p>{a}</p>
</template>`)
	if strings.Contains(diags, "VELA-HEAD") || strings.Contains(diags, "error ") {
		t.Errorf("a valid head: %s", diags)
	}
}

func TestHeadSplitKeepsTheRootShape(t *testing.T) {
	tmpl, err := ParseTemplate("\n  <vela:head><title>x</title></vela:head>\n  <div>a</div>\n")
	if err != nil {
		t.Fatal(err)
	}
	head, rest := sfcast.SplitHead(tmpl)
	if head == nil || len(rest) != 1 {
		t.Fatalf("head %v, rest %d nodes", head, len(rest))
	}
	if _, ok := rest[0].(*ElementNode); !ok {
		t.Fatalf("rest[0] is %T", rest[0])
	}
	tmpl, _ = ParseTemplate("<p>a</p> <vela:head><title>x</title></vela:head> <p>b</p>")
	if _, rest := sfcast.SplitHead(tmpl); len(rest) != 3 {
		t.Fatalf("two siblings around a head keep one space between them: %d nodes", len(rest))
	}
}

// TestHeadInTheBrowser: tags go to document.head and follow state; the
// deeper component wins a key and the outer value comes back when it goes;
// destroying the component removes its tags.
func TestHeadInTheBrowser(t *testing.T) {
	child := `<script>
  export let name = '';
</script>
<template>
  <vela:head>
    <title>{name} — Shop</title>
    <meta name="description" :content={'About ' + name} />
  </vela:head>
  <b class="child">{name}</b>
</template>`
	page := `<script>
  import { Child } from './Child.js';
  let show = $state(true);
  let name = $state('Ann');
  let noindex = $state(false);
</script>
<template>
  <vela:head>
    <title>Shop</title>
    <meta name="description" content="Everything" />
    {#if noindex}<meta name="robots" content="noindex" />{/if}
    <script type="application/ld+json">{JSON.stringify({ name })}</script>
  </vela:head>
  <div>
    <button class="toggle" @click={() => show = !show}>t</button>
    <button class="rename" @click={() => name = name + '!'}>r</button>
    <button class="robots" @click={() => noindex = !noindex}>n</button>
    {#if show}<Child :name={name} />{/if}
  </div>
</template>`
	assertRuntimeSet(t, "Page", map[string]string{"Page": page, "Child": child}, []rtStep{
		{"assertCount": "head title", "equals": 1},
		{"assertText": "head title", "equals": "Ann — Shop"},
		{"assertCount": `head meta[name="description"]`, "equals": 1},
		{"assertCount": `head meta[name="robots"]`, "equals": 0},
		{"assertText": `head script[type="application/ld+json"]`, "equals": `{"name":"Ann"}`},
		{"assertCount": "body title", "equals": 0},
		{"click": "button.rename"},
		{"assertText": "head title", "equals": "Ann! — Shop"},
		{"assertText": `head script[type="application/ld+json"]`, "equals": `{"name":"Ann!"}`},
		{"click": "button.robots"},
		{"assertCount": `head meta[name="robots"]`, "equals": 1},
		{"click": "button.toggle"},
		{"assertCount": "head title", "equals": 1},
		{"assertText": "head title", "equals": "Shop"},
		{"assertCount": `head meta[name="description"]`, "equals": 1},
		{"click": "button.toggle"},
		{"assertText": "head title", "equals": "Ann! — Shop"},
		{"remount": true},
		{"assertCount": "head title", "equals": 1},
		{"assertCount": `head meta[name="robots"]`, "equals": 0},
	})
}

// TestHeadJSONLDBeforeTheScript: a JSON-LD script of <vela:head> in a
// template that comes before the component's <script> is not the script.
func TestHeadJSONLDBeforeTheScript(t *testing.T) {
	src := `<template>
  <vela:head><script type="application/ld+json">{JSON.stringify(ld)}</script></vela:head>
  <p>{ld.name}</p>
</template>

<script lang="ts">
  const ld: { name: string } = { name: 'x' };
</script>`
	sfc, err := sfcast.ParseSFC(src)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sfc.Script, "const ld") || !sfc.TypeScript {
		t.Fatalf("script %q, TypeScript %v", sfc.Script, sfc.TypeScript)
	}
	if err, diags := compileDiagnostics(t, src); err != nil {
		t.Fatalf("%v\n%s", err, diags)
	}
}
