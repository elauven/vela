package main

import (
	"fmt"
)

// BuildSSRHydrateEntry emits the validation-only browser entry for one SSR
// component. Callers with a supported marker-aware factory use
// BuildSSRHydrateEntryWithClient instead.
func BuildSSRHydrateEntry(plan *SSRRenderPlan) (*JsProgram, error) {
	return buildSSRHydrateEntry(plan, "", SSRArtifactStaticOnly)
}

// BuildSSRHydrateEntryWithClient emits an entry that validates the server
// payload and invokes the supplied marker-aware client factory. The factory
// receives existing DOM through the checked walker; it must never enter the
// ordinary CSR creation or mount path.
func BuildSSRHydrateEntryWithClient(plan *SSRRenderPlan, clientModule string) (*JsProgram, error) {
	if clientModule == "" {
		return nil, fmt.Errorf("ssr hydrate entry: missing hydration client module")
	}
	return buildSSRHydrateEntry(plan, clientModule, SSRArtifactExecutable)
}

// BuildSSRClientBoundaryEntry emits the public marker/payload adoption entry
// for a future explicit client boundary. It deliberately has no live
// component controller; only the boundary lowerer may opt into this mode once
// it has emitted a deterministic server fallback.
func BuildSSRClientBoundaryEntry(plan *SSRRenderPlan) (*JsProgram, error) {
	return buildSSRHydrateEntry(plan, "", SSRArtifactClientBoundary)
}

