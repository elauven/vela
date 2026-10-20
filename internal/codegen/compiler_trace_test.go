package codegen

import (
	"strings"
	"testing"

	"github.com/elauven/vela/internal/analyzer"
	"github.com/elauven/vela/internal/jsast"
	"github.com/elauven/vela/internal/sfcast"
)

func TestScriptPlanRetainsOwnedUserStatementUntilTargetBuild(t *testing.T) {
	sfc, err := sfcast.ParseSFC(`<script>
  let count = $state(0);
  function increment() { count++; }
</script><template><button>{count}</button></template>`)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := sfcast.ParseTemplate(sfc.Template)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzer.AnalyzeScriptAST(sfc.ScriptAST)
	var user *analyzer.ScriptPlanStep
	for i := range analysis.ScriptPlan {
		if analysis.ScriptPlan[i].Kind == "user" {
			user = &analysis.ScriptPlan[i]
			break
		}
	}
	if user == nil || user.Stmt != sfc.ScriptAST.Program.Body[1] {
		t.Fatalf("ScriptPlan did not retain the owned function statement: %+v", user)
	}
	if user.SourcePos != user.Stmt.NodePos() {
		t.Fatalf("user source position = %d, want AST position %d", user.SourcePos, user.Stmt.NodePos())
	}
	if strings.Contains(jsast.PrintJS(&jsast.JsProgram{Body: []jsast.JsStmt{user.Stmt}}, jsast.PrintPretty), "$$set_count") {
		t.Fatal("source ScriptAST was lowered before input validation")
	}

	target := BuildProgramUnvalidated("Plan", sfc, nodes, analysis, "vela-test", false)
	if !strings.Contains(jsast.PrintJS(target, jsast.PrintPretty), "$$set_count(count + 1)") {
		t.Fatal("target build did not lower ScriptPlan state write")
	}
}
