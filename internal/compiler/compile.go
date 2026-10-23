// Package compiler is the compile pipeline of Vela: one component from
// source to module with its diagnostics, project and SSR project builds,
// the file flags and vela.config.json. The vela command and the language
// server (task 065) share it.
package compiler

import (
	"github.com/elauven/vela/internal/analyzer"
	"github.com/elauven/vela/internal/codegen"
	"github.com/elauven/vela/internal/css"
	"github.com/elauven/vela/internal/diag"
	"github.com/elauven/vela/internal/jsast"
	"github.com/elauven/vela/internal/project"
	"github.com/elauven/vela/internal/sfcast"
	"github.com/elauven/vela/internal/ssr"
	"github.com/elauven/vela/runtime"

	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// StageRuntime writes the compiler canonical runtime into outDir. Project
// builds write it once at the output root, and every generated module imports
// that file through a relative path (runtimeImportFor), so a page loads one
// runtime instance. Production builds get the production variant of the
// core runtime (R-12.2c).
func StageRuntime(outDir string, opts CompileOptions) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "runtime.js"), RuntimeSource(opts.Production), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "runtime-blocks.js"), runtime.Blocks, 0o644); err != nil {
		return err
	}
	// hydrate() of the block engine; a page imports it only to hydrate server
	// HTML (C-4.1).
	return os.WriteFile(filepath.Join(outDir, "runtime-hydrate.js"), hydrateRuntimeSource(opts.HydrationVerify), 0o644)
}

// RuntimeSource returns the core runtime for the build mode.
func RuntimeSource(production bool) []byte {
	if !production {
		return runtime.Core
	}
	return []byte(strings.Replace(string(runtime.Core), runtime.DevelopmentFlag, "const DEV = false;", 1))
}

// CompileOptions controls code generation behaviour.
type CompileOptions struct {
	Minify  bool
	EmitAST bool // write <n>.ast.json (input AST) alongside the compiled JS
	GenAST  bool // write <n>.genast.json (generated output AST) for inspection

	// CSSExternal selects CSS delivery.  When false (default), a
	// component injects its scoped <style> into document.head at
	// runtime (CSS-in-JS).  When true, the scoped CSS is written
	// to a sibling <n>.css file and no runtime injection is
	// emitted — the consumer links the stylesheet.
	CSSExternal bool
	SourceMap   bool // emit a v3 .js.map alongside generated JS
	// SitePath names the component in the hydration site table of development
	// builds (V-11.3b); Hosts resolves host symbols for that table.
	SitePath  string
	Hosts     *ssr.SSRHosts
	TraceSink TraceSink // optional source-free compiler trace observer
	// TraceComponentID is an internal source-free override used for project
	// traces. File mode derives a safe basename automatically.
	TraceComponentID string
	// StatusWriter receives human compiler progress. It defaults to stdout;
	// --trace=- redirects it to stderr so stdout remains valid JSONL.
	StatusWriter io.Writer

	// TargetProfile enables sound direct-global diagnostics for a known host.
	// The zero value is permissive for backwards compatibility.
	TargetProfile           analyzer.TargetProfile
	UnavailableCapabilities []string // direct known globals disabled by the host
	Strict                  bool     // elevate proven unresolved reads to errors
	SSRModule               string   // Go module import root for generated SSR server packages
	SSRReport               string   // --ssr-report: write the region site records as JSON (task 043)
	SSRScaffoldHosts        bool     // --ssr-scaffold-hosts: create stubs for missing host implementations (task 030)
	HydrationVerify         string   // --hydration-verify: repair (default), fail, or off (V-11.3c)
	Production              bool     // --mode=production: production runtime variant
	// BareRuntimeImports imports the runtime npm package instead of copies
	// beside the output (--runtime-import=bare, R-12.2f).
	BareRuntimeImports bool
	// PackageImports collects the package imports of a project build for
	// vela.manifest.json; BuildProject creates it when it is nil.
	PackageImports *packageImports
	// CheckDeps warns about package imports that do not resolve in
	// node_modules (--check-deps).
	CheckDeps bool

	// SourceComponentImports (--component-imports=vela) keeps `.vela`
	// specifiers in generated and copied modules, for bundler plugins.
	SourceComponentImports bool
	// DiagnosticsJSON prints diagnostics as JSON lines (--diagnostics=json).
	DiagnosticsJSON bool
	// ErrWriter receives build errors; it defaults to stderr.
	ErrWriter io.Writer
	// Report collects the diagnostics of a build for vela check.
	Report *diagReport
	// RuntimeImport is the module specifier generated code uses for the
	// runtime. Empty means "./runtime.js"; project builds set a path to the
	// single runtime at the output root.
	RuntimeImport string
}

