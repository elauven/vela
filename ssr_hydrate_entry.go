package main

import (
	"fmt"
)

// BuildSSRHydrateEntry emits the validation-only browser entry for one SSR
// component. Callers with a supported marker-aware factory use
// BuildSSRHydrateEntryWithClient instead.
func BuildSSRHydrateEntry(plan *SSRRenderPlan) (*JsProgram, error) {
	return buildSSRHydrateEntry(plan, "")
}

// BuildSSRHydrateEntryWithClient emits an entry that validates the server
// payload and invokes the supplied marker-aware client factory. The factory
// receives existing DOM through the checked walker; it must never enter the
// ordinary CSR creation or mount path.
func BuildSSRHydrateEntryWithClient(plan *SSRRenderPlan, clientModule string) (*JsProgram, error) {
	if clientModule == "" {
		return nil, fmt.Errorf("ssr hydrate entry: missing hydration client module")
	}
	return buildSSRHydrateEntry(plan, clientModule)
}

func buildSSRHydrateEntry(plan *SSRRenderPlan, clientModule string) (*JsProgram, error) {
	if plan == nil || plan.ComponentName == "" || plan.ScopeID == "" {
		return nil, fmt.Errorf("ssr hydrate entry: missing component identity")
	}
	functionName := "hydrate" + plan.ComponentName
	props := &JsObjectExpr{baseExpr: baseExpr{genNode()}}
	for _, prop := range plan.Props {
		props.Props = append(props.Props, &JsProp{
			baseNode: genNode(),
			Key:      id(prop.SourceName),
			Value:    genMember(genMember(id("$$payload"), "props"), prop.GoName),
			Kind:     "init",
		})
	}
	componentProps := genCall(
		generatedMemberPath("Object.assign"),
		&JsObjectExpr{baseExpr: baseExpr{genNode()}},
		props,
		&JsObjectExpr{baseExpr: baseExpr{genNode()}, Props: []*JsProp{
			{baseNode: genNode(), Key: id("$$velaState"), Value: genMember(id("$$payload"), "state"), Kind: "init"},
		}},
		genLogical("||", id("$$overrides"), &JsObjectExpr{baseExpr: baseExpr{genNode()}}),
	)
	elements := &JsObjectExpr{baseExpr: baseExpr{genNode()}}
	for _, element := range ssrPlanElements(plan.Nodes) {
		elements.Props = append(elements.Props, &JsProp{
			baseNode: genNode(),
			Key:      id("e" + itoa(element.HydrationID)),
			Value:    genCall(id("$$acquireHydrationElement"), id("$$walker"), numLit(element.HydrationID), strLit(element.Tag)),
			Kind:     "init",
		})
	}
	body := []JsStmt{
		genConst("$$scopeID", genLogical("||", id("$$instanceID"), strLit(plan.ScopeID))),
		genConst("$$walker", genCall(id("$$createHydrationWalker"), id("$$root"), id("$$scopeID"))),
		genConst("$$payload", genCall(id("$$readHydrationPayload"), id("$$root"), id("$$scopeID"))),
		genConst("$$velaProps", props),
		genConst("$$componentProps", componentProps),
	}
	if clientModule != "" {
		body = append(body, genConst("$$component", genCall(
			id("$$client"),
			id("$$root"),
			id("$$componentProps"),
			&JsObjectExpr{baseExpr: baseExpr{genNode()}},
			&JsObjectExpr{baseExpr: baseExpr{genNode()}, Props: []*JsProp{
				{baseNode: genNode(), Key: id("walker"), Value: id("$$walker"), Kind: "init"},
				{baseNode: genNode(), Key: id("instanceID"), Value: id("$$scopeID"), Kind: "init"},
			}},
		)))
	}
	returnProps := []*JsProp{
		{baseNode: genNode(), Key: id("root"), Value: id("$$root"), Kind: "init"},
		{baseNode: genNode(), Key: id("props"), Value: id("$$velaProps"), Kind: "init"},
		{baseNode: genNode(), Key: id("componentProps"), Value: id("$$componentProps"), Kind: "init"},
		{baseNode: genNode(), Key: id("state"), Value: genMember(id("$$payload"), "state"), Kind: "init"},
		{baseNode: genNode(), Key: id("deep"), Value: genLogical("||", genMember(id("$$payload"), "deep"), genArray()), Kind: "init"},
		{baseNode: genNode(), Key: id("ranges"), Value: genMember(id("$$walker"), "ranges"), Kind: "init"},
		{baseNode: genNode(), Key: id("elements"), Value: elements, Kind: "init"},
	}
	if clientModule != "" {
		returnProps = append(returnProps, &JsProp{baseNode: genNode(), Key: id("component"), Value: id("$$component"), Kind: "init"})
	}
	body = append(body, genReturn(&JsObjectExpr{baseExpr: baseExpr{genNode()}, Props: returnProps}))
	entry := &JsFuncDecl{
		baseStmt: baseStmt{genNode()},
		Name:     functionName,
		Params:   []JsExpr{id("$$root"), id("$$overrides"), id("$$instanceID")},
		Body:     &JsBlock{baseStmt: baseStmt{genNode()}, Body: body},
	}
	imports := []JsStmt{}
	if clientModule != "" {
		imports = append(imports, &JsImportDecl{baseStmt: baseStmt{genNode()}, DefaultName: "$$client", Source: clientModule})
	}
	componentAlias := &JsFuncDecl{
		baseStmt: baseStmt{genNode()},
		Name:     plan.ComponentName,
		Params:   []JsExpr{id("$$root"), id("$$overrides"), id("$$instanceID")},
		Body:     &JsBlock{baseStmt: baseStmt{genNode()}, Body: []JsStmt{genReturn(genCall(id(functionName), id("$$root"), id("$$overrides"), id("$$instanceID")))}},
	}
	imports = append(imports,
		&JsImportDecl{baseStmt: baseStmt{genNode()}, Specifiers: []JsImportSpec{
			{Imported: "$$acquireHydrationElement"},
			{Imported: "$$createHydrationWalker"},
			{Imported: "$$readHydrationPayload"},
		}, Source: "./runtime.js"},
		&JsExportDecl{baseStmt: baseStmt{genNode()}, Declaration: entry},
		&JsExportDecl{baseStmt: baseStmt{genNode()}, Declaration: componentAlias},
		&JsExportDecl{baseStmt: baseStmt{genNode()}, Default: true, Declaration: genExprStmt(id(functionName))},
	)
	program := &JsProgram{baseNode: genNode(), Body: imports}
	validateGeneratedProgram(program)
	return program, nil
}

func ssrPlanElements(nodes []SSRPlanNode) []*SSRPlanElement {
	var elements []*SSRPlanElement
	var visit func([]SSRPlanNode)
	visit = func(children []SSRPlanNode) {
		for _, node := range children {
			switch current := node.(type) {
			case *SSRPlanElement:
				elements = append(elements, current)
				visit(current.Children)
			// Structural ranges and child components can contain alternatives
			// that are not present in this server response. Their owning
			// marker-aware lowerings acquire the active DOM on demand; eagerly
			// indexing every theoretical element here would reject valid #if,
			// #each, await and slot output as "missing".
			case *SSRPlanComponent, *SSRPlanSlot, *SSRPlanIf, *SSRPlanEach, *SSRPlanAwait:
				continue
			}
		}
	}
	visit(nodes)
	return elements
}
