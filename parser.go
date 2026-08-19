package main

import (
	"fmt"
	"strings"
	"unicode"
)

// ──────────────────────────────────────────────
//  Single-File Component
// ──────────────────────────────────────────────

type SFC struct {
	Script   string
	Style    string
	Template string

	// Byte offsets of each section's body in the original source file.
	// Used by diagnostics to translate parser-local positions into
	// file-relative line/column for accurate error reporting.  A value
	// of 0 means the section wasn't found (and the corresponding
	// content string is empty).
	ScriptOffset   int
	StyleOffset    int
	TemplateOffset int
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
		low := strings.ToLower(src)
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

		closeIdx := strings.Index(strings.ToLower(src[bodyStart:]), closeTag)
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
	return sfc, nil
}

// ──────────────────────────────────────────────
//  Template AST
// ──────────────────────────────────────────────

type Node interface{ nodeKind() string }

// ElementNode represents a DOM element. Directives (#if, #each) are hoisted
// out of the attribute list into dedicated fields during parsing.
type ElementNode struct {
	Tag      string
	Attrs    []*Attr
	Children []Node

	// Directives — set by the attribute-style syntax (#if="expr" on the element)
	// For Svelte block syntax, IfCond/EachExpr live in IfChainNode/EachBlockNode.
	IfCond   string // #if="expr"
	EachExpr string // #each="items as item, i"
	EachKey  string // #key="expr"
}

// IfChainNode groups a #if element with its optional #elseif / #else siblings.
// This is produced by the post-parse grouper pass.
type IfChainNode struct {
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
	Cond     string // empty string → #else branch
	CondPos  int    // byte offset of condition in template
	Expr     *TemplateExpr
	Children []Node         // branch body
	Elem     *ElementNode   // non-nil for element branches (old attr syntax)
	Comp     *ComponentNode // non-nil for component branches
}

// EachBlockNode is produced by Svelte-style {#each items as item (key)}...{/each}.
// The children are the content of each row; they may include multiple nodes.
type EachBlockNode struct {
	EachExpr     string // e.g. "items as item" or "items as item, index"
	ExprPos      int
	IterExpr     *TemplateExpr
	EachKey      string
	KeyPos       int // e.g. "item.id"  (from the (key) suffix)
	KeyAST       *TemplateExpr
	Children     []Node
	ElseChildren []Node
}

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
	PromiseExpr     string // the JS expression after `#await`
	ExprPos         int
	Expr            *TemplateExpr
	ValueName       string // identifier bound in {:then NAME}, or ""
	ErrorName       string // identifier bound in {:catch NAME}, or ""
	PendingChildren []Node
	ThenChildren    []Node // nil when no {:then} branch
	CatchChildren   []Node // nil when no {:catch} branch
}

func (a *AwaitBlockNode) nodeKind() string { return "awaitblock" }

// ComponentNode represents a child component invocation: <MyComp :prop="val" />
type ComponentNode struct {
	Name  string            // component name, e.g. "MyComp"
	Attrs []*Attr           // :prop bindings and @event handlers
	Slots map[string][]Node // slot name → children (default slot keyed as "default")
	// Directives (transferred from parseAttrs scratch element)
	EachExpr string // #each="items as item, i"
	EachKey  string // #key="expr"
	IfCond   string // #if="expr"
}

// SlotNode represents a <slot> element — renders $$slots["name"] from parent.
type SlotNode struct {
	Name     string // "default" for bare <slot />, or the name attr value
	Fallback []Node // children rendered if slot is not filled
}

func (s *SlotNode) nodeKind() string      { return "slot" }
func (e *EachBlockNode) nodeKind() string { return "eachblock" }

type TextNode struct {
	Parts []*Part
}

// Part is one segment of a text node.
type TemplateExpr struct {
	Source  string
	Pos     int
	Program *JsProgram
	Errors  []string
}

func newTemplateExpr(source string, pos int) *TemplateExpr {
	prog, errs := parseTemplateExpression(source)
	return &TemplateExpr{Source: source, Pos: pos, Program: prog, Errors: errs}
}

func templateExprDeps(expr *TemplateExpr, fallback string, vars []string) []string {
	if expr != nil && expr.Program != nil && len(expr.Errors) == 0 {
		return FindDepsInNode(expr.Program, vars)
	}
	return findDeps(fallback, vars)
}

func templatePartsDeps(parts []*Part, vars []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, part := range parts {
		if part == nil || part.Static {
			continue
		}
		for _, dep := range templateExprDeps(part.Expr, part.Value, vars) {
			if !seen[dep] {
				seen[dep] = true
				out = append(out, dep)
			}
		}
	}
	return out
}

