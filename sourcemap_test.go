package main

import (
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

func TestCompileFileEmitsSourceMap(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "Demo.vela")
	input := "<script>\nlet count = $state(0);\nfunction inc() { count++; }\n</script>\n<template><button @click={inc}>{count}</button></template>"
	if err := os.WriteFile(src, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "Demo.js")
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
