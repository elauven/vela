package main

import (
	"fmt"
	"strings"
	"unicode"
)

// SSRRenderPlan is the server-target-neutral representation of a validated
// component template. It is built exclusively from ComponentAST nodes and
// cached template expression ASTs. The Go renderer and the later hydrator use
// this same node/event order; neither is allowed to reparse template strings.
type SSRRenderPlan struct {
	ComponentName string
	ScopeID       string
	Props         []SSRPlanProp
	// ReactiveNames is analysis metadata for diagnostics and bridge safety.
	// It is not emitted into server code.
	ReactiveNames []string
	Nodes         []SSRPlanNode
	Events        []SSRStaticEvent
	Dynamics      []SSRDynamicSite
	ScopedCSS     string
}

// SSRPlanNode is a typed render-plan node. It deliberately mirrors the
// template AST rather than storing printable HTML or Go source.
type SSRValueType uint8

const (
	SSRTypeAny SSRValueType = iota
	SSRTypeString
	SSRTypeBool
	SSRTypeInt
	SSRTypeFloat
	SSRTypeSlice
	SSRTypeObject
)

// SSRPlanProp is semantic server-input metadata. The Go AST backend maps Type
// to a typed ast.Expr rather than storing Go source fragments.
type SSRPlanProp struct {
	SourceName string
	GoName     string
	Type       SSRValueType
	Default    JsExpr
	Bindable   bool
}

type SSRPlanNode interface {
	ssrPlanNode()
}

type SSRPlanElement struct {
	Span     SectionSpan
	Tag      string
	Attrs    []SSRPlanAttr
	Children []SSRPlanNode
	EventKey string // compiler-owned data-vela-event key, empty without DOM events
}

type SSRPlanComponent struct {
	Span  SectionSpan
	Name  string
	Attrs []SSRPlanAttr
	Slots map[string][]SSRPlanNode
}

type SSRPlanSlot struct {
	Span     SectionSpan
	Name     string
	Fallback []SSRPlanNode
}

type SSRPlanText struct {
	Span  SectionSpan
	Parts []SSRPlanTextPart
}

type SSRPlanTextPart struct {
	Span   SectionSpan
	Static bool
	Value  string
	Expr   *TemplateExpr // cached expression; nil for Static parts
}

type SSRPlanIf struct {
	Span     SectionSpan
	Key      *TemplateExpr // non-nil for a {#key} block
	Branches []SSRPlanIfBranch
}

type SSRPlanIfBranch struct {
	Span      SectionSpan
	Condition *TemplateExpr // nil is the final else branch
	Children  []SSRPlanNode
}

type SSRPlanEach struct {
	Span         SectionSpan
	Iterable     *TemplateExpr
	ItemName     string
	IndexName    string
	Key          *TemplateExpr
	Children     []SSRPlanNode
	ElseChildren []SSRPlanNode
}

type SSRPlanAwait struct {
	Span            SectionSpan
	Promise         *TemplateExpr
	ValueName       string
	ErrorName       string
	PendingChildren []SSRPlanNode
	ThenChildren    []SSRPlanNode
	CatchChildren   []SSRPlanNode
}

func (*SSRPlanElement) ssrPlanNode()   {}
func (*SSRPlanComponent) ssrPlanNode() {}
func (*SSRPlanSlot) ssrPlanNode()      {}
func (*SSRPlanText) ssrPlanNode()      {}
func (*SSRPlanIf) ssrPlanNode()        {}
func (*SSRPlanEach) ssrPlanNode()      {}
func (*SSRPlanAwait) ssrPlanNode()     {}

// SSRPlanAttrRole distinguishes semantic template attributes without having
// to preserve their sigil as a printable source fragment.
type SSRPlanAttrRole uint8

const (
	SSRAttrStatic SSRPlanAttrRole = iota
	SSRAttrDynamic
	SSRAttrTwoWay
	SSRAttrBindThis
	SSRAttrClassDirective
	SSRAttrStyleDirective
	SSRAttrEvent
)

type SSRPlanAttr struct {
	Span      SectionSpan
	Name      string
	Value     string
	ValueKind AttrValueKind
	Role      SSRPlanAttrRole
	Expr      *TemplateExpr // cached; required by every non-static dynamic role
}

// SSRStaticEvent describes an event-bearing DOM element. Phase 1 will render
// EventKey as data-vela-event and the client bridge will attach native events
// by this deterministic key. Component events remain component-plan metadata;
// only DOM element events appear here.
type SSRStaticEvent struct {
	EventKey string
	Event    string
	Handler  *TemplateExpr
	Span     SectionSpan
}

type SSRDynamicKind uint8

const (
	SSRDynamicText SSRDynamicKind = iota
	SSRDynamicAttribute
	SSRDynamicIf
	SSRDynamicEach
	SSRDynamicEachKey
	SSRDynamicKey
	SSRDynamicAwait
)

