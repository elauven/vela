package main

import (
	"fmt"
	goast "go/ast"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

// Translated functions (docs/SSR_TRANSLATION_CONTRACT.md §7.1, §7.2,
// task 045). A top-level function of a component script with more than one
// return expression is translated as a Go closure ssrF_<name> of type
// vela.Func in the render function (T-7.1a). Its locals are Go variables
// of type any; JavaScript exceptions use the render's evaluation state,
// with a check after every statement that can throw (T-6.5a, T-6.5d).
// One dynamic-tier variant is generated per function; monomorphised typed
// variants (T-7.2a) are an optimisation not done yet.

// ssrFuncTable compiles the functions of one component on first use.
type ssrFuncTable struct {
	plan     *SSRRenderPlan
	region   *SSRRegion
	base     SSRExprContext
	order    []string
	compiled map[string]*goast.FuncLit
	failed   map[string]error
	building map[string]bool
	// captured lists the component locals that function bodies read: they
	// are declared up front as any and read without type facts.
	captured map[string]bool
	reads    map[string]bool
	// context is the context.Context of the render in generated code; nil
	// means vela.ContextOf(w) in a renderer.
	context goast.Expr
}

func newSSRFuncTable(plan *SSRRenderPlan, region *SSRRegion) *ssrFuncTable {
	table := &ssrFuncTable{plan: plan, region: region, compiled: map[string]*goast.FuncLit{}, failed: map[string]error{},
		building: map[string]bool{}, captured: map[string]bool{}, reads: map[string]bool{}}
	locals := map[string]bool{}
	for _, name := range plan.LocalOrder {
		locals[name] = true
	}
	// Locals read by compiled functions, directly or through the inlined
	// functions they call.
	functions := ssrComponentFunctions(plan)
	visited := map[string]bool{}
	var visit func(function *JsFuncDecl)
	visit = func(function *JsFuncDecl) {
		ssrWalkIdents(function.Body, func(ident string) {
			if locals[ident] {
				table.captured[ident] = true
			}
			if callee := functions[ident]; callee != nil && !visited[ident] {
				visited[ident] = true
				visit(callee)
			}
		})
	}
	for name, function := range functions {
		if (!ssrInlinable(function) || plan.Functions[name] == nil) && !visited[name] {
			visited[name] = true
			visit(function)
		}
	}
	return table
}

// ssrComponentFunctions returns the top-level functions of the component
// script: function declarations and const arrow or function expressions.
func ssrComponentFunctions(plan *SSRRenderPlan) map[string]*JsFuncDecl {
	out := map[string]*JsFuncDecl{}
	for name, function := range plan.Functions {
		out[name] = function
	}
	for name, initializer := range plan.PlainInitializers {
		if function := ssrFunctionOf(name, initializer); function != nil {
			out[name] = function
		}
	}
	return out
}

// ssrFunctionOf turns an arrow or function expression into a declaration.
func ssrFunctionOf(name string, expr JsExpr) *JsFuncDecl {
	switch fn := expr.(type) {
	case *JsArrowFunc:
		decl := &JsFuncDecl{Name: name, Params: fn.Params, Async: fn.Async}
		decl.BaseStmt = BaseStmt{BaseNode: fn.BaseExpr.BaseNode}
		switch body := fn.Body.(type) {
		case *JsBlock:
			decl.Body = body
		case JsExpr:
			decl.Body = &JsBlock{Body: []JsStmt{&JsReturnStmt{Arg: body}}}
		}
		return decl
	case *JsFuncExpr:
		decl := &JsFuncDecl{Name: name, Params: fn.Params, Body: fn.Body, Async: fn.Async, Generator: fn.Generator}
		decl.BaseStmt = BaseStmt{BaseNode: fn.BaseExpr.BaseNode}
		return decl
	}
	return nil
}

// ssrInlinable reports a function the single-return inliner handles.
func ssrInlinable(function *JsFuncDecl) bool {
	if function == nil || function.Async || function.Generator || function.Body == nil || len(function.Body.Body) != 1 {
		return false
	}
	result, ok := function.Body.Body[0].(*JsReturnStmt)
	if !ok || result.Arg == nil {
		return false
	}
	for _, param := range function.Params {
		if identName(param) == "" {
			return false
		}
	}
	return true
}

// ssrWalkIdents visits every identifier read in a statement tree.
func ssrWalkIdents(node JsNode, visit func(string)) {
	WalkNode(node, func(n JsNode) bool {
		if ident, ok := n.(*JsIdent); ok {
			visit(ident.Name)
		}
		return true
	})
}

// compile translates function name, or returns why it cannot be.
func (t *ssrFuncTable) compile(name string) error {
	if _, done := t.compiled[name]; done {
		return nil
	}
	if err, failed := t.failed[name]; failed {
		return err
	}
	if t.building[name] {
		return nil // recursion: the variable is declared before any body
	}
	function := ssrComponentFunctions(t.plan)[name]
	if function == nil {
		return ssrExprUnsupported(0, "%q is not a function of the component script", name)
	}
	t.building[name] = true
	defer delete(t.building, name)
	literal, err := (&ssrFuncCompiler{table: t, name: name, next: new(int)}).function(function)
	if err != nil {
		t.failed[name] = err
		return err
	}
	t.compiled[name] = literal
	t.order = append(t.order, name)
	return nil
}

// declarations returns the statements that declare the compiled functions
// and the captured locals, before the prop defaults.
func (t *ssrFuncTable) declarations() []goast.Stmt {
	if t == nil || len(t.compiled) == 0 {
		return nil
	}
	var out []goast.Stmt
	captured := make([]string, 0, len(t.captured))
	for name := range t.captured {
		captured = append(captured, name)
	}
	sort.Strings(captured)
	for _, name := range captured {
		out = append(out, ssrVarAny(ssrLocalName(name), nil), ssrUse(ssrLocalName(name)))
	}
	names := append([]string{}, t.order...)
	sort.Strings(names)
	for _, name := range names {
		out = append(out, &goast.DeclStmt{Decl: &goast.GenDecl{Tok: token.VAR, Specs: []goast.Spec{&goast.ValueSpec{
			Names: []*goast.Ident{goast.NewIdent(ssrFuncName(name))}, Type: goSelector("vela", "Func"),
		}}}})
	}
	for _, name := range names {
		out = append(out, &goast.AssignStmt{Lhs: []goast.Expr{goast.NewIdent(ssrFuncName(name))}, Tok: token.ASSIGN, Rhs: []goast.Expr{t.compiled[name]}}, ssrUse(ssrFuncName(name)))
	}
	return out
}

func ssrFuncName(name string) string { return "ssrF_" + name }

func ssrVarAny(name string, value goast.Expr) goast.Stmt {
	spec := &goast.ValueSpec{Names: []*goast.Ident{goast.NewIdent(name)}, Type: goast.NewIdent("any")}
	if value != nil {
		spec.Values = []goast.Expr{value}
	}
	return &goast.DeclStmt{Decl: &goast.GenDecl{Tok: token.VAR, Specs: []goast.Spec{spec}}}
}

func ssrUse(name string) goast.Stmt {
	return &goast.AssignStmt{Lhs: []goast.Expr{goast.NewIdent("_")}, Tok: token.ASSIGN, Rhs: []goast.Expr{goast.NewIdent(name)}}
}

// ── Function bodies ──────────────────────────────────────────────────────

type ssrFuncCompiler struct {
	table *ssrFuncTable
	name  string
	site  string
	// async marks the body of an async function, where await is admitted;
	// ctx, when set, is the template context of a function value created
	// in a template expression.
	async bool
	ctx   *SSRExprContext
	// deferImpure makes a write to state, props, or module bindings a run-time
	// error instead of a build error: a function value created in a template
	// (an event callback passed as a prop) is impure only if the render
	// calls it (T-6.6a).
	deferImpure bool
	// scopes maps JavaScript names to Go variables, innermost last.
	scopes []map[string]string
	next   *int
	// tries counts enclosing try parts, whose statements are closures that
	// complete with (value, returned); loops counts loops entered inside
	// the innermost try part.
	tries int
	loops []int
}

func (c *ssrFuncCompiler) fail(format string, args ...any) error {
	return ssrExprUnsupported(0, format, args...)
}

func (c *ssrFuncCompiler) declare(name string) string {
	*c.next++
	// Compiler temporaries are named $… in JavaScript terms; Go identifiers
	// cannot contain $.
	goName := "ssrV_" + strings.ReplaceAll(name, "$", "tmp_") + "_" + strconv.Itoa(*c.next)
	c.scopes[len(c.scopes)-1][name] = goName
	return goName
}

func (c *ssrFuncCompiler) lookup(name string) (string, bool) {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if goName, ok := c.scopes[i][name]; ok {
			return goName, true
		}
	}
	return "", false
}

