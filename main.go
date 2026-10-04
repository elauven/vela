package main

import (
	"crypto/md5"
	_ "embed"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed runtime/runtime.js
var embeddedRuntime []byte

//go:embed runtime/runtime-hydration.js
var embeddedHydrationRuntime []byte

//go:embed runtime/runtime-blocks.js
var embeddedBlocksRuntime []byte

// stageRuntime writes the compiler canonical runtime into outDir. Project
// builds write it once at the output root, and every generated module imports
// that file through a relative path (runtimeImportFor), so a page loads one
// runtime instance. The hydration runtime is written only for builds that
// emit hydration code, and production builds get the production variant of
// the core runtime (R-12.2c).
func stageRuntime(outDir string, opts CompileOptions) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "runtime.js"), runtimeSource(opts.Production), 0o644); err != nil {
		return err
	}
	if activeDOMEngine == DOMEngineBlocks {
		if err := os.WriteFile(filepath.Join(outDir, "runtime-blocks.js"), embeddedBlocksRuntime, 0o644); err != nil {
			return err
		}
	}
	if !opts.HydrationSupport {
		return nil
	}
	return os.WriteFile(filepath.Join(outDir, "runtime-hydration.js"), embeddedHydrationRuntime, 0o644)
}

// runtimeDevelopmentFlag is the core runtime's development switch. The
// production variant replaces it with a constant so warnings and other
// development-only paths are disabled.
const runtimeDevelopmentFlag = "const DEV = typeof process !== 'undefined'\n  ? process.env.NODE_ENV !== 'production'\n  : !globalThis.__VELA_PROD__;"

// runtimeSource returns the core runtime for the build mode.
func runtimeSource(production bool) []byte {
	if !production {
		return embeddedRuntime
	}
	return []byte(strings.Replace(string(embeddedRuntime), runtimeDevelopmentFlag, "const DEV = false;", 1))
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
	SourceMap   bool      // emit a v3 .js.map alongside generated JS
	TraceSink   TraceSink // optional source-free compiler trace observer
	// TraceComponentID is an internal source-free override used for project
	// traces. File mode derives a safe basename automatically.
	TraceComponentID string
	// StatusWriter receives human compiler progress. It defaults to stdout;
	// --trace=- redirects it to stderr so stdout remains valid JSONL.
	StatusWriter io.Writer

	// TargetProfile enables sound direct-global diagnostics for a known host.
	// The zero value is permissive for backwards compatibility.
	TargetProfile           TargetProfile
	UnavailableCapabilities []string // direct known globals disabled by the host
	Strict                  bool     // elevate proven unresolved reads to errors
	SSRModule               string   // Go module import root for generated SSR server packages
	Production              bool     // --mode=production: production runtime variant
	HydrationSupport        bool     // internal: emit SSR client state-restoration hooks
	// RuntimeImport is the module specifier generated code uses for the
	// runtime. Empty means "./runtime.js"; project builds set a path to the
	// single runtime at the output root.
	RuntimeImport string
}

