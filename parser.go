package main

import (
	"fmt"
	"strings"
	"unicode"
)

// ──────────────────────────────────────────────
//  Single-File Component
// ──────────────────────────────────────────────

// ComponentAST owns the immutable component source and the typed AST of each
// section. Section node positions remain relative to their section source;
// the section Span translates them to component-relative positions.
type ComponentAST struct {
	Source   string
	Script   *ScriptAST
	Template *TemplateAST
	Style    *CssStylesheet
}

type SFC struct {
	Script   string
	Style    string
	Template string

	AST         *ComponentAST
	TemplateAST *TemplateAST
	StyleAST    *CssStylesheet

	// ScriptAST owns the one parsed representation of the <script> section.
	// Script remains temporarily for source extraction and user-facing tooling;
	// compiler passes must use ScriptAST.Program rather than reparsing it.
	ScriptAST *ScriptAST

	// Byte offsets of each section's body in the original source file.
	// Used by diagnostics to translate parser-local positions into
	// file-relative line/column for accurate error reporting.  A value
	// of 0 means the section wasn't found (and the corresponding
	// content string is empty).
	ScriptOffset   int
	StyleOffset    int
	TemplateOffset int
}

// SectionSpan is an immutable half-open byte range in the full .vela source.
type SectionSpan struct {
	Start int
	End   int
}

// ScriptAST is the script portion of a ComponentAST. Program node positions
// remain relative to Source; Span translates those positions to the component
// source file. Diagnostics are structured parser failures collected during the
// same parse, before any analyzer transform can mutate the program.
type JSDocTypeKind uint8

const (
	JSDocTypeUnknown JSDocTypeKind = iota
	JSDocTypeString
	JSDocTypeBool
	JSDocTypeNumber
	JSDocTypeInteger
	JSDocTypeSlice
	JSDocTypeObject
	JSDocTypeAny
)

// JSDocComment is parser-owned documentation metadata for one export let
// declaration. It stores a normalized type rather than a raw comment string.
type JSDocComment struct {
	Start      int
	End        int
	TargetName string
	Type       JSDocTypeKind
}

type ScriptAST struct {
	Source      string
	Span        SectionSpan
	Program     *JsProgram
	Diagnostics []JSParseDiagnostic
	DocComments []JSDocComment
}

func newScriptAST(source string, offset int) *ScriptAST {
	program, diagnostics := ParseJSWithDiagnostics(source)
	return &ScriptAST{
		Source:      source,
		Span:        SectionSpan{Start: offset, End: offset + len(source)},
		Program:     program,
		Diagnostics: diagnostics,
		DocComments: scanJSDocComments(source),
	}
}

// parseSFC splits a .vela source file into its three sections by scanning for
// <script>, <style>, and <template> open/close tags without using regexp.
// The scanner is case-insensitive and tolerates arbitrary tag attributes.
//
// Each section's absolute byte offset is also recorded so positions
// within a section can be translated back to file-relative locations
// for diagnostics.  Importantly, the offset is the start of the body
// *before* whitespace trimming — we record where the inner text
// actually begins in the file, then compute how many leading
// whitespace bytes were trimmed and add that to the offset.  Without
// this, every diagnostic in a `<script>\n  let x = …` block would be
// off by the newline + indent.
func parseSFC(src string) (*SFC, error) {
	sfc := &SFC{}

	// extractBlock finds the first occurrence of <tag …>…</tag>
	// (case-insensitive), returns the trimmed inner text plus the
	// absolute byte offset of where that trimmed text starts in src.
	extractBlock := func(tag string) (string, int) {
		low := asciiLower(src)
		openTag := "<" + tag
		closeTag := "</" + tag + ">"

		start := strings.Index(low, openTag)
		if start < 0 {
			return "", 0
		}
		gt := strings.Index(src[start:], ">")
		if gt < 0 {
			return "", 0
		}
		bodyStart := start + gt + 1

		closeIdx := strings.Index(asciiLower(src[bodyStart:]), closeTag)
		if closeIdx < 0 {
			return "", 0
		}
		raw := src[bodyStart : bodyStart+closeIdx]
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return "", 0
		}
		// How many leading whitespace bytes were trimmed.
		leadingWS := strings.Index(raw, trimmed)
		if leadingWS < 0 {
			leadingWS = 0
		}
		return trimmed, bodyStart + leadingWS
	}

	sfc.Script, sfc.ScriptOffset = extractBlock("script")
	sfc.Style, sfc.StyleOffset = extractBlock("style")
	sfc.Template, sfc.TemplateOffset = extractBlock("template")

	if sfc.Template == "" {
		return nil, fmt.Errorf("missing <template> section")
	}
	sfc.ScriptAST = newScriptAST(sfc.Script, sfc.ScriptOffset)
	sfc.TemplateAST = newTemplateAST(sfc.Template, sfc.TemplateOffset)
	sfc.StyleAST = newCssStylesheet(sfc.Style, sfc.StyleOffset)
	sfc.AST = &ComponentAST{
		Source:   src,
		Script:   sfc.ScriptAST,
		Template: sfc.TemplateAST,
		Style:    sfc.StyleAST,
	}
	return sfc, nil
}

// asciiLower only folds ASCII bytes. SFC section tags are ASCII by grammar,
// and preserving byte length is essential because their indices address src.
func asciiLower(src string) string {
	buf := []byte(src)
	for i, c := range buf {
		if c >= 'A' && c <= 'Z' {
			buf[i] = c + ('a' - 'A')
		}
	}
	return string(buf)
}

// ──────────────────────────────────────────────
//  Template AST
// ──────────────────────────────────────────────

// TemplateAST is the typed template section of a ComponentAST.
// Source and Span are immutable ownership metadata; Nodes is the canonical
// parsed representation used by compiler stages.
type TemplateAST struct {
	Source      string
	Span        SectionSpan
	Nodes       []Node
	Diagnostics []TemplateParseDiagnostic
}

type TemplateParseDiagnostic struct {
	Pos     int
	Message string
}

func newTemplateAST(source string, offset int) *TemplateAST {
	nodes, err := parseTemplateAt(source, offset)
	ast := &TemplateAST{
		Source: source,
		Span:   SectionSpan{Start: offset, End: offset + len(source)},
		Nodes:  nodes,
	}
	if err != nil {
		ast.Diagnostics = append(ast.Diagnostics, TemplateParseDiagnostic{Pos: 0, Message: err.Error()})
	}
	return ast
}

