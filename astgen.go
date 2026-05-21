package main

// astgen.go — the AST-based code generator (Phase C).
//
// This is the replacement for codegen.go's string-concatenation
// approach.  Instead of writing JS text into buffers, it constructs
// a JsProgram tree and hands it to the printer (jsprinter.go).
//
// Built in parallel with the legacy generator behind the
// --codegen flag.  The differential test harness
// (astgen_diff_test.go) compiles every fixture both ways and diffs
// the output; as blocks migrate, the diff shrinks to zero, at which
// point the AST generator becomes the default.
//
// MIGRATION STATUS — blocks completed:
//   [x] 1. Module skeleton  (imports, fn shell, return object)
//   [x] 2. Static elements & attributes
//   [ ] 3. Text & interpolation
//   [ ] 4. Script splicing
//   [ ] 5. State & setters
//   [ ] 6. Derived & effects
//   [ ] 7. Event handlers & bindings
//   [ ] 8. class: / style: directives
//   [ ] 9. {#if} chains
//   [ ] 10. {#each} blocks
//   [ ] 11. {#key}
//   [ ] 12. {#await}
//   [ ] 13. Slots
//   [ ] 14. Deep state
//
// Until all blocks land, generateCodeAST falls back to the legacy
// generator for anything it can't yet build — see the fallback note
// in generateCodeAST.

// (no imports — astgen builds on helpers from elsewhere in package main)

// ─────────────────────────────────────────────
//  AST construction helpers
// ─────────────────────────────────────────────
//
// Hand-building AST nodes is verbose, so these helpers cover the
// shapes astgen reaches for most often.  Two strategies are mixed:
//
//   - Direct constructors (id, lit, call…) for small nodes.
//   - jsStmts / jsExpr — parse a known-good snippet of JS text and
//     lift its AST.  This is the "splice a parsed fragment" trick:
//     it lets a complex but *static* piece of output be written as
//     readable JS instead of a tower of struct literals.  The
//     fragment is fully under compiler control (never user input),
//     so parsing it is safe and deterministic.
//
// Everything constructed here is marked OriginGenerated.

// gen marks a freshly built node as compiler-generated.
func genNode() baseNode { return baseNode{Origin: OriginGenerated} }

// id builds an identifier.
func id(name string) *JsIdent {
	return &JsIdent{baseExpr: baseExpr{genNode()}, Name: name}
}

// strLit builds a single-quoted string literal.  The value is
// emitted verbatim between quotes — callers pass already-escaped
// content.
func strLit(s string) *JsLiteral {
	return &JsLiteral{
		baseExpr: baseExpr{genNode()},
		Raw:      "'" + s + "'",
		Kind:     "string",
	}
}

// jsStmts parses src as a sequence of statements and returns them,
// each re-marked OriginGenerated.  Panics on parse failure — the
// input is always compiler-controlled, so a failure is a compiler
// bug that should surface loudly during development.
func jsStmts(src string) []JsStmt {
	prog := ParseJS(src)
	markGenerated(prog)
	return prog.Body
}

// jsStmt parses src as exactly one statement.
func jsStmt(src string) JsStmt {
	s := jsStmts(src)
	if len(s) != 1 {
		panic("jsStmt: expected exactly one statement from: " + src)
	}
	return s[0]
}

// jsExpr parses src as a single expression.
func jsExpr(src string) JsExpr {
	e := parseJSExpr(src)
	if e == nil {
		panic("jsExpr: failed to parse: " + src)
	}
	markGeneratedNode(e)
	return e
}

// markGenerated recursively re-stamps every node in a tree as
// OriginGenerated.  Used after parsing a compiler-authored snippet,
// since the parser stamps everything OriginUser by default.
func markGenerated(prog *JsProgram) {
	for _, s := range prog.Body {
		markGeneratedNode(s)
	}
}

func markGeneratedNode(n JsNode) {
	if n == nil {
		return
	}
	WalkNode(n, func(c JsNode) bool {
		setOrigin(c, OriginGenerated)
		return true
	})
}

