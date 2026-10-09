package main

import (
	"fmt"
	goast "go/ast"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Local JavaScript modules (docs/SSR_TRANSLATION_CONTRACT.md §7.3, task
// 045). Every local .js module that region code imports becomes one Go
// package under server/modules/, mirroring its path under the source root
// (T-7.3a). The package has a Module struct with a field per translated
// export and New(ssrJS, ssrCtx), which evaluates the module's const
// bindings and builds its function closures for one render: module values
// are never shared between renders. Top-level code may only declare
// functions and const bindings with admitted initializers (T-7.3b); npm
// imports are never translated (T-7.3c).

// SSRModules compiles the local modules of one project on first import.
type SSRModules struct {
	layout  SSRProjectLayout
	srcRoot string
	hosts   *SSRHosts
	modules map[string]*ssrModule // by source, relative to the project root
	order   []*ssrModule
}

type ssrModule struct {
	Source     string // relative to the project root, with slashes
	abs        string
	ImportPath string
	Package    string
	// Alias is the import name of the package in importers; Var is the
	// variable holding the module of the render.
	Alias, Var string
	dir        string
	compiling  bool
	// problem makes every export unavailable (T-7.3b).
	problem error
	// status holds, per export, nil when it is translated or why not;
	// fields names its Go field and functions marks function exports.
	status    map[string]error
	fields    map[string]string
	functions map[string]bool
	file      *goast.File
}

// ssrModuleRef is a name a component or module imports from a module.
type ssrModuleRef struct {
	Module *ssrModule
	Export string
}

func newSSRModules(layout SSRProjectLayout, srcRoot string, hosts *SSRHosts) *SSRModules {
	return &SSRModules{layout: layout, srcRoot: srcRoot, hosts: hosts, modules: map[string]*ssrModule{}}
}

// refs resolves the named imports of local .js modules of a program in
// file (absolute) to their modules, compiling them on first use.
func (m *SSRModules) refs(file string, imports []*Import) map[string]ssrModuleRef {
	out := map[string]ssrModuleRef{}
	if m == nil {
		return out
	}
	for _, imp := range imports {
		if imp == nil || !strings.HasPrefix(imp.Source, ".") || path.Ext(imp.Source) != ".js" {
			continue
		}
		module := m.module(filepath.Join(filepath.Dir(file), filepath.FromSlash(imp.Source)))
		for _, spec := range imp.Specifiers {
			if spec.Local != "" && spec.Imported != "" && spec.Imported != "default" {
				out[spec.Local] = ssrModuleRef{Module: module, Export: spec.Imported}
			}
		}
	}
	return out
}

// module returns the compiled module of a source file.
func (m *SSRModules) module(file string) *ssrModule {
	abs, _ := filepath.Abs(file)
	source := m.hosts.Project.relative(abs)
	if module, ok := m.modules[source]; ok {
		if module.compiling && module.problem == nil {
			module.problem = ssrExprUnsupported(0, "%s is part of a circular module import, which is not supported during server rendering", source)
		}
		return module
	}
	module := &ssrModule{Source: source, abs: abs, status: map[string]error{}, fields: map[string]string{}, functions: map[string]bool{}}
	m.modules[source] = module
	srcRoot, _ := filepath.Abs(m.srcRoot)
	rel, err := filepath.Rel(srcRoot, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		module.problem = ssrExprUnsupported(0, "module %s is outside the source root", source)
		return module
	}
	stem := strings.TrimSuffix(filepath.ToSlash(rel), ".js")
	module.ImportPath = path.Join(m.layout.ModulePath, "modules", stem)
	module.Package = ssrModulePackageName(path.Base(stem))
	slug := goFileStem(strings.ReplaceAll(stem, "/", "_"))
	module.Alias, module.Var = "ssrMod_"+slug, "ssrM_"+slug
	module.dir = filepath.Join(m.layout.Root, "server", "modules", filepath.FromSlash(stem))
	module.compiling = true
	m.compile(module)
	module.compiling = false
	m.order = append(m.order, module)
	return module
}

func ssrModulePackageName(base string) string {
	name := strings.ReplaceAll(goFileStem(base), "_", "")
	if name == "" || !unicode.IsLetter(rune(name[0])) || token.IsKeyword(name) {
		name = "m" + name
	}
	return name
}

// ssrModuleFieldName is the exported Go field of an export.
func ssrModuleFieldName(name string, taken map[string]bool) string {
	field := ssrGoExportName(strings.TrimLeft(name, "_$"))
	if field == "" || !unicode.IsUpper([]rune(field)[0]) {
		field = "X" + field
	}
	field = strings.ReplaceAll(field, "$", "_")
	for base, n := field, 2; taken[field]; n++ {
		field = base + strconv.Itoa(n)
	}
	taken[field] = true
	return field
}

// compile translates a module: its exports and the New function.
func (m *SSRModules) compile(module *ssrModule) {
	data, err := os.ReadFile(module.abs)
	if err != nil {
		module.problem = err
		return
	}
	program, diagnostics := ParseJSWithDiagnostics(string(data))
	if program == nil || len(diagnostics) > 0 {
		module.problem = ssrExprUnsupported(0, "%s does not parse", module.Source)
		return
	}
	plan := &SSRRenderPlan{
		ComponentName: module.Package, Functions: map[string]*JsFuncDecl{}, PlainInitializers: map[string]JsExpr{},
		StateInitializers: map[string]JsExpr{}, DerivedInitializers: map[string]JsExpr{},
	}
	var exports []string
	var imports []*Import
	for _, stmt := range program.Body {
		exported := false
		if export, ok := stmt.(*JsExportDecl); ok && export.Declaration != nil && !export.Default {
			stmt, exported = export.Declaration, true
		}
		switch decl := stmt.(type) {
		case *JsImportDecl:
			imports = append(imports, &Import{Source: decl.Source, Specifiers: decl.Specifiers})
		case *JsFuncDecl:
			plan.Functions[decl.Name] = decl
			if exported {
				exports = append(exports, decl.Name)
			}
		case *JsVarDecl:
			if decl.Kind != "const" {
				m.fail(module, program, stmt, "top-level %s bindings are mutable module state; only const is admitted (T-7.3b)", decl.Kind)
				continue
			}
			for _, declarator := range decl.Decls {
				name := identName(declarator.Id)
				if name == "" || declarator.Init == nil {
					m.fail(module, program, stmt, "top-level destructuring is not supported during server rendering")
					continue
				}
				plan.PlainInitializers[name] = declarator.Init
				plan.LocalOrder = append(plan.LocalOrder, name)
				if exported {
					exports = append(exports, name)
				}
			}
		case *JsEmptyStmt:
		default:
			m.fail(module, program, stmt, "top-level statements other than imports, functions, and const declarations are side effects (T-7.3b)")
		}
	}
	sort.Strings(exports)
	taken := map[string]bool{}
	for _, name := range exports {
		module.fields[name] = ssrModuleFieldName(name, taken)
	}
	if module.problem != nil {
		for _, name := range exports {
			module.status[name] = module.problem
		}
		return
	}
	// Host bindings of the module's own functions and imported names.
	plan.HostProject = m.hosts.Project
	plan.Hosts, plan.HostSymbols, plan.HostDecls = map[string]*SSRHostBinding{}, map[string]SSRHostSymbol{}, map[string]*JsFuncDecl{}
	for name := range ssrComponentFunctions(plan) {
		plan.HostSymbols[name] = SSRHostSymbol{Source: module.Source, Name: name}
	}
	plan.ModuleRefs = m.refs(module.abs, imports)
	for local, ref := range plan.ModuleRefs {
		plan.HostSymbols[local] = SSRHostSymbol{Source: ref.Module.Source, Name: ref.Export}
		if decl := m.hosts.Sources[ref.Module.Source][ref.Export]; decl != nil {
			plan.HostDecls[local] = decl
		}
	}
	for local, symbol := range plan.HostSymbols {
		if binding, ok := m.hosts.Bindings[symbol]; ok {
			plan.Hosts[local] = binding
		}
	}

	region := newSSRRegion(plan)
	region.File, region.source = module.Source, string(data)
	ctx := ssrComponentContext(NewSSRExprContext(plan))
	table := newSSRFuncTable(plan, region)
	table.context = goast.NewIdent("ssrCtx")
	ctx.funcs = table
	table.base = ctx
	region.funcs = table

	functions := ssrComponentFunctions(plan)
	for _, name := range exports {
		if _, host := plan.Hosts[name]; host {
			// Importers call the host directly.
			module.status[name] = ssrExprUnsupported(0, "%s is implemented by a host", name)
			continue
		}
		if functions[name] != nil {
			module.functions[name] = true
			module.status[name] = ssrWithReason(table.compile(name), nil, fmt.Sprintf("function %q of %s", name, module.Source))
			if table.compiled[name] == nil && module.status[name] == nil {
				module.status[name] = ssrExprUnsupported(0, "%q could not be translated", name)
			}
			continue
		}
		module.status[name] = nil
	}

	// Const bindings, in declaration order.
	var consts []goast.Stmt
	evaluated := map[string]bool{}
	for _, name := range plan.LocalOrder {
		if functions[name] != nil {
			continue
		}
		builder := newSSRIRBuilder(ctx)
		builder.locals, builder.current, builder.js = true, name, true
		builder.site = region.siteLabel(plan.PlainInitializers[name].NodePos())
		ir, err := builder.build(plan.PlainInitializers[name], 0)
		region.absorb(builder)
		if err == nil {
			for read := range builder.reads {
				if !evaluated[read] && functions[read] == nil {
					err = ssrExprUnsupported(0, "%q reads %q, which has no server value", name, read)
				}
			}
		}
		if err != nil {
			if _, exported := module.fields[name]; exported {
				module.status[name] = ssrWithReason(err, err, fmt.Sprintf("const %q of %s", name, module.Source))
			}
			continue
		}
		evaluated[name] = true
		local := goast.NewIdent(ssrLocalName(name))
		value := lowerSSRIR(ir)
		if table.captured[name] {
			consts = append(consts, &goast.AssignStmt{Lhs: []goast.Expr{local}, Tok: token.ASSIGN, Rhs: []goast.Expr{value}})
		} else {
			consts = append(consts, ssrVarAny(local.Name, value), ssrUse(local.Name))
		}
		if builder.throws {
			consts = append(consts, ssrModuleCheck())
		}
	}

	// The New function.
	body := []goast.Stmt{ssrUse("ssrCtx")}
	body = append(body, ssrModulePrologue(region, goast.NewIdent("ssrCtx"))...)
	if len(region.modules) > 0 {
		body = append(body, ssrModuleCheck())
	}
	if len(table.compiled) == 0 {
		// declarations() declares captured bindings only with functions.
		table.captured = map[string]bool{}
	}
	body = append(body, table.declarations()...)
	body = append(body, consts...)
	var fields []*goast.Field
	var values []goast.Expr
	for _, name := range exports {
		if module.status[name] != nil {
			continue
		}
		field := module.fields[name]
		var value goast.Expr = goast.NewIdent(ssrLocalName(name))
		var fieldType goast.Expr = goast.NewIdent("any")
		if module.functions[name] {
			value, fieldType = goast.NewIdent(ssrFuncName(name)), goSelector("vela", "Func")
		}
		fields = append(fields, &goast.Field{Names: []*goast.Ident{goast.NewIdent(field)}, Type: fieldType})
		values = append(values, &goast.KeyValueExpr{Key: goast.NewIdent(field), Value: value})
	}
	body = append(body, &goast.ReturnStmt{Results: []goast.Expr{&goast.UnaryExpr{Op: token.AND, X: &goast.CompositeLit{Type: goast.NewIdent("Module"), Elts: values}}}})

	importDecl := &goast.GenDecl{Tok: token.IMPORT, Lparen: 1, Specs: []goast.Spec{
		&goast.ImportSpec{Path: &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote("context")}},
		&goast.ImportSpec{Name: goast.NewIdent("vela"), Path: &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(ssrRuntimeImport())}},
	}}
	module.file = &goast.File{
		Name: goast.NewIdent(module.Package),
		Decls: []goast.Decl{
			importDecl,
			goRuntimeContractDecl(),
			&goast.GenDecl{Tok: token.TYPE, Specs: []goast.Spec{&goast.TypeSpec{Name: goast.NewIdent("Module"), Type: &goast.StructType{Fields: &goast.FieldList{List: fields}}}}},
			&goast.FuncDecl{
				Name: goast.NewIdent("New"),
				Type: &goast.FuncType{
					Params: &goast.FieldList{List: []*goast.Field{
						{Names: []*goast.Ident{goast.NewIdent("ssrJS")}, Type: &goast.StarExpr{X: goSelector("vela", "JS")}},
						{Names: []*goast.Ident{goast.NewIdent("ssrCtx")}, Type: goSelector("context", "Context")},
					}},
					Results: &goast.FieldList{List: []*goast.Field{{Type: &goast.StarExpr{X: goast.NewIdent("Module")}}}},
				},
				Body: &goast.BlockStmt{List: body},
			},
		},
	}
	appendSSRGoImports(module.file, region.hostImports)
}

