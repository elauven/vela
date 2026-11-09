package main

import (
	"strings"
	"testing"
)

// Task 093: use:action={params}. An action starts once its element is in
// the document, update receives changed parameters, and destroy runs when
// the element goes away (branch change, row removal, component destroy).

// actionLog is a script fragment that records action calls in
// globalThis.__log, separated by spaces.
const actionLog = `
  function log(entry) {
    globalThis.__log = (globalThis.__log ? globalThis.__log + ' ' : '') + entry;
  }
  function tip(node, text) {
    log('start:' + node.localName + ':' + text + ':' + node.isConnected);
    return {
      update(next) { log('update:' + next); },
      destroy() { log('destroy:' + text); },
    };
  }
`

func TestActionStartsUpdatesAndDestroysWithBranch(t *testing.T) {
	src := `<script>` + actionLog + `
  let show = $state(true);
  let label = $state('a');
</script>
<template><div>
  <button class="toggle" @click={() => show = !show}>toggle</button>
  <button class="label" @click={() => label = label + 'a'}>label</button>
  {#if show}<p use:tip={label}>x</p>{/if}
</div></template>`
	assertRuntime(t, "ActionBranch", src, []rtStep{
		{"assertGlobal": "__log", "equals": "start:p:a:true"},
		{"click": "button.label"},
		{"assertGlobal": "__log", "equals": "start:p:a:true update:aa"},
		{"click": "button.toggle"},
		{"assertGlobal": "__log", "equals": "start:p:a:true update:aa destroy:a"},
		{"click": "button.label"},
		{"assertGlobal": "__log", "equals": "start:p:a:true update:aa destroy:a"},
		{"click": "button.toggle"},
		{"assertGlobal": "__log", "equals": "start:p:a:true update:aa destroy:a start:p:aaa:true"},
	})
}

func TestActionWithoutParametersOnRootAndDestroy(t *testing.T) {
	src := `<script>
  function focus(node) {
    globalThis.__log = (globalThis.__log || '') + 'start:' + node.isConnected + ':' + (node.firstChild ? node.firstChild.localName : '-');
    return { destroy() { globalThis.__log += ' destroy'; } };
  }
</script>
<template><section use:focus><input /></section></template>`
	assertRuntime(t, "ActionRoot", src, []rtStep{
		{"assertGlobal": "__log", "equals": "start:true:input"},
		{"remount": true},
		{"assertGlobal": "__log", "equals": "start:true:input destroystart:true:input"},
	})
}

func TestActionInEachRowsAndChildComponent(t *testing.T) {
	child := `<script>` + actionLog + `
  export let name = '';
</script>
<template><span use:tip={name}>{name}</span></template>`
	parent := `<script>
  import { Child } from './Child.vela';
  let items = $state(['x', 'y']);
</script>
<template><div>
  <button @click={() => items = items.slice(1)}>drop</button>
  {#each items as item (item)}<Child :name={item} />{/each}
</div></template>`
	assertRuntimeSet(t, "Parent", map[string]string{"Parent": parent, "Child": child}, []rtStep{
		{"assertGlobal": "__log", "equals": "start:span:x:true start:span:y:true"},
		{"click": "button"},
		{"assertGlobal": "__log", "equals": "start:span:x:true start:span:y:true destroy:x"},
	})
}

func TestActionMemberReferenceAndPlainUpdateless(t *testing.T) {
	src := `<script>
  const lib = {
    mark(node, value) {
      node.setAttribute('data-mark', String(value));
      return { update(next) { node.setAttribute('data-mark', String(next)); } };
    },
  };
  let n = $state(1);
</script>
<template><button use:lib.mark={n * 10} @click={() => n++}>{n}</button></template>`
	assertRuntime(t, "ActionMember", src, []rtStep{
		{"assertHTML": "body", "contains": `data-mark="10"`},
		{"click": "button"},
		{"assertHTML": "body", "contains": `data-mark="20"`},
	})
}

func TestActionDiagnostics(t *testing.T) {
	cases := []struct {
		name, template, script, want string
	}{
		{"component", `<Child use:tip />`, `import { Child } from './Child.vela'; function tip() {}`, "VELA-ACTION-TARGET"},
		{"quoted", `<p use:tip="x">x</p>`, `function tip() {}`, "takes its parameters in braces"},
		{"reference", `<p use:a-b>x</p>`, ``, "must name an action"},
		{"unknown", `<p use:missing>x</p>`, ``, "missing"},
		{"head", `<vela:head><meta name="a" use:tip /></vela:head><p>x</p>`, `function tip() {}`, "VELA-HEAD-CONTENT"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			source := "<script>" + c.script + "</script>\n<template>" + c.template + "</template>\n"
			svc := &CompilerService{Cwd: mustGetwd(t)}
			res := svc.Compile(ServiceRequest{Path: "App.vela", Source: &source})
			var all []string
			for _, d := range res.Diagnostics {
				all = append(all, d.Code+": "+d.Message)
			}
			if !strings.Contains(strings.Join(all, "\n")+res.Error, c.want) {
				t.Fatalf("diagnostics %v (error %q) do not mention %q", all, res.Error, c.want)
			}
		})
	}
}
