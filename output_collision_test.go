package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Two sources of a project whose outputs are one file, also when the names
// differ only in case (macOS and Windows ignore it), are an error, and the
// second is not written over the first.
func TestOutputCollisionsFailTheBuild(t *testing.T) {
	srcRoot, outRoot := tsProject(t, map[string]string{
		"App.vela":     "<script>\n  import Card from './Card.vela';\n</script>\n<template><Card /></template>",
		"Card.vela":    "<template><p>card</p></template>",
		"card.js":      "export const data = 1;\n",
		"util.js":      "export const u = 1;\n",
		"util.ts":      "export const u: number = 1;\n",
		"Pages/A.vela": "<template><p>a</p></template>",
		"pages/a.ts":   "export const a: number = 1;\n",
	})
	errors, diags := buildTSProject(t, srcRoot, outRoot, CompileOptions{})
	got := map[string]string{}
	// Keys without case: on a file system that ignores case, pages/a.ts is
	// written into the directory Pages that Pages/A.vela created.
	for _, d := range diags {
		got[strings.ToLower(filepath.ToSlash(d.File[strings.Index(filepath.ToSlash(d.File), "src/")+4:]))] = d.Code
	}
	want := map[string]string{
		"card.js":    "VELA-OUTPUT-COLLISION",
		"util.ts":    "VELA-TS-MODULE",
		"pages/a.ts": "VELA-OUTPUT-COLLISION",
	}
	for file, code := range want {
		if got[file] != code {
			t.Errorf("%s: got %q, want %q (all: %v)", file, got[file], code, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("diagnostics %v, want %v", got, want)
	}
	if errors != len(want) {
		t.Errorf("%d errors, want %d", errors, len(want))
	}
	// The component was written and not replaced by the module.
	card, err := os.ReadFile(filepath.Join(outRoot, "Card.js"))
	if err != nil || strings.Contains(string(card), "export const data") {
		t.Errorf("Card.js is not the component: %v\n%s", err, card)
	}
	if _, err := os.Stat(filepath.Join(outRoot, "card.js")); err == nil && !SameFile(filepath.Join(outRoot, "card.js"), filepath.Join(outRoot, "Card.js")) {
		t.Error("card.js was written")
	}
	for _, d := range diags {
		if d.Code == "VELA-OUTPUT-COLLISION" && strings.HasSuffix(d.File, "card.js") &&
			!strings.Contains(d.Message, "Card.vela and card.js compile to Card.js and card.js, one file on file systems that ignore case") {
			t.Errorf("message: %s", d.Message)
		}
	}
}

// `vela check` reports the collision as the build does.
func TestOutputCollisionsInCheck(t *testing.T) {
	srcRoot, _ := tsProject(t, map[string]string{
		"Card.vela": "<template><p>card</p></template>",
		"card.ts":   "export const c: number = 1;\n",
	})
	diags, _, err := CheckTarget{SrcRoot: srcRoot}.Run()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range diags {
		if d.Code == "VELA-OUTPUT-COLLISION" && strings.HasSuffix(filepath.ToSlash(d.File), "card.ts") {
			return
		}
	}
	t.Errorf("no VELA-OUTPUT-COLLISION: %+v", diags)
}

// With --ssr two components whose Go files are one (UserCard.vela and
// User_Card.vela both write user_card.go; CardProps.vela writes Card's
// card_props.go) fail, on every file system.
func TestSSROutputCollisions(t *testing.T) {
	srcRoot, outRoot := tsProject(t, map[string]string{
		"UserCard.vela":  "<template><p>a</p></template>",
		"User_Card.vela": "<template><p>b</p></template>",
		"Card.vela":      "<template><p>c</p></template>",
		"CardProps.vela": "<template><p>d</p></template>",
	})
	var errBuf bytes.Buffer
	opts := CompileOptions{StatusWriter: io.Discard, ErrWriter: &errBuf, SSRModule: "example.com/collide/server"}
	errors := BuildSSRProject(CollectVela(srcRoot), srcRoot, outRoot, opts, false)
	out := errBuf.String()
	if errors != 2 || strings.Count(out, "VELA-OUTPUT-COLLISION") != 2 ||
		!strings.Contains(out, "UserCard.vela and User_Card.vela both write the Go file user_card.go") ||
		!strings.Contains(out, "Card.vela and CardProps.vela both write the Go file card_props.go") {
		t.Errorf("%d errors:\n%s", errors, out)
	}
}

// The SSR watcher removes the files of a removed component, but not a file
// that is also a kept component's: after Card.vela became card.vela on a
// file system that ignores case, Card.js is card.js. A hard link stands in
// for such a file system where the real one keeps case.
func TestSSRWatchKeepsFilesOfKeptComponents(t *testing.T) {
	srcRoot, outRoot := t.TempDir(), t.TempDir()
	layout := NewSSRProjectLayoutWithModule(outRoot, SSRDefaultModule)
	removed, kept := filepath.Join(srcRoot, "Card.vela"), filepath.Join(srcRoot, "card.vela")
	write := func(path string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	keptClient := MirrorPath(kept, srcRoot, layout.ClientDir, ".js")
	removedClient := MirrorPath(removed, srcRoot, layout.ClientDir, ".js")
	write(keptClient)

	// A file system that ignores case (macOS, Windows): Card.js is card.js,
	// and it stays.
	if SameFile(keptClient, removedClient) {
		removeSSROutputs(removed, srcRoot, outRoot, []string{kept}, CompileOptions{StatusWriter: io.Discard})
		if _, err := os.Stat(keptClient); err != nil {
			t.Errorf("card.js of the kept component was removed: %v", err)
		}
		return
	}

	// Two distinct files (a file system that keeps case): the old one goes.
	write(removedClient)
	removeSSROutputs(removed, srcRoot, outRoot, []string{kept}, CompileOptions{StatusWriter: io.Discard})
	if _, err := os.Stat(removedClient); err == nil {
		t.Error("Card.js of the removed component was kept")
	}
	// One file under two names: it stays.
	if err := os.Link(keptClient, removedClient); err != nil {
		t.Skip("no hard links:", err)
	}
	removeSSROutputs(removed, srcRoot, outRoot, []string{kept}, CompileOptions{StatusWriter: io.Discard})
	if _, err := os.Stat(keptClient); err != nil {
		t.Errorf("card.js of the kept component was removed: %v", err)
	}
}
