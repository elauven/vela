package main

import (
	goast "go/ast"
	"go/token"
	"html"
	"strconv"
	"strings"
)

// BuildSSRGoFile emits the currently supported server render plan through
// standard Go AST nodes. It supports static elements, escaped text and
// attributes, and conditional template branches. Other plan node kinds keep
// producing explicit diagnostics until their renderer semantics exist.
func BuildSSRGoFile(plan *SSRRenderPlan, packageName string) (*goast.File, error) {
	return buildSSRGoFile(plan, packageName, true)
}

// BuildSSRGoRendererFile emits a project renderer that takes its props type
// from the adjacent, user-preservable *_props.go contract file.
func BuildSSRGoRendererFile(plan *SSRRenderPlan, packageName string) (*goast.File, error) {
	return buildSSRGoFile(plan, packageName, false)
}

func buildSSRGoFile(plan *SSRRenderPlan, packageName string, includeProps bool) (*goast.File, error) {
	file, err := buildSSRGoSkeleton(plan, packageName, includeProps)
	if err != nil {
		return nil, err
	}
	emitter := ssrGoEmitter{ctx: NewSSRExprContext(plan), packageName: packageName}
	body, err := emitter.nodes(plan.Nodes)
	if err != nil {
		return nil, err
	}
	body = append(body, &goast.ReturnStmt{Results: []goast.Expr{goast.NewIdent("nil")}})
	render, err := ssrRenderFunc(file, plan.ComponentName)
	if err != nil {
		return nil, err
	}
	render.Body.List = body
	return file, nil
}

type ssrGoEmitter struct {
	ctx           SSRExprContext
	packageName   string
	needsEscaping bool
	eachNext      int
}

func (e *ssrGoEmitter) nodes(nodes []SSRPlanNode) ([]goast.Stmt, error) {
	body := make([]goast.Stmt, 0, len(nodes))
	for _, node := range nodes {
		statements, err := e.node(node)
		if err != nil {
			return nil, err
		}
		body = append(body, statements...)
	}
	return body, nil
}

func (e *ssrGoEmitter) node(node SSRPlanNode) ([]goast.Stmt, error) {
	switch n := node.(type) {
	case *SSRPlanText:
		return e.text(n)
	case *SSRPlanElement:
		return e.element(n)
	case *SSRPlanComponent:
		return e.component(n)
	case *SSRPlanSlot:
		return e.slot(n)
	case *SSRPlanIf:
		return e.ifBlock(n)
	case *SSRPlanAwait:
		return e.awaitBlock(n)
	case *SSRPlanEach:
		return e.eachBlock(n)
	default:
		return nil, ssrExprUnsupported(0, "SSR node %T is not lowered yet", node)
	}
}

