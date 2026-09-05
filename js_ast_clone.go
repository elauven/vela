package main

// cloneJsExpr copies parser-owned expression AST before a lowering pass adds
// generated identity bindings or source-map metadata. Template expressions
// can be emitted from several structural paths, so mutation must remain local
// to one emission site.
func cloneJsExpr(expr JsExpr) JsExpr {
	if expr == nil {
		return nil
	}
	switch node := expr.(type) {
	case *JsIdent:
		out := *node
		return &out
	case *JsLiteral:
		out := *node
		return &out
	case *JsThisExpr:
		out := *node
		return &out
	case *JsSuperExpr:
		out := *node
		return &out
	case *JsTemplateLit:
		out := *node
		out.Quasis = append([]string(nil), node.Quasis...)
		out.Exprs = cloneJsExprs(node.Exprs)
		return &out
	case *JsArrayExpr:
		out := *node
		out.Elems = cloneJsExprs(node.Elems)
		return &out
	case *JsObjectExpr:
		out := *node
		out.Props = make([]*JsProp, len(node.Props))
		for i, prop := range node.Props {
			out.Props[i] = cloneJsProp(prop)
		}
		return &out
	case *JsFuncExpr:
		out := *node
		out.Params = cloneJsExprs(node.Params)
		out.Body = cloneJsBlock(node.Body)
		return &out
	case *JsArrowFunc:
		out := *node
		out.Params = cloneJsExprs(node.Params)
		out.Body = cloneJsNode(node.Body)
		return &out
	case *JsUnaryExpr:
		out := *node
		out.X = cloneJsExpr(node.X)
		return &out
	case *JsBinaryExpr:
		out := *node
		out.L, out.R = cloneJsExpr(node.L), cloneJsExpr(node.R)
		return &out
	case *JsLogicalExpr:
		out := *node
		out.L, out.R = cloneJsExpr(node.L), cloneJsExpr(node.R)
		return &out
	case *JsAssignExpr:
		out := *node
		out.L, out.R = cloneJsExpr(node.L), cloneJsExpr(node.R)
		return &out
	case *JsMemberExpr:
		out := *node
		out.Obj, out.Prop = cloneJsExpr(node.Obj), cloneJsExpr(node.Prop)
		return &out
	case *JsCallExpr:
		out := *node
		out.Callee, out.Args = cloneJsExpr(node.Callee), cloneJsExprs(node.Args)
		return &out
	case *JsNewExpr:
		out := *node
		out.Callee, out.Args = cloneJsExpr(node.Callee), cloneJsExprs(node.Args)
		return &out
	case *JsConditionalExpr:
		out := *node
		out.Test, out.Then, out.Else = cloneJsExpr(node.Test), cloneJsExpr(node.Then), cloneJsExpr(node.Else)
		return &out
	case *JsSeqExpr:
		out := *node
		out.Exprs = cloneJsExprs(node.Exprs)
		return &out
	case *JsSpreadExpr:
		out := *node
		out.X = cloneJsExpr(node.X)
		return &out
	case *JsAwaitExpr:
		out := *node
		out.X = cloneJsExpr(node.X)
		return &out
	case *JsYieldExpr:
		out := *node
		out.X = cloneJsExpr(node.X)
		return &out
	default:
		panic("vela: unsupported JS expression clone")
	}
}
func cloneJsExprs(in []JsExpr) []JsExpr {
	out := make([]JsExpr, len(in))
	for i, value := range in {
		out[i] = cloneJsExpr(value)
	}
	return out
}
func cloneJsProp(prop *JsProp) *JsProp {
	if prop == nil {
		return nil
	}
	out := *prop
	out.Key, out.Value = cloneJsExpr(prop.Key), cloneJsExpr(prop.Value)
	return &out
}
func cloneJsBlock(block *JsBlock) *JsBlock {
	if block == nil {
		return nil
	}
	out := *block
	out.Body = cloneJsStmts(block.Body)
	return &out
}
func cloneJsStmts(in []JsStmt) []JsStmt {
	out := make([]JsStmt, len(in))
	for i, stmt := range in {
		out[i] = cloneJsStmt(stmt)
	}
	return out
}
func cloneJsNode(node JsNode) JsNode {
	if node == nil {
		return nil
	}
	if expr, ok := node.(JsExpr); ok {
		return cloneJsExpr(expr)
	}
	if stmt, ok := node.(JsStmt); ok {
		return cloneJsStmt(stmt)
	}
	panic("vela: unsupported JS node clone")
}
func cloneJsStmt(stmt JsStmt) JsStmt {
	if stmt == nil {
		return nil
	}
	switch node := stmt.(type) {
	case *JsBlock:
		return cloneJsBlock(node)
	case *JsExprStmt:
		out := *node
		out.X = cloneJsExpr(node.X)
		return &out
	case *JsVarDecl:
		out := *node
		out.Decls = make([]*JsVarDeclarator, len(node.Decls))
		for i, decl := range node.Decls {
			if decl != nil {
				copy := *decl
				copy.Id, copy.Init = cloneJsExpr(decl.Id), cloneJsExpr(decl.Init)
				out.Decls[i] = &copy
			}
		}
		return &out
	case *JsIfStmt:
		out := *node
		out.Test, out.Then, out.Else = cloneJsExpr(node.Test), cloneJsStmt(node.Then), cloneJsStmt(node.Else)
		return &out
	case *JsForStmt:
		out := *node
		out.Init, out.Test, out.Update, out.Body = cloneJsStmt(node.Init), cloneJsExpr(node.Test), cloneJsExpr(node.Update), cloneJsStmt(node.Body)
		return &out
	case *JsForInStmt:
		out := *node
		out.Left, out.Right, out.Body = cloneJsStmt(node.Left), cloneJsExpr(node.Right), cloneJsStmt(node.Body)
		return &out
	case *JsWhileStmt:
		out := *node
		out.Test, out.Body = cloneJsExpr(node.Test), cloneJsStmt(node.Body)
		return &out
	case *JsDoWhileStmt:
		out := *node
		out.Body, out.Test = cloneJsStmt(node.Body), cloneJsExpr(node.Test)
		return &out
	case *JsReturnStmt:
		out := *node
		out.Arg = cloneJsExpr(node.Arg)
		return &out
	case *JsThrowStmt:
		out := *node
		out.Arg = cloneJsExpr(node.Arg)
		return &out
	case *JsFuncDecl:
		out := *node
		out.Params, out.Body = cloneJsExprs(node.Params), cloneJsBlock(node.Body)
		return &out
	case *JsEmptyStmt:
		out := *node
		return &out
	case *JsBreakStmt:
		out := *node
		return &out
	case *JsContinueStmt:
		out := *node
		return &out
	default:
		panic("vela: unsupported JS statement clone")
	}
}
