package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildSourceMapV3(t *testing.T) {
	m := BuildSourceMapV3("out.js", "App.vela", "let a = 1;\nlet b = 2;", []PrintLocation{
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
	m := BuildSourceMapV3("out.js", "App.vela", "🙂x", []PrintLocation{{GeneratedLine: 1, GeneratedColumn: 0, SourcePos: len("🙂")}})
	if m.Mappings != "AAAE" {
		t.Fatalf("mappings = %q, want UTF-16 column mapping %q", m.Mappings, "AAAE")
	}
}

func TestPrintJSWithLocationsUsesExplicitTemplateOrigin(t *testing.T) {
	expr := jsExpr("answer")
	setTemplateOrigin(expr, 17)
	var locations []PrintLocation
	PrintJSWithLocations(&JsProgram{Body: []JsStmt{&JsExprStmt{baseStmt: baseStmt{genNode()}, X: expr}}}, PrintPretty, func(loc PrintLocation) { locations = append(locations, loc) })
	if len(locations) != 1 || locations[0].SourcePos != 17 {
		t.Fatalf("locations = %+v, want one template-origin mapping at 17", locations)
	}
}

func TestPrintJSWithLocationsSkipsGeneratedNodes(t *testing.T) {
	expr := jsExpr("generated")
	var locations []PrintLocation
	PrintJSWithLocations(&JsProgram{Body: []JsStmt{&JsExprStmt{baseStmt: baseStmt{genNode()}, X: expr}}}, PrintPretty, func(loc PrintLocation) { locations = append(locations, loc) })
	if len(locations) != 0 {
		t.Fatalf("generated expression produced mappings: %+v", locations)
	}
}

func TestScriptPlanPreservesStatementOrigins(t *testing.T) {
	raw := "<script>\nlet before = 1;\n\nfunction explode() {\n  throw new Error('boom');\n}\n</script>\n<template><p>ok</p></template>"
	sfc, err := parseSFC(raw)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := parseTemplate(sfc.Template)
	if err != nil {
		t.Fatal(err)
	}
	prog := buildProgramCSS("Demo", sfc, nodes, analyzeScript(sfc.Script), "vela-test", false)
	shiftTree(prog, sfc.ScriptOffset)
	wantFunction := strings.Index(raw, "function explode")
	wantThrow := strings.Index(raw, "throw new Error")
	foundFunction, foundThrow := false, false
	PrintJSWithLocations(prog, PrintPretty, func(loc PrintLocation) {
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

func TestCompileFileEmitsSourceMap(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "Demo.vela")
	input := "<script>\nlet count = $state(0);\nfunction inc() { count++; }\n</script>\n<template><button @click={inc}>{count}</button></template>"
	if err := os.WriteFile(src, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "nested", "Demo.js")
	if _, err := compileFile(src, dest, CompileOptions{SourceMap: true}); err != nil {
		t.Fatal(err)
	}
	code, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(code), "sourceMappingURL=Demo.js.map") {
		t.Fatalf("missing sourceMappingURL: %s", code)
	}
	mapBytes, err := os.ReadFile(dest + ".map")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mapBytes), "\"version\":3") || !strings.Contains(string(mapBytes), "Demo.vela") {
		t.Fatalf("invalid source map: %s", mapBytes)
	}
}

func TestTemplateExpressionProducesSourceMapping(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "OnlyTemplate.vela")
	input := "<template>\n  <p>{'ok'}</p>\n</template>"
	if err := os.WriteFile(src, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "nested", "OnlyTemplate.js")
	if _, err := compileFile(src, dest, CompileOptions{SourceMap: true}); err != nil {
		t.Fatal(err)
	}
	var m SourceMapV3
	mapBytes, err := os.ReadFile(dest + ".map")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(mapBytes, &m); err != nil {
		t.Fatal(err)
	}
	if m.Mappings == "" {
		t.Fatal("template-only component produced no source mappings")
	}
	if len(m.Sources) != 1 || m.Sources[0] != filepath.ToSlash(src) || len(m.SourcesContent) != 1 || m.SourcesContent[0] != input {
		t.Fatalf("unexpected map source metadata: %#v", m)
	}
}

