package analysis

import (
	"github.com/elauven/vela/internal/jsast"
	"github.com/elauven/vela/internal/sfc"

	"fmt"
	"sort"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
//  Identifier collection
// ─────────────────────────────────────────────────────────────────────────────

// scanTemplateHoles extracts every ${...} expression body from a raw template
// literal string (e.g. `hello ${name}!`), correctly handling:
//   - nested braces:   `${obj({ a: 1 })}`
//   - nested strings:  `${"}" + x}`
//   - escaped chars:   `\${not a hole}`
//
// It returns the expression text (without the surrounding ${ and }) for each hole.
func scanTemplateHoles(raw string) []string {
	// raw includes the surrounding backticks; skip first backtick.
	var holes []string
	i := 1 // skip opening `
	n := len(raw)
	for i < n {
		c := raw[i]
		if c == '\\' {
			i += 2 // skip escaped character — not a hole
			continue
		}
		if c == '`' {
			break // closing backtick
		}
		if c == '$' && i+1 < n && raw[i+1] == '{' {
			i += 2 // skip ${
			start := i
			depth := 1
			for i < n && depth > 0 {
				ch := raw[i]
				switch ch {
				case '\\':
					i++ // skip next char
				case '{':
					depth++
				case '}':
					depth--
					if depth == 0 {
						holes = append(holes, raw[start:i])
						goto nextOuter
					}
				case '"', '\'', '`':
					// Skip over a nested string/template so its braces don't
					// confuse the depth counter.
					i = skipQuoted(raw, i)
					continue
				}
				i++
			}
		nextOuter:
			continue
		}
		i++
	}
	return holes
}

// skipQuoted advances past a quoted string or template literal starting at pos,
// returning the index of the character *after* the closing quote.
func skipQuoted(s string, pos int) int {
	q := s[pos]
	pos++
	for pos < len(s) {
		c := s[pos]
		if c == '\\' {
			pos += 2
			continue
		}
		if c == q {
			return pos + 1
		}
		pos++
	}
	return pos
}

// ─────────────────────────────────────────────────────────────────────────────
//  Assignment rewriter — source-level, position-based
// ─────────────────────────────────────────────────────────────────────────────

type repl struct {
	start, end int
	text       string
}

// RewriteAssignments finds every assignment or update expression that targets
// a state variable and replaces it with the appropriate $$set_X(...) call.
// It operates on src (which may be a CleanScript snippet) and its parsed prog.
func RewriteAssignments(src string, prog *jsast.JsProgram, stateSet map[string]bool) string {
	var repls []repl
	collectRepls(prog, src, stateSet, &repls)
	return applyRepls(src, repls)
}

// RewriteAssignmentsAST applies the reactive-assignment transform directly to
// a callback block. Unlike RewriteAssignments it never reparses or splices
// JavaScript source: user expressions retain their parsed nodes and source
// origins, while only the compiler-owned $$set_* wrapper is constructed.
//
// The transform intentionally mirrors the statement forms supported by the
// legacy source rewriter. It descends into nested blocks and closures so an
// assignment in a timer, promise callback, or event listener is reactive too.
func RewriteAssignmentsAST(block *jsast.JsBlock, stateSet map[string]bool) {
	if block == nil || len(stateSet) == 0 {
		return
	}
	rewriteAssignmentBlock(block, stateSet)
}

// RewriteStatementAssignmentsAST lowers state writes in one already-parsed
// user statement. ScriptPlan uses it after input validation so ordinary
// functions and top-level statements never cross a source-text ParseJS
// boundary during target generation.
func RewriteStatementAssignmentsAST(stmt jsast.JsStmt, stateSet map[string]bool) jsast.JsStmt {
	return rewriteAssignmentStmt(stmt, stateSet)
}

// RewriteHandlerAssignmentsAST applies the same state-write lowering to a
// cached template event handler. Block-bodied arrows reuse the callback-block
// transform; concise bodies are replaced with the generated setter call.
func RewriteHandlerAssignmentsAST(expr jsast.JsExpr, stateSet map[string]bool) jsast.JsExpr {
	arrow, ok := expr.(*jsast.JsArrowFunc)
	if !ok {
		return expr
	}
	if block, ok := arrow.Body.(*jsast.JsBlock); ok {
		RewriteAssignmentsAST(block, stateSet)
		return arrow
	}
	body, ok := arrow.Body.(jsast.JsExpr)
	if !ok {
		return arrow
	}
	rewriteAssignmentExpr(body, stateSet)
	if setter := reactiveSetterExpr(body, stateSet); setter != nil {
		arrow.Body = setter
	}
	return arrow
}

func rewriteAssignmentBlock(block *jsast.JsBlock, stateSet map[string]bool) {
	if block == nil {
		return
	}
	for i, stmt := range block.Body {
		block.Body[i] = rewriteAssignmentStmt(stmt, stateSet)
	}
}

func rewriteAssignmentStmt(stmt jsast.JsStmt, stateSet map[string]bool) jsast.JsStmt {
	if stmt == nil {
		return nil
	}
	switch s := stmt.(type) {
	case *jsast.JsExprStmt:
		rewriteAssignmentExpr(s.X, stateSet)
		if setter := reactiveSetterStmt(s.X, stateSet); setter != nil {
			return setter
		}
	case *jsast.JsBlock:
		rewriteAssignmentBlock(s, stateSet)
	case *jsast.JsIfStmt:
		rewriteAssignmentExpr(s.Test, stateSet)
		s.Then = rewriteAssignmentStmt(s.Then, stateSet)
		if s.Else != nil {
			s.Else = rewriteAssignmentStmt(s.Else, stateSet)
		}
	case *jsast.JsForStmt:
		if s.Init != nil {
			s.Init = rewriteAssignmentStmt(s.Init, stateSet)
		}
		rewriteAssignmentExpr(s.Test, stateSet)
		rewriteAssignmentExpr(s.Update, stateSet)
		s.Body = rewriteAssignmentStmt(s.Body, stateSet)
	case *jsast.JsForInStmt:
		s.Left = rewriteAssignmentStmt(s.Left, stateSet)
		rewriteAssignmentExpr(s.Right, stateSet)
		s.Body = rewriteAssignmentStmt(s.Body, stateSet)
	case *jsast.JsWhileStmt:
		rewriteAssignmentExpr(s.Test, stateSet)
		s.Body = rewriteAssignmentStmt(s.Body, stateSet)
	case *jsast.JsDoWhileStmt:
		s.Body = rewriteAssignmentStmt(s.Body, stateSet)
		rewriteAssignmentExpr(s.Test, stateSet)
	case *jsast.JsReturnStmt:
		rewriteAssignmentExpr(s.Arg, stateSet)
	case *jsast.JsThrowStmt:
		rewriteAssignmentExpr(s.Arg, stateSet)
	case *jsast.JsTryStmt:
		rewriteAssignmentBlock(s.Block, stateSet)
		rewriteAssignmentBlock(s.Handler, stateSet)
		rewriteAssignmentBlock(s.Finalizer, stateSet)
	case *jsast.JsSwitchStmt:
		rewriteAssignmentExpr(s.Disc, stateSet)
		for _, c := range s.Cases {
			rewriteAssignmentExpr(c.Test, stateSet)
			for i, caseStmt := range c.Body {
				c.Body[i] = rewriteAssignmentStmt(caseStmt, stateSet)
			}
		}
	case *jsast.JsLabeledStmt:
		s.Body = rewriteAssignmentStmt(s.Body, stateSet)
	case *jsast.JsVarDecl:
		for _, d := range s.Decls {
			rewriteAssignmentExpr(d.Init, stateSet)
		}
	case *jsast.JsFuncDecl:
		rewriteAssignmentBlock(s.Body, stateSet)
	case *jsast.JsClassDecl:
		rewriteAssignmentExpr(s.SuperClass, stateSet)
		rewriteAssignmentMembers(s.Members, stateSet)
	case *jsast.JsExportDecl:
		if s.Declaration != nil {
			s.Declaration = rewriteAssignmentStmt(s.Declaration, stateSet)
		}
	}
	return stmt
}

// rewriteAssignmentMembers rewrites state writes in class bodies: methods,
// accessors, field initializers, computed keys and static blocks (task 055).
func rewriteAssignmentMembers(members []*jsast.JsClassMember, stateSet map[string]bool) {
	for _, m := range members {
		if m.Computed {
			rewriteAssignmentExpr(m.Key, stateSet)
		}
		rewriteAssignmentExpr(m.Value, stateSet)
		rewriteAssignmentBlock(m.Body, stateSet)
	}
}

func reactiveSetterStmt(expr jsast.JsExpr, stateSet map[string]bool) jsast.JsStmt {
	if setter := reactiveSetterExpr(expr, stateSet); setter != nil {
		return jsast.GenExprStmt(setter)
	}
	if block := reactivePatternAssignment(expr, stateSet); block != nil {
		return block
	}
	return nil
}

// reactivePatternAssignment rewrites a destructuring assignment to state,
// `[a, b] = [b, a]` or `({ a = 5, b } = o)`, into assignments to block-local
// temporaries followed by one setter per state target (task 055). Without it
// the state changed without notifying the view.
func reactivePatternAssignment(expr jsast.JsExpr, stateSet map[string]bool) jsast.JsStmt {
	assign, ok := expr.(*jsast.JsAssignExpr)
	if !ok || assign.Op != "=" {
		return nil
	}
	switch assign.L.(type) {
	case *jsast.JsArrayExpr, *jsast.JsObjectExpr:
	default:
		return nil
	}
	var names []string
	seen := map[string]bool{}
	replacePatternTargets(assign.L, stateSet, func(name string) jsast.JsExpr {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
		return jsast.GenIdent("$$d_" + name)
	})
	if len(names) == 0 {
		return nil
	}
	decl := &jsast.JsVarDecl{BaseStmt: jsast.BaseStmt{BaseNode: jsast.GenNode()}, Kind: "let"}
	for _, name := range names {
		decl.Decls = append(decl.Decls, &jsast.JsVarDeclarator{BaseNode: jsast.GenNode(), Id: jsast.GenIdent("$$d_" + name)})
	}
	body := []jsast.JsStmt{decl, jsast.GenExprStmt(assign)}
	for _, name := range names {
		body = append(body, jsast.GenExprStmt(jsast.GenCall(jsast.GenIdent("$$set_"+name), jsast.GenIdent("$$d_"+name))))
	}
	return jsast.GenBlock(body...)
}

// replacePatternTargets replaces every state identifier that a destructuring
// pattern assigns with the expression temp returns for it.
func replacePatternTargets(pattern jsast.JsExpr, stateSet map[string]bool, temp func(string) jsast.JsExpr) jsast.JsExpr {
	switch p := pattern.(type) {
	case *jsast.JsIdent:
		if stateSet[p.Name] {
			return temp(p.Name)
		}
	case *jsast.JsAssignExpr:
		p.L = replacePatternTargets(p.L, stateSet, temp)
	case *jsast.JsSpreadExpr:
		p.X = replacePatternTargets(p.X, stateSet, temp)
	case *jsast.JsArrayExpr:
		for i, el := range p.Elems {
			if el != nil {
				p.Elems[i] = replacePatternTargets(el, stateSet, temp)
			}
		}
	case *jsast.JsObjectExpr:
		for _, prop := range p.Props {
			prop.Value = replacePatternTargets(prop.Value, stateSet, temp)
		}
	}
	return pattern
}

func reactiveSetterExpr(expr jsast.JsExpr, stateSet map[string]bool) jsast.JsExpr {
	if update, ok := expr.(*jsast.JsUnaryExpr); ok && (update.Op == "++" || update.Op == "--") {
		if name, ok := reactiveIdent(update.X, stateSet); ok {
			op := "+"
			if update.Op == "--" {
				op = "-"
			}
			return jsast.GenCall(jsast.GenIdent("$$set_"+name), jsast.GenBinary(op, jsast.GenIdent(name), jsast.GenNumLit(1)))
		}
	}

	assign, ok := expr.(*jsast.JsAssignExpr)
	if !ok {
		return nil
	}
	name, ok := reactiveIdent(assign.L, stateSet)
	if !ok {
		return nil
	}

	if assign.Op == "=" {
		return jsast.GenCall(jsast.GenIdent("$$set_"+name), assign.R)
	}
	op := strings.TrimSuffix(assign.Op, "=")
	if op == assign.Op {
		return nil
	}
	if op == "&&" || op == "||" || op == "??" {
		return jsast.GenCall(jsast.GenIdent("$$set_"+name), jsast.GenLogical(op, jsast.GenIdent(name), assign.R))
	}
	switch op {
	case "+", "-", "*", "/", "%", "**":
		return jsast.GenCall(jsast.GenIdent("$$set_"+name), jsast.GenBinary(op, jsast.GenIdent(name), assign.R))
	default:
		return nil
	}
}

func reactiveIdent(expr jsast.JsExpr, stateSet map[string]bool) (string, bool) {
	id, ok := expr.(*jsast.JsIdent)
	if !ok || !stateSet[id.Name] {
		return "", false
	}
	return id.Name, true
}

func rewriteAssignmentExpr(expr jsast.JsExpr, stateSet map[string]bool) {
	if expr == nil {
		return
	}
	switch e := expr.(type) {
	case *jsast.JsArrayExpr:
		for _, item := range e.Elems {
			rewriteAssignmentExpr(item, stateSet)
		}
	case *jsast.JsObjectExpr:
		for _, prop := range e.Props {
			if prop.Computed {
				rewriteAssignmentExpr(prop.Key, stateSet)
			}
			rewriteAssignmentExpr(prop.Value, stateSet)
		}
	case *jsast.JsFuncExpr:
		rewriteAssignmentBlock(e.Body, stateSet)
	case *jsast.JsArrowFunc:
		if block, ok := e.Body.(*jsast.JsBlock); ok {
			rewriteAssignmentBlock(block, stateSet)
		} else if body, ok := e.Body.(jsast.JsExpr); ok {
			rewriteAssignmentExpr(body, stateSet)
		}
	case *jsast.JsUnaryExpr:
		rewriteAssignmentExpr(e.X, stateSet)
	case *jsast.JsBinaryExpr:
		rewriteAssignmentExpr(e.L, stateSet)
		rewriteAssignmentExpr(e.R, stateSet)
	case *jsast.JsLogicalExpr:
		rewriteAssignmentExpr(e.L, stateSet)
		rewriteAssignmentExpr(e.R, stateSet)
	case *jsast.JsAssignExpr:
		rewriteAssignmentExpr(e.L, stateSet)
		rewriteAssignmentExpr(e.R, stateSet)
	case *jsast.JsMemberExpr:
		rewriteAssignmentExpr(e.Obj, stateSet)
		if e.Computed {
			rewriteAssignmentExpr(e.Prop, stateSet)
		}
	case *jsast.JsCallExpr:
		rewriteAssignmentExpr(e.Callee, stateSet)
		for _, arg := range e.Args {
			rewriteAssignmentExpr(arg, stateSet)
		}
	case *jsast.JsNewExpr:
		rewriteAssignmentExpr(e.Callee, stateSet)
		for _, arg := range e.Args {
			rewriteAssignmentExpr(arg, stateSet)
		}
	case *jsast.JsConditionalExpr:
		rewriteAssignmentExpr(e.Test, stateSet)
		rewriteAssignmentExpr(e.Then, stateSet)
		rewriteAssignmentExpr(e.Else, stateSet)
	case *jsast.JsSeqExpr:
		for _, item := range e.Exprs {
			rewriteAssignmentExpr(item, stateSet)
		}
	case *jsast.JsSpreadExpr:
		rewriteAssignmentExpr(e.X, stateSet)
	case *jsast.JsAwaitExpr:
		rewriteAssignmentExpr(e.X, stateSet)
	case *jsast.JsYieldExpr:
		rewriteAssignmentExpr(e.X, stateSet)
	case *jsast.JsTemplateLit:
		for _, item := range e.Exprs {
			rewriteAssignmentExpr(item, stateSet)
		}
	case *jsast.JsTaggedTemplate:
		rewriteAssignmentExpr(e.Tag, stateSet)
		rewriteAssignmentExpr(e.Quasi, stateSet)
	case *jsast.JsClassExpr:
		rewriteAssignmentExpr(e.SuperClass, stateSet)
		rewriteAssignmentMembers(e.Members, stateSet)
	}
}

// collectRepls walks the AST and accumulates replacements for assignment nodes.
func collectRepls(root jsast.JsNode, src string, stateSet map[string]bool, out *[]repl) {
	jsast.WalkNode(root, func(n jsast.JsNode) bool {
		stmt, ok := n.(*jsast.JsExprStmt)
		if !ok {
			return true
		}

		// ── Postfix/prefix update:  x++  x--  ++x  --x ────────────────────────
		if upd, ok := stmt.X.(*jsast.JsUnaryExpr); ok {
			if upd.Op == "++" || upd.Op == "--" {
				if id, ok := upd.X.(*jsast.JsIdent); ok && stateSet[id.Name] {
					op := "+"
					if upd.Op == "--" {
						op = "-"
					}
					*out = append(*out, repl{
						stmt.Pos, stmt.End,
						setter(id.Name, id.Name+op+"1") + ";",
					})
					return false
				}
			}
		}

		// ── Assignment expression:  x = …  x += …  x -= …  etc. ─────────────
		if assign, ok := stmt.X.(*jsast.JsAssignExpr); ok {
			// Destructuring-assignment LHS:  ({x} = obj)  [a, b] = pair
			// Only plain `=` makes sense for patterns (compound ops are nonsense
			// syntax in JS).  We rewrite the whole statement into:
			//   const $$d<n> = <rhs>;  $$set_x($$d<n>.x);  $$set_y($$d<n>.y);
			// and skip irrelevant idents in the pattern (non-state vars).
			//
			// Known limitation: default values inside the pattern (`{ x = 5 }`)
			// aren't supported by our JS parser in pattern position, so they'll
			// parse as two statements and this branch won't match.  In practice
			// templates rarely use defaults; users will get a clear parse error
			// via the existing unrewritten path.
			if _, isObj := assign.L.(*jsast.JsObjectExpr); isObj && assign.Op == "=" {
				if rewritten, ok := rewritePatternAssign(src, assign, stateSet); ok {
					*out = append(*out, repl{stmt.Pos, stmt.End, rewritten})
					return false
				}
			}
			if _, isArr := assign.L.(*jsast.JsArrayExpr); isArr && assign.Op == "=" {
				if rewritten, ok := rewritePatternAssign(src, assign, stateSet); ok {
					*out = append(*out, repl{stmt.Pos, stmt.End, rewritten})
					return false
				}
			}

			id, ok := assign.L.(*jsast.JsIdent)
			if !ok || !stateSet[id.Name] {
				return true
			}

			// Extract the RHS text by scanning forward from the end of the LHS
			// identifier. We can't rely on assign.R.NodePos() because parsePrimary
			// strips outer parentheses — e.g. for `ampm = (h >= 12) ? 'PM' : 'AM'`
			// the ConditionalExpr.Pos points to `h` (inside the parens), not `(`.
			// Instead, find the assignment operator in the source and take everything
			// after it up to the statement end (minus the trailing semicolon).
			rhsText := extractRHS(src, assign.L.NodeEnd(), assign.Op, stmt.End)
			name := id.Name

			var text string
			switch assign.Op {
			case "=":
				text = setter(name, rhsText)
			case "+=":
				text = setter(name, name+"+"+rhsText)
			case "-=":
				text = setter(name, name+"-"+rhsText)
			case "*=":
				text = setter(name, name+"*"+rhsText)
			case "/=":
				text = setter(name, name+"/"+rhsText)
			case "%=":
				text = setter(name, name+"%"+rhsText)
			case "**=":
				text = setter(name, name+"**"+rhsText)
			case "&&=":
				text = setter(name, name+"&&"+rhsText)
			case "||=":
				text = setter(name, name+"||"+rhsText)
			case "??=":
				text = setter(name, name+"??"+rhsText)
			default:
				return true // unrecognised compound op — leave as-is
			}
			*out = append(*out, repl{stmt.Pos, stmt.End, text + ";"})
			return false
		}

		return true
	})
}

// extractRHS extracts the right-hand side text of an assignment from the raw
// source string by scanning forward from lhsEnd to find the operator, then
// returning everything after it up to stmtEnd (trimmed, semicolons removed).
//
// This is more robust than using assign.R.NodePos() because parsePrimary strips
// outer parentheses from grouped expressions — e.g. for `x = (a) ? b : c` the
// ConditionalExpr.Pos points to `a` (inside the parens), losing the `(`.
func extractRHS(src string, lhsEnd int, op string, stmtEnd int) string {
	// Scan forward from the end of the LHS to find the operator.
	// The operator immediately follows optional whitespace after the identifier.
	i := lhsEnd
	n := len(src)
	for i < n && (src[i] == ' ' || src[i] == '\t') {
		i++
	}
	// Consume the operator
	if i+len(op) <= n && src[i:i+len(op)] == op {
		i += len(op)
	}
	// Trim leading whitespace after the operator
	for i < n && (src[i] == ' ' || src[i] == '\t') {
		i++
	}
	// Take everything up to stmtEnd, strip trailing semicolon and whitespace
	end := stmtEnd
	if end > n {
		end = n
	}
	rhs := strings.TrimSpace(src[i:end])
	rhs = strings.TrimRight(rhs, ";")
	return strings.TrimSpace(rhs)
}
func applyRepls(src string, repls []repl) string {
	sort.Slice(repls, func(i, j int) bool { return repls[i].start < repls[j].start })

	var out strings.Builder
	pos := 0
	for _, r := range repls {
		if r.start < pos {
			continue // skip overlapping (shouldn't happen with well-formed AST)
		}
		out.WriteString(src[pos:r.start])
		out.WriteString(r.text)
		pos = r.end
	}
	out.WriteString(src[pos:])
	return out.String()
}

// setter formats a reactive setter call.
func setter(name, expr string) string {
	return fmt.Sprintf("$$set_%s(%s)", name, expr)
}

// patternTempCounter produces fresh temp names across invocations of
// rewritePatternAssign within a single compilation.  Not exported; reset
// per compilation would require a CodeGen field, but the counter only
// needs to yield *unique* names per script — a package-level counter
// is fine because each call within one script appends a new temp name
// to the rewritten output regardless of counter value.
var patternTempCounter int

// rewritePatternAssign converts a destructuring assignment like
//
//	({ count, name: n } = obj)       →   const $$d0 = obj; $$set_count($$d0.count); $$set_n($$d0.name);
//	[a, , b] = pair                  →   const $$d1 = pair; $$set_a($$d1[0]); $$set_b($$d1[2]);
//	({ nested: { x } } = obj)        →   const $$d2 = obj; $$set_x($$d2.nested.x);
//
// into a sequence of setter calls.  Non-reactive targets in the pattern
// are silently dropped from the rewritten output (they didn't need a
// setter; but note that the original non-reactive assignment is also
// lost — users shouldn't mix reactive and non-reactive in the same
// destructuring.  If this becomes a real problem we can emit the original
// binding for non-reactive targets too).
//
// Returns ok=false if the pattern references zero reactive variables —
// callers then leave the original statement intact.
func rewritePatternAssign(src string, assign *jsast.JsAssignExpr, stateSet map[string]bool) (string, bool) {
	// Collect (accessPath, reactiveName) pairs for every state-var ident
	// inside the pattern.  accessPath is the suffix appended to the temp
	// variable — e.g. ".count", "[0]", ".nested.x".
	var targets []patternTarget
	collectPatternTargets(assign.L, "", stateSet, &targets)
	if len(targets) == 0 {
		return "", false
	}

	// Extract the RHS source text using the existing helper.
	rhsText := extractRHS(src, assign.L.NodeEnd(), assign.Op, assign.End)

	patternTempCounter++
	tmp := fmt.Sprintf("$$d%d", patternTempCounter)

	var b strings.Builder
	fmt.Fprintf(&b, "const %s = %s;", tmp, rhsText)
	for _, t := range targets {
		b.WriteString(" ")
		b.WriteString(setter(t.name, tmp+t.path))
		b.WriteString(";")
	}
	return b.String(), true
}

type patternTarget struct {
	name string // reactive state variable name  (e.g. "count")
	path string // access path from temp         (e.g. ".count", "[0]", ".a.b")
}

// collectPatternTargets walks a pattern-expression subtree (JsObjectExpr /
// JsArrayExpr / JsIdent) and emits one patternTarget for every JsIdent
// whose name is in stateSet.  Nested patterns extend the access path.
//
// The JsAssignExpr-inside-pattern case (default values, `{x = 5}`) is
// currently unreachable because the parser doesn't represent it this way
// — but we defensively handle it by descending through its L side anyway.
func collectPatternTargets(node jsast.JsExpr, path string, stateSet map[string]bool, out *[]patternTarget) {
	switch n := node.(type) {
	case *jsast.JsIdent:
		if stateSet[n.Name] {
			*out = append(*out, patternTarget{name: n.Name, path: path})
		}
	case *jsast.JsObjectExpr:
		for _, p := range n.Props {
			if p.Spread {
				// { ...rest } — can't map back to setters cleanly
				// (rest captures whatever's left, we'd need Object.assign).
				// Skip; leaves it unhandled (user gets original assignment
				// via the non-rewrite path, which will then likely error
				// because we DID rewrite the assignment).  Document this.
				continue
			}
			var keyName string
			if id, ok := p.Key.(*jsast.JsIdent); ok {
				keyName = id.Name
			} else if lit, ok := p.Key.(*jsast.JsLiteral); ok {
				// String literal keys:  {"foo": x}
				keyName = strings.Trim(lit.Raw, `"'`)
			} else {
				continue // computed keys: give up (can't build a static path)
			}
			var sub string
			if isValidPropName(keyName) {
				sub = path + "." + keyName
			} else {
				sub = path + "[" + quote(keyName) + "]"
			}
			collectPatternTargets(p.Value, sub, stateSet, out)
		}
	case *jsast.JsArrayExpr:
		for i, el := range n.Elems {
			if el == nil {
				continue // hole: [, , x]
			}
			if sp, ok := el.(*jsast.JsSpreadExpr); ok {
				// [...rest] — same issue as object rest. Skip.
				_ = sp
				continue
			}
			sub := fmt.Sprintf("%s[%d]", path, i)
			collectPatternTargets(el, sub, stateSet, out)
		}
	case *jsast.JsAssignExpr:
		// { x = 5 } default-value form (rare; parser may not reach here, but
		// defensive).  The L side is the binding target.
		if n.Op == "=" {
			collectPatternTargets(n.L, path, stateSet, out)
		}
	}
}

// isValidPropName reports whether name can follow a dot without quoting.
func isValidPropName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if i == 0 {
			if !(c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
				return false
			}
		} else {
			if !(c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
				return false
			}
		}
	}
	return true
}

