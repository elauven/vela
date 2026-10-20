package codegen

import (
	"strings"
	"testing"

	"github.com/elauven/vela/internal/analyzer"
	"github.com/elauven/vela/internal/jsast"
	"github.com/elauven/vela/internal/sfcast"
)

func TestBuildSourceMapV3(t *testing.T) {
	m := BuildSourceMapV3("out.js", "App.vela", "let a = 1;\nlet b = 2;", []jsast.PrintLocation{
		{GeneratedLine: 1, GeneratedColumn: 0, SourcePos: 0},
		{GeneratedLine: 3, GeneratedColumn: 2, SourcePos: 11},
	})
	if m.Version != 3 || m.File != "out.js" || len(m.Sources) != 1 || m.Sources[0] != "App.vela" {
		t.Fatalf("unexpected map header: %#v", m)
	}
	if m.Mappings != "AAAA;;EACA" {
		t.Fatalf("mappings = %q, want %q", m.Mappings, "AAAA;;EACA")
	}
	if _, err := SourceMapJSON(m); err != nil {
		t.Fatalf("marshal: %v", err)
	}
}

func TestSourceMapUsesUTF16Columns(t *testing.T) {
	m := BuildSourceMapV3("out.js", "App.vela", "🙂x", []jsast.PrintLocation{{GeneratedLine: 1, GeneratedColumn: 0, SourcePos: len("🙂")}})
	if m.Mappings != "AAAE" {
		t.Fatalf("mappings = %q, want UTF-16 column mapping %q", m.Mappings, "AAAE")
	}
}

func TestPrintJSWithLocationsUsesExplicitTemplateOrigin(t *testing.T) {
	expr := jsast.GenIdent("answer")
	setTemplateOrigin(expr, 17)
	var locations []jsast.PrintLocation
	jsast.PrintJSWithLocations(&jsast.JsProgram{Body: []jsast.JsStmt{&jsast.JsExprStmt{BaseStmt: jsast.BaseStmt{BaseNode: jsast.GenNode()}, X: expr}}}, jsast.PrintPretty, func(loc jsast.PrintLocation) { locations = append(locations, loc) })
	if len(locations) != 1 || locations[0].SourcePos != 17 {
		t.Fatalf("locations = %+v, want one template-origin mapping at 17", locations)
	}
}

func TestScriptPlanPreservesStatementOrigins(t *testing.T) {
	raw := "<script>\nlet before = 1;\n\nfunction explode() {\n  throw new Error('boom');\n}\n</script>\n<template><p>ok</p></template>"
	sfc, err := sfcast.ParseSFC(raw)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := sfcast.ParseTemplate(sfc.Template)
	if err != nil {
		t.Fatal(err)
	}
	prog := BuildProgramCSS("Demo", sfc, nodes, analyzer.AnalyzeScript(sfc.Script), "vela-test", false)
	jsast.ShiftTree(prog, sfc.ScriptOffset)
	wantFunction := strings.Index(raw, "function explode")
	wantThrow := strings.Index(raw, "throw new Error")
	foundFunction, foundThrow := false, false
	jsast.PrintJSWithLocations(prog, jsast.PrintPretty, func(loc jsast.PrintLocation) {
		if loc.SourcePos == wantFunction {
			foundFunction = true
		}
		if loc.SourcePos == wantThrow {
			foundThrow = true
		}
	})
	if !foundFunction || !foundThrow {
		t.Fatalf("script mappings: function=%v throw=%v, want source offsets %d and %d", foundFunction, foundThrow, wantFunction, wantThrow)
	}
}

