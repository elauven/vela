package lsp

import (
	velaproject "github.com/elauven/vela/internal/project"

	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The JavaScript language features of TypeScript over the virtual
// documents, merged with the Vela ones (task 065c).

// typescript is the helper once it started, nil without one.
func (s *server) typescript() *tsClient {
	if s.tsReady == nil {
		return nil
	}
	select {
	case <-s.tsReady:
		return s.ts
	case <-time.After(15 * time.Second):
		return nil
	}
}

// startTypeScript starts the helper in the background; without node or a
// TypeScript with the language service API the server says so once and
// keeps the Vela features.
func (s *server) startTypeScript(root, tsdk string) {
	s.tsReady = make(chan struct{})
	go func() {
		defer close(s.tsReady)
		c, err := startTypeScript(root, tsdk)
		if err != nil {
			s.log(3, "vela-lsp: JavaScript language features are off: "+err.Error()+". Install typescript@6 in the workspace or pass initializationOptions.typescript.tsdk.")
			return
		}
		s.ts = c
		s.log(3, "vela-lsp: TypeScript "+c.version)
	}()
}

// virtualOf is the virtual document of a component version, cached by text.
func (s *server) virtualOf(path, src string) *virtualDoc {
	key := pathKey(path)
	s.mu.Lock()
	v := s.virtuals[key]
	s.mu.Unlock()
	if v != nil && v.source.src == src {
		return v
	}
	v = buildVirtual(path, src)
	s.mu.Lock()
	s.virtuals[key] = v
	s.mu.Unlock()
	return v
}

// virtualByFile is the virtual document a helper file name names.
func (s *server) virtualByFile(file string) *virtualDoc {
	if !strings.HasSuffix(file, ".vela"+virtualSuffix) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.virtuals[pathKey(strings.TrimSuffix(file, virtualSuffix))]
}

// syncTS sends the virtual documents of c and of the components it imports;
// with all, of every component of the workspace, for references and rename.
func (s *server) syncTS(ts *tsClient, c *component, all bool) *virtualDoc {
	v := s.virtualOf(c.path, c.text.src)
	docs := []*virtualDoc{v}
	seen := map[string]bool{pathKey(c.path): true}
	add := func(file string) {
		if seen[pathKey(file)] {
			return
		}
		seen[pathKey(file)] = true
		if other := s.openComponent(file); other != nil {
			docs = append(docs, s.virtualOf(file, other.text.src))
		}
	}
	for _, name := range c.componentImports() {
		add(c.componentFile(name))
	}
	if all {
		if cwd, err := os.Getwd(); err == nil {
			for _, file := range velaproject.CollectVela(cwd) {
				if abs, err := filepath.Abs(file); err == nil {
					add(abs)
				}
			}
		}
	}
	if err := ts.update(docs); err != nil {
		s.log(1, "vela-lsp: "+err.Error())
	}
	return v
}

// tsPosition is the helper position of the component offset off.
func tsPosition(v *virtualDoc, off int) (int, bool) {
	virt, ok := v.toVirtual(off)
	if !ok {
		return 0, false
	}
	return utf16Index(v.text, virt), true
}

// tsLocation maps a helper span to a location: in a component through its
// virtual document, in another file by its line and character. Spans in
// generated text, and in the declarations of the runes, have none.
func (s *server) tsLocation(sp tsSpan) (Location, bool) {
	if v := s.virtualByFile(sp.File); v != nil {
		start, end, ok := v.toSourceRange(byteOffset(v.text, sp.Start), byteOffset(v.text, sp.Start+sp.Length))
		if !ok {
			return Location{}, false
		}
		return Location{URI: pathToURI(v.path), Range: v.source.rangeOf(start, end)}, true
	}
	if strings.HasSuffix(sp.File, "__vela_env__.d.ts") || sp.Line == nil {
		return Location{}, false
	}
	return Location{URI: pathToURI(filepath.FromSlash(sp.File)), Range: Range{Start: Position{*sp.Line, sp.Character}, End: Position{sp.EndLine, sp.EndCharacter}}}, true
}

func (s *server) tsLocations(spans []tsSpan) []Location {
	out := []Location{}
	for _, sp := range spans {
		if loc, ok := s.tsLocation(sp); ok {
			out = append(out, loc)
		}
	}
	return out
}

// tsAt prepares a request at the component offset off.
func (s *server) tsAt(c *component, off int, all bool) (*tsClient, *virtualDoc, int, bool) {
	ts := s.typescript()
	if ts == nil {
		return nil, nil, 0, false
	}
	v := s.syncTS(ts, c, all)
	pos, ok := tsPosition(v, off)
	return ts, v, pos, ok
}

func (s *server) tsDefinition(c *component, off int) []Location {
	ts, v, pos, ok := s.tsAt(c, off, false)
	if !ok {
		return nil
	}
	var spans []tsSpan
	if ts.call("definition", map[string]any{"path": virtualPath(v.path), "position": pos}, &spans) != nil {
		return nil
	}
	return s.tsLocations(spans)
}

type tsQuickInfo struct {
	Display       string `json:"display"`
	Documentation string `json:"documentation"`
}

func (s *server) tsHover(c *component, off int) *tsQuickInfo {
	ts, v, pos, ok := s.tsAt(c, off, false)
	if !ok {
		return nil
	}
	var info tsQuickInfo
	if ts.call("quickInfo", map[string]any{"path": virtualPath(v.path), "position": pos}, &info) != nil || info.Display == "" {
		return nil
	}
	return &info
}

type tsEntry struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	SortText    string `json:"sortText"`
	InsertText  string `json:"insertText"`
	Source      string `json:"source"`
	Replacement *struct {
		Start  int `json:"start"`
		Length int `json:"length"`
	} `json:"replacement"`
}

