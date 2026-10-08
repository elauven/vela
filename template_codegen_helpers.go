package main

import (
	"fmt"
	"sort"
	"strings"
)

// rewriteItemRef parses src, walks the AST with scope tracking, and replaces
// every bare reference to itemVar with boxVar.  Property-access positions
// (`obj.itemVar`, `{itemVar: …}` as a key) and shadowed references (inside
// a function whose parameter list or a nested `let/const/var` re-declares
// itemVar) are correctly left untouched.
//
// Used by emitEachRowBody to rewrite event-handler source text so row-scoped
// handlers read the always-fresh "current item" box rather than closing
// over the forEach callback's `itemVar` parameter (which never updates).
//
// Behaviour matrix:
//
//	() => openEdit(u)             →  () => openEdit($$item_N)
//	() => toggleStatus(u.id)      →  () => toggleStatus($$item_N.id)
//	() => obj.u                   →  unchanged (property access)
//	() => ({u: x})                →  unchanged (object key)
//	() => ({[u]: x})              →  ({[$$item_N]: x})  (computed key)
//	(u) => u + 1                  →  unchanged (shadowed)
//	() => { const u = 1; return u; } →  unchanged (block-scoped shadow)
//	`${u}`                        →  `${$$item_N}` (template hole — now
//	                                 handled correctly; the old token-based
//	                                 rewriter couldn't descend into these)
//
// visitItemRefs visits references to an each-block item binding that are not
// shadowed by a nested lexical scope. The callback may inspect or mutate the
// matching identifier. It intentionally excludes member-property names and
// non-computed object keys, while preserving computed keys, shorthand values,
// spreads, and template-literal interpolations.
func visitItemRefs(root JsNode, itemVar string, onRef func(*JsIdent)) {
	if root == nil || itemVar == "" || onRef == nil {
		return
	}

	// Scope stack: each entry is true iff itemVar is shadowed at that level.
	// The outermost scope starts unshadowed.
	shadow := []bool{false}
	isShadowed := func() bool {
		for i := len(shadow) - 1; i >= 0; i-- {
			if shadow[i] {
				return true
			}
		}
		return false
	}

	// patternBindsItem reports whether a binding pattern (JsIdent, or
	// JsObject/JsArrayExpr used as a pattern) introduces a new binding named
	// itemVar.  Called on function parameters and var-decl LHS.
	var patternBindsItem func(JsNode) bool
	patternBindsItem = func(n JsNode) bool {
		switch p := n.(type) {
		case *JsIdent:
			return p.Name == itemVar
		case *JsObjectExpr:
			for _, prop := range p.Props {
				if prop.Spread {
					if patternBindsItem(prop.Value) {
						return true
					}
					continue
				}
				if patternBindsItem(prop.Value) {
					return true
				}
			}
		case *JsArrayExpr:
			for _, el := range p.Elems {
				if el == nil {
					continue
				}
				if sp, ok := el.(*JsSpreadExpr); ok {
					if patternBindsItem(sp.X) {
						return true
					}
					continue
				}
				if patternBindsItem(el) {
					return true
				}
			}
		case *JsAssignExpr:
			// Default value in a pattern:  { x = 5 }  or  (x = 5) => ...
			if p.Op == "=" {
				return patternBindsItem(p.L)
			}
		}
		return false
	}

	// blockShadowsItem scans direct children of a block for `let/const/var
	// itemVar` declarations.  var-scoping up-leaks through blocks in real
	// JS but in the handler bodies we see (short, arrow-flavoured code),
	// treating a block-level var as block-scoped is safe: we only rewrite
	// BARE references, and if something is declared by any name in an
	// enclosing scope, rewriting the name would be wrong anyway.
	blockShadowsItem := func(body []JsStmt) bool {
		for _, s := range body {
			vd, ok := s.(*JsVarDecl)
			if !ok {
				continue
			}
			for _, d := range vd.Decls {
				if patternBindsItem(d.Id) {
					return true
				}
			}
		}
		return false
	}

	// visit is the recursive walker.  Mirrors WalkNode's shape but handles
	// scope push/pop and filters property-access positions explicitly.
	var visit func(JsNode)
	visit = func(n JsNode) {
		if n == nil {
			return
		}
		switch node := n.(type) {

		case *JsIdent:
			if node.Name == itemVar && !isShadowed() {
				onRef(node)
			}

		// ── Scope-introducing nodes ──────────────────────────────────────
		case *JsArrowFunc:
			shadowed := false
			for _, p := range node.Params {
				if patternBindsItem(p) {
					shadowed = true
					break
				}
			}
			shadow = append(shadow, shadowed)
			// Visit param defaults (RHS of `= default`), but their LHS
			// bindings should not trigger rewrites — patternBindsItem
			// already told us if the bound name matches.  For pattern
			// analysis we conservatively skip visiting params entirely,
			// since template handlers don't typically put state-refs in
			// default expressions.
			visit(node.Body)
			shadow = shadow[:len(shadow)-1]
		case *JsFuncExpr:
			shadowed := false
			for _, p := range node.Params {
				if patternBindsItem(p) {
					shadowed = true
					break
				}
			}
			shadow = append(shadow, shadowed)
			visit(node.Body)
			shadow = shadow[:len(shadow)-1]
		case *JsFuncDecl:
			shadowed := false
			for _, p := range node.Params {
				if patternBindsItem(p) {
					shadowed = true
					break
				}
			}
			shadow = append(shadow, shadowed)
			visit(node.Body)
			shadow = shadow[:len(shadow)-1]
		case *JsBlock:
			shadow = append(shadow, blockShadowsItem(node.Body))
			for _, s := range node.Body {
				visit(s)
			}
			shadow = shadow[:len(shadow)-1]

		// ── Property-access filtering ────────────────────────────────────
		case *JsMemberExpr:
			visit(node.Obj)
			// Skip node.Prop unless it's computed — `x[u]` should
			// rewrite the `u`, but `x.u` should leave it alone.
			if node.Computed {
				visit(node.Prop)
			}
		case *JsObjectExpr:
			for _, prop := range node.Props {
				// Computed keys `{[expr]: x}` ARE value-position expressions
				// and should be rewritten.  Literal keys `{u: x}` are not
				// references to the state var — skip.
				if prop.Computed {
					visit(prop.Key)
				}
				// Spread:  {...rest}  — Value is the expression being spread.
				if prop.Spread {
					visit(prop.Value)
					continue
				}
				// Regular property (`{u: x}` or shorthand `{u}`): Value is
				// the value-position expression.  For shorthand the parser
				// uses the same JsIdent for Key and Value, so visiting Value
				// correctly registers a hit.
				visit(prop.Value)
			}

		case *JsProgram:
			for _, s := range node.Body {
				visit(s)
			}
		case *JsExprStmt:
			visit(node.X)
		case *JsVarDecl:
			shadowHere := blockShadowsItem([]JsStmt{node})
			if shadowHere {
				// The declaration shadows — but its Init expressions are
				// evaluated BEFORE the binding is in scope (TDZ notwithstanding
				// for sequential decls).  For simplicity we treat the whole
				// statement as shadowed, which may miss a theoretical edge
				// case but is correct in every handler pattern we see.
				shadow = append(shadow, true)
				for _, d := range node.Decls {
					if d.Init != nil {
						visit(d.Init)
					}
				}
				shadow = shadow[:len(shadow)-1]
			} else {
				for _, d := range node.Decls {
					if d.Init != nil {
						visit(d.Init)
					}
				}
			}
		case *JsIfStmt:
			visit(node.Test)
			visit(node.Then)
			if node.Else != nil {
				visit(node.Else)
			}
		case *JsForStmt:
			if node.Init != nil {
				visit(node.Init)
			}
			if node.Test != nil {
				visit(node.Test)
			}
			if node.Update != nil {
				visit(node.Update)
			}
			visit(node.Body)
		case *JsForInStmt:
			visit(node.Left)
			visit(node.Right)
			visit(node.Body)
		case *JsWhileStmt:
			visit(node.Test)
			visit(node.Body)
		case *JsDoWhileStmt:
			visit(node.Body)
			visit(node.Test)
		case *JsReturnStmt:
			if node.Arg != nil {
				visit(node.Arg)
			}
		case *JsThrowStmt:
			visit(node.Arg)
		case *JsTryStmt:
			visit(node.Block)
			if node.CatchParam != nil {
				// catch(param) — treat the param as a new binding; if it
				// shadows itemVar, the handler body shouldn't rewrite.
				shadowed := patternBindsItem(node.CatchParam)
				shadow = append(shadow, shadowed)
				if node.Handler != nil {
					visit(node.Handler)
				}
				shadow = shadow[:len(shadow)-1]
			} else if node.Handler != nil {
				visit(node.Handler)
			}
			if node.Finalizer != nil {
				visit(node.Finalizer)
			}
		case *JsSwitchStmt:
			visit(node.Disc)
			for _, c := range node.Cases {
				if c.Test != nil {
					visit(c.Test)
				}
				for _, s := range c.Body {
					visit(s)
				}
			}
		case *JsUnaryExpr:
			visit(node.X)
		case *JsBinaryExpr:
			visit(node.L)
			visit(node.R)
		case *JsLogicalExpr:
			visit(node.L)
			visit(node.R)
		case *JsAssignExpr:
			visit(node.L)
			visit(node.R)
		case *JsCallExpr:
			visit(node.Callee)
			for _, a := range node.Args {
				visit(a)
			}
		case *JsNewExpr:
			visit(node.Callee)
			for _, a := range node.Args {
				visit(a)
			}
		case *JsConditionalExpr:
			visit(node.Test)
			visit(node.Then)
			visit(node.Else)
		case *JsSeqExpr:
			for _, e := range node.Exprs {
				visit(e)
			}
		case *JsSpreadExpr:
			visit(node.X)
		case *JsAwaitExpr:
			visit(node.X)
		case *JsYieldExpr:
			if node.X != nil {
				visit(node.X)
			}
		case *JsArrayExpr:
			for _, e := range node.Elems {
				if e != nil {
					visit(e)
				}
			}
		case *JsTemplateLit:
			// Template literals are structured: descend into each
			// ${…} interpolation expression so dependency collection
			// sees identifiers used inside string interpolation.
			for _, e := range node.Exprs {
				visit(e)
			}
		// Leaves / no-op nodes
		case *JsLiteral, *JsThisExpr, *JsSuperExpr,
			*JsBreakStmt, *JsContinueStmt,
			*JsEmptyStmt, *JsDebuggerStmt:
		default:
			// Fallback: fall back to generic WalkNode to at least visit
			// children.  This may double-visit if we later add a specific
			// case, but the hits list de-dupes via overlap-elimination
			// at splice time.
			WalkNode(node, func(c JsNode) bool {
				if c == node {
					return true // don't re-dispatch self
				}
				visit(c)
				return false // visit handled descent
			})
		}
	}

	visit(root)
}

