package main

import (
	"github.com/elauven/vela/runtime"

	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// checkCounts counts diagnostics by severity.
type checkCounts struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Hints    int `json:"hints"`
}

func countDiagnostics(diags []*Diagnostic) checkCounts {
	var c checkCounts
	for _, d := range diags {
		switch d.Sev {
		case SevError:
			c.Errors++
		case SevWarning:
			c.Warnings++
		default:
			c.Hints++
		}
	}
	return c
}

func writeCheckText(w io.Writer, diags []*Diagnostic, components int) {
	for _, d := range diags {
		fmt.Fprintf(w, "%s\n\n", d)
	}
	c := countDiagnostics(diags)
	fmt.Fprintf(w, "vela check: %d error(s), %d warning(s), %d hint(s) in %d component(s)\n", c.Errors, c.Warnings, c.Hints, components)
}

// checkJSON is the --format=json document.
type checkJSON struct {
	Version     int                 `json:"version"`
	Components  int                 `json:"components"`
	Diagnostics []MachineDiagnostic `json:"diagnostics"`
	checkCounts
}

func writeCheckJSON(w io.Writer, diags []*Diagnostic, components int) error {
	doc := checkJSON{Version: 1, Components: components, Diagnostics: []MachineDiagnostic{}, checkCounts: countDiagnostics(diags)}
	for _, d := range diags {
		doc.Diagnostics = append(doc.Diagnostics, d.Machine())
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
	return err
}

// sarifUncategorized is the rule of diagnostics without a code.
const sarifUncategorized = "VELA-UNCATEGORIZED"

// sarifURI is the artifact URI of a diagnostic file: a relative reference
// against the checkout root, or a file URI for an absolute path.
func sarifURI(file string) string {
	slashed := filepath.ToSlash(file)
	segments := strings.Split(slashed, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	escaped := strings.Join(segments, "/")
	if filepath.IsAbs(file) {
		if !strings.HasPrefix(escaped, "/") {
			escaped = "/" + escaped
		}
		return "file://" + escaped
	}
	return escaped
}

// sarifLog is the --format=sarif document (SARIF 2.1.0), one rule per
// diagnostic code.
func sarifLog(diags []*Diagnostic) map[string]any {
	var rules []map[string]any
	ruleIndex := map[string]int{}
	results := []map[string]any{}
	for _, d := range diags {
		id := d.Code
		if id == "" {
			id = sarifUncategorized
		}
		index, ok := ruleIndex[id]
		if !ok {
			index = len(rules)
			ruleIndex[id] = index
			description := "Vela compiler diagnostic " + id
			if id == sarifUncategorized {
				description = "Vela compiler diagnostic without a code"
			}
			rules = append(rules, map[string]any{
				"id":               id,
				"shortDescription": map[string]any{"text": description},
			})
		}
		level := map[DiagSeverity]string{SevError: "error", SevWarning: "warning", SevHint: "note"}[d.Sev]
		text := d.Message
		if d.Suggestion != "" {
			text += "\nhelp: " + d.Suggestion
		}
		result := map[string]any{
			"ruleId":    id,
			"ruleIndex": index,
			"level":     level,
			"message":   map[string]any{"text": text},
		}
		if d.File != "" {
			location := map[string]any{"artifactLocation": map[string]any{"uri": sarifURI(d.File)}}
			if d.Line > 0 {
				location["region"] = map[string]any{"startLine": d.Line, "startColumn": max(d.Col, 1)}
			}
			result["locations"] = []map[string]any{{"physicalLocation": location}}
		}
		results = append(results, result)
	}
	driver := map[string]any{
		"name":           "vela",
		"version":        runtime.Version,
		"informationUri": "https://github.com/elauven/vela",
	}
	if len(rules) > 0 {
		driver["rules"] = rules
	}
	return map[string]any{
		"$schema": "https://json.schemastore.org/sarif-2.1.0.json",
		"version": "2.1.0",
		"runs": []map[string]any{{
			"tool":       map[string]any{"driver": driver},
			"columnKind": "utf16CodeUnits",
			"results":    results,
		}},
	}
}

func writeCheckSARIF(w io.Writer, diags []*Diagnostic) error {
	data, err := json.MarshalIndent(sarifLog(diags), "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
	return err
}

// watchSnapshot is the modification time of every file under the roots,
// without dependency and output directories.
func watchSnapshot(roots []string) map[string]time.Time {
	files := map[string]time.Time{}
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				name := d.Name()
				if path != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "dist") {
					return filepath.SkipDir
				}
				return nil
			}
			if info, err := d.Info(); err == nil {
				files[path] = info.ModTime()
			}
			return nil
		})
	}
	return files
}

