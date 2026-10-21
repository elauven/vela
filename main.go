package main

import (
	"github.com/elauven/vela/runtime"

	"crypto/md5"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// stageRuntime writes the compiler canonical runtime into outDir. Project
// builds write it once at the output root, and every generated module imports
// that file through a relative path (runtimeImportFor), so a page loads one
// runtime instance. Production builds get the production variant of the
// core runtime (R-12.2c).
func stageRuntime(outDir string, opts CompileOptions) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "runtime.js"), runtimeSource(opts.Production), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "runtime-blocks.js"), runtime.Blocks, 0o644); err != nil {
		return err
	}
	// hydrate() of the block engine; a page imports it only to hydrate server
	// HTML (C-4.1).
	return os.WriteFile(filepath.Join(outDir, "runtime-hydrate.js"), hydrateRuntimeSource(opts.HydrationVerify), 0o644)
}

// runtimeSource returns the core runtime for the build mode.
func runtimeSource(production bool) []byte {
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
	Hosts     *SSRHosts
	TraceSink TraceSink // optional source-free compiler trace observer
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
	SSRReport               string   // --ssr-report: write the region site records as JSON (task 043)
	SSRScaffoldHosts        bool     // --ssr-scaffold-hosts: create stubs for missing host implementations (task 030)
	HydrationVerify         string   // --hydration-verify: repair (default), fail, or off (V-11.3c)
	Production              bool     // --mode=production: production runtime variant
	// BareRuntimeImports imports the runtime npm package instead of copies
	// beside the output (--runtime-import=bare, R-12.2f).
	BareRuntimeImports bool
	// PackageImports collects the package imports of a project build for
	// vela.manifest.json; buildProject creates it when it is nil.
	PackageImports *packageImports
	// CheckDeps warns about package imports that do not resolve in
	// node_modules (--check-deps).
	CheckDeps bool
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

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "compile":
			os.Exit(runCompileCommand(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
		case "serve":
			os.Exit(runServeCommand(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
		case "check":
			os.Exit(runCheckCommand(os.Args[2:], os.Stdout, os.Stderr))
		}
	}

	projectDir := flag.String("project", "", "source directory — compile all .vela files, mirroring the folder structure into --out")
	outDir := flag.String("out", "dist", "output directory")
	watch := flag.Bool("watch", false, "watch for changes and recompile")
	emitAST := flag.Bool("ast", false, "emit <n>.ast.json input-AST debug files")
	genAST := flag.Bool("emit-gen-ast", false, "emit <n>.genast.json — the generated output AST, for inspection")
	parallel := flag.Bool("parallel", true, "compile files in parallel (project mode)")
	file := addFileFlags(flag.CommandLine, buildFlagDefaults)
	tracePath := flag.String("trace", "", "append source-free CompilationTraceV1 records as JSONL; use '-' for stdout")
	ssr := flag.Bool("ssr", false, "emit SSR project outputs into client/, server/, and styles/")
	ssrModule := flag.String("ssr-module", SSRDefaultModule, "Go module import root for generated SSR packages")
	ssrReport := flag.String("ssr-report", "", "with --ssr, write the per-site SSR report (outcome, tier, type) to this JSON file")
	ssrScaffold := flag.Bool("ssr-scaffold-hosts", false, "with --ssr, create Go stubs in the host tree for functions that need a host implementation; existing files are never changed")
	configPath := flag.String("config", "", "configuration file (default: "+ConfigFileName+" in the working directory, if present)")
	diagnostics := flag.String("diagnostics", "text", "diagnostic output: 'text' or 'json' (one JSON object per line on stderr)")
	showVersion := flag.Bool("version", false, "print the compiler version")
	flag.Parse()

	if *showVersion {
		fmt.Println("vela " + runtime.Version)
		return
	}
	config, err := loadConfig(*configPath)
	if err == nil && config != nil {
		err = applyFlagValues(flag.CommandLine, ConfigFileName, config.flagValues())
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "vela: %v\n", err)
		os.Exit(2)
	}
	switch *diagnostics {
	case "text", "json":
	default:
		fmt.Fprintf(os.Stderr, "vela: invalid --diagnostics value %q (want 'text' or 'json')\n", *diagnostics)
		os.Exit(2)
	}
	opts, err := file.options()
	if err != nil {
		fmt.Fprintf(os.Stderr, "vela: %v\n", err)
		os.Exit(2)
	}
	opts.EmitAST = *emitAST
	opts.GenAST = *genAST
	opts.SSRModule = *ssrModule
	opts.SSRReport = *ssrReport
	opts.SSRScaffoldHosts = *ssrScaffold
	opts.DiagnosticsJSON = *diagnostics == "json"
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

	// Generated modules import ./runtime.js; listed files get the runtime in
	// the output directory as project builds do.
	if !opts.BareRuntimeImports {
		if err := stageRuntime(*outDir, opts); err != nil {
			fmt.Fprintf(os.Stderr, "cannot write the runtime: %v\n", err)
			os.Exit(1)
		}
	}

	exitCode := 0
	compile := func() {
		for _, src := range args {
			dest := filepath.Join(*outDir, ComponentName(src)+".js")
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
	files := CollectVela(srcRoot)
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
	files := CollectVela(srcRoot)

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
	if err := stageRuntime(outRoot, opts); err != nil {
		reportBuildIssue(opts, "cannot stage runtime: %v", err)
		errors++
	}
	return errors
}

func compileProjectFile(src, srcRoot, outRoot string, opts CompileOptions) bool {
	dest := MirrorPath(src, srcRoot, outRoot, ".js")
	opts.RuntimeImport = runtimeSpecifier(opts, filepath.Dir(dest), outRoot)
	if rel, err := filepath.Rel(srcRoot, src); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		opts.TraceComponentID = filepath.ToSlash(rel)
	}
	if rel, err := filepath.Rel(filepath.Dir(filepath.Clean(srcRoot)), src); err == nil {
		opts.SitePath = filepath.ToSlash(rel)
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
				dest := filepath.Join(outDir, ComponentName(src)+".js")
				compilePrint(src, dest, opts)
			}
		}
	}
}

