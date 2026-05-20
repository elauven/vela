package main

// reactivity.go — semantic checks for the Vela reactive primitives.
//
// These run after BuildScopes and operate on the *parsed* JS AST so
// positions are accurate (callers wrap them in ds.PushOffset for the
// script section).  Three checks live here:
//
//   1. validateDerivedPurity — $derived(expr) must be a pure function
//      of its dependencies.  Writing to *any* binding (especially a
//      $state var) from inside a derived is almost always a bug:
//      writes don't trigger re-derivation at runtime, and writing to
//      a state var creates an infinite reactive loop.
//
//   2. validateEffectBody — an $effect with no reactive references
//      runs once at mount and never again.  That's usually a mistake
//      — the user probably meant onMount().  Reuses the analyzer's
//      precomputed Effect.Deps so we don't re-parse the body.
//
//   3. validateTemplateRefs — every {expression} in the template
//      must reference names that exist in script scope.  Catches
//      typos like <p>{cont}</p> when the var is named `count`.
//
// All three emit through DiagSet with positions relative to the
// script section (or template section for #3); the caller handles
// translation to file coordinates via PushOffset.

import (
	"strings"
)

// ─────────────────────────────────────────────
//  Validate $derived expressions
// ─────────────────────────────────────────────

// validateDerivedPurity walks every `let X = $derived(...)` in the
// program and flags writes to any binding inside the derived
// expression.  Writes to state vars are upgraded to errors because
// they cause infinite loops; writes to other bindings are warnings
// (rarely intentional, sometimes legitimate if the user is mutating
// a local for a computation — but still surprising).
//
// `analysis` provides the set of state-var names for severity
// classification.
func validateDerivedPurity(prog *JsProgram, analysis *Analysis, ds *DiagSet) {
	if prog == nil {
		return
	}
	stateSet := analysis.stateSet()

	WalkNode(prog, func(n JsNode) bool {
		decl, ok := n.(*JsVarDecl)
		if !ok || len(decl.Decls) == 0 {
			return true
		}
		for _, d := range decl.Decls {
			call, ok := d.Init.(*JsCallExpr)
			if !ok {
				continue
			}
			callee, ok := call.Callee.(*JsIdent)
			if !ok || callee.Name != "$derived" {
				continue
			}
			if len(call.Args) == 0 {
				continue
			}
			scanDerivedExpr(call.Args[0], stateSet, ds)
		}
		return true
	})
}

// scanDerivedExpr walks the argument expression to $derived(...) and
// flags any assignment, update expression, or impurity-producing
// operation it finds.
func scanDerivedExpr(expr JsExpr, stateSet map[string]bool, ds *DiagSet) {
	WalkNode(expr, func(n JsNode) bool {
		switch e := n.(type) {

		case *JsAssignExpr:
			// LHS could be an identifier or a member.  Both are bad
			// inside a derived: assigning to a state var causes an
			// infinite loop; assigning to anything else is at best
			// surprising (the derived re-runs whenever its deps
			// change, so the assignment happens repeatedly).
			if id, ok := e.L.(*JsIdent); ok {
				if stateSet[id.Name] {
					ds.Error(id.Pos,
						"$derived expression writes to $state variable %q — "+
							"this creates an infinite reactive loop; derived "+
							"values must be pure", id.Name)
				} else {
					ds.Warn(id.Pos,
						"$derived expression writes to %q — derived values "+
							"should be pure functions of their dependencies",
						id.Name)
				}
			} else {
				// Member write: obj.x = ...  Still impure.  We don't
				// have a name to call out, so use the position of the
				// assignment operator (approximated by L's position).
				ds.Warn(e.L.nodePos(),
					"$derived expression contains an assignment — derived "+
						"values should be pure functions of their dependencies")
			}

		case *JsUnaryExpr:
			// ++x / x-- / delete — all write/mutate.
			if (e.Op == "++" || e.Op == "--") && e.X != nil {
				if id, ok := e.X.(*JsIdent); ok {
					if stateSet[id.Name] {
						ds.Error(id.Pos,
							"$derived expression mutates $state variable %q via %s — "+
								"this creates an infinite reactive loop",
							id.Name, e.Op)
					} else {
						ds.Warn(id.Pos,
							"$derived expression mutates %q via %s — derived "+
								"values should be pure",
							id.Name, e.Op)
					}
				}
			}

		case *JsAwaitExpr:
			ds.Warn(e.nodePos(),
				"$derived expression uses await — derived values run "+
					"synchronously; consider $effect with a state var")

		case *JsCallExpr:
			// Calling a function might mutate state, but we can't
			// know without inlining.  Stay quiet here to avoid noise.
			// Future work: flag calls to known-impure builtins
			// (console.log is fine, fetch is not).
			_ = e
		}
		return true
	})
}