// ValidateTemplateAST checks structural invariants before semantic analysis or
// target generation. Parser diagnostics cover malformed source syntax; this
// validator protects later phases from incomplete or manually-mutated AST
// shapes and from uncached/invalid executable template expressions.
func ValidateTemplateAST(ast *TemplateAST) []TemplateParseDiagnostic {
	var issues []TemplateParseDiagnostic
	add := func(pos int, format string, args ...interface{}) {
		issues = append(issues, TemplateParseDiagnostic{Pos: pos, Message: fmt.Sprintf(format, args...)})
	}
	if ast == nil {
		add(0, "missing template AST")
		return issues
	}
	if ast.Span.Start < 0 || ast.Span.End < ast.Span.Start || ast.Span.End-ast.Span.Start != len(ast.Source) {
		add(0, "template source span does not match its owned source")
	}
	checkSpan := func(span SectionSpan, label string) {
		if span.Start == 0 && span.End == 0 {
			return
		}
		if span.Start < ast.Span.Start || span.End < span.Start || span.End > ast.Span.End {
			add(0, "%s span escapes template source", label)
		}
	}
	validateExpr := func(expr *TemplateExpr, pos int, label string) {
		if expr == nil {
			add(pos, "%s is missing its cached expression AST", label)
			return
		}
		checkSpan(expr.Span, label+" expression")
		if expr.Program == nil {
			add(expr.Pos, "%s has no parsed expression program", label)
		} else if strings.TrimSpace(expr.Source) == "" {
			add(expr.Pos, "%s must not be empty", label)
		} else if expr.Root() == nil {
			add(expr.Pos, "%s must contain exactly one JavaScript expression", label)
		}
		for _, message := range expr.Errors {
			add(expr.Pos, "%s expression is invalid: %s", label, message)
		}
	}
	var walkNodes func([]Node)
	walkNodes = func(nodes []Node) {
		for _, node := range nodes {
			if node == nil {
				add(0, "template contains a nil node")
				continue
			}
			switch n := node.(type) {
			case *TextNode:
				checkSpan(n.Span, "text node")
				for _, part := range n.Parts {
					if part == nil {
						add(0, "text node contains a nil part")
					} else {
						checkSpan(part.Span, "text part")
						if !part.Static {
							validateExpr(part.Expr, part.Pos, "text interpolation")
						}
					}
				}
			case *ElementNode:
				checkSpan(n.Span, "element")
				if strings.TrimSpace(n.Tag) == "" {
					add(0, "element has an empty tag name")
				}
				for _, attr := range n.Attrs {
					if attr == nil {
						add(0, "element contains a nil attribute")
					} else {
						checkSpan(attr.Span, "attribute")
						if attr.Expr != nil {
							validateExpr(attr.Expr, attr.Pos, "attribute directive")
						}
					}
				}
				walkNodes(n.Children)
			case *ComponentNode:
				checkSpan(n.Span, "component")
				if strings.TrimSpace(n.Name) == "" {
					add(0, "component has an empty name")
				}
				for _, attr := range n.Attrs {
					if attr == nil {
						add(0, "component contains a nil attribute")
					} else {
						checkSpan(attr.Span, "component attribute")
						if attr.Expr != nil {
							validateExpr(attr.Expr, attr.Pos, "component attribute")
						}
					}
				}
				for _, children := range n.Slots {
					walkNodes(children)
				}
			case *SlotNode:
				checkSpan(n.Span, "slot")
				if strings.TrimSpace(n.Name) == "" {
					add(0, "slot has an empty name")
				}
				walkNodes(n.Fallback)
			case *EachBlockNode:
				checkSpan(n.Span, "each block")
				if strings.TrimSpace(n.ItemName) == "" {
					add(n.ExprPos, "#each block has an empty item binding")
				}
				validateExpr(n.IterExpr, n.ExprPos, "#each iterable")
				if n.HasKey() {
					validateExpr(n.KeyAST, n.KeyPos, "#each key")
				}
				walkNodes(n.Children)
				walkNodes(n.ElseChildren)
			case *AwaitBlockNode:
				checkSpan(n.Span, "await block")
				validateExpr(n.Expr, n.ExprPos, "#await promise")
				walkNodes(n.PendingChildren)
				walkNodes(n.ThenChildren)
				walkNodes(n.CatchChildren)
			case *IfChainNode:
				checkSpan(n.Span, "if/key block")
				if len(n.Branches) == 0 {
					add(0, "if/key block has no branches")
				}
				if n.HasKey() {
					validateExpr(n.KeyAST, n.KeyPos, "#key expression")
				}
				for _, branch := range n.Branches {
					if branch == nil {
						add(0, "if/key block contains a nil branch")
						continue
					}
					checkSpan(branch.Span, "if branch")
					if branch.HasCondition() && branch.SyntheticCond == nil {
						validateExpr(branch.Expr, branch.CondPos, "#if branch")
					}
					walkNodes(branch.Children)
				}
			default:
				add(0, "template contains unsupported node kind %q", node.nodeKind())
			}
		}
	}
	walkNodes(ast.Nodes)
	return issues
}

type Node interface{ nodeKind() string }

// ElementNode represents a DOM element.
type ElementNode struct {
	Span     SectionSpan
	Tag      string
	Attrs    []*Attr
	Children []Node
}

// IfChainNode represents a standard {#if} chain or a {#key} block.
type IfChainNode struct {
	Span     SectionSpan
	Branches []*IfBranch // [0]=if, [1..n-1]=elseif, last may have Cond==""(else)

	// KeyExpr, if non-empty, marks this chain as a {#key expr} block.
	// Modeled as a single-branch always-active chain whose cached
	// subtree is force-destroyed and rebuilt whenever expr changes
	// identity (===).  Useful for forcing re-init of stateful
	// children — animations, focus management, scroll position.
	KeyExpr string
	KeyPos  int
	KeyAST  *TemplateExpr
}

type IfBranch struct {
	Span          SectionSpan
	Cond          string // empty string → #else branch
	CondPos       int    // byte offset of condition in template
	Expr          *TemplateExpr
	SyntheticCond JsExpr // compiler-generated condition AST for synthetic branches

	Children []Node // branch body
}

// HasCondition reports whether this is a conditional #if branch. Cond is
// retained only as source/debug metadata; downstream semantics must follow
// the cached expression (or an explicitly generated condition), not text.
func (b *IfBranch) HasCondition() bool {
	return b != nil && (b.Expr != nil || b.SyntheticCond != nil)
}

// EachBlockNode is produced by Svelte-style {#each items as item (key)}...{/each}.
// The children are the content of each row; they may include multiple nodes.
type EachBlockNode struct {
	Span SectionSpan
	// EachExpr is retained as source/debug metadata. Standard block consumers use
	// IterExpr, ItemName, and IndexName instead of re-parsing this string.
	EachExpr     string // e.g. "items as item" or "items as item, index"
	ExprPos      int
	IterExpr     *TemplateExpr
	ItemName     string
	IndexName    string
	EachKey      string
	KeyPos       int // e.g. "item.id"  (from the (key) suffix)
	KeyAST       *TemplateExpr
	Children     []Node
	ElseChildren []Node
}

// HasKey reports whether the standard block contains a key expression. The
// raw EachKey string is metadata, so even an empty/malformed key is retained
// as KeyAST and can be rejected by validation instead of silently becoming an
// unkeyed block.
func (e *EachBlockNode) HasKey() bool { return e != nil && e.KeyAST != nil }

// AwaitBlockNode is produced by Svelte-style:
//
//	{#await promise}
//	  pending...
//	{:then value}
//	  resolved with {value}
//	{:catch err}
//	  failed: {err}
//	{/await}
//
// Either {:then} or {:catch} (or both) is optional.  When omitted,
// the corresponding branch slot is nil.  PendingChildren may also be
// empty (e.g. {#await p then x}...{/await}).
//
// At runtime the block renders the pending branch initially; when
// the promise settles the codegen swaps to then-or-catch.  If the
// user reassigns the promise expression, the codegen re-evaluates
// it and reattaches the .then/.catch handlers — so reactive prompts
// (e.g. await fetch(`/api/${id}`)) work as expected.
type AwaitBlockNode struct {
	Span            SectionSpan
	PromiseExpr     string // the JS expression after `#await`
	ExprPos         int
	Expr            *TemplateExpr
	ValueName       string // identifier bound in {:then NAME}, or ""
	ErrorName       string // identifier bound in {:catch NAME}, or ""
	PendingChildren []Node
	ThenChildren    []Node // nil when no {:then} branch
	CatchChildren   []Node // nil when no {:catch} branch
	mode            awaitMode
}

func (a *AwaitBlockNode) nodeKind() string { return "awaitblock" }

// awaitMode records whether an await block used the normal multi-branch form
// or one of Svelte's single-branch opening-tag shortcuts. It is parser-only:
// both code generators consume the normalized children and bindings above.
type awaitMode uint8

const (
	awaitBlockMode awaitMode = iota
	awaitInlineThenMode
	awaitInlineCatchMode
)

// ComponentNode represents a child component invocation: <MyComp :prop="val" />
type ComponentNode struct {
	Span  SectionSpan
	Name  string            // component name, e.g. "MyComp"
	Attrs []*Attr           // :prop bindings and @event handlers
	Slots map[string][]Node // slot name → children (default slot keyed as "default")
}

// HasKey reports whether this structured #key chain owns an expression AST.
// KeyExpr remains available for diagnostics and AST debug output only.
func (c *IfChainNode) HasKey() bool { return c != nil && c.KeyAST != nil }

// SlotNode represents a <slot> element — renders $$slots["name"] from parent.
type SlotNode struct {
	Span     SectionSpan
	Name     string // "default" for bare <slot />, or the name attr value
	Fallback []Node // children rendered if slot is not filled
}

func (s *SlotNode) nodeKind() string      { return "slot" }
func (e *EachBlockNode) nodeKind() string { return "eachblock" }

type TextNode struct {
	Span  SectionSpan
	Parts []*Part
}

// Part is one segment of a text node.
type TemplateExpr struct {
	Span    SectionSpan
	Source  string
	Pos     int
	Program *JsProgram
	Errors  []string
}

// Root returns the single JavaScript expression owned by a template
// expression. Template syntax never accepts statement lists here; callers
// that need executable meaning must use this typed root rather than Source.
func (e *TemplateExpr) Root() JsExpr {
	if e == nil || e.Program == nil || len(e.Program.Body) != 1 {
		return nil
	}
	stmt, ok := e.Program.Body[0].(*JsExprStmt)
	if !ok {
		return nil
	}
	return stmt.X
}

