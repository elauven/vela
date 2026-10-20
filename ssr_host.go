package main

import (
	"encoding/json"
	"fmt"
	goast "go/ast"
	"go/importer"
	goparser "go/parser"
	"go/token"
	"go/types"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// Developer host implementations (docs/SSR_TRANSLATION_CONTRACT.md §8, task
// 030). A Go function marked //vela:host <name> in the package that mirrors
// a JavaScript source file stands in for that file's function <name>
// during server rendering. The compiler reads the host tree with go/parser
// and go/types only (T-3.4) and never writes an existing file in it.

const (
	SSRConfigFile           = "vela.ssr.json"
	SSRHostMissingCode      = "VELA-SSR-HOST-MISSING"
	SSRHostSignatureCode    = "VELA-SSR-HOST-SIG"
	SSRHostStaleCode        = "VELA-SSR-HOST-STALE"
	ssrHostDirectivePrefix  = "//vela:host "
	ssrDefaultHostDirectory = "ssrhost"
)

// SSRConfig is vela.ssr.json (§8.1).
type SSRConfig struct {
	Module string `json:"module"`
	Host   struct {
		Dir    string `json:"dir"`
		Module string `json:"module"`
	} `json:"host"`
	Runtime struct {
		Module string `json:"module"`
	} `json:"runtime"`
}

// SSRProject locates the project root, the configuration, and the host
// tree.
type SSRProject struct {
	Root    string // absolute
	SrcRoot string // absolute
	Config  SSRConfig
	HostDir string // absolute
}

// loadSSRProject finds vela.ssr.json in srcRoot or its parent. Without one,
// the project root is the parent of srcRoot and the host tree is ssrhost/
// there, which is where diagnostics suggest host files.
func loadSSRProject(srcRoot string) (*SSRProject, error) {
	src, err := filepath.Abs(srcRoot)
	if err != nil {
		return nil, err
	}
	project := &SSRProject{Root: filepath.Dir(src), SrcRoot: src}
	for _, dir := range []string{src, filepath.Dir(src)} {
		data, err := os.ReadFile(filepath.Join(dir, SSRConfigFile))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &project.Config); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Join(dir, SSRConfigFile), err)
		}
		project.Root = dir
		break
	}
	hostDir := project.Config.Host.Dir
	if hostDir == "" {
		hostDir = ssrDefaultHostDirectory
	}
	project.HostDir = filepath.Join(project.Root, filepath.FromSlash(hostDir))
	return project, nil
}

// SSRHostSymbol is a JavaScript function a host can stand in for: its
// source file, relative to the project root, and its name.
type SSRHostSymbol struct {
	Source string `json:"source"`
	Name   string `json:"name"`
}

// relative returns a path relative to the project root, with slashes.
func (p *SSRProject) relative(file string) string {
	abs, err := filepath.Abs(file)
	if err != nil {
		return filepath.ToSlash(file)
	}
	rel, err := filepath.Rel(p.Root, abs)
	if err != nil {
		return filepath.ToSlash(file)
	}
	return filepath.ToSlash(rel)
}

// mirrorDir is the host package directory, relative to the project root,
// for a source file (H-8.2a): src/lib/format.js → ssrhost/src/lib/format,
// src/blocks/VideoBlock.vela → ssrhost/src/blocks/videoblock.
func (p *SSRProject) mirrorDir(source string) string {
	dir, base := path.Split(source)
	ext := path.Ext(base)
	name := strings.TrimSuffix(base, ext)
	if ext == ".vela" {
		name = strings.ToLower(name)
	}
	hostRel, _ := filepath.Rel(p.Root, p.HostDir)
	return path.Join(filepath.ToSlash(hostRel), dir, name)
}

