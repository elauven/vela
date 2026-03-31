package main

import (
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
//  Public analysis result types  (interface unchanged — codegen sees the same)
// ─────────────────────────────────────────────────────────────────────────────

type Analysis struct {
	StateVars   []*StateVar
	DerivedVars []*DerivedVar
	Effects     []*Effect
	Functions   []*Function
	Imports     []*Import
	Props       []*Prop
	Lifecycles  []*LifecycleHook
	CleanScript string
}

// LifecycleHook represents an onMount or onDestroy call.
type LifecycleHook struct {
	Kind string // "onMount" | "onDestroy"
	Body string // callback body text
}

type StateVar struct {
	Name    string
	Initial string
}

type DerivedVar struct {
	Name string
	Expr string
	Deps []string
}

type Effect struct {
	Body string
	Deps []string
}

type Function struct {
	Name string
}

type Import struct {
	Spec   string
	Source string
}

// Prop represents an `export let name = default` declaration.
// Bindable is set when the default is wrapped in $bindable(default).
type Prop struct {
	Name     string
	Default  string // JS expression; empty → required
	Bindable bool   // true when declared as export let x = $bindable(default)
}

// ─────────────────────────────────────────────────────────────────────────────
//  Entry point
// ─────────────────────────────────────────────────────────────────────────────

func analyzeScript(src string) *Analysis {
	prog := ParseJS(src)
	a := &Analysis{}

	// skipEnd maps the End byte-offset of every reactive top-level statement
	// so we can exclude it from CleanScript.
	skipEnd := map[int]bool{}

	// ── First pass: collect reactive declarations ──────────────────────────
	for _, stmt := range prog.Body {
		switch s := stmt.(type) {

		case *JsImportDecl:
			a.Imports = append(a.Imports, importFromDecl(s))
			skipEnd[s.End] = true

		case *JsVarDecl:
			for _, decl := range s.Decls {
				if decl.Init == nil {
					continue
				}
				call, ok := decl.Init.(*JsCallExpr)
				if !ok {
					continue
				}
				callee, ok := call.Callee.(*JsIdent)
				if !ok {
					continue
				}
				name := identName(decl.Id)
				switch callee.Name {
				case "$state":
					init := ""
					if len(call.Args) > 0 {
						init = strings.TrimSpace(src[call.Args[0].nodePos():call.Args[0].nodeEnd()])
					}
					a.StateVars = append(a.StateVars, &StateVar{Name: name, Initial: init})
					skipEnd[s.End] = true

				case "$derived":
					expr := ""
					if len(call.Args) > 0 {
						expr = strings.TrimSpace(src[call.Args[0].nodePos():call.Args[0].nodeEnd()])
					}
					a.DerivedVars = append(a.DerivedVars, &DerivedVar{Name: name, Expr: expr})
					skipEnd[s.End] = true
				}
			}

		case *JsExprStmt:
			if body, ok := extractEffect(s.X); ok {
				a.Effects = append(a.Effects, &Effect{Body: body})
				skipEnd[s.End] = true
			} else if kind, body, ok := extractLifecycle(s.X); ok {
				a.Lifecycles = append(a.Lifecycles, &LifecycleHook{Kind: kind, Body: body})
				skipEnd[s.End] = true
			}

		// export let prop = defaultVal  or  export let prop = $bindable(default)
		case *JsExportDecl:
			if vd, ok := s.Declaration.(*JsVarDecl); ok && vd.Kind == "let" {
				for _, decl := range vd.Decls {
					name := identName(decl.Id)
					if name == "" {
						continue
					}
					def := ""
					bindable := false
					if decl.Init != nil {
						// Check for $bindable(expr) wrapper
						if call, ok := decl.Init.(*JsCallExpr); ok {
							if callee, ok := call.Callee.(*JsIdent); ok && callee.Name == "$bindable" {
								bindable = true
								if len(call.Args) > 0 {
									def = strings.TrimSpace(src[call.Args[0].nodePos():call.Args[0].nodeEnd()])
								}
							}
						}
						if !bindable {
							def = strings.TrimSpace(src[decl.Init.nodePos():decl.Init.nodeEnd()])
						}
					}
					a.Props = append(a.Props, &Prop{Name: name, Default: def, Bindable: bindable})
				}
				skipEnd[s.End] = true
			}

		case *JsFuncDecl:
			if s.Name != "" {
				a.Functions = append(a.Functions, &Function{Name: s.Name})
			}
		}
	}

	// ── Second pass: AST-based dependency analysis ─────────────────────────
	allNames := a.reactiveNames()

	for _, dv := range a.DerivedVars {
		sub := ParseJS(dv.Expr)
		dv.Deps = FindDepsInNode(sub, allNames)
		allNames = append(allNames, dv.Name)
	}

	allNames = a.reactiveNames()
	for _, eff := range a.Effects {
		sub := ParseJS(eff.Body)
		// Find all reactive names referenced in the effect body
		allDeps := FindDepsInNode(sub, allNames)
		// Exclude variables that are ASSIGNED inside the effect body.
		// An effect that writes `x = y` should depend on y (the read) but
		// NOT on x (the write) — otherwise $$set_x would re-run the effect,
		// which re-runs $$set_x, causing infinite recursion.
		writtenVars := findWrittenVars(sub)
		var readDeps []string
		for _, d := range allDeps {
			if !writtenVars[d] {
				readDeps = append(readDeps, d)
			}
		}
		eff.Deps = readDeps
	}

	// ── Rewrite assignments inside lifecycle and effect bodies ───────────────
	ss := a.stateSet()
	a.CleanScript = buildCleanScript(prog, src, skipEnd, ss)

	for _, lc := range a.Lifecycles {
		if lc.Body == "" {
			continue
		}
		lcProg := ParseJS(lc.Body)
		lc.Body = RewriteAssignments(lc.Body, lcProg, ss)
	}

	for _, eff := range a.Effects {
		if eff.Body == "" {
			continue
		}
		effProg := ParseJS(eff.Body)
		eff.Body = RewriteAssignments(eff.Body, effProg, ss)
	}

	return a
}

// ─────────────────────────────────────────────────────────────────────────────
//  CleanScript
// ─────────────────────────────────────────────────────────────────────────────

func buildCleanScript(prog *JsProgram, src string, skipEnd map[int]bool, stateSet map[string]bool) string {
	var parts []string
	for _, stmt := range prog.Body {
		if skipEnd[stmt.nodeEnd()] {
			continue
		}
		raw := strings.TrimSpace(src[stmt.nodePos():stmt.nodeEnd()])
		if raw != "" {
			parts = append(parts, raw)
		}
	}
	clean := strings.Join(parts, "\n\n")
	if clean == "" {
		return ""
	}
	cleanProg := ParseJS(clean)
	return RewriteAssignments(clean, cleanProg, stateSet)
}

// ─────────────────────────────────────────────────────────────────────────────
//  Helpers
// ─────────────────────────────────────────────────────────────────────────────

func importFromDecl(d *JsImportDecl) *Import {
	return &Import{Spec: buildImportSpec(d), Source: d.Source}
}

func buildImportSpec(d *JsImportDecl) string {
	if d.Namespace != "" {
		return "* as " + d.Namespace
	}
	var parts []string
	if d.DefaultName != "" {
		parts = append(parts, d.DefaultName)
	}
	if len(d.Specifiers) > 0 {
		var named []string
		for _, s := range d.Specifiers {
			if s.Imported == s.Local {
				named = append(named, s.Local)
			} else {
				named = append(named, s.Imported+" as "+s.Local)
			}
		}
		parts = append(parts, "{ "+strings.Join(named, ", ")+" }")
	}
	return strings.Join(parts, ", ")
}

// extractLifecycle checks whether expr is an onMount(() => { … }) or
// onDestroy(() => { … }) call and returns (kind, body, true) if so.
func extractLifecycle(expr JsExpr) (kind, body string, ok bool) {
	call, isCall := expr.(*JsCallExpr)
	if !isCall {
		return
	}
	callee, isIdent := call.Callee.(*JsIdent)
	if !isIdent {
		return
	}
	switch callee.Name {
	case "onMount", "onDestroy":
	default:
		return
	}
	if len(call.Args) == 0 {
		return
	}
	arrow, isArrow := call.Args[0].(*JsArrowFunc)
	if !isArrow {
		return
	}
	block, isBlock := arrow.Body.(*JsBlock)
	if !isBlock {
		return
	}
	var stmts []string
	for _, s := range block.Body {
		stmts = append(stmts, printNode(s))
	}
	return callee.Name, strings.Join(stmts, "\n"), true
}

func extractEffect(expr JsExpr) (string, bool) {
	call, ok := expr.(*JsCallExpr)
	if !ok {
		return "", false
	}
	callee, ok := call.Callee.(*JsIdent)
	if !ok || callee.Name != "$effect" {
		return "", false
	}
	if len(call.Args) == 0 {
		return "", false
	}
	arrow, ok := call.Args[0].(*JsArrowFunc)
	if !ok {
		return "", false
	}
	block, ok := arrow.Body.(*JsBlock)
	if !ok {
		return "", false
	}
	var stmts []string
	for _, s := range block.Body {
		stmts = append(stmts, printNode(s))
	}
	return strings.Join(stmts, "\n"), true
}

// findWrittenVars returns a set of all identifier names that appear on the
// left-hand side of assignment expressions anywhere in the subtree.
// This is used to exclude written variables from effect dependencies —
// an effect should re-run when its *inputs* change, not when its *outputs* change.
func findWrittenVars(node JsNode) map[string]bool {
	written := map[string]bool{}
	WalkNode(node, func(n JsNode) bool {
		switch x := n.(type) {
		case *JsAssignExpr:
			if id, ok := x.L.(*JsIdent); ok {
				written[id.Name] = true
			}
		case *JsUnaryExpr:
			if x.Op == "++" || x.Op == "--" {
				if id, ok := x.X.(*JsIdent); ok {
					written[id.Name] = true
				}
			}
		}
		return true
	})
	return written
}

func identName(e JsExpr) string {
	if id, ok := e.(*JsIdent); ok {
		return id.Name
	}
	return ""
}

// ─────────────────────────────────────────────────────────────────────────────
//  Minimal AST printer
// ─────────────────────────────────────────────────────────────────────────────

func printNode(n JsNode) string {
	if n == nil {
		return ""
	}
	switch x := n.(type) {
	case *JsProgram:
		var p []string
		for _, s := range x.Body {
			p = append(p, printNode(s))
		}
		return strings.Join(p, "\n")
	case *JsBlock:
		var p []string
		for _, s := range x.Body {
			p = append(p, printNode(s))
		}
		return "{\n" + indentBlock(strings.Join(p, "\n")) + "\n}"
	case *JsExprStmt:
		return printNode(x.X) + ";"
	case *JsReturnStmt:
		if x.Arg == nil {
			return "return;"
		}
		return "return " + printNode(x.Arg) + ";"
	case *JsThrowStmt:
		return "throw " + printNode(x.Arg) + ";"
	case *JsIfStmt:
		s := "if (" + printNode(x.Test) + ") " + printNode(x.Then)
		if x.Else != nil {
			s += " else " + printNode(x.Else)
		}
		return s
	case *JsWhileStmt:
		return "while (" + printNode(x.Test) + ") " + printNode(x.Body)
	case *JsDoWhileStmt:
		return "do " + printNode(x.Body) + " while (" + printNode(x.Test) + ");"
	case *JsForStmt:
		init, test, upd := "", "", ""
		if x.Init != nil {
			init = strings.TrimSuffix(printNode(x.Init), ";")
		}
		if x.Test != nil {
			test = printNode(x.Test)
		}
		if x.Update != nil {
			upd = printNode(x.Update)
		}
		return "for (" + init + "; " + test + "; " + upd + ") " + printNode(x.Body)
	case *JsForInStmt:
		kw := "in"
		if x.IsOf {
			kw = "of"
		}
		return "for (" + strings.TrimSuffix(printNode(x.Left), ";") + " " + kw + " " + printNode(x.Right) + ") " + printNode(x.Body)
	case *JsVarDecl:
		var ds []string
		for _, d := range x.Decls {
			s := printNode(d.Id)
			if d.Init != nil {
				s += " = " + printNode(d.Init)
			}
			ds = append(ds, s)
		}
		return x.Kind + " " + strings.Join(ds, ", ") + ";"
	case *JsFuncDecl:
		pre := ""
		if x.Async {
			pre = "async "
		}
		return pre + "function " + x.Name + "(" + printParams(x.Params) + ") " + printNode(x.Body)
	case *JsBreakStmt:
		if x.Label != "" {
			return "break " + x.Label + ";"
		}
		return "break;"
	case *JsContinueStmt:
		if x.Label != "" {
			return "continue " + x.Label + ";"
		}
		return "continue;"
	case *JsEmptyStmt:
		return ";"
	// ── Expressions ──────────────────────────────────────────────────────
	case *JsIdent:
		return x.Name
	case *JsLiteral:
		return x.Raw
	case *JsTemplateLit:
		return x.Raw
	case *JsThisExpr:
		return "this"
	case *JsSuperExpr:
		return "super"
	case *JsUnaryExpr:
		if x.Prefix {
			return x.Op + printNode(x.X)
		}
		return printNode(x.X) + x.Op
	case *JsBinaryExpr:
		return "(" + printNode(x.L) + " " + x.Op + " " + printNode(x.R) + ")"
	case *JsLogicalExpr:
		return "(" + printNode(x.L) + " " + x.Op + " " + printNode(x.R) + ")"
	case *JsAssignExpr:
		return printNode(x.L) + " " + x.Op + " " + printNode(x.R)
	case *JsConditionalExpr:
		return printNode(x.Test) + " ? " + printNode(x.Then) + " : " + printNode(x.Else)
	case *JsMemberExpr:
		if x.Computed {
			return printNode(x.Obj) + "[" + printNode(x.Prop) + "]"
		}
		dot := "."
		if x.Optional {
			dot = "?."
		}
		return printNode(x.Obj) + dot + printNode(x.Prop)
	case *JsCallExpr:
		var args []string
		for _, a := range x.Args {
			args = append(args, printNode(a))
		}
		dot := ""
		if x.Optional {
			dot = "?."
		}
		return printNode(x.Callee) + dot + "(" + strings.Join(args, ", ") + ")"
	case *JsNewExpr:
		var args []string
		for _, a := range x.Args {
			args = append(args, printNode(a))
		}
		return "new " + printNode(x.Callee) + "(" + strings.Join(args, ", ") + ")"
	case *JsArrayExpr:
		var elems []string
		for _, e := range x.Elems {
			elems = append(elems, printNode(e))
		}
		return "[" + strings.Join(elems, ", ") + "]"
	case *JsObjectExpr:
		var props []string
		for _, p := range x.Props {
			if p.Spread {
				props = append(props, "..."+printNode(p.Value))
			} else if p.Shorthand {
				props = append(props, printNode(p.Key))
			} else if p.Computed {
				props = append(props, "["+printNode(p.Key)+"]: "+printNode(p.Value))
			} else {
				props = append(props, printNode(p.Key)+": "+printNode(p.Value))
			}
		}
		return "{ " + strings.Join(props, ", ") + " }"
	case *JsFuncExpr:
		pre := ""
		if x.Async {
			pre = "async "
		}
		return pre + "function(" + printParams(x.Params) + ") " + printNode(x.Body)
	case *JsArrowFunc:
		pre := ""
		if x.Async {
			pre = "async "
		}
		params := "(" + printParams(x.Params) + ")"
		if len(x.Params) == 1 {
			if id, ok := x.Params[0].(*JsIdent); ok {
				params = id.Name
			}
		}
		return pre + params + " => " + printNode(x.Body)
	case *JsSpreadExpr:
		return "..." + printNode(x.X)
	case *JsAwaitExpr:
		return "await " + printNode(x.X)
	case *JsSeqExpr:
		var parts []string
		for _, e := range x.Exprs {
			parts = append(parts, printNode(e))
		}
		return strings.Join(parts, ", ")
	case *JsLabeledStmt:
		return x.Label + ": " + printNode(x.Body)
	}
	return ""
}

func printParams(params []JsExpr) string {
	var parts []string
	for _, p := range params {
		parts = append(parts, printNode(p))
	}
	return strings.Join(parts, ", ")
}

func indentBlock(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = "  " + l
		}
	}
	return strings.Join(lines, "\n")
}