// SSRDynamicSite records stable dynamic order for the future paired hydration
// markers. Phase 1 does not emit markers yet, but assigning the order here
// prevents the Go and client paths from inventing incompatible traversals.
type SSRDynamicSite struct {
	ID   int
	Kind SSRDynamicKind
	Expr *TemplateExpr
	Span SectionSpan
}

type ssrPlanBuilder struct {
	plan        *SSRRenderPlan
	eventNext   int
	dynamicNext int
}

// BuildSSRRenderPlan converts only the validated template AST into a typed
// plan. It never parses source text and never emits Go or HTML strings.
func BuildSSRRenderPlan(componentName string, sfc *SFC, scopeID string) (*SSRRenderPlan, error) {
	return BuildSSRRenderPlanWithAnalysis(componentName, sfc, nil, scopeID)
}

// BuildSSRRenderPlanWithAnalysis accepts the compiler-owned analysis when it
// is already available in the pipeline. The nil compatibility path analyses
// the existing ScriptAST; it never reparses script source.
func BuildSSRRenderPlanWithAnalysis(componentName string, sfc *SFC, analysis *Analysis, scopeID string) (*SSRRenderPlan, error) {
	if sfc == nil || sfc.TemplateAST == nil {
		return nil, fmt.Errorf("ssr render plan: missing template AST")
	}
	if issues := ValidateTemplateAST(sfc.TemplateAST); len(issues) > 0 {
		return nil, fmt.Errorf("ssr render plan: template AST is invalid: %s", issues[0].Message)
	}
	componentName = strings.TrimSpace(componentName)
	if componentName == "" {
		return nil, fmt.Errorf("ssr render plan: missing component name")
	}
	if scopeID == "" {
		scopeID = "vela-" + componentName
	}
	if analysis == nil {
		analysis = analyzeScriptAST(sfc.ScriptAST)
	}
	props, err := buildSSRPlanProps(analysis)
	if err != nil {
		return nil, err
	}
	b := &ssrPlanBuilder{plan: &SSRRenderPlan{
		ComponentName: componentName,
		ScopeID:       scopeID,
		Props:         props,
		ReactiveNames: analysis.reactiveNames(),
		ScopedCSS:     ExtractScopedCSS(sfc, scopeID),
	}}
	b.plan.Nodes = b.nodes(sfc.TemplateAST.Nodes)
	return b.plan, nil
}

func (b *ssrPlanBuilder) nextEventKey() string {
	key := b.plan.ScopeID + "-e" + itoa(b.eventNext)
	b.eventNext++
	return key
}

func (b *ssrPlanBuilder) dynamic(kind SSRDynamicKind, expr *TemplateExpr, span SectionSpan) {
	b.plan.Dynamics = append(b.plan.Dynamics, SSRDynamicSite{
		ID: b.dynamicNext, Kind: kind, Expr: expr, Span: span,
	})
	b.dynamicNext++
}

func (b *ssrPlanBuilder) nodes(nodes []Node) []SSRPlanNode {
	out := make([]SSRPlanNode, 0, len(nodes))
	for _, node := range nodes {
		switch n := node.(type) {
		case *ElementNode:
			out = append(out, b.element(n))
		case *ComponentNode:
			out = append(out, b.component(n))
		case *SlotNode:
			out = append(out, &SSRPlanSlot{Span: n.Span, Name: n.Name, Fallback: b.nodes(n.Fallback)})
		case *TextNode:
			out = append(out, b.text(n))
		case *IfChainNode:
			branches := make([]SSRPlanIfBranch, 0, len(n.Branches))
			if n.HasKey() {
				b.dynamic(SSRDynamicKey, n.KeyAST, n.Span)
			}
			for _, branch := range n.Branches {
				if branch == nil {
					continue
				}
				if condition := branch.Expr; condition != nil {
					b.dynamic(SSRDynamicIf, condition, branch.Span)
				}
				branches = append(branches, SSRPlanIfBranch{
					Span: branch.Span, Condition: branch.Expr, Children: b.nodes(branch.Children),
				})
			}
			out = append(out, &SSRPlanIf{Span: n.Span, Key: n.KeyAST, Branches: branches})
		case *EachBlockNode:
			b.dynamic(SSRDynamicEach, n.IterExpr, n.Span)
			if n.HasKey() {
				b.dynamic(SSRDynamicEachKey, n.KeyAST, n.Span)
			}
			out = append(out, &SSRPlanEach{
				Span: n.Span, Iterable: n.IterExpr, ItemName: n.ItemName, IndexName: n.IndexName,
				Key: n.KeyAST, Children: b.nodes(n.Children), ElseChildren: b.nodes(n.ElseChildren),
			})
		case *AwaitBlockNode:
			b.dynamic(SSRDynamicAwait, n.Expr, n.Span)
			out = append(out, &SSRPlanAwait{
				Span: n.Span, Promise: n.Expr, ValueName: n.ValueName, ErrorName: n.ErrorName,
				PendingChildren: b.nodes(n.PendingChildren), ThenChildren: b.nodes(n.ThenChildren), CatchChildren: b.nodes(n.CatchChildren),
			})
		}
	}
	return out
}