// hostImportPath is the Go import path of a host package directory.
func (p *SSRProject) hostImportPath(mirror string) string {
	hostRel, _ := filepath.Rel(p.Root, p.HostDir)
	rel := strings.TrimPrefix(strings.TrimPrefix(mirror, filepath.ToSlash(hostRel)), "/")
	if rel == "" {
		return p.Config.Host.Module
	}
	return p.Config.Host.Module + "/" + rel
}

// ssrHostPackageName is the Go package name of a mirrored directory.
func ssrHostPackageName(dir string) string {
	var out strings.Builder
	for _, r := range strings.ToLower(path.Base(dir)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			out.WriteRune(r)
		} else {
			out.WriteByte('_')
		}
	}
	name := out.String()
	if name == "" || unicode.IsDigit(rune(name[0])) || token.IsKeyword(name) {
		name = "host_" + name
	}
	return name
}

// ssrHostGoName is the exported Go name suggested for a JavaScript name.
func ssrHostGoName(name string) string {
	return ssrGoExportName(name)
}

// ssrHostFileName is the stub file of a host function: format_currency_host.go.
func ssrHostFileName(name string) string {
	var out strings.Builder
	for i, r := range name {
		if unicode.IsUpper(r) && i > 0 {
			out.WriteByte('_')
		}
		out.WriteRune(unicode.ToLower(r))
	}
	return out.String() + "_host.go"
}

// ── Host packages ────────────────────────────────────────────────────────

// SSRHostParam is one Go parameter of a host function.
type SSRHostParam struct {
	Name string
	Type string
	// Kind is the JavaScript value the parameter takes: "string", "number",
	// "boolean", "array", "object", "time", or "any" (unchecked).
	Kind string
	// AcceptsUndefined: any, a pointer, or vela.Optional (H-8.3a).
	AcceptsUndefined bool
}

// SSRHostBinding is a //vela:host function.
type SSRHostBinding struct {
	Symbol     SSRHostSymbol
	ImportPath string
	Package    string
	GoName     string
	File       string // relative to the project root
	Line       int
	Context    bool
	Params     []SSRHostParam
	Variadic   bool
	Result     string
	// Problem is the signature problem that makes every call fail
	// (VELA-SSR-HOST-SIG), or "".
	Problem string
}

// Signature prints the Go signature of the binding.
func (b *SSRHostBinding) Signature() string {
	var params []string
	if b.Context {
		params = append(params, "ctx context.Context")
	}
	for i, p := range b.Params {
		prefix := ""
		if b.Variadic && i == len(b.Params)-1 {
			prefix = "..."
		}
		params = append(params, p.Name+" "+prefix+p.Type)
	}
	return fmt.Sprintf("func %s(%s) %s", b.GoName, strings.Join(params, ", "), b.Result)
}

// SSRHostProblem is a directive the build rejects.
type SSRHostProblem struct {
	File    string
	Line    int
	Code    string
	Message string
}

func (p SSRHostProblem) Error() string {
	return fmt.Sprintf("%s:%d: [error] %s %s", p.File, p.Line, p.Code, p.Message)
}

// SSRHosts is the analysed host tree of a project.
type SSRHosts struct {
	Project  *SSRProject
	Bindings map[SSRHostSymbol]*SSRHostBinding
	Problems []SSRHostProblem
	// Types holds the type-checked host packages by import path, for the
	// validation of generated packages that import them.
	Types map[string]*types.Package
	// RootModes holds the root mode of every component (true: interactive).
	RootModes map[string]bool
	// Sources holds the bindable functions of every source file.
	Sources map[string]map[string]*JsFuncDecl
	// Modules translates the local .js modules components import.
	Modules *SSRModules
	// Children holds, per component source, the sources of the child
	// components its renderer calls (client-only children excluded).
	Children map[string][]string
}

