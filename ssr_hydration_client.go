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
	return buildSSRHydrationClientProgram(name, sfc, analysis, scopeID, components, true)
}

func buildSSRHydrationClientProgram(name string, sfc *SFC, analysis *Analysis, scopeID string, components map[string]bool, requireCapability bool) (*JsProgram, error) {
	if sfc == nil {
		return nil, fmt.Errorf("SSR hydration client: missing component")
	}
	if requireCapability && !CanBuildSSRHydrationClient(sfc.TemplateAST.Nodes) {
		return nil, fmt.Errorf("SSR hydration client requires a capability-checked template")
	}
	plan, err := BuildSSRRenderPlanWithAnalysis(name, sfc, analysis, scopeID)
	if err != nil {
		return nil, err
	}
	if analysis != nil {
		analysis.lowerReactiveCallbackBodies()
	}
	lifecyclePlan := BuildSSRStructuralLifecyclePlan(sfc.TemplateAST.Nodes)
	g := &ASTGen{
		name: name, scopeID: scopeID, analysis: analysis, sfc: sfc, fragCounter: -1, cssExternal: true,
		domAcquireMode: DOMAcquireHydrate, domAcquirePlan: DOMAcquirePlan{Mode: DOMAcquireHydrate},
		hydrationStateInput: true, hydrationComponents: components, hydrationCSRComponents: ssrHydrationStructuralComponentAliasesForPlan(lifecyclePlan), hydrationMarkers: ssrHydrationMarkerIDs(plan.Nodes), hydrationLifecyclePlan: lifecyclePlan, hydrationComponentInstanceSuffixes: plan.ComponentInstanceSuffixes, hydrationComponentInstanceIdentities: plan.ComponentInstanceIdentities,
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
			case *AwaitBlockNode:
				visit(current.PendingChildren)
				visit(current.ThenChildren)
				visit(current.CatchChildren)
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
	return ssrHydrationStructuralComponentAliasesForPlan(BuildSSRStructuralLifecyclePlan(nodes))
}

// ssrHydrationStructuralComponentAliasesForPlan reserves a distinct CSR
// factory only for a child selected by a typed lifecycle that can actually
// materialize new DOM. Ordinary adopted children never need a CSR import:
// retaining one would make every hydration page require an otherwise unused
// .js module and could stop ESM evaluation before hydration starts.
func ssrHydrationStructuralComponentAliasesForPlan(plan *SSRStructuralLifecyclePlan) map[string]string {
	aliases := map[string]string{}
	var visit func(*SSRStructuralLifecyclePlan, bool)
	visit = func(current *SSRStructuralLifecyclePlan, insideStructuralOwner bool) {
		if current == nil {
			return
		}
		if insideStructuralOwner && current.Kind == SSRLifecyclePlanComponent {
			if component, ok := current.TemplateNode.(*ComponentNode); ok && component != nil {
				aliases[component.Name] = "$$csr_" + sanitizeIdent(component.Name)
			}
		}
		if current.Kind == SSRLifecyclePlanIf {
			if components, _, ok := ssrHydrationDirectIfComponents(current); ok {
				for _, component := range components {
					aliases[component.Name] = "$$csr_" + sanitizeIdent(component.Name)
				}
			}
			if components, ok := ssrHydrationDirectKeyComponents(current); ok {
				for _, component := range components {
					aliases[component.Name] = "$$csr_" + sanitizeIdent(component.Name)
				}
			}
		}
		if current.Kind == SSRLifecyclePlanEach {
			if components, ok := ssrHydrationDirectUnkeyedEachComponents(current); ok {
				for _, component := range components {
					aliases[component.Name] = "$$csr_" + sanitizeIdent(component.Name)
				}
			}
			if components, ok := ssrHydrationDirectKeyedEachComponents(current); ok {
				for _, component := range components {
					aliases[component.Name] = "$$csr_" + sanitizeIdent(component.Name)
				}
			}
		}
		if current.Kind == SSRLifecyclePlanAwait {
			if branches, ok := ssrHydrationDirectAwaitComponents(current); ok {
				for _, components := range branches {
					for _, component := range components {
						aliases[component.Name] = "$$csr_" + sanitizeIdent(component.Name)
					}
				}
			}
			for _, pending := range ssrHydrationPendingAwaitComponentPlans(current) {
				aliases[pending.Component.Name] = "$$csr_" + sanitizeIdent(pending.Component.Name)
			}
		}
		childStructuralOwner := insideStructuralOwner || current.Kind == SSRLifecyclePlanIf || current.Kind == SSRLifecyclePlanEach || current.Kind == SSRLifecyclePlanAwait
		for _, child := range current.Children {
			visit(child, childStructuralOwner)
		}
	}
	visit(plan, false)
	return aliases
}

// ssrHydrationDirectAwaitComponents reads the direct await-child subset from
// the typed lifecycle plan. Branch positions are pending, then, then catch.
func ssrHydrationDirectAwaitComponents(plan *SSRStructuralLifecyclePlan) ([][]*ComponentNode, bool) {
	if plan == nil || plan.Kind != SSRLifecyclePlanAwait {
		return nil, false
	}
	block, ok := plan.TemplateNode.(*AwaitBlockNode)
	if !ok || block == nil {
		return nil, false
	}
	return ssrCapabilityCurrentAwaitComponentBranches(block)
}

// ssrHydrationPendingAwaitComponents returns component boundaries owned by
// the server-rendered pending branch. Unlike the direct await slice, this
// walks the typed lifecycle plan recursively so a future adoption lowerer can
// acquire nested structural ranges and component payloads as one outer-owned
// pending handle.
func ssrHydrationPendingAwaitComponents(plan *SSRStructuralLifecyclePlan) []*ComponentNode {
	pending := plan.AwaitBranch(0)
	if pending == nil {
		return nil
	}
	var components []*ComponentNode
	var visit func(*SSRStructuralLifecyclePlan)
	visit = func(current *SSRStructuralLifecyclePlan) {
		if current == nil {
			return
		}
		if current.Kind == SSRLifecyclePlanComponent {
			component, ok := current.TemplateNode.(*ComponentNode)
			if ok && component != nil {
				components = append(components, component)
			}
		}
		for _, child := range current.Children {
			visit(child)
		}
	}
	visit(pending)
	return components
}

// ssrHydrationPendingAwaitComponent describes one server-rendered pending
// component boundary. The structural plan supplies ordering; the render plan
// later supplies its static instance suffix and payload namespace.
type ssrHydrationPendingAwaitComponent struct {
	Component *ComponentNode
	Plan      *SSRStructuralLifecyclePlan
}

func ssrHydrationPendingAwaitComponentPlans(plan *SSRStructuralLifecyclePlan) []ssrHydrationPendingAwaitComponent {
	pending := plan.AwaitBranch(0)
	if pending == nil {
		return nil
	}
	var components []ssrHydrationPendingAwaitComponent
	var visit func(*SSRStructuralLifecyclePlan)
	visit = func(current *SSRStructuralLifecyclePlan) {
		if current == nil {
			return
		}
		if current.Kind == SSRLifecyclePlanComponent {
			if component, ok := current.TemplateNode.(*ComponentNode); ok && component != nil {
				components = append(components, ssrHydrationPendingAwaitComponent{Component: component, Plan: current})
			}
		}
		for _, child := range current.Children {
			visit(child)
		}
	}
	visit(pending)
	return components
}

func ssrHydrationDirectKeyComponents(plan *SSRStructuralLifecyclePlan) ([]*ComponentNode, bool) {
	if plan == nil || plan.Kind != SSRLifecyclePlanIf {
		return nil, false
	}
	chain, ok := plan.TemplateNode.(*IfChainNode)
	if !ok || chain == nil {
		return nil, false
	}
	return ssrHydrationDirectKeyComponentsForCSR(chain)
}

// ssrHydrationDirectKeyComponentsForCSR is intentionally narrower than a
// generic slot lifecycle: it only reserves an ordinary CSR import for a
// component that will be materialized by a keyed structural owner. Static
// slots are safe here because every invocation builds a fresh fragment and
// neither callbacks nor reactive parent updates are captured by that fragment.
// Dynamic slots remain rejected until they have an owner-aware lifecycle.
func ssrHydrationDirectKeyComponentsForCSR(chain *IfChainNode) ([]*ComponentNode, bool) {
	if chain == nil || !chain.HasKey() || len(chain.Branches) != 1 || chain.Branches[0] == nil || chain.Branches[0].HasCondition() {
		return nil, false
	}
	branch := chain.Branches[0]
	// Keep alias selection identical to the nested keyed-await recipe.  That
	// recipe accepts direct component events and simple two-way bindings in
	// addition to ordinary props, while the older structural-component helper
	// intentionally accepts only one-way props.
	if !ssrKeyedEachDirectKeyBranch(branch.Children) {
		return nil, false
	}
	components := make([]*ComponentNode, 0, len(branch.Children))
	for _, child := range branch.Children {
		if component, ok := child.(*ComponentNode); ok && component != nil {
			components = append(components, component)
		}
	}
	return components, len(components) > 0
}

// ssrHydrationDirectUnkeyedEachComponent reads the first component-row
// lifecycle slice from the typed structural plan. Keyed rows need retained
// row instance handles and therefore remain a separate reconciliation phase.
func ssrHydrationDirectUnkeyedEachComponents(plan *SSRStructuralLifecyclePlan) ([]*ComponentNode, bool) {
	if plan == nil || plan.Kind != SSRLifecyclePlanEach || ssrHydrationEachPlanHasKey(plan) {
		return nil, false
	}
	block, ok := plan.TemplateNode.(*EachBlockNode)
	if !ok {
		return nil, false
	}
	return ssrCapabilityCurrentUnkeyedEachComponents(block)
}

func ssrHydrationDirectKeyedEachComponents(plan *SSRStructuralLifecyclePlan) ([]*ComponentNode, bool) {
	if plan == nil || plan.Kind != SSRLifecyclePlanEach || !ssrHydrationEachPlanHasKey(plan) {
		return nil, false
	}
	block, ok := plan.TemplateNode.(*EachBlockNode)
	if !ok {
		return nil, false
	}
	return ssrCapabilityCurrentKeyedEachComponents(block)
}

// ssrHydrationDirectKeyedEachControl reads the typed control-only row recipe.
// The lowerer must consume this plan node rather than rediscovering the input
// shape from arbitrary template children.
func ssrHydrationDirectKeyedEachControl(plan *SSRStructuralLifecyclePlan) (*EachBlockNode, *ElementNode, *Attr, bool) {
	if plan == nil || plan.Kind != SSRLifecyclePlanEach || plan.KeyedEach == nil || !plan.KeyedEach.DirectControlRows {
		return nil, nil, nil, false
	}
	block, ok := plan.TemplateNode.(*EachBlockNode)
	if !ok || block == nil || len(block.Children) != 1 {
		return nil, nil, nil, false
	}
	element, ok := block.Children[0].(*ElementNode)
	if !ok || element == nil {
		return nil, nil, nil, false
	}
	for _, attr := range element.Attrs {
		if attr != nil && attr.IsTwoWay && (attr.Name == "value" || attr.Name == "checked") {
			return block, element, attr, true
		}
	}
	return nil, nil, nil, false
}

func ssrHydrationDirectEachComponent(plan *SSRStructuralLifecyclePlan) (*ComponentNode, bool) {
	if plan == nil || plan.Kind != SSRLifecyclePlanEach {
		return nil, false
	}
	var component *ComponentNode
	for _, child := range plan.Children {
		candidate, ok := child.TemplateNode.(*ComponentNode)
		if !ok || candidate == nil {
			continue
		}
		if component != nil || len(candidate.Slots) != 0 || !ssrCapabilityCurrentStructuralComponentAttrs(candidate) {
			return nil, false
		}
		component = candidate
	}
	return component, component != nil
}

// The lifecycle plan preserves source-node identity, so keyedness is read
// from its cached EachBlockNode rather than inferred from printed expressions.
func ssrHydrationEachPlanHasKey(plan *SSRStructuralLifecyclePlan) bool {
	block, ok := plan.TemplateNode.(*EachBlockNode)
	return ok && block != nil && block.HasKey()
}

// ssrHydrationDirectIfComponents is the current direct-child lifecycle subset.
// Its input is a typed #if plan, so callers do not re-derive structure by
// inspecting raw template AST branch lists. Phase 5 expands this helper into
// branch lifecycle lowering for multiple and nested children.
func ssrHydrationDirectIfComponents(plan *SSRStructuralLifecyclePlan) ([]*ComponentNode, int, bool) {
	if plan == nil || plan.Kind != SSRLifecyclePlanIf {
		return nil, 0, false
	}
	var components []*ComponentNode
	componentIndex := -1
	for index, branch := range plan.Children {
		if branch == nil || branch.Kind != SSRLifecyclePlanIfBranch {
			continue
		}
		for _, child := range branch.Children {
			candidate, ok := child.TemplateNode.(*ComponentNode)
			if !ok || candidate == nil || len(candidate.Slots) != 0 {
				continue
			}
			if componentIndex >= 0 && componentIndex != index {
				return nil, 0, false
			}
			components = append(components, candidate)
			componentIndex = index
		}
	}
	return components, componentIndex, len(components) > 0
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
	addComponent := func(component *ComponentNode) error {
		if component == nil {
			return nil
		}
		if !imports[component.Name] {
			return fmt.Errorf("SSR hydration component %q must be imported from a local .vela module", component.Name)
		}
		components[component.Name] = true
		return nil
	}
	// Selected await snapshots own a capability-gated projected-child recipe.
	// Every nested local component in an accepted slot tree needs its named
	// hydrate entry and CSR factory; the selected range owns their handles.
	var visitProjectedSlots func([]Node) error
	visitProjectedSlots = func(nodes []Node) error {
		for _, node := range nodes {
			switch current := node.(type) {
			case *ElementNode:
				if err := visitProjectedSlots(current.Children); err != nil {
					return err
				}
			case *ComponentNode:
				if current == nil || !ssrCapabilityCurrentAwaitSnapshotSlotDirectDOMNodes(current.Slots) {
					continue
				}
				if err := addComponent(current); err != nil {
					return err
				}
				for _, name := range sortedSlotNames(current.Slots) {
					if err := visitProjectedSlots(current.Slots[name]); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	visitSnapshotSlots := func(nodes []Node) error {
		for _, node := range nodes {
			component, ok := node.(*ComponentNode)
			if !ok || component == nil || !ssrCapabilityCurrentAwaitComponentSlots(component, 1) {
				continue
			}
			for _, name := range sortedSlotNames(component.Slots) {
				if err := visitProjectedSlots(component.Slots[name]); err != nil {
					return err
				}
			}
		}
		return nil
	}
	var visit func([]Node) error
	visit = func(nodes []Node) error {
		for _, node := range nodes {
			switch current := node.(type) {
			case *ComponentNode:
				if err := addComponent(current); err != nil {
					return err
				}
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
			case *AwaitBlockNode:
				if err := visit(current.PendingChildren); err != nil {
					return err
				}
				if err := visit(current.ThenChildren); err != nil {
					return err
				}
				if err := visit(current.CatchChildren); err != nil {
					return err
				}
				if err := visitSnapshotSlots(current.ThenChildren); err != nil {
					return err
				}
				if err := visitSnapshotSlots(current.CatchChildren); err != nil {
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