func CompilerStatusWriter(opts CompileOptions) io.Writer {
	if opts.StatusWriter != nil {
		return opts.StatusWriter
	}
	return os.Stdout
}

func SplitCapabilityList(raw string) []string {
	var out []string
	seen := map[string]bool{}
	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// BuildProject compiles all files, optionally in parallel.
// Returns the number of files that failed.
func BuildProject(files []string, srcRoot, outRoot string, opts CompileOptions, parallel bool) int {
	if opts.PackageImports == nil {
		opts.PackageImports = newPackageImports()
	}
	type result struct{ ok bool }
	results := make([]result, len(files))

	if parallel {
		var wg sync.WaitGroup
		for i, src := range files {
			wg.Add(1)
			go func(i int, src string) {
				defer wg.Done()
				results[i] = result{CompileProjectFile(src, srcRoot, outRoot, opts)}
			}(i, src)
		}
		wg.Wait()
	} else {
		for i, src := range files {
			results[i] = result{CompileProjectFile(src, srcRoot, outRoot, opts)}
		}
	}

	errors := 0
	for _, r := range results {
		if !r.ok {
			errors++
		}
	}

	// Local JavaScript modules are part of a Vela project contract: generated
	// components retain imports such as ./format.js, so mirror these files next
	// to the compiled modules instead of leaving dangling imports in dist.
	errors += copyProjectJSModules(srcRoot, outRoot, opts)
	// An SSR build rewrites the manifest with root modes (writeSSRManifest).
	if err := writeProjectManifest(files, srcRoot, outRoot, nil, opts.PackageImports); err != nil {
		reportBuildIssue(opts, "cannot write vela.manifest.json: %v", err)
		errors++
	}
	if opts.BareRuntimeImports {
		// The application installs the runtime package (R-12.2f).
		return errors
	}
	if err := StageRuntime(outRoot, opts); err != nil {
		reportBuildIssue(opts, "cannot stage runtime: %v", err)
		errors++
	}
	return errors
}

func CompileProjectFile(src, srcRoot, outRoot string, opts CompileOptions) bool {
	dest := project.MirrorPath(src, srcRoot, outRoot, ".js")
	opts.RuntimeImport = runtimeSpecifier(opts, filepath.Dir(dest), outRoot)
	if rel, err := filepath.Rel(srcRoot, src); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		opts.TraceComponentID = filepath.ToSlash(rel)
	}
	if rel, err := filepath.Rel(filepath.Dir(filepath.Clean(srcRoot)), src); err == nil {
		opts.SitePath = filepath.ToSlash(rel)
	}
	return CompilePrint(src, dest, opts)
}

// CompilePrint compiles src → dest, prints a one-line status, returns success.
func CompilePrint(src, dest string, opts CompileOptions) bool {
	diags, err := CompileFile(src, dest, opts)

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

	if err != nil {
		if !hasErrorDiagnostic(diags) {
			// A failure without diagnostics (an unreadable file) becomes one.
			d := buildIssue(src, "%v", err)
			opts.Report.add(d)
			if opts.DiagnosticsJSON {
				line, _ := json.Marshal(d.Machine())
				fmt.Fprintf(ErrorWriter(opts), "%s\n", line)
			}
		}
		if !opts.DiagnosticsJSON {
			fmt.Fprintf(ErrorWriter(opts), "  ✗  %-40s  %v\n", src, err)
		}
		return false
	}

	suffix := ""
	warns := 0
	for _, d := range diags {
		if d.Sev == diag.SevWarning {
			warns++
		}
	}
	if warns > 0 {
		suffix += fmt.Sprintf("  (%d warn)", warns)
	}
	if opts.Minify {
		suffix += " [min]"
	}
	if opts.EmitAST {
		suffix += " [ast]"
	}

	fmt.Fprintf(CompilerStatusWriter(opts), "  ✓  %-40s → %s%s\n", src, dest, suffix)
	return true
}