type Part struct {
	Static bool // true → literal text; false → JS expression
	Value  string
	Pos    int
	Expr   *TemplateExpr
}

// Attr holds one HTML attribute, annotated with its sigil role.
type Attr struct {
	Name       string // without the leading sigil
	Value      string
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
	pos          int
	lastTagStart int
}

func parseTemplate(html string) ([]Node, error) {
	p := &parser{src: html}
	nodes, err := p.parseNodes(nil)
	if err != nil {
		return nil, err
	}
	return groupIfChains(nodes), nil
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
	return &TextNode{Parts: parts}, nil
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
			expr := strings.TrimSpace(s[start:i])
			if expr != "" {
				parts = append(parts, &Part{Static: false, Value: expr, Pos: base + start, Expr: newTemplateExpr(expr, base+start)})
			}
			i++ // skip '}'
		} else {
			buf.WriteByte(s[i])
			i++
		}
	}
	flush()
	return parts
}

func (p *parser) parseElement() (Node, error) {
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
		return p.parseSlotNode()
	}

	// ── svelte:fragment — transparent wrapper for named slot content ──────
	if tag == "svelte:fragment" {
		return p.parseSvelteFragment()
	}

	// ── Component detection ───────────────────────────────────────────────
	// Tags starting with an uppercase letter are treated as child components.
	if len(tag) > 0 && tag[0] >= 'A' && tag[0] <= 'Z' {
		return p.parseComponentNode(tag)
	}

	elem := &ElementNode{Tag: tag}

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
		elem.Children = groupIfChains(children)

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

	return elem, nil
}

