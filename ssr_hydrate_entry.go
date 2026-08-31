package main

import (
	"fmt"
	"sort"
)

// BuildSSRHydrateEntry emits the first public browser entry for one SSR
// component. It deliberately validates and adopts existing server data only:
// no call to the CSR factory is made here, so this stage cannot clear, move,
// or recreate DOM before the marker-aware component lowerings are complete.
func BuildSSRHydrateEntry(plan *SSRRenderPlan) (*JsProgram, error) {
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
	elements := &JsObjectExpr{baseExpr: baseExpr{genNode()}}
	for _, element := range ssrPlanElements(plan.Nodes) {
		elements.Props = append(elements.Props, &JsProp{
			baseNode: genNode(),
			Key:      id("e" + itoa(element.HydrationID)),
			Value:    genCall(id("$$acquireHydrationElement"), id("$$walker"), numLit(element.HydrationID), strLit(element.Tag)),
			Kind:     "init",
		})
	}
	entry := &JsFuncDecl{
		baseStmt: baseStmt{genNode()},
		Name:     functionName,
		Params:   []JsExpr{id("$$root")},
		Body: &JsBlock{baseStmt: baseStmt{genNode()}, Body: []JsStmt{
			genConst("$$walker", genCall(id("$$createHydrationWalker"), id("$$root"), strLit(plan.ScopeID))),
			genConst("$$payload", genCall(id("$$readHydrationPayload"), id("$$root"), strLit(plan.ScopeID))),
			genReturn(&JsObjectExpr{baseExpr: baseExpr{genNode()}, Props: []*JsProp{
				{baseNode: genNode(), Key: id("root"), Value: id("$$root"), Kind: "init"},
				{baseNode: genNode(), Key: id("props"), Value: props, Kind: "init"},
				{baseNode: genNode(), Key: id("state"), Value: genMember(id("$$payload"), "state"), Kind: "init"},
				{baseNode: genNode(), Key: id("deep"), Value: genLogical("||", genMember(id("$$payload"), "deep"), genArray()), Kind: "init"},
				{baseNode: genNode(), Key: id("ranges"), Value: genMember(id("$$walker"), "ranges"), Kind: "init"},
				{baseNode: genNode(), Key: id("elements"), Value: elements, Kind: "init"},
			}}),
		}},
	}
	program := &JsProgram{baseNode: genNode(), Body: []JsStmt{
		&JsImportDecl{baseStmt: baseStmt{genNode()}, Specifiers: []JsImportSpec{
			{Imported: "$$acquireHydrationElement"},
			{Imported: "$$createHydrationWalker"},
			{Imported: "$$readHydrationPayload"},
		}, Source: "./runtime.js"},
		&JsExportDecl{baseStmt: baseStmt{genNode()}, Declaration: entry},
		&JsExportDecl{baseStmt: baseStmt{genNode()}, Default: true, Declaration: genExprStmt(id(functionName))},
	}}
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
			case *SSRPlanComponent:
				slotNames := make([]string, 0, len(current.Slots))
				for name := range current.Slots {
					slotNames = append(slotNames, name)
				}
				sort.Strings(slotNames)
				for _, name := range slotNames {
					visit(current.Slots[name])
				}
			case *SSRPlanSlot:
				visit(current.Fallback)
			case *SSRPlanIf:
				for _, branch := range current.Branches {
					visit(branch.Children)
				}
			case *SSRPlanEach:
				visit(current.Children)
				visit(current.ElseChildren)
			case *SSRPlanAwait:
				visit(current.PendingChildren)
				visit(current.ThenChildren)
				visit(current.CatchChildren)
			}
		}
	}
	visit(nodes)
	return elements
}