// rewriteItemRef is the temporary source-string compatibility wrapper around
// visitItemRefs. Production codegen rewrites cached template ASTs directly;
// this wrapper remains a parity oracle for the lexical traversal until the
// string-based tests have been migrated.
//
// If src fails to parse, the original source is returned unchanged rather
// than producing malformed JS.
func rewriteItemRef(src, itemVar, boxVar string) string {
	if itemVar == "" || boxVar == "" || itemVar == boxVar || !strings.Contains(src, itemVar) {
		return src
	}

	prog := ParseJS(src)
	if prog == nil {
		return src
	}

	// Collect source spans, then splice them all at once in source order.
	var hits []struct{ pos, end int }
	visitItemRefs(prog, itemVar, func(node *JsIdent) {
		hits = append(hits, struct{ pos, end int }{node.Pos, node.End})
	})

	if len(hits) == 0 {
		return src
	}

	// Sort hits by pos, then splice.
	sort.Slice(hits, func(i, j int) bool { return hits[i].pos < hits[j].pos })

	var out strings.Builder
	out.Grow(len(src) + 16)
	cursor := 0
	for _, h := range hits {
		if h.pos < cursor {
			// Shouldn't happen with well-formed AST; skip overlapping.
			continue
		}
		out.WriteString(src[cursor:h.pos])
		out.WriteString(boxVar)
		cursor = h.end
	}
	out.WriteString(src[cursor:])
	return out.String()
}

