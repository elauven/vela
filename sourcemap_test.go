package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrintJSWithLocationsSkipsGeneratedNodes(t *testing.T) {
	expr := GenIdent("generated")
	var locations []PrintLocation
	PrintJSWithLocations(&JsProgram{Body: []JsStmt{&JsExprStmt{BaseStmt: BaseStmt{BaseNode: GenNode()}, X: expr}}}, PrintPretty, func(loc PrintLocation) { locations = append(locations, loc) })
	if len(locations) != 0 {
		t.Fatalf("generated expression produced mappings: %+v", locations)
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
	if _, err := CompileFile(src, dest, CompileOptions{SourceMap: true}); err != nil {
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
	if _, err := CompileFile(src, dest, CompileOptions{SourceMap: true}); err != nil {
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
		if _, err := CompileFile(src, filepath.Join(dir, "out", "Demo.js"), opts); err == nil {
			t.Fatalf("compileFile(%+v) succeeded, want unsupported source-map mode error", opts)
		}
	}
}
