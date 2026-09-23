package main

type hydrationKeyedAwaitNestedTextRecipe struct {
	Block   *EachBlockNode
	Element *ElementNode
	Part    *Part
	Attr    *Attr
	Class   *Attr
	Style   *Attr
}

// hydrationKeyedAwaitNestedTextRow is the first server-settled inner-row
// recipe. It deliberately keeps the executable boundary at one direct element
// with one dynamic text part. The broader DirectLeafRows recipe remains typed
// in the lifecycle plan, but attributes, listeners, controls, and children
// need their own owner-local adopt handles before they can join this path.
func hydrationKeyedAwaitNestedTextRow(block *EachBlockNode) (*ElementNode, *Part, *Attr, *Attr, *Attr, bool) {
	if block == nil || !block.HasKey() {
		return nil, nil, nil, nil, nil, false
	}
	descriptor, ok := hydrationAwaitSnapshotKeyedEachDirectRow(block.Children, block.ItemName)
	if !ok || descriptor.Element == nil || descriptor.Text == nil || len(descriptor.Text.Parts) != 1 || descriptor.Text.Parts[0] == nil || descriptor.Text.Parts[0].Static {
		return nil, nil, nil, nil, nil, false
	}
	// The direct-row descriptor already proves exact cardinality and item-root
	// ownership for each optional leaf. This recipe accepts one ordinary
	// attribute, one class directive, and one style directive; events, bindings,
	// components, and deeper structure have separate ownership recipes.
	return descriptor.Element, descriptor.Text.Parts[0], descriptor.Attr, descriptor.ClassAttr, descriptor.StyleAttr, true
}

