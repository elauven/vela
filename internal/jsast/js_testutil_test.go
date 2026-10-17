package jsast

// Shared helpers for the parser test-suite.
//
// Design notes
//   - The dumper and the invariant checker are reflection based and dispatch
//     on the *name* of the AST node type.  They therefore keep compiling when
//     AST nodes/fields are added or renamed, and the same test-suite can be run
//     against older revisions of the parser to see what changed.
//   - All helpers are prefixed with "ct" (compat test) to avoid clashes.

import (
	"fmt"
	"reflect"
	"strings"
	"time"
)

// ──────────────────────────────────────────────
//  Reflection accessors
// ──────────────────────────────────────────────

func ctStruct(n any) (reflect.Value, bool) {
	rv := reflect.ValueOf(n)
	if !rv.IsValid() {
		return reflect.Value{}, false
	}
	for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return reflect.Value{}, false
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return reflect.Value{}, false
	}
	return rv, true
}

func ctTypeName(n any) string {
	if s, ok := ctStruct(n); ok {
		return s.Type().Name()
	}
	return "<nil>"
}

// ctField returns the named field (promoted through embedded structs) or an
// invalid Value when the field does not exist in this revision of the AST.
func ctField(s reflect.Value, name string) reflect.Value {
	return s.FieldByName(name)
}

func ctStr(s reflect.Value, name string) string {
	f := ctField(s, name)
	if f.IsValid() && f.Kind() == reflect.String {
		return f.String()
	}
	return ""
}

func ctBool(s reflect.Value, name string) bool {
	f := ctField(s, name)
	return f.IsValid() && f.Kind() == reflect.Bool && f.Bool()
}

// ──────────────────────────────────────────────
//  S-expression dumper
// ──────────────────────────────────────────────

