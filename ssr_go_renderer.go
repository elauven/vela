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

// SSRHydrationPayloadVersion is the schema version emitted into generated SSR
// state payloads. Browser runtimes reject a different explicit version before
// attempting DOM adoption, so independently deployed server and client
// artifacts cannot silently hydrate against incompatible metadata.
const SSRHydrationPayloadVersion = 1

// BuildSSRGoFile emits the currently supported server render plan through
// standard Go AST nodes. It supports static elements, escaped text and
// attributes, and conditional template branches. Other plan node kinds keep
// producing explicit diagnostics until their renderer semantics exist.
func BuildSSRGoFile(plan *SSRRenderPlan, packageName string) (*goast.File, error) {
	return buildSSRGoFile(plan, packageName, true, newSSRRegion(plan))
}

// buildSSRGoRendererWithRegion is BuildSSRGoRendererFile that also returns
// the region site records, filled whether or not the renderer succeeds.
func buildSSRGoRendererWithRegion(plan *SSRRenderPlan, packageName, filename, source string) (*goast.File, *SSRRegion, error) {
	region := newSSRRegion(plan)
	region.File, region.source = filename, source
	file, err := buildSSRGoFile(plan, packageName, false, region)
	return file, region, err
}

func buildSSRGoFile(plan *SSRRenderPlan, packageName string, includeProps bool, region *SSRRegion) (*goast.File, error) {
	return buildSSRGoBlocksFile(plan, packageName, includeProps, region)
}

func (e *ssrGoEmitter) propDefaults(props []SSRPlanProp) ([]goast.Stmt, error) {
	var out []goast.Stmt
	for _, prop := range props {
		if prop.Default == nil {
			continue
		}
		source := -1
		if e.region != nil {
			source = e.region.ScriptOffset + prop.Default.NodePos()
		}
		value, err := e.translateJS("prop-default", prop.SourceName, prop.Default, 0, source, e.ctx)
		if err != nil {
			return nil, fmt.Errorf("SSR default prop %q: %w", prop.SourceName, err)
		}
		field := &goast.SelectorExpr{X: goast.NewIdent("props"), Sel: goast.NewIdent(prop.GoName)}
		assign := goast.Stmt(&goast.AssignStmt{Lhs: []goast.Expr{field}, Tok: token.ASSIGN, Rhs: []goast.Expr{value}})
		if !ssrDefaultFitsField(prop.Type, e.facts[prop.Default]) {
			// The value has no proven Go type of the field (an object literal is
			// a *vela.Object; a preserved sidecar may declare its own type).
			assign = goWriteSSRErr(&goast.CallExpr{Fun: goSelector("vela", "AssignProp"), Args: []goast.Expr{
				&goast.UnaryExpr{Op: token.AND, X: field}, value, ssrGoString(e.componentNameOrPlan() + "." + prop.SourceName),
			}})
		}
		name := &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(prop.SourceName)}
		// props.Set != nil && !props.Set["name"]: the presence contract of
		// vela.PropProvided without reflection (task 060). A nil Set means
		// every prop was provided.
		set := goSelector("props", "Set")
		missing := goast.Expr(&goast.BinaryExpr{
			X:  &goast.BinaryExpr{X: set, Op: token.NEQ, Y: goast.NewIdent("nil")},
			Op: token.LAND,
			Y:  &goast.UnaryExpr{Op: token.NOT, X: &goast.IndexExpr{X: goSelector("props", "Set"), Index: name}},
		})
		if e.propsWithoutPresence {
			missing = &goast.UnaryExpr{Op: token.NOT, X: &goast.CallExpr{Fun: goSelector("vela", "PropProvided"), Args: []goast.Expr{goast.NewIdent("props"), name}}}
		}
		out = append(out, &goast.IfStmt{Cond: missing, Body: &goast.BlockStmt{List: []goast.Stmt{assign}}})
	}
	return out, nil
}