// ─────────────────────────────────────────────
//  Validate $effect bodies
// ─────────────────────────────────────────────

// validateEffectBody walks every `$effect(...)` call and emits a
// warning if its argument references no reactive variable.  The
// analyzer already populated Effect.Deps; we reuse that and only
// re-walk to recover the source position.
//
// This replaces the same check in diagnostics.go (which reported at
// position 0 because the old Effect struct didn't carry one) — that
// version is left in place but now fires with a useful position via
// the offset machinery.  We do this here properly with real
// positions; the old version is harmless duplication and could be
// retired in a future cleanup.
func validateEffectBody(prog *JsProgram, analysis *Analysis, ds *DiagSet) {
	if prog == nil {
		return
	}
	depsByPos := make(map[int][]string)
	idx := 0
	WalkNode(prog, func(n JsNode) bool {
		stmt, ok := n.(*JsExprStmt)
		if !ok {
			return true
		}
		call, ok := stmt.X.(*JsCallExpr)
		if !ok {
			return true
		}
		callee, ok := call.Callee.(*JsIdent)
		if !ok || callee.Name != "$effect" {
			return true
		}
		if idx < len(analysis.Effects) {
			eff := analysis.Effects[idx]
			depsByPos[callee.Pos] = eff.Deps
		}
		idx++
		return true
	})

	// Emit warnings.  Walk again so positions are visited in source
	// order (better diagnostic UX).
	idx = 0
	WalkNode(prog, func(n JsNode) bool {
		stmt, ok := n.(*JsExprStmt)
		if !ok {
			return true
		}
		call, ok := stmt.X.(*JsCallExpr)
		if !ok {
			return true
		}
		callee, ok := call.Callee.(*JsIdent)
		if !ok || callee.Name != "$effect" {
			return true
		}
		defer func() { idx++ }()
		if idx >= len(analysis.Effects) {
			return true
		}
		eff := analysis.Effects[idx]
		if len(eff.Deps) == 0 && strings.TrimSpace(eff.Body) != "" {
			ds.Warn(callee.Pos,
				"$effect references no reactive variables — it will run "+
					"once on mount but never re-run; consider using onMount() "+
					"instead")
		}
		return true
	})
}

// ─────────────────────────────────────────────
//  Validate template expression references
// ─────────────────────────────────────────────

