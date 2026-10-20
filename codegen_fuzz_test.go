package main

// codegen_fuzz_test.go — E-stabilize 2.
//
// Differential / robustness fuzzing for the AST code generator.
//
// The recent real-world bugs were combinatorial: "construct X
// nested inside captured scope Y".  Curated corpus tests cover
// known shapes; a fuzzer explores the combination space the curated
// tests cannot enumerate by hand.
//
// Strategy: a bytes-driven generator builds a SYNTACTICALLY VALID
// .vela template by recursively composing fragments — elements,
// {#if}, {#each}, holes, components.  Each generated source is
// compiled through generateCodeAST and checked:
//
//   1. compilation does not panic
//   2. the generated AST passes ValidateProgram (no undeclared
//      ref, no empty node, no unreachable code)
//   3. the printed JS re-parses (a cheap structural sanity check —
//      ParseJS round-trips without error)
//
// The generator only emits valid Vela: the fuzzer targets CODEGEN
// robustness, not the parser.  Inputs that fail to parse are
// skipped (t.Skip) rather than failing — a malformed template is
// not a codegen bug.
//
// Run: go test -run xxx -fuzz FuzzCodegen
// Reproduce a crash: go test -run FuzzCodegen/<hash>

import (
	"strings"
	"testing"
)

// fuzzGen turns the fuzzer's opaque byte slice into a deterministic
// stream of small choices.  Exhausting the slice yields zeros, so
// generation always terminates.
type fuzzGen struct {
	data          []byte
	pos           int
	depth         int  // current nesting depth — bounds recursion
	holes         int  // count of state vars referenced
	usesComponent bool // a <Box> invocation was emitted
}

// next returns the next byte (0 once the slice is exhausted).
func (g *fuzzGen) next() byte {
	if g.pos >= len(g.data) {
		return 0
	}
	b := g.data[g.pos]
	g.pos++
	return b
}

// choice returns a value in [0,n).
func (g *fuzzGen) choice(n int) int {
	if n <= 1 {
		return 0
	}
	return int(g.next()) % n
}

// maxFuzzDepth bounds template nesting so generation is finite and
// a failing case stays small enough to read.
const maxFuzzDepth = 5

// stateNames are the reactive variables the generated <script>
// declares; holes reference them by index.
var fuzzStateNames = []string{"a", "b", "c", "d"}

// genTemplate produces a random template body — a sequence of
// 1-3 sibling nodes.
func (g *fuzzGen) genTemplate() string {
	var b strings.Builder
	n := 1 + g.choice(3)
	for i := 0; i < n; i++ {
		b.WriteString(g.genNode())
	}
	return b.String()
}

// genNode emits one node: a static element, a dynamic element, a
// hole, an {#if}, an {#each}, or a nested element.  Recursion is
// depth-bounded; at max depth only leaves are emitted.
func (g *fuzzGen) genNode() string {
	if g.depth >= maxFuzzDepth {
		return g.genLeaf()
	}
	switch g.choice(9) {
	case 0, 1:
		return g.genLeaf()
	case 2:
		return g.genElement()
	case 3:
		return g.genIf()
	case 4:
		return g.genEach()
	case 5:
		return g.genElementWithEvent()
	case 6:
		return g.genElementWithDirective()
	default:
		return g.genComponent()
	}
}

// genComponent emits a child-component invocation — optionally with
// a prop, an @event handler, and slot children.  The component is
// not defined (codegen does not need the import to resolve); this
// exercises buildComponentNode + buildSlotClosures, the captureScope
// boundary that hosted every recent slot/component bug.
func (g *fuzzGen) genComponent() string {
	g.depth++
	defer func() { g.depth-- }()
	g.usesComponent = true
	var b strings.Builder
	b.WriteString("<Box")
	if g.choice(2) == 0 {
		b.WriteString(` label="x"`)
	}
	if g.choice(2) == 0 {
		sv := fuzzStateNames[g.choice(len(fuzzStateNames))]
		g.holes++
		b.WriteString(" @select={() => " + sv + "++}")
	}
	if g.choice(2) == 0 {
		// Self-closing — no slot children.
		b.WriteString(" />")
		return b.String()
	}
	// Slot children — recurse for the default slot, sometimes a
	// named slot too.
	b.WriteString(">")
	b.WriteString(g.genNode())
	if g.choice(3) == 0 {
		b.WriteString(`<div slot="footer">`)
		b.WriteString(g.genNode())
		b.WriteString("</div>")
	}
	b.WriteString("</Box>")
	return b.String()
}

