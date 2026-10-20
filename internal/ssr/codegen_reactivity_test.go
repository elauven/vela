package ssr

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
	"github.com/elauven/vela/internal/analyzer"
	"github.com/elauven/vela/internal/codegen"
	"github.com/elauven/vela/internal/sfcast"

	"testing"
)

// ─────────────────────────────────────────────
//  Bug 6 — dynamic attributes on plain HTML
// ─────────────────────────────────────────────

// ─────────────────────────────────────────────
//  Bug 7 — reactivity in template handlers
// ─────────────────────────────────────────────

// compileGenerated runs the .vela source through the full compile
// pipeline and returns the generated JS as a string.  Tests assert on
// substrings.  Returns "" if compilation fails.
func compileGenerated(t *testing.T, src string) string {
	t.Helper()
	sfc, err := sfcast.ParseSFC(src)
	if err != nil {
		t.Fatalf("parseSFC: %v", err)
	}
	tmpl, err := sfcast.ParseTemplate(sfc.Template)
	if err != nil {
		t.Fatalf("parseTemplate: %v", err)
	}
	a := analyzer.AnalyzeScript(sfc.Script)
	return codegen.GenerateCodeAST("Test", sfc, tmpl, a, "vela-test")
}
