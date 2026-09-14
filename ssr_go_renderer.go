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
	emitter := ssrGoEmitter{ctx: NewSSRExprContext(plan), packageName: packageName, scopeID: plan.ScopeID, scopeExpr: goast.NewIdent("instanceID"), awaitOptions: goast.NewIdent("options"), componentInstanceSuffixes: plan.ComponentInstanceSuffixes, componentInstanceIdentities: plan.ComponentInstanceIdentities}
	body, err := emitter.nodes(plan.Nodes)
	if err != nil {
		return nil, err
	}
	payload, err := buildSSRHydrationPayload(plan, emitter.scopeExpr, emitter.awaitOptions)
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

func buildSSRHydrationPayload(plan *SSRRenderPlan, componentID, awaitOptions goast.Expr) ([]goast.Stmt, error) {
	if plan == nil {
		return nil, fmt.Errorf("ssr renderer: missing render plan for hydration payload")
	}
	if strings.TrimSpace(plan.ScopeID) == "" || componentID == nil {
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
		{Names: []*goast.Ident{goast.NewIdent("Await")}, Type: &goast.MapType{Key: goast.NewIdent("int"), Value: goast.NewIdent(plan.ComponentName + "AwaitSnapshot")}, Tag: &goast.BasicLit{Kind: token.STRING, Value: "`json:\"await,omitempty\"`"}},
	}
	payloadValue := &goast.CompositeLit{
		Type: &goast.StructType{Fields: &goast.FieldList{List: typeFields}},
		Elts: []goast.Expr{
			&goast.KeyValueExpr{Key: goast.NewIdent("Component"), Value: componentID},
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
	if awaitOptions != nil {
		payloadValue.Elts = append(payloadValue.Elts, &goast.KeyValueExpr{Key: goast.NewIdent("Await"), Value: &goast.SelectorExpr{X: awaitOptions, Sel: goast.NewIdent("Await")}})
	}
	marshal := &goast.CallExpr{Fun: goSelector("json", "Marshal"), Args: []goast.Expr{payloadValue}}
	encode := &goast.AssignStmt{Lhs: []goast.Expr{goast.NewIdent("ssrPayload"), goast.NewIdent("err")}, Tok: token.DEFINE, Rhs: []goast.Expr{marshal}}
	encoded := &goast.IfStmt{Cond: &goast.BinaryExpr{X: goast.NewIdent("err"), Op: token.NEQ, Y: goast.NewIdent("nil")}, Body: &goast.BlockStmt{List: []goast.Stmt{&goast.ReturnStmt{Results: []goast.Expr{goast.NewIdent("err")}}}}}
	open := ssrGoStringConcat(
		&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(`<script type="application/json" id="vela-state-`)},
		&goast.CallExpr{Fun: goSelector("vela", "EscapeString"), Args: []goast.Expr{componentID}},
		&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(`">`)},
	)
	return []goast.Stmt{
		encode,
		encoded,
		goWriteSSRValue(open),
		goWriteSSRValue(&goast.CallExpr{Fun: goast.NewIdent("string"), Args: []goast.Expr{goast.NewIdent("ssrPayload")}}),
		goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote("</script>")}),
	}, nil
}

