package compiler

import (
	"github.com/elauven/vela/internal/analyzer"
	"github.com/elauven/vela/internal/diag"
	"github.com/elauven/vela/internal/jsast"
	"github.com/elauven/vela/internal/sfcast"
)

// Context API (task 090). setContext, getContext and hasContext read and
// write the context frame of the component that is starting, so a call
// that runs later — in an effect, a lifecycle callback, a $derived
// expression that is recomputed, or a template expression or handler — is
// VELA-CONTEXT-OUTSIDE-INIT. Calls inside a function declared in the
// script are not reported: the script may call it while it starts. The
// runtime throws for the calls the compiler cannot see.

// contextFunctions are the context exports of 'vela'.
var contextFunctions = map[string]bool{"setContext": true, "getContext": true, "hasContext": true}

const contextOutsideInitHelp = "Call it in the component script while the component starts, keep the value in a variable, and use the variable here."

// contextAliases returns the local names of the context functions imported
// from 'vela', mapped to the imported name.
func contextAliases(prog *jsast.JsProgram) map[string]string {
	aliases := map[string]string{}
	if prog == nil {
		return aliases
	}
	for _, stmt := range prog.Body {
		imp, ok := stmt.(*jsast.JsImportDecl)
		if !ok || imp.Source != "vela" {
			continue
		}
		for _, spec := range imp.Specifiers {
			if contextFunctions[spec.Imported] {
				aliases[spec.Local] = spec.Imported
			}
		}
	}
	return aliases
}

// reportContextCalls reports every call of a context function inside node.
func reportContextCalls(node jsast.JsNode, aliases map[string]string, where string, offset int, ds *diag.DiagSet) {
	if node == nil {
		return
	}
	jsast.WalkNode(node, func(n jsast.JsNode) bool {
		call, ok := n.(*jsast.JsCallExpr)
		if !ok {
			return true
		}
		callee, ok := call.Callee.(*jsast.JsIdent)
		if !ok {
			return true
		}
		if name, found := aliases[callee.Name]; found {
			ds.Report(diag.SevError, offset+call.NodePos(), offset+call.NodeEnd(), "VELA-CONTEXT-OUTSIDE-INIT", contextOutsideInitHelp,
				"%s() runs %s, after the component started; context is read and written only while a component starts", name, where)
		}
		return true
	})
}

// ValidateContextCalls reports context calls of the script that run after
// initialisation: in $effect, $derived and lifecycle callbacks.
func ValidateContextCalls(prog *jsast.JsProgram, analysis *analyzer.Analysis, ds *diag.DiagSet) {
	aliases := contextAliases(prog)
	if len(aliases) == 0 {
		return
	}
	jsast.WalkNode(prog, func(n jsast.JsNode) bool {
		call, ok := n.(*jsast.JsCallExpr)
		if !ok {
			return true
		}
		name := intrinsicCallName(call.Callee)
		where := ""
		switch name {
		case "$effect", "$effect.pre":
			where = "in an effect"
		case "$derived":
			where = "in a $derived expression, which is recomputed when its inputs change"
		default:
			if ident, isIdent := call.Callee.(*jsast.JsIdent); isIdent && analysis != nil {
				if lifecycle, found := analysis.IntrinsicAliases[ident.Name]; found {
					where = "in the " + lifecycle + " callback"
				}
			}
		}
		if where == "" {
			return true
		}
		for _, arg := range call.Args {
			reportContextCalls(arg, aliases, where, 0, ds)
		}
		return false
	})
}

// ValidateTemplateContextCalls reports context calls in template
// expressions and inline handlers, which run while the template renders
// or later.
func ValidateTemplateContextCalls(nodes []sfcast.Node, prog *jsast.JsProgram, ds *diag.DiagSet) {
	aliases := contextAliases(prog)
	if len(aliases) == 0 {
		return
	}
	check := func(expr *sfcast.TemplateExpr) {
		if expr == nil || expr.Program == nil || expr.Pos < 0 || len(expr.Errors) > 0 {
			return
		}
		reportContextCalls(expr.Program, aliases, "in the template", expr.Pos, ds)
	}
	checkAttrs := func(attrs []*sfcast.Attr) {
		for _, a := range attrs {
			if a != nil {
				check(a.Expr)
			}
		}
	}
	var walk func([]sfcast.Node)
	walk = func(nodes []sfcast.Node) {
		for _, node := range nodes {
			switch n := node.(type) {
			case *sfcast.TextNode:
				for _, part := range n.Parts {
					if !part.Static {
						check(part.Expr)
					}
				}
			case *sfcast.ElementNode:
				checkAttrs(n.Attrs)
				walk(n.Children)
			case *sfcast.ComponentNode:
				checkAttrs(n.Attrs)
				if n.This != nil {
					check(n.This.Expr)
				}
				for _, slot := range n.Slots {
					walk(slot)
				}
			case *sfcast.SlotNode:
				walk(n.Fallback)
			case *sfcast.IfChainNode:
				check(n.KeyAST)
				for _, branch := range n.Branches {
					check(branch.Expr)
					walk(branch.Children)
				}
			case *sfcast.EachBlockNode:
				check(n.IterExpr)
				check(n.KeyAST)
				walk(n.Children)
				walk(n.ElseChildren)
			case *sfcast.AwaitBlockNode:
				check(n.Expr)
				walk(n.PendingChildren)
				walk(n.ThenChildren)
				walk(n.CatchChildren)
			case *sfcast.HeadNode:
				walk(n.Children)
			case *sfcast.BoundaryBlockNode:
				walk(n.Children)
				walk(n.CatchChildren)
			}
		}
	}
	walk(nodes)
}