func (b *ssrPlanBuilder) element(node *ElementNode) *SSRPlanElement {
	attrs, events := b.attrs(node.Attrs, true)
	el := &SSRPlanElement{Span: node.Span, Tag: node.Tag, Attrs: attrs, Children: b.nodes(node.Children)}
	if len(events) > 0 {
		el.EventKey = b.nextEventKey()
		for i := range events {
			events[i].EventKey = el.EventKey
			b.plan.Events = append(b.plan.Events, events[i])
		}
	}
	return el
}

func (b *ssrPlanBuilder) component(node *ComponentNode) *SSRPlanComponent {
	attrs, _ := b.attrs(node.Attrs, false)
	slots := make(map[string][]SSRPlanNode, len(node.Slots))
	for name, children := range node.Slots {
		slots[name] = b.nodes(children)
	}
	return &SSRPlanComponent{Span: node.Span, Name: node.Name, Attrs: attrs, Slots: slots}
}

func (b *ssrPlanBuilder) text(node *TextNode) *SSRPlanText {
	parts := make([]SSRPlanTextPart, 0, len(node.Parts))
	for _, part := range node.Parts {
		if part == nil {
			continue
		}
		parts = append(parts, SSRPlanTextPart{
			Span: part.Span, Static: part.Static, Value: part.Value, Expr: part.Expr,
		})
		if !part.Static {
			b.dynamic(SSRDynamicText, part.Expr, part.Span)
		}
	}
	return &SSRPlanText{Span: node.Span, Parts: parts}
}

func (b *ssrPlanBuilder) attrs(attrs []*Attr, domElement bool) ([]SSRPlanAttr, []SSRStaticEvent) {
	out := make([]SSRPlanAttr, 0, len(attrs))
	var events []SSRStaticEvent
	for _, attr := range attrs {
		if attr == nil {
			continue
		}
		role := SSRAttrStatic
		switch {
		case attr.IsEvent:
			role = SSRAttrEvent
		case attr.IsTwoWay:
			role = SSRAttrTwoWay
		case attr.IsBindThis:
			role = SSRAttrBindThis
		case attr.IsClassDir:
			role = SSRAttrClassDirective
		case attr.IsStyleDir:
			role = SSRAttrStyleDirective
		case attr.IsBind:
			role = SSRAttrDynamic
		}
		out = append(out, SSRPlanAttr{
			Span: attr.Span, Name: attr.Name, Value: attr.Value, ValueKind: attr.ValueKind,
			Role: role, Expr: attr.Expr,
		})
		if attr.IsEvent {
			if domElement {
				events = append(events, SSRStaticEvent{Event: attr.Name, Handler: attr.Expr, Span: attr.Span})
			}
			continue
		}
		if attr.Expr != nil {
			b.dynamic(SSRDynamicAttribute, attr.Expr, attr.Span)
		}
	}
	return out, events
}

func buildSSRPlanProps(analysis *Analysis) ([]SSRPlanProp, error) {
	if analysis == nil {
		return nil, nil
	}
	out := make([]SSRPlanProp, 0, len(analysis.Props))
	seenGoNames := map[string]string{}
	for _, prop := range analysis.Props {
		if prop == nil {
			continue
		}
		goName := ssrGoExportName(prop.Name)
		if previous, exists := seenGoNames[goName]; exists {
			return nil, fmt.Errorf("ssr render plan: props %q and %q both map to Go field %q", previous, prop.Name, goName)
		}
		seenGoNames[goName] = prop.Name
		out = append(out, SSRPlanProp{
			SourceName: prop.Name,
			GoName:     goName,
			Type:       inferSSRValueType(prop.DefaultAST),
			Default:    prop.DefaultAST,
			Bindable:   prop.Bindable,
		})
	}
	return out, nil
}

func ssrGoExportName(name string) string {
	for i, r := range name {
		return string(unicode.ToUpper(r)) + name[i+len(string(r)):]
	}
	return ""
}

func inferSSRValueType(expr JsExpr) SSRValueType {
	switch node := expr.(type) {
	case *JsLiteral:
		switch node.Kind {
		case "string":
			return SSRTypeString
		case "boolean":
			return SSRTypeBool
		case "number":
			if strings.ContainsAny(node.Raw, ".eE") {
				return SSRTypeFloat
			}
			return SSRTypeInt
		}
	case *JsArrayExpr:
		return SSRTypeSlice
	case *JsObjectExpr:
		return SSRTypeObject
	}
	return SSRTypeAny
}
