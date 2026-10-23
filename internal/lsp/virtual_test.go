package lsp

import (
	"github.com/elauven/vela/internal/jsast"
	velaproject "github.com/elauven/vela/internal/project"

	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVirtualDocument(t *testing.T) {
	src := `<script>
  import { Card } from './Card.vela';
  let items = $state([]);
  let promise = $state(null);
</script>

<template>
  {#if items.length > 0}
    {#each items as { id, title }, i (id)}
      <Card :title={title} @on:close={(event) => event.detail} />
      <button @click={(e) => e.preventDefault()} :class="i ? 'a' : 'b'">{i}</button>
    {:else}
      <p>none</p>
    {/each}
  {:else if promise}
    {#await promise}
      <p>…</p>
    {:then value}
      <p>{value}</p>
    {:catch error}
      <p>{error.message}</p>
    {/await}
  {:else}
    <!-- {ignored} -->
    <p title="literal {braces}">{items}</p>
  {/if}
</template>
`
	v := buildVirtual("/app/src/App.vela", src)
	for _, want := range []string{
		"import { Card } from './Card.vela';",
		"if (items.length > 0) {",
		"for (const { id, title } of $$each(items)) {\nlet i = 0;\n(id);",
		"(title);",
		"$$handler((event) => event.detail);",
		`$$on("click", (e) => e.preventDefault());`,
		"(i ? 'a' : 'b');",
		"} else if (promise) {",
		"{ (promise);",
		"const value = $$awaited(promise);",
		"const error = $$error;",
		"export function App($$target, $$props) {}",
	} {
		if !strings.Contains(v.text, want) {
			t.Errorf("the virtual document misses %q:\n%s", want, v.text)
		}
	}
	if strings.Contains(v.text, "ignored") || strings.Contains(v.text, "braces") {
		t.Errorf("a comment or a static attribute became code:\n%s", v.text)
	}
	if _, diags := jsast.ParseJSWithDiagnostics(v.text); len(diags) > 0 {
		t.Errorf("the virtual document does not parse: %v\n%s", diags, v.text)
	}

	// Copied text maps both ways, the end of a word included.
	for _, needle := range []string{"items.length", "{ id, title }", "error.message", "Card"} {
		off := strings.Index(src, needle)
		for _, delta := range []int{0, len(needle)} {
			virt, ok := v.toVirtual(off + delta)
			if !ok {
				t.Fatalf("%q+%d does not map", needle, delta)
			}
			if back, ok := v.toSource(virt); !ok || back != off+delta {
				t.Errorf("%q+%d maps back to %d", needle, delta, back)
			}
		}
		if virt, _ := v.toVirtual(off); !strings.HasPrefix(v.text[virt:], needle) {
			t.Errorf("%q maps to %q", needle, v.text[virt:virt+len(needle)])
		}
	}
	if _, ok := v.toSource(strings.Index(v.text, "$$render")); ok {
		t.Error("generated text maps to the component")
	}
}

// TestVirtualDocumentsParse: the virtual document of every example
// component, and of a component being typed, is JavaScript.
func TestVirtualDocumentsParse(t *testing.T) {
	var files []string
	for _, root := range []string{"../../examples", "../../test_projects/qpage-builder/src", "../../testdata/lsp"} {
		files = append(files, velaproject.CollectVela(root)...)
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		v := buildVirtual(file, string(data))
		if _, diags := jsast.ParseJSWithDiagnostics(v.text); len(diags) > 0 {
			t.Errorf("%s: %v\n%s", filepath.Base(file), diags[0], v.text)
		}
		for _, s := range v.byVirt {
			if v.text[s.virt:s.virt+s.n] != string(data)[s.src:s.src+s.n] {
				t.Fatalf("%s: segment %+v differs", file, s)
			}
		}
	}

	// An unclosed block and an expression being typed.
	v := buildVirtual("App.vela", "<script>let count = 1;</script>\n<template>\n{#if count}\n<p>{cou</p>\n")
	if !strings.Contains(v.text, "(cou);") || strings.Count(v.text, "{")-strings.Count(v.text, "}") != 0 {
		t.Errorf("unfinished template:\n%s", v.text)
	}
}