// ssrSourceFunctions lists, for every mirrored directory, the source file
// and the function names a host may bind there (H-8.2e): exported
// functions of .js modules and top-level functions of component scripts.
func ssrSourceFunctions(project *SSRProject, files []string) map[string]ssrSourceSymbols {
	out := map[string]ssrSourceSymbols{}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		rel := project.relative(file)
		symbols := ssrSourceSymbols{Source: rel, Functions: map[string]*JsFuncDecl{}}
		switch filepath.Ext(file) {
		case ".js":
			if program := ParseJS(string(data)); program != nil {
				ssrCollectFunctions(program, true, symbols.Functions)
			}
		case ".vela":
			if sfc, err := ParseSFC(string(data)); err == nil && sfc.ScriptAST != nil && sfc.ScriptAST.Program != nil {
				ssrCollectFunctions(sfc.ScriptAST.Program, false, symbols.Functions)
			}
		}
		out[project.mirrorDir(rel)] = symbols
	}
	return out
}

type ssrSourceSymbols struct {
	Source    string
	Functions map[string]*JsFuncDecl // a const arrow function has a nil declaration
}

func ssrCollectFunctions(program *JsProgram, exportedOnly bool, out map[string]*JsFuncDecl) {
	for _, stmt := range program.Body {
		exported := false
		if export, ok := stmt.(*JsExportDecl); ok && export.Declaration != nil {
			stmt, exported = export.Declaration, true
		}
		if exportedOnly && !exported {
			continue
		}
		switch decl := stmt.(type) {
		case *JsFuncDecl:
			out[decl.Name] = decl
		case *JsVarDecl:
			if decl.Kind != "const" {
				continue
			}
			// A const arrow function, or a module value a parameterless host
			// can provide (§8.7).
			for _, declarator := range decl.Decls {
				if name := identName(declarator.Id); name != "" && declarator.Init != nil {
					out[name] = nil
				}
			}
		}
	}
}

// analyzeSSRHosts reads the host tree: directives, their targets, and
// their signatures.
func analyzeSSRHosts(project *SSRProject, sources map[string]ssrSourceSymbols) *SSRHosts {
	hosts := &SSRHosts{Project: project, Bindings: map[SSRHostSymbol]*SSRHostBinding{}, Types: map[string]*types.Package{}, Sources: map[string]map[string]*JsFuncDecl{}}
	for _, source := range sources {
		hosts.Sources[source.Source] = source.Functions
	}
	if _, err := os.Stat(project.HostDir); err != nil {
		return hosts
	}
	dirs := map[string][]string{}
	_ = filepath.Walk(project.HostDir, func(file string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(file, ".go") && !strings.HasSuffix(file, "_test.go") {
			dirs[filepath.Dir(file)] = append(dirs[filepath.Dir(file)], file)
		}
		return nil
	})
	dirNames := make([]string, 0, len(dirs))
	for dir := range dirs {
		dirNames = append(dirNames, dir)
	}
	sort.Strings(dirNames)
	loader := &ssrHostImporter{fallback: importer.Default(), packages: map[string]*types.Package{}}
	for _, dir := range dirNames {
		hosts.analyzePackage(dir, dirs[dir], sources, loader)
	}
	return hosts
}

