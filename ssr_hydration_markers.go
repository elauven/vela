package main

// SSRHydrationMarkerIDs indexes the canonical marker IDs reserved by the
// shared SSR render plan. Hydration lowering must look up these IDs instead of
// replaying dynamic traversal order with a private counter.
type SSRHydrationMarkerIDs struct {
	Elements map[SectionSpan]int
	Text     map[SectionSpan]int
	If       map[SectionSpan]int
	Each     map[SectionSpan]int
	Await    map[SectionSpan]int
}

func ssrHydrationMarkerIDs(nodes []SSRPlanNode) SSRHydrationMarkerIDs {
	markers := SSRHydrationMarkerIDs{
		Elements: map[SectionSpan]int{},
		Text:     map[SectionSpan]int{},
		If:       map[SectionSpan]int{},
		Each:     map[SectionSpan]int{},
		Await:    map[SectionSpan]int{},
	}
	var visit func([]SSRPlanNode)
	visit = func(children []SSRPlanNode) {
		for _, node := range children {
			switch current := node.(type) {
			case *SSRPlanElement:
				markers.Elements[current.Span] = current.HydrationID
				visit(current.Children)
			case *SSRPlanText:
				for _, part := range current.Parts {
					if part.DynamicID >= 0 {
						markers.Text[part.Span] = part.DynamicID
					}
				}
			case *SSRPlanIf:
				if current.DynamicID >= 0 {
					markers.If[current.Span] = current.DynamicID
				}
				for _, branch := range current.Branches {
					visit(branch.Children)
				}
			case *SSRPlanEach:
				if current.DynamicID >= 0 {
					markers.Each[current.Span] = current.DynamicID
				}
				visit(current.Children)
				visit(current.ElseChildren)
			case *SSRPlanAwait:
				if current.DynamicID >= 0 {
					markers.Await[current.Span] = current.DynamicID
				}
				visit(current.PendingChildren)
				visit(current.ThenChildren)
				visit(current.CatchChildren)
			case *SSRPlanComponent:
				for _, slot := range current.Slots {
					visit(slot)
				}
			case *SSRPlanSlot:
				visit(current.Fallback)
			}
		}
	}
	visit(nodes)
	return markers
}

func (markers SSRHydrationMarkerIDs) textID(part *Part) (int, bool) {
	if part == nil {
		return 0, false
	}
	id, ok := markers.Text[part.Span]
	return id, ok
}

func (markers SSRHydrationMarkerIDs) ifID(span SectionSpan) (int, bool) {
	id, ok := markers.If[span]
	return id, ok
}

func (markers SSRHydrationMarkerIDs) eachID(span SectionSpan) (int, bool) {
	id, ok := markers.Each[span]
	return id, ok
}

func (markers SSRHydrationMarkerIDs) awaitID(span SectionSpan) (int, bool) {
	id, ok := markers.Await[span]
	return id, ok
}

func (g *ASTGen) hydrationTextMarkerID(part *Part) int {
	if id, ok := g.hydrationMarkers.textID(part); ok {
		return id
	}
	panic("vela: missing SSR text marker for hydration template span")
}

func (g *ASTGen) hydrationIfMarkerID(chain *IfChainNode) int {
	if chain != nil {
		if id, ok := g.hydrationMarkers.ifID(chain.Span); ok {
			return id
		}
	}
	panic("vela: missing SSR if/key marker for hydration template span")
}

func (g *ASTGen) hydrationEachMarkerID(block *EachBlockNode) int {
	if block != nil {
		if id, ok := g.hydrationMarkers.eachID(block.Span); ok {
			return id
		}
	}
	panic("vela: missing SSR each marker for hydration template span")
}

func (g *ASTGen) hydrationAwaitMarkerID(block *AwaitBlockNode) int {
	if block != nil {
		if id, ok := g.hydrationMarkers.awaitID(block.Span); ok {
			return id
		}
	}
	panic("vela: missing SSR await marker for hydration template span")
}
