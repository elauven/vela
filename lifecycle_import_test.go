package main

import (
	"strings"
	"testing"
)

// onMount and onDestroy are compiler intrinsics — they get extracted
// by analyzeScript and wired directly into the generated mount path
// and destroy() body.  They have NO runtime existence in runtime.js.
//
// The bug these tests guard against: the compiler used to pass the
// names through to the import statement anyway, emitting a phantom
// `import { onMount } from './runtime.js'` that fails at module load
// with "doesn't provide an export named 'onMount'".

// compile returns the full generated JS for a small SFC.
func compileLifecycle(t *testing.T, script string) string {
	t.Helper()
	src := "<script>\n" + script + "\n</script>\n<template><p>x</p></template>"
	sfc, err := parseSFC(src)
	if err != nil {
		t.Fatalf("parseSFC: %v", err)
	}
	tmpl, err := parseTemplate(sfc.Template)
	if err != nil {
		t.Fatalf("parseTemplate: %v", err)
	}
	a := analyzeScript(sfc.Script)
	return generateCodeAST("T", sfc, tmpl, a, "vela-test")
}

// A bare `import { onMount } from 'vela'` must vanish entirely —
// the intrinsic has no runtime export.  The mount-time wiring must
// still be present (a marker like $$mountCleanup0 or the user-
// supplied body landing in the right place).
func TestLifecycle_OnMountImportStripped(t *testing.T) {
	out := compileLifecycle(t, `
		import { onMount } from 'vela';
		onMount(() => { console.log('mounted'); });
	`)
	if strings.Contains(out, "onMount") &&
		strings.Contains(out, "from './runtime.js'") {
		// If 'onMount' appears in a runtime.js import line, the
		// bug is back.  A naive substring check is enough — the
		// only legal residue would be the inlined user code, and
		// that does not name onMount any more.
		for _, line := range strings.Split(out, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "import") &&
				strings.Contains(line, "onMount") {
				t.Fatalf("phantom onMount import remains:\n%s", line)
			}
		}
	}
	if !strings.Contains(out, "console.log('mounted')") {
		t.Errorf("onMount body not wired into the component:\n%s", out)
	}
}

// onDestroy gets the same treatment.
func TestLifecycle_OnDestroyImportStripped(t *testing.T) {
	out := compileLifecycle(t, `
		import { onDestroy } from 'vela';
		onDestroy(() => { console.log('bye'); });
	`)
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "import") &&
			strings.Contains(line, "onDestroy") {
			t.Fatalf("phantom onDestroy import remains:\n%s", line)
		}
	}
	if !strings.Contains(out, "console.log('bye')") {
		t.Errorf("onDestroy body not wired:\n%s", out)
	}
}

// Both together — the whole import line should disappear.
func TestLifecycle_BothImportStripped(t *testing.T) {
	out := compileLifecycle(t, `
		import { onMount, onDestroy } from 'vela';
		onMount(() => {});
		onDestroy(() => {});
	`)
	for _, line := range strings.Split(out, "\n") {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "import") &&
			strings.Contains(s, "'./runtime.js'") &&
			(strings.Contains(s, "onMount") ||
				strings.Contains(s, "onDestroy")) {
			t.Fatalf("phantom lifecycle import remains:\n%s", line)
		}
	}
}

// A MIXED import — one intrinsic plus one real runtime export —
// must keep the real export and drop only the intrinsic.
func TestLifecycle_MixedImportKeepsRuntimeName(t *testing.T) {
	out := compileLifecycle(t, `
		import { onMount, derived } from 'vela';
		const store = derived([], () => 0);
		onMount(() => {});
	`)
	// The runtime export `derived` must survive on its own import.
	want := "import { derived } from './runtime.js';"
	if !strings.Contains(out, want) {
		t.Errorf("expected %q in output:\n%s", want, out)
	}
	// And no onMount on a runtime.js import line.
	for _, line := range strings.Split(out, "\n") {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "import") &&
			strings.Contains(s, "'./runtime.js'") &&
			strings.Contains(s, "onMount") {
			t.Fatalf("phantom onMount in mixed import:\n%s", line)
		}
	}
}

// stripVelaIntrinsics unit tests — keep the table small + readable.
func TestStripVelaIntrinsics(t *testing.T) {
	cases := []struct{ in, want string }{
		{"{ onMount }", ""},
		{"{ onMount, onDestroy }", ""},
		{"{ derived }", "{ derived }"},
		{"{ onMount, derived }", "{ derived }"},
		{"{ derived, onMount }", "{ derived }"},
		{"{ onMount as init }", ""},
		{"{ onMount as init, derived }", "{ derived }"},
		{"", ""},
		// Default-only and namespace forms are passed through —
		// nothing to filter.
		{"D", "D"},
		{"* as Ns", "* as Ns"},
	}
	for _, c := range cases {
		got := stripVelaIntrinsics(c.in)
		if got != c.want {
			t.Errorf("stripVelaIntrinsics(%q) = %q, want %q",
				c.in, got, c.want)
		}
	}
}

// TestComponentHasDefaultExport — components compile to a named
// export `export function Name`, but the idiomatic import (and the
// way every multi-component project is written) is a default
// import: `import Name from './Name.vela'`.  The module must
// therefore also provide a default export, or component-to-
// component imports fail at load with "does not provide an export
// named 'default'".
func TestComponentHasDefaultExport(t *testing.T) {
	out := compileLifecycle(t, `let n = $state(0);`)
	if !strings.Contains(out, "export function T(") {
		t.Errorf("expected named export:\n%s", out)
	}
	if !strings.Contains(out, "export default T;") {
		t.Errorf("expected default export `export default T;`:\n%s", out)
	}
}
