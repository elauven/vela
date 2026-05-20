package main

// dce.go — dead-binding elimination for the Vela compiler.
//
// What this removes:
//   • top-level and nested `let` / `const` declarations whose binding
//     has zero references and whose initializer is side-effect-free
//   • `function` declarations that are never called or otherwise
//     referenced
//
// What it deliberately preserves:
//   • $state / $derived / $props / $bindable bindings — referenced by
//     codegen through the template, invisible to the JS scope graph
//   • `import` declarations — handled separately if at all
//   • exported declarations and every name appearing inside `export …`
//   • bindings whose name starts with `_` (intentionally-unused convention)
//   • declarations with side-effectful initializers (calls, await, etc.)
//   • top-level bindings whose name appears as an identifier token in
//     the raw template text
//   • multi-declarator forms like `let a = 1, b = 2;` (conservative —
//     surgically removing one declarator means more delicate splicing
//     than is worth it for the first pass)
//   • `var` declarations (hoisting weirdness; the value is small and
//     the risk of mis-eliminating something that escapes a block is
//     not worth it for the gain)
//
// Why textual splicing and not AST rewrite → reprint:
//   The codebase doesn't have a JS pretty-printer.  Splicing an
//   AST-bounded range out of the original source preserves the user's
//   formatting, comments, and any code we didn't touch, at the cost of
//   one extra parse round per pass.  Existing examples in this
//   compiler (RewriteScriptImports) use the same pattern.

import (
	"sort"
	"strings"
)

// ─────────────────────────────────────────────
//  Public API
// ─────────────────────────────────────────────

// EliminateDeadCode runs one round of dead-binding elimination.
// Returns the rewritten script and whether anything changed.
//
// To run to fixed point, use EliminateDeadCodeFixedPoint — a single
// round can remove a function whose only references were from another
// dead binding, which only the next round can detect.
//
// `analysis` provides the Vela-reactive name set (state vars, derived,
// props) so those bindings are never eliminated.  `templateText` is
// the raw <template> block — scanned for identifier-token references
// so a binding the template uses (event handlers, helper functions,
// constants in expressions) isn't dropped.
func EliminateDeadCode(script string, analysis *Analysis, templateText string) (string, bool) {
	if script == "" {
		return script, false
	}
	prog := ParseJS(script)
	if prog == nil {
		return script, false
	}
	// Use a throwaway DiagSet — we want the scope graph, not the
	// diagnostics, which were already emitted during validation.
	silent := NewDiagSet("", script)
	root := BuildScopes(prog, silent)
	if root == nil {
		return script, false
	}

	posIdx := indexBindingsByPos(root)
	reactive := reactiveNameSet(analysis)
	exported := exportedNamesIn(prog)

	var spans []dceSpan
	WalkNode(prog, func(n JsNode) bool {
		switch decl := n.(type) {
		case *JsExportDecl:
			// Don't recurse into exports — never touch anything inside.
			return false

		case *JsVarDecl:
			if span, ok := varDeclEliminable(decl, posIdx, reactive, exported, templateText); ok {
				spans = append(spans, span)
			}
			return true

		case *JsFuncDecl:
			if span, ok := funcDeclEliminable(decl, posIdx, reactive, exported, templateText); ok {
				spans = append(spans, span)
				return false // body is being removed; no point descending
			}
			return true
		}
		return true
	})

	if len(spans) == 0 {
		return script, false
	}
	return applyDCESpans(script, spans), true
}

// EliminateDeadCodeFixedPoint repeatedly calls EliminateDeadCode until
// no further changes, with a safety cap of 8 rounds (bounds the cost
// and acts as a defence against any bug that might cause oscillation).
//
// The returned Analysis is freshly computed against the final script,
// so the caller doesn't have to re-run analyzeScript afterward.
func EliminateDeadCodeFixedPoint(script string, analysis *Analysis, templateText string) (string, *Analysis) {
	cur := script
	curA := analysis
	for i := 0; i < 8; i++ {
		next, changed := EliminateDeadCode(cur, curA, templateText)
		if !changed {
			break
		}
		cur = next
		// Re-analyze so the next iteration sees the updated reactive
		// set.  Removing a binding can change which names look reactive
		// (in pathological cases — usually it's a no-op).
		curA = analyzeScript(cur)
	}
	return cur, curA
}

