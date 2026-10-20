package jsast

// WalkNode calls fn on every node in the subtree rooted at node, depth-first.
// If fn returns false the subtree below that node is not visited.
func WalkNode(node JsNode, fn func(JsNode) bool) {
	if node == nil {
		return
	}
	if !fn(node) {
		return
	}
	switch n := node.(type) {
	case *JsProgram:
		for _, s := range n.Body {
			WalkNode(s, fn)
		}
	case *JsBlock:
		for _, s := range n.Body {
			WalkNode(s, fn)
		}
	case *JsExprStmt:
		WalkNode(n.X, fn)
	case *JsVarDecl:
		for _, d := range n.Decls {
			WalkNode(d.Id, fn)
			if d.Init != nil {
				WalkNode(d.Init, fn)
			}
		}
	case *JsFuncDecl:
		for _, p := range n.Params {
			WalkNode(p, fn)
		}
		WalkNode(n.Body, fn)
	case *JsFuncExpr:
		for _, p := range n.Params {
			WalkNode(p, fn)
		}
		WalkNode(n.Body, fn)
	case *JsArrowFunc:
		for _, p := range n.Params {
			WalkNode(p, fn)
		}
		WalkNode(n.Body, fn)
	case *JsIfStmt:
		WalkNode(n.Test, fn)
		WalkNode(n.Then, fn)
		if n.Else != nil {
			WalkNode(n.Else, fn)
		}
	case *JsForStmt:
		if n.Init != nil {
			WalkNode(n.Init, fn)
		}
		if n.Test != nil {
			WalkNode(n.Test, fn)
		}
		if n.Update != nil {
			WalkNode(n.Update, fn)
		}
		WalkNode(n.Body, fn)
	case *JsForInStmt:
		WalkNode(n.Left, fn)
		WalkNode(n.Right, fn)
		WalkNode(n.Body, fn)
	case *JsWhileStmt:
		WalkNode(n.Test, fn)
		WalkNode(n.Body, fn)
	case *JsDoWhileStmt:
		WalkNode(n.Body, fn)
		WalkNode(n.Test, fn)
	case *JsReturnStmt:
		if n.Arg != nil {
			WalkNode(n.Arg, fn)
		}
	case *JsThrowStmt:
		WalkNode(n.Arg, fn)
	case *JsTryStmt:
		WalkNode(n.Block, fn)
		if n.Handler != nil {
			WalkNode(n.Handler, fn)
		}
		if n.Finalizer != nil {
			WalkNode(n.Finalizer, fn)
		}
	case *JsSwitchStmt:
		WalkNode(n.Disc, fn)
		for _, c := range n.Cases {
			if c.Test != nil {
				WalkNode(c.Test, fn)
			}
			for _, s := range c.Body {
				WalkNode(s, fn)
			}
		}
	case *JsLabeledStmt:
		WalkNode(n.Body, fn)
	case *JsExportDecl:
		if n.Declaration != nil {
			WalkNode(n.Declaration, fn)
		}
	case *JsClassDecl:
		if n.SuperClass != nil {
			WalkNode(n.SuperClass, fn)
		}
		for _, m := range n.Members {
			WalkNode(m, fn)
		}
	case *JsClassExpr:
		if n.SuperClass != nil {
			WalkNode(n.SuperClass, fn)
		}
		for _, m := range n.Members {
			WalkNode(m, fn)
		}
	case *JsClassMember:
		// A non-computed key, private names included, is a name and not a
		// variable reference, like a member expression's .Prop.
		if n.Computed && n.Key != nil {
			WalkNode(n.Key, fn)
		}
		if n.Value != nil {
			WalkNode(n.Value, fn)
		}
		if n.Body != nil {
			WalkNode(n.Body, fn)
		}
	// Expressions
	case *JsArrayExpr:
		for _, e := range n.Elems {
			if e != nil {
				WalkNode(e, fn)
			}
		}
	case *JsObjectExpr:
		for _, prop := range n.Props {
			// A non-computed property key — shorthand ({ x }) or
			// keyed ({ k: v }) — is a name literal, not a variable
			// reference, exactly like a member expression's .Prop.
			// Only a computed key ({ [expr]: v }) is a real
			// expression to walk.
			if prop.Computed {
				WalkNode(prop.Key, fn)
			}
			WalkNode(prop.Value, fn)
		}
	case *JsUnaryExpr:
		WalkNode(n.X, fn)
	case *JsBinaryExpr:
		WalkNode(n.L, fn)
		WalkNode(n.R, fn)
	case *JsLogicalExpr:
		WalkNode(n.L, fn)
		WalkNode(n.R, fn)
	case *JsAssignExpr:
		WalkNode(n.L, fn)
		WalkNode(n.R, fn)
	case *JsMemberExpr:
		WalkNode(n.Obj, fn)
		if n.Computed {
			WalkNode(n.Prop, fn)
		}
	case *JsCallExpr:
		WalkNode(n.Callee, fn)
		for _, a := range n.Args {
			WalkNode(a, fn)
		}
	case *JsNewExpr:
		WalkNode(n.Callee, fn)
		for _, a := range n.Args {
			WalkNode(a, fn)
		}
	case *JsConditionalExpr:
		WalkNode(n.Test, fn)
		WalkNode(n.Then, fn)
		WalkNode(n.Else, fn)
	case *JsSeqExpr:
		for _, e := range n.Exprs {
			WalkNode(e, fn)
		}
	case *JsSpreadExpr:
		WalkNode(n.X, fn)
	case *JsAwaitExpr:
		WalkNode(n.X, fn)
	case *JsYieldExpr:
		if n.X != nil {
			WalkNode(n.X, fn)
		}
	case *JsTemplateLit:
		// Template literals are structured — recurse into each ${…}
		// interpolation so identifier collection, scope resolution,
		// and rewriting all see inside them.
		for _, e := range n.Exprs {
			WalkNode(e, fn)
		}
	case *JsTaggedTemplate:
		WalkNode(n.Tag, fn)
		WalkNode(n.Quasi, fn)
		// Leaves: JsIdent, JsLiteral, JsThisExpr, JsSuperExpr,
		// JsImportDecl, JsBreakStmt, JsContinueStmt, JsEmptyStmt, JsDebuggerStmt
	}
}

// CollectIdents returns every identifier name that appears in the subtree
// rooted at node, including identifiers inside template literal expressions.
func CollectIdents(node JsNode) []string {
	seen := map[string]bool{}
	var names []string

	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}

	WalkNode(node, func(n JsNode) bool {
		switch x := n.(type) {
		case *JsIdent:
			add(x.Name)
		}
		return true
	})
	return names
}

// FindDepsInNode returns the subset of vars that appear as identifiers
// somewhere in the AST rooted at node (including inside template literals).
func FindDepsInNode(node JsNode, vars []string) []string {
	varSet := make(map[string]bool, len(vars))
	for _, v := range vars {
		varSet[v] = true
	}

	allIdents := CollectIdents(node)
	seen := map[string]bool{}
	var deps []string
	for _, name := range allIdents {
		if varSet[name] && !seen[name] {
			seen[name] = true
			deps = append(deps, name)
		}
	}
	return deps
}