func (e *ssrGoEmitter) component(node *SSRPlanComponent) ([]goast.Stmt, error) {
	if node.Link == nil {
		return nil, ssrExprUnsupported(node.Span.Start, "component %q is not linked to a local .vela import", node.Name)
	}
	if node.Link.PackageName != e.packageName {
		return nil, ssrExprUnsupported(node.Span.Start, "component %q belongs to Go package %q; cross-package component calls are not lowered yet", node.Name, node.Link.PackageName)
	}
	fields := make([]*goast.KeyValueExpr, 0, len(node.Attrs))
	for _, attr := range node.Attrs {
		var value goast.Expr
		switch attr.Role {
		case SSRAttrStatic:
			if attr.ValueKind == AttrValueMissing {
				return nil, ssrExprUnsupported(attr.Span.Start, "component prop %q requires a value", attr.Name)
			}
			value = &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(attr.Value)}
		case SSRAttrDynamic:
			var err error
			value, err = TranslateSSRExpression(attr.Expr, e.ctx)
			if err != nil {
				return nil, err
			}
		default:
			return nil, ssrExprUnsupported(attr.Span.Start, "component prop %q with role %d requires hydration", attr.Name, attr.Role)
		}
		fields = append(fields, &goast.KeyValueExpr{Key: goast.NewIdent(ssrGoExportName(attr.Name)), Value: value})
	}
	props := &goast.CompositeLit{Type: goast.NewIdent(node.Link.GoName + "Props"), Elts: make([]goast.Expr, len(fields))}
	for i, field := range fields {
		props.Elts[i] = field
	}
	slots := &goast.CompositeLit{Type: goast.NewIdent(node.Link.GoName + "Slots")}
	for name, children := range node.Slots {
		body, err := e.nodes(children)
		if err != nil {
			return nil, err
		}
		body = append(body, &goast.ReturnStmt{Results: []goast.Expr{goast.NewIdent("nil")}})
		closure := &goast.FuncLit{Type: &goast.FuncType{
			Params:  &goast.FieldList{List: []*goast.Field{{Names: []*goast.Ident{goast.NewIdent("w")}, Type: goSelector("io", "Writer")}}},
			Results: &goast.FieldList{List: []*goast.Field{{Type: goast.NewIdent("error")}}},
		}, Body: &goast.BlockStmt{List: body}}
		slots.Elts = append(slots.Elts, &goast.KeyValueExpr{Key: goast.NewIdent(ssrGoExportName(name)), Value: closure})
	}
	call := &goast.CallExpr{
		Fun:  goast.NewIdent(node.Link.GoName),
		Args: []goast.Expr{props, slots},
	}
	render := &goast.CallExpr{Fun: goSelectorExpr(call, "Render"), Args: []goast.Expr{goast.NewIdent("w")}}
	return []goast.Stmt{goWriteSSRErr(render)}, nil
}

func (e *ssrGoEmitter) slot(node *SSRPlanSlot) ([]goast.Stmt, error) {
	field := goSelectorExpr(goast.NewIdent("slots"), ssrGoExportName(node.Name))
	call := &goast.CallExpr{Fun: field, Args: []goast.Expr{goast.NewIdent("w")}}
	filled := &goast.IfStmt{
		Cond: &goast.BinaryExpr{X: field, Op: token.NEQ, Y: goast.NewIdent("nil")},
		Body: &goast.BlockStmt{List: []goast.Stmt{goWriteSSRErr(call)}},
	}
	if len(node.Fallback) > 0 {
		fallback, err := e.nodes(node.Fallback)
		if err != nil {
			return nil, err
		}
		filled.Else = &goast.BlockStmt{List: fallback}
	}
	return []goast.Stmt{filled}, nil
}

func (e *ssrGoEmitter) text(node *SSRPlanText) ([]goast.Stmt, error) {
	body := make([]goast.Stmt, 0, len(node.Parts))
	for _, part := range node.Parts {
		value, err := e.outputValue(part.Static, part.Value, part.Expr)
		if err != nil {
			return nil, err
		}
		body = append(body, goWriteSSRValue(value))
	}
	return body, nil
}