func runWatchProject(srcRoot, outRoot string, opts CompileOptions) {
	status := compilerStatusWriter(opts)
	fmt.Fprintln(status, "\nWatching for changes… (Ctrl-C to stop)")

	mtimes := map[string]time.Time{}
	for _, f := range CollectVela(srcRoot) {
		if info, err := os.Stat(f); err == nil {
			mtimes[f] = info.ModTime()
		}
	}

	for {
		time.Sleep(150 * time.Millisecond)

		current := CollectVela(srcRoot)
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
				fmt.Fprintf(status, "\n  +  %s (new)\n", RelOrBase(f, srcRoot))
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
				dest := MirrorPath(f, srcRoot, outRoot, ".js")
				_ = os.Remove(dest)
				if opts.EmitAST {
					_ = os.Remove(strings.TrimSuffix(dest, ".js") + ".ast.json")
				}
				fmt.Fprintf(status, "\n  −  %s (removed)\n", RelOrBase(f, srcRoot))
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

	opts.Report.add(diags...)
	for _, d := range diags {
		switch {
		case opts.DiagnosticsJSON:
			line, _ := json.Marshal(d.Machine())
			fmt.Fprintf(errorWriter(opts), "%s\n", line)
		case d.Sev == SevError:
			fmt.Fprintf(errorWriter(opts), "       %s\n", d)
		default:
			fmt.Fprintf(compilerStatusWriter(opts), "       %s\n", d)
		}
	}

	if err != nil {
		if !hasErrorDiagnostic(diags) {
			// A failure without diagnostics (an unreadable file) becomes one.
			d := buildIssue(src, "%v", err)
			opts.Report.add(d)
			if opts.DiagnosticsJSON {
				line, _ := json.Marshal(d.Machine())
				fmt.Fprintf(errorWriter(opts), "%s\n", line)
			}
		}
		if !opts.DiagnosticsJSON {
			fmt.Fprintf(errorWriter(opts), "  ✗  %-40s  %v\n", src, err)
		}
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

func hasErrorDiagnostic(diags []*Diagnostic) bool {
	for _, d := range diags {
		if d.Sev == SevError {
			return true
		}
	}
	return false
}

// compileFile compiles one .vela source file, writing JS to dest (full path).
// The output directory is created automatically.
func compileFile(src, dest string, opts CompileOptions) ([]*Diagnostic, error) {
	read := func() ([]byte, error) { return os.ReadFile(src) }
	return compileComponent(src, dest, opts, read, func(m *compiledModule) error { return writeCompiledModule(dest, m) })
}

// compiledModule is the output of one component before anything is written.
type compiledModule struct {
	JS      string // the module, without a sourceMappingURL comment
	Map     []byte // v3 source map with SourceMap
	CSS     string // scoped CSS with CSSExternal
	Imports []moduleImport
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
func compileComponent(src, dest string, opts CompileOptions, read func() ([]byte, error), emit func(*compiledModule) error) (diags []*Diagnostic, err error) {
	name := ComponentName(src)
	module := &compiledModule{}
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
		data, readErr = read()
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
		sfc, parseErr = ParseSFC(raw)
		return parseErr
	}); err != nil {
		ds.Error(0, "%v", err)
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
		analysis = AnalyzeScriptAST(sfc.ScriptAST)
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
			target = BuildProgramUnvalidated(name, sfc, tmpl, analysis, scope, opts.CSSExternal)
			trace.trace.Metrics.TargetNodes = countJSNodes(target)
			return nil
		}); err != nil {
			return ds.All(), err
		}
		if err = trace.stage("validate-target-ast", func() error {
			ValidateGeneratedProgram(target)
			return nil
		}); err != nil {
			return ds.All(), err
		}
	} else {
		target = BuildProgramCSS(name, sfc, tmpl, analysis, scope, opts.CSSExternal)
	}

	FinalizeRuntimeImports(target, opts.RuntimeImport)
	opts.PackageImports.add(dest, programPackageImports(target))

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
			code = HeaderComment + "\n\n" + body
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
			code = HeaderComment + "\n\n" + PrintJS(target, PrintPretty)
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
	if err = checkEmittedJS(code); err != nil {
		ds.Error(0, "%v", err)
		return ds.All(), err
	}

	if err = trace.stage("write-output", func() error {
		module.JS, module.Map = code, mapBytes
		if opts.CSSExternal {
			module.CSS = ExtractScopedCSS(sfc, scope)
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

// checkEmittedJS reparses a generated module. A printer or minifier defect
// would otherwise reach the browser as a module that fails to load, or as
// different code; it becomes an internal compiler error instead.
func checkEmittedJS(code string) error {
	prog, diags := ParseJSWithDiagnostics(code)
	if len(diags) == 0 {
		diags = EarlyErrors(prog, EarlyOptions{TopLevelAwait: true})
	}
	if len(diags) > 0 {
		return fmt.Errorf("%s: generated JavaScript does not parse (%s); this is a compiler bug", EmitCheckDiagnosticCode, diags[0].Message)
	}
	// Import and export declarations are only valid at module top level;
	// the parser does not report that early error.
	topLevel := map[JsNode]bool{}
	for _, stmt := range prog.Body {
		topLevel[stmt] = true
	}
	var nested string
	WalkNode(prog, func(n JsNode) bool {
		switch n.(type) {
		case *JsImportDecl, *JsExportDecl:
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

// ─────────────────────────────────────────────────────────────────────────────
//  Path helpers
// ─────────────────────────────────────────────────────────────────────────────

// runtimeSpecifier is the core runtime import of a module written to dir:
// the runtime package with --runtime-import=bare, else the relative path to
// the runtime staged at outRoot.
func runtimeSpecifier(opts CompileOptions, dir, outRoot string) string {
	if opts.BareRuntimeImports {
		return runtime.PackageName
	}
	return RuntimeImportFor(dir, outRoot)
}

// copyProjectJSModules mirrors local JavaScript modules into project output,
// with `.vela`, `vela` and `vela/store` specifiers rewritten
// (rewriteJSModuleSpecifiers). It deliberately uses the same directory exclusions as collectVela so build
// output and dependencies cannot be copied back into the project.
func copyProjectJSModules(srcRoot, outRoot string, opts CompileOptions) int {
	errors := 0
	for _, src := range CollectJSModules(srcRoot) {
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
			err = os.WriteFile(dest, []byte(RewriteJSModuleSpecifiers(string(data), runtimeSpecifier(opts, filepath.Dir(dest), outRoot))), 0644)
			opts.PackageImports.add(dest, sourcePackageImports(string(data)))
			if opts.CheckDeps {
				checkModulePackageImports(src, string(data), opts)
			}
		}
		if err != nil {
			fmt.Fprintf(errorWriter(opts), "  ✗ %s: copy local module: %v\n", rel, err)
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
