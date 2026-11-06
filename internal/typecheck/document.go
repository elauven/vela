// Package typecheck presents Vela components to TypeScript (tasks 065c,
// 084, docs/TYPESCRIPT.md TS-1.5, TS-6.4): a virtual document per
// component, the helper process that runs TypeScript over them, and the
// type check of a project (`vela check --types`). The language server
// uses the same documents for its JavaScript features.
package typecheck

import (
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/elauven/vela/internal/analyzer"
	"github.com/elauven/vela/internal/jsast"
	"github.com/elauven/vela/internal/sfcast"
)

// A virtual document is what TypeScript sees of a component: the <script>
// text as written, then the template inside a render function whose blocks
// declare the {#each} and {#await} names and whose statements check the
// expressions, the child component tags (props, events, bind:, slots) and
// <vela:component>, then the component's export, typed by its props,
// events, slots and bindable props (analyzer.ComponentTypes).
//
// A TypeScript component (`lang="ts"`) has a `.ts` document, App.vela.ts;
// a JavaScript one a `.js` document, checked as TypeScript checks
// JavaScript (`// @ts-check`, `checkJs`).
//
// Text copied from the component is mapped in both directions; generated
// text is not, except through anchors: a generated range whose diagnostics
// belong to a range of the component (the props object of a tag reports a
// missing prop at the tag name). The template is read lexically, like the
// cursor context of the language server, so a buffer that does not parse
// still has a document.

// segment is text copied from the component: n bytes at src in the
// component and at virt in the virtual document.
type segment struct{ src, virt, n int }

// anchor maps the generated range [virt, virtEnd) to the component range
// [src, srcEnd) for diagnostics.
type anchor struct{ virt, virtEnd, src, srcEnd int }

// Document is the virtual document of a component.
type Document struct {
	Path       string // the component
	Source     string // the component's text
	Text       string
	TypeScript bool
	bySrc      []segment
	byVirt     []segment
	anchors    []anchor
}

// VirtualPath names the virtual document of a component: App.vela.ts for a
// TypeScript component, App.vela.js for a JavaScript one.
func VirtualPath(component string, ts bool) string {
	if ts {
		return component + ".ts"
	}
	return component + ".js"
}

// VirtualPath is the file name of the document.
func (d *Document) VirtualPath() string { return VirtualPath(d.Path, d.TypeScript) }

// IsVirtualPath reports a virtual document's file name, and its component.
func IsVirtualPath(file string) (string, bool) {
	for _, ext := range []string{".vela.ts", ".vela.js"} {
		if strings.HasSuffix(file, ext) {
			return strings.TrimSuffix(file, ext[len(".vela"):]), true
		}
	}
	return "", false
}

// ToVirtual maps a component offset; ok is false outside copied text. An
// offset at the end of a segment maps, so a word being typed at the end of
// an expression has a position.
func (d *Document) ToVirtual(off int) (int, bool) {
	i := sort.Search(len(d.bySrc), func(i int) bool { return d.bySrc[i].src+d.bySrc[i].n >= off })
	for ; i < len(d.bySrc) && d.bySrc[i].src <= off; i++ {
		if s := d.bySrc[i]; off <= s.src+s.n {
			return s.virt + off - s.src, true
		}
	}
	return 0, false
}

// segmentAt is the copied segment holding the virtual offset off.
func (d *Document) segmentAt(off int) (segment, bool) {
	i := sort.Search(len(d.byVirt), func(i int) bool { return d.byVirt[i].virt+d.byVirt[i].n >= off })
	if i < len(d.byVirt) && d.byVirt[i].virt <= off && off <= d.byVirt[i].virt+d.byVirt[i].n {
		return d.byVirt[i], true
	}
	return segment{}, false
}

// ToSource maps a virtual offset back to the component.
func (d *Document) ToSource(off int) (int, bool) {
	if s, ok := d.segmentAt(off); ok {
		return s.src + off - s.virt, true
	}
	return 0, false
}

// ToSourceRange maps a virtual range; both ends must be in one segment.
func (d *Document) ToSourceRange(start, end int) (int, int, bool) {
	s, ok1 := d.ToSource(start)
	e, ok2 := d.ToSource(end)
	if !ok1 || !ok2 || e < s {
		return 0, 0, false
	}
	return s, e, true
}

