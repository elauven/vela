package compiler

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/elauven/vela/internal/diag"
	"github.com/elauven/vela/internal/jsast"
	"github.com/elauven/vela/internal/project"
)

// TypeScript checks of the compiler (task 081, docs/TYPESCRIPT.md).

// TypeImportDiagnosticCode reports an import without `type` whose bindings
// are used only in type positions (TS-3.3).
const TypeImportDiagnosticCode = "VELA-TS-TYPE-IMPORT"

// typeOnlyImport is a binding of a value import used only as a type.
type typeOnlyImport struct {
	Local, Source string
	Pos, End      int // the name in the import declaration
}

// typeOnlyImports lists the bindings of the value imports of prog (a
// TypeScript program) that are referenced in type positions and nowhere
// else: not as a value of the program and, for a component, not as a word
// of the template text (a component tag, an expression).
//
// The import is kept as written (verbatimModuleSyntax, TS-3.2): for an
// interface or a type alias the browser would find no such export. A class
// used only as a type does exist at run time, which is why this is a
// warning: the compiler resolves no types (TS-3.1) and cannot tell them
// apart; `vela check --types` reports the real errors (TS1484).
func typeOnlyImports(src string, prog *jsast.JsProgram, templateText string) []typeOnlyImport {
	if prog == nil || prog.TS == nil || len(prog.TS.Refs) == 0 {
		return nil
	}
	typeRefs := map[string]bool{}
	queried := map[string]bool{}
	for _, r := range prog.TS.Refs {
		if r.Query {
			queried[r.Name] = true // `typeof x` names a value
		} else {
			typeRefs[r.Name] = true
		}
	}
	var candidates []typeOnlyImport
	var names []string
	for _, stmt := range prog.Body {
		imp, ok := stmt.(*jsast.JsImportDecl)
		if !ok {
			continue
		}
		add := func(local string) {
			if local == "" || !typeRefs[local] || queried[local] {
				return
			}
			pos, end := imp.Pos, imp.End
			if imp.End <= len(src) {
				if i := indexWord(src[imp.Pos:imp.End], local); i >= 0 {
					pos, end = imp.Pos+i, imp.Pos+i+len(local)
				}
			}
			candidates = append(candidates, typeOnlyImport{Local: local, Source: imp.Source, Pos: pos, End: end})
			names = append(names, local)
		}
		add(imp.DefaultName)
		add(imp.Namespace)
		for _, s := range imp.Specifiers {
			add(s.Local)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	used := map[string]bool{}
	for _, name := range jsast.FindDepsInNode(prog, names) {
		used[name] = true
	}
	var out []typeOnlyImport
	for _, c := range candidates {
		if used[c.Local] || indexWord(templateText, c.Local) >= 0 {
			continue
		}
		out = append(out, c)
	}
	return out
}

// reportTypeOnlyImports adds VELA-TS-TYPE-IMPORT warnings; positions are
// relative to src, which the caller has offset in ds.
func reportTypeOnlyImports(ds *diag.DiagSet, src string, prog *jsast.JsProgram, templateText string) {
	for _, imp := range typeOnlyImports(src, prog, templateText) {
		ds.Report(diag.SevWarning, imp.Pos, imp.End, TypeImportDiagnosticCode,
			"Import it with `import type { "+imp.Local+" }` or `{ type "+imp.Local+" }`.",
			"%q from %q is imported as a value but used only as a type; the import is kept and fails in the browser unless %q exports a value of that name",
			imp.Local, imp.Source, imp.Source)
	}
}

// indexWord returns the offset of name in s as a whole identifier, or -1.
func indexWord(s, name string) int {
	for from := 0; from < len(s); {
		i := strings.Index(s[from:], name)
		if i < 0 {
			return -1
		}
		i += from
		before := i == 0 || !jsast.IsIdentCont(s[i-1])
		after := i+len(name) >= len(s) || !jsast.IsIdentCont(s[i+len(name)])
		if before && after {
			return i
		}
		from = i + 1
	}
	return -1
}

// TypeExportDiagnosticCode reports a local type exported without `type`
// (TS-3.4): `export { Props }` of an interface exports nothing at run time,
// and the module fails to load.
const TypeExportDiagnosticCode = "VELA-TS-TYPE-EXPORT"

// ModuleDiagnosticCode reports a .tsx, .jsx or .cts module (TS-5.2).
const ModuleDiagnosticCode = "VELA-TS-MODULE"

// reportTypeOnlyExports adds a VELA-TS-TYPE-EXPORT error for each name of a
// local export list (no `from`) that the module declares only as a type.
func reportTypeOnlyExports(ds *diag.DiagSet, src string, prog *jsast.JsProgram) {
	if prog == nil || prog.TS == nil {
		return
	}
	types := map[string]bool{}
	for _, d := range prog.TS.Decls {
		switch d.Kind {
		case jsast.TSDeclInterface, jsast.TSDeclTypeAlias:
			types[d.Name] = true
		}
	}
	if len(types) == 0 {
		return
	}
	values := map[string]bool{}
	for _, stmt := range prog.Body {
		if exp, ok := stmt.(*jsast.JsExportDecl); ok && exp.Declaration != nil {
			stmt = exp.Declaration
		}
		switch d := stmt.(type) {
		case *jsast.JsVarDecl:
			for _, decl := range d.Decls {
				jsast.WalkNode(decl.Id, func(n jsast.JsNode) bool {
					if id, ok := n.(*jsast.JsIdent); ok {
						values[id.Name] = true // over-approximates: keys too
					}
					return true
				})
			}
		case *jsast.JsFuncDecl:
			values[d.Name] = true
		case *jsast.JsClassDecl:
			values[d.Name] = true
		case *jsast.JsImportDecl:
			values[d.DefaultName], values[d.Namespace] = true, true
			for _, s := range d.Specifiers {
				values[s.Local] = true
			}
		}
	}
	for _, stmt := range prog.Body {
		exp, ok := stmt.(*jsast.JsExportDecl)
		if !ok || exp.HasSource || exp.Declaration != nil {
			continue
		}
		for _, spec := range exp.Specifiers {
			if !types[spec.Local] || values[spec.Local] {
				continue
			}
			pos, end := exp.Pos, exp.End
			if exp.End <= len(src) {
				if i := indexWord(src[exp.Pos:exp.End], spec.Local); i >= 0 {
					pos, end = exp.Pos+i, exp.Pos+i+len(spec.Local)
				}
			}
			ds.Report(diag.SevError, pos, end, TypeExportDiagnosticCode,
				"Export it with `export type { "+spec.Local+" }` or `{ type "+spec.Local+" }`.",
				"%q is a type: exported without `type`, the module would export a value that does not exist", spec.Local)
		}
	}
}

// compileTypeScriptModule turns a local .ts module of a project into
// JavaScript (TS-5.1): the source with its types blanked out, so lines and
// columns stay those of the source. ok is false with error diagnostics.
func compileTypeScriptModule(file, source string) (js string, diags []*diag.Diagnostic, ok bool) {
	ds := diag.NewDiagSet(cwdRelative(file), source)
	text, prog, parseDiags := jsast.EraseTypeScript(source)
	if len(parseDiags) == 0 {
		parseDiags = jsast.EarlyErrors(prog, jsast.EarlyOptions{TopLevelAwait: true})
	}
	for _, d := range parseDiags {
		code, hint := "VELA-JS-SYNTAX", "fix the TypeScript syntax"
		if d.Code == jsast.DiagTSNonErasable {
			code, hint = "VELA-TS-NON-ERASABLE", scriptDiagnosticHint("VELA-TS-NON-ERASABLE")
		}
		ds.ErrorWith(d.Pos, code, hint, "%s", d.Message)
	}
	if len(parseDiags) == 0 {
		reportTypeOnlyExports(ds, source, prog)
		reportTypeOnlyImports(ds, source, prog, "")
	}
	return trimTrailingSpaces(text), ds.All(), !ds.HasErrors()
}

// trimTrailingSpaces drops the spaces blanking leaves at the end of lines;
// every line and every column of code keeps its place.
func trimTrailingSpaces(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		cr := strings.HasSuffix(line, "\r")
		line = strings.TrimRight(strings.TrimSuffix(line, "\r"), " \t")
		if cr {
			line += "\r"
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

// printModuleDiagnostics prints and records the diagnostics of a module the
// way CompilePrint does for a component.
func printModuleDiagnostics(opts CompileOptions, diags []*diag.Diagnostic) {
	opts.Report.add(diags...)
	for _, d := range diags {
		switch {
		case opts.DiagnosticsJSON:
			line, _ := json.Marshal(d.Machine())
			fmt.Fprintf(ErrorWriter(opts), "%s\n", line)
		case d.Sev == diag.SevError:
			fmt.Fprintf(ErrorWriter(opts), "       %s\n", d)
		default:
			fmt.Fprintf(CompilerStatusWriter(opts), "       %s\n", d)
		}
	}
}

// reportUnsupportedModules reports the .tsx, .jsx and .cts modules of a
// project (TS-5.2) and returns their number.
func reportUnsupportedModules(srcRoot string, opts CompileOptions) int {
	files := project.CollectUnsupportedModules(srcRoot)
	for _, file := range files {
		d := &diag.Diagnostic{Sev: diag.SevError, File: cwdRelative(file), Line: 1, Col: 1, Code: ModuleDiagnosticCode,
			Message:    fmt.Sprintf("%s modules are not supported: write a .ts module (JSX is not part of Vela)", filepath.Ext(file)),
			Suggestion: "rename it to .ts and remove JSX, or move it out of the project directory"}
		printModuleDiagnostics(opts, []*diag.Diagnostic{d})
	}
	return len(files)
}

// GenericsDiagnosticCode reports an invalid `generics` attribute (TS-4.8).
const GenericsDiagnosticCode = "VELA-TS-GENERICS"

// genericsProblem checks the `generics` attribute of a TypeScript script: a
// list of type parameters as written inside `<…>`. It returns the offset of
// the problem in the attribute value and a message, or ok.
func genericsProblem(generics string) (pos int, msg string, ok bool) {
	if strings.TrimSpace(generics) == "" {
		return 0, "the generics attribute is empty", false
	}
	const prefix = "function f<"
	prog, diags := jsast.ParseWithOptions(prefix+generics+">() {}", jsast.ParseOptions{TypeScript: true})
	if len(diags) > 0 {
		at := min(max(diags[0].Pos-len(prefix), 0), len(generics))
		return at, diags[0].Message, false
	}
	if prog == nil || len(prog.Body) != 1 {
		return 0, "the generics attribute must be a list of type parameters", false
	}
	if fn, isFn := prog.Body[0].(*jsast.JsFuncDecl); !isFn || fn.TS == nil || len(fn.TS.TypeParams) == 0 {
		return 0, "the generics attribute must be a list of type parameters", false
	}
	return 0, "", true
}
