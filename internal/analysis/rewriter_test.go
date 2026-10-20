package analysis

import (
	"strings"
	"testing"
)

func TestRewriteTemplateHandlerAssignments(t *testing.T) {
	analysis := AnalyzeScript(`let count = $state(0); let label = $state("");`)

	got := rewriteTemplateHandlerAssignments(
		`{($event) => count = $event.target.value}`, analysis)
	want := `($event) => $$set_count($event.target.value)`
	if got != want {
		t.Fatalf("concise assignment = %q, want %q", got, want)
	}

	got = rewriteTemplateHandlerAssignments(
		`{() => { count++; label = "ready"; }}`, analysis)
	for _, fragment := range []string{
		`$$set_count(count+1)`,
		`$$set_label("ready")`,
	} {
		if !strings.Contains(got, fragment) {
			t.Errorf("block handler = %q, want fragment %q", got, fragment)
		}
	}
}
