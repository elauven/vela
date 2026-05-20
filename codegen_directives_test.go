package main

// codegen_directives_test.go — element directives `class:` and `style:`.
//
// Two new Svelte-compatible directives:
//
//   class:foo={cond}       toggle the "foo" class based on truthiness
//   class:foo              shorthand for class:foo={foo}
//   style:foo={expr}       set inline CSS property "foo" to expr
//   style:font-size="{x}px"  interpolated string value
//
// Both register reactive updates so changes to their inputs flow
// through to the DOM, and both leave existing static class/style
// attributes intact (no clobbering).

import (
	"strings"
	"testing"
)

func TestDir_ClassDirectiveTogglesClass(t *testing.T) {
	src := `<script>
  let active = $state(false);
</script>
<template>
  <div class:active={active}>x</div>
</template>`
	out := compileGenerated(t, src)
	if !strings.Contains(out, "classList.toggle('active', !!(active))") {
		t.Fatalf("expected classList.toggle for class:active; got:\n%s", out)
	}
}

func TestDir_ClassDirectiveShorthand(t *testing.T) {
	// `class:active` with no value should behave like `class:active={active}`.
	src := `<script>
  let active = $state(true);
</script>
<template>
  <div class:active>x</div>
</template>`
	out := compileGenerated(t, src)
	if !strings.Contains(out, "classList.toggle('active', !!(active))") {
		t.Fatalf("expected shorthand to expand to active flag:\n%s", out)
	}
}

func TestDir_ClassDirectivePreservesStaticClass(t *testing.T) {
	// Static `class="btn"` and dynamic `class:active={cond}` should
	// coexist — "btn" stays put while "active" toggles.
	src := `<script>
  let active = $state(false);
</script>
<template>
  <div class="btn" class:active={active}>x</div>
</template>`
	out := compileGenerated(t, src)
	if !strings.Contains(out, "setAttribute('class', 'btn')") {
		t.Fatalf("expected static class='btn' to be preserved:\n%s", out)
	}
	if !strings.Contains(out, "classList.toggle('active'") {
		t.Fatalf("expected toggle for class:active:\n%s", out)
	}
	// Crucially: no `setAttribute('active', ...)` — that would mean
	// the directive prefix wasn't recognized.
	if strings.Contains(out, "setAttribute('active'") {
		t.Fatalf("class: directive leaked as a static attribute:\n%s", out)
	}
}

func TestDir_ClassDirectiveRegistersUpdate(t *testing.T) {
	src := `<script>
  let active = $state(false);
</script>
<template>
  <div class:active={active}>x</div>
</template>`
	out := compileGenerated(t, src)
	// The setter for `active` should fire the toggle, so changing
	// state actually updates the DOM.
	if !strings.Contains(out, "function $$set_active(") {
		t.Fatalf("missing setter:\n%s", out)
	}
	// Find the setter body and ensure it contains the toggle call.
	idx := strings.Index(out, "function $$set_active(")
	end := strings.Index(out[idx:], "\n  }\n")
	if end < 0 {
		t.Fatalf("can't find setter end")
	}
	setterBody := out[idx : idx+end]
	if !strings.Contains(setterBody, "classList.toggle('active'") {
		t.Fatalf("setter doesn't refresh class:active toggle:\n%s", setterBody)
	}
}

func TestDir_StyleDirectiveSetsProperty(t *testing.T) {
	src := `<script>
  let color = $state('red');
</script>
<template>
  <div style:color={color}>x</div>
</template>`
	out := compileGenerated(t, src)
	if !strings.Contains(out, "style.setProperty('color', String(color))") {
		t.Fatalf("expected style.setProperty for style:color; got:\n%s", out)
	}
}

func TestDir_StyleDirectiveInterpolatedString(t *testing.T) {
	// `style:font-size="{size}px"` — the value is a string template,
	// not a bare expression.  Compiler should convert to a JS
	// template literal so concatenation works at runtime.
	src := `<script>
  let size = $state(16);
</script>
<template>
  <div style:font-size="{size}px">x</div>
</template>`
	out := compileGenerated(t, src)
	if !strings.Contains(out, "setProperty('font-size', String(`${size}px`))") {
		t.Fatalf("interpolated style value not converted to template literal:\n%s", out)
	}
}

func TestDir_StyleDirectiveHyphenatedName(t *testing.T) {
	// "font-size" must survive without being mangled.  setProperty
	// accepts hyphenated names; the alternative (`elem.style.foo =`)
	// would require camelCase conversion.
	src := `<script>
  let sz = $state(16);
</script>
<template>
  <div style:font-size={sz}>x</div>
</template>`
	out := compileGenerated(t, src)
	if !strings.Contains(out, "setProperty('font-size'") {
		t.Fatalf("hyphenated style prop got mangled:\n%s", out)
	}
}

func TestDir_StyleDirectiveCustomProperty(t *testing.T) {
	// `style:--theme={c}` — CSS custom property.  setProperty
	// handles `--theme` natively; `elem.style[name]` wouldn't.
	src := `<script>
  let c = $state('blue');
</script>
<template>
  <div style:--theme={c}>x</div>
</template>`
	out := compileGenerated(t, src)
	if !strings.Contains(out, "setProperty('--theme', String(c))") {
		t.Fatalf("custom property setProperty missing:\n%s", out)
	}
}

func TestDir_StyleShorthand(t *testing.T) {
	src := `<script>
  let color = $state('red');
</script>
<template>
  <div style:color>x</div>
</template>`
	out := compileGenerated(t, src)
	if !strings.Contains(out, "setProperty('color', String(color))") {
		t.Fatalf("style shorthand didn't expand:\n%s", out)
	}
}

func TestDir_DirectivesMarkElementDynamic(t *testing.T) {
	// An element with only a directive (no other dynamic attrs) must
	// NOT be flattened into static HTML.  Otherwise the directive
	// would be baked into the HTML as a literal attribute name.
	src := `<script>
  let active = $state(false);
</script>
<template>
  <div><span class:active={active}>nested</span></div>
</template>`
	out := compileGenerated(t, src)
	// No setAttribute('class:active', ...) leaking into static HTML.
	if strings.Contains(out, `class:active="`) || strings.Contains(out, "class:active='") {
		t.Fatalf("class: leaked into static HTML:\n%s", out)
	}
	if !strings.Contains(out, "classList.toggle('active'") {
		t.Fatalf("expected real toggle emission:\n%s", out)
	}
}

func TestDir_StyleAndClassCombined(t *testing.T) {
	// All four shapes on one element — sanity check that the parser
	// doesn't trip over the combination and codegen emits all.
	src := `<script>
  let active = $state(false);
  let color = $state('red');
</script>
<template>
  <div class="card" class:active={active} style:color={color}>x</div>
</template>`
	out := compileGenerated(t, src)
	checks := []string{
		"setAttribute('class', 'card')",
		"classList.toggle('active', !!(active))",
		"setProperty('color', String(color))",
	}
	for _, c := range checks {
		if !strings.Contains(out, c) {
			t.Errorf("missing emission %q in:\n%s", c, out)
		}
	}
}
