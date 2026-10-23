package lsp

import (
	"github.com/elauven/vela/internal/analyzer"
	"github.com/elauven/vela/internal/jsast"
	"github.com/elauven/vela/internal/sfcast"

	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// scriptOf is the analysis that knows the script names: the current one,
// else the last one whose script parsed.
func (s *server) scriptOf(doc *document, c *component) *component {
	s.mu.Lock()
	last := doc.lastScript
	s.mu.Unlock()
	if c.analysis.Scope == nil && last != nil {
		return last
	}
	return c
}

// templateOf is the template nodes: the current ones, else the last parsed.
func (s *server) templateOf(doc *document, c *component) []sfcast.Node {
	s.mu.Lock()
	last := doc.lastTemplate
	s.mu.Unlock()
	if c.analysis.Template == nil && last != nil {
		return last.analysis.Template
	}
	return c.analysis.Template
}

// attrTarget splits an attribute name of a component tag into what it
// names of the child: a prop (`:name`, `bind:name`, `name`) or an event
// (`@on:name`, or `@name` as the compiler also reads it).
func attrTarget(attr string) (name string, isEvent bool) {
	switch {
	case strings.HasPrefix(attr, "@"):
		return strings.TrimPrefix(attr[1:], "on:"), true
	case strings.HasPrefix(attr, "bind:"):
		return attr[len("bind:"):], false
	case strings.HasPrefix(attr, ":"):
		return attr[1:], false
	}
	return attr, false
}

// attrWord is the attribute name around off that starts at start.
func attrWord(src string, start, off int) string {
	end := off
	for end < len(src) && !strings.ContainsRune(" \t\r\n=>/", rune(src[end])) {
		end++
	}
	return src[start:end]
}

func (s *server) locationIn(path string, c *component, start, end int) *Location {
	if c == nil {
		return nil
	}
	return &Location{URI: pathToURI(path), Range: c.text.rangeOf(start, end)}
}

func (s *server) definition(doc *document, off int) any {
	c := s.componentOf(doc)
	src := c.text.src
	ctx := contextAt(src, off)
	switch ctx.kind {
	case inScript:
		if file := importAt(c, off); file != "" {
			return Location{URI: pathToURI(file)}
		}
		if locs := s.tsDefinition(c, off); len(locs) > 0 {
			return locs
		}
		b := c.bindingAt(off)
		if b == nil {
			return nil
		}
		start, end := c.nameLocation(b)
		if b.Kind == analyzer.BindImport && off >= start && off <= end {
			if file := c.componentFile(b.Name); file != "" {
				return Location{URI: pathToURI(file)}
			}
		}
		return s.locationIn(doc.path, c, start, end)
	case inTagName:
		name := strings.TrimPrefix(tagName(src, ctx.wordStart-1), "/")
		if file := s.scriptOf(doc, c).componentFile(name); file != "" {
			return Location{URI: pathToURI(file)}
		}
	case inAttrName:
		file := s.scriptOf(doc, c).componentFile(ctx.tag)
		child := s.openComponent(file)
		if file == "" || child == nil {
			return nil
		}
		name, isEvent := attrTarget(attrWord(src, ctx.wordStart, off))
		if isEvent {
			for _, e := range child.events() {
				if e.name == name {
					return s.locationIn(file, child, e.pos, e.end)
				}
			}
			return nil
		}
		for _, p := range child.props() {
			if p.name == name && p.end > p.pos {
				return s.locationIn(file, child, p.pos, p.end)
			}
		}
	case inExpr:
		if locs := s.tsDefinition(c, off); len(locs) > 0 {
			return locs
		}
		start, end := wordAt(src, off)
		if start == end || (start > 0 && src[start-1] == '.') {
			return nil
		}
		name := src[start:end]
		for _, l := range c.templateLocals(s.templateOf(doc, c), off) {
			if l.name == name {
				if l.end > l.pos {
					return s.locationIn(doc.path, c, l.pos, l.end)
				}
				return nil
			}
		}
		// The declaration of the current script: positions of an older
		// analysis would point elsewhere.
		if c.analysis.Scope != nil {
			if b := c.analysis.Scope.Bindings[name]; b != nil && b.End > b.Pos {
				start, end := c.nameLocation(b)
				return s.locationIn(doc.path, c, start, end)
			}
		}
	}
	return nil
}

// importAt is the file of the relative import specifier string at off.
func importAt(c *component, off int) string {
	script, sfc := c.analysis.Script, c.analysis.SFC
	if script == nil || sfc == nil {
		return ""
	}
	rel := c.scriptRel(off)
	for _, stmt := range script.Body {
		imp, ok := stmt.(*jsast.JsImportDecl)
		if !ok || rel < imp.NodePos() || rel > imp.NodeEnd() || imp.NodeEnd() > len(sfc.Script) {
			continue
		}
		text := sfc.Script[imp.NodePos():imp.NodeEnd()]
		for _, q := range []string{"'", `"`} {
			i := strings.Index(text, q+imp.Source+q)
			if i < 0 {
				continue
			}
			start := imp.NodePos() + i
			if rel < start || rel > start+len(imp.Source)+2 {
				continue
			}
			if !strings.HasPrefix(imp.Source, "./") && !strings.HasPrefix(imp.Source, "../") {
				return ""
			}
			file := filepath.Join(filepath.Dir(c.path), filepath.FromSlash(imp.Source))
			if strings.HasSuffix(file, ".js") && !exists(file) {
				if vela := strings.TrimSuffix(file, ".js") + ".vela"; exists(vela) {
					return vela
				}
			}
			return file
		}
	}
	return ""
}

func markdown(code, note string) *Hover {
	value := ""
	if code != "" {
		value = "```js\n" + code + "\n```"
	}
	if note != "" {
		if value != "" {
			value += "\n\n"
		}
		value += note
	}
	if value == "" {
		return nil
	}
	return &Hover{Contents: MarkupContent{Kind: "markdown", Value: value}}
}

// tsMarkdown is a TypeScript hover with the Vela kind of the name.
func tsMarkdown(info *tsQuickInfo, note string) *Hover {
	value := "```ts\n" + info.Display + "\n```"
	for _, part := range []string{info.Documentation, note} {
		if part != "" {
			value += "\n\n" + part
		}
	}
	return &Hover{Contents: MarkupContent{Kind: "markdown", Value: value}}
}

// describe is the hover of a top-level name of the component.
func describe(c *component, name string) *Hover {
	var decl string
	if c.analysis.Scope != nil {
		if b := c.analysis.Scope.Bindings[name]; b != nil {
			decl = c.declaration(b)
		}
	}
	return markdown(decl, c.stateKind(name))
}

// componentSummary is the hover of a component tag: its props and events.
func componentSummary(name, file string, child *component) *Hover {
	var lines []string
	for _, p := range child.props() {
		line := "- `" + p.name + "`"
		if p.bindable {
			line += " (bindable)"
		}
		if p.decl != "" {
			line += " — `" + p.decl + "`"
		}
		lines = append(lines, line)
	}
	note := "**" + name + "** — " + filepath.Base(file)
	if len(lines) > 0 {
		note += "\n\nProps:\n" + strings.Join(lines, "\n")
	}
	var events []string
	for _, e := range child.events() {
		events = append(events, "`@on:"+e.name+"`")
	}
	if len(events) > 0 {
		note += "\n\nEvents: " + strings.Join(events, ", ")
	}
	return markdown("", note)
}

func (s *server) hover(doc *document, off int) any {
	c := s.componentOf(doc)
	src := c.text.src
	ctx := contextAt(src, off)
	withRange := func(h *Hover, start, end int) any {
		if h == nil {
			return nil
		}
		r := c.text.rangeOf(start, end)
		h.Range = &r
		return h
	}
	switch ctx.kind {
	case inScript:
		start, end := wordAt(src, off)
		b := c.bindingAt(off)
		note := ""
		if b != nil && b.Scope != nil && b.Scope.Parent != nil && b.Scope.Parent.Parent == nil {
			note = c.stateKind(b.Name)
		}
		if info := s.tsHover(c, off); info != nil {
			return withRange(tsMarkdown(info, note), start, end)
		}
		if b == nil {
			return nil
		}
		if note != "" {
			return withRange(describe(c, b.Name), start, end)
		}
		// A local of a function or block.
		return withRange(markdown(c.declaration(b), ""), start, end)
	case inTagName:
		name := strings.TrimPrefix(tagName(src, ctx.wordStart-1), "/")
		file := s.scriptOf(doc, c).componentFile(name)
		if child := s.openComponent(file); file != "" && child != nil {
			return withRange(componentSummary(name, file, child), ctx.wordStart, ctx.wordStart+len(tagName(src, ctx.wordStart-1)))
		}
	case inAttrName:
		file := s.scriptOf(doc, c).componentFile(ctx.tag)
		child := s.openComponent(file)
		if file == "" || child == nil {
			return nil
		}
		word := attrWord(src, ctx.wordStart, off)
		name, isEvent := attrTarget(word)
		if isEvent {
			for _, e := range child.events() {
				if e.name == name {
					return withRange(markdown("", fmt.Sprintf("event `%s` of %s, dispatched with `$$dispatch('%s', detail)`", name, ctx.tag, name)), ctx.wordStart, ctx.wordStart+len(word))
				}
			}
			return nil
		}
		for _, p := range child.props() {
			if p.name == name {
				return withRange(markdown(p.decl, "prop of "+ctx.tag), ctx.wordStart, ctx.wordStart+len(word))
			}
		}
	case inExpr:
		start, end := wordAt(src, off)
		member := start > 0 && src[start-1] == '.'
		name := src[start:end]
		note := ""
		local := false
		if !member {
			for _, l := range c.templateLocals(s.templateOf(doc, c), off) {
				if l.name == name {
					note, local = l.detail, true
				}
			}
			if !local {
				note = s.scriptOf(doc, c).stateKind(name)
			}
		}
		if info := s.tsHover(c, off); info != nil {
			return withRange(tsMarkdown(info, note), start, end)
		}
		if start == end || member {
			return nil
		}
		if local {
			return withRange(markdown("", note), start, end)
		}
		return withRange(describe(s.scriptOf(doc, c), name), start, end)
	}
	return nil
}

// runes are the compiler intrinsics of a component script.
var runes = []struct{ name, snippet, detail string }{
	{"$state", "\\$state(${1})", "reactive state: reassign to update"},
	{"$state.deep", "\\$state.deep(${1})", "reactive state with nested mutation"},
	{"$derived", "\\$derived(${1})", "computed value of a pure expression"},
	{"$effect", "\\$effect(() => {\n\t$0\n})", "work outside rendering; return a cleanup"},
	{"$bindable", "\\$bindable(${1})", "default of a two-way prop: export let value = $bindable(…)"},
	{"$$dispatch", "\\$\\$dispatch('${1:name}', ${2:detail})", "dispatch a component event"},
}

// blocks are the template block tags, inserted from their opening brace.
var blocks = []struct{ label, snippet string }{
	{"#if", "{#if ${1:condition}}\n\t$0\n{/if}"},
	{"#each", "{#each ${1:items} as ${2:item} (${3:$2.id})}\n\t$0\n{/each}"},
	{"#await", "{#await ${1:promise}}\n\t$2\n{:then ${3:value}}\n\t$0\n{:catch error}\n\t<p>{error.message}</p>\n{/await}"},
	{"#key", "{#key ${1:identity}}\n\t$0\n{/key}"},
	{"#client", "{#client}\n\t$1\n{:else}\n\t$0\n{/client}"},
	{":else", "{:else}"},
	{":else if", "{:else if ${1:condition}}"},
	{":then", "{:then ${1:value}}"},
	{":catch", "{:catch ${1:error}}"},
	{"/if", "{/if}"},
	{"/each", "{/each}"},
	{"/await", "{/await}"},
	{"/key", "{/key}"},
	{"/client", "{/client}"},
}

// elementDirectives are the attributes offered on an HTML element.
var elementDirectives = []struct{ label, snippet, detail string }{
	{":", ":${1:attribute}={${2}}", "dynamic attribute"},
	{"@click", "@click={${1}}", "event listener"},
	{"@input", "@input={${1}}", "event listener"},
	{"@change", "@change={${1}}", "event listener"},
	{"@submit", "@submit={${1}}", "event listener"},
	{"@keydown", "@keydown={${1}}", "event listener"},
	{"@focus", "@focus={${1}}", "event listener"},
	{"@blur", "@blur={${1}}", "event listener"},
	{"bind:value", "bind:value={${1}}", "two-way binding"},
	{"bind:checked", "bind:checked={${1}}", "two-way binding"},
	{"bind:this", "bind:this={${1}}", "the element"},
	{"class:", "class:${1:name}={${2}}", "conditional class"},
	{"style:", "style:${1:property}={${2}}", "dynamic style property"},
}

func (s *server) completion(doc *document, off int) any {
	c := s.componentOf(doc)
	src := c.text.src
	ctx := contextAt(src, off)
	var items []CompletionItem
	edit := func(start, end int, label, snippet string, kind CompletionItemKind, detail string) CompletionItem {
		return CompletionItem{Label: label, Kind: kind, Detail: detail, InsertTextFormat: insertSnippet, TextEdit: &TextEdit{Range: c.text.rangeOf(start, end), NewText: snippet}}
	}
	script := s.scriptOf(doc, c)
	names := func(start int) {
		if script.analysis.Scope == nil {
			return
		}
		var sorted []string
		for name, b := range script.analysis.Scope.Bindings {
			if b.Kind != analyzer.BindBuiltin {
				sorted = append(sorted, name)
			}
		}
		sort.Strings(sorted)
		for _, name := range sorted {
			kind := KindVariable
			detail := script.stateKind(name)
			switch {
			case strings.HasSuffix(detail, "prop"):
				kind = KindProperty
			case detail == "function":
				kind = KindFunction
			case strings.HasPrefix(detail, "import"):
				kind = KindModule
			}
			items = append(items, edit(start, off, name, escapeSnippet(name), kind, detail))
		}
	}
	// TypeScript knows the script's names, the template's block names and
	// the globals; the Vela names are the fallback without it.
	withTS := func() bool {
		tsItems, member, ok := s.tsCompletion(c, off, ctx.wordStart)
		if !ok {
			return false
		}
		if member {
			items = tsItems
			return true
		}
		runeNames := map[string]bool{}
		if ctx.kind == inScript {
			for _, r := range runes {
				runeNames[r.name] = true
				items = append(items, edit(ctx.wordStart, off, r.name, r.snippet, KindKeyword, r.detail))
			}
		}
		for _, item := range tsItems {
			if runeNames[item.Label] || (ctx.kind == inExpr && strings.HasPrefix(item.Label, "$")) {
				continue
			}
			if detail := script.stateKind(item.Label); detail != "" {
				item.Detail = detail
			}
			items = append(items, item)
		}
		return true
	}
	switch ctx.kind {
	case inScript:
		if withTS() {
			break
		}
		for _, r := range runes {
			items = append(items, edit(ctx.wordStart, off, r.name, r.snippet, KindKeyword, r.detail))
		}
		names(ctx.wordStart)
	case inTagName:
		for _, name := range script.componentImports() {
			items = append(items, edit(ctx.wordStart, off, name, name, KindClass, "component"))
		}
		items = append(items, edit(ctx.wordStart, off, "svelte:fragment", "svelte:fragment slot=\"${1:name}\">$0</svelte:fragment", KindKeyword, "several nodes in a named slot"))
	case inAttrName:
		if file := script.componentFile(ctx.tag); file != "" {
			if child := s.openComponent(file); child != nil {
				for _, p := range child.props() {
					items = append(items, edit(ctx.wordStart, off, ":"+p.name, ":"+p.name+"={${1}}", KindProperty, p.decl))
					if p.bindable {
						items = append(items, edit(ctx.wordStart, off, "bind:"+p.name, "bind:"+p.name+"={${1}}", KindProperty, "two-way binding of "+p.decl))
					}
				}
				for _, e := range child.events() {
					items = append(items, edit(ctx.wordStart, off, "@on:"+e.name, "@on:"+e.name+"={${1}}", KindEvent, "component event"))
				}
			}
			items = append(items, edit(ctx.wordStart, off, "bind:this", "bind:this={${1}}", KindProperty, "the component"))
			break
		}
		for _, d := range elementDirectives {
			items = append(items, edit(ctx.wordStart, off, d.label, d.snippet, KindProperty, d.detail))
		}
	case inBlock:
		end := off
		if end < len(src) && src[end] == '}' {
			end++
		}
		for _, b := range blocks {
			item := edit(ctx.wordStart, end, b.label, b.snippet, KindKeyword, "block")
			item.FilterText = "{" + b.label
			items = append(items, item)
		}
	case inExpr:
		member := ctx.wordStart > 0 && src[ctx.wordStart-1] == '.'
		if !withTS() {
			if member {
				return CompletionList{Items: []CompletionItem{}}
			}
			for i, l := range c.templateLocals(s.templateOf(doc, c), off) {
				item := edit(ctx.wordStart, off, l.name, escapeSnippet(l.name), KindVariable, l.detail)
				item.SortText = fmt.Sprintf("0%03d", i)
				items = append(items, item)
			}
			names(ctx.wordStart)
		}
		if member {
			break
		}
		// Right after the opening brace of a text expression, the blocks.
		if ctx.tag == "" && ctx.wordStart == off && off > 0 && src[off-1] == '{' {
			end := off
			if end < len(src) && src[end] == '}' {
				end++
			}
			for _, b := range blocks[:5] {
				item := edit(off-1, end, b.label, b.snippet, KindKeyword, "block")
				item.FilterText = "{" + b.label
				items = append(items, item)
			}
		}
	}
	if items == nil {
		items = []CompletionItem{}
	}
	return CompletionList{Items: items}
}

func escapeSnippet(s string) string {
	return strings.NewReplacer(`\`, `\\`, `$`, `\$`, `}`, `\}`).Replace(s)
}