// genLeaf emits a leaf: static text or a reactive hole.
func (g *fuzzGen) genLeaf() string {
	if g.choice(2) == 0 {
		return "text" + g.tag()
	}
	g.holes++
	return "{" + fuzzStateNames[g.choice(len(fuzzStateNames))] + "}"
}

// tag returns a short unique-ish suffix for readability.
func (g *fuzzGen) tag() string {
	return string(rune('A' + g.choice(20)))
}

// genElement wraps random children in a plain element.
func (g *fuzzGen) genElement() string {
	g.depth++
	defer func() { g.depth-- }()
	tag := []string{"div", "section", "span", "p", "ul"}[g.choice(5)]
	kids := 1 + g.choice(2)
	var b strings.Builder
	b.WriteString("<" + tag + ">")
	for i := 0; i < kids; i++ {
		b.WriteString(g.genNode())
	}
	b.WriteString("</" + tag + ">")
	return b.String()
}

// genElementWithEvent emits an element carrying an inline @click —
// exercises the handler-rewrite + listener-wiring path.
func (g *fuzzGen) genElementWithEvent() string {
	g.depth++
	defer func() { g.depth-- }()
	sv := fuzzStateNames[g.choice(len(fuzzStateNames))]
	g.holes++
	var b strings.Builder
	b.WriteString("<button @click={() => " + sv + "++}>")
	b.WriteString(g.genLeaf())
	b.WriteString("</button>")
	return b.String()
}

// genElementWithDirective emits an element with a class:/style:
// directive — the shape that exposed the buildHoleTemplate panic.
func (g *fuzzGen) genElementWithDirective() string {
	g.depth++
	defer func() { g.depth-- }()
	sv := fuzzStateNames[g.choice(len(fuzzStateNames))]
	g.holes++
	dir := "class:on"
	if g.choice(2) == 0 {
		dir = "style:color"
	}
	var b strings.Builder
	b.WriteString("<div " + dir + "={" + sv + "}>")
	b.WriteString(g.genNode())
	b.WriteString("</div>")
	return b.String()
}

// genIf emits an {#if}, optionally with an {:else}.
func (g *fuzzGen) genIf() string {
	g.depth++
	defer func() { g.depth-- }()
	sv := fuzzStateNames[g.choice(len(fuzzStateNames))]
	g.holes++
	var b strings.Builder
	b.WriteString("{#if " + sv + "}")
	b.WriteString(g.genNode())
	if g.choice(2) == 0 {
		b.WriteString("{:else}")
		b.WriteString(g.genNode())
	}
	b.WriteString("{/if}")
	return b.String()
}

// genEach emits an {#each} over a state array, optionally keyed.
func (g *fuzzGen) genEach() string {
	g.depth++
	defer func() { g.depth-- }()
	sv := fuzzStateNames[g.choice(len(fuzzStateNames))]
	g.holes++
	var b strings.Builder
	b.WriteString("{#each " + sv + " as item}")
	// Row body — a single element wrapping random children.
	b.WriteString("<li>")
	b.WriteString(g.genNode())
	b.WriteString("</li>")
	b.WriteString("{/each}")
	return b.String()
}

// genSource assembles a complete .vela source: a <script> declaring
// every state name (plus a Box import when a component was used),
// and the generated <template>.
func (g *fuzzGen) genSource() string {
	body := g.genTemplate()
	var script strings.Builder
	// The import must precede the state decls; whether a component
	// was emitted is known only after genTemplate has run.
	if g.usesComponent {
		script.WriteString("import { Box } from './Box.js';\n")
	}
	for _, n := range fuzzStateNames {
		// Each state var is an array so {#each} over it is valid,
		// and array truthiness makes {#if} over it valid too.
		script.WriteString("let " + n + " = $state([]);\n")
	}
	return "<script>\n" + script.String() + "</script>\n" +
		"<template><div>" + body + "</div></template>"
}

