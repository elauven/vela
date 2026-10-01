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
	"sort"
	"strings"
	"unicode"
)

const SSRRuntimeImportPath = "github.com/elauven/vela/ssr_runtime"

// BuildSSRGoSkeleton creates the first server renderer file entirely through
// the standard Go AST. Later SSR stages extend the function body from the
// typed SSRRenderPlan; they must not switch to source-string generation.
func BuildSSRGoSkeleton(plan *SSRRenderPlan, packageName string) (*goast.File, error) {
	return buildSSRGoSkeleton(plan, packageName, true)
}

// BuildSSRGoRendererSkeleton creates the renderer half of a project output.
// Its Props declaration is emitted separately into the user-preservable
// *_props.go sidecar.
func BuildSSRGoRendererSkeleton(plan *SSRRenderPlan, packageName string) (*goast.File, error) {
	return buildSSRGoSkeleton(plan, packageName, false)
}

func buildSSRGoSkeleton(plan *SSRRenderPlan, packageName string, includeProps bool) (*goast.File, error) {
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
	awaitSnapshotName := component + "AwaitSnapshot"
	renderOptionsName := component + "RenderOptions"
	decls := []goast.Decl{goSSRImportDecl()}
	if includeProps {
		decls = append(decls, goPropsTypeDecl(propsName, plan.Props))
	}
	decls = append(decls,
		goSlotsTypeDecl(slotsName, plan.SlotNames),
		goAwaitSnapshotTypeDecl(awaitSnapshotName),
		goRenderOptionsTypeDecl(renderOptionsName, awaitSnapshotName),
		goRenderWithOptionsAtFuncDecl(component, propsName, slotsName, renderOptionsName),
		goRenderAtFuncDecl(component, propsName, slotsName, renderOptionsName),
		goRenderWithOptionsFuncDecl(component, propsName, slotsName, renderOptionsName, plan.ScopeID),
		goRenderFuncDecl(component, propsName, slotsName, plan.ScopeID),
		goComponentRendererAtFuncDecl(component, propsName, slotsName),
		goComponentRendererFuncDecl(component, propsName, slotsName, plan.ScopeID),
	)
	file := &goast.File{Name: goast.NewIdent(packageName), Decls: decls}
	return file, nil
}

// goAwaitSnapshotTypeDecl declares the server-owned result of one await
// boundary. The compiler never evaluates JavaScript promises: an application
// data loader explicitly chooses pending, then, or catch for a canonical
// marker ID before invoking the renderer.
func goAwaitSnapshotTypeDecl(name string) *goast.GenDecl {
	return &goast.GenDecl{Tok: token.TYPE, Specs: []goast.Spec{&goast.TypeSpec{
		Name: goast.NewIdent(name),
		Type: &goast.StructType{Fields: &goast.FieldList{List: []*goast.Field{
			{Names: []*goast.Ident{goast.NewIdent("State")}, Type: goast.NewIdent("string"), Tag: &goast.BasicLit{Kind: token.STRING, Value: "`json:\"state\"`"}},
			{Names: []*goast.Ident{goast.NewIdent("Value")}, Type: goast.NewIdent("string"), Tag: &goast.BasicLit{Kind: token.STRING, Value: "`json:\"value,omitempty\"`"}},
			{Names: []*goast.Ident{goast.NewIdent("Error")}, Type: goast.NewIdent("string"), Tag: &goast.BasicLit{Kind: token.STRING, Value: "`json:\"error,omitempty\"`"}},
		}}},
	}}}
}

func goRenderOptionsTypeDecl(name, awaitSnapshotName string) *goast.GenDecl {
	return &goast.GenDecl{Tok: token.TYPE, Specs: []goast.Spec{&goast.TypeSpec{
		Name: goast.NewIdent(name),
		Type: &goast.StructType{Fields: &goast.FieldList{List: []*goast.Field{
			{Names: []*goast.Ident{goast.NewIdent("Await")}, Type: &goast.MapType{Key: goast.NewIdent("int"), Value: goast.NewIdent(awaitSnapshotName)}, Tag: &goast.BasicLit{Kind: token.STRING, Value: "`json:\"await,omitempty\"`"}},
			// AwaitAt addresses one await snapshot within a runtime structural
			// instance. It is required when the same await marker occurs in
			// several keyed rows; Await remains the backwards-compatible root
			// snapshot fallback.
			{Names: []*goast.Ident{goast.NewIdent("AwaitAt")}, Type: &goast.MapType{Key: goast.NewIdent("string"), Value: goast.NewIdent(awaitSnapshotName)}, Tag: &goast.BasicLit{Kind: token.STRING, Value: "`json:\"awaitAt,omitempty\"`"}},
		}}},
	}}}
}

