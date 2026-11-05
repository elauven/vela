package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TypeScript twins (task 081, docs/TYPESCRIPT.md TS-4): every component
// written with <script lang="ts"> compiles to the module its JavaScript twin
// compiles to. The twins cover the rows of FEATURES.md "Component format and
// script" and "Template and component features". Only the site table of
// development builds may differ: it holds the source columns of the sites.

type tsTwin struct {
	name   string
	ts, js string
}

var tsTwins = []tsTwin{
	{"props and bindable", `<script lang="ts">
  export let title: string = 'Untitled';
  export let count: number;
  export let items: Array<{ id: number; label?: string }> = [];
  export let value: string = $bindable('');
  export let other = $bindable<number>(0);
</script>
<template><p>{title} {count} {items.length} {value} {other}</p></template>`, `<script>
  export let title = 'Untitled';
  export let count;
  export let items = [];
  export let value = $bindable('');
  export let other = $bindable(0);
</script>
<template><p>{title} {count} {items.length} {value} {other}</p></template>`},

	{"runes with type arguments", `<script lang="ts">
  interface Item { id: number; done: boolean }
  let count = $state<number>(0);
  let name = $state<string | null>(null);
  let missing = $state<number>();
  let todos = $state.deep<Item[]>([]);
  let doubled = $derived<number>(count * 2);
  let open = $derived(todos.filter((t: Item): boolean => !t.done).length);
  function add(id: number): void {
    todos.push({ id, done: false });
    count++;
    name = 'n' + id;
    missing = id;
  }
</script>
<template><button @click={() => add(count)}>{doubled} {open} {name} {missing}</button></template>`, `<script>
  let count = $state(0);
  let name = $state(null);
  let missing = $state(undefined);
  let todos = $state.deep([]);
  let doubled = $derived(count * 2);
  let open = $derived(todos.filter((t) => !t.done).length);
  function add(id) {
    todos.push({ id, done: false });
    count++;
    name = 'n' + id;
    missing = id;
  }
</script>
<template><button @click={() => add(count)}>{doubled} {open} {name} {missing}</button></template>`},

	{"effects and lifecycle", `<script lang="ts">
  import { onMount, onDestroy } from 'vela';
  let ticks = $state<number>(0);
  let timer: ReturnType<typeof setInterval> | undefined;
  $effect((): (() => void) => {
    const t: number = ticks;
    const id = setTimeout((): void => console.log(t), 10);
    return (): void => clearTimeout(id);
  });
  onMount((): void => {
    timer = setInterval(() => ticks++, 1000);
  });
  onDestroy(() => { clearInterval(timer!) });
</script>
<template><p>{ticks}</p></template>`, `<script>
  import { onMount, onDestroy } from 'vela';
  let ticks = $state(0);
  let timer;
  $effect(() => {
    const t = ticks;
    const id = setTimeout(() => console.log(t), 10);
    return () => clearTimeout(id);
  });
  onMount(() => {
    timer = setInterval(() => ticks++, 1000);
  });
  onDestroy(() => { clearInterval(timer) });
</script>
<template><p>{ticks}</p></template>`},

	{"script declarations", `<script lang="ts">
  type Mode = 'list' | 'grid';
  interface Shape { area(): number }
  declare const DEBUG: boolean;
  namespace Types { export type Id = string }
  interface $$Events { select: { id: Types.Id } }
  interface $$Slots { default: {}; actions: {} }

  abstract class Base<T> implements Shape {
    protected abstract readonly kind: string;
    declare tag: T;
    private static count?: number;
    constructor(public_: number) {}
    abstract area(): number;
    describe(this: Base<T>, prefix?: string): string { return (prefix ?? '') + this.area() }
  }
  class Square extends Base<number> {
    protected readonly kind = 'square';
    side!: number;
    override area(): number { return this.side ** 2 }
  }

  function pick(a: string): string;
  function pick(a: number): number;
  function pick(a: any): any { return a }

  const sizes = { s: 1, m: 2 } satisfies Record<string, number>;
  const modes = ['list', 'grid'] as const;
  let mode = $state<Mode>(modes[0] as Mode);
  const sq = new Square(1);
  const first = <T,>(xs: readonly T[]): T | undefined => xs[0];
  function select(id: Types.Id) { $$dispatch('select', { id }) }
</script>
<template><p @click={() => select(String(pick(1)))}>{mode} {sizes.s} {first(modes)} {sq.area()}</p></template>`, `<script>
  abstract_class_placeholder
</script>`},

	{"imports", `<script lang="ts">
  import Card from './Card.vela';
  import type { Props } from './Card.vela';
  import { format, type Fmt } from './util.ts';
  import type * as Types from './types.ts';
  import { type Item } from './types.ts';
  const fmt: Fmt = { upper: true };
  let item: Item | null = null;
  let props: Props = {} as Props;
  let t: Types.Any;
</script>
<template><Card :title={format('a', fmt)} /></template>`, `<script>
  import Card from './Card.vela';
  import { format } from './util.ts';
  import './types.ts';
  const fmt = { upper: true };
  let item = null;
  let props = {};
  let t;
</script>
<template><Card :title={format('a', fmt)} /></template>`},

	{"template expressions", `<script lang="ts">
  let name = $state<string | null>('a');
  let url = $state<unknown>('/x');
  let user = $state<{ profile?: { name: string } }>({});
  function handle(e: MouseEvent, n: number): void { name = String(n) + e.type }
  function keyed<T>(x: T): T { return x }
</script>
<template>
  <p :title={name!} :data-n={keyed<number>(1)}>{name as string} {(url as string).length} {user.profile!.name}</p>
  <a :href={url as string} @click={(e: MouseEvent) => handle(e, 1)} @mouseover={(e: MouseEvent): void => handle(e, 2 satisfies number)}>x</a>
</template>`, `<script>
  let name = $state('a');
  let url = $state('/x');
  let user = $state({});
  function handle(e, n) { name = String(n) + e.type }
  function keyed(x) { return x }
</script>
<template>
  <p :title={name} :data-n={keyed(1)}>{name} {(url).length} {user.profile.name}</p>
  <a :href={url} @click={(e) => handle(e, 1)} @mouseover={(e) => handle(e, 2)}>x</a>
</template>`},

	{"bindings", `<script lang="ts">
  let text = $state<string>('');
  let done = $state<boolean>(false);
  let input: HTMLInputElement | undefined = $state();
  function focus(): void { input?.focus() }
</script>
<template>
  <input bind:value={text} bind:this={input} />
  <input type="checkbox" bind:checked={done} />
  <button @click={focus}>{text} {done}</button>
</template>`, `<script>
  let text = $state('');
  let done = $state(false);
  let input = $state(undefined);
  function focus() { input?.focus() }
</script>
<template>
  <input bind:value={text} bind:this={input} />
  <input type="checkbox" bind:checked={done} />
  <button @click={focus}>{text} {done}</button>
</template>`},

	{"control flow", `<script lang="ts">
  interface Row { id: number; label: string }
  let rows = $state<Row[]>([]);
  let open = $state<boolean>(true);
  let userId = $state<number>(1);
  const load = async <T,>(v: T): Promise<T> => v;
  let request = $state<Promise<number>>(load<number>(1));
</script>
<template>
  {#if open as boolean}
    <p>open</p>
  {:else if (rows.length as number) > 0}
    <p>rows</p>
  {:else}
    <p>none</p>
  {/if}
  {#each rows.filter((r: Row) => r.id > 0) as row, i (row.id as number)}
    <p>{i}: {row.label!}</p>
  {:else}
    <p>empty</p>
  {/each}
  {#key userId as number}<p>{userId}</p>{/key}
  {#await request}
    <p>loading</p>
  {:then value}
    <p>{(value as number).toFixed(1)}</p>
  {:catch err}
    <p>{(err as Error).message}</p>
  {/await}
  {#await load<string>('x') then v}<p>{v}</p>{/await}
</template>`, `<script>
  let rows = $state([]);
  let open = $state(true);
  let userId = $state(1);
  const load = async (v) => v;
  let request = $state(load(1));
</script>
<template>
  {#if open}
    <p>open</p>
  {:else if (rows.length) > 0}
    <p>rows</p>
  {:else}
    <p>none</p>
  {/if}
  {#each rows.filter((r) => r.id > 0) as row, i (row.id)}
    <p>{i}: {row.label}</p>
  {:else}
    <p>empty</p>
  {/each}
  {#key userId}<p>{userId}</p>{/key}
  {#await request}
    <p>loading</p>
  {:then value}
    <p>{(value).toFixed(1)}</p>
  {:catch err}
    <p>{(err).message}</p>
  {/await}
  {#await load('x') then v}<p>{v}</p>{/await}
</template>`},

	{"components, events and slots", `<script lang="ts">
  import Card from './Card.vela';
  import Counter from './Counter.vela';
  let count = $state<number>(0);
  let title = $state<string>('t');
  function close(e: CustomEvent<string>): void { title = e.detail }
</script>
<template>
  <Card :title={title as string} @on:close={close} @on:open={(e: CustomEvent<number>) => count = e.detail}>
    <p>{count as number}</p>
    <svelte:fragment slot="actions"><button @click={(): void => { count++ }}>+</button></svelte:fragment>
    <slot name="extra" />
  </Card>
  <Counter bind:count={count} />
</template>`, `<script>
  import Card from './Card.vela';
  import Counter from './Counter.vela';
  let count = $state(0);
  let title = $state('t');
  function close(e) { title = e.detail }
</script>
<template>
  <Card :title={title} @on:close={close} @on:open={(e) => count = e.detail}>
    <p>{count}</p>
    <svelte:fragment slot="actions"><button @click={() => { count++ }}>+</button></svelte:fragment>
    <slot name="extra" />
  </Card>
  <Counter bind:count={count} />
</template>`},

	{"dynamic and lazy components", `<script lang="ts">
  import { lazy } from 'vela';
  import List from './List.vela';
  import Grid from './Grid.vela';
  const views = { list: List, grid: Grid } as const;
  let mode = $state<keyof typeof views>('list');
  const Settings = lazy(() => import('./Settings.vela'));
  let rows = $state<number[]>([]);
</script>
<template>
  <vela:component :this={views[mode as keyof typeof views]} :rows={rows as number[]} />
  <vela:component :this={Settings} />
</template>`, `<script>
  import { lazy } from 'vela';
  import List from './List.vela';
  import Grid from './Grid.vela';
  const views = { list: List, grid: Grid };
  let mode = $state('list');
  const Settings = lazy(() => import('./Settings.vela'));
  let rows = $state([]);
</script>
<template>
  <vela:component :this={views[mode]} :rows={rows} />
  <vela:component :this={Settings} />
</template>`},

	{"class and style directives", `<script lang="ts">
  let active = $state<boolean>(false);
  let color = $state<string | undefined>('red');
</script>
<template>
  <p class="a" :class={active ? 'on' as const : 'off'} class:active={active as boolean} style:color={color!} style:--accent={color ?? ('blue' satisfies string)}>x</p>
</template>`, `<script>
  let active = $state(false);
  let color = $state('red');
</script>
<template>
  <p class="a" :class={active ? 'on' : 'off'} class:active={active} style:color={color} style:--accent={color ?? ('blue')}>x</p>
</template>`},

	{"generic component", `<script lang="ts" generics="T extends { id: string }, K = string">
  export let items: T[] = [];
  export let key: K | undefined = undefined;
  let selected = $state<T | null>(null);
  function choose(item: T): void { selected = item }
</script>
<template>{#each items as item (item.id)}<button @click={() => choose(item)}>{item.id} {String(key)}</button>{/each}{selected?.id}</template>`, `<script>
  export let items = [];
  export let key = undefined;
  let selected = $state(null);
  function choose(item) { selected = item }
</script>
<template>{#each items as item (item.id)}<button @click={() => choose(item)}>{item.id} {String(key)}</button>{/each}{selected?.id}</template>`},

	{"client-only TypeScript component", `<script lang="ts" context="client">
  let width = $state<number>(window.innerWidth);
</script>
<template><p>{width as number}</p></template>`, `<script context="client">
  let width = $state(window.innerWidth);
</script>
<template><p>{width}</p></template>`},

	{"style section", `<style>p { color: red }</style>
<script lang="typescript">
  let n: number = 1;
</script>
<template><p>{n}</p></template>`, `<style>p { color: red }</style>
<script lang="js">
  let n = 1;
</script>
<template><p>{n}</p></template>`},
}