type ssrGoEmitter struct {
	ctx SSRExprContext
	// propsWithoutPresence: SSRRenderPlan.PropsWithoutPresence.
	propsWithoutPresence bool
	packageName          string
	scopeID              string
	elementScope         string
	awaitOptions         goast.Expr
	needsEscaping        bool
	eachNext             int
	imports              map[string]string
	selectValue          *TemplateExpr
	selectMultiple       bool
	componentName        string
	rootElement          *SSRPlanElement // single root element; nil with a wrapper root
	// region records the sites this renderer lowers (ssr_region.go); nil
	// for callers without a region. speculative counts enclosing branches
	// whose failure has a fallback. facts holds the IR facts of each
	// translated expression for conditions.
	region      *SSRRegion
	speculative int
	facts       map[JsExpr]ssrIRFacts
}

func (e *ssrGoEmitter) nodes(nodes []SSRPlanNode) ([]goast.Stmt, error) {
	body := make([]goast.Stmt, 0, len(nodes))
	for _, node := range nodes {
		throwing := e.throwing()
		statements, err := e.node(node)
		if err != nil {
			return nil, err
		}
		body = append(body, statements...)
		// A JavaScript exception raised in this node stops the render before
		// the next node (T-6.5a).
		if e.throwing() > throwing {
			body = append(body, ssrJSCheck())
		}
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
	case *SSRPlanClient:
		return e.clientBlock(n)
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
	fields := make([]*goast.KeyValueExpr, 0, len(node.Attrs)+1)
	presence := make([]goast.Expr, 0, len(node.Attrs))
	// A value of unproven type goes through vela.AssignProp, which converts it
	// to the child's declared Go field type (T-5.1) or fails the render.
	var assigns []goast.Stmt
	propsThrowing := e.throwing()
	for _, attr := range node.Attrs {
		var value goast.Expr
		switch attr.Role {
		case SSRAttrEvent:
			// Component events are a browser lifecycle concern. They have no
			// serializable server prop and attach after hydration adoption.
			continue
		case SSRAttrStatic:
			if attr.ValueKind == AttrValueMissing {
				return nil, ssrExprUnsupported(attr.Span.Start, "component prop %q requires a value", attr.Name)
			}
			value = &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(attr.Value)}
		case SSRAttrDynamic, SSRAttrTwoWay:
			var err error
			value, err = e.translate("component-prop", node.Name+"."+attr.Name, attr.Expr, e.ctx)
			if err != nil {
				return nil, err
			}
			if facts := e.facts[attr.Expr.Root()]; facts.SemanticType() == "unknown" && !facts.GoString {
				field := &goast.UnaryExpr{Op: token.AND, X: goSelectorExpr(goast.NewIdent("ssrChildProps"), ssrGoExportName(attr.Name))}
				assigns = append(assigns, goWriteSSRErr(&goast.CallExpr{Fun: goSelector("vela", "AssignProp"), Args: []goast.Expr{
					field, value, &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(node.Name + "." + attr.Name)},
				}}))
				presence = append(presence, &goast.KeyValueExpr{Key: &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(attr.Name)}, Value: goast.NewIdent("true")})
				continue
			}
		default:
			return nil, ssrExprUnsupported(attr.Span.Start, "component prop %q with role %d requires hydration", attr.Name, attr.Role)
		}
		fields = append(fields, &goast.KeyValueExpr{Key: goast.NewIdent(ssrGoExportName(attr.Name)), Value: value})
		presence = append(presence, &goast.KeyValueExpr{
			Key:   &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(attr.Name)},
			Value: goast.NewIdent("true"),
		})
	}
	// Child defaults depend on source-level prop presence, not on a Go zero
	// value. Always initialize Set for compiler-generated calls: an empty map
	// means every child prop is absent, while a key retains an explicit empty,
	// false, zero, or admitted null value.
	fields = append(fields, &goast.KeyValueExpr{
		Key: goast.NewIdent("Set"),
		Value: &goast.CompositeLit{
			Type: &goast.MapType{Key: goast.NewIdent("string"), Value: goast.NewIdent("bool")},
			Elts: presence,
		},
	})
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
	propsArg := goast.Expr(props)
	var prelude []goast.Stmt
	if len(assigns) > 0 || e.throwing() > propsThrowing {
		prelude = append([]goast.Stmt{&goast.AssignStmt{Lhs: []goast.Expr{goast.NewIdent("ssrChildProps")}, Tok: token.DEFINE, Rhs: []goast.Expr{props}}}, assigns...)
		if e.throwing() > propsThrowing {
			// The child renders only when its props evaluated (T-6.5a).
			prelude = append(prelude, ssrJSCheck())
		}
		propsArg = goast.NewIdent("ssrChildProps")
	}
	call := &goast.CallExpr{Fun: component, Args: []goast.Expr{propsArg, slots}}
	render := &goast.CallExpr{Fun: goSelectorExpr(call, "Render"), Args: []goast.Expr{goast.NewIdent("w")}}
	// The client sets the parent scope on the child root element.
	setScope := &goast.ExprStmt{X: &goast.CallExpr{Fun: goSelector("vela", "SetHostScope"), Args: []goast.Expr{goast.NewIdent("w"), &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(e.elementScope)}}}}
	return ssrScopedStatements(prelude, setScope, goWriteSSRErr(render)), nil
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

	return e.ranged("", []goast.Stmt{filled}), nil

}

