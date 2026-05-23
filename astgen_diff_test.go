package main

// astgen_diff_test.go — the differential migration harness.
//
// Phase C builds the AST generator in parallel with the legacy
// string generator.  This harness compiles representative fixtures
// with BOTH and compares the output.
//
// While the migration is in progress the two outputs differ — the
// AST generator only emits the blocks migrated so far.  The harness
// therefore does NOT fail on difference; instead it reports a
// similarity metric so progress is visible.  When the metric
// reaches 100% across all fixtures, TestASTGenMatchesLegacy is
// switched from reporting to asserting, and the AST generator
// becomes the default.
//
// What the harness gives us:
//   - TestASTGenSkeleton — asserts the block-1 skeleton is present
//     and structurally sound (this DOES hard-fail).
//   - TestASTGenDiffReport — prints a per-fixture similarity score.
//     Informational; never fails. The shrinking diff is the
//     migration progress bar.

import (
	"fmt"
	"strings"
	"testing"
)

// diffFixtures — small, representative .vela sources spanning the
// feature surface.  Kept inline so the harness has no file
// dependencies.
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

// knownDiff lists diff-harness fixtures where the AST generator
// intentionally diverges from legacy — documented, valid
// alternatives, not bugs.  Excluded from structural equality;
// still exercised for validity by the corpus harness.
var knownDiff = map[string]string{
	"slots": "multiple sibling <slot>s — document-order vs " +
		"create/wire-buffer ordering; same DOM result",
	"component": "component slot children — struct-built slot-factory " +
		"closures (E3.6) vs legacy's buffer-partitioned emission; " +
		"both valid, same runtime behaviour",
}

// compileBothWays runs a .vela source through the legacy and the
// AST generators, returning both outputs.
func compileBothWays(t *testing.T, src string) (legacy, ast string) {
	t.Helper()
	sfc, err := parseSFC(src)
	if err != nil {
		t.Fatalf("parseSFC: %v", err)
	}
	tmpl, err := parseTemplate(sfc.Template)
	if err != nil {
		t.Fatalf("parseTemplate: %v", err)
	}
	analysis := analyzeScript(sfc.Script)
	const scope = "vela-000000"
	legacy = generateCode("Test", sfc, tmpl, analysis, scope)
	ast = generateCodeAST("Test", sfc, tmpl, analysis, scope)
	return legacy, ast
}

// ─────────────────────────────────────────────
//  Block 1 — skeleton assertions (hard-fail)
// ─────────────────────────────────────────────

func TestASTGenSkeleton(t *testing.T) {
	for name, src := range diffFixtures {
		sfc, _ := parseSFC(src)
		analysis := analyzeScript(sfc.Script)
		_, ast := compileBothWays(t, src)

		// The AST generator's output must always be valid,
		// re-parseable JS — even when incomplete.
		prog := ParseJS(stripBanner(ast))
		if len(prog.Body) == 0 {
			t.Errorf("[%s] AST output did not parse to any statements:\n%s", name, ast)
			continue
		}

		// Skeleton invariants — these hold from block 1 onward.
		mustContain := []string{
			"export function Test(",
			"$$props = Object.assign({}, $$props)",
			"$$slots = $$slots || {}",
			"function $$dispatch(",
			"function $$insertHTML(",
			"return {",
			"destroy()",
		}
		for _, frag := range mustContain {
			if !strings.Contains(ast, frag) {
				t.Errorf("[%s] skeleton missing %q in:\n%s", name, frag, ast)
			}
		}

		// set() appears only when the component receives props.
		hasProps := len(analysis.Props) > 0
		hasSet := strings.Contains(ast, "set(props)")
		if hasProps && !hasSet {
			t.Errorf("[%s] component has props but no set() method:\n%s", name, ast)
		}
	}
}

// ─────────────────────────────────────────────
//  Diff report (informational, never fails)
// ─────────────────────────────────────────────

