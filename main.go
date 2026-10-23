package main

import (
	"github.com/elauven/vela/runtime"

	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

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
	file := AddFileFlags(flag.CommandLine, BuildFlagDefaults)
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
	config, err := LoadConfig(*configPath)
	if err == nil && config != nil {
		err = ApplyFlagValues(flag.CommandLine, ConfigFileName, config.FlagValues())
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
	opts, err := file.Options()
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
			runSSRProject(*projectDir, *outDir, opts, *watch, *parallel)
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
		if err := StageRuntime(*outDir, opts); err != nil {
			fmt.Fprintf(os.Stderr, "cannot write the runtime: %v\n", err)
			os.Exit(1)
		}
	}

	exitCode := 0
	compile := func() {
		for _, src := range args {
			dest := filepath.Join(*outDir, ComponentName(src)+".js")
			if !CompilePrint(src, dest, opts) {
				exitCode = 1
			}
		}
	}

	fmt.Fprintln(CompilerStatusWriter(opts), "Vela Compiler")
	fmt.Fprintln(CompilerStatusWriter(opts))
	compile()

	if exitCode != 0 {
		os.Exit(exitCode)
	}

	if *watch {
		runWatchFiles(args, *outDir, opts)
	}
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

// ─────────────────────────────────────────────────────────────────────────────
//  Project mode
// ─────────────────────────────────────────────────────────────────────────────

func runSSRProject(srcRoot, outRoot string, opts CompileOptions, watch, parallel bool) {
	srcRoot = filepath.Clean(srcRoot)
	absRoot, err := filepath.Abs(srcRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot resolve project path: %v\n", err)
		os.Exit(1)
	}
	status := CompilerStatusWriter(opts)
	fmt.Fprintln(status, "Vela Compiler")
	fmt.Fprintf(status, "   project   %s\n", absRoot)
	fmt.Fprintf(status, "   out       %s (SSR layout)\n", filepath.Clean(outRoot))
	if parallel {
		fmt.Fprintln(status, "   parallel  on")
	}
	fmt.Fprintln(status)
	if watch {
		watchSSRProject(srcRoot, outRoot, opts, parallel, 150*time.Millisecond, nil)
		return
	}
	start := time.Now()
	files := CollectVela(srcRoot)
	if len(files) == 0 {
		fmt.Fprintln(status, "  no .vela files found")
		return
	}
	errors := BuildSSRProject(files, srcRoot, outRoot, opts, parallel)
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

	status := CompilerStatusWriter(opts)
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

	errors := BuildProject(files, srcRoot, outRoot, opts, parallel)

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

// ─────────────────────────────────────────────────────────────────────────────
//  Watch modes
// ─────────────────────────────────────────────────────────────────────────────

func runWatchFiles(files []string, outDir string, opts CompileOptions) {
	status := CompilerStatusWriter(opts)
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
				CompilePrint(src, dest, opts)
			}
		}
	}
}

func runWatchProject(srcRoot, outRoot string, opts CompileOptions) {
	status := CompilerStatusWriter(opts)
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
				CompileProjectFile(src, srcRoot, outRoot, opts)
			}
		}
	}
}