// rewriteItemRefAST redirects unshadowed each-item references to the mutable
// row box without parsing or printing template source again. It returns root
// for fluent use by the AST generator; the tree is deliberately mutated in
// place because a row attribute expression is lowered exactly once.
func rewriteItemRefAST(root JsExpr, itemVar, boxVar string) JsExpr {
	if root == nil || itemVar == "" || boxVar == "" || itemVar == boxVar {
		return root
	}
	visitItemRefs(root, itemVar, func(ref *JsIdent) {
		ref.Name = boxVar
	})
	return root
}

// ──────────────────────────────────────────────
//  Expression builders
// ──────────────────────────────────────────────

// buildExpr produces a JS expression that evaluates to the text content
// of a mixed static/dynamic TextNode.
func buildExpr(parts []*Part) string {
	if len(parts) == 1 {
		if parts[0].Static {
			return backtickString(parts[0].Value)
		}
		return "String(" + parts[0].Value + ")"
	}
	var b strings.Builder
	b.WriteByte('`')
	for _, p := range parts {
		if p.Static {
			b.WriteString(escapeTL(p.Value))
		} else {
			b.WriteString("${")
			b.WriteString(p.Value)
			b.WriteByte('}')
		}
	}
	b.WriteByte('`')
	return b.String()
}

