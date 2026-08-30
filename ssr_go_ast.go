package main

import (
	"bytes"
	"fmt"
	goast "go/ast"
	"go/format"
	"go/importer"
	goparser "go/parser"
	"go/token"
	"go/types"
	"strings"
	"unicode"
)

const SSRRuntimeImportPath = "github.com/elauven/vela/ssr_runtime"

// BuildSSRGoSkeleton creates the first server renderer file entirely through
// the standard Go AST. Later SSR stages extend the function body from the
// typed SSRRenderPlan; they must not switch to source-string generation.
func BuildSSRGoSkeleton(plan *SSRRenderPlan, packageName string) (*goast.File, error) {
	if plan == nil {
		return nil, fmt.Errorf("ssr go AST: missing render plan")
	}
	component := strings.TrimSpace(plan.ComponentName)
	if !isGoExportedIdentifier(component) {
		return nil, fmt.Errorf("ssr go AST: component name %q is not an exported Go identifier", component)
	}
	packageName = strings.TrimSpace(packageName)
	if !isGoPackageIdentifier(packageName) {
		return nil, fmt.Errorf("ssr go AST: package name %q is not a valid Go identifier", packageName)
	}

	propsName := component + "Props"
	slotsName := component + "Slots"
	file := &goast.File{
		Name: goast.NewIdent(packageName),
		Decls: []goast.Decl{
			goSSRImportDecl(),
			goPropsTypeDecl(propsName, plan.Props),
			goEmptyStructTypeDecl(slotsName),
			goRenderFuncDecl(component, propsName, slotsName),
			goComponentRendererFuncDecl(component, propsName, slotsName),
		},
	}
	return file, nil
}

func goImportDecl(path string) *goast.GenDecl {
	return &goast.GenDecl{
		Tok: token.IMPORT,
		Specs: []goast.Spec{&goast.ImportSpec{
			Path: &goast.BasicLit{Kind: token.STRING, Value: fmt.Sprintf("%q", path)},
		}},
	}
}

func goSSRImportDecl() *goast.GenDecl {
	decl := goImportDecl("io")
	decl.Specs = append(decl.Specs, &goast.ImportSpec{
		Name: goast.NewIdent("vela"),
		Path: &goast.BasicLit{Kind: token.STRING, Value: fmt.Sprintf("%q", SSRRuntimeImportPath)},
	})
	return decl
}

func goEmptyStructTypeDecl(name string) *goast.GenDecl {
	return &goast.GenDecl{
		Tok: token.TYPE,
		Specs: []goast.Spec{&goast.TypeSpec{
			Name: goast.NewIdent(name),
			Type: &goast.StructType{Fields: &goast.FieldList{}},
		}},
	}
}

func goRenderFuncDecl(component, propsName, slotsName string) *goast.FuncDecl {
	return &goast.FuncDecl{
		Name: goast.NewIdent("Render" + component),
		Type: &goast.FuncType{
			Params: &goast.FieldList{List: []*goast.Field{
				{Names: []*goast.Ident{goast.NewIdent("w")}, Type: goSelector("io", "Writer")},
				{Names: []*goast.Ident{goast.NewIdent("props")}, Type: goast.NewIdent(propsName)},
				{Names: []*goast.Ident{goast.NewIdent("slots")}, Type: goast.NewIdent(slotsName)},
			}},
			Results: &goast.FieldList{List: []*goast.Field{{Type: goast.NewIdent("error")}}},
		},
		Body: &goast.BlockStmt{List: []goast.Stmt{
			&goast.ReturnStmt{Results: []goast.Expr{goast.NewIdent("nil")}},
		}},
	}
}

