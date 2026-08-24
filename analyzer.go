package main

import (
	"strings"
	"unicode"
)

// ─────────────────────────────────────────────────────────────────────────────
//  Public analysis result types  (interface unchanged — codegen sees the same)
// ─────────────────────────────────────────────────────────────────────────────

type ScriptPlanStep struct {
	Kind string
	Pos  int
	// SourcePos is the start of a user statement in the original script body.
	// Pos remains the historical statement-end key used for rune steps.
	SourcePos int
	Source    string
	State     *StateVar
	Derived   *DerivedVar
}

type Analysis struct {
	Script        *ScriptAST
	StateVars     []*StateVar
	DerivedVars   []*DerivedVar
	Effects       []*Effect
	Functions     []*Function
	Imports       []*Import
	Props         []*Prop
	Lifecycles    []*LifecycleHook
	CleanScript   string
	ImportRecords []ImportRecord
	DerivedCycles [][]string
	ScriptPlan    []ScriptPlanStep
	// IntrinsicAliases maps a local binding name back to the
	// original intrinsic it was aliased from (`"onMount"` or
	// `"onDestroy"`).  Populated when the script imports an
	// intrinsic under a different name, e.g.
	//
	//   import { onMount as init } from 'vela';
	//   init(() => { … });
	//
	// extractLifecycle consults this map so the aliased call is
	// still recognised as a lifecycle hook.  Without it, the
	// callee `init` matched no intrinsic name; the import got
	// stripped (because the imported name was an intrinsic) but
	// the call survived as a reference to an undeclared name,
	// crashing at module load.
	IntrinsicAliases map[string]string
}

// LifecycleHook represents an onMount or onDestroy call.
type LifecycleHook struct {
	Kind    string   // "onMount" | "onDestroy"
	Body    string   // callback body text retained for diagnostics and legacy callers
	BodyAST *JsBlock // source AST for the lifecycle callback body
}

type StateVar struct {
	Name       string
	Initial    string
	InitialAST JsExpr // immutable source AST for the initial value
	Pos        int
	// Deep means the state was declared via `$state.deep(...)` rather
	// than plain `$state(...)`.  Codegen wraps the value in a Proxy
	// so that mutations to nested properties (`arr.push(x)`,
	// `obj.a.b = 1`) fire the same notify path that reassignment does.
	// Plain $state only tracks reassignment — matching the previous
	// behavior — so existing code keeps working without proxy overhead.
	Deep bool
}

type DerivedVar struct {
	Name    string
	Expr    string
	ExprAST JsExpr // immutable source AST for the derived expression
	Pos     int
	Deps    []string
}

type Effect struct {
	Body    string
	BodyAST *JsBlock // source AST for the effect callback body
	Deps    []string
}

type Function struct {
	Name string
}

type Import struct {
	Spec        string
	Source      string
	DefaultName string
	Namespace   string
	Specifiers  []JsImportSpec
	// Names is the list of local binding names introduced by this import.
	// For `import Card from '...'` → ["Card"]; for `import { A, B as C } from
	// '...'` → ["A", "C"]; for `import * as Ns from '...'` → ["Ns"]; for
	// `import Def, { A } from '...'` → ["Def", "A"].  Side-effect imports
	// (`import 'polyfill'`) contribute no names.  Populated once at parse
	// time so downstream passes don't re-parse Spec to recover them.
	Names []string
}

// Prop represents an `export let name = default` declaration.
// Bindable is set when the default is wrapped in $bindable(default).
type Prop struct {
	Name       string
	Default    string // JS expression; empty → required
	DefaultAST JsExpr // source AST for the declared default, nil → required
	Bindable   bool   // true when declared as export let x = $bindable(default)
}

// ─────────────────────────────────────────────────────────────────────────────
//  Entry point
// ─────────────────────────────────────────────────────────────────────────────

func analyzeScript(src string) *Analysis {
	return analyzeScriptAST(newScriptAST(src, 0))
}

