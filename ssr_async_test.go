package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ssrAsyncModule exercises the synchronous model of §7.4: async functions,
// await, try/catch around await, promise chains, Promise.all and
// allSettled.
const ssrAsyncModule = `export async function load(id) {
  if (id < 0) throw new RangeError('negative ' + id);
  const base = await Promise.resolve(id * 2);
  return { id, label: 'item-' + base };
}

export async function total(ids) {
  let sum = 0;
  for (const id of ids) {
    const item = await load(id);
    sum += item.id;
  }
  return sum;
}

export function guarded(id) {
  return load(id).then((item) => item.label).catch((e) => 'caught ' + e.message).finally(() => 'ignored');
}

export async function safe(id) {
  try {
    const item = await load(id);
    return item.label;
  } catch (e) {
    return e.name;
  } finally {
    id = 0;
  }
}

export function settled(a, b) {
  return Promise.allSettled([load(a), load(b)]).then((rs) => rs[0].status + ',' + rs[1].status);
}

export async function both(a, b) {
  const pair = await Promise.all([load(a), Promise.resolve(b)]);
  return pair[0].label + '+' + pair[1];
}

export async function rejectWith(v) {
  return Promise.reject(v);
}
`

const ssrAsyncComponent = `<script>
  import { load, total, guarded, safe, settled, both, rejectWith } from './async.js';
  export let id = 3;
  export let ids = [1, 2, 3];
  export let other = -1;
</script>
<template><p>{#await load(id)}<i>wait</i>{:then item}<b>{item.label}</b>{:catch e}<s>{e.name}: {e.message}</s>{/await}{#await total(ids)}{:then t}<b>{t}</b>{:catch e}<s>{e.message}</s>{/await}{#await guarded(id) then v}<b>{v}</b>{/await}{#await safe(id) then v}<b>{v}</b>{/await}{#await settled(id, other) then v}<b>{v}</b>{/await}{#await both(id, 'x')}{:then v}<b>{v}</b>{:catch e}<s>{e.message}</s>{/await}{#await rejectWith('no')}{:then v}<b>{v}</b>{:catch e}<s>{e}</s>{/await}{#await 7 then v}<b>{v}</b>{/await}</p></template>
`

// TestSSRAsyncMatchesNode renders {#await} blocks over translated async
// code on the server and compares the settled branches with Node.
func TestSSRAsyncMatchesNode(t *testing.T) {
	node, _ := requireJsdom(t)
	src := writeSSRModuleFixture(t, map[string]string{"async.js": ssrAsyncModule, "Async.vela": ssrAsyncComponent})
	cases := []string{`{}`, `{"id":-2}`, `{"ids":[4,-1],"other":5}`}
	script := `
import { load, total, guarded, safe, settled, both, rejectWith } from './async.js';
const defaults = { id: 3, ids: [1, 2, 3], other: -1 };
const branch = async (promise, then, katch) => {
  try { return 'b:' + then(await promise); } catch (e) { return katch ? 's:' + katch(e) : 'uncaught'; }
};
const out = [];
for (const text of JSON.parse(process.argv[2])) {
  const { id, ids, other } = { ...defaults, ...JSON.parse(text) };
  const parts = [
    await branch(load(id), (item) => item.label, (e) => e.name + ': ' + e.message),
    await branch(total(ids), (t) => t, (e) => e.message),
    await branch(guarded(id), (v) => v),
    await branch(safe(id), (v) => v),
    await branch(settled(id, other), (v) => v),
    await branch(both(id, 'x'), (v) => v, (e) => e.message),
    await branch(rejectWith('no'), (v) => v, (e) => e),
    await branch(7, (v) => v),
  ];
  out.push(parts.join('|'));
}
console.log(JSON.stringify(out));
`
	if err := os.WriteFile(filepath.Join(src, "oracle.mjs"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(cases)
	command := exec.Command(node, "oracle.mjs", string(encoded))
	command.Dir = src
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, output)
	}
	var want []string
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatalf("node output: %v\n%s", err, output)
	}
	if err := os.Remove(filepath.Join(src, "oracle.mjs")); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(filepath.Dir(src), "out")
	report := filepath.Join(out, "report.json")

	opts := CompileOptions{StatusWriter: io.Discard, SSRModule: "example.com/async/server", SSRReport: report}
	if errors := buildSSRProject(collectVela(src), src, out, opts, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}

	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(data), `"kind": "await"`); count != 8 {
		t.Errorf("report has %d await sites, want 8", count)
	}
	branches := regexp.MustCompile(`<(b|s)[^>]*>(.*?)</(?:b|s)>`)
	for i, html := range ssrRenderCases(t, out, "example.com/async", "Async", cases) {
		var parts []string
		for _, match := range branches.FindAllStringSubmatch(html, -1) {
			parts = append(parts, match[1]+":"+match[2])
		}
		if got := strings.Join(parts, "|"); got != want[i] {
			t.Errorf("props %s:\n Go   %s\n Node %s\n html %s", cases[i], got, want[i], html)
		}
	}
}
