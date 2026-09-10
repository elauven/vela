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

// SSRKeyedEachLifecycleRecipe is the typed identity contract for a keyed
// #each range. Collection and Key retain the parser-owned cached expression
// ASTs; lowerers must evaluate those nodes and must never reconstruct either
// expression from EachExpr or EachKey source text. OuterScope and RowScope
// identify, respectively, the enclosing range and one runtime inner row.
//
// The recipe is deliberately attached to the structural plan even before a
// particular keyed range is executable for hydration. This lets a later
// lowerer add a bounded lifecycle slice without repeating template-shape
// analysis or inventing marker namespaces from printed JavaScript.
type SSRKeyedEachLifecycleRecipe struct {
	Span       SectionSpan
	Collection *TemplateExpr
	Key        *TemplateExpr
	ItemName   string
	IndexName  string
	OuterScope SSRInstancePath
	RowScope   SSRInstancePath
	// DirectLeafRows identifies the deliberately narrow first lowering slice:
	// direct text plus ordinary sibling elements with direct text leaves and
	// currently supported attribute/directive/event/native-binding operations.
	// It excludes else, components, and deeper structural ownership.
	DirectLeafRows bool
	// DirectConditionalRows records the next bounded inner-row slice: direct
	// `#if` content whose branches stay within the recursive direct-DOM subset.
	// It is analysis metadata until the inner range controller consumes it.
	DirectConditionalRows bool
	// DirectKeyRows reserves the adjacent direct-DOM `#key` slice. Its
	// evaluated key owns a nested range inside an already keyed row; execution
	// remains gated until the range controller is lowered on both SSR/CSR paths.
	DirectKeyRows bool
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
	// KeyedEach is present exactly for a keyed EachBlockNode. It is the
	// source-ordered recipe a future keyed range lowerer consumes.
	KeyedEach *SSRKeyedEachLifecycleRecipe
}