func TestASTGenDiffReport(t *testing.T) {
	fmt.Println("\n=== AST generator migration progress ===")
	var total float64
	native := 0
	for name, src := range diffFixtures {
		sfc, _ := parseSFC(src)
		tmpl, _ := parseTemplate(sfc.Template)
		analysis := analyzeScript(sfc.Script)
		claimed := astCanBuildDirectly(tmpl, analysis)
		if claimed {
			native++
		}

		legacy, ast := compileBothWays(t, src)
		sim := similarity(legacy, ast)
		total += sim

		tag := "fallback→legacy"
		if claimed {
			tag = "AST-native"
		}
		fmt.Printf("  %-14s  %5.1f%%  %-16s  (legacy %d / ast %d lines)\n",
			name, sim*100, tag,
			countLines(legacy), countLines(ast))
	}
	fmt.Printf("  %-14s  %5.1f%%  %d/%d AST-native\n", "SUMMARY",
		total/float64(len(diffFixtures))*100, native, len(diffFixtures))
	fmt.Println("========================================")
}

// TestASTGenMatchesLegacy is the eventual hard gate.  It compares
// the two generators by RE-PARSED AST structural equality, not by
// text — an AST-based generator is expressly allowed to format
// differently (indentation, else-placement, blank lines).  What
// must match is the *meaning*: parse both outputs, compare the
// trees.
//
// While the migration is incomplete this only checks fixtures the
// AST generator claims (astCanBuildDirectly); once all blocks land it
// covers everything and the claim check is removed.
func TestASTGenMatchesLegacy(t *testing.T) {
	for name, src := range diffFixtures {
		sfc, err := parseSFC(src)
		if err != nil {
			t.Fatalf("[%s] parseSFC: %v", name, err)
		}
		tmpl, err := parseTemplate(sfc.Template)
		if err != nil {
			t.Fatalf("[%s] parseTemplate: %v", name, err)
		}
		analysis := analyzeScript(sfc.Script)

		// Skip fixtures the AST generator doesn't yet claim.
		if !astCanBuildDirectly(tmpl, analysis) {
			continue
		}
		// Skip fixtures where the AST generator intentionally
		// diverges from legacy (documented, valid alternatives).
		if _, isDiff := knownDiff[name]; isDiff {
			continue
		}

		const scope = "vela-000000"
		legacy := generateCode("Test", sfc, tmpl, analysis, scope)
		ast := generateCodeAST("Test", sfc, tmpl, analysis, scope)

		// Strip the legacy banner comment (printer emits none),
		// then compare re-parsed trees.
		legacyTree := ParseJS(stripBanner(legacy))
		astTree := ParseJS(ast)

		if nodeSig(legacyTree) != nodeSig(astTree) {
			t.Errorf("[%s] generators diverge structurally\n--- legacy ---\n%s\n--- ast ---\n%s",
				name, legacy, ast)
		}
	}
}

// stripBanner removes the leading `// Generated…` comment line so
// it doesn't interfere with re-parsing for comparison.
// ─────────────────────────────────────────────
//  Helpers
// ─────────────────────────────────────────────

// similarity returns a 0..1 score — the fraction of legacy lines
// that also appear (trimmed) in the AST output.  Crude, but a
// usable migration progress signal: as blocks land, more legacy
// lines find a match.
func similarity(legacy, ast string) float64 {
	astLines := map[string]bool{}
	for _, l := range strings.Split(ast, "\n") {
		astLines[strings.TrimSpace(l)] = true
	}
	legLines := strings.Split(legacy, "\n")
	if len(legLines) == 0 {
		return 1
	}
	matched := 0
	nonEmpty := 0
	for _, l := range legLines {
		tl := strings.TrimSpace(l)
		if tl == "" {
			continue
		}
		nonEmpty++
		if astLines[tl] {
			matched++
		}
	}
	if nonEmpty == 0 {
		return 1
	}
	return float64(matched) / float64(nonEmpty)
}

func countLines(s string) int {
	n := 0
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}
