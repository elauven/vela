package sfcast

import (
	"strings"
	"testing"
)

// TypeScript components (task 081): the script is parsed as TypeScript and
// every template expression is blanked to JavaScript in place, so the
// template text, the raw strings of its nodes and every span stay at the
// offsets of the source.

func TestTypeScriptComponentParse(t *testing.T) {
	src := `<script lang="ts" generics="T extends { id: string }">
  export let items: T[] = [];
  let open = $state<boolean>(true);
</script>
<template>
  <ul :title={items.length as number}>
    {#each items as item (item.id as string)}
      <li @click={(e: MouseEvent): void => console.log(e, item!)}>{item.id satisfies string}</li>
    {/each}
  </ul>
  {#if open as boolean}<p>{items[0]!.id}</p>{/if}
</template>`
	sfc, err := ParseSFC(src)
	if err != nil {
		t.Fatal(err)
	}
	if !sfc.TypeScript || sfc.Lang != "ts" || sfc.Generics != "T extends { id: string }" || src[sfc.GenericsPos:sfc.GenericsPos+len(sfc.Generics)] != sfc.Generics {
		t.Fatalf("lang %q ts %v generics %q at %d", sfc.Lang, sfc.TypeScript, sfc.Generics, sfc.GenericsPos)
	}
	if len(sfc.ScriptAST.Diagnostics) > 0 {
		t.Fatalf("script diagnostics: %v", sfc.ScriptAST.Diagnostics)
	}
	raw := src[sfc.TemplateOffset : sfc.TemplateOffset+len(sfc.Template)]
	if len(sfc.Template) != len(raw) || strings.Count(sfc.Template, "\n") != strings.Count(raw, "\n") {
		t.Fatalf("the template changed length or lines:\n%s", sfc.Template)
	}
	for _, ts := range []string{" as number", " as string", " as boolean", "satisfies", "MouseEvent", ": void", "item!", "]!"} {
		if strings.Contains(sfc.Template, ts) {
			t.Errorf("template text keeps %q:\n%s", ts, sfc.Template)
		}
	}
	if len(sfc.TemplateAST.Diagnostics) > 0 || len(ValidateTemplateAST(sfc.TemplateAST)) > 0 {
		t.Fatalf("template diagnostics: %v %v", sfc.TemplateAST.Diagnostics, ValidateTemplateAST(sfc.TemplateAST))
	}
	// Every expression span points at its text in the source.
	var check func([]Node)
	exprs := 0
	visit := func(e *TemplateExpr) {
		if e == nil {
			return
		}
		exprs++
		if len(e.Errors) > 0 {
			t.Errorf("expression %q: %v", e.Source, e.Errors)
		}
		at := sfc.TemplateOffset + e.Pos
		if at < 0 || at+len(e.Source) > len(src) {
			t.Errorf("expression %q out of the source", e.Source)
			return
		}
		// Blanking only turns characters into spaces: every other
		// character is the source's, at the same offset.
		for i := 0; i < len(e.Source); i++ {
			if e.Source[i] != ' ' && e.Source[i] != src[at+i] {
				t.Errorf("expression %q is not aligned with %q", e.Source, src[at:at+len(e.Source)])
				break
			}
		}
	}
	check = func(nodes []Node) {
		for _, n := range nodes {
			switch n := n.(type) {
			case *ElementNode:
				for _, a := range n.Attrs {
					visit(a.Expr)
				}
				check(n.Children)
			case *TextNode:
				for _, p := range n.Parts {
					visit(p.Expr)
				}
			case *IfChainNode:
				for _, b := range n.Branches {
					visit(b.Expr)
					check(b.Children)
				}
			case *EachBlockNode:
				visit(n.IterExpr)
				visit(n.KeyAST)
				check(n.Children)
			}
		}
	}
	check(sfc.TemplateAST.Nodes)
	if exprs < 6 {
		t.Errorf("only %d expressions visited", exprs)
	}
}

func TestJavaScriptComponentUnchanged(t *testing.T) {
	src := "<script>\n  let a = 1;\n</script>\n<template><p :title={a < 2 > (3)}>{a}</p></template>"
	sfc, err := ParseSFC(src)
	if err != nil {
		t.Fatal(err)
	}
	if sfc.TypeScript || sfc.LangInvalid || sfc.Template != "<p :title={a < 2 > (3)}>{a}</p>" {
		t.Fatalf("ts %v invalid %v template %q", sfc.TypeScript, sfc.LangInvalid, sfc.Template)
	}
}

func TestScriptLangAttribute(t *testing.T) {
	for src, want := range map[string]string{
		`<script lang="ts">`:                          "ts",
		`<script lang='typescript' context="client">`: "typescript",
		`<SCRIPT context=client lang=ts>`:             "ts",
		`<script data-lang="ts">`:                     "",
		`<script>`:                                    "",
		`<template></template><script lang="ts">`:     "ts",
	} {
		if got, _ := ScriptLang(src); got != want {
			t.Errorf("ScriptLang(%q) = %q, want %q", src, got, want)
		}
	}
	if !ScriptContextClient(`<script lang="ts" context="client">`) {
		t.Error("lang hid context=client")
	}
}