// Locate maps the virtual range of a diagnostic to the component: a range
// that starts in copied text ends at the end of its segment at the latest;
// one that starts in generated text belongs to the innermost anchor around
// it. ok is false for generated text no anchor covers.
func (d *Document) Locate(start, end int) (int, int, bool) {
	if s, ok := d.segmentAt(start); ok && (start < s.virt+s.n || s.n == 0 || end == start) {
		from := s.src + start - s.virt
		to := s.src + min(end, s.virt+s.n) - s.virt
		return from, max(to, from), true
	}
	best := -1
	for i, a := range d.anchors {
		if a.virt <= start && start < a.virtEnd && (best < 0 || a.virtEnd-a.virt < d.anchors[best].virtEnd-d.anchors[best].virt) {
			best = i
		}
	}
	if best < 0 {
		return 0, 0, false
	}
	return d.anchors[best].src, d.anchors[best].srcEnd, true
}

type writer struct {
	src     string
	b       strings.Builder
	segs    []segment
	anchors []anchor
}

func (w *writer) gen(s string) { w.b.WriteString(s) }

// copy copies src[start:end]; an empty range is still a mapped position,
// where a name is about to be typed.
func (w *writer) copy(start, end int) {
	if end < start {
		return
	}
	w.segs = append(w.segs, segment{src: start, virt: w.b.Len(), n: end - start})
	w.b.WriteString(w.src[start:end])
}

// copyTrimmed copies src[start:end] without its surrounding white space.
func (w *writer) copyTrimmed(start, end int) {
	for start < end && isSpaceByte(w.src[start]) {
		start++
	}
	for end > start && isSpaceByte(w.src[end-1]) {
		end--
	}
	w.copy(start, end)
}

// anchored writes what fn writes as one anchor of the component range
// [src, srcEnd).
func (w *writer) anchored(src, srcEnd int, fn func()) {
	at := w.b.Len()
	fn()
	w.anchors = append(w.anchors, anchor{virt: at, virtEnd: w.b.Len(), src: src, srcEnd: srcEnd})
}

// name writes src[start:end] as a property name or a string literal: copied
// when it is an identifier (rename and references see it), else quoted
// and anchored.
func (w *writer) name(start, end int, literal bool) {
	text := w.src[start:end]
	if !literal && isIdentifier(text) {
		w.copy(start, end)
		return
	}
	w.anchored(start, end, func() {
		w.gen(quote(text))
	})
}

func isSpaceByte(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func isIdentByte(c byte) bool {
	return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80
}

func isIdentifier(s string) bool {
	if s == "" || s[0] >= '0' && s[0] <= '9' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isIdentByte(s[i]) {
			return false
		}
	}
	return true
}

// quote is s as a JavaScript string literal.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// Build builds the virtual document of the component at path.
func Build(path, src string) *Document {
	w := &writer{src: src}
	ts, generics := scriptLang(src)
	model, scriptStart := componentModel(src)
	if ts && generics != "" {
		// The type parameters of a generic component are their constraints
		// inside it (TS-4.8); its export is generic.
		w.gen(genericAliases(generics))
	}
	if start, end, ok := sectionBody(src, "script"); ok {
		// A required prop (`export let title: string;`) is assigned by the
		// parent: `!` keeps TypeScript from reporting its uses as before
		// assignment (TS2454).
		at := start
		for _, pos := range requiredPropEnds(model, ts, scriptStart) {
			if pos > at && pos <= end {
				w.copy(at, pos)
				w.gen("!")
				at = pos
			}
		}
		w.copy(at, end)
	}
	w.gen("\n;\n")
	if model != nil && model.Events != nil {
		w.gen(typedDispatch(ts))
	}
	w.gen("function $$render() {\n")
	if start, end, ok := sectionBody(src, "template"); ok {
		writeTemplate(w, start, end)
	}
	w.gen("}\n")
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if isIdentifier(name) {
		w.gen(componentExport(name, model, ts, generics))
	}
	d := &Document{Path: path, Source: src, Text: w.b.String(), TypeScript: ts, byVirt: w.segs, anchors: w.anchors}
	d.bySrc = append([]segment(nil), w.segs...)
	sort.SliceStable(d.bySrc, func(i, j int) bool { return d.bySrc[i].src < d.bySrc[j].src })
	return d
}

// scriptLang reads the lang and generics attributes of <script>.
func scriptLang(src string) (ts bool, generics string) {
	low := strings.ToLower(src)
	open := strings.Index(low, "<script")
	if open < 0 {
		return false, ""
	}
	gt := strings.IndexByte(src[open:], '>')
	if gt < 0 {
		return false, ""
	}
	tag := src[open : open+gt]
	lang := attrValue(tag, "lang")
	ts = lang == "ts" || lang == "typescript"
	if ts {
		generics = strings.TrimSpace(attrValue(tag, "generics"))
	}
	return ts, generics
}

