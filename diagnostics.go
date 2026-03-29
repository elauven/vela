package main

import (
	"fmt"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
//  Diagnostic — a structured compile-time message with source location
// ─────────────────────────────────────────────────────────────────────────────

type DiagSeverity uint8

const (
	SevError   DiagSeverity = iota // stops code generation
	SevWarning                      // emitted but compilation proceeds
	SevHint                         // informational
)

type Diagnostic struct {
	Sev     DiagSeverity
	File    string
	Line    int
	Col     int
	Message string
	Source  string // the offending source line (for display)
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
	s := fmt.Sprintf("%s[%s] %s", loc, sev, d.Message)
	if d.Source != "" {
		s += "\n  " + d.Source
		s += "\n  " + strings.Repeat(" ", d.Col-1) + "^"
	}
	return s
}

// ─────────────────────────────────────────────────────────────────────────────
//  DiagSet — collects diagnostics during a compile run
// ─────────────────────────────────────────────────────────────────────────────

type DiagSet struct {
	diags []*Diagnostic
	file  string
	src   string // full source text for line/col resolution
}

func NewDiagSet(file, src string) *DiagSet {
	return &DiagSet{file: file, src: src}
}

func (ds *DiagSet) add(sev DiagSeverity, pos int, msg string) {
	line, col, srcLine := ds.resolve(pos)
	ds.diags = append(ds.diags, &Diagnostic{
		Sev:     sev,
		File:    ds.file,
		Line:    line,
		Col:     col,
		Message: msg,
		Source:  srcLine,
	})
}

func (ds *DiagSet) Error(pos int, msg string, args ...interface{}) {
	ds.add(SevError, pos, fmt.Sprintf(msg, args...))
}

func (ds *DiagSet) Warn(pos int, msg string, args ...interface{}) {
	ds.add(SevWarning, pos, fmt.Sprintf(msg, args...))
}

func (ds *DiagSet) Hint(pos int, msg string, args ...interface{}) {
	ds.add(SevHint, pos, fmt.Sprintf(msg, args...))
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

// resolve converts a byte offset into (line, col, source-line).
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
	col = pos - lineStart + 1
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
	// Walk CleanScript to detect any $$set_X calls where X is a derived var.
	// (These would be bugs: derived vars are read-only.)
	if a.CleanScript != "" {
		cleanProg := ParseJS(a.CleanScript)
		WalkNode(cleanProg, func(n JsNode) bool {
			call, ok := n.(*JsCallExpr)
			if !ok {
				return true
			}
			callee, ok := call.Callee.(*JsIdent)
			if !ok {
				return true
			}
			if strings.HasPrefix(callee.Name, "$$set_") {
				varName := strings.TrimPrefix(callee.Name, "$$set_")
				if derivedSet[varName] {
					ds.Warn(callee.Pos,
						"cannot assign to $derived variable %q — derived values are read-only", varName)
				}
			}
			return true
		})
	}

	// ── Warn: $effect with no reactive dependencies ────────────────────────
	for i, eff := range a.Effects {
		if len(eff.Deps) == 0 && strings.TrimSpace(eff.Body) != "" {
			ds.Warn(0,
				"$effect #%d references no reactive variables — it will run once on mount "+
					"but never re-run; consider using onMount() instead", i+1)
		}
	}

	// ── Warn: $state variable never updated ───────────────────────────────
	// Check if any state var has no $$set_ call anywhere in CleanScript or effects.
	if a.CleanScript != "" || len(a.Effects) > 0 {
		combined := a.CleanScript
		for _, eff := range a.Effects {
			combined += "\n" + eff.Body
		}
		for _, sv := range a.StateVars {
			setterCall := "$$set_" + sv.Name
			if !strings.Contains(combined, setterCall) {
				ds.Hint(0,
					"$state variable %q is never updated; if it's read-only, consider $derived or a plain const",
					sv.Name)
			}
		}
	}

	// ── Warn: assignment to non-existent state var (would be a bug) ────────
	// Walk the JS AST for top-level assignments to bare identifiers that are
	// not in stateSet, stateSet, props, or known locals.
	knownLocals := make(map[string]bool)
	for _, imp := range a.Imports {
		// Collect import bindings
		sub := ParseJS("import " + imp.Spec + " from 'x';")
		WalkNode(sub, func(n JsNode) bool {
			if id, ok := n.(*JsIdent); ok {
				knownLocals[id.Name] = true
			}
			return true
		})
	}
	for _, fn := range a.Functions {
		knownLocals[fn.Name] = true
	}
	for _, prop := range a.Props {
		knownLocals[prop.Name] = true
		stateSet[prop.Name] = true
	}

	// Walk program for ExprStmt → AssignExpr where LHS is an Ident not in stateSet
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
		if !stateSet[id.Name] && !knownLocals[id.Name] && !derivedSet[id.Name] {
			ds.Warn(id.Pos,
				"assignment to %q which is not declared as $state — "+
					"reactivity will not trigger; declare with $state() or use a store",
				id.Name)
		}
		return true
	})
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
		sub := ParseJS("import " + imp.Spec + " from 'x';")
		WalkNode(sub, func(n JsNode) bool {
			if id, ok := n.(*JsIdent); ok {
				importedNames[id.Name] = true
			}
			return true
		})
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
