package main

import "strings"

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

// validateStateWrites hints at a plain $state binding that nothing writes.
// Writes come from the scope graph of the original script (assignments and
// updates anywhere in it, including nested functions and effects) and from
// the template (inline event handlers and bind: targets). The check is
// AST-based: printed script text no longer contains setter calls.
// $state.deep bindings are skipped because in-place mutation also updates
// them.
func validateStateWrites(prog *JsProgram, scriptScope *Scope, tmpl []Node, a *Analysis, ds *DiagSet) {
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
	if len(statePos) == 0 {
		return
	}

	written := templateWrittenNames(tmpl)
	for _, sv := range a.StateVars {
		pos, ok := statePos[sv.Name]
		if !ok || written[sv.Name] {
			continue
		}
		if scriptScope != nil {
			if binding := scriptScope.Bindings[sv.Name]; binding != nil && len(binding.Writes) > 0 {
				continue
			}
		}
		ds.Hint(pos,
			"$state variable %q is never updated; if it's read-only, consider $derived or a plain const",
			sv.Name)
	}
}

// templateWrittenNames returns the identifiers that template code writes:
// assignment and update targets inside event handlers, and the targets of
// bind: directives on elements and components.
func templateWrittenNames(nodes []Node) map[string]bool {
	written := make(map[string]bool)
	visitAttrs := func(attrs []*Attr) {
		for _, attr := range attrs {
			if attr.Expr == nil || attr.Expr.Root() == nil {
				continue
			}
			root := attr.Expr.Root()
			if attr.IsTwoWay || attr.IsBindThis {
				if id, ok := root.(*JsIdent); ok {
					written[id.Name] = true
				}
				continue
			}
			if attr.IsEvent {
				collectWrittenNames(root, written)
			}
		}
	}
	var walk func([]Node)
	walk = func(ns []Node) {
		for _, node := range ns {
			switch n := node.(type) {
			case *ElementNode:
				visitAttrs(n.Attrs)
				walk(n.Children)
			case *ComponentNode:
				visitAttrs(n.Attrs)
				for _, slotNodes := range n.Slots {
					walk(slotNodes)
				}
			case *IfChainNode:
				for _, branch := range n.Branches {
					walk(branch.Children)
				}
			case *EachBlockNode:
				walk(n.Children)
				walk(n.ElseChildren)
			case *AwaitBlockNode:
				walk(n.PendingChildren)
				walk(n.ThenChildren)
				walk(n.CatchChildren)
			case *SlotNode:
				walk(n.Fallback)
			}
		}
	}
	walk(nodes)
	return written
}

// collectWrittenNames adds the identifiers assigned or updated anywhere in
// expr, including destructuring targets.
func collectWrittenNames(expr JsNode, written map[string]bool) {
	var addTarget func(JsExpr)
	addTarget = func(target JsExpr) {
		switch t := target.(type) {
		case *JsIdent:
			written[t.Name] = true
		case *JsAssignExpr:
			addTarget(t.L)
		case *JsArrayExpr:
			for _, el := range t.Elems {
				addTarget(el)
			}
		case *JsObjectExpr:
			for _, prop := range t.Props {
				if prop.Value != nil {
					addTarget(prop.Value)
				} else {
					addTarget(prop.Key)
				}
			}
		case *JsSpreadExpr:
			addTarget(t.X)
		}
	}
	WalkNode(expr, func(n JsNode) bool {
		switch node := n.(type) {
		case *JsAssignExpr:
			addTarget(node.L)
		case *JsUnaryExpr:
			if node.Op == "++" || node.Op == "--" {
				addTarget(node.X)
			}
		}
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