func (m *SSRModules) fail(module *ssrModule, program *JsProgram, stmt JsStmt, format string, args ...any) {
	if module.problem == nil {
		module.problem = ssrExprUnsupported(0, "%s: "+format, append([]any{module.Source}, args...)...)
	}
}

// ssrModuleCheck leaves New when module initialisation threw; the importer
// checks the exception.
func ssrModuleCheck() goast.Stmt {
	return &goast.IfStmt{Cond: &goast.CallExpr{Fun: goSelector("ssrJS", "Failed")}, Body: &goast.BlockStmt{List: []goast.Stmt{
		&goast.ReturnStmt{Results: []goast.Expr{&goast.UnaryExpr{Op: token.AND, X: &goast.CompositeLit{Type: goast.NewIdent("Module")}}}},
	}}}
}

// ssrModulePrologue creates the modules a region uses, once per render.
func ssrModulePrologue(region *SSRRegion, ctx goast.Expr) []goast.Stmt {
	if region == nil || len(region.modules) == 0 {
		return nil
	}
	vars := make([]string, 0, len(region.modules))
	for name := range region.modules {
		vars = append(vars, name)
	}
	sort.Strings(vars)
	var out []goast.Stmt
	for _, name := range vars {
		out = append(out,
			&goast.AssignStmt{Lhs: []goast.Expr{goast.NewIdent(name)}, Tok: token.DEFINE, Rhs: []goast.Expr{&goast.CallExpr{Fun: goSelector(region.modules[name], "New"), Args: []goast.Expr{goast.NewIdent("ssrJS"), ctx}}}},
			ssrUse(name),
		)
	}
	return out
}

