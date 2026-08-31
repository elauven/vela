package main

import (
	"fmt"
	goast "go/ast"
	"go/token"
	"html"
	"sort"
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
	emitter := ssrGoEmitter{ctx: NewSSRExprContext(plan), packageName: packageName, scopeID: plan.ScopeID}
	body, err := emitter.nodes(plan.Nodes)
	if err != nil {
		return nil, err
	}
	payload, err := buildSSRHydrationPayload(plan)
	if err != nil {
		return nil, err
	}
	body = append(body, payload...)
	body = append(body, &goast.ReturnStmt{Results: []goast.Expr{goast.NewIdent("nil")}})
	render, err := ssrRenderFunc(file, plan.ComponentName)
	if err != nil {
		return nil, err
	}
	render.Body.List = body
	appendSSRGoImports(file, map[string]string{"encoding/json": "json"})
	appendSSRGoImports(file, emitter.imports)
	return file, nil
}

func buildSSRHydrationPayload(plan *SSRRenderPlan) ([]goast.Stmt, error) {
	if plan == nil {
		return nil, fmt.Errorf("ssr renderer: missing render plan for hydration payload")
	}
	componentID := strings.TrimSpace(plan.ScopeID)
	if componentID == "" {
		return nil, fmt.Errorf("ssr renderer: missing component scope ID for hydration payload")
	}
	stateJSON := plan.Hydration.JSON
	if stateJSON == "" {
		stateJSON = "{}"
	}
	typeFields := []*goast.Field{
		{Names: []*goast.Ident{goast.NewIdent("Component")}, Type: goast.NewIdent("string"), Tag: &goast.BasicLit{Kind: token.STRING, Value: "`json:\"component\"`"}},
		{Names: []*goast.Ident{goast.NewIdent("Props")}, Type: goast.NewIdent(plan.ComponentName + "Props"), Tag: &goast.BasicLit{Kind: token.STRING, Value: "`json:\"props\"`"}},
		{Names: []*goast.Ident{goast.NewIdent("State")}, Type: goSelector("json", "RawMessage"), Tag: &goast.BasicLit{Kind: token.STRING, Value: "`json:\"state\"`"}},
		{Names: []*goast.Ident{goast.NewIdent("Deep")}, Type: &goast.ArrayType{Elt: goast.NewIdent("string")}, Tag: &goast.BasicLit{Kind: token.STRING, Value: "`json:\"deep,omitempty\"`"}},
		{Names: []*goast.Ident{goast.NewIdent("Incomplete")}, Type: goast.NewIdent("bool"), Tag: &goast.BasicLit{Kind: token.STRING, Value: "`json:\"incomplete\"`"}},
		{Names: []*goast.Ident{goast.NewIdent("Omitted")}, Type: &goast.ArrayType{Elt: goast.NewIdent("string")}, Tag: &goast.BasicLit{Kind: token.STRING, Value: "`json:\"omitted,omitempty\"`"}},
	}
	payloadValue := &goast.CompositeLit{
		Type: &goast.StructType{Fields: &goast.FieldList{List: typeFields}},
		Elts: []goast.Expr{
			&goast.KeyValueExpr{Key: goast.NewIdent("Component"), Value: &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(componentID)}},
			&goast.KeyValueExpr{Key: goast.NewIdent("Props"), Value: goast.NewIdent("props")},
			&goast.KeyValueExpr{Key: goast.NewIdent("State"), Value: &goast.CallExpr{Fun: goSelector("json", "RawMessage"), Args: []goast.Expr{&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(stateJSON)}}}},
			&goast.KeyValueExpr{Key: goast.NewIdent("Incomplete"), Value: goast.NewIdent(strconv.FormatBool(plan.Hydration.Incomplete))},
		},
	}
	if len(plan.Hydration.Omitted) > 0 {
		omitted := make([]goast.Expr, 0, len(plan.Hydration.Omitted))
		for _, name := range plan.Hydration.Omitted {
			omitted = append(omitted, &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(name)})
		}
		payloadValue.Elts = append(payloadValue.Elts, &goast.KeyValueExpr{Key: goast.NewIdent("Omitted"), Value: &goast.CompositeLit{Type: &goast.ArrayType{Elt: goast.NewIdent("string")}, Elts: omitted}})
	}
	if len(plan.Hydration.Deep) > 0 {
		deep := make([]goast.Expr, 0, len(plan.Hydration.Deep))
		for _, name := range plan.Hydration.Deep {
			deep = append(deep, &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(name)})
		}
		payloadValue.Elts = append(payloadValue.Elts, &goast.KeyValueExpr{Key: goast.NewIdent("Deep"), Value: &goast.CompositeLit{Type: &goast.ArrayType{Elt: goast.NewIdent("string")}, Elts: deep}})
	}
	marshal := &goast.CallExpr{Fun: goSelector("json", "Marshal"), Args: []goast.Expr{payloadValue}}
	encode := &goast.AssignStmt{Lhs: []goast.Expr{goast.NewIdent("ssrPayload"), goast.NewIdent("err")}, Tok: token.DEFINE, Rhs: []goast.Expr{marshal}}
	encoded := &goast.IfStmt{Cond: &goast.BinaryExpr{X: goast.NewIdent("err"), Op: token.NEQ, Y: goast.NewIdent("nil")}, Body: &goast.BlockStmt{List: []goast.Stmt{&goast.ReturnStmt{Results: []goast.Expr{goast.NewIdent("err")}}}}}
	open := `<script type="application/json" id="vela-state-` + html.EscapeString(componentID) + `">`
	return []goast.Stmt{
		encode,
		encoded,
		goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(open)}),
		goWriteSSRValue(&goast.CallExpr{Fun: goast.NewIdent("string"), Args: []goast.Expr{goast.NewIdent("ssrPayload")}}),
		goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote("</script>")}),
	}, nil
}