func newTemplateExpr(source string, pos int) *TemplateExpr {
	return newTemplateExprAt(source, pos, pos)
}

func newTemplateExprAt(source string, pos, absolutePos int) *TemplateExpr {
	leading := len(source) - len(strings.TrimLeftFunc(source, unicode.IsSpace))
	source = strings.TrimSpace(source)
	prog, errs := parseTemplateExpression(source)
	start := absolutePos + leading
	return &TemplateExpr{
		Span:    SectionSpan{Start: start, End: start + len(source)},
		Source:  source,
		Pos:     pos + leading,
		Program: prog,
		Errors:  errs,
	}
}

func (p *parser) templateExpr(source string, pos int) *TemplateExpr {
	return newTemplateExprAt(source, pos, p.base+pos)
}

// newAttributeTemplateExpr parses a dynamic attribute value through the same
// cached expression path as text holes. A plain `{expr}` keeps its original
// source, while an interpolated value such as `{size}px` is first represented
// as a JavaScript template literal. This prevents later code generation from
// reparsing an invalid `{size}px` fragment and preserves a single AST for both
// initial DOM writes and reactive updates.
func newAttributeTemplateExpr(value string, pos int) *TemplateExpr {
	return newTemplateExpr(attrExprToJS(value), pos)
}

func (p *parser) attributeTemplateExpr(value string, pos int) *TemplateExpr {
	return newTemplateExprAt(attrExprToJS(value), pos, p.base+pos)
}

func templateExprDeps(expr *TemplateExpr, vars []string) []string {
	if expr == nil || expr.Program == nil || len(expr.Errors) != 0 {
		return nil
	}
	return FindDepsInNode(expr.Program, vars)
}

func templatePartsDeps(parts []*Part, vars []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, part := range parts {
		if part == nil || part.Static {
			continue
		}
		for _, dep := range templateExprDeps(part.Expr, vars) {
			if !seen[dep] {
				seen[dep] = true
				out = append(out, dep)
			}
		}
	}
	return out
}

type Part struct {
	Span   SectionSpan
	Static bool // true → literal text; false → JS expression
	Value  string
	Pos    int
	Expr   *TemplateExpr
}

// AttrValueKind records the syntactic value form captured by the template parser.
type AttrValueKind uint8

const (
	AttrValueMissing AttrValueKind = iota
	AttrValueBare
	AttrValueQuoted
	AttrValueBraced
	AttrValueSynthetic
	// AttrValueQuotedObjectCompat identifies the legacy unsupported form
	// :class="{'name': condition}" before value normalization.
	AttrValueQuotedObjectCompat
)

func (k AttrValueKind) String() string {
	switch k {
	case AttrValueBare:
		return "bare"
	case AttrValueQuoted:
		return "quoted"
	case AttrValueBraced:
		return "braced"
	case AttrValueSynthetic:
		return "synthetic"
	case AttrValueQuotedObjectCompat:
		return "quoted-object-compat"
	default:
		return "missing"
	}
}

// Attr holds one HTML attribute, annotated with its sigil role.
type Attr struct {
	Span       SectionSpan
	Name       string // without the leading sigil
	Value      string
	ValueKind  AttrValueKind
	IsEvent    bool // @ prefix → addEventListener
	IsBind     bool // : prefix → dynamic attribute (one-way)
	IsTwoWay   bool // bind: prefix → two-way binding (reads + writes the state var)
	IsBindThis bool // bind:this={ref} → write the live element into `ref`
	IsClassDir bool // class:foo={cond} → toggle the `foo` class based on cond
	IsStyleDir bool
	Pos        int // style:foo={expr} → set inline style property `foo` to expr
	Expr       *TemplateExpr
}

func (e *ElementNode) nodeKind() string   { return "element" }
func (c *IfChainNode) nodeKind() string   { return "ifchain" }
func (c *ComponentNode) nodeKind() string { return "component" }
func (t *TextNode) nodeKind() string      { return "text" }

// ──────────────────────────────────────────────
//  Template Parser
// ──────────────────────────────────────────────

type parser struct {
	src          string
	base         int
	pos          int
	lastTagStart int
}

// templateJSSyntax tracks the lexical forms that make punctuation inert while
// scanning an embedded JavaScript expression. It is intentionally small: the
// JavaScript parser remains responsible for validating the expression itself;
// this scanner only prevents template delimiters from being recognised inside
// strings and comments.
type templateJSSyntax struct {
	quote        byte
	escaped      bool
	lineComment  bool
	blockComment bool
}

// skip consumes a quoted or commented span starting at i. It returns the next
// source position and true when the caller must not interpret src[i] as
// template structure.
func (s *templateJSSyntax) skip(src string, i int) (int, bool) {
	if i >= len(src) {
		return i, false
	}
	c := src[i]
	if s.lineComment {
		if c == '\n' || c == '\r' {
			s.lineComment = false
		}
		return i + 1, true
	}
	if s.blockComment {
		if c == '*' && i+1 < len(src) && src[i+1] == '/' {
			s.blockComment = false
			return i + 2, true
		}
		return i + 1, true
	}
	if s.quote != 0 {
		if s.escaped {
			s.escaped = false
		} else if c == '\\' {
			s.escaped = true
		} else if c == s.quote {
			s.quote = 0
		}
		return i + 1, true
	}
	if c == '/' && i+1 < len(src) {
		switch src[i+1] {
		case '/':
			s.lineComment = true
			return i + 2, true
		case '*':
			s.blockComment = true
			return i + 2, true
		}
	}
	if c == '\'' || c == '"' || c == '`' {
		s.quote = c
		return i + 1, true
	}
	return i, false
}

func parseTemplate(html string) ([]Node, error) {
	return parseTemplateAt(html, 0)
}

func parseTemplateAt(html string, base int) ([]Node, error) {
	p := &parser{src: html, base: base}
	nodes, err := p.parseNodes(nil)
	if err != nil {
		return nil, err
	}
	return nodes, nil
}

func (p *parser) ch() byte {
	if p.pos >= len(p.src) {
		return 0
	}
	return p.src[p.pos]
}

func (p *parser) eof() bool { return p.pos >= len(p.src) }

func (p *parser) skipWS() {
	for !p.eof() && unicode.IsSpace(rune(p.ch())) {
		p.pos++
	}
}

func (p *parser) has(s string) bool {
	return strings.HasPrefix(p.src[p.pos:], s)
}

func (p *parser) parseNodes(stopTags []string) ([]Node, error) {
	var nodes []Node
	for !p.eof() {
		// Track whether the upcoming run is preceded by source
		// whitespace.  If so, and the next thing is text (not a tag),
		// we need to preserve a single leading space — otherwise
		// inline markup like `<p>before <code>x</code> after</p>` ends
		// up rendered as `before<code>x</code>after`, with the space
		// after the closing tag silently eaten by skipWS.
		sawWS := false
		startPos := p.pos
		p.skipWS()
		if p.pos > startPos {
			sawWS = true
		}
		if p.eof() {
			break
		}

		// ── Closing HTML tag — bubble up to parent ────────────────────────
		if p.has("</") {
			inner := p.src[p.pos+2:]
			for _, stop := range stopTags {
				if strings.HasPrefix(strings.ToLower(inner), strings.ToLower(stop)) {
					return nodes, nil
				}
			}
			return nodes, nil
		}

		// ── Svelte-style block directives {#…} {/…} {:…} ─────────────────
		if p.isSvelteBlockStart() {
			node, term, err := p.parseSvelteBlock()
			if err != nil {
				return nil, err
			}
			if term != "" {
				// A terminator ({:else}, {/if}, {/each}) was encountered —
				// leave the parser positioned just after it and return.
				return nodes, nil
			}
			if node != nil {
				nodes = append(nodes, node)
			}
			continue
		}

		// ── Svelte block terminators — stop here, caller handles them ─────
		if p.isSvelteBlockTerm() {
			return nodes, nil
		}

		// ── HTML comment → skip ───────────────────────────────────────────
		if p.ch() == '<' {
			if p.has("<!--") {
				end := strings.Index(p.src[p.pos:], "-->")
				if end < 0 {
					p.pos = len(p.src)
				} else {
					p.pos += end + 3
				}
				continue
			}
			node, err := p.parseElement()
			if err != nil {
				return nil, err
			}
			if node != nil {
				nodes = append(nodes, node)
			}
		} else {
			// If we consumed leading whitespace before reaching this
			// text run, prepend a space-only text node so the inline
			// boundary is preserved.  Only fire when there's already
			// at least one preceding node (i.e. this isn't the very
			// first child) — leading whitespace at the start of an
			// element's content is still cosmetic indentation that
			// should be dropped.
			if sawWS && len(nodes) > 0 {
				nodes = append(nodes, &TextNode{
					Span:  SectionSpan{Start: p.base + startPos, End: p.base + p.pos},
					Parts: []*Part{{Static: true, Value: " "}},
				})
			}
			node, err := p.parseText()
			if err != nil {
				return nil, err
			}
			if node != nil {
				nodes = append(nodes, node)
			}
		}
	}
	return nodes, nil
}

