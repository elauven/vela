package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
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
	SlotNames     []string
	// ReactiveNames is analysis metadata for diagnostics and bridge safety.
	// It is not emitted into server code.
	ReactiveNames []string
	Nodes         []SSRPlanNode
	Events        []SSRStaticEvent
	Dynamics      []SSRDynamicSite
	Hydration     SSRHydrationState
	ScopedCSS     string
}

// SSRHydrationState is the inert, serializable portion of component state.
// Props are serialized by the generated renderer because their values arrive
// at render time; JSON holds only compiler-known $state initial values. When
// an initializer is not a JSON literal tree, it is retained in Omitted so a
// future hydrator can reject incomplete restoration explicitly.
type SSRHydrationState struct {
	JSON       string
	Incomplete bool
	Omitted    []string
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
	Link  *SSRComponentLink
	Attrs []SSRPlanAttr
	Slots map[string][]SSRPlanNode
}

// SSRComponentLink is project-level import metadata retained by the typed
// render plan. LocalName is the template-visible import alias; SourcePath is
// canonical and prevents a later Go backend from guessing child identity from
// a tag name alone.
type SSRComponentLink struct {
	LocalName   string
	SourcePath  string
	PackageName string
	ImportPath  string
	GoName      string
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
	Span      SectionSpan
	Static    bool
	Value     string
	Expr      *TemplateExpr // cached expression; nil for Static parts
	DynamicID int           // -1 for static text; paired server marker otherwise
}

type SSRPlanIf struct {
	Span      SectionSpan
	Key       *TemplateExpr // non-nil for a {#key} block
	Branches  []SSRPlanIfBranch
	DynamicID int // paired marker around the rendered branch range
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
	DynamicID    int // paired marker around the rendered list range
}

type SSRPlanAwait struct {
	Span            SectionSpan
	Promise         *TemplateExpr
	ValueName       string
	ErrorName       string
	PendingChildren []SSRPlanNode
	ThenChildren    []SSRPlanNode
	CatchChildren   []SSRPlanNode
	DynamicID       int // paired marker around the rendered branch range
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
	links       map[string]SSRComponentLink
	eventNext   int
	dynamicNext int
	slotNames   map[string]bool
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
	return BuildSSRRenderPlanWithAnalysisAndLinks(componentName, sfc, analysis, scopeID, nil)
}

func BuildSSRRenderPlanWithAnalysisAndLinks(componentName string, sfc *SFC, analysis *Analysis, scopeID string, links map[string]SSRComponentLink) (*SSRRenderPlan, error) {
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
	props, err := buildSSRPlanProps(analysis, sfc.ScriptAST)
	if err != nil {
		return nil, err
	}
	b := &ssrPlanBuilder{links: links, slotNames: map[string]bool{}, plan: &SSRRenderPlan{
		ComponentName: componentName,
		ScopeID:       scopeID,
		Props:         props,
		ReactiveNames: analysis.reactiveNames(),
		ScopedCSS:     ExtractScopedCSS(sfc, scopeID),
	}}
	b.plan.Hydration = buildSSRHydrationState(analysis)
	b.plan.Nodes = b.nodes(sfc.TemplateAST.Nodes)
	for name := range b.slotNames {
		b.plan.SlotNames = append(b.plan.SlotNames, name)
	}
	sort.Strings(b.plan.SlotNames)
	return b.plan, nil
}

func buildSSRHydrationState(analysis *Analysis) SSRHydrationState {
	values := map[string]any{}
	state := SSRHydrationState{}
	if analysis != nil {
		for _, variable := range analysis.StateVars {
			if variable == nil {
				continue
			}
			value, ok := ssrJSONValue(variable.InitialAST)
			if !ok {
				state.Incomplete = true
				state.Omitted = append(state.Omitted, variable.Name)
				continue
			}
			values[variable.Name] = value
		}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		// ssrJSONValue produces only JSON-compatible Go values. Preserve a
		// valid empty payload if that invariant is ever broken rather than
		// allowing renderer construction to emit malformed JSON.
		state.Incomplete = true
		state.JSON = "{}"
		return state
	}
	state.JSON = string(encoded)
	return state
}