// BuildSSRGoPropsFile emits the inferred prop contract as its own Go file.
// Project generation creates it once and preserves it on later builds, so a
// developer can refine fields without generated output overwriting the edit.
func BuildSSRGoPropsFile(plan *SSRRenderPlan, packageName string) (*goast.File, error) {
	if _, err := buildSSRGoSkeleton(plan, packageName, false); err != nil {
		return nil, err
	}
	return &goast.File{
		Name:  goast.NewIdent(packageName),
		Decls: []goast.Decl{goPropsTypeDecl(plan.ComponentName+"Props", plan.Props)},
	}, nil
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

func goSlotsTypeDecl(name string, slots []string) *goast.GenDecl {
	fields := make([]*goast.Field, 0, len(slots))
	for _, slot := range slots {
		fields = append(fields, &goast.Field{Names: []*goast.Ident{goast.NewIdent(ssrGoExportName(slot))}, Type: goSelector("vela", "Slot")})
	}
	return &goast.GenDecl{Tok: token.TYPE, Specs: []goast.Spec{&goast.TypeSpec{Name: goast.NewIdent(name), Type: &goast.StructType{Fields: &goast.FieldList{List: fields}}}}}
}

func goRenderWithOptionsAtFuncDecl(component, propsName, slotsName, optionsName string) *goast.FuncDecl {
	return &goast.FuncDecl{
		Name: goast.NewIdent("Render" + component + "WithOptionsAt"),
		Type: &goast.FuncType{
			Params: &goast.FieldList{List: []*goast.Field{
				{Names: []*goast.Ident{goast.NewIdent("w")}, Type: goSelector("io", "Writer")},
				{Names: []*goast.Ident{goast.NewIdent("props")}, Type: goast.NewIdent(propsName)},
				{Names: []*goast.Ident{goast.NewIdent("slots")}, Type: goast.NewIdent(slotsName)},
				{Names: []*goast.Ident{goast.NewIdent("options")}, Type: goast.NewIdent(optionsName)},
				{Names: []*goast.Ident{goast.NewIdent("instanceID")}, Type: goast.NewIdent("string")},
			}},
			Results: &goast.FieldList{List: []*goast.Field{{Type: goast.NewIdent("error")}}},
		},
		Body: &goast.BlockStmt{List: []goast.Stmt{
			&goast.ReturnStmt{Results: []goast.Expr{goast.NewIdent("nil")}},
		}},
	}
}

func goRenderAtFuncDecl(component, propsName, slotsName, optionsName string) *goast.FuncDecl {
	return &goast.FuncDecl{
		Name: goast.NewIdent("Render" + component + "At"),
		Type: &goast.FuncType{Params: &goast.FieldList{List: []*goast.Field{
			{Names: []*goast.Ident{goast.NewIdent("w")}, Type: goSelector("io", "Writer")},
			{Names: []*goast.Ident{goast.NewIdent("props")}, Type: goast.NewIdent(propsName)},
			{Names: []*goast.Ident{goast.NewIdent("slots")}, Type: goast.NewIdent(slotsName)},
			{Names: []*goast.Ident{goast.NewIdent("instanceID")}, Type: goast.NewIdent("string")},
		}}, Results: &goast.FieldList{List: []*goast.Field{{Type: goast.NewIdent("error")}}}},
		Body: &goast.BlockStmt{List: []goast.Stmt{&goast.ReturnStmt{Results: []goast.Expr{&goast.CallExpr{
			Fun: goast.NewIdent("Render" + component + "WithOptionsAt"), Args: []goast.Expr{
				goast.NewIdent("w"), goast.NewIdent("props"), goast.NewIdent("slots"),
				&goast.CompositeLit{Type: goast.NewIdent(optionsName)}, goast.NewIdent("instanceID"),
			},
		}}}}},
	}
}

func goRenderWithOptionsFuncDecl(component, propsName, slotsName, optionsName, scopeID string) *goast.FuncDecl {
	return &goast.FuncDecl{
		Name: goast.NewIdent("Render" + component + "WithOptions"),
		Type: &goast.FuncType{Params: &goast.FieldList{List: []*goast.Field{
			{Names: []*goast.Ident{goast.NewIdent("w")}, Type: goSelector("io", "Writer")},
			{Names: []*goast.Ident{goast.NewIdent("props")}, Type: goast.NewIdent(propsName)},
			{Names: []*goast.Ident{goast.NewIdent("slots")}, Type: goast.NewIdent(slotsName)},
			{Names: []*goast.Ident{goast.NewIdent("options")}, Type: goast.NewIdent(optionsName)},
		}}, Results: &goast.FieldList{List: []*goast.Field{{Type: goast.NewIdent("error")}}}},
		Body: &goast.BlockStmt{List: []goast.Stmt{&goast.ReturnStmt{Results: []goast.Expr{&goast.CallExpr{
			Fun: goast.NewIdent("Render" + component + "WithOptionsAt"), Args: []goast.Expr{
				goast.NewIdent("w"), goast.NewIdent("props"), goast.NewIdent("slots"), goast.NewIdent("options"),
				&goast.BasicLit{Kind: token.STRING, Value: fmt.Sprintf("%q", scopeID)},
			},
		}}}}},
	}
}

func goRenderFuncDecl(component, propsName, slotsName, scopeID string) *goast.FuncDecl {
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
		Body: &goast.BlockStmt{List: []goast.Stmt{&goast.ReturnStmt{Results: []goast.Expr{
			&goast.CallExpr{Fun: goast.NewIdent("Render" + component + "At"), Args: []goast.Expr{
				goast.NewIdent("w"), goast.NewIdent("props"), goast.NewIdent("slots"), &goast.BasicLit{Kind: token.STRING, Value: fmt.Sprintf("%q", scopeID)},
			}},
		}}}},
	}
}

