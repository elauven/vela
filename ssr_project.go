package main

import (
	"crypto/md5"
	"fmt"
	goast "go/ast"
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

// SSRProjectLayout keeps the three output products independent: client modules
// remain consumable by the existing browser toolchain, while Go renderers and
// extracted scoped CSS never share a directory with generated JavaScript.
type SSRProjectLayout struct {
	Root       string
	ModulePath string
	ClientDir  string
	ServerDir  string
	StylesDir  string
}

// SSRArtifactMode states the public runtime contract of one generated SSR
// component. A validation-only hydrate entry is deliberately not called
// executable: consumers must not assume it has a live set/destroy instance.
type SSRArtifactMode string

const (
	SSRArtifactExecutable SSRArtifactMode = "executable"
	SSRArtifactStaticOnly SSRArtifactMode = "static-only"
	// SSRArtifactClientBoundary denotes an explicitly declared client-owned
	// region. It is not an executable component controller: the structural
	// boundary lowerer supplies its deterministic server fallback in Task 030.
	SSRArtifactClientBoundary SSRArtifactMode = "client-boundary"
)

// SSRArtifactManifest is emitted next to browser artifacts so a CLI consumer
// can distinguish an executable hydrate client from static markup validation.
// Paths are relative to the client output directory.
type SSRArtifactManifest struct {
	Component         string          `json:"component"`
	Mode              SSRArtifactMode `json:"mode"`
	Hydrate           string          `json:"hydrate"`
	HydrationClient   string          `json:"hydrationClient,omitempty"`
	StaticEventBridge string          `json:"staticEventBridge,omitempty"`
}

const SSRStateDiagnosticCode = "VELA-SSR-STATE"

func NewSSRProjectLayout(outRoot string) SSRProjectLayout {
	return NewSSRProjectLayoutWithModule(outRoot, "vela/generated/server")
}

func NewSSRProjectLayoutWithModule(outRoot, modulePath string) SSRProjectLayout {
	root := filepath.Clean(outRoot)
	return SSRProjectLayout{
		Root:       root,
		ModulePath: strings.TrimSuffix(strings.TrimSpace(modulePath), "/"),
		ClientDir:  filepath.Join(root, "client"),
		ServerDir:  filepath.Join(root, "server", "components"),
		StylesDir:  filepath.Join(root, "styles"),
	}
}

func (layout SSRProjectLayout) serverPath(src, srcRoot string) string {
	return mirrorGoPath(src, srcRoot, layout.ServerDir)
}

func (layout SSRProjectLayout) propsPath(src, srcRoot string) string {
	return mirrorGoPathWithSuffix(src, srcRoot, layout.ServerDir, "_props.go")
}

func (layout SSRProjectLayout) packageName(src, srcRoot string) string {
	rel := componentPackageDir(src, srcRoot)
	if rel == "." {
		return "components"
	}
	return goFileStem(filepath.Base(rel))
}

func (layout SSRProjectLayout) importPath(src, srcRoot string) string {
	rel := componentPackageDir(src, srcRoot)
	if rel == "." {
		return path.Join(layout.ModulePath, "components")
	}
	return path.Join(layout.ModulePath, "components", filepath.ToSlash(rel))
}

// componentPackageDir is the directory of src relative to srcRoot. Both are
// made absolute first: a child component linked by an absolute path inside
// a project given as a relative --project directory used to fail filepath.Rel
// and fall back to the root package (task 031).
func componentPackageDir(src, srcRoot string) string {
	dir, err1 := filepath.Abs(filepath.Dir(src))
	root, err2 := filepath.Abs(srcRoot)
	if err1 != nil || err2 != nil {
		return "."
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return "."
	}
	return rel
}

func (layout SSRProjectLayout) stylePath(src, srcRoot string) string {
	return mirrorPath(src, srcRoot, layout.StylesDir, ".css")
}

// useSSRRuntimeModule makes generated code and host checks import the Go
// runtime from module (vela.ssr.json "runtime.module", R-12.1b) and returns
// the function that restores the default.
func useSSRRuntimeModule(module string) func() {
	if module == "" {
		return func() {}
	}
	ssrRuntimePath = module
	return func() { ssrRuntimePath = SSRRuntimeImportPath }
}

// buildSSRProject adds server and styles products around the established CSR
// project build. The caller owns user-facing progress and exit handling.
func buildSSRProject(files []string, srcRoot, outRoot string, opts CompileOptions, parallel bool) int {
	hosts, err := loadSSRHosts(srcRoot, files)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  SSR project: %v\n", err)
		return 1
	}
	if opts.SSRModule == "" || (opts.SSRModule == SSRDefaultModule && hosts.Project.Config.Module != "") {
		// vela.ssr.json sets the module unless --ssr-module overrides it.
		opts.SSRModule = hosts.Project.Config.Module
	}
	defer useSSRRuntimeModule(hosts.Project.Config.Runtime.Module)()
	layout := NewSSRProjectLayoutWithModule(outRoot, opts.SSRModule)
	clientOpts := opts
	clientOpts.Hosts = hosts
	hosts.Modules = newSSRModules(layout, srcRoot, hosts)
	// The client module is the ordinary CSR module; hydrate() adopts server
	// HTML with it (C-4.1).
	clientOpts.PackageImports = newPackageImports()
	errors := buildProject(files, srcRoot, layout.ClientDir, clientOpts, parallel)
	hosts.RootModes = ssrRootModes(files, srcRoot)
	for _, problem := range hosts.Problems {
		fmt.Fprintf(os.Stderr, "  x  %s\n", problem.Error())
		errors++
	}
	var suggestions []*SSRSiteDiagnostic
	var report *ssrReportCollector
	if opts.SSRReport != "" {
		report = newSSRReportCollector()
	}
	hosts.Children = map[string][]string{}
	regions := map[string]*SSRRegion{}
	results := map[string]error{}
	for _, src := range files {
		var region *SSRRegion
		results[src] = compileSSRProjectFileInto(src, srcRoot, layout, hosts, &region)
		regions[src] = region
	}
	for _, src := range propagateSSRChildFailures(files, srcRoot, hosts.Children, results) {
		// The renderer was written before its child failed; without it the
		// rest of the Go package still builds.
		_ = os.Remove(layout.serverPath(src, srcRoot))
		_ = os.Remove(layout.stylePath(src, srcRoot))
	}
	for _, src := range files {
		region, err := regions[src], results[src]
		if isSSRClientOnly(err) {
			report.addClientOnly(src, srcRoot)
			fmt.Fprintf(compilerStatusWriter(opts), "  +  %-40s -> client only\n", relOrBase(src, srcRoot))
			continue
		}
		if region != nil {
			for _, record := range region.Records {
				if record.Diagnostic != nil && record.Diagnostic.Host != nil {
					suggestions = append(suggestions, record.Diagnostic)
				}
			}
		}
		report.add(src, srcRoot, region, err)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  x  %s: SSR: %v\n", relOrBase(src, srcRoot), ssrDiagnosticSummary(err))
			if detail := ssrDiagnosticDetail(hosts.Project.relative(src), err); detail != "" {
				fmt.Fprintln(os.Stderr, detail)
			}
			errors++
			continue
		}
		fmt.Fprintf(compilerStatusWriter(opts), "  +  %-40s -> server/styles\n", relOrBase(src, srcRoot))
	}
	if err := hosts.Modules.write(); err != nil {
		fmt.Fprintf(os.Stderr, "  x  SSR modules: %v\n", err)
		errors++
	}
	if errors == 0 {
		if err := validateSSRProjectPackages(layout, hosts.Types); err != nil {
			fmt.Fprintf(os.Stderr, "  x  SSR package validation: %v\n", err)
			errors++
		}
	}
	if err := writeSSRManifest(files, srcRoot, layout, hosts.RootModes, clientOpts.PackageImports); err != nil {
		fmt.Fprintf(os.Stderr, "  x  SSR manifest: %v\n", err)
		errors++
	}
	if opts.SSRScaffoldHosts {
		for _, created := range scaffoldSSRHosts(hosts.Project, suggestions) {
			fmt.Fprintf(compilerStatusWriter(opts), "  +  host stub %s\n", created)
		}
	}
	if report != nil {
		if err := report.write(opts.SSRReport); err != nil {
			fmt.Fprintf(os.Stderr, "  x  SSR report: %v\n", err)
			errors++
		}
	}
	return errors
}

