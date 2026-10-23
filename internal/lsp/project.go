package lsp

import (
	"github.com/elauven/vela/internal/analyzer"
	"github.com/elauven/vela/internal/compiler"
	"github.com/elauven/vela/internal/jsast"
	"github.com/elauven/vela/internal/sfcast"

	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// component is the analysis of one version of a component.
type component struct {
	path     string
	text     *text
	analysis *compiler.ComponentAnalysis
}

// fileStamp identifies a version of a file on disk.
type fileStamp struct {
	mod  time.Time
	size int64
}

// project analyzes components: the open documents from their buffers, the
// other files from disk, cached by version.
type project struct {
	service *compiler.CompilerService

	mu   sync.Mutex
	disk map[string]diskEntry
}

type diskEntry struct {
	stamp     fileStamp
	component *component
}

func (p *project) analyze(path, source string) *component {
	c := &component{path: path, text: newText(source)}
	a, err := p.service.Analyze(path, source)
	if err != nil {
		a = &compiler.ComponentAnalysis{Err: err}
	}
	c.analysis = a
	return c
}

// file is the analysis of the component file at path on disk, nil when it
// cannot be read.
func (p *project) file(path string) *component {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	stamp := fileStamp{info.ModTime(), info.Size()}
	p.mu.Lock()
	entry, ok := p.disk[path]
	p.mu.Unlock()
	if ok && entry.stamp == stamp {
		return entry.component
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	c := p.analyze(path, string(data))
	p.mu.Lock()
	p.disk[path] = diskEntry{stamp, c}
	p.mu.Unlock()
	return c
}

// scriptAbs converts a position of the script program to the component.
func (c *component) scriptAbs(pos int) int {
	if c.analysis.SFC == nil {
		return pos
	}
	return c.analysis.SFC.ScriptOffset + pos
}

// scriptRel converts a component offset to the script program.
func (c *component) scriptRel(off int) int {
	if c.analysis.SFC == nil {
		return off
	}
	return off - c.analysis.SFC.ScriptOffset
}

// componentFile resolves the component a tag name names: the file of the
// import that declares the name. A `./X.js` import of a component resolves
// to the X.vela beside it.
func (c *component) componentFile(name string) string {
	a := c.analysis.Analysis
	if a == nil {
		return ""
	}
	for _, imp := range a.Imports {
		for _, local := range imp.Names {
			if local != name {
				continue
			}
			if !strings.HasPrefix(imp.Source, "./") && !strings.HasPrefix(imp.Source, "../") {
				return ""
			}
			file := filepath.Join(filepath.Dir(c.path), filepath.FromSlash(imp.Source))
			if strings.HasSuffix(file, ".js") {
				if vela := strings.TrimSuffix(file, ".js") + ".vela"; exists(vela) {
					return vela
				}
			}
			return file
		}
	}
	return ""
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// componentImports are the local names of the component imports.
func (c *component) componentImports() []string {
	a := c.analysis.Analysis
	if a == nil {
		return nil
	}
	var out []string
	for _, imp := range a.Imports {
		for _, name := range imp.Names {
			if name != "" && name[0] >= 'A' && name[0] <= 'Z' && c.componentFile(name) != "" {
				out = append(out, name)
			}
		}
	}
	return out
}

// prop is a declared prop of a component.
type prop struct {
	name     string
	bindable bool
	pos, end int // the name, in the component
	decl     string
}

func (c *component) props() []prop {
	a := c.analysis
	if a.Analysis == nil || a.Scope == nil {
		return nil
	}
	var out []prop
	for _, p := range a.Analysis.Props {
		pr := prop{name: p.Name, bindable: p.Bindable}
		if b := a.Scope.Bindings[p.Name]; b != nil {
			pr.pos, pr.end = c.nameLocation(b)
			pr.decl = c.declaration(b)
		}
		out = append(out, pr)
	}
	return out
}

// event is a component event: a $$dispatch call with a literal name.
type event struct {
	name     string
	pos, end int // the name literal, in the component
}

func (c *component) events() []event {
	if c.analysis.Script == nil {
		return nil
	}
	var out []event
	seen := map[string]bool{}
	jsast.WalkNode(c.analysis.Script, func(n jsast.JsNode) bool {
		call, ok := n.(*jsast.JsCallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		if callee, ok := call.Callee.(*jsast.JsIdent); !ok || callee.Name != "$$dispatch" {
			return true
		}
		if lit, ok := call.Args[0].(*jsast.JsLiteral); ok && lit.Kind == "string" && len(lit.Raw) >= 2 {
			name := lit.Raw[1 : len(lit.Raw)-1]
			if !seen[name] {
				seen[name] = true
				out = append(out, event{name, c.scriptAbs(lit.NodePos()), c.scriptAbs(lit.NodeEnd())})
			}
		}
		return true
	})
	return out
}

// bindingAt is the binding declared or referenced at the component offset
// off of the script.
func (c *component) bindingAt(off int) *analyzer.Binding {
	if c.analysis.Scope == nil {
		return nil
	}
	pos := c.scriptRel(off)
	var found *analyzer.Binding
	var walk func(s *analyzer.Scope)
	walk = func(s *analyzer.Scope) {
		for _, b := range s.Bindings {
			if b.Kind == analyzer.BindBuiltin {
				continue
			}
			if start, end := c.nameRange(b); pos >= start && pos <= end && end > start {
				found = b
				return
			}
			for _, ref := range b.Refs {
				if pos >= ref.Pos && pos <= ref.Pos+len(ref.Name) {
					found = b
					return
				}
			}
		}
		for _, child := range s.Children {
			if found == nil {
				walk(child)
			}
		}
	}
	walk(c.analysis.Scope)
	return found
}

// nameRange is the declared name of b in the script program: a binding
// spans its whole declaration for functions and classes.
func (c *component) nameRange(b *analyzer.Binding) (start, end int) {
	start, end = b.Pos, b.End
	if c.analysis.SFC == nil || end > len(c.analysis.SFC.Script) || start < 0 || end <= start {
		return start, end
	}
	if i := indexWord(c.analysis.SFC.Script[start:end], b.Name); i >= 0 {
		start += i
		return start, start + len(b.Name)
	}
	return start, end
}

// nameLocation is nameRange in the component.
func (c *component) nameLocation(b *analyzer.Binding) (start, end int) {
	start, end = c.nameRange(b)
	return c.scriptAbs(start), c.scriptAbs(end)
}

// declaration is the source of the statement declaring b, to its first line.
func (c *component) declaration(b *analyzer.Binding) string {
	script := c.analysis.Script
	if script == nil || c.analysis.SFC == nil {
		return ""
	}
	source := c.analysis.SFC.Script
	for _, stmt := range script.Body {
		if stmt.NodePos() <= b.Pos && b.Pos < stmt.NodeEnd() && stmt.NodeEnd() <= len(source) {
			decl := strings.TrimSpace(source[stmt.NodePos():stmt.NodeEnd()])
			if i := strings.IndexByte(decl, '\n'); i >= 0 {
				decl = strings.TrimSpace(decl[:i])
			}
			return decl
		}
	}
	return ""
}

// stateKind is what the analysis knows of a top-level name: prop,
// bindable prop, state, deep state, derived, function, import.
func (c *component) stateKind(name string) string {
	a := c.analysis.Analysis
	if a == nil {
		return ""
	}
	for _, p := range a.Props {
		if p.Name == name {
			if p.Bindable {
				return "bindable prop"
			}
			return "prop"
		}
	}
	for _, s := range a.StateVars {
		if s.Name == name {
			if s.Deep {
				return "deep state ($state.deep)"
			}
			return "state ($state)"
		}
	}
	for _, d := range a.DerivedVars {
		if d.Name == name {
			return "derived ($derived)"
		}
	}
	for _, f := range a.Functions {
		if f.Name == name {
			return "function"
		}
	}
	for _, imp := range a.Imports {
		for _, n := range imp.Names {
			if n == name {
				return "import from '" + imp.Source + "'"
			}
		}
	}
	return ""
}

// templateLocal is a name a block declares for its content.
type templateLocal struct {
	name     string
	detail   string
	pos, end int // the declaration, in the component; zero when unknown
}

// templateLocals are the names the blocks enclosing off declare.
func (c *component) templateLocals(nodes []sfcast.Node, off int) []templateLocal {
	var out []templateLocal
	src := c.text.src
	var walk func([]sfcast.Node)
	inside := func(start, end int) bool { return off >= start && (end <= start || off <= end) }
	walk = func(ns []sfcast.Node) {
		for _, node := range ns {
			switch n := node.(type) {
			case *sfcast.ElementNode:
				walk(n.Children)
			case *sfcast.ComponentNode:
				for _, slot := range n.Slots {
					walk(slot)
				}
			case *sfcast.IfChainNode:
				for _, b := range n.Branches {
					walk(b.Children)
				}
			case *sfcast.EachBlockNode:
				if inside(n.Span.Start, n.Span.End) {
					header := blockHeader(src, n.Span.Start)
					iter := strings.TrimSpace(strings.SplitN(n.EachExpr, " as ", 2)[0])
					for _, name := range []string{n.ItemName, n.IndexName} {
						if name == "" {
							continue
						}
						l := templateLocal{name: name, detail: "each item of " + iter}
						if name == n.IndexName {
							l.detail = "each index of " + iter
						}
						if as := strings.Index(header, " as "); as >= 0 {
							if i := indexWord(header[as:], name); i >= 0 {
								l.pos = n.Span.Start + as + i
								l.end = l.pos + len(name)
							}
						}
						out = append(out, l)
					}
					walk(n.Children)
				}
				walk(n.ElseChildren)
			case *sfcast.AwaitBlockNode:
				if inside(n.Span.Start, n.Span.End) {
					for _, name := range []string{n.ValueName, n.ErrorName} {
						if name != "" {
							out = append(out, templateLocal{name: name, detail: "await result of " + strings.TrimSpace(n.PromiseExpr)})
						}
					}
				}
				walk(n.PendingChildren)
				walk(n.ThenChildren)
				walk(n.CatchChildren)
			}
		}
	}
	walk(nodes)
	return out
}

// blockHeader is the {#…} tag at start.
func blockHeader(src string, start int) string {
	if start < 0 || start >= len(src) {
		return ""
	}
	end := strings.IndexByte(src[start:], '}')
	if end < 0 {
		return src[start:]
	}
	return src[start : start+end]
}

// indexWord is the index of name as a whole identifier in s, or -1.
func indexWord(s, name string) int {
	for i := 0; ; {
		j := strings.Index(s[i:], name)
		if j < 0 {
			return -1
		}
		j += i
		before := j == 0 || !isIdentByte(s[j-1])
		after := j+len(name) == len(s) || !isIdentByte(s[j+len(name)])
		if before && after {
			return j
		}
		i = j + 1
	}
}
