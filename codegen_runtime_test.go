package main

// codegen_runtime_test.go — regression tests for two more codegen
// bugs surfaced by running the playground Demo against a real DOM:
//
//   • Bug 8 — slot factories created a DocumentFragment and then
//     called insertAdjacentHTML on it.  Fragments don't have that
//     method ("Uncaught TypeError: $$sf_...insertAdjacentHTML is not
//     a function").  The compiler now emits a $$insertHTML helper
//     that picks the right path for elements vs fragments.
//
//   • Bug 9 — inline whitespace between text and adjacent elements
//     was eaten by skipWS in parseNodes, so `before <code>x</code>
//     after` rendered as `before<code>x</code>after`.  The parser
//     now preserves a single inter-element space when it was present
//     in the source.

import (
	"strings"
	"testing"
)

// ─────────────────────────────────────────────
//  Bug 8 — DocumentFragment insertAdjacentHTML
// ─────────────────────────────────────────────

func TestBug8_FragmentHelperEmitted(t *testing.T) {
	src := `<script></script><template><div>hi</div></template>`
	out := compileGenerated(t, src)
	if !strings.Contains(out, "function $$insertHTML") {
		t.Fatalf("expected $$insertHTML helper in every component preamble:\n%s", out)
	}
}

func TestBug8_StaticChildrenRouteThroughHelper(t *testing.T) {
	// Static HTML batched into one call should use the helper, not
	// the raw insertAdjacentHTML which fails on fragments.
	src := `<script></script>
<template>
  <div><h1>Title</h1><p>Body</p></div>
</template>`
	out := compileGenerated(t, src)
	if strings.Contains(out, ".insertAdjacentHTML(") {
		// Allowed only inside the helper body itself.
		// Count occurrences and make sure the only one is the helper.
		count := strings.Count(out, ".insertAdjacentHTML(")
		// One inside the helper definition is OK; any more is a bug.
		if count > 1 {
			t.Fatalf("found %d insertAdjacentHTML calls outside helper; expected only the helper:\n%s",
				count, out)
		}
	}
	if !strings.Contains(out, "$$insertHTML(") {
		t.Fatalf("expected $$insertHTML call somewhere:\n%s", out)
	}
}

func TestBug8_SlotFactoryUsesHelper(t *testing.T) {
	// The crashing case: a component slot with static HTML content.
	src := `<script></script>
<template>
  <MyComp>
    <p>slot content</p>
  </MyComp>
</template>`
	out := compileGenerated(t, src)
	// Find the slot factory's body and make sure it doesn't call
	// raw .insertAdjacentHTML on the $$sf_ fragment.
	if strings.Contains(out, "$$sf_") && strings.Contains(out, "$$sf_default") {
		// If a $$sf_ identifier is followed by .insertAdjacentHTML(
		// somewhere in the output, that's the bug.
		idx := 0
		for {
			j := strings.Index(out[idx:], "$$sf_default")
			if j < 0 {
				break
			}
			tail := out[idx+j:]
			// Look ahead a short distance for the broken pattern.
			// This is the exact shape that threw in the playground.
			lineEnd := strings.Index(tail, "\n")
			if lineEnd < 0 {
				lineEnd = len(tail)
			}
			line := tail[:lineEnd]
			if strings.Contains(line, ".insertAdjacentHTML(") {
				t.Fatalf("$$sf_default still uses .insertAdjacentHTML:\n%s", line)
			}
			idx += j + 1
		}
	}
}

// ─────────────────────────────────────────────
//  Bug 9 — inline whitespace
// ─────────────────────────────────────────────

func TestBug9_LeadingSpaceBeforeInlineElement(t *testing.T) {
	src := `<script></script>
<template>
  <p>before <code>x</code></p>
</template>`
	out := compileGenerated(t, src)
	// Expect the static HTML run to contain "before <code>" with a
	// space — not "before<code>".
	if strings.Contains(out, "before<code") {
		t.Fatalf("leading space before <code> was eaten:\n%s", out)
	}
	if !strings.Contains(out, "before <code") {
		t.Fatalf("expected 'before <code' (with space) in output:\n%s", out)
	}
}

func TestBug9_TrailingSpaceAfterInlineElement(t *testing.T) {
	src := `<script></script>
<template>
  <p><code>x</code> after</p>
</template>`
	out := compileGenerated(t, src)
	if strings.Contains(out, "</code>after") {
		t.Fatalf("trailing space after </code> was eaten:\n%s", out)
	}
	if !strings.Contains(out, "</code> after") {
		t.Fatalf("expected '</code> after' (with space) in output:\n%s", out)
	}
}

func TestBug9_BothSides(t *testing.T) {
	// The exact shape from the user's Demo.vela.
	src := `<script></script>
<template>
  <p>uses <code>X</code> for stuff</p>
</template>`
	out := compileGenerated(t, src)
	if !strings.Contains(out, "uses <code") {
		t.Fatalf("missing leading space:\n%s", out)
	}
	if !strings.Contains(out, "</code> for") {
		t.Fatalf("missing trailing space:\n%s", out)
	}
}

func TestBug9_LeadingWhitespaceAtElementStartStillTrimmed(t *testing.T) {
	// Indentation at the start of an element's content (the very
	// first child) should still be stripped — this is the cosmetic
	// whitespace from pretty-printed source that nobody wants in
	// their output.
	src := `<script></script>
<template>
  <p>
    hello
  </p>
</template>`
	out := compileGenerated(t, src)
	// The text "hello" should appear without a leading space inside
	// the <p>'s static HTML emission.  We're looking for `>hello<` or
	// just hello content — not `> hello` with a leading space.
	if strings.Contains(out, ">  hello") || strings.Contains(out, "> hello") {
		t.Fatalf("indentation leaked into output:\n%s", out)
	}
}