// propagateSSRChildFailures marks a component whose renderer calls a child
// without a Go renderer (its SSR compilation failed) as failed too: its
// generated package would not build, and the report would call it ok
// (task 031). Children are matched by absolute path.
func propagateSSRChildFailures(files []string, srcRoot string, children map[string][]string, results map[string]error) (marked []string) {
	failed := map[string]string{}
	for _, list := range children {
		sort.Strings(list)
	}
	for _, src := range files {
		if err := results[src]; err != nil && !isSSRClientOnly(err) {
			failed[ssrAbs(src)] = src
		}
	}
	for changed := true; changed; {
		changed = false
		for _, src := range files {
			if results[src] != nil {
				continue
			}
			for _, child := range children[src] {
				if failedSrc, ok := failed[ssrAbs(child)]; ok {
					results[src] = fmt.Errorf("VELA-SSR-CHILD: child component %s has no server renderer; fix its SSR diagnostics or declare it <script context=\"client\">", filepath.ToSlash(relOrBase(failedSrc, srcRoot)))
					failed[ssrAbs(src)] = src
					marked = append(marked, src)
					changed = true
					break
				}
			}
		}
	}
	return marked
}

func ssrAbs(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// ValidateSSRProjectPackages checks every materialized generated-server Go
// package as a graph. Renderer files deliberately rely on their preserved
// *_props.go sibling, and components from another directory import its Go
// package, so isolated-file checking cannot catch every conflict.
func ValidateSSRProjectPackages(layout SSRProjectLayout) error {
	return validateSSRProjectPackages(layout, nil)
}

func validateSSRProjectPackages(layout SSRProjectLayout, hostTypes map[string]*types.Package) error {
	packages := map[string]map[string][]byte{}
	if err := collectSSRGoPackages(filepath.Join(layout.Root, "server", "modules"), path.Join(layout.ModulePath, "modules"), packages); err != nil && !os.IsNotExist(err) {
		return err
	}
	err := filepath.Walk(layout.ServerDir, func(filename string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info == nil || info.IsDir() || filepath.Ext(filename) != ".go" {
			return nil
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		dir := filepath.Dir(filename)
		rel, err := filepath.Rel(layout.ServerDir, dir)
		if err != nil {
			return err
		}
		importPath := path.Join(layout.ModulePath, "components")
		if rel != "." {
			importPath = path.Join(importPath, filepath.ToSlash(rel))
		}
		if packages[importPath] == nil {
			packages[importPath] = map[string][]byte{}
		}
		packages[importPath][filename] = data
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := validateGeneratedSSRGoGraph(packages, hostTypes); err != nil {
		return err
	}
	return nil
}

// SSRHydrationBlockedError reports why a component fell back to static-only
// output before the error of that fallback, so the build names the construct
// that blocks hydration rather than an event handler the static bridge cannot
// carry. It unwraps to the fallback error.
type SSRHydrationBlockedError struct {
	Reason   string
	Fallback error
}

// SSRHydrationBlockedDiagnosticCode is reported when a component cannot use
// executable hydration and its static-only fallback is not possible either.
const SSRHydrationBlockedDiagnosticCode = "VELA-SSR-HYDRATION"

func compileSSRProjectFile(src, srcRoot string, layout SSRProjectLayout) error {
	hosts, err := loadSSRHosts(srcRoot, collectVela(srcRoot))
	if err != nil {
		return err
	}
	defer useSSRRuntimeModule(hosts.Project.Config.Runtime.Module)()
	return compileSSRProjectFileInto(src, srcRoot, layout, hosts, nil)
}

// compileSSRProjectFileInto also stores the region site records of the
// component in regionOut when the render plan exists, whatever the result.
func compileSSRProjectFileInto(src, srcRoot string, layout SSRProjectLayout, hosts *SSRHosts, regionOut **SSRRegion) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	sfc, err := ParseSFC(string(data))
	if err != nil {
		return fmt.Errorf("parse component: %w", err)
	}
	if sfc.ClientOnly {
		// A client-only component has no Go renderer (T-9.2a).
		return errSSRClientOnly
	}
	hash := md5.Sum([]byte(src))
	scopeID := fmt.Sprintf("vela-%x", hash[:3])
	analysis := AnalyzeScriptAST(sfc.ScriptAST)
	links, err := ResolveSSRLocalComponentLinks(src, srcRoot, analysis)
	if err != nil {
		return err
	}
	links = LinkSSRComponentPackages(links, srcRoot, layout)
	if hosts.Children != nil {
		for _, link := range links {
			if !link.ClientOnly {
				hosts.Children[src] = append(hosts.Children[src], link.SourcePath)
			}
		}
	}
	NormalizeTableRows(sfc.TemplateAST.Nodes)
	if err := checkSSRBlocksNesting(src, string(data), sfc.TemplateAST.Nodes, links); err != nil {
		return err
	}
	plan, err := BuildSSRRenderPlanWithAnalysisAndLinks(componentName(src), sfc, analysis, scopeID, links)
	if err != nil {
		return err
	}
	bindSSRHosts(plan, hosts, src, analysis)
	if plan.PropsWithoutPresence, err = ssrPropsWithoutPresence(layout.propsPath(src, srcRoot), plan.ComponentName+"Props"); err != nil {
		return err
	}
	if hosts.RootModes != nil {
		plan.RootStatic = !ssrInteractive(hosts.RootModes, src)
	}
	// The region records are kept whether or not the renderer succeeds, so
	// the report covers every component.
	file, region, renderErr := buildSSRGoRendererWithRegion(plan, layout.packageName(src, srcRoot), hosts.Project.relative(src), string(data))
	region.locate(string(data))
	if regionOut != nil {
		*regionOut = region
	}
	if renderErr != nil {
		return renderErr
	}
	return writeSSRBlocksProjectFile(src, srcRoot, layout, plan, file)
}

func ssrProjectPropsSource(plan *SSRRenderPlan, path, packageName string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return data, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	file, err := BuildSSRGoPropsFile(plan, packageName)
	if err != nil {
		return nil, err
	}
	data, err = FormatSSRGoFile(file)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return nil, err
	}
	return data, nil
}

// mirrorGoPath preserves the component directory tree but applies Go source
// filename convention to every emitted component file.
func mirrorGoPath(src, srcRoot, outRoot string) string {
	return mirrorGoPathWithSuffix(src, srcRoot, outRoot, ".go")
}

func mirrorGoPathWithSuffix(src, srcRoot, outRoot, suffix string) string {
	rel, err := filepath.Rel(srcRoot, src)
	if err != nil {
		rel = filepath.Base(src)
	}
	dir := filepath.Dir(rel)
	stem := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
	name := goFileStem(stem) + suffix
	if dir == "." {
		return filepath.Join(outRoot, name)
	}
	return filepath.Join(outRoot, dir, name)
}

func goFileStem(name string) string {
	runes := []rune(name)
	var out []rune
	separator := true
	for index, current := range runes {
		if !unicode.IsLetter(current) && !unicode.IsDigit(current) {
			if !separator && len(out) > 0 {
				out = append(out, rune('_'))
				separator = true
			}
			continue
		}
		upper := unicode.IsUpper(current)
		previousLowerOrDigit := index > 0 && (unicode.IsLower(runes[index-1]) || unicode.IsDigit(runes[index-1]))
		nextLower := index+1 < len(runes) && unicode.IsLower(runes[index+1])
		previousUpper := index > 0 && unicode.IsUpper(runes[index-1])
		if upper && !separator && (previousLowerOrDigit || (previousUpper && nextLower)) {
			out = append(out, rune('_'))
		}
		out = append(out, unicode.ToLower(current))
		separator = false
	}
	stem := strings.Trim(string(out), "_")
	if stem == "" {
		return "component"
	}
	return stem
}

// ssrPropsWithoutPresence reports whether the preserved props sidecar at path
// declares typeName without the Set map[string]bool presence field. A missing
// file is generated with the field, so it reports false (task 060).
func ssrPropsWithoutPresence(path, typeName string) (bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	file, err := goparser.ParseFile(token.NewFileSet(), path, data, goparser.SkipObjectResolution)
	if err != nil {
		// The Go build reports the broken sidecar; keep the reflective check.
		return true, nil
	}
	for _, decl := range file.Decls {
		gen, ok := decl.(*goast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec := spec.(*goast.TypeSpec)
			structType, ok := typeSpec.Type.(*goast.StructType)
			if typeSpec.Name.Name != typeName || !ok || typeSpec.TypeParams != nil {
				continue
			}
			for _, field := range structType.Fields.List {
				for _, name := range field.Names {
					if name.Name == "Set" {
						return !ssrIsStringBoolMap(field.Type), nil
					}
				}
			}
			return true, nil
		}
	}
	return true, nil
}

func ssrIsStringBoolMap(expr goast.Expr) bool {
	m, ok := expr.(*goast.MapType)
	if !ok {
		return false
	}
	key, keyOK := m.Key.(*goast.Ident)
	value, valueOK := m.Value.(*goast.Ident)
	return keyOK && valueOK && key.Name == "string" && value.Name == "bool"
}
