package ssr

import (
	"errors"
	"strings"
	"testing"

	"github.com/elauven/vela/internal/analyzer"
	"github.com/elauven/vela/internal/sfcast"
)

func regionForSource(t *testing.T, source string) (*SSRRegion, error) {
	t.Helper()
	sfc, err := sfcast.ParseSFC(source)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlanWithAnalysis("Page", sfc, analyzer.AnalyzeScriptAST(sfc.ScriptAST), "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	_, region, err := buildSSRGoRendererWithRegion(plan, "components", "Page.vela", source)
	region.locate(source)
	return region, err
}

// TestSSRRegionReportsEveryFailureWithReasonChain covers D-10.3a/b and
// §10.1: every failed site of a component is reported in one build, a cause
// shared by several sites appears once with its other sites, and each
// diagnostic carries a position, a reason chain, and a fix.
func TestSSRRegionReportsEveryFailureWithReasonChain(t *testing.T) {
	source := `<script>
  export let text = '';
  export let items = [];
  let paras = $derived(text.localeCompare('NFC'));
</script>
<template>
  <section>
    <p>{items.localeCompare()}</p>
    <ul>{#each paras as para}<li>{para}</li>{/each}</ul>
    <ol>{#each paras as para}<li>{para}</li>{/each}</ol>
    <b>{text}</b>
  </section>
</template>`
	region, err := regionForSource(t, source)
	var failure *SSRRegionError
	if !errors.As(err, &failure) {
		t.Fatalf("error = %T %v, want *SSRRegionError", err, err)
	}
	if lines := strings.Split(err.Error(), "\n"); len(lines) != 2 {
		t.Fatalf("one-line errors = %q, want the map call and the shared $derived cause", lines)
	}
	detail := FormatSSRRegionError("Page.vela", failure)
	for _, want := range []string{
		"Page.vela:8:9: [error] VELA-SSR-EXPR",
		"reason: template text",
		"reason: {#each} iterable → $derived \"paras\"",
		"Page.vela:4:24: [error] VELA-SSR-EXPR",
		"also at Page.vela:10:16 ({#each} iterable)",
		"fix: ",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("diagnostic misses %q:\n%s", want, detail)
		}
	}
	native, unsupported := 0, 0
	for _, record := range region.Records {
		switch record.Outcome {
		case SSROutcomeNative:
			native++
		case SSROutcomeUnsupported:
			unsupported++
		}
	}
	// text, and both {#each} rows' {para} plus <b>{text}</b> are native; the
	// map call, both iterables, and the $derived declaration are not.
	if native == 0 || unsupported != 4 {
		t.Fatalf("records: %d native, %d unsupported, want 4 unsupported: %+v", native, unsupported, region.Records)
	}
}

// TestSSRRegionExcludesEventHandlersAndEffects covers T-2.2: handlers,
// effects, lifecycle hooks, and binding write-back are never translated,
// so constructs the server cannot run produce no site and no diagnostic.
func TestSSRRegionExcludesEventHandlersAndEffects(t *testing.T) {
	source := `<script>
  import { onMount } from 'vela';
  export let label = 'go';
  let count = $state(0);
  let input = $state('');
  function load() { return fetch('/api').then(r => r.json()); }
  $effect(() => { document.title = label + count; });
  onMount(() => { load().then(data => { count = data.length; }); });
</script>
<template>
  <div>
    <button @click={() => { count++; load(); }}>{label}</button>
    <input bind:value={input} />
  </div>
</template>`
	region, err := regionForSource(t, source)
	if err != nil {
		t.Fatalf("handlers and effects must not block SSR: %v", err)
	}
	for _, record := range region.Records {
		if record.Required && record.Outcome != SSROutcomeNative {
			t.Fatalf("unexpected failed site %+v", record)
		}
		if strings.Contains(strings.Join(record.Reads, ","), "load") {
			t.Fatalf("a handler-only function entered the region: %+v", record)
		}
	}
}

func TestSSRRegionReportsTemplateWritesAsImpure(t *testing.T) {
	_, err := regionForSource(t, `<script>let count = $state(0);</script>
<template><p>{count++}</p><p>{count = 2}</p></template>`)
	if err == nil || strings.Count(err.Error(), SSRImpureDiagnosticCode) != 2 {
		t.Fatalf("error = %v, want two %s diagnostics", err, SSRImpureDiagnosticCode)
	}
}
