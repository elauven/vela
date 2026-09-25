package main

type hydrationKeyedAwaitNestedTextRecipe struct {
	Block   *EachBlockNode
	Element *ElementNode
	Part    *Part
	Attr    *Attr
	Class   *Attr
	Style   *Attr
	Event   *Attr
}

// hydrationKeyedAwaitNestedControlRecipe deliberately stays separate from the
// text-row recipe: a native control has a browser-owned live property and must
// read that property before any later reactive writer is allowed to run.
type hydrationKeyedAwaitNestedControlRecipe struct {
	Block              *EachBlockNode
	Element            *ElementNode
	Bind               *Attr
	OptionText         *TextNode
	OptionTextIndex    int
	OptionDynamic      *Attr
	OptionDynamicIndex int
	OptionGroupDynamic *Attr
	OptionGroupIndex   int
	OptionList         *hydrationKeyedEachSelectOptionListDescriptor
	// OptionLists is deliberately separate from OptionList. A select with
	// several dynamic optgroups cannot share a single keyed-element Map:
	// identity belongs to an option's immediate optgroup owner. The first
	// multi-owner lowering accepts exactly two keyed lists and retains one Map
	// per descriptor. Until that lowering is selected, this field stays empty
	// and the existing single-owner path remains unchanged.
	OptionLists []*hydrationKeyedEachSelectOptionListDescriptor
}

// hydrationKeyedAwaitNestedMultiSelectOptionLists recognizes the deliberately
// narrow first multi-owner shape:
//
//	<select bind:value={field.choice}>
//	  <optgroup label="A">{#each field.a as option (option.id)}…{/each}</optgroup>
//	  <optgroup label="B">{#each field.b as option (option.id)}…{/each}</optgroup>
//	</select>
//
// Both owners are static-label optgroups with a keyed list and no fallback.
// Dynamic labels, fallback branches, unkeyed replacement and direct options
// intentionally remain outside this recipe: adopting one owner's option
// nodes into another owner's Map would silently corrupt DOM identity on a
// later update. The capability gate must not claim support until the matching
// per-owner controller lowering is emitted.
func hydrationKeyedAwaitNestedMultiSelectOptionLists(block *EachBlockNode) (*ElementNode, *Attr, []*hydrationKeyedEachSelectOptionListDescriptor, bool) {
	if block == nil || !block.HasKey() {
		return nil, nil, nil, false
	}
	descriptor, ok := hydrationAwaitSnapshotKeyedEachDirectControlRow(block.Children, block.ItemName)
	if !ok || descriptor.Element == nil || descriptor.Element.Tag != "select" || descriptor.Bind == nil || descriptor.Bind.Name != "value" {
		return nil, nil, nil, false
	}
	if descriptor.OptionDynamic != nil || descriptor.OptionText != nil || descriptor.OptionGroupDynamic != nil || len(descriptor.OptionGroupDynamics) != 0 || len(descriptor.OptionLists) != 2 {
		return nil, nil, nil, false
	}
	for _, list := range descriptor.OptionLists {
		if list == nil || list.Kind != hydrationKeyedEachSelectOptionListKeyed || list.Group == nil || list.Key == nil || len(list.Fallback) != 0 {
			return nil, nil, nil, false
		}
		if len(list.Groups) != 2 {
			return nil, nil, nil, false
		}
	}
	// Parser ownership is source ordered. Recheck it here because the lowering
	// indexes optgroup DOM nodes by GroupIndex rather than by a broad descendant
	// selector.
	if descriptor.OptionLists[0].GroupIndex >= descriptor.OptionLists[1].GroupIndex {
		return nil, nil, nil, false
	}
	return descriptor.Element, descriptor.Bind, descriptor.OptionLists, true
}

func hydrationKeyedAwaitNestedInputControlRow(block *EachBlockNode) (*ElementNode, *Attr, *TextNode, int, *Attr, int, *Attr, int, *hydrationKeyedEachSelectOptionListDescriptor, bool) {
	if block == nil || !block.HasKey() {
		return nil, nil, nil, -1, nil, -1, nil, -1, nil, false
	}
	descriptor, ok := hydrationAwaitSnapshotKeyedEachDirectControlRow(block.Children, block.ItemName)
	if !ok || descriptor.Element == nil || descriptor.Bind == nil {
		return nil, nil, nil, -1, nil, -1, nil, -1, nil, false
	}
	if descriptor.Element.Tag == "textarea" && descriptor.Bind.Name == "value" {
		return descriptor.Element, descriptor.Bind, nil, -1, nil, -1, nil, -1, nil, true
	}
	if descriptor.Element.Tag == "select" && descriptor.Bind.Name == "value" {
		if hydrationAwaitSnapshotKeyedEachStaticSelectOptions(descriptor.Element.Children) {
			return descriptor.Element, descriptor.Bind, nil, -1, nil, -1, nil, -1, nil, true
		}
		if descriptor.OptionText != nil && descriptor.OptionTextIndex >= 0 && descriptor.OptionDynamic == nil && descriptor.OptionGroupDynamic == nil && len(descriptor.OptionLists) == 0 {
			return descriptor.Element, descriptor.Bind, descriptor.OptionText, descriptor.OptionTextIndex, nil, -1, nil, -1, nil, true
		}
		if descriptor.OptionDynamic != nil && (descriptor.OptionDynamic.Name == "disabled" || descriptor.OptionDynamic.Name == "value") && descriptor.OptionDynamicIndex >= 0 && descriptor.OptionText == nil && descriptor.OptionGroupDynamic == nil && len(descriptor.OptionLists) == 0 {
			return descriptor.Element, descriptor.Bind, nil, -1, descriptor.OptionDynamic, descriptor.OptionDynamicIndex, nil, -1, nil, true
		}
		if descriptor.OptionGroupDynamic != nil && descriptor.OptionGroupIndex >= 0 && descriptor.OptionDynamic == nil && descriptor.OptionText == nil && len(descriptor.OptionLists) == 0 {
			return descriptor.Element, descriptor.Bind, nil, -1, nil, -1, descriptor.OptionGroupDynamic, descriptor.OptionGroupIndex, nil, true
		}
		if descriptor.OptionList != nil && (descriptor.OptionList.Group == nil || descriptor.OptionList.Key != nil || len(descriptor.OptionList.Fallback) == 0) && descriptor.OptionDynamic == nil && (descriptor.OptionGroupDynamic == nil || (descriptor.OptionList.Group != nil && descriptor.OptionList.Key != nil)) && descriptor.OptionText == nil && len(descriptor.OptionLists) == 1 {
			return descriptor.Element, descriptor.Bind, nil, -1, nil, -1, descriptor.OptionGroupDynamic, descriptor.OptionGroupIndex, descriptor.OptionList, true
		}
	}
	if descriptor.Element.Tag != "input" || (descriptor.Bind.Name != "value" && descriptor.Bind.Name != "checked") {
		return nil, nil, nil, -1, nil, -1, nil, -1, nil, false
	}
	return descriptor.Element, descriptor.Bind, nil, -1, nil, -1, nil, -1, nil, true
}

