package main

// Tests of task 059: scope attributes only for components with styles,
// payload entries without reproducible state, HTML whitespace rules.

import (
	"strings"
	"testing"
)

func TestScopeAttributeOnlyWithStyles(t *testing.T) {
	plain := "<template><div><p>a</p><span>{x}</span></div></template>\n<script>export let x = 1;</script>"
	styled := plain + "\n<style>p { color: red; }</style>"
	if out := compileGenerated(t, plain); strings.Contains(out, "vela-test") {
		t.Errorf("a component without styles carries its scope attribute:\n%s", out)
	}
	if out := compileGenerated(t, styled); !strings.Contains(out, "vela-test") {
		t.Errorf("a component with styles lost its scope attribute:\n%s", out)
	}
	for src, want := range map[string]string{plain: "", styled: "vela-test"} {
		sfc, err := ParseSFC(src)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := BuildSSRRenderPlan("Test", sfc, "vela-test")
		if err != nil {
			t.Fatal(err)
		}
		if plan.ElementScope != want {
			t.Errorf("server element scope %q, want %q", plan.ElementScope, want)
		}
	}
}

func TestPayloadEntryOnlyForSerializedState(t *testing.T) {
	reproducible := "<script>export let rows = [];\nlet list = $state(rows);\nlet q = $state('');\n</script><template><p @click={() => q = 'x'}>{list.length}{q}</p></template>"
	if out := compileGenerated(t, reproducible); strings.Contains(out, "$$hydrationState") {
		t.Errorf("a component with reproducible state reads a payload entry:\n%s", out)
	}
	serialized := "<script>let started = $state(Date.now());\n</script><template><p>{started}</p></template>"
	if out := compileGenerated(t, serialized); !strings.Contains(out, "$$hydrationState") {
		t.Errorf("a component with serialized state does not read its entry:\n%s", out)
	}
	for src, want := range map[string]int{reproducible: 0, serialized: 1} {
		sfc, err := ParseSFC(src)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := BuildSSRRenderPlanWithAnalysis("Test", sfc, AnalyzeScriptAST(sfc.ScriptAST), "vela-test")
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.PayloadState) != want {
			t.Errorf("payload state %v, want %d names", plan.PayloadState, want)
		}
	}
}

// shape describes a child list: element tags, blocks, and text in quotes.
func shape(nodes []Node) string {
	var parts []string
	for _, node := range nodes {
		switch n := node.(type) {
		case *ElementNode:
			parts = append(parts, n.Tag)
		case *ComponentNode:
			parts = append(parts, n.Name)
		case *TextNode:
			var b strings.Builder
			for _, p := range n.Parts {
				if p.Static {
					b.WriteString(p.Value)
				} else {
					b.WriteString("{}")
				}
			}
			parts = append(parts, `"`+b.String()+`"`)
		default:
			parts = append(parts, node.NodeKind())
		}
	}
	return strings.Join(parts, ",")
}

func TestHTMLWhitespaceRules(t *testing.T) {
	cases := []struct{ template, want string }{
		{"<p><b>a</b> <i>b</i></p>", `b," ",i`},
		{"<p>\n  <b>a</b>\n</p>", `b`},
		{"<p>a <b>b</b> c</p>", `"a ",b," c"`},
		{"<p>a    b\n\t c</p>", `"a b c"`},
		{"<p>  {x}  y  </p>", `"{} y"`},
		{"<div><p>x</p>\n{#if a}<p>y</p>{/if}\n<p>z</p></div>", `p," ",ifchain," ",p`},
		{"<p>a <!-- c --> b</p>", `"a b"`},
		{"<p>a<!-- c -->b</p>", `"ab"`},
		{"<pre>  a\n  b  </pre>", "\"  a\n  b  \""},
		{"<p> x </p>", "\" x \""},
		{"<ul>\n  {#each xs as x}\n    <li>{x}</li>\n  {/each}\n</ul>", `eachblock`},
	}
	for _, c := range cases {
		nodes, err := ParseTemplate(c.template)
		if err != nil {
			t.Fatalf("%q: %v", c.template, err)
		}
		if got := shape(nodes[0].(*ElementNode).Children); got != c.want {
			t.Errorf("%q: children %s, want %s", c.template, got, c.want)
		}
	}
	nodes, err := ParseTemplate("<Card>\n  <p>x</p>\n  <svelte:fragment slot=\"a\"><b>y</b></svelte:fragment>\n</Card>")
	if err != nil {
		t.Fatal(err)
	}
	card := nodes[0].(*ComponentNode)
	if got := shape(card.Slots["default"]); got != "p" {
		t.Errorf("default slot %s, want p", got)
	}
	if got := shape(card.Slots["a"]); got != "b" {
		t.Errorf("named slot %s, want b", got)
	}
}