// builder returns an expression builder that sees the function's locals.
func (c *ssrFuncCompiler) builder() *ssrIRBuilder {
	t := c.table
	base := ssrComponentContext(t.base)
	if c.ctx != nil {
		base = *c.ctx
	}
	b := newSSRIRBuilder(base)
	b.js, b.locals, b.site = true, true, c.site
	b.params = map[string]SSRIRExpr{}
	for _, scope := range c.scopes {
		for name, goName := range scope {
			b.params[name] = &ssrIRGo{Expr: goast.NewIdent(goName)}
		}
	}
	b.funcCompiler = c
	return b
}

// expr builds and lowers an expression; throws reports whether it can
// throw.
func (c *ssrFuncCompiler) expr(expr JsExpr) (goast.Expr, bool, error) {
	b := c.builder()
	ir, err := b.build(expr, 0)
	c.absorb(b)
	if err != nil {
		return nil, false, err
	}
	return lowerSSRIR(ir), b.throws, nil
}

func (c *ssrFuncCompiler) absorb(b *ssrIRBuilder) {
	t := c.table
	if t.region != nil {
		t.region.absorb(b)
		if b.usedJS {
			t.region.usesJS = true
		}
	}
	for name := range b.reads {
		t.reads[name] = true
	}
}

// function compiles a declaration to func(ssrArgs ...any) any.
func (c *ssrFuncCompiler) function(function *JsFuncDecl) (*goast.FuncLit, error) {
	if function.Generator {
		return nil, c.fail("generator function %q is not admitted (§7.1)", c.name)
	}
	c.async = function.Async
	if c.table.region != nil && c.ctx == nil {
		c.site = c.table.region.siteLabel(c.table.region.ScriptOffset + function.NodePos())
	}
	c.scopes = append(c.scopes, map[string]string{})
	defer func() { c.scopes = c.scopes[:len(c.scopes)-1] }()
	var body []goast.Stmt
	// An exception already pending (an earlier operand threw) is not
	// seen by this call's catch clauses: the call does not run.
	body = append(body, &goast.IfStmt{Cond: &goast.CallExpr{Fun: goSelector("ssrJS", "Failed")}, Body: &goast.BlockStmt{List: []goast.Stmt{&goast.ReturnStmt{Results: []goast.Expr{ssrUndefined()}}}}})
	for i, param := range function.Params {
		stmts, err := c.bind(param, &goast.CallExpr{Fun: goSelector("vela", "Arg"), Args: []goast.Expr{goast.NewIdent("ssrArgs"), &goast.BasicLit{Kind: token.INT, Value: strconv.Itoa(i)}}})
		if err != nil {
			return nil, err
		}
		body = append(body, stmts...)
	}
	if function.Body == nil {
		return nil, c.fail("function %q has no body", c.name)
	}
	stmts, err := c.statements(function.Body.Body)
	if err != nil {
		return nil, err
	}
	body = append(body, stmts...)
	if !ssrEndsWithReturn(body) {
		body = append(body, &goast.ReturnStmt{Results: []goast.Expr{ssrUndefined()}})
	}
	if function.Async {
		// The body runs to completion now; its result or exception settles
		// the returned promise (T-7.4a).
		inner := &goast.FuncLit{Type: &goast.FuncType{Params: &goast.FieldList{}, Results: &goast.FieldList{List: []*goast.Field{{Type: goast.NewIdent("any")}}}}, Body: &goast.BlockStmt{List: body[1:]}}
		body = []goast.Stmt{body[0], &goast.ReturnStmt{Results: []goast.Expr{&goast.CallExpr{Fun: goSelector("ssrJS", "Async"), Args: []goast.Expr{&goast.CallExpr{Fun: inner}}}}}}
	}
	return &goast.FuncLit{
		Type: &goast.FuncType{
			Params:  &goast.FieldList{List: []*goast.Field{{Names: []*goast.Ident{goast.NewIdent("ssrArgs")}, Type: &goast.Ellipsis{Elt: goast.NewIdent("any")}}}},
			Results: &goast.FieldList{List: []*goast.Field{{Type: goast.NewIdent("any")}}},
		},
		Body: &goast.BlockStmt{List: body},
	}, nil
}

func ssrUndefined() goast.Expr { return &goast.CallExpr{Fun: goSelector("vela", "Undefined")} }

