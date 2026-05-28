package main

import (
	"strings"
	"testing"
)

// CSS output mode: a component's scoped <style> is delivered either
// inline (CSS-in-JS, injected at runtime — the default) or written
// to a sibling .css file with no runtime injection.  Scoping
// happens either way; only delivery differs.

const cssModeSrc = `<script>
  let n = $state(0);
</script>
<template><p class="box">{n}</p></template>
<style>
  .box { color: red; padding: 4px; }
</style>`

func cssModeCompile(t *testing.T, src string, external bool) string {
	t.Helper()
	sfc, err := parseSFC(src)
	if err != nil {
		t.Fatalf("parseSFC: %v", err)
	}
	tmpl, err := parseTemplate(sfc.Template)
	if err != nil {
		t.Fatalf("parseTemplate: %v", err)
	}
	a := analyzeScript(sfc.Script)
	return generateCodeASTCSS("Test", sfc, tmpl, a, "vela-test", external)
}

// Inject mode (default): the component creates a <style> element
// and appends it to document.head at runtime.
func TestCSSMode_InjectEmitsRuntimeStyle(t *testing.T) {
	out := cssModeCompile(t, cssModeSrc, false)
	if !strings.Contains(out, "document.createElement('style')") {
		t.Errorf("inject mode should create a <style> element:\n%s", out)
	}
	if !strings.Contains(out, "document.head.appendChild") {
		t.Errorf("inject mode should append <style> to document.head:\n%s", out)
	}
	// The scoped CSS travels inside the JS.
	if !strings.Contains(out, ".box[vela-test]") {
		t.Errorf("inject mode should embed the scoped CSS:\n%s", out)
	}
}

// External mode: no runtime <style> injection at all — the JS is
// free of style-element machinery.
func TestCSSMode_ExternalEmitsNoRuntimeStyle(t *testing.T) {
	out := cssModeCompile(t, cssModeSrc, true)
	if strings.Contains(out, "document.createElement('style')") {
		t.Errorf("external mode must not create a <style> element:\n%s", out)
	}
	if strings.Contains(out, "document.head.appendChild") {
		t.Errorf("external mode must not inject into document.head:\n%s", out)
	}
	// The component must not carry a $$style cleanup it never made.
	if strings.Contains(out, "$$style.remove()") {
		t.Errorf("external mode must not emit $$style cleanup:\n%s", out)
	}
}

// ExtractScopedCSS returns the scope-transformed stylesheet that
// the compiler writes to the sibling .css file.
func TestCSSMode_ExtractScopedCSS(t *testing.T) {
	sfc, err := parseSFC(cssModeSrc)
	if err != nil {
		t.Fatalf("parseSFC: %v", err)
	}
	css := ExtractScopedCSS(sfc, "vela-test")
	if css == "" {
		t.Fatal("expected non-empty scoped CSS")
	}
	// Scope attribute applied to the selector.
	if !strings.Contains(css, ".box[vela-test]") {
		t.Errorf("expected scoped selector .box[vela-test]:\n%s", css)
	}
	// Declarations preserved.
	if !strings.Contains(css, "color: red") {
		t.Errorf("expected declarations preserved:\n%s", css)
	}
}

// A component with no <style> block yields no stylesheet in either
// mode — ExtractScopedCSS returns "" so the compiler writes no .css.
func TestCSSMode_NoStyleNoCss(t *testing.T) {
	src := `<script>let n = $state(0);</script>
<template><p>{n}</p></template>`
	sfc, err := parseSFC(src)
	if err != nil {
		t.Fatalf("parseSFC: %v", err)
	}
	if css := ExtractScopedCSS(sfc, "vela-test"); css != "" {
		t.Errorf("style-less component should yield no CSS, got:\n%s", css)
	}
}

// The scoped CSS must be byte-identical whether delivered inline or
// externally — only the delivery mechanism differs, not the scoping.
func TestCSSMode_ScopingIdenticalBothModes(t *testing.T) {
	sfc, err := parseSFC(cssModeSrc)
	if err != nil {
		t.Fatalf("parseSFC: %v", err)
	}
	external := ExtractScopedCSS(sfc, "vela-test")
	inline := ScopeCSS(sfc.Style, "vela-test")
	if external != inline {
		t.Errorf("scoped CSS differs between modes:\nexternal:\n%s\ninline:\n%s",
			external, inline)
	}
}
