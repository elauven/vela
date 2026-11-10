package main

// {@html} (task 100): raw markup in the browser, the Go renderer and
// hydration.

import (
	"os/exec"
	"strings"
	"testing"
)

func TestHtmlTagCSR(t *testing.T) {
	src := `<script>
  import { onMount } from 'vela';
  let markup = $state('<b>bold</b> text');
  let count = $state(0);
  let out = $state(null);
  let records = $state(0);
  let kept = $state('');
  let first = null;
  let last = null;
  onMount(() => {
    first = out.firstChild;
    last = out.lastChild;
    const Observer = out.ownerDocument.defaultView.MutationObserver;
    const observer = new Observer((list) => {
      for (const record of list) records += record.addedNodes.length + record.removedNodes.length;
    });
    observer.observe(out, { childList: true, subtree: true, characterData: true });
    return () => observer.disconnect();
  });
  function probe() {
    kept = String(out.firstChild === first && out.lastChild === last && first.data === 'a ');
  }
</script>
<template><div>
  <button class="set" @click={() => markup = '<i>it</i>'}>set</button>
  <button class="same" @click={() => { markup = '<i>' + 'it</i>'; count++; }}>same</button>
  <button class="none" @click={() => markup = null}>none</button>
  <button class="probe" @click={probe}>probe</button>
  <p class="out" bind:this={out}>a {@html markup} z</p>
  <span class="count">{count}</span><span class="records">{records}</span><span class="kept">{kept}</span>
</div></template>`
	assertRuntime(t, "HtmlTagCSR", src, []rtStep{
		{"assertText": "p.out", "equals": "a bold text z"},
		{"assertCount": "p.out b", "equals": 1},
		{"click": "button.set"},
		{"assertCount": "p.out b", "equals": 0},
		{"assertText": "p.out i", "equals": "it"},
		// <b>, " text" removed, <i> added.
		{"assertText": "span.records", "equals": "3"},
		{"click": "button.same"},
		{"assertText": "span.count", "equals": "1"},
		{"assertText": "span.records", "equals": "3"},
		{"assertText": "p.out", "equals": "a it z"},
		{"click": "button.probe"},
		{"assertText": "span.kept", "equals": "true"},
		{"click": "button.none"},
		{"assertText": "p.out", "equals": "a  z"},
	})
}

func TestHtmlTagSVG(t *testing.T) {
	src := `<script>
  let shape = $state('<circle r="4"></circle>');
  let size = $state(10);
  let icon = $state(null);
  let probe = $state('');
  function inspect() {
    probe = [...icon.querySelectorAll('*')].map((node) => node.localName + ':' + (node.namespaceURI.endsWith('svg') ? 'svg' : 'html')).join(' ') + ' ' + icon.getAttribute('class') + ' ' + icon.getAttribute('width');
  }
</script>
<template><div>
  <button class="grow" @click={() => { size = 20; shape = '<rect width="2" height="2"></rect>'; }}>grow</button>
  <button class="probe" @click={inspect}>probe</button>
  <svg bind:this={icon} :width={size} :class={'icon s' + size}>{@html shape}<g><path d="M0 0"></path></g>{#if size > 10}<text :x={size}>big</text>{/if}<foreignObject><p>html</p></foreignObject></svg>
  <span class="probe">{probe}</span>
</div></template>`
	assertRuntime(t, "HtmlTagSVG", src, []rtStep{
		{"click": "button.probe"},
		{"assertText": "span.probe", "equals": "circle:svg g:svg path:svg foreignObject:svg p:html icon s10 10"},
		{"click": "button.grow"},
		{"click": "button.probe"},
		{"assertText": "span.probe", "equals": "rect:svg g:svg path:svg text:svg foreignObject:svg p:html icon s20 20"},
	})
}