// ctDump renders a node as a compact S-expression, e.g.
//
//	a + b * c   →   (expr (+ a (* b c)))
//
// The format is intentionally lossy (no positions) — positions are covered
// by the invariant checks instead.
func ctDump(n any) string {
	s, ok := ctStruct(n)
	if !ok {
		return "<nil>"
	}
	one := func(name string) string { return ctDumpVal(ctField(s, name)) }
	list := func(name string) string { return ctDumpList(ctField(s, name)) }
	withList := func(head string, parts ...string) string {
		out := []string{head}
		for _, p := range parts {
			if p != "" {
				out = append(out, p)
			}
		}
		return "(" + strings.Join(out, " ") + ")"
	}
	fnHead := func() string {
		h := "function"
		if ctBool(s, "Generator") {
			h += "*"
		}
		if ctBool(s, "Async") {
			h = "async " + h
		}
		return h
	}

	switch s.Type().Name() {
	case "JsProgram":
		return withList("program", list("Body"))

	// ── statements ──
	case "JsImportDecl":
		var parts []string
		if v := ctStr(s, "DefaultName"); v != "" {
			parts = append(parts, "def:"+v)
		}
		if v := ctStr(s, "Namespace"); v != "" {
			parts = append(parts, "ns:"+v)
		}
		if sp := ctField(s, "Specifiers"); sp.IsValid() && sp.Len() > 0 {
			var names []string
			for i := 0; i < sp.Len(); i++ {
				e := sp.Index(i)
				imp, loc := ctStr(e, "Imported"), ctStr(e, "Local")
				if imp == loc {
					names = append(names, imp)
				} else {
					names = append(names, imp+">"+loc)
				}
			}
			parts = append(parts, "{"+strings.Join(names, " ")+"}")
		}
		parts = append(parts, "'"+ctStr(s, "Source")+"'")
		if at := ctDumpAttrs(ctField(s, "Attributes")); at != "" {
			parts = append(parts, at)
		}
		return withList("import", parts...)
	case "JsExportDecl":
		if ctBool(s, "All") || ctStr(s, "Namespace") != "" || ctField(s, "Declaration").IsValid() && ctField(s, "Declaration").IsNil() {
			var parts []string
			switch {
			case ctStr(s, "Namespace") != "":
				parts = append(parts, "*>"+ctStr(s, "Namespace"))
			case ctBool(s, "All"):
				parts = append(parts, "*")
			}
			if sp := ctField(s, "Specifiers"); sp.IsValid() && sp.Len() > 0 {
				var names []string
				for i := 0; i < sp.Len(); i++ {
					e := sp.Index(i)
					l, x := ctStr(e, "Local"), ctStr(e, "Exported")
					if l == x {
						names = append(names, l)
					} else {
						names = append(names, l+">"+x)
					}
				}
				parts = append(parts, "{"+strings.Join(names, " ")+"}")
			} else if !ctBool(s, "All") && ctStr(s, "Namespace") == "" {
				parts = append(parts, "{}")
			}
			if ctBool(s, "HasSource") {
				parts = append(parts, "from", "'"+ctStr(s, "Source")+"'")
			}
			if at := ctDumpAttrs(ctField(s, "Attributes")); at != "" {
				parts = append(parts, at)
			}
			return withList("export", parts...)
		}
		if ctBool(s, "Default") {
			return withList("export-default", one("Declaration"))
		}
		return withList("export", one("Declaration"))
	case "JsVarDecl":
		return withList(ctStr(s, "Kind"), list("Decls"))
	case "JsVarDeclarator":
		if ctIsNilField(s, "Init") {
			return one("Id")
		}
		return withList("=", one("Id"), one("Init"))
	case "JsFuncDecl":
		return withList(fnHead(), ctStr(s, "Name"), "("+list("Params")+")", one("Body"))
	case "JsClassDecl", "JsClassExpr":
		head := "class"
		if s.Type().Name() == "JsClassExpr" {
			head = "class-expr"
		}
		parts := []string{ctStr(s, "Name")}
		if !ctIsNilField(s, "SuperClass") {
			parts = append(parts, "<", one("SuperClass"))
		}
		parts = append(parts, list("Members"))
		return withList(head, parts...)
	case "JsClassMember":
		kind := ctStr(s, "Kind")
		if ctBool(s, "Accessor") {
			kind = "accessor " + kind
		}
		if ctBool(s, "Static") {
			kind = "static " + kind
		}
		if kind == "static static-block" || kind == "static-block" {
			return withList("static-block", one("Body"))
		}
		key := one("Key")
		if ctBool(s, "Computed") {
			key = "[" + key + "]"
		}
		if ctIsNilField(s, "Value") {
			return withList(kind, key)
		}
		return withList(kind, key, one("Value"))
	case "JsBlock":
		return withList("block", list("Body"))
	case "JsExprStmt":
		return withList("expr", one("X"))
	case "JsIfStmt":
		if ctIsNilField(s, "Else") {
			return withList("if", one("Test"), one("Then"))
		}
		return withList("if", one("Test"), one("Then"), one("Else"))
	case "JsForStmt":
		return withList("for", one("Init"), one("Test"), one("Update"), one("Body"))
	case "JsForInStmt":
		head := "for-in"
		if ctBool(s, "IsOf") {
			head = "for-of"
		}
		if ctBool(s, "Await") {
			head = "for-await-of"
		}
		return withList(head, one("Left"), one("Right"), one("Body"))
	case "JsWhileStmt":
		return withList("while", one("Test"), one("Body"))
	case "JsDoWhileStmt":
		return withList("do-while", one("Body"), one("Test"))
	case "JsReturnStmt":
		if ctIsNilField(s, "Arg") {
			return "(return)"
		}
		return withList("return", one("Arg"))
	case "JsThrowStmt":
		return withList("throw", one("Arg"))
	case "JsTryStmt":
		parts := []string{one("Block")}
		if !ctIsNilField(s, "Handler") {
			if ctIsNilField(s, "CatchParam") {
				parts = append(parts, withList("catch", one("Handler")))
			} else {
				parts = append(parts, withList("catch", one("CatchParam"), one("Handler")))
			}
		}
		if !ctIsNilField(s, "Finalizer") {
			parts = append(parts, withList("finally", one("Finalizer")))
		}
		return withList("try", parts...)
	case "JsSwitchStmt":
		return withList("switch", one("Disc"), list("Cases"))
	case "JsSwitchCase":
		if ctIsNilField(s, "Test") {
			return withList("default", list("Body"))
		}
		return withList("case", one("Test"), list("Body"))
	case "JsBreakStmt":
		return withList("break", ctStr(s, "Label"))
	case "JsContinueStmt":
		return withList("continue", ctStr(s, "Label"))
	case "JsLabeledStmt":
		return withList("label", ctStr(s, "Label"), one("Body"))
	case "JsEmptyStmt":
		return "(empty)"
	case "JsDebuggerStmt":
		return "(debugger)"

	// ── expressions ──
	case "JsIdent":
		return ctStr(s, "Name")
	case "JsLiteral":
		if ctStr(s, "Kind") == "unknown" {
			return "<unknown:" + ctStr(s, "Raw") + ">"
		}
		return ctStr(s, "Raw")
	case "JsTemplateLit":
		var parts []string
		q := ctField(s, "Quasis")
		e := ctField(s, "Exprs")
		for i := 0; i < q.Len(); i++ {
			parts = append(parts, fmt.Sprintf("%q", q.Index(i).String()))
			if i < e.Len() {
				parts = append(parts, ctDumpVal(e.Index(i)))
			}
		}
		return withList("tpl", parts...)
	case "JsTaggedTemplate":
		return withList("tagged", one("Tag"), one("Quasi"))
	case "JsArrayExpr":
		return withList("array", ctDumpListHoles(ctField(s, "Elems")))
	case "JsObjectExpr":
		return withList("obj", list("Props"))
	case "JsProp":
		key := one("Key")
		if ctBool(s, "Computed") {
			key = "[" + key + "]"
		}
		switch {
		case ctBool(s, "Spread"):
			return withList("...", one("Value"))
		case ctStr(s, "Kind") == "get" || ctStr(s, "Kind") == "set":
			return withList(ctStr(s, "Kind"), key, one("Value"))
		case ctBool(s, "Method"):
			return withList("method", key, one("Value"))
		case ctBool(s, "Shorthand"):
			if v := ctField(s, "Value"); v.IsValid() && !v.IsNil() && ctTypeName(v.Interface()) == "JsAssignExpr" {
				vs, _ := ctStruct(v.Interface())
				return withList("short", key, "=", ctDumpVal(ctField(vs, "R")))
			}
			return withList("short", key)
		}
		return withList("prop", key, one("Value"))
	case "JsFuncExpr":
		return withList(fnHead(), ctStr(s, "Name"), "("+list("Params")+")", one("Body"))
	case "JsArrowFunc":
		head := "=>"
		if ctBool(s, "Async") {
			head = "async=>"
		}
		return withList(head, "("+list("Params")+")", one("Body"))
	case "JsUnaryExpr":
		if ctBool(s, "Prefix") {
			return withList(ctStr(s, "Op"), one("X"))
		}
		return withList("post"+ctStr(s, "Op"), one("X"))
	case "JsBinaryExpr", "JsLogicalExpr", "JsAssignExpr":
		return withList(ctStr(s, "Op"), one("L"), one("R"))
	case "JsMemberExpr":
		op := "."
		if ctBool(s, "Computed") {
			op = "[]"
		}
		if ctBool(s, "Optional") {
			op = "?" + op
		}
		return withList(op, one("Obj"), one("Prop"))
	case "JsCallExpr":
		head := "call"
		if ctBool(s, "Optional") {
			head = "?call"
		}
		return withList(head, one("Callee"), list("Args"))
	case "JsNewExpr":
		return withList("new", one("Callee"), list("Args"))
	case "JsConditionalExpr":
		return withList("?", one("Test"), one("Then"), one("Else"))
	case "JsSeqExpr":
		return withList("seq", list("Exprs"))
	case "JsSpreadExpr":
		return withList("...", one("X"))
	case "JsAwaitExpr":
		return withList("await", one("X"))
	case "JsYieldExpr":
		head := "yield"
		if ctBool(s, "Delegate") {
			head = "yield*"
		}
		if ctIsNilField(s, "X") {
			return "(" + head + ")"
		}
		return withList(head, one("X"))
	case "JsThisExpr":
		return "this"
	case "JsSuperExpr":
		return "super"
	}

	// Unknown node type (e.g. added after this file was written): generic dump.
	var kids []string
	for _, c := range ctChildren(n) {
		kids = append(kids, ctDump(c))
	}
	return withList(s.Type().Name(), kids...)
}

