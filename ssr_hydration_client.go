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
			for _, attr := range current.Attrs {
				if attr.Expr != nil || attr.IsEvent || attr.IsBind || attr.IsTwoWay || attr.IsBindThis || attr.IsClassDir || attr.IsStyleDir {
					return false
				}
			}
			if !ssrHydrationStaticChildren(current.Children) {
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
	if chain == nil || chain.HasKey() || len(chain.Branches) == 0 || len(chain.Branches) > 2 {
		return false
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
	if block == nil || block.HasKey() || len(block.ElseChildren) != 0 {
		return false
	}
	return ssrHydrationStaticChildren(block.Children)
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
