package gen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"path"
	"path/filepath"
	"strconv"
)

// AppImportPath is the import path of the app package that loaders use.
const AppImportPath = "github.com/elauven/vela/app"

// RuntimeImportPath is the import path of the SSR runtime, whose Component
// the route table lists.
const RuntimeImportPath = "github.com/elauven/vela/ssr_runtime"

// LoadFile is what the Go package of a mirrored directory declares
// (docs/APP.md L-1, L-6, L-7). Only the syntax is read: the result type of
// a loader is not resolved, since it reaches the client as JSON.
type LoadFile struct {
	// Dir is the module-relative directory of the package.
	Dir    string
	Page   bool
	Layout bool
	Deps   bool
	Render bool
	// RenderMode is the value of Render when it is written as
	// app.RenderData, app.RenderFragment, app.RenderDocument or
	// app.RenderAuto: "data", "fragment", "document" or "auto"; "" otherwise.
	// routes.js gives it to the client router (task 077).
	RenderMode string
	// PageHead and LayoutHead report that a loader may set the head: its
	// body, or a declaration of the package other than the two loaders,
	// calls a method Head with no arguments, such as r.Head(). A data
	// navigation runs such a loader for the head even when the client keeps
	// its level (N-4).
	PageHead   bool
	LayoutHead bool
	// Actions reports `func Actions() app.Actions`, the form actions of the
	// page (task 078), usually in actions.go.
	Actions bool
}

// Signatures printed by the loader diagnostics (L-1).
const (
	pageSignature   = "func Page(ctx context.Context, r *app.Request) (T, error)"
	layoutSignature = "func Layout(ctx context.Context, r *app.Request) (T, error)"
)

func (t *Tree) readLoad(d *Dir, dir string, files []string, report func(Diagnostic)) *LoadFile {
	moduleDir := path.Join(t.Config.Server, d.GoRel)
	load := &LoadFile{Dir: moduleDir}
	fset := token.NewFileSet()
	for _, name := range files {
		rel := path.Join(moduleDir, name)
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			report(t.goDiagnostic(rel, err))
			continue
		}
		imports := fileImports(file)
		for _, decl := range file.Decls {
			if !callsHead(decl) {
				continue
			}
			switch fn, _ := decl.(*ast.FuncDecl); {
			case fn != nil && fn.Recv == nil && fn.Name.Name == "Page":
				load.PageHead = true
			case fn != nil && fn.Recv == nil && fn.Name.Name == "Layout":
				load.LayoutHead = true
			default:
				// A helper may be called by either loader.
				load.PageHead, load.LayoutHead = true, true
			}
		}
		at := func(node ast.Node) (int, int) {
			p := fset.Position(node.Pos())
			return p.Line, p.Column
		}
		for _, decl := range file.Decls {
			switch decl := decl.(type) {
			case *ast.FuncDecl:
				if decl.Recv != nil {
					continue
				}
				name := decl.Name.Name
				if name == "Actions" {
					line, col := at(decl.Name)
					switch {
					case d.Page == "":
						report(Diagnostic{File: rel, Line: line, Col: col, Code: CodeLoader, Message: fmt.Sprintf("Actions has no Page.vela in %s; actions belong to a page", t.projectRel(d.Rel)), Help: fmt.Sprintf("create %s/Page.vela, or move the actions to the directory of a page", t.projectRel(d.Rel))})
					case !actionsSignature(decl, imports):
						report(Diagnostic{File: rel, Line: line, Col: col, Code: CodeLoader, Message: "Actions has the wrong signature", Help: "declare it as " + actionsSignatureText})
					default:
						load.Actions = true
					}
					continue
				}
				var component, signature string
				switch name {
				case "Page":
					component, signature, load.Page = d.Page, pageSignature, true
				case "Layout":
					component, signature, load.Layout = d.Layout, layoutSignature, true
				default:
					continue
				}
				line, col := at(decl.Name)
				if component == "" {
					report(Diagnostic{File: rel, Line: line, Col: col, Code: CodeLoader, Message: fmt.Sprintf("loader %s has no %s.vela in %s (L-1)", name, name, t.projectRel(d.Rel)), Help: fmt.Sprintf("create %s/%s.vela, or remove the loader", t.projectRel(d.Rel), name)})
					continue
				}
				if !loaderSignature(decl, imports) {
					report(Diagnostic{File: rel, Line: line, Col: col, Code: CodeLoader, Message: fmt.Sprintf("loader %s has the wrong signature (L-1)", name), Help: "declare it as " + signature})
				}
			case *ast.GenDecl:
				for _, spec := range decl.Specs {
					value, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, ident := range value.Names {
						line, col := at(ident)
						switch {
						case ident.Name == "Deps" && decl.Tok == token.VAR:
							load.Deps = true
						case ident.Name == "Deps":
							report(Diagnostic{File: rel, Line: line, Col: col, Code: CodeLoader, Message: "Deps must be a variable of type app.Deps (L-6)", Help: "declare it as var Deps = app.Deps{Query: []string{\"q\"}}"})
						case ident.Name == "Render" && decl.Tok == token.CONST:
							load.Render = true
							load.RenderMode = renderMode(value, ident, imports)
							if d.Page == "" {
								report(Diagnostic{File: rel, Line: line, Col: col, Code: CodeLoader, Message: fmt.Sprintf("Render is declared without a Page.vela in %s; only the page's load.go declares the response mode (L-7)", t.projectRel(d.Rel)), Help: "move it to the load.go of the page"})
							}
						case ident.Name == "Render":
							report(Diagnostic{File: rel, Line: line, Col: col, Code: CodeLoader, Message: "Render must be a constant of type app.RenderMode (L-7)", Help: "declare it as const Render = app.RenderData"})
						}
					}
				}
			}
		}
	}
	return load
}