func main() {
	projectDir := flag.String("project", "", "source directory — compile all .vela files, mirroring the folder structure into --out")
	outDir := flag.String("out", "dist", "output directory")
	watch := flag.Bool("watch", false, "watch for changes and recompile")
	minify := flag.Bool("minify", false, "minify output JS")
	emitAST := flag.Bool("ast", false, "emit <n>.ast.json input-AST debug files")
	genAST := flag.Bool("emit-gen-ast", false, "emit <n>.genast.json — the generated output AST, for inspection")
	parallel := flag.Bool("parallel", true, "compile files in parallel (project mode)")
	cssMode := flag.String("css", "inject", "CSS delivery: 'inject' (default, CSS-in-JS) or 'external' (sibling .css files)")
	sourceMap := flag.Bool("sourcemap", false, "emit an external v3 source map alongside AST-generated JS")
	target := flag.String("target", "permissive", "JavaScript host profile: permissive, browser, worker, node, or ssr")
	unavailableCapabilities := flag.String("unavailable-capabilities", "", "comma-separated direct host capabilities to reject, e.g. fetch,indexedDB")
	strict := flag.Bool("strict", false, "treat proven unresolved JavaScript reads as errors")
	tracePath := flag.String("trace", "", "append source-free CompilationTraceV1 records as JSONL; use '-' for stdout")
	ssr := flag.Bool("ssr", false, "emit SSR project outputs into client/, server/, and styles/")
	ssrModule := flag.String("ssr-module", "vela/generated/server", "Go module import root for generated SSR packages")
	mode := flag.String("mode", "development", "build mode: 'development' (default) or 'production'")
	domEngine := flag.String("dom-engine", "legacy", "DOM engine: 'legacy' (default) or 'blocks' (block runtime, task 039)")
	flag.Parse()

	targetProfile, err := ParseTargetProfile(*target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vela: %v\n", err)
		os.Exit(2)
	}

	if engine, err := parseDOMEngine(*domEngine); err != nil {
		fmt.Fprintf(os.Stderr, "vela: %v\n", err)
		os.Exit(2)
	} else if *domEngine != "legacy" || os.Getenv("VELA_DOM_ENGINE") == "" {
		activeDOMEngine = engine
	}

	switch *mode {
	case "development", "production":
		// ok
	default:
		fmt.Fprintf(os.Stderr, "vela: invalid --mode value %q (want 'development' or 'production')\n", *mode)
		os.Exit(2)
	}

	switch *cssMode {
	case "inject", "external":
		// ok
	default:
		fmt.Fprintf(os.Stderr,
			"vela: invalid --css value %q (want 'inject' or 'external')\n",
			*cssMode)
		os.Exit(2)
	}

	opts := CompileOptions{
		Minify:                  *minify,
		EmitAST:                 *emitAST,
		GenAST:                  *genAST,
		CSSExternal:             *cssMode == "external",
		SourceMap:               *sourceMap,
		TargetProfile:           targetProfile,
		UnavailableCapabilities: splitCapabilityList(*unavailableCapabilities),
		Strict:                  *strict,
		SSRModule:               *ssrModule,
		Production:              *mode == "production",
	}
	if closeTrace, traceErr := configureTrace(&opts, *tracePath); traceErr != nil {
		fmt.Fprintf(os.Stderr, "vela: %v\n", traceErr)
		os.Exit(2)
	} else if closeTrace != nil {
		defer func() { _ = closeTrace() }()
	}

	// ── Project mode ─────────────────────────────────────────────────────────
	if *projectDir != "" {
		if *ssr {
			if *watch {
				fmt.Fprintln(os.Stderr, "vela: --ssr does not support --watch yet")
				os.Exit(2)
			}
			runSSRProject(*projectDir, *outDir, opts, *parallel)
			return
		}
		runProject(*projectDir, *outDir, opts, *watch, *parallel)
		return
	}
	if *ssr {
		fmt.Fprintln(os.Stderr, "vela: --ssr requires --project")
		os.Exit(2)
	}

	// ── File mode (original behaviour, flat output) ───────────────────────────
	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Vela Compiler — from the backend to the frontend")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Usage:")
		fmt.Fprintln(os.Stderr, "  vela [flags] <file.vela>...        # compile listed files (flat output)")
		fmt.Fprintln(os.Stderr, "  vela --project <dir> [flags]       # compile entire project, mirror tree")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		flag.PrintDefaults()
		os.Exit(1)
	}

	if err := os.MkdirAll(*outDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "cannot create output dir: %v\n", err)
		os.Exit(1)
	}

	exitCode := 0
	compile := func() {
		for _, src := range args {
			dest := filepath.Join(*outDir, componentName(src)+".js")
			if !compilePrint(src, dest, opts) {
				exitCode = 1
			}
		}
	}

	fmt.Fprintln(compilerStatusWriter(opts), "Vela Compiler")
	fmt.Fprintln(compilerStatusWriter(opts))
	compile()

	if exitCode != 0 {
		os.Exit(exitCode)
	}

	if *watch {
		runWatchFiles(args, *outDir, opts)
	}
}

func compilerStatusWriter(opts CompileOptions) io.Writer {
	if opts.StatusWriter != nil {
		return opts.StatusWriter
	}
	return os.Stdout
}

// configureTrace applies CLI trace transport policy without changing normal
// compilation options. When JSONL uses stdout, all human progress is moved to
// stderr so every stdout line remains an independently parseable JSON record.
func configureTrace(opts *CompileOptions, tracePath string) (func() error, error) {
	if tracePath == "" {
		return nil, nil
	}
	sink, err := OpenJSONLTraceSink(tracePath)
	if err != nil {
		return nil, err
	}
	opts.TraceSink = sink
	if tracePath == "-" {
		opts.StatusWriter = os.Stderr
	}
	return sink.Close, nil
}

