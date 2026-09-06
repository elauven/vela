package main

import "sort"

// SSRExecutionMode records the server-side decision for one cached template
// AST site. Phase 4 currently classifies template structure; Phase 6 extends
// this same type to script and expression capability decisions.
type SSRExecutionMode uint8

const (
	SSRServerNative SSRExecutionMode = iota
	SSRServerHost
	SSRClientDeferred
	SSRUnsupported
)

// SSRHydrationMode describes whether the current marker-aware lowerer can
// execute the node, needs a future structural lifecycle, or cannot safely
// classify the node at all.
type SSRHydrationMode uint8

const (
	SSRHydrationExecutable SSRHydrationMode = iota
	SSRHydrationNeedsLifecycle
	SSRHydrationUnavailable
)

// SSRMarkerOwnership names the marker family that owns this node's server DOM.
// It is deliberately structural metadata rather than generated identifier text.
type SSRMarkerOwnership uint8

const (
	SSRMarkerNone SSRMarkerOwnership = iota
	SSRMarkerElement
	SSRMarkerTextRange
	SSRMarkerStructuralRange
	SSRMarkerComponentBoundary
	SSRMarkerSlotProjection
)

// SSRLifecycleRequirement lets later lowering choose a recursive lifecycle
// operation without rediscovering a node's role from source text.
type SSRLifecycleRequirement uint8

const (
	SSRLifecycleStatic SSRLifecycleRequirement = iota
	SSRLifecycleElement
	SSRLifecycleStructural
	SSRLifecycleComponent
	SSRLifecycleSlot
)

// SSRCapability is the typed recursive compatibility record for one template
// AST site. Path is deterministic and intended for diagnostics, feature
// matrices, and the future `vela ssr --check` output.
type SSRCapability struct {
	Path      string
	Span      SectionSpan
	NodeKind  string
	Execution SSRExecutionMode
	Hydration SSRHydrationMode
	Marker    SSRMarkerOwnership
	Lifecycle SSRLifecycleRequirement
	Reason    string
	Children  []*SSRCapability
}

// SSRCapabilityIssue is a compact, source-addressable explanation for an
// execution boundary. It is intentionally independent of CLI diagnostics so
// `vela ssr --check` and project compilation can render the same reason chain.
type SSRCapabilityIssue struct {
	Path      string
	Span      SectionSpan
	NodeKind  string
	Execution SSRExecutionMode
	Hydration SSRHydrationMode
	Reason    string
}

// ExecutableHydration reports whether this node belongs to the currently
// emitted marker-aware hydration subset.
func (c *SSRCapability) ExecutableHydration() bool {
	return c != nil && c.Hydration == SSRHydrationExecutable
}

// HydrationIssues returns the most specific non-executable sites in template
// order. A parent that merely summarizes a child's limitation is omitted, so
// callers receive actionable paths instead of a cascade of generic messages.
func (c *SSRCapability) HydrationIssues() []SSRCapabilityIssue {
	var issues []SSRCapabilityIssue
	var visit func(*SSRCapability) bool
	visit = func(current *SSRCapability) bool {
		if current == nil {
			return false
		}
		hasChildIssue := false
		for _, child := range current.Children {
			if visit(child) {
				hasChildIssue = true
			}
		}
		if current.Hydration == SSRHydrationExecutable {
			return hasChildIssue
		}
		if !hasChildIssue && current.Reason != "" {
			issues = append(issues, SSRCapabilityIssue{
				Path: current.Path, Span: current.Span, NodeKind: current.NodeKind,
				Execution: current.Execution, Hydration: current.Hydration, Reason: current.Reason,
			})
		}
		return true
	}
	visit(c)
	return issues
}

// FirstHydrationIssue returns the first deterministic actionable boundary.
func (c *SSRCapability) FirstHydrationIssue() (SSRCapabilityIssue, bool) {
	issues := c.HydrationIssues()
	if len(issues) == 0 {
		return SSRCapabilityIssue{}, false
	}
	return issues[0], true
}

