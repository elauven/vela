package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	// ElementScope is the scope attribute of elements: ScopeID when the
	// component has styles, "" otherwise (as the client, task 059).
	ElementScope string
	Props        []SSRPlanProp
	// PropsWithoutPresence is set when a preserved props sidecar has no
	// Set map[string]bool field: defaults then go through vela.PropProvided
	// (reflection) instead of reading props.Set directly (task 060).
	PropsWithoutPresence bool
	SlotNames            []string
	// ReactiveNames is analysis metadata for diagnostics and bridge safety.
	// It is not emitted into server code.
	ReactiveNames []string
	// StateInitializers retains immutable, parser-owned $state initializer ASTs
	// for the server-safe local-state subset. It is never printable source and
	// lets the expression translator decide whether an initializer is actually
	// representable in Go.
	StateInitializers map[string]JsExpr
	// DerivedInitializers retains immutable, parser-owned $derived expression
	// ASTs. The SSR expression translator resolves them only through the same
	// admitted pure subset used for plain $state initializers.
	DerivedInitializers map[string]JsExpr
	// PlainInitializers retains top-level const/unchanged-let initializers from
	// the component-owned script AST. They are admitted only through the same
	// pure expression translator as reactive initializers.
	PlainInitializers map[string]JsExpr
	// Functions retains parser-owned top-level named declarations. The first
	// SSR function subset accepts only a synchronous, non-generator function
	// whose body is one return expression; the expression translator enforces
	// that contract at each call site.
	Functions map[string]*JsFuncDecl
	Nodes     []SSRPlanNode
	Dynamics  []SSRDynamicSite
	ScopedCSS string
	// Hosts binds callee names of this component to //vela:host functions,
	// and HostSymbols names the bindable JavaScript function behind every
	// local or imported function name (task 030). HostProject locates the
	// host tree for diagnostics.
	Hosts       map[string]*SSRHostBinding
	HostSymbols map[string]SSRHostSymbol
	HostDecls   map[string]*JsFuncDecl
	HostProject *SSRProject
	// ModuleRefs maps names imported from local .js modules to their
	// translated exports (ssr_module.go).
	ModuleRefs map[string]ssrModuleRef
	// RootStatic marks a component that renders plain HTML as a root (A-8.1).
	RootStatic bool
	// DeepInitializers holds $state.deep initializers: their initial value is
	// an ordinary server value (T-6.6d). They are kept apart from
	// StateInitializers.
	DeepInitializers map[string]JsExpr
	// StateNames lists every $state and $state.deep binding, and LocalOrder
	// every $state, $state.deep, $derived, and plain binding, in script
	// declaration order (T-6.6d).
	StateNames []string
	LocalOrder []string
	// PayloadState lists the StateNames whose initial values travel in the
	// payload: the non-reproducible ones (task 059, S-6.4). Empty: the
	// component writes no entry.
	PayloadState []string
	// TemplateOffset and ScriptOffset place template expression and script
	// node positions in the .vela source for the region report (T-3.2).
	TemplateOffset int
	ScriptOffset   int
}

// SSRHydrationState is the inert, serializable portion of component state.
// Props are serialized by the generated renderer because their values arrive
// at render time; JSON holds only compiler-known $state initial values. When
// an initializer is not a JSON literal tree, it is retained in Omitted so a
// future hydrator can reject incomplete restoration explicitly.
type SSRHydrationState struct {
	JSON       string
	Deep       []string
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
	// ClientOnly is set when the child declares <script context="client">
	// (contract §9.2): it has no Go renderer.
	ClientOnly bool
}

// SSRPlanClient is a {#client} block or a client-only component (contract
// §9): the server renders only the fallback, in a range with the state
// token "f"; the main branch is not region code (T-9.1c, T-9.2c).
type SSRPlanClient struct {
	Span     SectionSpan
	Fallback []SSRPlanNode
}