type ssrGoEmitter struct {
	ctx                         SSRExprContext
	packageName                 string
	scopeID                     string
	scopeExpr                   goast.Expr
	componentInstanceSuffixes   map[SectionSpan]string
	componentInstanceIdentities map[SectionSpan]SSRComponentInstanceIdentity
	awaitOptions                goast.Expr
	needsEscaping               bool
	eachNext                    int
	imports                     map[string]string
	selectValue                 *TemplateExpr
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
		case SSRAttrDynamic, SSRAttrTwoWay:
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
	componentName := node.Link.GoName
	component := goast.Expr(goast.NewIdent(componentName))
	if prefix != "" {
		propsType = goSelector(prefix, node.Link.GoName+"Props")
		slotsType = goSelector(prefix, node.Link.GoName+"Slots")
		component = goSelector(prefix, componentName)
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
	args := []goast.Expr{props, slots}
	suffix := e.componentInstanceSuffixes[node.Span]
	identity, hasIdentity := e.componentInstanceIdentities[node.Span]
	if hasIdentity && identity.RuntimeScoped {
		suffix = ssrRuntimeComponentSuffix(identity)
	}
	if suffix != "" {
		componentName += "At"
		if prefix != "" {
			component = goSelector(prefix, componentName)
		} else {
			component = goast.NewIdent(componentName)
		}
		args = append(args, ssrGoStringConcat(e.scopeExpr, &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote("--" + suffix)}))
	}
	call := &goast.CallExpr{Fun: component, Args: args}
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
	var textareaValue SSRPlanAttr
	hasTextareaValue := false
	childSelectValue := e.selectValue
	var optionValue SSRPlanAttr
	hasOptionValue := false
	hasSelectValueBinding := false
	selectMultiple := false
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
			if node.Tag == "select" && attr.Name == "multiple" {
				selectMultiple = true
			}
			if e.selectValue != nil && node.Tag == "option" && attr.Name == "selected" {
				// bind:value owns the initial selectedness. Leaving a static
				// selected attribute here would make the server DOM disagree
				// with the bound value before hydration can adopt it.
				continue
			}
			if e.selectValue != nil && node.Tag == "option" && attr.Name == "value" {
				if attr.ValueKind == AttrValueMissing {
					return nil, ssrExprUnsupported(attr.Span.Start, "an option under select bind:value requires an explicit value")
				}
				optionValue = attr
				hasOptionValue = true
			}
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
			if attr.Name == "value" && node.Tag == "select" {
				childSelectValue = attr.Expr
				hasSelectValueBinding = true
				continue
			}
			if attr.Name == "value" && node.Tag == "textarea" {
				if len(node.Children) != 0 {
					return nil, ssrExprUnsupported(attr.Span.Start, "textarea bind:value cannot be combined with child content during SSR")
				}
				textareaValue = attr
				hasTextareaValue = true
				continue
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
			// Element references are client-only state. They have no HTML
			// representation, so SSR leaves the attribute out and hydration
			// assigns the already-adopted element through its reactive setter.
			continue
		case SSRAttrDynamic:
			if e.selectValue != nil && node.Tag == "option" && attr.Name == "selected" {
				return nil, ssrExprUnsupported(attr.Span.Start, "option selected cannot be combined with parent select bind:value during SSR")
			}
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
	if hasSelectValueBinding && selectMultiple {
		return nil, ssrExprUnsupported(node.Span.Start, "select multiple bind:value has no safe SSR representation yet")
	}
	if e.selectValue != nil && node.Tag == "option" && !hasOptionValue {
		return nil, ssrExprUnsupported(node.Span.Start, "an option under select bind:value requires an explicit static value")
	}
	if e.selectValue != nil && node.Tag == "option" {
		selected, err := e.selectOptionCondition(e.selectValue, optionValue)
		if err != nil {
			return nil, err
		}
		if open != "" {
			body = append(body, goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(open)}))
			open = ""
		}
		body = append(body, &goast.IfStmt{Cond: selected, Body: &goast.BlockStmt{List: []goast.Stmt{
			goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(" selected")}),
		}}})
	}
	if node.EventKey != "" {
		open += ` data-vela-event="` + node.EventKey + `"`
	}
	open += ">"
	body = append(body, goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(open)}))
	if hasTextareaValue {
		value, err := e.outputValue(false, "", textareaValue.Expr)
		if err != nil {
			return nil, err
		}
		body = append(body, goWriteSSRValue(value))
	}
	previousSelectValue := e.selectValue
	e.selectValue = childSelectValue
	children, err := e.nodes(node.Children)
	e.selectValue = previousSelectValue
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
		key, err := TranslateSSRExpression(node.Key, e.ctx)
		if err != nil {
			return nil, err
		}
		child := *e
		child.scopeExpr = child.runtimeScope("key-"+itoa(node.Span.Start), key)
		children, err := child.nodes(node.Branches[0].Children)
		if err != nil {
			return nil, err
		}
		e.eachNext = child.eachNext
		e.needsEscaping = e.needsEscaping || child.needsEscaping
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
	indexName := "ssrEachIndex" + itoa(id)
	iterIdent := goast.NewIdent(iterName)
	itemIdent := goast.NewIdent(itemName)
	child := *e
	child.ctx = ssrContextWithBindings(e.ctx)
	child.ctx.Bindings[node.ItemName] = itemIdent
	if node.IndexName != "" {
		child.ctx.Bindings[node.IndexName] = goast.NewIdent(indexName)
	}
	runtimeValue := goast.Expr(goast.NewIdent(indexName))
	if node.Key != nil {
		runtimeValue, err = TranslateSSRExpression(node.Key, child.ctx)
		if err != nil {
			return nil, err
		}
	}
	child.scopeExpr = child.runtimeScope("each-"+itoa(node.Span.Start), runtimeValue)
	children, err := child.nodes(node.Children)
	if err != nil {
		return nil, err
	}
	if node.Key != nil && node.DynamicID >= 0 {
		children = append([]goast.Stmt{child.rowMarkerStart(node.DynamicID)}, append(children, child.rowMarkerEnd(node.DynamicID))...)
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

// runtimeScope adds one evaluated structural segment to the component instance
// namespace. vela.String defines the same visible-value conversion already
// used for SSR text and select values.
func (e *ssrGoEmitter) runtimeScope(kind string, value goast.Expr) goast.Expr {
	return ssrGoStringConcat(
		e.scopeExpr,
		&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote("--" + kind + "-")},
		&goast.CallExpr{Fun: goSelector("vela", "String"), Args: []goast.Expr{value}},
	)
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

// selectOptionCondition compares the browser-visible string values. Go SSR
// prop inference can produce several concrete types, while the DOM's
// HTMLSelectElement.value is always a string; vela.String keeps that semantic
// boundary explicit without evaluating source text in the compiler.
func (e *ssrGoEmitter) selectOptionCondition(selectValue *TemplateExpr, optionValue SSRPlanAttr) (goast.Expr, error) {
	selected, err := TranslateSSRExpression(selectValue, e.ctx)
	if err != nil {
		return nil, err
	}
	option := goast.Expr(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(optionValue.Value)})
	return &goast.BinaryExpr{
		X:  &goast.CallExpr{Fun: goSelector("vela", "String"), Args: []goast.Expr{selected}},
		Op: token.EQL,
		Y:  option,
	}, nil
}

