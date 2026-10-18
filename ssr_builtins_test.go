package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSSRMutationRules: methods and member assignments that change a value
// are admitted only on values the function created (T-6.6b); a callback
// prop that writes state builds and fails only if the render calls it.
func TestSSRMutationRules(t *testing.T) {
	src := writeSSRModuleFixture(t, map[string]string{
		"Rules.vela": `<script>
  export let items = [1, 2];
  export let box = { n: 1 };
  let picked = $state(0);
  function addParam(xs) { xs.push(1); return xs.length; }
  function aliased() { const a = []; const b = a; b.push(1); a.push(2); return a.length; }
  function setParam(o) { o.n = 2; return o.n; }
  function fine() { const a = []; a.push(1, 2); const o = { n: 0 }; o.n = a.length; return o.n; }
  function select(id) { picked = id; items.splice(0, 1); }
</script>
<template><p>{addParam(items)} {aliased()} {setParam(box)} {fine()} {typeof select}</p><button @click={() => picked = 1}>x</button></template>
`,
	})
	out := filepath.Join(filepath.Dir(src), "out")
	report := filepath.Join(out, "report.json")

	opts := CompileOptions{StatusWriter: io.Discard, SSRModule: "example.com/rules/server", SSRReport: report}
	buildSSRProject(collectVela(src), src, out, opts, false)

	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`xs.push changes \"xs\"; during server rendering it is admitted only on an array the function created and does not copy`,
		`b.push changes \"b\"`,
		`assignment to a member of \"o\" changes a value the function did not create`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("report lacks %s", want)
		}
	}
	if strings.Contains(text, `select`) {
		t.Errorf("a state-writing function used as a value must build")
	}
	if strings.Contains(text, `"name": "fine"`) && strings.Contains(text, `fine has no`) {
		t.Errorf("fine() must translate")
	}
}

// TestSSRArrayIteratorsOnlyWhenIterated: xs.entries(), keys() and values()
// translate where the iterator is iterated at once (for…of, spread,
// Array.from); any other use of the iterator object stays rejected.
func TestSSRArrayIteratorsOnlyWhenIterated(t *testing.T) {
	src := writeSSRModuleFixture(t, map[string]string{
		"Iter.vela": `<script>
  export let items = [1, 2];
  function size(xs) { const it = xs.keys(); return it; }
  function count(xs) { return xs.entries().length; }
  function pairs(xs) { let n = 0; for (const [i] of xs.entries()) n += i; return [...xs.values(), n]; }
</script>
<template><p>{size(items)} {count(items)} {pairs(items)}</p></template>
`,
	})
	out := filepath.Join(filepath.Dir(src), "out")
	report := filepath.Join(out, "report.json")
	opts := CompileOptions{StatusWriter: io.Discard, SSRModule: "example.com/iter/server", SSRReport: report}
	buildSSRProject(collectVela(src), src, out, opts, false)
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{`size has no server-native translation: method value.keys is not available`, `count has no server-native translation: method value.entries is not available`} {
		if !strings.Contains(text, want) {
			t.Errorf("report lacks %s", want)
		}
	}
	if strings.Contains(text, `pairs has no`) {
		t.Errorf("pairs() must translate:\n%s", text)
	}
}