// ssrJSONValue converts only cached JavaScript literal trees. It intentionally
// does not evaluate identifiers, calls, members, or expressions: evaluating
// user JavaScript inside the compiler would make server and client state
// diverge before hydration starts.
func ssrJSONValue(expr JsExpr) (any, bool) {
	switch node := expr.(type) {
	case *JsLiteral:
		switch node.Kind {
		case "string":
			value, err := decodeSSRJSONString(node)
			return value, err == nil
		case "number":
			decoder := json.NewDecoder(bytes.NewBufferString(node.Raw))
			decoder.UseNumber()
			var value any
			if err := decoder.Decode(&value); err != nil {
				return nil, false
			}
			if _, ok := value.(json.Number); !ok {
				return nil, false
			}
			return value, true
		case "boolean":
			value, err := strconv.ParseBool(node.Raw)
			return value, err == nil
		case "null":
			return nil, true
		}
	case *JsArrayExpr:
		values := make([]any, len(node.Elems))
		for index, element := range node.Elems {
			if element == nil {
				continue // JavaScript array holes stringify to JSON null.
			}
			value, ok := ssrJSONValue(element)
			if !ok {
				return nil, false
			}
			values[index] = value
		}
		return values, true
	case *JsObjectExpr:
		values := map[string]any{}
		for _, property := range node.Props {
			if property == nil || property.Computed || property.Spread || property.Method || property.Kind != "init" {
				return nil, false
			}
			key, ok := ssrJSONPropertyName(property.Key)
			if !ok {
				return nil, false
			}
			value, ok := ssrJSONValue(property.Value)
			if !ok {
				return nil, false
			}
			values[key] = value
		}
		return values, true
	}
	return nil, false
}

func ssrJSONPropertyName(key JsExpr) (string, bool) {
	switch value := key.(type) {
	case *JsIdent:
		return value.Name, true
	case *JsLiteral:
		if value.Kind == "string" {
			decoded, err := decodeSSRJSONString(value)
			return decoded, err == nil
		}
		if value.Kind == "number" {
			return value.Raw, true
		}
	}
	return "", false
}

func decodeSSRJSONString(literal *JsLiteral) (string, error) {
	if literal == nil {
		return "", fmt.Errorf("missing string literal")
	}
	if literal.Decoded {
		return literal.Value, nil
	}
	raw := literal.Raw
	if len(raw) >= 2 && raw[0] == '\'' && raw[len(raw)-1] == '\'' {
		// Go's strconv understands the overlapping escape repertoire once a
		// JavaScript single-quoted string is converted to double quotes. The
		// only escape that is invalid in a Go double-quoted literal is \' .
		inner := strings.ReplaceAll(raw[1:len(raw)-1], `\'`, `'`)
		raw = `"` + inner + `"`
	}
	return strconv.Unquote(raw)
}

func (b *ssrPlanBuilder) nextEventKey() string {
	key := b.plan.ScopeID + "-e" + itoa(b.eventNext)
	b.eventNext++
	return key
}