// analyzeScriptAST consumes the owned script program created by parseSFC.
// The compatibility analyzeScript wrapper above remains for unit tests and
// standalone callers that only have source text.
func analyzeScriptAST(script *ScriptAST) *Analysis {
	if script == nil {
		script = newScriptAST("", 0)
	}
	prog := script.Program
	src := script.Source
	a := &Analysis{Script: script}

	// skipEnd maps the End byte-offset of every reactive top-level statement
	// so we can exclude it from CleanScript.
	skipEnd := map[int]bool{}

	// ── First pass: collect reactive declarations ──────────────────────────
	for _, stmt := range prog.Body {
		switch s := stmt.(type) {

		case *JsImportDecl:
			a.Imports = append(a.Imports, importFromDecl(s))
			// When importing from 'vela' or 'vela/store', any
			// specifier whose ORIGINAL name is a compiler
			// intrinsic gets recorded — keyed by the local
			// binding so a later `init(() => …)` (where `init`
			// aliased `onMount`) still resolves to the intrinsic.
			if s.Source == "vela" || s.Source == "vela/store" {
				for _, spec := range s.Specifiers {
					if velaCompilerIntrinsics[spec.Imported] {
						if a.IntrinsicAliases == nil {
							a.IntrinsicAliases = map[string]string{}
						}
						a.IntrinsicAliases[spec.Local] = spec.Imported
					}
				}
			}
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
				name := identName(decl.Id)

				// Classify the callee.  Two shapes we care about:
				//
				//   $state(initial)        → callee is *JsIdent "$state"
				//   $state.deep(initial)   → callee is *JsMemberExpr,
				//                             obj=$state, prop=deep
				//
				// We accept the member form only when the property name
				// is "deep"; any other member call (e.g. `$state.foo`)
				// falls through and is left as a plain call by the
				// rewriter.
				var calleeName string
				var stateDeep bool
				switch c := call.Callee.(type) {
				case *JsIdent:
					calleeName = c.Name
				case *JsMemberExpr:
					if obj, ok := c.Obj.(*JsIdent); ok && obj.Name == "$state" {
						if prop, ok := c.Prop.(*JsIdent); ok && prop.Name == "deep" {
							calleeName = "$state"
							stateDeep = true
						}
					}
				}
				if calleeName == "" {
					continue
				}

				switch calleeName {
				case "$state":
					init := ""
					var initAST JsExpr
					if len(call.Args) > 0 {
						initAST = call.Args[0]
						init = strings.TrimSpace(src[initAST.nodePos():initAST.nodeEnd()])
					}
					a.StateVars = append(a.StateVars, &StateVar{
						Name: name, Initial: init, InitialAST: initAST, Pos: s.nodeEnd(), Deep: stateDeep,
					})
					skipEnd[s.End] = true

				case "$derived":
					expr := ""
					var exprAST JsExpr
					if len(call.Args) > 0 {
						exprAST = call.Args[0]
						expr = strings.TrimSpace(src[exprAST.nodePos():exprAST.nodeEnd()])
					}
					a.DerivedVars = append(a.DerivedVars, &DerivedVar{Name: name, Expr: expr, ExprAST: exprAST, Pos: s.nodeEnd()})
					skipEnd[s.End] = true
					skipEnd[s.End] = true
				}
			}

		case *JsExprStmt:
			if body, ok := extractEffect(s.X); ok {
				if raw, found := callbackBodySource(s.X, src); found {
					body = raw
				}
				a.Effects = append(a.Effects, &Effect{Body: body, BodyAST: effectCallbackBlock(s.X)})
				skipEnd[s.End] = true
			} else if kind, body, ok := extractLifecycle(s.X, a.IntrinsicAliases); ok {
				if raw, found := callbackBodySource(s.X, src); found {
					body = raw
				}
				a.Lifecycles = append(a.Lifecycles, &LifecycleHook{Kind: kind, Body: body, BodyAST: callbackBlock(s.X)})
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
					var defAST JsExpr
					bindable := false
					if decl.Init != nil {
						// Check for $bindable(expr) wrapper
						if call, ok := decl.Init.(*JsCallExpr); ok {
							if callee, ok := call.Callee.(*JsIdent); ok && callee.Name == "$bindable" {
								bindable = true
								if len(call.Args) > 0 {
									defAST = call.Args[0]
									def = strings.TrimSpace(src[defAST.nodePos():defAST.nodeEnd()])
								}
							}
						}
						if !bindable {
							defAST = decl.Init
							def = strings.TrimSpace(src[decl.Init.nodePos():decl.Init.nodeEnd()])
						}
					}
					a.Props = append(a.Props, &Prop{Name: name, Default: def, DefaultAST: defAST, Bindable: bindable})
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

	a.DerivedVars, a.DerivedCycles = orderDerivedVars(a.DerivedVars)

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
	a.ScriptPlan = buildScriptPlan(prog, src, skipEnd, a, ss)

	for _, lc := range a.Lifecycles {
		RewriteAssignmentsAST(lc.BodyAST, ss)
	}

	for _, eff := range a.Effects {
		RewriteAssignmentsAST(eff.BodyAST, ss)
	}
	a.ImportRecords = ParseImportRecords(src, 0)
	return a
}

func buildScriptPlan(prog *JsProgram, src string, skipEnd map[int]bool, a *Analysis, stateSet map[string]bool) []ScriptPlanStep {
	stateAt := map[int]*StateVar{}
	for _, state := range a.StateVars {
		stateAt[state.Pos] = state
	}
	derivedAt := map[int]*DerivedVar{}
	for _, derived := range a.DerivedVars {
		derivedAt[derived.Pos] = derived
	}
	var plan []ScriptPlanStep
	for _, stmt := range prog.Body {
		if _, ok := stmt.(*JsImportDecl); ok {
			continue
		}
		if state := stateAt[stmt.nodeEnd()]; state != nil {
			plan = append(plan, ScriptPlanStep{Kind: "state", Pos: stmt.nodeEnd(), State: state})
			continue
		}
		if derived := derivedAt[stmt.nodeEnd()]; derived != nil {
			plan = append(plan, ScriptPlanStep{Kind: "derived", Pos: stmt.nodeEnd(), Derived: derived})
			continue
		}
		if skipEnd[stmt.nodeEnd()] {
			continue
		}
		rawSource := src[stmt.nodePos():stmt.nodeEnd()]
		leading := len(rawSource) - len(strings.TrimLeftFunc(rawSource, unicode.IsSpace))
		raw := strings.TrimSpace(rawSource)
		if raw != "" {
			plan = append(plan, ScriptPlanStep{
				Kind: "user", Pos: stmt.nodeEnd(), SourcePos: stmt.nodePos() + leading,
				Source: RewriteAssignments(raw, ParseJS(raw), stateSet),
			})
		}
	}
	return plan
}

// orderDerivedVars produces a stable topological order for derived values.
// A later declaration may be a dependency of an earlier one; generation must
// nevertheless initialize the dependency first. Cyclic entries stay at the
// end and are reported by semantic validation before code generation.
func orderDerivedVars(vars []*DerivedVar) ([]*DerivedVar, [][]string) {
	byName := make(map[string]*DerivedVar, len(vars))
	index := make(map[string]int, len(vars))
	for i, dv := range vars {
		byName[dv.Name] = dv
		index[dv.Name] = i
	}
	indegree := make(map[string]int, len(vars))
	dependents := make(map[string][]string, len(vars))
	for _, dv := range vars {
		for _, dep := range dv.Deps {
			if _, isDerived := byName[dep]; isDerived {
				indegree[dv.Name]++
				dependents[dep] = append(dependents[dep], dv.Name)
			}
		}
	}
	queue := make([]string, 0, len(vars))
	for _, dv := range vars {
		if indegree[dv.Name] == 0 {
			queue = append(queue, dv.Name)
		}
	}
	var ordered []*DerivedVar
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		ordered = append(ordered, byName[name])
		for _, next := range dependents[name] {
			indegree[next]--
			if indegree[next] == 0 {
				insert := len(queue)
				for i, queued := range queue {
					if index[next] < index[queued] {
						insert = i
						break
					}
				}
				queue = append(queue, "")
				copy(queue[insert+1:], queue[insert:])
				queue[insert] = next
			}
		}
	}
	if len(ordered) == len(vars) {
		return ordered, nil
	}
	var cycle []string
	for _, dv := range vars {
		if indegree[dv.Name] > 0 {
			cycle = append(cycle, dv.Name)
			ordered = append(ordered, dv)
		}
	}
	return ordered, [][]string{cycle}
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
	var names []string
	if d.DefaultName != "" {
		names = append(names, d.DefaultName)
	}
	if d.Namespace != "" {
		names = append(names, d.Namespace)
	}
	for _, spec := range d.Specifiers {
		names = append(names, spec.Local)
	}
	return &Import{
		Spec:        buildImportSpec(d),
		Source:      d.Source,
		DefaultName: d.DefaultName,
		Namespace:   d.Namespace,
		Specifiers:  append([]JsImportSpec(nil), d.Specifiers...),
		Names:       names,
	}
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
// aliases (optional) maps local binding names to the original
// intrinsic name when the script aliased the import, e.g.
// `import { onMount as init } from 'vela'` registers
// aliases["init"] = "onMount" so init(() => { … }) is recognised.
func extractLifecycle(expr JsExpr, aliases map[string]string) (kind, body string, ok bool) {
	call, isCall := expr.(*JsCallExpr)
	if !isCall {
		return
	}
	callee, isIdent := call.Callee.(*JsIdent)
	if !isIdent {
		return
	}
	name := callee.Name
	if real, hit := aliases[name]; hit {
		name = real
	}
	switch name {
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
	return name, strings.Join(stmts, "\n"), true
}

// callbackBodySource returns the original statements inside an arrow callback block.
// Keeping source text avoids losing valid constructs (notably try/catch) in
// the partial JavaScript printer used by legacy extraction.
func callbackBlock(expr JsExpr) *JsBlock {
	call, ok := expr.(*JsCallExpr)
	if !ok || len(call.Args) == 0 {
		return nil
	}
	arrow, ok := call.Args[0].(*JsArrowFunc)
	if !ok {
		return nil
	}
	block, _ := arrow.Body.(*JsBlock)
	return block
}

func callbackBodySource(expr JsExpr, src string) (string, bool) {
	call, ok := expr.(*JsCallExpr)
	if !ok || len(call.Args) == 0 {
		return "", false
	}
	arrow, ok := call.Args[0].(*JsArrowFunc)
	if !ok {
		return "", false
	}
	block, ok := arrow.Body.(*JsBlock)
	if !ok || block.nodePos() < 0 || block.nodeEnd() > len(src) || block.nodeEnd() <= block.nodePos()+1 {
		return "", false
	}
	return strings.TrimSpace(src[block.nodePos()+1 : block.nodeEnd()-1]), true
}

func effectCallbackBlock(expr JsExpr) *JsBlock {
	call, ok := expr.(*JsCallExpr)
	if !ok || len(call.Args) == 0 {
		return nil
	}
	callee, ok := call.Callee.(*JsIdent)
	if !ok || callee.Name != "$effect" {
		return nil
	}
	arrow, ok := call.Args[0].(*JsArrowFunc)
	if !ok {
		return nil
	}
	block, _ := arrow.Body.(*JsBlock)
	return block
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
		var b strings.Builder
		b.WriteByte('`')
		for i, q := range x.Quasis {
			b.WriteString(q)
			if i < len(x.Exprs) {
				b.WriteString("${")
				b.WriteString(printNode(x.Exprs[i]))
				b.WriteByte('}')
			}
		}
		b.WriteByte('`')
		return b.String()
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

// findDeps retains the old signature so codegen and tests continue to work.
func findDeps(expr string, vars []string) []string {
	return FindDepsInNode(ParseJS(expr), vars)
}
