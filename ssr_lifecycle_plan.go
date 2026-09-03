package main

import "sort"

// SSRLifecycleOperation is one ownership action for a server-rendered range.
// The recursive hydration lowerer consumes these operations directly rather
// than rediscovering them from a printable template representation.
type SSRLifecycleOperation uint8

const (
	SSRLifecycleAdopt SSRLifecycleOperation = iota
	SSRLifecycleMountCSR
	SSRLifecycleUpdate
	SSRLifecycleDestroy
	SSRLifecycleCollectPayload
)

// SSRLifecyclePlanKind identifies a template or synthetic structural branch.
type SSRLifecyclePlanKind uint8

const (
	SSRLifecyclePlanRoot SSRLifecyclePlanKind = iota
	SSRLifecyclePlanText
	SSRLifecyclePlanElement
	SSRLifecyclePlanIf
	SSRLifecyclePlanIfBranch
	SSRLifecyclePlanEach
	SSRLifecyclePlanAwait
	SSRLifecyclePlanAwaitBranch
	SSRLifecyclePlanComponent
	SSRLifecyclePlanSlot
)

// SSRInstanceSegmentKind separates compiler-owned structural identity from
// printable selector text. Dynamic each rows retain their cached KeyAST so a
// future lowerer can resolve the same identity on server and client.
type SSRInstanceSegmentKind uint8

const (
	SSRInstanceRoot SSRInstanceSegmentKind = iota
	SSRInstanceNode
	SSRInstanceIfBranch
	SSRInstanceKey
	SSRInstanceEachRow
	SSRInstanceAwaitBranch
	SSRInstanceComponent
	SSRInstanceSlot
)

// SSRInstanceSegment is one typed portion of a component/range instance path.
// Name is template metadata for components, slots, and await branches. Key is
// always a cached AST reference, never a reparsed expression string.
type SSRInstanceSegment struct {
	Kind  SSRInstanceSegmentKind
	Index int
	Name  string
	Key   *TemplateExpr
}

// SSRInstancePath is an immutable structural identity recipe. At runtime an
// each-row segment is resolved with its index or stable key value; keeping the
// recipe typed prevents repeated child components from sharing a document ID.
type SSRInstancePath struct {
	Segments []SSRInstanceSegment
}

func (p SSRInstancePath) append(segment SSRInstanceSegment) SSRInstancePath {
	next := SSRInstancePath{Segments: make([]SSRInstanceSegment, len(p.Segments)+1)}
	copy(next.Segments, p.Segments)
	next.Segments[len(p.Segments)] = segment
	return next
}

// SSRStructuralLifecyclePlan is the recursive ownership plan shared by future
// SSR marker emission and hydration lowering. Capability points at the same
// typed analysis node that decided the current execution boundary.
type SSRStructuralLifecyclePlan struct {
	TemplatePath string
	Span         SectionSpan
	Kind         SSRLifecyclePlanKind
	// TemplateNode retains the immutable source AST node for ordinary template
	// sites. Synthetic if/await branch nodes deliberately leave it nil. This
	// lets lowerers consume plan ownership and typed source metadata together
	// without scanning template children a second time.
	TemplateNode Node
	InstancePath SSRInstancePath
	Capability   *SSRCapability
	Operations   []SSRLifecycleOperation
	Children     []*SSRStructuralLifecyclePlan
}

// Find returns the unique plan node for one typed template site. It is used by
// incremental lowerers while they migrate from AST-shape dispatch to direct
// lifecycle-plan dispatch. Matching both kind and span keeps synthetic branch
// nodes distinct from their structural owner. A dynamic text Part belongs to
// the enclosing TextNode, so text lookup accepts a contained part span.
func (p *SSRStructuralLifecyclePlan) Find(kind SSRLifecyclePlanKind, span SectionSpan) *SSRStructuralLifecyclePlan {
	if p == nil {
		return nil
	}
	if p.Kind == kind {
		if p.Span == span {
			return p
		}
		if kind == SSRLifecyclePlanText && p.Span.Start <= span.Start && span.End <= p.Span.End {
			return p
		}
	}
	for _, child := range p.Children {
		if found := child.Find(kind, span); found != nil {
			return found
		}
	}
	return nil
}

func (p *SSRStructuralLifecyclePlan) hasOperation(operation SSRLifecycleOperation) bool {
	if p == nil {
		return false
	}
	for _, current := range p.Operations {
		if current == operation {
			return true
		}
	}
	return false
}

