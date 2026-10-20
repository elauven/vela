package codegen

import (
	"github.com/elauven/vela/internal/jsast"
)

// cloneJsExpr copies parser-owned expression AST before a lowering pass adds
// generated identity bindings or source-map metadata. Template expressions
// can be emitted from several structural paths, so mutation must remain local
// to one emission site.
func cloneJsExpr(expr jsast.JsExpr) jsast.JsExpr {
	if expr == nil {
		return nil
	}
	switch node := expr.(type) {
	case *jsast.JsIdent:
		out := *node
		return &out
	case *jsast.JsLiteral:
		out := *node
		return &out
	case *jsast.JsThisExpr:
		out := *node
		return &out
	case *jsast.JsSuperExpr:
		out := *node
		return &out
	case *jsast.JsTemplateLit:
		out := *node
		out.Quasis = append([]string(nil), node.Quasis...)
		out.Exprs = cloneJsExprs(node.Exprs)
		return &out
	case *jsast.JsArrayExpr:
		out := *node
		out.Elems = cloneJsExprs(node.Elems)
		return &out
	case *jsast.JsObjectExpr:
		out := *node
		out.Props = make([]*jsast.JsProp, len(node.Props))
		for i, prop := range node.Props {
			out.Props[i] = cloneJsProp(prop)
		}
		return &out
	case *jsast.JsFuncExpr:
		out := *node
		out.Params = cloneJsExprs(node.Params)
		out.Body = cloneJsBlock(node.Body)
		return &out
	case *jsast.JsArrowFunc:
		out := *node
		out.Params = cloneJsExprs(node.Params)
		out.Body = cloneJsNode(node.Body)
		return &out
	case *jsast.JsUnaryExpr:
		out := *node
		out.X = cloneJsExpr(node.X)
		return &out
	case *jsast.JsBinaryExpr:
		out := *node
		out.L, out.R = cloneJsExpr(node.L), cloneJsExpr(node.R)
		return &out
	case *jsast.JsLogicalExpr:
		out := *node
		out.L, out.R = cloneJsExpr(node.L), cloneJsExpr(node.R)
		return &out
	case *jsast.JsAssignExpr:
		out := *node
		out.L, out.R = cloneJsExpr(node.L), cloneJsExpr(node.R)
		return &out
	case *jsast.JsMemberExpr:
		out := *node
		out.Obj, out.Prop = cloneJsExpr(node.Obj), cloneJsExpr(node.Prop)
		return &out
	case *jsast.JsCallExpr:
		out := *node
		out.Callee, out.Args = cloneJsExpr(node.Callee), cloneJsExprs(node.Args)
		return &out
	case *jsast.JsNewExpr:
		out := *node
		out.Callee, out.Args = cloneJsExpr(node.Callee), cloneJsExprs(node.Args)
		return &out
	case *jsast.JsConditionalExpr:
		out := *node
		out.Test, out.Then, out.Else = cloneJsExpr(node.Test), cloneJsExpr(node.Then), cloneJsExpr(node.Else)
		return &out
	case *jsast.JsSeqExpr:
		out := *node
		out.Exprs = cloneJsExprs(node.Exprs)
		return &out
	case *jsast.JsSpreadExpr:
		out := *node
		out.X = cloneJsExpr(node.X)
		return &out
	case *jsast.JsAwaitExpr:
		out := *node
		out.X = cloneJsExpr(node.X)
		return &out
	case *jsast.JsYieldExpr:
		out := *node
		out.X = cloneJsExpr(node.X)
		return &out
	case *jsast.JsTaggedTemplate:
		out := *node
		out.Tag = cloneJsExpr(node.Tag)
		if quasi, ok := cloneJsExpr(node.Quasi).(*jsast.JsTemplateLit); ok {
			out.Quasi = quasi
		}
		return &out
	case *jsast.JsClassExpr:
		out := *node
		out.SuperClass = cloneJsExpr(node.SuperClass)
		out.Members = cloneJsClassMembers(node.Members)
		return &out
	default:
		panic("vela: unsupported JS expression clone")
	}
}