func (e *ssrGoEmitter) element(node *SSRPlanElement) ([]goast.Stmt, error) {
	body := make([]goast.Stmt, 0, len(node.Attrs)+len(node.Children)+2)
	open := "<" + node.Tag
	classStatic, classDirectives, styleStatic, styleDirectives := ssrElementDirectives(node.Attrs)
	if len(classDirectives) > 0 {
		open += ` class="` + html.EscapeString(classStatic)
		body = append(body, goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(open)}))
		for _, attr := range classDirectives {
			if attr.Expr == nil {
				return nil, ssrExprUnsupported(attr.Span.Start, "class directive %q has no cached expression", attr.Name)
			}
			condition, err := TranslateSSRExpression(attr.Expr, e.ctx)
			if err != nil {
				return nil, err
			}
			body = append(body, &goast.IfStmt{
				Cond: condition,
				Body: &goast.BlockStmt{List: []goast.Stmt{
					goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(" " + html.EscapeString(attr.Name))}),
				}},
			})
		}
		open = `"`
	}
	if len(styleDirectives) > 0 {
		open += ` style="` + html.EscapeString(ssrStylePrefix(styleStatic))
		for _, attr := range styleDirectives {
			if attr.Expr == nil {
				return nil, ssrExprUnsupported(attr.Span.Start, "style directive %q has no cached expression", attr.Name)
			}
			open += html.EscapeString(attr.Name) + `:`
			body = append(body, goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(open)}))
			value, err := e.outputValue(false, "", attr.Expr)
			if err != nil {
				return nil, err
			}
			body = append(body, goWriteSSRValue(value))
			open = ";"
		}
		open += `"`
	}
	for _, attr := range node.Attrs {
		if attr.Role == SSRAttrClassDirective || attr.Role == SSRAttrStyleDirective ||
			(attr.Role == SSRAttrStatic && ((attr.Name == "class" && len(classDirectives) > 0) || (attr.Name == "style" && len(styleDirectives) > 0))) {
			continue
		}
		switch attr.Role {
		case SSRAttrStatic:
			open += " " + attr.Name
			if attr.ValueKind != AttrValueMissing {
				open += "=\"" + html.EscapeString(attr.Value) + "\""
			}
		case SSRAttrEvent:
			continue
		case SSRAttrTwoWay:
			return nil, ssrExprUnsupported(attr.Span.Start, "bind:%s requires hydration; use :%s for server-only output", attr.Name, attr.Name)
		case SSRAttrBindThis:
			return nil, ssrExprUnsupported(attr.Span.Start, "bind:this requires hydration and has no server HTML representation")
		case SSRAttrDynamic:
			if attr.Expr == nil {
				return nil, ssrExprUnsupported(attr.Span.Start, "dynamic attribute %q has no cached expression", attr.Name)
			}
			open += " " + attr.Name + "=\""
			body = append(body, goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(open)}))
			value, err := e.outputValue(false, "", attr.Expr)
			if err != nil {
				return nil, err
			}
			body = append(body, goWriteSSRValue(value))
			open = "\""
		default:
			return nil, ssrExprUnsupported(attr.Span.Start, "attribute %q with role %d is not lowered yet", attr.Name, attr.Role)
		}
	}
	if node.EventKey != "" {
		open += ` data-vela-event="` + node.EventKey + `"`
	}
	open += ">"
	body = append(body, goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(open)}))
	children, err := e.nodes(node.Children)
	if err != nil {
		return nil, err
	}
	body = append(body, children...)
	if !ssrVoidElement(node.Tag) {
		body = append(body, goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote("</" + node.Tag + ">")}))
	}
	return body, nil
}

// ssrElementDirectives separates directive attributes from their ordinary
// static class/style counterparts. The emitter serializes both forms into one
// HTML attribute, which is the server equivalent of the client classList and
// style.setProperty updates.
func ssrElementDirectives(attrs []SSRPlanAttr) (classStatic string, classDirectives []SSRPlanAttr, styleStatic string, styleDirectives []SSRPlanAttr) {
	for _, attr := range attrs {
		switch attr.Role {
		case SSRAttrClassDirective:
			classDirectives = append(classDirectives, attr)
		case SSRAttrStyleDirective:
			styleDirectives = append(styleDirectives, attr)
		case SSRAttrStatic:
			switch attr.Name {
			case "class":
				classStatic = attr.Value
			case "style":
				styleStatic = attr.Value
			}
		}
	}
	return classStatic, classDirectives, styleStatic, styleDirectives
}

func ssrStylePrefix(style string) string {
	style = strings.TrimSpace(style)
	if style == "" || strings.HasSuffix(style, ";") {
		return style
	}
	return style + ";"
}

func (e *ssrGoEmitter) ifBlock(node *SSRPlanIf) ([]goast.Stmt, error) {
	if node.Key != nil {
		if len(node.Branches) != 1 || node.Branches[0].Condition != nil {
			return nil, ssrExprUnsupported(node.Span.Start, "SSR key block has an invalid branch shape")
		}
		return e.nodes(node.Branches[0].Children)
	}
	var first *goast.IfStmt
	var previous *goast.IfStmt
	for _, branch := range node.Branches {
		body, err := e.nodes(branch.Children)
		if err != nil {
			return nil, err
		}
		if branch.Condition == nil {
			if previous == nil {
				return nil, ssrExprUnsupported(branch.Span.Start, "SSR if block has an unconditional first branch")
			}
			previous.Else = &goast.BlockStmt{List: body}
			continue
		}
		condition, err := TranslateSSRExpression(branch.Condition, e.ctx)
		if err != nil {
			return nil, err
		}
		current := &goast.IfStmt{Cond: condition, Body: &goast.BlockStmt{List: body}}
		if first == nil {
			first = current
		} else {
			previous.Else = current
		}
		previous = current
	}
	if first == nil {
		return nil, ssrExprUnsupported(node.Span.Start, "SSR if block has no conditional branch")
	}
	return []goast.Stmt{first}, nil
}

