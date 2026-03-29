package main

import (
	"crypto/md5"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CompileOptions controls code generation behaviour.
type CompileOptions struct {
	Minify bool
}

func main() {
	outDir  := flag.String("out",    "dist",  "output directory")
	watch   := flag.Bool("watch",   false,   "watch files and recompile on change")
	minify  := flag.Bool("minify",  false,   "minify output JS (removes comments, collapses whitespace)")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Vela Compiler — from the backend to the frontend")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Usage:")
		fmt.Fprintln(os.Stderr, "  vela [flags] <file.vela>...")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		flag.PrintDefaults()
		os.Exit(1)
	}

	if err := os.MkdirAll(*outDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "cannot create output dir: %v\n", err)
		os.Exit(1)
	}

	opts := CompileOptions{Minify: *minify}
	exitCode := 0

	compile := func() {
		for _, src := range args {
			diags, err := compileFile(src, *outDir, opts)
			name := componentName(src)
			out  := filepath.Join(*outDir, name+".js")

			// Print diagnostics
			for _, d := range diags {
				if d.Sev == SevError {
					fmt.Fprintf(os.Stderr, "%s\n", d)
				} else {
					fmt.Printf("%s\n", d)
				}
			}

			if err != nil {
				fmt.Fprintf(os.Stderr, "  ✗  %-30s  %v\n", src, err)
				exitCode = 1
			} else {
				warnCount := 0
				for _, d := range diags {
					if d.Sev == SevWarning {
						warnCount++
					}
				}
				suffix := ""
				if warnCount > 0 {
					suffix = fmt.Sprintf("  (%d warning(s))", warnCount)
				}
				if opts.Minify {
					suffix += " [minified]"
				}
				fmt.Printf("  ✓  %-30s → %s%s\n", src, out, suffix)
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
		fmt.Println("\nWatching for changes... (Ctrl-C to stop)")
		mtimes := map[string]time.Time{}

		// Seed mtimes
		for _, src := range args {
			if info, err := os.Stat(src); err == nil {
				mtimes[src] = info.ModTime()
			}
		}

		for {
			time.Sleep(200 * time.Millisecond)
			for _, src := range args {
				info, err := os.Stat(src)
				if err != nil {
					continue
				}
				prev := mtimes[src]
				if info.ModTime().After(prev) {
					mtimes[src] = info.ModTime()
					fmt.Printf("\n  ↻  %s changed\n", src)
					diags, err := compileFile(src, *outDir, opts)
					for _, d := range diags {
						fmt.Printf("     %s\n", d)
					}
					if err != nil {
						fmt.Fprintf(os.Stderr, "  ✗  %v\n", err)
					} else {
						fmt.Printf("  ✓  done\n")
					}
				}
			}
		}
	}
}

// compileFile compiles one .vela source file, writing the output to outDir.
// It returns the list of diagnostics and any fatal error.
func compileFile(src, outDir string, opts CompileOptions) ([]*Diagnostic, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return nil, err
	}
	raw := string(data)

	name  := componentName(src)
	hash  := md5.Sum([]byte(src))
	scope := fmt.Sprintf("vela-%x", hash[:3])

	ds := NewDiagSet(src, raw)

	// ── Parse SFC ────────────────────────────────────────────────────────
	sfc, err := parseSFC(raw)
	if err != nil {
		ds.Error(0, "%v", err)
		return ds.All(), fmt.Errorf("parse: %w", err)
	}

	// ── Parse template ───────────────────────────────────────────────────
	tmpl, err := parseTemplate(sfc.Template)
	if err != nil {
		ds.Error(0, "template: %v", err)
		return ds.All(), fmt.Errorf("template: %w", err)
	}

	// ── Analyse script ───────────────────────────────────────────────────
	analysis := analyzeScript(sfc.Script)

	// ── Validate ─────────────────────────────────────────────────────────
	prog := ParseJS(sfc.Script)
	validateScript(prog, analysis, ds)
	validateTemplate(tmpl, analysis, ds)

	if ds.HasErrors() {
		return ds.All(), fmt.Errorf("compilation failed with %d error(s)", len(ds.Errors()))
	}

	// ── Generate code ─────────────────────────────────────────────────────
	code := generateCode(name, sfc, tmpl, analysis, scope)

	if opts.Minify {
		code = Minify(code)
	}

	out := filepath.Join(outDir, name+".js")
	if err := os.WriteFile(out, []byte(code), 0644); err != nil {
		return ds.All(), err
	}

	return ds.All(), nil
}

func componentName(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