// ─────────────────────────────────────────────
//  Splicing
// ─────────────────────────────────────────────

type dceSpan struct{ start, end int }

// applyDCESpans removes every byte range in spans from s.  Spans are
// sorted by start ascending and applied left-to-right; overlapping
// spans (which shouldn't happen given that top-level decls don't
// contain each other) are dropped defensively.
func applyDCESpans(s string, spans []dceSpan) string {
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	var b strings.Builder
	b.Grow(len(s))
	prev := 0
	for _, sp := range spans {
		if sp.start < prev {
			continue // overlap — defensive
		}
		b.WriteString(s[prev:sp.start])
		prev = sp.end
	}
	b.WriteString(s[prev:])
	return b.String()
}

// ─────────────────────────────────────────────
//  Eligibility predicates
// ─────────────────────────────────────────────

func varDeclEliminable(d *JsVarDecl, idx map[int]*Binding, reactive, exported map[string]bool, templateText string) (dceSpan, bool) {
	// Only single-declarator let/const.  var is excluded because
	// hoisting means a `var` inside a conditional can be referenced
	// from anywhere in the enclosing function, and the scope graph's
	// reference set already accounts for that — but the cost of being
	// wrong (silently dropping a referenced var) is high.  Skip.
	if d.Kind == "var" || len(d.Decls) != 1 {
		return dceSpan{}, false
	}
	dec := d.Decls[0]
	id, ok := dec.Id.(*JsIdent)
	if !ok {
		return dceSpan{}, false // destructuring — skip
	}
	bind := idx[id.Pos]
	if bind == nil {
		return dceSpan{}, false
	}
	if !shouldEliminate(bind, reactive, exported, templateText) {
		return dceSpan{}, false
	}
	if dec.Init != nil && !isPureExpr(dec.Init) {
		return dceSpan{}, false
	}
	return dceSpan{start: d.nodePos(), end: d.nodeEnd()}, true
}

func funcDeclEliminable(d *JsFuncDecl, idx map[int]*Binding, reactive, exported map[string]bool, templateText string) (dceSpan, bool) {
	// Function bindings have Pos == start of the declaration, not the
	// name's position — see scope.go's hoisting pass.
	bind := idx[d.nodePos()]
	if bind == nil {
		return dceSpan{}, false
	}
	if !shouldEliminate(bind, reactive, exported, templateText) {
		return dceSpan{}, false
	}
	// Function bodies have no observable effect at declaration time —
	// only when called.  Safe to remove unconditionally.
	return dceSpan{start: d.nodePos(), end: d.nodeEnd()}, true
}

func shouldEliminate(b *Binding, reactive, exported map[string]bool, templateText string) bool {
	if len(b.Refs) > 0 {
		return false
	}
	if reactive[b.Name] {
		return false
	}
	if exported[b.Name] {
		return false
	}
	// Conventional "intentionally unused" prefix.
	if strings.HasPrefix(b.Name, "_") {
		return false
	}
	// Top-level bindings may be reached from the template.  Nested
	// bindings (anything inside a block, function, etc.) cannot —
	// they're invisible outside their scope.
	if b.Scope != nil && b.Scope.Kind == ScopeProgram && referencedInTemplate(b.Name, templateText) {
		return false
	}
	return true
}

// ─────────────────────────────────────────────
//  Lookup helpers
// ─────────────────────────────────────────────

// indexBindingsByPos walks the scope tree and maps every user
// binding's Pos → *Binding.  Builtins are excluded — their Pos is 0
// and they'd collide with each other in the map.
func indexBindingsByPos(s *Scope) map[int]*Binding {
	out := map[int]*Binding{}
	var visit func(*Scope)
	visit = func(s *Scope) {
		for _, b := range s.Bindings {
			if b.Kind == BindBuiltin {
				continue
			}
			out[b.Pos] = b
		}
		for _, c := range s.Children {
			visit(c)
		}
	}
	visit(s)
	return out
}

