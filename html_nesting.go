package main

import (
	"fmt"
	"strings"
)

// Parse stability of server HTML (docs/HYDRATION_ARCHITECTURE.md §5.2).
// The client builds DOM with createElement, which accepts any nesting; the
// browser's HTML parser does not, so server HTML for some templates parses
// into a different node sequence. The compiler normalises the fixed cases
// (M-5.7) in the template both outputs are built from, and rejects the rest
// (M-5.8, M-5.9) before writing a server renderer.

// HTMLNestingDiagnosticCode identifies a template the HTML parser would
// rearrange.
const HTMLNestingDiagnosticCode = "VELA-HTML-NESTING"

// HTMLNestingError is one rejected construct with its source position and a
// fix.
type HTMLNestingError struct {
	Pos     int
	Message string
	Fix     string
}

func (e *HTMLNestingError) Error() string {
	return HTMLNestingDiagnosticCode + ": " + e.Message + "; " + e.Fix
}

// normalizeTableRows inserts the <tbody> the HTML parser creates around
// rows written directly in a <table> (M-5.7). A run starts at the first row
// or block after a table section and keeps the whitespace that follows it,
// as the parser does. It is idempotent.
func normalizeTableRows(nodes []Node) {
	for _, node := range nodes {
		forEachChildList(node, normalizeTableRows)
		element, ok := node.(*ElementNode)
		if !ok || element.Tag != "table" {
			continue
		}
		var children []Node
		var run []Node
		flush := func() {
			if len(run) > 0 {
				children = append(children, &ElementNode{Span: SectionSpan{Start: templateNodeSpan(run[0]).Start, End: templateNodeSpan(run[len(run)-1]).End}, Tag: "tbody", Children: run})
			}
			run = nil
		}
		for _, child := range element.Children {
			if el, ok := child.(*ElementNode); ok && tableSectionTag(el.Tag) {
				flush()
				children = append(children, child)
				continue
			}
			if text, ok := child.(*TextNode); ok && len(run) == 0 && textNodeIsBlank(text) {
				children = append(children, child)
				continue
			}
			run = append(run, child)
		}
		flush()
		element.Children = children
	}
}

func tableSectionTag(tag string) bool {
	switch tag {
	case "caption", "colgroup", "col", "thead", "tbody", "tfoot", "script", "template", "style":
		return true
	}
	return false
}

// HTMLNestingContext supplies what a check needs beyond the template: the
// root element tag of a child component ("div" for a display:contents
// wrapper), or "" when it is unknown.
type HTMLNestingContext struct {
	ComponentRoot func(*ComponentNode) string
}

// checkHTMLNesting reports the first construct the HTML parser would
// rearrange (M-5.8) or a text-only element with more than one text run or a
// block (M-5.9).
func checkHTMLNesting(nodes []Node, ctx HTMLNestingContext) *HTMLNestingError {
	return checkNestingNodes(nodes, "", nil, ctx)
}

func checkNestingNodes(nodes []Node, parent string, ancestors []string, ctx HTMLNestingContext) *HTMLNestingError {
	for _, node := range nodes {
		if err := checkNestingNode(node, parent, ancestors, ctx); err != nil {
			return err
		}
	}
	return nil
}

func checkNestingNode(node Node, parent string, ancestors []string, ctx HTMLNestingContext) *HTMLNestingError {
	switch n := node.(type) {
	case *TextNode:
		if tableContextTag(parent) && (!textNodeAllStatic(n) || !textNodeIsBlank(n)) {
			return &HTMLNestingError{Pos: n.Span.Start,
				Message: fmt.Sprintf("text directly inside <%s> is moved before the table by the HTML parser", parent),
				Fix:     "put it in a " + tableCellHint(parent)}
		}
		return nil
	case *ElementNode:
		if err := checkNestingTag(n.Tag, n.Span.Start, parent, ancestors); err != nil {
			return err
		}
		if isVoidElement(n.Tag) && len(n.Children) > 0 {
			return &HTMLNestingError{Pos: n.Span.Start,
				Message: fmt.Sprintf("<%s> is a void element and cannot have children", n.Tag),
				Fix:     "move the children after it"}
		}
		if textOnlyTag(n.Tag) {
			if len(n.Children) > 1 {
				return &HTMLNestingError{Pos: n.Span.Start,
					Message: fmt.Sprintf("<%s> holds one text run only", n.Tag),
					Fix:     "compute the content as one expression"}
			}
			if len(n.Children) == 1 {
				if _, ok := n.Children[0].(*TextNode); !ok {
					return &HTMLNestingError{Pos: n.Span.Start,
						Message: fmt.Sprintf("<%s> holds text only; elements and blocks inside it are parsed as text", n.Tag),
						Fix:     "compute the content as one expression"}
				}
			}
			return nil
		}
		return checkNestingNodes(n.Children, n.Tag, append(ancestors[:len(ancestors):len(ancestors)], n.Tag), ctx)
	case *ComponentNode:
		if ctx.ComponentRoot != nil {
			if root := ctx.ComponentRoot(n); root != "" {
				if err := checkNestingTag(root, n.Span.Start, parent, ancestors); err != nil {
					err.Message = fmt.Sprintf("<%s> renders a <%s> root: %s", n.Name, root, err.Message)
					return err
				}
			}
		}
		// Slot content is placed inside the child's DOM, whose parent is
		// unknown here; ancestor rules still apply.
		for _, children := range n.Slots {
			if err := checkNestingNodes(children, "", ancestors, ctx); err != nil {
				return err
			}
		}
		return nil
	case *SlotNode:
		if tableContextTag(parent) {
			return &HTMLNestingError{Pos: n.Span.Start,
				Message: fmt.Sprintf("a slot directly inside <%s> can receive content the HTML parser moves before the table", parent),
				Fix:     "put the slot in a " + tableCellHint(parent)}
		}
		return checkNestingNodes(n.Fallback, parent, ancestors, ctx)
	}
	var err *HTMLNestingError
	forEachChildList(node, func(children []Node) {
		if err == nil {
			err = checkNestingNodes(children, parent, ancestors, ctx)
		}
	})
	return err
}

