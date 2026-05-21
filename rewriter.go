package main

import (
	"fmt"
	"sort"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
//  Generic AST walker
// ─────────────────────────────────────────────────────────────────────────────

// WalkNode calls fn on every node in the subtree rooted at node, depth-first.
// If fn returns false the subtree below that node is not visited.
func WalkNode(node JsNode, fn func(JsNode) bool) {
	if node == nil {
		return
	}
	if !fn(node) {
		return
	}
	switch n := node.(type) {
	case *JsProgram:
		for _, s := range n.Body {
			WalkNode(s, fn)
		}
	case *JsBlock:
		for _, s := range n.Body {
			WalkNode(s, fn)
		}
	case *JsExprStmt:
		WalkNode(n.X, fn)
	case *JsVarDecl:
		for _, d := range n.Decls {
			WalkNode(d.Id, fn)
			if d.Init != nil {
				WalkNode(d.Init, fn)
			}
		}
	case *JsFuncDecl:
		for _, p := range n.Params {
			WalkNode(p, fn)
		}
		WalkNode(n.Body, fn)
	case *JsFuncExpr:
		for _, p := range n.Params {
			WalkNode(p, fn)
		}
		WalkNode(n.Body, fn)
	case *JsArrowFunc:
		for _, p := range n.Params {
			WalkNode(p, fn)
		}
		WalkNode(n.Body, fn)
	case *JsIfStmt:
		WalkNode(n.Test, fn)
		WalkNode(n.Then, fn)
		if n.Else != nil {
			WalkNode(n.Else, fn)
		}
	case *JsForStmt:
		if n.Init != nil {
			WalkNode(n.Init, fn)
		}
		if n.Test != nil {
			WalkNode(n.Test, fn)
		}
		if n.Update != nil {
			WalkNode(n.Update, fn)
		}
		WalkNode(n.Body, fn)
	case *JsForInStmt:
		WalkNode(n.Left, fn)
		WalkNode(n.Right, fn)
		WalkNode(n.Body, fn)
	case *JsWhileStmt:
		WalkNode(n.Test, fn)
		WalkNode(n.Body, fn)
	case *JsDoWhileStmt:
		WalkNode(n.Body, fn)
		WalkNode(n.Test, fn)
	case *JsReturnStmt:
		if n.Arg != nil {
			WalkNode(n.Arg, fn)
		}
	case *JsThrowStmt:
		WalkNode(n.Arg, fn)
	case *JsTryStmt:
		WalkNode(n.Block, fn)
		if n.Handler != nil {
			WalkNode(n.Handler, fn)
		}
		if n.Finalizer != nil {
			WalkNode(n.Finalizer, fn)
		}
	case *JsSwitchStmt:
		WalkNode(n.Disc, fn)
		for _, c := range n.Cases {
			if c.Test != nil {
				WalkNode(c.Test, fn)
			}
			for _, s := range c.Body {
				WalkNode(s, fn)
			}
		}
	case *JsLabeledStmt:
		WalkNode(n.Body, fn)
	case *JsExportDecl:
		WalkNode(n.Declaration, fn)
	// Expressions
	case *JsArrayExpr:
		for _, e := range n.Elems {
			if e != nil {
				WalkNode(e, fn)
			}
		}
	case *JsObjectExpr:
		for _, prop := range n.Props {
			if !prop.Computed {
				// Don't descend into shorthand key idents as "references"
				if !prop.Shorthand {
					WalkNode(prop.Key, fn)
				}
			} else {
				WalkNode(prop.Key, fn)
			}
			WalkNode(prop.Value, fn)
		}
	case *JsUnaryExpr:
		WalkNode(n.X, fn)
	case *JsBinaryExpr:
		WalkNode(n.L, fn)
		WalkNode(n.R, fn)
	case *JsLogicalExpr:
		WalkNode(n.L, fn)
		WalkNode(n.R, fn)
	case *JsAssignExpr:
		WalkNode(n.L, fn)
		WalkNode(n.R, fn)
	case *JsMemberExpr:
		WalkNode(n.Obj, fn)
		if n.Computed {
			WalkNode(n.Prop, fn)
		}
	case *JsCallExpr:
		WalkNode(n.Callee, fn)
		for _, a := range n.Args {
			WalkNode(a, fn)
		}
	case *JsNewExpr:
		WalkNode(n.Callee, fn)
		for _, a := range n.Args {
			WalkNode(a, fn)
		}
	case *JsConditionalExpr:
		WalkNode(n.Test, fn)
		WalkNode(n.Then, fn)
		WalkNode(n.Else, fn)
	case *JsSeqExpr:
		for _, e := range n.Exprs {
			WalkNode(e, fn)
		}
	case *JsSpreadExpr:
		WalkNode(n.X, fn)
	case *JsAwaitExpr:
		WalkNode(n.X, fn)
	case *JsYieldExpr:
		if n.X != nil {
			WalkNode(n.X, fn)
		}
	case *JsTemplateLit:
		// Template literals are structured — recurse into each ${…}
		// interpolation so identifier collection, scope resolution,
		// and rewriting all see inside them.
		for _, e := range n.Exprs {
			WalkNode(e, fn)
		}
	// Leaves: JsIdent, JsLiteral, JsThisExpr, JsSuperExpr,
	// JsImportDecl, JsBreakStmt, JsContinueStmt, JsEmptyStmt, JsDebuggerStmt
	}
}

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

// CollectIdents returns every identifier name that appears in the subtree
// rooted at node, including identifiers inside template literal expressions.
func CollectIdents(node JsNode) []string {
	seen := map[string]bool{}
	var names []string

	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}

	WalkNode(node, func(n JsNode) bool {
		switch x := n.(type) {
		case *JsIdent:
			add(x.Name)
		}
		return true
	})
	return names
}

