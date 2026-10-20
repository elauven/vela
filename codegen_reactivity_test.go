package main

// codegen_reactivity_test.go — regression tests for two bugs in the
// codegen path that the original test suite didn't cover:
//
//   • Bug 6 — `href={url}` on a plain HTML element used to emit
//     `setAttribute('href', '{url}')` (a literal string).  Now treated
//     as a dynamic bind, equivalent to `:href={url}`.
//
//   • Bug 7 — writes inside template event handlers used to leave the
//     state-var assignment as a bare write that didn't trigger the
//     reactive setter.  Both DOM and component handler paths now run
//     through rewriteHandlerAssignments, producing `$$set_X(...)`
//     calls inside the listener body.

import (
	"strings"
	"testing"
)

// compileGenerated runs the .vela source through the full compile
// pipeline and returns the generated JS as a string.  Tests assert on
// substrings.  Returns "" if compilation fails.
func compileGenerated(t *testing.T, src string) string {
	t.Helper()
	sfc, err := ParseSFC(src)
	if err != nil {
		t.Fatalf("parseSFC: %v", err)
	}
	tmpl, err := ParseTemplate(sfc.Template)
	if err != nil {
		t.Fatalf("parseTemplate: %v", err)
	}
	a := analyzeScript(sfc.Script)
	return generateCodeAST("Test", sfc, tmpl, a, "vela-test")
}

// ─────────────────────────────────────────────
//  Bug 6 — dynamic attributes on plain HTML
// ─────────────────────────────────────────────

func TestBug6_BracedHrefIsDynamic(t *testing.T) {
	src := `<script>
  let url = $state('https://example.com');
</script>
<template>
  <a href={url}>x</a>
</template>`
	out := compileGenerated(t, src)
	// Should emit dynamic setAttribute, not a literal '{url}'.
	if strings.Contains(out, "setAttribute('href', '{url}')") {
		t.Fatalf("href={url} still emitted as literal string:\n%s", out)
	}
	if !strings.Contains(out, "setAttribute('href', String(url))") {
		t.Fatalf("expected setAttribute('href', String(url)) somewhere; got:\n%s", out)
	}
}

func TestBug6_BracedClassUsesClassNameProperty(t *testing.T) {
	src := `<script>
  let cls = $state('active');
</script>
<template>
  <div class={cls}>x</div>
</template>`
	out := compileGenerated(t, src)
	// `class` is special-cased — should go through .className.
	if !strings.Contains(out, ".className = String(cls)") {
		t.Fatalf("expected .className = String(cls); got:\n%s", out)
	}
}

func TestBug6_BindStripsBracesInSetAttribute(t *testing.T) {
	// The explicit `:`-prefixed form historically emitted
	// `setAttribute('href', String({url}))` because bindAttrStmt
	// didn't unwrap the outer braces — `String({url})` is
	// `String({url: url})` at runtime, i.e. "[object Object]".
	src := `<script>
  let url = $state('https://example.com');
</script>
<template>
  <a :href={url}>x</a>
</template>`
	out := compileGenerated(t, src)
	if strings.Contains(out, "String({url})") {
		t.Fatalf("bindAttrStmt still emits shorthand-object literal:\n%s", out)
	}
	if !strings.Contains(out, "String(url)") {
		t.Fatalf("expected String(url); got:\n%s", out)
	}
}

// ─────────────────────────────────────────────
//  Bug 7 — reactivity in template handlers
// ─────────────────────────────────────────────

func TestBug7_DomHandlerWritesUseSetter(t *testing.T) {
	src := `<script>
  let count = $state(0);
</script>
<template>
  <button @click={() => count = 5}>x</button>
</template>`
	out := compileGenerated(t, src)
	// Handler body should call the setter, not assign directly.
	if !strings.Contains(out, "$$set_count(5)") {
		t.Fatalf("expected $$set_count(5) in handler body; got:\n%s", out)
	}
	// And it shouldn't be wrapped in a useless block that defines
	// but never calls the arrow.
	if strings.Contains(out, "{() => $$set_count") {
		t.Fatalf("handler is wrapped in a block-statement that never runs the arrow:\n%s", out)
	}
}

func TestBug7_DomHandlerIncrementUsesSetter(t *testing.T) {
	src := `<script>
  let count = $state(0);
</script>
<template>
  <button @click={() => count++}>x</button>
</template>`
	out := compileGenerated(t, src)
	// count++ should be rewritten to a setter call.
	if !strings.Contains(out, "$$set_count(count+1)") &&
		!strings.Contains(out, "$$set_count(count + 1)") {
		t.Fatalf("expected $$set_count(count+1); got:\n%s", out)
	}
}

func TestBug7_ComponentHandlerWritesUseSetter(t *testing.T) {
	src := `<script>
  let count = $state(0);
</script>
<template>
  <MyBtn @click={() => count = 0} />
</template>`
	out := compileGenerated(t, src)
	if !strings.Contains(out, "$$set_count(0)") {
		t.Fatalf("expected $$set_count(0) in component handler:\n%s", out)
	}
}

func TestBug7_NamedHandlersUnchanged(t *testing.T) {
	// `@click={openModal}` — bare identifier, should pass through.
	// The rewriter sees a bare identifier with no arrow, returns as-is.
	src := `<script>
  let showHelp = $state(false);
  function openModal() { showHelp = true; }
</script>
<template>
  <MyBtn @click={openModal} />
</template>`
	out := compileGenerated(t, src)
	if !strings.Contains(out, "openModal($e)") {
		t.Fatalf("expected (openModal)($e) wrapper; got:\n%s", out)
	}
}

func TestBug7_BlockBodyHandler(t *testing.T) {
	// Multi-statement block body — each assignment inside should be
	// rewritten independently.
	src := `<script>
  let a = $state(1);
  let b = $state(2);
</script>
<template>
  <button @click={() => { a = 10; b = 20; }}>x</button>
</template>`
	out := compileGenerated(t, src)
	if !strings.Contains(out, "$$set_a(10)") {
		t.Fatalf("expected $$set_a(10) in block body:\n%s", out)
	}
	if !strings.Contains(out, "$$set_b(20)") {
		t.Fatalf("expected $$set_b(20) in block body:\n%s", out)
	}
}