func (hosts *SSRHosts) analyzePackage(dir string, filenames []string, sources map[string]ssrSourceSymbols, loader *ssrHostImporter) {
	project := hosts.Project
	mirror := project.relative(dir)
	fset := token.NewFileSet()
	var files []*goast.File
	sort.Strings(filenames)
	for _, filename := range filenames {
		file, err := goparser.ParseFile(fset, filename, nil, goparser.ParseComments)
		if err != nil {
			hosts.Problems = append(hosts.Problems, SSRHostProblem{File: project.relative(filename), Line: 1, Code: SSRHostSignatureCode, Message: "cannot parse host file: " + err.Error()})
			continue
		}
		files = append(files, file)
	}
	if len(files) == 0 {
		return
	}
	importPath := project.hostImportPath(mirror)
	config := types.Config{Importer: loader, Error: func(error) {}}
	pkg, _ := config.Check(importPath, fset, files, nil)
	if pkg != nil {
		hosts.Types[importPath] = pkg
	}
	source, mirrored := sources[mirror]
	for _, file := range files {
		for _, decl := range file.Decls {
			function, ok := decl.(*goast.FuncDecl)
			if !ok || function.Doc == nil {
				continue
			}
			for _, comment := range function.Doc.List {
				if !strings.HasPrefix(comment.Text, ssrHostDirectivePrefix) {
					continue
				}
				name := strings.TrimSpace(strings.TrimPrefix(comment.Text, ssrHostDirectivePrefix))
				position := fset.Position(comment.Pos())
				location := SSRHostProblem{File: project.relative(position.Filename), Line: position.Line}
				problem := func(code, format string, args ...any) {
					location.Code, location.Message = code, fmt.Sprintf(format, args...)
					hosts.Problems = append(hosts.Problems, location)
				}
				if !mirrored {
					problem(SSRHostStaleCode, "//vela:host %s: no JavaScript source mirrors %s (a .js file or a component at the same path)", name, mirror)
					continue
				}
				if _, exists := source.Functions[name]; !exists {
					problem(SSRHostStaleCode, "//vela:host %s: %s has no function %q (renamed or removed?)", name, source.Source, name)
					continue
				}
				symbol := SSRHostSymbol{Source: source.Source, Name: name}
				if previous, duplicate := hosts.Bindings[symbol]; duplicate {
					problem(SSRHostStaleCode, "//vela:host %s is bound twice: also %s.%s at %s:%d", name, previous.Package, previous.GoName, previous.File, previous.Line)
					continue
				}
				if project.Config.Host.Module == "" {
					problem(SSRHostSignatureCode, "//vela:host %s: %s sets no host.module, so the renderer cannot import %s", name, SSRConfigFile, mirror)
					continue
				}
				binding := &SSRHostBinding{Symbol: symbol, ImportPath: importPath, Package: file.Name.Name, GoName: function.Name.Name,
					File: location.File, Line: location.Line}
				binding.Problem = ssrHostSignature(binding, pkg, function)
				if function.Recv != nil {
					binding.Problem = "a host is a function, not a method"
				}
				if binding.Problem != "" {
					problem(SSRHostSignatureCode, "//vela:host %s: %s: %s", name, binding.GoName, binding.Problem)
				}
				hosts.Bindings[symbol] = binding
			}
		}
	}
}

// ssrHostSignature fills the parameters and result of a binding from
// go/types and returns the reason the signature is not a host signature
// (§8.3), or "".
func ssrHostSignature(binding *SSRHostBinding, pkg *types.Package, function *goast.FuncDecl) string {
	if pkg == nil {
		return "the host package does not type-check"
	}
	object, ok := pkg.Scope().Lookup(function.Name.Name).(*types.Func)
	if !ok {
		return "not a package-level function"
	}
	signature := object.Type().(*types.Signature)
	qualifier := types.RelativeTo(pkg)
	params := signature.Params()
	binding.Variadic = signature.Variadic()
	for i := 0; i < params.Len(); i++ {
		param := params.At(i)
		if i == 0 && types.TypeString(param.Type(), nil) == "context.Context" {
			binding.Context = true
			continue
		}
		t := param.Type()
		if binding.Variadic && i == params.Len()-1 {
			t = t.(*types.Slice).Elem()
		}
		kind, undefined, problem := ssrHostTypeKind(t, true)
		if problem != "" {
			return fmt.Sprintf("parameter %s %s: %s", param.Name(), types.TypeString(t, qualifier), problem)
		}
		name := param.Name()
		if name == "" || name == "_" {
			name = fmt.Sprintf("arg%d", i+1)
		}
		binding.Params = append(binding.Params, SSRHostParam{Name: name, Type: types.TypeString(t, qualifier), Kind: kind, AcceptsUndefined: undefined})
	}
	results := signature.Results()
	switch {
	case results.Len() == 1 && types.TypeString(results.At(0).Type(), nil) != "error":
	case results.Len() == 2 && types.TypeString(results.At(1).Type(), nil) == "error":
	default:
		return "a host returns R or (R, error)"
	}
	if _, _, problem := ssrHostTypeKind(results.At(0).Type(), false); problem != "" {
		return "result " + types.TypeString(results.At(0).Type(), qualifier) + ": " + problem
	}
	parts := make([]string, results.Len())
	for i := range parts {
		parts[i] = types.TypeString(results.At(i).Type(), qualifier)
	}
	binding.Result = parts[0]
	if len(parts) == 2 {
		binding.Result = "(" + strings.Join(parts, ", ") + ")"
	}
	return ""
}