func goComponentRendererFuncDecl(component, propsName, slotsName string) *goast.FuncDecl {
	wrapper := &goast.FuncLit{
		Type: &goast.FuncType{
			Params:  &goast.FieldList{List: []*goast.Field{{Names: []*goast.Ident{goast.NewIdent("w")}, Type: goSelector("io", "Writer")}}},
			Results: &goast.FieldList{List: []*goast.Field{{Type: goast.NewIdent("error")}}},
		},
		Body: &goast.BlockStmt{List: []goast.Stmt{&goast.ReturnStmt{Results: []goast.Expr{
			&goast.CallExpr{Fun: goast.NewIdent("Render" + component), Args: []goast.Expr{goast.NewIdent("w"), goast.NewIdent("props"), goast.NewIdent("slots")}},
		}}}},
	}
	return &goast.FuncDecl{
		Name: goast.NewIdent(component),
		Type: &goast.FuncType{
			Params: &goast.FieldList{List: []*goast.Field{
				{Names: []*goast.Ident{goast.NewIdent("props")}, Type: goast.NewIdent(propsName)},
				{Names: []*goast.Ident{goast.NewIdent("slots")}, Type: goast.NewIdent(slotsName)},
			}},
			Results: &goast.FieldList{List: []*goast.Field{{Type: goSelector("vela", "Renderer")}}},
		},
		Body: &goast.BlockStmt{List: []goast.Stmt{&goast.ReturnStmt{Results: []goast.Expr{
			&goast.CallExpr{Fun: goSelector("vela", "RendererFunc"), Args: []goast.Expr{wrapper}},
		}}}},
	}
}

func goSelector(pkg, name string) goast.Expr {
	return &goast.SelectorExpr{X: goast.NewIdent(pkg), Sel: goast.NewIdent(name)}
}

// FormatSSRGoFile is the sole printer for compiler-owned Go output.
func FormatSSRGoFile(file *goast.File) ([]byte, error) {
	if file == nil {
		return nil, fmt.Errorf("ssr go AST: cannot format nil file")
	}
	var out bytes.Buffer
	if err := format.Node(&out, token.NewFileSet(), file); err != nil {
		return nil, fmt.Errorf("ssr go AST: format: %w", err)
	}
	return out.Bytes(), nil
}

// ValidateGeneratedSSRGo independently reparses and type-checks the printed
// result. This protects the AST builder boundary before generated files are
// written into a user project.
func ValidateGeneratedSSRGo(filename string, source []byte) error {
	fset := token.NewFileSet()
	file, err := goparser.ParseFile(fset, filename, source, goparser.AllErrors)
	if err != nil {
		return fmt.Errorf("ssr go AST: parse generated Go: %w", err)
	}
	config := types.Config{Importer: ssrGeneratedImporter{fallback: importer.Default()}}
	if _, err := config.Check("vela/generated", fset, []*goast.File{file}, nil); err != nil {
		return fmt.Errorf("ssr go AST: type-check generated Go: %w", err)
	}
	return nil
}

func isGoExportedIdentifier(name string) bool {
	if name == "" || !token.IsIdentifier(name) {
		return false
	}
	first, _ := utf8FirstRune(name)
	return unicode.IsUpper(first)
}

func isGoPackageIdentifier(name string) bool {
	return name != "" && token.IsIdentifier(name) && !token.IsKeyword(name)
}

func utf8FirstRune(value string) (rune, int) {
	for _, r := range value {
		return r, len(string(r))
	}
	return 0, 0
}

func goPropsTypeDecl(name string, props []SSRPlanProp) *goast.GenDecl {
	fields := make([]*goast.Field, 0, len(props))
	for _, prop := range props {
		fields = append(fields, &goast.Field{
			Names: []*goast.Ident{goast.NewIdent(prop.GoName)},
			Type:  goSSRTypeExpr(prop.Type),
		})
	}
	return &goast.GenDecl{
		Tok: token.TYPE,
		Specs: []goast.Spec{&goast.TypeSpec{
			Name: goast.NewIdent(name),
			Type: &goast.StructType{Fields: &goast.FieldList{List: fields}},
		}},
	}
}