func hasErrorDiagnostic(diags []*diag.Diagnostic) bool {
	for _, d := range diags {
		if d.Sev == diag.SevError {
			return true
		}
	}
	return false
}

// CompileFile compiles one .vela source file, writing JS to dest (full path).
// The output directory is created automatically.
func CompileFile(src, dest string, opts CompileOptions) ([]*diag.Diagnostic, error) {
	read := func() ([]byte, error) { return os.ReadFile(src) }
	return compileComponent(src, dest, opts, read, func(m *compiledModule) error { return writeCompiledModule(dest, m) })
}

// compiledModule is the output of one component before anything is written.
type compiledModule struct {
	JS      string // the module, without a sourceMappingURL comment
	Map     []byte // v3 source map with SourceMap
	CSS     string // scoped CSS with CSSExternal
	Imports []ModuleImport
}

// writeCompiledModule writes a compiled component to dest: the module, its
// source map and its external stylesheet.
func writeCompiledModule(dest string, m *compiledModule) error {
	if writeErr := os.MkdirAll(filepath.Dir(dest), 0755); writeErr != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(dest), writeErr)
	}
	if len(m.Map) > 0 {
		mapPath := dest + ".map"
		if writeErr := os.WriteFile(mapPath, m.Map, 0644); writeErr != nil {
			return writeErr
		}
		m.JS += "\n//# sourceMappingURL=" + filepath.Base(mapPath) + "\n"
	}
	if writeErr := os.WriteFile(dest, []byte(m.JS), 0644); writeErr != nil {
		return writeErr
	}
	if m.CSS != "" {
		cssPath := strings.TrimSuffix(dest, ".js") + ".css"
		if writeErr := os.WriteFile(cssPath, []byte(m.CSS), 0644); writeErr != nil {
			return fmt.Errorf("write %s: %w", cssPath, writeErr)
		}
	}
	return nil
}

