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
}

// parseSFC splits a .vela source file into its three sections by scanning for
// <script>, <style>, and <template> open/close tags without using regexp.
// The scanner is case-insensitive and tolerates arbitrary tag attributes.
func parseSFC(src string) (*SFC, error) {
	sfc := &SFC{}

	// extractBlock finds the first occurrence of <tag …>…</tag> (case-insensitive)
	// and returns the inner text.
	extractBlock := func(tag string) string {
		low := strings.ToLower(src)
		openTag := "<" + tag
		closeTag := "</" + tag + ">"

		start := strings.Index(low, openTag)
		if start < 0 {
			return ""
		}
		// Advance past the opening tag's closing ">"
		gt := strings.Index(src[start:], ">")
		if gt < 0 {
			return ""
		}
		bodyStart := start + gt + 1

		closeIdx := strings.Index(strings.ToLower(src[bodyStart:]), closeTag)
		if closeIdx < 0 {
			return ""
		}
		return strings.TrimSpace(src[bodyStart : bodyStart+closeIdx])
	}

	sfc.Script = extractBlock("script")
	sfc.Style = extractBlock("style")
	sfc.Template = extractBlock("template")

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

	// Directives
	IfCond   string // #if="expr"
	EachExpr string // #each="items as item, i"
	EachKey  string // #key="expr"
}

// IfChainNode groups a #if element with its optional #elseif / #else siblings.
// This is produced by the post-parse grouper pass.
type IfChainNode struct {
	Branches []*IfBranch // [0]=if, [1..n-1]=elseif, last may have Cond==""(else)
}

type IfBranch struct {
	Cond     string       // empty string → #else branch
	Children []Node       // branch body (children of the original element)
	Elem     *ElementNode // non-nil for element branches
	Comp     *ComponentNode // non-nil for component branches (#if on a component)
}

// ComponentNode represents a child component invocation: <MyComp :prop="val" />
type ComponentNode struct {
	Name     string      // component name, e.g. "MyComp"
	Attrs    []*Attr     // :prop bindings and @event handlers
	Slots    map[string][]Node // slot name → children (default slot keyed as "default")
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

func (s *SlotNode) nodeKind() string { return "slot" }
type TextNode struct {
	Parts []*Part
}

// Part is one segment of a text node.
type Part struct {
	Static bool   // true → literal text; false → JS expression
	Value  string
}

// Attr holds one HTML attribute, annotated with its sigil role.
type Attr struct {
	Name    string // without the leading sigil
	Value   string
	IsEvent bool // @ prefix → addEventListener
	IsBind  bool // : prefix → dynamic attribute
}

func (e *ElementNode) nodeKind() string   { return "element" }
func (c *IfChainNode) nodeKind() string   { return "ifchain" }
func (c *ComponentNode) nodeKind() string { return "component" }
func (t *TextNode) nodeKind() string      { return "text" }

// ──────────────────────────────────────────────
//  Template Parser
// ──────────────────────────────────────────────

type parser struct {
	src string
	pos int
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
		p.skipWS()
		if p.eof() {
			break
		}

		// Peek: are we at a closing tag that matches one of our stoppers?
		if p.has("</") {
			inner := p.src[p.pos+2:]
			for _, stop := range stopTags {
				if strings.HasPrefix(strings.ToLower(inner), strings.ToLower(stop)) {
					return nodes, nil
				}
			}
			return nodes, nil // unknown closing tag — bubble up
		}

		if p.ch() == '<' {
			// HTML comment → skip
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
	for !p.eof() && p.ch() != '<' {
		buf.WriteByte(p.ch())
		p.pos++
	}
	raw := buf.String()
	parts := parseInterpolations(raw)
	if len(parts) == 0 {
		return nil, nil
	}
	return &TextNode{Parts: parts}, nil
}

// parseInterpolations splits a raw string into static and dynamic parts
// by scanning for {expr} sequences.
func parseInterpolations(s string) []*Part {
	var parts []*Part
	var buf strings.Builder

	flush := func() {
		if buf.Len() > 0 {
			text := buf.String()
			buf.Reset()
			// Collapse whitespace-only segments unless they're a single space
			trimmed := strings.TrimSpace(text)
			if trimmed == "" {
				// preserve a single space between words
				if strings.Contains(text, " ") {
					parts = append(parts, &Part{Static: true, Value: " "})
				}
				return
			}
			parts = append(parts, &Part{Static: true, Value: trimmed})
		}
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
				parts = append(parts, &Part{Static: false, Value: expr})
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
	comp.Attrs    = scratch.Attrs
	comp.EachExpr = scratch.EachExpr
	comp.EachKey  = scratch.EachKey
	comp.IfCond   = scratch.IfCond

	if p.has("/>") {
		p.pos += 2
		return comp, nil
	}
	if !p.eof() && p.ch() == '>'  {
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
		if el, ok := child.(*ElementNode); ok {
			slotName := ""
			for _, attr := range el.Attrs {
				if attr.Name == "slot" {
					slotName = attr.Value
					break
				}
			}
			if slotName != "" {
				var kept []*Attr
				for _, a := range el.Attrs {
					if a.Name != "slot" {
						kept = append(kept, a)
					}
				}
				el.Attrs = kept
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
		if !p.eof() && p.ch() == '=' {
			p.pos++
			p.skipWS()
			value = p.readAttrValue()
		}

		// Classify and store
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
		case strings.HasPrefix(name, ":"):
			elem.Attrs = append(elem.Attrs, &Attr{Name: name[1:], Value: value, IsBind: true})
		default:
			elem.Attrs = append(elem.Attrs, &Attr{Name: name, Value: value})
		}
	}
	return nil
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
	// Unquoted
	start := p.pos
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
