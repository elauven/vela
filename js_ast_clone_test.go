package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestTemplateExpressionsWithExtendedSyntaxCompile(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available")
	}
	src := "<script>let a = $state(1); const tag = (s, ...v) => s.join('') + v.join('');</script>\n" +
		"<template><p @click={() => { try { a = a + 1 } catch (e) { a = 0 } switch (a) { case 3: a = 1; break; default: } }}>{tag`n${a}`}</p></template>"
	dir := t.TempDir()
	file := filepath.Join(dir, "App.vela")
	if err := os.WriteFile(file, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "App.js")
	if _, err := compileFile(file, out, CompileOptions{}); err != nil {
		t.Fatal(err)
	}
	if msg, err := exec.Command(node, "--check", out).CombinedOutput(); err != nil {
		t.Fatalf("emitted module does not load: %s", msg)
	}
}
