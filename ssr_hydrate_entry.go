package main

import "fmt"

// BuildSSRHydrateEntry emits the first public browser entry for one SSR
// component. It deliberately validates and adopts existing server data only:
// no call to the CSR factory is made here, so this stage cannot clear, move,
// or recreate DOM before the marker-aware component lowerings are complete.
func BuildSSRHydrateEntry(plan *SSRRenderPlan) (*JsProgram, error) {
	if plan == nil || plan.ComponentName == "" || plan.ScopeID == "" {
		return nil, fmt.Errorf("ssr hydrate entry: missing component identity")
	}
	functionName := "hydrate" + plan.ComponentName
	entry := &JsFuncDecl{
		baseStmt: baseStmt{genNode()},
		Name:     functionName,
		Params:   []JsExpr{id("$$root")},
		Body: &JsBlock{baseStmt: baseStmt{genNode()}, Body: []JsStmt{
			genConst("$$walker", genCall(id("$$createHydrationWalker"), id("$$root"), strLit(plan.ScopeID))),
			genConst("$$payload", genCall(id("$$readHydrationPayload"), id("$$root"), strLit(plan.ScopeID))),
			genReturn(&JsObjectExpr{baseExpr: baseExpr{genNode()}, Props: []*JsProp{
				{baseNode: genNode(), Key: id("root"), Value: id("$$root"), Kind: "init"},
				{baseNode: genNode(), Key: id("props"), Value: genMember(id("$$payload"), "props"), Kind: "init"},
				{baseNode: genNode(), Key: id("state"), Value: genMember(id("$$payload"), "state"), Kind: "init"},
				{baseNode: genNode(), Key: id("ranges"), Value: genMember(id("$$walker"), "ranges"), Kind: "init"},
			}}),
		}},
	}
	program := &JsProgram{baseNode: genNode(), Body: []JsStmt{
		&JsImportDecl{baseStmt: baseStmt{genNode()}, Specifiers: []JsImportSpec{
			{Imported: "$$createHydrationWalker"},
			{Imported: "$$readHydrationPayload"},
		}, Source: "./runtime.js"},
		&JsExportDecl{baseStmt: baseStmt{genNode()}, Declaration: entry},
		&JsExportDecl{baseStmt: baseStmt{genNode()}, Default: true, Declaration: genExprStmt(id(functionName))},
	}}
	validateGeneratedProgram(program)
	return program, nil
}