// hydrationKeyedAwaitNestedTextRow is the first server-settled inner-row
// recipe. It deliberately keeps the executable boundary at one direct element
// with one dynamic text part. The broader DirectLeafRows recipe remains typed
// in the lifecycle plan, but attributes, listeners, controls, and children
// need their own owner-local adopt handles before they can join this path.
func hydrationKeyedAwaitNestedTextRow(block *EachBlockNode) (*ElementNode, *Part, *Attr, *Attr, *Attr, *Attr, bool) {
	if block == nil || !block.HasKey() {
		return nil, nil, nil, nil, nil, nil, false
	}
	descriptor, ok := hydrationAwaitSnapshotKeyedEachDirectRow(block.Children, block.ItemName)
	if !ok || descriptor.Element == nil || descriptor.Text == nil || len(descriptor.Text.Parts) != 1 || descriptor.Text.Parts[0] == nil || descriptor.Text.Parts[0].Static {
		return nil, nil, nil, nil, nil, nil, false
	}
	// The direct-row descriptor already proves exact cardinality and item-root
	// ownership for each optional leaf. This recipe accepts one ordinary
	// attribute, one class directive, and one style directive; events, bindings,
	// components, and deeper structure have separate ownership recipes.
	return descriptor.Element, descriptor.Text.Parts[0], descriptor.Attr, descriptor.ClassAttr, descriptor.StyleAttr, descriptor.EventAttr, true
}

