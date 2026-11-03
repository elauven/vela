package ssr

import (
	goast "go/ast"
	"go/token"
	"sort"
	"strconv"

	"github.com/elauven/vela/internal/sfcast"
)

// Component values (task 070): every generated renderer also exports
//
//	type ssrComponentX struct{}
//	func (ssrComponentX) ComponentID() string { return "routes/X" }
//	func (ssrComponentX) RenderDynamic(ctx context.Context, w io.Writer, props map[string]any, slots map[string]func(io.Writer) error, options vela.DynamicOptions) error {
//		p := XProps{Set: map[string]bool{}}
//		if value, ok := props["title"]; ok {
//			if err := vela.AssignProp(&p.Title, value, "X.title"); err != nil {
//				return err
//			}
//			p.Set["title"] = true
//		}
//		return RenderX(vela.DynamicWriter(w, ctx, options), p, XSlots{Default: slots["default"]})
//	}
//	var XComponent vela.Component = ssrComponentX{}
//
// so that an imported component used as a value translates to XComponent
// and <vela:component> renders it with props by name. Props the component
// does not declare are ignored; the keys present are the props supplied.

func goComponentValueDecls(plan *SSRRenderPlan, component, propsName, slotsName string) []goast.Decl {
	adapter := "ssrComponent" + component
	str := func(s string) goast.Expr { return &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(s)} }
	ident := goast.NewIdent
	recv := &goast.FieldList{List: []*goast.Field{{Type: ident(adapter)}}}
	errResult := &goast.FieldList{List: []*goast.Field{{Type: ident("error")}}}
	writer := goSelector("io", "Writer")
	slotFunc := &goast.FuncType{Params: &goast.FieldList{List: []*goast.Field{{Type: goSelector("io", "Writer")}}}, Results: &goast.FieldList{List: []*goast.Field{{Type: ident("error")}}}}

	presence := !plan.PropsWithoutPresence
	init := &goast.CompositeLit{Type: ident(propsName)}
	if presence {
		init.Elts = []goast.Expr{&goast.KeyValueExpr{Key: ident("Set"), Value: &goast.CompositeLit{Type: &goast.MapType{Key: ident("string"), Value: ident("bool")}}}}
	}
	body := []goast.Stmt{&goast.AssignStmt{Lhs: []goast.Expr{ident("p")}, Tok: token.DEFINE, Rhs: []goast.Expr{init}}}
	for _, prop := range plan.Props {
		assign := []goast.Stmt{goWriteSSRErr(&goast.CallExpr{Fun: goSelector("vela", "AssignProp"), Args: []goast.Expr{
			&goast.UnaryExpr{Op: token.AND, X: goSelectorExpr(ident("p"), prop.GoName)}, ident("value"), str(component + "." + prop.SourceName),
		}})}
		if presence {
			assign = append(assign, &goast.AssignStmt{
				Lhs: []goast.Expr{&goast.IndexExpr{X: goSelectorExpr(ident("p"), "Set"), Index: str(prop.SourceName)}},
				Tok: token.ASSIGN, Rhs: []goast.Expr{ident("true")},
			})
		}
		body = append(body, &goast.IfStmt{
			Init: &goast.AssignStmt{Lhs: []goast.Expr{ident("value"), ident("ok")}, Tok: token.DEFINE, Rhs: []goast.Expr{&goast.IndexExpr{X: ident("props"), Index: str(prop.SourceName)}}},
			Cond: ident("ok"),
			Body: &goast.BlockStmt{List: assign},
		})
	}
	slots := &goast.CompositeLit{Type: ident(slotsName)}
	for _, name := range plan.SlotNames {
		slots.Elts = append(slots.Elts, &goast.KeyValueExpr{Key: ident(ssrGoExportName(name)), Value: &goast.IndexExpr{X: ident("slots"), Index: str(name)}})
	}
	body = append(body, &goast.ReturnStmt{Results: []goast.Expr{&goast.CallExpr{Fun: ident("Render" + component), Args: []goast.Expr{
		&goast.CallExpr{Fun: goSelector("vela", "DynamicWriter"), Args: []goast.Expr{ident("w"), ident("ctx"), ident("options")}},
		ident("p"), slots,
	}}}})

	return []goast.Decl{
		&goast.GenDecl{Tok: token.TYPE, Specs: []goast.Spec{&goast.TypeSpec{Name: ident(adapter), Type: &goast.StructType{Fields: &goast.FieldList{}}}}},
		&goast.FuncDecl{
			Recv: recv,
			Name: ident("ComponentID"),
			Type: &goast.FuncType{Params: &goast.FieldList{}, Results: &goast.FieldList{List: []*goast.Field{{Type: ident("string")}}}},
			Body: &goast.BlockStmt{List: []goast.Stmt{&goast.ReturnStmt{Results: []goast.Expr{str(plan.ComponentID)}}}},
		},
		&goast.FuncDecl{
			Recv: &goast.FieldList{List: []*goast.Field{{Type: ident(adapter)}}},
			Name: ident("RenderDynamic"),
			Type: &goast.FuncType{Params: &goast.FieldList{List: []*goast.Field{
				{Names: []*goast.Ident{ident("ctx")}, Type: goSelector("context", "Context")},
				{Names: []*goast.Ident{ident("w")}, Type: writer},
				{Names: []*goast.Ident{ident("props")}, Type: &goast.MapType{Key: ident("string"), Value: ident("any")}},
				{Names: []*goast.Ident{ident("slots")}, Type: &goast.MapType{Key: ident("string"), Value: slotFunc}},
				{Names: []*goast.Ident{ident("options")}, Type: goSelector("vela", "DynamicOptions")},
			}}, Results: errResult},
			Body: &goast.BlockStmt{List: body},
		},
		&goast.GenDecl{Tok: token.VAR, Specs: []goast.Spec{&goast.ValueSpec{
			Names:  []*goast.Ident{ident(component + "Component")},
			Type:   goSelector("vela", "Component"),
			Values: []goast.Expr{&goast.CompositeLit{Type: ident(adapter)}},
		}}},
	}
}

