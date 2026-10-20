// Package diag holds the structured compile-time diagnostics shared by the
// compiler layers.
package diag

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf16"
)

// ─────────────────────────────────────────────────────────────────────────────
//  Diagnostic — a structured compile-time message with source location
// ─────────────────────────────────────────────────────────────────────────────

type DiagSeverity uint8

const (
	SevError   DiagSeverity = iota // stops code generation
	SevWarning                     // emitted but compilation proceeds
	SevHint                        // informational
)

func (s DiagSeverity) String() string {
	switch s {
	case SevError:
		return "error"
	case SevWarning:
		return "warning"
	case SevHint:
		return "hint"
	default:
		return "unknown"
	}
}

type MachineDiagnostic struct {
	Severity   string
	Code       string
	File       string
	Line       int
	Column     int
	Message    string
	Suggestion string
}

func (d MachineDiagnostic) MarshalJSON() ([]byte, error) {
	out := map[string]interface{}{
		"severity": d.Severity, "line": d.Line, "column": d.Column,
		"message": d.Message,
	}
	if d.Code != "" {
		out["code"] = d.Code
	}
	if d.File != "" {
		out["file"] = d.File
	}
	if d.Suggestion != "" {
		out["suggestion"] = d.Suggestion
	}
	return json.Marshal(out)
}
func (d *Diagnostic) Machine() MachineDiagnostic {
	return MachineDiagnostic{
		Severity: d.Sev.String(), Code: d.Code, File: d.File, Line: d.Line,
		Column: d.Col, Message: d.Message, Suggestion: d.Suggestion,
	}
}

// DiagnosticsJSON provides a compact, stable representation for editor and AI
// integrations. It deliberately omits Source because consumers can read the
// file themselves; positions are one-based UTF-16 line/column coordinates.
func DiagnosticsJSON(diags []*Diagnostic) ([]byte, error) {
	out := make([]MachineDiagnostic, 0, len(diags))
	for _, d := range diags {
		if d != nil {
			out = append(out, d.Machine())
		}
	}
	return json.Marshal(out)
}

type Diagnostic struct {
	Sev        DiagSeverity
	File       string
	Line       int
	Col        int
	Code       string // stable machine-readable category, e.g. VELA-EVENT-MODIFIER
	Message    string
	Suggestion string // optional canonical correction
	Source     string // the offending source line (for display)
}

func (d *Diagnostic) String() string {
	sev := map[DiagSeverity]string{
		SevError:   "error",
		SevWarning: "warning",
		SevHint:    "hint",
	}[d.Sev]

	loc := ""
	if d.File != "" {
		loc = fmt.Sprintf("%s:%d:%d: ", d.File, d.Line, d.Col)
	}
	category := ""
	if d.Code != "" {
		category = " [" + d.Code + "]"
	}
	s := fmt.Sprintf("%s[%s]%s %s", loc, sev, category, d.Message)
	if d.Source != "" {
		s += "\n  " + d.Source
		s += "\n  " + strings.Repeat(" ", d.Col-1) + "^"
	}
	if d.Suggestion != "" {
		s += "\n  help: " + d.Suggestion
	}
	return s
}

// ─────────────────────────────────────────────────────────────────────────────
//  DiagSet — collects diagnostics during a compile run
//
//  Section offsets: a .vela file has up to three sections (<script>,
//  <template>, <style>) whose parsers each see only the inner text.
//  When the script parser reports an error at byte offset 12, that's
//  byte 12 of the *script body*, not byte 12 of the file.  To get
//  accurate line numbers, callers push the section's absolute byte
//  offset before emitting diagnostics from that section:
//
//      ds.PushOffset(sfc.ScriptOffset)
//      validateScript(prog, analysis, ds)
//      ds.PopOffset()
//
//  add() translates incoming positions by the current offset before
//  resolving them against the full source.  This is a stack so nested
//  passes (e.g. a template that re-parses an embedded expression) can
//  layer offsets correctly.
// ─────────────────────────────────────────────────────────────────────────────

type DiagSet struct {
	diags   []*Diagnostic
	file    string
	src     string // full source text for line/col resolution
	offsets []int  // stack of section offsets; top is added to every incoming pos
}

func NewDiagSet(file, src string) *DiagSet {
	return &DiagSet{file: file, src: src}
}

