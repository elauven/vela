package analyzer

import (
	"sort"
	"strings"

	"github.com/elauven/vela/internal/jsast"
	"github.com/elauven/vela/internal/sfcast"
)

// The type model of a component (task 083, docs/TYPESCRIPT.md TS-4): what a
// parent sees of it. `vela --dts` prints it as a declaration file; the type
// checker and the language server (084, 085) present it to TypeScript.
//
// Types are source text: an annotation, a JSDoc type, the body of
// `$$Events`. The compiler resolves no types (TS-3.1); it only knows which
// names a type text references, so that the local types and the imports it
// needs can travel with it.

// ComponentTypes is the type of one component.
type ComponentTypes struct {
	TypeScript bool
	// Generics are the type parameters of `generics` as written, and
	// TypeParams their names; both empty for a component that is not
	// generic (TS-4.8).
	Generics   string
	TypeParams []string

	Props []PropType

	// Events is the declaration of `$$Events` (an interface or a type alias
	// in TypeScript, a JSDoc @typedef in JavaScript), nil without one.
	// EventNames are its member names, or without it the literal names of
	// the `$$dispatch` calls; AnyEvents is set when there is no `$$Events`
	// and a call has a computed name (TS-4.6).
	Events     *TypeDecl
	EventNames []string
	AnyEvents  bool

	// Slots is the declaration of `$$Slots`, nil without one; then
	// SlotNames are the names of the template's <slot> elements (TS-4.7).
	Slots     *TypeDecl
	SlotNames []string

	// Decls are the other type declarations of the script, Imports its
	// import bindings and Values the names it declares as values.
	Decls   []*TypeDecl
	Imports []TypeImport
	Values  map[string]bool
}

// PropType is one `export let` prop (TS-4.4).
type PropType struct {
	Name string
	// Type is the declared type: the annotation, or the JSDoc @type of a
	// JavaScript prop. Without one, Type is what the literal default says
	// (`'x'` is string, `[]` any[]) and Inferred is set; "" is unknown.
	Type     string
	Inferred bool
	// Refs are the names Type references: types and `typeof` values.
	Refs []TypeRef
	// Optional: the prop has a default. Bindable: `$bindable(…)`.
	Optional, Bindable bool
	Pos                int // the name, in the script
}

// TypeRef is a name a type text references; Value is set for `typeof x`.
type TypeRef struct {
	Name  string
	Value bool
}

// TypeDecl is a type declaration of the script: Name, the source text Text
// (an interface or a type alias, without `export` and `declare`), the names
// its text references. JSDoc is set for an @typedef; Text is then its type.
type TypeDecl struct {
	Name     string
	Text     string
	Refs     []TypeRef
	Exported bool
	JSDoc    bool
}

// TypeImport is one binding of an import of the script.
type TypeImport struct {
	Local    string
	Imported string // "default", "*" for a namespace import, or the export name
	Source   string
}

// ComponentTypesOf builds the type model of a parsed component. A script
// that does not parse gives the props it could read and no declarations.
func ComponentTypesOf(sfc *sfcast.SFC) *ComponentTypes {
	t := &ComponentTypes{Values: map[string]bool{}}
	if sfc == nil {
		return t
	}
	t.TypeScript = sfc.TypeScript
	if sfc.TypeScript && strings.TrimSpace(sfc.Generics) != "" {
		if names, ok := typeParamNames(sfc.Generics); ok {
			t.Generics, t.TypeParams = strings.TrimSpace(sfc.Generics), names
		}
	}
	script := sfc.ScriptAST
	if script != nil && script.Program != nil {
		t.readScript(script)
	}
	if sfc.TemplateAST != nil {
		seen := map[string]bool{}
		walkSlots(sfc.TemplateAST.Nodes, func(name string) {
			if name == "" {
				name = "default"
			}
			if !seen[name] {
				seen[name] = true
				t.SlotNames = append(t.SlotNames, name)
			}
		})
		sort.Strings(t.SlotNames)
	}
	return t
}