func (p *parser) parseText() (*TextNode, error) {
	start := p.pos
	var buf strings.Builder
	// braceDepth > 0 means the scanner is inside a {…} interpolation.
	// A '<' there is part of the expression (e.g. `{i < n}`), not the
	// start of an HTML tag — so only stop on '<' at depth 0.
	braceDepth := 0
	for !p.eof() {
		c := p.ch()
		if c == '<' && braceDepth == 0 {
			break
		}
		// Stop before Svelte block directives so parseNodes can
		// handle them — but only at the top level, never mid-
		// interpolation.
		if braceDepth == 0 &&
			(p.isSvelteBlockStart() || p.isSvelteBlockTerm()) {
			break
		}
		if c == '{' {
			braceDepth++
		} else if c == '}' && braceDepth > 0 {
			braceDepth--
		}
		buf.WriteByte(c)
		p.pos++
	}
	raw := buf.String()
	parts := parseInterpolationsAt(raw, p.pos-len(raw))
	if len(parts) == 0 {
		return nil, nil
	}
	span := SectionSpan{Start: p.base + start, End: p.base + p.pos}
	for _, part := range parts {
		if part == nil {
			continue
		}
		part.Span = span
		if !part.Static && part.Expr != nil {
			part.Expr.Span = SectionSpan{
				Start: p.base + part.Expr.Pos,
				End:   p.base + part.Expr.Pos + len(part.Expr.Source),
			}
			part.Span = part.Expr.Span
		}
	}
	return &TextNode{Span: span, Parts: parts}, nil
}

// isHTMLSpace reports whether c is a whitespace character treated as
// inter-element spacing by HTML.  Same set the spec calls "ASCII
// whitespace": space, tab, line-feed, form-feed, carriage-return.
func isHTMLSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}

// parseInterpolations splits a raw string into static and dynamic parts
// by scanning for {expr} sequences.
func parseInterpolations(s string) []*Part { return parseInterpolationsAt(s, 0) }

func parseInterpolationsAt(s string, base int) []*Part {
	var parts []*Part
	var buf strings.Builder

	flush := func() {
		if buf.Len() == 0 {
			return
		}
		text := buf.String()
		buf.Reset()
		// Whitespace-only segment: collapse to a single space if it
		// contained one, otherwise drop entirely.  This is what kept
		// trailing newlines from polluting the output before, and we
		// keep it.
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			if strings.ContainsAny(text, " \t\n\r") {
				parts = append(parts, &Part{Static: true, Value: " "})
			}
			return
		}
		// Mixed text: keep boundary whitespace as a single space so
		// inline markup like `word <code>x</code> word` stays
		// visually correct.  Without this, the parser used to trim
		// both sides, producing `word<code>x</code>word` in output.
		// Internal runs of whitespace inside `trimmed` are kept as-is
		// because HTML collapses them at render time anyway.
		leading := ""
		if len(text) > 0 && isHTMLSpace(text[0]) {
			leading = " "
		}
		trailing := ""
		if len(text) > 0 && isHTMLSpace(text[len(text)-1]) {
			trailing = " "
		}
		parts = append(parts, &Part{Static: true, Value: leading + trimmed + trailing})
	}

	i := 0
	for i < len(s) {
		if s[i] == '{' {
			flush()
			i++ // skip '{'
			depth := 1
			start := i
			for i < len(s) && depth > 0 {
				switch s[i] {
				case '{':
					depth++
				case '}':
					depth--
					if depth == 0 {
						goto done
					}
				case '"', '\'', '`':
					q := s[i]
					i++
					for i < len(s) && s[i] != q {
						if s[i] == '\\' {
							i++
						}
						i++
					}
				}
				i++
			}
		done:
			// An unterminated quoted interpolation can advance past EOF in
			// the quote scanner. Clamp before slicing and only consume a
			// closing brace when one was actually found.
			if i > len(s) {
				i = len(s)
			}
			expr := strings.TrimSpace(s[start:i])
			if expr != "" {
				parts = append(parts, &Part{Static: false, Value: expr, Pos: base + start, Expr: newTemplateExpr(expr, base+start)})
			}
			if i < len(s) {
				i++ // skip the matched '}'
			}
		} else {
			buf.WriteByte(s[i])
			i++
		}
	}
	flush()
	return parts
}

func (p *parser) parseElement() (Node, error) {
	elementStart := p.pos
	p.pos++ // consume '<'

	// Tag name
	start := p.pos
	for !p.eof() && !isTagNameStop(p.ch()) {
		p.pos++
	}
	tag := p.src[start:p.pos]
	if tag == "" {
		return nil, fmt.Errorf("empty tag name at column %d", p.pos)
	}

	// ── Slot detection ────────────────────────────────────────────────────
	if tag == "slot" {
		return p.parseSlotNode(elementStart)
	}

	// ── svelte:fragment — transparent wrapper for named slot content ──────
	if tag == "svelte:fragment" {
		return p.parseSvelteFragment(elementStart)
	}

	// ── Component detection ───────────────────────────────────────────────
	// Tags starting with an uppercase letter are treated as child components.
	if len(tag) > 0 && tag[0] >= 'A' && tag[0] <= 'Z' {
		return p.parseComponentNode(tag, elementStart)
	}

	elem := &ElementNode{Tag: tag, Span: SectionSpan{Start: p.base + elementStart}}

	if err := p.parseAttrs(elem); err != nil {
		return nil, err
	}

	selfClose := false
	if p.has("/>") {
		p.pos += 2
		selfClose = true
	} else if !p.eof() && p.ch() == '>' {
		p.pos++
	}

	if !selfClose && !isVoidElement(tag) {
		children, err := p.parseNodes([]string{tag})
		if err != nil {
			return nil, err
		}
		elem.Children = children

		// Consume </tag>
		if p.has("</") {
			p.pos += 2
			for !p.eof() && p.ch() != '>' {
				p.pos++
			}
			if !p.eof() {
				p.pos++
			}
		}
	}

	elem.Span.End = p.base + p.pos
	return elem, nil
}

// parseSlotNode parses a <slot name="header">fallback</slot> element.
func (p *parser) parseSlotNode(elementStart int) (Node, error) {
	slot := &SlotNode{Span: SectionSpan{Start: p.base + elementStart}}
	scratch := &ElementNode{}
	if err := p.parseAttrs(scratch); err != nil {
		return nil, err
	}
	name := "default"
	for _, attr := range scratch.Attrs {
		if attr.Name == "name" {
			name = attr.Value
		}
	}

	selfClose := p.has("/>")
	if selfClose {
		p.pos += 2
		slot.Name = name
		slot.Span.End = p.base + p.pos
		return slot, nil
	}
	if !p.eof() && p.ch() == '>' {
		p.pos++
	}

	// Parse fallback content
	fallback, err := p.parseNodes([]string{"slot"})
	if err != nil {
		return nil, err
	}
	if p.has("</") {
		p.pos += 2
		for !p.eof() && p.ch() != '>' {
			p.pos++
		}
		if !p.eof() {
			p.pos++
		}
	}
	slot.Name = name
	slot.Fallback = fallback
	slot.Span.End = p.base + p.pos
	return slot, nil
}
func (p *parser) parseComponentNode(name string, elementStart int) (Node, error) {
	comp := &ComponentNode{Span: SectionSpan{Start: p.base + elementStart}, Name: name, Slots: map[string][]Node{}}
	scratch := &ElementNode{}
	if err := p.parseAttrs(scratch); err != nil {
		return nil, err
	}
	comp.Attrs = scratch.Attrs

	if p.has("/>") {
		p.pos += 2
		comp.Span.End = p.base + p.pos
		return comp, nil
	}
	if !p.eof() && p.ch() == '>' {
		p.pos++
	}

	// Parse children and sort into slots
	children, err := p.parseNodes([]string{name})
	if err != nil {
		return nil, err
	}
	closeTag := "</" + name
	if p.has(closeTag) {
		p.pos += len(closeTag)
		for !p.eof() && p.ch() != '>' {
			p.pos++
		}
		if !p.eof() {
			p.pos++
		}
	}

	var defaultSlot []Node
	for _, child := range children {
		slotName := ""

		switch c := child.(type) {
		case *ElementNode:
			// svelte:fragment children go directly into the slot (no wrapper div)
			if c.Tag == "svelte:fragment" {
				for _, a := range c.Attrs {
					if a.Name == "slot" {
						slotName = a.Value
						break
					}
				}
				if slotName != "" {
					comp.Slots[slotName] = append(comp.Slots[slotName], c.Children...)
					continue
				}
			}
			// Regular element with slot="name" attr
			for _, a := range c.Attrs {
				if a.Name == "slot" {
					slotName = a.Value
					break
				}
			}
			if slotName != "" {
				var kept []*Attr
				for _, a := range c.Attrs {
					if a.Name != "slot" {
						kept = append(kept, a)
					}
				}
				c.Attrs = kept
				comp.Slots[slotName] = append(comp.Slots[slotName], child)
				continue
			}
		}

		defaultSlot = append(defaultSlot, child)
	}
	if len(defaultSlot) > 0 {
		comp.Slots["default"] = defaultSlot
	}
	comp.Span.End = p.base + p.pos
	return comp, nil
}