// compileComponent compiles the component src: read returns its source and
// emit receives the result. dest names the output for source maps and the
// debug AST files.
func compileComponent(src, dest string, opts CompileOptions, read func() ([]byte, error), emit func(*compiledModule) error) (diags []*diag.Diagnostic, err error) {
	name := project.ComponentName(src)
	module := &compiledModule{}
	trace := newTraceRecorder(src, name, opts)
	var ds *diag.DiagSet
	internalFailure := false
	defer func() {
		if r := recover(); r != nil {
			internalFailure = true
			if ds != nil {
				ds.Error(0, "internal compiler error while generating this component: %v", r)
				diags = ds.All()
			}
			err = fmt.Errorf("internal compiler error: %v", r)
		}
		if trace != nil {
			if diags == nil && ds != nil {
				diags = ds.All()
			}
			outcome := "success"
			if internalFailure {
				outcome = "internal-error"
			} else if err != nil {
				outcome = "failed"
			}
			if traceErr := trace.finish(diags, outcome); traceErr != nil && err == nil {
				err = fmt.Errorf("write compiler trace: %w", traceErr)
			}
		}
	}()

	var data []byte
	if err = trace.stage("read-source", func() error {
		var readErr error
		data, readErr = read()
		return readErr
	}); err != nil {
		return nil, err
	}
	raw := string(data)
	if trace != nil {
		trace.trace.Metrics.SourceBytes = len(data)
	}

	scope := project.ScopeID(src)

	ds = diag.NewDiagSet(src, raw)
	if opts.SourceMap && opts.Minify {
		ds.Error(0, "--sourcemap cannot be combined with --minify; Vela minifier is not source-map-aware")
		return ds.All(), fmt.Errorf("invalid source map options")
	}

	var sfc *sfcast.SFC
	if err = trace.stage("parse-sfc", func() error {
		var parseErr error
		sfc, parseErr = sfcast.ParseSFC(raw)
		return parseErr
	}); err != nil {
		var section *sfcast.SectionError
		if errors.As(err, &section) {
			ds.Report(diag.SevError, section.Pos, section.Pos+len("<"+section.Section+">"), "VELA-SFC-UNCLOSED", "Close the section with </"+section.Section+">.", "%s", section.Message)
		} else {
			ds.Error(0, "%v", err)
		}
		return ds.All(), fmt.Errorf("parse: %w", err)
	}
	if !opts.Minify {
		sfc.SitePath = opts.SitePath
		if sfc.SitePath == "" {
			sfc.SitePath = filepath.ToSlash(src)
		}
		if opts.Hosts != nil {
			hosts, file := opts.Hosts, src
			sfc.SiteHosts = func(local string) (string, bool) { return hosts.BoundName(file, sfc, local) }
		}
	}

	var tmpl []sfcast.Node
	if err = trace.stage("parse-template", func() error {
		if sfc.TemplateAST == nil {
			return fmt.Errorf("missing template AST")
		}
		if len(sfc.TemplateAST.Diagnostics) > 0 {
			ds.PushOffset(sfc.TemplateAST.Span.Start)
			for _, issue := range sfc.TemplateAST.Diagnostics {
				ds.ErrorWith(issue.Pos, "VELA-TEMPLATE-SYNTAX", "fix the template syntax", "%s", issue.Message)
			}
			ds.PopOffset()
			return fmt.Errorf("template syntax validation failed")
		}
		if issues := sfcast.ValidateTemplateAST(sfc.TemplateAST); len(issues) > 0 {
			ds.PushOffset(sfc.TemplateAST.Span.Start)
			for _, issue := range issues {
				ds.ErrorWith(issue.Pos, "VELA-TEMPLATE-AST", "fix the template structure", "%s", issue.Message)
			}
			ds.PopOffset()
			return fmt.Errorf("template AST validation failed")
		}
		tmpl = sfc.TemplateAST.Nodes
		return nil
	}); err != nil {
		if !ds.HasErrors() {
			ds.Error(0, "template: %v", err)
		}
		return ds.All(), fmt.Errorf("template: %w", err)
	}
	if trace != nil {
		trace.trace.Metrics.TemplateNodes = countTemplateNodes(tmpl)
	}

	var analysis *analyzer.Analysis
	var scriptProg *jsast.JsProgram
	if err = trace.stage("parse-script-and-analyze", func() error {
		if len(sfc.ScriptAST.Diagnostics) > 0 {
			ds.PushOffset(sfc.ScriptAST.Span.Start)
			for _, issue := range sfc.ScriptAST.Diagnostics {
				ds.ErrorWith(issue.Pos, "VELA-JS-SYNTAX", "fix the JavaScript syntax in <script>", "%s", issue.Message)
			}
			ds.PopOffset()
			return fmt.Errorf("script syntax validation failed")
		}
		analysis = analyzer.AnalyzeScriptAST(sfc.ScriptAST)
		if opts.CheckDeps {
			checkScriptPackageImports(src, sfc.ScriptAST, ds)
		}
		scriptProg = sfc.ScriptAST.Program
		module.Imports = sourceModuleImports(src, scriptProg)
		return nil
	}); err != nil {
		return ds.All(), fmt.Errorf("compilation failed with %d error(s)", len(ds.Errors()))
	}
	if trace != nil {
		trace.trace.Metrics.ScriptNodes = countJSNodes(scriptProg)
		trace.trace.Metrics.ScriptParseCount = 1
	}

	if opts.EmitAST {
		if astJSON, astErr := DumpParsedAST(src, sfc, tmpl, analysis); astErr == nil {
			astPath := strings.TrimSuffix(dest, ".js") + ".ast.json"
			_ = os.MkdirAll(filepath.Dir(astPath), 0755)
			_ = os.WriteFile(astPath, []byte(astJSON), 0644)
		}
	}

	if err = trace.stage("validate-input", func() error {
		if sfc.StyleAST != nil {
			ds.PushOffset(sfc.StyleAST.Span.Start)
			for _, issue := range sfc.StyleAST.Diagnostics {
				ds.ErrorWith(issue.Pos, "VELA-CSS-SYNTAX", "fix the stylesheet syntax", "%s", issue.Message)
			}
			for _, issue := range css.ValidateCssAST(sfc.StyleAST) {
				ds.ErrorWith(issue.Pos, "VELA-CSS-AST", "fix the stylesheet structure", "%s", issue.Message)
			}
			ds.PopOffset()
		}
		// Scope + flow analysis over the original parsed program. All these
		// passes report positions relative to the script body, so the DiagSet
		// offset translates them into original component coordinates.
		ds.PushOffset(sfc.ScriptOffset)
		scriptScope := analyzer.BuildScopesWithOptions(scriptProg, ds, analyzer.ScopeOptions{
			TargetProfile:           opts.TargetProfile,
			UnavailableCapabilities: opts.UnavailableCapabilities,
			Strict:                  opts.Strict,
		})
		DetectUnreachable(scriptProg, ds)
		ValidateScript(scriptProg, analysis, ds)
		validateStateWrites(scriptProg, scriptScope, tmpl, analysis, ds)
		ValidateDerivedCycles(scriptProg, analysis, ds)
		ValidateDerivedPurity(scriptProg, analysis, ds)
		validateScriptFormsWithAnalysis(scriptProg, analysis, ds)
		ValidateEffectBody(scriptProg, analysis, ds)
		ds.PopOffset()

		ds.PushOffset(sfc.TemplateOffset)
		ValidateTemplate(tmpl, analysis, ds)
		ValidateTemplateForms(tmpl, sfc.Template, ds)
		ValidateTemplateRefs(tmpl, analysis, scriptScope, sfc.Template, ds)
		ds.PopOffset()
		if ds.HasErrors() {
			return fmt.Errorf("input validation failed")
		}
		return nil
	}); err != nil {
		return ds.All(), fmt.Errorf("compilation failed with %d error(s)", len(ds.Errors()))
	}

	if opts.Minify {
		if err = trace.stage("optimize", func() error {
			analysis = EliminateDeadCodeAST(sfc.ScriptAST, analysis, sfc.Template)
			return nil
		}); err != nil {
			return ds.All(), err
		}
	} else {
		trace.skipped("optimize", "disabled")
	}

	var target *jsast.JsProgram
	if trace != nil {
		if err = trace.stage("build-target-ast", func() error {
			target = codegen.BuildProgramUnvalidatedWith(name, sfc, tmpl, analysis, scope, programOptions(opts))
			trace.trace.Metrics.TargetNodes = countJSNodes(target)
			return nil
		}); err != nil {
			return ds.All(), err
		}
		if err = trace.stage("validate-target-ast", func() error {
			codegen.ValidateGeneratedProgram(target)
			return nil
		}); err != nil {
			return ds.All(), err
		}
	} else {
		target = codegen.BuildProgramUnvalidatedWith(name, sfc, tmpl, analysis, scope, programOptions(opts))
		codegen.ValidateGeneratedProgram(target)
	}

	codegen.FinalizeRuntimeImports(target, opts.RuntimeImport)
	opts.PackageImports.add(dest, programPackageImports(target))

	if opts.GenAST {
		genPath := strings.TrimSuffix(dest, ".js") + ".genast.json"
		_ = os.MkdirAll(filepath.Dir(genPath), 0755)
		_ = os.WriteFile(genPath, []byte(ASTToJSON(target, false)), 0644)
	}

	var code string
	var mapBytes []byte
	if opts.SourceMap {
		var locations []jsast.PrintLocation
		if err = trace.stage("print-target", func() error {
			jsast.ShiftTree(target, sfc.ScriptOffset)
			body := jsast.PrintJSWithLocations(target, jsast.PrintPretty, func(loc jsast.PrintLocation) {
				loc.GeneratedLine += 2
				locations = append(locations, loc)
			})
			code = codegen.HeaderComment + "\n\n" + body
			return nil
		}); err != nil {
			return ds.All(), err
		}
		if err = trace.stage("build-source-map", func() error {
			m := codegen.BuildSourceMapV3(filepath.Base(dest), filepath.ToSlash(src), raw, locations)
			var mapErr error
			mapBytes, mapErr = codegen.SourceMapJSON(m)
			return mapErr
		}); err != nil {
			return ds.All(), err
		}
	} else {
		if err = trace.stage("print-target", func() error {
			code = codegen.HeaderComment + "\n\n" + jsast.PrintJS(target, jsast.PrintPretty)
			return nil
		}); err != nil {
			return ds.All(), err
		}
		trace.skipped("build-source-map", "disabled")
	}
	if opts.Minify {
		code = Minify(code)
	}
	if trace != nil {
		trace.trace.Metrics.SourceMapBytes = len(mapBytes)
	}
	// Never write a module the compiler cannot read back (task 055).
	if err = CheckEmittedJS(code); err != nil {
		ds.Error(0, "%v", err)
		return ds.All(), err
	}

	if err = trace.stage("write-output", func() error {
		module.JS, module.Map = code, mapBytes
		if opts.CSSExternal {
			module.CSS = codegen.ExtractScopedCSS(sfc, scope)
		}
		if writeErr := emit(module); writeErr != nil {
			return writeErr
		}
		if trace != nil {
			trace.trace.Metrics.OutputBytes = len(module.JS)
			trace.trace.Metrics.CSSBytes = len(module.CSS)
		}
		return nil
	}); err != nil {
		return ds.All(), err
	}

	return ds.All(), nil
}

