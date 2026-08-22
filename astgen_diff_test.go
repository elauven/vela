package main

import (
	"strings"
	"testing"
)

// astgen_diff_test.go retains the historical migration fixtures as AST-only
// validity coverage. The legacy string generator is no longer an oracle.

var diffFixtures = map[string]string{
	"minimal": `<script>
  export let title = '';
</script>
<template>
  <p>{title}</p>
</template>`,

	"static": `<script></script>
<template>
  <div class="card">
    <h1>Hello</h1>
    <p>static content</p>
  </div>
</template>`,

	"counter": `<script>
  let count = $state(0);
  function inc() { count++; }
</script>
<template>
  <button @click={inc}>{count}</button>
</template>`,

	"conditional": `<script>
  let show = $state(true);
</script>
<template>
  {#if show}
    <p>visible</p>
  {/if}
</template>`,

	"interp": `<script></script>
<template>
  <p>sum is {1 + 2} and product is {3 * 4}</p>
</template>`,

	"script": `<script>
  import { fmt } from './util.js';
  const LABEL = 'total';
  function pad(s) { return ' ' + s + ' '; }
</script>
<template>
  <p>{pad(LABEL)} = {fmt(99)}</p>
</template>`,

	"derived": `<script>
  let count = $state(1);
  let doubled = $derived(count * 2);
  $effect(() => { document.title = 'n=' + count; });
</script>
<template>
  <p>{count} and {doubled}</p>
</template>`,

	"binding": `<script>
  let name = $state('');
</script>
<template>
  <input bind:value={name} />
</template>`,

	"directives": `<script>
  let active = $state(false);
  let size = $state(14);
</script>
<template>
  <div class="card" class:active={active} style:font-size="{size}px">content</div>
</template>`,

	"each": `<script>
  let items = $state(['a', 'b', 'c']);
</script>
<template>
  {#each items as item}
    <li>{item}</li>
  {/each}
</template>`,

	"each_keyed": `<script>
  let rows = $state([{ id: 1, name: 'x' }]);
</script>
<template>
  {#each rows as row (row.id)}
    <li>{row.name}</li>
  {/each}
</template>`,

	"keyblock": `<script>
  let userId = $state(1);
</script>
<template>
  {#key userId}
    <p>static profile</p>
  {/key}
</template>`,

	"await": `<script>
  let data = $state(Promise.resolve(1));
</script>
<template>
  {#await data}
    <p>loading</p>
  {:then result}
    <p>done</p>
  {:catch err}
    <p>failed</p>
  {/await}
</template>`,

	"slots": `<script></script>
<template>
  <div class="card">
    <slot />
    <slot name="footer" />
  </div>
</template>`,

	"component": `<script>
  import { Card } from './Card.vela';
</script>
<template>
  <div>
    <Card>
      <p>body content</p>
      <span slot="footer">footer content</span>
    </Card>
  </div>
</template>`,

	"deepstate": `<script>
  let items = $state.deep([{ name: 'a', count: 1 }]);
  let total = $derived(items.length);
</script>
<template>
  <p>{items.length} items, total {total}</p>
</template>`,

	"lifecycle": `<script>
  import { onMount, onDestroy } from 'vela';
  let n = $state(0);
  onMount(() => {
    const id = setInterval(() => { n++; }, 1000);
    return () => clearInterval(id);
  });
  onDestroy(() => { console.log('bye'); });
</script>
<template>
  <p>ticks: {n}</p>
</template>`,

	"bindable": `<script>
  export let value = $bindable('');
  let touched = $state(false);
</script>
<template>
  <input bind:value={value} />
</template>`,

	"scopedstyle": `<script>
  let n = $state(0);
</script>
<template>
  <p>{n}</p>
</template>
<style>
  p { color: blue; font-weight: bold; }
</style>`,
}

// compileASTFixture compiles a fixture through the sole supported generator.
func compileASTFixture(t *testing.T, src string) string {
	t.Helper()
	sfc, err := parseSFC(src)
	if err != nil {
		t.Fatalf("parseSFC: %v", err)
	}
	tmpl, err := parseTemplate(sfc.Template)
	if err != nil {
		t.Fatalf("parseTemplate: %v", err)
	}
	return generateCodeAST("Test", sfc, tmpl, analyzeScript(sfc.Script), "vela-000000")
}

func TestASTGenFixtureValidity(t *testing.T) {
	for name, src := range diffFixtures {
		t.Run(name, func(t *testing.T) {
			ast := compileASTFixture(t, src)
			prog := ParseJS(stripBanner(ast))
			if len(prog.Body) == 0 {
				t.Fatalf("AST output did not parse to any statements:\n%s", ast)
			}
			if issues := ValidateProgram(prog); len(issues) > 0 {
				t.Fatalf("AST output failed validation: %v", issues)
			}
			for _, fragment := range []string{
				"export function Test(",
				"$$props = Object.assign({}, $$props)",
				"$$slots = $$slots || {}",
				"return {",
				"destroy()",
			} {
				if !strings.Contains(ast, fragment) {
					t.Errorf("output missing %q:\n%s", fragment, ast)
				}
			}
		})
	}
}
