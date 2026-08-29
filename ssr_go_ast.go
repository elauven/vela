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
			goImportDecl("io"),
			goPropsTypeDecl(propsName, plan.Props),
			goEmptyStructTypeDecl(slotsName),
			goRenderFuncDecl(component, propsName, slotsName),
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
	config := types.Config{Importer: importer.Default()}
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