func (e *ssrGoEmitter) eachBlock(node *SSRPlanEach) ([]goast.Stmt, error) {
	iterable, err := TranslateSSRExpression(node.Iterable, e.ctx)
	if err != nil {
		return nil, err
	}
	id := e.eachNext
	e.eachNext++
	iterName := "ssrEach" + itoa(id)
	itemName := "ssrEachItem" + itoa(id)
	indexName := "_"
	if node.IndexName != "" {
		indexName = "ssrEachIndex" + itoa(id)
	}
	iterIdent := goast.NewIdent(iterName)
	itemIdent := goast.NewIdent(itemName)
	child := *e
	child.ctx = ssrContextWithBindings(e.ctx)
	child.ctx.Bindings[node.ItemName] = itemIdent
	if node.IndexName != "" {
		child.ctx.Bindings[node.IndexName] = goast.NewIdent(indexName)
	}
	children, err := child.nodes(node.Children)
	if err != nil {
		return nil, err
	}
	e.eachNext = child.eachNext
	e.needsEscaping = e.needsEscaping || child.needsEscaping
	rangeStmt := &goast.RangeStmt{
		Key:   goast.NewIdent(indexName),
		Value: itemIdent,
		Tok:   token.DEFINE,
		X:     iterIdent,
		Body:  &goast.BlockStmt{List: children},
	}
	assign := &goast.AssignStmt{Lhs: []goast.Expr{iterIdent}, Tok: token.DEFINE, Rhs: []goast.Expr{iterable}}
	if len(node.ElseChildren) == 0 {
		return []goast.Stmt{assign, rangeStmt}, nil
	}
	otherwise, err := e.nodes(node.ElseChildren)
	if err != nil {
		return nil, err
	}
	empty := &goast.BinaryExpr{X: &goast.CallExpr{Fun: goast.NewIdent("len"), Args: []goast.Expr{iterIdent}}, Op: token.EQL, Y: &goast.BasicLit{Kind: token.INT, Value: "0"}}
	return []goast.Stmt{assign, &goast.IfStmt{
		Cond: empty,
		Body: &goast.BlockStmt{List: otherwise},
		Else: &goast.BlockStmt{List: []goast.Stmt{rangeStmt}},
	}}, nil
}

func ssrContextWithBindings(base SSRExprContext) SSRExprContext {
	bindings := make(map[string]goast.Expr, len(base.Bindings))
	for name, binding := range base.Bindings {
		bindings[name] = binding
	}
	return SSRExprContext{Props: base.Props, Bindings: bindings, ReactiveNames: base.ReactiveNames}
}

func (e *ssrGoEmitter) outputValue(static bool, literal string, expr *TemplateExpr) (goast.Expr, error) {
	if static {
		return &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(literal)}, nil
	}
	value, err := TranslateSSRExpression(expr, e.ctx)
	if err != nil {
		return nil, err
	}
	e.needsEscaping = true
	return &goast.CallExpr{Fun: goSelector("vela", "EscapeString"), Args: []goast.Expr{
		&goast.CallExpr{Fun: goSelector("vela", "String"), Args: []goast.Expr{value}},
	}}, nil
}

func (e *ssrGoEmitter) awaitBlock(node *SSRPlanAwait) ([]goast.Stmt, error) {
	if len(node.PendingChildren) == 0 {
		return nil, ssrExprUnsupported(node.Span.Start, "SSR await requires an explicit pending branch until hydration")
	}
	return e.nodes(node.PendingChildren)
}
