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
	if ssrHydrationRootChildren(nodes[0].(*ElementNode).Children) && ssrHydrationElementAttrs(nodes[0].(*ElementNode)) {
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
		if !ssrHydrationElement(current) {
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
		if !ssrHydrationIfChain(current) {
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
		if !ssrHydrationEach(current) {
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
		if !ssrHydrationAwait(current) {
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
		if !ssrHydrationComponent(current) {
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
		if !ssrHydrationSlot(current) {
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