func (*SSRPlanClient) ssrPlanNode() {}

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
		analysis = AnalyzeScriptAST(sfc.ScriptAST)
	}
	props, err := buildSSRPlanProps(analysis, sfc.ScriptAST)
	if err != nil {
		return nil, err
	}
	b := &ssrPlanBuilder{links: links, slotNames: map[string]bool{}, plan: &SSRRenderPlan{
		ComponentName:       componentName,
		ScopeID:             scopeID,
		Props:               props,
		ReactiveNames:       analysis.ReactiveNames(),
		StateInitializers:   ssrStateInitializers(analysis),
		DerivedInitializers: ssrDerivedInitializers(analysis),
		PlainInitializers:   ssrPlainInitializers(analysis),
		Functions:           ssrNamedFunctions(analysis),
		ScopedCSS:           ExtractScopedCSS(sfc, scopeID),
		ElementScope:        ElementScopeFor(sfc, scopeID),
		DeepInitializers:    ssrDeepInitializers(analysis),
		TemplateOffset:      sfc.TemplateOffset,
		ScriptOffset:        sfc.ScriptOffset,
	}}
	b.plan.StateNames, b.plan.LocalOrder = ssrDeclarationOrder(b.plan, analysis)
	if analysis != nil {
		serialized := map[string]bool{}
		for _, sv := range analysis.SerializedState() {
			serialized[sv.Name] = true
		}
		for _, name := range b.plan.StateNames {
			if serialized[name] {
				b.plan.PayloadState = append(b.plan.PayloadState, name)
			}
		}
	}
	b.plan.Nodes = b.nodes(sfc.TemplateAST.Nodes)
	for name := range b.slotNames {
		b.plan.SlotNames = append(b.plan.SlotNames, name)
	}
	sort.Strings(b.plan.SlotNames)
	return b.plan, nil
}

func ssrStateInitializers(analysis *Analysis) map[string]JsExpr {
	initializers := map[string]JsExpr{}
	if analysis == nil {
		return initializers
	}
	for _, state := range analysis.StateVars {
		// $state.deep owns proxy/mutation semantics which have no equivalent
		// server value model yet. Only plain $state initializers may cross this
		// expression boundary; deep bindings retain the explicit hydration
		// diagnostic even when their literal happens to be Go-representable.
		if state != nil && !state.Deep && state.Name != "" && state.InitialAST != nil {
			initializers[state.Name] = state.InitialAST
		}
	}
	return initializers
}

func ssrDerivedInitializers(analysis *Analysis) map[string]JsExpr {
	initializers := map[string]JsExpr{}
	if analysis == nil {
		return initializers
	}
	for _, derived := range analysis.DerivedVars {
		if derived != nil && derived.Name != "" && derived.ExprAST != nil {
			initializers[derived.Name] = derived.ExprAST
		}
	}
	return initializers
}

func ssrPlainInitializers(analysis *Analysis) map[string]JsExpr {
	initializers := map[string]JsExpr{}
	if analysis == nil || analysis.Script == nil {
		return initializers
	}
	reactive := map[string]bool{}
	for _, name := range analysis.ReactiveNames() {
		reactive[name] = true
	}
	if analysis.Script.Program == nil {
		return initializers
	}
	written := FindWrittenVars(analysis.Script.Program)
	for _, stmt := range analysis.Script.Program.Body {
		decl, ok := stmt.(*JsVarDecl)
		if !ok || (decl.Kind != "const" && decl.Kind != "let") {
			continue
		}
		for _, declarator := range decl.Decls {
			name := IdentName(declarator.Id)
			if name == "" || declarator.Init == nil || reactive[name] || written[name] {
				continue
			}
			initializers[name] = declarator.Init
		}
	}
	return initializers
}