// CheckEmittedJS reparses a generated module. A printer or minifier defect
// would otherwise reach the browser as a module that fails to load, or as
// different code; it becomes an internal compiler error instead.
func CheckEmittedJS(code string) error {
	prog, diags := jsast.ParseJSWithDiagnostics(code)
	if len(diags) == 0 {
		diags = jsast.EarlyErrors(prog, jsast.EarlyOptions{TopLevelAwait: true})
	}
	if len(diags) > 0 {
		return fmt.Errorf("%s: generated JavaScript does not parse (%s); this is a compiler bug", EmitCheckDiagnosticCode, diags[0].Message)
	}
	// Import and export declarations are only valid at module top level;
	// the parser does not report that early error.
	topLevel := map[jsast.JsNode]bool{}
	for _, stmt := range prog.Body {
		topLevel[stmt] = true
	}
	var nested string
	jsast.WalkNode(prog, func(n jsast.JsNode) bool {
		switch n.(type) {
		case *jsast.JsImportDecl, *jsast.JsExportDecl:
			if !topLevel[n] && nested == "" {
				nested = "an import or export declaration is not at module top level"
			}
		}
		return nested == ""
	})
	if nested != "" {
		return fmt.Errorf("%s: generated JavaScript does not parse (%s); this is a compiler bug", EmitCheckDiagnosticCode, nested)
	}
	return nil
}

