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
	if _, err := compileFile(src, dest, CompileOptions{Codegen: "ast", SourceMap: true}); err != nil {
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
	if _, err := compileFile(src, dest, CompileOptions{Codegen: "ast", SourceMap: true}); err != nil {
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
	for _, opts := range []CompileOptions{{Codegen: "ast", SourceMap: true, Minify: true}, {Codegen: "legacy", SourceMap: true}} {
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
	markTemplateOrigins(prog, sfc, nodes, analyzeScript(sfc.Script))
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