// AwaitBranch returns one synthetic pending/then/catch branch from a typed
// await lifecycle node. It keeps lowerers on the shared plan rather than
// re-deriving branch ownership by scanning template children.
func (p *SSRStructuralLifecyclePlan) AwaitBranch(index int) *SSRStructuralLifecyclePlan {
	if p == nil || p.Kind != SSRLifecyclePlanAwait || index < 0 || index >= len(p.Children) {
		return nil
	}
	branch := p.Children[index]
	if branch == nil || branch.Kind != SSRLifecyclePlanAwaitBranch {
		return nil
	}
	return branch
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
		if current.HasKey() {
			plan.KeyedEach = &SSRKeyedEachLifecycleRecipe{
				Span:                  current.Span,
				Collection:            current.IterExpr,
				Key:                   current.KeyAST,
				ItemName:              current.ItemName,
				IndexName:             current.IndexName,
				OuterScope:            plan.InstancePath,
				RowScope:              rowPath,
				DirectLeafRows:        ssrKeyedEachDirectLeafRows(current),
				DirectConditionalRows: ssrKeyedEachDirectConditionalRows(current),
				DirectKeyRows:         ssrKeyedEachDirectKeyRows(current),
			}
		}
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

// ssrKeyedEachDirectLeafRows describes the first independently reconciled
// inner keyed-row slice. It deliberately permits no structural descendants:
// their ownership requires nested marker ranges rather than a leaf row map.
// Attribute validation reuses the established keyed-await direct-DOM subset so
// this plan cannot promise a leaf operation the existing DOM lowerer rejects.
func ssrKeyedEachDirectLeafRows(block *EachBlockNode) bool {
	if block == nil || !block.HasKey() || len(block.Children) == 0 {
		return false
	}
	// The empty branch has no row identity. It is instead owned by one
	// temporary direct-DOM controller alongside the keyed row map. Keep the
	// same direct-DOM boundary as a settled keyed-await branch: components and
	// deeper structural blocks require their own lifecycle recipes.
	if !ssrCapabilityKeyedAwaitDirectIfChildren(block.ElseChildren) {
		return false
	}
	for _, node := range block.Children {
		switch current := node.(type) {
		case *TextNode:
			if current == nil {
				return false
			}
		case *ElementNode:
			if current == nil || !ssrCapabilityKeyedAwaitDirectElement(current) {
				return false
			}
			for _, child := range current.Children {
				if _, ok := child.(*TextNode); !ok {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}

// ssrKeyedEachDirectConditionalRows is deliberately separate from
// DirectLeafRows. It preserves the existing executable boundary while the
// lifecycle plan records exactly which rows can later be lowered through an
// inner range controller rather than a whole outer-row replacement.
func ssrKeyedEachDirectConditionalRows(block *EachBlockNode) bool {
	if block == nil || !block.HasKey() || len(block.Children) == 0 {
		return false
	}
	if !ssrCapabilityKeyedAwaitDirectIfChildren(block.ElseChildren) {
		return false
	}
	found := false
	for _, node := range block.Children {
		switch current := node.(type) {
		case *TextNode:
			if current == nil {
				return false
			}
		case *ElementNode:
			if current == nil || !ssrCapabilityKeyedAwaitDirectElement(current) {
				return false
			}
			for _, child := range current.Children {
				if _, ok := child.(*TextNode); !ok {
					return false
				}
			}
		case *IfChainNode:
			if !ssrCapabilityKeyedAwaitDirectIfChain(current) {
				return false
			}
			// First executable inner-controller slice: the condition is dynamic,
			// but branches are static DOM. Dynamic branch leaves are added only
			// once they have a row-owned update collector.
			for _, branch := range current.Branches {
				if branch == nil || !ssrKeyedEachDirectConditionalBranch(branch.Children) {
					return false
				}
			}
			found = true
		default:
			return false
		}
	}
	return found
}

func ssrKeyedEachDirectKeyRows(block *EachBlockNode) bool {
	if block == nil || !block.HasKey() || len(block.Children) != 1 || len(block.ElseChildren) != 0 {
		return false
	}
	chain, ok := block.Children[0].(*IfChainNode)
	if !ok || chain == nil || !chain.HasKey() || len(chain.Branches) != 1 || chain.Branches[0] == nil || chain.Branches[0].HasCondition() {
		return false
	}
	return ssrKeyedEachDirectKeyBranch(chain.Branches[0].Children)
}

func ssrKeyedEachDirectKeyBranch(nodes []Node) bool {
	hasConditional := false
	hasEach := false
	for _, node := range nodes {
		switch current := node.(type) {
		case *TextNode:
			if current == nil {
				return false
			}
		case *ElementNode:
			if !ssrKeyedEachDirectConditionalElement(current) {
				return false
			}
		case *ComponentNode:
			if current == nil {
				return false
			}
			for _, slot := range current.Slots {
				if !ssrKeyedEachDirectKeySlot(slot) {
					return false
				}
			}
			for _, attr := range current.Attrs {
				if attr == nil || attr.IsBindThis || attr.IsClassDir || attr.IsStyleDir {
					return false
				}
				if attr.IsTwoWay {
					if attr.Expr == nil {
						return false
					}
					if _, ok := templateExprIdent(attr.Expr); !ok {
						return false
					}
				}
				if attr.IsEvent && attr.Expr == nil {
					return false
				}
			}
		case *IfChainNode:
			// A plain direct conditional is owned by the surrounding #key
			// controller.  It may replace only that key range when its active
			// branch changes; nested #key/component ranges need another recipe.
			if current == nil || current.HasKey() || !ssrCapabilityKeyedAwaitDirectIfChain(current) {
				return false
			}
			if hasConditional {
				return false
			}
			hasConditional = true
		case *EachBlockNode:
			// The nested-list controller owns one direct-DOM range per row. Both
			// keyed and unkeyed lists exclude deeper structures here. Their
			// fallback is owned by the list controller rather than the row map.
			if current == nil || !ssrKeyedEachDirectKeyEachRows(current) || !ssrKeyedEachDirectKeyEachElse(current.ElseChildren) || hasEach {
				return false
			}
			hasEach = true
		default:
			return false
		}
	}
	return true
}

// ssrKeyedEachDirectKeySlot accepts static slot trees and the first dynamic
// slot recipe used by a nested keyed await child. Dynamic values are limited
// to text and ordinary element attributes, events, and native bindings: those
// DOM references, listeners, and update callbacks can be owned by the
// enclosing #key controller. Structural blocks and nested components still
// need their own slot lifecycle controller and remain outside this slice.
func ssrKeyedEachDirectKeySlot(nodes []Node) bool {
	if allStatic(nodes) {
		return true
	}
	for _, node := range nodes {
		switch current := node.(type) {
		case *TextNode:
			if current == nil {
				return false
			}
		case *ElementNode:
			if !ssrKeyedEachDirectKeySlotElement(current) {
				return false
			}
		case *IfChainNode:
			if current == nil || current.HasKey() || len(current.Branches) == 0 {
				return false
			}
			for _, branch := range current.Branches {
				if branch == nil || !ssrKeyedEachDirectKeySlot(branch.Children) {
					return false
				}
			}
		case *EachBlockNode:
			if current == nil || current.HasKey() || !ssrKeyedEachDirectKeySlot(current.Children) || !ssrKeyedEachDirectKeySlot(current.ElseChildren) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func ssrKeyedEachDirectKeySlotElement(element *ElementNode) bool {
	if element == nil {
		return false
	}
	for _, attr := range element.Attrs {
		// IsBind is the parser representation for canonical :name={expr}
		// one-way attributes. Two-way bind:value is tracked separately.
		if attr == nil || attr.IsBindThis || ((attr.IsEvent || attr.IsTwoWay) && attr.Expr == nil) {
			return false
		}
	}
	return ssrKeyedEachDirectKeySlot(element.Children)
}

func ssrKeyedEachDirectKeyEachRows(block *EachBlockNode) bool {
	if block == nil || len(block.Children) == 0 {
		return false
	}
	for _, node := range block.Children {
		switch current := node.(type) {
		case *TextNode:
			if current == nil {
				return false
			}
		case *ElementNode:
			if current == nil || !ssrKeyedEachDirectConditionalElement(current) {
				return false
			}
			for _, child := range current.Children {
				if _, ok := child.(*TextNode); !ok {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}

// ssrKeyedEachDirectKeyEachElse accepts the same direct-DOM shape as a list
// row. The fallback has a dedicated controller, so text/attribute updates and
// listener teardown are not shared with the keyed row map.
func ssrKeyedEachDirectKeyEachElse(nodes []Node) bool {
	return ssrKeyedEachDirectConditionalBranch(nodes)
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
