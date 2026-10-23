// Package diag holds the structured compile-time diagnostics shared by the
// compiler layers.
package diag

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
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

// MachineDiagnostic is the JSON form of a Diagnostic (docs/TOOLING_PROTOCOL.md).
// EndLine and EndColumn end the range exclusively (Diagnostic.End). File uses
// forward slashes on every platform.
type MachineDiagnostic struct {
	Severity   string
	Code       string
	File       string
	Line       int
	Column     int
	EndLine    int
	EndColumn  int
	Message    string
	Suggestion string
}

func (d MachineDiagnostic) MarshalJSON() ([]byte, error) {
	out := map[string]interface{}{
		"severity": d.Severity, "line": d.Line, "column": d.Column,
		"endLine": d.EndLine, "endColumn": d.EndColumn, "message": d.Message,
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
	endLine, endCol := d.End()
	return MachineDiagnostic{
		Severity: d.Sev.String(), Code: d.Code, File: filepath.ToSlash(d.File), Line: d.Line,
		Column: d.Col, EndLine: endLine, EndColumn: endCol, Message: d.Message,
		Suggestion: d.Suggestion,
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
	Sev  DiagSeverity
	File string
	Line int
	Col  int
	// EndLine and EndCol end the range exclusively, in the coordinates of
	// Line and Col; zero means the token at the start (End).
	EndLine    int
	EndCol     int
	Code       string // stable machine-readable category, e.g. VELA-EVENT-MODIFIER
	Message    string
	Suggestion string // optional canonical correction
	Source     string // the offending source line (for display)
}

// End is the exclusive end of the range: EndLine and EndCol when they are
// set, else the end of the token at the start on the Source line, else the
// start (a diagnostic of the file without a position).
func (d *Diagnostic) End() (line, col int) {
	if d.EndLine > 0 {
		return d.EndLine, d.EndCol
	}
	return d.Line, d.Col + tokenLength(d.Source, d.Col)
}

// tokenLength is the UTF-16 length of the token at the one-based UTF-16
// column col of line: an identifier, a tag name with its <, a balanced
// {…} or (…) or a quoted string on the line, else one character; zero at
// the end of the line.
func tokenLength(line string, col int) int {
	text := utf16.Encode([]rune(line))
	start := col - 1
	if start < 0 || start >= len(text) {
		return 0
	}
	runes := []rune(string(utf16.Decode(text[start:])))
	isIdent := func(r rune) bool { return r == '_' || r == '$' || unicode.IsLetter(r) || unicode.IsDigit(r) }
	n := 1
	switch first := runes[0]; {
	case isIdent(first):
		for n < len(runes) && isIdent(runes[n]) {
			n++
		}
	case first == '<' && len(runes) > 1 && (unicode.IsLetter(runes[1]) || runes[1] == '/'):
		n = 2
		for n < len(runes) && (isIdent(runes[n]) || runes[n] == '-' || runes[n] == ':' || runes[n] == '.') {
			n++
		}
	case first == '{' || first == '(':
		closing := map[rune]rune{'{': '}', '(': ')'}[first]
		depth := 0
		for i, r := range runes {
			if r == first {
				depth++
			} else if r == closing {
				if depth--; depth == 0 {
					n = i + 1
					break
				}
			}
		}
	case first == '"' || first == '\'' || first == '`':
		for i := 1; i < len(runes); i++ {
			if runes[i] == first {
				n = i + 1
				break
			}
		}
	}
	return len(utf16.Encode(runes[:n]))
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

// add records a diagnostic at pos; with end > pos it covers [pos, end),
// otherwise the token at pos.
func (ds *DiagSet) add(sev DiagSeverity, pos, end int, code, suggestion, msg string) {
	line, col, srcLine := ds.resolve(pos + ds.currentOffset())
	d := &Diagnostic{
		Sev:        sev,
		File:       ds.file,
		Line:       line,
		Col:        col,
		Code:       code,
		Message:    msg,
		Suggestion: suggestion,
		Source:     srcLine,
	}
	if end > pos && pos+ds.currentOffset() >= 0 && end+ds.currentOffset() <= len(ds.src) {
		d.EndLine, d.EndCol, _ = ds.resolve(end + ds.currentOffset())
	}
	ds.diags = append(ds.diags, d)
}

func (ds *DiagSet) Error(pos int, msg string, args ...interface{}) {
	ds.add(SevError, pos, -1, "", "", fmt.Sprintf(msg, args...))
}

// ErrorWith records a stable diagnostic category and canonical correction while
// retaining Message for existing API callers and human-readable output.
func (ds *DiagSet) ErrorWith(pos int, code, suggestion, msg string, args ...interface{}) {
	ds.add(SevError, pos, -1, code, suggestion, fmt.Sprintf(msg, args...))
}

func (ds *DiagSet) Warn(pos int, msg string, args ...interface{}) {
	ds.add(SevWarning, pos, -1, "", "", fmt.Sprintf(msg, args...))
}

// WarnWith records a warning with a stable machine-readable code.
func (ds *DiagSet) WarnWith(pos int, code, suggestion, msg string, args ...interface{}) {
	ds.add(SevWarning, pos, -1, code, suggestion, fmt.Sprintf(msg, args...))
}

func (ds *DiagSet) Hint(pos int, msg string, args ...interface{}) {
	ds.add(SevHint, pos, -1, "", "", fmt.Sprintf(msg, args...))
}

// HintWith records an informational diagnostic with a stable code.
func (ds *DiagSet) HintWith(pos int, code, suggestion, msg string, args ...interface{}) {
	ds.add(SevHint, pos, -1, code, suggestion, fmt.Sprintf(msg, args...))
}

// Report records a diagnostic of severity sev over the source range
// [start, end), for a node whose extent is known; the other methods cover
// the token at their position.
func (ds *DiagSet) Report(sev DiagSeverity, start, end int, code, suggestion, msg string, args ...interface{}) {
	ds.add(sev, start, end, code, suggestion, fmt.Sprintf(msg, args...))
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