// ssrHostTypeKind classifies a boundary type (H-8.3b).
func ssrHostTypeKind(t types.Type, parameter bool) (kind string, acceptsUndefined bool, problem string) {
	switch types.TypeString(t, nil) {
	case "time.Time":
		return "time", false, ""
	case "any", "interface{}":
		return "any", true, ""
	}
	if named, ok := t.(*types.Named); ok && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == ssrRuntimeImport() && named.Obj().Name() == "Optional" {
		if args := named.TypeArgs(); args != nil && args.Len() == 1 {
			kind, _, problem := ssrHostTypeKind(args.At(0), parameter)
			return kind, true, problem
		}
	}
	switch u := t.Underlying().(type) {
	case *types.Basic:
		switch {
		case u.Kind() == types.Invalid:
			return "any", false, "" // an unresolved import; converted at run time
		case u.Info()&types.IsString != 0:
			return "string", false, ""
		case u.Info()&types.IsBoolean != 0:
			return "boolean", false, ""
		case u.Info()&types.IsFloat != 0:
			return "number", false, ""
		case u.Info()&types.IsInteger != 0 && u.Kind() != types.Uintptr:
			if !parameter {
				return "", false, "an integer result is not a JavaScript number; return float64"
			}
			return "number", false, ""
		}
		return "", false, "not a boundary type"
	case *types.Pointer:
		kind, _, problem := ssrHostTypeKind(u.Elem(), parameter)
		return kind, true, problem
	case *types.Slice:
		if _, _, problem := ssrHostTypeKind(u.Elem(), parameter); problem != "" {
			return "", false, problem
		}
		return "array", false, ""
	case *types.Map:
		if basic, ok := u.Key().Underlying().(*types.Basic); !ok || basic.Info()&types.IsString == 0 {
			return "", false, "map keys must be strings"
		}
		return "object", false, ""
	case *types.Struct:
		return "object", false, ""
	case *types.Interface:
		if u.Empty() {
			return "any", true, ""
		}
	}
	return "", false, "not a boundary type (functions, channels, and interfaces cannot cross into JavaScript)"
}

// ssrHostImporter imports the standard library, the Vela runtime (from its
// declared types), and nothing else: an unknown import becomes an empty
// package and the types it provides are unresolved, which the signature
// check accepts as converted at run time.
type ssrHostImporter struct {
	fallback types.Importer
	packages map[string]*types.Package
}

func (loader *ssrHostImporter) Import(importPath string) (*types.Package, error) {
	if pkg, ok := loader.packages[importPath]; ok {
		return pkg, nil
	}
	var pkg *types.Package
	if importPath == ssrRuntimeImport() {
		pkg, _ = ssrRuntimeTypesPackage(loader.fallback)
	} else if !strings.Contains(strings.SplitN(importPath, "/", 2)[0], ".") {
		pkg, _ = loader.fallback.Import(importPath)
	}
	if pkg == nil {
		pkg = types.NewPackage(importPath, path.Base(importPath))
		pkg.MarkComplete()
	}
	loader.packages[importPath] = pkg
	return pkg, nil
}

