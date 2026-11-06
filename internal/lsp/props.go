package lsp

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/elauven/vela/internal/analyzer"
	velaproject "github.com/elauven/vela/internal/project"
	"github.com/elauven/vela/internal/typecheck"
)

// Props across components (task 085): the references of a prop are its
// uses in the component and the attributes that set it in the parents'
// tags; renaming it renames both.

// propRef names a prop: the component's file and the prop.
type propRef struct {
	file, name string
}

// propUse is an attribute of a parent tag that sets a prop.
type propUse struct {
	comp       *component
	start, end int // the prop name in the attribute
	shorthand  bool
	attrStart  int // a shorthand {name}: its braces
	attrEnd    int
}

// propAt is the prop at the offset off of c: the name of an `export let`
// of c, or the attribute of a component tag of c's template that sets one.
func (s *server) propAt(c *component, off int) (propRef, bool) {
	if b := c.bindingAt(off); b != nil && b.Scope != nil && b.Scope.Parent != nil && b.Scope.Parent.Parent == nil {
		if strings.HasSuffix(c.stateKind(b.Name), "prop") {
			return propRef{file: c.path, name: b.Name}, true
		}
		return propRef{}, false
	}
	for _, tag := range typecheck.ComponentTags(c.text.src) {
		for _, a := range tag.Attrs {
			if a.Shorthand || off < a.PropStart || off > a.PropEnd {
				continue
			}
			if file := c.componentFile(tag.Name); file != "" {
				return propRef{file: file, name: a.Prop}, true
			}
		}
	}
	return propRef{}, false
}

// propUses lists the attributes of the workspace's components that set the
// prop p.
func (s *server) propUses(p propRef) []propUse {
	var out []propUse
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	for _, file := range velaproject.CollectVela(cwd) {
		abs, err := filepath.Abs(file)
		if err != nil {
			continue
		}
		parent := s.openComponent(abs)
		if parent == nil {
			continue
		}
		for _, tag := range typecheck.ComponentTags(parent.text.src) {
			if file := parent.componentFile(tag.Name); file == "" || pathKey(file) != pathKey(p.file) {
				continue
			}
			for _, a := range tag.Attrs {
				if a.Prop == p.name {
					out = append(out, propUse{comp: parent, start: a.PropStart, end: a.PropEnd, shorthand: a.Shorthand, attrStart: a.Start, attrEnd: a.End})
				}
			}
		}
	}
	return out
}

// propDeclaration is the `export let` name of p in its component.
func (s *server) propDeclaration(p propRef) (*component, int, bool) {
	child := s.openComponent(p.file)
	if child == nil {
		return nil, 0, false
	}
	for _, pr := range child.props() {
		if pr.name == p.name && pr.end > pr.pos {
			return child, pr.pos, true
		}
	}
	return nil, 0, false
}

// propReferences are the uses of p in its component (through TypeScript
// when it runs, else the declaration) and the parents' attributes.
func (s *server) propReferences(p propRef) []Location {
	out := []Location{}
	child, decl, ok := s.propDeclaration(p)
	if !ok {
		return out
	}
	found := false
	if ts, v, pos, ok := s.tsAt(child, decl, false); ok {
		var spans []tsSpan
		if ts.Call("references", map[string]any{"path": v.VirtualPath(), "position": pos}, &spans) == nil {
			out = append(out, s.tsLocations(spans)...)
			found = len(out) > 0
		}
	}
	if !found {
		out = append(out, Location{URI: pathToURI(child.path), Range: child.text.rangeOf(decl, decl+len(p.name))})
	}
	for _, u := range s.propUses(p) {
		out = append(out, Location{URI: pathToURI(u.comp.path), Range: u.comp.text.rangeOf(u.start, u.end)})
	}
	return out
}

// renameProp renames p in its component (TypeScript's rename of the
// variable) and in the parents' attributes; a shorthand {title} becomes
// newName={title}.
func (s *server) renameProp(p propRef, newName string) (any, error) {
	child, decl, ok := s.propDeclaration(p)
	if !ok {
		return nil, &rpcError{Code: codeRequestFailed, Message: "the prop's declaration was not found"}
	}
	changes := map[string][]TextEdit{}
	ts, v, pos, ok := s.tsAt(child, decl, false)
	if !ok {
		return nil, &rpcError{Code: codeRequestFailed, Message: "renaming a prop needs TypeScript (initializationOptions.typescript)"}
	}
	var res struct {
		Error     string   `json:"error"`
		Locations []tsSpan `json:"locations"`
	}
	if err := ts.Call("rename", map[string]any{"path": v.VirtualPath(), "position": pos}, &res); err != nil {
		return nil, err
	}
	if res.Error != "" {
		return nil, &rpcError{Code: codeRequestFailed, Message: res.Error}
	}
	for _, sp := range res.Locations {
		loc, ok := s.tsLocation(sp)
		if !ok {
			if s.virtualByFile(sp.File) != nil {
				continue // the generated props type of the virtual document
			}
			return nil, &rpcError{Code: codeRequestFailed, Message: "the prop is also used where it cannot be renamed"}
		}
		changes[loc.URI] = append(changes[loc.URI], TextEdit{Range: loc.Range, NewText: sp.Prefix + newName + sp.Suffix})
	}
	for _, u := range s.propUses(p) {
		uri := pathToURI(u.comp.path)
		if u.shorthand {
			value := strings.TrimSpace(u.comp.text.src[u.attrStart+1 : u.attrEnd-1])
			changes[uri] = append(changes[uri], TextEdit{Range: u.comp.text.rangeOf(u.attrStart, u.attrEnd), NewText: newName + "={" + value + "}"})
			continue
		}
		changes[uri] = append(changes[uri], TextEdit{Range: u.comp.text.rangeOf(u.start, u.end), NewText: newName})
	}
	return map[string]any{"changes": changes}, nil
}

// componentTypes is the type model of a component (analyzer, task 083).
type componentTypes struct{ t *analyzer.ComponentTypes }

func (c *component) types() componentTypes {
	if c.analysis == nil || c.analysis.SFC == nil {
		return componentTypes{}
	}
	return componentTypes{analyzer.ComponentTypesOf(c.analysis.SFC)}
}

// prop is the declaration of a prop with its type, `title?: string`; ""
// when the type is unknown.
func (ct componentTypes) prop(name string) string {
	if ct.t == nil {
		return ""
	}
	for _, p := range ct.t.Props {
		if p.Name != name || p.Type == "" {
			continue
		}
		opt := ""
		if p.Optional {
			opt = "?"
		}
		return p.Name + opt + ": " + strings.Join(strings.Fields(p.Type), " ")
	}
	return ""
}

// events are the names of the component's events: of $$Events when it
// declares them, else of its $$dispatch calls.
func (ct componentTypes) events(c *component) []string {
	if ct.t != nil && ct.t.Events != nil {
		return ct.t.EventNames
	}
	var out []string
	for _, e := range c.events() {
		out = append(out, e.name)
	}
	return out
}