// parseSlotNode parses a <slot name="header">fallback</slot> element.
func (p *parser) parseSlotNode() (Node, error) {
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
		return &SlotNode{Name: name}, nil
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
	return &SlotNode{Name: name, Fallback: groupIfChains(fallback)}, nil
}
func (p *parser) parseComponentNode(name string) (Node, error) {
	comp := &ComponentNode{Name: name, Slots: map[string][]Node{}}
	scratch := &ElementNode{}
	if err := p.parseAttrs(scratch); err != nil {
		return nil, err
	}
	// Transfer directive fields from scratch — parseAttrs puts #each/#key/#if
	// into the ElementNode fields, not into Attrs.
	comp.Attrs = scratch.Attrs
	comp.EachExpr = scratch.EachExpr
	comp.EachKey = scratch.EachKey
	comp.IfCond = scratch.IfCond

	if p.has("/>") {
		p.pos += 2
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
	children = groupIfChains(children)
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
		if !p.eof() && p.ch() == '=' {
			p.pos++
			p.skipWS()
			valuePos = p.pos
			value = p.readAttrValue()
		}

		// Classify and store
		attrCount := len(elem.Attrs)
		switch {
		case name == "#if":
			elem.IfCond = value
		case name == "#elseif":
			// Stored as a special sentinel attr; groupIfChains() picks this up.
			elem.Attrs = append(elem.Attrs, &Attr{Name: "__elseif__", Value: value})
		case name == "#else":
			elem.Attrs = append(elem.Attrs, &Attr{Name: "__else__", Value: ""})
		case name == "#each":
			elem.EachExpr = value
		case name == "#key":
			elem.EachKey = value
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
			elem.Attrs[len(elem.Attrs)-1].Pos = start
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
				a.Expr = newTemplateExpr(stripBraces(a.Value), exprPos)
			}
		}
	}
	return nil
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
	for p.pos < len(p.src) && depth > 0 {
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
		node, err := p.parseSvelteIfBlock(tag[3:], p.blockExprPos(tagStart, tag, strings.TrimSpace(tag[3:])))
		return node, "", err

	case strings.HasPrefix(tag, "#each"):
		node, err := p.parseSvelteEachBlock(tag[5:], p.blockExprPos(tagStart, tag, strings.TrimSpace(tag[5:])))
		return node, "", err

	case strings.HasPrefix(tag, "#key"):
		// {#key expr}...{/key} — re-create the children whenever
		// `expr` changes identity.  Modeled as a one-branch
		// IfChainNode with KeyExpr set; the codegen handles the
		// destroy-and-rebuild logic.
		node, err := p.parseSvelteKeyBlock(tag[4:], p.blockExprPos(tagStart, tag, strings.TrimSpace(tag[4:])))
		return node, "", err

	case strings.HasPrefix(tag, "#await"):
		// {#await promise}...{:then v}...{:catch e}...{/await}
		node, err := p.parseSvelteAwaitBlock(tag[6:], p.blockExprPos(tagStart, tag, strings.TrimSpace(tag[6:])))
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
func (p *parser) parseSvelteIfBlock(condRaw string, condPos int) (Node, error) {
	chain := &IfChainNode{}

	cond := strings.TrimSpace(condRaw)
	for {
		// Parse the body of this branch
		children, term, err := p.parseSvelteBlockBody()
		if err != nil {
			return nil, err
		}

		chain.Branches = append(chain.Branches, &IfBranch{
			Cond:     cond,
			CondPos:  condPos,
			Expr:     newTemplateExpr(cond, condPos),
			Children: groupIfChains(children),
		})

		switch {
		case strings.HasPrefix(term, ":else if ") || strings.HasPrefix(term, ":else if\t"):
			cond = strings.TrimSpace(term[9:])
			condPos = p.blockExprPos(p.lastTagStart, term, cond)
			// continue loop for next branch
		case term == ":else" || strings.HasPrefix(term, ":else}"):
			// Parse else body
			elseChildren, _, err2 := p.parseSvelteBlockBody()
			if err2 != nil {
				return nil, err2
			}
			chain.Branches = append(chain.Branches, &IfBranch{
				Cond:     "",
				Children: groupIfChains(elseChildren),
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

	// Extract optional (key) suffix
	key := ""
	keyPos := 0
	if idx := strings.LastIndex(raw, "("); idx >= 0 {
		close := strings.Index(raw[idx:], ")")
		if close >= 0 {
			key = strings.TrimSpace(raw[idx+1 : idx+close])
			keyPos = exprPos + idx + 1
			raw = strings.TrimSpace(raw[:idx])
		}
	}

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
		EachExpr: raw, ExprPos: exprPos, IterExpr: newTemplateExpr(strings.TrimSpace(strings.SplitN(raw, " as ", 2)[0]), exprPos), EachKey: key, KeyPos: keyPos, KeyAST: newTemplateExpr(key, keyPos),
		Children:     groupIfChains(children),
		ElseChildren: groupIfChains(elseChildren),
	}, nil
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
		KeyExpr: expr, KeyPos: keyPos, KeyAST: newTemplateExpr(expr, keyPos),
		Branches: []*IfBranch{{
			// Always-true condition.  The chain stays mounted; the
			// destroy-and-rebuild is driven by the key change, not by
			// the condition flipping.
			Cond:     "true",
			Children: groupIfChains(children),
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
// Either {:then} or {:catch} may be absent; in those cases the
// corresponding child slot is nil and codegen treats the branch as
// "do nothing on that state".  Order is fixed: pending body comes
// first, then optional :then, then optional :catch, then /await.
func (p *parser) parseSvelteAwaitBlock(raw string, exprPos int) (Node, error) {
	node := &AwaitBlockNode{
		PromiseExpr: strings.TrimSpace(raw),
		Expr:        newTemplateExpr(strings.TrimSpace(raw), exprPos),
		ExprPos:     exprPos,
	}

	// Pending body — consume until the first terminator.
	pending, term, err := p.parseSvelteBlockBody()
	if err != nil {
		return nil, err
	}
	node.PendingChildren = groupIfChains(pending)

	// :then [name]
	if strings.HasPrefix(term, ":then") {
		// Everything after ":then" up to the closing brace is the
		// binding name (may be empty).
		node.ValueName = strings.TrimSpace(strings.TrimPrefix(term, ":then"))
		// Strip any trailing '}' if it came back attached.
		node.ValueName = strings.TrimRight(node.ValueName, "}")
		node.ValueName = strings.TrimSpace(node.ValueName)
		thenChildren, t2, err := p.parseSvelteBlockBody()
		if err != nil {
			return nil, err
		}
		node.ThenChildren = groupIfChains(thenChildren)
		term = t2
	}

	// :catch [name]
	if strings.HasPrefix(term, ":catch") {
		node.ErrorName = strings.TrimSpace(strings.TrimPrefix(term, ":catch"))
		node.ErrorName = strings.TrimRight(node.ErrorName, "}")
		node.ErrorName = strings.TrimSpace(node.ErrorName)
		catchChildren, _, err := p.parseSvelteBlockBody()
		if err != nil {
			return nil, err
		}
		node.CatchChildren = groupIfChains(catchChildren)
	}

	return node, nil
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
func (p *parser) parseSvelteFragment() (Node, error) {
	elem := &ElementNode{Tag: "svelte:fragment"}
	if err := p.parseAttrs(elem); err != nil {
		return nil, err
	}
	if p.has("/>") {
		p.pos += 2
		return elem, nil
	}
	if !p.eof() && p.ch() == '>' {
		p.pos++
	}
	children, err := p.parseNodes([]string{"svelte:fragment"})
	if err != nil {
		return nil, err
	}
	elem.Children = groupIfChains(children)
	if p.has("</") {
		p.pos += 2
		for !p.eof() && p.ch() != '>' {
			p.pos++
		}
		if !p.eof() {
			p.pos++
		}
	}
	return elem, nil
}

// ─────────────────────────────────────────────────────────────────────────────
//  If-chain grouper
// ─────────────────────────────────────────────────────────────────────────────

// groupIfChains performs a second pass over a node list and merges consecutive
// #if / #elseif / #else elements into IfChainNode groups.
// Works for both ElementNode and ComponentNode.
func groupIfChains(nodes []Node) []Node {
	var out []Node
	i := 0
	for i < len(nodes) {
		// ── ElementNode #if ───────────────────────────────────────────────
		el, isEl := nodes[i].(*ElementNode)
		if isEl && el.IfCond != "" {
			chain := &IfChainNode{}
			chain.Branches = append(chain.Branches, &IfBranch{
				Cond:     el.IfCond,
				Children: groupIfChains(el.Children),
				Elem:     el,
			})
			i++
			for i < len(nodes) {
				sib, ok := nodes[i].(*ElementNode)
				if !ok {
					break
				}
				if hasMarker(sib, "__elseif__") {
					cond := markerVal(sib, "__elseif__")
					removeMarker(sib, "__elseif__")
					chain.Branches = append(chain.Branches, &IfBranch{
						Cond:     cond,
						Children: groupIfChains(sib.Children),
						Elem:     sib,
					})
					i++
				} else if hasMarker(sib, "__else__") {
					removeMarker(sib, "__else__")
					chain.Branches = append(chain.Branches, &IfBranch{
						Cond:     "",
						Children: groupIfChains(sib.Children),
						Elem:     sib,
					})
					i++
					break
				} else {
					break
				}
			}
			out = append(out, chain)
			continue
		}

		// ── ComponentNode #if ─────────────────────────────────────────────
		comp, isComp := nodes[i].(*ComponentNode)
		if isComp && comp.IfCond != "" {
			chain := &IfChainNode{}
			chain.Branches = append(chain.Branches, &IfBranch{
				Cond: comp.IfCond,
				Comp: comp,
			})
			i++
			for i < len(nodes) {
				sib, ok := nodes[i].(*ComponentNode)
				if !ok {
					break
				}
				if hasCompMarker(sib, "__elseif__") {
					cond := compMarkerVal(sib, "__elseif__")
					removeCompMarker(sib, "__elseif__")
					chain.Branches = append(chain.Branches, &IfBranch{
						Cond: cond,
						Comp: sib,
					})
					i++
				} else if hasCompMarker(sib, "__else__") {
					removeCompMarker(sib, "__else__")
					chain.Branches = append(chain.Branches, &IfBranch{
						Cond: "",
						Comp: sib,
					})
					i++
					break
				} else {
					break
				}
			}
			out = append(out, chain)
			continue
		}

		out = append(out, nodes[i])
		i++
	}
	return out
}

func hasMarker(el *ElementNode, marker string) bool {
	for _, a := range el.Attrs {
		if a.Name == marker {
			return true
		}
	}
	return false
}

func markerVal(el *ElementNode, marker string) string {
	for _, a := range el.Attrs {
		if a.Name == marker {
			return a.Value
		}
	}
	return ""
}

func removeMarker(el *ElementNode, marker string) {
	var kept []*Attr
	for _, a := range el.Attrs {
		if a.Name != marker {
			kept = append(kept, a)
		}
	}
	el.Attrs = kept
}

// ComponentNode equivalents
func hasCompMarker(c *ComponentNode, marker string) bool {
	for _, a := range c.Attrs {
		if a.Name == marker {
			return true
		}
	}
	return false
}

func compMarkerVal(c *ComponentNode, marker string) string {
	for _, a := range c.Attrs {
		if a.Name == marker {
			return a.Value
		}
	}
	return ""
}

func removeCompMarker(c *ComponentNode, marker string) {
	var kept []*Attr
	for _, a := range c.Attrs {
		if a.Name != marker {
			kept = append(kept, a)
		}
	}
	c.Attrs = kept
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