func (t *ComponentTypes) readScript(script *sfcast.ScriptAST) {
	src, prog := script.Source, script.Program
	for _, stmt := range prog.Body {
		decl := stmt
		if exp, ok := stmt.(*jsast.JsExportDecl); ok && exp.Declaration != nil {
			decl = exp.Declaration
		}
		switch d := decl.(type) {
		case *jsast.JsImportDecl:
			if d.DefaultName != "" {
				t.Imports = append(t.Imports, TypeImport{Local: d.DefaultName, Imported: "default", Source: d.Source})
			}
			if d.Namespace != "" {
				t.Imports = append(t.Imports, TypeImport{Local: d.Namespace, Imported: "*", Source: d.Source})
			}
			for _, s := range d.Specifiers {
				t.Imports = append(t.Imports, TypeImport{Local: s.Local, Imported: s.Imported, Source: d.Source})
			}
		case *jsast.JsVarDecl:
			for _, v := range d.Decls {
				jsast.WalkNode(v.Id, func(n jsast.JsNode) bool {
					if id, ok := n.(*jsast.JsIdent); ok {
						t.Values[id.Name] = true
					}
					return true
				})
			}
		case *jsast.JsFuncDecl:
			t.Values[d.Name] = true
		case *jsast.JsClassDecl:
			t.Values[d.Name] = true
		}
		exp, ok := stmt.(*jsast.JsExportDecl)
		if !ok {
			continue
		}
		vd, ok := exp.Declaration.(*jsast.JsVarDecl)
		if !ok || vd.Kind != "let" {
			continue
		}
		for _, v := range vd.Decls {
			name := IdentName(v.Id)
			if name == "" {
				continue
			}
			p := PropType{Name: name, Optional: v.Init != nil, Pos: v.Id.NodePos()}
			def := v.Init
			if call, ok := v.Init.(*jsast.JsCallExpr); ok {
				if callee, ok := call.Callee.(*jsast.JsIdent); ok && callee.Name == "$bindable" {
					p.Bindable, def = true, nil
					if len(call.Args) > 0 {
						def = call.Args[0]
					}
				}
			}
			switch {
			case v.Type != nil && script.TypeScript:
				p.Type = dedent(strings.TrimSpace(src[v.Type.Pos:v.Type.End]))
			case !script.TypeScript:
				if typ, ok := jsdocTag(precedingJSDoc(src, exp.Pos), "@type"); ok {
					p.Type = typ
				}
			}
			if p.Type != "" {
				if refs, ok := typeTextRefs(p.Type, t.Generics); ok {
					p.Refs = refs
				} else {
					p.Type = "" // JSDoc that is not TypeScript (Closure syntax)
				}
			}
			if p.Type == "" {
				p.Type, p.Inferred = literalType(def), true
			}
			t.Props = append(t.Props, p)
		}
	}
	t.readDispatches(prog)
	if script.TypeScript && prog.TS != nil {
		for _, imp := range prog.TS.Imports {
			t.Imports = append(t.Imports, TypeImport{Local: imp.Local, Imported: imp.Imported, Source: imp.Source})
		}
		for _, d := range prog.TS.Decls {
			if d.Kind != jsast.TSDeclInterface && d.Kind != jsast.TSDeclTypeAlias {
				continue
			}
			text := declText(src[d.Pos:d.End])
			refs, _ := typeDeclRefs(text, t.Generics)
			td := &TypeDecl{Name: d.Name, Text: text, Refs: refs, Exported: d.Exported}
			switch d.Name {
			case "$$Events":
				t.Events = td
				t.declaredEvents(objectMembers(d.Type))
			case "$$Slots":
				t.Slots = td
			default:
				t.Decls = append(t.Decls, td)
			}
		}
	}
	if !script.TypeScript {
		for _, td := range jsdocTypedefs(src, t.Generics) {
			switch td.Name {
			case "$$Events":
				t.Events = td
				if prog, diags := jsast.ParseWithOptions("type $$T = "+td.Text+";", jsast.ParseOptions{TypeScript: true}); len(diags) == 0 && prog.TS != nil && len(prog.TS.Decls) == 1 {
					t.declaredEvents(objectMembers(prog.TS.Decls[0].Type))
				}
			case "$$Slots":
				t.Slots = td
			default:
				t.Decls = append(t.Decls, td)
			}
		}
	}
}

// readDispatches collects the names of the $$dispatch calls.
func (t *ComponentTypes) readDispatches(prog *jsast.JsProgram) {
	seen := map[string]bool{}
	jsast.WalkNode(prog, func(n jsast.JsNode) bool {
		call, ok := n.(*jsast.JsCallExpr)
		if !ok {
			return true
		}
		if callee, ok := call.Callee.(*jsast.JsIdent); !ok || callee.Name != "$$dispatch" || len(call.Args) == 0 {
			return true
		}
		lit, ok := call.Args[0].(*jsast.JsLiteral)
		if !ok || lit.Kind != "string" || len(lit.Raw) < 2 || strings.ContainsAny(lit.Raw[1:len(lit.Raw)-1], "\\'\"`\n") {
			t.AnyEvents = true
			return true
		}
		name := lit.Raw[1 : len(lit.Raw)-1]
		if !seen[name] {
			seen[name] = true
			t.EventNames = append(t.EventNames, name)
		}
		return true
	})
	sort.Strings(t.EventNames)
}