// ─────────────────────────────────────────────────────────────────────────────
//  Helpers on *Analysis
// ─────────────────────────────────────────────────────────────────────────────

func (a *Analysis) stateNames() []string {
	names := make([]string, len(a.StateVars))
	for i, v := range a.StateVars {
		names[i] = v.Name
	}
	return names
}

func (a *Analysis) derivedNames() []string {
	names := make([]string, len(a.DerivedVars))
	for i, v := range a.DerivedVars {
		names[i] = v.Name
	}
	return names
}

func (a *Analysis) reactiveNames() []string {
	names := append(a.stateNames(), a.derivedNames()...)
	for _, p := range a.Props {
		names = append(names, p.Name)
	}
	return names
}

// stateSet returns a set of all reactive variable names that need setter
// rewriting — both $state vars and export let props (which also get setters).
func (a *Analysis) stateSet() map[string]bool {
	m := make(map[string]bool, len(a.StateVars)+len(a.Props))
	for _, v := range a.StateVars {
		m[v.Name] = true
	}
	for _, p := range a.Props {
		m[p.Name] = true
	}
	return m
}

func (a *Analysis) isStateName(name string) bool {
	for _, v := range a.StateVars {
		if v.Name == name {
			return true
		}
	}
	return false
}

func (a *Analysis) isDerivedName(name string) bool {
	for _, v := range a.DerivedVars {
		if v.Name == name {
			return true
		}
	}
	return false
}

// findDeps retains the old signature so codegen and tests continue to work.
func findDeps(expr string, vars []string) []string {
	return FindDepsInNode(ParseJS(expr), vars)
}