// returnStmt returns value from the function, or completes the current
// try part with (value, true).
func (c *ssrFuncCompiler) returnStmt(value goast.Expr) goast.Stmt {
	if c.tries > 0 {
		return &goast.ReturnStmt{Results: []goast.Expr{value, goast.NewIdent("true")}}
	}
	return &goast.ReturnStmt{Results: []goast.Expr{value}}
}

// unwind leaves the function or the try part when an exception is pending.
func (c *ssrFuncCompiler) unwind() goast.Stmt {
	var exit goast.Stmt = &goast.ReturnStmt{Results: []goast.Expr{ssrUndefined()}}
	if c.tries > 0 {
		exit = &goast.ReturnStmt{Results: []goast.Expr{goast.NewIdent("nil"), goast.NewIdent("false")}}
	}
	return &goast.IfStmt{Cond: &goast.CallExpr{Fun: goSelector("ssrJS", "Failed")}, Body: &goast.BlockStmt{List: []goast.Stmt{exit}}}
}

func (c *ssrFuncCompiler) statements(list []JsStmt) ([]goast.Stmt, error) {
	var out []goast.Stmt
	// Function declarations are hoisted within their block.
	for _, stmt := range list {
		if decl, ok := stmt.(*JsFuncDecl); ok {
			goName := c.declare(decl.Name)
			out = append(out, ssrVarAny(goName, nil), ssrUse(goName))
		}
	}
	// Hoisted declarations come first, so that calls before them see the
	// function.
	for _, stmt := range list {
		if _, ok := stmt.(*JsFuncDecl); ok {
			stmts, err := c.statement(stmt)
			if err != nil {
				return nil, err
			}
			out = append(out, stmts...)
		}
	}
	for _, stmt := range list {
		if _, ok := stmt.(*JsFuncDecl); ok {
			continue
		}
		stmts, err := c.statement(stmt)
		if err != nil {
			return nil, err
		}
		out = append(out, stmts...)
	}
	return out, nil
}

func (c *ssrFuncCompiler) block(stmt JsStmt) ([]goast.Stmt, error) {
	c.scopes = append(c.scopes, map[string]string{})
	defer func() { c.scopes = c.scopes[:len(c.scopes)-1] }()
	if block, ok := stmt.(*JsBlock); ok {
		return c.statements(block.Body)
	}
	return c.statements([]JsStmt{stmt})
}

// checked appends the unwind check after a statement that can throw.
func (c *ssrFuncCompiler) checked(stmt goast.Stmt, throws bool) []goast.Stmt {
	if throws {
		return []goast.Stmt{stmt, c.unwind()}
	}
	return []goast.Stmt{stmt}
}

func (c *ssrFuncCompiler) statement(stmt JsStmt) ([]goast.Stmt, error) {
	switch s := stmt.(type) {
	case *JsEmptyStmt:
		return nil, nil
	case *JsBlock:
		body, err := c.block(s)
		if err != nil {
			return nil, err
		}
		return []goast.Stmt{&goast.BlockStmt{List: body}}, nil
	case *JsVarDecl:
		if s.Kind == "var" {
			return nil, c.fail("var declarations are not admitted; use let or const (§7.1)")
		}
		var out []goast.Stmt
		for _, declarator := range s.Decls {
			value := goast.Expr(ssrUndefined())
			throws := false
			if declarator.Init != nil {
				var err error
				if value, throws, err = c.valueOf(declarator.Init); err != nil {
					return nil, err
				}
			}
			stmts, err := c.bind(declarator.Id, value)
			if err != nil {
				return nil, err
			}
			out = append(out, stmts...)
			if throws {
				out = append(out, c.unwind())
			}
		}
		return out, nil
	case *JsFuncDecl:
		goName, _ := c.lookup(s.Name)
		closure, err := (&ssrFuncCompiler{table: c.table, name: s.Name, scopes: c.scopes, next: c.next, ctx: c.ctx, site: c.site, deferImpure: c.deferImpure}).function(s)
		if err != nil {
			return nil, err
		}
		return []goast.Stmt{
			&goast.AssignStmt{Lhs: []goast.Expr{goast.NewIdent(goName)}, Tok: token.ASSIGN, Rhs: []goast.Expr{&goast.CallExpr{Fun: goSelector("vela", "Func"), Args: []goast.Expr{closure}}}},
		}, nil
	case *JsExprStmt:
		return c.exprStatement(s.X)
	case *JsReturnStmt:
		if s.Arg == nil {
			return []goast.Stmt{c.returnStmt(ssrUndefined())}, nil
		}
		if assignment, ok := s.Arg.(*JsAssignExpr); ok {
			// An arrow such as (v) => x = v: assign, then return the target.
			stmts, err := c.assign(assignment.L, assignment.Op, assignment.R)
			if err != nil {
				return nil, err
			}
			if _, local := c.lookup(ssrAssignmentRoot(assignment.L)); !local {
				return append(stmts, c.returnStmt(ssrUndefined())), nil
			}
			result, err := c.statement(&JsReturnStmt{Arg: assignment.L})
			if err != nil {
				return nil, err
			}
			return append(stmts, result...), nil
		}
		value, throws, err := c.valueOf(s.Arg)
		if err != nil {
			return nil, err
		}
		if !throws {
			return []goast.Stmt{c.returnStmt(value)}, nil
		}
		// The result is not returned when evaluating it threw.
		temp := c.declare("$result")
		return []goast.Stmt{ssrVarAny(temp, value), c.unwind(), c.returnStmt(goast.NewIdent(temp))}, nil
	case *JsThrowStmt:
		value, _, err := c.valueOf(s.Arg)
		if err != nil {
			return nil, err
		}
		return []goast.Stmt{
			&goast.ExprStmt{X: &goast.CallExpr{Fun: goSelector("ssrJS", "Throw"), Args: []goast.Expr{value, ssrGoString(c.site)}}},
			c.unwind(),
		}, nil
	case *JsIfStmt:
		test, throws, err := c.condition(s.Test)
		if err != nil {
			return nil, err
		}
		then, err := c.block(s.Then)
		if err != nil {
			return nil, err
		}
		branch := &goast.IfStmt{Cond: test, Body: &goast.BlockStmt{List: then}}
		if s.Else != nil {
			otherwise, err := c.block(s.Else)
			if err != nil {
				return nil, err
			}
			branch.Else = &goast.BlockStmt{List: otherwise}
		}
		return c.guardTest(branch, throws), nil
	case *JsWhileStmt:
		return c.loop(nil, s.Test, nil, s.Body, false)
	case *JsDoWhileStmt:
		return c.loop(nil, s.Test, nil, s.Body, true)
	case *JsForStmt:
		return c.forStatement(s)
	case *JsForInStmt:
		if !s.IsOf {
			return nil, c.fail("for…in is not admitted; use for…of over Object.keys (§7.1)")
		}
		return c.forOf(s)
	case *JsBreakStmt, *JsContinueStmt:
		label := ""
		tok := token.BREAK
		if b, ok := s.(*JsBreakStmt); ok {
			label = b.Label
		} else {
			label = s.(*JsContinueStmt).Label
			tok = token.CONTINUE
		}
		if label != "" {
			return nil, c.fail("labels are not admitted (§7.1)")
		}
		if len(c.loops) == 0 || c.loops[len(c.loops)-1] == 0 {
			if tok == token.CONTINUE || c.tries > 0 {
				return nil, c.fail("break or continue across a try statement is not supported during server rendering")
			}
		}
		return []goast.Stmt{&goast.BranchStmt{Tok: tok}}, nil
	case *JsSwitchStmt:
		return c.switchStatement(s)
	case *JsTryStmt:
		return c.tryStatement(s)
	case *JsLabeledStmt:
		return nil, c.fail("labels are not admitted (§7.1)")
	}
	return nil, c.fail("statement %T is not admitted during server rendering (§7.1)", stmt)
}

