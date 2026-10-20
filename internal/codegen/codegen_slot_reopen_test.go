package codegen

// codegen_slot_reopen_test.go — regression test for bug 10:
// nested components inside a conditional slot (e.g. <Modal>) were
// duplicated on every reopen because the slot factory re-ran the
// component constructor call on the same wrapper element, and
// `Comp(target, ...)` ends with `target.appendChild($$root)` — so
// each reopen accumulated another `<button>` (or whatever) in the
// wrapper.
//
// Fix: guard the constructor call so it only runs the first time.
// Reactive prop changes still flow via the existing instance's
// `.set()` method.

import (
	"regexp"
	"strings"
	"testing"
)

// TestBug10_NestedComponentInstantiationGuarded checks that every
// `$$nX_comp = Comp(...)` emission is preceded by an `if (!$$nX_comp)`
// guard.  Without this, slot factories called multiple times would
// keep appending duplicate DOM into the cached wrapper.
func TestBug10_NestedComponentInstantiationGuarded(t *testing.T) {
	src := `<script>
  let open = $state(false);
</script>
<template>
  <Modal :open={open}>
    <Button label="Inside" />
  </Modal>
</template>`
	out := compileGenerated(t, src)

	// Pattern: an assignment of the form `  $$XXX_comp = SomeName(`
	// that isn't preceded by an if-guard is a regression.
	re := regexp.MustCompile(`(?m)^(\s*)(\$\$\w+_comp) = (\w+)\(`)
	matches := re.FindAllStringSubmatchIndex(out, -1)
	for _, m := range matches {
		// Look at the preceding non-whitespace stretch on the same line.
		start := m[0]
		lineStart := strings.LastIndexByte(out[:start+1], '\n') + 1
		line := out[lineStart:m[1]]
		// We want either `if (!XXX_comp) XXX_comp = Comp(` or
		// patterns where the call is part of a larger conditional
		// (e.g. inside an `if ($$active === 0)` branch in a chain).
		// The simplest invariant: every plain-line `$$XXX_comp = Comp(`
		// must have `if (!$$XXX_comp)` either on the same line or as
		// a known-safe context.
		instVar := out[m[4]:m[5]]
		guardOnSameLine := strings.Contains(line, "if (!"+instVar+")")
		// The AST printer emits an if-body on the following line, so the
		// immediately preceding guard is semantically equivalent.
		guardBefore := strings.Contains(out[max(0, start-200):start], "if (!"+instVar+")")
		// Inside an each-block factory ($$frag_$$nN), the constructor
		// call lives inside the per-row builder where guarding would
		// be wrong (each row gets its own instance).  These appear in
		// `function $$frag_…` scopes, which we detect by looking
		// upward.
		insideFragFactory := false
		{
			head := out[:m[0]]
			lastFrag := strings.LastIndex(head, "function $$frag_")
			lastReturn := strings.LastIndex(head, "\n  };\n")
			if lastFrag > lastReturn {
				insideFragFactory = true
			}
		}
		if !guardOnSameLine && !guardBefore && !insideFragFactory {
			t.Errorf("unguarded constructor call on line:\n  %s\n  (would duplicate DOM on slot reopen)", line)
		}
	}
}

// TestBug10_ReopenDoesNotEmitDuplicateCall ensures the specific
// shape that caused the playground bug isn't in the output.  The
// slot factory's reopen path used to contain a bare `$$nX_comp =
// Comp(...)` call; it must now be guarded.
func TestBug10_SlotReopenPathGuarded(t *testing.T) {
	src := `<script>
  let open = $state(false);
</script>
<template>
  <Modal :open={open}>
    <p>body</p>
    <Button label="X" />
  </Modal>
</template>`
	out := compileGenerated(t, src)

	// Find the default-slot factory body — it's the closure assigned
	// to "default" inside the Modal call.  We look for the pattern
	// `"default": () => {` and inspect what's inside.
	defaultIdx := strings.Index(out, `"default": () =>`)
	if defaultIdx < 0 {
		t.Skip("no default slot in output; nothing to assert")
		return
	}
	// Take a chunk after that point — the factory body runs to the
	// next `}` at matching depth, but we don't need precision: the
	// shape we're checking is local.
	tail := out[defaultIdx:]
	// Inside the reopen path (`if (cache) { ... return frag; }`),
	// look for any bare `$$nN_comp = Name(` without a guard.
	reopenStart := strings.Index(tail, "if ($$sc_default")
	if reopenStart < 0 {
		// Some templates don't cache (single static element) — skip.
		return
	}
	// Look from reopenStart for the next `return` (end of reopen path).
	end := strings.Index(tail[reopenStart:], "return")
	if end < 0 {
		return
	}
	reopenBody := tail[reopenStart : reopenStart+end]
	// Now check: no unguarded constructor calls.
	re := regexp.MustCompile(`(\$\$\w+_comp) = (\w+)\(`)
	matches := re.FindAllStringSubmatch(reopenBody, -1)
	for _, m := range matches {
		guard := "if (!" + m[1] + ")"
		if !strings.Contains(reopenBody, guard) {
			t.Errorf("reopen path has unguarded call: %s = %s(...)\n"+
				"  reopen body:\n%s", m[1], m[2], reopenBody)
		}
	}
}
