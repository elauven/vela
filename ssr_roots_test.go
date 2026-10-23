package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestSSRManifestRootModes covers A-8.1, A-8.2, and A-8.8: a component is
// interactive when it or any component it uses has events, bindings,
// effects, or lifecycle hooks; vela.manifest.json lists every component
// with its mode and client module.
func TestSSRManifestRootModes(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	files := map[string]string{
		"Plain.vela":        `<template><p>static</p></template>`,
		"Click.vela":        `<template><button @click={() => {}}>x</button></template>`,
		"Parent.vela":       `<script>import Click from './Click.vela';</script><template><div><Click /></div></template>`,
		"Grand.vela":        `<script>import Parent from './Parent.vela';</script><template><section><Parent /></section></template>`,
		"Effect.vela":       `<script>let n = $state(0); $effect(() => { n; });</script><template><p>{n}</p></template>`,
		"nested/Hook.vela":  `<script>import { onMount } from 'vela'; onMount(() => {});</script><template><p>x</p></template>`,
		"nested/Bound.vela": `<script>let v = $state('');</script><template><input bind:value={v} /></template>`,
	}
	for name, source := range files {
		file := filepath.Join(srcRoot, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	outRoot := filepath.Join(root, "out")

	opts := CompileOptions{StatusWriter: io.Discard, SSRModule: "example.com/modes/server"}
	if errors := BuildSSRProject(CollectVela(srcRoot), srcRoot, outRoot, opts, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}

	data, err := os.ReadFile(filepath.Join(outRoot, "client", "vela.manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest SSRManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	want := map[string][2]string{
		"Plain.vela": {"static", "Plain.js"}, "Click.vela": {"interactive", "Click.js"}, "Parent.vela": {"interactive", "Parent.js"},
		"Grand.vela": {"interactive", "Grand.js"}, "Effect.vela": {"interactive", "Effect.js"},
		"nested/Hook.vela": {"interactive", "nested/Hook.js"}, "nested/Bound.vela": {"interactive", "nested/Bound.js"},
	}
	if len(manifest.Components) != len(want) {
		t.Fatalf("manifest = %+v", manifest)
	}
	for _, component := range manifest.Components {
		if got := [2]string{component.Mode, component.Module}; got != want[component.Source] {
			t.Errorf("%s = %v, want %v", component.Source, got, want[component.Source])
		}
	}
}