func (p *parser) parseAttrs(elem *ElementNode) error {
	for {
		p.skipWS()
		if p.eof() || p.ch() == '>' || p.has("/>") {
			break
		}

		// Attribute name — may start with @, :, #
		start := p.pos
		for !p.eof() && !isAttrStop(p.ch()) {
			p.pos++
		}
		name := p.src[start:p.pos]
		if name == "" {
			break
		}

		p.skipWS()
		value := ""
		valuePos := -1
		valueKind := AttrValueMissing
		if !p.eof() && p.ch() == '=' {
			p.pos++
			p.skipWS()
			valuePos = p.pos
			switch p.ch() {
			case '"', '\'':
				valueKind = AttrValueQuoted
			case '{':
				valueKind = AttrValueBraced
			default:
				valueKind = AttrValueBare
			}
			value = p.readAttrValue()
		}
		if name == ":class" && valueKind == AttrValueQuoted && quotedObjectClassValue(value) {
			valueKind = AttrValueQuotedObjectCompat
		}

		attrCount := len(elem.Attrs)
		switch {
		case strings.HasPrefix(name, "#"):
			return legacyElementDirectiveError(name, value)
		case strings.HasPrefix(name, "@"):
			elem.Attrs = append(elem.Attrs, &Attr{Name: name[1:], Value: value, IsEvent: true})
		case strings.HasPrefix(name, "class:"):
			// class:active={cond}  →  toggle the "active" class based on cond.
			//
			// Shorthand: `class:active` (no value) is treated as
			// `class:active={active}` — using the local variable of
			// the same name as the toggle.  This matches Svelte 5
			// semantics and is convenient for state-bound flags.
			cls := name[len("class:"):]
			expr := value
			if expr == "" {
				expr = "{" + cls + "}"
			}
			elem.Attrs = append(elem.Attrs, &Attr{
				Name: cls, Value: expr, IsClassDir: true,
			})
		case strings.HasPrefix(name, "style:"):
			// style:color={c}  →  elem.style.color = c
			// style:font-size="14px"  →  elem.style.fontSize = "14px"
			//
			// Shorthand: `style:color` (no value) → `style:color={color}`,
			// same as class: shorthand.
			prop := name[len("style:"):]
			expr := value
			if expr == "" {
				expr = "{" + prop + "}"
			}
			elem.Attrs = append(elem.Attrs, &Attr{
				Name: prop, Value: expr, IsStyleDir: true,
			})
		case strings.HasPrefix(name, "bind:"):
			rest := name[len("bind:"):]
			if rest == "this" {
				// bind:this={ref} — write the live DOM element into `ref`
				// once it has been created.  Codegen handles emission;
				// here we just tag the attr.  The "value" is the target
				// expression (typically a state variable name).
				elem.Attrs = append(elem.Attrs, &Attr{
					Name: "this", Value: value, IsBindThis: true,
				})
				break
			}
			// Two-way binding:  bind:value="x"  behaves as :value="x" for the
			// write-to-DOM direction, and additionally wires a listener that
			// calls $$set_<x>(...) on user input.
			elem.Attrs = append(elem.Attrs, &Attr{
				Name: rest, Value: value,
				IsBind: true, IsTwoWay: true,
			})
		case strings.HasPrefix(name, ":"):
			elem.Attrs = append(elem.Attrs, &Attr{Name: name[1:], Value: value, IsBind: true})
		default:
			// Plain attribute name with a braced value (`href={url}`)
			// is treated as a dynamic bind, matching `:href={url}`.
			// Without this, the codegen falls into the static-attribute
			// path and emits `setAttribute('href', '{url}')` as a
			// literal, which is almost certainly not what the author
			// meant.  Quoted values (`href="static"`) still go through
			// the static path.
			isBraced := strings.HasPrefix(value, "{") && strings.HasSuffix(value, "}")
			elem.Attrs = append(elem.Attrs, &Attr{
				Name: name, Value: value, IsBind: isBraced,
			})
		}
		if len(elem.Attrs) > attrCount {
			a := elem.Attrs[len(elem.Attrs)-1]
			a.Pos = start
			a.Span = SectionSpan{Start: p.base + start, End: p.base + p.pos}
			if valueKind == AttrValueMissing && (a.IsClassDir || a.IsStyleDir) {
				valueKind = AttrValueSynthetic
			}
			a.ValueKind = valueKind
		}
		if len(elem.Attrs) > attrCount {
			a := elem.Attrs[len(elem.Attrs)-1]
			if a.IsEvent || a.IsBind || a.IsClassDir || a.IsStyleDir || a.IsBindThis {
				exprPos := a.Pos
				if valuePos >= 0 {
					exprPos = valuePos
					if strings.HasPrefix(a.Value, "{") {
						exprPos++
					}
				} else if idx := strings.LastIndex(p.src[start:p.pos], a.Name); idx >= 0 {
					exprPos = start + idx
				}
				if a.IsClassDir || a.IsStyleDir {
					a.Expr = p.attributeTemplateExpr(a.Value, exprPos)
				} else if a.ValueKind == AttrValueQuotedObjectCompat {
					a.Expr = p.templateExpr(a.Value, exprPos)
				} else {
					a.Expr = p.templateExpr(stripBraces(a.Value), exprPos)
				}
			}
		}
	}
	return nil
}

func legacyElementDirectiveError(name, value string) error {
	expr := strings.TrimSpace(stripBraces(value))
	switch name {
	case "#if":
		return fmt.Errorf("element directive #if is no longer supported; wrap the element in {#if %s} ... {/if}", expr)
	case "#elseif", "#else":
		return fmt.Errorf("element directive %s is no longer supported; use {:else if condition} or {:else} inside a {#if} block", name)
	case "#each":
		return fmt.Errorf("element directive #each is no longer supported; wrap the element in {#each %s} ... {/each}", expr)
	case "#key":
		return fmt.Errorf("element directive #key is no longer supported; use {#each items as item (%s)} or wrap content in {#key %s} ... {/key}", expr, expr)
	default:
		return fmt.Errorf("element directive %s is not supported; use Vela block syntax instead", name)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
//  Svelte-style block directives: {#if}, {#each}, {:else}, {/if}, {/each}
// ─────────────────────────────────────────────────────────────────────────────

// isSvelteBlockStart reports whether the parser is at a {#if, {#each, {#key, or {#await opener.
func (p *parser) isSvelteBlockStart() bool {
	if p.pos+1 >= len(p.src) || p.src[p.pos] != '{' {
		return false
	}
	rest := p.src[p.pos+1:]
	return strings.HasPrefix(rest, "#if ") || strings.HasPrefix(rest, "#if\t") ||
		strings.HasPrefix(rest, "#if}") ||
		strings.HasPrefix(rest, "#each ") || strings.HasPrefix(rest, "#each\t") ||
		strings.HasPrefix(rest, "#key ") || strings.HasPrefix(rest, "#key\t") ||
		strings.HasPrefix(rest, "#await ") || strings.HasPrefix(rest, "#await\t")
}

// isSvelteBlockTerm reports whether the parser is at {:else}, {:else if}, {:then}, {:catch}, {/if}, {/each}, {/key}, {/await}.
func (p *parser) isSvelteBlockTerm() bool {
	if p.pos+1 >= len(p.src) || p.src[p.pos] != '{' {
		return false
	}
	rest := p.src[p.pos+1:]
	return strings.HasPrefix(rest, ":else") ||
		strings.HasPrefix(rest, ":then") ||
		strings.HasPrefix(rest, ":catch") ||
		strings.HasPrefix(rest, "/if") ||
		strings.HasPrefix(rest, "/each") ||
		strings.HasPrefix(rest, "/key") ||
		strings.HasPrefix(rest, "/await")
}

// readSvelteTag reads from current '{' to the closing '}', returning the inner text.
func (p *parser) readSvelteTag() string {
	p.lastTagStart = p.pos
	p.pos++ // skip '{'
	start := p.pos
	depth := 1
	var syntax templateJSSyntax
	for p.pos < len(p.src) && depth > 0 {
		if next, skipped := syntax.skip(p.src, p.pos); skipped {
			p.pos = next
			continue
		}
		switch p.src[p.pos] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				tag := strings.TrimSpace(p.src[start:p.pos])
				p.pos++ // skip '}'
				return tag
			}
		}
		p.pos++
	}
	return strings.TrimSpace(p.src[start:p.pos])
}

