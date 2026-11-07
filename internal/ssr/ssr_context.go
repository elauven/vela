package ssr

import (
	goast "go/ast"
	"go/token"

	"github.com/elauven/vela/internal/analyzer"
	"github.com/elauven/vela/internal/jsast"
)

// Component context on the server (task 090, contract §7.5). The renderer
// of a script that calls setContext opens a context frame before its
// prologue (vela.EnterContext) and closes it when it returns; setContext,
// getContext and hasContext translate to methods of the evaluation state
// ssrJS, which reads the frames of the render it belongs to
// (vela.NewJSFor). Functions of local modules use the ssrJS of the
// component that created them, so getContext in a module function called
// by the script reads that component's context. Keys are string literals.

// ssrContextFuncs returns the local names of the context functions
// imported from 'vela'.
func ssrContextFuncs(imports []*analyzer.Import) map[string]string {
	funcs := map[string]string{}
	for _, imp := range imports {
		if imp == nil || imp.Source != "vela" {
			continue
		}
		for _, spec := range imp.Specifiers {
			switch spec.Imported {
			case "setContext", "getContext", "hasContext":
				funcs[spec.Local] = spec.Imported
			}
		}
	}
	return funcs
}

// ssrContextPlan finds the context functions of a component script, the
// top-level statements that set context — a setContext call, or a call of
// a script function that sets context — and whether the script calls
// setContext at all.
func ssrContextPlan(imports []*analyzer.Import, program *jsast.JsProgram, functions map[string]*jsast.JsFuncDecl) (map[string]string, []jsast.JsExpr, bool) {
	funcs := ssrContextFuncs(imports)
	setter := ""
	for local, name := range funcs {
		if name == "setContext" {
			setter = local
		}
	}
	if setter == "" || program == nil {
		return funcs, nil, false
	}
	sets := false
	// setting are the script functions that call setContext, directly or
	// through another script function.
	setting := map[string]bool{}
	callsSetter := func(node jsast.JsNode) bool {
		found := false
		jsast.WalkNode(node, func(n jsast.JsNode) bool {
			call, ok := n.(*jsast.JsCallExpr)
			if !ok || found {
				return !found
			}
			if callee, ok := call.Callee.(*jsast.JsIdent); ok && (callee.Name == setter || setting[callee.Name]) {
				found = true
			}
			return !found
		})
		return found
	}
	for changed := true; changed; {
		changed = false
		for name, function := range functions {
			if function != nil && !setting[name] && callsSetter(function.Body) {
				setting[name] = true
				changed = true
			}
		}
	}
	jsast.WalkNode(program, func(n jsast.JsNode) bool {
		if ident, ok := n.(*jsast.JsIdent); ok && ident.Name == setter {
			sets = true
		}
		return !sets
	})
	var statements []jsast.JsExpr
	for _, stmt := range program.Body {
		expr, ok := stmt.(*jsast.JsExprStmt)
		if !ok || expr.X == nil {
			continue
		}
		if call, ok := expr.X.(*jsast.JsCallExpr); ok {
			if callee, ok := call.Callee.(*jsast.JsIdent); ok && (callee.Name == setter || setting[callee.Name]) {
				statements = append(statements, call)
			}
		}
	}
	return funcs, statements, sets
}

// contextCall translates setContext(key, value), getContext(key) and
// hasContext(key) to the context methods of ssrJS.
func (b *ssrIRBuilder) contextCall(call *jsast.JsCallExpr, pos int) (SSRIRExpr, bool, error) {
	callee, ok := call.Callee.(*jsast.JsIdent)
	if !ok {
		return nil, false, nil
	}
	name, ok := b.ctx.ContextFuncs[callee.Name]
	if !ok || b.shadowed(callee.Name) {
		return nil, false, nil
	}
	if !b.js {
		return nil, true, ssrExprUnsupported(pos, "%s() runs only in the script of a server-rendered component", name)
	}
	arity := 1
	if name == "setContext" {
		arity = 2
	}
	if len(call.Args) != arity {
		return nil, true, ssrExprUnsupported(pos, "%s() takes %d argument(s) during server rendering, got %d", name, arity, len(call.Args))
	}
	key, ok := call.Args[0].(*jsast.JsLiteral)
	if !ok || key.Kind != "string" {
		return nil, true, ssrExprUnsupported(pos, "the key of %s() must be a string literal during server rendering", name)
	}
	text := key.Value
	if !key.Decoded {
		decoded, err := decodeSSRJSStringLiteral(key.Raw)
		if err != nil {
			return nil, true, ssrExprUnsupported(pos, "the key of %s() is not a valid string literal", name)
		}
		text = decoded
	}
	args := []goast.Expr{ssrGoString(text)}
	method, facts := "GetContext", ssrIRFacts{}
	switch name {
	case "setContext":
		value, err := b.build(call.Args[1], pos)
		if err != nil {
			return nil, true, err
		}
		args = append(args, lowerSSRIR(value))
		method = "SetContext"
	case "hasContext":
		method, facts = "HasContext", ssrIRFacts{Bool: true}
	}
	b.usedJS = true
	return &ssrIRGo{ssrIRBase: b.base(pos, facts), Expr: &goast.CallExpr{Fun: goSelector("ssrJS", method), Args: args}}, true, nil
}

// ssrContextFrame opens the context frame of a renderer:
//
//	w = vela.EnterContext(w)
//	defer vela.LeaveContext(w)
func ssrContextFrame() []goast.Stmt {
	w := func() goast.Expr { return goast.NewIdent("w") }
	return []goast.Stmt{
		&goast.AssignStmt{Lhs: []goast.Expr{w()}, Tok: token.ASSIGN, Rhs: []goast.Expr{&goast.CallExpr{Fun: goSelector("vela", "EnterContext"), Args: []goast.Expr{w()}}}},
		&goast.DeferStmt{Call: &goast.CallExpr{Fun: goSelector("vela", "LeaveContext"), Args: []goast.Expr{w()}}},
	}
}