// setOrigin stamps a node's Origin.  WalkNode hands back the
// interface; we reach the embedded baseNode through the concrete
// types.  Only the types astgen actually produces need handling;
// anything else is left as-is (harmless).
func setOrigin(n JsNode, o NodeOrigin) {
	switch x := n.(type) {
	case *JsIdent:
		x.Origin = o
	case *JsLiteral:
		x.Origin = o
	case *JsTemplateLit:
		x.Origin = o
	case *JsArrayExpr:
		x.Origin = o
	case *JsObjectExpr:
		x.Origin = o
	case *JsFuncExpr:
		x.Origin = o
	case *JsArrowFunc:
		x.Origin = o
	case *JsUnaryExpr:
		x.Origin = o
	case *JsBinaryExpr:
		x.Origin = o
	case *JsLogicalExpr:
		x.Origin = o
	case *JsAssignExpr:
		x.Origin = o
	case *JsMemberExpr:
		x.Origin = o
	case *JsCallExpr:
		x.Origin = o
	case *JsNewExpr:
		x.Origin = o
	case *JsConditionalExpr:
		x.Origin = o
	case *JsSeqExpr:
		x.Origin = o
	case *JsSpreadExpr:
		x.Origin = o
	case *JsAwaitExpr:
		x.Origin = o
	case *JsYieldExpr:
		x.Origin = o
	case *JsThisExpr:
		x.Origin = o
	case *JsSuperExpr:
		x.Origin = o
	case *JsVarDecl:
		x.Origin = o
	case *JsFuncDecl:
		x.Origin = o
	case *JsClassDecl:
		x.Origin = o
	case *JsBlock:
		x.Origin = o
	case *JsExprStmt:
		x.Origin = o
	case *JsIfStmt:
		x.Origin = o
	case *JsForStmt:
		x.Origin = o
	case *JsForInStmt:
		x.Origin = o
	case *JsWhileStmt:
		x.Origin = o
	case *JsDoWhileStmt:
		x.Origin = o
	case *JsReturnStmt:
		x.Origin = o
	case *JsThrowStmt:
		x.Origin = o
	case *JsTryStmt:
		x.Origin = o
	case *JsSwitchStmt:
		x.Origin = o
	case *JsBreakStmt:
		x.Origin = o
	case *JsContinueStmt:
		x.Origin = o
	case *JsLabeledStmt:
		x.Origin = o
	case *JsEmptyStmt:
		x.Origin = o
	case *JsDebuggerStmt:
		x.Origin = o
	case *JsImportDecl:
		x.Origin = o
	case *JsExportDecl:
		x.Origin = o
	}
}

// ─────────────────────────────────────────────
//  ASTGen — the generator
// ─────────────────────────────────────────────

type ASTGen struct {
	name     string
	scopeID  string
	analysis *Analysis
	sfc      *SFC

	// body accumulates the statements that go inside the component
	// function, in order.  Later blocks append to it.
	body []JsStmt
}

// generateCodeAST is the AST-based counterpart of generateCode.
// Phase C is incremental: it fully compiles only what the migrated
// blocks cover (see astCanHandle) and falls back to the legacy
// generator for everything else.  This keeps the compiler correct
// and shippable throughout the migration.
func generateCodeAST(
	name string,
	sfc *SFC,
	nodes []Node,
	analysis *Analysis,
	scopeID string,
) string {
	// Fallback: anything the migrated blocks don't yet cover goes
	// through the legacy generator.  As blocks land, astCanHandle
	// widens and the fallback shrinks.
	if !astCanHandle(nodes, analysis) {
		return generateCode(name, sfc, nodes, analysis, scopeID)
	}
	g := &ASTGen{
		name:     name,
		scopeID:  scopeID,
		analysis: analysis,
		sfc:      sfc,
	}
	prog := g.buildModule(nodes)
	return headerComment + "\n\n" + PrintJS(prog, PrintPretty)
}

