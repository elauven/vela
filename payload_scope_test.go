package main

// Tests of task 059: scope attributes only for components with styles,
// payload entries without reproducible state, HTML whitespace rules.

import (
	"strings"
	"testing"
)

func TestScopeAttributeOnlyWithStyles(t *testing.T) {
	plain := "<template><div><p>a</p><span>{x}</span></div></template>\n<script>export let x = 1;</script>"
	styled := plain + "\n<style>p { color: red; }</style>"
	if out := compileGenerated(t, plain); strings.Contains(out, "vela-test") {
		t.Errorf("a component without styles carries its scope attribute:\n%s", out)
	}
	if out := compileGenerated(t, styled); !strings.Contains(out, "vela-test") {
		t.Errorf("a component with styles lost its scope attribute:\n%s", out)
	}
	for src, want := range map[string]string{plain: "", styled: "vela-test"} {
		sfc, err := parseSFC(src)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := BuildSSRRenderPlan("Test", sfc, "vela-test")
		if err != nil {
			t.Fatal(err)
		}
		if plan.ElementScope != want {
			t.Errorf("server element scope %q, want %q", plan.ElementScope, want)
		}
	}
}

func TestReproducibleStateInitializers(t *testing.T) {
	a := analyzeScript(`export let columns = [];
export let issue = null;
import { load } from './api.js';
const LIMIT = 3;
let board = $state(columns);
let title = $state(issue.title);
let key = $state(issue['key']);
let empty = $state();
let n = $state(-1);
let flags = $state.deep({ open: false, tags: ['a', 'b'], owner: issue.owner });
let copy = $state([...columns]);
let loaded = $state(load());
let limit = $state(LIMIT);
let twice = $state(columns.length * 2);
let dyn = $state(issue[title]);`)
	want := map[string]bool{"board": true, "title": true, "key": true, "empty": true, "n": true, "flags": true,
		"copy": false, "loaded": false, "limit": false, "twice": false, "dyn": false}
	for _, sv := range a.StateVars {
		if sv.Reproducible != want[sv.Name] {
			t.Errorf("%s: reproducible = %v, want %v", sv.Name, sv.Reproducible, want[sv.Name])
		}
	}
}

func TestPayloadEntryOnlyForSerializedState(t *testing.T) {
	reproducible := "<script>export let rows = [];\nlet list = $state(rows);\nlet q = $state('');\n</script><template><p @click={() => q = 'x'}>{list.length}{q}</p></template>"
	if out := compileGenerated(t, reproducible); strings.Contains(out, "$$hydrationState") {
		t.Errorf("a component with reproducible state reads a payload entry:\n%s", out)
	}
	serialized := "<script>let started = $state(Date.now());\n</script><template><p>{started}</p></template>"
	if out := compileGenerated(t, serialized); !strings.Contains(out, "$$hydrationState") {
		t.Errorf("a component with serialized state does not read its entry:\n%s", out)
	}
	for src, want := range map[string]int{reproducible: 0, serialized: 1} {
		sfc, err := parseSFC(src)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := BuildSSRRenderPlanWithAnalysis("Test", sfc, analyzeScriptAST(sfc.ScriptAST), "vela-test")
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.PayloadState) != want {
			t.Errorf("payload state %v, want %d names", plan.PayloadState, want)
		}
	}
}
