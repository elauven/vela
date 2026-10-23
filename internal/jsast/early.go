package jsast

import "strings"

// EarlyOptions selects the context a program is checked in.
type EarlyOptions struct {
	// TopLevelIsFunction checks the top level as a function body: a component
	// script runs inside its component factory, so `new.target` is valid
	// there but `await` is not.
	TopLevelIsFunction bool
	// TopLevelAwait admits `await` outside functions (an ES module).
	TopLevelAwait bool
}

// EarlyErrors reports the early errors of ECMAScript that the parser does not
// report itself: break/continue targets, labels, duplicate constructors,
// super() outside a constructor, undeclared private names, regex flags and
// grouping, await/yield outside async functions and generators, and
// new.target outside functions, import and export declarations below the top
// level. A program with such an error does not load,
// so the compiler must not accept it silently (task 055).
func EarlyErrors(prog *JsProgram, opts EarlyOptions) []JSParseDiagnostic {
	if prog == nil {
		return nil
	}
	c := &earlyChecker{}
	top := earlyCtx{inFunc: opts.TopLevelIsFunction, async: opts.TopLevelAwait && !opts.TopLevelIsFunction}
	topLevel := map[JsNode]bool{}
	for _, s := range prog.Body {
		topLevel[s] = true
		c.node(s, top)
	}
	// The parser accepts import and export declarations in any statement
	// position (`label: import …`); they belong to the top level.
	WalkNode(prog, func(n JsNode) bool {
		switch n.(type) {
		case *JsImportDecl:
			if !topLevel[n] {
				c.report(n.NodePos(), "an import declaration may only appear at the top level")
			}
		case *JsExportDecl:
			if !topLevel[n] {
				c.report(n.NodePos(), "an export declaration may only appear at the top level")
			}
		}
		return true
	})
	return c.diags
}

type earlyLabel struct {
	name   string
	isLoop bool
}

type earlyCtx struct {
	inFunc      bool
	async       bool
	generator   bool
	loop        bool
	breakable   bool // loop or switch
	constructor bool
	labels      []earlyLabel
	private     []map[string]bool
}

type earlyChecker struct {
	diags []JSParseDiagnostic
}

func (c *earlyChecker) report(pos int, msg string) {
	c.diags = append(c.diags, JSParseDiagnostic{Pos: pos, Message: msg})
}

// function returns the context of a function body.
func (ctx earlyCtx) function(async, generator, constructor bool) earlyCtx {
	return earlyCtx{inFunc: true, async: async, generator: generator, constructor: constructor, private: ctx.private}
}

// children returns the nodes WalkNode visits directly below n.
func earlyChildren(n JsNode) []JsNode {
	var out []JsNode
	WalkNode(n, func(child JsNode) bool {
		if child == n {
			return true
		}
		out = append(out, child)
		return false
	})
	return out
}

func isLoopStmt(s JsStmt) bool {
	switch x := s.(type) {
	case *JsForStmt, *JsForInStmt, *JsWhileStmt, *JsDoWhileStmt:
		return true
	case *JsLabeledStmt:
		return isLoopStmt(x.Body)
	}
	return false
}

func (c *earlyChecker) node(n JsNode, ctx earlyCtx) {
	switch x := n.(type) {
	case nil:
		return
	case *JsFuncDecl:
		inner := ctx.function(x.Async, x.Generator, false)
		c.params(x.Params, inner)
		c.node(x.Body, inner)
		return
	case *JsFuncExpr:
		inner := ctx.function(x.Async, x.Generator, false)
		c.params(x.Params, inner)
		c.node(x.Body, inner)
		return
	case *JsArrowFunc:
		// Arrows keep super() and new.target of their context but not
		// loops, labels or yield.
		inner := ctx.function(x.Async, false, ctx.constructor)
		inner.inFunc = ctx.inFunc
		c.params(x.Params, inner)
		c.node(x.Body, inner)
		return
	case *JsClassDecl:
		c.class(x.SuperClass, x.Members, ctx)
		return
	case *JsClassExpr:
		c.class(x.SuperClass, x.Members, ctx)
		return
	case *JsForStmt, *JsWhileStmt, *JsDoWhileStmt:
		inner := ctx
		inner.loop, inner.breakable = true, true
		for _, child := range earlyChildren(n) {
			c.node(child, inner)
		}
		return
	case *JsForInStmt:
		if x.Await && !ctx.async {
			c.report(x.Pos, "for await is only valid in async functions")
		}
		inner := ctx
		inner.loop, inner.breakable = true, true
		for _, child := range earlyChildren(n) {
			c.node(child, inner)
		}
		return
	case *JsSwitchStmt:
		c.node(x.Disc, ctx)
		inner := ctx
		inner.breakable = true
		for _, cs := range x.Cases {
			c.node(cs.Test, ctx)
			for _, s := range cs.Body {
				c.node(s, inner)
			}
		}
		return
	case *JsLabeledStmt:
		inner := ctx
		inner.labels = append(append([]earlyLabel(nil), ctx.labels...), earlyLabel{name: x.Label, isLoop: isLoopStmt(x.Body)})
		c.node(x.Body, inner)
		return
	case *JsBreakStmt:
		if x.Label != "" {
			if !ctx.hasLabel(x.Label, false) {
				c.report(x.Pos, "undefined label '"+x.Label+"'")
			}
		} else if !ctx.breakable {
			c.report(x.Pos, "illegal break statement")
		}
		return
	case *JsContinueStmt:
		if x.Label != "" {
			if !ctx.hasLabel(x.Label, true) {
				c.report(x.Pos, "undefined label '"+x.Label+"'")
			}
		} else if !ctx.loop {
			c.report(x.Pos, "illegal continue statement: no surrounding iteration statement")
		}
		return
	case *JsAwaitExpr:
		if !ctx.async {
			c.report(x.Pos, "await is only valid in async functions and the top level of modules")
		}
	case *JsYieldExpr:
		if !ctx.generator {
			c.report(x.Pos, "yield is only valid in generator functions")
		}
	case *JsCallExpr:
		if _, ok := x.Callee.(*JsSuperExpr); ok && !ctx.constructor {
			c.report(x.Pos, "'super' keyword unexpected here")
		}
	case *JsMemberExpr:
		if obj, ok := x.Obj.(*JsIdent); ok && !x.Computed {
			if prop, ok := x.Prop.(*JsIdent); ok && obj.Name == "new" && prop.Name == "target" && !ctx.inFunc {
				c.report(x.Pos, "new.target expression is not allowed here")
			}
		}
		if prop, ok := x.Prop.(*JsIdent); ok && !x.Computed {
			c.privateName(prop, ctx)
		}
	case *JsBinaryExpr:
		if left, ok := x.L.(*JsIdent); ok && x.Op == "in" {
			c.privateName(left, ctx)
		}
	case *JsLiteral:
		if x.Kind == "regex" {
			if msg := regexEarlyError(x.Raw); msg != "" {
				c.report(x.Pos, msg)
			}
		}
	}
	for _, child := range earlyChildren(n) {
		c.node(child, ctx)
	}
}