func (e *ssrGoEmitter) awaitBlock(node *SSRPlanAwait) ([]goast.Stmt, error) {
	if len(node.PendingChildren) == 0 {
		return nil, ssrExprUnsupported(node.Span.Start, "SSR await requires an explicit pending branch until hydration")
	}
	pending, err := e.nodes(node.PendingChildren)
	if err != nil {
		return nil, err
	}
	if node.DynamicID < 0 || e.awaitOptions == nil {
		return e.markedStatements(node.DynamicID, pending), nil
	}

	snapshot := "ssrAwaitSnapshot" + itoa(node.DynamicID)
	thenBody, thenStatic := e.awaitSnapshotBranch(node.ThenChildren, node.ValueName, &goast.SelectorExpr{X: goast.NewIdent(snapshot), Sel: goast.NewIdent("Value")})
	catchBody, catchStatic := e.awaitSnapshotBranch(node.CatchChildren, node.ErrorName, &goast.SelectorExpr{X: goast.NewIdent(snapshot), Sel: goast.NewIdent("Error")})
	if e.imports == nil {
		e.imports = map[string]string{}
	}
	e.imports["fmt"] = "fmt"
	state := &goast.SelectorExpr{X: goast.NewIdent(snapshot), Sel: goast.NewIdent("State")}
	unsupported := func(branch string) goast.Stmt {
		return &goast.ReturnStmt{Results: []goast.Expr{&goast.CallExpr{Fun: goSelector("fmt", "Errorf"), Args: []goast.Expr{
			&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote("SSR await snapshot " + itoa(node.DynamicID) + " " + branch + " branch requires static template content")},
		}}}}
	}
	thenCase := []goast.Stmt{unsupported("then")}
	if thenStatic {
		thenCase = thenBody
	}
	catchCase := []goast.Stmt{unsupported("catch")}
	if catchStatic {
		catchCase = catchBody
	}
	invalid := &goast.ReturnStmt{Results: []goast.Expr{&goast.CallExpr{Fun: goSelector("fmt", "Errorf"), Args: []goast.Expr{
		&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote("SSR await snapshot " + itoa(node.DynamicID) + " has an invalid state")},
	}}}}
	body := []goast.Stmt{
		&goast.AssignStmt{Lhs: []goast.Expr{goast.NewIdent(snapshot)}, Tok: token.DEFINE, Rhs: []goast.Expr{&goast.IndexExpr{X: &goast.SelectorExpr{X: e.awaitOptions, Sel: goast.NewIdent("Await")}, Index: &goast.BasicLit{Kind: token.INT, Value: itoa(node.DynamicID)}}}},
		&goast.SwitchStmt{Tag: state, Body: &goast.BlockStmt{List: []goast.Stmt{
			&goast.CaseClause{List: []goast.Expr{&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote("")}, &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote("pending")}}, Body: pending},
			&goast.CaseClause{List: []goast.Expr{&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote("then")}}, Body: thenCase},
			&goast.CaseClause{List: []goast.Expr{&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote("catch")}}, Body: catchCase},
			&goast.CaseClause{Body: []goast.Stmt{invalid}},
		}}},
	}
	return e.markedStatements(node.DynamicID, body), nil
}