func TestInlineAwaitPromiseSourceMapOrigin(t *testing.T) {
	raw := "<script>let request = $state(Promise.resolve(1));</script><template>{#await request then value}<p>{value}</p>{/await}</template>"
	sfc, err := sfcast.ParseSFC(raw)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := sfcast.ParseTemplate(sfc.Template)
	if err != nil {
		t.Fatal(err)
	}
	prog := BuildProgramCSS("InlineAwait", sfc, nodes, analyzer.AnalyzeScript(sfc.Script), "vela-test", false)
	jsast.ShiftTree(prog, sfc.ScriptOffset)
	promisePos := strings.Index(raw, "request then")
	inlineKeywordPos := strings.Index(raw, "then value")
	mappedPromise, mappedKeyword := false, false
	jsast.PrintJSWithLocations(prog, jsast.PrintPretty, func(loc jsast.PrintLocation) {
		mappedPromise = mappedPromise || loc.SourcePos == promisePos
		mappedKeyword = mappedKeyword || loc.SourcePos == inlineKeywordPos
	})
	if !mappedPromise || mappedKeyword {
		t.Fatalf("inline await source origins: promise=%v keyword=%v, want true/false", mappedPromise, mappedKeyword)
	}
}

func TestRepeatedTemplateExpressionsKeepDistinctOrigins(t *testing.T) {
	raw := "<script>let value = $state('');</script><template><p>{value} {value}</p></template>"
	sfc, err := sfcast.ParseSFC(raw)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := sfcast.ParseTemplate(sfc.Template)
	if err != nil {
		t.Fatal(err)
	}
	prog := BuildProgramCSS("Repeated", sfc, nodes, analyzer.AnalyzeScript(sfc.Script), "vela-test", false)
	jsast.ShiftTree(prog, sfc.ScriptOffset)
	first := strings.Index(raw, "{value}") + 1
	second := strings.Index(raw[first+len("value}"):], "{value}") + first + len("value}") + 1
	seen := map[int]bool{}
	jsast.WalkNode(prog, func(n jsast.JsNode) bool {
		if ident, ok := n.(*jsast.JsIdent); ok && ident.Name == "fallback" {
			t.Logf("fallback ident pos=%d end=%d origin=%d source=%d has=%v", ident.Pos, ident.End, ident.Origin, ident.SourceOrigin, ident.HasSourceOrigin)
		}
		return true
	})
	jsast.PrintJSWithLocations(prog, jsast.PrintPretty, func(loc jsast.PrintLocation) { seen[loc.SourcePos] = true })
	if !seen[first] || !seen[second] {
		t.Fatalf("template mappings = %#v, want distinct expression starts %d and %d", seen, first, second)
	}
}

func TestComponentEventSourceMapOriginAndEventParameter(t *testing.T) {
	raw := "<script>let count = $state(0);</script><template><Child @on:close={count++} /></template>"
	sfc, err := sfcast.ParseSFC(raw)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := sfcast.ParseTemplate(sfc.Template)
	if err != nil {
		t.Fatal(err)
	}
	prog := BuildProgramCSS("Parent", sfc, nodes, analyzer.AnalyzeScript(sfc.Script), "vela-test", false)
	jsast.ShiftTree(prog, sfc.ScriptOffset)
	output := jsast.PrintJS(prog, jsast.PrintPretty)
	if !strings.Contains(output, "($e) =>") || !strings.Contains(output, "$e instanceof CustomEvent") {
		t.Fatalf("component event wrapper lost its native event parameter:\n%s", output)
	}
	want := strings.Index(raw, "count++")
	found := false
	jsast.PrintJSWithLocations(prog, jsast.PrintPretty, func(loc jsast.PrintLocation) {
		found = found || loc.SourcePos == want
	})
	if !found {
		t.Fatalf("component event handler was not mapped to source offset %d", want)
	}
}

func TestEachRowTextExpressionKeepsSourceMapOrigin(t *testing.T) {
	raw := "<script>let items = $state.deep([]);</script><template>{#each items as item}<p>{item.title}</p>{/each}</template>"
	sfc, err := sfcast.ParseSFC(raw)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := sfcast.ParseTemplate(sfc.Template)
	if err != nil {
		t.Fatal(err)
	}
	prog := BuildProgramCSS("Rows", sfc, nodes, analyzer.AnalyzeScript(sfc.Script), "vela-test", false)
	jsast.ShiftTree(prog, sfc.ScriptOffset)
	want := strings.Index(raw, "item.title")
	found := false
	jsast.PrintJSWithLocations(prog, jsast.PrintPretty, func(loc jsast.PrintLocation) {
		found = found || loc.SourcePos == want
	})
	if !found {
		t.Fatalf("each-row text expression was not mapped to source offset %d", want)
	}
}

