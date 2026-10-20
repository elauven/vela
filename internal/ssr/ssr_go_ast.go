package ssr

import (
	"bytes"
	"fmt"
	goast "go/ast"
	"go/constant"
	"go/format"
	"go/importer"
	goparser "go/parser"
	"go/token"
	"go/types"
	"sort"
	"unicode"
)

// SSRRuntimeImportPath is the default module path of the Go runtime
// (R-12.1b). A project overrides it with "runtime": {"module": …} in
// vela.ssr.json.
const SSRRuntimeImportPath = "github.com/elauven/vela/ssr_runtime"

// ssrRuntimePath is the runtime module path of the project being built.
var ssrRuntimePath = SSRRuntimeImportPath

// SSRRuntimeImport returns the import path generated code and host checks
// use for the Go runtime.
func SSRRuntimeImport() string { return ssrRuntimePath }

// SSRRuntimeContract is the contract marker every generated file references
// (R-12.1d); it must match a constant the runtime exports.
const SSRRuntimeContract = "ContractV3"

// BuildSSRGoSkeleton creates the declarations of a server renderer file,
// including its Props type, entirely through the standard Go AST. The
// renderer body is added from the typed SSRRenderPlan; it must not switch to
// source-string generation.
func BuildSSRGoSkeleton(plan *SSRRenderPlan, packageName string) (*goast.File, error) {
	file, err := buildSSRGoBlocksSkeleton(plan, packageName, true)
	if err != nil {
		return nil, err
	}
	// Without a template body the renderer writes nothing.
	render, err := ssrFuncDecl(file, "Render"+plan.ComponentName+"WithOptions")
	if err != nil {
		return nil, err
	}
	if len(render.Body.List) == 0 {
		render.Body.List = []goast.Stmt{&goast.ReturnStmt{Results: []goast.Expr{goast.NewIdent("nil")}}}
	}
	return file, nil
}

// goAwaitSnapshotTypeDecl declares the server-owned result of one await
// boundary in the render options; valueType is any, a JSON value (T-7.4f).
func goAwaitSnapshotTypeDecl(name, valueType string) *goast.GenDecl {
	return &goast.GenDecl{Tok: token.TYPE, Specs: []goast.Spec{&goast.TypeSpec{
		Name: goast.NewIdent(name),
		Type: &goast.StructType{Fields: &goast.FieldList{List: []*goast.Field{
			{Names: []*goast.Ident{goast.NewIdent("State")}, Type: goast.NewIdent("string"), Tag: &goast.BasicLit{Kind: token.STRING, Value: "`json:\"state\"`"}},
			{Names: []*goast.Ident{goast.NewIdent("Value")}, Type: goast.NewIdent(valueType), Tag: &goast.BasicLit{Kind: token.STRING, Value: "`json:\"value,omitempty\"`"}},
			{Names: []*goast.Ident{goast.NewIdent("Error")}, Type: goast.NewIdent(valueType), Tag: &goast.BasicLit{Kind: token.STRING, Value: "`json:\"error,omitempty\"`"}},
		}}},
	}}}
}