type ssrGoEmitter struct {
	ctx           SSRExprContext
	packageName   string
	scopeID       string
	needsEscaping bool
	eachNext      int
	imports       map[string]string
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
	prefix, err := e.componentPackagePrefix(node.Link)
	if err != nil {
		return nil, err
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
	propsType := goast.Expr(goast.NewIdent(node.Link.GoName + "Props"))
	slotsType := goast.Expr(goast.NewIdent(node.Link.GoName + "Slots"))
	component := goast.Expr(goast.NewIdent(node.Link.GoName))
	if prefix != "" {
		propsType = goSelector(prefix, node.Link.GoName+"Props")
		slotsType = goSelector(prefix, node.Link.GoName+"Slots")
		component = goSelector(prefix, node.Link.GoName)
	}
	props := &goast.CompositeLit{Type: propsType, Elts: make([]goast.Expr, len(fields))}
	for i, field := range fields {
		props.Elts[i] = field
	}
	slots := &goast.CompositeLit{Type: slotsType}
	slotNames := make([]string, 0, len(node.Slots))
	for name := range node.Slots {
		slotNames = append(slotNames, name)
	}
	sort.Strings(slotNames)
	for _, name := range slotNames {
		children := node.Slots[name]
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
		Fun:  component,
		Args: []goast.Expr{props, slots},
	}
	render := &goast.CallExpr{Fun: goSelectorExpr(call, "Render"), Args: []goast.Expr{goast.NewIdent("w")}}
	return []goast.Stmt{goWriteSSRErr(render)}, nil
}

func (e *ssrGoEmitter) componentPackagePrefix(link *SSRComponentLink) (string, error) {
	if link == nil {
		return "", fmt.Errorf("ssr renderer: missing component link")
	}
	if link.PackageName == e.packageName {
		return "", nil
	}
	if link.ImportPath == "" {
		return "", ssrExprUnsupported(0, "component %q has no generated Go import path", link.LocalName)
	}
	if e.imports == nil {
		e.imports = map[string]string{}
	}
	if alias, exists := e.imports[link.ImportPath]; exists {
		return alias, nil
	}
	base := link.PackageName
	alias := base
	for suffix := 2; ; suffix++ {
		collision := false
		for _, existing := range e.imports {
			if existing == alias {
				collision = true
				break
			}
		}
		if !collision {
			break
		}
		alias = base + "_" + strconv.Itoa(suffix)
	}
	e.imports[link.ImportPath] = alias
	return alias, nil
}

func appendSSRGoImports(file *goast.File, imports map[string]string) {
	if file == nil || len(imports) == 0 {
		return
	}
	paths := make([]string, 0, len(imports))
	for importPath := range imports {
		paths = append(paths, importPath)
	}
	sort.Strings(paths)
	for _, declaration := range file.Decls {
		importDecl, ok := declaration.(*goast.GenDecl)
		if !ok || importDecl.Tok != token.IMPORT {
			continue
		}
		for _, importPath := range paths {
			importDecl.Specs = append(importDecl.Specs, &goast.ImportSpec{
				Name: goast.NewIdent(imports[importPath]),
				Path: &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(importPath)},
			})
		}
		return
	}
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
	body := make([]goast.Stmt, 0, len(node.Parts)*3)
	for _, part := range node.Parts {
		if !part.Static {
			body = append(body, e.markerStart(part.DynamicID))
		}
		value, err := e.outputValue(part.Static, part.Value, part.Expr)
		if err != nil {
			return nil, err
		}
		body = append(body, goWriteSSRValue(value))
		if !part.Static {
			body = append(body, e.markerEnd(part.DynamicID))
		}
	}
	return body, nil
}

