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
			{baseNode: genNode(), Key: id("$$velaAwait"), Value: genLogical("||", genMember(id("$$payload"), "await"), &JsObjectExpr{baseExpr: baseExpr{genNode()}}), Kind: "init"},
			{baseNode: genNode(), Key: id("$$velaAwaitAt"), Value: genLogical("||", genMember(id("$$payload"), "awaitAt"), &JsObjectExpr{baseExpr: baseExpr{genNode()}}), Kind: "init"},
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
		genIf(id("$$createdInstances"), genExprStmt(genCall(genMember(id("$$transaction"), "add"), &JsArrowFunc{baseExpr: baseExpr{genNode()}, Body: genBlock(
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
			&JsObjectExpr{baseExpr: baseExpr{genNode()}},
			&JsObjectExpr{baseExpr: baseExpr{genNode()}, Props: []*JsProp{
				{baseNode: genNode(), Key: id("walker"), Value: id("$$walker"), Kind: "init"},
				{baseNode: genNode(), Key: id("instanceID"), Value: id("$$scopeID"), Kind: "init"},
				{baseNode: genNode(), Key: id("transaction"), Value: id("$$transaction"), Kind: "init"},
			}},
		)))
		// The payload belongs to this hydrated instance, not to its parent
		// range. Wrapping destroy makes all owners (ordinary children, #if,
		// unkeyed and keyed rows) release inert state metadata together with
		// the live component without a second, feature-specific cleanup path.
		hydrateBody = append(hydrateBody,
			genConst("$$destroy", genMember(id("$$component"), "destroy")),
			genAssignStmt(genMember(id("$$component"), "destroy"), &JsArrowFunc{baseExpr: baseExpr{genNode()}, Body: genBlock(
				genInvoke("$$destroy"),
				genExprStmt(genCall(genMember(id("$$instances"), "delete"), id("$$scopeID"))),
				genIf(genUnary("!", genMember(id("$$instances"), "size")), genExprStmt(genCall(genMember(id("$$hydratedInstances"), "delete"), id("$$root")))),
				genConst("$$payloadNode", genCall(genMember(id("document"), "getElementById"), genBinary("+", strLit("vela-state-"), id("$$scopeID")))),
				genIf(id("$$payloadNode"), genExprStmt(genCall(genMember(id("$$payloadNode"), "remove")))),
			)}),
		)
	}
	returnProps := []*JsProp{
		{baseNode: genNode(), Key: id("mode"), Value: strLit(string(mode)), Kind: "init"},
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
	hydrateBody = append(hydrateBody,
		genConst("$$hydration", &JsObjectExpr{baseExpr: baseExpr{genNode()}, Props: returnProps}),
		genExprStmt(genCall(genMember(id("$$instances"), "set"), id("$$scopeID"), id("$$hydration"))),
		genExprStmt(genCall(genMember(id("$$transaction"), "add"), &JsArrowFunc{baseExpr: baseExpr{genNode()}, Body: genBlock(
			genExprStmt(genCall(genMember(id("$$instances"), "delete"), id("$$scopeID"))),
			genIf(genUnary("!", genMember(id("$$instances"), "size")), genExprStmt(genCall(genMember(id("$$hydratedInstances"), "delete"), id("$$root")))),
		)})),
		genIf(id("$$ownsTransaction"), genExprStmt(genCall(genMember(id("$$transaction"), "commit")))),
		genReturn(id("$$hydration")),
	)
	body = append(body, &JsTryStmt{
		baseStmt:   baseStmt{genNode()},
		Block:      genBlock(hydrateBody...),
		CatchParam: id("$$error"),
		Handler: genBlock(
			genIf(id("$$ownsTransaction"), genExprStmt(genCall(genMember(id("$$transaction"), "rollback")))),
			&JsThrowStmt{baseStmt: baseStmt{genNode()}, Arg: id("$$error")},
		),
	})
	entry := &JsFuncDecl{
		baseStmt: baseStmt{genNode()},
		Name:     functionName,
		Params:   []JsExpr{id("$$root"), id("$$overrides"), id("$$instanceID"), id("$$parentTransaction")},
		Body:     &JsBlock{baseStmt: baseStmt{genNode()}, Body: body},
	}
	imports := []JsStmt{genConst("$$hydratedInstances", genNew(id("WeakMap")))}
	if clientModule != "" {
		imports = append(imports, &JsImportDecl{baseStmt: baseStmt{genNode()}, DefaultName: "$$client", Source: clientModule})
	}
	componentAlias := &JsFuncDecl{
		baseStmt: baseStmt{genNode()},
		Name:     plan.ComponentName,
		Params:   []JsExpr{id("$$root"), id("$$overrides"), id("$$instanceID"), id("$$parentTransaction")},
		Body:     &JsBlock{baseStmt: baseStmt{genNode()}, Body: []JsStmt{genReturn(genCall(id(functionName), id("$$root"), id("$$overrides"), id("$$instanceID"), id("$$parentTransaction")))}},
	}
	imports = append(imports,
		&JsImportDecl{baseStmt: baseStmt{genNode()}, Specifiers: []JsImportSpec{
			{Imported: "$$acquireHydrationElement"},
			{Imported: "$$createHydrationTransaction"},
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