// BuildSSRStructuralLifecyclePlan preserves every template descendant even
// when its current Hydration status is needs-lifecycle. This makes the plan a
// forward-compatible ownership graph rather than another subset predicate.
func BuildSSRStructuralLifecyclePlan(nodes []Node) *SSRStructuralLifecyclePlan {
	capability := AnalyzeSSRCapability(nodes)
	root := &SSRStructuralLifecyclePlan{
		TemplatePath: capability.Path,
		Span:         capability.Span,
		Kind:         SSRLifecyclePlanRoot,
		InstancePath: SSRInstancePath{Segments: []SSRInstanceSegment{{Kind: SSRInstanceRoot}}},
		Capability:   capability,
		Operations:   []SSRLifecycleOperation{SSRLifecycleAdopt, SSRLifecycleUpdate, SSRLifecycleDestroy},
	}
	root.Children = buildSSRStructuralLifecycleNodes(nodes, capability.Children, root.InstancePath, capability.Path)
	return root
}

func buildSSRStructuralLifecycleNodes(nodes []Node, capabilities []*SSRCapability, path SSRInstancePath, parentTemplatePath string) []*SSRStructuralLifecyclePlan {
	plans := make([]*SSRStructuralLifecyclePlan, 0, len(nodes))
	for index, node := range nodes {
		var capability *SSRCapability
		if index < len(capabilities) {
			capability = capabilities[index]
		}
		nodePath := path.append(SSRInstanceSegment{Kind: SSRInstanceNode, Index: index})
		plans = append(plans, buildSSRStructuralLifecycleNode(node, capability, nodePath, parentTemplatePath+"["+itoa(index)+"]"))
	}
	return plans
}

