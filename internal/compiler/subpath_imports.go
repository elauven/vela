package compiler

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/elauven/vela/internal/codegen"
	"github.com/elauven/vela/internal/diag"
	"github.com/elauven/vela/internal/jsast"
	"github.com/elauven/vela/internal/project"
	"github.com/elauven/vela/internal/sfcast"
)

// # imports (task 090, project.ResolveSubpathImport). Every build checks
// that a # specifier resolves to a module of the package; the generated
// JavaScript keeps it as written.

// subpathImportProblem returns why spec, imported by a file in dir, cannot
// be used, or "".
func subpathImportProblem(dir, spec string) string {
	resolved, err := project.ResolveSubpathImport(dir, spec)
	if err != nil {
		return err.Error()
	}
	if strings.HasSuffix(resolved.File, ".vela") {
		return fmt.Sprintf("%s: resolves to the component %s; import components by their relative .vela path", spec, filepath.ToSlash(resolved.File))
	}
	return ""
}

// checkScriptSubpathImports reports the # imports of a component script
// that do not resolve.
func checkScriptSubpathImports(src string, script *sfcast.ScriptAST, ds *diag.DiagSet) {
	if script == nil || script.Program == nil {
		return
	}
	ds.PushOffset(script.Span.Start)
	defer ds.PopOffset()
	jsast.WalkNode(script.Program, func(n jsast.JsNode) bool {
		spec := ""
		switch x := n.(type) {
		case *jsast.JsImportDecl:
			spec = x.Source
		case *jsast.JsExportDecl:
			if x.HasSource {
				spec = x.Source
			}
		default:
			return true
		}
		if project.IsSubpathImport(spec) {
			if problem := subpathImportProblem(filepath.Dir(src), spec); problem != "" {
				ds.Report(diag.SevError, n.NodePos(), n.NodeEnd(), project.SubpathImportCode, "Map the specifier to a module of the package in the \"imports\" field of package.json, for example \"#app/*\": \"./src/_app/*\".", "%s", problem)
			}
		}
		return false
	})
}

// checkModuleSubpathImports reports the # imports of a copied module that
// do not resolve and returns whether all of them do.
func checkModuleSubpathImports(src, source string, opts CompileOptions) bool {
	ok := true
	for _, spec := range codegen.ModuleSpecifiers(source) {
		if !project.IsSubpathImport(spec.Value) {
			continue
		}
		problem := subpathImportProblem(filepath.Dir(src), spec.Value)
		if problem == "" {
			continue
		}
		ok = false
		column := spec.Pos - strings.LastIndex(source[:spec.Pos], "\n")
		printModuleDiagnostics(opts, []*diag.Diagnostic{{Sev: diag.SevError, File: cwdRelative(src), Line: spec.Line, Col: column, Code: project.SubpathImportCode, Message: problem,
			Suggestion: "map the specifier to a module of the package in the \"imports\" field of package.json"}})
	}
	return ok
}