// declText drops the `export` and `declare` before a declaration and the
// indentation its lines after the first share.
func declText(s string) string {
	s = dedent(strings.TrimSpace(s))
	for _, kw := range []string{"export", "declare"} {
		if strings.HasPrefix(s, kw) && len(s) > len(kw) && (s[len(kw)] == ' ' || s[len(kw)] == '\t' || s[len(kw)] == '\n') {
			s = strings.TrimSpace(s[len(kw):])
		}
	}
	return s
}

// typeParamNames lists the names of a `generics` attribute.
func typeParamNames(generics string) ([]string, bool) {
	prog, diags := jsast.ParseWithOptions("function f<"+generics+">() {}", jsast.ParseOptions{TypeScript: true})
	if len(diags) > 0 || prog == nil || len(prog.Body) != 1 {
		return nil, false
	}
	fn, ok := prog.Body[0].(*jsast.JsFuncDecl)
	if !ok || fn.TS == nil || len(fn.TS.TypeParams) == 0 {
		return nil, false
	}
	var names []string
	for _, p := range fn.TS.TypeParams {
		names = append(names, p.Name)
	}
	return names, true
}

// typeTextRefs parses a type text and lists the names it references; the
// type parameters of the component are in scope. ok is false when the text
// is not a TypeScript type.
func typeTextRefs(text, generics string) ([]TypeRef, bool) {
	params := ""
	if generics != "" {
		params = "<" + generics + ">"
	}
	return parsedRefs("type $$T" + params + " = (\n" + text + "\n);")
}

// typeDeclRefs lists the names a declaration (an interface or a type alias)
// references, with the type parameters of the component in scope.
func typeDeclRefs(decl, generics string) ([]TypeRef, bool) {
	if generics == "" {
		return parsedRefs(decl)
	}
	// The declaration is parsed inside a generic function, where the
	// component's type parameters are in scope.
	refs, ok := parsedRefs("function $$scope<" + generics + ">() {\n" + decl + "\n}")
	if !ok {
		return nil, false
	}
	params, _ := typeParamNames(generics)
	var out []TypeRef
	for _, r := range refs {
		if !containsString(params, r.Name) {
			out = append(out, r)
		}
	}
	return out, true
}

func parsedRefs(src string) ([]TypeRef, bool) {
	prog, diags := jsast.ParseWithOptions(src, jsast.ParseOptions{TypeScript: true})
	if len(diags) > 0 || prog == nil || prog.TS == nil {
		return nil, false
	}
	seen := map[TypeRef]bool{}
	var out []TypeRef
	for _, r := range prog.TS.Refs {
		if r.Name == "$$T" {
			continue
		}
		ref := TypeRef{Name: r.Name, Value: r.Query}
		if !seen[ref] {
			seen[ref] = true
			out = append(out, ref)
		}
	}
	return out, true
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// literalType is the type of a literal default, widened as TypeScript
// widens a `let`; "" when the default says nothing the compiler can tell.
func literalType(e jsast.JsExpr) string {
	switch x := e.(type) {
	case nil:
		return ""
	case *jsast.JsLiteral:
		switch x.Kind {
		case "string":
			return "string"
		case "number":
			return "number"
		case "boolean":
			return "boolean"
		case "bigint":
			return "bigint"
		}
	case *jsast.JsTemplateLit:
		return "string"
	case *jsast.JsUnaryExpr:
		if x.Op == "-" || x.Op == "+" {
			if lit, ok := x.X.(*jsast.JsLiteral); ok && lit.Kind == "number" {
				return "number"
			}
		}
		if x.Op == "!" {
			return "boolean"
		}
	case *jsast.JsArrayExpr:
		elem := ""
		for _, el := range x.Elems {
			t := literalType(el)
			if t == "" || elem != "" && t != elem {
				return "any[]"
			}
			elem = t
		}
		if elem == "" {
			return "any[]"
		}
		return elem + "[]"
	}
	return ""
}

// precedingJSDoc is the /** … */ comment that ends right before pos (white
// space between), or "".
func precedingJSDoc(src string, pos int) string {
	i := min(pos, len(src))
	for i > 0 && strings.ContainsRune(" \t\r\n", rune(src[i-1])) {
		i--
	}
	if i < 2 || src[i-2:i] != "*/" {
		return ""
	}
	open := strings.LastIndex(src[:i-2], "/**")
	if open < 0 || strings.Contains(src[open+3:i-2], "*/") {
		return ""
	}
	return src[open:i]
}

// jsdocTag returns the {type} of the first tag (`@type`) of a JSDoc
// comment; braces nest, so `{{ a: string }}` is one type.
func jsdocTag(comment, tag string) (string, bool) {
	at := strings.Index(comment, tag)
	for at >= 0 {
		end := at + len(tag)
		if end < len(comment) && !jsast.IsIdentCont(comment[end]) {
			if typ, _, ok := jsdocBraced(comment, end); ok {
				return typ, true
			}
		}
		next := strings.Index(comment[end:], tag)
		if next < 0 {
			break
		}
		at = end + next
	}
	return "", false
}

// jsdocBraced reads the {…} after white space at i: the type, the offset
// after the brace.
func jsdocBraced(comment string, i int) (string, int, bool) {
	for i < len(comment) && (comment[i] == ' ' || comment[i] == '\t') {
		i++
	}
	if i >= len(comment) || comment[i] != '{' {
		return "", 0, false
	}
	depth := 0
	for j := i; j < len(comment); j++ {
		switch comment[j] {
		case '{':
			depth++
		case '}':
			if depth--; depth == 0 {
				return jsdocText(comment[i+1 : j]), j + 1, true
			}
		}
	}
	return "", 0, false
}

// jsdocText joins the lines of a type that spans comment lines.
func jsdocText(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		lines[i] = strings.TrimSpace(strings.TrimPrefix(line, "*"))
	}
	return strings.TrimSpace(strings.Join(lines, " "))
}