// valueOf builds an expression value; a function value is compiled as a
// closure.
func (c *ssrFuncCompiler) valueOf(expr JsExpr) (goast.Expr, bool, error) {
	if function := ssrFunctionOf("", expr); function != nil {
		closure, err := (&ssrFuncCompiler{table: c.table, name: "function", scopes: c.scopes, next: c.next, ctx: c.ctx, site: c.site, deferImpure: c.deferImpure}).function(function)
		if err != nil {
			return nil, false, err
		}
		return &goast.CallExpr{Fun: goSelector("vela", "Func"), Args: []goast.Expr{closure}}, false, nil
	}
	return c.expr(expr)
}

// condition lowers a test with JavaScript truthiness.
func (c *ssrFuncCompiler) condition(expr JsExpr) (goast.Expr, bool, error) {
	b := c.builder()
	ir, err := b.build(expr, 0)
	c.absorb(b)
	if err != nil {
		return nil, false, err
	}
	value := lowerSSRIR(ir)
	if !ir.irBase().Facts.Bool {
		value = ssrGoTruthy(value)
	}
	return value, b.throws, nil
}

// guardTest evaluates a throwing test into a variable first, so that the
// statement does not run after an exception.
func (c *ssrFuncCompiler) guardTest(stmt *goast.IfStmt, throws bool) []goast.Stmt {
	if !throws {
		return []goast.Stmt{stmt}
	}
	temp := c.declare("$test")
	test := stmt.Cond
	stmt.Cond = goast.NewIdent(temp)
	return []goast.Stmt{
		&goast.AssignStmt{Lhs: []goast.Expr{goast.NewIdent(temp)}, Tok: token.DEFINE, Rhs: []goast.Expr{test}},
		c.unwind(), stmt,
	}
}

// exprStatement lowers an expression statement: an assignment or update
// of a local, a member assignment, or an expression evaluated for its
// effect (a call).
func (c *ssrFuncCompiler) exprStatement(expr JsExpr) ([]goast.Stmt, error) {
	switch e := expr.(type) {
	case *JsAssignExpr:
		return c.assign(e.L, e.Op, e.R)
	case *JsUnaryExpr:
		if e.Op == "++" || e.Op == "--" {
			op := "+="
			if e.Op == "--" {
				op = "-="
			}
			one := &JsLiteral{Kind: "number", Raw: "1"}
			return c.assign(e.X, op, one)
		}
	case *JsSeqExpr:
		var out []goast.Stmt
		for _, item := range e.Exprs {
			stmts, err := c.exprStatement(item)
			if err != nil {
				return nil, err
			}
			out = append(out, stmts...)
		}
		return out, nil
	}
	value, throws, err := c.expr(expr)
	if err != nil {
		return nil, err
	}
	return c.checked(&goast.AssignStmt{Lhs: []goast.Expr{goast.NewIdent("_")}, Tok: token.ASSIGN, Rhs: []goast.Expr{value}}, throws), nil
}

var ssrCompoundOps = map[string]string{"+=": "+", "-=": "-", "*=": "*", "/=": "/", "%=": "%", "**=": "**"}

// assign lowers target op= value for a local variable or a member of a
// local value (T-6.6b). Writes to component state, props, or module
// bindings are impure (T-6.6a).
func (c *ssrFuncCompiler) assign(target JsExpr, op string, value JsExpr) ([]goast.Stmt, error) {
	right := value
	if binary, ok := ssrCompoundOps[op]; ok {
		right = &JsBinaryExpr{Op: binary, L: target, R: value}
	} else if op == "&&=" || op == "||=" || op == "??=" {
		right = &JsLogicalExpr{Op: op[:2], L: target, R: value}
	} else if op != "=" {
		return nil, c.fail("assignment operator %s is not supported", op)
	}
	switch t := target.(type) {
	case *JsIdent:
		goName, ok := c.lookup(t.Name)
		if !ok {
			return c.impure(fmt.Sprintf("%q is not a local of the function; server rendering must not change state, props, or module bindings (T-6.6a)", t.Name))
		}
		result, throws, err := c.valueOf(right)
		if err != nil {
			return nil, err
		}
		return c.checked(&goast.AssignStmt{Lhs: []goast.Expr{goast.NewIdent(goName)}, Tok: token.ASSIGN, Rhs: []goast.Expr{result}}, throws), nil
	case *JsMemberExpr:
		root := JsExpr(t)
		for {
			member, ok := root.(*JsMemberExpr)
			if !ok {
				break
			}
			root = member.Obj
		}
		ident, ok := root.(*JsIdent)
		if !ok {
			return nil, c.fail("assignment to a member of a computed value is not supported")
		}
		if _, local := c.lookup(ident.Name); !local {
			return c.impure(fmt.Sprintf("assignment to a member of %q changes a value the function did not create (T-6.6a)", ident.Name))
		}
		object, objectThrows, err := c.expr(t.Obj)
		if err != nil {
			return nil, err
		}
		var key goast.Expr
		keyThrows := false
		if t.Computed {
			if key, keyThrows, err = c.expr(t.Prop); err != nil {
				return nil, err
			}
		} else if property, ok := t.Prop.(*JsIdent); ok {
			key = ssrGoString(property.Name)
		} else {
			return nil, c.fail("member name %T is not supported", t.Prop)
		}
		result, resultThrows, err := c.valueOf(right)
		if err != nil {
			return nil, err
		}
		set := &goast.CallExpr{Fun: goSelector("ssrJS", "SetMember"), Args: []goast.Expr{object, key, result, ssrGoString(c.site)}}
		_ = objectThrows || keyThrows || resultThrows
		return c.checked(&goast.AssignStmt{Lhs: []goast.Expr{goast.NewIdent("_")}, Tok: token.ASSIGN, Rhs: []goast.Expr{set}}, true), nil
	}
	return nil, c.fail("assignment target %T is not supported", target)
}

