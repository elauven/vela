package typecheck

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
      <vela:component :this={i ? Card : null} :title={title} @on:pick={(event) => event.detail.id} />
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
	v := Build("/app/src/App.vela", src)
	for _, want := range []string{
		"import { Card } from './Card.vela';",
		"if (items.length > 0) {",
		"for (const { id, title } of $$each(items)) {\nlet i = 0;\n(id);",
		"const $$c1 = Card;\n$$c1($$target, { title: (title), });\n$$event($$c1, \"close\", (event) => event.detail);",
		"const $$c2 = $$dynamicValue(i ? Card : null);\n$$dynamic($$c2, { title: (title), });\n$$event($$c2, \"pick\", (event) => event.detail.id);",
		`$$on("click", (e) => e.preventDefault());`,
		"(i ? 'a' : 'b');",
		"} else if (promise) {",
		"{ (promise);",
		"const value = $$awaited(promise);",
		"const error = $$error;",
		"/** @type {import('vela').Component<ReturnType<typeof $$propsOf>, {}, {}, never>} */\nexport const App = /** @type {any} */ (null);",
	} {
		if !strings.Contains(v.Text, want) {
			t.Errorf("the virtual document misses %q:\n%s", want, v.Text)
		}
	}
	if strings.Contains(v.Text, "ignored") || strings.Contains(v.Text, "braces") {
		t.Errorf("a comment or a static attribute became code:\n%s", v.Text)
	}
	if _, diags := jsast.ParseJSWithDiagnostics(v.Text); len(diags) > 0 {
		t.Errorf("the virtual document does not parse: %v\n%s", diags, v.Text)
	}

	// Copied text maps both ways, the end of a word included.
	for _, needle := range []string{"items.length", "{ id, title }", "error.message", "Card"} {
		off := strings.Index(src, needle)
		for _, delta := range []int{0, len(needle)} {
			virt, ok := v.ToVirtual(off + delta)
			if !ok {
				t.Fatalf("%q+%d does not map", needle, delta)
			}
			if back, ok := v.ToSource(virt); !ok || back != off+delta {
				t.Errorf("%q+%d maps back to %d", needle, delta, back)
			}
		}
		if virt, _ := v.ToVirtual(off); !strings.HasPrefix(v.Text[virt:], needle) {
			t.Errorf("%q maps to %q", needle, v.Text[virt:virt+len(needle)])
		}
	}
	if _, ok := v.ToSource(strings.Index(v.Text, "$$render")); ok {
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
		v := Build(file, string(data))
		if _, diags := jsast.ParseWithOptions(v.Text, jsast.ParseOptions{TypeScript: v.TypeScript}); len(diags) > 0 {
			t.Errorf("%s: %v\n%s", filepath.Base(file), diags[0], v.Text)
		}
		for _, s := range v.byVirt {
			if v.Text[s.virt:s.virt+s.n] != string(data)[s.src:s.src+s.n] {
				t.Fatalf("%s: segment %+v differs", file, s)
			}
		}
	}

	// An unclosed block and an expression being typed.
	v := Build("App.vela", "<script>let count = 1;</script>\n<template>\n{#if count}\n<p>{cou</p>\n")
	if !strings.Contains(v.Text, "(cou);") || strings.Count(v.Text, "{")-strings.Count(v.Text, "}") != 0 {
		t.Errorf("unfinished template:\n%s", v.Text)
	}
}