// astCanHandle reports whether the AST generator can fully compile
// the given template with the blocks migrated so far.  Until all 14
// blocks land, generateCodeAST falls back to the legacy generator
// for anything astCanHandle rejects — this keeps the compiler
// correct throughout the migration.
//
// Block 2 status: handles a template that is exactly one fully
// static root element, or multiple fully static root elements
// (the display:contents wrapper case).  Anything with dynamic
// content — interpolation, state, events, blocks — is deferred to
// later blocks and falls back for now.
func astCanHandle(nodes []Node, analysis *Analysis) bool {
	// Props/state/derived/effects mean dynamic behavior the later
	// blocks own.  Block 2 is static-only.
	if analysis != nil {
		if len(analysis.StateVars) > 0 || len(analysis.DerivedVars) > 0 ||
			len(analysis.Effects) > 0 || len(analysis.Props) > 0 {
			return false
		}
	}
	if len(nodes) == 0 {
		return false
	}
	// Every root node must be a fully-static element.
	for _, n := range nodes {
		el, ok := n.(*ElementNode)
		if !ok {
			return false
		}
		if !isFullyStatic(el) {
			return false
		}
	}
	return true
}

// buildModule constructs the whole component module: the exported
// function wrapper and everything inside it.
func (g *ASTGen) buildModule(nodes []Node) *JsProgram {
	prog := &JsProgram{}

	fnBody := &JsBlock{baseStmt: baseStmt{genNode()}}

	// ── Preamble: normalize props/slots ──────────────────────
	fnBody.Body = append(fnBody.Body, jsStmts(`
		$$props = Object.assign({}, $$props);
		$$slots = $$slots || {};
		void $$slots;
	`)...)

	// ── Runtime helper: $$dispatch ───────────────────────────
	fnBody.Body = append(fnBody.Body, jsStmts(`
		function $$dispatch(name, detail) {
			$$target.dispatchEvent(new CustomEvent(name, { detail, bubbles: true }));
		}
		void $$dispatch;
	`)...)

	// ── Runtime helper: $$insertHTML ─────────────────────────
	fnBody.Body = append(fnBody.Body, jsStmts(`
		function $$insertHTML($$t, $$h) {
			if ($$t.nodeType === 11) {
				const $$tmp = document.createElement('template');
				$$tmp.innerHTML = $$h;
				$$t.appendChild($$tmp.content);
			} else {
				$$t.insertAdjacentHTML('beforeend', $$h);
			}
		}
		void $$insertHTML;
	`)...)

	// ── DOM construction (Block 2: static case) ──────────────
	g.buildStaticDOM(nodes)
	fnBody.Body = append(fnBody.Body, g.body...)

	// ── Mount ────────────────────────────────────────────────
	fnBody.Body = append(fnBody.Body,
		jsStmt(`$$target.appendChild($$root);`))

	// ── Return object ────────────────────────────────────────
	fnBody.Body = append(fnBody.Body, g.buildReturn())

	// ── Wrap in the exported function declaration ────────────
	fn := &JsFuncDecl{
		baseStmt: baseStmt{genNode()},
		Name:     g.name,
		Params: []JsExpr{
			id("$$target"), id("$$props"), id("$$slots"),
		},
		Body: fnBody,
	}
	prog.Body = append(prog.Body, &JsExportDecl{
		baseStmt:    baseStmt{genNode()},
		Declaration: fn,
	})
	return prog
}

// buildStaticDOM emits the DOM-construction statements for a fully
// static template into g.body.  Two shapes, mirroring the legacy
// generator:
//
//   - Single root element  → that element becomes $$root directly.
//   - Multiple root elements → wrapped in a display:contents <div>
//     so siblings don't nest inside the first.
//
// Each element gets createElement + the scope-ID attribute + its
// own attributes; a fully static child subtree collapses to one
// innerHTML (single root) or $$insertHTML (wrapper) assignment.
func (g *ASTGen) buildStaticDOM(nodes []Node) {
	if len(nodes) == 1 {
		el := nodes[0].(*ElementNode)
		g.emitStaticRootElement(el)
		return
	}
	// Multi-root: a display:contents wrapper holds the siblings.
	g.body = append(g.body,
		jsStmt(`const $$root = document.createElement('div');`))
	g.body = append(g.body, g.setAttrStmt("$$root", g.scopeID, ""))
	g.body = append(g.body,
		jsStmt(`$$root.style.display = 'contents';`))
	html := emitStaticHTML(nodes, g.scopeID)
	g.body = append(g.body, &JsExprStmt{
		baseStmt: baseStmt{genNode()},
		X: &JsCallExpr{
			baseExpr: baseExpr{genNode()},
			Callee:   id("$$insertHTML"),
			Args:     []JsExpr{id("$$root"), strLit(escapeJSString(html))},
		},
	})
}