// reactiveNameSet returns a set view of analysis.reactiveNames() so we
// can do O(1) lookups while eliminating.  The underlying slice method
// lives on *Analysis and is shared with codegen.
func reactiveNameSet(a *Analysis) map[string]bool {
	m := map[string]bool{}
	if a == nil {
		return m
	}
	for _, name := range a.reactiveNames() {
		m[name] = true
	}
	return m
}

// exportedNamesIn collects every identifier appearing anywhere inside
// an `export …` statement.  This is intentionally coarse — including
// names that appear in initializer expressions, not just the exported
// names themselves.  Over-collection only causes us to *keep* a few
// extra bindings, which is safe; under-collection would risk dropping
// something the module's consumers depend on.
func exportedNamesIn(prog *JsProgram) map[string]bool {
	out := map[string]bool{}
	WalkNode(prog, func(n JsNode) bool {
		ed, ok := n.(*JsExportDecl)
		if !ok {
			return true
		}
		WalkNode(ed, func(inner JsNode) bool {
			if id, ok := inner.(*JsIdent); ok {
				out[id.Name] = true
			}
			return true
		})
		return false // already handled this subtree
	})
	return out
}

// referencedInTemplate scans the raw template text for `name` as a
// standalone identifier token (i.e. not part of a longer word).
//
// Conservative: a name appearing inside literal text content matches
// too.  False positives keep extra bindings — safe — but never the
// other way around.
func referencedInTemplate(name, templateText string) bool {
	if name == "" {
		return false
	}
	for i := 0; i+len(name) <= len(templateText); i++ {
		if templateText[i:i+len(name)] != name {
			continue
		}
		before := i == 0 || !isIdentByte(templateText[i-1])
		after := i+len(name) == len(templateText) || !isIdentByte(templateText[i+len(name)])
		if before && after {
			return true
		}
	}
	return false
}

// ─────────────────────────────────────────────
//  Purity check
// ─────────────────────────────────────────────

// isPureExpr reports whether evaluating e could be safely dropped.
// "Pure" is conservative: we err toward false whenever there's any
// chance of an observable side effect (a getter triggering, a function
// call, an iterator running for a spread, etc.).
//
// Pure: literals, identifier reads, this/super, plain arrays and
// objects whose elements are themselves pure, function/arrow value
// expressions (declaring a function has no effect — only calling does),
// sequence expressions of pure parts, conditionals and binary/logical
// ops over pure operands, unary ops other than delete and void.
//
// Impure: any call (function or constructor), member access (getters),
// assignment, spread, await, yield, delete, void.
func isPureExpr(e JsExpr) bool {
	switch n := e.(type) {
	case nil:
		return true
	case *JsLiteral, *JsTemplateLit:
		return true
	case *JsIdent:
		return true
	case *JsThisExpr, *JsSuperExpr:
		return true
	case *JsArrayExpr:
		for _, el := range n.Elems {
			if el != nil && !isPureExpr(el) {
				return false
			}
		}
		return true
	case *JsObjectExpr:
		for _, p := range n.Props {
			if p.Spread {
				// ...x triggers iteration / Symbol.iterator
				return false
			}
			if p.Computed && !isPureExpr(p.Key) {
				return false
			}
			if p.Value != nil && !isPureExpr(p.Value) {
				return false
			}
		}
		return true
	case *JsFuncExpr, *JsArrowFunc:
		return true
	case *JsUnaryExpr:
		if n.Op == "delete" || n.Op == "void" {
			return false
		}
		return isPureExpr(n.X)
	case *JsBinaryExpr:
		return isPureExpr(n.L) && isPureExpr(n.R)
	case *JsLogicalExpr:
		return isPureExpr(n.L) && isPureExpr(n.R)
	case *JsConditionalExpr:
		return isPureExpr(n.Test) && isPureExpr(n.Then) && isPureExpr(n.Else)
	case *JsSeqExpr:
		for _, x := range n.Exprs {
			if !isPureExpr(x) {
				return false
			}
		}
		return true
	case *JsMemberExpr, *JsCallExpr, *JsNewExpr,
		*JsAssignExpr, *JsSpreadExpr, *JsAwaitExpr, *JsYieldExpr:
		return false
	}
	return false
}
