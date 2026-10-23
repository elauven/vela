package compiler

import (
	"github.com/elauven/vela/internal/diag"
	"github.com/elauven/vela/internal/project"

	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// diagReport collects the diagnostics of a build for vela check. Components
// compile in parallel, so it is safe for concurrent use; a nil report
// ignores adds.
type diagReport struct {
	mu    sync.Mutex
	diags []*diag.Diagnostic
}

func (r *diagReport) add(diags ...*diag.Diagnostic) {
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
func (r *diagReport) sorted() []*diag.Diagnostic {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := append([]*diag.Diagnostic(nil), r.diags...)
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

func ErrorWriter(opts CompileOptions) io.Writer {
	if opts.ErrWriter != nil {
		return opts.ErrWriter
	}
	return os.Stderr
}

// buildIssue is a build error of file without a source position.
func buildIssue(file, format string, args ...any) *diag.Diagnostic {
	return &diag.Diagnostic{Sev: diag.SevError, File: filepath.ToSlash(file), Line: 1, Col: 1, Message: fmt.Sprintf(format, args...)}
}

// reportBuildIssue prints and records a build error that belongs to no
// component.
func reportBuildIssue(opts CompileOptions, format string, args ...any) {
	fmt.Fprintf(ErrorWriter(opts), format+"\n", args...)
	opts.Report.add(&diag.Diagnostic{Sev: diag.SevError, Message: fmt.Sprintf(format, args...)})
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

// CheckTarget is what vela check builds: a project, or listed files.
type CheckTarget struct {
	SrcRoot string
	Files   []string // listed files, when srcRoot is empty
	SSR     bool
	Opts    CompileOptions
}

// run builds the target into a temporary directory, discarding the output
// and the build's own messages, and returns the diagnostics and the number
// of components checked.
func (c CheckTarget) Run() ([]*diag.Diagnostic, int, error) {
	out, err := os.MkdirTemp("", "vela-check-")
	if err != nil {
		return nil, 0, err
	}
	defer os.RemoveAll(out)
	opts := c.Opts
	opts.StatusWriter, opts.ErrWriter = io.Discard, io.Discard
	opts.Report = &diagReport{}
	files := c.Files
	failed := 0
	switch {
	case c.SrcRoot != "":
		files = project.CollectVela(c.SrcRoot)
		if len(files) == 0 {
			break
		}
		if c.SSR {
			failed = BuildSSRProject(files, c.SrcRoot, out, opts, true)
		} else {
			failed = BuildProject(files, c.SrcRoot, out, opts, true)
		}
	default:
		for _, src := range files {
			if !CompilePrint(src, filepath.Join(out, project.ComponentName(src)+".js"), opts) {
				failed++
			}
		}
	}
	diags := opts.Report.sorted()
	if failed > 0 && !hasErrorDiagnostic(diags) {
		// Every failure reports a diagnostic; keep the result failing if one
		// does not.
		diags = append(diags, &diag.Diagnostic{Sev: diag.SevError, Message: fmt.Sprintf("the build failed with %d error(s)", failed)})
	}
	return diags, len(files), nil
}