// jsdocTypedefs reads the `@typedef {Type} Name` tags of a JavaScript
// script: `$$Events` and `$$Slots` (TS-4.9), and types they reference.
func jsdocTypedefs(src, generics string) []*TypeDecl {
	var out []*TypeDecl
	for from := 0; ; {
		open := strings.Index(src[from:], "/**")
		if open < 0 {
			return out
		}
		open += from
		close := strings.Index(src[open+3:], "*/")
		if close < 0 {
			return out
		}
		close += open + 3
		comment := src[open : close+2]
		from = close + 2
		for at := strings.Index(comment, "@typedef"); at >= 0; {
			typ, after, ok := jsdocBraced(comment, at+len("@typedef"))
			if ok {
				name := after
				for name < len(comment) && (comment[name] == ' ' || comment[name] == '\t') {
					name++
				}
				end := name
				for end < len(comment) && (jsast.IsIdentCont(comment[end]) || comment[end] == '$') {
					end++
				}
				if end > name {
					if refs, ok := typeTextRefs(typ, generics); ok {
						out = append(out, &TypeDecl{Name: comment[name:end], Text: typ, Refs: refs, JSDoc: true})
					}
				}
			}
			next := strings.Index(comment[at+1:], "@typedef")
			if next < 0 {
				break
			}
			at += 1 + next
		}
	}
}

func walkSlots(nodes []sfcast.Node, visit func(string)) {
	for _, node := range nodes {
		switch n := node.(type) {
		case *sfcast.ElementNode:
			walkSlots(n.Children, visit)
		case *sfcast.ComponentNode:
			for _, slot := range n.Slots {
				walkSlots(slot, visit)
			}
		case *sfcast.IfChainNode:
			for _, b := range n.Branches {
				walkSlots(b.Children, visit)
			}
		case *sfcast.EachBlockNode:
			walkSlots(n.Children, visit)
			walkSlots(n.ElseChildren, visit)
		case *sfcast.AwaitBlockNode:
			walkSlots(n.PendingChildren, visit)
			walkSlots(n.ThenChildren, visit)
			walkSlots(n.CatchChildren, visit)
		case *sfcast.HeadNode:
			walkSlots(n.Children, visit)
		case *sfcast.BoundaryBlockNode:
			walkSlots(n.Children, visit)
			walkSlots(n.CatchChildren, visit)
		case *sfcast.SlotNode:
			visit(n.Name)
			walkSlots(n.Fallback, visit)
		}
	}
}

// dedent removes from the lines after the first the indentation they share
// (the declaration's own indentation in the script).
func dedent(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) < 2 {
		return s
	}
	common := -1
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		n := len(line) - len(strings.TrimLeft(line, " \t"))
		if common < 0 || n < common {
			common = n
		}
	}
	if common <= 0 {
		return s
	}
	for i := 1; i < len(lines); i++ {
		if len(lines[i]) >= common && strings.TrimSpace(lines[i][:common]) == "" {
			lines[i] = lines[i][common:]
		}
	}
	return strings.Join(lines, "\n")
}

// declaredEvents sets the event names to the members of $$Events.
func (t *ComponentTypes) declaredEvents(names []string) {
	t.EventNames, t.AnyEvents = names, false
	sort.Strings(t.EventNames)
}

// objectMembers are the property names of an object type.
func objectMembers(typ *jsast.TSType) []string {
	for typ != nil && typ.Kind == jsast.TSParen && len(typ.Args) == 1 {
		typ = typ.Args[0]
	}
	if typ == nil || typ.Kind != jsast.TSObject {
		return nil
	}
	var out []string
	for _, m := range typ.Members {
		if (m.Kind == jsast.TSPropertySig || m.Kind == jsast.TSMethodSig) && !m.Computed {
			out = append(out, m.Name)
		}
	}
	return out
}