func goComponentRendererAtFuncDecl(component, propsName, slotsName string) *goast.FuncDecl {
	wrapper := &goast.FuncLit{
		Type: &goast.FuncType{
			Params:  &goast.FieldList{List: []*goast.Field{{Names: []*goast.Ident{goast.NewIdent("w")}, Type: goSelector("io", "Writer")}}},
			Results: &goast.FieldList{List: []*goast.Field{{Type: goast.NewIdent("error")}}},
		},
		Body: &goast.BlockStmt{List: []goast.Stmt{&goast.ReturnStmt{Results: []goast.Expr{
			&goast.CallExpr{Fun: goast.NewIdent("Render" + component + "At"), Args: []goast.Expr{goast.NewIdent("w"), goast.NewIdent("props"), goast.NewIdent("slots"), goast.NewIdent("instanceID")}},
		}}}},
	}
	return &goast.FuncDecl{
		Name: goast.NewIdent(component + "At"),
		Type: &goast.FuncType{
			Params: &goast.FieldList{List: []*goast.Field{
				{Names: []*goast.Ident{goast.NewIdent("props")}, Type: goast.NewIdent(propsName)},
				{Names: []*goast.Ident{goast.NewIdent("slots")}, Type: goast.NewIdent(slotsName)},
				{Names: []*goast.Ident{goast.NewIdent("instanceID")}, Type: goast.NewIdent("string")},
			}},
			Results: &goast.FieldList{List: []*goast.Field{{Type: goSelector("vela", "Renderer")}}},
		},
		Body: &goast.BlockStmt{List: []goast.Stmt{&goast.ReturnStmt{Results: []goast.Expr{
			&goast.CallExpr{Fun: goSelector("vela", "RendererFunc"), Args: []goast.Expr{wrapper}},
		}}}},
	}
}

func goComponentRendererFuncDecl(component, propsName, slotsName, scopeID string) *goast.FuncDecl {
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
			&goast.CallExpr{Fun: goast.NewIdent(component + "At"), Args: []goast.Expr{
				goast.NewIdent("props"), goast.NewIdent("slots"), &goast.BasicLit{Kind: token.STRING, Value: fmt.Sprintf("%q", scopeID)},
			}},
		}}}},
	}
}

func goSelector(pkg, name string) goast.Expr {
	return &goast.SelectorExpr{X: goast.NewIdent(pkg), Sel: goast.NewIdent(name)}
}