// ──────────────────────────────────────────────
//  Utility
// ──────────────────────────────────────────────

func isSimpleIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 && !(r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return false
		}
		if i > 0 && !(r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// twoWaySpec describes how a bind:X attribute should sync back into state —
// which DOM event to listen for, and which property on event.target to read.
type twoWaySpec struct {
	event    string // e.g. "input", "change"
	readProp string // e.g. "value", "checked"
}

// twoWayBindSpec returns the wiring for  bind:<name>  on element tag <tag>.
// Returns ok=false for unsupported combinations (we only wire listeners for
// shapes with well-defined semantics; unknowns still get the one-way write).
//
// Supported:
//
//	input[type=checkbox|radio]  bind:checked  →  change event, target.checked
//	input[other types]          bind:value    →  input  event, target.value
//	textarea                    bind:value    →  input  event, target.value
//	select                      bind:value    →  change event, target.value
//
// For <input> without an explicit type the default is "text", so bind:value
// is fine.  We don't peek at the type attribute statically because it may
// be dynamic (e.g. :type="..."); at runtime the input event fires for text
// inputs and 'change' fires for checkbox/radio — and bind:checked is the
// affordance for the latter, not bind:value.  So the rule is:
//
//	bind:checked → change event  (regardless of tag; only meaningful on input)
//	bind:value   → input or change event depending on tag
func twoWayBindSpec(tag, attrName string) (twoWaySpec, bool) {
	switch attrName {
	case "value":
		switch tag {
		case "input", "textarea":
			return twoWaySpec{event: "input", readProp: "value"}, true
		case "select":
			return twoWaySpec{event: "change", readProp: "value"}, true
		}
	case "checked":
		if tag == "input" {
			return twoWaySpec{event: "change", readProp: "checked"}, true
		}
	}
	return twoWaySpec{}, false
}

// isBoolAttr reports whether name is a boolean HTML attribute.
// Boolean attributes must be set via the DOM property (element.checked = bool)
// rather than setAttribute, because setAttribute('checked', 'false') still
// marks the element as checked — the mere presence of the attribute is truthy.
func isBoolAttr(name string) bool {
	switch strings.ToLower(name) {
	case "checked", "disabled", "readonly", "required", "selected",
		"multiple", "autofocus", "autoplay", "controls", "default",
		"defer", "formnovalidate", "hidden", "ismap", "loop", "muted",
		"nomodule", "novalidate", "open", "reversed":
		return true
	}
	return false
}

// bindAttrStmt returns the JS statement that correctly sets a dynamic attribute
// on elem (a JS variable name) to the value of expr.
//
//	Boolean attrs  →  elem.checked = !!(expr)
//	class          →  elem.className = String(expr)   (also setAttribute for compat)
//	everything else →  elem.setAttribute('attr', String(expr))
func bindAttrStmt(elem, attr, expr, indent string) string {
	// Attribute values carry their source `{…}` wrapper from the
	// template parser (see readAttrValue).  Strip it here so every
	// caller — there are many — emits the inner expression directly
	// rather than `String({foo})`, which evaluates to "[object
	// Object]" at runtime via the shorthand-object-literal shape.
	expr = unwrapBraces(expr)
	if isBoolAttr(attr) {
		return fmt.Sprintf("%s%s.%s = !!(%s);", indent, elem, attr, expr)
	}
	if attr == "class" {
		return fmt.Sprintf("%s%s.className = String(%s);", indent, elem, expr)
	}
	return fmt.Sprintf("%s%s.setAttribute('%s', String(%s));", indent, elem, attr, expr)
}

// classDirStmt emits a `class:foo={cond}` directive — toggle the
// class `foo` based on the truthiness of expr.  Using
// classList.toggle preserves any other class names the user set via
// a regular `class="..."` attribute, so a mix of static and dynamic
// classes works: `<div class="btn" class:active={isActive}>` keeps
// "btn" always and toggles "active".
func classDirStmt(elem, cls, expr, indent string) string {
	return fmt.Sprintf("%s%s.classList.toggle('%s', !!(%s));",
		indent, elem, cls, attrExprToJS(expr))
}

// styleDirStmt emits a `style:foo={expr}` directive — set an inline
// CSS property.  Uses setProperty rather than `elem.style.foo =`
// because setProperty handles hyphenated names ("font-size",
// "--theme-color") correctly without the user having to camelCase
// them.
func styleDirStmt(elem, prop, expr, indent string) string {
	return fmt.Sprintf("%s%s.style.setProperty('%s', String(%s));",
		indent, elem, prop, attrExprToJS(expr))
}

// attrExprToJS converts an attribute-value string into a JS
// expression that produces its runtime value.  Three input shapes:
//
//   - `{expr}`               → expr                 (pure JS expression)
//   - `prefix{x}suffix`      → `prefix${x}suffix`   (template literal)
//   - `text`                 → 'text'               (plain string literal)
//
// The middle case is what makes `style:font-size="{size}px"` work:
// the user wants the string "16px" at runtime, not the JS expression
// `{size}px` (which is a syntax error).  Without this conversion every
// unit-suffixed style directive would fail.
//
// Brace-matching honors string literals so `{f("}")}` doesn't end
// at the `}` inside the string.
func attrExprToJS(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "''"
	}
	// Pure-expression shape: `{...}` and nothing outside the
	// matching braces.
	if strings.HasPrefix(value, "{") && strings.HasSuffix(value, "}") {
		inner := unwrapBraces(value)
		if inner != value {
			return inner
		}
	}
	// No interpolation at all → plain quoted string.
	if !strings.Contains(value, "{") {
		// Escape single quotes and backslashes.
		s := strings.ReplaceAll(value, "\\", "\\\\")
		s = strings.ReplaceAll(s, "'", "\\'")
		return "'" + s + "'"
	}
	// Mixed: build a template literal.
	var b strings.Builder
	b.WriteByte('`')
	i := 0
	for i < len(value) {
		if value[i] == '{' {
			// Find the matching `}` honoring nested braces & strings.
			depth := 1
			j := i + 1
			for j < len(value) && depth > 0 {
				switch value[j] {
				case '"', '\'':
					q := value[j]
					j++
					for j < len(value) && value[j] != q {
						if value[j] == '\\' && j+1 < len(value) {
							j += 2
							continue
						}
						j++
					}
				case '{':
					depth++
				case '}':
					depth--
				}
				if depth > 0 {
					j++
				}
			}
			b.WriteString("${")
			b.WriteString(value[i+1 : j])
			b.WriteByte('}')
			i = j + 1
			continue
		}
		// Literal char — escape template-literal specials.
		c := value[i]
		switch c {
		case '`':
			b.WriteString("\\`")
		case '\\':
			b.WriteString("\\\\")
		case '$':
			if i+1 < len(value) && value[i+1] == '{' {
				b.WriteString("\\$")
			} else {
				b.WriteByte('$')
			}
		default:
			b.WriteByte(c)
		}
		i++
	}
	b.WriteByte('`')
	return b.String()
}