// completionData identifies a TypeScript entry for completionItem/resolve.
type completionData struct {
	Path     string `json:"path"`
	Position int    `json:"position"`
	Name     string `json:"name"`
	Source   string `json:"source,omitempty"`
}

// tsCompletion is TypeScript's completion at off; member is true after a
// `.`, where only TypeScript knows the names.
func (s *server) tsCompletion(c *component, off, wordStart int) (items []CompletionItem, member, ok bool) {
	ts, v, pos, ok := s.tsAt(c, off, false)
	if !ok {
		return nil, false, false
	}
	var res struct {
		IsMember bool      `json:"isMember"`
		Entries  []tsEntry `json:"entries"`
	}
	if ts.call("completion", map[string]any{"path": virtualPath(v.path), "position": pos}, &res) != nil {
		return nil, false, false
	}
	for _, e := range res.Entries {
		if strings.HasPrefix(e.Name, "$$") && e.Name != "$$dispatch" {
			continue // the helpers of the render function
		}
		if e.Kind == "warning" {
			// The identifiers TypeScript finds anywhere in a JavaScript file,
			// in scope or not: the virtual document's would mislead.
			continue
		}
		text := e.Name
		if e.InsertText != "" {
			text = e.InsertText
		}
		start, end := wordStart, off
		if e.Replacement != nil {
			if rs, re, ok := v.toSourceRange(byteOffset(v.text, e.Replacement.Start), byteOffset(v.text, e.Replacement.Start+e.Replacement.Length)); ok {
				start, end = rs, re
			}
		}
		data, _ := json.Marshal(completionData{Path: virtualPath(v.path), Position: pos, Name: e.Name, Source: e.Source})
		items = append(items, CompletionItem{
			Label:    e.Name,
			Kind:     tsKind(e.Kind),
			SortText: "1" + e.SortText,
			TextEdit: &TextEdit{Range: c.text.rangeOf(start, end), NewText: text},
			Data:     data,
		})
	}
	return items, res.IsMember, true
}

func (s *server) resolveCompletion(item CompletionItem) CompletionItem {
	var data completionData
	ts := s.typescript()
	if ts == nil || len(item.Data) == 0 || json.Unmarshal(item.Data, &data) != nil || data.Path == "" {
		return item
	}
	var details struct {
		Detail        string `json:"detail"`
		Documentation string `json:"documentation"`
	}
	if ts.call("completionDetails", map[string]any{"path": data.Path, "position": data.Position, "name": data.Name, "source": data.Source}, &details) == nil {
		item.Detail = details.Detail
		if details.Documentation != "" {
			item.Documentation = &MarkupContent{Kind: "markdown", Value: details.Documentation}
		}
	}
	return item
}

// tsKind maps a TypeScript ScriptElementKind.
func tsKind(kind string) CompletionItemKind {
	switch kind {
	case "function", "local function":
		return KindFunction
	case "method", "construct", "call":
		return KindMethod
	case "property", "getter", "setter", "accessor":
		return KindProperty
	case "class", "local class":
		return KindClass
	case "interface", "type":
		return KindInterface
	case "module", "external module name", "alias":
		return KindModule
	case "keyword":
		return KindKeyword
	case "enum member":
		return KindConstant
	case "const":
		return KindConstant
	case "parameter", "type parameter":
		return KindVariable
	}
	return KindVariable
}