func TestSourceMapRejectsUnsupportedModes(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "Demo.vela")
	if err := os.WriteFile(src, []byte("<template><p>ok</p></template>"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, opts := range []CompileOptions{{SourceMap: true, Minify: true}} {
		if _, err := compileFile(src, filepath.Join(dir, "out", "Demo.js"), opts); err == nil {
			t.Fatalf("compileFile(%+v) succeeded, want unsupported source-map mode error", opts)
		}
	}
}

func TestInlineAwaitPromiseSourceMapOrigin(t *testing.T) {
	raw := "<script>let request = $state(Promise.resolve(1));</script><template>{#await request then value}<p>{value}</p>{/await}</template>"
	sfc, err := parseSFC(raw)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := parseTemplate(sfc.Template)
	if err != nil {
		t.Fatal(err)
	}
	prog := buildProgramCSS("InlineAwait", sfc, nodes, analyzeScript(sfc.Script), "vela-test", false)
	shiftTree(prog, sfc.ScriptOffset)
	promisePos := strings.Index(raw, "request then")
	inlineKeywordPos := strings.Index(raw, "then value")
	mappedPromise, mappedKeyword := false, false
	PrintJSWithLocations(prog, PrintPretty, func(loc PrintLocation) {
		mappedPromise = mappedPromise || loc.SourcePos == promisePos
		mappedKeyword = mappedKeyword || loc.SourcePos == inlineKeywordPos
	})
	if !mappedPromise || mappedKeyword {
		t.Fatalf("inline await source origins: promise=%v keyword=%v, want true/false", mappedPromise, mappedKeyword)
	}
}

func TestRepeatedTemplateExpressionsKeepDistinctOrigins(t *testing.T) {
	raw := "<script>let value = $state('');</script><template><p>{value} {value}</p></template>"
	sfc, err := parseSFC(raw)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := parseTemplate(sfc.Template)
	if err != nil {
		t.Fatal(err)
	}
	prog := buildProgramCSS("Repeated", sfc, nodes, analyzeScript(sfc.Script), "vela-test", false)
	shiftTree(prog, sfc.ScriptOffset)
	first := strings.Index(raw, "{value}") + 1
	second := strings.Index(raw[first+len("value}"):], "{value}") + first + len("value}") + 1
	seen := map[int]bool{}
	WalkNode(prog, func(n JsNode) bool {
		if ident, ok := n.(*JsIdent); ok && ident.Name == "fallback" {
			t.Logf("fallback ident pos=%d end=%d origin=%d source=%d has=%v", ident.Pos, ident.End, ident.Origin, ident.SourceOrigin, ident.HasSourceOrigin)
		}
		return true
	})
	PrintJSWithLocations(prog, PrintPretty, func(loc PrintLocation) { seen[loc.SourcePos] = true })
	if !seen[first] || !seen[second] {
		t.Fatalf("template mappings = %#v, want distinct expression starts %d and %d", seen, first, second)
	}
}

func TestComponentEventSourceMapOriginAndEventParameter(t *testing.T) {
	raw := "<script>let count = $state(0);</script><template><Child @on:close={count++} /></template>"
	sfc, err := parseSFC(raw)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := parseTemplate(sfc.Template)
	if err != nil {
		t.Fatal(err)
	}
	prog := buildProgramCSS("Parent", sfc, nodes, analyzeScript(sfc.Script), "vela-test", false)
	shiftTree(prog, sfc.ScriptOffset)
	output := PrintJS(prog, PrintPretty)
	if !strings.Contains(output, "($e) =>") || !strings.Contains(output, "$e instanceof CustomEvent") {
		t.Fatalf("component event wrapper lost its native event parameter:\n%s", output)
	}
	want := strings.Index(raw, "count++")
	found := false
	PrintJSWithLocations(prog, PrintPretty, func(loc PrintLocation) {
		found = found || loc.SourcePos == want
	})
	if !found {
		t.Fatalf("component event handler was not mapped to source offset %d", want)
	}
}

func TestEachRowTextExpressionKeepsSourceMapOrigin(t *testing.T) {
	raw := "<script>let items = $state.deep([]);</script><template>{#each items as item}<p>{item.title}</p>{/each}</template>"
	sfc, err := parseSFC(raw)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := parseTemplate(sfc.Template)
	if err != nil {
		t.Fatal(err)
	}
	prog := buildProgramCSS("Rows", sfc, nodes, analyzeScript(sfc.Script), "vela-test", false)
	shiftTree(prog, sfc.ScriptOffset)
	want := strings.Index(raw, "item.title")
	found := false
	PrintJSWithLocations(prog, PrintPretty, func(loc PrintLocation) {
		found = found || loc.SourcePos == want
	})
	if !found {
		t.Fatalf("each-row text expression was not mapped to source offset %d", want)
	}
}

func TestEachRowAttributeAndHandlerExpressionsKeepSourceMapOrigins(t *testing.T) {
	raw := "<script>let items = $state.deep([]); let count = $state(0);</script><template>{#each items as item}<button :title={item.title} class:selected={item.done} style:opacity={item.opacity} @click={() => count++}>{item.title}</button>{/each}</template>"
	sfc, err := parseSFC(raw)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := parseTemplate(sfc.Template)
	if err != nil {
		t.Fatal(err)
	}
	prog := buildProgramCSS("Rows", sfc, nodes, analyzeScript(sfc.Script), "vela-test", false)
	shiftTree(prog, sfc.ScriptOffset)
	seen := map[int]bool{}
	PrintJSWithLocations(prog, PrintPretty, func(loc PrintLocation) {
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
	sfc, err := parseSFC(raw)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := parseTemplate(sfc.Template)
	if err != nil {
		t.Fatal(err)
	}
	prog := buildProgramCSS("SlotFallback", sfc, nodes, analyzeScript(sfc.Script), "vela-test", false)
	shiftTree(prog, sfc.ScriptOffset)
	slotNode := nodes[0].(*SlotNode)
	part := slotNode.Fallback[0].(*TextNode).Parts[0]
	want := sfc.TemplateOffset + part.Expr.Pos
	found := false
	seen := map[int]bool{}
	PrintJSWithLocations(prog, PrintPretty, func(loc PrintLocation) {
		seen[loc.SourcePos] = true
		found = found || loc.SourcePos == want
	})
	if !found {
		t.Fatalf("slot fallback expression was not mapped to source offset %d; mappings: %#v", want, seen)
	}
}

func TestStructuredIfBranchExpressionKeepsSourceMapOrigin(t *testing.T) {
	raw := "<script>let visible = $state(true); let title = $state('ready');</script><template>{#if visible}<p>{title}</p>{:else}<p>hidden</p>{/if}</template>"
	sfc, err := parseSFC(raw)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := parseTemplate(sfc.Template)
	if err != nil {
		t.Fatal(err)
	}
	prog := buildProgramCSS("Conditional", sfc, nodes, analyzeScript(sfc.Script), "vela-test", false)
	shiftTree(prog, sfc.ScriptOffset)
	seen := map[int]bool{}
	PrintJSWithLocations(prog, PrintPretty, func(loc PrintLocation) {
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
	sfc, err := parseSFC(raw)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := parseTemplate(sfc.Template)
	if err != nil {
		t.Fatal(err)
	}
	prog := buildProgramCSS("ConditionalDOM", sfc, nodes, analyzeScript(sfc.Script), "vela-test", false)
	shiftTree(prog, sfc.ScriptOffset)
	seen := map[int]bool{}
	PrintJSWithLocations(prog, PrintPretty, func(loc PrintLocation) {
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
	sfc, err := parseSFC(raw)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := parseTemplate(sfc.Template)
	if err != nil {
		t.Fatal(err)
	}
	prog := buildProgramCSS("KeyedParent", sfc, nodes, analyzeScript(sfc.Script), "vela-test", false)
	shiftTree(prog, sfc.ScriptOffset)
	seen := map[int]bool{}
	PrintJSWithLocations(prog, PrintPretty, func(loc PrintLocation) { seen[loc.SourcePos] = true })
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
	sfc, err := parseSFC(raw)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := parseTemplate(sfc.Template)
	if err != nil {
		t.Fatal(err)
	}
	prog := buildProgramCSS("NestedRows", sfc, nodes, analyzeScript(sfc.Script), "vela-test", false)
	shiftTree(prog, sfc.ScriptOffset)
	seen := map[int]bool{}
	PrintJSWithLocations(prog, PrintPretty, func(loc PrintLocation) { seen[loc.SourcePos] = true })
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
	sfc, err := parseSFC(raw)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := parseTemplate(sfc.Template)
	if err != nil {
		t.Fatal(err)
	}
	prog := buildProgramCSS("AwaitOrigins", sfc, nodes, analyzeScript(sfc.Script), "vela-test", false)
	shiftTree(prog, sfc.ScriptOffset)
	seen := map[int]bool{}
	PrintJSWithLocations(prog, PrintPretty, func(loc PrintLocation) { seen[loc.SourcePos] = true })
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
	call := &JsCallExpr{baseExpr: baseExpr{genNode()}, Callee: id("$$set_count"), Args: []JsExpr{id("$e")}}
	setTemplateOrigin(call, want)
	guard := &JsIfStmt{baseStmt: baseStmt{genNode()}, Test: &JsBinaryExpr{baseExpr: baseExpr{genNode()}, Op: "instanceof", L: id("$e"), R: id("CustomEvent")}, Then: &JsExprStmt{baseStmt: baseStmt{genNode()}, X: call}}
	wrapper := &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$e")}, Body: &JsBlock{baseStmt: baseStmt{genNode()}, Body: []JsStmt{guard}}}
	prog := &JsProgram{Body: []JsStmt{&JsVarDecl{baseStmt: baseStmt{genNode()}, Kind: "const", Decls: []*JsVarDeclarator{{Id: id("$$handler"), Init: wrapper}}}}}
	seen := false
	PrintJSWithLocations(prog, PrintPretty, func(loc PrintLocation) { seen = seen || loc.SourcePos == want })
	if !seen {
		t.Fatalf("nested hoisted handler call origin %d was not printed", want)
	}
}