// awaitSnapshotBranch admits static content plus direct text and one-way
// attribute interpolation of the declared then/catch binding. Its value stays
// a string because a Go data loader must not smuggle an untyped JavaScript
// object into a server renderer. Ordinary DOM events are emitted as inert
// server markup and attached by the branch-owned hydrator. Native value and
// checked bindings use the same branch-owned lifecycle. Components and
// structural descendants are intentionally deferred until their settled-branch
// hydration ownership exists.
func (e *ssrGoEmitter) awaitSnapshotBranch(nodes []SSRPlanNode, binding string, value goast.Expr) ([]goast.Stmt, bool) {
	if len(nodes) == 0 {
		return nil, false
	}
	if !ssrAwaitSnapshotLeafBranch(nodes, binding) {
		return nil, false
	}
	previous := e.ctx
	e.ctx = ssrContextWithBindings(e.ctx)
	if binding != "" {
		e.ctx.Bindings[binding] = value
	}
	body, err := e.nodes(nodes)
	e.ctx = previous
	if err != nil {
		return nil, false
	}
	return body, true
}

func ssrAwaitSnapshotLeafBranch(nodes []SSRPlanNode, binding string) bool {
	for _, node := range nodes {
		switch current := node.(type) {
		case *SSRPlanText:
			for _, part := range current.Parts {
				if part.Static {
					continue
				}
				name, ok := templateExprIdent(part.Expr)
				if !ok || name != binding {
					return false
				}
			}
		case *SSRPlanElement:
			for _, attr := range current.Attrs {
				if attr.Role == SSRAttrStatic {
					continue
				}
				if attr.Role == SSRAttrEvent {
					continue
				}
				if attr.Role == SSRAttrTwoWay {
					if attr.Name == "value" || attr.Name == "checked" {
						continue
					}
					return false
				}
				if attr.Role != SSRAttrDynamic {
					return false
				}
				name, ok := templateExprIdent(attr.Expr)
				if !ok || name != binding {
					return false
				}
			}
			if !ssrAwaitSnapshotLeafBranch(current.Children, binding) {
				return false
			}
		case *SSRPlanComponent:
			if current.Link == nil || len(current.Slots) != 0 {
				return false
			}
			for _, attr := range current.Attrs {
				if attr.Role == SSRAttrStatic {
					continue
				}
				if attr.Role != SSRAttrDynamic || attr.Expr == nil {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
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
	return goWriteSSRValue(e.markerValue("<!--v:", ":"+itoa(id)+"-->"))
}

func (e *ssrGoEmitter) markerEnd(id int) goast.Stmt {
	return goWriteSSRValue(e.markerValue("<!--/v:", ":"+itoa(id)+"-->"))
}

func (e *ssrGoEmitter) elementMarkerStart(id int) goast.Stmt {
	return goWriteSSRValue(e.markerValue("<!--e:", ":"+itoa(id)+"-->"))
}

func (e *ssrGoEmitter) elementMarkerEnd(id int) goast.Stmt {
	return goWriteSSRValue(e.markerValue("<!--/e:", ":"+itoa(id)+"-->"))
}

// Keyed rows need an ownership boundary distinct from element markers: one
// logical row may contain static siblings, child roots, and payload scripts.
// The comments stay inside the enclosing each range and are intentionally
// scope-qualified like every other SSR marker.
func (e *ssrGoEmitter) rowMarkerStart(id int) goast.Stmt {
	return goWriteSSRValue(e.markerValue("<!--r:", ":"+itoa(id)+"-->"))
}

func (e *ssrGoEmitter) rowMarkerEnd(id int) goast.Stmt {
	return goWriteSSRValue(e.markerValue("<!--/r:", ":"+itoa(id)+"-->"))
}

func (e *ssrGoEmitter) markerValue(prefix, suffix string) goast.Expr {
	scope := e.scopeExpr
	if scope == nil {
		scope = &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(e.scopeID)}
	}
	return ssrGoStringConcat(
		&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(prefix)},
		scope,
		&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(suffix)},
	)
}

func ssrGoStringConcat(parts ...goast.Expr) goast.Expr {
	if len(parts) == 0 {
		return &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote("")}
	}
	result := parts[0]
	for _, part := range parts[1:] {
		result = &goast.BinaryExpr{X: result, Op: token.ADD, Y: part}
	}
	return result
}
