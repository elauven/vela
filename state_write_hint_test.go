package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stateWriteHints compiles a component and returns the names reported by the
// "$state variable … is never updated" hint.
func stateWriteHints(t *testing.T, source string) map[string]bool {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "App.vela")
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	diags, err := CompileFile(src, filepath.Join(dir, "App.js"), CompileOptions{})
	if err != nil {
		t.Fatalf("compile failed: %v\n%v", err, diags)
	}
	hinted := map[string]bool{}
	for _, d := range diags {
		if d.Sev != SevHint || !strings.Contains(d.Message, "is never updated") {
			continue
		}
		start := strings.Index(d.Message, `"`)
		end := strings.Index(d.Message[start+1:], `"`)
		hinted[d.Message[start+1:start+1+end]] = true
	}
	return hinted
}

func TestStateWriteHintIgnoresWrittenBindings(t *testing.T) {
	hinted := stateWriteHints(t, `<script>
  let viaHelper = $state('all');
  let viaHandler = $state(0);
  let viaCompound = $state(0);
  let viaDestructure = $state(0);
  let viaEffect = $state(0);
  let viaInline = $state(0);
  let viaBindValue = $state('');
  let viaBindChecked = $state(false);
  let viaBindThis = $state(null);
  function setFilter(v) { viaHelper = v; }
  function inc() { viaHandler++; viaCompound += 2; }
  function swap(pair) { [viaDestructure] = pair; }
  $effect(() => { viaEffect = viaHandler * 2; });
</script>
<template>
  <button @click={inc}>{viaHelper}{viaHandler}{viaCompound}{viaDestructure}{viaEffect}</button>
  <button @click={() => setFilter('done')}>filter</button>
  <button @click={() => swap([1])}>swap</button>
  <button @click={() => viaInline = viaInline + 1}>{viaInline}</button>
  <input bind:value={viaBindValue} />
  <input type="checkbox" bind:checked={viaBindChecked} />
  <div bind:this={viaBindThis}></div>
</template>
`)
	if len(hinted) != 0 {
		t.Fatalf("written bindings must not be hinted, got %v", hinted)
	}
}

func TestStateWriteHintReportsUnwrittenBinding(t *testing.T) {
	hinted := stateWriteHints(t, `<script>
  let constant = $state(1);
  let shadowed = $state(2);
  let list = $state.deep([]);
  function local() { let shadowed = 0; shadowed++; return shadowed; }
</script>
<template>
  <p>{constant}{shadowed}{list.length}{local()}</p>
</template>
`)
	if !hinted["constant"] {
		t.Fatalf("expected a hint for the unwritten binding, got %v", hinted)
	}
	if !hinted["shadowed"] {
		t.Fatalf("a write to a shadowing local must not count, got %v", hinted)
	}
	if hinted["list"] {
		t.Fatalf("$state.deep bindings are not hinted, got %v", hinted)
	}
}