// emitStaticRootElement emits createElement + attributes for a
// single static root element, and collapses its static children
// into one innerHTML assignment.
func (g *ASTGen) emitStaticRootElement(el *ElementNode) {
	// const $$root = document.createElement('<tag>');
	g.body = append(g.body, g.createElementStmt("$$root", el.Tag))
	// Scope-ID attribute, then the element's own attributes.
	g.body = append(g.body, g.setAttrStmt("$$root", g.scopeID, ""))
	for _, attr := range el.Attrs {
		g.body = append(g.body,
			g.setAttrStmt("$$root", attr.Name, attr.Value))
	}
	// Static child subtree → innerHTML.
	if len(el.Children) > 0 {
		html := emitStaticHTML(el.Children, g.scopeID)
		g.body = append(g.body, &JsExprStmt{
			baseStmt: baseStmt{genNode()},
			X: &JsAssignExpr{
				baseExpr: baseExpr{genNode()},
				Op:       "=",
				L: &JsMemberExpr{
					baseExpr: baseExpr{genNode()},
					Obj:      id("$$root"),
					Prop:     id("innerHTML"),
				},
				R: strLit(escapeJSString(html)),
			},
		})
	}
}

// createElementStmt builds `const <varName> = document.createElement('<tag>');`
func (g *ASTGen) createElementStmt(varName, tag string) JsStmt {
	return &JsVarDecl{
		baseStmt: baseStmt{genNode()},
		Kind:     "const",
		Decls: []*JsVarDeclarator{{
			Id: id(varName),
			Init: &JsCallExpr{
				baseExpr: baseExpr{genNode()},
				Callee: &JsMemberExpr{
					baseExpr: baseExpr{genNode()},
					Obj:      id("document"),
					Prop:     id("createElement"),
				},
				Args: []JsExpr{strLit(tag)},
			},
		}},
	}
}

// setAttrStmt builds `<varName>.setAttribute('<name>', '<value>');`
func (g *ASTGen) setAttrStmt(varName, name, value string) JsStmt {
	return &JsExprStmt{
		baseStmt: baseStmt{genNode()},
		X: &JsCallExpr{
			baseExpr: baseExpr{genNode()},
			Callee: &JsMemberExpr{
				baseExpr: baseExpr{genNode()},
				Obj:      id(varName),
				Prop:     id("setAttribute"),
			},
			Args: []JsExpr{
				strLit(name),
				strLit(escapeAttr(value)),
			},
		},
	}
}

// buildReturn constructs the `return { destroy, set };` statement.
//
// Block 2: destroy() removes $$root.  set() is emitted only when
// the component has props to receive — a static component with no
// props returns just { destroy }.  Later blocks (5) flesh out
// set()'s body with per-prop dispatch.
func (g *ASTGen) buildReturn() JsStmt {
	hasProps := g.analysis != nil && len(g.analysis.Props) > 0

	if hasProps {
		return jsStmt(`
			return {
				destroy() { $$root.remove(); },
				set(props) {},
			};
		`)
	}
	return jsStmt(`
		return {
			destroy() { $$root.remove(); },
		};
	`)
}

// headerComment is the banner the legacy generator prints at the
// top of every file.  Kept identical so differential diffs aren't
// noisy.  (The printer doesn't emit comments, so the harness
// strips this from the legacy side before diffing — see
// astgen_diff_test.go.)
const headerComment = "// Generated by Vela Compiler — do not edit"