func (c *earlyChecker) params(params []JsExpr, ctx earlyCtx) {
	for _, p := range params {
		c.node(p, ctx)
	}
}

func (ctx earlyCtx) hasLabel(name string, needLoop bool) bool {
	for _, l := range ctx.labels {
		if l.name == name {
			return !needLoop || l.isLoop
		}
	}
	return false
}

func (c *earlyChecker) privateName(id *JsIdent, ctx earlyCtx) {
	if !strings.HasPrefix(id.Name, "#") {
		return
	}
	for i := len(ctx.private) - 1; i >= 0; i-- {
		if ctx.private[i][id.Name] {
			return
		}
	}
	c.report(id.Pos, "private field '"+id.Name+"' must be declared in an enclosing class")
}

func (c *earlyChecker) class(superClass JsExpr, members []*JsClassMember, ctx earlyCtx) {
	if superClass != nil {
		c.node(superClass, ctx)
	}
	declared := map[string]bool{}
	constructors := 0
	for _, m := range members {
		if key, ok := m.Key.(*JsIdent); ok && !m.Computed {
			if strings.HasPrefix(key.Name, "#") {
				declared[key.Name] = true
			}
			if key.Name == "constructor" && !m.Static && m.Kind == "method" {
				constructors++
				if constructors == 2 {
					c.report(m.Pos, "a class may only have one constructor")
				}
			}
		}
	}
	inner := ctx
	inner.private = append(append([]map[string]bool(nil), ctx.private...), declared)
	for _, m := range members {
		if m.Computed {
			c.node(m.Key, ctx)
		}
		isConstructor := false
		if key, ok := m.Key.(*JsIdent); ok && !m.Computed && key.Name == "constructor" && !m.Static {
			isConstructor = superClass != nil
		}
		switch value := m.Value.(type) {
		case *JsFuncExpr:
			body := inner.function(value.Async, value.Generator, isConstructor)
			c.params(value.Params, body)
			c.node(value.Body, body)
		case nil:
		default:
			// A field initializer runs in an implicit method.
			c.node(value, inner.function(false, false, false))
		}
		if m.Body != nil {
			c.node(m.Body, inner.function(false, false, false))
		}
	}
}

// regexEarlyError checks the flags of a regex literal and the balance of its
// groups and classes; it reports only definite errors.
func regexEarlyError(raw string) string {
	end := strings.LastIndexByte(raw, '/')
	if len(raw) < 2 || raw[0] != '/' || end <= 0 {
		return ""
	}
	body, flags := raw[1:end], raw[end+1:]
	seen := map[rune]bool{}
	for _, f := range flags {
		if !strings.ContainsRune("dgimsuyv", f) {
			return "invalid regular expression flags"
		}
		if seen[f] {
			return "invalid regular expression flags"
		}
		seen[f] = true
	}
	if seen['u'] && seen['v'] {
		return "invalid regular expression flags"
	}
	depth := 0
	inClass := false
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '\\':
			i++
		case '[':
			inClass = true
		case ']':
			inClass = false
		case '(':
			if !inClass {
				depth++
			}
		case ')':
			if !inClass {
				depth--
				if depth < 0 {
					return "invalid regular expression: unmatched ')'"
				}
			}
		}
	}
	if depth > 0 {
		return "invalid regular expression: unterminated group"
	}
	if inClass {
		return "invalid regular expression: unterminated character class"
	}
	return ""
}