// buildHydrationKeyedAwaitNestedTextRowAdopt connects an already server-settled
// inner keyed range to the outer keyed-await row. It receives parser-owned
// AST nodes and the typed lifecycle recipe; no printable template source is
// inspected or regenerated. Initial attachment only acquires ranges and
// callbacks. CSR DOM is created exclusively by innerCreate for a later key.
func (g *ASTGen) buildHydrationKeyedAwaitNestedTextRowAdopt(outer *EachBlockNode, nested *EachBlockNode, element *ElementNode, part *Part, attr, classAttr, styleAttr *Attr, outerState, outerItem, outerIndex string, awaitRange JsExpr) []JsStmt {
	if outer == nil || nested == nil || element == nil || part == nil || part.Expr == nil {
		panic("vela: missing nested keyed await text-row adoption recipe")
	}
	plan := g.hydrationLifecycleNode(SSRLifecyclePlanEach, nested.Span)
	if plan.KeyedEach == nil || !plan.KeyedEach.DirectLeafRows {
		panic("vela: missing typed nested keyed await text-row lifecycle recipe")
	}
	markerID, ok := g.hydrationMarkers.keyedEachID(plan.KeyedEach)
	if !ok {
		panic("vela: missing nested keyed await row marker")
	}
	textMarkerID := g.hydrationTextMarkerID(part)
	g.hydrationKeyedRows = true
	g.hydrationNestedRanges = true
	g.hydrationNestedText = true

	prefix := "$$hsettled" + itoa(markerID) + "_" + itoa(nested.Span.Start)
	itemsVar, blocksVar, keyVar := prefix+"_items", prefix+"_blocks", prefix+"_key"
	findElementVar, findTextVar := prefix+"_find_element", prefix+"_find_text"
	attachVar, createVar, updateVar, refreshVar := prefix+"_attach", prefix+"_create", prefix+"_update", prefix+"_refresh"
	indexVar, nestedItemVar, nestedIndexVar := prefix+"_index", prefix+"_item", prefix+"_item_index"

	itemsExpr := JsExpr(genArray())
	g.withTemplateBinding(outer.ItemName, outerItem, func() {
		g.withTemplateBinding(outer.IndexName, outerIndex, func() {
			itemsExpr = g.templateExpr(nested.IterExpr)
		})
	})
	keyExpr := func(item, index string) JsExpr {
		value := JsExpr(id("undefined"))
		g.withTemplateBinding(nested.ItemName, item, func() {
			g.withTemplateBinding(nested.IndexName, index, func() {
				value = genString(g.templateExpr(plan.KeyedEach.Key))
			})
		})
		return value
	}
	textValue := func(item, index string) JsExpr {
		value := JsExpr(strLit(""))
		g.withTemplateBinding(outer.ItemName, outerItem, func() {
			g.withTemplateBinding(outer.IndexName, outerIndex, func() {
				g.withTemplateBinding(nested.ItemName, item, func() {
					g.withTemplateBinding(nested.IndexName, index, func() {
						value = genString(g.templateExpr(part.Expr))
					})
				})
			})
		})
		return value
	}
	attrValue := func(item, index string) JsExpr {
		if attr == nil {
			return strLit("")
		}
		value := JsExpr(strLit(""))
		g.withTemplateBinding(outer.ItemName, outerItem, func() {
			g.withTemplateBinding(outer.IndexName, outerIndex, func() {
				g.withTemplateBinding(nested.ItemName, item, func() {
					g.withTemplateBinding(nested.IndexName, index, func() {
						value = genString(g.templateExpr(attr.Expr))
					})
				})
			})
		})
		return value
	}
	directiveValue := func(directive *Attr, item, index string, boolean bool) JsExpr {
		if directive == nil {
			return boolLit(false)
		}
		value := JsExpr(strLit(""))
		g.withTemplateBinding(outer.ItemName, outerItem, func() {
			g.withTemplateBinding(outer.IndexName, outerIndex, func() {
				g.withTemplateBinding(nested.ItemName, item, func() {
					g.withTemplateBinding(nested.IndexName, index, func() {
						expr := g.templateExpr(directive.Expr)
						if boolean {
							value = genBool(expr)
						} else {
							value = genString(expr)
						}
					})
				})
			})
		})
		return value
	}
	keyOf := &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$item"), id("$$index")}, Body: keyExpr("$$item", "$$index")}

	findElement := &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$row")}, Body: genBlock(
		genLet("$$node", genMember(genMember(id("$$row"), "start"), "nextSibling")),
		&JsWhileStmt{baseStmt: baseStmt{genNode()}, Test: genLogical("&&", id("$$node"), genBinary("!==", id("$$node"), genMember(id("$$row"), "end"))), Body: genBlock(
			genIf(genBinary("===", genMember(id("$$node"), "nodeType"), numLit(1)), genReturn(id("$$node"))),
			genAssignStmt(id("$$node"), genMember(id("$$node"), "nextSibling")),
		)},
		genReturn(genNull()),
	)}
	markerSuffix := ":" + itoa(textMarkerID)
	findText := &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$row")}, Body: genBlock(
		genConst("$$element", genCall(id(findElementVar), id("$$row"))),
		genIf(genUnary("!", id("$$element")), genReturn(genNull())),
		genConst("$$walker", genCall(genMember(id("document"), "createTreeWalker"), id("$$element"), numLit(128))),
		genLet("$$start", genNull()),
		genLet("$$node", genCall(genMember(id("$$walker"), "nextNode"))),
		&JsWhileStmt{baseStmt: baseStmt{genNode()}, Test: id("$$node"), Body: genBlock(
			genConst("$$data", genCall(id("String"), genLogical("||", genMember(id("$$node"), "data"), strLit("")))),
			genIf(genLogical("&&", genCall(genMember(id("$$data"), "startsWith"), strLit("v:")), genCall(genMember(id("$$data"), "endsWith"), strLit(markerSuffix))), genAssignStmt(id("$$start"), id("$$node"))),
			genIf(genLogical("&&", id("$$start"), genLogical("&&", genCall(genMember(id("$$data"), "startsWith"), strLit("/v:")), genCall(genMember(id("$$data"), "endsWith"), strLit(markerSuffix)))), genReturn(&JsObjectExpr{baseExpr: baseExpr{genNode()}, Props: []*JsProp{{Key: id("start"), Value: id("$$start")}, {Key: id("end"), Value: id("$$node")}}})),
			genAssignStmt(id("$$node"), genCall(genMember(id("$$walker"), "nextNode"))),
		)},
		genReturn(genNull()),
	)}

	// The CSR create path declares its mutable item/index bindings before it
	// constructs the initial text node. Hydrated attachment has no such scope,
	// so it asks controllerFor to declare them. Keeping this distinction here
	// avoids duplicate lexical declarations in generated JS.
	controllerFor := func(row, item, index, elementNode, textNode JsExpr, hydrated, declareBindings bool) []JsStmt {
		innerKeyVar, innerVar, textUpdateVar := "$$inner_key", "$$inner", "$$inner_text_update"
		body := []JsStmt{
			genConst(innerKeyVar, genCall(id(keyVar), item, index)),
		}
		if declareBindings {
			body = append(body,
				genLet(nestedItemVar, item),
				genLet(nestedIndexVar, index),
			)
		}
		body = append(body, genConst(innerVar, genCall(id("$$createHydrationRowController"), row, &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$next_item"), id("$$next_index")}, Body: genBlock(
			genAssignStmt(id(nestedItemVar), id("$$next_item")),
			genAssignStmt(id(nestedIndexVar), id("$$next_index")),
			genForOf("$$update", genMember(id(innerVar), "updates"), genExprStmt(genCall(id("$$update")))),
		)}, &JsArrowFunc{baseExpr: baseExpr{genNode()}, Body: genBlock(
			genExprStmt(genCall(genMember(genMember(id(outerState), "innerRows"), "delete"), id(innerKeyVar))),
		)})))
		if hydrated {
			body = append(body,
				genConst("$$text_range", genCall(id(findTextVar), row)),
				genIf(genUnary("!", id("$$text_range")), &JsThrowStmt{baseStmt: baseStmt{genNode()}, Arg: genNew(id("Error"), strLit("Vela hydration mismatch: missing nested keyed row text range"))}),
				genConst(textUpdateVar, &JsArrowFunc{baseExpr: baseExpr{genNode()}, Body: genBlock(
					genExprStmt(genCall(id("$$writeHydrationText"), id("$$text_range"), textValue(nestedItemVar, nestedIndexVar))),
				)}),
			)
		} else {
			body = append(body, genConst(textUpdateVar, &JsArrowFunc{baseExpr: baseExpr{genNode()}, Body: genBlock(
				genAssignStmt(genMember(textNode, "data"), textValue(nestedItemVar, nestedIndexVar)),
			)}))
		}
		body = append(body,
			genExprStmt(genCall(genMember(genMember(id(innerVar), "updates"), "push"), id(textUpdateVar))),
		)
		if attr != nil {
			attrValueVar, attrUpdateVar := "$$inner_attr_value", "$$inner_attr_update"
			body = append(body,
				genConst(attrUpdateVar, &JsArrowFunc{baseExpr: baseExpr{genNode()}, Body: genBlock(
					genConst(attrValueVar, attrValue(nestedItemVar, nestedIndexVar)),
					genIf(genBinary("!==", genCall(genMember(elementNode, "getAttribute"), strLit(attr.Name)), id(attrValueVar)), genExprStmt(genCall(genMember(elementNode, "setAttribute"), strLit(attr.Name), id(attrValueVar)))),
				)}),
				genExprStmt(genCall(genMember(genMember(id(innerVar), "updates"), "push"), id(attrUpdateVar))),
			)
		}
		if classAttr != nil {
			classUpdateVar := "$$inner_class_update"
			body = append(body,
				genConst(classUpdateVar, &JsArrowFunc{baseExpr: baseExpr{genNode()}, Body: genBlock(
					genExprStmt(genCall(genMember(genMember(elementNode, "classList"), "toggle"), strLit(classAttr.Name), directiveValue(classAttr, nestedItemVar, nestedIndexVar, true))),
				)}),
				genExprStmt(genCall(genMember(genMember(id(innerVar), "updates"), "push"), id(classUpdateVar))),
			)
		}
		if styleAttr != nil {
			styleUpdateVar := "$$inner_style_update"
			body = append(body,
				genConst(styleUpdateVar, &JsArrowFunc{baseExpr: baseExpr{genNode()}, Body: genBlock(
					genExprStmt(genCall(genMember(genMember(elementNode, "style"), "setProperty"), strLit(styleAttr.Name), directiveValue(styleAttr, nestedItemVar, nestedIndexVar, false))),
				)}),
				genExprStmt(genCall(genMember(genMember(id(innerVar), "updates"), "push"), id(styleUpdateVar))),
			)
		}
		body = append(body,
			genExprStmt(genCall(genMember(genMember(id(outerState), "innerRows"), "set"), id(innerKeyVar), id(innerVar))),
			genAssignStmt(genMember(row, "controller"), id(innerVar)),
		)
		return body
	}

	attachBody := []JsStmt{
		genConst("$$element", genCall(id(findElementVar), id("$$row"))),
		genIf(genUnary("!", id("$$element")), &JsThrowStmt{baseStmt: baseStmt{genNode()}, Arg: genNew(id("Error"), strLit("Vela hydration mismatch: missing nested keyed row element"))}),
	}
	attachBody = append(attachBody, controllerFor(id("$$row"), id("$$item"), id("$$index"), id("$$element"), genNull(), true, true)...)
	attach := &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$row"), id("$$item"), id("$$index")}, Body: genBlock(attachBody...)}
	createBody := []JsStmt{
		genConst("$$fragment", genCall(genMember(id("document"), "createDocumentFragment"))),
		genConst("$$start", genCall(genMember(id("document"), "createComment"), strLit("r"))),
		genExprStmt(genCall(genMember(id("$$fragment"), "append"), id("$$start"))),
		genConst("$$element", genCall(genMember(id("document"), "createElement"), strLit(element.Tag))),
		g.setAttrStmt("$$element", g.scopeID, ""),
	}
	for _, elementAttr := range element.Attrs {
		if elementAttr != nil && elementAttr.Expr == nil {
			createBody = append(createBody, g.setAttrStmt("$$element", elementAttr.Name, elementAttr.Value))
		}
	}
	createBody = append(createBody,
		genLet(nestedItemVar, id("$$item")),
		genLet(nestedIndexVar, id("$$index")),
	)
	if attr != nil {
		createBody = append(createBody, genExprStmt(genCall(genMember(id("$$element"), "setAttribute"), strLit(attr.Name), attrValue(nestedItemVar, nestedIndexVar))))
	}
	if classAttr != nil {
		createBody = append(createBody, genExprStmt(genCall(genMember(genMember(id("$$element"), "classList"), "toggle"), strLit(classAttr.Name), directiveValue(classAttr, nestedItemVar, nestedIndexVar, true))))
	}
	if styleAttr != nil {
		createBody = append(createBody, genExprStmt(genCall(genMember(genMember(id("$$element"), "style"), "setProperty"), strLit(styleAttr.Name), directiveValue(styleAttr, nestedItemVar, nestedIndexVar, false))))
	}
	createBody = append(createBody,
		genConst("$$text", genCall(genMember(id("document"), "createTextNode"), textValue(nestedItemVar, nestedIndexVar))),
		genExprStmt(genCall(genMember(id("$$element"), "appendChild"), id("$$text"))),
		genExprStmt(genCall(genMember(id("$$fragment"), "appendChild"), id("$$element"))),
		genConst("$$end", genCall(genMember(id("document"), "createComment"), strLit("/r"))),
		genExprStmt(genCall(genMember(id("$$fragment"), "append"), id("$$end"))),
		genConst("$$row", &JsObjectExpr{baseExpr: baseExpr{genNode()}, Props: []*JsProp{{Key: id("start"), Value: id("$$start")}, {Key: id("end"), Value: id("$$end")}}}),
	)
	createBody = append(createBody, controllerFor(id("$$row"), id("$$item"), id("$$index"), id("$$element"), id("$$text"), false, false)...)
	createBody = append(createBody, genReturn(id("$$row")))
	create := &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$item"), id("$$index")}, Body: genBlock(createBody...)}
	update := &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$row"), id("$$item"), id("$$index")}, Body: genBlock(
		genIf(genLogical("&&", id("$$row"), genMember(id("$$row"), "controller")), genExprStmt(genCall(genMember(genMember(id("$$row"), "controller"), "update"), id("$$item"), id("$$index")))),
	)}
	refresh := &JsArrowFunc{baseExpr: baseExpr{genNode()}, Body: genBlock(
		genConst("$$next_items", itemsExpr),
		genAssignStmt(id(blocksVar), genCall(id("$$reconcileHydrationKeyedRowRanges"), awaitRange, id(blocksVar), id("$$next_items"), id(keyVar), id(createVar), id(updateVar))),
	)}

	return []JsStmt{
		genConst(findElementVar, findElement),
		genConst(findTextVar, findText),
		genConst(keyVar, keyOf),
		genConst(itemsVar, itemsExpr),
		genLet(blocksVar, genCall(id("$$adoptHydrationKeyedRowRanges"), awaitRange, id(itemsVar), id(keyVar), numLit(markerID))),
		genConst(attachVar, attach),
		genConst(createVar, create),
		genConst(updateVar, update),
		genLet(indexVar, numLit(0)),
		genForOf("$$item", id(itemsVar), genBlock(
			genExprStmt(genCall(id(attachVar), genCall(genMember(id(blocksVar), "get"), genCall(id(keyVar), id("$$item"), id(indexVar))), id("$$item"), id(indexVar))),
			genAssignStmt(id(indexVar), genBinary("+", id(indexVar), numLit(1))),
		)),
		genConst(refreshVar, refresh),
		genExprStmt(genCall(genMember(genMember(id(outerState), "innerRefresh"), "push"), id(refreshVar))),
	}
}
