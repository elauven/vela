package main

import (
	"fmt"
	goast "go/ast"
	"go/token"
)

// Render-time initialization (docs/SSR_TRANSLATION_CONTRACT.md T-6.6d,
// task 044). After the prop defaults, the renderer evaluates the local
// bindings it needs once, in script declaration order, into Go locals
// ssrL_<name>; every site reads the local. Under the block engine the
// payload stream also needs every $state value, so all of them are
// evaluated; one whose initializer cannot be translated is omitted from
// the stream instead of failing the render.

// ssrInitPlan is the outcome of the prologue: its statements and the
// bindings that hold a value.
type ssrInitPlan struct {
	statements []goast.Stmt
	evaluated  map[string]bool
}

// localsPrologue lowers the needed local bindings in declaration order.
// needed are the bindings read by render sites; extra are evaluated when
// possible (payload state).
func (e *ssrGoEmitter) localsPrologue(plan *SSRRenderPlan, extra []string) ssrInitPlan {
	needed := map[string]bool{}
	if e.region != nil {
		for _, record := range e.region.Records {
			if !record.Required {
				continue
			}
			for _, name := range record.Reads {
				needed[name] = true
			}
		}
	}
	optional := map[string]bool{}
	for _, name := range extra {
		if !needed[name] {
			optional[name] = true
		}
	}
	// A payload-only binding needs the bindings its initializer reads.
	base := ssrComponentContext(NewSSRExprContext(plan))
	initializer := func(name string) JsExpr {
		for _, initializers := range []map[string]JsExpr{plan.StateInitializers, plan.DeepInitializers, plan.DerivedInitializers, plan.PlainInitializers} {
			if init, ok := initializers[name]; ok {
				return init
			}
		}
		return nil
	}
	out := ssrInitPlan{evaluated: map[string]bool{}}
	throws := false
	for _, name := range plan.LocalOrder {
		if !needed[name] && !optional[name] {
			continue
		}
		init := initializer(name)
		if init == nil {
			continue
		}
		builder := newSSRIRBuilder(base)
		builder.locals, builder.current = true, name
		source := -1
		if e.region != nil {
			builder.js = true
			source = e.region.ScriptOffset + init.NodePos()
			builder.site = e.region.siteLabel(source)
		}
		var ir SSRIRExpr
		var err error
		if binding, ok := plan.Hosts[name]; ok && e.region != nil && plan.PlainInitializers[name] != nil {
			ir, err = builder.hostCall(binding, &JsCallExpr{Callee: &JsIdent{Name: name}}, fmt.Sprintf("value %q", name), 0)
		} else {
			ir, err = builder.build(init, 0)
		}
		if e.region != nil {
			e.region.absorb(builder)
		}
		if err == nil {
			for read := range builder.reads {
				if !out.evaluated[read] {
					// A binding it reads failed or is payload-only and missing.
					err = ssrExprUnsupported(0, "%q reads %q, which has no server value", name, read)
				}
			}
		}
		if err != nil {
			// A render site that reads this binding already reports the failure.
			continue
		}
		if builder.usedJS && e.region != nil {
			e.region.usesJS = true
		}
		throws = throws || builder.throws
		local := goast.NewIdent(ssrLocalName(name))
		value := lowerSSRIR(ir)
		declare := goast.Stmt(&goast.AssignStmt{Lhs: []goast.Expr{local}, Tok: token.DEFINE, Rhs: []goast.Expr{value}})
		if ident, ok := value.(*goast.Ident); ok && ident.Name == "nil" {
			// null has no Go type of its own.
			declare = &goast.DeclStmt{Decl: &goast.GenDecl{Tok: token.VAR, Specs: []goast.Spec{&goast.ValueSpec{Names: []*goast.Ident{local}, Type: goast.NewIdent("any"), Values: []goast.Expr{value}}}}}
		}
		out.statements = append(out.statements,
			declare,
			&goast.AssignStmt{Lhs: []goast.Expr{goast.NewIdent("_")}, Tok: token.ASSIGN, Rhs: []goast.Expr{goast.NewIdent(ssrLocalName(name))}},
		)
		out.evaluated[name] = true
	}
	if throws {
		out.statements = append(out.statements, ssrJSCheck())
	}
	return out
}