// bind declares the names of a binding pattern: an identifier, an array or
// object pattern, with defaults (§7.1).
func (c *ssrFuncCompiler) bind(pattern JsExpr, value goast.Expr) ([]goast.Stmt, error) {
	switch p := pattern.(type) {
	case *JsIdent:
		goName := c.declare(p.Name)
		return []goast.Stmt{ssrVarAny(goName, value), ssrUse(goName)}, nil
	case *JsAssignExpr:
		// A default applies when the value is undefined.
		temp := c.declare("$default")
		fallback, _, err := c.valueOf(p.R)
		if err != nil {
			return nil, err
		}
		out := []goast.Stmt{
			ssrVarAny(temp, value), ssrUse(temp),
			&goast.IfStmt{
				Cond: &goast.CallExpr{Fun: goSelector("vela", "StrictEqual"), Args: []goast.Expr{goast.NewIdent(temp), ssrUndefined()}},
				Body: &goast.BlockStmt{List: []goast.Stmt{&goast.AssignStmt{Lhs: []goast.Expr{goast.NewIdent(temp)}, Tok: token.ASSIGN, Rhs: []goast.Expr{fallback}}}},
			},
		}
		rest, err := c.bind(p.L, goast.NewIdent(temp))
		if err != nil {
			return nil, err
		}
		return append(out, rest...), nil
	case *JsArrayExpr:
		temp := c.declare("$array")
		out := []goast.Stmt{ssrVarAny(temp, value), ssrUse(temp)}
		for i, element := range p.Elems {
			if element == nil {
				continue
			}
			if _, spread := element.(*JsSpreadExpr); spread {
				return nil, c.fail("rest elements in destructuring are not supported yet")
			}
			item := &goast.CallExpr{Fun: goSelector("ssrJS", "Index"), Args: []goast.Expr{goast.NewIdent(temp), &goast.BasicLit{Kind: token.FLOAT, Value: strconv.Itoa(i) + ".0"}, ssrGoString(c.site)}}
			stmts, err := c.bind(element, item)
			if err != nil {
				return nil, err
			}
			out = append(out, stmts...)
		}
		return append(out, c.unwind()), nil
	case *JsObjectExpr:
		temp := c.declare("$object")
		out := []goast.Stmt{ssrVarAny(temp, value), ssrUse(temp)}
		for _, property := range p.Props {
			if property.Spread || property.Computed {
				return nil, c.fail("rest and computed properties in destructuring are not supported yet")
			}
			key, err := translateSSRObjectKey(property.Key, 0)
			if err != nil {
				return nil, err
			}
			item := &goast.CallExpr{Fun: goSelector("ssrJS", "Get"), Args: []goast.Expr{goast.NewIdent(temp), ssrGoString(key), ssrGoString(c.site)}}
			target := property.Value
			if target == nil {
				target = property.Key
			}
			stmts, err := c.bind(target, item)
			if err != nil {
				return nil, err
			}
			out = append(out, stmts...)
		}
		return append(out, c.unwind()), nil
	}
	return nil, c.fail("binding pattern %T is not supported", pattern)
}

// loop lowers while (post nil, doWhile false) and do…while. A do…while
// evaluates its test after the body and after continue.
func (c *ssrFuncCompiler) loop(init []goast.Stmt, test JsExpr, post []goast.Stmt, body JsStmt, doWhile bool) ([]goast.Stmt, error) {
	c.loops = append(c.loops, 1)
	defer func() { c.loops = c.loops[:len(c.loops)-1] }()
	inner, err := c.block(body)
	if err != nil {
		return nil, err
	}
	var cond goast.Expr = goast.NewIdent("true")
	throws := false
	if test != nil {
		if cond, throws, err = c.condition(test); err != nil {
			return nil, err
		}
	}
	if doWhile {
		first := c.declare("$first")
		cond = &goast.BinaryExpr{X: goast.NewIdent(first), Op: token.LOR, Y: cond}
		loop := &goast.ForStmt{
			Init: &goast.AssignStmt{Lhs: []goast.Expr{goast.NewIdent(first)}, Tok: token.DEFINE, Rhs: []goast.Expr{goast.NewIdent("true")}},
			Cond: cond,
			Post: &goast.AssignStmt{Lhs: []goast.Expr{goast.NewIdent(first)}, Tok: token.ASSIGN, Rhs: []goast.Expr{goast.NewIdent("false")}},
			Body: &goast.BlockStmt{List: inner},
		}
		return []goast.Stmt{loop, c.unwind()}, nil
	}
	if throws {
		// The test runs at the top of every iteration; an exception in it
		// leaves the loop and unwinds.
		body := append([]goast.Stmt{&goast.IfStmt{Cond: &goast.UnaryExpr{Op: token.NOT, X: cond}, Body: &goast.BlockStmt{List: []goast.Stmt{&goast.BranchStmt{Tok: token.BREAK}}}}, c.unwind()}, inner...)
		body = append(body, post...)
		return append(init, &goast.ForStmt{Body: &goast.BlockStmt{List: body}}, c.unwind()), nil
	}
	loop := &goast.ForStmt{Cond: cond, Body: &goast.BlockStmt{List: inner}}
	if len(post) == 1 {
		loop.Post = post[0]
	} else if len(post) > 1 {
		loop.Body.List = append(loop.Body.List, post...)
	}
	return append(init, loop, c.unwind()), nil
}