func TestEachRowAttributeAndHandlerExpressionsKeepSourceMapOrigins(t *testing.T) {
	raw := "<script>let items = $state.deep([]); let count = $state(0);</script><template>{#each items as item}<button :title={item.title} class:selected={item.done} style:opacity={item.opacity} @click={() => count++}>{item.title}</button>{/each}</template>"
	sfc, err := sfcast.ParseSFC(raw)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := sfcast.ParseTemplate(sfc.Template)
	if err != nil {
		t.Fatal(err)
	}
	prog := BuildProgramCSS("Rows", sfc, nodes, analyzer.AnalyzeScript(sfc.Script), "vela-test", false)
	jsast.ShiftTree(prog, sfc.ScriptOffset)
	seen := map[int]bool{}
	jsast.PrintJSWithLocations(prog, jsast.PrintPretty, func(loc jsast.PrintLocation) {
		seen[loc.SourcePos] = true
	})
	wants := map[string]int{
		"attribute": strings.Index(raw, ":title={item.title}") + len(":title={"),
		"class":     strings.Index(raw, "item.done"),
		"style":     strings.Index(raw, "item.opacity"),
		"handler":   strings.Index(raw, "() => count++"),
	}
	for kind, want := range wants {
		if !seen[want] {
			t.Fatalf("each-row %s expression was not mapped to source offset %d; mappings: %#v", kind, want, seen)
		}
	}
}

func TestSlotFallbackExpressionKeepsSourceMapOrigin(t *testing.T) {
	raw := "<script>let fallback = $state(\"default\");</script><template><slot>{fallback}</slot></template>"
	sfc, err := sfcast.ParseSFC(raw)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := sfcast.ParseTemplate(sfc.Template)
	if err != nil {
		t.Fatal(err)
	}
	prog := BuildProgramCSS("SlotFallback", sfc, nodes, analyzer.AnalyzeScript(sfc.Script), "vela-test", false)
	jsast.ShiftTree(prog, sfc.ScriptOffset)
	slotNode := nodes[0].(*sfcast.SlotNode)
	part := slotNode.Fallback[0].(*sfcast.TextNode).Parts[0]
	want := sfc.TemplateOffset + part.Expr.Pos
	found := false
	seen := map[int]bool{}
	jsast.PrintJSWithLocations(prog, jsast.PrintPretty, func(loc jsast.PrintLocation) {
		seen[loc.SourcePos] = true
		found = found || loc.SourcePos == want
	})
	if !found {
		t.Fatalf("slot fallback expression was not mapped to source offset %d; mappings: %#v", want, seen)
	}
}

func TestStructuredIfBranchExpressionKeepsSourceMapOrigin(t *testing.T) {
	raw := "<script>let visible = $state(true); let title = $state('ready');</script><template>{#if visible}<p>{title}</p>{:else}<p>hidden</p>{/if}</template>"
	sfc, err := sfcast.ParseSFC(raw)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := sfcast.ParseTemplate(sfc.Template)
	if err != nil {
		t.Fatal(err)
	}
	prog := BuildProgramCSS("Conditional", sfc, nodes, analyzer.AnalyzeScript(sfc.Script), "vela-test", false)
	jsast.ShiftTree(prog, sfc.ScriptOffset)
	seen := map[int]bool{}
	jsast.PrintJSWithLocations(prog, jsast.PrintPretty, func(loc jsast.PrintLocation) {
		seen[loc.SourcePos] = true
	})
	for kind, want := range map[string]int{
		"condition": strings.LastIndex(raw, "visible"),
		"body":      strings.LastIndex(raw, "title"),
	} {
		if !seen[want] {
			t.Fatalf("structured if %s was not mapped to source offset %d; mappings: %#v", kind, want, seen)
		}
	}
}