// attrValue is the quoted value of the attribute name in the open tag tag.
func attrValue(tag, name string) string {
	for from := 0; ; {
		i := strings.Index(tag[from:], name)
		if i < 0 {
			return ""
		}
		i += from
		from = i + len(name)
		if i > 0 && !isSpaceByte(tag[i-1]) {
			continue
		}
		rest := strings.TrimLeft(tag[from:], " \t\r\n")
		if !strings.HasPrefix(rest, "=") {
			continue
		}
		rest = strings.TrimLeft(rest[1:], " \t\r\n")
		if rest == "" || rest[0] != '"' && rest[0] != '\'' {
			continue
		}
		if j := strings.IndexByte(rest[1:], rest[0]); j >= 0 {
			return rest[1 : 1+j]
		}
		return ""
	}
}

// componentModel is the type model of a component that parses, else nil,
// and the offset of its script.
func componentModel(src string) (*analyzer.ComponentTypes, int) {
	sfc, err := sfcast.ParseSFC(src)
	if err != nil || sfc == nil || sfc.ScriptAST == nil || len(sfc.ScriptAST.Diagnostics) > 0 {
		return nil, 0
	}
	return analyzer.ComponentTypesOf(sfc), sfc.ScriptAST.Span.Start
}

// genericAliases declares the type parameters of `generics` as aliases of
// their constraints.
func genericAliases(generics string) string {
	prog, diags := jsast.ParseWithOptions("function f<"+generics+">() {}", jsast.ParseOptions{TypeScript: true})
	if len(diags) > 0 || prog == nil || len(prog.Body) != 1 {
		return ""
	}
	fn, ok := prog.Body[0].(*jsast.JsFuncDecl)
	if !ok || fn.TS == nil {
		return ""
	}
	src := "function f<" + generics + ">() {}"
	var b strings.Builder
	for _, p := range fn.TS.TypeParams {
		constraint := "unknown"
		if p.Constraint != nil {
			constraint = src[p.Constraint.Pos:p.Constraint.End]
		}
		b.WriteString("type " + p.Name + " = " + constraint + ";\n")
	}
	return b.String()
}

// typedDispatch declares $$dispatch by the script's $$Events (TS-4.6); it
// shadows the any-typed one of the ambient types.
func typedDispatch(ts bool) string {
	if ts {
		return "declare function $$dispatch<K extends keyof $$Events & string>(name: K, ...detail: undefined extends $$Events[K] ? [detail?: $$Events[K]] : [detail: $$Events[K]]): void;\n"
	}
	return "/** @type {<K extends keyof $$Events & string>(name: K, detail?: $$Events[K]) => void} */\nvar $$dispatch;\n"
}

// componentExport declares the component's export with its type.
func componentExport(name string, t *analyzer.ComponentTypes, ts bool, generics string) string {
	if t == nil {
		// A script that does not parse: the export is a component of any
		// props, so that importers still resolve it.
		if ts {
			return "declare const $$component: import('vela').Component;\nexport { $$component as " + name + " };\nexport default $$component;\n"
		}
		return "/** @type {import('vela').Component} */\nexport const " + name + " = /** @type {any} */ (null);\nexport default " + name + ";\n"
	}
	var props []string
	var bindable []string
	for _, p := range t.Props {
		typ := "typeof " + p.Name
		if generics != "" && !p.Inferred && p.Type != "" {
			typ = p.Type // the generic signature's own type parameters
		}
		opt := ""
		if p.Optional {
			opt = "?"
		}
		props = append(props, p.Name+opt+": "+typ)
		if p.Bindable {
			bindable = append(bindable, quote(p.Name))
		}
	}
	propsType := "{}"
	if len(props) > 0 {
		propsType = "{ " + strings.Join(props, "; ") + " }"
	}
	events := "$$Events"
	if t.Events == nil {
		events = fallbackObject(t.EventNames, "any", t.AnyEvents)
	}
	slots := "$$Slots"
	if t.Slots == nil {
		slots = fallbackObject(t.SlotNames, "{}", false)
	}
	bindings := "never"
	if len(bindable) > 0 {
		bindings = strings.Join(bindable, " | ")
	}
	// A prop's type is the declared type of its variable: `typeof x` in a
	// function, where the assignments of the script do not narrow it.
	var out strings.Builder
	if len(t.Props) > 0 {
		// The parent assigns the props: linters (prefer-const,
		// no-unassigned-vars) see the assignments here, in generated text.
		out.WriteString("function $$setProps() {")
		for _, p := range t.Props {
			out.WriteString(" " + p.Name + " = " + p.Name + ";")
		}
		out.WriteString(" }\n")
	}
	if generics == "" {
		if ts {
			out.WriteString("function $$propsOf() { return null as unknown as " + propsType + "; }\n")
		} else {
			out.WriteString("function $$propsOf() { return /** @type {" + propsType + "} */ (/** @type {any} */ (null)); }\n")
		}
		propsType = "ReturnType<typeof $$propsOf>"
	}
	var typ string
	if ts && generics != "" {
		typ = "{\n  <" + generics + ">(target: Element | DocumentFragment | Comment, props?: " + propsType + ", slots?: import('vela').Slots): import('vela').ComponentInstance<" + propsType + ">;\n" +
			"  readonly $$events_def?: " + events + ";\n  readonly $$slot_def?: " + slots + ";\n  readonly $$bindings_def?: " + bindings + ";\n}"
	} else {
		typ = "import('vela').Component<" + propsType + ", " + events + ", " + slots + ", " + bindings + ">"
	}
	if ts {
		out.WriteString("declare const $$component: " + typ + ";\nexport { $$component as " + name + " };\nexport default $$component;\n")
	} else {
		out.WriteString("/** @type {" + typ + "} */\nexport const " + name + " = /** @type {any} */ (null);\nexport default " + name + ";\n")
	}
	return out.String()
}