func (c *ssrFuncCompiler) forStatement(s *JsForStmt) ([]goast.Stmt, error) {
	if decl, ok := s.Init.(*JsVarDecl); ok && ssrClosesOver(s.Body, decl) {
		// JavaScript gives each iteration its own copy of a let loop
		// variable; the lowering shares one Go variable.
		return nil, c.fail("a function created in a for loop body must not capture the loop variable during server rendering")
	}
	c.scopes = append(c.scopes, map[string]string{})
	defer func() { c.scopes = c.scopes[:len(c.scopes)-1] }()
	var init []goast.Stmt
	if s.Init != nil {
		var err error
		if init, err = c.statement(s.Init); err != nil {
			return nil, err
		}
	}
	var post []goast.Stmt
	if s.Update != nil {
		stmts, err := c.exprStatement(s.Update)
		if err != nil {
			return nil, err
		}
		// A post statement is a single simple statement in Go.
		for _, stmt := range stmts {
			if _, check := stmt.(*goast.IfStmt); !check {
				post = append(post, stmt)
			}
		}
		if len(post) != 1 {
			post = stmts
		}
	}
	stmts, err := c.loop(nil, s.Test, post, s.Body, false)
	if err != nil {
		return nil, err
	}
	return []goast.Stmt{&goast.BlockStmt{List: append(init, stmts...)}}, nil
}

// forOf iterates array elements or string code points.
func (c *ssrFuncCompiler) forOf(s *JsForInStmt) ([]goast.Stmt, error) {
	collection, _, err := c.expr(s.Right)
	if err != nil {
		return nil, err
	}
	c.scopes = append(c.scopes, map[string]string{})
	defer func() { c.scopes = c.scopes[:len(c.scopes)-1] }()
	item := c.declare("$item")
	var pattern JsExpr
	switch left := s.Left.(type) {
	case *JsVarDecl:
		if len(left.Decls) != 1 {
			return nil, c.fail("for…of declares one binding")
		}
		pattern = left.Decls[0].Id
	default:
		return nil, c.fail("for…of must declare its binding with let or const")
	}
	bind, err := c.bind(pattern, goast.NewIdent(item))
	if err != nil {
		return nil, err
	}
	c.loops = append(c.loops, 1)
	inner, err := c.block(s.Body)
	c.loops = c.loops[:len(c.loops)-1]
	if err != nil {
		return nil, err
	}
	items := &goast.CallExpr{Fun: goSelector("ssrJS", "SpreadItems"), Args: []goast.Expr{collection, ssrGoString(c.site)}}
	loop := &goast.RangeStmt{Key: goast.NewIdent("_"), Value: goast.NewIdent(item), Tok: token.DEFINE, X: items, Body: &goast.BlockStmt{List: append(bind, inner...)}}
	return []goast.Stmt{loop, c.unwind()}, nil
}

// switchStatement matches with === in order; a case body without break,
// return, throw, or continue at its end falls through.
func (c *ssrFuncCompiler) switchStatement(s *JsSwitchStmt) ([]goast.Stmt, error) {
	discriminant, throws, err := c.expr(s.Disc)
	if err != nil {
		return nil, err
	}
	temp := c.declare("$switch")
	out := []goast.Stmt{ssrVarAny(temp, discriminant), ssrUse(temp)}
	if throws {
		out = append(out, c.unwind())
	}
	sw := &goast.SwitchStmt{Body: &goast.BlockStmt{}}
	c.loops = append(c.loops, 1) // break leaves the switch
	defer func() { c.loops = c.loops[:len(c.loops)-1] }()
	for i, kase := range s.Cases {
		clause := &goast.CaseClause{}
		if kase.Test != nil {
			test, _, err := c.expr(kase.Test)
			if err != nil {
				return nil, err
			}
			clause.List = []goast.Expr{&goast.CallExpr{Fun: goSelector("vela", "StrictEqual"), Args: []goast.Expr{goast.NewIdent(temp), test}}}
		}
		body, err := c.block(&JsBlock{Body: kase.Body})
		if err != nil {
			return nil, err
		}
		if i < len(s.Cases)-1 && !ssrCompletesAbruptly(kase.Body) {
			body = append(body, &goast.BranchStmt{Tok: token.FALLTHROUGH})
		}
		clause.Body = body
		sw.Body.List = append(sw.Body.List, clause)
	}
	return append(out, sw), nil
}

func ssrCompletesAbruptly(body []JsStmt) bool {
	if len(body) == 0 {
		return false
	}
	switch last := body[len(body)-1].(type) {
	case *JsBreakStmt, *JsReturnStmt, *JsThrowStmt, *JsContinueStmt:
		return true
	case *JsBlock:
		return ssrCompletesAbruptly(last.Body)
	}
	return false
}

// tryStatement lowers try/catch/finally through vela.Try (T-6.5d).
func (c *ssrFuncCompiler) tryStatement(s *JsTryStmt) ([]goast.Stmt, error) {
	part := func(body *JsBlock, param JsExpr) (*goast.FuncLit, error) {
		c.tries++
		c.loops = append(c.loops, 0)
		defer func() {
			c.tries--
			c.loops = c.loops[:len(c.loops)-1]
		}()
		c.scopes = append(c.scopes, map[string]string{})
		defer func() { c.scopes = c.scopes[:len(c.scopes)-1] }()
		var stmts []goast.Stmt
		params := &goast.FieldList{}
		if param != nil {
			params.List = []*goast.Field{{Names: []*goast.Ident{goast.NewIdent("ssrCaught")}, Type: goast.NewIdent("any")}}
			bind, err := c.bind(param, goast.NewIdent("ssrCaught"))
			if err != nil {
				return nil, err
			}
			stmts = append(stmts, bind...)
		}
		inner, err := c.statements(body.Body)
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, inner...)
		if !ssrEndsWithReturn(stmts) {
			stmts = append(stmts, &goast.ReturnStmt{Results: []goast.Expr{goast.NewIdent("nil"), goast.NewIdent("false")}})
		}
		return &goast.FuncLit{
			Type: &goast.FuncType{Params: params, Results: &goast.FieldList{List: []*goast.Field{{Type: goast.NewIdent("any")}, {Type: goast.NewIdent("bool")}}}},
			Body: &goast.BlockStmt{List: stmts},
		}, nil
	}
	try, err := part(s.Block, nil)
	if err != nil {
		return nil, err
	}
	var catch, finally goast.Expr = goast.NewIdent("nil"), goast.NewIdent("nil")
	if s.Handler != nil {
		param := s.CatchParam
		if param == nil {
			param = &JsIdent{Name: "$ignored"}
		}
		literal, err := part(s.Handler, param)
		if err != nil {
			return nil, err
		}
		catch = literal
	}
	if s.Finalizer != nil {
		literal, err := part(s.Finalizer, nil)
		if err != nil {
			return nil, err
		}
		finally = literal
	}
	value := c.declare("$try")
	returned := c.declare("$returned")
	call := &goast.CallExpr{Fun: goSelector("vela", "Try"), Args: []goast.Expr{
		goast.NewIdent("ssrJS"), c.table.contextExpr(), try, catch, finally,
	}}
	return []goast.Stmt{
		&goast.AssignStmt{Lhs: []goast.Expr{goast.NewIdent(value), goast.NewIdent(returned)}, Tok: token.DEFINE, Rhs: []goast.Expr{call}},
		&goast.IfStmt{Cond: goast.NewIdent(returned), Body: &goast.BlockStmt{List: []goast.Stmt{c.returnStmt(goast.NewIdent(value))}}},
		ssrUse(value),
		c.unwind(),
	}, nil
}