func (e *ssrGoEmitter) text(node *SSRPlanText) ([]goast.Stmt, error) {
	body := make([]goast.Stmt, 0, len(node.Parts)*3)
	for _, part := range node.Parts {

		value, err := e.outputValue("text", "", part.Static, part.Value, part.Expr)
		if err != nil {
			return nil, err
		}
		body = append(body, goWriteSSRValue(value))

	}
	return body, nil
}

func (e *ssrGoEmitter) element(node *SSRPlanElement) ([]goast.Stmt, error) {
	if node.Tag == "select" && ssrSingleSelectHasKnownNullBinding(node, e.ctx) {
		return nil, ssrExprUnsupported(node.Span.Start, "single-select bind:value has a known null server value, which HTML cannot represent before hydration")
	}
	body := make([]goast.Stmt, 0, len(node.Attrs)+len(node.Children)+2)

	open := "<" + node.Tag

	// The client sets the scope attribute on every template element of a
	// component with styles.
	if e.elementScope != "" {
		open += " " + e.elementScope
	}

	var textareaValue SSRPlanAttr
	hasTextareaValue := false
	childSelectValue := e.selectValue
	childSelectMultiple := e.selectMultiple
	var optionValue SSRPlanAttr
	hasOptionValue := false
	hasSelectValueBinding := false
	selectMultiple := false
	classStatic, classDirectives, styleStatic, styleDirectives := ssrElementDirectives(node.Attrs)
	// The client toggles classes through classList, which serialises the
	// token set with single spaces and creates no attribute while the set
	// stays empty; the server writes the same text (task 031).
	classStatic = strings.Join(strings.Fields(classStatic), " ")
	if len(classDirectives) > 0 && classStatic != "" {
		open += ` class="` + html.EscapeString(classStatic)
		body = append(body, goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(open)}))
		for _, attr := range classDirectives {
			if attr.Expr == nil {
				return nil, ssrExprUnsupported(attr.Span.Start, "class directive %q has no cached expression", attr.Name)
			}
			condition, err := e.translate("class", attr.Name, attr.Expr, e.ctx)
			if err != nil {
				return nil, err
			}
			body = append(body, &goast.IfStmt{
				Cond: e.condition(attr.Expr, condition),
				Body: &goast.BlockStmt{List: []goast.Stmt{
					goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(" " + html.EscapeString(attr.Name))}),
				}},
			})
		}
		open = `"`
	} else if len(classDirectives) > 0 {
		// Without static classes: collect the enabled names, then write the
		// attribute only when one is enabled.
		body = append(body, goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(open)}))
		open = ""
		classes := goast.NewIdent("ssrClass")
		block := []goast.Stmt{&goast.AssignStmt{Lhs: []goast.Expr{classes}, Tok: token.DEFINE, Rhs: []goast.Expr{&goast.BasicLit{Kind: token.STRING, Value: `""`}}}}
		for _, attr := range classDirectives {
			if attr.Expr == nil {
				return nil, ssrExprUnsupported(attr.Span.Start, "class directive %q has no cached expression", attr.Name)
			}
			condition, err := e.translate("class", attr.Name, attr.Expr, e.ctx)
			if err != nil {
				return nil, err
			}
			block = append(block, &goast.IfStmt{
				Cond: e.condition(attr.Expr, condition),
				Body: &goast.BlockStmt{List: []goast.Stmt{&goast.AssignStmt{Lhs: []goast.Expr{classes}, Tok: token.ADD_ASSIGN,
					Rhs: []goast.Expr{&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(" " + html.EscapeString(attr.Name))}}}}},
			})
		}
		// if ssrClass != "" { write(` class="` + ssrClass[1:] + `"`) }
		value := &goast.BinaryExpr{Op: token.ADD, X: &goast.BinaryExpr{Op: token.ADD,
			X: &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(` class="`)},
			Y: &goast.SliceExpr{X: classes, Low: &goast.BasicLit{Kind: token.INT, Value: "1"}}},
			Y: &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(`"`)}}
		block = append(block, &goast.IfStmt{
			Cond: &goast.BinaryExpr{Op: token.NEQ, X: classes, Y: &goast.BasicLit{Kind: token.STRING, Value: `""`}},
			Body: &goast.BlockStmt{List: []goast.Stmt{goWriteSSRValue(value)}},
		})
		body = append(body, &goast.BlockStmt{List: block})
	}
	if len(styleDirectives) > 0 {
		open += ` style="` + html.EscapeString(ssrStylePrefix(styleStatic))
		for _, attr := range styleDirectives {
			if attr.Expr == nil {
				return nil, ssrExprUnsupported(attr.Span.Start, "style directive %q has no cached expression", attr.Name)
			}
			open += html.EscapeString(attr.Name) + `:`
			body = append(body, goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(open)}))
			value, err := e.outputValue("style", attr.Name, false, "", attr.Expr)
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
				condition, err := e.translate("form-value", "bind:checked", attr.Expr, e.ctx)
				if err != nil {
					return nil, err
				}
				if open != "" {
					body = append(body, goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(open)}))
					open = ""
				}
				body = append(body, &goast.IfStmt{Cond: e.condition(attr.Expr, condition), Body: &goast.BlockStmt{List: []goast.Stmt{
					goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(" checked")}),
				}}})
				continue
			}
			open += " value=\""
			body = append(body, goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(open)}))
			value, err := e.outputFormValue(attr.Expr)
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
			if isBoolAttr(attr.Name) {
				condition, err := e.translate("attribute", attr.Name, attr.Expr, e.ctx)
				if err != nil {
					return nil, err
				}
				if open != "" {
					body = append(body, goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(open)}))
					open = ""
				}
				body = append(body, &goast.IfStmt{Cond: e.condition(attr.Expr, condition), Body: &goast.BlockStmt{List: []goast.Stmt{
					goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(" " + html.EscapeString(attr.Name))}),
				}}})
				continue
			}
			if e.selectValue != nil && node.Tag == "option" && attr.Name == "value" {
				optionValue = attr
				hasOptionValue = true
			}
			open += " " + attr.Name + "=\""
			body = append(body, goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(open)}))
			value, err := e.outputValue("attribute", attr.Name, false, "", attr.Expr)
			if err != nil {
				return nil, err
			}
			body = append(body, goWriteSSRValue(value))
			open = "\""
		default:
			return nil, ssrExprUnsupported(attr.Span.Start, "attribute %q with role %d is not lowered yet", attr.Name, attr.Role)
		}
	}
	if e.selectValue != nil && node.Tag == "option" && !hasOptionValue {
		return nil, ssrExprUnsupported(node.Span.Start, "an option under select bind:value requires an explicit static value (or a supported dynamic value)")
	}
	if e.selectValue != nil && node.Tag == "option" {
		selected, err := e.selectOptionCondition(e.selectValue, optionValue, e.selectMultiple)
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

	if node == e.rootElement {
		body = append(body, goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(open)}))
		body = append(body, goWriteSSRErr(&goast.CallExpr{Fun: goSelector("vela", "WriteHostScope"), Args: []goast.Expr{goast.NewIdent("w")}}))
		open = ""
	}
	open += ">"
	if node.Tag == "pre" || node.Tag == "textarea" || node.Tag == "listing" {
		// The HTML parser drops one newline right after these start tags;
		// writing one keeps a leading newline of the content (M-5.7).
		open += "\n"
	}
	body = append(body, goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(open)}))
	if hasTextareaValue {
		value, err := e.outputFormValue(textareaValue.Expr)
		if err != nil {
			return nil, err
		}
		body = append(body, goWriteSSRValue(value))
	}
	previousSelectValue := e.selectValue
	previousSelectMultiple := e.selectMultiple
	e.selectValue = childSelectValue
	e.selectMultiple = childSelectMultiple
	if hasSelectValueBinding {
		e.selectMultiple = selectMultiple
	}
	children, err := e.nodes(node.Children)
	e.selectValue = previousSelectValue
	e.selectMultiple = previousSelectMultiple
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

	return e.blocksIf(node)

}