func sameSnapshot(a, b map[string]time.Time) bool {
	if len(a) != len(b) {
		return false
	}
	for path, t := range a {
		if u, ok := b[path]; !ok || !u.Equal(t) {
			return false
		}
	}
	return true
}

// runCheckCommand is `vela check`.
func runCheckCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vela check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectDir := fs.String("project", "", "source directory — check all .vela files")
	ssr := fs.Bool("ssr", false, "also run the server-rendering checks (requires --project)")
	ssrModule := fs.String("ssr-module", SSRDefaultModule, "Go module import root for generated SSR packages")
	format := fs.String("format", "text", "output: 'text', 'json' or 'sarif'")
	failOn := fs.String("fail-on", "error", "exit with 1 on diagnostics of this severity or above: 'error' or 'warning'")
	watch := fs.Bool("watch", false, "check again whenever a file changes")
	configPath := fs.String("config", "", "configuration file (default: "+ConfigFileName+" in the working directory, if present)")
	file := AddFileFlags(fs, BuildFlagDefaults)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	usage := func(format string, args ...any) int {
		fmt.Fprintf(stderr, "vela check: "+format+"\n", args...)
		return 2
	}
	config, err := LoadConfig(*configPath)
	if err == nil && config != nil {
		err = ApplyFlagValues(fs, ConfigFileName, config.FlagValues())
	}
	if err != nil {
		return usage("%v", err)
	}
	switch *format {
	case "text", "json", "sarif":
	default:
		return usage("invalid --format value %q (want 'text', 'json' or 'sarif')", *format)
	}
	switch *failOn {
	case "error", "warning":
	default:
		return usage("invalid --fail-on value %q (want 'error' or 'warning')", *failOn)
	}
	opts, err := file.Options()
	if err != nil {
		return usage("%v", err)
	}
	opts.SSRModule = *ssrModule
	target := CheckTarget{SrcRoot: *projectDir, Files: fs.Args(), SSR: *ssr, Opts: opts}
	switch {
	case target.SrcRoot != "" && len(target.Files) > 0:
		return usage("give --project or files, not both")
	case target.SrcRoot == "" && len(target.Files) == 0:
		return usage("usage: vela check --project <dir> [flags] | vela check [flags] <file.vela>...")
	case target.SSR && target.SrcRoot == "":
		return usage("--ssr requires --project")
	}
	if target.SrcRoot != "" {
		target.SrcRoot = filepath.Clean(target.SrcRoot)
		if info, err := os.Stat(target.SrcRoot); err != nil || !info.IsDir() {
			return usage("project directory %s not found", target.SrcRoot)
		}
	}

	check := func() int {
		diags, components, err := target.Run()
		if err != nil {
			fmt.Fprintf(stderr, "vela check: %v\n", err)
			return 2
		}
		switch *format {
		case "json":
			err = writeCheckJSON(stdout, diags, components)
		case "sarif":
			err = writeCheckSARIF(stdout, diags)
		default:
			writeCheckText(stdout, diags, components)
		}
		if err != nil {
			fmt.Fprintf(stderr, "vela check: %v\n", err)
			return 2
		}
		c := countDiagnostics(diags)
		if c.Errors > 0 || (*failOn == "warning" && c.Warnings > 0) {
			return 1
		}
		return 0
	}
	code := check()
	if !*watch {
		return code
	}
	roots := target.Files
	if target.SrcRoot != "" {
		roots = []string{target.SrcRoot}
		if target.SSR {
			if project, err := LoadSSRProject(target.SrcRoot); err == nil && project.HostDir != "" {
				roots = append(roots, project.HostDir)
			}
		}
	}
	if config != nil {
		roots = append(roots, ConfigFileName)
	}
	last := watchSnapshot(roots)
	for {
		time.Sleep(300 * time.Millisecond)
		current := watchSnapshot(roots)
		if sameSnapshot(last, current) {
			continue
		}
		last = current
		if *format == "text" {
			fmt.Fprintln(stdout, "\nvela check: files changed, checking again")
		}
		check()
	}
}