// validateTemplateRefs walks the template AST and checks that every
// identifier appearing in an expression context (text interpolation,
// event handlers, bindings, control-flow expressions) refers to a
// name that exists in script scope.
//
// Positions in the template AST are not tracked precisely today —
// the template parser stores expression text as strings, so we
// approximate by reporting at offset 0 within the template section.
// That's accurate enough to point the user at the right *file* and
// the right *section*, even if the line within the template is
// approximate.  Improving template position tracking is a separate
// effort.
//
// `templateOffsetWithin` is the byte offset of the template body
// within the source file — used only when we have a string to
// substring-locate within the template text.  Passed in by the
// caller; if zero, all template diagnostics land at the section
// start.
func validateTemplateRefs(nodes []Node, analysis *Analysis, scriptScope *Scope, templateText string, ds *DiagSet) {
	if scriptScope == nil {
		return
	}
	// Build the set of names visible to template expressions: every
	// binding in the script's program scope, plus state/derived/prop
	// names (already covered by the scope, but explicit for clarity).
	known := make(map[string]bool)
	for name := range scriptScope.Bindings {
		known[name] = true
	}
	// Include builtins from the global parent scope.
	if scriptScope.Parent != nil {
		for name := range scriptScope.Parent.Bindings {
			known[name] = true
		}
	}
	// Reactive names are bindings too, but include them defensively
	// in case scope-graph construction missed any (e.g. if the
	// analyzer found something the parser didn't).
	for _, name := range analysis.reactiveNames() {
		known[name] = true
	}

	var walkNodes func([]Node)
	walkNodes = func(ns []Node) {
		for _, node := range ns {
			switch n := node.(type) {
			case *TextNode:
				for _, p := range n.Parts {
					if !p.Static {
						checkExprRefs(p.Value, known, templateText, ds)
					}
				}
			case *ElementNode:
				for _, a := range n.Attrs {
					checkAttrRefs(a, known, templateText, ds)
				}
				if n.IfCond != "" {
					checkExprRefs(n.IfCond, known, templateText, ds)
				}
				if n.EachExpr != "" {
					// "items as item, i" — the part before "as" is an
					// expression; the names after are introduced locally.
					if before, _, ok := splitEachExpr(n.EachExpr); ok {
						checkExprRefs(before, known, templateText, ds)
					}
				}
				walkNodes(n.Children)
			case *ComponentNode:
				for _, a := range n.Attrs {
					checkAttrRefs(a, known, templateText, ds)
				}
				for _, slotNodes := range n.Slots {
					walkNodes(slotNodes)
				}
			case *IfChainNode:
				for _, branch := range n.Branches {
					if branch.Cond != "" {
						checkExprRefs(branch.Cond, known, templateText, ds)
					}
					walkNodes(branch.Children)
				}
			case *EachBlockNode:
				if before, _, ok := splitEachExpr(n.EachExpr); ok {
					checkExprRefs(before, known, templateText, ds)
				}
				walkNodes(n.Children)
			}
		}
	}
	walkNodes(nodes)
}

// checkAttrRefs validates the expression value of an attribute,
// skipping plain string attributes.
func checkAttrRefs(a *Attr, known map[string]bool, templateText string, ds *DiagSet) {
	if a == nil {
		return
	}
	// Heuristic: an attribute whose value is wrapped in {…} or is a
	// bound/event handler holds an expression.  Static strings don't.
	val := a.Value
	if a.IsEvent || a.IsBind {
		checkExprRefs(val, known, templateText, ds)
		return
	}
	// {expr} form
	if strings.HasPrefix(val, "{") && strings.HasSuffix(val, "}") {
		inner := val[1 : len(val)-1]
		checkExprRefs(inner, known, templateText, ds)
	}
}