func (e *ssrGoEmitter) eachBlock(node *SSRPlanEach) ([]goast.Stmt, error) {
	iterable, err := e.translate("each", "", node.Iterable, e.ctx)
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
	if node.Key != nil {
		// The server does not use the key; the client computes it from the
		// payload state. The key stays a server site so that it is checked
		// like the other template expressions.
		if _, err := child.translate("each-key", "", node.Key, child.ctx); err != nil {
			return nil, err
		}
	}
	children, err := child.nodes(node.Children)
	if err != nil {
		return nil, err
	}

	// Every row is its own range (M-5.2).
	children = e.ranged("", children)

	e.eachNext = child.eachNext
	e.needsEscaping = e.needsEscaping || child.needsEscaping
	e.absorbImports(child.imports)
	rangeKey := goast.Expr(goast.NewIdent(indexName))
	// A keyed row derives its runtime scope from Key, so an omitted template
	// index must not become an unused generated Go binding. Keep the normal
	// index name whenever it is observable by the template or needed by an
	// unkeyed row's scope.
	if node.IndexName == "" {
		rangeKey = goast.NewIdent("_")
	}
	// A row that reads neither its item nor its index still compiles.
	used := []goast.Stmt{&goast.AssignStmt{Lhs: []goast.Expr{goast.NewIdent("_")}, Tok: token.ASSIGN, Rhs: []goast.Expr{itemIdent}}}
	if ident, ok := rangeKey.(*goast.Ident); ok && ident.Name != "_" {
		used = append(used, &goast.AssignStmt{Lhs: []goast.Expr{goast.NewIdent("_")}, Tok: token.ASSIGN, Rhs: []goast.Expr{rangeKey}})
	}
	children = append(used, children...)
	rangeStmt := &goast.RangeStmt{
		Key:   rangeKey,
		Value: itemIdent,
		Tok:   token.DEFINE,
		X:     iterIdent,
		Body:  &goast.BlockStmt{List: children},
	}
	assign := &goast.AssignStmt{Lhs: []goast.Expr{iterIdent}, Tok: token.DEFINE, Rhs: []goast.Expr{&goast.CallExpr{Fun: goSelector("vela", "Iter"), Args: []goast.Expr{iterable}}}}
	if len(node.ElseChildren) == 0 {

		return e.ranged("", []goast.Stmt{assign, rangeStmt}), nil

	}
	otherwise, err := e.nodes(node.ElseChildren)
	if err != nil {
		return nil, err
	}
	empty := &goast.BinaryExpr{X: &goast.CallExpr{Fun: goast.NewIdent("len"), Args: []goast.Expr{iterIdent}}, Op: token.EQL, Y: &goast.BasicLit{Kind: token.INT, Value: "0"}}

	// The state token tells the client that the fallback is rendered.
	return []goast.Stmt{assign, &goast.IfStmt{
		Cond: empty,
		Body: &goast.BlockStmt{List: e.ranged("e", otherwise)},
		Else: &goast.BlockStmt{List: e.ranged("", []goast.Stmt{rangeStmt})},
	}}, nil

}