// AnalyzeSSRCapability returns a complete template tree even when the current
// hydrator cannot lower a descendant. That distinction is intentional: a
// planned lifecycle site remains server-native template structure, while its
// Hydration status records why the current client generator cannot adopt it.
func AnalyzeSSRCapability(nodes []Node) *SSRCapability {
	root := &SSRCapability{
		Path:      "template",
		NodeKind:  "component-template",
		Execution: SSRServerNative,
		Hydration: SSRHydrationNeedsLifecycle,
		Marker:    SSRMarkerNone,
		Lifecycle: SSRLifecycleStatic,
		Children:  analyzeSSRCapabilityChildren(nodes, "template"),
	}
	if len(nodes) == 1 {
		if element, ok := nodes[0].(*ElementNode); ok && element != nil {
			root.Span = element.Span
		}
	}
	if len(nodes) != 1 {
		root.Reason = "current hydration requires exactly one element root"
		return root
	}
	if _, ok := nodes[0].(*ElementNode); !ok {
		root.Reason = "current hydration root must be an element"
		return root
	}
	if ssrCapabilityCurrentRootChildren(nodes[0].(*ElementNode).Children) && ssrCapabilityCurrentElementAttrs(nodes[0].(*ElementNode)) {
		root.Hydration = SSRHydrationExecutable
		root.Reason = ""
		return root
	}
	root.Reason = "one or more descendants require a marker-aware structural lifecycle"
	return root
}

func analyzeSSRCapabilityChildren(nodes []Node, path string) []*SSRCapability {
	children := make([]*SSRCapability, 0, len(nodes))
	for index, node := range nodes {
		children = append(children, analyzeSSRCapabilityNode(node, path+"["+itoa(index)+"]"))
	}
	return children
}

