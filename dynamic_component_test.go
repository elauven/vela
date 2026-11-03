package main

// Behaviour of <vela:component :this={…}> in the browser runtime (task 070):
// switching the child, keeping it on prop changes, rendering nothing, props
// the child does not declare, slots moving to the next child, component
// events and bind: to a $bindable prop.

import (
	"strings"
	"testing"
)

const dynamicChart = `<script>
  export let rows = [];
  let clicks = $state(0);
  function hit() {
    clicks++;
    $$dispatch('select', clicks);
  }
</script>
<template>
  <div class="chart"><button class="hit" @click={hit}>hit</button><span class="chart-text">chart {rows.length} clicks {clicks}</span><slot /></div>
</template>`

const dynamicTable = `<script>
  export let rows = [];
  export let value = $bindable(0);
</script>
<template>
  <div class="table"><button class="inc" @click={() => value++}>inc</button><span class="table-text">table {rows.length} value {value}</span><slot /></div>
</template>`

const dynamicApp = `<script>
  import { Chart } from './Chart.vela';
  import { Table } from './Table.vela';

  let mode = $state('chart');
  let rows = $state([1, 2]);
  let value = $state(5);
  const views = { chart: Chart, table: Table, none: null, off: false };
  const warn = console.warn;
  globalThis.warnings = '';
  console.warn = (...args) => { globalThis.warnings += args.join(' ') + '\n'; };
  function pick(event) {
    globalThis.picked = event.detail;
  }
</script>
<template>
  <main>
    <button id="chart" @click={() => mode = 'chart'}>c</button>
    <button id="table" @click={() => mode = 'table'}>t</button>
    <button id="none" @click={() => mode = 'none'}>n</button>
    <button id="off" @click={() => mode = 'off'}>o</button>
    <button id="add" @click={() => rows = [...rows, rows.length + 1]}>add</button>
    <span id="value">{value}</span>
    <vela:component :this={views[mode]} :rows={rows} :clicks={100} bind:value={value} @on:select={pick}>
      <p class="slot">slot {value}</p>
    </vela:component>
    <i id="after">after</i>
  </main>
</template>`

func dynamicComponents() map[string]string {
	return map[string]string{"App": dynamicApp, "Chart": dynamicChart, "Table": dynamicTable}
}

func TestRuntimeDynamicComponentSwitchesAndKeepsState(t *testing.T) {
	assertRuntimeSet(t, "App", dynamicComponents(), []rtStep{
		// An undeclared prop (:clicks) is ignored: it does not reach the
		// child's own state.
		{"assertText": ".chart-text", "equals": "chart 2 clicks 0"},
		{"assertText": ".chart .slot", "equals": "slot 5"},
		{"click": ".hit"},
		{"assertText": ".chart-text", "equals": "chart 2 clicks 1"},
		{"assertGlobal": "picked", "equals": 1},
		// A prop change keeps the child and its state.
		{"click": "#add"},
		{"assertText": ".chart-text", "equals": "chart 3 clicks 1"},
		// Another component replaces the child; the slot moves with it.
		{"click": "#table"},
		{"assertCount": ".chart", "equals": 0},
		{"assertText": ".table-text", "equals": "table 3 value 5"},
		{"assertText": ".table .slot", "equals": "slot 5"},
		{"assertCount": ".slot", "equals": 1},
		// bind: on a $bindable prop of the current child.
		{"click": ".inc"},
		{"assertText": "#value", "equals": "6"},
		{"assertText": ".table-text", "equals": "table 3 value 6"},
		{"assertText": ".table .slot", "equals": "slot 6"},
		// Back to the first component: a new instance.
		{"click": "#chart"},
		{"assertCount": ".table", "equals": 0},
		{"assertText": ".chart-text", "equals": "chart 3 clicks 0"},
		{"assertText": ".chart .slot", "equals": "slot 6"},
		// null and false render nothing, in place.
		{"click": "#none"},
		{"assertCount": ".chart", "equals": 0},
		{"assertCount": ".slot", "equals": 0},
		{"assertHTML": "main", "contains": "</span> <!----> <i id=\"after\">"},
		{"click": "#off"},
		{"assertCount": ".chart", "equals": 0},
		{"click": "#table"},
		{"assertText": ".table-text", "equals": "table 3 value 6"},
		{"assertText": ".table .slot", "equals": "slot 6"},
		{"assertHTML": "main", "contains": "<i id=\"after\">"},
		// Chart declares no $bindable value: each mount of it warns that
		// bind:value is inert (the first mount, step 16 and this one).
		{"click": "#chart"},
		{"assertGlobal": "warnings", "equals": strings.Repeat("[Vela] <vela:component> at App.vela:24:28: bind:value has no effect on Chart, which does not declare export let value = $bindable(…)\n", 3)},
	})
}

func TestRuntimeDynamicComponentRejectsAValueThatIsNotAComponent(t *testing.T) {
	app := `<script>
  let view = $state(42);
</script>
<template>
  <main><vela:component :this={view} /></main>
</template>`
	res := runRuntimeHarness(t, "App", map[string]string{"App": app}, []rtStep{})
	want := "TypeError: vela: <vela:component> at App.vela:5:32: :this is number, not a component"
	if res.OK || len(res.Failures) != 1 || !strings.Contains(res.Failures[0], want) {
		t.Fatalf("failures %q, want one containing %q", res.Failures, want)
	}
}

func TestRuntimeDynamicComponentInBlocks(t *testing.T) {
	item := `<script>
  export let label = '';
  let n = $state(0);
</script>
<template><button class="item" @click={() => n++}>{label}:{n}</button></template>`
	other := `<script>
  export let label = '';
</script>
<template><em class="other">{label}</em></template>`
	app := `<script>
  import { Item } from './Item.vela';
  import { Other } from './Other.vela';
  let show = $state(true);
  let rows = $state([{ id: 1, kind: Item }, { id: 2, kind: Other }, { id: 3, kind: Item }]);
  function swap() {
    rows = rows.map((row) => row.id === 1 ? { id: 1, kind: row.kind === Item ? Other : Item } : row);
  }
  function reverse() {
    rows = [...rows].reverse();
  }
</script>
<template>
  <main>
    <button id="show" @click={() => show = !show}>show</button>
    <button id="swap" @click={swap}>swap</button>
    <button id="reverse" @click={reverse}>reverse</button>
    {#if show}
      <ul>
        {#each rows as row (row.id)}
          <li><vela:component :this={row.kind} :label={'r' + row.id} /></li>
        {/each}
      </ul>
    {/if}
  </main>
</template>`
	components := map[string]string{"App": app, "Item": item, "Other": other}
	assertRuntimeSet(t, "App", components, []rtStep{
		{"assertText": "ul", "equals": "r1:0r2r3:0"},
		{"click": "li:last-child .item"},
		{"assertText": "ul", "equals": "r1:0r2r3:1"},
		// Moving keyed rows moves their dynamic children with their state.
		{"click": "#reverse"},
		{"assertText": "ul", "equals": "r3:1r2r1:0"},
		{"click": "#swap"},
		{"assertText": "ul", "equals": "r3:1r2r1"},
		{"assertCount": ".other", "equals": 2},
		{"click": "#show"},
		{"assertCount": "li", "equals": 0},
		{"click": "#show"},
		{"assertText": "ul", "equals": "r3:0r2r1"},
	})
}