// checkNestingTag checks an element (or a component root element) with tag
// placed under parent inside ancestors.
func checkNestingTag(tag string, pos int, parent string, ancestors []string) *HTMLNestingError {
	if tableContextTag(parent) && !tableChildAllowed(parent, tag) {
		return &HTMLNestingError{Pos: pos,
			Message: fmt.Sprintf("<%s> directly inside <%s> is moved before the table by the HTML parser", tag, parent),
			Fix:     "put it in a " + tableCellHint(parent)}
	}
	for _, ancestor := range ancestors {
		if ancestor == "option" {
			return &HTMLNestingError{Pos: pos,
				Message: fmt.Sprintf("<%s> inside <option> is dropped by the HTML parser", tag),
				Fix:     "use text only inside <option>"}
		}
	}
	switch tag {
	case "a", "form", "button":
		for _, ancestor := range ancestors {
			if ancestor == tag {
				return &HTMLNestingError{Pos: pos,
					Message: fmt.Sprintf("<%s> inside <%s> is split by the HTML parser", tag, tag),
					Fix:     fmt.Sprintf("do not nest <%s> elements", tag)}
			}
		}
	}
	if closesParagraph(tag) && paragraphInScope(ancestors) {
		return &HTMLNestingError{Pos: pos,
			Message: fmt.Sprintf("<%s> inside <p> closes the paragraph in the HTML parser", tag),
			Fix:     "use a <div> instead of the <p>, or an inline element instead of the <" + tag + ">"}
	}
	return nil
}

func tableContextTag(tag string) bool {
	switch tag {
	case "table", "thead", "tbody", "tfoot", "tr":
		return true
	}
	return false
}

func tableChildAllowed(parent, tag string) bool {
	switch tag {
	case "script", "template":
		return true
	}
	switch parent {
	case "table":
		return tag == "caption" || tag == "colgroup" || tag == "thead" || tag == "tbody" || tag == "tfoot" || tag == "style"
	case "tr":
		return tag == "td" || tag == "th"
	default:
		return tag == "tr"
	}
}

func tableCellHint(parent string) string {
	switch parent {
	case "tr":
		return "<td> or <th>"
	case "table":
		return "<caption>, or a <td> inside a row"
	default:
		return "<td> inside a <tr>"
	}
}

// closesParagraph lists the start tags that close an open <p>.
func closesParagraph(tag string) bool {
	switch tag {
	case "address", "article", "aside", "blockquote", "details", "dialog", "div", "dl", "fieldset",
		"figcaption", "figure", "footer", "form", "h1", "h2", "h3", "h4", "h5", "h6", "header",
		"hgroup", "hr", "main", "menu", "nav", "ol", "p", "pre", "search", "section", "table", "ul":
		return true
	}
	return false
}

// paragraphInScope reports whether an open <p> is in button scope.
func paragraphInScope(ancestors []string) bool {
	for i := len(ancestors) - 1; i >= 0; i-- {
		switch ancestors[i] {
		case "p":
			return true
		case "button", "table", "td", "th", "caption", "template", "object", "marquee", "applet", "html":
			return false
		}
	}
	return false
}

func textOnlyTag(tag string) bool {
	switch tag {
	case "textarea", "title", "script", "style":
		return true
	}
	return false
}

func textNodeIsBlank(text *TextNode) bool {
	for _, part := range text.Parts {
		if !part.Static || strings.TrimSpace(part.Value) != "" {
			return false
		}
	}
	return true
}

// forEachChildList visits the child lists of a structural node whose
// children land in the same parent element.
func forEachChildList(node Node, visit func([]Node)) {
	switch n := node.(type) {
	case *ElementNode:
		visit(n.Children)
	case *IfChainNode:
		for _, branch := range n.Branches {
			if branch != nil {
				visit(branch.Children)
			}
		}
	case *EachBlockNode:
		visit(n.Children)
		visit(n.ElseChildren)
	case *AwaitBlockNode:
		visit(n.PendingChildren)
		visit(n.ThenChildren)
		visit(n.CatchChildren)
	case *ComponentNode:
		for _, children := range n.Slots {
			visit(children)
		}
	case *SlotNode:
		visit(n.Fallback)
	}
}

func templateNodeSpan(node Node) SectionSpan {
	switch n := node.(type) {
	case *ElementNode:
		return n.Span
	case *TextNode:
		return n.Span
	case *IfChainNode:
		return n.Span
	case *EachBlockNode:
		return n.Span
	case *AwaitBlockNode:
		return n.Span
	case *ComponentNode:
		return n.Span
	case *SlotNode:
		return n.Span
	}
	return SectionSpan{}
}
