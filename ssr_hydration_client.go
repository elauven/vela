package main

import "fmt"

// CanBuildSSRHydrationClient reports whether the current marker-aware client
// lowering can execute this component. This first vertical slice owns one
// existing root element; its descendants are static server DOM, while root
// attributes and ordinary DOM events remain reactive after adoption.
func CanBuildSSRHydrationClient(nodes []Node) bool {
	if len(nodes) != 1 {
		return false
	}
	root, ok := nodes[0].(*ElementNode)
	if !ok || root == nil {
		return false
	}
	for _, attr := range root.Attrs {
		if attr.IsTwoWay || attr.IsBindThis {
			return false
		}
	}
	return ssrHydrationRootChildren(root.Children)
}

func ssrHydrationRootChildren(nodes []Node) bool {
	for _, node := range nodes {
		switch current := node.(type) {
		case *TextNode:
			// Direct text parts are represented by SSR `v` ranges when dynamic.
		case *ElementNode:
			if !ssrHydrationElement(current) {
				return false
			}
		case *IfChainNode:
			if !ssrHydrationIfChain(current) {
				return false
			}
		case *EachBlockNode:
			if !ssrHydrationEach(current) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// ssrHydrationElement accepts the recursive marker-aware element subset.
// Every accepted descendant receives an `e` acquisition marker; dynamic text
// and direct structural blocks retain their template-order `v` ranges.
func ssrHydrationElement(element *ElementNode) bool {
	if element == nil {
		return false
	}
	if _, rawText := rawTextProp(element.Tag); rawText {
		return ssrHydrationStaticChildren(element.Children)
	}
	for _, attr := range element.Attrs {
		if attr.IsTwoWay || attr.IsBindThis {
			return false
		}
	}
	for _, child := range element.Children {
		switch current := child.(type) {
		case *TextNode:
			// `v` ranges cover every non-static part at any nesting depth.
		case *ElementNode:
			if !ssrHydrationElement(current) {
				return false
			}
		case *IfChainNode:
			if !ssrHydrationIfChain(current) {
				return false
			}
		case *EachBlockNode:
			if !ssrHydrationEach(current) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func ssrHydrationIfChain(chain *IfChainNode) bool {
	if chain == nil || len(chain.Branches) == 0 || len(chain.Branches) > 2 {
		return false
	}
	if chain.HasKey() {
		return len(chain.Branches) == 1 && !chain.Branches[0].HasCondition() && ssrHydrationStaticChildren(chain.Branches[0].Children)
	}
	conditions := 0
	for _, branch := range chain.Branches {
		if branch == nil || !ssrHydrationStaticChildren(branch.Children) {
			return false
		}
		if branch.HasCondition() {
			conditions++
		}
	}
	return conditions == 1
}

func ssrHydrationEach(block *EachBlockNode) bool {
	if block == nil {
		return false
	}
	if block.HasKey() {
		// A keyed hydration row is adopted as one existing element.  Empty
		// fallbacks and nested rows will join later with range-aware row
		// handles; accepting either here would silently leave server fallback
		// nodes outside the keyed map on the first client update.
		_, ok := ssrHydrationEachDynamicTextRow(block)
		return ok && len(block.ElseChildren) == 0
	}
	if _, ok := ssrHydrationEachDynamicTextRow(block); !ok && !ssrHydrationStaticChildren(block.Children) {
		return false
	}
	return ssrHydrationStaticChildren(block.ElseChildren)
}

// ssrHydrationEachDynamicTextRow accepts one ordinary row element with only
// static attributes and direct text nodes. Text parts may refer to the each
// item; replacement rows are built as DOM nodes, not HTML source strings.
func ssrHydrationEachDynamicTextRow(block *EachBlockNode) (*ElementNode, bool) {
	if block == nil || len(block.Children) != 1 {
		return nil, false
	}
	row, ok := block.Children[0].(*ElementNode)
	if !ok || row == nil {
		return nil, false
	}
	for _, attr := range row.Attrs {
		if attr.Expr != nil || attr.IsEvent || attr.IsBind || attr.IsTwoWay || attr.IsBindThis || attr.IsClassDir || attr.IsStyleDir {
			return nil, false
		}
	}
	for _, child := range row.Children {
		tn, ok := child.(*TextNode)
		if !ok || tn == nil {
			return nil, false
		}
	}
	return row, true
}

func ssrHydrationStaticChildren(nodes []Node) bool {
	for _, node := range nodes {
		switch current := node.(type) {
		case *TextNode:
			for _, part := range current.Parts {
				if !part.Static {
					return false
				}
			}
		case *ElementNode:
			for _, attr := range current.Attrs {
				if attr.Expr != nil || attr.IsEvent || attr.IsBind || attr.IsTwoWay || attr.IsBindThis || attr.IsClassDir || attr.IsStyleDir {
					return false
				}
			}
			if !ssrHydrationStaticChildren(current.Children) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// BuildSSRHydrationClientProgram emits the executable browser factory for the
// supported SSR subset. It acquires the root through the checked element
// marker and never runs the CSR mount or initial DOM-write paths.
func BuildSSRHydrationClientProgram(name string, sfc *SFC, analysis *Analysis, scopeID string) (*JsProgram, error) {
	if sfc == nil || !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		return nil, fmt.Errorf("SSR hydration client currently requires one element root with static descendants and no DOM bindings")
	}
	program, _ := buildProgramWithDOMAcquirePlanAndHydration(
		name, sfc, sfc.TemplateAST.Nodes, analysis, scopeID, true, DOMAcquireHydrate, true,
	)
	validateGeneratedProgram(program)
	return program, nil
}