// unwrapBraces strips a single layer of outer `{...}` from an attribute
// value when the value is being interpolated as a JavaScript expression.
//
// Why this exists:  the template parser preserves the source `{...}`
// wrapper in Attr.Value (so `:disabled={true}` arrives as the literal
// string `"{true}"`).  Codegen sites that interpolate Attr.Value into
// a JS expression context — props objects, event handler bodies, set()
// calls — must remove that wrapper, or the output becomes either a
// syntax error ("disabled: {true}" → {true} is a block) or wrong
// semantics ("value: {username}" → shorthand object literal instead of
// the variable's value).
//
// Only the outermost matched pair is stripped.  Nested braces inside
// (e.g. `{ {x: 1} }`) are preserved because the inner braces are part
// of the user expression.  If the value doesn't start with `{` it's
// returned verbatim — this covers both quoted attributes and bare
// identifiers.
func unwrapBraces(s string) string {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '{' || s[len(s)-1] != '}' {
		return s
	}
	// Confirm the outer braces are a matched pair (not e.g. "{a} + {b}").
	// Walk the string tracking brace depth, skipping over string and
	// template literals.  If depth hits zero before the final char, the
	// outer braces aren't a pair and we leave the value alone.
	depth := 0
	i := 0
	for i < len(s) {
		c := s[i]
		switch c {
		case '"', '\'':
			// Skip the string literal.
			q := c
			i++
			for i < len(s) && s[i] != q {
				if s[i] == '\\' && i+1 < len(s) {
					i += 2
					continue
				}
				i++
			}
			i++ // closing quote
		case '`':
			// Skip the template literal.  We don't descend into ${} for
			// brace matching — any { inside a template literal is content,
			// not structure.
			i++
			for i < len(s) && s[i] != '`' {
				if s[i] == '\\' && i+1 < len(s) {
					i += 2
					continue
				}
				i++
			}
			i++
		case '{':
			depth++
			i++
		case '}':
			depth--
			if depth == 0 && i < len(s)-1 {
				// Closed the outer braces before the end → not a pair.
				return s
			}
			i++
		default:
			i++
		}
	}
	return strings.TrimSpace(s[1 : len(s)-1])
}

