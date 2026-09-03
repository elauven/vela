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
	return AnalyzeSSRCapability(nodes).ExecutableHydration()
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
	if sfc == nil {
		return nil, fmt.Errorf("SSR hydration client: missing component")
	}
	capability := AnalyzeSSRCapability(sfc.TemplateAST.Nodes)
	if !capability.ExecutableHydration() {
		if issue, ok := capability.FirstHydrationIssue(); ok {
			return nil, fmt.Errorf("SSR hydration client requires marker-aware lifecycle at %s (%s): %s", issue.Path, issue.NodeKind, issue.Reason)
		}
		return nil, fmt.Errorf("SSR hydration client requires one element root and supported marker-aware descendants")
	}
	components, err := ssrHydrationComponentNames(sfc, analysis)
	if err != nil {
		return nil, err
	}
	return buildSSRHydrationClientProgramWithComponents(name, sfc, analysis, scopeID, components)
}

// buildSSRHydrationClientProgramWithComponents keeps the child-component import
// map explicit. Project compilation supplies verified local .vela imports.
func buildSSRHydrationClientProgramWithComponents(name string, sfc *SFC, analysis *Analysis, scopeID string, components map[string]bool) (*JsProgram, error) {
	if sfc == nil {
		return nil, fmt.Errorf("SSR hydration client: missing component")
	}
	if !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		return nil, fmt.Errorf("SSR hydration client requires a capability-checked template")
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
		hydrationStateInput: true, hydrationComponents: components, hydrationCSRComponents: ssrHydrationStructuralComponentAliases(sfc.TemplateAST.Nodes), hydrationElementIDs: ssrHydrationElementMarkerIDs(plan.Nodes),
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

// ssrHydrationStructuralComponentAliases reserves a separate ordinary CSR
// factory binding for a direct local component located in an #if branch. The
// hydrate binding owns only server DOM adoption; a later materialized branch
// must call the ordinary client factory instead. This scanner intentionally
// prepares aliases before the branch lifecycle lowering is enabled.
func ssrHydrationStructuralComponentAliases(nodes []Node) map[string]string {
	aliases := map[string]string{}
	var visit func([]Node)
	visit = func(children []Node) {
		for _, node := range children {
			switch current := node.(type) {
			case *ElementNode:
				visit(current.Children)
			case *IfChainNode:
				for _, branch := range current.Branches {
					if branch == nil {
						continue
					}
					if len(branch.Children) == 1 {
						if component, ok := branch.Children[0].(*ComponentNode); ok && component != nil && len(component.Slots) == 0 {
							aliases[component.Name] = "$$csr_" + sanitizeIdent(component.Name)
						}
					}
					visit(branch.Children)
				}
			case *EachBlockNode:
				visit(current.Children)
				visit(current.ElseChildren)
			}
		}
	}
	visit(nodes)
	return aliases
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
