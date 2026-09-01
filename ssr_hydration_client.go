package main

import (
	"fmt"
	"strings"
)

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
	if !ssrHydrationElementAttrs(root) {
		return false
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
		case *ComponentNode:
			if !ssrHydrationComponent(current) {
				return false
			}
		case *SlotNode:
			if !ssrHydrationSlot(current) {
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
	if !ssrHydrationElementAttrs(element) {
		return false
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
		case *ComponentNode:
			if !ssrHydrationComponent(current) {
				return false
			}
		case *SlotNode:
			if !ssrHydrationSlot(current) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func ssrHydrationElementAttrs(element *ElementNode) bool {
	if element == nil {
		return false
	}
	for _, attr := range element.Attrs {
		if attr.IsTwoWay {
			if _, ok := twoWayBindSpec(element.Tag, attr.Name); !ok {
				return false
			}
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

// ssrHydrationComponent accepts a direct local child with ordinary props, `@on:*`
// component events, bindable `bind:value`, static slot fallback, recursive ordinary
// slot elements, and top-level #if/#key/#each nodes in the existing root subset.
func ssrHydrationComponent(component *ComponentNode) bool {
	if component == nil {
		return false
	}
	for _, slot := range component.Slots {
		if !ssrHydrationSlotContent(slot) {
			return false
		}
	}
	for _, attr := range component.Attrs {
		if attr == nil || attr.IsBindThis || attr.IsClassDir || attr.IsStyleDir {
			return false
		}
	}
	return true
}

// ssrHydrationSlot is static because parent-owned slot DOM is already present
// inside the child root. The child hydrator deliberately leaves it untouched.
func ssrHydrationSlot(slot *SlotNode) bool {
	return slot != nil && ssrHydrationStaticChildren(slot.Fallback)
}

// ssrHydrationSlotContent admits parent-projected text and recursive ordinary
// elements. Parent owns their markers and reactive work; child leaves them in place.
func ssrHydrationSlotContent(nodes []Node) bool {
	for _, node := range nodes {
		switch current := node.(type) {
		case *TextNode:
			continue
		case *ElementNode:
			if !ssrHydrationSlotElement(current) {
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

// ssrHydrationSlotElement intentionally excludes structural blocks, nested
// components, and nested slots. Their marker ownership needs a dedicated plan.
func ssrHydrationSlotElement(element *ElementNode) bool {
	if element == nil || !ssrHydrationElementAttrs(element) {
		return false
	}
	if _, rawText := rawTextProp(element.Tag); rawText {
		return ssrHydrationStaticChildren(element.Children)
	}
	for _, child := range element.Children {
		switch current := child.(type) {
		case *TextNode:
			continue
		case *ElementNode:
			if !ssrHydrationSlotElement(current) {
				return false
			}
		default:
			return false
		}
	}
	return true
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

// ssrHydrationElementMarkerIDs retains the canonical SSR marker ID for every
// template element that can appear in this component output, including parent
// slot content and inactive structural alternatives.
func ssrHydrationElementMarkerIDs(nodes []SSRPlanNode) map[SectionSpan]int {
	ids := map[SectionSpan]int{}
	var visit func([]SSRPlanNode)
	visit = func(children []SSRPlanNode) {
		for _, node := range children {
			switch current := node.(type) {
			case *SSRPlanElement:
				ids[current.Span] = current.HydrationID
				visit(current.Children)
			case *SSRPlanComponent:
				for _, slot := range current.Slots {
					visit(slot)
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
	return ids
}

// BuildSSRHydrationClientProgram emits the executable browser factory for the
// supported SSR subset. It acquires the root through the checked element
// marker and never runs the CSR mount or initial DOM-write paths.
func BuildSSRHydrationClientProgram(name string, sfc *SFC, analysis *Analysis, scopeID string) (*JsProgram, error) {
	components, err := ssrHydrationComponentNames(sfc, analysis)
	if err != nil {
		return nil, err
	}
	return buildSSRHydrationClientProgramWithComponents(name, sfc, analysis, scopeID, components)
}

// buildSSRHydrationClientProgramWithComponents keeps the child-component import
// map explicit. Project compilation supplies verified local .vela imports.
func buildSSRHydrationClientProgramWithComponents(name string, sfc *SFC, analysis *Analysis, scopeID string, components map[string]bool) (*JsProgram, error) {
	if sfc == nil || !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		return nil, fmt.Errorf("SSR hydration client requires one element root and supported marker-aware descendants")
	}
	plan, err := BuildSSRRenderPlanWithAnalysis(name, sfc, analysis, scopeID)
	if err != nil {
		return nil, err
	}
	if analysis != nil {
		analysis.lowerReactiveCallbackBodies()
	}
	g := &ASTGen{
		name: name, scopeID: scopeID, analysis: analysis, sfc: sfc, fragCounter: -1, cssExternal: true,
		domAcquireMode: DOMAcquireHydrate, domAcquirePlan: DOMAcquirePlan{Mode: DOMAcquireHydrate},
		hydrationStateInput: true, hydrationComponents: components, hydrationElementIDs: ssrHydrationElementMarkerIDs(plan.Nodes),
	}
	program := g.buildModule(sfc.TemplateAST.Nodes)
	validateGeneratedProgram(program)
	return program, nil
}

// ssrHydrationUsedComponentNames returns only invocations present in template AST.
// Imported but unused components do not constrain hydration capability.
func ssrHydrationUsedComponentNames(nodes []Node) map[string]bool {
	components := map[string]bool{}
	var visit func([]Node)
	visit = func(children []Node) {
		for _, node := range children {
			switch current := node.(type) {
			case *ComponentNode:
				components[current.Name] = true
			case *ElementNode:
				visit(current.Children)
			case *IfChainNode:
				for _, branch := range current.Branches {
					if branch != nil {
						visit(branch.Children)
					}
				}
			case *EachBlockNode:
				visit(current.Children)
				visit(current.ElseChildren)
			}
		}
	}
	visit(nodes)
	return components
}

func ssrHydrationComponentNames(sfc *SFC, analysis *Analysis) (map[string]bool, error) {
	if sfc == nil {
		return nil, fmt.Errorf("SSR hydration client: missing component")
	}
	imports := map[string]bool{}
	if analysis != nil {
		for _, imp := range analysis.Imports {
			if imp == nil || !strings.HasSuffix(imp.Source, ".vela") {
				continue
			}
			for _, local := range imp.Names {
				imports[local] = true
			}
		}
	}
	components := map[string]bool{}
	var visit func([]Node) error
	visit = func(nodes []Node) error {
		for _, node := range nodes {
			switch current := node.(type) {
			case *ComponentNode:
				if !imports[current.Name] {
					return fmt.Errorf("SSR hydration component %q must be imported from a local .vela module", current.Name)
				}
				components[current.Name] = true
			case *ElementNode:
				if err := visit(current.Children); err != nil {
					return err
				}
			case *IfChainNode:
				for _, branch := range current.Branches {
					if branch != nil {
						if err := visit(branch.Children); err != nil {
							return err
						}
					}
				}
			case *EachBlockNode:
				if err := visit(current.Children); err != nil {
					return err
				}
				if err := visit(current.ElseChildren); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(sfc.TemplateAST.Nodes); err != nil {
		return nil, err
	}
	return components, nil
}
