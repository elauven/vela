package compiler

import (
	"github.com/elauven/vela/internal/analyzer"
	"github.com/elauven/vela/internal/diag"
	"github.com/elauven/vela/internal/jsast"
	"github.com/elauven/vela/internal/sfcast"

	"sort"
	"strings"
)

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
func EliminateDeadCode(script string, analysis *analyzer.Analysis, templateText string) (string, bool) {
	if script == "" {
		return script, false
	}
	prog := jsast.ParseJS(script)
	if prog == nil {
		return script, false
	}
	// Use a throwaway DiagSet — we want the scope graph, not the
	// diagnostics, which were already emitted during validation.
	silent := diag.NewDiagSet("", script)
	root := analyzer.BuildScopes(prog, silent)
	if root == nil {
		return script, false
	}

	posIdx := indexBindingsByPos(root)
	reactive := reactiveNameSet(analysis)
	exported := exportedNamesIn(prog)

	var spans []dceSpan
	jsast.WalkNode(prog, func(n jsast.JsNode) bool {
		switch decl := n.(type) {
		case *jsast.JsExportDecl:
			// Don't recurse into exports — never touch anything inside.
			return false

		case *jsast.JsVarDecl:
			if span, ok := varDeclEliminable(decl, posIdx, reactive, exported, templateText); ok {
				spans = append(spans, span)
			}
			return true

		case *jsast.JsFuncDecl:
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
func EliminateDeadCodeFixedPoint(script string, analysis *analyzer.Analysis, templateText string) (string, *analyzer.Analysis) {
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
		curA = analyzer.AnalyzeScript(cur)
	}
	return cur, curA
}

// EliminateDeadCodeAST is the compiler-path counterpart of the legacy
// string API above. It removes eligible statements directly from the owned
// ScriptAST and re-runs analysis over that same tree, never reparsing script
// source. The public string API remains for compatibility tests/tools.
func EliminateDeadCodeAST(script *sfcast.ScriptAST, analysis *analyzer.Analysis, templateText string) *analyzer.Analysis {
	if script == nil || script.Program == nil {
		return analysis
	}
	current := analysis
	for i := 0; i < 8; i++ {
		drops := deadStatementPositions(script.Program, current, templateText)
		if len(drops) == 0 {
			break
		}
		filterDeadProgram(script.Program, drops)
		current = analyzer.AnalyzeScriptAST(script)
	}
	return current
}

func deadStatementPositions(prog *jsast.JsProgram, analysis *analyzer.Analysis, templateText string) map[int]bool {
	silent := diag.NewDiagSet("", "")
	root := analyzer.BuildScopes(prog, silent)
	if root == nil {
		return nil
	}
	idx := indexBindingsByPos(root)
	reactive, exported := reactiveNameSet(analysis), exportedNamesIn(prog)
	drops := map[int]bool{}
	jsast.WalkNode(prog, func(n jsast.JsNode) bool {
		switch stmt := n.(type) {
		case *jsast.JsExportDecl:
			return false
		case *jsast.JsVarDecl:
			if span, ok := varDeclEliminable(stmt, idx, reactive, exported, templateText); ok {
				drops[span.start] = true
			}
		case *jsast.JsFuncDecl:
			if span, ok := funcDeclEliminable(stmt, idx, reactive, exported, templateText); ok {
				drops[span.start] = true
				return false
			}
		}
		return true
	})
	return drops
}

func filterDeadProgram(prog *jsast.JsProgram, drops map[int]bool) {
	prog.Body = filterDeadStmts(prog.Body, drops)
}

func filterDeadStmts(stmts []jsast.JsStmt, drops map[int]bool) []jsast.JsStmt {
	out := make([]jsast.JsStmt, 0, len(stmts))
	for _, stmt := range stmts {
		if stmt == nil || drops[stmt.NodePos()] {
			continue
		}
		switch s := stmt.(type) {
		case *jsast.JsBlock:
			s.Body = filterDeadStmts(s.Body, drops)
		case *jsast.JsFuncDecl:
			s.Body.Body = filterDeadStmts(s.Body.Body, drops)
		case *jsast.JsIfStmt:
			s.Then = filterDeadNestedStmt(s.Then, drops)
			if s.Else != nil {
				s.Else = filterDeadNestedStmt(s.Else, drops)
			}
		case *jsast.JsForStmt:
			s.Body = filterDeadNestedStmt(s.Body, drops)
		case *jsast.JsForInStmt:
			s.Body = filterDeadNestedStmt(s.Body, drops)
		case *jsast.JsWhileStmt:
			s.Body = filterDeadNestedStmt(s.Body, drops)
		case *jsast.JsDoWhileStmt:
			s.Body = filterDeadNestedStmt(s.Body, drops)
		case *jsast.JsTryStmt:
			if s.Block != nil {
				s.Block.Body = filterDeadStmts(s.Block.Body, drops)
			}
			if s.Handler != nil {
				s.Handler.Body = filterDeadStmts(s.Handler.Body, drops)
			}
			if s.Finalizer != nil {
				s.Finalizer.Body = filterDeadStmts(s.Finalizer.Body, drops)
			}
		case *jsast.JsSwitchStmt:
			for _, c := range s.Cases {
				c.Body = filterDeadStmts(c.Body, drops)
			}
		}
		out = append(out, stmt)
	}
	return out
}

func filterDeadNestedStmt(stmt jsast.JsStmt, drops map[int]bool) jsast.JsStmt {
	filtered := filterDeadStmts([]jsast.JsStmt{stmt}, drops)
	if len(filtered) > 0 {
		return filtered[0]
	}
	return &jsast.JsEmptyStmt{BaseStmt: jsast.BaseStmt{BaseNode: jsast.GenNode()}}
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

func varDeclEliminable(d *jsast.JsVarDecl, idx map[int]*analyzer.Binding, reactive, exported map[string]bool, templateText string) (dceSpan, bool) {
	// Only single-declarator let/const.  var is excluded because
	// hoisting means a `var` inside a conditional can be referenced
	// from anywhere in the enclosing function, and the scope graph's
	// reference set already accounts for that — but the cost of being
	// wrong (silently dropping a referenced var) is high.  Skip.
	if d.Kind == "var" || len(d.Decls) != 1 {
		return dceSpan{}, false
	}
	dec := d.Decls[0]
	id, ok := dec.Id.(*jsast.JsIdent)
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
	if dec.Init != nil && !IsPureExpr(dec.Init) {
		return dceSpan{}, false
	}
	return dceSpan{start: d.NodePos(), end: d.NodeEnd()}, true
}

func funcDeclEliminable(d *jsast.JsFuncDecl, idx map[int]*analyzer.Binding, reactive, exported map[string]bool, templateText string) (dceSpan, bool) {
	// Function bindings have Pos == start of the declaration, not the
	// name's position — see scope.go's hoisting pass.
	bind := idx[d.NodePos()]
	if bind == nil {
		return dceSpan{}, false
	}
	if !shouldEliminate(bind, reactive, exported, templateText) {
		return dceSpan{}, false
	}
	// Function bodies have no observable effect at declaration time —
	// only when called.  Safe to remove unconditionally.
	return dceSpan{start: d.NodePos(), end: d.NodeEnd()}, true
}

func shouldEliminate(b *analyzer.Binding, reactive, exported map[string]bool, templateText string) bool {
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
	if b.Scope != nil && b.Scope.Kind == analyzer.ScopeProgram && referencedInTemplate(b.Name, templateText) {
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
func indexBindingsByPos(s *analyzer.Scope) map[int]*analyzer.Binding {
	out := map[int]*analyzer.Binding{}
	var visit func(*analyzer.Scope)
	visit = func(s *analyzer.Scope) {
		for _, b := range s.Bindings {
			if b.Kind == analyzer.BindBuiltin {
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
func reactiveNameSet(a *analyzer.Analysis) map[string]bool {
	m := map[string]bool{}
	if a == nil {
		return m
	}
	for _, name := range a.ReactiveNames() {
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
func exportedNamesIn(prog *jsast.JsProgram) map[string]bool {
	out := map[string]bool{}
	jsast.WalkNode(prog, func(n jsast.JsNode) bool {
		ed, ok := n.(*jsast.JsExportDecl)
		if !ok {
			return true
		}
		jsast.WalkNode(ed, func(inner jsast.JsNode) bool {
			if id, ok := inner.(*jsast.JsIdent); ok {
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

// IsPureExpr reports whether evaluating e could be safely dropped.
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
func IsPureExpr(e jsast.JsExpr) bool {
	switch n := e.(type) {
	case nil:
		return true
	case *jsast.JsLiteral:
		return true
	case *jsast.JsTemplateLit:
		// Pure only if every interpolation is pure — a template can
		// embed arbitrary expressions, including side-effecting calls.
		for _, e := range n.Exprs {
			if !IsPureExpr(e) {
				return false
			}
		}
		return true
	case *jsast.JsIdent:
		return true
	case *jsast.JsThisExpr, *jsast.JsSuperExpr:
		return true
	case *jsast.JsArrayExpr:
		for _, el := range n.Elems {
			if el != nil && !IsPureExpr(el) {
				return false
			}
		}
		return true
	case *jsast.JsObjectExpr:
		for _, p := range n.Props {
			if p.Spread {
				// ...x triggers iteration / Symbol.iterator
				return false
			}
			if p.Computed && !IsPureExpr(p.Key) {
				return false
			}
			if p.Value != nil && !IsPureExpr(p.Value) {
				return false
			}
		}
		return true
	case *jsast.JsFuncExpr, *jsast.JsArrowFunc:
		return true
	case *jsast.JsUnaryExpr:
		if n.Op == "delete" || n.Op == "void" {
			return false
		}
		return IsPureExpr(n.X)
	case *jsast.JsBinaryExpr:
		return IsPureExpr(n.L) && IsPureExpr(n.R)
	case *jsast.JsLogicalExpr:
		return IsPureExpr(n.L) && IsPureExpr(n.R)
	case *jsast.JsConditionalExpr:
		return IsPureExpr(n.Test) && IsPureExpr(n.Then) && IsPureExpr(n.Else)
	case *jsast.JsSeqExpr:
		for _, x := range n.Exprs {
			if !IsPureExpr(x) {
				return false
			}
		}
		return true
	case *jsast.JsMemberExpr, *jsast.JsCallExpr, *jsast.JsNewExpr,
		*jsast.JsAssignExpr, *jsast.JsSpreadExpr, *jsast.JsAwaitExpr, *jsast.JsYieldExpr:
		return false
	}
	return false
}
