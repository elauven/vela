package compiler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elauven/vela/internal/analyzer"
	"github.com/elauven/vela/internal/jsast"
	"github.com/elauven/vela/internal/sfcast"
)

func declarationsOf(t *testing.T, name, src string) string {
	t.Helper()
	sfc, err := sfcast.ParseSFC(src)
	if err != nil {
		t.Fatal(err)
	}
	out := ComponentDeclarations(name, name+".vela", analyzer.ComponentTypesOf(sfc))
	// Every declaration file is TypeScript the compiler's parser accepts.
	if _, diags := jsast.ParseWithOptions(out, jsast.ParseOptions{TypeScript: true}); len(diags) > 0 {
		t.Fatalf("the declarations do not parse: %s\n%s", diags[0].Message, out)
	}
	return out
}

func TestComponentDeclarations(t *testing.T) {
	cases := []struct {
		name, src string
		want      []string
		not       []string
	}{{
		name: "a local type named like the generated one",
		src: `<script lang="ts">
  interface CardProps { size: number }
  export let p: CardProps;
</script>
<template><p>{p.size}</p></template>`,
		want: []string{"interface CardProps { size: number }", "export interface CardComponentProps {\n  p: CardProps;\n}",
			"$$VelaComponent<CardComponentProps, CardEvents, CardSlots, never>"},
	}, {
		name: "values of the script are any for consumers",
		src: `<script lang="ts">
  class Point { x = 0 }
  const config = { dense: true };
  export let at: Point;
  export let options: typeof config | null = null;
</script>
<template><p>{at.x}</p></template>`,
		want: []string{"type Point = any;", "declare const config: any;", "at: Point;", "options?: typeof config | null;"},
	}, {
		name: "the modules of the compiler become the runtime package",
		src: `<script lang="ts">
  import type { Readable } from 'vela/store';
  import { type Component } from 'vela';
  import type * as util from './util.ts';
  import Row from './Row.vela';
  export let count: Readable<number>;
  export let view: Component | null = null;
  export let mode: util.Mode = 'a';
  export let row: typeof Row | null = null;
</script>
<template><p>{mode}</p></template>`,
		want: []string{`import type { Readable } from "@govela/runtime";`, `import type { Component } from "@govela/runtime";`,
			`import type * as util from "./util.js";`, `import type Row from "./Row.js";`},
	}, {
		name: "annotations spanning lines keep their lines",
		src: `<script lang="ts">
  export let shape:
    | { kind: 'circle'; r: number }
    | { kind: 'square'; side: number };
</script>
<template><p>{shape.kind}</p></template>`,
		want: []string{"  shape: | { kind: 'circle'; r: number }\n    | { kind: 'square'; side: number };"},
	}, {
		name: "JSDoc that is not TypeScript falls back to the default",
		src: `<script>
  /** @type {?string} */
  export let label = 'x';
  /** @type {function(string): void} */
  export let onPick;
</script>
<template><p>{label}</p></template>`,
		want: []string{"label?: string;", "onPick: any;"},
	}, {
		name: "event names that are not identifiers are quoted",
		src: `<script>
  function open() { $$dispatch('item:open', 1); $$dispatch('close'); }
</script>
<template><p @click={open}>x</p></template>`,
		want: []string{`export type CardEvents = { close: any; "item:open": any };`},
	}, {
		name: "a computed event name allows any event",
		src: `<script>
  export let name = 'a';
  function fire() { $$dispatch(name); $$dispatch('fixed'); }
</script>
<template><p @click={fire}>x</p></template>`,
		want: []string{"export type CardEvents = Record<string, any>;"},
	}, {
		name: "unused local types stay out",
		src: `<script lang="ts">
  interface Internal { secret: string }
  type Size = 'sm' | 'lg';
  let state = $state<Internal | null>(null);
  export let size: Size = 'sm';
</script>
<template><p>{size}</p></template>`,
		want: []string{"type Size = 'sm' | 'lg';", "size?: Size;"},
		not:  []string{"Internal"},
	}, {
		name: "exported local types stay exported",
		src: `<script lang="ts">
  export interface Item { id: string }
  export let items: Item[] = [];
</script>
<template><p>{items.length}</p></template>`,
		want: []string{"export interface Item { id: string }"},
	}, {
		name: "slots of $$Slots win over the template",
		src: `<script lang="ts">
  type $$Slots = { header: {}; default: {} };
</script>
<template><slot name="other" /></template>`,
		want: []string{"export type CardSlots = { header: {}; default: {} };"},
		not:  []string{"other"},
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := declarationsOf(t, "Card", c.src)
			for _, w := range c.want {
				if !strings.Contains(out, w) {
					t.Errorf("missing %q in\n%s", w, out)
				}
			}
			for _, n := range c.not {
				if strings.Contains(out, n) {
					t.Errorf("unexpected %q in\n%s", n, out)
				}
			}
		})
	}
}

func TestDTSOption(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "Card.vela")
	if err := os.WriteFile(src, []byte("<script lang=\"ts\">\n  export let title: string;\n</script>\n<template><p>{title}</p></template>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "out", "Card.js")
	if _, err := CompileFile(src, dest, CompileOptions{DTS: true}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "out", "Card.d.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "title: string;") {
		t.Errorf("Card.d.ts:\n%s", data)
	}
	if _, err := CompileFile(src, filepath.Join(dir, "plain", "Card.js"), CompileOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "plain", "Card.d.ts")); err == nil {
		t.Error("Card.d.ts written without --dts")
	}

	// The service answers the declarations with the dts option.
	yes := true
	service, err := NewCompilerService("", nil)
	if err != nil {
		t.Fatal(err)
	}
	res := service.Compile(ServiceRequest{Path: src, Options: FileOptions{DTS: &yes}})
	if !res.OK || !strings.Contains(res.DTS, "title: string;") {
		b, _ := json.Marshal(res)
		t.Errorf("service result: %s", b)
	}
}