func goSelectorExpr(x goast.Expr, name string) goast.Expr {
	return &goast.SelectorExpr{X: x, Sel: goast.NewIdent(name)}
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
	return ValidateGeneratedSSRGoPackage(map[string][]byte{filename: source})
}

// ValidateGeneratedSSRGoPackage independently reparses and type-checks every
// source file in one generated package. It is used when a renderer depends on
// a preserved *_props.go sidecar.
func ValidateGeneratedSSRGoPackage(sources map[string][]byte) error {
	return ValidateGeneratedSSRGoGraph(map[string]map[string][]byte{"vela/generated": sources})
}

// ValidateGeneratedSSRGoGraph type-checks every generated package as one
// import graph. Project output uses one Go package per source directory, so
// validating directories independently would hide unresolved child-component
// imports until a consumer tried to build the generated project.
func ValidateGeneratedSSRGoGraph(packages map[string]map[string][]byte) error {
	loader := &ssrGeneratedGraphImporter{
		fallback: importer.Default(),
		packages: packages,
		cache:    map[string]*types.Package{},
		checking: map[string]bool{},
	}
	paths := make([]string, 0, len(packages))
	for importPath := range packages {
		paths = append(paths, importPath)
	}
	sort.Strings(paths)
	for _, importPath := range paths {
		if _, err := loader.Import(importPath); err != nil {
			return fmt.Errorf("ssr go AST: type-check generated Go package %s: %w", importPath, err)
		}
	}
	return nil
}

type ssrGeneratedGraphImporter struct {
	fallback types.Importer
	packages map[string]map[string][]byte
	cache    map[string]*types.Package
	checking map[string]bool
	runtime  *types.Package
}