// write emits the packages of the compiled modules.
func (m *SSRModules) write() error {
	if m == nil {
		return nil
	}
	for _, module := range m.order {
		if module.file == nil {
			continue
		}
		if err := os.MkdirAll(module.dir, 0o755); err != nil {
			return err
		}
		source, err := FormatSSRGoFile(module.file)
		if err != nil {
			return fmt.Errorf("%s: %w", module.Source, err)
		}
		header := "// Code generated by vela from " + module.Source + ". DO NOT EDIT.\n\n"
		if err := os.WriteFile(filepath.Join(module.dir, path.Base(module.ImportPath)+".go"), append([]byte(header), source...), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// ── Use from components and modules ─────────────────────────────────────

// moduleCall calls an export of a module.
func (b *ssrIRBuilder) moduleCall(ref ssrModuleRef, call *JsCallExpr, reason string, pos int) (SSRIRExpr, error) {
	name := call.Callee.(*JsIdent).Name
	if err := ref.err(); err != nil {
		problem := err
		if symbol, bindable := b.ctx.HostSymbols[name]; bindable {
			return nil, b.hostMissing(problem, symbol, call, b.ctx.HostDecls[name], reason)
		}
		return nil, ssrWithReason(problem, problem, reason)
	}
	callee := b.moduleValue(ref)
	var args []SSRIRExpr
	for _, argument := range call.Args {
		if _, spread := argument.(*JsSpreadExpr); spread {
			return nil, ssrExprUnsupported(pos, "spread arguments are not supported yet")
		}
		value, err := b.build(argument, pos)
		if err != nil {
			return nil, err
		}
		args = append(args, value)
	}
	b.usedJS, b.throws = true, true
	b.called(name, SSROutcomeNative, "")
	if ref.Module.functions[ref.Export] {
		return &ssrIRCallFunc{ssrIRBase: b.base(pos, ssrIRFacts{}), Callee: callee, Args: args, Site: b.site}, nil
	}
	return &ssrIRCallFunc{ssrIRBase: b.base(pos, ssrIRFacts{}), Value: &ssrIRGo{Expr: callee}, Args: args, Site: b.site}, nil
}

// moduleIdent reads an export of a module as a value.
func (b *ssrIRBuilder) moduleIdent(ref ssrModuleRef, name string, pos int) (SSRIRExpr, error) {
	if err := ref.err(); err != nil {
		return nil, ssrWithReason(ssrExprUnsupported(pos, "%q imported from %s has no server value: %v", name, ref.Module.Source, err), err, fmt.Sprintf("import %q", name))
	}
	b.usedJS = true
	return &ssrIRGo{ssrIRBase: b.base(pos, ssrIRFacts{}), Expr: b.moduleValue(ref)}, nil
}

func (b *ssrIRBuilder) moduleValue(ref ssrModuleRef) goast.Expr {
	if b.hostImports == nil {
		b.hostImports = map[string]string{}
	}
	b.hostImports[ref.Module.ImportPath] = ref.Module.Alias
	if b.modules == nil {
		b.modules = map[string]string{}
	}
	b.modules[ref.Module.Var] = ref.Module.Alias
	return goSelectorExpr(goast.NewIdent(ref.Module.Var), ref.Module.fields[ref.Export])
}

func (ref ssrModuleRef) err() error {
	if ref.Module.problem != nil {
		return ref.Module.problem
	}
	status, ok := ref.Module.status[ref.Export]
	if !ok {
		return ssrExprUnsupported(0, "%s does not export %q", ref.Module.Source, ref.Export)
	}
	return status
}

// collectSSRGoPackages reads the generated Go files under dir into
// packages, by import path below base.
func collectSSRGoPackages(dir, base string, packages map[string]map[string][]byte) error {
	return filepath.Walk(dir, func(filename string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() || filepath.Ext(filename) != ".go" {
			return nil
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, filepath.Dir(filename))
		if err != nil {
			return err
		}
		importPath := base
		if rel != "." {
			importPath = path.Join(base, filepath.ToSlash(rel))
		}
		if packages[importPath] == nil {
			packages[importPath] = map[string][]byte{}
		}
		packages[importPath][filename] = data
		return nil
	})
}
