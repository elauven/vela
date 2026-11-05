package compiler

import (
	"strings"

	"github.com/elauven/vela/internal/diag"
	"github.com/elauven/vela/internal/jsast"
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
