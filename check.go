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
	"sort"
	"strings"
	"sync"
	"time"
)

// diagReport collects the diagnostics of a build for vela check. Components
// compile in parallel, so it is safe for concurrent use; a nil report
// ignores adds.
type diagReport struct {
	mu    sync.Mutex
	diags []*Diagnostic
}

func (r *diagReport) add(diags ...*Diagnostic) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, d := range diags {
		if d != nil {
			r.diags = append(r.diags, d)
		}
	}
}

// sorted returns the diagnostics by file and position; diagnostics of one
// position keep the order they were reported in.
func (r *diagReport) sorted() []*Diagnostic {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := append([]*Diagnostic(nil), r.diags...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Col < b.Col
	})
	return out
}

func errorWriter(opts CompileOptions) io.Writer {
	if opts.ErrWriter != nil {
		return opts.ErrWriter
	}
	return os.Stderr
}

// buildIssue is a build error of file without a source position.
func buildIssue(file, format string, args ...any) *Diagnostic {
	return &Diagnostic{Sev: SevError, File: filepath.ToSlash(file), Line: 1, Col: 1, Message: fmt.Sprintf(format, args...)}
}

// reportBuildIssue prints and records a build error that belongs to no
// component.
func reportBuildIssue(opts CompileOptions, format string, args ...any) {
	fmt.Fprintf(errorWriter(opts), format+"\n", args...)
	opts.Report.add(&Diagnostic{Sev: SevError, Message: fmt.Sprintf(format, args...)})
}

// cwdRelative is path relative to the working directory when it is inside
// it, as the paths of component diagnostics are.
func cwdRelative(path string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return filepath.ToSlash(path)
	}
	rel, err := filepath.Rel(cwd, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// checkTarget is what vela check builds: a project, or listed files.
type checkTarget struct {
	srcRoot string
	files   []string // listed files, when srcRoot is empty
	ssr     bool
	opts    CompileOptions
}

// run builds the target into a temporary directory, discarding the output
// and the build's own messages, and returns the diagnostics and the number
// of components checked.
func (c checkTarget) run() ([]*Diagnostic, int, error) {
	out, err := os.MkdirTemp("", "vela-check-")
	if err != nil {
		return nil, 0, err
	}
	defer os.RemoveAll(out)
	opts := c.opts
	opts.StatusWriter, opts.ErrWriter = io.Discard, io.Discard
	opts.Report = &diagReport{}
	files := c.files
	failed := 0
	switch {
	case c.srcRoot != "":
		files = CollectVela(c.srcRoot)
		if len(files) == 0 {
			break
		}
		if c.ssr {
			failed = buildSSRProject(files, c.srcRoot, out, opts, true)
		} else {
			failed = buildProject(files, c.srcRoot, out, opts, true)
		}
	default:
		for _, src := range files {
			if !compilePrint(src, filepath.Join(out, ComponentName(src)+".js"), opts) {
				failed++
			}
		}
	}
	diags := opts.Report.sorted()
	if failed > 0 && !hasErrorDiagnostic(diags) {
		// Every failure reports a diagnostic; keep the result failing if one
		// does not.
		diags = append(diags, &Diagnostic{Sev: SevError, Message: fmt.Sprintf("the build failed with %d error(s)", failed)})
	}
	return diags, len(files), nil
}

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
	file := addFileFlags(fs, buildFlagDefaults)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	usage := func(format string, args ...any) int {
		fmt.Fprintf(stderr, "vela check: "+format+"\n", args...)
		return 2
	}
	config, err := loadConfig(*configPath)
	if err == nil && config != nil {
		err = applyFlagValues(fs, ConfigFileName, config.flagValues())
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
	opts, err := file.options()
	if err != nil {
		return usage("%v", err)
	}
	opts.SSRModule = *ssrModule
	target := checkTarget{srcRoot: *projectDir, files: fs.Args(), ssr: *ssr, opts: opts}
	switch {
	case target.srcRoot != "" && len(target.files) > 0:
		return usage("give --project or files, not both")
	case target.srcRoot == "" && len(target.files) == 0:
		return usage("usage: vela check --project <dir> [flags] | vela check [flags] <file.vela>...")
	case target.ssr && target.srcRoot == "":
		return usage("--ssr requires --project")
	}
	if target.srcRoot != "" {
		target.srcRoot = filepath.Clean(target.srcRoot)
		if info, err := os.Stat(target.srcRoot); err != nil || !info.IsDir() {
			return usage("project directory %s not found", target.srcRoot)
		}
	}

	check := func() int {
		diags, components, err := target.run()
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
	roots := target.files
	if target.srcRoot != "" {
		roots = []string{target.srcRoot}
		if target.ssr {
			if project, err := LoadSSRProject(target.srcRoot); err == nil && project.HostDir != "" {
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
