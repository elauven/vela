package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

type browserRuntimeConfig struct {
	RootDir    string            `json:"rootDir"`
	Entry      string            `json:"entry"`
	ExportName string            `json:"exportName"`
	Steps      []rtStep          `json:"steps"`
	Storage    map[string]string `json:"storage,omitempty"`
}

type browserRuntimeResult struct {
	OK       bool     `json:"ok"`
	Failures []string `json:"failures"`
}

func runBrowserProjectWithStorage(t *testing.T, srcRoot, entry string, storage map[string]string, steps []rtStep) {
	t.Helper()
	node := requirePlaywright(t)
	outRoot := t.TempDir()
	files := collectVela(srcRoot)
	if len(files) == 0 {
		t.Fatalf("project %s has no .vela files", srcRoot)
	}
	if errors := buildProject(files, srcRoot, outRoot, CompileOptions{}, false); errors != 0 {
		t.Fatalf("project build failed with %d error(s)", errors)
	}
	config := browserRuntimeConfig{RootDir: outRoot, Entry: entry, ExportName: componentName(entry), Steps: steps, Storage: storage}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(outRoot, "browser-runtime.json")
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(node, "tools/browser-runtime-harness.mjs", configPath).CombinedOutput()
	if err != nil {
		t.Fatalf("browser runtime harness: %v\n%s", err, output)
	}
	var result browserRuntimeResult
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("invalid browser runtime output: %v\n%s", err, output)
	}
	if !result.OK {
		t.Fatalf("browser runtime failures: %v", result.Failures)
	}
}

func runBrowserProject(t *testing.T, srcRoot, entry string, steps []rtStep) {
	t.Helper()
	runBrowserProjectWithStorage(t, srcRoot, entry, nil, steps)
}