func init() {
	// The JavaScript twin of "script declarations" is long; it is written
	// here so the table above reads as pairs.
	for i := range tsTwins {
		if tsTwins[i].name == "script declarations" {
			tsTwins[i].js = `<script>
  class Base {
    static count;
    constructor(public_) {}
    describe(prefix) { return (prefix ?? '') + this.area() }
  }
  class Square extends Base {
    kind = 'square';
    side;
    area() { return this.side ** 2 }
  }

  function pick(a) { return a }

  const sizes = { s: 1, m: 2 };
  const modes = ['list', 'grid'];
  let mode = $state(modes[0]);
  const sq = new Square(1);
  const first = (xs) => xs[0];
  function select(id) { $$dispatch('select', { id }) }
</script>
<template><p @click={() => select(String(pick(1)))}>{mode} {sizes.s} {first(modes)} {sq.area()}</p></template>`
		}
	}
}

// siteTable is the development site table, which holds source columns.
var siteTable = regexp.MustCompile(`(?m)^const \$\$sites = .*;$`)

// compileTwin compiles a component written to dir/Twin.vela.
func compileTwin(t *testing.T, dir, source string, opts CompileOptions) (string, []*Diagnostic) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "Twin.vela")
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "out", "Twin.js")
	diags, err := CompileFile(src, dest, opts)
	if err != nil {
		for _, d := range diags {
			t.Logf("  %s", d)
		}
		t.Fatalf("compile %s: %v", src, err)
	}
	out, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	return siteTable.ReplaceAllString(string(out), "const $$$$sites = …;"), diags
}

