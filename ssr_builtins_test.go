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
	BuildSSRProject(CollectVela(src), src, out, opts, false)

	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`xs.push changes \"xs\"; during server rendering it is admitted only on an array the function created (T-6.6b)`,
		`assignment to a member of \"o\" changes a value the function did not create`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("report lacks %s", want)
		}
	}
	// An array copied into another variable is boxed: both see the change.
	if strings.Contains(text, `aliased has no`) {
		t.Errorf("aliased() must translate")
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
	BuildSSRProject(CollectVela(src), src, out, opts, false)
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

// TestSSRDateTimeZoneDependentStaysRejected: new Date(), local-time
// construction and getters, and setters on a date the function did not
// create keep their diagnostics (T-6.6e).
func TestSSRDateTimeZoneDependentStaysRejected(t *testing.T) {
	src := writeSSRModuleFixture(t, map[string]string{
		"When.vela": `<script>
  export let ms = 0;
  function now() { return new Date().getTime(); }
  function local(y) { return new Date(y, 0).getTime(); }
  function year(t) { return new Date(t).getFullYear(); }
  function shift(d) { d.setUTCDate(1); return d.getTime(); }
  function iso(t) { const d = new Date(t); d.setUTCDate(1); return d.toISOString(); }
</script>
<template><p>{now()} {local(ms)} {year(ms)} {shift(new Date(ms))} {iso(ms)}</p></template>
`,
	})
	out := filepath.Join(filepath.Dir(src), "out")
	report := filepath.Join(out, "report.json")
	opts := CompileOptions{StatusWriter: io.Discard, SSRModule: "example.com/when/server", SSRReport: report}
	BuildSSRProject(CollectVela(src), src, out, opts, false)
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`now has no server-native translation: new Date() reads the clock`,
		`local has no server-native translation: new Date(year, month, …) is local time`,
		`year has no server-native translation: method value.getFullYear is not available`,
		`setUTCDate changes its date; during server rendering it is admitted only on a date the function created`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("report lacks %s\n%s", want, text)
		}
	}
	if strings.Contains(text, `iso has no`) {
		t.Errorf("iso() must translate:\n%s", text)
	}
}

// TestSSRForOfChangingItsCollectionStaysRejected: for…of iterates the
// items taken before the first iteration, so a body that adds or removes
// items of the iterated array or collection is rejected.
func TestSSRForOfChangingItsCollectionStaysRejected(t *testing.T) {
	src := writeSSRModuleFixture(t, map[string]string{
		"Walk.vela": `<script>
  export let items = [1, 2];
  function walk(xs) { const q = [...xs]; let n = 0; for (const x of q) { if (x < 3) q.push(x + 2); n += x; } return n; }
  function drain(xs) { const s = new Set(xs); let n = 0; for (const x of s) { s.delete(x + 1); n += x; } return n; }
  function copy(xs) { const out = []; for (const x of xs) out.push(x); return out.length; }
</script>
<template><p>{walk(items)} {drain(items)} {copy(items)}</p></template>
`,
	})
	out := filepath.Join(filepath.Dir(src), "out")
	report := filepath.Join(out, "report.json")
	opts := CompileOptions{StatusWriter: io.Discard, SSRModule: "example.com/walk/server", SSRReport: report}
	BuildSSRProject(CollectVela(src), src, out, opts, false)
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{`walk has no server-native translation: the body of for…of changes \"q\"`, `drain has no server-native translation: the body of for…of changes \"s\"`} {
		if !strings.Contains(text, want) {
			t.Errorf("report lacks %s\n%s", want, text)
		}
	}
	if strings.Contains(text, `copy has no`) {
		t.Errorf("copy() must translate:\n%s", text)
	}
}

// TestSSRLabelledJumpsAcrossTryStayRejected: break and continue, labelled
// or not, cannot leave a try part, whose statements run in a closure.
func TestSSRLabelledJumpsAcrossTryStayRejected(t *testing.T) {
	src := writeSSRModuleFixture(t, map[string]string{
		"Jumps.vela": `<script>
  export let n = 2;
  function labelled(k) { let s = 0; outer: for (let i = 0; i < k; i++) { try { continue outer; } catch (e) { s++; } } return s; }
  function plain(k) { let s = 0; for (let i = 0; i < k; i++) { try { break; } finally { s++; } } return s; }
  function inside(k) { let s = 0; try { inner: for (let i = 0; i < k; i++) { if (i) break inner; s++; } } catch (e) {} return s; }
</script>
<template><p>{labelled(n)} {plain(n)} {inside(n)}</p></template>
`,
	})
	out := filepath.Join(filepath.Dir(src), "out")
	report := filepath.Join(out, "report.json")
	opts := CompileOptions{StatusWriter: io.Discard, SSRModule: "example.com/jumps/server", SSRReport: report}
	BuildSSRProject(CollectVela(src), src, out, opts, false)
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{`labelled has no server-native translation: break or continue across a try statement`, `plain has no server-native translation: break or continue across a try statement`} {
		if !strings.Contains(text, want) {
			t.Errorf("report lacks %s", want)
		}
	}
	if strings.Contains(text, `inside has no`) {
		t.Errorf("a labelled loop inside a try part must translate:\n%s", text)
	}
}