// buildHydrationKeyedAwaitNestedTextRowAdopt connects an already server-settled
// inner keyed range to the outer keyed-await row. It receives parser-owned
// AST nodes and the typed lifecycle recipe; no printable template source is
// inspected or regenerated. Initial attachment only acquires ranges and
// callbacks. CSR DOM is created exclusively by innerCreate for a later key.
func (g *ASTGen) buildHydrationKeyedAwaitNestedTextRowAdopt(outer *EachBlockNode, nested *EachBlockNode, element *ElementNode, part *Part, attr, classAttr, styleAttr, eventAttr *Attr, outerState, outerItem, outerIndex string, awaitRange JsExpr) []JsStmt {
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
	eventHandler := func() JsExpr {
		if eventAttr == nil {
			return id("undefined")
		}
		value := JsExpr(id("undefined"))
		g.withTemplateBinding(outer.ItemName, outerItem, func() {
			g.withTemplateBinding(outer.IndexName, outerIndex, func() {
				g.withTemplateBinding(nested.ItemName, nestedItemVar, func() {
					g.withTemplateBinding(nested.IndexName, nestedIndexVar, func() {
						value = g.eventHandlerExpr(eventAttr)
					})
				})
			})
		})
		if value == nil {
			value = id("undefined")
		}
		if !templateExprIsHandler(eventAttr.Expr) {
			value = &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$event")}, Body: genBlock(genExprStmt(value))}
		}
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
		if eventAttr != nil {
			eventHandlerVar := "$$inner_event_handler"
			body = append(body,
				genConst(eventHandlerVar, eventHandler()),
				genExprStmt(genCall(genMember(elementNode, "addEventListener"), strLit(eventAttr.Name), id(eventHandlerVar))),
				genExprStmt(genCall(genMember(genMember(id(innerVar), "listeners"), "push"), &JsArrowFunc{baseExpr: baseExpr{genNode()}, Body: genBlock(
					genExprStmt(genCall(genMember(elementNode, "removeEventListener"), strLit(eventAttr.Name), id(eventHandlerVar))),
				)})),
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

// buildHydrationKeyedAwaitNestedInputControlAdopt owns the first browser-live
// control recipe. It accepts input/textarea bind:value or checkbox bind:checked under an outer $state.deep
// collection. The initial attach reads the live input before registering its
// guarded writer, so user input made after SSR paint is retained.
func (g *ASTGen) buildHydrationKeyedAwaitNestedInputControlAdopt(outer *EachBlockNode, nested *EachBlockNode, element *ElementNode, bind *Attr, optionText *TextNode, optionTextIndex int, optionDynamic *Attr, optionDynamicIndex int, optionGroupDynamic *Attr, optionGroupIndex int, optionList *hydrationKeyedEachSelectOptionListDescriptor, outerState, outerItem, outerIndex string, awaitRange JsExpr) []JsStmt {
	if outer == nil || nested == nil || element == nil || bind == nil || bind.Expr == nil {
		panic("vela: missing nested keyed await value-control adoption recipe")
	}
	plan := g.hydrationLifecycleNode(SSRLifecyclePlanEach, nested.Span)
	if plan.KeyedEach == nil || !plan.KeyedEach.DirectControlRows {
		panic("vela: missing typed nested keyed await value-control lifecycle recipe")
	}
	markerID, ok := g.hydrationMarkers.keyedEachID(plan.KeyedEach)
	if !ok {
		panic("vela: missing nested keyed await value-control marker")
	}
	g.hydrationKeyedRows = true
	g.hydrationNestedRanges = true
	spec, ok := twoWayBindSpec(element.Tag, bind.Name)
	if !ok {
		panic("vela: missing nested keyed await input binding specification")
	}

	prefix := "$$hsettled" + itoa(markerID) + "_" + itoa(nested.Span.Start) + "_control"
	itemsVar, blocksVar, keyVar := prefix+"_items", prefix+"_blocks", prefix+"_key"
	findElementVar, attachVar, createVar := prefix+"_find_element", prefix+"_attach", prefix+"_create"
	updateVar, refreshVar, indexVar := prefix+"_update", prefix+"_refresh", prefix+"_index"
	nestedItemVar, nestedIndexVar := prefix+"_item", prefix+"_item_index"

	itemsExpr := JsExpr(genArray())
	g.withTemplateBinding(outer.ItemName, outerItem, func() {
		g.withTemplateBinding(outer.IndexName, outerIndex, func() { itemsExpr = g.templateExpr(nested.IterExpr) })
	})
	keyExpr := func(item, index string) JsExpr {
		value := JsExpr(id("undefined"))
		g.withTemplateBinding(nested.ItemName, item, func() {
			g.withTemplateBinding(nested.IndexName, index, func() { value = genString(g.templateExpr(plan.KeyedEach.Key)) })
		})
		return value
	}
	valueExpr := func(item, index string) JsExpr {
		value := JsExpr(strLit(""))
		g.withTemplateBinding(outer.ItemName, outerItem, func() {
			g.withTemplateBinding(outer.IndexName, outerIndex, func() {
				g.withTemplateBinding(nested.ItemName, item, func() {
					g.withTemplateBinding(nested.IndexName, index, func() {
						expr := g.templateExpr(bind.Expr)
						if bind.Name == "checked" {
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
	optionTextValue := func(item, index string) JsExpr {
		value := JsExpr(strLit(""))
		if optionText == nil {
			return value
		}
		g.withTemplateBinding(outer.ItemName, outerItem, func() {
			g.withTemplateBinding(outer.IndexName, outerIndex, func() {
				g.withTemplateBinding(nested.ItemName, item, func() {
					g.withTemplateBinding(nested.IndexName, index, func() { value = g.textValueExpr(optionText.Parts) })
				})
			})
		})
		return value
	}
	optionDynamicValue := func(item, index string) JsExpr {
		value := JsExpr(boolLit(false))
		if optionDynamic == nil {
			return value
		}
		g.withTemplateBinding(outer.ItemName, outerItem, func() {
			g.withTemplateBinding(outer.IndexName, outerIndex, func() {
				g.withTemplateBinding(nested.ItemName, item, func() {
					g.withTemplateBinding(nested.IndexName, index, func() {
						expr := g.templateExpr(optionDynamic.Expr)
						if optionDynamic.Name == "value" {
							value = genString(expr)
						} else {
							value = genBool(expr)
						}
					})
				})
			})
		})
		return value
	}
	optionGroupValue := func(item, index string) JsExpr {
		value := JsExpr(strLit(""))
		if optionGroupDynamic == nil {
			return value
		}
		g.withTemplateBinding(outer.ItemName, outerItem, func() {
			g.withTemplateBinding(outer.IndexName, outerIndex, func() {
				g.withTemplateBinding(nested.ItemName, item, func() {
					g.withTemplateBinding(nested.IndexName, index, func() { value = genString(g.templateExpr(optionGroupDynamic.Expr)) })
				})
			})
		})
		return value
	}
	optionListItems := func(item, index string) JsExpr {
		value := JsExpr(genArray())
		if optionList == nil {
			return value
		}
		g.withTemplateBinding(outer.ItemName, outerItem, func() {
			g.withTemplateBinding(outer.IndexName, outerIndex, func() {
				g.withTemplateBinding(nested.ItemName, item, func() {
					g.withTemplateBinding(nested.IndexName, index, func() { value = g.templateExpr(optionList.Each.IterExpr) })
				})
			})
		})
		return value
	}
	optionListFragment := JsExpr(id("undefined"))
	optionListKey := JsExpr(id("undefined"))
	optionListCreate := JsExpr(id("undefined"))
	optionListUpdate := JsExpr(id("undefined"))
	if optionList != nil {
		var optionValue, optionText JsExpr
		g.withTemplateBinding(optionList.Each.ItemName, "$$option", func() {
			g.withTemplateBinding(optionList.Each.IndexName, "$$option_index", func() {
				optionValue = genString(g.templateExpr(optionList.ValueAttr.Expr))
				optionText = g.textValueExpr(optionList.Text.Parts)
			})
		})
		optionListFragment = &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$items")}, Body: genBlock(
			genConst("$$option_fragment", genCall(genMember(id("document"), "createDocumentFragment"))),
			genLet("$$option_index", numLit(0)),
			genForOf("$$option", genLogical("||", id("$$items"), genArray()), genBlock(
				genConst("$$option_element", genCall(genMember(id("document"), "createElement"), strLit("option"))),
				g.setAttrStmt("$$option_element", g.scopeID, ""),
				genAssignStmt(genMember(id("$$option_element"), "value"), optionValue),
				genExprStmt(genCall(genMember(id("$$option_element"), "appendChild"), genCall(genMember(id("document"), "createTextNode"), optionText))),
				genExprStmt(genCall(genMember(id("$$option_fragment"), "append"), id("$$option_element"))),
				genAssignStmt(id("$$option_index"), genBinary("+", id("$$option_index"), numLit(1))),
			)),
			genIf(genUnary("!", genLogical("&&", id("$$items"), genMember(id("$$items"), "length"))), genExprStmt(genCall(id("$$insertHTML"), id("$$option_fragment"), strLit(emitStaticHTMLWithoutEvents(optionList.Fallback, g.scopeID))))),
			genReturn(id("$$option_fragment")),
		)}
		if optionList.Key != nil {
			g.hydrationKeyedElements = true
			var optionKey JsExpr
			g.withTemplateBinding(optionList.Each.ItemName, "$$option", func() {
				g.withTemplateBinding(optionList.Each.IndexName, "$$option_index", func() { optionKey = g.templateExpr(optionList.Key) })
			})
			optionListKey = &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$option"), id("$$option_index")}, Body: optionKey}
			optionListCreate = &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$option"), id("$$option_index")}, Body: genBlock(
				genConst("$$option_element", genCall(genMember(id("document"), "createElement"), strLit("option"))),
				g.setAttrStmt("$$option_element", g.scopeID, ""),
				genAssignStmt(genMember(id("$$option_element"), "value"), optionValue),
				genExprStmt(genCall(genMember(id("$$option_element"), "appendChild"), genCall(genMember(id("document"), "createTextNode"), optionText))),
				genReturn(id("$$option_element")),
			)}
			optionListUpdate = &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$option_element"), id("$$option"), id("$$option_index")}, Body: genBlock(
				genIf(genBinary("!==", genMember(id("$$option_element"), "value"), optionValue), genAssignStmt(genMember(id("$$option_element"), "value"), optionValue)),
				genIf(genBinary("!==", genMember(id("$$option_element"), "textContent"), optionText), genAssignStmt(genMember(id("$$option_element"), "textContent"), optionText)),
			)}
		}
	}
	assignValue := func(item, index string, value JsExpr) JsStmt {
		var target JsExpr = id("undefined")
		g.withTemplateBinding(outer.ItemName, outerItem, func() {
			g.withTemplateBinding(outer.IndexName, outerIndex, func() {
				g.withTemplateBinding(nested.ItemName, item, func() {
					g.withTemplateBinding(nested.IndexName, index, func() { target = g.templateExpr(bind.Expr) })
				})
			})
		})
		return genAssignStmt(target, value)
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
	controllerFor := func(row, item, index, control JsExpr, declareBindings bool) []JsStmt {
		body := []JsStmt{genConst("$$inner_key", genCall(id(keyVar), item, index))}
		if declareBindings {
			body = append(body, genLet(nestedItemVar, item), genLet(nestedIndexVar, index))
		}
		body = append(body, genConst("$$inner", genCall(id("$$createHydrationRowController"), row, &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$next_item"), id("$$next_index")}, Body: genBlock(
			genAssignStmt(id(nestedItemVar), id("$$next_item")),
			genAssignStmt(id(nestedIndexVar), id("$$next_index")),
			genForOf("$$update", genMember(id("$$inner"), "updates"), genExprStmt(genCall(id("$$update")))),
		)}, &JsArrowFunc{baseExpr: baseExpr{genNode()}, Body: genBlock(
			genExprStmt(genCall(genMember(genMember(id(outerState), "innerRows"), "delete"), id("$$inner_key"))),
		)})))
		controlAttach := []JsStmt{
			genConst("$$inner_control_update", &JsArrowFunc{baseExpr: baseExpr{genNode()}, Body: genBlock(
				genConst("$$inner_control_value", valueExpr(nestedItemVar, nestedIndexVar)),
				genIf(genBinary("!==", genMember(control, spec.readProp), id("$$inner_control_value")), genAssignStmt(genMember(control, spec.readProp), id("$$inner_control_value"))),
			)}),
			genExprStmt(genCall(genMember(genMember(id("$$inner"), "updates"), "push"), id("$$inner_control_update"))),
			genConst("$$inner_control_handler", &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$event")}, Body: genBlock(assignValue(nestedItemVar, nestedIndexVar, genMember(genMember(id("$event"), "target"), spec.readProp)))}),
			genExprStmt(genCall(genMember(control, "addEventListener"), strLit(spec.event), id("$$inner_control_handler"))),
			genExprStmt(genCall(genMember(genMember(id("$$inner"), "listeners"), "push"), &JsArrowFunc{baseExpr: baseExpr{genNode()}, Body: genBlock(
				genExprStmt(genCall(genMember(control, "removeEventListener"), strLit(spec.event), id("$$inner_control_handler"))),
			)})),
			genExprStmt(genCall(genMember(genMember(id(outerState), "innerRows"), "set"), id("$$inner_key"), id("$$inner"))),
			genAssignStmt(genMember(row, "controller"), id("$$inner")),
		}
		optionDynamicAttach := []JsStmt{}
		if optionDynamic != nil {
			optionDynamicAttach = []JsStmt{
				genConst("$$inner_option_dynamic", genCall(genMember(genMember(control, "options"), "item"), numLit(optionDynamicIndex))),
				genIf(id("$$inner_option_dynamic"), genBlock(
					genConst("$$inner_option_dynamic_update", &JsArrowFunc{baseExpr: baseExpr{genNode()}, Body: genBlock(
						genConst("$$inner_option_dynamic_value", optionDynamicValue(nestedItemVar, nestedIndexVar)),
						genIf(genBinary("!==", genMember(id("$$inner_option_dynamic"), optionDynamic.Name), id("$$inner_option_dynamic_value")), genAssignStmt(genMember(id("$$inner_option_dynamic"), optionDynamic.Name), id("$$inner_option_dynamic_value"))),
					)}),
					genExprStmt(genCall(genMember(genMember(id("$$inner"), "updates"), "push"), id("$$inner_option_dynamic_update"))),
				)),
			}
			if optionDynamic.Name == "value" {
				body = append(body, optionDynamicAttach...)
			}
		}
		body = append(body, controlAttach...)
		if optionText != nil {
			body = append(body,
				genConst("$$inner_option", genCall(genMember(genMember(control, "options"), "item"), numLit(optionTextIndex))),
				genIf(id("$$inner_option"), genBlock(
					genConst("$$inner_option_update", &JsArrowFunc{baseExpr: baseExpr{genNode()}, Body: genBlock(
						genConst("$$inner_option_text", optionTextValue(nestedItemVar, nestedIndexVar)),
						genIf(genBinary("!==", genMember(id("$$inner_option"), "textContent"), id("$$inner_option_text")), genAssignStmt(genMember(id("$$inner_option"), "textContent"), id("$$inner_option_text"))),
					)}),
					genExprStmt(genCall(genMember(genMember(id("$$inner"), "updates"), "push"), id("$$inner_option_update"))),
				)),
			)
		}
		if optionDynamic != nil && optionDynamic.Name != "value" {
			body = append(body, optionDynamicAttach...)
		}
		if optionGroupDynamic != nil {
			body = append(body,
				genConst("$$inner_option_group", genCall(genMember(genCall(genMember(control, "querySelectorAll"), strLit("optgroup")), "item"), numLit(optionGroupIndex))),
				genIf(id("$$inner_option_group"), genBlock(
					genConst("$$inner_option_group_update", &JsArrowFunc{baseExpr: baseExpr{genNode()}, Body: genBlock(
						genConst("$$inner_option_group_value", optionGroupValue(nestedItemVar, nestedIndexVar)),
						genIf(genBinary("!==", genMember(id("$$inner_option_group"), "label"), id("$$inner_option_group_value")), genAssignStmt(genMember(id("$$inner_option_group"), "label"), id("$$inner_option_group_value"))),
					)}),
					genExprStmt(genCall(genMember(genMember(id("$$inner"), "updates"), "push"), id("$$inner_option_group_update"))),
				)),
			)
		}
		if optionList != nil {
			listParent := JsExpr(control)
			optionElements := JsExpr(genCall(genMember(id("Array"), "from"), genMember(control, "options")))
			optionItemsPresent := func(items JsExpr) JsExpr {
				return genLogical("&&", items, genMember(items, "length"))
			}
			if optionList.Group != nil {
				listParent = id("$$inner_option_parent")
				body = append(body,
					genConst("$$inner_option_parent", genCall(genMember(genCall(genMember(control, "querySelectorAll"), strLit("optgroup")), "item"), numLit(optionList.GroupIndex))),
					genIf(genUnary("!", listParent), &JsThrowStmt{baseStmt: baseStmt{genNode()}, Arg: genNew(id("Error"), strLit("Vela hydration mismatch: missing nested option-list optgroup owner"))}),
				)
				optionElements = genCall(genMember(id("Array"), "from"), genCall(genMember(listParent, "querySelectorAll"), strLit("option")))
			}
			if optionList.Key != nil {
				body = append(body,
					genConst("$$inner_option_items", optionListItems(nestedItemVar, nestedIndexVar)),
					genLet("$$inner_option_blocks", genNew(id("Map"))),
				)
				adoptKeyedOptions := []JsStmt{
					genConst("$$inner_option_elements", optionElements),
					genIf(genBinary("!==", genMember(id("$$inner_option_elements"), "length"), genMember(id("$$inner_option_items"), "length")), &JsThrowStmt{baseStmt: baseStmt{genNode()}, Arg: genNew(id("Error"), strLit("Vela hydration mismatch: keyed nested option list length differs from its server snapshot"))}),
					genLet("$$inner_option_index", numLit(0)),
					genForOf("$$inner_option_item", id("$$inner_option_items"), genBlock(
						genConst("$$inner_option_key", genCall(optionListKey, id("$$inner_option_item"), id("$$inner_option_index"))),
						genIf(genCall(genMember(id("$$inner_option_blocks"), "has"), id("$$inner_option_key")), &JsThrowStmt{baseStmt: baseStmt{genNode()}, Arg: genNew(id("Error"), strLit("Vela keyed update has duplicate key in nested option list"))}),
						genExprStmt(genCall(genMember(id("$$inner_option_blocks"), "set"), id("$$inner_option_key"), genComputedMember(id("$$inner_option_elements"), id("$$inner_option_index")))),
						genAssignStmt(id("$$inner_option_index"), genBinary("+", id("$$inner_option_index"), numLit(1))),
					)),
				}
				if len(optionList.Fallback) > 0 {
					body = append(body,
						genLet("$$inner_option_fallback", genUnary("!", optionItemsPresent(id("$$inner_option_items")))),
						genIf(genUnary("!", id("$$inner_option_fallback")), genBlock(adoptKeyedOptions...)),
					)
				} else {
					body = append(body, adoptKeyedOptions...)
				}
			}
			listUpdateBody := []JsStmt{
				genConst("$$inner_option_list_selected", valueExpr(nestedItemVar, nestedIndexVar)),
			}
			if optionList.Key != nil {
				optionListUpdateItems := optionListItems(nestedItemVar, nestedIndexVar)
				if len(optionList.Fallback) > 0 {
					listUpdateBody = append([]JsStmt{
						genConst("$$inner_option_list_items", optionListUpdateItems),
						genIf(genUnary("!", optionItemsPresent(id("$$inner_option_list_items"))), genBlock(
							genIf(genUnary("!", id("$$inner_option_fallback")), genBlock(
								genExprStmt(genCall(genMember(listParent, "replaceChildren"), genCall(optionListFragment, id("$$inner_option_list_items")))),
								genAssignStmt(id("$$inner_option_blocks"), genNew(id("Map"))),
								genAssignStmt(id("$$inner_option_fallback"), boolLit(true)),
							)),
						)),
						genIf(optionItemsPresent(id("$$inner_option_list_items")), genBlock(
							genIf(id("$$inner_option_fallback"), genBlock(
								genExprStmt(genCall(genMember(listParent, "replaceChildren"))),
								genAssignStmt(id("$$inner_option_blocks"), genNew(id("Map"))),
								genAssignStmt(id("$$inner_option_fallback"), boolLit(false)),
							)),
							genAssignStmt(id("$$inner_option_blocks"), genCall(id("$$reconcileHydrationKeyedElements"), listParent, id("$$inner_option_blocks"), id("$$inner_option_list_items"), optionListKey, optionListCreate, optionListUpdate)),
						)),
					}, listUpdateBody...)
				} else {
					listUpdateBody = append([]JsStmt{genAssignStmt(id("$$inner_option_blocks"), genCall(id("$$reconcileHydrationKeyedElements"), listParent, id("$$inner_option_blocks"), optionListUpdateItems, optionListKey, optionListCreate, optionListUpdate))}, listUpdateBody...)
				}
			} else {
				listUpdateBody = append([]JsStmt{genExprStmt(genCall(genMember(listParent, "replaceChildren"), genCall(optionListFragment, optionListItems(nestedItemVar, nestedIndexVar))))}, listUpdateBody...)
			}
			body = append(body,
				genConst("$$inner_option_list_update", &JsArrowFunc{baseExpr: baseExpr{genNode()}, Body: genBlock(append(listUpdateBody, genIf(genBinary("!==", genMember(control, spec.readProp), id("$$inner_option_list_selected")), genAssignStmt(genMember(control, spec.readProp), id("$$inner_option_list_selected"))))...)}),
				genExprStmt(genCall(genMember(genMember(id("$$inner"), "updates"), "push"), id("$$inner_option_list_update"))),
			)
		}
		return body
	}
	attachBody := []JsStmt{
		genConst("$$control", genCall(id(findElementVar), id("$$row"))),
		genIf(genUnary("!", id("$$control")), &JsThrowStmt{baseStmt: baseStmt{genNode()}, Arg: genNew(id("Error"), strLit("Vela hydration mismatch: missing nested keyed input"))}),
	}
	attachBody = append(attachBody, controllerFor(id("$$row"), id("$$item"), id("$$index"), id("$$control"), true)...)
	// Read before any update callback is run. The target is a deep-proxy member,
	// so this preserves an edit made after server paint without a DOM write.
	attachBody = append(attachBody, assignValue(nestedItemVar, nestedIndexVar, genMember(id("$$control"), spec.readProp)))
	attach := &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$row"), id("$$item"), id("$$index")}, Body: genBlock(attachBody...)}
	createBody := []JsStmt{
		genConst("$$fragment", genCall(genMember(id("document"), "createDocumentFragment"))),
		genConst("$$start", genCall(genMember(id("document"), "createComment"), strLit("r"))),
		genExprStmt(genCall(genMember(id("$$fragment"), "append"), id("$$start"))),
		genConst("$$control", genCall(genMember(id("document"), "createElement"), strLit(element.Tag))),
		g.setAttrStmt("$$control", g.scopeID, ""),
		genLet(nestedItemVar, id("$$item")), genLet(nestedIndexVar, id("$$index")),
	}
	for _, attr := range element.Attrs {
		if attr != nil && attr.Expr == nil {
			createBody = append(createBody, g.setAttrStmt("$$control", attr.Name, attr.Value))
		}
	}
	if element.Tag == "select" && len(element.Children) > 0 {
		if optionList != nil {
			if optionList.Group != nil {
				groups := optionList.Groups
				if len(groups) == 0 {
					groups = []*ElementNode{optionList.Group}
				}
				for groupIndex, group := range groups {
					groupVar := "$$initial_option_group"
					if len(groups) > 1 {
						groupVar += "_" + itoa(groupIndex)
					}
					createBody = append(createBody,
						genConst(groupVar, genCall(genMember(id("document"), "createElement"), strLit("optgroup"))),
						g.setAttrStmt(groupVar, g.scopeID, ""),
					)
					for _, attr := range group.Attrs {
						if attr != nil && attr.Expr == nil {
							createBody = append(createBody, g.setAttrStmt(groupVar, attr.Name, attr.Value))
						}
					}
					if group == optionList.Group {
						createBody = append(createBody, genExprStmt(genCall(genMember(id(groupVar), "append"), genCall(optionListFragment, optionListItems(nestedItemVar, nestedIndexVar)))))
					} else {
						createBody = append(createBody, genExprStmt(genCall(id("$$insertHTML"), id(groupVar), strLit(emitStaticHTMLWithoutEvents(group.Children, g.scopeID)))))
					}
					createBody = append(createBody, genExprStmt(genCall(genMember(id("$$control"), "append"), id(groupVar))))
				}
			} else {
				createBody = append(createBody, genExprStmt(genCall(genMember(id("$$control"), "append"), genCall(optionListFragment, optionListItems(nestedItemVar, nestedIndexVar)))))
			}
		} else {
			createBody = append(createBody, genExprStmt(genCall(id("$$insertHTML"), id("$$control"), strLit(emitStaticHTML(element.Children, g.scopeID)))))
		}
		if optionText != nil {
			createBody = append(createBody, genConst("$$initial_option", genCall(genMember(genMember(id("$$control"), "options"), "item"), numLit(optionTextIndex))), genIf(id("$$initial_option"), genAssignStmt(genMember(id("$$initial_option"), "textContent"), optionTextValue(nestedItemVar, nestedIndexVar))))
		}
		if optionDynamic != nil {
			createBody = append(createBody, genConst("$$initial_option_dynamic", genCall(genMember(genMember(id("$$control"), "options"), "item"), numLit(optionDynamicIndex))), genIf(id("$$initial_option_dynamic"), genAssignStmt(genMember(id("$$initial_option_dynamic"), optionDynamic.Name), optionDynamicValue(nestedItemVar, nestedIndexVar))))
		}
		if optionGroupDynamic != nil {
			initialOptionGroup := "$$initial_option_group"
			if optionList != nil && optionList.Group != nil {
				initialOptionGroup = "$$initial_option_group_dynamic"
			}
			createBody = append(createBody, genConst(initialOptionGroup, genCall(genMember(genCall(genMember(id("$$control"), "querySelectorAll"), strLit("optgroup")), "item"), numLit(optionGroupIndex))), genIf(id(initialOptionGroup), genAssignStmt(genMember(id(initialOptionGroup), "label"), optionGroupValue(nestedItemVar, nestedIndexVar))))
		}
	}
	createBody = append(createBody,
		genAssignStmt(genMember(id("$$control"), spec.readProp), valueExpr(nestedItemVar, nestedIndexVar)),
		genExprStmt(genCall(genMember(id("$$fragment"), "appendChild"), id("$$control"))),
		genConst("$$end", genCall(genMember(id("document"), "createComment"), strLit("/r"))),
		genExprStmt(genCall(genMember(id("$$fragment"), "append"), id("$$end"))),
		genConst("$$row", &JsObjectExpr{baseExpr: baseExpr{genNode()}, Props: []*JsProp{{Key: id("start"), Value: id("$$start")}, {Key: id("end"), Value: id("$$end")}}}),
	)
	createBody = append(createBody, controllerFor(id("$$row"), id("$$item"), id("$$index"), id("$$control"), false)...)
	createBody = append(createBody, genReturn(id("$$row")))
	create := &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$item"), id("$$index")}, Body: genBlock(createBody...)}
	update := &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$row"), id("$$item"), id("$$index")}, Body: genBlock(genIf(genLogical("&&", id("$$row"), genMember(id("$$row"), "controller")), genExprStmt(genCall(genMember(genMember(id("$$row"), "controller"), "update"), id("$$item"), id("$$index")))))}
	refresh := &JsArrowFunc{baseExpr: baseExpr{genNode()}, Body: genBlock(
		genConst("$$next_items", itemsExpr),
		genAssignStmt(id(blocksVar), genCall(id("$$reconcileHydrationKeyedRowRanges"), awaitRange, id(blocksVar), id("$$next_items"), id(keyVar), id(createVar), id(updateVar))),
	)}
	return []JsStmt{
		genConst(findElementVar, findElement), genConst(keyVar, keyOf), genConst(itemsVar, itemsExpr),
		genLet(blocksVar, genCall(id("$$adoptHydrationKeyedRowRanges"), awaitRange, id(itemsVar), id(keyVar), numLit(markerID))),
		genConst(attachVar, attach), genConst(createVar, create), genConst(updateVar, update),
		genLet(indexVar, numLit(0)),
		genForOf("$$item", id(itemsVar), genBlock(
			genExprStmt(genCall(id(attachVar), genCall(genMember(id(blocksVar), "get"), genCall(id(keyVar), id("$$item"), id(indexVar))), id("$$item"), id(indexVar))),
			genAssignStmt(id(indexVar), genBinary("+", id(indexVar), numLit(1))),
		)),
		genConst(refreshVar, refresh), genExprStmt(genCall(genMember(genMember(id(outerState), "innerRefresh"), "push"), id(refreshVar))),
	}
}

// buildHydrationKeyedAwaitNestedMultiSelectOptionListsAdopt is the paired
// owner variant of the single option-list control lowering above. It is kept
// intentionally narrow: the typed recipe has already proved two static-label
// optgroups, one keyed list per group and no fallback. That lets every list
// retain only descendants of its own group in its own Map.
func (g *ASTGen) buildHydrationKeyedAwaitNestedMultiSelectOptionListsAdopt(outer *EachBlockNode, nested *EachBlockNode, element *ElementNode, bind *Attr, optionLists []*hydrationKeyedEachSelectOptionListDescriptor, outerState, outerItem, outerIndex string, awaitRange JsExpr) []JsStmt {
	if outer == nil || nested == nil || element == nil || bind == nil || bind.Expr == nil || len(optionLists) != 2 {
		panic("vela: missing nested keyed await multi-option control adoption recipe")
	}
	plan := g.hydrationLifecycleNode(SSRLifecyclePlanEach, nested.Span)
	if plan.KeyedEach == nil || !plan.KeyedEach.DirectControlRows {
		panic("vela: missing typed nested keyed await multi-option control lifecycle recipe")
	}
	markerID, ok := g.hydrationMarkers.keyedEachID(plan.KeyedEach)
	if !ok {
		panic("vela: missing nested keyed await multi-option control marker")
	}
	spec, ok := twoWayBindSpec(element.Tag, bind.Name)
	if !ok {
		panic("vela: missing nested keyed await multi-option binding specification")
	}
	for _, list := range optionLists {
		if list == nil || list.Kind != hydrationKeyedEachSelectOptionListKeyed || list.Group == nil || list.Key == nil || len(list.Fallback) != 0 {
			panic("vela: invalid nested keyed await multi-option control recipe")
		}
	}
	g.hydrationKeyedRows = true
	g.hydrationNestedRanges = true
	g.hydrationKeyedElements = true

	prefix := "$$hsettled" + itoa(markerID) + "_" + itoa(nested.Span.Start) + "_multi_control"
	itemsVar, blocksVar, keyVar := prefix+"_items", prefix+"_blocks", prefix+"_key"
	findElementVar, attachVar, createVar := prefix+"_find_element", prefix+"_attach", prefix+"_create"
	updateVar, refreshVar, indexVar := prefix+"_update", prefix+"_refresh", prefix+"_index"
	nestedItemVar, nestedIndexVar := prefix+"_item", prefix+"_item_index"

	itemsExpr := JsExpr(genArray())
	g.withTemplateBinding(outer.ItemName, outerItem, func() {
		g.withTemplateBinding(outer.IndexName, outerIndex, func() { itemsExpr = g.templateExpr(nested.IterExpr) })
	})
	keyExpr := func(item, index string) JsExpr {
		value := JsExpr(id("undefined"))
		g.withTemplateBinding(nested.ItemName, item, func() {
			g.withTemplateBinding(nested.IndexName, index, func() { value = genString(g.templateExpr(plan.KeyedEach.Key)) })
		})
		return value
	}
	valueExpr := func(item, index string) JsExpr {
		value := JsExpr(strLit(""))
		g.withTemplateBinding(outer.ItemName, outerItem, func() {
			g.withTemplateBinding(outer.IndexName, outerIndex, func() {
				g.withTemplateBinding(nested.ItemName, item, func() {
					g.withTemplateBinding(nested.IndexName, index, func() { value = genString(g.templateExpr(bind.Expr)) })
				})
			})
		})
		return value
	}
	listItems := func(list *hydrationKeyedEachSelectOptionListDescriptor, item, index string) JsExpr {
		value := JsExpr(genArray())
		g.withTemplateBinding(outer.ItemName, outerItem, func() {
			g.withTemplateBinding(outer.IndexName, outerIndex, func() {
				g.withTemplateBinding(nested.ItemName, item, func() {
					g.withTemplateBinding(nested.IndexName, index, func() { value = g.templateExpr(list.Each.IterExpr) })
				})
			})
		})
		return value
	}
	assignValue := func(item, index string, value JsExpr) JsStmt {
		target := JsExpr(id("undefined"))
		g.withTemplateBinding(outer.ItemName, outerItem, func() {
			g.withTemplateBinding(outer.IndexName, outerIndex, func() {
				g.withTemplateBinding(nested.ItemName, item, func() {
					g.withTemplateBinding(nested.IndexName, index, func() { target = g.templateExpr(bind.Expr) })
				})
			})
		})
		return genAssignStmt(target, value)
	}

	fragmentVars := make([]string, 0, len(optionLists))
	keyVars := make([]string, 0, len(optionLists))
	createVars := make([]string, 0, len(optionLists))
	updateVars := make([]string, 0, len(optionLists))
	declarations := []JsStmt{}
	for listIndex, list := range optionLists {
		fragmentVar := prefix + "_option_fragment_" + itoa(listIndex)
		keyVarForList := prefix + "_option_key_" + itoa(listIndex)
		createVarForList := prefix + "_option_create_" + itoa(listIndex)
		updateVarForList := prefix + "_option_update_" + itoa(listIndex)
		fragmentVars = append(fragmentVars, fragmentVar)
		keyVars = append(keyVars, keyVarForList)
		createVars = append(createVars, createVarForList)
		updateVars = append(updateVars, updateVarForList)
		var optionValue, optionText, optionKey JsExpr
		g.withTemplateBinding(list.Each.ItemName, "$$option", func() {
			g.withTemplateBinding(list.Each.IndexName, "$$option_index", func() {
				optionValue = genString(g.templateExpr(list.ValueAttr.Expr))
				optionText = g.textValueExpr(list.Text.Parts)
				optionKey = g.templateExpr(list.Key)
			})
		})
		fragment := &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$items")}, Body: genBlock(
			genConst("$$option_fragment", genCall(genMember(id("document"), "createDocumentFragment"))),
			genLet("$$option_index", numLit(0)),
			genForOf("$$option", genLogical("||", id("$$items"), genArray()), genBlock(
				genConst("$$option_element", genCall(genMember(id("document"), "createElement"), strLit("option"))),
				g.setAttrStmt("$$option_element", g.scopeID, ""),
				genAssignStmt(genMember(id("$$option_element"), "value"), optionValue),
				genExprStmt(genCall(genMember(id("$$option_element"), "appendChild"), genCall(genMember(id("document"), "createTextNode"), optionText))),
				genExprStmt(genCall(genMember(id("$$option_fragment"), "append"), id("$$option_element"))),
				genAssignStmt(id("$$option_index"), genBinary("+", id("$$option_index"), numLit(1))),
			)),
			genReturn(id("$$option_fragment")),
		)}
		key := &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$option"), id("$$option_index")}, Body: optionKey}
		create := &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$option"), id("$$option_index")}, Body: genBlock(
			genConst("$$option_element", genCall(genMember(id("document"), "createElement"), strLit("option"))),
			g.setAttrStmt("$$option_element", g.scopeID, ""),
			genAssignStmt(genMember(id("$$option_element"), "value"), optionValue),
			genExprStmt(genCall(genMember(id("$$option_element"), "appendChild"), genCall(genMember(id("document"), "createTextNode"), optionText))),
			genReturn(id("$$option_element")),
		)}
		update := &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$option_element"), id("$$option"), id("$$option_index")}, Body: genBlock(
			genIf(genBinary("!==", genMember(id("$$option_element"), "value"), optionValue), genAssignStmt(genMember(id("$$option_element"), "value"), optionValue)),
			genIf(genBinary("!==", genMember(id("$$option_element"), "textContent"), optionText), genAssignStmt(genMember(id("$$option_element"), "textContent"), optionText)),
		)}
		declarations = append(declarations, genConst(fragmentVar, fragment), genConst(keyVarForList, key), genConst(createVarForList, create), genConst(updateVarForList, update))
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
	controllerFor := func(row, item, index, control JsExpr, declareBindings bool) []JsStmt {
		body := []JsStmt{genConst("$$inner_key", genCall(id(keyVar), item, index))}
		if declareBindings {
			body = append(body, genLet(nestedItemVar, item), genLet(nestedIndexVar, index))
		}
		body = append(body, genConst("$$inner", genCall(id("$$createHydrationRowController"), row, &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$next_item"), id("$$next_index")}, Body: genBlock(
			genAssignStmt(id(nestedItemVar), id("$$next_item")),
			genAssignStmt(id(nestedIndexVar), id("$$next_index")),
			genForOf("$$update", genMember(id("$$inner"), "updates"), genExprStmt(genCall(id("$$update")))),
		)}, &JsArrowFunc{baseExpr: baseExpr{genNode()}, Body: genBlock(
			genExprStmt(genCall(genMember(genMember(id(outerState), "innerRows"), "delete"), id("$$inner_key"))),
		)})))
		for listIndex, list := range optionLists {
			ownerVar := "$$inner_option_parent_" + itoa(listIndex)
			itemsVarForList := "$$inner_option_items_" + itoa(listIndex)
			blocksVarForList := "$$inner_option_blocks_" + itoa(listIndex)
			elementsVar := "$$inner_option_elements_" + itoa(listIndex)
			updateVarForList := "$$inner_option_list_update_" + itoa(listIndex)
			body = append(body,
				genConst(ownerVar, genCall(genMember(genCall(genMember(control, "querySelectorAll"), strLit("optgroup")), "item"), numLit(list.GroupIndex))),
				genIf(genUnary("!", id(ownerVar)), &JsThrowStmt{baseStmt: baseStmt{genNode()}, Arg: genNew(id("Error"), strLit("Vela hydration mismatch: missing nested multi-option optgroup owner"))}),
				genConst(itemsVarForList, listItems(list, nestedItemVar, nestedIndexVar)),
				genLet(blocksVarForList, genNew(id("Map"))),
				genConst(elementsVar, genCall(genMember(id("Array"), "from"), genCall(genMember(id(ownerVar), "querySelectorAll"), strLit("option")))),
				genIf(genBinary("!==", genMember(id(elementsVar), "length"), genMember(id(itemsVarForList), "length")), &JsThrowStmt{baseStmt: baseStmt{genNode()}, Arg: genNew(id("Error"), strLit("Vela hydration mismatch: keyed nested multi-option list length differs from its server snapshot"))}),
				genLet("$$inner_option_index_"+itoa(listIndex), numLit(0)),
				genForOf("$$inner_option_item_"+itoa(listIndex), id(itemsVarForList), genBlock(
					genConst("$$inner_option_key_"+itoa(listIndex), genCall(id(keyVars[listIndex]), id("$$inner_option_item_"+itoa(listIndex)), id("$$inner_option_index_"+itoa(listIndex)))),
					genIf(genCall(genMember(id(blocksVarForList), "has"), id("$$inner_option_key_"+itoa(listIndex))), &JsThrowStmt{baseStmt: baseStmt{genNode()}, Arg: genNew(id("Error"), strLit("Vela keyed update has duplicate key in nested multi-option list"))}),
					genExprStmt(genCall(genMember(id(blocksVarForList), "set"), id("$$inner_option_key_"+itoa(listIndex)), genComputedMember(id(elementsVar), id("$$inner_option_index_"+itoa(listIndex))))),
					genAssignStmt(id("$$inner_option_index_"+itoa(listIndex)), genBinary("+", id("$$inner_option_index_"+itoa(listIndex)), numLit(1))),
				)),
				genConst(updateVarForList, &JsArrowFunc{baseExpr: baseExpr{genNode()}, Body: genBlock(
					genAssignStmt(id(blocksVarForList), genCall(id("$$reconcileHydrationKeyedElements"), id(ownerVar), id(blocksVarForList), listItems(list, nestedItemVar, nestedIndexVar), id(keyVars[listIndex]), id(createVars[listIndex]), id(updateVars[listIndex]))),
				)}),
				genExprStmt(genCall(genMember(genMember(id("$$inner"), "updates"), "push"), id(updateVarForList))),
			)
		}
		body = append(body,
			genConst("$$inner_control_update", &JsArrowFunc{baseExpr: baseExpr{genNode()}, Body: genBlock(
				genConst("$$inner_control_value", valueExpr(nestedItemVar, nestedIndexVar)),
				genIf(genBinary("!==", genMember(control, spec.readProp), id("$$inner_control_value")), genAssignStmt(genMember(control, spec.readProp), id("$$inner_control_value"))),
			)}),
			genExprStmt(genCall(genMember(genMember(id("$$inner"), "updates"), "push"), id("$$inner_control_update"))),
			genConst("$$inner_control_handler", &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$event")}, Body: genBlock(assignValue(nestedItemVar, nestedIndexVar, genMember(genMember(id("$event"), "target"), spec.readProp)))}),
			genExprStmt(genCall(genMember(control, "addEventListener"), strLit(spec.event), id("$$inner_control_handler"))),
			genExprStmt(genCall(genMember(genMember(id("$$inner"), "listeners"), "push"), &JsArrowFunc{baseExpr: baseExpr{genNode()}, Body: genBlock(genExprStmt(genCall(genMember(control, "removeEventListener"), strLit(spec.event), id("$$inner_control_handler"))))})),
			genExprStmt(genCall(genMember(genMember(id(outerState), "innerRows"), "set"), id("$$inner_key"), id("$$inner"))),
			genAssignStmt(genMember(row, "controller"), id("$$inner")),
		)
		return body
	}
	attachBody := []JsStmt{genConst("$$control", genCall(id(findElementVar), id("$$row"))), genIf(genUnary("!", id("$$control")), &JsThrowStmt{baseStmt: baseStmt{genNode()}, Arg: genNew(id("Error"), strLit("Vela hydration mismatch: missing nested multi-option input"))})}
	attachBody = append(attachBody, controllerFor(id("$$row"), id("$$item"), id("$$index"), id("$$control"), true)...)
	attachBody = append(attachBody, assignValue(nestedItemVar, nestedIndexVar, genMember(id("$$control"), spec.readProp)))
	attach := &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$row"), id("$$item"), id("$$index")}, Body: genBlock(attachBody...)}
	createBody := []JsStmt{
		genConst("$$fragment", genCall(genMember(id("document"), "createDocumentFragment"))),
		genConst("$$start", genCall(genMember(id("document"), "createComment"), strLit("r"))),
		genExprStmt(genCall(genMember(id("$$fragment"), "append"), id("$$start"))),
		genConst("$$control", genCall(genMember(id("document"), "createElement"), strLit(element.Tag))),
		g.setAttrStmt("$$control", g.scopeID, ""),
		genLet(nestedItemVar, id("$$item")), genLet(nestedIndexVar, id("$$index")),
	}
	for _, attr := range element.Attrs {
		if attr != nil && attr.Expr == nil {
			createBody = append(createBody, g.setAttrStmt("$$control", attr.Name, attr.Value))
		}
	}
	for listIndex, list := range optionLists {
		groupVar := "$$initial_option_group_" + itoa(listIndex)
		createBody = append(createBody, genConst(groupVar, genCall(genMember(id("document"), "createElement"), strLit("optgroup"))), g.setAttrStmt(groupVar, g.scopeID, ""))
		for _, attr := range list.Group.Attrs {
			if attr != nil && attr.Expr == nil {
				createBody = append(createBody, g.setAttrStmt(groupVar, attr.Name, attr.Value))
			}
		}
		createBody = append(createBody,
			genExprStmt(genCall(genMember(id(groupVar), "append"), genCall(id(fragmentVars[listIndex]), listItems(list, nestedItemVar, nestedIndexVar)))),
			genExprStmt(genCall(genMember(id("$$control"), "append"), id(groupVar))),
		)
	}
	createBody = append(createBody,
		genAssignStmt(genMember(id("$$control"), spec.readProp), valueExpr(nestedItemVar, nestedIndexVar)),
		genExprStmt(genCall(genMember(id("$$fragment"), "appendChild"), id("$$control"))),
		genConst("$$end", genCall(genMember(id("document"), "createComment"), strLit("/r"))),
		genExprStmt(genCall(genMember(id("$$fragment"), "append"), id("$$end"))),
		genConst("$$row", &JsObjectExpr{baseExpr: baseExpr{genNode()}, Props: []*JsProp{{Key: id("start"), Value: id("$$start")}, {Key: id("end"), Value: id("$$end")}}}),
	)
	createBody = append(createBody, controllerFor(id("$$row"), id("$$item"), id("$$index"), id("$$control"), false)...)
	createBody = append(createBody, genReturn(id("$$row")))
	create := &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$item"), id("$$index")}, Body: genBlock(createBody...)}
	update := &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$row"), id("$$item"), id("$$index")}, Body: genBlock(genIf(genLogical("&&", id("$$row"), genMember(id("$$row"), "controller")), genExprStmt(genCall(genMember(genMember(id("$$row"), "controller"), "update"), id("$$item"), id("$$index")))))}
	refresh := &JsArrowFunc{baseExpr: baseExpr{genNode()}, Body: genBlock(genConst("$$next_items", itemsExpr), genAssignStmt(id(blocksVar), genCall(id("$$reconcileHydrationKeyedRowRanges"), awaitRange, id(blocksVar), id("$$next_items"), id(keyVar), id(createVar), id(updateVar))))}
	result := []JsStmt{genConst(findElementVar, findElement), genConst(keyVar, keyOf)}
	result = append(result, declarations...)
	result = append(result,
		genConst(itemsVar, itemsExpr), genLet(blocksVar, genCall(id("$$adoptHydrationKeyedRowRanges"), awaitRange, id(itemsVar), id(keyVar), numLit(markerID))),
		genConst(attachVar, attach), genConst(createVar, create), genConst(updateVar, update),
		genLet(indexVar, numLit(0)),
		genForOf("$$item", id(itemsVar), genBlock(genExprStmt(genCall(id(attachVar), genCall(genMember(id(blocksVar), "get"), genCall(id(keyVar), id("$$item"), id(indexVar))), id("$$item"), id(indexVar))), genAssignStmt(id(indexVar), genBinary("+", id(indexVar), numLit(1))))),
		genConst(refreshVar, refresh), genExprStmt(genCall(genMember(genMember(id(outerState), "innerRefresh"), "push"), id(refreshVar))),
	)
	return result
}