// dynamicComponent renders <vela:component :this={…}> (H-5.1): :this is
// evaluated first; the props only when it is a component, as on the client;
// vela.RenderDynamicComponent writes the range, the "d" entry and the child.
//
//	{
//		ssrDynamic3 := any(views[mode])
//		var ssrDynamicProps3 map[string]any
//		if _, ok := ssrDynamic3.(vela.Component); ok {
//			ssrDynamicProps3 = map[string]any{"rows": ssrL_rows}
//		}
//		vela.SetHostScope(w, "vela-x")
//		if err := vela.RenderDynamicComponent(w, "App#3", "App.vela:4:28", ssrDynamic3, ssrDynamicProps3, map[string]func(io.Writer) error{…}); err != nil {
//			return err
//		}
//		vela.SetHostScope(w, "")
//	}
func (e *ssrGoEmitter) dynamicComponent(node *SSRPlanComponent) ([]goast.Stmt, error) {
	id := strconv.Itoa(node.DynamicID)
	valueVar, propsVar := "ssrDynamic"+id, "ssrDynamicProps"+id
	ident := goast.NewIdent
	str := func(s string) goast.Expr { return &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(s)} }

	throwing := e.throwing()
	this, err := e.translate("dynamic-component", "this", node.This, e.ctx)
	if err != nil {
		return nil, err
	}
	body := []goast.Stmt{&goast.AssignStmt{Lhs: []goast.Expr{ident(valueVar)}, Tok: token.DEFINE, Rhs: []goast.Expr{&goast.CallExpr{Fun: ident("any"), Args: []goast.Expr{this}}}}}
	if e.throwing() > throwing {
		body = append(body, ssrJSCheck())
	}

	props := &goast.CompositeLit{Type: &goast.MapType{Key: ident("string"), Value: ident("any")}}
	throwing = e.throwing()
	for _, attr := range node.Attrs {
		var value goast.Expr
		switch attr.Role {
		case SSRAttrEvent:
			continue
		case SSRAttrStatic:
			if attr.ValueKind == sfcast.AttrValueMissing {
				return nil, ssrExprUnsupported(attr.Span.Start, "component prop %q requires a value", attr.Name)
			}
			value = str(attr.Value)
		case SSRAttrDynamic, SSRAttrTwoWay:
			var err error
			value, err = e.translate("component-prop", "vela:component."+attr.Name, attr.Expr, e.ctx)
			if err != nil {
				return nil, err
			}
		default:
			return nil, ssrExprUnsupported(attr.Span.Start, "component prop %q with role %d requires hydration", attr.Name, attr.Role)
		}
		props.Elts = append(props.Elts, &goast.KeyValueExpr{Key: str(attr.Name), Value: value})
	}
	assignProps := []goast.Stmt{&goast.AssignStmt{Lhs: []goast.Expr{ident(propsVar)}, Tok: token.ASSIGN, Rhs: []goast.Expr{props}}}
	if e.throwing() > throwing {
		// The child renders only when its props evaluated (T-6.5a).
		assignProps = append(assignProps, ssrJSCheck())
	}
	body = append(body,
		&goast.DeclStmt{Decl: &goast.GenDecl{Tok: token.VAR, Specs: []goast.Spec{&goast.ValueSpec{Names: []*goast.Ident{ident(propsVar)}, Type: &goast.MapType{Key: ident("string"), Value: ident("any")}}}}},
		&goast.IfStmt{
			Init: &goast.AssignStmt{Lhs: []goast.Expr{ident("_"), ident("ok")}, Tok: token.DEFINE, Rhs: []goast.Expr{&goast.TypeAssertExpr{X: ident(valueVar), Type: goSelector("vela", "Component")}}},
			Cond: ident("ok"),
			Body: &goast.BlockStmt{List: assignProps},
		},
	)

	slotFunc := &goast.FuncType{Params: &goast.FieldList{List: []*goast.Field{{Type: goSelector("io", "Writer")}}}, Results: &goast.FieldList{List: []*goast.Field{{Type: ident("error")}}}}
	slots := &goast.CompositeLit{Type: &goast.MapType{Key: ident("string"), Value: slotFunc}}
	names := make([]string, 0, len(node.Slots))
	for name := range node.Slots {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		children, err := e.nodes(node.Slots[name])
		if err != nil {
			return nil, err
		}
		children = append(children, &goast.ReturnStmt{Results: []goast.Expr{ident("nil")}})
		closure := &goast.FuncLit{Type: &goast.FuncType{
			Params:  &goast.FieldList{List: []*goast.Field{{Names: []*goast.Ident{ident("w")}, Type: goSelector("io", "Writer")}}},
			Results: &goast.FieldList{List: []*goast.Field{{Type: ident("error")}}},
		}, Body: &goast.BlockStmt{List: children}}
		slots.Elts = append(slots.Elts, &goast.KeyValueExpr{Key: str(name), Value: closure})
	}

	componentID := e.componentID
	if componentID == "" {
		componentID = e.componentName
	}
	where := componentID + ".vela"
	if node.ThisLine > 0 {
		where += ":" + strconv.Itoa(node.ThisLine) + ":" + strconv.Itoa(node.ThisColumn)
	}
	setScope := func(scope string) goast.Stmt {
		return &goast.ExprStmt{X: &goast.CallExpr{Fun: goSelector("vela", "SetHostScope"), Args: []goast.Expr{ident("w"), str(scope)}}}
	}
	body = append(body,
		setScope(e.elementScope),
		goWriteSSRErr(&goast.CallExpr{Fun: goSelector("vela", "RenderDynamicComponent"), Args: []goast.Expr{
			ident("w"), str(e.awaitSite(node.DynamicID)), str(where), ident(valueVar), ident(propsVar), slots,
		}}),
		// A site that rendered nothing leaves the scope unused.
		setScope(""),
	)
	return []goast.Stmt{&goast.BlockStmt{List: body}}, nil
}
