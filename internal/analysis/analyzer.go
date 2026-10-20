package analysis

import (
	"github.com/elauven/vela/internal/jsast"
	"github.com/elauven/vela/internal/sfc"

	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
//  Public analysis result types  (interface unchanged — codegen sees the same)
// ─────────────────────────────────────────────────────────────────────────────

type ScriptPlanStep struct {
	Kind string
	Pos  int
	// SourcePos is the start of a user statement in the original script body.
	// Pos remains the historical statement-end key used for ordering steps.
	SourcePos int
	// Stmt points into the component-owned ScriptAST. It replaces the former
	// executable Source string, so target generation can retain user origins
	// without printing and parsing ordinary script statements again.
	Stmt    jsast.JsStmt
	State   *StateVar
	Derived *DerivedVar
}

type Analysis struct {
	Script        *sfc.ScriptAST
	StateVars     []*StateVar
	DerivedVars   []*DerivedVar
	Effects       []*Effect
	Functions     []*Function
	Imports       []*Import
	Props         []*Prop
	Lifecycles    []*LifecycleHook
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
	Kind    string         // "onMount" | "onDestroy"
	BodyAST *jsast.JsBlock // source AST for the lifecycle callback body
}

type StateVar struct {
	Name       string
	InitialAST jsast.JsExpr // immutable source AST for the initial value
	Pos        int
	// Deep means the state was declared via `$state.deep(...)` rather
	// than plain `$state(...)`.  Codegen wraps the value in a Proxy
	// so that mutations to nested properties (`arr.push(x)`,
	// `obj.a.b = 1`) fire the same notify path that reassignment does.
	// Plain $state only tracks reassignment — matching the previous
	// behavior — so existing code keeps working without proxy overhead.
	Deep bool
	// Reproducible marks an initializer the client computes itself, at no
	// cost and with the server's result: literals and references to props
	// (task 059). Its value is not written to the hydration payload.
	Reproducible bool
}

type DerivedVar struct {
	Name    string
	ExprAST jsast.JsExpr // immutable source AST for the derived expression
	Pos     int
	Deps    []string
}

type Effect struct {
	BodyAST *jsast.JsBlock // source AST for the effect callback body
	Deps    []string
}

type Function struct {
	Name string
}

type Import struct {
	Source      string
	DefaultName string
	Namespace   string
	Specifiers  []jsast.JsImportSpec
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
	DefaultAST jsast.JsExpr // source AST for the declared default, nil → required
	Bindable   bool         // true when declared as export let x = $bindable(default)
}

// ─────────────────────────────────────────────────────────────────────────────
//  Entry point
// ─────────────────────────────────────────────────────────────────────────────

func AnalyzeScript(src string) *Analysis {
	analysis := AnalyzeScriptAST(sfc.NewScriptAST(src, 0))
	// This source-only helper predates owned component ScriptASTs and exposes
	// lowered callback bodies to its standalone callers. The compiler pipeline
	// uses analyzeScriptAST directly and postpones this mutation until after
	// validating the original user program.
	analysis.LowerReactiveCallbackBodies()
	return analysis
}

// AnalyzeScriptAST consumes the owned script program created by parseSFC.
// The compatibility analyzeScript wrapper above remains for unit tests and
// standalone callers that only have source text.
func AnalyzeScriptAST(script *sfc.ScriptAST) *Analysis {
	if script == nil {
		script = sfc.NewScriptAST("", 0)
	}
	prog := script.Program
	a := &Analysis{Script: script}

	// skipEnd maps the End byte-offset of every reactive top-level statement
	// so we can exclude it from CleanScript.
	skipEnd := map[int]bool{}

	// ── First pass: collect reactive declarations ──────────────────────────
	for _, stmt := range prog.Body {
		switch s := stmt.(type) {

		case *jsast.JsImportDecl:
			a.Imports = append(a.Imports, importFromDecl(s))
			// When importing from 'vela' or 'vela/store', any
			// specifier whose ORIGINAL name is a compiler
			// intrinsic gets recorded — keyed by the local
			// binding so a later `init(() => …)` (where `init`
			// aliased `onMount`) still resolves to the intrinsic.
			if s.Source == "vela" || s.Source == "vela/store" {
				for _, spec := range s.Specifiers {
					if VelaCompilerIntrinsics[spec.Imported] {
						if a.IntrinsicAliases == nil {
							a.IntrinsicAliases = map[string]string{}
						}
						a.IntrinsicAliases[spec.Local] = spec.Imported
					}
				}
			}
			skipEnd[s.End] = true

		case *jsast.JsVarDecl:
			for _, decl := range s.Decls {
				if decl.Init == nil {
					continue
				}
				call, ok := decl.Init.(*jsast.JsCallExpr)
				if !ok {
					continue
				}
				name := IdentName(decl.Id)

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
				case *jsast.JsIdent:
					calleeName = c.Name
				case *jsast.JsMemberExpr:
					if obj, ok := c.Obj.(*jsast.JsIdent); ok && obj.Name == "$state" {
						if prop, ok := c.Prop.(*jsast.JsIdent); ok && prop.Name == "deep" {
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
					var initAST jsast.JsExpr
					if len(call.Args) > 0 {
						initAST = call.Args[0]
					}
					a.StateVars = append(a.StateVars, &StateVar{
						Name: name, InitialAST: initAST, Pos: s.NodeEnd(), Deep: stateDeep,
					})
					skipEnd[s.End] = true

				case "$derived":
					var exprAST jsast.JsExpr
					if len(call.Args) > 0 {
						exprAST = call.Args[0]
					}
					a.DerivedVars = append(a.DerivedVars, &DerivedVar{Name: name, ExprAST: exprAST, Pos: s.NodeEnd()})
					skipEnd[s.End] = true
					skipEnd[s.End] = true
				}
			}

		case *jsast.JsExprStmt:
			if extractEffect(s.X) {
				a.Effects = append(a.Effects, &Effect{BodyAST: effectCallbackBlock(s.X)})
				skipEnd[s.End] = true
			} else if kind, ok := extractLifecycle(s.X, a.IntrinsicAliases); ok {
				a.Lifecycles = append(a.Lifecycles, &LifecycleHook{Kind: kind, BodyAST: callbackBlock(s.X)})
				skipEnd[s.End] = true
			}

		// export let prop = defaultVal  or  export let prop = $bindable(default)
		case *jsast.JsExportDecl:
			if vd, ok := s.Declaration.(*jsast.JsVarDecl); ok && vd.Kind == "let" {
				for _, decl := range vd.Decls {
					name := IdentName(decl.Id)
					if name == "" {
						continue
					}
					var defAST jsast.JsExpr
					bindable := false
					if decl.Init != nil {
						// Check for $bindable(expr) wrapper
						if call, ok := decl.Init.(*jsast.JsCallExpr); ok {
							if callee, ok := call.Callee.(*jsast.JsIdent); ok && callee.Name == "$bindable" {
								bindable = true
								if len(call.Args) > 0 {
									defAST = call.Args[0]
								}
							}
						}
						if !bindable {
							defAST = decl.Init
						}
					}
					a.Props = append(a.Props, &Prop{Name: name, DefaultAST: defAST, Bindable: bindable})
				}
				skipEnd[s.End] = true
			}

		case *jsast.JsFuncDecl:
			if s.Name != "" {
				a.Functions = append(a.Functions, &Function{Name: s.Name})
			}
		}
	}

	// ── Second pass: AST-based dependency analysis ─────────────────────────
	allNames := a.ReactiveNames()

	for _, dv := range a.DerivedVars {
		dv.Deps = jsast.FindDepsInNode(dv.ExprAST, allNames)
		allNames = append(allNames, dv.Name)
	}

	a.DerivedVars, a.DerivedCycles = orderDerivedVars(a.DerivedVars)

	allNames = a.ReactiveNames()
	for _, eff := range a.Effects {
		// Find all reactive names referenced in the effect body
		allDeps := jsast.FindDepsInNode(eff.BodyAST, allNames)
		// Exclude variables that are ASSIGNED inside the effect body.
		// An effect that writes `x = y` should depend on y (the read) but
		// NOT on x (the write) — otherwise $$set_x would re-run the effect,
		// which re-runs $$set_x, causing infinite recursion.
		writtenVars := FindWrittenVars(eff.BodyAST)
		var readDeps []string
		for _, d := range allDeps {
			if !writtenVars[d] {
				readDeps = append(readDeps, d)
			}
		}
		eff.Deps = readDeps
	}

	// ── Rewrite assignments inside lifecycle and effect bodies ───────────────
	a.ScriptPlan = buildScriptPlan(prog, skipEnd, a)
	a.markReproducibleState()

	return a
}

// markReproducibleState classifies the $state initializers (task 059).
func (a *Analysis) markReproducibleState() {
	props := map[string]bool{}
	for _, p := range a.Props {
		props[p.Name] = true
	}
	for _, sv := range a.StateVars {
		sv.Reproducible = reproducibleInit(sv.InitialAST, props)
	}
}

// SerializedState lists the $state bindings whose initial values travel in
// the hydration payload: those without a reproducible initializer. A
// component without any writes no payload entry and reads none (S-6.4).
func (a *Analysis) SerializedState() []*StateVar {
	var out []*StateVar
	for _, sv := range a.StateVars {
		if !sv.Reproducible {
			out = append(out, sv)
		}
	}
	return out
}

// reproducibleInit reports whether the client computes expr itself with the
// server's result and no work worth avoiding: a literal tree (numbers,
// strings, booleans, null, undefined, arrays and plain objects of such
// values) or a reference to a prop and its fields. On the server nothing
// changes state between its initializer and the render (handlers, effects
// and lifecycle hooks do not run there), so the client value is the server
// value; the props reach the client in the payload (root) or from the
// parent's code (children, S-6.3). Calls, other bindings and everything
// else keep the payload, so S-6.8 still avoids repeated loading.
func reproducibleInit(expr jsast.JsExpr, props map[string]bool) bool {
	switch e := expr.(type) {
	case nil:
		return true
	case *jsast.JsLiteral:
		return e.Kind == "number" || e.Kind == "string" || e.Kind == "boolean" || e.Kind == "null"
	case *jsast.JsTemplateLit:
		return len(e.Exprs) == 0
	case *jsast.JsUnaryExpr:
		_, literal := e.X.(*jsast.JsLiteral)
		return e.Prefix && (e.Op == "-" || e.Op == "+" || e.Op == "!") && literal && reproducibleInit(e.X, props)
	case *jsast.JsIdent:
		return e.Name == "undefined" || props[e.Name]
	case *jsast.JsMemberExpr:
		if e.Computed {
			if _, literal := e.Prop.(*jsast.JsLiteral); !literal {
				return false
			}
		}
		return propReference(e.Obj, props)
	case *jsast.JsArrayExpr:
		for _, item := range e.Elems {
			if _, spread := item.(*jsast.JsSpreadExpr); spread || !reproducibleInit(item, props) {
				return false
			}
		}
		return true
	case *jsast.JsObjectExpr:
		for _, p := range e.Props {
			if p.Spread || p.Computed || p.Method || p.Kind != "init" || !reproducibleInit(p.Value, props) {
				return false
			}
		}
		return true
	}
	return false
}

// propReference reports a prop or a field path of one (issue.project.key).
func propReference(expr jsast.JsExpr, props map[string]bool) bool {
	switch e := expr.(type) {
	case *jsast.JsIdent:
		return props[e.Name]
	case *jsast.JsMemberExpr:
		if e.Computed {
			if _, literal := e.Prop.(*jsast.JsLiteral); !literal {
				return false
			}
		}
		return propReference(e.Obj, props)
	}
	return false
}

// LowerReactiveCallbackBodies runs only after input validation, when the
// source ScriptAST is no longer used for scope diagnostics. Keeping analysis
// non-mutating prevents compiler-generated $$set_* identifiers from leaking
// into user-scope validation when ScriptAST is shared across the pipeline.
func (a *Analysis) LowerReactiveCallbackBodies() {
	if a == nil {
		return
	}
	stateSet := a.StateSet()
	for _, lc := range a.Lifecycles {
		RewriteAssignmentsAST(lc.BodyAST, stateSet)
	}
	for _, eff := range a.Effects {
		RewriteAssignmentsAST(eff.BodyAST, stateSet)
	}
	for i := range a.ScriptPlan {
		step := &a.ScriptPlan[i]
		if step.Kind == "user" {
			step.Stmt = RewriteStatementAssignmentsAST(step.Stmt, stateSet)
		}
	}
}

func buildScriptPlan(prog *jsast.JsProgram, skipEnd map[int]bool, a *Analysis) []ScriptPlanStep {
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
		if _, ok := stmt.(*jsast.JsImportDecl); ok {
			continue
		}
		if state := stateAt[stmt.NodeEnd()]; state != nil {
			plan = append(plan, ScriptPlanStep{Kind: "state", Pos: stmt.NodeEnd(), State: state})
			continue
		}
		if derived := derivedAt[stmt.NodeEnd()]; derived != nil {
			plan = append(plan, ScriptPlanStep{Kind: "derived", Pos: stmt.NodeEnd(), Derived: derived})
			continue
		}
		if skipEnd[stmt.NodeEnd()] {
			continue
		}
		if stmt != nil {
			plan = append(plan, ScriptPlanStep{
				Kind: "user", Pos: stmt.NodeEnd(), SourcePos: stmt.NodePos(), Stmt: stmt,
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

// CleanScript is a compatibility/debug view generated directly from the
// typed ScriptPlan. It is deliberately not stored on Analysis, so user code
// has one canonical representation: the owned source AST.
func (a *Analysis) CleanScript() string {
	if a == nil {
		return ""
	}
	return buildCleanScript(a.ScriptPlan)
}

func buildCleanScript(plan []ScriptPlanStep) string {
	prog := &jsast.JsProgram{}
	for _, step := range plan {
		if step.Kind == "user" && step.Stmt != nil {
			prog.Body = append(prog.Body, step.Stmt)
		}
	}
	if len(prog.Body) == 0 {
		return ""
	}
	return strings.TrimSpace(jsast.PrintJS(prog, jsast.PrintPretty))
}

// ─────────────────────────────────────────────────────────────────────────────
//  Helpers
// ─────────────────────────────────────────────────────────────────────────────

func importFromDecl(d *jsast.JsImportDecl) *Import {
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
		Source:      d.Source,
		DefaultName: d.DefaultName,
		Namespace:   d.Namespace,
		Specifiers:  append([]jsast.JsImportSpec(nil), d.Specifiers...),
		Names:       names,
	}
}

func buildImportSpec(d *jsast.JsImportDecl) string {
	return formatImportSpec(&Import{
		DefaultName: d.DefaultName,
		Namespace:   d.Namespace,
		Specifiers:  d.Specifiers,
	})
}

// formatImportSpec is a debug/test presentation generated from the typed
// import fields. It is never used as compiler input or reparsed.
func formatImportSpec(imp *Import) string {
	if imp == nil {
		return ""
	}
	if imp.Namespace != "" {
		return "* as " + imp.Namespace
	}
	var parts []string
	if imp.DefaultName != "" {
		parts = append(parts, imp.DefaultName)
	}
	if len(imp.Specifiers) > 0 {
		var named []string
		for _, s := range imp.Specifiers {
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
func extractLifecycle(expr jsast.JsExpr, aliases map[string]string) (kind string, ok bool) {
	call, isCall := expr.(*jsast.JsCallExpr)
	if !isCall {
		return
	}
	callee, isIdent := call.Callee.(*jsast.JsIdent)
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
	arrow, isArrow := call.Args[0].(*jsast.JsArrowFunc)
	if !isArrow {
		return
	}
	_, isBlock := arrow.Body.(*jsast.JsBlock)
	if !isBlock {
		return
	}
	return name, true
}

// callbackBlock returns the callback body owned by the parsed source AST.
func callbackBlock(expr jsast.JsExpr) *jsast.JsBlock {
	call, ok := expr.(*jsast.JsCallExpr)
	if !ok || len(call.Args) == 0 {
		return nil
	}
	arrow, ok := call.Args[0].(*jsast.JsArrowFunc)
	if !ok {
		return nil
	}
	block, _ := arrow.Body.(*jsast.JsBlock)
	return block
}

func effectCallbackBlock(expr jsast.JsExpr) *jsast.JsBlock {
	call, ok := expr.(*jsast.JsCallExpr)
	if !ok || len(call.Args) == 0 {
		return nil
	}
	callee, ok := call.Callee.(*jsast.JsIdent)
	if !ok || callee.Name != "$effect" {
		return nil
	}
	arrow, ok := call.Args[0].(*jsast.JsArrowFunc)
	if !ok {
		return nil
	}
	block, _ := arrow.Body.(*jsast.JsBlock)
	return block
}

func extractEffect(expr jsast.JsExpr) bool {
	call, ok := expr.(*jsast.JsCallExpr)
	if !ok {
		return false
	}
	callee, ok := call.Callee.(*jsast.JsIdent)
	if !ok || callee.Name != "$effect" {
		return false
	}
	if len(call.Args) == 0 {
		return false
	}
	arrow, ok := call.Args[0].(*jsast.JsArrowFunc)
	if !ok {
		return false
	}
	_, ok = arrow.Body.(*jsast.JsBlock)
	if !ok {
		return false
	}
	return true
}

// FindWrittenVars returns a set of all identifier names that appear on the
// left-hand side of assignment expressions anywhere in the subtree.
// This is used to exclude written variables from effect dependencies —
// an effect should re-run when its *inputs* change, not when its *outputs* change.
func FindWrittenVars(node jsast.JsNode) map[string]bool {
	written := map[string]bool{}
	jsast.WalkNode(node, func(n jsast.JsNode) bool {
		switch x := n.(type) {
		case *jsast.JsAssignExpr:
			if id, ok := x.L.(*jsast.JsIdent); ok {
				written[id.Name] = true
			}
		case *jsast.JsUnaryExpr:
			if x.Op == "++" || x.Op == "--" {
				if id, ok := x.X.(*jsast.JsIdent); ok {
					written[id.Name] = true
				}
			}
		}
		return true
	})
	return written
}

func IdentName(e jsast.JsExpr) string {
	if id, ok := e.(*jsast.JsIdent); ok {
		return id.Name
	}
	return ""
}

// ─────────────────────────────────────────────────────────────────────────────
//  Minimal AST printer
// ─────────────────────────────────────────────────────────────────────────────

func PrintNode(n jsast.JsNode) string {
	if n == nil {
		return ""
	}
	switch x := n.(type) {
	case *jsast.JsProgram:
		var p []string
		for _, s := range x.Body {
			p = append(p, PrintNode(s))
		}
		return strings.Join(p, "\n")
	case *jsast.JsBlock:
		var p []string
		for _, s := range x.Body {
			p = append(p, PrintNode(s))
		}
		return "{\n" + indentBlock(strings.Join(p, "\n")) + "\n}"
	case *jsast.JsExprStmt:
		return PrintNode(x.X) + ";"
	case *jsast.JsReturnStmt:
		if x.Arg == nil {
			return "return;"
		}
		return "return " + PrintNode(x.Arg) + ";"
	case *jsast.JsThrowStmt:
		return "throw " + PrintNode(x.Arg) + ";"
	case *jsast.JsIfStmt:
		s := "if (" + PrintNode(x.Test) + ") " + PrintNode(x.Then)
		if x.Else != nil {
			s += " else " + PrintNode(x.Else)
		}
		return s
	case *jsast.JsWhileStmt:
		return "while (" + PrintNode(x.Test) + ") " + PrintNode(x.Body)
	case *jsast.JsDoWhileStmt:
		return "do " + PrintNode(x.Body) + " while (" + PrintNode(x.Test) + ");"
	case *jsast.JsForStmt:
		init, test, upd := "", "", ""
		if x.Init != nil {
			init = strings.TrimSuffix(PrintNode(x.Init), ";")
		}
		if x.Test != nil {
			test = PrintNode(x.Test)
		}
		if x.Update != nil {
			upd = PrintNode(x.Update)
		}
		return "for (" + init + "; " + test + "; " + upd + ") " + PrintNode(x.Body)
	case *jsast.JsForInStmt:
		kw := "in"
		if x.IsOf {
			kw = "of"
		}
		return "for (" + strings.TrimSuffix(PrintNode(x.Left), ";") + " " + kw + " " + PrintNode(x.Right) + ") " + PrintNode(x.Body)
	case *jsast.JsVarDecl:
		var ds []string
		for _, d := range x.Decls {
			s := PrintNode(d.Id)
			if d.Init != nil {
				s += " = " + PrintNode(d.Init)
			}
			ds = append(ds, s)
		}
		return x.Kind + " " + strings.Join(ds, ", ") + ";"
	case *jsast.JsFuncDecl:
		pre := ""
		if x.Async {
			pre = "async "
		}
		return pre + "function " + x.Name + "(" + printParams(x.Params) + ") " + PrintNode(x.Body)
	case *jsast.JsBreakStmt:
		if x.Label != "" {
			return "break " + x.Label + ";"
		}
		return "break;"
	case *jsast.JsContinueStmt:
		if x.Label != "" {
			return "continue " + x.Label + ";"
		}
		return "continue;"
	case *jsast.JsEmptyStmt:
		return ";"
	// ── Expressions ──────────────────────────────────────────────────────
	case *jsast.JsIdent:
		return x.Name
	case *jsast.JsLiteral:
		return x.Raw
	case *jsast.JsTemplateLit:
		var b strings.Builder
		b.WriteByte('`')
		for i, q := range x.Quasis {
			b.WriteString(q)
			if i < len(x.Exprs) {
				b.WriteString("${")
				b.WriteString(PrintNode(x.Exprs[i]))
				b.WriteByte('}')
			}
		}
		b.WriteByte('`')
		return b.String()
	case *jsast.JsThisExpr:
		return "this"
	case *jsast.JsSuperExpr:
		return "super"
	case *jsast.JsUnaryExpr:
		if x.Prefix {
			return x.Op + PrintNode(x.X)
		}
		return PrintNode(x.X) + x.Op
	case *jsast.JsBinaryExpr:
		return "(" + PrintNode(x.L) + " " + x.Op + " " + PrintNode(x.R) + ")"
	case *jsast.JsLogicalExpr:
		return "(" + PrintNode(x.L) + " " + x.Op + " " + PrintNode(x.R) + ")"
	case *jsast.JsAssignExpr:
		return PrintNode(x.L) + " " + x.Op + " " + PrintNode(x.R)
	case *jsast.JsConditionalExpr:
		return PrintNode(x.Test) + " ? " + PrintNode(x.Then) + " : " + PrintNode(x.Else)
	case *jsast.JsMemberExpr:
		if x.Computed {
			return PrintNode(x.Obj) + "[" + PrintNode(x.Prop) + "]"
		}
		dot := "."
		if x.Optional {
			dot = "?."
		}
		return PrintNode(x.Obj) + dot + PrintNode(x.Prop)
	case *jsast.JsCallExpr:
		var args []string
		for _, a := range x.Args {
			args = append(args, PrintNode(a))
		}
		dot := ""
		if x.Optional {
			dot = "?."
		}
		return PrintNode(x.Callee) + dot + "(" + strings.Join(args, ", ") + ")"
	case *jsast.JsNewExpr:
		var args []string
		for _, a := range x.Args {
			args = append(args, PrintNode(a))
		}
		return "new " + PrintNode(x.Callee) + "(" + strings.Join(args, ", ") + ")"
	case *jsast.JsArrayExpr:
		var elems []string
		for _, e := range x.Elems {
			elems = append(elems, PrintNode(e))
		}
		return "[" + strings.Join(elems, ", ") + "]"
	case *jsast.JsObjectExpr:
		var props []string
		for _, p := range x.Props {
			if p.Spread {
				props = append(props, "..."+PrintNode(p.Value))
			} else if p.Shorthand {
				props = append(props, PrintNode(p.Key))
			} else if p.Computed {
				props = append(props, "["+PrintNode(p.Key)+"]: "+PrintNode(p.Value))
			} else {
				props = append(props, PrintNode(p.Key)+": "+PrintNode(p.Value))
			}
		}
		return "{ " + strings.Join(props, ", ") + " }"
	case *jsast.JsFuncExpr:
		pre := ""
		if x.Async {
			pre = "async "
		}
		return pre + "function(" + printParams(x.Params) + ") " + PrintNode(x.Body)
	case *jsast.JsArrowFunc:
		pre := ""
		if x.Async {
			pre = "async "
		}
		params := "(" + printParams(x.Params) + ")"
		if len(x.Params) == 1 {
			if id, ok := x.Params[0].(*jsast.JsIdent); ok {
				params = id.Name
			}
		}
		return pre + params + " => " + PrintNode(x.Body)
	case *jsast.JsSpreadExpr:
		return "..." + PrintNode(x.X)
	case *jsast.JsAwaitExpr:
		return "await " + PrintNode(x.X)
	case *jsast.JsSeqExpr:
		var parts []string
		for _, e := range x.Exprs {
			parts = append(parts, PrintNode(e))
		}
		return strings.Join(parts, ", ")
	case *jsast.JsLabeledStmt:
		return x.Label + ": " + PrintNode(x.Body)
	}
	return ""
}

func printParams(params []jsast.JsExpr) string {
	var parts []string
	for _, p := range params {
		parts = append(parts, PrintNode(p))
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

func (a *Analysis) ReactiveNames() []string {
	names := append(a.stateNames(), a.derivedNames()...)
	for _, p := range a.Props {
		names = append(names, p.Name)
	}
	return names
}

// StateSet returns a set of all reactive variable names that need setter
// rewriting — both $state vars and export let props (which also get setters).
func (a *Analysis) StateSet() map[string]bool {
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

// VelaCompilerIntrinsics is the set of names that look like imports
// from 'vela' but are actually compiler intrinsics — extracted at
// analysis time and wired directly into generated code.  An import
// statement that mentions one of these must not pass the name
// through to the runtime module.
var VelaCompilerIntrinsics = map[string]bool{
	"onMount":   true,
	"onDestroy": true,
}
