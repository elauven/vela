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

// ─────────────────────────────────────────────
//  Aliased intrinsics — `import { onMount as init } from 'vela'`
// ─────────────────────────────────────────────

// Before this fix: the import was stripped (the imported NAME
// matched a Vela intrinsic), but the call survived as a
// reference to an undeclared local — `init` is not defined at
// module load.
//
// The fix records each intrinsic-from-'vela' import as
// `localAlias → originalIntrinsic` in Analysis.IntrinsicAliases
// and threads the map into extractLifecycle.  Aliased calls now
// get extracted into Lifecycles like their canonical
// counterparts, and the call site is removed from CleanScript so
// no dangling reference survives.

func TestAliasedOnMount_Recognized(t *testing.T) {
	out := compileLifecycle(t, `
		import { onMount as init } from 'vela';
		let ready = $state(false);
		init(() => { ready = true; });
	`)
	// The aliased call must NOT appear in the emitted output —
	// otherwise it would be a ReferenceError at module load.
	if strings.Contains(out, "init(") {
		t.Errorf("expected aliased intrinsic call `init(...)` to be removed:\n%s", out)
	}
	// And it should have been wired into the mount path.
	if !strings.Contains(out, "$$mountCleanup") {
		t.Errorf("expected aliased onMount to wire into the mount path:\n%s", out)
	}
}

func TestAliasedOnDestroy_Recognized(t *testing.T) {
	out := compileLifecycle(t, `
		import { onDestroy as teardown } from 'vela';
		let bye = $state(false);
		teardown(() => { bye = true; });
	`)
	if strings.Contains(out, "teardown(") {
		t.Errorf("expected aliased onDestroy call `teardown(...)` to be removed:\n%s", out)
	}
	// The onDestroy body should appear in the destroy() return.
	// Both `bye = true` (via the setter) and the bookkeeping
	// should be present.
	if !strings.Contains(out, "destroy()") {
		t.Errorf("expected destroy() in generated output:\n%s", out)
	}
}

func TestAliasedBothIntrinsics_BothRecognized(t *testing.T) {
	out := compileLifecycle(t, `
		import { onMount as setup, onDestroy as teardown } from 'vela';
		let m = $state(false);
		let d = $state(false);
		setup(() => { m = true; });
		teardown(() => { d = true; });
	`)
	for _, banned := range []string{"setup(", "teardown("} {
		if strings.Contains(out, banned) {
			t.Errorf("expected %q to be removed:\n%s", banned, out)
		}
	}
	if !strings.Contains(out, "$$mountCleanup") {
		t.Errorf("expected mount wiring:\n%s", out)
	}
}

// The Analysis.IntrinsicAliases map is the single source of
// truth — verify it is populated as expected directly, so a
// future change in how lifecycles get emitted does not silently
// regress the alias mechanism.
func TestAnalyzer_IntrinsicAliases_Populated(t *testing.T) {
	a := analyzeScript(`
		import { onMount as setup, onDestroy as teardown } from 'vela';
		setup(() => {});
		teardown(() => {});
	`)
	if a.IntrinsicAliases["setup"] != "onMount" {
		t.Errorf("setup → onMount missing: %#v", a.IntrinsicAliases)
	}
	if a.IntrinsicAliases["teardown"] != "onDestroy" {
		t.Errorf("teardown → onDestroy missing: %#v", a.IntrinsicAliases)
	}
}

// An intrinsic imported from a non-Vela source must NOT be
// recorded as an alias — the user's `onMount` from their own
// module is a regular function, and the alias map is keyed on
// `import { … } from 'vela'` only.  (Note: the current direct-
// name match in extractLifecycle still hijacks `onMount(...)`
// regardless of import source — that pre-existing behavior is
// separate from this fix.)
func TestAnalyzer_IntrinsicAliases_NotPopulatedForOtherSource(t *testing.T) {
	a := analyzeScript(`
		import { onMount as setup } from './my-other.js';
		setup(() => {});
	`)
	if _, ok := a.IntrinsicAliases["setup"]; ok {
		t.Errorf("setup should NOT be in IntrinsicAliases for non-vela source: %#v",
			a.IntrinsicAliases)
	}
}
