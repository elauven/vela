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