// ── Component binding ───────────────────────────────────────────────────

// bindSSRHosts attaches to a render plan the host bindings and the
// bindable symbols of the functions its scripts call: its own top-level
// functions and the functions it imports from local .js modules.
func bindSSRHosts(plan *SSRRenderPlan, hosts *SSRHosts, src string, analysis *Analysis) {
	if hosts == nil {
		return
	}
	project := hosts.Project
	plan.HostSymbols = map[string]SSRHostSymbol{}
	plan.Hosts = map[string]*SSRHostBinding{}
	plan.HostProject = project
	plan.HostDecls = map[string]*JsFuncDecl{}
	own := project.relative(src)
	for name := range plan.Functions {
		plan.HostSymbols[name] = SSRHostSymbol{Source: own, Name: name}
	}
	if analysis != nil {
		for name := range analysis.constArrowFunctions() {
			plan.HostSymbols[name] = SSRHostSymbol{Source: own, Name: name}
		}
		for name := range plan.PlainInitializers {
			plan.HostSymbols[name] = SSRHostSymbol{Source: own, Name: name}
		}
		for _, imp := range analysis.Imports {
			if imp == nil || !strings.HasPrefix(imp.Source, ".") || path.Ext(imp.Source) != ".js" {
				continue
			}
			source := project.relative(filepath.Join(filepath.Dir(src), filepath.FromSlash(imp.Source)))
			for _, spec := range imp.Specifiers {
				if spec.Local != "" && spec.Imported != "" && spec.Imported != "default" {
					plan.HostSymbols[spec.Local] = SSRHostSymbol{Source: source, Name: spec.Imported}
					if decl := hosts.Sources[source][spec.Imported]; decl != nil {
						plan.HostDecls[spec.Local] = decl
					}
				}
			}
		}
	}
	if analysis != nil && hosts.Modules != nil {
		abs, _ := filepath.Abs(src)
		plan.ModuleRefs = hosts.Modules.refs(abs, analysis.Imports)
	}
	for local, symbol := range plan.HostSymbols {
		if binding, ok := hosts.Bindings[symbol]; ok {
			plan.Hosts[local] = binding
		}
	}
}

// constArrowFunctions lists top-level `const name = (…) => …` functions.
func (a *Analysis) constArrowFunctions() map[string]bool {
	out := map[string]bool{}
	if a == nil || a.Script == nil || a.Script.Program == nil {
		return out
	}
	functions := map[string]*JsFuncDecl{}
	ssrCollectFunctions(a.Script.Program, false, functions)
	for name, decl := range functions {
		if decl == nil {
			out[name] = true
		}
	}
	return out
}

// ssrHostSuggestion is the host fix of a call the compiler cannot
// translate: where to implement it and with which signature.
type ssrHostSuggestion struct {
	Symbol     SSRHostSymbol
	ParamNames []string
	ParamTypes []string
}

// suggestion describes the stub for symbol in project.
func (s *ssrHostSuggestion) describe(project *SSRProject) (file, directive, signature string) {
	mirror := ssrDefaultHostDirectory + "/" + strings.TrimSuffix(s.Symbol.Source, path.Ext(s.Symbol.Source))
	if project != nil {
		mirror = project.mirrorDir(s.Symbol.Source)
	}
	params := []string{"ctx context.Context"}
	for i, name := range s.ParamNames {
		params = append(params, name+" "+s.ParamTypes[i])
	}
	return path.Join(mirror, ssrHostFileName(s.Symbol.Name)), "//vela:host " + s.Symbol.Name,
		fmt.Sprintf("func %s(%s) (any, error)", ssrHostGoName(s.Symbol.Name), strings.Join(params, ", "))
}

