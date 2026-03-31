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
}

type IfBranch struct {
	Cond     string         // empty string → #else branch
	Children []Node         // branch body
	Elem     *ElementNode   // non-nil for element branches (old attr syntax)
	Comp     *ComponentNode // non-nil for component branches
}

// EachBlockNode is produced by Svelte-style {#each items as item (key)}...{/each}.
// The children are the content of each row; they may include multiple nodes.
type EachBlockNode struct {
	EachExpr string // e.g. "items as item" or "items as item, index"
	EachKey  string // e.g. "item.id"  (from the (key) suffix)
	Children []Node
}

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
		// Stop before Svelte block directives so parseNodes can handle them
		if p.isSvelteBlockStart() || p.isSvelteBlockTerm() {
			break
		}
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
//  Svelte-style block directives: {#if}, {#each}, {:else}, {/if}, {/each}
// ─────────────────────────────────────────────────────────────────────────────

// isSvelteBlockStart reports whether the parser is at a {#if or {#each opener.
func (p *parser) isSvelteBlockStart() bool {
	if p.pos+1 >= len(p.src) || p.src[p.pos] != '{' {
		return false
	}
	rest := p.src[p.pos+1:]
	return strings.HasPrefix(rest, "#if ") || strings.HasPrefix(rest, "#if\t") ||
		strings.HasPrefix(rest, "#if}") ||
		strings.HasPrefix(rest, "#each ") || strings.HasPrefix(rest, "#each\t")
}

// isSvelteBlockTerm reports whether the parser is at {:else}, {:else if}, {/if}, {/each}.
func (p *parser) isSvelteBlockTerm() bool {
	if p.pos+1 >= len(p.src) || p.src[p.pos] != '{' {
		return false
	}
	rest := p.src[p.pos+1:]
	return strings.HasPrefix(rest, ":else") ||
		strings.HasPrefix(rest, "/if") ||
		strings.HasPrefix(rest, "/each")
}

// readSvelteTag reads from current '{' to the closing '}', returning the inner text.
func (p *parser) readSvelteTag() string {
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
func (p *parser) parseSvelteBlock() (Node, string, error) {
	tag := p.readSvelteTag() // e.g. "#if cond", "#each items as item (key)"

	switch {
	case strings.HasPrefix(tag, "#if"):
		node, err := p.parseSvelteIfBlock(tag[3:])
		return node, "", err

	case strings.HasPrefix(tag, "#each"):
		node, err := p.parseSvelteEachBlock(tag[5:])
		return node, "", err

	case strings.HasPrefix(tag, ":else") || strings.HasPrefix(tag, "/if") || strings.HasPrefix(tag, "/each"):
		// Terminator — caller should have caught this via isSvelteBlockTerm
		return nil, tag, nil

	default:
		return nil, "", nil
	}
}

// parseSvelteIfBlock parses {#if cond}...{:else if cond}...{:else}...{/if}.
// condRaw is everything after "#if" (with leading space).
func (p *parser) parseSvelteIfBlock(condRaw string) (Node, error) {
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
			Children: groupIfChains(children),
		})

		switch {
		case strings.HasPrefix(term, ":else if ") || strings.HasPrefix(term, ":else if\t"):
			cond = strings.TrimSpace(term[9:])
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
func (p *parser) parseSvelteEachBlock(raw string) (Node, error) {
	raw = strings.TrimSpace(raw)

	// Extract optional (key) suffix
	key := ""
	if idx := strings.LastIndex(raw, "("); idx >= 0 {
		close := strings.Index(raw[idx:], ")")
		if close >= 0 {
			key = strings.TrimSpace(raw[idx+1 : idx+close])
			raw = strings.TrimSpace(raw[:idx])
		}
	}

	children, _, err := p.parseSvelteBlockBody()
	if err != nil {
		return nil, err
	}

	return &EachBlockNode{
		EachExpr: raw,
		EachKey:  key,
		Children: groupIfChains(children),
	}, nil
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