func (b *ssrPlanBuilder) dynamic(kind SSRDynamicKind, expr *TemplateExpr, span SectionSpan) int {
	id := b.dynamicNext
	b.plan.Dynamics = append(b.plan.Dynamics, SSRDynamicSite{
		ID: id, Kind: kind, Expr: expr, Span: span,
	})
	b.dynamicNext++
	return id
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
			b.slotNames[n.Name] = true
			out = append(out, &SSRPlanSlot{Span: n.Span, Name: n.Name, Fallback: b.nodes(n.Fallback)})
		case *TextNode:
			out = append(out, b.text(n))
		case *IfChainNode:
			branches := make([]SSRPlanIfBranch, 0, len(n.Branches))
			markerID := -1
			if n.HasKey() {
				markerID = b.dynamic(SSRDynamicKey, n.KeyAST, n.Span)
			}
			for _, branch := range n.Branches {
				if branch == nil {
					continue
				}
				if condition := branch.Expr; condition != nil {
					id := b.dynamic(SSRDynamicIf, condition, branch.Span)
					if markerID < 0 {
						markerID = id
					}
				}
				branches = append(branches, SSRPlanIfBranch{
					Span: branch.Span, Condition: branch.Expr, Children: b.nodes(branch.Children),
				})
			}
			out = append(out, &SSRPlanIf{Span: n.Span, Key: n.KeyAST, Branches: branches, DynamicID: markerID})
		case *EachBlockNode:
			markerID := b.dynamic(SSRDynamicEach, n.IterExpr, n.Span)
			if n.HasKey() {
				b.dynamic(SSRDynamicEachKey, n.KeyAST, n.Span)
			}
			out = append(out, &SSRPlanEach{
				Span: n.Span, Iterable: n.IterExpr, ItemName: n.ItemName, IndexName: n.IndexName,
				Key: n.KeyAST, Children: b.nodes(n.Children), ElseChildren: b.nodes(n.ElseChildren), DynamicID: markerID,
			})
		case *AwaitBlockNode:
			markerID := b.dynamic(SSRDynamicAwait, n.Expr, n.Span)
			out = append(out, &SSRPlanAwait{
				Span: n.Span, Promise: n.Expr, ValueName: n.ValueName, ErrorName: n.ErrorName,
				PendingChildren: b.nodes(n.PendingChildren), ThenChildren: b.nodes(n.ThenChildren), CatchChildren: b.nodes(n.CatchChildren), DynamicID: markerID,
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
	component := &SSRPlanComponent{Span: node.Span, Name: node.Name, Attrs: attrs, Slots: slots}
	if link, ok := b.links[node.Name]; ok {
		copy := link
		component.Link = &copy
	}
	return component
}

// ResolveSSRLocalComponentLinks resolves only relative .vela imports. It does
// not infer package names or Go import paths yet; those are a later project
// linking concern. Returning canonical source paths here keeps both later
// stages independent of import spelling and aliases.
func ResolveSSRLocalComponentLinks(src, srcRoot string, analysis *Analysis) (map[string]SSRComponentLink, error) {
	links := map[string]SSRComponentLink{}
	if analysis == nil {
		return links, nil
	}
	root, err := filepath.Abs(srcRoot)
	if err != nil {
		return nil, err
	}
	for _, imp := range analysis.Imports {
		if imp == nil || !strings.HasSuffix(imp.Source, ".vela") || (!strings.HasPrefix(imp.Source, "./") && !strings.HasPrefix(imp.Source, "../")) {
			continue
		}
		resolved, err := filepath.Abs(filepath.Join(filepath.Dir(src), imp.Source))
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(root, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("ssr component import %q escapes project source root", imp.Source)
		}
		for _, local := range imp.Names {
			links[local] = SSRComponentLink{LocalName: local, SourcePath: resolved}
		}
	}
	return links, nil
}

// LinkSSRComponentPackages enriches canonical source links with the Go
// package identity selected by the project layout. It keeps all filesystem and
// module-path policy out of template parsing and Go-AST emission.
func LinkSSRComponentPackages(links map[string]SSRComponentLink, srcRoot string, layout SSRProjectLayout) map[string]SSRComponentLink {
	for name, link := range links {
		link.PackageName = layout.packageName(link.SourcePath, srcRoot)
		link.ImportPath = layout.importPath(link.SourcePath, srcRoot)
		link.GoName = componentName(link.SourcePath)
		links[name] = link
	}
	return links
}

func (b *ssrPlanBuilder) text(node *TextNode) *SSRPlanText {
	parts := make([]SSRPlanTextPart, 0, len(node.Parts))
	for _, part := range node.Parts {
		if part == nil {
			continue
		}
		planned := SSRPlanTextPart{Span: part.Span, Static: part.Static, Value: part.Value, Expr: part.Expr, DynamicID: -1}
		if !part.Static {
			planned.DynamicID = b.dynamic(SSRDynamicText, part.Expr, part.Span)
		}
		parts = append(parts, planned)
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

func buildSSRPlanProps(analysis *Analysis, script *ScriptAST) ([]SSRPlanProp, error) {
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
			Type:       inferSSRPropType(script, prop.Name, prop.DefaultAST),
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

func inferSSRPropType(script *ScriptAST, name string, fallback JsExpr) SSRValueType {
	if script != nil {
		for _, comment := range script.DocComments {
			if comment.TargetName != name {
				continue
			}
			switch comment.Type {
			case JSDocTypeString:
				return SSRTypeString
			case JSDocTypeBool:
				return SSRTypeBool
			case JSDocTypeNumber:
				return SSRTypeFloat
			case JSDocTypeInteger:
				return SSRTypeInt
			case JSDocTypeSlice:
				return SSRTypeSlice
			case JSDocTypeObject:
				return SSRTypeObject
			case JSDocTypeAny:
				return SSRTypeAny
			}
		}
	}
	return inferSSRValueType(fallback)
}