// parseSvelteBlock parses one complete {#if} or {#each} block including all
// branches and the closing {/if} or {/each}.
// Returns (node, terminator, error). If a terminator was consumed before the
// block was complete (shouldn't happen at this level), terminator is non-empty.
func (p *parser) blockExprPos(tagStart int, tag, expr string) int {
	if expr == "" {
		return tagStart
	}
	if idx := strings.Index(p.src[tagStart:p.pos], expr); idx >= 0 {
		return tagStart + idx
	}
	return tagStart
}

func (p *parser) parseSvelteBlock() (Node, string, error) {
	tagStart := p.pos
	tag := p.readSvelteTag() // e.g. "#if cond", "#each items as item (key)"

	switch {
	case strings.HasPrefix(tag, "#if"):
		node, err := p.parseSvelteIfBlock(tag[3:], p.blockExprPos(tagStart, tag, strings.TrimSpace(tag[3:])), tagStart)
		if node != nil {
			setTemplateNodeSpan(node, p.base+tagStart, p.base+p.pos)
		}
		return node, "", err

	case strings.HasPrefix(tag, "#each"):
		node, err := p.parseSvelteEachBlock(tag[5:], p.blockExprPos(tagStart, tag, strings.TrimSpace(tag[5:])))
		if node != nil {
			setTemplateNodeSpan(node, p.base+tagStart, p.base+p.pos)
		}
		return node, "", err

	case strings.HasPrefix(tag, "#key"):
		// {#key expr}...{/key} — re-create the children whenever
		// `expr` changes identity.  Modeled as a one-branch
		// IfChainNode with KeyExpr set; the codegen handles the
		// destroy-and-rebuild logic.
		node, err := p.parseSvelteKeyBlock(tag[4:], p.blockExprPos(tagStart, tag, strings.TrimSpace(tag[4:])))
		if node != nil {
			setTemplateNodeSpan(node, p.base+tagStart, p.base+p.pos)
		}
		return node, "", err

	case strings.HasPrefix(tag, "#await"):
		// {#await promise}...{:then v}...{:catch e}...{/await}
		node, err := p.parseSvelteAwaitBlock(tag[6:], p.blockExprPos(tagStart, tag, strings.TrimSpace(tag[6:])))
		if node != nil {
			setTemplateNodeSpan(node, p.base+tagStart, p.base+p.pos)
		}
		return node, "", err

	case strings.HasPrefix(tag, ":else") || strings.HasPrefix(tag, ":then") ||
		strings.HasPrefix(tag, ":catch") ||
		strings.HasPrefix(tag, "/if") || strings.HasPrefix(tag, "/each") ||
		strings.HasPrefix(tag, "/key") || strings.HasPrefix(tag, "/await"):
		// Terminator — caller should have caught this via isSvelteBlockTerm
		return nil, tag, nil

	default:
		return nil, "", nil
	}
}

// parseSvelteIfBlock parses {#if cond}...{:else if cond}...{:else}...{/if}.
// condRaw is everything after "#if" (with leading space).
func (p *parser) parseSvelteIfBlock(condRaw string, condPos int, branchStart int) (Node, error) {
	chain := &IfChainNode{}

	cond := strings.TrimSpace(condRaw)
	for {
		// Parse the body of this branch
		children, term, err := p.parseSvelteBlockBody()
		if err != nil {
			return nil, err
		}

		chain.Branches = append(chain.Branches, &IfBranch{
			Span:     SectionSpan{Start: p.base + branchStart, End: p.base + p.lastTagStart},
			Cond:     cond,
			CondPos:  condPos,
			Expr:     p.templateExpr(cond, condPos),
			Children: children,
		})

		switch {
		case strings.HasPrefix(term, ":else if ") || strings.HasPrefix(term, ":else if\t"):
			cond = strings.TrimSpace(term[9:])
			condPos = p.blockExprPos(p.lastTagStart, term, cond)
			branchStart = p.lastTagStart
			// continue loop for next branch
		case term == ":else" || strings.HasPrefix(term, ":else}"):
			branchStart = p.lastTagStart
			// Parse else body
			elseChildren, _, err2 := p.parseSvelteBlockBody()
			if err2 != nil {
				return nil, err2
			}
			chain.Branches = append(chain.Branches, &IfBranch{
				Span:     SectionSpan{Start: p.base + branchStart, End: p.base + p.lastTagStart},
				Cond:     "",
				Children: elseChildren,
			})
			return chain, nil
		default:
			// {/if} consumed — done
			return chain, nil
		}
	}
}

// parseSvelteEachBlock parses {#each expr as item (key)}...{/each}.
// raw is everything after "#each" (with leading space).
func (p *parser) parseSvelteEachBlock(raw string, exprPos int) (Node, error) {
	raw = strings.TrimSpace(raw)

	// Extract the optional terminal `(key)` suffix.  It must be terminal:
	// parentheses inside the iterable expression are ordinary JavaScript, e.g.
	// `{#each feats(plan) as feature}`.  The former LastIndex("(") logic
	// mistook that call for a key, reduced the iterable to `feats`, and also
	// dropped the declared row binding.
	key := ""
	keyPos := 0
	var keyAST *TemplateExpr
	if keyStart := eachTerminalKeyStart(raw); keyStart >= 0 {
		keyRaw := raw[keyStart+1 : len(raw)-1]
		key = strings.TrimSpace(keyRaw)
		keyPos = exprPos + keyStart + 1 + len(keyRaw) - len(strings.TrimLeftFunc(keyRaw, unicode.IsSpace))
		keyAST = p.templateExpr(key, keyPos)
		raw = strings.TrimSpace(raw[:keyStart])
	}

	iter, itemName, indexName := parseEachHeader(raw)
	children, term, err := p.parseSvelteBlockBody()
	if err != nil {
		return nil, err
	}
	var elseChildren []Node
	if term == ":else" {
		elseChildren, _, err = p.parseSvelteBlockBody()
		if err != nil {
			return nil, err
		}
	}
	return &EachBlockNode{
		EachExpr: raw, ExprPos: exprPos, IterExpr: p.templateExpr(iter, exprPos), ItemName: itemName, IndexName: indexName, EachKey: key, KeyPos: keyPos, KeyAST: keyAST,
		Children:     children,
		ElseChildren: elseChildren,
	}, nil
}

// quotedObjectClassValue identifies the legacy unsupported object-class form
// while the original quote syntax is still available to the parser.
func quotedObjectClassValue(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "{'") || strings.HasPrefix(value, "{\"")
}

func parseEachHeader(raw string) (iter, item, index string) {
	as := eachAsDelimiter(raw)
	if as < 0 {
		return strings.TrimSpace(raw), "item", ""
	}
	iter = strings.TrimSpace(raw[:as])
	item = "item"
	rest := strings.TrimSpace(raw[as+4:])
	if comma := strings.Index(rest, ","); comma >= 0 {
		return iter, strings.TrimSpace(rest[:comma]), strings.TrimSpace(rest[comma+1:])
	}
	return iter, rest, ""
}

// eachAsDelimiter finds the structural ` as ` separator in a standard #each
// header. JS expressions before the separator may contain the same bytes in
// strings, template literals, nested calls, objects, and arrays; none of
// those are header syntax. Keeping this distinction in the parser prevents a
// later stage from turning `lookup(plan, " as ")` into the bare identifier
// `lookup`.
func eachAsDelimiter(raw string) int {
	var syntax templateJSSyntax
	depth := 0
	for i := 0; i < len(raw); i++ {
		if next, skipped := syntax.skip(raw, i); skipped {
			i = next - 1
			continue
		}
		c := raw[i]
		switch c {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 && i+4 <= len(raw) && raw[i:i+4] == " as " {
				return i
			}
		}
	}
	return -1
}