func TestStructuredIfDynamicDOMKeepsSourceMapOrigins(t *testing.T) {
	raw := "<script>let visible = $state(true); let active = $state(false); let label = $state(\"ready\"); let count = $state(0);</script><template>{#if visible}<input :data-label={label} class:selected={active} style:opacity={active ? 1 : 0.5} bind:value={label} @click={() => count++} />{/if}</template>"
	sfc, err := sfcast.ParseSFC(raw)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := sfcast.ParseTemplate(sfc.Template)
	if err != nil {
		t.Fatal(err)
	}
	prog := BuildProgramCSS("ConditionalDOM", sfc, nodes, analyzer.AnalyzeScript(sfc.Script), "vela-test", false)
	jsast.ShiftTree(prog, sfc.ScriptOffset)
	seen := map[int]bool{}
	jsast.PrintJSWithLocations(prog, jsast.PrintPretty, func(loc jsast.PrintLocation) {
		seen[loc.SourcePos] = true
	})
	for kind, want := range map[string]int{
		"condition":       strings.LastIndex(raw, "visible"),
		"attribute":       strings.LastIndex(raw, "label}"),
		"class directive": strings.Index(raw, "class:selected={active}") + len("class:selected={"),
		"style directive": strings.LastIndex(raw, "active"),
		"DOM handler":     strings.Index(raw, "() => count++"),
		"binding":         strings.LastIndex(raw, "label}"),
	} {
		if !seen[want] {
			t.Fatalf("structured if dynamic %s was not mapped to source offset %d; mappings: %#v", kind, want, seen)
		}
	}
}

func TestStructuredKeyComponentKeepsSourceMapOrigins(t *testing.T) {
	raw := "<script>let identity = $state(1); let value = $state(\"one\"); let count = $state(0);</script><template>{#key identity}<Child :value={value} @on:close={count++} />{/key}</template>"
	sfc, err := sfcast.ParseSFC(raw)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := sfcast.ParseTemplate(sfc.Template)
	if err != nil {
		t.Fatal(err)
	}
	prog := BuildProgramCSS("KeyedParent", sfc, nodes, analyzer.AnalyzeScript(sfc.Script), "vela-test", false)
	jsast.ShiftTree(prog, sfc.ScriptOffset)
	seen := map[int]bool{}
	jsast.PrintJSWithLocations(prog, jsast.PrintPretty, func(loc jsast.PrintLocation) { seen[loc.SourcePos] = true })
	for kind, want := range map[string]int{
		"key":   strings.LastIndex(raw, "identity"),
		"prop":  strings.LastIndex(raw, "value}"),
		"event": strings.LastIndex(raw, "count++"),
	} {
		if !seen[want] {
			t.Fatalf("structured key component %s was not mapped to source offset %d; mappings: %#v", kind, want, seen)
		}
	}
}

func TestNestedEachAndIfExpressionsKeepSourceMapOrigins(t *testing.T) {
	raw := "<script>let items = $state.deep([]); let flag = $state(true); let show = $state(true);</script><template>{#each items as item}<p>{#if flag}<span :title={item.title}>{item.title}</span>{/if}</p>{/each}{#if show}<ul>{#each items as item}<li>{item.title}</li>{/each}</ul>{/if}</template>"
	sfc, err := sfcast.ParseSFC(raw)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := sfcast.ParseTemplate(sfc.Template)
	if err != nil {
		t.Fatal(err)
	}
	prog := BuildProgramCSS("NestedRows", sfc, nodes, analyzer.AnalyzeScript(sfc.Script), "vela-test", false)
	jsast.ShiftTree(prog, sfc.ScriptOffset)
	seen := map[int]bool{}
	jsast.PrintJSWithLocations(prog, jsast.PrintPretty, func(loc jsast.PrintLocation) { seen[loc.SourcePos] = true })
	for kind, want := range map[string]int{
		"row iterable": strings.Index(raw, "items as item"),
		// Locate the directive payload, not the same state binding in
		// <script>. A source map for `{#if flag}` must point into the
		// template even when the declaration happens to have a similar
		// source position.
		"row condition":    strings.Index(raw, "{#if flag}") + len("{#if "),
		"row attribute":    strings.Index(raw, ":title={item.title}") + len(":title={"),
		"branch condition": strings.Index(raw, "{#if show}") + len("{#if "),
		"nested iterable":  strings.LastIndex(raw, "items as item"),
	} {
		if !seen[want] {
			t.Fatalf("nested phase-4 %s was not mapped to source offset %d; mappings: %#v", kind, want, seen)
		}
	}
}

