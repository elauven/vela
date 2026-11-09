package main

import (
	"strings"
	"testing"
)

// Task 068: a statement after `return` is a VELA-FLOW-UNREACHABLE
// warning, and the component still compiles. The dead statement is left
// out of the module; hoisted declarations after the terminator keep
// their meaning.

func compileUnreachable(t *testing.T, source string) CompileResult {
	t.Helper()
	svc := &CompilerService{Cwd: mustGetwd(t)}
	res := svc.Compile(ServiceRequest{Path: "App.vela", Source: &source})
	if !res.OK {
		t.Fatalf("compile failed: %s\n%+v", res.Error, res.Diagnostics)
	}
	var codes []string
	for _, d := range res.Diagnostics {
		codes = append(codes, d.Code)
	}
	if len(codes) != 1 || codes[0] != "VELA-FLOW-UNREACHABLE" {
		t.Fatalf("diagnostics = %v, want exactly VELA-FLOW-UNREACHABLE", codes)
	}
	return res
}

func TestUnreachableAfterReturnCompiles(t *testing.T) {
	res := compileUnreachable(t, `<script>
  function f() { return 1; console.log('x'); }
</script>
<template><p>{f()}</p></template>
`)
	if strings.Contains(res.JS, "console.log") {
		t.Errorf("the unreachable statement is still in the module:\n%s", res.JS)
	}
}

func TestUnreachableKeepsHoistedDeclarations(t *testing.T) {
	res := compileUnreachable(t, `<script>
  function f() {
    return helper() + String(late);
    console.log('dead');
    var late = 'never', [a, { b }] = [1, { b: 2 }];
    let gone = 3;
    function helper() { return 41; }
  }
</script>
<template><p>{f()}</p></template>
`)
	for _, dead := range []string{"console.log", "'never'", "gone"} {
		if strings.Contains(res.JS, dead) {
			t.Errorf("module still has %s:\n%s", dead, res.JS)
		}
	}
	for _, kept := range []string{"function helper()", "var late, a, b;"} {
		if !strings.Contains(res.JS, kept) {
			t.Errorf("module misses %q:\n%s", kept, res.JS)
		}
	}
}
