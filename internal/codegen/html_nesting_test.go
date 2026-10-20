package codegen

import (
	"strings"
	"testing"

	"github.com/elauven/vela/internal/sfcast"
)

func nestingNodes(t *testing.T, template string) (string, []sfcast.Node) {
	t.Helper()
	source := "<template>" + template + "</template>"
	sfc, err := sfcast.ParseSFC(source)
	if err != nil {
		t.Fatalf("parse %q: %v", template, err)
	}
	return source, sfc.TemplateAST.Nodes
}

// TestHTMLNestingRejectsParserRearrangements covers every rejection of
// HYDRATION_ARCHITECTURE M-5.8 and M-5.9 (Q-10.4): the error names the
// construct and points at it.
func TestHTMLNestingRejectsParserRearrangements(t *testing.T) {
	childRoots := map[string]string{"Row": "tr", "Wide": "div"}
	ctx := HTMLNestingContext{ComponentRoot: func(c *sfcast.ComponentNode) string { return childRoots[c.Name] }}
	for _, tc := range []struct {
		template, at, message string
	}{
		{`<table><tbody>text</tbody></table>`, `text`, "text directly inside <tbody>"},
		{`<table><tbody><tr>{label}</tr></tbody></table>`, `{label}`, "text directly inside <tr>"},
		{`<table><tbody><div>x</div></tbody></table>`, `<div>x`, "<div> directly inside <tbody>"},
		{`<table><tbody>{#if open}<span>x</span>{/if}</tbody></table>`, `<span>`, "<span> directly inside <tbody>"},
		{`<table><tbody>{#each rows as row}<Wide />{/each}</tbody></table>`, `<Wide`, "<Wide> renders a <div> root"},
		{`<table><tbody><tr><slot /></tr></tbody></table>`, `<slot`, "a slot directly inside <tr>"},
		{`<p>a <div>b</div></p>`, `<div>`, "<div> inside <p>"},
		{`<p><span>{#if x}<ul><li>a</li></ul>{/if}</span></p>`, `<ul>`, "<ul> inside <p>"},
		{`<a href="/"><span><a href="/x">x</a></span></a>`, `<a href="/x">`, "<a> inside <a>"},
		{`<form><div><form></form></div></form>`, `<form></form>`, "<form> inside <form>"},
		{`<button><button>x</button></button>`, `<button>x`, "<button> inside <button>"},
		{`<select><option><b>x</b></option></select>`, `<b>`, "<b> inside <option>"},
		{`<textarea>{a}{#if b}x{/if}</textarea>`, `<textarea>`, "<textarea> holds one text run only"},
		{`<title><b>x</b></title>`, `<title>`, "<title> holds text only"},
	} {
		source, nodes := nestingNodes(t, tc.template)
		err := CheckHTMLNesting(nodes, ctx)
		if err == nil {
			t.Errorf("%s: accepted", tc.template)
			continue
		}
		if !strings.Contains(err.Error(), HTMLNestingDiagnosticCode) || !strings.Contains(err.Error(), tc.message) {
			t.Errorf("%s: error %q, want %q", tc.template, err, tc.message)
		}
		if want := strings.Index(source, tc.at); err.Pos != want {
			t.Errorf("%s: error at %d (%q), want %d", tc.template, err.Pos, source[err.Pos:], want)
		}
	}
}

func TestHTMLNestingAcceptsStableTemplates(t *testing.T) {
	ctx := HTMLNestingContext{ComponentRoot: func(c *sfcast.ComponentNode) string { return "tr" }}
	for _, template := range []string{
		`<table>
  <thead><tr><th>a</th></tr></thead>
  <tbody>{#each rows as row (row.id)}<tr><td>{row.name}</td></tr>{:else}<tr><td>none</td></tr>{/each}</tbody>
</table>`,
		`<table><tbody>{#each rows as row}<Row :row={row} />{/each}</tbody></table>`,
		`<p><span>a</span> {b} <button><div>c</div></button></p>`,
		`<a href="/"><span>x</span></a><a href="/y">y</a>`,
		`<select><option>{label}</option></select>`,
		`<textarea>{value}</textarea>`,
	} {
		_, nodes := nestingNodes(t, template)
		if err := CheckHTMLNesting(nodes, ctx); err != nil {
			t.Errorf("%s: %v", template, err)
		}
	}
}

// TestNormalizeTableRowsInsertsTbody covers M-5.7: rows and row blocks
// written directly in a <table> move into one <tbody>, leading whitespace
// stays in the table, and sections are left alone.
func TestNormalizeTableRowsInsertsTbody(t *testing.T) {
	_, nodes := nestingNodes(t, "<table>\n  <caption>c</caption>\n  <tr><td>a</td></tr>\n  {#each rows as row}<tr><td>{row}</td></tr>{/each}\n  <tfoot><tr><td>f</td></tr></tfoot></table>")
	NormalizeTableRows(nodes)
	NormalizeTableRows(nodes)
	table := nodes[0].(*sfcast.ElementNode)
	var shape []string
	for _, child := range table.Children {
		switch n := child.(type) {
		case *sfcast.ElementNode:
			inner := []string{}
			for _, grandchild := range n.Children {
				switch g := grandchild.(type) {
				case *sfcast.ElementNode:
					inner = append(inner, g.Tag)
				case *sfcast.EachBlockNode:
					inner = append(inner, "each")
				case *sfcast.TextNode:
					inner = append(inner, "text")
				}
			}
			shape = append(shape, n.Tag+"("+strings.Join(inner, ",")+")")
		case *sfcast.TextNode:
			shape = append(shape, "text")
		}
	}
	got := strings.Join(shape, " ")
	// Whitespace between the elements stays where the HTML parser keeps it:
	// in the table before the implied tbody, and inside the tbody (task 059).
	want := "caption(text) text tbody(tr,text,each,text) tfoot(tr)"
	if got != want {
		t.Fatalf("normalised table = %s, want %s", got, want)
	}
	_, nodes = nestingNodes(t, `<table><tbody>{#each rows as row}<tr><td>{row}</td></tr>{/each}</tbody></table>`)
	if err := CheckHTMLNesting(nodes, HTMLNestingContext{}); err != nil {
		t.Fatal(err)
	}
}