func (e *ssrGoEmitter) element(node *SSRPlanElement) ([]goast.Stmt, error) {
	body := make([]goast.Stmt, 0, len(node.Attrs)+len(node.Children)+2)
	body = append(body, e.elementMarkerStart(node.HydrationID))
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
			// bind:value and bind:checked have faithful server forms. The
			// boolean form must be emitted conditionally: checked="false" is
			// still true in HTML. Hydration installs the browser listener.
			if attr.Name != "value" && attr.Name != "checked" {
				return nil, ssrExprUnsupported(attr.Span.Start, "bind:%s has no safe SSR representation yet", attr.Name)
			}
			if attr.Expr == nil {
				return nil, ssrExprUnsupported(attr.Span.Start, "bind:%s has no cached expression", attr.Name)
			}
			if attr.Name == "checked" {
				condition, err := TranslateSSRExpression(attr.Expr, e.ctx)
				if err != nil {
					return nil, err
				}
				if open != "" {
					body = append(body, goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(open)}))
					open = ""
				}
				body = append(body, &goast.IfStmt{Cond: condition, Body: &goast.BlockStmt{List: []goast.Stmt{
					goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(" checked")}),
				}}})
				continue
			}
			open += " value=\""
			body = append(body, goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(open)}))
			value, err := e.outputValue(false, "", attr.Expr)
			if err != nil {
				return nil, err
			}
			body = append(body, goWriteSSRValue(value))
			open = "\""
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
	body = append(body, e.elementMarkerEnd(node.HydrationID))
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
	var body []goast.Stmt
	if node.DynamicID >= 0 {
		body = append(body, e.markerStart(node.DynamicID))
	}
	if node.Key != nil {
		if len(node.Branches) != 1 || node.Branches[0].Condition != nil {
			return nil, ssrExprUnsupported(node.Span.Start, "SSR key block has an invalid branch shape")
		}
		children, err := e.nodes(node.Branches[0].Children)
		if err != nil {
			return nil, err
		}
		body = append(body, children...)
		if node.DynamicID >= 0 {
			body = append(body, e.markerEnd(node.DynamicID))
		}
		return body, nil
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
	body = append(body, first)
	if node.DynamicID >= 0 {
		body = append(body, e.markerEnd(node.DynamicID))
	}
	return body, nil
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
		return e.markedStatements(node.DynamicID, []goast.Stmt{assign, rangeStmt}), nil
	}
	otherwise, err := e.nodes(node.ElseChildren)
	if err != nil {
		return nil, err
	}
	empty := &goast.BinaryExpr{X: &goast.CallExpr{Fun: goast.NewIdent("len"), Args: []goast.Expr{iterIdent}}, Op: token.EQL, Y: &goast.BasicLit{Kind: token.INT, Value: "0"}}
	return e.markedStatements(node.DynamicID, []goast.Stmt{assign, &goast.IfStmt{
		Cond: empty,
		Body: &goast.BlockStmt{List: otherwise},
		Else: &goast.BlockStmt{List: []goast.Stmt{rangeStmt}},
	}}), nil
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
	body, err := e.nodes(node.PendingChildren)
	if err != nil {
		return nil, err
	}
	return e.markedStatements(node.DynamicID, body), nil
}

func (e *ssrGoEmitter) markedStatements(id int, body []goast.Stmt) []goast.Stmt {
	if id < 0 {
		return body
	}
	marked := make([]goast.Stmt, 0, len(body)+2)
	marked = append(marked, e.markerStart(id))
	marked = append(marked, body...)
	marked = append(marked, e.markerEnd(id))
	return marked
}

func (e *ssrGoEmitter) markerStart(id int) goast.Stmt {
	return goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote("<!--v:" + e.scopeID + ":" + itoa(id) + "-->")})
}

func (e *ssrGoEmitter) markerEnd(id int) goast.Stmt {
	return goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote("<!--/v:" + e.scopeID + ":" + itoa(id) + "-->")})
}

func (e *ssrGoEmitter) elementMarkerStart(id int) goast.Stmt {
	return goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote("<!--e:" + e.scopeID + ":" + itoa(id) + "-->")})
}

func (e *ssrGoEmitter) elementMarkerEnd(id int) goast.Stmt {
	return goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote("<!--/e:" + e.scopeID + ":" + itoa(id) + "-->")})
}