func cloneJsClassMembers(in []*jsast.JsClassMember) []*jsast.JsClassMember {
	if in == nil {
		return nil
	}
	out := make([]*jsast.JsClassMember, len(in))
	for i, m := range in {
		copied := *m
		copied.Key = cloneJsExpr(m.Key)
		copied.Value = cloneJsExpr(m.Value)
		copied.Body = cloneJsBlock(m.Body)
		out[i] = &copied
	}
	return out
}
func cloneJsExprs(in []jsast.JsExpr) []jsast.JsExpr {
	out := make([]jsast.JsExpr, len(in))
	for i, value := range in {
		out[i] = cloneJsExpr(value)
	}
	return out
}
func cloneJsProp(prop *jsast.JsProp) *jsast.JsProp {
	if prop == nil {
		return nil
	}
	out := *prop
	out.Key, out.Value = cloneJsExpr(prop.Key), cloneJsExpr(prop.Value)
	return &out
}
func cloneJsBlock(block *jsast.JsBlock) *jsast.JsBlock {
	if block == nil {
		return nil
	}
	out := *block
	out.Body = cloneJsStmts(block.Body)
	return &out
}
func cloneJsStmts(in []jsast.JsStmt) []jsast.JsStmt {
	out := make([]jsast.JsStmt, len(in))
	for i, stmt := range in {
		out[i] = cloneJsStmt(stmt)
	}
	return out
}
func cloneJsNode(node jsast.JsNode) jsast.JsNode {
	if node == nil {
		return nil
	}
	if expr, ok := node.(jsast.JsExpr); ok {
		return cloneJsExpr(expr)
	}
	if stmt, ok := node.(jsast.JsStmt); ok {
		return cloneJsStmt(stmt)
	}
	panic("vela: unsupported JS node clone")
}
func cloneJsStmt(stmt jsast.JsStmt) jsast.JsStmt {
	if stmt == nil {
		return nil
	}
	switch node := stmt.(type) {
	case *jsast.JsBlock:
		return cloneJsBlock(node)
	case *jsast.JsExprStmt:
		out := *node
		out.X = cloneJsExpr(node.X)
		return &out
	case *jsast.JsVarDecl:
		out := *node
		out.Decls = make([]*jsast.JsVarDeclarator, len(node.Decls))
		for i, decl := range node.Decls {
			if decl != nil {
				copy := *decl
				copy.Id, copy.Init = cloneJsExpr(decl.Id), cloneJsExpr(decl.Init)
				out.Decls[i] = &copy
			}
		}
		return &out
	case *jsast.JsIfStmt:
		out := *node
		out.Test, out.Then, out.Else = cloneJsExpr(node.Test), cloneJsStmt(node.Then), cloneJsStmt(node.Else)
		return &out
	case *jsast.JsForStmt:
		out := *node
		out.Init, out.Test, out.Update, out.Body = cloneJsStmt(node.Init), cloneJsExpr(node.Test), cloneJsExpr(node.Update), cloneJsStmt(node.Body)
		return &out
	case *jsast.JsForInStmt:
		out := *node
		out.Left, out.Right, out.Body = cloneJsStmt(node.Left), cloneJsExpr(node.Right), cloneJsStmt(node.Body)
		return &out
	case *jsast.JsWhileStmt:
		out := *node
		out.Test, out.Body = cloneJsExpr(node.Test), cloneJsStmt(node.Body)
		return &out
	case *jsast.JsDoWhileStmt:
		out := *node
		out.Body, out.Test = cloneJsStmt(node.Body), cloneJsExpr(node.Test)
		return &out
	case *jsast.JsReturnStmt:
		out := *node
		out.Arg = cloneJsExpr(node.Arg)
		return &out
	case *jsast.JsThrowStmt:
		out := *node
		out.Arg = cloneJsExpr(node.Arg)
		return &out
	case *jsast.JsFuncDecl:
		out := *node
		out.Params, out.Body = cloneJsExprs(node.Params), cloneJsBlock(node.Body)
		return &out
	case *jsast.JsEmptyStmt:
		out := *node
		return &out
	case *jsast.JsBreakStmt:
		out := *node
		return &out
	case *jsast.JsContinueStmt:
		out := *node
		return &out
	case *jsast.JsTryStmt:
		out := *node
		out.Block, out.Handler, out.Finalizer = cloneJsBlock(node.Block), cloneJsBlock(node.Handler), cloneJsBlock(node.Finalizer)
		out.CatchParam = cloneJsExpr(node.CatchParam)
		return &out
	case *jsast.JsSwitchStmt:
		out := *node
		out.Disc = cloneJsExpr(node.Disc)
		out.Cases = make([]*jsast.JsSwitchCase, len(node.Cases))
		for i, c := range node.Cases {
			copied := *c
			copied.Test = cloneJsExpr(c.Test)
			copied.Body = cloneJsStmts(c.Body)
			out.Cases[i] = &copied
		}
		return &out
	case *jsast.JsLabeledStmt:
		out := *node
		out.Body = cloneJsStmt(node.Body)
		return &out
	case *jsast.JsDebuggerStmt:
		out := *node
		return &out
	case *jsast.JsClassDecl:
		out := *node
		out.SuperClass = cloneJsExpr(node.SuperClass)
		out.Members = cloneJsClassMembers(node.Members)
		return &out
	default:
		panic("vela: unsupported JS statement clone")
	}
}