func (s *server) references(doc *document, off int) any {
	c := s.componentOf(doc)
	ts, v, pos, ok := s.tsAt(c, off, true)
	if !ok {
		return []Location{}
	}
	var spans []tsSpan
	if ts.call("references", map[string]any{"path": virtualPath(v.path), "position": pos}, &spans) != nil {
		return []Location{}
	}
	return s.tsLocations(spans)
}

func (s *server) rename(doc *document, off int, newName string) (any, error) {
	c := s.componentOf(doc)
	// Names the template uses outside expressions are not in the virtual
	// documents: a prop is an attribute name of its parents, a component a
	// tag name. Renaming them would leave those behind.
	if b := c.bindingAt(off); b != nil && b.Scope != nil && b.Scope.Parent != nil && b.Scope.Parent.Parent == nil {
		switch kind := c.stateKind(b.Name); {
		case strings.HasSuffix(kind, "prop"):
			return nil, &rpcError{Code: codeRequestFailed, Message: "renaming a prop is not supported: the attributes of its parents would keep the old name"}
		case strings.HasPrefix(kind, "import") && c.componentFile(b.Name) != "":
			return nil, &rpcError{Code: codeRequestFailed, Message: "renaming a component import is not supported: its tags would keep the old name"}
		}
	}
	ts, v, pos, ok := s.tsAt(c, off, true)
	if !ok {
		return nil, &rpcError{Code: codeRequestFailed, Message: "no JavaScript name here"}
	}
	var res struct {
		Error     string   `json:"error"`
		Locations []tsSpan `json:"locations"`
	}
	if err := ts.call("rename", map[string]any{"path": virtualPath(v.path), "position": pos}, &res); err != nil {
		return nil, err
	}
	if res.Error != "" {
		return nil, &rpcError{Code: codeRequestFailed, Message: res.Error}
	}
	changes := map[string][]TextEdit{}
	for _, sp := range res.Locations {
		loc, ok := s.tsLocation(sp)
		if !ok {
			return nil, &rpcError{Code: codeRequestFailed, Message: "the name is also used where it cannot be renamed"}
		}
		changes[loc.URI] = append(changes[loc.URI], TextEdit{Range: loc.Range, NewText: sp.Prefix + newName + sp.Suffix})
	}
	return map[string]any{"changes": changes}, nil
}

func (s *server) signatureHelp(doc *document, off int) any {
	c := s.componentOf(doc)
	ts, v, pos, ok := s.tsAt(c, off, false)
	if !ok {
		return nil
	}
	var help struct {
		ActiveSignature int `json:"activeSignature"`
		ActiveParameter int `json:"activeParameter"`
		Signatures      []struct {
			Label         string `json:"label"`
			Documentation string `json:"documentation"`
			Parameters    []struct {
				Label         string `json:"label"`
				Documentation string `json:"documentation"`
			} `json:"parameters"`
		} `json:"signatures"`
	}
	if ts.call("signatureHelp", map[string]any{"path": virtualPath(v.path), "position": pos}, &help) != nil || len(help.Signatures) == 0 {
		return nil
	}
	return help
}

// tsDiagnostics are TypeScript's semantic diagnostics of the component:
// none unless the project enables checkJs or the script starts with
// // @ts-check. Diagnostics in generated text are dropped.
func (s *server) tsDiagnostics(c *component) []Diagnostic {
	ts := s.typescript()
	if ts == nil {
		return nil
	}
	if !ts.checkJs && !strings.Contains(c.text.src, "@ts-check") {
		return nil
	}
	v := s.syncTS(ts, c, false)
	path := c.path
	var found []struct {
		Start    int    `json:"start"`
		Length   int    `json:"length"`
		Message  string `json:"message"`
		Category string `json:"category"`
		Code     int    `json:"code"`
	}
	if ts.call("diagnostics", map[string]any{"path": virtualPath(path)}, &found) != nil {
		return nil
	}
	var out []Diagnostic
	for _, d := range found {
		start, end, ok := v.toSourceRange(byteOffset(v.text, d.Start), byteOffset(v.text, d.Start+d.Length))
		if !ok {
			continue
		}
		out = append(out, Diagnostic{Range: v.source.rangeOf(start, end), Severity: severity(d.Category), Code: "TS" + strconv.Itoa(d.Code), Source: "ts", Message: d.Message})
	}
	return out
}