func (loader *ssrGeneratedGraphImporter) Import(importPath string) (*types.Package, error) {
	if importPath == SSRRuntimeImportPath {
		if loader.runtime != nil {
			return loader.runtime, nil
		}
		pkg, err := ssrRuntimeTypesPackage(loader.fallback)
		if err != nil {
			return nil, err
		}
		loader.runtime = pkg
		return pkg, nil
	}
	if pkg, ok := loader.cache[importPath]; ok {
		return pkg, nil
	}
	sources, generated := loader.packages[importPath]
	if !generated {
		return loader.fallback.Import(importPath)
	}
	if loader.checking[importPath] {
		return nil, fmt.Errorf("generated import cycle includes %q", importPath)
	}
	loader.checking[importPath] = true
	defer delete(loader.checking, importPath)

	fset := token.NewFileSet()
	filenames := make([]string, 0, len(sources))
	for filename := range sources {
		filenames = append(filenames, filename)
	}
	sort.Strings(filenames)
	files := make([]*goast.File, 0, len(filenames))
	for _, filename := range filenames {
		file, err := goparser.ParseFile(fset, filename, sources[filename], goparser.AllErrors)
		if err != nil {
			return nil, fmt.Errorf("parse generated Go: %w", err)
		}
		files = append(files, file)
	}
	config := types.Config{Importer: loader}
	pkg, err := config.Check(importPath, fset, files, nil)
	if err != nil {
		return nil, err
	}
	loader.cache[importPath] = pkg
	return pkg, nil
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
	fields := make([]*goast.Field, 0, len(props)+1)
	fields = append(fields, &goast.Field{
		Names: []*goast.Ident{goast.NewIdent("Set")},
		Type:  &goast.MapType{Key: goast.NewIdent("string"), Value: goast.NewIdent("bool")},
		Tag:   &goast.BasicLit{Kind: token.STRING, Value: "`json:\"-\"`"},
	})
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
	ssrRuntimeAddFunc(pkg, "FormValueString", []*types.Var{types.NewVar(token.NoPos, pkg, "value", anyType)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", stringType)})
	undefinedName := types.NewTypeName(token.NoPos, pkg, "UndefinedValue", nil)
	undefinedType := types.NewNamed(undefinedName, types.NewStruct(nil, nil), nil)
	pkg.Scope().Insert(undefinedName)
	ssrRuntimeAddFunc(pkg, "Undefined", nil, []*types.Var{types.NewVar(token.NoPos, pkg, "", undefinedType)})
	ssrRuntimeAddVariadicFunc(pkg, "TemplateString", []*types.Var{types.NewVar(token.NoPos, pkg, "values", types.NewSlice(anyType))}, []*types.Var{types.NewVar(token.NoPos, pkg, "", stringType)})
	ssrRuntimeAddFunc(pkg, "Length", []*types.Var{types.NewVar(token.NoPos, pkg, "value", anyType)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", types.Typ[types.Int])})
	ssrRuntimeAddFunc(pkg, "Truthy", []*types.Var{types.NewVar(token.NoPos, pkg, "value", anyType)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", types.Typ[types.Bool])})
	ssrRuntimeAddFunc(pkg, "PropProvided", []*types.Var{types.NewVar(token.NoPos, pkg, "props", anyType), types.NewVar(token.NoPos, pkg, "name", stringType)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", types.Typ[types.Bool])})
	ssrRuntimeAddFunc(pkg, "MarshalHydrationPayload", []*types.Var{types.NewVar(token.NoPos, pkg, "value", anyType)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", types.NewSlice(types.Typ[types.Byte])), types.NewVar(token.NoPos, pkg, "", errorType)})
	lazyAny := types.NewSignatureType(nil, nil, nil, types.NewTuple(), types.NewTuple(types.NewVar(token.NoPos, pkg, "", anyType)), false)
	ssrRuntimeAddFunc(pkg, "TernaryLazy", []*types.Var{types.NewVar(token.NoPos, pkg, "condition", lazyAny), types.NewVar(token.NoPos, pkg, "thenValue", lazyAny), types.NewVar(token.NoPos, pkg, "elseValue", lazyAny)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", anyType)})
	ssrRuntimeAddFunc(pkg, "AndLazy", []*types.Var{types.NewVar(token.NoPos, pkg, "left", lazyAny), types.NewVar(token.NoPos, pkg, "right", lazyAny)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", anyType)})
	ssrRuntimeAddFunc(pkg, "OrLazy", []*types.Var{types.NewVar(token.NoPos, pkg, "left", lazyAny), types.NewVar(token.NoPos, pkg, "right", lazyAny)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", anyType)})
	ssrRuntimeAddFunc(pkg, "ContainsString", []*types.Var{types.NewVar(token.NoPos, pkg, "values", anyType), types.NewVar(token.NoPos, pkg, "candidate", stringType)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", types.Typ[types.Bool])})
	ssrRuntimeAddFunc(pkg, "Iter", []*types.Var{types.NewVar(token.NoPos, pkg, "value", anyType)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", types.NewSlice(anyType))})
	ssrRuntimeAddFunc(pkg, "Get", []*types.Var{types.NewVar(token.NoPos, pkg, "value", anyType), types.NewVar(token.NoPos, pkg, "key", stringType)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", anyType)})
	ssrRuntimeAddFunc(pkg, "EscapeString", []*types.Var{types.NewVar(token.NoPos, pkg, "value", stringType)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", stringType)})
	pkg.MarkComplete()
	return pkg, nil
}

func ssrRuntimeAddFunc(pkg *types.Package, name string, params, results []*types.Var) {
	pkg.Scope().Insert(types.NewFunc(token.NoPos, pkg, name, ssrRuntimeSignature(pkg, nil, params, results)))
}

func ssrRuntimeAddVariadicFunc(pkg *types.Package, name string, params, results []*types.Var) {
	signature := types.NewSignatureType(nil, nil, nil, types.NewTuple(params...), types.NewTuple(results...), true)
	pkg.Scope().Insert(types.NewFunc(token.NoPos, pkg, name, signature))
}

func ssrRuntimeSignature(pkg *types.Package, receiver *types.Var, params, results []*types.Var) *types.Signature {
	return types.NewSignatureType(receiver, nil, nil, types.NewTuple(params...), types.NewTuple(results...), false)
}

func ssrRenderFunc(file *goast.File, component string) (*goast.FuncDecl, error) {
	if file == nil {
		return nil, fmt.Errorf("ssr go AST: missing generated file")
	}
	name := "Render" + component + "WithOptionsAt"
	for _, declaration := range file.Decls {
		function, ok := declaration.(*goast.FuncDecl)
		if ok && function.Name != nil && function.Name.Name == name {
			return function, nil
		}
	}
	return nil, fmt.Errorf("ssr go AST: missing %s declaration", name)
}
