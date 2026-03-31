package main

import (
	"crypto/md5"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// CompileOptions controls code generation behaviour.
type CompileOptions struct {
	Minify  bool
	EmitAST bool // write <n>.ast.json alongside the compiled JS
}

func main() {
	projectDir := flag.String("project", "", "source directory — compile all .vela files, mirroring the folder structure into --out")
	outDir     := flag.String("out",     "dist", "output directory")
	watch      := flag.Bool("watch",    false,  "watch for changes and recompile")
	minify     := flag.Bool("minify",   false,  "minify output JS")
	emitAST    := flag.Bool("ast",      false,  "emit <n>.ast.json debug files")
	parallel   := flag.Bool("parallel", true,   "compile files in parallel (project mode)")
	flag.Parse()

	opts := CompileOptions{Minify: *minify, EmitAST: *emitAST}

	// ── Project mode ─────────────────────────────────────────────────────────
	if *projectDir != "" {
		runProject(*projectDir, *outDir, opts, *watch, *parallel)
		return
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

	fmt.Println("Vela Compiler")
	fmt.Println()
	compile()

	if exitCode != 0 {
		os.Exit(exitCode)
	}

	if *watch {
		runWatchFiles(args, *outDir, opts)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Project mode
// ─────────────────────────────────────────────────────────────────────────────

func runProject(srcRoot, outRoot string, opts CompileOptions, watch, parallel bool) {
	srcRoot = filepath.Clean(srcRoot)
	absRoot, err := filepath.Abs(srcRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot resolve project path: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Vela Compiler")
	fmt.Printf("   project   %s\n", absRoot)
	fmt.Printf("   out       %s\n", filepath.Clean(outRoot))
	if opts.Minify { fmt.Println("   minify    on") }
	if opts.EmitAST { fmt.Println("   ast       on") }
	if parallel { fmt.Println("   parallel  on") }
	fmt.Println()

	start := time.Now()
	files := collectVela(srcRoot)

	if len(files) == 0 {
		fmt.Println("  no .vela files found")
		return
	}

	errors := buildProject(files, srcRoot, outRoot, opts, parallel)

	elapsed := time.Since(start).Round(time.Millisecond)
	total := len(files)
	ok := total - errors
	fmt.Printf("\n  %d/%d compiled", ok, total)
	if errors > 0 {
		fmt.Printf("  (%d error(s))\n", errors)
		os.Exit(1)
	} else {
		fmt.Printf("  in %s ✓\n", elapsed)
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
				dest := mirrorPath(src, srcRoot, outRoot, ".js")
				results[i] = result{compilePrint(src, dest, opts)}
			}(i, src)
		}
		wg.Wait()
	} else {
		for i, src := range files {
			dest := mirrorPath(src, srcRoot, outRoot, ".js")
			results[i] = result{compilePrint(src, dest, opts)}
		}
	}

	errors := 0
	for _, r := range results {
		if !r.ok {
			errors++
		}
	}
	return errors
}

// ─────────────────────────────────────────────────────────────────────────────
//  Watch modes
// ─────────────────────────────────────────────────────────────────────────────

func runWatchFiles(files []string, outDir string, opts CompileOptions) {
	fmt.Println("\nWatching for changes… (Ctrl-C to stop)")
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
				fmt.Printf("\n  ↻  %s\n", src)
				dest := filepath.Join(outDir, componentName(src)+".js")
				compilePrint(src, dest, opts)
			}
		}
	}
}

func runWatchProject(srcRoot, outRoot string, opts CompileOptions) {
	fmt.Println("\nWatching for changes… (Ctrl-C to stop)")

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
				fmt.Printf("\n  +  %s (new)\n", relOrBase(f, srcRoot))
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
				fmt.Printf("\n  −  %s (removed)\n", relOrBase(f, srcRoot))
			}
		}

		if len(changed) > 0 {
			fmt.Printf("\n  ↻  %d file(s) changed\n", len(changed))
			for _, src := range changed {
				dest := mirrorPath(src, srcRoot, outRoot, ".js")
				compilePrint(src, dest, opts)
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
			fmt.Printf("       %s\n", d)
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

	fmt.Printf("  ✓  %-40s → %s%s\n", src, dest, suffix)
	return true
}

// compileFile compiles one .vela source file, writing JS to dest (full path).
// The output directory is created automatically.
func compileFile(src, dest string, opts CompileOptions) ([]*Diagnostic, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return nil, err
	}
	raw := string(data)

	name := componentName(src)
	hash := md5.Sum([]byte(src))
	scope := fmt.Sprintf("vela-%x", hash[:3])

	ds := NewDiagSet(src, raw)

	sfc, err := parseSFC(raw)
	if err != nil {
		ds.Error(0, "%v", err)
		return ds.All(), fmt.Errorf("parse: %w", err)
	}

	tmpl, err := parseTemplate(sfc.Template)
	if err != nil {
		ds.Error(0, "template: %v", err)
		return ds.All(), fmt.Errorf("template: %w", err)
	}

	analysis := analyzeScript(sfc.Script)

	if opts.EmitAST {
		if astJSON, astErr := DumpAST(src, raw); astErr == nil {
			astPath := strings.TrimSuffix(dest, ".js") + ".ast.json"
			_ = os.MkdirAll(filepath.Dir(astPath), 0755)
			_ = os.WriteFile(astPath, []byte(astJSON), 0644)
		}
	}

	prog := ParseJS(sfc.Script)
	validateScript(prog, analysis, ds)
	validateTemplate(tmpl, analysis, ds)

	if ds.HasErrors() {
		return ds.All(), fmt.Errorf("compilation failed with %d error(s)", len(ds.Errors()))
	}

	code := generateCode(name, sfc, tmpl, analysis, scope)
	if opts.Minify {
		code = Minify(code)
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return ds.All(), fmt.Errorf("mkdir %s: %w", filepath.Dir(dest), err)
	}
	if err := os.WriteFile(dest, []byte(code), 0644); err != nil {
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
			if strings.HasPrefix(name, ".") || skipDirs[name] {
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
