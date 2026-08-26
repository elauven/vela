package main

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

// ─────────────────────────────────────────────────────────────────────────────
//  Script validator — runs after analysis, emits warnings/errors
// ─────────────────────────────────────────────────────────────────────────────

// validateScript walks the program AST and the Analysis to emit semantic
// diagnostics: unused state vars, effects with no deps, direct mutation of
// derived vars, missing required props, etc.
func validateScript(prog *JsProgram, a *Analysis, ds *DiagSet) {
	stateSet := a.stateSet()
	derivedSet := make(map[string]bool, len(a.DerivedVars))
	for _, dv := range a.DerivedVars {
		derivedSet[dv.Name] = true
	}

	// ── Warn: $derived variable assigned in user code ──────────────────────
	// Walk the raw program (not CleanScript) for assignments whose LHS is a
	// derived var.  The rewriter doesn't produce $$set_<derived>() calls —
	// derived names aren't in stateSet — so checking CleanScript for those
	// was a no-op; we look at the pre-rewrite source shape instead.
	WalkNode(prog, func(n JsNode) bool {
		stmt, ok := n.(*JsExprStmt)
		if !ok {
			return true
		}
		// Direct assignment: doubled = …  / doubled += …  / etc.
		if assign, ok := stmt.X.(*JsAssignExpr); ok {
			if id, ok := assign.L.(*JsIdent); ok && derivedSet[id.Name] {
				ds.Warn(id.Pos,
					"cannot assign to $derived variable %q — derived values are read-only", id.Name)
			}
		}
		// Update expression: doubled++ / --doubled
		if upd, ok := stmt.X.(*JsUnaryExpr); ok && (upd.Op == "++" || upd.Op == "--") {
			if id, ok := upd.X.(*JsIdent); ok && derivedSet[id.Name] {
				ds.Warn(id.Pos,
					"cannot assign to $derived variable %q — derived values are read-only", id.Name)
			}
		}
		return true
	})

	// ── $effect with no reactive deps ──────────────────────────────────────
	// Moved to validateEffectBody (reactivity.go), which emits with
	// accurate positions pointing at the $effect call itself rather
	// than the start of the script.

	// ── Warn: $state variable never updated ───────────────────────────────
	// Find each state-var's declaration position in the parsed program
	// so the hint points at the variable rather than the script start.
	statePos := make(map[string]int)
	WalkNode(prog, func(n JsNode) bool {
		decl, ok := n.(*JsVarDecl)
		if !ok {
			return true
		}
		for _, d := range decl.Decls {
			id, ok := d.Id.(*JsIdent)
			if !ok {
				continue
			}
			call, ok := d.Init.(*JsCallExpr)
			if !ok {
				continue
			}
			callee, ok := call.Callee.(*JsIdent)
			if !ok || callee.Name != "$state" {
				continue
			}
			statePos[id.Name] = id.Pos
		}
		return true
	})

	if a.CleanScript() != "" || len(a.Effects) > 0 {
		combined := a.CleanScript()
		for _, eff := range a.Effects {
			combined += "\n" + sourceBlock(eff.BodyAST)
		}
		for _, sv := range a.StateVars {
			setterCall := "$$set_" + sv.Name
			if !strings.Contains(combined, setterCall) {
				ds.Hint(statePos[sv.Name],
					"$state variable %q is never updated; if it's read-only, consider $derived or a plain const",
					sv.Name)
			}
		}
	}

	// ── Warn: reactivity won't trigger on a non-$state binding ────────────
	// Top-level assignment to a *declared but non-reactive* local won't
	// re-render — flag it.  Fully undeclared names ("typoed") are handled
	// by the scope analyzer (BuildScopes), which has a real scope graph
	// and won't false-positive on let/const declared inside blocks.
	knownLocals := make(map[string]bool)
	for _, imp := range a.Imports {
		// Use the pre-parsed Names list rather than re-parsing imp.Spec —
		// the old approach failed on side-effect imports (empty Spec) and
		// imposed a spurious ParseJS call per import.
		for _, name := range imp.Names {
			knownLocals[name] = true
		}
	}
	for _, fn := range a.Functions {
		knownLocals[fn.Name] = true
	}
	for _, prop := range a.Props {
		knownLocals[prop.Name] = true
		stateSet[prop.Name] = true
	}

	// We only fire when we can prove the name *is* declared somewhere —
	// otherwise the scope analyzer already emitted "X is not defined" and
	// piling on with a reactivity warning would be confusing.  A real
	// scope tree would tell us exactly; here we approximate by combining
	// known-local sets plus a quick scan of CleanScript for "let X" /
	// "const X" / "var X" / "function X" tokens.
	declaredSomewhere := func(name string) bool {
		if knownLocals[name] || stateSet[name] || derivedSet[name] {
			return true
		}
		// Cheap textual fallback — good enough to suppress double
		// warnings.  False positives here only mean a missed reactivity
		// warning, never a spurious one.
		for _, kw := range []string{"let ", "const ", "var ", "function "} {
			if idx := strings.Index(a.CleanScript(), kw+name); idx >= 0 {
				// boundary check
				after := idx + len(kw) + len(name)
				if after >= len(a.CleanScript()) || !isIdentByte(a.CleanScript()[after]) {
					return true
				}
			}
		}
		return false
	}

	WalkNode(prog, func(n JsNode) bool {
		stmt, ok := n.(*JsExprStmt)
		if !ok {
			return true
		}
		assign, ok := stmt.X.(*JsAssignExpr)
		if !ok {
			return true
		}
		id, ok := assign.L.(*JsIdent)
		if !ok {
			return true // member expr — fine
		}
		if stateSet[id.Name] || knownLocals[id.Name] || derivedSet[id.Name] {
			return true
		}
		if !declaredSomewhere(id.Name) {
			return true // undeclared — scope pass handles it
		}
		ds.Warn(id.Pos,
			"assignment to %q which is not declared as $state — "+
				"reactivity will not trigger; declare with $state() or use a store",
			id.Name)
		return true
	})
}