func ssrNamedFunctions(analysis *Analysis) map[string]*JsFuncDecl {
	functions := map[string]*JsFuncDecl{}
	if analysis == nil || analysis.Script == nil {
		return functions
	}
	if analysis.Script.Program == nil {
		return functions
	}
	for _, stmt := range analysis.Script.Program.Body {
		if function, ok := stmt.(*JsFuncDecl); ok && function.Name != "" {
			functions[function.Name] = function
		}
	}
	return functions
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
			if link, ok := b.links[n.Name]; ok && link.ClientOnly {
				// Lowered as {#client}<X />{/client} with an empty fallback
				// (T-9.2b); its props and slots are not region code.
				out = append(out, &SSRPlanClient{Span: n.Span})
				continue
			}
			out = append(out, b.component(n))
		case *SlotNode:
			b.slotNames[n.Name] = true
			out = append(out, &SSRPlanSlot{Span: n.Span, Name: n.Name, Fallback: b.nodes(n.Fallback)})
		case *TextNode:
			out = append(out, b.text(n))
		case *IfChainNode:
			if n.Client {
				client := &SSRPlanClient{Span: n.Span}
				if len(n.Branches) > 1 {
					client.Fallback = b.nodes(n.Branches[1].Children)
				}
				out = append(out, client)
				continue
			}
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
	return &SSRPlanElement{Span: node.Span, Tag: node.Tag, Attrs: b.attrs(node.Attrs), Children: b.nodes(node.Children)}
}

func (b *ssrPlanBuilder) component(node *ComponentNode) *SSRPlanComponent {
	attrs := b.attrs(node.Attrs)
	slots := make(map[string][]SSRPlanNode, len(node.Slots))
	names := make([]string, 0, len(node.Slots))
	for name := range node.Slots {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		slots[name] = b.nodes(node.Slots[name])
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
		if data, err := os.ReadFile(link.SourcePath); err == nil {
			link.ClientOnly = ScriptContextClient(string(data))
		}
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

func (b *ssrPlanBuilder) attrs(attrs []*Attr) []SSRPlanAttr {
	out := make([]SSRPlanAttr, 0, len(attrs))
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
	}
	return out
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
		if goName == "Set" {
			return nil, fmt.Errorf("ssr render plan: prop %q maps to reserved generated presence field %q", prop.Name, goName)
		}
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
	case *JsUnaryExpr:
		if node.Op == "+" || node.Op == "-" {
			kind := inferSSRValueType(node.X)
			if kind == SSRTypeInt || kind == SSRTypeFloat {
				return SSRTypeFloat
			}
		}
	case *JsBinaryExpr:
		left := inferSSRValueType(node.L)
		right := inferSSRValueType(node.R)
		if node.Op == "+" && (left == SSRTypeString || right == SSRTypeString) {
			return SSRTypeString
		}
		if left == SSRTypeInt || left == SSRTypeFloat {
			if right == SSRTypeInt || right == SSRTypeFloat {
				switch node.Op {
				case "+", "-", "*", "/", "%":
					return SSRTypeFloat
				}
			}
		}
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

func ssrDeepInitializers(analysis *Analysis) map[string]JsExpr {
	initializers := map[string]JsExpr{}
	if analysis == nil {
		return initializers
	}
	for _, state := range analysis.StateVars {
		if state != nil && state.Deep && state.Name != "" && state.InitialAST != nil {
			initializers[state.Name] = state.InitialAST
		}
	}
	return initializers
}

// ssrDeclarationOrder orders the local bindings by the script position of
// their initializers, which is their declaration order.
func ssrDeclarationOrder(plan *SSRRenderPlan, analysis *Analysis) (states, locals []string) {
	type declaration struct {
		name string
		pos  int
	}
	var declarations []declaration
	seen := map[string]bool{}
	for _, initializers := range []map[string]JsExpr{plan.StateInitializers, plan.DeepInitializers, plan.DerivedInitializers, plan.PlainInitializers} {
		for name, initializer := range initializers {
			if !seen[name] {
				seen[name] = true
				declarations = append(declarations, declaration{name, initializer.NodePos()})
			}
		}
	}
	sort.Slice(declarations, func(i, j int) bool {
		if declarations[i].pos != declarations[j].pos {
			return declarations[i].pos < declarations[j].pos
		}
		return declarations[i].name < declarations[j].name
	})
	for _, d := range declarations {
		locals = append(locals, d.name)
	}
	if analysis != nil {
		for _, state := range analysis.StateVars {
			if state != nil && state.Name != "" {
				states = append(states, state.Name)
			}
		}
	}
	return states, locals
}