func buildSSRHydrateEntry(plan *SSRRenderPlan, clientModule string, mode SSRArtifactMode) (*JsProgram, error) {
	if plan == nil || plan.ComponentName == "" || plan.ScopeID == "" {
		return nil, fmt.Errorf("ssr hydrate entry: missing component identity")
	}
	if mode == SSRArtifactExecutable && clientModule == "" {
		return nil, fmt.Errorf("ssr hydrate entry: executable mode requires a hydration client module")
	}
	if mode != SSRArtifactExecutable && clientModule != "" {
		return nil, fmt.Errorf("ssr hydrate entry: only executable mode accepts a hydration client module")
	}
	functionName := "hydrate" + plan.ComponentName
	props := &JsObjectExpr{BaseExpr: BaseExpr{BaseNode: genNode()}}
	for _, prop := range plan.Props {
		props.Props = append(props.Props, &JsProp{
			BaseNode: genNode(),
			Key:      id(prop.SourceName),
			Value:    genMember(genMember(id("$$payload"), "props"), prop.GoName),
			Kind:     "init",
		})
	}
	componentProps := genCall(
		generatedMemberPath("Object.assign"),
		&JsObjectExpr{BaseExpr: BaseExpr{BaseNode: genNode()}},
		props,
		&JsObjectExpr{BaseExpr: BaseExpr{BaseNode: genNode()}, Props: []*JsProp{
			{BaseNode: genNode(), Key: id("$$velaState"), Value: genMember(id("$$payload"), "state"), Kind: "init"},
			{BaseNode: genNode(), Key: id("$$velaAwait"), Value: genLogical("||", genMember(id("$$payload"), "await"), &JsObjectExpr{BaseExpr: BaseExpr{BaseNode: genNode()}}), Kind: "init"},
			{BaseNode: genNode(), Key: id("$$velaAwaitAt"), Value: genLogical("||", genMember(id("$$payload"), "awaitAt"), &JsObjectExpr{BaseExpr: BaseExpr{BaseNode: genNode()}}), Kind: "init"},
		}},
		genLogical("||", id("$$overrides"), &JsObjectExpr{BaseExpr: BaseExpr{BaseNode: genNode()}}),
	)
	elements := &JsObjectExpr{BaseExpr: BaseExpr{BaseNode: genNode()}}
	for _, element := range ssrPlanElements(plan.Nodes) {
		elements.Props = append(elements.Props, &JsProp{
			BaseNode: genNode(),
			Key:      id("e" + itoa(element.HydrationID)),
			Value:    genCall(id("$$acquireHydrationElement"), id("$$walker"), numLit(element.HydrationID), strLit(element.Tag)),
			Kind:     "init",
		})
	}
	body := []JsStmt{
		genConst("$$scopeID", genLogical("||", id("$$instanceID"), strLit(plan.ScopeID))),
		genConst("$$ownsTransaction", genUnary("!", id("$$parentTransaction"))),
		genConst("$$transaction", genLogical("||", id("$$parentTransaction"), genCall(id("$$createHydrationTransaction")))),
	}
	hydrateBody := []JsStmt{
		genLet("$$instances", genCall(genMember(id("$$hydratedInstances"), "get"), id("$$root"))),
		genConst("$$createdInstances", genUnary("!", id("$$instances"))),
		genIf(genUnary("!", id("$$instances")), genBlock(
			genAssignStmt(id("$$instances"), genNew(id("Map"))),
			genExprStmt(genCall(genMember(id("$$hydratedInstances"), "set"), id("$$root"), id("$$instances"))),
		)),
		genIf(id("$$createdInstances"), genExprStmt(genCall(genMember(id("$$transaction"), "add"), &JsArrowFunc{BaseExpr: BaseExpr{BaseNode: genNode()}, Body: genBlock(
			genIf(genUnary("!", genMember(id("$$instances"), "size")), genExprStmt(genCall(genMember(id("$$hydratedInstances"), "delete"), id("$$root")))),
		)}))),
		genConst("$$existing", genCall(genMember(id("$$instances"), "get"), id("$$scopeID"))),
		genIf(id("$$existing"), genBlock(
			genIf(id("$$ownsTransaction"), genExprStmt(genCall(genMember(id("$$transaction"), "commit")))),
			genReturn(id("$$existing")),
		)),
		genConst("$$walker", genCall(id("$$createHydrationWalker"), id("$$root"), id("$$scopeID"))),
		genConst("$$payload", genCall(id("$$readHydrationPayload"), id("$$root"), id("$$scopeID"))),
		genConst("$$velaProps", props),
		genConst("$$componentProps", componentProps),
	}
	if clientModule != "" {
		hydrateBody = append(hydrateBody, genConst("$$component", genCall(
			id("$$client"),
			id("$$root"),
			id("$$componentProps"),
			&JsObjectExpr{BaseExpr: BaseExpr{BaseNode: genNode()}},
			&JsObjectExpr{BaseExpr: BaseExpr{BaseNode: genNode()}, Props: []*JsProp{
				{BaseNode: genNode(), Key: id("walker"), Value: id("$$walker"), Kind: "init"},
				{BaseNode: genNode(), Key: id("instanceID"), Value: id("$$scopeID"), Kind: "init"},
				{BaseNode: genNode(), Key: id("transaction"), Value: id("$$transaction"), Kind: "init"},
			}},
		)))
		// The payload belongs to this hydrated instance, not to its parent
		// range. Wrapping destroy makes all owners (ordinary children, #if,
		// unkeyed and keyed rows) release inert state metadata together with
		// the live component without a second, feature-specific cleanup path.
		hydrateBody = append(hydrateBody,
			genConst("$$destroy", genMember(id("$$component"), "destroy")),
			genAssignStmt(genMember(id("$$component"), "destroy"), &JsArrowFunc{BaseExpr: BaseExpr{BaseNode: genNode()}, Body: genBlock(
				genInvoke("$$destroy"),
				genExprStmt(genCall(genMember(id("$$instances"), "delete"), id("$$scopeID"))),
				genIf(genUnary("!", genMember(id("$$instances"), "size")), genExprStmt(genCall(genMember(id("$$hydratedInstances"), "delete"), id("$$root")))),
				genConst("$$payloadNode", genCall(genMember(id("document"), "getElementById"), genBinary("+", strLit("vela-state-"), id("$$scopeID")))),
				genIf(id("$$payloadNode"), genExprStmt(genCall(genMember(id("$$payloadNode"), "remove")))),
			)}),
		)
	}
	returnProps := []*JsProp{
		{BaseNode: genNode(), Key: id("mode"), Value: strLit(string(mode)), Kind: "init"},
		{BaseNode: genNode(), Key: id("root"), Value: id("$$root"), Kind: "init"},
		{BaseNode: genNode(), Key: id("props"), Value: id("$$velaProps"), Kind: "init"},
		{BaseNode: genNode(), Key: id("componentProps"), Value: id("$$componentProps"), Kind: "init"},
		{BaseNode: genNode(), Key: id("state"), Value: genMember(id("$$payload"), "state"), Kind: "init"},
		{BaseNode: genNode(), Key: id("deep"), Value: genLogical("||", genMember(id("$$payload"), "deep"), genArray()), Kind: "init"},
		{BaseNode: genNode(), Key: id("ranges"), Value: genMember(id("$$walker"), "ranges"), Kind: "init"},
		{BaseNode: genNode(), Key: id("elements"), Value: elements, Kind: "init"},
	}
	if clientModule != "" {
		returnProps = append(returnProps, &JsProp{BaseNode: genNode(), Key: id("component"), Value: id("$$component"), Kind: "init"})
	}
	hydrateBody = append(hydrateBody,
		genConst("$$hydration", &JsObjectExpr{BaseExpr: BaseExpr{BaseNode: genNode()}, Props: returnProps}),
		genExprStmt(genCall(genMember(id("$$instances"), "set"), id("$$scopeID"), id("$$hydration"))),
		genExprStmt(genCall(genMember(id("$$transaction"), "add"), &JsArrowFunc{BaseExpr: BaseExpr{BaseNode: genNode()}, Body: genBlock(
			genExprStmt(genCall(genMember(id("$$instances"), "delete"), id("$$scopeID"))),
			genIf(genUnary("!", genMember(id("$$instances"), "size")), genExprStmt(genCall(genMember(id("$$hydratedInstances"), "delete"), id("$$root")))),
		)})),
		genIf(id("$$ownsTransaction"), genExprStmt(genCall(genMember(id("$$transaction"), "commit")))),
		genReturn(id("$$hydration")),
	)
	body = append(body, &JsTryStmt{
		BaseStmt:   BaseStmt{BaseNode: genNode()},
		Block:      genBlock(hydrateBody...),
		CatchParam: id("$$error"),
		Handler: genBlock(
			genIf(id("$$ownsTransaction"), genExprStmt(genCall(genMember(id("$$transaction"), "rollback")))),
			&JsThrowStmt{BaseStmt: BaseStmt{BaseNode: genNode()}, Arg: id("$$error")},
		),
	})
	entry := &JsFuncDecl{
		BaseStmt: BaseStmt{BaseNode: genNode()},
		Name:     functionName,
		Params:   []JsExpr{id("$$root"), id("$$overrides"), id("$$instanceID"), id("$$parentTransaction")},
		Body:     &JsBlock{BaseStmt: BaseStmt{BaseNode: genNode()}, Body: body},
	}
	imports := []JsStmt{genConst("$$hydratedInstances", genNew(id("WeakMap")))}
	if clientModule != "" {
		imports = append(imports, &JsImportDecl{BaseStmt: BaseStmt{BaseNode: genNode()}, DefaultName: "$$client", Source: clientModule})
	}
	componentAlias := &JsFuncDecl{
		BaseStmt: BaseStmt{BaseNode: genNode()},
		Name:     plan.ComponentName,
		Params:   []JsExpr{id("$$root"), id("$$overrides"), id("$$instanceID"), id("$$parentTransaction")},
		Body:     &JsBlock{BaseStmt: BaseStmt{BaseNode: genNode()}, Body: []JsStmt{genReturn(genCall(id(functionName), id("$$root"), id("$$overrides"), id("$$instanceID"), id("$$parentTransaction")))}},
	}
	imports = append(imports,
		&JsImportDecl{BaseStmt: BaseStmt{BaseNode: genNode()}, Specifiers: []JsImportSpec{
			{Imported: "$$acquireHydrationElement"},
			{Imported: "$$createHydrationTransaction"},
			{Imported: "$$createHydrationWalker"},
			{Imported: "$$readHydrationPayload"},
		}, Source: "./runtime.js"},
		&JsExportDecl{BaseStmt: BaseStmt{BaseNode: genNode()}, Declaration: entry},
		&JsExportDecl{BaseStmt: BaseStmt{BaseNode: genNode()}, Declaration: componentAlias},
		&JsExportDecl{BaseStmt: BaseStmt{BaseNode: genNode()}, Default: true, Declaration: genExprStmt(id(functionName))},
	)
	program := &JsProgram{BaseNode: genNode(), Body: imports}
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