// PushOffset declares that subsequent diagnostic positions are
// relative to byte `off` of the source.  Pair with PopOffset; safe to
// nest.
func (ds *DiagSet) PushOffset(off int) {
	ds.offsets = append(ds.offsets, off)
}

// PopOffset removes the topmost offset pushed via PushOffset.
func (ds *DiagSet) PopOffset() {
	if n := len(ds.offsets); n > 0 {
		ds.offsets = ds.offsets[:n-1]
	}
}

// currentOffset returns the offset added to incoming positions.
func (ds *DiagSet) currentOffset() int {
	if n := len(ds.offsets); n > 0 {
		return ds.offsets[n-1]
	}
	return 0
}

func (ds *DiagSet) add(sev DiagSeverity, pos int, code, suggestion, msg string) {
	line, col, srcLine := ds.resolve(pos + ds.currentOffset())
	ds.diags = append(ds.diags, &Diagnostic{
		Sev:        sev,
		File:       ds.file,
		Line:       line,
		Col:        col,
		Code:       code,
		Message:    msg,
		Suggestion: suggestion,
		Source:     srcLine,
	})
}

func (ds *DiagSet) Error(pos int, msg string, args ...interface{}) {
	ds.add(SevError, pos, "", "", fmt.Sprintf(msg, args...))
}

// ErrorWith records a stable diagnostic category and canonical correction while
// retaining Message for existing API callers and human-readable output.
func (ds *DiagSet) ErrorWith(pos int, code, suggestion, msg string, args ...interface{}) {
	ds.add(SevError, pos, code, suggestion, fmt.Sprintf(msg, args...))
}

func (ds *DiagSet) Warn(pos int, msg string, args ...interface{}) {
	ds.add(SevWarning, pos, "", "", fmt.Sprintf(msg, args...))
}

// WarnWith records a warning with a stable machine-readable code.
func (ds *DiagSet) WarnWith(pos int, code, suggestion, msg string, args ...interface{}) {
	ds.add(SevWarning, pos, code, suggestion, fmt.Sprintf(msg, args...))
}

func (ds *DiagSet) Hint(pos int, msg string, args ...interface{}) {
	ds.add(SevHint, pos, "", "", fmt.Sprintf(msg, args...))
}

// HintWith records an informational diagnostic with a stable code.
func (ds *DiagSet) HintWith(pos int, code, suggestion, msg string, args ...interface{}) {
	ds.add(SevHint, pos, code, suggestion, fmt.Sprintf(msg, args...))
}

// HasErrors returns true if any error-severity diagnostic was recorded.
func (ds *DiagSet) HasErrors() bool {
	for _, d := range ds.diags {
		if d.Sev == SevError {
			return true
		}
	}
	return false
}

// All returns all diagnostics in order.
func (ds *DiagSet) All() []*Diagnostic { return ds.diags }

// Errors returns only error-severity diagnostics.
func (ds *DiagSet) Errors() []*Diagnostic {
	var out []*Diagnostic
	for _, d := range ds.diags {
		if d.Sev == SevError {
			out = append(out, d)
		}
	}
	return out
}

// Warnings returns only warning-severity diagnostics.
func (ds *DiagSet) Warnings() []*Diagnostic {
	var out []*Diagnostic
	for _, d := range ds.diags {
		if d.Sev == SevWarning {
			out = append(out, d)
		}
	}
	return out
}

// resolve converts a byte offset into (line, UTF-16 column, source-line).
// UTF-16 columns match the coordinate system used by JavaScript tooling and
// source maps, so locations remain useful for editors when a line contains
// non-ASCII text before the diagnostic.
func (ds *DiagSet) resolve(pos int) (line, col int, srcLine string) {
	if pos < 0 || pos > len(ds.src) {
		return 1, 1, ""
	}
	line = 1
	lineStart := 0
	for i := 0; i < pos && i < len(ds.src); i++ {
		if ds.src[i] == '\n' {
			line++
			lineStart = i + 1
		}
	}
	col = len(utf16.Encode([]rune(ds.src[lineStart:pos]))) + 1
	// Extract the source line
	end := strings.IndexByte(ds.src[lineStart:], '\n')
	if end < 0 {
		srcLine = ds.src[lineStart:]
	} else {
		srcLine = ds.src[lineStart : lineStart+end]
	}
	return line, col, srcLine
}