// checkExprRefs parses a small JS expression and resolves every
// identifier it contains against `known`.  Unknown identifiers are
// reported as warnings.
//
// `templateText` is the original template source; if non-empty, we
// substring-search for the unknown name to produce a usable position
// (relative to the template section's offset, which the caller has
// pushed onto DiagSet).
func checkExprRefs(exprText string, known map[string]bool, templateText string, ds *DiagSet) {
	exprText = strings.TrimSpace(exprText)
	if exprText == "" {
		return
	}
	// ParseJS parses a program; wrapping the expression in a statement
	// position is the easiest way to use it.  An assignment shape is
	// neutral and works for both expressions and expression-statements.
	prog := ParseJS("(" + exprText + ");")
	if prog == nil {
		return // unparseable — leave alone, the user will see the JS error
	}

	seen := make(map[string]bool)
	WalkNode(prog, func(n JsNode) bool {
		// Don't descend into function/arrow bodies — they introduce
		// their own scopes, and parameters / locals would falsely
		// register as unknown.  The common case in templates is a
		// short expression; entering closures is rare and would
		// require running the full scope analyzer per expression.
		switch n.(type) {
		case *JsFuncExpr, *JsArrowFunc, *JsFuncDecl:
			return false
		}
		// Member expressions: only the object is a reference, not
		// the property name.
		if me, ok := n.(*JsMemberExpr); ok {
			// Walk the object as a reference, but stop here so the
			// non-computed property name isn't visited.
			WalkNode(me.Obj, func(inner JsNode) bool {
				if id, ok := inner.(*JsIdent); ok {
					checkIdentRef(id, known, seen, templateText, ds)
				}
				if me, ok := inner.(*JsMemberExpr); ok {
					_ = me
					return true
				}
				switch inner.(type) {
				case *JsFuncExpr, *JsArrowFunc, *JsFuncDecl:
					return false
				}
				return true
			})
			if me.Computed {
				// Property is an expression — recurse normally.
				WalkNode(me.Prop, func(inner JsNode) bool {
					if id, ok := inner.(*JsIdent); ok {
						checkIdentRef(id, known, seen, templateText, ds)
					}
					return true
				})
			}
			return false
		}
		// Object literal property keys are names, not references —
		// only walk values.  Shorthand `{ x }` makes x a reference,
		// which the parser already represents by having Key == Value
		// as the same JsIdent; that lands here normally.
		if obj, ok := n.(*JsObjectExpr); ok {
			for _, prop := range obj.Props {
				if prop.Shorthand {
					if id, ok := prop.Key.(*JsIdent); ok {
						checkIdentRef(id, known, seen, templateText, ds)
					}
					continue
				}
				if prop.Computed && prop.Key != nil {
					WalkNode(prop.Key, func(inner JsNode) bool {
						if id, ok := inner.(*JsIdent); ok {
							checkIdentRef(id, known, seen, templateText, ds)
						}
						return true
					})
				}
				if prop.Value != nil {
					WalkNode(prop.Value, func(inner JsNode) bool {
						if id, ok := inner.(*JsIdent); ok {
							checkIdentRef(id, known, seen, templateText, ds)
						}
						switch inner.(type) {
						case *JsFuncExpr, *JsArrowFunc, *JsFuncDecl:
							return false
						}
						return true
					})
				}
			}
			return false
		}
		if id, ok := n.(*JsIdent); ok {
			checkIdentRef(id, known, seen, templateText, ds)
		}
		return true
	})
}

// checkIdentRef resolves one identifier against the known-names set
// and emits a warning if missing.  `seen` deduplicates so the same
// unknown name in one expression only warns once.
func checkIdentRef(id *JsIdent, known, seen map[string]bool, templateText string, ds *DiagSet) {
	if known[id.Name] || seen[id.Name] {
		return
	}
	seen[id.Name] = true
	// Try to locate the name inside the template text for a better
	// position.  If not found, fall back to position 0 (the start of
	// the template section).
	pos := findTemplatePos(templateText, id.Name)
	ds.Warn(pos,
		"template references %q but no such name is declared in <script>",
		id.Name)
}

// findTemplatePos returns the byte offset of the first occurrence of
// `name` as a standalone identifier in `templateText`, or 0 if not
// found.  Same boundary-check logic as dce.go's referencedInTemplate.
func findTemplatePos(templateText, name string) int {
	for i := 0; i+len(name) <= len(templateText); i++ {
		if templateText[i:i+len(name)] != name {
			continue
		}
		before := i == 0 || !isIdentByte(templateText[i-1])
		after := i+len(name) == len(templateText) || !isIdentByte(templateText[i+len(name)])
		if before && after {
			return i
		}
	}
	return 0
}

// splitEachExpr parses the "items as item, i" form of #each, returning
// the expression part and the bound names.  Returns ok=false on
// malformed input.
func splitEachExpr(expr string) (iterExpr string, boundNames []string, ok bool) {
	idx := strings.Index(expr, " as ")
	if idx < 0 {
		return expr, nil, true // bare "items" — no bindings
	}
	iterExpr = strings.TrimSpace(expr[:idx])
	rest := strings.TrimSpace(expr[idx+4:])
	for _, name := range strings.Split(rest, ",") {
		if n := strings.TrimSpace(name); n != "" {
			boundNames = append(boundNames, n)
		}
	}
	return iterExpr, boundNames, true
}
