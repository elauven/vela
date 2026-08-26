package main

import "testing"

func TestProvenNonCallableLiteralsAreRejected(t *testing.T) {
	for _, script := range []string{`42();`, `'text'();`, `null();`, `[]();`, `({})();`, "`text`();"} {
		diags, err := compileProfileScript(t, script, CompileOptions{})
		if err == nil || findDiagnostic(diags, "VELA-NONCALLABLE") == nil {
			t.Fatalf("script %q diagnostics = %+v, err = %v; want VELA-NONCALLABLE", script, diags, err)
		}
	}
}

func TestCallDiagnosticsDoNotInferVariablesOrMembers(t *testing.T) {
	diags, err := compileProfileScript(t, `let value; value(); object.method();`, CompileOptions{})
	if err != nil || findDiagnostic(diags, "VELA-NONCALLABLE") != nil {
		t.Fatalf("diagnostics = %+v, err = %v; variable/member calls must not be inferred", diags, err)
	}
}

func TestFrameworkIntrinsicCallDiagnostics(t *testing.T) {
	tests := []struct {
		script, code string
	}{
		{`let value = $derived();`, "VELA-DERIVED-ARITY"},
		{`let value = $derived(() => 1);`, "VELA-DERIVED-CALLBACK"},
		{`let value = $state();`, "VELA-STATE-ARITY"},
		{`$effect(1);`, "VELA-EFFECT-CALLBACK"},
		{`$effect(() => 1);`, "VELA-EFFECT-CALLBACK"},
		{`$props();`, "VELA-INTRINSIC-UNSUPPORTED"},
		{`import { onMount } from 'vela'; onMount();`, "VELA-LIFECYCLE-ARITY"},
		{`import { onDestroy as cleanup } from 'vela'; cleanup(() => 1);`, "VELA-LIFECYCLE-CALLBACK"},
	}
	for _, tc := range tests {
		diags, err := compileProfileScript(t, tc.script, CompileOptions{})
		if err == nil || findDiagnostic(diags, tc.code) == nil {
			t.Fatalf("script %q diagnostics = %+v, err = %v; want %s", tc.script, diags, err, tc.code)
		}
	}
}