// ── Calls ────────────────────────────────────────────────────────────────

// ssrIRCallFunc calls a compiled component function or a function value.
type ssrIRCallFunc struct {
	ssrIRBase
	// Func is the Go variable of a compiled function; Value calls a value
	// through ssrJS.Call.
	Func string
	// Callee is a Go function expression, such as a module export.
	Callee goast.Expr
	Value  SSRIRExpr
	Args   []SSRIRExpr
	Site   string
}

func lowerSSRCallFunc(n *ssrIRCallFunc) goast.Expr {
	args := make([]goast.Expr, 0, len(n.Args)+2)
	if n.Value != nil {
		args = append(args, ssrGoString(n.Site), lowerSSRIR(n.Value))
	}
	for _, arg := range n.Args {
		args = append(args, lowerSSRIR(arg))
	}
	if n.Value != nil {
		return &goast.CallExpr{Fun: goSelector("ssrJS", "Call"), Args: args}
	}
	if n.Callee != nil {
		return &goast.CallExpr{Fun: n.Callee, Args: args}
	}
	return &goast.CallExpr{Fun: goast.NewIdent(n.Func), Args: args}
}

// compiledCall calls component function name through its compiled
// closure, compiling it on first use.
func (b *ssrIRBuilder) compiledCall(name string, call *JsCallExpr, pos int) (SSRIRExpr, error) {
	table := b.ctx.funcs
	if table == nil || !b.js {
		return nil, ssrExprUnsupported(pos, "local formatter %q must contain exactly one return expression", name)
	}
	if err := table.compile(name); err != nil {
		return nil, err
	}
	var args []SSRIRExpr
	for _, argument := range call.Args {
		if _, spread := argument.(*JsSpreadExpr); spread {
			return nil, ssrExprUnsupported(pos, "spread arguments are not supported yet")
		}
		value, err := b.build(argument, pos)
		if err != nil {
			return nil, err
		}
		args = append(args, value)
	}
	for local := range table.captured {
		b.inlined[local] = true
	}
	for local := range table.reads {
		if b.reads == nil {
			b.reads = map[string]bool{}
		}
		b.reads[local] = true
		b.inlined[local] = true
	}
	b.usedJS, b.throws = true, true
	b.called(name, SSROutcomeNative, "")
	return &ssrIRCallFunc{ssrIRBase: b.base(pos, ssrIRFacts{}), Func: ssrFuncName(name), Args: args, Site: b.site}, nil
}

// valueCall calls a function value held by a local of a translated
// function.
func (b *ssrIRBuilder) valueCall(callee SSRIRExpr, call *JsCallExpr, pos int) (SSRIRExpr, error) {
	var args []SSRIRExpr
	for _, argument := range call.Args {
		value, err := b.build(argument, pos)
		if err != nil {
			return nil, err
		}
		args = append(args, value)
	}
	b.usedJS, b.throws = true, true
	return &ssrIRCallFunc{ssrIRBase: b.base(pos, ssrIRFacts{}), Value: callee, Args: args, Site: b.site}, nil
}

// newError lowers new Error(m), new TypeError(m), new RangeError(m).
func (b *ssrIRBuilder) newError(node *JsNewExpr, pos int) (SSRIRExpr, error) {
	callee, ok := node.Callee.(*JsIdent)
	if !ok || (callee.Name != "Error" && callee.Name != "TypeError" && callee.Name != "RangeError") {
		return nil, ssrExprUnsupported(pos, "new is admitted only for Error, TypeError, and RangeError (§6.2)")
	}
	var message SSRIRExpr = &ssrIRUndefined{ssrIRBase: b.base(pos, ssrIRFacts{})}
	if len(node.Args) > 0 {
		value, err := b.build(node.Args[0], pos)
		if err != nil {
			return nil, err
		}
		message = value
	}
	kind := &ssrIRLiteral{ssrIRBase: b.base(pos, ssrIRFacts{String: true}), Kind: token.STRING, Value: callee.Name}
	return &ssrIRHelper{ssrIRBase: b.base(pos, ssrIRFacts{Object: true}), Name: "NewError", Args: []SSRIRExpr{kind, message}}, nil
}

var _ = fmt.Sprintf

// funcsPlan returns the plan of the function table, or an empty plan.
func (ctx SSRExprContext) funcsPlan() *SSRRenderPlan {
	if ctx.funcs == nil {
		return &SSRRenderPlan{}
	}
	return ctx.funcs.plan
}

func ssrEndsWithReturn(stmts []goast.Stmt) bool {
	if len(stmts) == 0 {
		return false
	}
	_, ok := stmts[len(stmts)-1].(*goast.ReturnStmt)
	return ok
}

// ssrClosesOver reports a function inside body that reads a name declared
// by decl.
func ssrClosesOver(body JsStmt, decl *JsVarDecl) bool {
	names := map[string]bool{}
	for _, declarator := range decl.Decls {
		if ident, ok := declarator.Id.(*JsIdent); ok {
			names[ident.Name] = true
		}
	}
	found := false
	WalkNode(body, func(n JsNode) bool {
		switch n.(type) {
		case *JsArrowFunc, *JsFuncExpr, *JsFuncDecl:
			ssrWalkIdents(n, func(name string) {
				if names[name] {
					found = true
				}
			})
			return false
		}
		return !found
	})
	return found
}

func (t *ssrFuncTable) contextExpr() goast.Expr {
	if t.context != nil {
		return t.context
	}
	return &goast.CallExpr{Fun: goSelector("vela", "ContextOf"), Args: []goast.Expr{goast.NewIdent("w")}}
}

// ── Function values, await, and promises ────────────────────────────────