// isIdentByte reports whether c can appear inside a JS identifier
// (ASCII subset — good enough for the textual boundary check above).
func isIdentByte(c byte) bool {
	return c == '_' || c == '$' ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9')
}

// ─────────────────────────────────────────────────────────────────────────────
//  Template validator
// ─────────────────────────────────────────────────────────────────────────────

// validateTemplate checks the parsed template AST against the analysis for
// common mistakes: referencing undefined variables, unregistered components,
// missing required props on component invocations.
func validateTemplate(nodes []Node, a *Analysis, ds *DiagSet) {
	importedNames := make(map[string]bool)
	for _, imp := range a.Imports {
		for _, name := range imp.Names {
			importedNames[name] = true
		}
	}

	var walkNodes func([]Node)
	walkNodes = func(ns []Node) {
		for _, node := range ns {
			switch n := node.(type) {
			case *ElementNode:
				walkNodes(n.Children)
			case *ComponentNode:
				// Component must be imported
				if !importedNames[n.Name] {
					ds.Warn(0,
						"component <%s> is used but not imported; "+
							"add: import { %s } from './%s.js'",
						n.Name, n.Name, n.Name)
				}
				walkNodes(flatten(n.Slots))
			case *IfChainNode:
				for _, branch := range n.Branches {
					walkNodes(branch.Children)
				}
			case *EachBlockNode:
				// Session C — unkeyed each over component-bearing
				// rows.  The unkeyed reconciler is positional
				// (index-based): a middle insert or delete shifts
				// every subsequent row's content, so the user's
				// mental model "this row is item B" silently
				// becomes "this row is item C", and the focused
				// input would now belong to a different item.
				// Adding a key (e.g. `as item (item.id)`) tells
				// the reconciler to match rows by identity, which
				// preserves both DOM nodes and the row-to-item
				// mapping across reorderings.
				if n.EachKey == "" && eachHasComponent(n.Children) {
					ds.Hint(0,
						"unkeyed {#each %s} renders a child "+
							"component per row; consider keying "+
							"the loop (e.g. add `(item.id)` after "+
							"`as item`) so middle insertions and "+
							"reorderings preserve each row's "+
							"identity and component state",
						n.EachExpr)
				}
				walkNodes(n.Children)
				walkNodes(n.ElseChildren)
			}
		}
	}
	walkNodes(nodes)
}

// flatten extracts all nodes from a slot map.
func flatten(slots map[string][]Node) []Node {
	var out []Node
	for _, nodes := range slots {
		out = append(out, nodes...)
	}
	return out
}