func analyzeSSRCapabilityNode(node Node, path string) *SSRCapability {
	capability := &SSRCapability{
		Path:      path,
		Execution: SSRServerNative,
		Hydration: SSRHydrationExecutable,
		Marker:    SSRMarkerNone,
		Lifecycle: SSRLifecycleStatic,
	}
	switch current := node.(type) {
	case *TextNode:
		capability.NodeKind = "text"
		capability.Span = current.Span
		for _, part := range current.Parts {
			if part != nil && !part.Static {
				capability.Marker = SSRMarkerTextRange
				break
			}
		}
	case *ElementNode:
		if current == nil {
			return unsupportedSSRCapability(path, "element", SectionSpan{}, "nil element node")
		}
		capability.NodeKind = "element"
		capability.Span = current.Span
		capability.Marker = SSRMarkerElement
		capability.Lifecycle = SSRLifecycleElement
		capability.Children = analyzeSSRCapabilityChildren(current.Children, path+".children")
		if !ssrCapabilityCurrentElement(current) {
			capability.Hydration = SSRHydrationNeedsLifecycle
			capability.Reason = "element descendants or bindings require additional hydration lifecycle lowering"
		}
	case *IfChainNode:
		if current == nil {
			return unsupportedSSRCapability(path, "if", SectionSpan{}, "nil if chain")
		}
		capability.NodeKind = "key" // overwritten below for ordinary conditionals
		if !current.HasKey() {
			capability.NodeKind = "if"
		}
		capability.Span = current.Span
		capability.Marker = SSRMarkerStructuralRange
		capability.Lifecycle = SSRLifecycleStructural
		for index, branch := range current.Branches {
			if branch == nil {
				capability.Children = append(capability.Children, unsupportedSSRCapability(path+".branches["+itoa(index)+"]", "if-branch", SectionSpan{}, "nil branch"))
				continue
			}
			branchCapability := &SSRCapability{
				Path: path + ".branches[" + itoa(index) + "]", Span: branch.Span, NodeKind: "if-branch",
				Execution: SSRServerNative, Hydration: SSRHydrationExecutable, Marker: SSRMarkerStructuralRange,
				Lifecycle: SSRLifecycleStructural, Children: analyzeSSRCapabilityChildren(branch.Children, path+".branches["+itoa(index)+"]"),
			}
			capability.Children = append(capability.Children, branchCapability)
		}
		if !ssrCapabilityCurrentIfChain(current) {
			capability.Hydration = SSRHydrationNeedsLifecycle
			capability.Reason = "if/key branches require recursive structural lifecycle lowering"
		}
	case *EachBlockNode:
		if current == nil {
			return unsupportedSSRCapability(path, "each", SectionSpan{}, "nil each block")
		}
		capability.NodeKind = "each"
		capability.Span = current.Span
		capability.Marker = SSRMarkerStructuralRange
		capability.Lifecycle = SSRLifecycleStructural
		capability.Children = append(capability.Children, analyzeSSRCapabilityChildren(current.Children, path+".rows")...)
		capability.Children = append(capability.Children, analyzeSSRCapabilityChildren(current.ElseChildren, path+".else")...)
		if !ssrCapabilityCurrentEach(current) {
			capability.Hydration = SSRHydrationNeedsLifecycle
			capability.Reason = "each rows require recursive instance and range lifecycle lowering"
		}
	case *AwaitBlockNode:
		if current == nil {
			return unsupportedSSRCapability(path, "await", SectionSpan{}, "nil await block")
		}
		capability.NodeKind = "await"
		capability.Span = current.Span
		capability.Marker = SSRMarkerStructuralRange
		capability.Lifecycle = SSRLifecycleStructural
		capability.Children = append(capability.Children, analyzeSSRCapabilityChildren(current.PendingChildren, path+".pending")...)
		capability.Children = append(capability.Children, analyzeSSRCapabilityChildren(current.ThenChildren, path+".then")...)
		capability.Children = append(capability.Children, analyzeSSRCapabilityChildren(current.CatchChildren, path+".catch")...)
		if !ssrCapabilityCurrentAwait(current) {
			capability.Hydration = SSRHydrationNeedsLifecycle
			capability.Reason = "await branches require recursive component and cleanup lifecycle lowering"
		}
	case *ComponentNode:
		if current == nil {
			return unsupportedSSRCapability(path, "component", SectionSpan{}, "nil component node")
		}
		capability.NodeKind = "component"
		capability.Span = current.Span
		capability.Marker = SSRMarkerComponentBoundary
		capability.Lifecycle = SSRLifecycleComponent
		names := make([]string, 0, len(current.Slots))
		for name := range current.Slots {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			capability.Children = append(capability.Children, analyzeSSRCapabilityChildren(current.Slots[name], path+".slots."+name)...)
		}
		if !ssrCapabilityCurrentComponent(current) {
			capability.Hydration = SSRHydrationNeedsLifecycle
			capability.Reason = "component props, slots, or callbacks require recursive lifecycle lowering"
		}
	case *SlotNode:
		if current == nil {
			return unsupportedSSRCapability(path, "slot", SectionSpan{}, "nil slot node")
		}
		capability.NodeKind = "slot"
		capability.Span = current.Span
		capability.Marker = SSRMarkerSlotProjection
		capability.Lifecycle = SSRLifecycleSlot
		capability.Children = analyzeSSRCapabilityChildren(current.Fallback, path+".fallback")
		if !ssrCapabilityCurrentSlot(current) {
			capability.Hydration = SSRHydrationNeedsLifecycle
			capability.Reason = "slot fallback requires recursive projection lifecycle lowering"
		}
	default:
		kind := "unknown"
		if node != nil {
			kind = node.nodeKind()
		}
		return unsupportedSSRCapability(path, kind, SectionSpan{}, "unknown template node")
	}
	return capability
}

func unsupportedSSRCapability(path, kind string, span SectionSpan, reason string) *SSRCapability {
	return &SSRCapability{
		Path: path, Span: span, NodeKind: kind, Execution: SSRUnsupported,
		Hydration: SSRHydrationUnavailable, Marker: SSRMarkerNone,
		Lifecycle: SSRLifecycleStatic, Reason: reason,
	}
}

// Current hydration policy lives beside SSRCapability while the recursive
// structural lifecycle is being introduced. These helpers preserve the
// established executable subset; later Phase 4 slices replace their narrow
// shape rules with lifecycle-plan decisions.