// FuzzCodegen drives the generator and asserts codegen robustness.
func FuzzCodegen(f *testing.F) {
	// Seed corpus — a few byte slices that exercise distinct
	// shapes, so a plain `go test` (no -fuzz) still runs them.
	f.Add([]byte{0, 0, 0})
	f.Add([]byte{3, 1, 0, 2, 1})       // an {#if}
	f.Add([]byte{4, 1, 2, 0, 1})       // an {#each}
	f.Add([]byte{3, 0, 4, 1, 2, 0, 6}) // {#if} > {#each} > directive
	f.Add([]byte{6, 1, 3, 0, 5, 2})    // directive > {#if}, event
	f.Add([]byte{2, 2, 3, 1, 6, 0, 4}) // deep nest

	f.Fuzz(func(t *testing.T, data []byte) {
		g := &fuzzGen{data: data}
		src := g.genSource()

		// Parse — a malformed generated template is a generator
		// bug, not a codegen bug; skip rather than fail.
		sfc, err := ParseSFC(src)
		if err != nil {
			t.Skipf("generator produced unparseable SFC: %v\n%s", err, src)
		}
		tmpl, err := ParseTemplate(sfc.Template)
		if err != nil {
			t.Skipf("generator produced unparseable template: %v\n%s",
				err, src)
		}
		analysis := AnalyzeScript(sfc.Script)

		// 1. Compilation must not panic.
		var js string
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("codegen panicked: %v\n--- source ---\n%s",
						r, src)
				}
			}()
			js = generateCodeAST("Fuzz", sfc, tmpl, analysis, "vela-000000")
		}()

		// 2. The generated AST must validate.  buildProgram already
		//    panics on a validation failure, so reaching here means
		//    it passed — but re-check buildProgram's tree directly
		//    for a clearer message if that ever changes.
		prog := buildProgram("Fuzz", sfc, tmpl, analysis, "vela-000000")
		if issues := ValidateProgram(prog); len(issues) > 0 {
			var b strings.Builder
			for _, iss := range issues {
				b.WriteString("\n  [" + iss.Kind + "] " + iss.Msg)
			}
			t.Fatalf("generated AST failed validation:%s\n--- source ---\n%s",
				b.String(), src)
		}

		// 3. The printed JS must re-parse — a cheap structural
		//    sanity check that the printer emitted well-formed JS.
		if reparsed := ParseJS(js); reparsed == nil {
			t.Fatalf("generated JS does not re-parse\n--- source ---\n%s"+
				"\n--- js ---\n%s", src, js)
		}
	})
}

// FuzzComponentASTBoundaries exercises the parser/validator boundary with both
// valid and malformed template/CSS input. Unlike FuzzCodegen it never asks
// codegen to recover from malformed structures: the property here is that one
// parse produces owned section ASTs whose validators can inspect safely.
func FuzzComponentASTBoundaries(f *testing.F) {
	f.Add(`<p>{count}</p>`, `.card { color: red; }`)
	f.Add(`{#if visible}<button @click={() => count++}>ok</button>{:else}<p>no</p>{/if}`, `@media screen { .card { color: red; } }`)
	f.Add(`{#each items as item, i (item.id)}<p class:selected={item.done}>{i}</p>{/each}`, `@keyframes fade { from { opacity: 0; } to { opacity: 1; } }`)
	f.Add(`<div :class="{'active': enabled}">broken legacy form</div>`, `.card { color: `)
	f.Add(`{#await request}<p>pending</p>{:then value}<p>{value}</p>{/await}`, `/* unterminated`)

	f.Fuzz(func(t *testing.T, template, css string) {
		if len(template) > 4096 || len(css) > 4096 {
			t.Skip()
		}
		source := `<script></script><template>` + template + `</template><style>` + css + `</style>`
		sfc, err := ParseSFC(source)
		if err != nil {
			return // malformed outer SFC is a valid parser outcome
		}
		if sfc.AST == nil || sfc.AST.Source != source {
			t.Fatalf("component AST ownership lost for %q", source)
		}
		if sfc.AST.Script != sfc.ScriptAST || sfc.AST.Template != sfc.TemplateAST || sfc.AST.Style != sfc.StyleAST {
			t.Fatal("component AST sections do not match SFC section pointers")
		}
		checkSection := func(name, text string, span SectionSpan) {
			if span.Start < 0 || span.End < span.Start || span.End > len(source) {
				t.Fatalf("%s span %#v escapes source length %d", name, span, len(source))
			}
			if got := source[span.Start:span.End]; got != text {
				t.Fatalf("%s span source = %q, want %q", name, got, text)
			}
		}
		checkSection("script", sfc.ScriptAST.Source, sfc.ScriptAST.Span)
		checkSection("template", sfc.TemplateAST.Source, sfc.TemplateAST.Span)
		checkSection("style", sfc.StyleAST.Source, sfc.StyleAST.Span)

		// Validators may return diagnostics for intentionally malformed inner
		// input, but must always be safe to call on the owned parse result.
		_ = ValidateTemplateAST(sfc.TemplateAST)
		_ = ValidateCssAST(sfc.StyleAST)
	})
}
