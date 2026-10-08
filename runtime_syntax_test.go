package main

import "testing"

// State writes reached through syntax the compiler used to skip update the
// view (task 055): class methods, destructuring assignment with defaults,
// tagged templates, and private members.
func TestRuntimeStateWritesThroughExtendedSyntax(t *testing.T) {
	src := `<script>
  let count = $state(0);
  let a = $state(1);
  let b = $state(2);
  let label = $state('');
  class Counter {
    #step = 2;
    inc() { count += this.#step; }
    static has(o) { return #step in o; }
  }
  const counter = new Counter();
  const tag = (s, ...v) => s.join('_') + v.join(',');
  function swap() { [a, b] = [b, a]; }
  function load(o) { ({ a = 5, b } = o); }
  function relabel() { label = tag` + "`" + `n${count}m${Counter.has(counter)}` + "`" + `; }
</script>
<template><div>
  <button class="inc" @click={() => counter.inc()}>{count}</button>
  <button class="swap" @click={swap}>{a}-{b}</button>
  <button class="load" @click={() => load({ b: 9 })}>load</button>
  <button class="label" @click={relabel}>{label}</button>
</div></template>`
	assertRuntime(t, "ExtendedSyntax", src, []rtStep{
		{"assertText": "button.inc", "equals": "0"},
		{"click": "button.inc"},
		{"assertText": "button.inc", "equals": "2"},
		{"assertText": "button.swap", "equals": "1-2"},
		{"click": "button.swap"},
		{"assertText": "button.swap", "equals": "2-1"},
		{"click": "button.load"},
		{"assertText": "button.swap", "equals": "5-9"},
		{"click": "button.label"},
		{"assertText": "button.label", "equals": "n_m_2,true"},
	})
}