func TestAwaitBranchExpressionsKeepSourceMapOrigins(t *testing.T) {
	raw := "<script>let request = $state(Promise.resolve(\"ok\")); let pendingLabel = $state(\"loading\");</script><template>{#await request}<p>{pendingLabel}</p>{:then value}<p :title={value}>{value}</p>{:catch error}<p>{error.message}</p>{/await}</template>"
	sfc, err := sfcast.ParseSFC(raw)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := sfcast.ParseTemplate(sfc.Template)
	if err != nil {
		t.Fatal(err)
	}
	prog := BuildProgramCSS("AwaitOrigins", sfc, nodes, analyzer.AnalyzeScript(sfc.Script), "vela-test", false)
	jsast.ShiftTree(prog, sfc.ScriptOffset)
	seen := map[int]bool{}
	jsast.PrintJSWithLocations(prog, jsast.PrintPretty, func(loc jsast.PrintLocation) { seen[loc.SourcePos] = true })
	for kind, want := range map[string]int{
		"promise": strings.Index(raw, "request}<p"),
		"pending": strings.Index(raw, "pendingLabel}"),
		"then":    strings.Index(raw, ":title={value}") + len(":title={"),
		"catch":   strings.Index(raw, "error.message"),
	} {
		if !seen[want] {
			t.Fatalf("await %s expression was not mapped to source offset %d; mappings: %#v", kind, want, seen)
		}
	}
}

func TestPrintJSWithLocationsReportsNestedHoistedHandlerCallOrigin(t *testing.T) {
	const want = 67
	call := &jsast.JsCallExpr{BaseExpr: jsast.BaseExpr{BaseNode: jsast.GenNode()}, Callee: jsast.GenIdent("$$set_count"), Args: []jsast.JsExpr{jsast.GenIdent("$e")}}
	setTemplateOrigin(call, want)
	guard := &jsast.JsIfStmt{BaseStmt: jsast.BaseStmt{BaseNode: jsast.GenNode()}, Test: &jsast.JsBinaryExpr{BaseExpr: jsast.BaseExpr{BaseNode: jsast.GenNode()}, Op: "instanceof", L: jsast.GenIdent("$e"), R: jsast.GenIdent("CustomEvent")}, Then: &jsast.JsExprStmt{BaseStmt: jsast.BaseStmt{BaseNode: jsast.GenNode()}, X: call}}
	wrapper := &jsast.JsArrowFunc{BaseExpr: jsast.BaseExpr{BaseNode: jsast.GenNode()}, Params: []jsast.JsExpr{jsast.GenIdent("$e")}, Body: &jsast.JsBlock{BaseStmt: jsast.BaseStmt{BaseNode: jsast.GenNode()}, Body: []jsast.JsStmt{guard}}}
	prog := &jsast.JsProgram{Body: []jsast.JsStmt{&jsast.JsVarDecl{BaseStmt: jsast.BaseStmt{BaseNode: jsast.GenNode()}, Kind: "const", Decls: []*jsast.JsVarDeclarator{{Id: jsast.GenIdent("$$handler"), Init: wrapper}}}}}
	seen := false
	jsast.PrintJSWithLocations(prog, jsast.PrintPretty, func(loc jsast.PrintLocation) { seen = seen || loc.SourcePos == want })
	if !seen {
		t.Fatalf("nested hoisted handler call origin %d was not printed", want)
	}
}