func runBrowserSources(t *testing.T, sources map[string]string, entry string, steps []rtStep) {
	t.Helper()
	srcRoot := t.TempDir()
	for name, source := range sources {
		path := filepath.Join(srcRoot, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
	}
	runBrowserProject(t, srcRoot, entry, steps)
}

func TestBrowserRuntimeTodoCorpusQwen(t *testing.T) {
	runBrowserProject(t, "test_projects/corpus/qwen-fixed/src", "App.js", []rtStep{
		{"assertCount": ".todo-item", "equals": 0},
		{"input": "input.new-title-input", "value": "write browser tests"},
		{"submit": "form.add-form"},
		{"assertCount": ".todo-item", "equals": 1},
		{"change": "input.toggle-checkbox", "checked": true},
		{"assertCount": ".todo-item.done", "equals": 1},
		{"click": "button.filter-button:nth-child(2)"},
		{"assertCount": ".todo-item", "equals": 0},
		{"click": "button.filter-button:nth-child(3)"},
		{"assertCount": ".todo-item.done", "equals": 1},
		{"dblclick": ".todo-title"},
		{"input": "input.edit-input", "value": "edited in chromium"},
		{"keydown": "input.edit-input", "key": "Enter"},
		{"assertText": ".todo-title", "equals": "edited in chromium"},
		{"click": "button.clear-button"},
		{"assertCount": ".todo-item", "equals": 0},
	})
}

func TestBrowserRuntimeTodoCorpusDeepSeek(t *testing.T) {
	runBrowserProjectWithStorage(t, "test_projects/corpus/deepseek-fixed/src", "App.js", map[string]string{
		"vela-todo-tasks": `[{"id":41,"text":"Stored task","done":false}]`,
	}, []rtStep{
		{"assertCount": ".task-item", "equals": 1},
		{"assertText": ".task-item span", "equals": "Stored task"},
		{"input": ".add-form input", "value": "browser deep state"},
		{"keydown": ".add-form input", "key": "Enter"},
		{"assertCount": ".task-item", "equals": 2},
		{"assertStorage": "vela-todo-tasks", "contains": "browser deep state"},
		{"change": ".task-item input[type=checkbox]", "checked": true},
		{"click": ".filters button:nth-child(3)"},
		{"assertCount": ".task-item", "equals": 1},
	})
}

func TestBrowserRuntimeTodoCorpusGPT(t *testing.T) {
	runBrowserProject(t, "test_projects/corpus/gpt-fixed/src", "App.js", []rtStep{
		{"assertCount": ".todo-item", "equals": 2},
		{"click": "button.remove"},
		{"assertCount": ".todo-item", "equals": 1},
		{"change": "input[type=checkbox]", "checked": true},
		{"assertCount": ".todo-item.completed", "equals": 1},
		{"click": "button.clear"},
		{"assertCount": ".todo-item", "equals": 0},
	})
}

func TestBrowserRuntimeDashboardCorpusQwen(t *testing.T) {
	runBrowserProject(t, "test_projects/qwen-dashboard-vela/src", "App.js", []rtStep{
		{"assertCount": ".stat-card", "equals": 4},
		{"assertCount": "button.period-button.selected", "equals": 1},
		{"assertCount": "tbody tr", "equals": 8},
		{"assertCount": ".bar-column[title]", "equals": 7},
		{"click": "button.period-button:nth-of-type(2)"},
		{"assertCount": "button.period-button:nth-of-type(2).selected", "equals": 1},
		{"assertCount": ".bar-column[title]", "equals": 14},
		{"click": "button.refresh-button"},
		{"assertCount": "tbody tr", "equals": 8},
		{"assertCount": ".status", "equals": 8},
	})
}

func TestBrowserRuntimeKeepsFocusAcrossDeepEachUpdate(t *testing.T) {
	runBrowserSources(t, map[string]string{"App.vela": `<script>
  let items = $state.deep([{ id: 1, title: "one" }]);
</script>
<template>{#each items as item (item.id)}<input class="row-input" :value={item.title} @input={(event) => item.title = event.target.value} />{/each}</template>`}, "App.js", []rtStep{
		{"focus": "input.row-input"},
		{"input": "input.row-input", "value": "edited"},
		{"assertValue": "input.row-input", "equals": "edited"},
		{"assertFocused": "input.row-input"},
	})
}

// The audit fixtures below intentionally record the current runtime-cost
// baseline. They are not acceptance tests for the eventual optimized
// behaviour: tasks 021–025 will lower these counts and update the expectation
// together with the implementation. Keeping the baseline executable makes the
// cost and the affected generated path unambiguous.

func TestBrowserRuntimeAuditDeepNoOpWrite(t *testing.T) {
	runBrowserSources(t, map[string]string{"App.vela": `<script>
  let data = $state.deep({ value: 'ready' });
  const metrics = { probeCalls: 0 };
  function probe(value) {
    metrics.probeCalls++;
    return value;
  }
  function writeSameValue() {
    metrics.probeCalls = 0;
    data.value = data.value;
    queueMicrotask(() => { globalThis.__velaAudit = { deepNoOpRenders: metrics.probeCalls }; });
  }
</script>
<template>
  <button class="same" @click={writeSameValue}>same</button>
  <p class="value">{probe(data.value)}</p>
</template>`}, "App.js", []rtStep{
		{"click": "button.same"},
		{"assertText": ".value", "equals": "ready"},
		{"assertGlobal": "__velaAudit.deepNoOpRenders", "equals": 0},
	})
}

func TestBrowserRuntimeDeepArrayMutationRerendersOnce(t *testing.T) {
	runBrowserSources(t, map[string]string{"App.vela": `<script>
  let items = $state.deep([3, 2, 1]);
  const metrics = { renders: 0 };
  function render(values) {
    metrics.renders++;
    return values.join(',');
  }
  function record(operation) {
    queueMicrotask(() => {
      globalThis.__velaAudit = {
        arrayOperation: operation,
        arrayRenders: metrics.renders,
      };
    });
  }
  function push() {
    metrics.renders = 0;
    items.push(4);
    record('push');
  }
  function splice() {
    metrics.renders = 0;
    items.splice(1, 1, 7);
    record('splice');
  }
  function sort() {
    metrics.renders = 0;
    items.sort();
    record('sort');
  }
</script>
<template>
  <button class="push" @click={push}>push</button>
  <button class="splice" @click={splice}>splice</button>
  <button class="sort" @click={sort}>sort</button>
  <p class="values">{render(items)}</p>
  {#each items as item (item)}<span class="row">{item}</span>{/each}
</template>`}, "App.js", []rtStep{
		{"click": "button.push"},
		{"assertText": ".values", "equals": "3,2,1,4"},
		{"assertGlobal": "__velaAudit.arrayOperation", "equals": "push"},
		{"assertGlobal": "__velaAudit.arrayRenders", "equals": 1},
		{"click": "button.splice"},
		{"assertText": ".values", "equals": "3,7,1,4"},
		{"assertGlobal": "__velaAudit.arrayOperation", "equals": "splice"},
		{"assertGlobal": "__velaAudit.arrayRenders", "equals": 1},
		{"click": "button.sort"},
		{"assertText": ".values", "equals": "1,3,4,7"},
		{"assertGlobal": "__velaAudit.arrayOperation", "equals": "sort"},
		{"assertGlobal": "__velaAudit.arrayRenders", "equals": 1},
	})
}

func TestBrowserRuntimeAuditBurstSetterAndEffectWork(t *testing.T) {
	runBrowserSources(t, map[string]string{"App.vela": `<script>
  let first = $state(0);
  let second = $state(0);
  const metrics = { effectRuns: 0, domRuns: 0 };
  function sample(value) {
    metrics.domRuns++;
    return value;
  }
  $effect(() => {
    first;
    second;
    metrics.effectRuns++;
  });
  function burst() {
    metrics.effectRuns = 0;
    metrics.domRuns = 0;
    first = 1;
    second = 1;
    queueMicrotask(() => {
      globalThis.__velaAudit = { burstEffectRuns: metrics.effectRuns, burstDOMRuns: metrics.domRuns };
    });
  }
</script>
<template>
  <button class="burst" @click={burst}>burst</button>
  <p class="value">{sample(first + second)}</p>
</template>`}, "App.js", []rtStep{
		{"click": "button.burst"},
		{"assertText": ".value", "equals": "2"},
		{"assertGlobal": "__velaAudit.burstEffectRuns", "equals": 2},
		{"assertGlobal": "__velaAudit.burstDOMRuns", "equals": 2},
	})
}

func TestBrowserRuntimeAuditKeyedDeepEachUpdatesEveryRow(t *testing.T) {
	runBrowserSources(t, map[string]string{"App.vela": `<script>
  let rows = $state.deep([
    { id: 1, label: 'one' },
    { id: 2, label: 'two' },
    { id: 3, label: 'three' },
  ]);
  const metrics = { rowCalls: 0 };
  function label(row) {
    metrics.rowCalls++;
    return row.label;
  }
  function updateMiddle() {
    const firstRow = document.querySelector('.row');
    metrics.rowCalls = 0;
    rows[1].label = 'two updated';
    queueMicrotask(() => {
      globalThis.__velaAudit = {
        keyedRowUpdates: metrics.rowCalls,
        firstRowPreserved: document.querySelector('.row') === firstRow,
      };
    });
  }
</script>
<template>
  <button class="update" @click={updateMiddle}>update</button>
  {#each rows as row (row.id)}
    <p class="row">{label(row)}</p>
  {/each}
</template>`}, "App.js", []rtStep{
		{"click": "button.update"},
		{"assertText": ".row:nth-of-type(2)", "equals": "two updated"},
		{"assertGlobal": "__velaAudit.keyedRowUpdates", "equals": 3},
		{"assertGlobal": "__velaAudit.firstRowPreserved", "equals": true},
	})
}

func TestBrowserRuntimeAuditBindingRepeatsDOMValueWrite(t *testing.T) {
	runBrowserSources(t, map[string]string{"App.vela": `<script>
  const inputPrototype = document.createElement('input').constructor.prototype;
  const valueDescriptor = Object.getOwnPropertyDescriptor(inputPrototype, 'value');
  const metrics = { valueWrites: 0 };
  Object.defineProperty(inputPrototype, 'value', {
    configurable: true,
    get: valueDescriptor.get,
    set: function(next) {
      metrics.valueWrites++;
      return valueDescriptor.set.call(this, next);
    },
  });
  let value = $state('');
  function measureInput() {
    metrics.valueWrites = 0;
    queueMicrotask(() => {
      globalThis.__velaAudit = { bindingValueWrites: metrics.valueWrites };
    });
  }
  function dispatchNativeInput() {
    const field = document.querySelector('input.field');
    field.value = 'typed';
    metrics.valueWrites = 0;
    field.dispatchEvent(new Event('input', { bubbles: true }));
  }
</script>
<template>
  <input class="field" bind:value={value} @input={measureInput} />
  <button class="dispatch" @click={dispatchNativeInput}>dispatch</button>
</template>`}, "App.js", []rtStep{
		{"click": "button.dispatch"},
		{"assertValue": "input.field", "equals": "typed"},
		{"assertGlobal": "__velaAudit.bindingValueWrites", "equals": 1},
	})
}

func TestBrowserRuntimeAuditDestroyBeforeDeferredFlush(t *testing.T) {
	runBrowserSources(t, map[string]string{"App.vela": `<script>
  const textPrototype = Object.getPrototypeOf(
    Object.getPrototypeOf(document.createTextNode(''))
  );
  const dataDescriptor = Object.getOwnPropertyDescriptor(textPrototype, 'data');
  const metrics = { detachedWrites: 0 };
  Object.defineProperty(textPrototype, 'data', {
    configurable: true,
    get: dataDescriptor.get,
    set: function(next) {
      if (!this.isConnected) metrics.detachedWrites++;
      return dataDescriptor.set.call(this, next);
    },
  });
  let value = $state('before');
  function update() {
    metrics.detachedWrites = 0;
    value = 'after';
    queueMicrotask(() => {
      globalThis.__velaAudit = { detachedWrites: metrics.detachedWrites };
    });
  }
</script>
<template><button class="update" @click={update}>{value}</button></template>`}, "App.js", []rtStep{
		{"clickAndDestroy": "button.update"},
		{"assertCount": "button.update", "equals": 0},
		{"assertGlobal": "__velaAudit.detachedWrites", "equals": 1},
	})
}

func TestBrowserRuntimeComponentKeepsDirectChildCSSSelector(t *testing.T) {
	runBrowserSources(t, map[string]string{
		"App.vela": `<script>
  import { Child } from "./Child.vela";
  let show = $state(true);
</script>
<template><div class="parent">{#if show}<Child />{/if}</div></template>
<style>.parent > .child { color: rgb(1, 2, 3); }</style>`,
		"Child.vela": `<template><p class="child">child</p></template>`,
	}, "App.js", []rtStep{
		{"assertStyle": ".parent > .child", "property": "color", "equals": "rgb(1, 2, 3)"},
	})
}