func TestHtmlTagDiagnostics(t *testing.T) {
	cases := []struct {
		name, template, script, code, help string
	}{
		{"const", `<p>{@const total = price * 2}</p>`, `let price = 1;`, "VELA-UNSUPPORTED-TAG", "$derived"},
		{"debug", `<p>{@debug price}</p>`, `let price = 1;`, "VELA-UNSUPPORTED-TAG", "console.log"},
		{"render", `{@render row()}`, `function row() {}`, "VELA-UNSUPPORTED-TAG", "slots"},
		{"in block", `{#if ok}{@const a = 1}{/if}`, `let ok = true;`, "VELA-UNSUPPORTED-TAG", "$derived"},
		{"attribute", `<p :title={@html markup}>x</p>`, `let markup = '';`, "VELA-HTML-TAG-PLACEMENT", "content of an element"},
		{"interpolated attribute", `<p :title="a {@html markup}">x</p>`, `let markup = '';`, "VELA-HTML-TAG-PLACEMENT", ""},
		{"textarea", `<textarea>{@html markup}</textarea>`, `let markup = '';`, "VELA-HTML-TAG-PLACEMENT", ""},
		{"title", `<title>{@html markup}</title>`, `let markup = '';`, "VELA-HTML-TAG-PLACEMENT", ""},
		{"named slot", `<Card><svelte:fragment slot="foot">{@html markup}</svelte:fragment></Card>`, `import { Card } from './Card.vela'; let markup = '';`, "VELA-HTML-TAG-PLACEMENT", "Wrap it in an element"},
		{"head", `<vela:head>{@html markup}</vela:head><p>x</p>`, `let markup = '';`, "VELA-HEAD-CONTENT", ""},
		{"empty", `<p>{@html}</p>`, ``, "VELA-TEMPLATE-SYNTAX", ""},
		{"spaced", `<p>{ @debug price }</p>`, `let price = 1;`, "VELA-UNSUPPORTED-TAG", "console.log"},
		{"unclosed", `<p>{@html markup</p>`, `let markup = '';`, "not closed", ""},
		{"unknown name", `<p>{@html missing}</p>`, ``, "missing", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			source := "<script>" + c.script + "</script>\n<template>" + c.template + "</template>\n"
			svc := &CompilerService{Cwd: mustGetwd(t)}
			res := svc.Compile(ServiceRequest{Path: "App.vela", Source: &source})
			var all []string
			found := 0
			for _, d := range res.Diagnostics {
				all = append(all, d.Code+": "+d.Message+" ("+d.Suggestion+")")
				if d.Code == c.code || strings.Contains(d.Message, c.code) {
					found++
					if c.help != "" && !strings.Contains(d.Suggestion, c.help) {
						t.Errorf("help %q does not mention %q", d.Suggestion, c.help)
					}
				}
			}
			if found == 0 {
				t.Fatalf("diagnostics %v (error %q) do not have %q", all, res.Error, c.code)
			}
			if c.code == "VELA-UNSUPPORTED-TAG" && len(res.Diagnostics) != 1 {
				t.Errorf("want one diagnostic, got %v", all)
			}
		})
	}
}

func TestHtmlTagCompiles(t *testing.T) {
	source := `<script>
  let markup = $state('<b>x</b>');
  let rows = $state([]);
</script>
<template>
  <pre>{@html markup}</pre>
  <div>{#each rows as row (row)}{@html row}{/each}</div>
  {@html markup}
  <p>{ @html markup }</p>
</template>
`
	svc := &CompilerService{Cwd: mustGetwd(t)}
	res := svc.Compile(ServiceRequest{Path: "App.vela", Source: &source})
	if res.Error != "" {
		t.Fatalf("compile: %s %v", res.Error, res.Diagnostics)
	}
	for _, d := range res.Diagnostics {
		if d.Severity == "error" {
			t.Errorf("unexpected diagnostic %s: %s", d.Code, d.Message)
		}
	}
}

// TestHtmlTagServerHost covers the usual {@html} source, a Markdown
// renderer: a function the translator cannot convert needs a Go host like
// a text interpolation, and its markup is written unescaped.
func TestHtmlTagServerHost(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not available")
	}
	p := newSSRHostProject(t)
	p.write("src/markdown.js", `export function renderMarkdown(text) {
  return new Intl.ListFormat('en').format([text]).replace(/\*(.+)\*/, '<b>$1</b>');
}
`)
	p.write("src/App.vela", `<script>
  import { renderMarkdown } from './markdown.js';
  export let total = 0;
</script>
<template>
  <article class="post">{@html renderMarkdown('*' + total + '*')}</article>
</template>
`)
	errors, stderr := p.build(false)
	if errors == 0 {
		t.Fatal("a call without native translation or host built")
	}
	for _, want := range []string{"VELA-SSR-HOST-MISSING", "reason: {@html} markup → call \"renderMarkdown\"", "//vela:host renderMarkdown"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("diagnostic misses %q:\n%s", want, stderr)
		}
	}
	p.write("ssrhost/src/format/locale.go", "package format\n\n// LocaleKey carries the locale in the render context.\ntype LocaleKey struct{}\n\n// Calls counts host calls for the test.\nvar Calls int\n")
	p.write("ssrhost/src/markdown/markdown_host.go", `package markdown

import (
	"context"
	"strings"
)

// RenderMarkdown implements renderMarkdown of src/markdown.js.
//
//vela:host renderMarkdown
func RenderMarkdown(ctx context.Context, text string) (string, error) {
	return "<b>" + strings.Trim(text, "*") + "</b>", nil
}
`)
	if errors, stderr := p.build(false); errors != 0 {
		t.Fatalf("build with the host: %d errors\n%s", errors, stderr)
	}
	out := p.run()
	// Without events the component is a static root: plain markup.
	if !strings.Contains(out, `<article class="post"><b>1234</b></article>`) {
		t.Fatalf("host render:\n%s", out)
	}
}