func goSSRTypeExpr(kind SSRValueType) goast.Expr {
	switch kind {
	case SSRTypeString:
		return goast.NewIdent("string")
	case SSRTypeBool:
		return goast.NewIdent("bool")
	case SSRTypeInt:
		return goast.NewIdent("int")
	case SSRTypeFloat:
		return goast.NewIdent("float64")
	case SSRTypeSlice:
		return &goast.ArrayType{Elt: goast.NewIdent("any")}
	case SSRTypeObject:
		return &goast.MapType{Key: goast.NewIdent("string"), Value: goast.NewIdent("any")}
	default:
		return goast.NewIdent("any")
	}
}

type ssrGeneratedImporter struct {
	fallback types.Importer
}

func (importer ssrGeneratedImporter) Import(path string) (*types.Package, error) {
	if path == SSRRuntimeImportPath {
		return ssrRuntimeTypesPackage(importer.fallback)
	}
	return importer.fallback.Import(path)
}

func ssrRuntimeTypesPackage(loader types.Importer) (*types.Package, error) {
	pkg := types.NewPackage(SSRRuntimeImportPath, "ssr_runtime")
	ioPkg, err := loader.Import("io")
	if err != nil {
		return nil, err
	}
	writer := ioPkg.Scope().Lookup("Writer").Type()
	errorType := types.Universe.Lookup("error").Type()
	stringType := types.Typ[types.String]
	anyType := types.Universe.Lookup("any").Type()
	renderSignature := ssrRuntimeSignature(pkg, nil, []*types.Var{types.NewVar(token.NoPos, pkg, "w", writer)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", errorType)})
	rendererMethod := types.NewFunc(token.NoPos, pkg, "Render", renderSignature)
	renderer := types.NewInterfaceType([]*types.Func{rendererMethod}, nil)
	renderer.Complete()
	pkg.Scope().Insert(types.NewTypeName(token.NoPos, pkg, "Renderer", renderer))

	rendererFuncName := types.NewTypeName(token.NoPos, pkg, "RendererFunc", nil)
	rendererFunc := types.NewNamed(rendererFuncName, renderSignature, nil)
	pkg.Scope().Insert(rendererFuncName)
	receiver := types.NewVar(token.NoPos, pkg, "render", rendererFunc)
	rendererFunc.AddMethod(types.NewFunc(token.NoPos, pkg, "Render", ssrRuntimeSignature(pkg, receiver, []*types.Var{types.NewVar(token.NoPos, pkg, "w", writer)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", errorType)})))

	slotName := types.NewTypeName(token.NoPos, pkg, "Slot", nil)
	types.NewNamed(slotName, renderSignature, nil)
	pkg.Scope().Insert(slotName)
	ssrRuntimeAddFunc(pkg, "WriteString", []*types.Var{types.NewVar(token.NoPos, pkg, "w", writer), types.NewVar(token.NoPos, pkg, "value", stringType)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", errorType)})
	ssrRuntimeAddFunc(pkg, "String", []*types.Var{types.NewVar(token.NoPos, pkg, "value", anyType)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", stringType)})
	ssrRuntimeAddFunc(pkg, "EscapeString", []*types.Var{types.NewVar(token.NoPos, pkg, "value", stringType)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", stringType)})
	pkg.MarkComplete()
	return pkg, nil
}

func ssrRuntimeAddFunc(pkg *types.Package, name string, params, results []*types.Var) {
	pkg.Scope().Insert(types.NewFunc(token.NoPos, pkg, name, ssrRuntimeSignature(pkg, nil, params, results)))
}

func ssrRuntimeSignature(pkg *types.Package, receiver *types.Var, params, results []*types.Var) *types.Signature {
	return types.NewSignatureType(receiver, nil, nil, types.NewTuple(params...), types.NewTuple(results...), false)
}

func ssrRenderFunc(file *goast.File, component string) (*goast.FuncDecl, error) {
	if file == nil {
		return nil, fmt.Errorf("ssr go AST: missing generated file")
	}
	name := "Render" + component
	for _, declaration := range file.Decls {
		function, ok := declaration.(*goast.FuncDecl)
		if ok && function.Name != nil && function.Name.Name == name {
			return function, nil
		}
	}
	return nil, fmt.Errorf("ssr go AST: missing %s declaration", name)
}