func ctIsNilField(s reflect.Value, name string) bool {
	f := ctField(s, name)
	if !f.IsValid() {
		return true
	}
	switch f.Kind() {
	case reflect.Interface, reflect.Ptr, reflect.Slice, reflect.Map:
		return f.IsNil()
	}
	return false
}

func ctDumpVal(v reflect.Value) string {
	if !v.IsValid() {
		return "<nil>"
	}
	switch v.Kind() {
	case reflect.Interface, reflect.Ptr:
		if v.IsNil() {
			return "<nil>"
		}
		return ctDump(v.Interface())
	}
	return "<?>"
}

func ctDumpList(v reflect.Value) string {
	if !v.IsValid() || v.Kind() != reflect.Slice {
		return ""
	}
	parts := make([]string, 0, v.Len())
	for i := 0; i < v.Len(); i++ {
		parts = append(parts, ctDumpVal(v.Index(i)))
	}
	return strings.Join(parts, " ")
}

func ctDumpListHoles(v reflect.Value) string {
	if !v.IsValid() || v.Kind() != reflect.Slice {
		return ""
	}
	parts := make([]string, 0, v.Len())
	for i := 0; i < v.Len(); i++ {
		e := v.Index(i)
		if (e.Kind() == reflect.Interface || e.Kind() == reflect.Ptr) && e.IsNil() {
			parts = append(parts, "<hole>")
			continue
		}
		parts = append(parts, ctDumpVal(e))
	}
	return strings.Join(parts, " ")
}