func TestTypeScriptTwinsCompileAlike(t *testing.T) {
	for _, tw := range tsTwins {
		t.Run(tw.name, func(t *testing.T) {
			for _, mode := range []struct {
				name string
				opts CompileOptions
			}{
				{"development", CompileOptions{}},
				{"minified", CompileOptions{Minify: true}},
				{"bundler", CompileOptions{SourceComponentImports: true, BareRuntimeImports: true}},
			} {
				// The same path for both: the CSS scope hashes the path.
				dir := t.TempDir()
				gotTS, tsDiags := compileTwin(t, dir, tw.ts, mode.opts)
				gotJS, jsDiags := compileTwin(t, dir, tw.js, mode.opts)
				if gotTS != gotJS {
					t.Errorf("%s: the TypeScript component compiles differently\n%s", mode.name, firstDifference(gotTS, gotJS))
				}
				if codes(tsDiags) != codes(jsDiags) {
					t.Errorf("%s: diagnostics differ\n  ts: %s\n  js: %s", mode.name, codes(tsDiags), codes(jsDiags))
				}
			}
		})
	}
}

func codes(diags []*Diagnostic) string {
	var out []string
	for _, d := range diags {
		out = append(out, d.Code)
	}
	return strings.Join(out, ",")
}

// firstDifference shows the first line where two outputs differ.
func firstDifference(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < len(al) || i < len(bl); i++ {
		var x, y string
		if i < len(al) {
			x = al[i]
		}
		if i < len(bl) {
			y = bl[i]
		}
		if x != y {
			return "  line " + itoa(i+1) + "\n  ts: " + x + "\n  js: " + y
		}
	}
	return ""
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