// ssrGoTypeForFacts is the Go parameter type suggested for an argument.
func ssrGoTypeForFacts(facts ssrIRFacts) string {
	switch {
	case facts.String:
		return "string"
	case facts.Number:
		return "float64"
	case facts.Bool:
		return "bool"
	case facts.Array:
		return "[]any"
	case facts.Object:
		return "map[string]any"
	}
	return "any"
}

// SSRDefaultModule is the default Go import root of generated packages.
const SSRDefaultModule = "vela/generated/server"

// loadSSRHosts locates the project and analyses its host tree against the
// functions of its components and .js modules.
func loadSSRHosts(srcRoot string, files []string) (*SSRHosts, error) {
	project, err := loadSSRProject(srcRoot)
	if err != nil {
		return nil, err
	}
	sources := append(append([]string{}, files...), collectProjectJSModules(srcRoot)...)
	return analyzeSSRHosts(project, ssrSourceFunctions(project, sources)), nil
}

// scaffoldSSRHosts writes a stub for every suggested host whose file does
// not exist yet (H-8.6a); it never modifies an existing file. It returns
// the created files, relative to the project root.
func scaffoldSSRHosts(project *SSRProject, diagnostics []*SSRSiteDiagnostic) []string {
	var created []string
	seen := map[string]bool{}
	for _, diagnostic := range diagnostics {
		fix := diagnostic.Host
		if fix == nil || seen[fix.File] {
			continue
		}
		seen[fix.File] = true
		file := filepath.Join(project.Root, filepath.FromSlash(fix.File))
		if _, err := os.Stat(file); err == nil {
			continue
		}
		dir := filepath.Dir(file)
		packageName := ssrHostPackageName(dir)
		if existing, _ := filepath.Glob(filepath.Join(dir, "*.go")); len(existing) > 0 {
			if parsed, err := goparser.ParseFile(token.NewFileSet(), existing[0], nil, goparser.PackageClauseOnly); err == nil {
				packageName = parsed.Name.Name
			}
		}
		name := strings.TrimPrefix(fix.Directive, ssrHostDirectivePrefix)
		goName := ssrHostGoName(name)
		source := `package ` + packageName + `

import (
	"context"

	vela "` + ssrRuntimeImport() + `"
)

// ` + goName + ` implements ` + name + ` during server rendering. It must
// return what the JavaScript function returns for the same arguments
// (docs/SSR_TRANSLATION_CONTRACT.md H-8.5a); the client keeps using the
// JavaScript function.
//
` + fix.Directive + `
` + fix.Signature + ` {
	return nil, vela.ErrHostNotImplemented
}
`
		if err := os.MkdirAll(dir, 0o755); err != nil {
			continue
		}
		if err := os.WriteFile(file, []byte(source), 0o644); err != nil {
			continue
		}
		created = append(created, fix.File)
	}
	sort.Strings(created)
	return created
}

// boundName reports whether local, a name a component calls, is bound to
// a host implementation, and returns the JavaScript name of the symbol
// (for the hydration site table of development builds, V-11.3b).
func (hosts *SSRHosts) boundName(src string, sfc *SFC, local string) (string, bool) {
	if hosts == nil || sfc == nil {
		return "", false
	}
	project := hosts.Project
	symbol := SSRHostSymbol{Source: project.relative(src), Name: local}
	if analysis := analyzeScriptAST(sfc.ScriptAST); analysis != nil {
		for _, imp := range analysis.Imports {
			if imp == nil || !strings.HasPrefix(imp.Source, ".") || path.Ext(imp.Source) != ".js" {
				continue
			}
			for _, spec := range imp.Specifiers {
				if spec.Local == local && spec.Imported != "" && spec.Imported != "default" {
					symbol = SSRHostSymbol{Source: project.relative(filepath.Join(filepath.Dir(src), filepath.FromSlash(imp.Source))), Name: spec.Imported}
				}
			}
		}
	}
	if _, ok := hosts.Bindings[symbol]; ok {
		return symbol.Name, true
	}
	return "", false
}