func ctDumpAttrs(v reflect.Value) string {
	if !v.IsValid() || v.Kind() != reflect.Slice || v.Len() == 0 {
		return ""
	}
	var parts []string
	for i := 0; i < v.Len(); i++ {
		parts = append(parts, ctStr(v.Index(i), "Key")+"="+ctStr(v.Index(i), "Value"))
	}
	return "with{" + strings.Join(parts, ",") + "}"
}

// ──────────────────────────────────────────────
//  Generic child traversal (independent of WalkNode)
// ──────────────────────────────────────────────

func ctChildren(n any) []JsNode {
	s, ok := ctStruct(n)
	if !ok {
		return nil
	}
	var out []JsNode
	for i := 0; i < s.NumField(); i++ {
		f := s.Field(i)
		if !f.CanInterface() {
			continue
		}
		switch f.Kind() {
		case reflect.Interface, reflect.Ptr:
			if !f.IsNil() {
				if c, ok := f.Interface().(JsNode); ok {
					out = append(out, c)
				}
			}
		case reflect.Slice:
			for j := 0; j < f.Len(); j++ {
				e := f.Index(j)
				if (e.Kind() == reflect.Interface || e.Kind() == reflect.Ptr) && !e.IsNil() && e.CanInterface() {
					if c, ok := e.Interface().(JsNode); ok {
						out = append(out, c)
					}
				}
			}
		}
	}
	return out
}

// ──────────────────────────────────────────────
//  Parsing with a watchdog
// ──────────────────────────────────────────────

type ctParsed struct {
	prog  *JsProgram
	diags []JSParseDiagnostic
	panic any
	hang  bool
}

// ctParse parses src with panic recovery and a timeout so a single bad input
// cannot take the whole test binary down.
func ctParse(src string) ctParsed {
	done := make(chan ctParsed, 1)
	go func() {
		var r ctParsed
		defer func() {
			if e := recover(); e != nil {
				r.panic = e
			}
			done <- r
		}()
		r.prog, r.diags = ParseJSWithDiagnostics(src)
	}()
	// The race detector makes parsing several times slower.
	timeout := 3 * time.Second
	if raceEnabled {
		timeout *= 10
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-done:
		return r
	case <-timer.C:
		return ctParsed{hang: true}
	}
}

// ──────────────────────────────────────────────
//  Invariants
// ──────────────────────────────────────────────