// absorbImports returns imports collected by a copied emitter (for example a
// keyed each row) to its lexical parent. Structural lowering intentionally
// copies the emitter to give rows their own bindings and runtime scope, but
// package imports remain module-wide state.
func (e *ssrGoEmitter) absorbImports(imports map[string]string) {
	if len(imports) == 0 {
		return
	}
	if e.imports == nil {
		e.imports = map[string]string{}
	}
	for path, name := range imports {
		e.imports[path] = name
	}
}

// ssrGoTruthy makes a template-controlled Go branch use JavaScript
// truthiness when the lowered expression is not statically a Go bool.
func ssrGoTruthy(value goast.Expr) goast.Expr {
	return &goast.CallExpr{Fun: goSelector("vela", "Truthy"), Args: []goast.Expr{value}}
}

func (e *ssrGoEmitter) condition(expr *TemplateExpr, value goast.Expr) goast.Expr {
	// The IR of the expression decides whether its Go form is a bool.
	if expr != nil && e.facts[expr.Root()].Bool {
		return value
	}
	return ssrGoTruthy(value)
}

func ssrContextWithBindings(base SSRExprContext) SSRExprContext {
	bindings := make(map[string]goast.Expr, len(base.Bindings))
	for name, binding := range base.Bindings {
		bindings[name] = binding
	}
	// Everything but the lexical bindings and the resolution state carries
	// over, so that new context fields cannot be dropped here.
	ctx := base
	ctx.Bindings = bindings
	ctx.resolving, ctx.resolvingFunctions = nil, nil
	return ctx
}