func buildSSRStructuralLifecycleNode(node Node, capability *SSRCapability, path SSRInstancePath, fallbackTemplatePath string) *SSRStructuralLifecyclePlan {
	plan := &SSRStructuralLifecyclePlan{TemplateNode: node, Capability: capability, InstancePath: path}
	if capability != nil {
		plan.TemplatePath = capability.Path
		plan.Span = capability.Span
	} else {
		plan.TemplatePath = fallbackTemplatePath
	}
	switch current := node.(type) {
	case *TextNode:
		plan.Kind = SSRLifecyclePlanText
		plan.Operations = []SSRLifecycleOperation{SSRLifecycleAdopt, SSRLifecycleUpdate}
	case *ElementNode:
		plan.Kind = SSRLifecyclePlanElement
		plan.Operations = []SSRLifecycleOperation{SSRLifecycleAdopt, SSRLifecycleUpdate, SSRLifecycleDestroy}
		plan.Children = buildSSRStructuralLifecycleNodes(current.Children, capabilityChildren(capability), path, plan.TemplatePath+".children")
	case *IfChainNode:
		plan.Kind = SSRLifecyclePlanIf
		plan.Operations = structuralLifecycleOperations()
		if current.HasKey() {
			plan.InstancePath = plan.InstancePath.append(SSRInstanceSegment{Kind: SSRInstanceKey, Key: current.KeyAST})
		}
		for index, branch := range current.Branches {
			var branchCapability *SSRCapability
			if capability != nil && index < len(capability.Children) {
				branchCapability = capability.Children[index]
			}
			branchPlan := &SSRStructuralLifecyclePlan{
				TemplatePath: plan.TemplatePath + ".branches[" + itoa(index) + "]",
				Kind:         SSRLifecyclePlanIfBranch,
				InstancePath: plan.InstancePath.append(SSRInstanceSegment{Kind: SSRInstanceIfBranch, Index: index}),
				Capability:   branchCapability,
				Operations:   structuralLifecycleOperations(),
			}
			if branch != nil {
				branchPlan.Span = branch.Span
				branchPlan.Children = buildSSRStructuralLifecycleNodes(branch.Children, capabilityChildren(branchCapability), branchPlan.InstancePath, branchPlan.TemplatePath)
			}
			plan.Children = append(plan.Children, branchPlan)
		}
	case *EachBlockNode:
		plan.Kind = SSRLifecyclePlanEach
		plan.Operations = structuralLifecycleOperations()
		rowPath := plan.InstancePath.append(SSRInstanceSegment{Kind: SSRInstanceEachRow, Index: -1, Key: current.KeyAST})
		rowCapabilities, elseCapabilities := splitEachCapabilityChildren(capability, len(current.Children))
		plan.Children = append(plan.Children, buildSSRStructuralLifecycleNodes(current.Children, rowCapabilities, rowPath, plan.TemplatePath+".rows")...)
		plan.Children = append(plan.Children, buildSSRStructuralLifecycleNodes(current.ElseChildren, elseCapabilities, plan.InstancePath, plan.TemplatePath+".else")...)
	case *AwaitBlockNode:
		plan.Kind = SSRLifecyclePlanAwait
		plan.Operations = structuralLifecycleOperations()
		branchCapabilities := capabilityChildren(capability)
		branchNames := []string{"pending", "then", "catch"}
		branchNodes := [][]Node{current.PendingChildren, current.ThenChildren, current.CatchChildren}
		cursor := 0
		for index, children := range branchNodes {
			count := len(children)
			var branchCapabilityChildren []*SSRCapability
			if cursor+count <= len(branchCapabilities) {
				branchCapabilityChildren = branchCapabilities[cursor : cursor+count]
			}
			cursor += count
			branchPlan := &SSRStructuralLifecyclePlan{
				TemplatePath: plan.TemplatePath + "." + branchNames[index],
				Kind:         SSRLifecyclePlanAwaitBranch,
				InstancePath: plan.InstancePath.append(SSRInstanceSegment{Kind: SSRInstanceAwaitBranch, Index: index, Name: branchNames[index]}),
				Operations:   structuralLifecycleOperations(),
			}
			branchPlan.Children = buildSSRStructuralLifecycleNodes(children, branchCapabilityChildren, branchPlan.InstancePath, branchPlan.TemplatePath)
			plan.Children = append(plan.Children, branchPlan)
		}
	case *ComponentNode:
		plan.Kind = SSRLifecyclePlanComponent
		plan.InstancePath = plan.InstancePath.append(SSRInstanceSegment{Kind: SSRInstanceComponent, Name: current.Name})
		plan.Operations = structuralLifecycleOperations()
		names := sortedSlotNames(current.Slots)
		cursor := 0
		for _, name := range names {
			children := current.Slots[name]
			var slotCapabilities []*SSRCapability
			if capability != nil && cursor+len(children) <= len(capability.Children) {
				slotCapabilities = capability.Children[cursor : cursor+len(children)]
			}
			cursor += len(children)
			slotPlan := &SSRStructuralLifecyclePlan{
				TemplatePath: plan.TemplatePath + ".slots." + name,
				Kind:         SSRLifecyclePlanSlot,
				InstancePath: plan.InstancePath.append(SSRInstanceSegment{Kind: SSRInstanceSlot, Name: name}),
				Operations:   []SSRLifecycleOperation{SSRLifecycleAdopt, SSRLifecycleUpdate, SSRLifecycleDestroy},
			}
			slotPlan.Children = buildSSRStructuralLifecycleNodes(children, slotCapabilities, slotPlan.InstancePath, slotPlan.TemplatePath)
			plan.Children = append(plan.Children, slotPlan)
		}
	case *SlotNode:
		plan.Kind = SSRLifecyclePlanSlot
		plan.InstancePath = plan.InstancePath.append(SSRInstanceSegment{Kind: SSRInstanceSlot, Name: current.Name})
		plan.Operations = []SSRLifecycleOperation{SSRLifecycleAdopt, SSRLifecycleUpdate, SSRLifecycleDestroy}
		plan.Children = buildSSRStructuralLifecycleNodes(current.Fallback, capabilityChildren(capability), plan.InstancePath, plan.TemplatePath+".fallback")
	default:
		plan.Kind = SSRLifecyclePlanRoot
		plan.Operations = []SSRLifecycleOperation{SSRLifecycleDestroy}
	}
	return plan
}

func structuralLifecycleOperations() []SSRLifecycleOperation {
	return []SSRLifecycleOperation{SSRLifecycleAdopt, SSRLifecycleMountCSR, SSRLifecycleUpdate, SSRLifecycleDestroy, SSRLifecycleCollectPayload}
}

func capabilityChildren(capability *SSRCapability) []*SSRCapability {
	if capability == nil {
		return nil
	}
	return capability.Children
}

func splitEachCapabilityChildren(capability *SSRCapability, rowCount int) ([]*SSRCapability, []*SSRCapability) {
	children := capabilityChildren(capability)
	if rowCount > len(children) {
		return children, nil
	}
	return children[:rowCount], children[rowCount:]
}

func sortedSlotNames(slots map[string][]Node) []string {
	names := make([]string, 0, len(slots))
	for name := range slots {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