// eachTerminalKeyStart returns the opening parenthesis of a terminal
// `{#each iterable as item (key)}` suffix, or -1 when the header has no key.
// Requiring the closing parenthesis to be the final token distinguishes the
// suffix from a normal call expression in the iterable.
func eachTerminalKeyStart(raw string) int {
	if !strings.HasSuffix(raw, ")") || eachAsDelimiter(raw) < 0 {
		return -1
	}
	as := eachAsDelimiter(raw)
	var syntax templateJSSyntax
	depth := 0
	keyStart := -1
	for i := as + 4; i < len(raw); i++ {
		if next, skipped := syntax.skip(raw, i); skipped {
			i = next - 1
			continue
		}
		c := raw[i]
		switch c {
		case '(':
			if depth == 0 {
				keyStart = i
			}
			depth++
		case ')':
			if depth == 0 {
				return -1
			}
			depth--
			if depth == 0 && i == len(raw)-1 {
				return keyStart
			}
		}
	}
	return -1
}

// parseSvelteKeyBlock parses {#key expr}...{/key}.  expr is whatever
// follows "#key" on the opening tag.  Lowered to a one-branch
// IfChainNode with KeyExpr set; codegen reads KeyExpr and emits a
// "destroy if key changed" guard before the chain's active-branch
// re-check.
func (p *parser) parseSvelteKeyBlock(raw string, keyPos int) (Node, error) {
	expr := strings.TrimSpace(raw)
	children, _, err := p.parseSvelteBlockBody()
	if err != nil {
		return nil, err
	}
	return &IfChainNode{
		KeyExpr: expr, KeyPos: keyPos, KeyAST: p.templateExpr(expr, keyPos),
		Branches: []*IfBranch{{
			// Always-true condition.  The chain stays mounted; the
			// destroy-and-rebuild is driven by the key change, not by
			// the condition flipping.
			Cond:     "",
			Children: children,
		}},
	}, nil
}

// parseSvelteAwaitBlock parses
//
//	{#await promise}
//	  pending...
//	{:then value}
//	  resolved with {value}
//	{:catch err}
//	  failed: {err}
//	{/await}
//
// Either {:then} or {:catch} may be absent; the opening tag may instead use
// `{#await promise then value}` or `{#await promise catch error}`. Inline
// forms have no pending branch and cannot be mixed with block-form branches.
func (p *parser) parseSvelteAwaitBlock(raw string, exprPos int) (Node, error) {
	promise, binding, mode, err := splitAwaitClause(raw)
	if err != nil {
		return nil, err
	}
	node := &AwaitBlockNode{PromiseExpr: promise, Expr: p.templateExpr(promise, exprPos), ExprPos: exprPos, mode: mode}
	if node.PromiseExpr == "" {
		return nil, fmt.Errorf("await block requires a promise expression")
	}
	if mode == awaitInlineThenMode {
		node.ValueName = binding
	} else if mode == awaitInlineCatchMode {
		node.ErrorName = binding
	}
	children, term, err := p.parseSvelteBlockBody()
	if err != nil {
		return nil, err
	}
	if mode != awaitBlockMode {
		if strings.HasPrefix(term, ":then") || strings.HasPrefix(term, ":catch") {
			return nil, fmt.Errorf("inline await %s shorthand cannot be combined with %s", inlineAwaitModeName(mode), term)
		}
		if term != "/await" {
			return nil, fmt.Errorf("await block expected {/await}, got {%s}", term)
		}
		if mode == awaitInlineThenMode {
			node.ThenChildren = children
		} else {
			node.CatchChildren = children
		}
		return node, nil
	}
	node.PendingChildren = children
	if strings.HasPrefix(term, ":then") {
		binding, err = awaitBranchBinding(term, ":then")
		if err != nil {
			return nil, err
		}
		node.ValueName = binding
		thenChildren, t2, err := p.parseSvelteBlockBody()
		if err != nil {
			return nil, err
		}
		node.ThenChildren = thenChildren
		term = t2
	}
	if strings.HasPrefix(term, ":catch") {
		binding, err = awaitBranchBinding(term, ":catch")
		if err != nil {
			return nil, err
		}
		node.ErrorName = binding
		catchChildren, t2, err := p.parseSvelteBlockBody()
		if err != nil {
			return nil, err
		}
		node.CatchChildren = catchChildren
		term = t2
	}
	if term != "/await" {
		return nil, fmt.Errorf("await block expected {/await}, got {%s}", term)
	}
	return node, nil
}

