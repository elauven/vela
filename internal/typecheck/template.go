package typecheck

import (
	"strconv"
	"strings"

	"github.com/elauven/vela/internal/sfcast"
)

// The lexical reading of a template: the {…} of text and blocks, the tags
// with their attributes, and the slot="…" of the children of components.

type attrKind uint8

const (
	attrBare   attrKind = iota // name
	attrStatic                 // name="text"
	attrExpr                   // name={expr}, a dynamic name="expr", {name}
)

type tagAttr struct {
	name               string
	nameStart, nameEnd int
	kind               attrKind
	valStart, valEnd   int // the text of a static value, the expression
	shorthand          bool
}

type templateTag struct {
	name      string
	nameStart int
	attrs     []tagAttr
	id        int // the const of a component tag: $$c<id>
}

// templateItem is one thing the render function writes, in source order.
type templateItem struct {
	// region is a {…} outside tags (text, blocks), or an expression
	// attribute of an element; tag and attr name its attribute.
	region    *templateRegion
	component *templateTag // a component tag or <vela:component>
	// slot is the slot="…" of a child of the component parent.
	slot   *tagAttr
	parent *templateTag
}

// templateRegion is a {…} of the template or the value of a dynamic
// attribute of an element: [start, end) is its content.
type templateRegion struct {
	start, end int
	tag, attr  string
}

func isComponentTag(name string) bool {
	return name == "vela:component" || name != "" && name[0] >= 'A' && name[0] <= 'Z'
}

// tagName is the name of the tag opened at the < at start.
func tagName(src string, start int) string {
	end := start + 1
	for end < len(src) && (isIdentByte(src[end]) || src[end] == ':' || src[end] == '-' || src[end] == '.') {
		end++
	}
	return src[start+1 : end]
}

