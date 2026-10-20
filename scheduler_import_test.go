package main

import (
	"strings"
	"testing"
)

func TestASTReactiveComponentsImportScheduler(t *testing.T) {
	sigil := string(rune(36))
	src := "<script>let count = " + sigil + "state(0);</script><template><p>{count}</p></template>"
	sfc, err := ParseSFC(src)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := ParseTemplate(sfc.Template)
	if err != nil {
		t.Fatal(err)
	}
	out := generateCodeAST("Scheduler", sfc, nodes, AnalyzeScript(sfc.Script), "vela-test")

	if !strings.Contains(out, sigil+sigil+"defer") || !strings.Contains(out, "from './runtime.js'") {
		t.Fatalf("reactive component must import scheduler from runtime:\n%s", out)
	}
	if strings.Contains(out, "Symbol.for('vela.defer')") {
		t.Fatalf("AST output must not use global scheduler fallback:\n%s", out)
	}
}