// splitAwaitClause separates a top-level opening-tag `then name` or `catch name`
// clause from its promise expression. Strings, comments, and bracketed expressions
// are skipped so either word inside one remains part of the promise.
func splitAwaitClause(raw string) (promise, binding string, mode awaitMode, err error) {
	raw = strings.TrimSpace(raw)
	depth := 0
	var quote byte
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if quote != 0 {
			if c == 92 {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == 47 && i+1 < len(raw) && raw[i+1] == 47 {
			i += 2
			for i < len(raw) && raw[i] != 10 {
				i++
			}
			continue
		}
		if c == 47 && i+1 < len(raw) && raw[i+1] == 42 {
			i += 2
			for i+1 < len(raw) && !(raw[i] == 42 && raw[i+1] == 47) {
				i++
			}
			if i+1 < len(raw) {
				i++
			}
			continue
		}
		switch c {
		case 39, 34, 96:
			quote = c
		case 40, 91, 123:
			depth++
		case 41, 93, 125:
			if depth > 0 {
				depth--
			}
		default:
			if depth != 0 || !isIdentStart(c) || i == 0 || !unicode.IsSpace(rune(raw[i-1])) {
				continue
			}
			end := i + 1
			for end < len(raw) && isIdentCont(raw[end]) {
				end++
			}
			word := raw[i:end]
			if word != "then" && word != "catch" {
				i = end - 1
				continue
			}
			promise, binding = strings.TrimSpace(raw[:i]), strings.TrimSpace(raw[end:])
			if promise == "" {
				return "", "", awaitBlockMode, fmt.Errorf("await block requires a promise expression")
			}
			if !isPlainAwaitBinding(binding) {
				return "", "", awaitBlockMode, fmt.Errorf("await %s binding must be one plain JavaScript identifier", word)
			}
			if word == "then" {
				return promise, binding, awaitInlineThenMode, nil
			}
			return promise, binding, awaitInlineCatchMode, nil
		}
	}
	return raw, "", awaitBlockMode, nil
}

func awaitBranchBinding(term, prefix string) (string, error) {
	binding := strings.TrimSpace(strings.TrimPrefix(term, prefix))
	if !isPlainAwaitBinding(binding) {
		return "", fmt.Errorf("await %s binding must be one plain JavaScript identifier", strings.TrimPrefix(prefix, ":"))
	}
	return binding, nil
}

func isPlainAwaitBinding(name string) bool {
	if name == "" || !isIdentStart(name[0]) || kwMap[name] != TokIllegal {
		return false
	}
	for i := 1; i < len(name); i++ {
		if !isIdentCont(name[i]) {
			return false
		}
	}
	return true
}

func inlineAwaitModeName(mode awaitMode) string {
	if mode == awaitInlineThenMode {
		return "then"
	}
	return "catch"
}

// parseSvelteBlockBody collects nodes until a {:else}, {:else if}, {/if}, or
// {/each} terminator. Returns (children, terminator-tag, error).
func (p *parser) parseSvelteBlockBody() ([]Node, string, error) {
	var nodes []Node
	for !p.eof() {
		p.skipWS()
		if p.eof() {
			break
		}

		if p.isSvelteBlockTerm() {
			term := p.readSvelteTag()
			return nodes, term, nil
		}

		if p.isSvelteBlockStart() {
			node, _, err := p.parseSvelteBlock()
			if err != nil {
				return nil, "", err
			}
			if node != nil {
				nodes = append(nodes, node)
			}
			continue
		}

		if p.has("</") {
			// Unexpected closing HTML tag inside a Svelte block — stop
			break
		}

		if p.ch() == '<' {
			if p.has("<!--") {
				end := strings.Index(p.src[p.pos:], "-->")
				if end < 0 {
					p.pos = len(p.src)
				} else {
					p.pos += end + 3
				}
				continue
			}
			node, err := p.parseElement()
			if err != nil {
				return nil, "", err
			}
			if node != nil {
				nodes = append(nodes, node)
			}
		} else {
			node, err := p.parseText()
			if err != nil {
				return nil, "", err
			}
			if node != nil {
				nodes = append(nodes, node)
			}
		}
	}
	return nodes, "", nil
}

// parseSvelteFragment parses <svelte:fragment slot="name">...</svelte:fragment>.
// Returns an ElementNode with Tag="svelte:fragment" so slot sorting can detect it.
func (p *parser) parseSvelteFragment(elementStart int) (Node, error) {
	elem := &ElementNode{Span: SectionSpan{Start: p.base + elementStart}, Tag: "svelte:fragment"}
	if err := p.parseAttrs(elem); err != nil {
		return nil, err
	}
	if p.has("/>") {
		p.pos += 2
		elem.Span.End = p.base + p.pos
		return elem, nil
	}
	if !p.eof() && p.ch() == '>' {
		p.pos++
	}
	children, err := p.parseNodes([]string{"svelte:fragment"})
	if err != nil {
		return nil, err
	}
	elem.Children = children
	if p.has("</") {
		p.pos += 2
		for !p.eof() && p.ch() != '>' {
			p.pos++
		}
		if !p.eof() {
			p.pos++
		}
	}
	elem.Span.End = p.base + p.pos
	return elem, nil
}

func (p *parser) readAttrValue() string {
	if p.eof() {
		return ""
	}
	quote := p.ch()
	if quote == '"' || quote == '\'' {
		p.pos++
		start := p.pos
		for !p.eof() && p.ch() != quote {
			if p.ch() == '\\' {
				p.pos++
			}
			p.pos++
		}
		val := p.src[start:p.pos]
		if !p.eof() {
			p.pos++
		}
		return val
	}

	// Unquoted.
	//
	// Two shapes are possible here:
	//
	//   1. A bare token like `primary` or `42` — stops at whitespace
	//      or the tag terminator.
	//   2. A braced expression like `{() => count++}` — the *contents*
	//      can include whitespace, nested braces, string literals,
	//      template literals.  The expression ends at the matching
	//      outer `}`, not at the first whitespace.
	//
	// Without case 2 the parser used to split `@click={() => count++}
	// variant="primary"` into something like four separate attrs
	// ("@click={()", "=>", "count++}", `variant="primary"`), which
	// the codegen later turned into broken JS.
	start := p.pos
	if p.ch() == '{' {
		depth := 0
		for !p.eof() {
			c := p.ch()
			switch c {
			case '"', '\'':
				// Skip a string literal verbatim — its contents are
				// arbitrary including unbalanced braces.
				q := c
				p.pos++
				for !p.eof() && p.ch() != q {
					if p.ch() == '\\' && p.pos+1 < len(p.src) {
						p.pos += 2
						continue
					}
					p.pos++
				}
				if !p.eof() {
					p.pos++ // closing quote
				}
			case '`':
				// Template literal.  Don't recurse into `${...}`
				// because any `{`/`}` inside a template literal is
				// content as far as our balancing goes — the literal
				// closes at the matching backtick.
				p.pos++
				for !p.eof() && p.ch() != '`' {
					if p.ch() == '\\' && p.pos+1 < len(p.src) {
						p.pos += 2
						continue
					}
					p.pos++
				}
				if !p.eof() {
					p.pos++
				}
			case '{':
				depth++
				p.pos++
			case '}':
				depth--
				p.pos++
				if depth == 0 {
					return p.src[start:p.pos]
				}
			default:
				p.pos++
			}
		}
		// EOF before matching `}` — return what we have.
		return p.src[start:p.pos]
	}

	// Bare token path (no braces).
	for !p.eof() && p.ch() != ' ' && p.ch() != '\t' && p.ch() != '\n' && p.ch() != '>' && !p.has("/>") {
		p.pos++
	}
	return p.src[start:p.pos]
}

// ──────────────────────────────────────────────
//  Helpers
// ──────────────────────────────────────────────

func isTagNameStop(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '>' || c == '/'
}

func isAttrStop(c byte) bool {
	return c == '=' || c == '>' || c == '/' || c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

func isVoidElement(tag string) bool {
	void := map[string]bool{
		"area": true, "base": true, "br": true, "col": true,
		"embed": true, "hr": true, "img": true, "input": true,
		"link": true, "meta": true, "param": true, "source": true,
		"track": true, "wbr": true,
	}
	return void[strings.ToLower(tag)]
}

func setTemplateNodeSpan(node Node, start, end int) {
	span := SectionSpan{Start: start, End: end}
	switch n := node.(type) {
	case *IfChainNode:
		n.Span = span
	case *EachBlockNode:
		n.Span = span
	case *AwaitBlockNode:
		n.Span = span
	}
}

func scanJSDocComments(source string) []JSDocComment {
	comments := []JSDocComment{}
	for index := 0; index < len(source); {
		switch source[index] {
		case 39, 34, 96:
			index = skipJSDocString(source, index)
		case 47:
			if index+1 >= len(source) {
				index++
				continue
			}
			if source[index+1] == 47 {
				index += 2
				for index < len(source) && source[index] != 10 {
					index++
				}
				continue
			}
			if source[index+1] != 42 {
				index++
				continue
			}
			endOffset := strings.Index(source[index+2:], "*/")
			if endOffset < 0 {
				return comments
			}
			end := endOffset + index + 4
			if index+2 < len(source) && source[index+2] == 42 {
				if comment, ok := parseJSDocComment(source, index, end); ok {
					comments = append(comments, comment)
				}
			}
			index = end
		default:
			index++
		}
	}
	return comments
}

func skipJSDocString(source string, start int) int {
	quote := source[start]
	index := start + 1
	for index < len(source) {
		if source[index] == 92 {
			index += 2
			continue
		}
		if source[index] == quote {
			return index + 1
		}
		index++
	}
	return index
}

func parseJSDocComment(source string, start, end int) (JSDocComment, bool) {
	body := source[start:end]
	marker := strings.Index(body, "@type")
	if marker < 0 {
		return JSDocComment{}, false
	}
	open := strings.Index(body[marker+len("@type"):], "{")
	if open < 0 {
		return JSDocComment{}, false
	}
	open += marker + len("@type")
	close := strings.Index(body[open+1:], "}")
	if close < 0 {
		return JSDocComment{}, false
	}
	close += open + 1
	name, ok := jsdocExportLetName(source, end)
	if !ok {
		return JSDocComment{}, false
	}
	return JSDocComment{Start: start, End: end, TargetName: name, Type: parseJSDocType(body[open+1 : close])}, true
}

func jsdocExportLetName(source string, start int) (string, bool) {
	index := skipJSDocSpace(source, start)
	if !strings.HasPrefix(source[index:], "export") || !jsdocWordBoundary(source, index+len("export")) {
		return "", false
	}
	index = skipJSDocSpace(source, index+len("export"))
	if !strings.HasPrefix(source[index:], "let") || !jsdocWordBoundary(source, index+len("let")) {
		return "", false
	}
	index = skipJSDocSpace(source, index+len("let"))
	if index >= len(source) || !jsdocIdentifierStart(source[index]) {
		return "", false
	}
	end := index + 1
	for end < len(source) && jsdocIdentifierPart(source[end]) {
		end++
	}
	return source[index:end], true
}

func skipJSDocSpace(source string, index int) int {
	for index < len(source) && unicode.IsSpace(rune(source[index])) {
		index++
	}
	return index
}

func jsdocWordBoundary(source string, index int) bool {
	return index >= len(source) || !jsdocIdentifierPart(source[index])
}

func jsdocIdentifierStart(value byte) bool {
	return value == 95 || value == 36 || unicode.IsLetter(rune(value))
}

func jsdocIdentifierPart(value byte) bool {
	return jsdocIdentifierStart(value) || unicode.IsDigit(rune(value))
}

func parseJSDocType(value string) JSDocTypeKind {
	value = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), " ", ""))
	switch {
	case value == "string":
		return JSDocTypeString
	case value == "boolean" || value == "bool":
		return JSDocTypeBool
	case value == "number" || value == "float" || value == "float64":
		return JSDocTypeNumber
	case value == "integer" || value == "int":
		return JSDocTypeInteger
	case value == "object" || strings.HasPrefix(value, "record<"):
		return JSDocTypeObject
	case strings.HasSuffix(value, "[]") || strings.HasPrefix(value, "array<"):
		return JSDocTypeSlice
	case value == "any" || value == "*":
		return JSDocTypeAny
	default:
		return JSDocTypeUnknown
	}
}