func fallbackObject(names []string, value string, any bool) string {
	if any {
		return "Record<string, any>"
	}
	if len(names) == 0 {
		return "{}"
	}
	parts := make([]string, len(names))
	for i, n := range names {
		key := n
		if !isIdentifier(n) {
			key = quote(n)
		}
		parts[i] = key + ": " + value
	}
	return "{ " + strings.Join(parts, "; ") + " }"
}

// UTF16Index is the UTF-16 index of the byte offset off of s, as
// TypeScript counts positions.
func UTF16Index(s string, off int) int {
	off = min(max(off, 0), len(s))
	return len(utf16.Encode([]rune(s[:off])))
}

// ByteOffset is the byte offset of the UTF-16 index i of s.
func ByteOffset(s string, i int) int {
	at, units := 0, 0
	for at < len(s) && units < i {
		r, size := utf8.DecodeRuneInString(s[at:])
		units += utf16.RuneLen(r)
		at += size
	}
	return at
}

// sectionBody bounds the body of the first <tag …> of a .vela source up to
// its closing tag, the next section or the end of the file (as sfcast reads
// an unclosed section).
func sectionBody(src, tag string) (start, end int, ok bool) {
	low := strings.ToLower(src)
	open := strings.Index(low, "<"+tag)
	if open < 0 {
		return 0, 0, false
	}
	gt := strings.IndexByte(src[open:], '>')
	if gt < 0 {
		return 0, 0, false
	}
	start = open + gt + 1
	end = len(src)
	if i := strings.Index(low[start:], "</"+tag+">"); i >= 0 {
		end = start + i
	} else {
		for _, next := range []string{"<script", "<style", "<template"} {
			if i := strings.Index(low[start:], next); i >= 0 && start+i < end {
				end = start + i
			}
		}
	}
	return start, end, true
}

// requiredPropEnds are the component offsets after the names of the
// annotated props without a default of a TypeScript script at offset
// scriptStart.
func requiredPropEnds(model *analyzer.ComponentTypes, ts bool, scriptStart int) []int {
	if model == nil || !ts {
		return nil
	}
	var out []int
	for _, p := range model.Props {
		if !p.Optional && !p.Inferred && p.Type != "" {
			out = append(out, scriptStart+p.Pos+len(p.Name))
		}
	}
	sort.Ints(out)
	return out
}

// Segment is text copied from the component into the virtual document, in
// UTF-16 units as JavaScript counts: Len units at Src in the component and
// at Virt in the document.
type Segment struct{ Src, Virt, Len int }

// Segments lists the copied text of the document in document order, for
// tools that map positions themselves (the ESLint processor, task 067).
func (d *Document) Segments() []Segment {
	src, virt := utf16Offsets(d.Source), utf16Offsets(d.Text)
	out := make([]Segment, 0, len(d.byVirt))
	for _, s := range d.byVirt {
		if s.src+s.n > len(d.Source) || s.virt+s.n > len(d.Text) {
			continue
		}
		out = append(out, Segment{Src: src[s.src], Virt: virt[s.virt], Len: src[s.src+s.n] - src[s.src]})
	}
	return out
}

// utf16Offsets maps every byte offset of s (and len(s)) to its UTF-16
// index; a byte inside a character maps to the index of the character.
func utf16Offsets(s string) []int {
	out := make([]int, len(s)+1)
	units := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		for k := 0; k < size; k++ {
			out[i+k] = units
		}
		units += utf16.RuneLen(r)
		i += size
	}
	out[len(s)] = units
	return out
}
