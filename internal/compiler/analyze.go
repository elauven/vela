package compiler

import (
	"github.com/elauven/vela/internal/analyzer"
	"github.com/elauven/vela/internal/diag"
	"github.com/elauven/vela/internal/jsast"
	"github.com/elauven/vela/internal/sfcast"

	"fmt"
)

// ComponentAnalysis is a component as the language server sees it while it
// is being written (task 065): the sections parse and the script is
// analyzed independently, so a broken template keeps the script analysis
// and a script syntax error keeps the template. Positions of Script and
// Scope are relative to SFC.ScriptOffset, template nodes carry spans of
// the component source.
type ComponentAnalysis struct {
	SFC *sfcast.SFC
	// Template is the template when it parses and validates.
	Template []sfcast.Node
	// Script is the parsed script; with syntax errors, the parser's
	// recovered program.
	Script   *jsast.JsProgram
	Analysis *analyzer.Analysis
	Scope    *analyzer.Scope
	Imports  []ModuleImport
	// ScriptComplete and TemplateComplete report the sections that parsed
	// without errors. The analysis of an incomplete section is partial; a
	// client keeps the last complete one for what it misses.
	ScriptComplete   bool
	TemplateComplete bool
	// Err is an internal failure of the analysis; the fields set before it
	// remain valid.
	Err error
}

// AnalyzeComponent analyzes the component src with the given source. It
// reports no diagnostics: those come from compiling the component, as the
// build reports them.
func AnalyzeComponent(src, source string, opts CompileOptions) (a *ComponentAnalysis) {
	a = &ComponentAnalysis{}
	defer func() {
		if r := recover(); r != nil {
			a.Err = fmt.Errorf("internal compiler error while analyzing this component: %v", r)
		}
	}()
	sfc, _ := sfcast.ParseSFC(source)
	a.SFC = sfc
	unclosed := map[string]bool{}
	for _, section := range sfc.Unclosed {
		unclosed[section] = true
	}
	if sfc.TemplateAST != nil && len(sfc.TemplateAST.Diagnostics) == 0 && len(sfcast.ValidateTemplateAST(sfc.TemplateAST)) == 0 {
		a.Template, a.TemplateComplete = sfc.TemplateAST.Nodes, !unclosed["template"]
	}
	if sfc.ScriptAST == nil || sfc.ScriptAST.Program == nil {
		return a
	}
	a.Script = sfc.ScriptAST.Program
	a.ScriptComplete = len(sfc.ScriptAST.Diagnostics) == 0 && !unclosed["script"]
	a.Imports = sourceModuleImports(src, a.Script)
	a.Analysis = analyzer.AnalyzeScriptAST(sfc.ScriptAST)
	// The scope graph reports the diagnostics of a build; they are not
	// needed here.
	a.Scope = analyzer.BuildScopesWithOptions(a.Script, diag.NewDiagSet(src, source), analyzer.ScopeOptions{
		TargetProfile:           opts.TargetProfile,
		UnavailableCapabilities: opts.UnavailableCapabilities,
		Strict:                  opts.Strict,
	})
	return a
}