// ctInvariants verifies structural properties every parsed tree must have,
// regardless of what the source means.  Returns a list of violations.
func ctInvariants(src string, prog *JsProgram) []string {
	var bad []string
	seen := map[JsNode]int{}
	var visit func(n JsNode, parent JsNode)
	visit = func(n JsNode, parent JsNode) {
		name := ctTypeName(n)
		pos, end := n.NodePos(), n.NodeEnd()
		seen[n]++
		if seen[n] > 1 {
			bad = append(bad, fmt.Sprintf("%s [%d,%d) is reachable twice (shared node)", name, pos, end))
			return
		}
		if pos < 0 || end < pos || end > len(src) {
			bad = append(bad, fmt.Sprintf("%s has invalid span [%d,%d) for source length %d", name, pos, end, len(src)))
			return
		}
		if name != "JsProgram" && pos == 0 && end == 0 {
			bad = append(bad, fmt.Sprintf("%s has no source position (synthetic 0,0 span)", name))
		}
		if parent != nil {
			pp, pe := parent.NodePos(), parent.NodeEnd()
			if !(pp == 0 && pe == 0) && (pos < pp || end > pe) {
				bad = append(bad, fmt.Sprintf("%s [%d,%d) escapes parent %s [%d,%d)", name, pos, end, ctTypeName(parent), pp, pe))
			}
		}
		if end > pos {
			text := src[pos:end]
			switch name {
			case "JsIdent":
				s, _ := ctStruct(n)
				if nm := ctStr(s, "Name"); nm != text && !strings.Contains(text, "\\") {
					bad = append(bad, fmt.Sprintf("JsIdent %q does not match source text %q at [%d,%d)", nm, text, pos, end))
				}
			case "JsLiteral":
				s, _ := ctStruct(n)
				if raw := ctStr(s, "Raw"); raw != text && ctStr(s, "Kind") != "unknown" {
					bad = append(bad, fmt.Sprintf("JsLiteral raw %q does not match source text %q at [%d,%d)", raw, text, pos, end))
				}
			}
		}
		for _, c := range ctChildren(n) {
			visit(c, n)
		}
	}
	visit(prog, nil)
	return bad
}

// ctUnknownNodes lists placeholder literals the parser emitted for tokens it
// could not make sense of.
func ctUnknownNodes(prog *JsProgram) []string {
	var out []string
	var visit func(n JsNode)
	visit = func(n JsNode) {
		if ctTypeName(n) == "JsLiteral" {
			s, _ := ctStruct(n)
			if k := ctStr(s, "Kind"); k == "unknown" {
				out = append(out, ctStr(s, "Raw"))
			}
		}
		for _, c := range ctChildren(n) {
			visit(c)
		}
	}
	visit(prog)
	return out
}

// ──────────────────────────────────────────────
//  Feature-case runner
// ──────────────────────────────────────────────

// ctCheckValid parses src and returns "" when the parse is clean, i.e.:
//
//	no panic / hang, no diagnostics, no placeholder nodes, invariants hold and
//	(when want != "") the dumped tree equals want.
func ctCheckValid(src, want string) string {
	r := ctParse(src)
	switch {
	case r.hang:
		return "parser did not terminate (3s timeout)"
	case r.panic != nil:
		return fmt.Sprintf("parser panicked: %v", r.panic)
	}
	var problems []string
	for _, d := range r.diags {
		problems = append(problems, fmt.Sprintf("diagnostic @%d: %s", d.Pos, d.Message))
	}
	for _, u := range ctUnknownNodes(r.prog) {
		problems = append(problems, fmt.Sprintf("placeholder node for unparsed token %q", u))
	}
	problems = append(problems, ctInvariants(src, r.prog)...)
	if want != "" {
		if got := ctDump(r.prog); got != want {
			problems = append(problems, fmt.Sprintf("tree mismatch\n      got:  %s\n      want: %s", got, want))
		}
	}
	if len(problems) == 0 {
		return ""
	}
	return strings.Join(problems, "\n    ")
}

// ctCheckInvalid expects src to be rejected: at least one diagnostic (or a
// placeholder node) and, above all, no panic and no hang.
func ctCheckInvalid(src string) string {
	r := ctParse(src)
	switch {
	case r.hang:
		return "parser did not terminate (3s timeout)"
	case r.panic != nil:
		return fmt.Sprintf("parser panicked: %v", r.panic)
	case len(r.diags) == 0 && len(EarlyErrors(r.prog, EarlyOptions{TopLevelAwait: true})) == 0:
		// Early errors are reported by EarlyErrors, the validation pass
		// the compiler runs after parsing (task 055).
		return "no diagnostic reported for invalid input (parser accepted it silently)"
	}
	return ""
}