// isDynamicAttr reports an attribute whose value is an expression.
func isDynamicAttr(name string) bool {
	for _, prefix := range []string{":", "@", "bind:", "class:", "style:"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// typedEnd is the end of an expression being typed that has no closing
// brace: the next tag or line.
func typedEnd(src string, from, end int) int {
	for from < end && src[from] != '<' && src[from] != '\n' {
		from++
	}
	return from
}

// scanTemplate reads the template body [start, end).
func scanTemplate(src string, start, end int) []templateItem {
	var items []templateItem
	var open []*templateTag // component tags whose children follow
	components := 0
	for i := start; i < end; i++ {
		c := src[i]
		switch {
		case strings.HasPrefix(src[i:], "<!--"):
			if j := strings.Index(src[i:end], "-->"); j >= 0 {
				i += j + 2
			} else {
				i = end
			}
		case c == '<' && i+1 < end && src[i+1] == '/':
			name := tagName(src, i+1)
			for k := len(open) - 1; k >= 0; k-- {
				if open[k].name == name {
					open = open[:k]
					break
				}
			}
			if j := strings.IndexByte(src[i:end], '>'); j >= 0 {
				i += j
			} else {
				i = end
			}
		case c == '<' && i+1 < end && isIdentByte(src[i+1]):
			name := tagName(src, i)
			tag := &templateTag{name: name, nameStart: i + 1}
			j, selfClosing := scanAttrs(src, i+1+len(name), end, tag)
			var parent *templateTag
			if len(open) > 0 {
				parent = open[len(open)-1]
			}
			for k := range tag.attrs {
				if a := &tag.attrs[k]; a.name == "slot" && a.kind == attrStatic && parent != nil {
					items = append(items, templateItem{slot: a, parent: parent})
				}
			}
			if isComponentTag(name) {
				components++
				tag.id = components
				items = append(items, templateItem{component: tag})
				if !selfClosing {
					open = append(open, tag)
				}
			} else {
				for _, a := range tag.attrs {
					if a.kind != attrExpr {
						continue
					}
					r := &templateRegion{start: a.valStart, end: a.valEnd, tag: name, attr: a.name}
					if a.shorthand {
						r.tag, r.attr = "", ""
					}
					items = append(items, templateItem{region: r})
				}
			}
			i = j
		case c == '{':
			close := matchBrace(src, i, end)
			r := &templateRegion{start: i + 1, end: close}
			if close == end || src[close] != '}' {
				// An expression being typed ends at the next tag or line.
				r.end = typedEnd(src, i+1, end)
				close = r.end - 1
			}
			items = append(items, templateItem{region: r})
			i = close
		}
	}
	return items
}

// scanAttrs reads the attributes of a tag from at to its '>', the offset
// of which it returns, and whether the tag closes itself.
func scanAttrs(src string, at, end int, tag *templateTag) (int, bool) {
	j := at
	for j < end {
		c := src[j]
		switch {
		case isSpaceByte(c):
			j++
			continue
		case c == '>':
			return j, false
		case c == '/' && j+1 < end && src[j+1] == '>':
			return j + 1, true
		case c == '/':
			j++
			continue
		case c == '<':
			// A tag left open while typing ends at the next tag.
			return j - 1, false
		case c == '{':
			close := matchBrace(src, j, end)
			a := tagAttr{kind: attrExpr, valStart: j + 1, valEnd: close, shorthand: true}
			if close == end || src[close] != '}' {
				a.valEnd = typedEnd(src, j+1, end)
				close = a.valEnd - 1
			}
			a.name = strings.TrimSpace(src[a.valStart:a.valEnd])
			a.nameStart, a.nameEnd = a.valStart, a.valEnd
			tag.attrs = append(tag.attrs, a)
			j = close + 1
			continue
		}
		ns := j
		for j < end && !isSpaceByte(src[j]) && !strings.ContainsRune("=>/{<", rune(src[j])) {
			j++
		}
		a := tagAttr{name: src[ns:j], nameStart: ns, nameEnd: j}
		k := j
		for k < end && isSpaceByte(src[k]) {
			k++
		}
		if k < end && src[k] == '=' {
			k++
			for k < end && isSpaceByte(src[k]) {
				k++
			}
			switch {
			case k < end && (src[k] == '"' || src[k] == '\''):
				q := strings.IndexByte(src[k+1:end], src[k])
				if q < 0 {
					q = end - k - 1
				}
				a.kind, a.valStart, a.valEnd = attrStatic, k+1, k+1+q
				if isDynamicAttr(a.name) {
					a.kind = attrExpr
				}
				j = min(k+2+q, end)
			case k < end && src[k] == '{':
				close := matchBrace(src, k, end)
				a.kind, a.valStart, a.valEnd = attrExpr, k+1, close
				if close == end || src[close] != '}' {
					a.valEnd = typedEnd(src, k+1, end)
					close = a.valEnd - 1
				}
				j = close + 1
			default:
				v := k
				for v < end && !isSpaceByte(src[v]) && src[v] != '>' {
					v++
				}
				a.kind, a.valStart, a.valEnd = attrStatic, k, v
				j = v
			}
		}
		tag.attrs = append(tag.attrs, a)
	}
	return end, false
}

// writeTemplate writes the render function body of the template [start, end).
func writeTemplate(w *writer, start, end int) {
	src := w.src
	type open struct {
		kind         string
		promiseStart int // #await: the promise expression
		promiseEnd   int
	}
	var stack []open
	closeTo := func(kind string) {
		for i := len(stack) - 1; i >= 0; i-- {
			if stack[i].kind == kind {
				for len(stack) > i {
					stack = stack[:len(stack)-1]
					w.gen("}\n")
				}
				return
			}
		}
	}
	top := func() string {
		if len(stack) == 0 {
			return ""
		}
		return stack[len(stack)-1].kind
	}
	declared := map[int]bool{} // component tags whose $$c const is in scope
	for _, item := range scanTemplate(src, start, end) {
		switch {
		case item.component != nil:
			writeComponent(w, item.component)
			declared[item.component.id] = true
			continue
		case item.slot != nil:
			a := item.slot
			if declared[item.parent.id] {
				w.anchored(a.valStart, a.valEnd, func() {
					w.gen("$$slot($$c" + strconv.Itoa(item.parent.id) + ", ")
					w.name(a.valStart, a.valEnd, true)
					w.gen(");\n")
				})
			}
			continue
		}
		r := *item.region
		content := src[r.start:r.end]
		trimmed := strings.TrimSpace(content)
		switch {
		case r.attr != "":
			writeAttr(w, r)
		case strings.HasPrefix(trimmed, "#"), strings.HasPrefix(trimmed, ":"), strings.HasPrefix(trimmed, "/"):
			at := r.start + strings.Index(content, trimmed)
			keyword := blockKeyword(trimmed)
			rest := at + len(keyword) // the block's expression
			switch keyword {
			case "#if":
				w.gen("if (")
				w.copyTrimmed(rest, r.end)
				w.gen(") {\n")
				stack = append(stack, open{kind: "if"})
			case ":else":
				if strings.HasPrefix(strings.TrimSpace(src[rest:r.end]), "if") {
					cond := rest + strings.Index(src[rest:r.end], "if") + 2
					w.gen("} else if (")
					w.copyTrimmed(cond, r.end)
					w.gen(") {\n")
				} else if top() == "if" {
					w.gen("} else {\n")
				} else if top() != "" {
					w.gen("} {\n")
				}
			case "#each":
				writeEach(w, rest, r.end)
				stack = append(stack, open{kind: "each"})
			case "#await":
				p := strings.TrimSpace(src[rest:r.end])
				pStart := rest + strings.Index(src[rest:r.end], p)
				pEnd := pStart + len(p)
				name, nameStart := "", 0
				for _, branch := range []string{" then ", " catch "} {
					if i := strings.Index(p, branch); i >= 0 {
						name = strings.TrimSpace(p[i+len(branch):])
						nameStart = pStart + i + len(branch) + strings.Index(p[i+len(branch):], name)
						pEnd = pStart + i
						if branch == " catch " {
							name = "catch:" + name
						}
						break
					}
				}
				w.gen("{ (")
				w.copyTrimmed(pStart, pEnd)
				w.gen(");\n")
				stack = append(stack, open{kind: "await", promiseStart: pStart, promiseEnd: pEnd})
				if name != "" {
					writeAwaitName(w, src, strings.TrimPrefix(name, "catch:"), nameStart, strings.HasPrefix(name, "catch:"), pStart, pEnd)
				}
			case ":then", ":catch":
				if keyword == ":catch" && top() == "boundary" {
					w.gen("} {\n")
					writeBoundaryNames(w, rest, r.end)
					continue
				}
				if top() != "await" {
					continue
				}
				o := stack[len(stack)-1]
				w.gen("} {\n")
				name := strings.TrimSpace(src[rest:r.end])
				if name != "" {
					writeAwaitName(w, src, name, rest+strings.Index(src[rest:r.end], name), keyword == ":catch", o.promiseStart, o.promiseEnd)
				}
			case "#key":
				w.gen("{ (")
				w.copyTrimmed(rest, r.end)
				w.gen(");\n")
				stack = append(stack, open{kind: "key"})
			case "#client":
				w.gen("{\n")
				stack = append(stack, open{kind: "client"})
			case "#boundary":
				w.gen("{\n")
				stack = append(stack, open{kind: "boundary"})
			case "/if", "/each", "/await", "/key", "/client", "/boundary":
				closeTo(keyword[1:])
			}
		case strings.HasPrefix(trimmed, "@"):
			// {@html …} and other tags Vela does not support.
		default:
			w.gen("(")
			w.copyTrimmed(r.start, r.end)
			w.gen(");\n")
		}
	}
	for range stack {
		w.gen("}\n")
	}
}

// writeAttr writes the expression of a dynamic attribute of an element: an
// event handler gets its event type from $$on.
func writeAttr(w *writer, r templateRegion) {
	if sfcast.IsGlobalTag(r.tag) {
		// <vela:window> and <vela:document> (task 095): a handler gets the
		// event of the target, a binding assigns the value of the target.
		if strings.HasPrefix(r.attr, "@") {
			helper := "$$onWindow"
			if r.tag == sfcast.DocumentTag {
				helper = "$$onDocument"
			}
			w.gen(helper + "(\"" + r.attr[1:] + "\", ")
			w.copyTrimmed(r.start, r.end)
			w.gen(");\n")
			return
		}
		if binding, ok := sfcast.GlobalBindings[r.tag][strings.TrimPrefix(r.attr, "bind:")]; ok && strings.HasPrefix(r.attr, "bind:") {
			w.gen("(($$v: " + binding.Type + ") => { ")
			w.copyTrimmed(r.start, r.end)
			w.gen(" = $$v; });\n")
			return
		}
	}
	if strings.HasPrefix(r.attr, "@") {
		event := strings.SplitN(r.attr[1:], ".", 2)[0]
		w.gen("$$on(\"" + event + "\", ")
		w.copyTrimmed(r.start, r.end)
		w.gen(");\n")
		return
	}
	w.gen("(")
	w.copyTrimmed(r.start, r.end)
	w.gen(");\n")
}

// writeComponent checks a component tag against the component's type: the
// props are an argument of its call signature (required, unknown and
// mistyped props, type arguments of a generic component), the events
// handlers get their detail, bind: needs a bindable prop. A
// <vela:component> checks its props against every candidate (TS-4.10).
func writeComponent(w *writer, t *templateTag) {
	id := "$$c" + strconv.Itoa(t.id)
	dynamic := t.name == "vela:component"
	nameEnd := t.nameStart + len(t.name)
	w.gen("const " + id + " = ")
	switch {
	case !dynamic:
		w.copy(t.nameStart, nameEnd)
	default:
		this := -1
		for k, a := range t.attrs {
			if (a.name == ":this" || a.name == "this") && a.kind == attrExpr {
				this = k
			}
		}
		if this < 0 {
			w.gen("null")
		} else {
			a := t.attrs[this]
			w.gen("$$dynamicValue(")
			w.copyTrimmed(a.valStart, a.valEnd)
			w.gen(")")
		}
	}
	w.gen(";\n")
	w.anchored(t.nameStart, nameEnd, func() {
		if dynamic {
			w.gen("$$dynamic(" + id + ", {")
		} else {
			w.gen(id + "($$target, {")
		}
		for _, a := range t.attrs {
			key, keyStart, keyEnd := propOf(a)
			if key == "" {
				continue
			}
			w.gen(" ")
			if a.shorthand {
				w.gen(quote(key))
			} else {
				w.name(keyStart, keyEnd, false)
			}
			w.gen(": ")
			switch a.kind {
			case attrBare:
				w.gen("true")
			case attrStatic:
				w.anchored(a.valStart, a.valEnd, func() { w.gen(quote(w.src[a.valStart:a.valEnd])) })
			default:
				w.gen("(")
				w.copyTrimmed(a.valStart, a.valEnd)
				w.gen(")")
			}
			w.gen(",")
		}
		w.gen(" });\n")
	})
	for _, a := range t.attrs {
		switch {
		case strings.HasPrefix(a.name, "bind:") && a.name != "bind:this":
			w.gen("$$bind(" + id + ", ")
			w.name(a.nameStart+len("bind:"), a.nameEnd, true)
			w.gen(");\n")
		case strings.HasPrefix(a.name, "@") && a.kind == attrExpr:
			from := a.nameStart + 1
			if strings.HasPrefix(a.name, "@on:") {
				from = a.nameStart + len("@on:")
			}
			to := from + len(strings.SplitN(w.src[from:a.nameEnd], ".", 2)[0])
			w.gen("$$event(" + id + ", ")
			w.name(from, to, true)
			w.gen(", ")
			w.copyTrimmed(a.valStart, a.valEnd)
			w.gen(");\n")
		}
	}
}

// propOf is the prop an attribute of a component tag sets, with the range
// of its name; "" for attributes that set none.
func propOf(a tagAttr) (string, int, int) {
	n := a.name
	switch {
	case a.shorthand:
		if isIdentifier(n) {
			return n, a.nameStart, a.nameEnd
		}
		return "", 0, 0
	case n == "" || n == "slot" || n == "this" || n == ":this" || n == "bind:this":
		return "", 0, 0
	case strings.HasPrefix(n, "bind:"):
		return n[len("bind:"):], a.nameStart + len("bind:"), a.nameEnd
	case strings.HasPrefix(n, ":"):
		return n[1:], a.nameStart + 1, a.nameEnd
	case strings.HasPrefix(n, "@"), strings.HasPrefix(n, "class:"), strings.HasPrefix(n, "style:"), strings.Contains(n, ":"):
		return "", 0, 0
	}
	return n, a.nameStart, a.nameEnd
}

// Tag is a component tag of a template, read lexically: the language
// server finds the parents' attributes of a prop with it.
type Tag struct {
	Name      string
	NameStart int
	Attrs     []TagAttr
}

// TagAttr is an attribute of a component tag that sets a prop: Prop and its
// range in the source; a shorthand {title} spans [Start, End) with its
// braces.
type TagAttr struct {
	Prop               string
	PropStart, PropEnd int
	Shorthand          bool
	Start, End         int
}

// ComponentTags are the component tags of the template of a component
// source (not <vela:component>).
func ComponentTags(src string) []Tag {
	start, end, ok := sectionBody(src, "template")
	if !ok {
		return nil
	}
	var out []Tag
	for _, item := range scanTemplate(src, start, end) {
		t := item.component
		if t == nil || t.name == "vela:component" {
			continue
		}
		tag := Tag{Name: t.name, NameStart: t.nameStart}
		for _, a := range t.attrs {
			prop, ps, pe := propOf(a)
			if prop == "" {
				continue
			}
			ta := TagAttr{Prop: prop, PropStart: ps, PropEnd: pe, Shorthand: a.shorthand}
			if a.shorthand {
				ta.Start, ta.End = a.valStart-1, min(a.valEnd+1, len(src))
			}
			tag.Attrs = append(tag.Attrs, ta)
		}
		out = append(out, tag)
	}
	return out
}