// isHandlerExpr returns true when s is already a function/arrow expression
// that can be used as an event listener directly (after storing in a const).
// Examples: "() => foo()", "($e) => bar($e)", "function() { ... }", "async () => ..."
func isHandlerExpr(s string) bool {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "function") {
		return true
	}
	if strings.HasPrefix(s, "async ") {
		return true
	}
	// Arrow function: starts with '(' or a single identifier followed by '=>'
	if strings.HasPrefix(s, "(") {
		return strings.Contains(s, "=>")
	}
	// Single-param arrow: ident => ...
	if idx := strings.Index(s, "=>"); idx > 0 {
		param := strings.TrimSpace(s[:idx])
		return isSimpleIdent(param)
	}
	return false
}

func containsStr(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// isStructuralUpdate reports whether an update statement (as passed to
// addUpdate) is a call to a mount/unmount function — $$chain_N(),
// $$each_N(arr), $$ceach_N(arr), $$bceach_N(arr).
// These must fire BEFORE attribute updates within the same setter, so a
// conditional branch that reads e.g. `editTarget.name` gets unmounted
// before any deferred attribute write tries to dereference null.
//
// The check is a loose name prefix test — it intentionally also matches
// when the call is wrapped in an `if (branchVar)` guard emitted by
// walkIfChain / emitBranchChildren, because the guard is preserved as a
// prefix of the statement text.
func isStructuralUpdate(u string) bool {
	t := strings.TrimSpace(u)
	// Strip an optional `if (...) ` guard so we can inspect the real call.
	if strings.HasPrefix(t, "if (") {
		if i := strings.Index(t, ") "); i >= 0 {
			t = strings.TrimSpace(t[i+2:])
		}
	}
	for _, prefix := range []string{
		"$$chain_",
		"$$each_",
		"$$ceach_",
		"$$bceach_",
	} {
		if strings.HasPrefix(t, prefix) {
			return true
		}
	}
	return false
}

// backtickString wraps s in a JS template literal, escaping as needed.
func backtickString(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "`", "\\`")
	s = strings.ReplaceAll(s, "${", "\\${")
	return "`" + s + "`"
}

// escapeTL escapes characters that would be interpreted inside a template literal.
func escapeTL(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "`", "\\`")
	s = strings.ReplaceAll(s, "${", "\\${")
	return s
}

func escapeAttr(s string) string {
	s = strings.ReplaceAll(s, "'", "\\'")
	return s
}

// ─────────────────────────────────────────────────────────────────────────────
//  Static-subtree → innerHTML compression
//
//  Motivation: runtime DOM operations are expensive.  A chain of
//  createElement + setAttribute + appendChild + createTextNode + appendChild
//  for each element makes the browser bounce through JS→native transitions
//  thousands of times during mount.  A single `elem.innerHTML = '<html>'`
//  hands the whole string to the HTML parser, which runs in native code and
//  builds the DOM tree in one pass — dramatically fewer JS↔native crossings.
//
//  For a static-heavy page (dashboard shells, form scaffolds, presentation
//  components), this is by far the single largest win available at mount
//  time.  Emitted-code size may go UP (the HTML string is verbose), but
//  that's gzip's problem; mount speed is the user-facing metric.
//
//  isFullyStatic(node) identifies subtrees whose DOM never changes after
//  creation: no events, bindings, interpolations, directives, components,
//  or slots, recursively.  emitStaticHTML serializes such a subtree as an
//  HTML string with the scope attribute baked into every tag so scoped
//  CSS still matches.
// ─────────────────────────────────────────────────────────────────────────────