func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' || c == '\\' {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	b.WriteByte('"')
	return b.String()
}

// rewriteTemplateHandlerAssignments rewrites writes to reactive bindings in a
// template event handler. Template attributes bypass script-level rewriting,
// so concise and block-bodied arrows need this dedicated entry point.
func rewriteTemplateHandlerAssignments(handler string, analysis *Analysis) string {
	handler = sfc.UnwrapBraces(handler)
	if analysis == nil || (len(analysis.StateVars) == 0 && len(analysis.Props) == 0) {
		return handler
	}
	stateSet := analysis.StateSet()

	arrowIdx := strings.Index(handler, "=>")
	if arrowIdx < 0 {
		return handler
	}
	body := strings.TrimSpace(handler[arrowIdx+2:])
	params := handler[:arrowIdx+2]

	if strings.HasPrefix(body, "{") && strings.HasSuffix(strings.TrimSpace(body), "}") {
		inner := body[1 : len(body)-1]
		rewritten := RewriteAssignments(inner, jsast.ParseJS(inner), stateSet)
		return params + " { " + strings.TrimSpace(rewritten) + " }"
	}

	wrappedParens := strings.HasPrefix(body, "(") && strings.HasSuffix(body, ")")
	if wrappedParens {
		body = strings.TrimSpace(body[1 : len(body)-1])
	}
	bodyStmt := body + ";"
	rewritten := RewriteAssignments(bodyStmt, jsast.ParseJS(bodyStmt), stateSet)
	if rewritten == bodyStmt {
		return handler
	}
	rewritten = strings.TrimSuffix(strings.TrimSpace(rewritten), ";")
	if wrappedParens {
		return params + " (" + strings.TrimSpace(rewritten) + ")"
	}
	return params + " " + strings.TrimSpace(rewritten)
}