// EmitCheckDiagnosticCode reports generated JavaScript that does not parse.
const EmitCheckDiagnosticCode = "VELA-INTERNAL-EMIT"

func programOptions(opts CompileOptions) codegen.ProgramOptions {
	return codegen.ProgramOptions{CSSExternal: opts.CSSExternal, SourceComponentImports: opts.SourceComponentImports}
}

// runtimeSpecifier is the core runtime import of a module written to dir:
// the runtime package with --runtime-import=bare, else the relative path to
// the runtime staged at outRoot.
func runtimeSpecifier(opts CompileOptions, dir, outRoot string) string {
	if opts.BareRuntimeImports {
		return runtime.PackageName
	}
	return codegen.RuntimeImportFor(dir, outRoot)
}

// copyProjectJSModules mirrors local JavaScript modules into project output,
// with `.vela`, `vela` and `vela/store` specifiers rewritten
// (rewriteJSModuleSpecifiers). It deliberately uses the same directory exclusions as collectVela so build
// output and dependencies cannot be copied back into the project.
func copyProjectJSModules(srcRoot, outRoot string, opts CompileOptions) int {
	errors := 0
	for _, src := range project.CollectJSModules(srcRoot) {
		rel, err := filepath.Rel(srcRoot, src)
		if err != nil {
			errors++
			continue
		}
		dest := filepath.Join(outRoot, rel)
		data, err := os.ReadFile(src)
		if err == nil {
			err = os.MkdirAll(filepath.Dir(dest), 0755)
		}
		if err == nil {
			err = os.WriteFile(dest, []byte(codegen.RewriteJSModuleSpecifiersWith(string(data), runtimeSpecifier(opts, filepath.Dir(dest), outRoot), opts.SourceComponentImports)), 0644)
			opts.PackageImports.add(dest, sourcePackageImports(string(data)))
			if opts.CheckDeps {
				checkModulePackageImports(src, string(data), opts)
			}
		}
		if err != nil {
			fmt.Fprintf(ErrorWriter(opts), "  ✗ %s: copy local module: %v\n", rel, err)
			opts.Report.add(buildIssue(src, "copy local module: %v", err))
			errors++
		}
	}
	return errors
}

// hydrateRuntimeSource returns runtime-hydrate.js with the default
// development verification policy (V-11.3c).
func hydrateRuntimeSource(verify string) []byte {
	if verify == "" || verify == "repair" {
		return runtime.Hydrate
	}
	return []byte(strings.Replace(string(runtime.Hydrate), "const VERIFY = 'repair';", "const VERIFY = '"+verify+"';", 1))
}