// functionValue lowers an arrow or function expression to a vela.Func
// closure, inside a translated function or in a template expression.
func (b *ssrIRBuilder) functionValue(expr JsExpr, pos int) (SSRIRExpr, error) {
	var c *ssrFuncCompiler
	switch {
	case b.funcCompiler != nil:
		parent := b.funcCompiler
		c = &ssrFuncCompiler{table: parent.table, name: "function", scopes: parent.scopes, next: parent.next, ctx: parent.ctx, site: parent.site, deferImpure: parent.deferImpure}
	case b.ctx.funcs != nil && b.js:
		ctx := b.ctx
		ctx.resolving, ctx.resolvingFunctions = nil, nil
		c = &ssrFuncCompiler{table: b.ctx.funcs, name: "function", next: new(int), ctx: &ctx, site: b.site, deferImpure: true}
	default:
		return nil, ssrExprUnsupported(pos, "arrow functions are not available to static SSR")
	}
	literal, err := c.function(ssrFunctionOf("", expr))
	if err != nil {
		return nil, err
	}
	b.usedJS = true
	return &ssrIRGo{ssrIRBase: b.base(pos, ssrIRFacts{}), Expr: &goast.CallExpr{Fun: goSelector("vela", "Func"), Args: []goast.Expr{literal}}}, nil
}

// contextExpr is the render context in the code being generated.
func (b *ssrIRBuilder) contextExpr() goast.Expr {
	if b.funcCompiler != nil {
		return b.funcCompiler.table.contextExpr()
	}
	if b.ctx.funcs != nil {
		return b.ctx.funcs.contextExpr()
	}
	return &goast.CallExpr{Fun: goSelector("vela", "ContextOf"), Args: []goast.Expr{goast.NewIdent("w")}}
}

// await lowers await e inside an async function (T-7.4a).
func (b *ssrIRBuilder) await(node *JsAwaitExpr, pos int) (SSRIRExpr, error) {
	if b.funcCompiler == nil || !b.funcCompiler.async {
		return nil, ssrExprUnsupported(pos, "await is admitted only inside async functions; top-level await in a component script is not supported (T-7.4d)")
	}
	value, err := b.build(node.X, pos)
	if err != nil {
		return nil, err
	}
	b.usedJS, b.throws = true, true
	return &ssrIRGo{ssrIRBase: b.base(pos, ssrIRFacts{}), Expr: &goast.CallExpr{Fun: goSelector("ssrJS", "Await"), Args: []goast.Expr{b.contextExpr(), lowerSSRIR(value)}}}, nil
}

// promiseCall lowers the promise operations of T-7.4b: the methods then,
// catch, and finally, and Promise.resolve, reject, all, and allSettled.
// handled is false for any other member call.
func (b *ssrIRBuilder) promiseCall(member *JsMemberExpr, call *JsCallExpr, pos int) (SSRIRExpr, bool, error) {
	property, ok := member.Prop.(*JsIdent)
	if !ok || member.Computed || member.Optional {
		return nil, false, nil
	}
	args := make([]goast.Expr, 0, len(call.Args))
	arg := func(i int) goast.Expr {
		if i < len(args) {
			return args[i]
		}
		return ssrUndefined()
	}
	build := func() error {
		for _, argument := range call.Args {
			if _, spread := argument.(*JsSpreadExpr); spread {
				return ssrExprUnsupported(pos, "spread arguments are not supported yet")
			}
			value, err := b.build(argument, pos)
			if err != nil {
				return err
			}
			args = append(args, lowerSSRIR(value))
		}
		return nil
	}
	site := ssrGoString(b.site)
	result := func(fun goast.Expr, values ...goast.Expr) (SSRIRExpr, bool, error) {
		b.usedJS, b.throws = true, true
		return &ssrIRGo{ssrIRBase: b.base(pos, ssrIRFacts{}), Expr: &goast.CallExpr{Fun: fun, Args: values}}, true, nil
	}
	if object, ok := member.Obj.(*JsIdent); ok && object.Name == "Promise" && !b.shadowed("Promise") {
		switch property.Name {
		case "resolve", "reject", "all", "allSettled":
		case "race", "any":
			return nil, true, ssrExprUnsupported(pos, "Promise.%s depends on timing and is not admitted during server rendering; use a host or a client boundary (T-7.4d)", property.Name)
		default:
			return nil, true, ssrExprUnsupported(pos, "Promise.%s is not admitted during server rendering (T-7.4b)", property.Name)
		}
		if err := build(); err != nil {
			return nil, true, err
		}
		switch property.Name {
		case "resolve":
			return result(goSelector("vela", "Resolve"), arg(0))
		case "reject":
			return result(goSelector("vela", "Reject"), arg(0), site)
		case "all":
			return result(goSelector("ssrJS", "All"), site, arg(0))
		default:
			return result(goSelector("ssrJS", "AllSettled"), b.contextExpr(), site, arg(0))
		}
	}
	switch property.Name {
	case "then", "catch", "finally":
	default:
		return nil, false, nil
	}
	receiver, err := b.build(member.Obj, pos)
	if err != nil {
		return nil, true, err
	}
	if err := build(); err != nil {
		return nil, true, err
	}
	switch property.Name {
	case "then":
		return result(goSelector("ssrJS", "Then"), b.contextExpr(), site, lowerSSRIR(receiver), arg(0), arg(1))
	case "catch":
		return result(goSelector("ssrJS", "CatchPromise"), b.contextExpr(), site, lowerSSRIR(receiver), arg(0))
	}
	return result(goSelector("ssrJS", "Finally"), b.contextExpr(), site, lowerSSRIR(receiver), arg(0))
}

// shadowed reports a name bound by the code being translated.
func (b *ssrIRBuilder) shadowed(name string) bool {
	if _, ok := b.params[name]; ok {
		return true
	}
	if _, ok := b.ctx.Bindings[name]; ok {
		return true
	}
	if _, ok := b.ctx.Props[name]; ok {
		return true
	}
	return false
}

// impure rejects a write the server must not make, or defers it to a
// run-time error in a function value that a render may never call.
func (c *ssrFuncCompiler) impure(message string) ([]goast.Stmt, error) {
	if !c.deferImpure {
		return nil, ssrImpure(0, "%s", message)
	}
	return []goast.Stmt{
		&goast.ExprStmt{X: &goast.CallExpr{Fun: goSelector("ssrJS", "Impure"), Args: []goast.Expr{ssrGoString(message), ssrGoString(c.site)}}},
		c.unwind(),
	}, nil
}

// ssrAssignmentRoot is the variable an assignment target writes through:
// x for x, x.a, and x[i].b.
func ssrAssignmentRoot(target JsExpr) string {
	for {
		member, ok := target.(*JsMemberExpr)
		if !ok {
			return identName(target)
		}
		target = member.Obj
	}
}