// FindDepsInNode returns the subset of vars that appear as identifiers
// somewhere in the AST rooted at node (including inside template literals).
func FindDepsInNode(node JsNode, vars []string) []string {
	varSet := make(map[string]bool, len(vars))
	for _, v := range vars {
		varSet[v] = true
	}

	allIdents := CollectIdents(node)
	seen := map[string]bool{}
	var deps []string
	for _, name := range allIdents {
		if varSet[name] && !seen[name] {
			seen[name] = true
			deps = append(deps, name)
		}
	}
	return deps
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
func RewriteAssignments(src string, prog *JsProgram, stateSet map[string]bool) string {
	var repls []repl
	collectRepls(prog, src, stateSet, &repls)
	return applyRepls(src, repls)
}

// collectRepls walks the AST and accumulates replacements for assignment nodes.
func collectRepls(root JsNode, src string, stateSet map[string]bool, out *[]repl) {
	WalkNode(root, func(n JsNode) bool {
		stmt, ok := n.(*JsExprStmt)
		if !ok {
			return true
		}

		// ── Postfix/prefix update:  x++  x--  ++x  --x ────────────────────────
		if upd, ok := stmt.X.(*JsUnaryExpr); ok {
			if upd.Op == "++" || upd.Op == "--" {
				if id, ok := upd.X.(*JsIdent); ok && stateSet[id.Name] {
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
		if assign, ok := stmt.X.(*JsAssignExpr); ok {
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
			if _, isObj := assign.L.(*JsObjectExpr); isObj && assign.Op == "=" {
				if rewritten, ok := rewritePatternAssign(src, assign, stateSet); ok {
					*out = append(*out, repl{stmt.Pos, stmt.End, rewritten})
					return false
				}
			}
			if _, isArr := assign.L.(*JsArrayExpr); isArr && assign.Op == "=" {
				if rewritten, ok := rewritePatternAssign(src, assign, stateSet); ok {
					*out = append(*out, repl{stmt.Pos, stmt.End, rewritten})
					return false
				}
			}

			id, ok := assign.L.(*JsIdent)
			if !ok || !stateSet[id.Name] {
				return true
			}

			// Extract the RHS text by scanning forward from the end of the LHS
			// identifier. We can't rely on assign.R.nodePos() because parsePrimary
			// strips outer parentheses — e.g. for `ampm = (h >= 12) ? 'PM' : 'AM'`
			// the ConditionalExpr.Pos points to `h` (inside the parens), not `(`.
			// Instead, find the assignment operator in the source and take everything
			// after it up to the statement end (minus the trailing semicolon).
			rhsText := extractRHS(src, assign.L.nodeEnd(), assign.Op, stmt.End)
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
// This is more robust than using assign.R.nodePos() because parsePrimary strips
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
func rewritePatternAssign(src string, assign *JsAssignExpr, stateSet map[string]bool) (string, bool) {
	// Collect (accessPath, reactiveName) pairs for every state-var ident
	// inside the pattern.  accessPath is the suffix appended to the temp
	// variable — e.g. ".count", "[0]", ".nested.x".
	var targets []patternTarget
	collectPatternTargets(assign.L, "", stateSet, &targets)
	if len(targets) == 0 {
		return "", false
	}

	// Extract the RHS source text using the existing helper.
	rhsText := extractRHS(src, assign.L.nodeEnd(), assign.Op, assign.End)

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
func collectPatternTargets(node JsExpr, path string, stateSet map[string]bool, out *[]patternTarget) {
	switch n := node.(type) {
	case *JsIdent:
		if stateSet[n.Name] {
			*out = append(*out, patternTarget{name: n.Name, path: path})
		}
	case *JsObjectExpr:
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
			if id, ok := p.Key.(*JsIdent); ok {
				keyName = id.Name
			} else if lit, ok := p.Key.(*JsLiteral); ok {
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
	case *JsArrayExpr:
		for i, el := range n.Elems {
			if el == nil {
				continue // hole: [, , x]
			}
			if sp, ok := el.(*JsSpreadExpr); ok {
				// [...rest] — same issue as object rest. Skip.
				_ = sp
				continue
			}
			sub := fmt.Sprintf("%s[%d]", path, i)
			collectPatternTargets(el, sub, stateSet, out)
		}
	case *JsAssignExpr:
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