func splitCapabilityList(raw string) []string {
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

// ─────────────────────────────────────────────────────────────────────────────
//  Project mode
// ─────────────────────────────────────────────────────────────────────────────

func runSSRProject(srcRoot, outRoot string, opts CompileOptions, parallel bool) {
	srcRoot = filepath.Clean(srcRoot)
	absRoot, err := filepath.Abs(srcRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot resolve project path: %v\n", err)
		os.Exit(1)
	}
	status := compilerStatusWriter(opts)
	fmt.Fprintln(status, "Vela Compiler")
	fmt.Fprintf(status, "   project   %s\n", absRoot)
	fmt.Fprintf(status, "   out       %s (SSR layout)\n", filepath.Clean(outRoot))
	if parallel {
		fmt.Fprintln(status, "   parallel  on")
	}
	fmt.Fprintln(status)
	start := time.Now()
	files := collectVela(srcRoot)
	if len(files) == 0 {
		fmt.Fprintln(status, "  no .vela files found")
		return
	}
	errors := buildSSRProject(files, srcRoot, outRoot, opts, parallel)
	elapsed := time.Since(start).Round(time.Millisecond)
	ok := len(files) - errors
	if ok < 0 {
		ok = 0
	}
	fmt.Fprintf(status, "\n  %d/%d SSR components emitted", ok, len(files))
	if errors > 0 {
		fmt.Fprintf(status, "  (%d error(s))\n", errors)
		os.Exit(1)
	}
	fmt.Fprintf(status, "  in %s ✓\n", elapsed)
}

func runProject(srcRoot, outRoot string, opts CompileOptions, watch, parallel bool) {
	srcRoot = filepath.Clean(srcRoot)
	absRoot, err := filepath.Abs(srcRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot resolve project path: %v\n", err)
		os.Exit(1)
	}

	status := compilerStatusWriter(opts)
	fmt.Fprintln(status, "Vela Compiler")
	fmt.Fprintf(status, "   project   %s\n", absRoot)
	fmt.Fprintf(status, "   out       %s\n", filepath.Clean(outRoot))
	if opts.Minify {
		fmt.Fprintln(status, "   minify    on")
	}
	if opts.EmitAST {
		fmt.Fprintln(status, "   ast       on")
	}
	if parallel {
		fmt.Fprintln(status, "   parallel  on")
	}
	fmt.Fprintln(status)

	start := time.Now()
	files := collectVela(srcRoot)

	if len(files) == 0 {
		fmt.Fprintln(status, "  no .vela files found")
		return
	}

	errors := buildProject(files, srcRoot, outRoot, opts, parallel)

	elapsed := time.Since(start).Round(time.Millisecond)
	total := len(files)
	ok := total - errors
	fmt.Fprintf(status, "\n  %d/%d compiled", ok, total)
	if errors > 0 {
		fmt.Fprintf(status, "  (%d error(s))\n", errors)
		os.Exit(1)
	} else {
		fmt.Fprintf(status, "  in %s ✓\n", elapsed)
	}

	if watch {
		runWatchProject(srcRoot, outRoot, opts)
	}
}

// buildProject compiles all files, optionally in parallel.
// Returns the number of files that failed.
func buildProject(files []string, srcRoot, outRoot string, opts CompileOptions, parallel bool) int {
	type result struct{ ok bool }
	results := make([]result, len(files))

	if parallel {
		var wg sync.WaitGroup
		for i, src := range files {
			wg.Add(1)
			go func(i int, src string) {
				defer wg.Done()
				results[i] = result{compileProjectFile(src, srcRoot, outRoot, opts)}
			}(i, src)
		}
		wg.Wait()
	} else {
		for i, src := range files {
			results[i] = result{compileProjectFile(src, srcRoot, outRoot, opts)}
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
	errors += copyProjectJSModules(srcRoot, outRoot)
	if err := stageRuntime(outRoot, opts); err != nil {
		fmt.Fprintf(os.Stderr, "cannot stage runtime: %v\n", err)
		errors++
	}
	return errors
}

func compileProjectFile(src, srcRoot, outRoot string, opts CompileOptions) bool {
	dest := mirrorPath(src, srcRoot, outRoot, ".js")
	opts.RuntimeImport = runtimeImportFor(filepath.Dir(dest), outRoot)
	if rel, err := filepath.Rel(srcRoot, src); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		opts.TraceComponentID = filepath.ToSlash(rel)
	}
	return compilePrint(src, dest, opts)
}

// ─────────────────────────────────────────────────────────────────────────────
//  Watch modes
// ─────────────────────────────────────────────────────────────────────────────

func runWatchFiles(files []string, outDir string, opts CompileOptions) {
	status := compilerStatusWriter(opts)
	fmt.Fprintln(status, "\nWatching for changes… (Ctrl-C to stop)")
	mtimes := map[string]time.Time{}
	for _, f := range files {
		if info, err := os.Stat(f); err == nil {
			mtimes[f] = info.ModTime()
		}
	}
	for {
		time.Sleep(150 * time.Millisecond)
		for _, src := range files {
			info, err := os.Stat(src)
			if err != nil {
				continue
			}
			if info.ModTime().After(mtimes[src]) {
				mtimes[src] = info.ModTime()
				fmt.Fprintf(status, "\n  ↻  %s\n", src)
				dest := filepath.Join(outDir, componentName(src)+".js")
				compilePrint(src, dest, opts)
			}
		}
	}
}

func runWatchProject(srcRoot, outRoot string, opts CompileOptions) {
	status := compilerStatusWriter(opts)
	fmt.Fprintln(status, "\nWatching for changes… (Ctrl-C to stop)")

	mtimes := map[string]time.Time{}
	for _, f := range collectVela(srcRoot) {
		if info, err := os.Stat(f); err == nil {
			mtimes[f] = info.ModTime()
		}
	}

	for {
		time.Sleep(150 * time.Millisecond)

		current := collectVela(srcRoot)
		currentSet := map[string]bool{}
		for _, f := range current {
			currentSet[f] = true
		}

		// Modifications and new files
		var changed []string
		for _, f := range current {
			info, err := os.Stat(f)
			if err != nil {
				continue
			}
			prev, known := mtimes[f]
			if !known {
				fmt.Fprintf(status, "\n  +  %s (new)\n", relOrBase(f, srcRoot))
			}
			if !known || info.ModTime().After(prev) {
				mtimes[f] = info.ModTime()
				changed = append(changed, f)
			}
		}

		// Deletions
		for f := range mtimes {
			if !currentSet[f] {
				delete(mtimes, f)
				dest := mirrorPath(f, srcRoot, outRoot, ".js")
				_ = os.Remove(dest)
				if opts.EmitAST {
					_ = os.Remove(strings.TrimSuffix(dest, ".js") + ".ast.json")
				}
				fmt.Fprintf(status, "\n  −  %s (removed)\n", relOrBase(f, srcRoot))
			}
		}

		if len(changed) > 0 {
			fmt.Fprintf(status, "\n  ↻  %d file(s) changed\n", len(changed))
			for _, src := range changed {
				compileProjectFile(src, srcRoot, outRoot, opts)
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Core compile
// ─────────────────────────────────────────────────────────────────────────────

// compilePrint compiles src → dest, prints a one-line status, returns success.
func compilePrint(src, dest string, opts CompileOptions) bool {
	diags, err := compileFile(src, dest, opts)

	for _, d := range diags {
		if d.Sev == SevError {
			fmt.Fprintf(os.Stderr, "       %s\n", d)
		} else {
			fmt.Fprintf(compilerStatusWriter(opts), "       %s\n", d)
		}
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "  ✗  %-40s  %v\n", src, err)
		return false
	}

	suffix := ""
	warns := 0
	for _, d := range diags {
		if d.Sev == SevWarning {
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

	fmt.Fprintf(compilerStatusWriter(opts), "  ✓  %-40s → %s%s\n", src, dest, suffix)
	return true
}

// compileFile compiles one .vela source file, writing JS to dest (full path).
// The output directory is created automatically.
func compileFile(src, dest string, opts CompileOptions) (diags []*Diagnostic, err error) {
	name := componentName(src)
	trace := newTraceRecorder(src, name, opts)
	var ds *DiagSet
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
		data, readErr = os.ReadFile(src)
		return readErr
	}); err != nil {
		return nil, err
	}
	raw := string(data)
	if trace != nil {
		trace.trace.Metrics.SourceBytes = len(data)
	}

	hash := md5.Sum([]byte(src))
	scope := fmt.Sprintf("vela-%x", hash[:3])

	ds = NewDiagSet(src, raw)
	if opts.SourceMap && opts.Minify {
		ds.Error(0, "--sourcemap cannot be combined with --minify; Vela minifier is not source-map-aware")
		return ds.All(), fmt.Errorf("invalid source map options")
	}

	var sfc *SFC
	if err = trace.stage("parse-sfc", func() error {
		var parseErr error
		sfc, parseErr = parseSFC(raw)
		return parseErr
	}); err != nil {
		ds.Error(0, "%v", err)
		return ds.All(), fmt.Errorf("parse: %w", err)
	}

	var tmpl []Node
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
		if issues := ValidateTemplateAST(sfc.TemplateAST); len(issues) > 0 {
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

	var analysis *Analysis
	var scriptProg *JsProgram
	if err = trace.stage("parse-script-and-analyze", func() error {
		if len(sfc.ScriptAST.Diagnostics) > 0 {
			ds.PushOffset(sfc.ScriptAST.Span.Start)
			for _, issue := range sfc.ScriptAST.Diagnostics {
				ds.ErrorWith(issue.Pos, "VELA-JS-SYNTAX", "fix the JavaScript syntax in <script>", "%s", issue.Message)
			}
			ds.PopOffset()
			return fmt.Errorf("script syntax validation failed")
		}
		analysis = analyzeScriptAST(sfc.ScriptAST)
		scriptProg = sfc.ScriptAST.Program
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
			for _, issue := range ValidateCssAST(sfc.StyleAST) {
				ds.ErrorWith(issue.Pos, "VELA-CSS-AST", "fix the stylesheet structure", "%s", issue.Message)
			}
			ds.PopOffset()
		}
		// Scope + flow analysis over the original parsed program. All these
		// passes report positions relative to the script body, so the DiagSet
		// offset translates them into original component coordinates.
		ds.PushOffset(sfc.ScriptOffset)
		scriptScope := BuildScopesWithOptions(scriptProg, ds, ScopeOptions{
			TargetProfile:           opts.TargetProfile,
			UnavailableCapabilities: opts.UnavailableCapabilities,
			Strict:                  opts.Strict,
		})
		DetectUnreachable(scriptProg, ds)
		validateScript(scriptProg, analysis, ds)
		validateStateWrites(scriptProg, scriptScope, tmpl, analysis, ds)
		validateDerivedCycles(scriptProg, analysis, ds)
		validateDerivedPurity(scriptProg, analysis, ds)
		validateScriptFormsWithAnalysis(scriptProg, analysis, ds)
		validateEffectBody(scriptProg, analysis, ds)
		ds.PopOffset()

		ds.PushOffset(sfc.TemplateOffset)
		validateTemplate(tmpl, analysis, ds)
		validateTemplateForms(tmpl, sfc.Template, ds)
		validateTemplateRefs(tmpl, analysis, scriptScope, sfc.Template, ds)
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

	var target *JsProgram
	if trace != nil {
		if err = trace.stage("build-target-ast", func() error {
			target = buildProgramUnvalidatedWithHydration(name, sfc, tmpl, analysis, scope, opts.CSSExternal, opts.HydrationSupport)
			trace.trace.Metrics.TargetNodes = countJSNodes(target)
			return nil
		}); err != nil {
			return ds.All(), err
		}
		if err = trace.stage("validate-target-ast", func() error {
			validateGeneratedProgram(target)
			return nil
		}); err != nil {
			return ds.All(), err
		}
	} else {
		target = buildProgramCSSWithHydration(name, sfc, tmpl, analysis, scope, opts.CSSExternal, opts.HydrationSupport)
	}

	finalizeRuntimeImports(target, opts.RuntimeImport)

	if opts.GenAST {
		genPath := strings.TrimSuffix(dest, ".js") + ".genast.json"
		_ = os.MkdirAll(filepath.Dir(genPath), 0755)
		_ = os.WriteFile(genPath, []byte(ASTToJSON(target, false)), 0644)
	}

	var code string
	var mapBytes []byte
	if opts.SourceMap {
		var locations []PrintLocation
		if err = trace.stage("print-target", func() error {
			ShiftTree(target, sfc.ScriptOffset)
			body := PrintJSWithLocations(target, PrintPretty, func(loc PrintLocation) {
				loc.GeneratedLine += 2
				locations = append(locations, loc)
			})
			code = headerComment + "\n\n" + body
			return nil
		}); err != nil {
			return ds.All(), err
		}
		if err = trace.stage("build-source-map", func() error {
			m := BuildSourceMapV3(filepath.Base(dest), filepath.ToSlash(src), raw, locations)
			var mapErr error
			mapBytes, mapErr = SourceMapJSON(m)
			return mapErr
		}); err != nil {
			return ds.All(), err
		}
	} else {
		if err = trace.stage("print-target", func() error {
			code = headerComment + "\n\n" + PrintJS(target, PrintPretty)
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

	if err = trace.stage("write-output", func() error {
		if len(mapBytes) > 0 {
			if writeErr := os.MkdirAll(filepath.Dir(dest), 0755); writeErr != nil {
				return fmt.Errorf("mkdir %s: %w", filepath.Dir(dest), writeErr)
			}
			mapPath := dest + ".map"
			if writeErr := os.WriteFile(mapPath, mapBytes, 0644); writeErr != nil {
				return writeErr
			}
			code += "\n//# sourceMappingURL=" + filepath.Base(mapPath) + "\n"
		}
		if writeErr := os.MkdirAll(filepath.Dir(dest), 0755); writeErr != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(dest), writeErr)
		}
		if writeErr := os.WriteFile(dest, []byte(code), 0644); writeErr != nil {
			return writeErr
		}
		if trace != nil {
			trace.trace.Metrics.OutputBytes = len(code)
		}
		if opts.CSSExternal {
			if css := ExtractScopedCSS(sfc, scope); css != "" {
				cssPath := strings.TrimSuffix(dest, ".js") + ".css"
				if writeErr := os.WriteFile(cssPath, []byte(css), 0644); writeErr != nil {
					return fmt.Errorf("write %s: %w", cssPath, writeErr)
				}
				if trace != nil {
					trace.trace.Metrics.CSSBytes = len(css)
				}
			}
		}
		return nil
	}); err != nil {
		return ds.All(), err
	}

	return ds.All(), nil
}

// ─────────────────────────────────────────────────────────────────────────────
//  Path helpers
// ─────────────────────────────────────────────────────────────────────────────

// mirrorPath maps  <srcRoot>/a/b/C.vela  →  <outRoot>/a/b/C<ext>
//
// Example:
//
//	src/dashboard/components/Card.vela  →  dist/dashboard/components/Card.js
func mirrorPath(src, srcRoot, outRoot, ext string) string {
	rel, err := filepath.Rel(srcRoot, src)
	if err != nil {
		rel = filepath.Base(src)
	}
	noExt := strings.TrimSuffix(rel, filepath.Ext(rel))
	return filepath.Join(outRoot, noExt+ext)
}

// collectVela recursively finds all .vela files under root,
// skipping hidden directories, node_modules, and common output dirs.
func collectVela(root string) []string {
	skipDirs := map[string]bool{
		"node_modules": true,
		"dist":         true,
		".git":         true,
		".svn":         true,
	}
	var files []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			// The project root itself is never skipped, so `--project .` works.
			if path != root && (strings.HasPrefix(name, ".") || skipDirs[name]) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".vela") {
			files = append(files, path)
		}
		return nil
	})
	return files
}

// copyProjectJSModules mirrors local JavaScript modules into project output.
// It deliberately uses the same directory exclusions as collectVela so build
// output and dependencies cannot be copied back into the project.
func copyProjectJSModules(srcRoot, outRoot string) int {
	errors := 0
	for _, src := range collectProjectJSModules(srcRoot) {
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
			err = os.WriteFile(dest, data, 0644)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ %s: copy local module: %v\n", rel, err)
			errors++
		}
	}
	return errors
}

func collectProjectJSModules(root string) []string {
	skipDirs := map[string]bool{
		"node_modules": true,
		"dist":         true,
		".git":         true,
		".svn":         true,
	}
	var files []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			// The project root itself is never skipped, so `--project .` works.
			if path != root && (strings.HasPrefix(name, ".") || skipDirs[name]) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".js") {
			files = append(files, path)
		}
		return nil
	})
	return files
}

// relOrBase returns path relative to root, or just the base name on error.
func relOrBase(f, root string) string {
	if rel, err := filepath.Rel(root, f); err == nil {
		return rel
	}
	return filepath.Base(f)
}

func componentName(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