func (e *ssrGoEmitter) outputValue(kind, name string, static bool, literal string, expr *TemplateExpr) (goast.Expr, error) {
	if static {
		return &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(literal)}, nil
	}
	value, err := e.translate(kind, name, expr, e.ctx)
	if err != nil {
		return nil, err
	}
	e.needsEscaping = true
	return &goast.CallExpr{Fun: goSelector("vela", "EscapeString"), Args: []goast.Expr{
		&goast.CallExpr{Fun: goSelector("vela", "String"), Args: []goast.Expr{value}},
	}}, nil
}

// outputFormValue is deliberately separate from outputValue: browser
// input/textarea properties convert null to an empty string, whereas template
// text and ordinary attributes visibly render it as "null".
func (e *ssrGoEmitter) outputFormValue(expr *TemplateExpr) (goast.Expr, error) {
	value, err := e.translate("form-value", "bind:value", expr, e.ctx)
	if err != nil {
		return nil, err
	}
	e.needsEscaping = true
	return &goast.CallExpr{Fun: goSelector("vela", "EscapeString"), Args: []goast.Expr{
		&goast.CallExpr{Fun: goSelector("vela", "FormValueString"), Args: []goast.Expr{value}},
	}}, nil
}

// selectOptionCondition compares the browser-visible string values. Go SSR
// prop inference can produce several concrete types, while the DOM's
// HTMLSelectElement.value is always a string; vela.String keeps that semantic
// boundary explicit without evaluating source text in the compiler.
// ssrSingleSelectHasKnownNullBinding rejects only values the compiler can
// prove are null. Browser assignment to HTMLSelectElement.value with null
// produces selectedIndex=-1, whereas parsing server HTML with no selected
// option selects the first option. There is no faithful static HTML encoding
// for that initial state, so executable SSR must diagnose rather than claim a
// zero-write adoption it cannot provide.
func ssrSingleSelectHasKnownNullBinding(node *SSRPlanElement, ctx SSRExprContext) bool {
	for _, attr := range node.Attrs {
		if attr.Role == SSRAttrStatic && attr.Name == "multiple" {
			return false
		}
	}
	for _, attr := range node.Attrs {
		if attr.Role == SSRAttrTwoWay && attr.Name == "value" {
			return ssrExprIsKnownNull(attr.Expr, ctx, map[string]bool{})
		}
	}
	return false
}