// voidTags is the set of HTML elements that have no closing tag.
var voidTags = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "link": true, "meta": true,
	"param": true, "source": true, "track": true, "wbr": true,
}

// isFullyStatic reports whether a node and its entire subtree produce
// deterministic, unchanging DOM — no reactive bindings, no events, no
// interpolation, no structural directives, no component instantiations,
// no slots.
func isFullyStatic(node Node) bool {
	switch n := node.(type) {
	case *TextNode:
		for _, p := range n.Parts {
			if !p.Static {
				return false
			}
		}
		return true

	case *ElementNode:
		for _, attr := range n.Attrs {
			if attr.IsEvent || attr.IsBind || attr.IsTwoWay || attr.IsBindThis ||
				attr.IsClassDir || attr.IsStyleDir {
				return false
			}
		}
		for _, child := range n.Children {
			if !isFullyStatic(child) {
				return false
			}
		}
		return true

	default:
		return false
	}
}

// allStatic returns true iff every node in the list is fully-static.
func allStatic(nodes []Node) bool {
	for _, n := range nodes {
		if !isFullyStatic(n) {
			return false
		}
	}
	return true
}

// hasElement returns true iff any node in the list is an ElementNode.
// Used to decide whether an innerHTML compression is worth it — a list of
// only TextNodes can still be compressed, but its savings are marginal.
// Keeping this threshold permissive: even a single-element static subtree
// pays back the HTML-parse cost when measured by operation count.
func hasElement(nodes []Node) bool {
	for _, n := range nodes {
		if _, ok := n.(*ElementNode); ok {
			return true
		}
	}
	return false
}

// emitStaticHTML serializes a fully-static subtree as an HTML string.
// Scope attribute is baked into every opening tag.  Text is HTML-escaped.
// Void elements emitted without closing tags.  Attribute values use
// double-quotes so the string can sit inside a single-quoted JS literal.
//
// Precondition: every node passed must satisfy isFullyStatic.
func emitStaticHTML(nodes []Node, scopeID string) string {
	var b strings.Builder
	for _, node := range nodes {
		emitStaticHTMLNode(&b, node, scopeID)
	}
	return b.String()
}

func emitStaticHTMLNode(b *strings.Builder, node Node, scopeID string) {
	switch n := node.(type) {
	case *TextNode:
		for _, p := range n.Parts {
			b.WriteString(escapeHTMLText(p.Value))
		}
	case *ElementNode:
		b.WriteByte('<')
		b.WriteString(n.Tag)
		if scopeID != "" {
			b.WriteByte(' ')
			b.WriteString(scopeID)
			b.WriteString(`=""`)
		}
		for _, attr := range n.Attrs {
			b.WriteByte(' ')
			b.WriteString(attr.Name)
			b.WriteString(`="`)
			b.WriteString(escapeHTMLAttr(attr.Value))
			b.WriteByte('"')
		}
		if voidTags[strings.ToLower(n.Tag)] {
			b.WriteString("/>")
			return
		}
		b.WriteByte('>')
		for _, child := range n.Children {
			emitStaticHTMLNode(b, child, scopeID)
		}
		b.WriteString("</")
		b.WriteString(n.Tag)
		b.WriteByte('>')
	}
}

// escapeHTMLText escapes <, >, & in text content.
func escapeHTMLText(s string) string {
	if !strings.ContainsAny(s, "<>&") {
		return s
	}
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// escapeHTMLAttr escapes & and " in attribute values.
func escapeHTMLAttr(s string) string {
	if !strings.ContainsAny(s, `&"`) {
		return s
	}
	r := strings.NewReplacer("&", "&amp;", `"`, "&quot;")
	return r.Replace(s)
}

// escapeJSString escapes a string for a single-quoted JS string literal.
func escapeJSString(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\'':
			b.WriteString(`\'`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