// BuildSSRGoPropsFile emits the inferred prop contract as its own Go file.
// Project generation creates it once and preserves it on later builds, so a
// developer can refine fields without generated output overwriting the edit.
func BuildSSRGoPropsFile(plan *SSRRenderPlan, packageName string) (*goast.File, error) {
	if _, err := buildSSRGoBlocksSkeleton(plan, packageName, false); err != nil {
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

// goRuntimeContractDecl references the runtime contract marker
// (`var _ = vela.ContractV3`), so a runtime of another version fails go build
// with an undefined identifier (R-12.1d).
func goRuntimeContractDecl() *goast.GenDecl {
	return &goast.GenDecl{Tok: token.VAR, Specs: []goast.Spec{&goast.ValueSpec{
		Names:  []*goast.Ident{goast.NewIdent("_")},
		Values: []goast.Expr{goSelector("vela", SSRRuntimeContract)},
	}}}
}

func goSSRImportDecl() *goast.GenDecl {
	decl := goImportDecl("context")
	decl.Specs = append(decl.Specs, goImportDecl("io").Specs...)
	decl.Specs = append(decl.Specs, &goast.ImportSpec{
		Name: goast.NewIdent("vela"),
		Path: &goast.BasicLit{Kind: token.STRING, Value: fmt.Sprintf("%q", SSRRuntimeImport())},
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
	return validateGeneratedSSRGoGraph(packages, nil)
}

// validateGeneratedSSRGoGraph also resolves imports of host packages to
// their analysed types (task 030).
func validateGeneratedSSRGoGraph(packages map[string]map[string][]byte, hosts map[string]*types.Package) error {
	loader := &ssrGeneratedGraphImporter{
		hosts:    hosts,
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
	hosts    map[string]*types.Package
	fallback types.Importer
	packages map[string]map[string][]byte
	cache    map[string]*types.Package
	checking map[string]bool
	runtime  *types.Package
}

func (loader *ssrGeneratedGraphImporter) Import(importPath string) (*types.Package, error) {
	if importPath == SSRRuntimeImport() {
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
	if pkg, ok := loader.hosts[importPath]; ok {
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
	case SSRTypeInt, SSRTypeFloat:
		// Every JavaScript number is a float64 (T-5.3a).

		return goast.NewIdent("float64")
	case SSRTypeSlice:
		return &goast.ArrayType{Elt: goast.NewIdent("any")}
	case SSRTypeObject:
		return &goast.MapType{Key: goast.NewIdent("string"), Value: goast.NewIdent("any")}
	default:
		return goast.NewIdent("any")
	}
}

func ssrRuntimeTypesPackage(loader types.Importer) (*types.Package, error) {
	pkg := types.NewPackage(SSRRuntimeImport(), "ssr_runtime")
	pkg.Scope().Insert(types.NewConst(token.NoPos, pkg, SSRRuntimeContract, types.Typ[types.UntypedBool], constant.MakeBool(true)))
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
	ssrRuntimeAddFunc(pkg, "AddString", []*types.Var{types.NewVar(token.NoPos, pkg, "value", anyType)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", stringType)})
	ssrRuntimeAddFunc(pkg, "FormValueString", []*types.Var{types.NewVar(token.NoPos, pkg, "value", anyType)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", stringType)})
	undefinedName := types.NewTypeName(token.NoPos, pkg, "UndefinedValue", nil)
	undefinedType := types.NewNamed(undefinedName, types.NewStruct(nil, nil), nil)
	pkg.Scope().Insert(undefinedName)
	ssrRuntimeAddFunc(pkg, "Undefined", nil, []*types.Var{types.NewVar(token.NoPos, pkg, "", undefinedType)})
	ssrRuntimeAddVariadicFunc(pkg, "TemplateString", []*types.Var{types.NewVar(token.NoPos, pkg, "values", types.NewSlice(anyType))}, []*types.Var{types.NewVar(token.NoPos, pkg, "", stringType)})
	ssrRuntimeAddFunc(pkg, "Length", []*types.Var{types.NewVar(token.NoPos, pkg, "value", anyType)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", types.Typ[types.Int])})
	ssrRuntimeAddFunc(pkg, "Truthy", []*types.Var{types.NewVar(token.NoPos, pkg, "value", anyType)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", types.Typ[types.Bool])})
	ssrRuntimeAddFunc(pkg, "StrictEqual", []*types.Var{types.NewVar(token.NoPos, pkg, "left", anyType), types.NewVar(token.NoPos, pkg, "right", anyType)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", types.Typ[types.Bool])})
	ssrRuntimeAddFunc(pkg, "StringLess", []*types.Var{types.NewVar(token.NoPos, pkg, "left", stringType), types.NewVar(token.NoPos, pkg, "right", stringType)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", types.Typ[types.Bool])})
	ssrRuntimeAddFunc(pkg, "Index", []*types.Var{types.NewVar(token.NoPos, pkg, "value", anyType), types.NewVar(token.NoPos, pkg, "index", anyType)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", anyType)})
	ssrRuntimeAddFunc(pkg, "Number", []*types.Var{types.NewVar(token.NoPos, pkg, "value", anyType)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", types.Typ[types.Float64])})
	for _, name := range []string{"NumberAdd", "NumberSubtract", "NumberMultiply", "NumberDivide", "NumberRemainder"} {
		ssrRuntimeAddFunc(pkg, name, []*types.Var{types.NewVar(token.NoPos, pkg, "left", anyType), types.NewVar(token.NoPos, pkg, "right", anyType)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", types.Typ[types.Float64])})
	}
	ssrRuntimeAddFunc(pkg, "NumberNegate", []*types.Var{types.NewVar(token.NoPos, pkg, "value", anyType)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", types.Typ[types.Float64])})
	ssrRuntimeAddFunc(pkg, "PropProvided", []*types.Var{types.NewVar(token.NoPos, pkg, "props", anyType), types.NewVar(token.NoPos, pkg, "name", stringType)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", types.Typ[types.Bool])})
	ssrRuntimeAddFunc(pkg, "MarshalHydrationPayload", []*types.Var{types.NewVar(token.NoPos, pkg, "value", anyType)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", types.NewSlice(types.Typ[types.Byte])), types.NewVar(token.NoPos, pkg, "", errorType)})
	lazyAny := types.NewSignatureType(nil, nil, nil, types.NewTuple(), types.NewTuple(types.NewVar(token.NoPos, pkg, "", anyType)), false)
	ssrRuntimeAddFunc(pkg, "TernaryLazy", []*types.Var{types.NewVar(token.NoPos, pkg, "condition", lazyAny), types.NewVar(token.NoPos, pkg, "thenValue", lazyAny), types.NewVar(token.NoPos, pkg, "elseValue", lazyAny)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", anyType)})
	ssrRuntimeAddFunc(pkg, "AndLazy", []*types.Var{types.NewVar(token.NoPos, pkg, "left", lazyAny), types.NewVar(token.NoPos, pkg, "right", lazyAny)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", anyType)})
	ssrRuntimeAddFunc(pkg, "OrLazy", []*types.Var{types.NewVar(token.NoPos, pkg, "left", lazyAny), types.NewVar(token.NoPos, pkg, "right", lazyAny)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", anyType)})
	ssrRuntimeAddFunc(pkg, "ContainsString", []*types.Var{types.NewVar(token.NoPos, pkg, "values", anyType), types.NewVar(token.NoPos, pkg, "candidate", stringType)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", types.Typ[types.Bool])})
	ssrRuntimeAddFunc(pkg, "Iter", []*types.Var{types.NewVar(token.NoPos, pkg, "value", anyType)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", types.NewSlice(anyType))})
	ssrRuntimeAddFunc(pkg, "Get", []*types.Var{types.NewVar(token.NoPos, pkg, "value", anyType), types.NewVar(token.NoPos, pkg, "key", stringType)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", anyType)})
	ssrRuntimeAddFunc(pkg, "WriteEscaped", []*types.Var{types.NewVar(token.NoPos, pkg, "w", writer), types.NewVar(token.NoPos, pkg, "value", stringType)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", errorType)})
	ssrRuntimeAddFunc(pkg, "EscapeString", []*types.Var{types.NewVar(token.NoPos, pkg, "value", stringType)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", stringType)})

	// Dynamic tier (ssr_runtime/value.go).
	v := func(name string, t types.Type) *types.Var { return types.NewVar(token.NoPos, pkg, name, t) }
	boolT := types.Typ[types.Bool]
	floatT := types.Typ[types.Float64]
	anySlice := types.NewSlice(anyType)
	lazy := types.NewSignatureType(nil, nil, nil, types.NewTuple(), types.NewTuple(v("", anyType)), false)
	rest := types.NewSignatureType(nil, nil, nil, types.NewTuple(v("value", anyType)), types.NewTuple(v("", anyType)), false)
	jsName := types.NewTypeName(token.NoPos, pkg, "JS", nil)
	jsType := types.NewNamed(jsName, types.NewStruct(nil, nil), nil)
	pkg.Scope().Insert(jsName)
	jsRecv := v("js", types.NewPointer(jsType))
	jsMethod := func(name string, params []*types.Var, result types.Type) {
		jsType.AddMethod(types.NewFunc(token.NoPos, pkg, name, types.NewSignatureType(jsRecv, nil, nil, types.NewTuple(params...), types.NewTuple(v("", result)), false)))
	}
	jsMethod("Get", []*types.Var{v("value", anyType), v("key", stringType), v("site", stringType)}, anyType)
	jsMethod("Index", []*types.Var{v("value", anyType), v("key", anyType), v("site", stringType)}, anyType)
	jsMethod("Length", []*types.Var{v("value", anyType), v("site", stringType)}, anyType)
	jsMethod("SpreadItems", []*types.Var{v("value", anyType), v("site", stringType)}, anySlice)
	jsMethod("ArrayIterItems", []*types.Var{v("receiver", anyType), v("name", stringType), v("site", stringType)}, anySlice)
	jsMethod("In", []*types.Var{v("key", anyType), v("object", anyType), v("site", stringType)}, types.Typ[types.Bool])
	restFunc := types.NewSignatureType(nil, nil, nil, types.NewTuple(v("receiver", anyType)), types.NewTuple(v("", anyType)), false)
	jsMethod("OptionalMethod", []*types.Var{v("site", stringType), v("receiver", anyType), v("name", stringType), v("call", restFunc)}, anyType)
	jsMethod("Err", nil, errorType)
	ssrRuntimeAddFunc(pkg, "NewJS", nil, []*types.Var{v("", types.NewPointer(jsType))})
	objectName := types.NewTypeName(token.NoPos, pkg, "Object", nil)
	objectType := types.NewNamed(objectName, types.NewStruct(nil, nil), nil)
	pkg.Scope().Insert(objectName)
	objectPtr := types.NewPointer(objectType)
	jsMethod("ObjectRest", []*types.Var{v("value", anyType), v("excluded", types.NewSlice(stringType)), v("site", stringType)}, objectPtr)
	jsMethod("NewObject", nil, objectPtr)
	objectRecv := v("o", objectPtr)
	objectType.AddMethod(types.NewFunc(token.NoPos, pkg, "Set", types.NewSignatureType(objectRecv, nil, nil, types.NewTuple(v("key", stringType), v("value", anyType)), types.NewTuple(v("", objectPtr)), false)))
	objectType.AddMethod(types.NewFunc(token.NoPos, pkg, "Spread", types.NewSignatureType(objectRecv, nil, nil, types.NewTuple(v("value", anyType)), types.NewTuple(v("", objectPtr)), false)))
	ssrRuntimeAddFunc(pkg, "NewObject", nil, []*types.Var{v("", objectPtr)})
	pkg.Scope().Insert(types.NewFunc(token.NoPos, pkg, "JoinArrays", types.NewSignatureType(nil, nil, nil, types.NewTuple(v("parts", types.NewSlice(anySlice))), types.NewTuple(v("", anySlice)), true)))
	ssrRuntimeAddFunc(pkg, "Add", []*types.Var{v("left", anyType), v("right", anyType)}, []*types.Var{v("", anyType)})
	ssrRuntimeAddFunc(pkg, "Pow", []*types.Var{v("left", anyType), v("right", anyType)}, []*types.Var{v("", floatT)})
	for _, name := range []string{"Less", "LessEqual", "Greater", "GreaterEqual"} {
		ssrRuntimeAddFunc(pkg, name, []*types.Var{v("left", anyType), v("right", anyType)}, []*types.Var{v("", boolT)})
	}
	ssrRuntimeAddFunc(pkg, "LooseNull", []*types.Var{v("value", anyType)}, []*types.Var{v("", boolT)})
	ssrRuntimeAddFunc(pkg, "TypeOf", []*types.Var{v("value", anyType)}, []*types.Var{v("", stringType)})
	ssrRuntimeAddFunc(pkg, "ToNumber", []*types.Var{v("value", anyType)}, []*types.Var{v("", floatT)})
	ssrRuntimeAddFunc(pkg, "NullishLazy", []*types.Var{v("left", lazy), v("right", lazy)}, []*types.Var{v("", anyType)})
	ssrRuntimeAddFunc(pkg, "OptionalChain", []*types.Var{v("base", anyType), v("rest", rest)}, []*types.Var{v("", anyType)})

	// Host implementations (ssr_runtime/host.go).
	contextPkg, err := loader.Import("context")
	if err != nil {
		return nil, err
	}
	contextT := contextPkg.Scope().Lookup("Context").Type()
	ssrRuntimeAddFunc(pkg, "WithContext", []*types.Var{v("w", writer), v("ctx", contextT)}, []*types.Var{v("", writer)})
	ssrRuntimeAddFunc(pkg, "ContextOf", []*types.Var{v("w", writer)}, []*types.Var{v("", contextT)})
	pkg.Scope().Insert(types.NewFunc(token.NoPos, pkg, "CallHost", types.NewSignatureType(nil, nil, nil, types.NewTuple(
		v("js", types.NewPointer(jsType)), v("ctx", contextT), v("site", stringType), v("name", stringType), v("fn", anyType), v("args", anySlice),
	), types.NewTuple(v("", anyType)), true)))
	pkg.Scope().Insert(types.NewVar(token.NoPos, pkg, "ErrHostNotImplemented", errorType))
	errorMethod := func(name string) {
		typeName := types.NewTypeName(token.NoPos, pkg, name, nil)
		named := types.NewNamed(typeName, types.NewStruct([]*types.Var{
			types.NewField(token.NoPos, pkg, "Site", stringType, false),
		}, nil), nil)
		recv := v("e", types.NewPointer(named))
		named.AddMethod(types.NewFunc(token.NoPos, pkg, "Error", types.NewSignatureType(recv, nil, nil, nil, types.NewTuple(v("", stringType)), false)))
		pkg.Scope().Insert(typeName)
	}
	errorMethod("HostError")
	errorMethod("RenderError")
	optionalName := types.NewTypeName(token.NoPos, pkg, "Optional", nil)
	optionalT := types.NewTypeParam(types.NewTypeName(token.NoPos, pkg, "T", nil), types.NewInterfaceType(nil, nil))
	optional := types.NewNamed(optionalName, nil, nil)
	optional.SetTypeParams([]*types.TypeParam{optionalT})
	optional.SetUnderlying(types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, pkg, "Value", optionalT, false),
		types.NewField(token.NoPos, pkg, "Null", boolT, false),
		types.NewField(token.NoPos, pkg, "Absent", boolT, false),
	}, nil))
	pkg.Scope().Insert(optionalName)

	// Translated functions (ssr_runtime/func.go).
	funcName := types.NewTypeName(token.NoPos, pkg, "Func", nil)
	funcSig := types.NewSignatureType(nil, nil, nil, types.NewTuple(v("this", anyType), v("args", anySlice)), types.NewTuple(v("", anyType)), true)
	funcType := types.NewNamed(funcName, funcSig, nil)
	pkg.Scope().Insert(funcName)
	_ = funcType
	objectType.AddMethod(types.NewFunc(token.NoPos, pkg, "Define", types.NewSignatureType(objectRecv, nil, nil, types.NewTuple(v("key", stringType), v("value", anyType)), types.NewTuple(v("", objectPtr)), false)))
	objectType.AddMethod(types.NewFunc(token.NoPos, pkg, "DefineGetter", types.NewSignatureType(objectRecv, nil, nil, types.NewTuple(v("key", stringType), v("get", funcType)), types.NewTuple(v("", objectPtr)), false)))
	objectType.AddMethod(types.NewFunc(token.NoPos, pkg, "DefineSetter", types.NewSignatureType(objectRecv, nil, nil, types.NewTuple(v("key", stringType), v("set", funcType)), types.NewTuple(v("", objectPtr)), false)))
	ssrRuntimeAddFunc(pkg, "Arg", []*types.Var{v("args", anySlice), v("i", types.Typ[types.Int])}, []*types.Var{v("", anyType)})
	ssrRuntimeAddFunc(pkg, "PropertyKey", []*types.Var{v("key", anyType)}, []*types.Var{v("", stringType)})
	ssrRuntimeAddFunc(pkg, "ForInKeys", []*types.Var{v("value", anyType)}, []*types.Var{v("", anySlice)})
	ssrRuntimeAddFunc(pkg, "RestItems", []*types.Var{v("items", anySlice), v("from", types.Typ[types.Int])}, []*types.Var{v("", anySlice)})
	ssrRuntimeAddFunc(pkg, "NewError", []*types.Var{v("kind", stringType), v("message", anyType)}, []*types.Var{v("", objectPtr)})
	jsMethod("Throw", []*types.Var{v("value", anyType), v("site", stringType)}, undefinedType)
	jsType.AddMethod(types.NewFunc(token.NoPos, pkg, "Failed", types.NewSignatureType(jsRecv, nil, nil, nil, types.NewTuple(v("", boolT)), false)))
	jsMethod("Catch", []*types.Var{v("ctx", contextT)}, anyType)
	jsType.AddMethod(types.NewFunc(token.NoPos, pkg, "Call", types.NewSignatureType(jsRecv, nil, nil, types.NewTuple(v("site", stringType), v("fn", anyType), v("args", anySlice)), types.NewTuple(v("", anyType)), true)))
	jsMethod("SetMember", []*types.Var{v("object", anyType), v("key", anyType), v("value", anyType), v("site", stringType)}, anyType)
	jsMethod("SetThis", []*types.Var{v("this", anyType), v("key", anyType), v("value", anyType), v("site", stringType)}, anyType)
	jsMethod("SetElement", []*types.Var{v("target", anyType), v("key", anyType), v("value", anyType), v("site", stringType)}, anyType)
	jsMethod("Delete", []*types.Var{v("target", anyType), v("key", anyType), v("local", boolT), v("site", stringType)}, boolT)
	jsMethod("LocalObject", nil, objectPtr)
	part := types.NewSignatureType(nil, nil, nil, types.NewTuple(), types.NewTuple(v("", anyType), v("", boolT)), false)
	catchPart := types.NewSignatureType(nil, nil, nil, types.NewTuple(v("error", anyType)), types.NewTuple(v("", anyType), v("", boolT)), false)
	pkg.Scope().Insert(types.NewFunc(token.NoPos, pkg, "Try", types.NewSignatureType(nil, nil, nil, types.NewTuple(
		v("js", types.NewPointer(jsType)), v("ctx", contextT), v("try", part), v("catch", catchPart), v("finally", part),
	), types.NewTuple(v("", anyType), v("", boolT)), false)))

	// Promises (ssr_runtime/promise.go).
	promiseName := types.NewTypeName(token.NoPos, pkg, "Promise", nil)
	promiseType := types.NewNamed(promiseName, types.NewStruct(nil, nil), nil)
	pkg.Scope().Insert(promiseName)
	promisePtr := types.NewPointer(promiseType)
	ssrRuntimeAddFunc(pkg, "Resolve", []*types.Var{v("value", anyType)}, []*types.Var{v("", promisePtr)})
	ssrRuntimeAddFunc(pkg, "Reject", []*types.Var{v("value", anyType), v("site", stringType)}, []*types.Var{v("", promisePtr)})
	jsMethod("Async", []*types.Var{v("result", anyType)}, promisePtr)
	jsMethod("Impure", []*types.Var{v("message", stringType), v("site", stringType)}, undefinedType)
	jsMethod("Await", []*types.Var{v("ctx", contextT), v("value", anyType)}, anyType)
	jsMethod("Then", []*types.Var{v("ctx", contextT), v("site", stringType), v("value", anyType), v("onFulfilled", anyType), v("onRejected", anyType)}, anyType)
	jsMethod("CatchPromise", []*types.Var{v("ctx", contextT), v("site", stringType), v("value", anyType), v("onRejected", anyType)}, anyType)
	jsMethod("Finally", []*types.Var{v("ctx", contextT), v("site", stringType), v("value", anyType), v("onFinally", anyType)}, anyType)
	jsMethod("All", []*types.Var{v("site", stringType), v("values", anyType)}, anyType)
	jsMethod("AllSettled", []*types.Var{v("ctx", contextT), v("site", stringType), v("values", anyType)}, anyType)
	pkg.Scope().Insert(types.NewFunc(token.NoPos, pkg, "AwaitSettle", types.NewSignatureType(nil, nil, nil, types.NewTuple(
		v("w", writer), v("js", types.NewPointer(jsType)), v("site", stringType), v("operand", anyType),
	), types.NewTuple(v("", stringType), v("", anyType)), false)))

	// Built-ins (ssr_runtime/builtins.go, strings.go, arrays.go).
	for _, name := range []string{"ToBoolean", "IsNaN", "IsFinite", "NumberIsNaN", "NumberIsFinite", "NumberIsInteger", "NumberIsSafeInteger", "IsArray"} {
		ssrRuntimeAddFunc(pkg, name, []*types.Var{v("value", anyType)}, []*types.Var{v("", boolT)})
	}
	for _, name := range []string{"ParseFloat", "MathRound", "MathFloor", "MathCeil", "MathTrunc", "MathAbs", "MathSign", "MathSqrt", "MathCbrt"} {
		ssrRuntimeAddFunc(pkg, name, []*types.Var{v("value", anyType)}, []*types.Var{v("", floatT)})
	}
	for _, name := range []string{"MathMin", "MathMax", "MathHypot"} {
		ssrRuntimeAddVariadicFunc(pkg, name, []*types.Var{v("values", anySlice)}, []*types.Var{v("", floatT)})
	}
	ssrRuntimeAddFunc(pkg, "ToStringValue", []*types.Var{v("value", anyType)}, []*types.Var{v("", stringType)})
	ssrRuntimeAddFunc(pkg, "ParseInt", []*types.Var{v("text", anyType), v("radix", anyType)}, []*types.Var{v("", floatT)})
	ssrRuntimeAddVariadicFunc(pkg, "ArrayOf", []*types.Var{v("values", anySlice)}, []*types.Var{v("", anySlice)})
	ssrRuntimeAddVariadicFunc(pkg, "StringFromCharCode", []*types.Var{v("codes", anySlice)}, []*types.Var{v("", stringType)})
	jsType.AddMethod(types.NewFunc(token.NoPos, pkg, "StringFromCodePoint", types.NewSignatureType(jsRecv, nil, nil, types.NewTuple(v("site", stringType), v("codes", anySlice)), types.NewTuple(v("", anyType)), true)))
	jsMethod("StructuredClone", []*types.Var{v("site", stringType), v("value", anyType)}, anyType)
	ssrRuntimeAddFunc(pkg, "Hole", nil, []*types.Var{v("", undefinedType)})
	ssrRuntimeAddFunc(pkg, "Box", []*types.Var{v("value", anyType)}, []*types.Var{v("", anyType)})
	jsType.AddMethod(types.NewFunc(token.NoPos, pkg, "MutateShared", types.NewSignatureType(jsRecv, nil, nil, types.NewTuple(v("site", stringType), v("receiver", anyType), v("name", stringType), v("args", anySlice)), types.NewTuple(v("", anyType)), true)))
	jsType.AddMethod(types.NewFunc(token.NoPos, pkg, "Method", types.NewSignatureType(jsRecv, nil, nil, types.NewTuple(v("site", stringType), v("receiver", anyType), v("name", stringType), v("args", anySlice)), types.NewTuple(v("", anyType)), true)))
	jsType.AddMethod(types.NewFunc(token.NoPos, pkg, "MutateArray", types.NewSignatureType(jsRecv, nil, nil, types.NewTuple(v("site", stringType), v("array", anyType), v("name", stringType), v("args", anySlice)), types.NewTuple(v("", anyType), v("", anyType)), true)))
	jsType.AddMethod(types.NewFunc(token.NoPos, pkg, "ObjectAssign", types.NewSignatureType(jsRecv, nil, nil, types.NewTuple(v("site", stringType), v("target", anyType), v("sources", anySlice)), types.NewTuple(v("", anyType)), true)))
	for _, name := range []string{"EncodeURIComponent", "EncodeURI", "DecodeURIComponent", "DecodeURI", "ObjectKeys", "ObjectValues", "ObjectEntries", "ObjectFromEntries", "JSONParse"} {
		jsMethod(name, []*types.Var{v("site", stringType), v("value", anyType)}, anyType)
	}
	jsMethod("ObjectHasOwn", []*types.Var{v("site", stringType), v("value", anyType), v("key", anyType)}, anyType)
	jsMethod("NewDate", []*types.Var{v("site", stringType), v("value", anyType)}, anyType)
	jsMethod("DateParse", []*types.Var{v("site", stringType), v("text", anyType)}, floatT)
	ssrRuntimeAddVariadicFunc(pkg, "DateUTC", []*types.Var{v("args", anySlice)}, []*types.Var{v("", floatT)})
	jsMethod("NewCollection", []*types.Var{v("site", stringType), v("set", boolT), v("local", boolT), v("iterable", anyType)}, anyType)
	jsMethod("MapGroupBy", []*types.Var{v("site", stringType), v("items", anyType), v("callback", anyType), v("local", boolT)}, anyType)
	jsMethod("ObjectGroupBy", []*types.Var{v("site", stringType), v("items", anyType), v("callback", anyType)}, anyType)
	ssrRuntimeAddFunc(pkg, "ObjectFreeze", []*types.Var{v("value", anyType)}, []*types.Var{v("", anyType)})
	jsMethod("ObjectIsFrozen", []*types.Var{v("site", stringType), v("value", anyType)}, boolT)
	jsMethod("ArrayFrom", []*types.Var{v("site", stringType), v("source", anyType), v("mapFn", anyType)}, anyType)
	jsMethod("JSONStringify", []*types.Var{v("site", stringType), v("value", anyType), v("replacer", anyType), v("indent", anyType)}, anyType)
	for _, name := range []string{"MathLog", "MathLog2", "MathLog10", "MathLog1p", "MathExp", "MathExpm1", "MathSin", "MathCos", "MathTan", "MathAsin", "MathAcos", "MathAtan", "MathSinh", "MathCosh", "MathTanh", "MathAsinh", "MathAcosh", "MathAtanh", "MathFround", "MathClz32"} {
		ssrRuntimeAddFunc(pkg, name, []*types.Var{v("value", anyType)}, []*types.Var{v("", floatT)})
	}
	for _, name := range []string{"MathAtan2", "MathImul"} {
		ssrRuntimeAddFunc(pkg, name, []*types.Var{v("a", anyType), v("b", anyType)}, []*types.Var{v("", floatT)})
	}
	jsType.AddMethod(types.NewFunc(token.NoPos, pkg, "Console", types.NewSignatureType(jsRecv, nil, nil, types.NewTuple(v("ctx", contextT), v("method", stringType), v("args", anySlice)), types.NewTuple(v("", undefinedType)), true)))
	jsType.AddMethod(types.NewFunc(token.NoPos, pkg, "GlobalFunc", types.NewSignatureType(jsRecv, nil, nil, types.NewTuple(v("site", stringType), v("name", stringType)), types.NewTuple(v("", funcType)), false)))

	// Block markup and payload stream (docs/HYDRATION_ARCHITECTURE.md §5, §6).
	jsonPkg, err := loader.Import("encoding/json")
	if err != nil {
		return nil, err
	}
	rawMessage := jsonPkg.Scope().Lookup("RawMessage").Type()
	boolType := types.Typ[types.Bool]
	ssrRuntimeAddFunc(pkg, "StaticRoot", []*types.Var{types.NewVar(token.NoPos, pkg, "w", writer)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", writer)})
	ssrRuntimeAddFunc(pkg, "StaticWriter", []*types.Var{types.NewVar(token.NoPos, pkg, "w", writer)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", writer)})
	pkg.Scope().Insert(types.NewFunc(token.NoPos, pkg, "BeginComponent", types.NewSignatureType(nil, nil, nil, types.NewTuple(
		types.NewVar(token.NoPos, pkg, "w", writer),
		types.NewVar(token.NoPos, pkg, "component", stringType),
		types.NewVar(token.NoPos, pkg, "state", anyType),
		types.NewVar(token.NoPos, pkg, "omitted", types.NewSlice(stringType)),
	), types.NewTuple(types.NewVar(token.NoPos, pkg, "", writer), types.NewVar(token.NoPos, pkg, "", boolType)), true)))
	ssrRuntimeAddFunc(pkg, "LeaveComponent", []*types.Var{types.NewVar(token.NoPos, pkg, "w", writer)}, nil)
	ssrRuntimeAddFunc(pkg, "EndComponent", []*types.Var{types.NewVar(token.NoPos, pkg, "w", writer), types.NewVar(token.NoPos, pkg, "root", boolType), types.NewVar(token.NoPos, pkg, "props", rawMessage)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", errorType)})
	ssrRuntimeAddFunc(pkg, "OpenRange", []*types.Var{types.NewVar(token.NoPos, pkg, "w", writer), types.NewVar(token.NoPos, pkg, "state", stringType)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", errorType)})
	ssrRuntimeAddFunc(pkg, "CloseRange", []*types.Var{types.NewVar(token.NoPos, pkg, "w", writer)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", errorType)})
	// func AssignProp[T any](field *T, value any, prop string) error
	assignT := types.NewTypeParam(types.NewTypeName(token.NoPos, pkg, "T", nil), types.NewInterfaceType(nil, nil))
	pkg.Scope().Insert(types.NewFunc(token.NoPos, pkg, "AssignProp", types.NewSignatureType(nil, nil, []*types.TypeParam{assignT}, types.NewTuple(
		types.NewVar(token.NoPos, pkg, "field", types.NewPointer(assignT)),
		types.NewVar(token.NoPos, pkg, "value", anyType),
		types.NewVar(token.NoPos, pkg, "prop", stringType),
	), types.NewTuple(types.NewVar(token.NoPos, pkg, "", errorType)), false)))
	ssrRuntimeAddFunc(pkg, "SetHostScope", []*types.Var{types.NewVar(token.NoPos, pkg, "w", writer), types.NewVar(token.NoPos, pkg, "scope", stringType)}, nil)
	ssrRuntimeAddFunc(pkg, "WriteHostScope", []*types.Var{types.NewVar(token.NoPos, pkg, "w", writer)}, []*types.Var{types.NewVar(token.NoPos, pkg, "", errorType)})
	ssrRuntimeAddFunc(pkg, "AwaitEntry", []*types.Var{
		types.NewVar(token.NoPos, pkg, "w", writer),
		types.NewVar(token.NoPos, pkg, "site", stringType),
		types.NewVar(token.NoPos, pkg, "state", stringType),
		types.NewVar(token.NoPos, pkg, "value", anyType),
		types.NewVar(token.NoPos, pkg, "errValue", anyType),
	}, []*types.Var{types.NewVar(token.NoPos, pkg, "", stringType)})
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

// goRenderContextFuncDecl declares RenderXContext (H-8.3c): the render
// context reaches host functions through the writer.
//
//	func RenderXContext(ctx context.Context, w io.Writer, props XProps, slots XSlots) error {
//		return RenderX(vela.WithContext(w, ctx), props, slots)
//	}
func goRenderContextFuncDecl(component, propsName, slotsName string) *goast.FuncDecl {
	return &goast.FuncDecl{
		Name: goast.NewIdent("Render" + component + "Context"),
		Type: &goast.FuncType{
			Params: &goast.FieldList{List: []*goast.Field{
				{Names: []*goast.Ident{goast.NewIdent("ctx")}, Type: goSelector("context", "Context")},
				{Names: []*goast.Ident{goast.NewIdent("w")}, Type: goSelector("io", "Writer")},
				{Names: []*goast.Ident{goast.NewIdent("props")}, Type: goast.NewIdent(propsName)},
				{Names: []*goast.Ident{goast.NewIdent("slots")}, Type: goast.NewIdent(slotsName)},
			}},
			Results: &goast.FieldList{List: []*goast.Field{{Type: goast.NewIdent("error")}}},
		},
		Body: &goast.BlockStmt{List: []goast.Stmt{&goast.ReturnStmt{Results: []goast.Expr{&goast.CallExpr{
			Fun: goast.NewIdent("Render" + component),
			Args: []goast.Expr{
				&goast.CallExpr{Fun: goSelector("vela", "WithContext"), Args: []goast.Expr{goast.NewIdent("w"), goast.NewIdent("ctx")}},
				goast.NewIdent("props"), goast.NewIdent("slots"),
			},
		}}}}},
	}
}