func ssrExprIsKnownNull(expr *TemplateExpr, ctx SSRExprContext, seen map[string]bool) bool {
	if expr == nil || expr.Root() == nil {
		return false
	}
	return ssrJSExprIsKnownNull(expr.Root(), ctx, seen)
}

func ssrJSExprIsKnownNull(expr JsExpr, ctx SSRExprContext, seen map[string]bool) bool {
	switch node := expr.(type) {
	case *JsLiteral:
		return node.Kind == "null"
	case *JsIdent:
		if seen[node.Name] {
			return false
		}
		seen[node.Name] = true
		defer delete(seen, node.Name)
		if initializer, ok := ctx.StateInitializers[node.Name]; ok {
			return ssrJSExprIsKnownNull(initializer, ctx, seen)
		}
		if initializer, ok := ctx.DerivedInitializers[node.Name]; ok {
			return ssrJSExprIsKnownNull(initializer, ctx, seen)
		}
	}
	return false
}

func (e *ssrGoEmitter) selectOptionCondition(selectValue *TemplateExpr, optionValue SSRPlanAttr, multiple bool) (goast.Expr, error) {
	selected, err := e.translate("form-value", "select bind:value", selectValue, e.ctx)
	if err != nil {
		return nil, err
	}
	var option goast.Expr
	switch optionValue.Role {
	case SSRAttrStatic:
		option = &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(optionValue.Value)}
	case SSRAttrDynamic:
		value, err := e.translate("attribute", "value", optionValue.Expr, e.ctx)
		if err != nil {
			return nil, err
		}
		option = &goast.CallExpr{Fun: goSelector("vela", "String"), Args: []goast.Expr{value}}
	default:
		return nil, ssrExprUnsupported(optionValue.Span.Start, "an option under select bind:value has no renderable value")
	}
	if multiple {
		return &goast.CallExpr{Fun: goSelector("vela", "ContainsString"), Args: []goast.Expr{selected, option}}, nil
	}
	return &goast.BinaryExpr{X: &goast.CallExpr{Fun: goSelector("vela", "String"), Args: []goast.Expr{selected}}, Op: token.EQL, Y: option}, nil
}

func (e *ssrGoEmitter) awaitBlock(node *SSRPlanAwait) ([]goast.Stmt, error) {
	if len(node.PendingChildren) == 0 && false {
		return nil, ssrExprUnsupported(node.Span.Start, "SSR await requires an explicit pending branch until hydration")
	}
	pending, err := e.nodes(node.PendingChildren)
	if err != nil {
		return nil, err
	}

	return e.blocksAwait(node, pending)

}

// ssrScopedStatements appends statements to a prelude; with a prelude the
// whole sequence becomes one block, so prelude variables stay local.
func ssrScopedStatements(prelude []goast.Stmt, statements ...goast.Stmt) []goast.Stmt {
	if len(prelude) == 0 {
		return statements
	}
	return []goast.Stmt{&goast.BlockStmt{List: append(prelude, statements...)}}
}

// ssrDefaultFitsField reports whether a prop default lowers to a Go value
// of the field type the props contract infers for it.
func ssrDefaultFitsField(kind SSRValueType, facts ssrIRFacts) bool {
	switch kind {
	case SSRTypeAny:
		return true
	case SSRTypeString:
		return facts.String
	case SSRTypeBool:
		return facts.Bool
	case SSRTypeInt, SSRTypeFloat:
		return facts.Number
	case SSRTypeSlice:
		return facts.Array
	}
	return false
}

func (e *ssrGoEmitter) componentNameOrPlan() string {
	if e.componentName != "" {
		return e.componentName
	}
	if e.region != nil {
		return e.region.Component
	}
	return "props"
}