func ssrCapabilityCurrentRootChildren(nodes []Node) bool {
	for _, node := range nodes {
		switch current := node.(type) {
		case *TextNode:
			// Direct text parts are represented by SSR `v` ranges when dynamic.
		case *ElementNode:
			if !ssrCapabilityCurrentElement(current) {
				return false
			}
		case *IfChainNode:
			if !ssrCapabilityCurrentIfChain(current) {
				return false
			}
		case *EachBlockNode:
			if !ssrCapabilityCurrentEach(current) {
				return false
			}
		case *AwaitBlockNode:
			if !ssrCapabilityCurrentAwait(current) {
				return false
			}
		case *ComponentNode:
			if !ssrCapabilityCurrentComponent(current) {
				return false
			}
		case *SlotNode:
			if !ssrCapabilityCurrentSlot(current) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// Nested structural components inside an await branch need ownership-aware
// cleanup tied to the outer range. Until that recursive lifecycle lowering is
// present, do not let the generic await branch builder silently materialize
// them and leave live child handles after the range is replaced.
func ssrCapabilityAwaitStructuralChildren(branches []*IfBranch) bool {
	for _, branch := range branches {
		if branch != nil && ssrCapabilityAwaitStructuralNodes(branch.Children) {
			return true
		}
	}
	return false
}

func ssrCapabilityAwaitStructuralNodes(nodes []Node) bool {
	for _, node := range nodes {
		switch current := node.(type) {
		case *ComponentNode:
			return true
		case *ElementNode:
			if ssrCapabilityAwaitStructuralNodes(current.Children) {
				return true
			}
		case *IfChainNode:
			if ssrCapabilityAwaitStructuralChildren(current.Branches) {
				return true
			}
		case *EachBlockNode:
			if ssrCapabilityAwaitStructuralNodes(current.Children) || ssrCapabilityAwaitStructuralNodes(current.ElseChildren) {
				return true
			}
		case *AwaitBlockNode:
			if ssrCapabilityAwaitStructuralNodes(current.PendingChildren) || ssrCapabilityAwaitStructuralNodes(current.ThenChildren) || ssrCapabilityAwaitStructuralNodes(current.CatchChildren) {
				return true
			}
		}
	}
	return false
}

// ssrCapabilityCurrentElement accepts the recursive marker-aware element subset.
// Every accepted descendant receives an `e` acquisition marker; dynamic text
// and direct structural blocks retain their template-order `v` ranges.
func ssrCapabilityCurrentElement(element *ElementNode) bool {
	if element == nil {
		return false
	}
	if _, rawText := rawTextProp(element.Tag); rawText {
		return ssrCapabilityCurrentStaticChildren(element.Children)
	}
	if !ssrCapabilityCurrentElementAttrs(element) {
		return false
	}
	for _, child := range element.Children {
		switch current := child.(type) {
		case *TextNode:
			// `v` ranges cover every non-static part at any nesting depth.
		case *ElementNode:
			if !ssrCapabilityCurrentElement(current) {
				return false
			}
		case *IfChainNode:
			if !ssrCapabilityCurrentIfChain(current) {
				return false
			}
		case *EachBlockNode:
			if !ssrCapabilityCurrentEach(current) {
				return false
			}
		case *AwaitBlockNode:
			if !ssrCapabilityCurrentAwait(current) {
				return false
			}
		case *ComponentNode:
			if !ssrCapabilityCurrentComponent(current) {
				return false
			}
		case *SlotNode:
			if !ssrCapabilityCurrentSlot(current) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func ssrCapabilityCurrentElementAttrs(element *ElementNode) bool {
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

func ssrCapabilityCurrentIfChain(chain *IfChainNode) bool {
	if _, _, ok := ssrCapabilityCurrentIfComponentBranch(chain); ok {
		return true
	}
	if _, _, ok := ssrHydrationIfNestedEachBranch(chain); ok {
		return true
	}
	if chain == nil || len(chain.Branches) == 0 || len(chain.Branches) > 2 {
		return false
	}
	if chain.HasKey() {
		if _, ok := ssrCapabilityCurrentKeyComponents(chain); ok {
			return true
		}
		return len(chain.Branches) == 1 && !chain.Branches[0].HasCondition() && ssrCapabilityCurrentStaticChildren(chain.Branches[0].Children)
	}
	conditions := 0
	for _, branch := range chain.Branches {
		if branch == nil || !ssrCapabilityCurrentStaticChildren(branch.Children) {
			return false
		}
		if branch.HasCondition() {
			conditions++
		}
	}
	return conditions == 1
}

// ssrCapabilityCurrentKeyComponents recognizes the component-owned #key
// lifecycle slice. The keyed range may contain static siblings and any number
// of direct local children. A key transition disposes every owned child before
// rebuilding the complete branch through ordinary CSR factories.
func ssrCapabilityCurrentKeyComponents(chain *IfChainNode) ([]*ComponentNode, bool) {
	if chain == nil || !chain.HasKey() || len(chain.Branches) != 1 || chain.Branches[0] == nil || chain.Branches[0].HasCondition() {
		return nil, false
	}
	branch := chain.Branches[0]
	components := make([]*ComponentNode, 0, len(branch.Children))
	staticChildren := make([]Node, 0, len(branch.Children))
	for _, child := range branch.Children {
		component, ok := child.(*ComponentNode)
		if !ok || component == nil {
			staticChildren = append(staticChildren, child)
			continue
		}
		if len(component.Slots) != 0 || !ssrCapabilityCurrentStructuralComponentAttrs(component) {
			return nil, false
		}
		components = append(components, component)
	}
	if len(components) == 0 || !ssrCapabilityCurrentStaticChildren(staticChildren) {
		return nil, false
	}
	return components, true
}

// ssrCapabilityCurrentIfComponentBranch recognizes the first structural-component
// lifecycle slice: exactly one direct local child with ordinary one-way props
// in a normal #if branch, plus any number of static siblings in either branch.
// The child can adopt its server DOM on the initial branch and an ordinary CSR
// factory can mount it after a later branch switch. Slots, component events,
// bindings, and a second child still need separate ownership lifecycles.
func ssrCapabilityCurrentIfComponentBranch(chain *IfChainNode) ([]*ComponentNode, int, bool) {
	if chain == nil || chain.HasKey() || len(chain.Branches) == 0 || len(chain.Branches) > 2 {
		return nil, 0, false
	}
	conditions := 0
	componentIndex := -1
	var components []*ComponentNode
	for index, branch := range chain.Branches {
		if branch == nil {
			return nil, 0, false
		}
		if branch.HasCondition() {
			conditions++
		}
		staticChildren := make([]Node, 0, len(branch.Children))
		for _, child := range branch.Children {
			candidate, ok := child.(*ComponentNode)
			if !ok || candidate == nil {
				staticChildren = append(staticChildren, child)
				continue
			}
			if len(candidate.Slots) != 0 || !ssrCapabilityCurrentStructuralComponentAttrs(candidate) {
				return nil, 0, false
			}
			if componentIndex >= 0 && componentIndex != index {
				return nil, 0, false
			}
			components = append(components, candidate)
			componentIndex = index
		}
		if !ssrCapabilityCurrentStaticChildren(staticChildren) {
			return nil, 0, false
		}
	}
	return components, componentIndex, len(components) > 0 && conditions == 1
}

// ssrHydrationIfNestedEachBranch recognizes a normal two-branch #if with
// exactly one direct #each branch. The list retains its own marker-aware
// lifecycle, while the outer conditional owns its entry and removal. The
// sibling branch remains static in this first composable slice.
func ssrHydrationIfNestedEachBranch(chain *IfChainNode) (*EachBlockNode, int, bool) {
	if chain == nil || chain.HasKey() || len(chain.Branches) == 0 || len(chain.Branches) > 2 {
		return nil, 0, false
	}
	conditions := 0
	eachIndex := -1
	var each *EachBlockNode
	for index, branch := range chain.Branches {
		if branch == nil {
			return nil, 0, false
		}
		if branch.HasCondition() {
			conditions++
		}
		if len(branch.Children) == 1 {
			if candidate, ok := branch.Children[0].(*EachBlockNode); ok && ssrCapabilityCurrentEach(candidate) {
				if each != nil {
					return nil, 0, false
				}
				each, eachIndex = candidate, index
				continue
			}
		}
		if !ssrCapabilityCurrentStaticChildren(branch.Children) {
			return nil, 0, false
		}
	}
	return each, eachIndex, each != nil && conditions == 1
}

func ssrCapabilityCurrentStructuralComponentAttrs(component *ComponentNode) bool {
	for _, attr := range component.Attrs {
		if attr == nil || attr.IsEvent || attr.IsTwoWay || attr.IsBindThis || attr.IsClassDir || attr.IsStyleDir {
			return false
		}
	}
	return true
}

func ssrCapabilityCurrentEach(block *EachBlockNode) bool {
	if block == nil {
		return false
	}
	if _, _, _, ok := ssrHydrationEachNestedIfComponentRow(block); ok {
		if block.HasKey() {
			return len(block.ElseChildren) == 0
		}
		return ssrCapabilityCurrentStaticChildren(block.ElseChildren)
	}
	if block.HasKey() {
		if _, ok := ssrCapabilityCurrentKeyedEachComponents(block); ok {
			return len(block.ElseChildren) == 0
		}
		// A keyed hydration row is adopted as one existing element.  Empty
		// fallbacks and nested rows will join later with range-aware row
		// handles; accepting either here would silently leave server fallback
		// nodes outside the keyed map on the first client update.
		_, ok := ssrCapabilityCurrentEachDynamicTextRow(block)
		return ok && len(block.ElseChildren) == 0
	}
	if _, ok := ssrCapabilityCurrentUnkeyedEachComponents(block); ok {
		return ssrCapabilityCurrentStaticChildren(block.ElseChildren)
	}
	if _, ok := ssrCapabilityCurrentEachDynamicTextRow(block); !ok && !ssrCapabilityCurrentStaticChildren(block.Children) {
		return false
	}
	return ssrCapabilityCurrentStaticChildren(block.ElseChildren)
}

func ssrCapabilityCurrentKeyedEachComponents(block *EachBlockNode) ([]*ComponentNode, bool) {
	if block == nil || !block.HasKey() || len(block.Children) == 0 {
		return nil, false
	}
	components := make([]*ComponentNode, 0, len(block.Children))
	staticChildren := make([]Node, 0, len(block.Children))
	for _, child := range block.Children {
		component, ok := child.(*ComponentNode)
		if !ok || component == nil {
			staticChildren = append(staticChildren, child)
			continue
		}
		if len(component.Slots) != 0 || !ssrCapabilityCurrentStructuralComponentAttrs(component) {
			return nil, false
		}
		components = append(components, component)
	}
	return components, len(components) > 0 && ssrCapabilityCurrentStaticChildren(staticChildren)
}

func ssrCapabilityCurrentUnkeyedEachComponents(block *EachBlockNode) ([]*ComponentNode, bool) {
	if block == nil || block.HasKey() || len(block.Children) == 0 {
		return nil, false
	}
	components := make([]*ComponentNode, 0, len(block.Children))
	staticChildren := make([]Node, 0, len(block.Children))
	for _, child := range block.Children {
		component, ok := child.(*ComponentNode)
		if !ok || component == nil {
			staticChildren = append(staticChildren, child)
			continue
		}
		if len(component.Slots) != 0 || !ssrCapabilityCurrentStructuralComponentAttrs(component) {
			return nil, false
		}
		components = append(components, component)
	}
	return components, len(components) > 0 && ssrCapabilityCurrentStaticChildren(staticChildren)
}

// ssrHydrationEachNestedIfComponentRow recognizes the first per-row
// structural-child slice: an unkeyed row contains one ordinary #if whose
// component branch contains exactly one direct child. The unkeyed row owner
// may replace its complete range on change, so this preserves correct child
// ownership before row-local marker reconciliation is introduced.
func ssrHydrationEachNestedIfComponentRow(block *EachBlockNode) (*IfChainNode, *ComponentNode, int, bool) {
	if block == nil || len(block.Children) != 1 {
		return nil, nil, 0, false
	}
	chain, ok := block.Children[0].(*IfChainNode)
	if !ok || chain == nil || chain.HasKey() {
		return nil, nil, 0, false
	}
	components, componentIndex, ok := ssrCapabilityCurrentIfComponentBranch(chain)
	if !ok || len(components) != 1 || componentIndex < 0 || componentIndex >= len(chain.Branches) {
		return nil, nil, 0, false
	}
	branch := chain.Branches[componentIndex]
	if branch == nil || len(branch.Children) != 1 || branch.Children[0] != components[0] {
		return nil, nil, 0, false
	}
	return chain, components[0], componentIndex, true
}

// ssrCapabilityCurrentEachComponentRow is the first list-child lifecycle
// subset: one direct local child per unkeyed row, no slots/events/bindings.
// The outer range remains the row ownership boundary, so a later list update
// destroys every adopted child before rebuilding that range through CSR.
func ssrCapabilityCurrentEachComponentRow(block *EachBlockNode) (*ComponentNode, bool) {
	if block == nil || len(block.Children) != 1 {
		return nil, false
	}
	component, ok := block.Children[0].(*ComponentNode)
	if !ok || component == nil || len(component.Slots) != 0 || !ssrCapabilityCurrentStructuralComponentAttrs(component) {
		return nil, false
	}
	return component, true
}

// ssrCapabilityCurrentAwait admits the first marker-aware await subset. SSR always
// renders the pending branch, so hydration can retain that exact DOM and only
// replace its range once the client-side promise settles. The pending branch
// is static; then/catch support static HTML frames with dynamic text holes
// bound to the settled value or error.
func ssrCapabilityCurrentAwait(block *AwaitBlockNode) bool {
	if block == nil || len(block.PendingChildren) == 0 {
		return false
	}
	if ssrCapabilityCurrentStaticChildren(block.PendingChildren) &&
		ssrCapabilityCurrentAwaitBranch(block.ThenChildren) &&
		ssrCapabilityCurrentAwaitBranch(block.CatchChildren) {
		return true
	}
	if ssrHydrationPendingAwaitStructural(block) &&
		ssrCapabilityCurrentAwaitBranch(block.ThenChildren) &&
		ssrCapabilityCurrentAwaitBranch(block.CatchChildren) {
		return true
	}
	_, ok := ssrCapabilityCurrentAwaitComponentBranches(block)
	return ok
}

// ssrHydrationPendingAwaitIfChain recognizes the first recursive pending
// adoption slice: the server-pending branch consists of one ordinary #if
// whose direct child lifecycle is already supported by the #if hydrator.
// The outer await owns that inner range while pending and disposes it before
// removing the complete await range on settlement or promise replacement.
func ssrHydrationPendingAwaitStructural(block *AwaitBlockNode) bool {
	if _, ok := ssrHydrationPendingAwaitIfChain(block); ok {
		return true
	}
	_, ok := ssrHydrationPendingAwaitEachBlock(block)
	return ok
}

// ssrHydrationPendingAwaitEachBlock recognizes one direct list structural
// branch. The normal each hydrator owns row adoption, child payload lookup,
// row replacement, and child destruction; the outer await owns its lifetime.
func ssrHydrationPendingAwaitEachBlock(block *AwaitBlockNode) (*EachBlockNode, bool) {
	if block == nil || len(block.PendingChildren) != 1 {
		return nil, false
	}
	each, ok := block.PendingChildren[0].(*EachBlockNode)
	if !ok || each == nil || !ssrCapabilityCurrentEach(each) {
		return nil, false
	}
	return each, true
}

func ssrHydrationPendingAwaitIfChain(block *AwaitBlockNode) (*IfChainNode, bool) {
	if block == nil || len(block.PendingChildren) != 1 {
		return nil, false
	}
	chain, ok := block.PendingChildren[0].(*IfChainNode)
	if !ok || chain == nil {
		return nil, false
	}
	if chain.HasKey() {
		_, ok := ssrCapabilityCurrentKeyComponents(chain)
		return chain, ok
	}
	if _, _, ok := ssrCapabilityCurrentIfComponentBranch(chain); !ok {
		return nil, false
	}
	return chain, true
}

// ssrCapabilityCurrentAwaitComponentBranches admits the first await child
// lifecycle slice. Every branch may contain direct local components with
// ordinary one-way props and static siblings. The pending branch is adopted
// from SSR; settled branches are materialized with ordinary CSR factories.
// Dynamic DOM holes remain on the existing await-only lowering path.
func ssrCapabilityCurrentAwaitComponentBranches(block *AwaitBlockNode) ([][]*ComponentNode, bool) {
	if block == nil || len(block.PendingChildren) == 0 {
		return nil, false
	}
	branches := [][]Node{block.PendingChildren, block.ThenChildren, block.CatchChildren}
	result := make([][]*ComponentNode, len(branches))
	found := false
	for index, children := range branches {
		staticChildren := make([]Node, 0, len(children))
		for _, child := range children {
			component, ok := child.(*ComponentNode)
			if !ok || component == nil {
				staticChildren = append(staticChildren, child)
				continue
			}
			if len(component.Slots) != 0 || !ssrCapabilityCurrentStructuralComponentAttrs(component) {
				return nil, false
			}
			result[index] = append(result[index], component)
			found = true
		}
		if !ssrCapabilityCurrentStaticChildren(staticChildren) {
			return nil, false
		}
	}
	return result, found
}

// ssrCapabilityCurrentAwaitBranch permits static HTML plus text interpolation after a
// promise settles. It also permits DOM events and class/style directives.
// Nested static-branch if blocks and supported each forms are available; nested await blocks and components
// still need a dedicated lifecycle plan rather than being silently lowered as static HTML.
func ssrCapabilityCurrentAwaitBranch(nodes []Node) bool {
	for _, node := range nodes {
		switch current := node.(type) {
		case *TextNode:
			continue
		case *IfChainNode:
			if !ssrCapabilityCurrentIfChain(current) {
				return false
			}
		case *ElementNode:
			if current == nil {
				return false
			}
			for _, attr := range current.Attrs {
				if attr == nil || attr.IsBindThis {
					return false
				}
			}
			if _, rawText := rawTextProp(current.Tag); rawText {
				if _, interpolation := singleInterpolation(current.Children); interpolation {
					return false
				}
			}
			if !ssrCapabilityCurrentAwaitBranch(current.Children) {
				return false
			}
		case *EachBlockNode:
			if !ssrCapabilityCurrentEach(current) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// ssrCapabilityCurrentEachDynamicTextRow accepts one ordinary row element with only
// static attributes and direct text nodes. Text parts may refer to the each
// item; replacement rows are built as DOM nodes, not HTML source strings.
func ssrCapabilityCurrentEachDynamicTextRow(block *EachBlockNode) (*ElementNode, bool) {
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

// ssrCapabilityCurrentComponent accepts a direct local child with ordinary props, `@on:*`
// component events, bindable `bind:value`, static slot fallback, recursive ordinary
// slot elements, and top-level #if/#key/#each nodes in the existing root subset.
func ssrCapabilityCurrentComponent(component *ComponentNode) bool {
	if component == nil {
		return false
	}
	for _, slot := range component.Slots {
		if !ssrCapabilityCurrentSlotContent(slot) {
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

// ssrCapabilityCurrentSlot is static because parent-owned slot DOM is already present
// inside the child root. The child hydrator deliberately leaves it untouched.
func ssrCapabilityCurrentSlot(slot *SlotNode) bool {
	return slot != nil && ssrCapabilityCurrentStaticChildren(slot.Fallback)
}

// ssrCapabilityCurrentSlotContent admits parent-projected text and recursive ordinary
// elements. Parent owns their markers and reactive work; child leaves them in place.
func ssrCapabilityCurrentSlotContent(nodes []Node) bool {
	for _, node := range nodes {
		switch current := node.(type) {
		case *TextNode:
			continue
		case *ElementNode:
			if !ssrCapabilityCurrentSlotElement(current) {
				return false
			}
		case *IfChainNode:
			if !ssrCapabilityCurrentIfChain(current) {
				return false
			}
		case *EachBlockNode:
			if !ssrCapabilityCurrentEach(current) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// ssrCapabilityCurrentSlotElement intentionally excludes structural blocks, nested
// components, and nested slots. Their marker ownership needs a dedicated plan.
func ssrCapabilityCurrentSlotElement(element *ElementNode) bool {
	if element == nil || !ssrCapabilityCurrentElementAttrs(element) {
		return false
	}
	if _, rawText := rawTextProp(element.Tag); rawText {
		return ssrCapabilityCurrentStaticChildren(element.Children)
	}
	for _, child := range element.Children {
		switch current := child.(type) {
		case *TextNode:
			continue
		case *ElementNode:
			if !ssrCapabilityCurrentSlotElement(current) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func ssrCapabilityCurrentStaticChildren(nodes []Node) bool {
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
			if !ssrCapabilityCurrentStaticChildren(current.Children) {
				return false
			}
		default:
			return false
		}
	}
	return true
}
