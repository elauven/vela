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
  let probeCalls = 0;
  function probe(value) {
    probeCalls++;
    return value;
  }
  function writeSameValue() {
    probeCalls = 0;
    data.value = data.value;
    queueMicrotask(() => { globalThis.__velaAudit = { deepNoOpRenders: probeCalls }; });
  }
</script>
<template>
  <button class="same" @click={writeSameValue}>same</button>
  <p class="value">{probe(data.value)}</p>
</template>`}, "App.js", []rtStep{
		{"click": "button.same"},
		{"assertText": ".value", "equals": "ready"},
		{"assertGlobal": "__velaAudit.deepNoOpRenders", "equals": 1},
	})
}

func TestBrowserRuntimeAuditBurstSetterAndEffectWork(t *testing.T) {
	runBrowserSources(t, map[string]string{"App.vela": `<script>
  let first = $state(0);
  let second = $state(0);
  let effectRuns = 0;
  let domRuns = 0;
  function sample(value) {
    domRuns++;
    return value;
  }
  $effect(() => {
    first;
    second;
    effectRuns++;
  });
  function burst() {
    effectRuns = 0;
    domRuns = 0;
    first = 1;
    second = 1;
    queueMicrotask(() => {
      globalThis.__velaAudit = { burstEffectRuns: effectRuns, burstDOMRuns: domRuns };
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
  let rowCalls = 0;
  function label(row) {
    rowCalls++;
    return row.label;
  }
  function updateMiddle() {
    const firstRow = document.querySelector('.row');
    rowCalls = 0;
    rows[1].label = 'two updated';
    queueMicrotask(() => {
      globalThis.__velaAudit = {
        keyedRowUpdates: rowCalls,
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
  let valueWrites = 0;
  Object.defineProperty(inputPrototype, 'value', {
    configurable: true,
    get: valueDescriptor.get,
    set: function(next) {
      valueWrites++;
      return valueDescriptor.set.call(this, next);
    },
  });
  let value = $state('');
  function measureInput() {
    valueWrites = 0;
    queueMicrotask(() => {
      globalThis.__velaAudit = { bindingValueWrites: valueWrites };
    });
  }
  function dispatchNativeInput() {
    const field = document.querySelector('input.field');
    field.value = 'typed';
    valueWrites = 0;
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
  let detachedWrites = 0;
  Object.defineProperty(textPrototype, 'data', {
    configurable: true,
    get: dataDescriptor.get,
    set: function(next) {
      if (!this.isConnected) detachedWrites++;
      return dataDescriptor.set.call(this, next);
    },
  });
  let value = $state('before');
  function update() {
    detachedWrites = 0;
    value = 'after';
    queueMicrotask(() => {
      globalThis.__velaAudit = { detachedWrites: detachedWrites };
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