func (t *Tree) goDiagnostic(rel string, err error) Diagnostic {
	d := Diagnostic{File: rel, Line: 1, Col: 1, Code: CodeLoader, Message: err.Error()}
	if errs, ok := err.(scanner.ErrorList); ok && errs.Len() > 0 {
		first := errs[0]
		d.Line, d.Col, d.Message = first.Pos.Line, first.Pos.Column, first.Msg
	}
	return d
}

// fileImports maps the local names of a file's imports to their paths.
func fileImports(file *ast.File) map[string]string {
	imports := map[string]string{}
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := path.Base(importPath)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		imports[name] = importPath
	}
	return imports
}

// loaderSignature checks func(context.Context, *app.Request) (T, error).
func loaderSignature(decl *ast.FuncDecl, imports map[string]string) bool {
	typ := decl.Type
	if typ.TypeParams != nil && len(typ.TypeParams.List) > 0 {
		return false
	}
	params := fieldTypes(typ.Params)
	results := fieldTypes(typ.Results)
	if len(params) != 2 || len(results) != 2 {
		return false
	}
	if !selector(params[0], imports, "context", "Context") {
		return false
	}
	star, ok := params[1].(*ast.StarExpr)
	if !ok || !selector(star.X, imports, AppImportPath, "Request") {
		return false
	}
	errType, ok := results[1].(*ast.Ident)
	return ok && errType.Name == "error"
}

// actionsSignatureText is printed by the Actions diagnostic.
const actionsSignatureText = "func Actions() app.Actions"

// actionsSignature checks func Actions() app.Actions.
func actionsSignature(decl *ast.FuncDecl, imports map[string]string) bool {
	typ := decl.Type
	if typ.TypeParams != nil && len(typ.TypeParams.List) > 0 {
		return false
	}
	results := fieldTypes(typ.Results)
	return len(fieldTypes(typ.Params)) == 0 && len(results) == 1 && selector(results[0], imports, AppImportPath, "Actions")
}

// fieldTypes lists one type per parameter or result.
func fieldTypes(fields *ast.FieldList) []ast.Expr {
	if fields == nil {
		return nil
	}
	var types []ast.Expr
	for _, field := range fields.List {
		n := max(len(field.Names), 1)
		for range n {
			types = append(types, field.Type)
		}
	}
	return types
}

func selector(expr ast.Expr, imports map[string]string, importPath, name string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && imports[pkg.Name] == importPath
}

// callsHead reports a call x.Head() anywhere in a declaration.
func callsHead(decl ast.Decl) bool {
	found := false
	ast.Inspect(decl, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && len(call.Args) == 0 {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Head" {
				found = true
			}
		}
		return !found
	})
	return found
}

// renderMode reads `const Render = app.RenderX` (L-7).
func renderMode(spec *ast.ValueSpec, ident *ast.Ident, imports map[string]string) string {
	for i, name := range spec.Names {
		if name != ident || i >= len(spec.Values) {
			continue
		}
		for value, mode := range map[string]string{"RenderAuto": "auto", "RenderData": "data", "RenderFragment": "fragment", "RenderDocument": "document"} {
			if selector(spec.Values[i], imports, AppImportPath, value) {
				return mode
			}
		}
	}
	return ""
}
