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
//   [x] 3. Text & interpolation
//   [x] 4. Script splicing  (imports + plain declarations)
//   [x] 5. State & setters  (plain $state + props)
//   [x] 6. Derived & effects
//   [x] 7. Event handlers & bindings  (root element)
//   [x] 8. class: / style: directives  (root element)
//   [x] 9. {#if} chains  (fully-static branch content)
//   [x] 10. {#each} blocks  (via fragment-generator reuse)
//   [x] 11. {#key}  (fully-static branch content)
//   [x] 12. {#await}  (E3.2 — struct-built 3-branch chain + driver)
//   [x] 13. Slots  (via legacy walkElement reuse)
//   [x] 14. Deep state  ($state.deep — Proxy-wrapped, split setter)
//
// ALL 14 BLOCKS MIGRATED.  Phase D (cutover) is next: flip the
// default to the AST generator, remove the astCanBuildDirectly claim
// check, and add the validation passes the migration enables.
//
// Until all blocks land, generateCodeAST falls back to the legacy
// generator for anything it can't yet build — see the fallback note
// in generateCodeAST.

import (
	"sort"
	"strings"
)

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

	// counter backs id() — the deterministic $$nN naming used for
	// DOM-node variables.  Mirrors the legacy CodeGen.counter so
	// generated names match between the two generators.
	counter int

	// fragCounter, when >= 0, switches id() into the {#each}
	// fragment-builder namespace ($$fN).  -1 means component
	// scope ($$nN).  Managed by withFragmentIDs.
	fragCounter int

	// body accumulates DOM-construction statements; initBody holds
	// the "initial render" statements that run after construction.
	body     []JsStmt
	initBody []JsStmt

	// events holds addEventListener calls; cleanup holds the
	// matching removeEventListener calls placed in destroy().
	events  []JsStmt
	cleanup []JsStmt

	// updates maps a reactive variable name → the list of DOM
	// update statements that must re-run when it changes.  Block 5
	// collects these while building holes and replays them inside
	// each setter's $$defer body.  Mirrors CodeGen.updates.
	updates map[string][]JsStmt
}

// addUpdate records that statement stmt must re-run whenever the
// reactive variable dep changes.
func (g *ASTGen) addUpdate(dep string, stmt JsStmt) {
	if g.updates == nil {
		g.updates = map[string][]JsStmt{}
	}
	g.updates[dep] = append(g.updates[dep], stmt)
}

// capturedScope holds the statements a builder function emitted
// while the generator's component-scope buffers were redirected —
// see captureScope.
type capturedScope struct {
	body     []JsStmt            // DOM construction
	initBody []JsStmt            // initial-render statements
	events   []JsStmt            // addEventListener calls
	cleanup  []JsStmt            // teardown statements
	updates  map[string][]JsStmt // reactive dep → update statements
}

// captureScope runs fn with the generator's emission buffers
// (body, initBody, events, cleanup, updates) temporarily swapped
// to fresh empty ones, then restores the originals and returns
// whatever fn emitted.
//
// This is what lets the recursive walker build into a LOCAL scope
// — a slot-factory closure, or an {#each} fragment body — instead
// of component scope.  The legacy generator does the equivalent
// with save/Reset/restore on its strings.Builder buffers; this is
// the AST-side mechanism (E3.6).
func (g *ASTGen) captureScope(fn func()) capturedScope {
	saveBody, saveInit := g.body, g.initBody
	saveEvents, saveCleanup := g.events, g.cleanup
	saveUpdates := g.updates

	g.body, g.initBody = nil, nil
	g.events, g.cleanup = nil, nil
	g.updates = map[string][]JsStmt{}

	fn()

	out := capturedScope{
		body:     g.body,
		initBody: g.initBody,
		events:   g.events,
		cleanup:  g.cleanup,
		updates:  g.updates,
	}

	g.body, g.initBody = saveBody, saveInit
	g.events, g.cleanup = saveEvents, saveCleanup
	g.updates = saveUpdates
	return out
}

// id returns the next deterministic node variable name ($$n0,
// $$n1, …), matching the legacy generator's numbering so the two
// outputs stay comparable.
func (g *ASTGen) id() string {
	if g.fragCounter >= 0 {
		// Inside an {#each} fragment builder: captured vars use the
		// fragment-local $$fN namespace so they are isolated per
		// row builder and stable across multiple {#each} blocks.
		n := g.fragCounter
		g.fragCounter++
		return "$$f" + itoa(n)
	}
	n := g.counter
	g.counter++
	return jsName(n)
}

// withFragmentIDs runs fn with id() switched to the fragment-local
// $$fN namespace (starting at $$f0), then restores the component
// counter.  Used while building an {#each} row fragment.
func (g *ASTGen) withFragmentIDs(fn func()) {
	saved := g.fragCounter
	g.fragCounter = 0
	fn()
	g.fragCounter = saved
}

// jsName formats a node-variable name from a counter value.
func jsName(n int) string {
	return "$$n" + itoa(n)
}

// itoa is a tiny base-10 formatter (avoids importing strconv just
// for this).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// generateCodeAST is the AST-based counterpart of generateCode.
// Phase C is incremental: it fully compiles only what the migrated
// blocks cover (see astCanBuildDirectly) and falls back to the legacy
// generator for everything else.  This keeps the compiler correct
// and shippable throughout the migration.
// generateCodeAST is the code generator.  It always produces a
// unified JS AST, runs the validation passes over it, and prints
// it — there is no path that bypasses the AST.
//
// For templates whose every construct the AST builder handles
// directly, buildModule constructs the tree node-by-node.  For the
// few shapes the direct builder does not yet cover, buildModule
// falls through to buildModuleSplice, which runs the legacy walk
// and RE-PARSES its output into the same JsProgram.  Either way
// the result is one validated AST: the legacy generator is no
// longer a parallel code path, only an internal DOM-emitter
// library behind the splice.
func generateCodeAST(
	name string,
	sfc *SFC,
	nodes []Node,
	analysis *Analysis,
	scopeID string,
) string {
	prog := buildProgram(name, sfc, nodes, analysis, scopeID)
	return headerComment + "\n\n" + PrintJS(prog, PrintPretty)
}

// buildProgram produces the validated JsProgram for a component —
// the structured form behind generateCodeAST.  It is exposed
// separately so tooling (the AST JSON dump, the differential
// harness) can inspect the tree without re-deriving it from the
// printed text.
//
// For templates whose every construct the direct builder handles,
// buildModule constructs the tree node-by-node.  For the few
// shapes it does not yet cover, buildModuleSplice runs the legacy
// walk and re-parses its output into the same JsProgram.  Either
// way the result is one validated AST.
func buildProgram(
	name string,
	sfc *SFC,
	nodes []Node,
	analysis *Analysis,
	scopeID string,
) *JsProgram {
	g := &ASTGen{
		name:        name,
		scopeID:     scopeID,
		analysis:    analysis,
		sfc:         sfc,
		fragCounter: -1,
	}

	var prog *JsProgram
	if astCanBuildDirectly(nodes, analysis) {
		prog = g.buildModule(nodes)
	} else {
		// Universal escape hatch: build the module via the legacy
		// walk, then parse it back so the rest of the pipeline
		// (validation, printing) sees one uniform AST.
		prog = g.buildModuleSplice(name, sfc, nodes, analysis, scopeID)
	}

	// Validation pass — the payoff of the AST migration.  Any
	// issue here is a code-generation bug (a malformed control
	// structure, a reference to wiring that was never emitted),
	// not a user error.  Surfaced loudly: a panic with the
	// located diagnostics, so the bug is caught in CI rather than
	// shipped as broken JS.
	if issues := ValidateProgram(prog); len(issues) > 0 {
		msg := "vela: internal codegen error — generated AST failed validation:\n"
		for _, iss := range issues {
			msg += "  [" + iss.Kind + "] " + iss.Msg + "\n"
		}
		panic(msg)
	}

	return prog
}

// buildModuleSplice is the DEFENSIVE fallback, kept as a safety
// net only.  As of E3.6 the AST generator's recursive element
// walker handles every construct directly, and astCanBuildDirectly
// returns true for all well-formed input — so in practice this is
// never reached.  It is retained (rather than deleted with the
// rest of the splice machinery) so that any unanticipated input
// shape still produces valid output instead of a hard failure.
//
// It runs the legacy generator and re-parses the result into a
// JsProgram, so even a fallback compile flows through the unified
// AST pipeline (validation, printing).  The legacy banner is
// stripped before parsing — the caller re-adds the canonical one.
func (g *ASTGen) buildModuleSplice(
	name string,
	sfc *SFC,
	nodes []Node,
	analysis *Analysis,
	scopeID string,
) *JsProgram {
	legacy := generateCode(name, sfc, nodes, analysis, scopeID)
	return ParseJS(stripBanner(legacy))
}

// stripBanner removes the generated-by banner comment from a piece
// of generated JS, leaving the code body.  Used when re-parsing
// one generator's output for splicing or comparison so the banner
// is not duplicated.
func stripBanner(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "// Generated") {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// astCanBuildDirectly reports whether the AST generator can fully compile
// the given template with the blocks migrated so far.  Until all 14
// blocks land, generateCodeAST falls back to the legacy generator
// for anything astCanBuildDirectly rejects — this keeps the compiler
// correct throughout the migration.
//
// Blocks 2-3 status: no props/state/derived/effects (those are
// blocks 4-6).  Within that limit it handles:
//   - fully static templates (block 2)
//   - a single root element whose children are static or contain
//     non-reactive text-interpolation holes (block 3)
func astCanBuildDirectly(nodes []Node, analysis *Analysis) bool {
	if len(nodes) == 0 {
		return false
	}

	// Case A — every root node is a fully-static element (block 2).
	allStaticRoots := true
	for _, n := range nodes {
		el, ok := n.(*ElementNode)
		if !ok || !isFullyStatic(el) {
			allStaticRoots = false
			break
		}
	}
	if allStaticRoots {
		return true
	}

	// Case B — exactly one root element.
	//
	// Two sub-cases:
	//   B1 — children are static or non-reactive text holes: the
	//        AST generator builds the element directly (blocks
	//        3/7/8 + buildHolesDOM).
	//   B2 — the subtree contains <slot> or <Component> nodes
	//        (block 13): the whole element's DOM is spliced from
	//        the legacy walkElement via the sub-CodeGen, exactly
	//        as {#each}/{#await} are reused.
	if len(nodes) == 1 {
		if el, ok := nodes[0].(*ElementNode); ok {
			if el.IfCond != "" {
				return false
			}
			if el.EachExpr != "" {
				// #each-attribute on the root element — buildElement
				// reframes it to an EachBlockNode.  Claimable when
				// the row body is one buildEachBlock handles.
				return astEachRowOK([]Node{stripEachDirective(el)})
			}
			// A childless root element is fine — e.g.
			// <input bind:value={x} />.
			if len(el.Children) == 0 {
				return true
			}
			if astTextHolesOnly(el.Children) {
				return true // B1 — direct build
			}
			// B2 — slot/component subtree, reuse-able via splice.
			return astSlotComponentOK(el)
		}
	}

	// Case C — control-flow blocks at the root (blocks 9-10).
	// Every root node must be a fully-static element, an {#if}
	// chain with fully-static branches, or an {#each} block with a
	// fully-static row body.
	return astChainRootOK(nodes)
}

// eachHasComponent reports whether an {#each} row body contains a
// component node anywhere — those need block-13 slot machinery.
func eachHasComponent(nodes []Node) bool {
	for _, n := range nodes {
		switch x := n.(type) {
		case *ComponentNode:
			return true
		case *ElementNode:
			if eachHasComponent(x.Children) {
				return true
			}
		case *IfChainNode:
			for _, b := range x.Branches {
				if b.Comp != nil {
					return true
				}
				if eachHasComponent(b.Children) {
					return true
				}
			}
		case *EachBlockNode:
			if eachHasComponent(x.Children) {
				return true
			}
		}
	}
	return false
}

// awaitExprHasInlineClause reports whether an {#await} promise
// expression contains an unparsed inline `then`/`catch` clause —
// the result of the parser not handling `{#await expr then name}`
// shorthand.  Detected by a top-level ` then ` / ` catch ` token
// outside of brackets/strings.
func awaitExprHasInlineClause(expr string) bool {
	depth := 0
	var quote rune
	runes := []rune(expr)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			quote = c
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case ' ', '\t', '\n':
			if depth == 0 {
				rest := string(runes[i:])
				if hasWordPrefix(rest, " then ") || hasWordPrefix(rest, " catch ") {
					return true
				}
			}
		}
	}
	return false
}

// hasWordPrefix reports whether s begins with prefix.
func hasWordPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// astSlotComponentOK reports whether a root element whose subtree
// contains <slot>/<Component> nodes can be claimed for block 13.
//
// The whole element is spliced from the legacy walkElement, which
// handles the full template grammar — so the only thing to reject
// is the known-broken {#await x then y} shorthand if it appears
// anywhere in the subtree.
func astSlotComponentOK(el *ElementNode) bool {
	return !subtreeHasBrokenAwait([]Node{el})
}

// subtreeHasBrokenAwait reports whether any {#await} in the subtree
// uses the unparsed inline then/catch shorthand.
func subtreeHasBrokenAwait(nodes []Node) bool {
	for _, n := range nodes {
		switch x := n.(type) {
		case *ElementNode:
			if subtreeHasBrokenAwait(x.Children) {
				return true
			}
		case *AwaitBlockNode:
			if awaitExprHasInlineClause(x.PromiseExpr) {
				return true
			}
			if subtreeHasBrokenAwait(x.PendingChildren) ||
				subtreeHasBrokenAwait(x.ThenChildren) ||
				subtreeHasBrokenAwait(x.CatchChildren) {
				return true
			}
		case *IfChainNode:
			for _, br := range x.Branches {
				if subtreeHasBrokenAwait(br.Children) {
					return true
				}
			}
		case *EachBlockNode:
			if subtreeHasBrokenAwait(x.Children) {
				return true
			}
		case *ComponentNode:
			for _, kids := range x.Slots {
				if subtreeHasBrokenAwait(kids) {
					return true
				}
			}
		}
	}
	return false
}

// astChainRootOK reports whether nodes is a mix of fully-static
// elements, {#if} chains (static branches), and {#each} blocks
// (static row bodies) — the block-9/10 claimable shape.
func astChainRootOK(nodes []Node) bool {
	sawBlock := false
	for _, n := range nodes {
		switch x := n.(type) {
		case *ElementNode:
			if !isFullyStatic(x) {
				return false
			}
		case *IfChainNode:
			// Both {#if} chains (block 9) and {#key} blocks
			// (block 11) arrive as IfChainNode — the latter with a
			// non-empty KeyExpr.  A branch body may be fully static
			// or carry text-interpolation holes (E3.1); element
			// holes and nested control-flow in a branch are still
			// deferred to the splice.
			for _, b := range x.Branches {
				if b.Comp != nil {
					return false // component branch — block 13
				}
				// Normalize an element branch (old #if-attr
				// syntax) to its children for the check.
				kids := b.Children
				if b.Elem != nil {
					kids = []Node{stripBranchDirectives(b.Elem)}
				}
				if !allStatic(kids) && !astHoleSubtreeOK(kids) {
					return false
				}
			}
			sawBlock = true
		case *EachBlockNode:
			// {#each} is struct-built by buildEachBlock (E3.3).
			// The direct path covers hole-bearing-subtree rows;
			// per-row events / binds / directives and nested
			// control-flow are not yet covered.
			if !astEachRowOK(x.Children) {
				return false
			}
			sawBlock = true
		case *AwaitBlockNode:
			// {#await} is lowered to a 3-branch chain by
			// buildAwaitBlock (E3.2) — struct-built, no splice.
			// Branch holes and the {:then}/{:catch} bindings are
			// handled; component content in a branch still needs
			// block 13.
			if eachHasComponent(x.PendingChildren) ||
				eachHasComponent(x.ThenChildren) ||
				eachHasComponent(x.CatchChildren) {
				return false
			}
			// The inline `{#await expr then name}` / `{#await expr
			// catch name}` shorthand is not parsed correctly by the
			// template parser — the `then name` clause leaks into
			// PromiseExpr (a pre-existing bug, identical in both
			// generators).  Defer such blocks rather than claim a
			// knowingly-broken shape.
			if awaitExprHasInlineClause(x.PromiseExpr) {
				return false
			}
			sawBlock = true
		default:
			return false
		}
	}
	return sawBlock
}

// astTextHolesOnly reports whether a child list is compatible with
// the block-3 innerHTML+TreeWalker path: a flat list of static
// nodes and non-reactive text-interpolation holes.
//
// Nested elements are accepted only if FULLY static — an element
// that itself contains a hole needs recursive descent (legacy
// "step 2"), which is a later block.  Likewise element holes
// (dynamic attributes) are deferred to block 7.
// astEachRowOK reports whether an {#each} row body can be built by
// buildEachBlock's direct path: the row resolves to a single
// element (or a wrapper) whose content is a hole-bearing subtree.
//
// Per-row events, binds, and class:/style: directives ARE handled
// (E3.3b — buildEachRowAttrs).  What still routes to the splice is
// nested control-flow inside a row.
// astEachRowOK reports whether an {#each} row body can be built by
// buildEachBlock's direct path.  Since E3.6 the fragment builder
// uses the recursive walker for rows containing nested control-
// flow, so the only hard requirement is that the row resolves to
// a single element (or a wrapper) the walker can construct — which
// it always can.  This stays a function (rather than `return
// true`) as the seam for any future row shape that genuinely
// cannot be struct-built.
func astEachRowOK(children []Node) bool {
	var rowElem *ElementNode
	if len(children) == 1 {
		if el, ok := children[0].(*ElementNode); ok {
			rowElem = el
		}
	}
	if rowElem == nil {
		// Wrapped row — the recursive walker builds the wrapper's
		// children; always constructible.
		return true
	}
	// The row element must not itself carry control-flow (that is
	// a different node shape — handled before reaching here).
	if rowElem.IfCond != "" || rowElem.EachExpr != "" {
		return false
	}
	return true
}

// astHoleSubtreeOK reports whether a subtree can be built by
// buildHoleSubtree: static content and text-interpolation holes at
// any depth, but no element holes, nested control-flow,
// components, or dynamic attributes (events / binds / directives).
//
// Unlike astTextHolesOnly (block 3, which requires nested elements
// to be fully static), this permits a hole inside a nested element
// — buildHoleSubtree's innerHTML + TreeWalker pass captures holes
// at any depth.  What it cannot do is element-level dynamics, so
// those still disqualify the subtree.
func astHoleSubtreeOK(nodes []Node) bool {
	for _, node := range nodes {
		switch n := node.(type) {
		case *TextNode:
			// Any text (static or hole) is fine.
		case *ElementNode:
			// Dynamic attributes need per-element wiring the
			// innerHTML path cannot express.
			for _, a := range n.Attrs {
				if a.IsEvent || a.IsBind || a.IsTwoWay ||
					a.IsBindThis || a.IsClassDir || a.IsStyleDir {
					return false
				}
			}
			if n.IfCond != "" || n.EachExpr != "" {
				return false
			}
			if !astHoleSubtreeOK(n.Children) {
				return false
			}
		default:
			// IfChainNode, EachBlockNode, AwaitBlockNode,
			// ComponentNode, SlotNode — out of scope.
			return false
		}
	}
	return true
}

func astTextHolesOnly(nodes []Node) bool {
	for _, node := range nodes {
		switch n := node.(type) {
		case *TextNode:
			for _, p := range n.Parts {
				if p.Static {
					continue
				}
				// Dynamic part — must have no reactive dependency.
				if len(findDeps(p.Value, nil)) > 0 {
					return false
				}
			}
		case *ElementNode:
			// A nested element must be entirely static; anything
			// with its own holes or dynamic behavior is out of
			// scope for block 3.
			if !isFullyStatic(n) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// buildModule constructs the whole component module: the exported
// function wrapper and everything inside it.
func (g *ASTGen) buildModule(nodes []Node) *JsProgram {
	prog := &JsProgram{}

	// ── Module-scope imports (block 4) ───────────────────────
	// User imports are hoisted out of the component function to
	// module scope, matching ES module semantics and the legacy
	// generator.  Each is built as a real JsImportDecl by parsing
	// the reconstructed import statement.
	for _, imp := range g.buildImportDecls() {
		prog.Body = append(prog.Body, imp)
	}

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

	// ── Deep-reactivity helper (block 14) ────────────────────
	// Emitted only when at least one $state.deep variable exists.
	// $$proxy wraps a value in a recursive, WeakMap-cached Proxy
	// so nested mutations fire the same notify path as
	// reassignment.
	if g.hasDeepState() {
		fnBody.Body = append(fnBody.Body, jsStmts(`
			function $$proxy(val, notify) {
				if (val === null || typeof val !== 'object') return val;
				const cache = new WeakMap();
				const wrap = (v) => {
					if (v === null || typeof v !== 'object') return v;
					let p = cache.get(v);
					if (p) return p;
					p = new Proxy(v, {
						get(t, k) { return wrap(t[k]); },
						set(t, k, x) { t[k] = x; notify(); return true; },
						deleteProperty(t, k) { delete t[k]; notify(); return true; },
					});
					cache.set(v, p);
					return p;
				};
				return wrap(val);
			}
		`)...)
	}

	// ── Scoped <style> injection ─────────────────────────────
	// A component <style> block is scope-transformed (each
	// selector gains the [vela-xxxxxx] attribute) and injected as
	// a <style> element into document.head.  $$style is removed in
	// destroy().  Reuses the legacy ScopeCSS transform so the
	// scoping rules stay single-sourced.
	fnBody.Body = append(fnBody.Body, g.buildStyleInjection()...)

	// ── Props (block 5) ──────────────────────────────────────
	// Each prop becomes a `let` initialized from $$props with its
	// default, declared before DOM so holes and the user script
	// can read it.
	fnBody.Body = append(fnBody.Body, g.buildPropDecls()...)

	// ── State (block 5) ──────────────────────────────────────
	fnBody.Body = append(fnBody.Body, g.buildStateDecls()...)

	// ── Derived (block 6) ────────────────────────────────────
	// Each $derived becomes an uninitialized `let` plus an
	// $$upd_X updater.  The updater is invoked once before initial
	// render and again from the setter of any dependency.
	fnBody.Body = append(fnBody.Body, g.buildDerivedDecls()...)

	// ── Effects (block 6) ────────────────────────────────────
	// Each $effect becomes a $$cleanupN holder plus an $$effectN
	// runner that disposes the previous cleanup and re-runs the
	// body (an IIFE whose return value is the new cleanup).
	fnBody.Body = append(fnBody.Body, g.buildEffectDecls()...)

	// ── DOM construction (blocks 2-3) ────────────────────────
	g.buildDOM(nodes)
	fnBody.Body = append(fnBody.Body, g.body...)

	// ── Component logic — user <script> (block 4) ────────────
	fnBody.Body = append(fnBody.Body, g.buildUserScript()...)

	// ── Reactive setters (blocks 5-6) ────────────────────────
	fnBody.Body = append(fnBody.Body, g.buildSetters()...)

	// ── Event listeners (block 7) ────────────────────────────
	// addEventListener calls for @event handlers and the input
	// listeners powering bind:.  After setters so handlers (which
	// call $$set_X) are in scope.
	fnBody.Body = append(fnBody.Body, g.events...)

	// ── Initialise derived (block 6) ─────────────────────────
	// Run every derived updater once so derived values are valid
	// before the initial render reads them.
	fnBody.Body = append(fnBody.Body, g.buildDerivedInit()...)

	// ── Initial render (block 3) ─────────────────────────────
	fnBody.Body = append(fnBody.Body, g.initBody...)

	// ── Run effects once (block 6) ───────────────────────────
	fnBody.Body = append(fnBody.Body, g.buildEffectInit()...)

	// ── Mount ────────────────────────────────────────────────
	fnBody.Body = append(fnBody.Body,
		jsStmt(`$$target.appendChild($$root);`))

	// ── onMount callbacks (stage 1a) ─────────────────────────
	// Run after the component's DOM is in the document.  Each
	// callback body is wrapped in an IIFE so a `return fn` inside
	// it is captured as that mount's cleanup function rather than
	// returning from the component factory.
	fnBody.Body = append(fnBody.Body, g.buildOnMount()...)

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

// buildImportDecls reconstructs each user import as a JsImportDecl.
//
// The analyzer stores imports as (Spec, Source) string pairs.  We
// rebuild the import-statement source text — applying the same
// path rewriting the legacy generator does (.vela→.js, the vela
// runtime alias) — and parse it back into a real AST node, so the
// import participates in the unified tree like everything else.
func (g *ASTGen) buildImportDecls() []JsStmt {
	if g.analysis == nil {
		return nil
	}
	var out []JsStmt
	for _, imp := range g.analysis.Imports {
		src := imp.Source
		if src == "vela/store" || src == "vela" {
			src = "./runtime.js"
		} else {
			src = RewriteImportPath(src)
		}
		var stmtSrc string
		if imp.Spec == "" {
			stmtSrc = "import '" + src + "';"
		} else {
			stmtSrc = "import " + imp.Spec + " from '" + src + "';"
		}
		out = append(out, jsStmt(stmtSrc))
	}
	return out
}

// buildUserScript parses the analyzer's CleanScript — the user's
// <script> with $state/$derived/imports already stripped and
// assignments rewritten — into AST statements spliced straight
// into the component function body.
//
// This is the heart of block 4: user-authored code becomes part
// of the same tree as the generated wiring, so later validation
// and optimization passes see across the boundary.  Nodes keep
// OriginUser (jsStmts would stamp them generated, so we parse
// directly and leave the origin alone).
func (g *ASTGen) buildUserScript() []JsStmt {
	if g.analysis == nil || g.analysis.CleanScript == "" {
		return nil
	}
	prog := ParseJS(g.analysis.CleanScript)
	// Leave Origin as OriginUser — these nodes genuinely are the
	// developer's code, and downstream passes rely on that mark.
	return prog.Body
}

// reactiveNames returns the names of all reactive variables (state
// + props) — used to scope dependency detection in hole
// expressions.
func (g *ASTGen) reactiveNames() []string {
	if g.analysis == nil {
		return nil
	}
	return g.analysis.reactiveNames()
}

// buildPropDecls emits one `let` per prop, initialized from $$props
// with the declared default.  A prop with no default is required
// and initialized directly from $$props.
// buildPropDecls emits one `let` per prop, plus — for each
// $bindable prop — a `const $$onChange_X` callback handle.
//
// The $$onChange_X callback is supplied by a parent that two-way-
// binds the prop (<Child bind:x={…}>); it is null when the prop is
// passed one-way.  The child's setter for a bindable prop fires it
// so the parent observes child-side changes.
func (g *ASTGen) buildPropDecls() []JsStmt {
	if g.analysis == nil {
		return nil
	}
	var out []JsStmt
	for _, p := range g.analysis.Props {
		var src string
		if p.Default != "" {
			src = "let " + p.Name + " = $$props." + p.Name +
				" !== undefined ? $$props." + p.Name + " : " + p.Default + ";"
		} else {
			src = "let " + p.Name + " = $$props." + p.Name + ";"
		}
		out = append(out, jsStmt(src))
	}
	// $$onChange handles for bindable props.
	for _, p := range g.analysis.Props {
		if p.Bindable {
			out = append(out, jsStmt(
				"const $$onChange_"+p.Name+
					" = $$props.$$onChange_"+p.Name+" || null;"))
		}
	}
	return out
}

// hasStyle reports whether the component has a <style> block.
func (g *ASTGen) hasStyle() bool {
	return g.sfc != nil && g.sfc.Style != ""
}

// buildStyleInjection emits the scoped-<style> section: a <style>
// element whose textContent is the scope-transformed CSS, appended
// to document.head.  Empty when the component has no <style>.
func (g *ASTGen) buildStyleInjection() []JsStmt {
	if !g.hasStyle() {
		return nil
	}
	css := ScopeCSS(g.sfc.Style, g.scopeID)
	return []JsStmt{
		jsStmt("const $$style = document.createElement('style');"),
		jsStmt("$$style.textContent = " + backtickString(css) + ";"),
		jsStmt("document.head.appendChild($$style);"),
	}
}

// hasDeepState reports whether any state variable was declared
// via $state.deep — i.e. whether the $$proxy helper is needed.
func (g *ASTGen) hasDeepState() bool {
	if g.analysis == nil {
		return false
	}
	for _, sv := range g.analysis.StateVars {
		if sv.Deep {
			return true
		}
	}
	return false
}

// buildStateDecls emits one declaration per $state variable.
//
// Plain $state:  let X = <initial>;
//
// $state.deep:   const $$mut_X = () => $$set_X_notify();
//                let X = $$proxy(<initial>, $$mut_X);
// The value is Proxy-wrapped so nested mutations route through the
// same notify path as reassignment; $$mut_X is the mutation
// callback the Proxy traps invoke.
func (g *ASTGen) buildStateDecls() []JsStmt {
	if g.analysis == nil {
		return nil
	}
	var out []JsStmt
	for _, sv := range g.analysis.StateVars {
		init := sv.Initial
		if init == "" {
			init = "undefined"
		}
		if sv.Deep {
			out = append(out, jsStmt(
				"const $$mut_"+sv.Name+" = () => $$set_"+sv.Name+"_notify();"))
			out = append(out, jsStmt(
				"let "+sv.Name+" = $$proxy("+init+", $$mut_"+sv.Name+");"))
		} else {
			out = append(out, jsStmt("let "+sv.Name+" = "+init+";"))
		}
	}
	return out
}

// buildDerivedDecls emits, per $derived var, an uninitialized
// `let X;` and a `const $$upd_X = () => { X = <expr>; };` updater.
// buildDerivedDecls emits, per $derived var, an uninitialized
// `let X;` and a `const $$upd_X = () => { X = expr; };` updater.
//
// Emission is two-pass — every `let` first, then every `$$upd_*` —
// matching the legacy generator.  An updater for a chained derived
// reads earlier derived names, so all declarations must precede
// all updaters; interleaving them still works at runtime (updaters
// only run later) but diverges structurally.
func (g *ASTGen) buildDerivedDecls() []JsStmt {
	if g.analysis == nil {
		return nil
	}
	var out []JsStmt
	for _, dv := range g.analysis.DerivedVars {
		out = append(out, jsStmt("let "+dv.Name+";"))
	}
	for _, dv := range g.analysis.DerivedVars {
		out = append(out, jsStmt(
			"const $$upd_"+dv.Name+" = () => { "+dv.Name+" = "+dv.Expr+"; };"))
	}
	return out
}

// buildDerivedInit emits one `$$upd_X();` per derived var, run
// before the initial render so derived values are populated.
func (g *ASTGen) buildDerivedInit() []JsStmt {
	if g.analysis == nil {
		return nil
	}
	var out []JsStmt
	for _, dv := range g.analysis.DerivedVars {
		out = append(out, jsStmt("$$upd_"+dv.Name+"();"))
	}
	return out
}

// buildEffectDecls emits, per $effect, a `let $$cleanupN = null;`
// holder and a `const $$effectN = () => { … };` runner.
//
// The runner disposes any previous cleanup, then re-runs the
// effect body as an IIFE whose return value (if any) becomes the
// new cleanup function — matching the legacy generator.
func (g *ASTGen) buildEffectDecls() []JsStmt {
	if g.analysis == nil {
		return nil
	}
	var out []JsStmt
	for i, eff := range g.analysis.Effects {
		n := itoa(i)
		out = append(out, jsStmt("let $$cleanup"+n+" = null;"))
		out = append(out, jsStmt(
			"const $$effect"+n+" = () => {"+
				"  if ($$cleanup"+n+") { $$cleanup"+n+"(); $$cleanup"+n+" = null; }"+
				"  $$cleanup"+n+" = (function() {"+eff.Body+"})();"+
				"};"))
	}
	return out
}

// buildEffectInit emits one `$$effectN();` per effect, run once
// after the initial render.
func (g *ASTGen) buildEffectInit() []JsStmt {
	if g.analysis == nil {
		return nil
	}
	var out []JsStmt
	for i := range g.analysis.Effects {
		out = append(out, jsStmt("$$effect"+itoa(i)+"();"))
	}
	return out
}

// buildOnMount emits, per onMount lifecycle hook, an IIFE-wrapped
// callback whose return value (if any) is captured as that mount's
// cleanup function:
//
//   const $$mountCleanup0 = (function() { …body… })();
//
// The IIFE matters: a `return fn` inside an onMount callback is the
// cleanup return (like an $effect cleanup), NOT the component
// factory's { destroy, set } return.  Without the wrapper the
// return would exit the whole component function.
func (g *ASTGen) buildOnMount() []JsStmt {
	if g.analysis == nil {
		return nil
	}
	var out []JsStmt
	for i, lc := range g.analysis.Lifecycles {
		if lc.Kind != "onMount" {
			continue
		}
		out = append(out, jsStmt(
			"const $$mountCleanup"+itoa(i)+
				" = (function() {"+lc.Body+"})();"))
	}
	return out
}

// buildOnDestroyCleanup returns the statements that run inside
// destroy(): each onMount cleanup function (invoked if it is a
// function), then every onDestroy callback body inline.  Effect
// cleanup and event-listener cleanup are emitted separately by
// buildReturn around these.
func (g *ASTGen) buildOnDestroyCleanup() []JsStmt {
	if g.analysis == nil {
		return nil
	}
	var out []JsStmt
	// onMount cleanup functions.
	for i, lc := range g.analysis.Lifecycles {
		if lc.Kind != "onMount" {
			continue
		}
		cv := "$$mountCleanup" + itoa(i)
		out = append(out, jsStmt(
			"if (typeof "+cv+" === 'function') { "+cv+"(); }"))
	}
	// onDestroy callback bodies, inline.
	for _, lc := range g.analysis.Lifecycles {
		if lc.Kind != "onDestroy" {
			continue
		}
		out = append(out, jsStmts(lc.Body)...)
	}
	return out
}

// buildSetters emits the reactive-setter section: the $$defer
// hookup followed by one $$set_X per state var and per prop.
//
// Each setter has the shape the legacy generator produces for the
// non-deep, non-derived case:
//
//   function $$set_X($$v) {
//     if (X === $$v) return;            // cycle break + no-op skip
//     X = $$v;
//     $$defer(() => { …DOM updates… }); // only if X drives a hole
//   }
//
// The DOM updates are the statements addUpdate collected for X
// while holes were built.
func (g *ASTGen) buildSetters() []JsStmt {
	if g.analysis == nil {
		return nil
	}
	if len(g.analysis.StateVars) == 0 && len(g.analysis.Props) == 0 {
		return nil
	}
	var out []JsStmt

	// $$defer hookup — pulled from the runtime, with a synchronous
	// fallback so a component works even without the batch runtime.
	out = append(out, jsStmt(
		`const $$defer = globalThis[Symbol.for('vela.defer')] ?? (fn => fn());`))

	for _, sv := range g.analysis.StateVars {
		if sv.Deep {
			out = append(out, g.buildDeepSetters(sv.Name)...)
		} else {
			out = append(out, g.buildOneSetter(sv.Name))
		}
	}
	for _, p := range g.analysis.Props {
		out = append(out, g.buildOneSetter(p.Name))
	}
	return out
}

// buildSetterCascade builds the reactive cascade for a state/prop
// var: recompute dependent derived, $$defer the DOM updates
// (direct + transitive), re-run dependent effects.  Shared by the
// plain combined setter and the deep-state notify function.
func (g *ASTGen) buildSetterCascade(name string) []JsStmt {
	var out []JsStmt

	// 0. Bindable-prop notification.  If `name` is a $bindable
	//    prop, fire its $$onChange callback so a two-way-binding
	//    parent observes the change.  Runs first, before the local
	//    reactive cascade.
	if g.analysis != nil {
		for _, p := range g.analysis.Props {
			if p.Name == name && p.Bindable {
				out = append(out, jsStmt(
					"if ($$onChange_"+name+") $$onChange_"+name+"("+name+");"))
			}
		}
	}

	// 1. Recompute dependent derived vars — the full transitive
	//    closure, in declaration order.  A change to `name` must
	//    refresh not only derived that read `name` directly but
	//    also derived that read those, and so on.  DerivedVars is
	//    already in declaration (topological) order, so a single
	//    forward pass that grows the dirty set as it goes
	//    recomputes each derived exactly once, after its inputs.
	dirty := map[string]bool{name: true}
	var dependentDerived []*DerivedVar
	if g.analysis != nil {
		for _, dv := range g.analysis.DerivedVars {
			depDirty := false
			for _, d := range dv.Deps {
				if dirty[d] {
					depDirty = true
					break
				}
			}
			if depDirty {
				dirty[dv.Name] = true
				dependentDerived = append(dependentDerived, dv)
				out = append(out, jsStmt("$$upd_"+dv.Name+"();"))
			}
		}
	}

	// 2. DOM updates — direct (read name) + transitive (read a
	//    dependent derived).  De-duplicated by source text.
	seen := map[string]bool{}
	var updates []JsStmt
	add := func(stmts []JsStmt) {
		for _, s := range stmts {
			key := PrintJS(&JsProgram{Body: []JsStmt{s}}, PrintMinified)
			if seen[key] {
				continue
			}
			seen[key] = true
			updates = append(updates, cloneStmt(s))
		}
	}
	add(g.updates[name])
	for _, dv := range dependentDerived {
		add(g.updates[dv.Name])
	}
	if len(updates) > 0 {
		deferBody := &JsBlock{baseStmt: baseStmt{genNode()}}
		deferBody.Body = append(deferBody.Body, updates...)
		out = append(out, &JsExprStmt{
			baseStmt: baseStmt{genNode()},
			X: &JsCallExpr{
				baseExpr: baseExpr{genNode()},
				Callee:   id("$$defer"),
				Args: []JsExpr{&JsArrowFunc{
					baseExpr: baseExpr{genNode()},
					Body:     deferBody,
				}},
			},
		})
	}

	// 3. Re-run dependent effects.
	if g.analysis != nil {
		for i, eff := range g.analysis.Effects {
			if containsStr(eff.Deps, name) {
				out = append(out, jsStmt("$$effect"+itoa(i)+"();"))
			}
		}
	}
	return out
}

// buildOneSetter constructs a single $$set_<name> function for a
// plain ($state / prop) reactive variable: a guard, the
// assignment, then the reactive cascade.
func (g *ASTGen) buildOneSetter(name string) JsStmt {
	body := &JsBlock{baseStmt: baseStmt{genNode()}}

	// if (<name> === $$v) return;
	body.Body = append(body.Body, &JsIfStmt{
		baseStmt: baseStmt{genNode()},
		Test: &JsBinaryExpr{
			baseExpr: baseExpr{genNode()},
			Op:       "===",
			L:        id(name),
			R:        id("$$v"),
		},
		Then: &JsReturnStmt{baseStmt: baseStmt{genNode()}},
	})

	// <name> = $$v;
	body.Body = append(body.Body, &JsExprStmt{
		baseStmt: baseStmt{genNode()},
		X: &JsAssignExpr{
			baseExpr: baseExpr{genNode()},
			Op:       "=",
			L:        id(name),
			R:        id("$$v"),
		},
	})

	body.Body = append(body.Body, g.buildSetterCascade(name)...)

	return &JsFuncDecl{
		baseStmt: baseStmt{genNode()},
		Name:     "$$set_" + name,
		Params:   []JsExpr{id("$$v")},
		Body:     body,
	}
}

// buildDeepSetters constructs the split setter pair for a
// $state.deep variable:
//
//   function $$set_X_notify() { …reactive cascade… }
//   function $$set_X($$v) {
//     if (X === $$v) return;
//     X = $$proxy($$v, $$mut_X);   // re-wrap the new value
//     $$set_X_notify();
//   }
//
// The notify function is what the Proxy's mutation traps call (via
// $$mut_X), so a nested mutation triggers the same cascade as a
// full reassignment.
func (g *ASTGen) buildDeepSetters(name string) []JsStmt {
	// $$set_X_notify — the cascade body.
	notifyBody := &JsBlock{baseStmt: baseStmt{genNode()}}
	notifyBody.Body = append(notifyBody.Body, g.buildSetterCascade(name)...)
	notify := &JsFuncDecl{
		baseStmt: baseStmt{genNode()},
		Name:     "$$set_" + name + "_notify",
		Body:     notifyBody,
	}

	// $$set_X — guard, re-wrap, notify.
	setBody := &JsBlock{baseStmt: baseStmt{genNode()}}
	setBody.Body = append(setBody.Body, &JsIfStmt{
		baseStmt: baseStmt{genNode()},
		Test: &JsBinaryExpr{
			baseExpr: baseExpr{genNode()},
			Op:       "===",
			L:        id(name),
			R:        id("$$v"),
		},
		Then: &JsReturnStmt{baseStmt: baseStmt{genNode()}},
	})
	setBody.Body = append(setBody.Body,
		jsStmt(name+" = $$proxy($$v, $$mut_"+name+");"))
	setBody.Body = append(setBody.Body,
		jsStmt("$$set_"+name+"_notify();"))
	set := &JsFuncDecl{
		baseStmt: baseStmt{genNode()},
		Name:     "$$set_" + name,
		Params:   []JsExpr{id("$$v")},
		Body:     setBody,
	}

	return []JsStmt{notify, set}
}

// cloneStmt re-parses a statement so the same update can appear in
// multiple setters without aliasing one node pointer across the
// tree.  Statements here are always compiler-generated and small,
// so a print+reparse round-trip is cheap and exact.
func cloneStmt(s JsStmt) JsStmt {
	src := PrintJS(&JsProgram{Body: []JsStmt{s}}, PrintPretty)
	return jsStmt(src)
}

// buildDOM emits the DOM-construction statements for the template
// into g.body (and any initial-render statements into g.initBody).
//
// Dispatches on the shape astCanBuildDirectly already validated:
//   - all roots static  → buildStaticDOM (block 2)
//   - single root with text holes → buildHolesDOM (block 3)
func (g *ASTGen) buildDOM(nodes []Node) {
	allStaticRoots := true
	for _, n := range nodes {
		el, ok := n.(*ElementNode)
		if !ok || !isFullyStatic(el) {
			allStaticRoots = false
			break
		}
	}
	if allStaticRoots {
		g.buildStaticDOM(nodes)
		return
	}

	// If any root node is a control-flow block ({#if} / {#each} /
	// {#await}), use the multi-root wrapper path: a
	// display:contents <div> holds the blocks' anchors and any
	// static siblings.
	for _, n := range nodes {
		switch n.(type) {
		case *IfChainNode, *EachBlockNode, *AwaitBlockNode:
			g.buildChainRootDOM(nodes)
			return
		}
	}

	// Single root element.  If its children are static or text
	// holes, buildHolesDOM handles it (blocks 3/7/8).  Otherwise
	// the recursive element walker (buildElement, E3.4) constructs
	// the whole subtree — nested dynamic elements, components,
	// slots — as struct construction, no splice.
	el := nodes[0].(*ElementNode)
	// An #each / #if directive on the root element makes it a
	// control-flow block — it must NOT be treated as a plain
	// element by the text-hole path below.  buildElement reframes
	// #each to an EachBlockNode; the wrapper is built here.
	if el.EachExpr != "" {
		// Reframe the #each-attribute element as an EachBlockNode
		// and route it through buildChainRootDOM, which builds the
		// display:contents $$root wrapper the block needs.
		g.buildChainRootDOM([]Node{&EachBlockNode{
			EachExpr: el.EachExpr,
			EachKey:  el.EachKey,
			Children: []Node{stripEachDirective(el)},
		}})
		return
	}
	if astTextHolesOnly(el.Children) || len(el.Children) == 0 {
		g.buildHolesDOM(el)
		return
	}
	// The legacy walkNodes consumes one id() for the root element
	// before naming it $$root — match that so $$nN numbering of
	// the subtree aligns with legacy.
	_ = g.id()
	g.buildElement(el, "$$root", "$$target", true)
}

// buildChainRootDOM builds DOM for a root list containing one or
// more {#if} chains (block 9), possibly mixed with fully-static
// sibling elements.  Everything goes inside a display:contents
// wrapper so the chains have a stable parent and document order is
// preserved.
func (g *ASTGen) buildChainRootDOM(nodes []Node) {
	// The wrapper <div> is named $$root literally — legacy does not
	// call id() for it, so the chain numbering must not either.
	g.body = append(g.body,
		jsStmt(`const $$root = document.createElement('div');`))
	g.body = append(g.body, g.setAttrStmt("$$root", g.scopeID, ""))
	g.body = append(g.body,
		jsStmt(`$$root.style.display = 'contents';`))

	for _, n := range nodes {
		switch x := n.(type) {
		case *ElementNode:
			// A static sibling element — serialize via insertHTML
			// so document order between it and the chains holds.
			html := emitStaticHTML([]Node{x}, g.scopeID)
			g.body = append(g.body, &JsExprStmt{
				baseStmt: baseStmt{genNode()},
				X: &JsCallExpr{
					baseExpr: baseExpr{genNode()},
					Callee:   id("$$insertHTML"),
					Args: []JsExpr{
						id("$$root"),
						strLit(escapeJSString(html)),
					},
				},
			})
		case *IfChainNode:
			g.buildIfChain(x, "$$root")
		case *EachBlockNode:
			g.buildEachBlock(x, "$$root")
		case *AwaitBlockNode:
			g.buildAwaitBlock(x, "$$root")
		}
	}
}

// holeSubtree is the result of building a hole-bearing element
// subtree — see buildHoleSubtree.
type holeSubtree struct {
	// decls are component-scope `let X = null;` declarations for
	// the captured nodes (TreeWalker, anchors, text nodes).  Empty
	// for the const/$$root case.
	decls []JsStmt
	// build are the DOM-construction statements: innerHTML, the
	// TreeWalker pass, per-hole createTextNode + replaceChild.
	build []JsStmt
	// initUpdates are the initial-render hole writes ($$nN.data = …).
	initUpdates []JsStmt
	// captured lists the names of every node variable captured —
	// used by a chain branch to null them on unmount.
	captured []string
	// updateStmts holds the per-hole reactive writes when
	// buildHoleSubtree is asked to collect rather than register
	// them (the {#each} fragment case — the row update() re-runs
	// every write unconditionally, so they are not registered
	// against component-scope reactive deps).
	updateStmts []JsStmt
}

// buildHoleSubtree builds the innerHTML + TreeWalker hole-capture
// pattern for an element subtree, parameterised so it serves both
// the root element (buildHolesDOM) and an {#if}/{#key} chain
// branch (buildIfChain).
//
//	targetVar — the element variable the children are built into
//	            ("$$root", or "$$chain_X_bI" for a branch)
//	children  — the subtree's child nodes
//	hoist     — when true, captured node variables are declared at
//	            component scope as `let X = null;` (a branch needs
//	            this so the setter can reach them); when false they
//	            are `const` locals inline (the $$root case)
//	guard     — when non-empty, every reactive hole update is
//	            wrapped `if (<guard>) …` so it is a no-op while the
//	            branch is unmounted; empty for $$root
//
// Updates are registered in g.updates against each hole's reactive
// dependencies, exactly as before.
func (g *ASTGen) buildHoleSubtree(
	targetVar string,
	children []Node,
	hoist bool,
	guard string,
	collectUpdates bool,
) holeSubtree {
	var res holeSubtree

	html, holes := buildHoleTemplate(children, g.scopeID)
	if len(children) == 0 {
		return res
	}
	if len(holes) == 0 {
		// Fully static subtree — innerHTML for element-bearing
		// runs, direct text nodes for text-only runs.
		if hasElement(children) {
			res.build = append(res.build, g.innerHTMLStmt(targetVar, html))
		} else {
			res.build = append(res.build,
				g.staticTextChildStmts(targetVar, children)...)
		}
		return res
	}

	// Holes present.  Inside an {#each} fragment, a text-only
	// subtree (no nested elements) is built as direct text nodes —
	// createTextNode + appendChild, one per TextNode, no innerHTML
	// / TreeWalker.  This matches the legacy fragment generator.
	// The root-element path keeps innerHTML+TreeWalker (legacy
	// does too), so this fast path is gated on fragment mode.
	if g.fragCounter >= 0 && !hasElement(children) {
		for _, child := range children {
			tn, ok := child.(*TextNode)
			if !ok {
				continue
			}
			if textNodeAllStatic(tn) {
				text := ""
				for _, p := range tn.Parts {
					text += p.Value
				}
				if isBlankText(text) {
					continue
				}
				res.build = append(res.build, jsStmt(
					targetVar+".appendChild(document.createTextNode("+
						backtickString(text)+"));"))
				continue
			}
			// Dynamic TextNode — one text-node hole.
			refVar := g.id()
			if hoist {
				res.decls = append(res.decls,
					jsStmt("let "+refVar+" = null;"))
				res.build = append(res.build, jsStmt(
					refVar+" = document.createTextNode('');"))
				res.captured = append(res.captured, refVar)
			} else {
				res.build = append(res.build, jsStmt(
					"const "+refVar+" = document.createTextNode('');"))
			}
			res.build = append(res.build, jsStmt(
				targetVar+".appendChild("+refVar+");"))

			exprSrc := buildExpr(tn.Parts)
			writeSrc := refVar + ".data = " + exprSrc + ";"
			res.initUpdates = append(res.initUpdates,
				g.guardedStmt(writeSrc, guard))
			if collectUpdates {
				res.updateStmts = append(res.updateStmts, jsStmt(writeSrc))
			} else {
				for _, dep := range findDeps(exprSrc, g.reactiveNames()) {
					g.addUpdate(dep, g.guardedStmt(writeSrc, guard))
				}
			}
		}
		return res
	}

	// Element-bearing subtree with holes — innerHTML carries the
	// <!--$--> anchors.
	res.build = append(res.build, g.innerHTMLStmt(targetVar, html))

	// TreeWalker over the target; capture every anchor first
	// (capture-then-mutate is load-bearing — mutating invalidates
	// a live TreeWalker).
	twVar := targetVar + "_tw"
	if hoist {
		res.decls = append(res.decls, jsStmt("let "+twVar+" = null;"))
		res.build = append(res.build, jsStmt(
			twVar+" = document.createTreeWalker("+targetVar+", 128);"))
		res.captured = append(res.captured, twVar)
	} else {
		res.build = append(res.build, jsStmt(
			"const "+twVar+" = document.createTreeWalker("+targetVar+", 128);"))
	}

	anchors := make([]string, len(holes))
	for i := range holes {
		anchors[i] = g.id() + "_a"
		decl := anchors[i] + " = " + twVar + ".nextNode();"
		if hoist {
			res.decls = append(res.decls, jsStmt("let "+anchors[i]+" = null;"))
			res.build = append(res.build, jsStmt(anchors[i]+" = "+twVar+".nextNode();"))
			res.captured = append(res.captured, anchors[i])
		} else {
			res.build = append(res.build, jsStmt("const "+decl))
		}
	}

	// Per hole: createTextNode + replaceChild, and the initial /
	// reactive update.
	for i, h := range holes {
		if h.kind != "text" {
			continue // element holes — not yet handled
		}
		textNode := h.node.(*TextNode)
		refVar := g.id()
		anchor := anchors[i]

		if hoist {
			res.decls = append(res.decls, jsStmt("let "+refVar+" = null;"))
			res.build = append(res.build, jsStmt(
				refVar+" = document.createTextNode('');"))
			res.captured = append(res.captured, refVar)
		} else {
			res.build = append(res.build, jsStmt(
				"const "+refVar+" = document.createTextNode('');"))
		}
		res.build = append(res.build, jsStmt(
			anchor+".parentNode.replaceChild("+refVar+", "+anchor+");"))

		exprSrc := buildExpr(textNode.Parts)
		writeSrc := refVar + ".data = " + exprSrc + ";"

		// Initial render write — guarded too, for symmetry with the
		// setter path (a branch may be unmounted at init time).
		res.initUpdates = append(res.initUpdates,
			g.guardedStmt(writeSrc, guard))

		// Reactive updates.  For a chain branch they are registered
		// against the component-scope setters, mounted-guarded.
		// For an {#each} fragment (collectUpdates) the row update()
		// re-runs every write unconditionally, so collect them
		// instead — one write per hole, deduplicated across deps.
		if collectUpdates {
			res.updateStmts = append(res.updateStmts, jsStmt(writeSrc))
		} else {
			for _, dep := range findDeps(exprSrc, g.reactiveNames()) {
				g.addUpdate(dep, g.guardedStmt(writeSrc, guard))
			}
		}
	}
	return res
}

// textNodeAllStatic reports whether every part of a TextNode is
// static (no interpolation hole).
func textNodeAllStatic(tn *TextNode) bool {
	for _, p := range tn.Parts {
		if !p.Static {
			return false
		}
	}
	return true
}

// eachRowAttrs is the result of building a row element's
// attributes for an {#each} fragment — see buildEachRowAttrs.
type eachRowAttrs struct {
	// create runs once when the row is built (static attributes,
	// addEventListener calls).
	create []JsStmt
	// update runs once at build AND on every reconcile update()
	// (class:/style: directives, bound attributes).
	update []JsStmt
}

// buildEachRowAttrs builds the per-row attribute wiring for an
// {#each} row element, mirroring the attribute handling in legacy
// walkEach.  Event handlers have their row-item references
// rewritten (rewriteItemRef) so they close over the row's mutable
// $$item box rather than the stale forEach parameter.
//
//	rowVar  — the row element variable ($$f0)
//	itemVar — the loop binding name
//	boxVar  — the row's mutable item box ($$item_$$nN)
func (g *ASTGen) buildEachRowAttrs(
	rowElem *ElementNode, rowVar, itemVar, boxVar string,
) eachRowAttrs {
	var res eachRowAttrs
	for _, attr := range rowElem.Attrs {
		switch {
		case attr.IsEvent:
			handler := rewriteItemRef(stripBraces(attr.Value), itemVar, boxVar)
			if isSimpleIdent(handler) {
				// Bare identifier — reference directly.
				res.create = append(res.create, jsStmt(
					rowVar+".addEventListener('"+attr.Name+"', "+handler+");"))
			} else {
				// Expression handler — hoist into a named const so
				// addEventListener/removeEventListener share one
				// reference (matches legacy walkEach).
				hv := rowVar + "_" + attr.Name + "_h"
				if isHandlerExpr(handler) {
					res.create = append(res.create, jsStmt(
						"const "+hv+" = "+handler+";"))
				} else {
					res.create = append(res.create, jsStmt(
						"const "+hv+" = ($event) => { "+handler+"; };"))
				}
				res.create = append(res.create, jsStmt(
					rowVar+".addEventListener('"+attr.Name+"', "+hv+");"))
			}

		case attr.IsClassDir:
			stmt := classDirStmt(rowVar, attr.Name,
				rewriteItemRef(attr.Value, itemVar, boxVar), "")
			res.update = append(res.update, jsStmt(stmt))

		case attr.IsStyleDir:
			stmt := styleDirStmt(rowVar, attr.Name,
				rewriteItemRef(attr.Value, itemVar, boxVar), "")
			res.update = append(res.update, jsStmt(stmt))

		case attr.IsBind:
			stmt := bindAttrStmt(rowVar, attr.Name,
				rewriteItemRef(attr.Value, itemVar, boxVar), "")
			res.update = append(res.update, jsStmt(stmt))

		case attr.IsBindThis:
			// bind:this on an each-row would overwrite the same ref
			// on every iteration — legacy skips it, so do we.

		default:
			// Plain static attribute.
			res.create = append(res.create, jsStmt(
				rowVar+".setAttribute('"+attr.Name+"', "+
					backtickString(attr.Value)+");"))
		}
	}
	return res
}

// guardedStmt parses src into a statement, wrapping it
// `if (<guard>) …` when guard is non-empty.
func (g *ASTGen) guardedStmt(src, guard string) JsStmt {
	if guard == "" {
		return jsStmt(src)
	}
	return jsStmt("if (" + guard + ") { " + src + " }")
}

// staticTextChildStmts returns the createTextNode/appendChild
// statements for a fully-static text-only child run, without
// appending them to g.body (so it composes inside buildHoleSubtree).
func (g *ASTGen) staticTextChildStmts(targetVar string, children []Node) []JsStmt {
	var out []JsStmt
	for _, child := range children {
		tn, ok := child.(*TextNode)
		if !ok {
			continue
		}
		text := ""
		for _, p := range tn.Parts {
			text += p.Value
		}
		if isBlankText(text) {
			continue
		}
		out = append(out, jsStmt(
			targetVar+".appendChild(document.createTextNode("+
				backtickString(text)+"));"))
	}
	return out
}

// buildHolesDOM emits the innerHTML + TreeWalker capture pattern for
// a root element whose subtree contains text-interpolation holes.
// It is a thin caller of buildHoleSubtree with targetVar="$$root",
// inline const captures, and no mount-guard.
func (g *ASTGen) buildHolesDOM(el *ElementNode) {
	// The legacy generator calls id() for the root element even
	// though it then overrides the name to $$root (see walkNodes).
	// Consume one id here so the hole/anchor numbering that follows
	// lines up exactly with legacy output.
	_ = g.id()

	// 1. Root element + attributes.
	g.body = append(g.body, g.createElementStmt("$$root", el.Tag))
	g.body = append(g.body, g.setAttrStmt("$$root", g.scopeID, ""))
	g.applyRootAttrs(el)

	// 2-4. Children — innerHTML + TreeWalker hole capture.
	sub := g.buildHoleSubtree("$$root", el.Children, false, "", false)
	g.body = append(g.body, sub.build...)
	g.initBody = append(g.initBody, sub.initUpdates...)
}

// buildEachBlock emits an {#each} block (block 10).
//
// The {#each} reconciler — fragment-builder function, keyed/unkeyed
// row diffing, anchor management — is intricate and already fully
// implemented and tested in the legacy generator (walkEach +
// fraggen.go).  Rather than re-derive all of it as hand-built AST
// (which would only ever be checked against the original anyway),
// block 10 REUSES walkEach: it runs the legacy routine in an
// isolated sub-CodeGen, harvests the emitted text, and splices it
// into the unified AST tree as parsed statements.
//
// This is the "splice a parsed fragment" technique at its largest
// scale.  The spliced text is fully compiler-controlled and
// deterministic, so parsing it back is safe — and the reconciler
// stays single-sourced, so a future fix to fraggen.go benefits
// both generators.
// buildEachBlock emits an {#each} block (E3.3) as struct
// construction — no splice.
//
// The lowering has five parts:
//   1. an anchor comment ($$nN_a) the rows are inserted before
//   2. a fragment builder $$frag_$$nN(item, $$i) that builds one
//      row's DOM and returns { el, update(item,$$i), destroy() }
//   3. block storage — $$nN_bs (array); $$nN_km (Map) when keyed
//   4. the reconciler $$each_$$nN($$arr): unkeyed destroys-all-
//      then-rebuilds; keyed diffs against $$nN_km, reusing matched
//      rows and destroying dropped keys
//   5. wiring — anchor appendChild, the setter $$defer re-render,
//      the init call, and the destroy-all teardown
//
// Row content is built via buildHoleSubtree (the common hole-
// bearing-subtree row).  Rows with per-row events / binds /
// directives, or nested control-flow, are not yet covered by the
// direct path — astEachRowOK gates those to buildModuleSplice.
func (g *ASTGen) buildEachBlock(block *EachBlockNode, parentVar string) {
	array, itemVar, indexVar := parseEachExpr(block.EachExpr)
	if indexVar == "" {
		indexVar = "$$i"
	}
	keyExpr := block.EachKey // "" means unkeyed

	id := g.id()
	anchor := id + "_a"
	blocks := id + "_bs"
	keymap := id + "_km"
	fragFn := "$$frag_" + id
	eachFn := "$$each_" + id

	// Determine the row element: a single child element is used
	// directly; anything else is wrapped in a <div>.
	var rowElem *ElementNode
	if len(block.Children) == 1 {
		if el, ok := block.Children[0].(*ElementNode); ok {
			rowElem = el
		}
	}
	if rowElem == nil {
		rowElem = &ElementNode{Tag: "div", Children: block.Children}
	}

	// ── 1. Anchor ────────────────────────────────────────────
	g.body = append(g.body, jsStmt(
		"const "+anchor+" = document.createComment('#each');"))

	// ── 2. Fragment builder ──────────────────────────────────
	// Built under the fragment-local $$fN namespace: the row
	// element is $$f0 and buildHoleSubtree's captures continue
	// $$f1, $$f2, … — isolated per fragment and stable across
	// multiple {#each} blocks.
	var fragBody []JsStmt
	var sub holeSubtree
	var rowAttrs eachRowAttrs
	var nestedUpdate []JsStmt // captured init+updates for nested-block rows
	rowVar := "$$f0"
	rowHasBlocks := !astHoleSubtreeOK(rowElem.Children)
	g.withFragmentIDs(func() {
		rowVar = g.id() // $$f0
		fragBody = []JsStmt{
			jsStmt("let $$item_" + id + " = " + itemVar + ";"),
			jsStmt("const " + rowVar + " = document.createElement('" +
				rowElem.Tag + "');"),
			jsStmt(rowVar + ".setAttribute('" + g.scopeID + "', '');"),
		}
		// Per-row attribute wiring (events, binds, directives).
		rowAttrs = g.buildEachRowAttrs(rowElem, rowVar, itemVar,
			"$$item_"+id)
		fragBody = append(fragBody, rowAttrs.create...)

		if rowHasBlocks {
			// Row body contains nested control-flow ({#if}/{#each}
			// /{#await}).  The recursive walker builds it — into a
			// captured local scope so its emissions land in the
			// fragment closure, not component scope.  Any reactive
			// updates the nested blocks register re-run through the
			// chain/each functions the walker emits, which are
			// folded into the row update().
			cap := g.captureScope(func() {
				g.buildChildRun(rowElem.Children, rowVar)
			})
			fragBody = append(fragBody, cap.body...)
			fragBody = append(fragBody, rowAttrs.update...)
			fragBody = append(fragBody, cap.initBody...)
			fragBody = append(fragBody, cap.events...)
			nestedUpdate = append(nestedUpdate, cap.initBody...)
		} else {
			sub = g.buildHoleSubtree(rowVar, rowElem.Children, false, "", true)
			fragBody = append(fragBody, sub.build...)
			// Directive/bind init statements — emitted after the
			// child build, matching legacy buffer ordering.
			fragBody = append(fragBody, rowAttrs.update...)
			fragBody = append(fragBody, sub.initUpdates...)
		}
	})

	// update(item, $$i): refresh the row box, then re-run the
	// per-row directive/bind statements and the hole writes — in
	// that order, matching legacy (attr loop precedes child walk).
	updateBody := []JsStmt{jsStmt("$$item_" + id + " = " + itemVar + ";")}
	updateBody = append(updateBody, rowAttrs.update...)
	updateBody = append(updateBody, sub.updateStmts...)
	// Nested-block rows: re-run the nested chain/each init calls so
	// the row's dynamic content refreshes when the row is reused.
	updateBody = append(updateBody, nestedUpdate...)

	// return { el, update, destroy };
	fragReturn := "return { el: " + rowVar + ", " +
		"update(" + itemVar + ", " + indexVar + ") { " +
		stmtsToText(updateBody) + " }, " +
		"destroy() { " + rowVar + ".remove(); } };"
	fragBody = append(fragBody, jsStmt(fragReturn))

	g.body = append(g.body, jsStmt(
		"function "+fragFn+"("+itemVar+", "+indexVar+") { "+
			stmtsToText(fragBody)+" }"))

	// ── 3. Block storage ─────────────────────────────────────
	if keyExpr != "" {
		g.body = append(g.body, jsStmt("const "+keymap+" = new Map();"))
	}
	g.body = append(g.body, jsStmt("let "+blocks+" = [];"))

	// ── 4. Reconciler ────────────────────────────────────────
	g.body = append(g.body, jsStmt(g.eachReconciler(
		eachFn, fragFn, anchor, blocks, keymap, keyExpr, itemVar, indexVar)))

	// ── 5. Wiring ────────────────────────────────────────────
	g.body = append(g.body, jsStmt(parentVar+".appendChild("+anchor+");"))
	g.initBody = append(g.initBody, jsStmt(eachFn+"("+array+");"))
	for _, dep := range findDeps(array, g.reactiveNames()) {
		g.addUpdate(dep, jsStmt(eachFn+"("+array+");"))
	}
	g.cleanup = append(g.cleanup, jsStmt(
		blocks+".forEach(b => b.destroy());"))
}

// eachReconciler returns the source of the $$each_$$nN reconciler
// function — the unkeyed or keyed variant.  Both are fixed-shape;
// only names and the key expression vary.
func (g *ASTGen) eachReconciler(
	eachFn, fragFn, anchor, blocks, keymap, keyExpr, itemVar, indexVar string,
) string {
	if keyExpr == "" {
		// Unkeyed: destroy every row, rebuild from scratch.
		return "function " + eachFn + "($$arr) {" +
			"  if (!" + anchor + ".parentNode) return;" +
			"  " + blocks + ".forEach(b => b.destroy());" +
			"  " + blocks + " = [];" +
			"  ($$arr||[]).forEach(function(" + itemVar + ", " + indexVar + ") {" +
			"    const $$blk = " + fragFn + "(" + itemVar + ", " + indexVar + ");" +
			"    " + anchor + ".before($$blk.el);" +
			"    " + blocks + ".push($$blk);" +
			"  });" +
			"}"
	}
	// Keyed: diff against the key map, reuse matched rows.
	return "function " + eachFn + "($$arr) {" +
		"  if (!" + anchor + ".parentNode) return;" +
		"  const $$newKeys = new Set();" +
		"  const $$newBlocks = [];" +
		"  ($$arr||[]).forEach(function(" + itemVar + ", " + indexVar + ") {" +
		"    const $$key = String(" + stripBraces(keyExpr) + ");" +
		"    $$newKeys.add($$key);" +
		"    let $$blk = " + keymap + ".get($$key);" +
		"    if ($$blk) {" +
		"      if ($$blk.update) $$blk.update(" + itemVar + ", " + indexVar + ");" +
		"    } else {" +
		"      $$blk = " + fragFn + "(" + itemVar + ", " + indexVar + ");" +
		"      " + keymap + ".set($$key, $$blk);" +
		"    }" +
		"    " + anchor + ".before($$blk.el);" +
		"    $$newBlocks.push($$blk);" +
		"  });" +
		"  " + keymap + ".forEach(($$blk, $$key) => {" +
		"    if (!$$newKeys.has($$key)) { $$blk.destroy(); " +
		keymap + ".delete($$key); }" +
		"  });" +
		"  " + blocks + " = $$newBlocks;" +
		"}"
}

// buildAwaitBlock emits an {#await}/{:then}/{:catch} block
// (block 12).
//
// {#await} is lowered by the legacy walkAwaitBlock into promise-
// state storage, a synthesized 3-branch chain, and a $$awaitDrive
// driver with a staleness guard.  That lowering is intricate and
// already correct, so — exactly as with {#each} — block 12 reuses
// it via an isolated sub-CodeGen and splices the result.
// buildAwaitBlock emits an {#await}/{:then}/{:catch} block (E3.2).
//
// {#await} lowers to: four component-scope storage variables
// (promise state, resolved value, error, current-promise cell), a
// synthetic 3-branch IfChainNode gated by the state variable, and
// an $$awaitDrive function that attaches .then/.catch to the
// promise expression with a staleness guard.
//
// The chain is built by buildIfChain — the same struct-based path
// {#if}/{#key} use, including hole-bearing branches (E3.1), so the
// {:then v} / {:catch e} branches may freely reference their
// bindings.  Those bindings are redirected to the module-scope
// $$awaitVal/$$awaitErr storage via rewriteIdentInNodes before the
// chain sees them.
func (g *ASTGen) buildAwaitBlock(block *AwaitBlockNode, parentVar string) {
	id := g.id()
	stateVar := "$$promState_" + id
	valVar := "$$awaitVal_" + id
	errVar := "$$awaitErr_" + id
	cellVar := "$$promCell_" + id

	// Component-scope storage.  State starts at 0 (pending).
	g.body = append(g.body,
		jsStmt("let "+stateVar+" = 0;"),
		jsStmt("let "+valVar+" = undefined;"),
		jsStmt("let "+errVar+" = undefined;"),
		jsStmt("let "+cellVar+" = null;"))

	// Synthetic chain: pending / resolved / rejected.  The {:then}
	// and {:catch} binding names are rewritten to the module-scope
	// storage variables so branch content can reference them.
	branches := []*IfBranch{
		{Cond: stateVar + " === 0", Children: block.PendingChildren},
	}
	if block.ThenChildren != nil {
		branches = append(branches, &IfBranch{
			Cond:     stateVar + " === 1",
			Children: rewriteIdentInNodes(block.ThenChildren, block.ValueName, valVar),
		})
	}
	if block.CatchChildren != nil {
		branches = append(branches, &IfBranch{
			Cond:     stateVar + " === 2",
			Children: rewriteIdentInNodes(block.CatchChildren, block.ErrorName, errVar),
		})
	}
	chainFn := g.buildIfChain(&IfChainNode{Branches: branches}, parentVar)

	// Promise driver.  Resolving the *current* promise twice is
	// guarded by the cell check: a stale settle cannot clobber a
	// fresher one.  The driver runs once at init and again whenever
	// the promise expression's reactive deps change.
	driver := "$$awaitDrive_" + id
	driverStmt := jsStmt(
		"function " + driver + "() {" +
			"  const $$p = Promise.resolve(" + stripBraces(block.PromiseExpr) + ");" +
			"  if (" + cellVar + " === $$p) return;" +
			"  " + cellVar + " = $$p;" +
			"  " + stateVar + " = 0;" +
			"  " + valVar + " = undefined; " + errVar + " = undefined;" +
			"  " + chainFn + "();" +
			"  $$p.then(" +
			"    v => { if (" + cellVar + " !== $$p) return; " +
			valVar + " = v; " + stateVar + " = 1; " + chainFn + "(); }," +
			"    e => { if (" + cellVar + " !== $$p) return; " +
			errVar + " = e; " + stateVar + " = 2; " + chainFn + "(); }" +
			"  );" +
			"}")

	// buildIfChain's last appended statement is the anchor's
	// appendChild — legacy emits the driver BEFORE that wiring
	// (driver goes in the create buffer, the appendChild in the
	// wire buffer).  Insert the driver just before that trailing
	// statement so the structural order matches.
	if n := len(g.body); n > 0 {
		g.body = append(g.body[:n-1],
			append([]JsStmt{driverStmt}, g.body[n-1])...)
	} else {
		g.body = append(g.body, driverStmt)
	}

	// Run the driver at init.
	g.initBody = append(g.initBody, jsStmt(driver+"();"))

	// Re-run the driver when the promise expression's deps change.
	for _, dep := range findDeps(stripBraces(block.PromiseExpr), g.reactiveNames()) {
		g.addUpdate(dep, jsStmt(driver+"();"))
	}
}

// buildIfChain emits an {#if}/{:else if}/{:else} chain (block 9).
//
// Structure mirrors the legacy walkIfChain:
//   - a persistent anchor comment, appended to parentVar
//   - one cached element var per branch ($$chain_N_bI)
//   - reentrancy flags ($$chain_N_running / _pending)
//   - the $$chain_N() reconcile function: pick the active branch
//     by evaluating conditions, remove inactive branches, mount
//     the active one before the anchor
//   - the chain function is registered as a STRUCTURAL update
//     against every reactive var its conditions read
//
// Branch content may be fully static (one $$insertHTML) or carry
// text-interpolation holes (built via buildHoleSubtree, whose
// captured node vars are hoisted to component scope and nulled on
// branch removal).  Element holes / nested control-flow in a
// branch are still deferred.

// stmtsToText renders a slice of statements to minified JS text,
// for embedding in the string-built chain function.  The chain
// function is assembled as text and spliced; statements produced
// by buildHoleSubtree are folded in the same way.
func stmtsToText(stmts []JsStmt) string {
	if len(stmts) == 0 {
		return ""
	}
	return PrintJS(&JsProgram{Body: stmts}, PrintMinified)
}

// nullCaptured returns ` X = null; Y = null;` for every captured
// node variable of a branch — emitted when the branch is removed
// so detached DOM nodes are not retained.
func nullCaptured(captured []string) string {
	var b strings.Builder
	for _, v := range captured {
		b.WriteString(" " + v + " = null;")
	}
	return b.String()
}

func (g *ASTGen) buildIfChain(chain *IfChainNode, parentVar string) string {
	anchor := g.id() + "_a"
	fn := "$$chain_" + g.id()

	// Anchor comment.  The appendChild that wires it into the
	// parent is emitted AFTER the chain function below — legacy
	// keeps element creation and structural wiring in separate
	// buffers, so wiring trails all create statements.
	g.body = append(g.body, jsStmt(
		"const "+anchor+" = document.createComment('if-chain');"))

	// Per-branch cached element vars + reentrancy flags.
	for i := range chain.Branches {
		g.body = append(g.body,
			jsStmt("let "+fn+"_b"+itoa(i)+" = null;"))
	}
	g.body = append(g.body, jsStmt(
		"let "+fn+"_running = false, "+fn+"_pending = false;"))

	// {#key} support: a keyprev holder remembers the last key
	// value.  Initialized to a fresh object so the first run's
	// `keyprev !== keynew` is always true (a unique reference can
	// never === any user value).
	if chain.KeyExpr != "" {
		g.body = append(g.body, jsStmt("let "+fn+"_keyprev = {};"))
	}

	// Per-branch: precompute the build statements and the captured
	// node variables.  A fully-static branch is one $$insertHTML
	// call; a hole-bearing branch uses buildHoleSubtree, whose
	// captured node vars must be declared at component scope (so
	// the setter can reach them) and nulled on branch removal.
	type branchPlan struct {
		buildText string   // statements that populate $$chain_X_bI
		captured  []string // node vars to null on unmount
	}
	plans := make([]branchPlan, len(chain.Branches))
	for i, br := range chain.Branches {
		bv := fn + "_b" + itoa(i)
		// An element branch (old #if-attr syntax) carries its body
		// as br.Elem rather than br.Children — normalize so the
		// build paths below see a uniform child list.
		kids := br.Children
		if br.Elem != nil {
			kids = []Node{stripBranchDirectives(br.Elem)}
		}
		if allStatic(kids) {
			html := emitStaticHTML(kids, g.scopeID)
			plans[i].buildText =
				"$$insertHTML(" + bv + ", '" + escapeJSString(html) + "');"
			continue
		}
		// Hole-bearing branch.  guard = bv: every reactive hole
		// update is a no-op while the branch is unmounted.
		sub := g.buildHoleSubtree(bv, kids, true, bv, false)
		// Component-scope `let X = null;` for every captured var.
		g.body = append(g.body, sub.decls...)
		// Build statements run inside the branch's `if (!bv)` block.
		plans[i].buildText = stmtsToText(sub.build)
		// Initial hole writes — these run once after mount; the
		// chain's mount path already calls the branch build, so
		// fold them into the build text (guarded by bv, harmless
		// at build time since bv is freshly assigned).
		plans[i].buildText += stmtsToText(sub.initUpdates)
		plans[i].captured = sub.captured
	}

	// The chain function — assembled as text then spliced.  The
	// shape is fixed; only branch count, conditions and branch
	// content vary, so building it as a string keeps it readable.
	var b strings.Builder
	b.WriteString("function " + fn + "() {")
	b.WriteString("if (" + fn + "_running) { " + fn + "_pending = true; return; }")
	b.WriteString(fn + "_running = true;")
	b.WriteString("try {")

	// {#key} comparison preamble: when the key expression's value
	// changes identity, force-destroy the cached subtree so the
	// active-branch logic below rebuilds it fresh.
	if chain.KeyExpr != "" {
		b.WriteString("const $$keynew = (" + stripBraces(chain.KeyExpr) + ");")
		b.WriteString("if (" + fn + "_keyprev !== $$keynew) {")
		b.WriteString("if (" + fn + "_b0) { " + fn + "_b0.remove(); " +
			fn + "_b0 = null;" + nullCaptured(plans[0].captured) + " }")
		b.WriteString(fn + "_keyprev = $$keynew;")
		b.WriteString("}")
	}

	b.WriteString("let $$active = -1;")
	// Active-branch selection: first branch whose condition is
	// truthy; an empty Cond ({:else}) always matches.
	for i, br := range chain.Branches {
		idx := itoa(i)
		if br.Cond != "" {
			b.WriteString("if ($$active < 0 && (" + stripBraces(br.Cond) +
				")) $$active = " + idx + ";")
		} else {
			b.WriteString("if ($$active < 0) $$active = " + idx + ";")
		}
	}
	// Remove every branch that is not the active one — and null
	// that branch's captured node variables.
	for i := range chain.Branches {
		idx := itoa(i)
		bv := fn + "_b" + idx
		b.WriteString("if ($$active !== " + idx + " && " + bv + ") { " +
			bv + ".remove(); " + bv + " = null;" + nullCaptured(plans[i].captured) + " }")
	}
	// Mount the active branch (build lazily, then ensure attached).
	for i := range chain.Branches {
		idx := itoa(i)
		bv := fn + "_b" + idx
		b.WriteString("if ($$active === " + idx + ") {")
		b.WriteString("if (!" + bv + ") {")
		b.WriteString(bv + " = document.createElement('div');")
		b.WriteString(bv + ".setAttribute('" + g.scopeID + "', '');")
		b.WriteString(plans[i].buildText)
		b.WriteString(anchor + ".before(" + bv + ");")
		b.WriteString("}")
		b.WriteString("if (" + anchor + ".parentNode && " + bv +
			".parentNode !== " + anchor + ".parentNode) { " +
			anchor + ".before(" + bv + "); }")
		b.WriteString("}")
	}
	b.WriteString("} finally {")
	b.WriteString(fn + "_running = false;")
	b.WriteString("if (" + fn + "_pending) { " + fn + "_pending = false; " +
		fn + "(); }")
	b.WriteString("}")
	b.WriteString("}")

	g.body = append(g.body, jsStmt(b.String()))

	// Wire the anchor into its parent — after the chain function,
	// matching legacy's create/wire buffer split.
	g.body = append(g.body, jsStmt(parentVar+".appendChild("+anchor+");"))

	// Initial render: run the chain once.
	g.initBody = append(g.initBody, jsStmt(fn+"();"))

	// Register the chain as a structural update against every
	// reactive var any branch condition reads — and, for {#key},
	// the key expression too, so a key change re-runs the chain.
	deps := map[string]bool{}
	for _, br := range chain.Branches {
		if br.Cond == "" {
			continue
		}
		for _, d := range findDeps(stripBraces(br.Cond), g.reactiveNames()) {
			deps[d] = true
		}
	}
	if chain.KeyExpr != "" {
		for _, d := range findDeps(stripBraces(chain.KeyExpr), g.reactiveNames()) {
			deps[d] = true
		}
	}
	for d := range deps {
		g.addUpdate(d, jsStmt(fn+"();"))
	}
	return fn
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
	g.applyRootAttrs(el)
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

// applyRootAttrs processes every attribute on the root element,
// dispatching by kind: static attribute, @event handler,
// bind:value/checked (two-way DOM binding), or bind:this.
//
// Block 7 scope — the root element only.  Events/binds on nested
// elements need per-element construction and are still rejected by
// astCanBuildDirectly.
// ─────────────────────────────────────────────────────────────
//  Recursive element walker (E3.4)
//
//  buildNodes / buildElement / buildChildren are the struct-
//  construction twins of the legacy walkNodes / walkElement /
//  walkChildren.  They let the AST generator construct arbitrarily
//  nested dynamic content — nested elements with their own
//  events/binds, components, and slots — without splicing.
// ─────────────────────────────────────────────────────────────

// buildNodes dispatches a list of sibling nodes, each appended to
// parentVar.  When isRootList is set the first element is named
// $$root (matching legacy walkNodes).
func (g *ASTGen) buildNodes(nodes []Node, parentVar string, isRootList bool) {
	for i, node := range nodes {
		switch n := node.(type) {
		case *ElementNode:
			varName := g.id()
			if isRootList && i == 0 {
				varName = "$$root"
			}
			g.buildElement(n, varName, parentVar, isRootList && i == 0)
		case *IfChainNode:
			g.buildIfChain(n, parentVar)
		case *EachBlockNode:
			g.buildEachBlock(n, parentVar)
		case *AwaitBlockNode:
			g.buildAwaitBlock(n, parentVar)
		case *ComponentNode:
			g.buildComponentNode(n, parentVar)
		case *SlotNode:
			g.buildSlotNode(n, parentVar)
		case *TextNode:
			g.buildTextNode(n, parentVar)
		}
	}
}

// buildElement constructs one element: createElement, attribute
// wiring, children, and (unless it is the root) the appendChild
// into its parent.
func (g *ASTGen) buildElement(el *ElementNode, varName, parentVar string, isRoot bool) {
	// An element carrying #each is a control-flow block — reframe
	// it as an EachBlockNode.  (#if is wrapped into an IfChainNode
	// upstream by groupIfChains, so it never arrives here.)
	if el.EachExpr != "" {
		g.buildEachBlock(&EachBlockNode{
			EachExpr: el.EachExpr,
			EachKey:  el.EachKey,
			Children: []Node{stripEachDirective(el)},
		}, parentVar)
		return
	}

	g.body = append(g.body, g.createElementStmt(varName, el.Tag))
	g.body = append(g.body, g.setAttrStmt(varName, g.scopeID, ""))
	g.applyElementAttrs(varName, el)

	g.buildChildren(el, varName)

	if !isRoot {
		g.body = append(g.body, jsStmt(parentVar+".appendChild("+varName+");"))
	}
}

// buildChildren emits an element's children using the same
// strategy ladder as legacy walkElement:
//   1. fully-static element subtree → innerHTML
//   2. hole-compatible (static frame, text holes) → buildHoleSubtree
//   3. otherwise → recurse per child, batching static runs
func (g *ASTGen) buildChildren(el *ElementNode, varName string) {
	children := el.Children
	if len(children) == 0 {
		return
	}
	isTemplate := strings.EqualFold(el.Tag, "template")

	switch {
	case !isTemplate && hasElement(children) && allStatic(children):
		g.body = append(g.body,
			g.innerHTMLStmt(varName, emitStaticHTML(children, g.scopeID)))

	case !isTemplate && astHoleSubtreeOK(children):
		sub := g.buildHoleSubtree(varName, children, false, "", false)
		g.body = append(g.body, sub.build...)
		g.initBody = append(g.initBody, sub.initUpdates...)

	default:
		g.buildChildRun(children, varName)
	}
}

// buildChildRun walks a child list, dispatching dynamic nodes
// individually and collapsing contiguous fully-static runs into a
// single $$insertHTML call.
func (g *ASTGen) buildChildRun(children []Node, parentVar string) {
	flush := func(run []Node) {
		if len(run) == 0 {
			return
		}
		html := emitStaticHTML(run, g.scopeID)
		if strings.TrimSpace(html) == "" {
			return
		}
		g.body = append(g.body, jsStmt(
			"$$insertHTML("+parentVar+", '"+escapeJSString(html)+"');"))
	}
	var staticRun []Node
	for _, child := range children {
		if isStaticNode(child) {
			staticRun = append(staticRun, child)
			continue
		}
		flush(staticRun)
		staticRun = nil
		g.buildNodes([]Node{child}, parentVar, false)
	}
	flush(staticRun)
}

// isStaticNode reports whether a node has no dynamic content and
// can be batched into a static-HTML run.
func isStaticNode(n Node) bool {
	switch x := n.(type) {
	case *TextNode:
		return textNodeAllStatic(x)
	case *ElementNode:
		return isFullyStatic(x)
	default:
		return false
	}
}

// stripEachDirective returns a shallow copy of el with its #each
// directive cleared — so the element can be the row body of the
// EachBlockNode that now carries the directive.
func stripEachDirective(el *ElementNode) *ElementNode {
	cp := *el
	cp.EachExpr = ""
	cp.EachKey = ""
	return &cp
}

// stripBranchDirectives returns a shallow copy of el with BOTH its
// #if and #each directives cleared.  An #if-attribute branch
// element keeps IfCond set after groupIfChains lifts it into an
// IfChainNode; the directive is already represented by the branch
// Cond, so the element body must not re-carry it.
func stripBranchDirectives(el *ElementNode) *ElementNode {
	cp := *el
	cp.IfCond = ""
	cp.EachExpr = ""
	cp.EachKey = ""
	return &cp
}

// buildTextNode appends a text node to parentVar.  A fully-static
// text node becomes one createTextNode; a text node with holes
// becomes a hole text node whose data is written initially and on
// every reactive dependency change.
func (g *ASTGen) buildTextNode(tn *TextNode, parentVar string) {
	if textNodeAllStatic(tn) {
		text := ""
		for _, p := range tn.Parts {
			text += p.Value
		}
		if isBlankText(text) {
			return
		}
		g.body = append(g.body, jsStmt(
			parentVar+".appendChild(document.createTextNode("+
				backtickString(text)+"));"))
		return
	}
	refVar := g.id()
	g.body = append(g.body, jsStmt(
		"const "+refVar+" = document.createTextNode('');"))
	g.body = append(g.body, jsStmt(
		parentVar+".appendChild("+refVar+");"))
	exprSrc := buildExpr(tn.Parts)
	writeSrc := refVar + ".data = " + exprSrc + ";"
	g.initBody = append(g.initBody, jsStmt(writeSrc))
	for _, dep := range findDeps(exprSrc, g.reactiveNames()) {
		g.addUpdate(dep, jsStmt(writeSrc))
	}
}

// buildSlotNode projects a <slot> — an anchor comment plus an init
// block that, if the parent supplied slot content under this
// slot's name, inserts it before the anchor.  Static fallback
// content is rendered when the slot is unfilled.
func (g *ASTGen) buildSlotNode(slot *SlotNode, parentVar string) {
	slotVar := g.id() + "_slot"
	anchor := slotVar + "_a"

	g.body = append(g.body, jsStmt(
		"const "+anchor+" = document.createComment('slot:"+slot.Name+"');"))
	g.body = append(g.body, jsStmt(parentVar+".appendChild("+anchor+");"))

	// Init block — runs after wiring (the anchor needs a parent).
	var b strings.Builder
	b.WriteString("{ const $$sf = $$slots[")
	b.WriteString(jsonQuote(slot.Name))
	b.WriteString("]; if ($$sf) { { const $$sn = $$sf(); ")
	b.WriteString("if ($$sn) " + anchor + ".before($$sn); } }")
	if len(slot.Fallback) > 0 {
		b.WriteString(" else { ")
		b.WriteString(g.slotFallbackText(slot.Fallback, anchor))
		b.WriteString("}")
	}
	b.WriteString(" }")
	g.initBody = append(g.initBody, jsStmt(b.String()))
}

// slotFallbackText emits the fallback content for an unfilled slot
// — simple static text and static elements inserted before the
// slot anchor.  Mirrors legacy walkSlot's fallback handling.
func (g *ASTGen) slotFallbackText(fallback []Node, anchor string) string {
	var b strings.Builder
	for _, child := range fallback {
		switch c := child.(type) {
		case *TextNode:
			b.WriteString(anchor + ".before(document.createTextNode(" +
				buildExpr(c.Parts) + ")); ")
		case *ElementNode:
			html := emitStaticHTML([]Node{c}, g.scopeID)
			b.WriteString("{ const $$fb = document.createElement('template'); " +
				"$$fb.innerHTML = '" + escapeJSString(html) + "'; " +
				anchor + ".before($$fb.content); } ")
		}
	}
	return b.String()
}

// buildComponentNode invokes a child component: a wrapper element,
// an instance handle, the constructor call, a reactive .set() for
// prop changes, and .destroy() teardown.
//
// Slot children passed to the component are not yet struct-built —
// astComponentOK gates a component with slot content to the
// splice.
func (g *ASTGen) buildComponentNode(comp *ComponentNode, parentVar string) {
	inst := g.id() + "_comp"
	wrap := inst + "_wrap"

	// Build the $$slots object — one factory closure per slot.
	// Each slot's content is built by the recursive walker into a
	// captured local scope (captureScope), then assembled into a
	// fragment-factory closure: first call builds a DocumentFragment
	// and caches its children; a reopen re-adopts the cached nodes.
	slotsObj := g.buildSlotClosures(comp)

	g.body = append(g.body, g.createElementStmt(wrap, "div"))
	g.body = append(g.body, g.setAttrStmt(wrap, g.scopeID, ""))
	g.body = append(g.body, jsStmt("let "+inst+" = null;"))
	g.body = append(g.body, jsStmt(parentVar+".appendChild("+wrap+");"))

	propsObj := g.componentPropsObject(comp.Attrs)

	// Initial mount — the constructor calls each slot factory,
	// which inserts the slot content (and its anchors) into the DOM.
	g.initBody = append(g.initBody, jsStmt(
		"if (!"+inst+") "+inst+" = "+comp.Name+"("+wrap+", "+
			propsObj+", "+slotsObj+");"))

	// Reactive prop updates.
	setStmt := "if (" + inst + ") " + inst + ".set(" + propsObj + ");"
	deps := map[string]bool{}
	for _, attr := range comp.Attrs {
		if attr.IsEvent || attr.IsBindThis {
			continue
		}
		for _, d := range findDeps(attrExprToJS(attr.Value), g.reactiveNames()) {
			deps[d] = true
		}
	}
	for d := range deps {
		g.addUpdate(d, jsStmt(setStmt))
	}

	// Teardown.
	g.cleanup = append(g.cleanup, jsStmt(
		"if ("+inst+") "+inst+".destroy();"))
}

// buildSlotClosures assembles the $$slots object literal passed as
// a component constructor's third argument.  Each slot becomes a
// factory closure that returns a DocumentFragment of the slot's
// content.  Empty when the component has no slotted children.
//
// The closure has two paths, mirroring legacy walkComponent:
//   - first call — build a fresh fragment, populate it with the
//     slot's DOM, run any init (nested chain/each setup), then
//     snapshot the fragment's children into a cache array
//   - reopen     — move the still-attached cached children into a
//     new fragment and re-run init (a DocumentFragment is emptied
//     when appended, so the children, not the fragment, are cached)
func (g *ASTGen) buildSlotClosures(comp *ComponentNode) string {
	if len(comp.Slots) == 0 {
		return "{}"
	}
	// Deterministic slot order — ranging a Go map is random and
	// would make output non-reproducible.
	names := make([]string, 0, len(comp.Slots))
	for name := range comp.Slots {
		names = append(names, name)
	}
	sort.Strings(names)

	var entries []string
	for _, name := range names {
		slotNodes := comp.Slots[name]
		fragVar := "$$sfrag_" + g.id()
		cacheVar := "$$scache_" + g.id()

		// Build the slot content into a captured local scope so its
		// emissions land inside the closure, not component scope.
		cap := g.captureScope(func() {
			g.buildChildRun(slotNodes, fragVar)
		})
		buildText := stmtsToText(cap.body)
		initText := stmtsToText(cap.initBody)

		// The cache var is hoisted to component scope so the
		// closure (and reopens) can see it.
		g.body = append(g.body, jsStmt("let "+cacheVar+" = null;"))

		var b strings.Builder
		b.WriteString("() => { ")
		// Reopen path.
		b.WriteString("if (" + cacheVar + ") { ")
		b.WriteString("const " + fragVar +
			" = document.createDocumentFragment(); ")
		b.WriteString("for (const $$c of " + cacheVar + ") { ")
		b.WriteString("if ($$c.parentNode !== null) " +
			fragVar + ".appendChild($$c); } ")
		b.WriteString(initText + " ")
		b.WriteString("return " + fragVar + "; } ")
		// First-open path.
		b.WriteString("const " + fragVar +
			" = document.createDocumentFragment(); ")
		b.WriteString(buildText + " ")
		b.WriteString(initText + " ")
		b.WriteString(cacheVar + " = Array.from(" + fragVar +
			".childNodes); ")
		b.WriteString("return " + fragVar + "; }")

		entries = append(entries, jsonQuote(name)+": "+b.String())
	}
	return "{ " + strings.Join(entries, ", ") + " }"
}

// componentPropsObject builds the `{ prop: expr, … }` literal
// passed to a component constructor / .set().  Reuses the legacy
// componentPropsEntries so prop handling (bindable $$onChange
// callbacks, event forwarding) stays single-sourced.
func (g *ASTGen) componentPropsObject(attrs []*Attr) string {
	sub := &CodeGen{analysis: g.analysis, scopeID: g.scopeID}
	entries := sub.componentPropsEntries(attrs)
	if len(entries) == 0 {
		return "{}"
	}
	return "{ " + strings.Join(entries, ", ") + " }"
}

// applyElementAttrs wires every attribute of an element to the
// variable varName — events, binds, directives, static attrs.
// Used for the root element ($$root) and, via the recursive
// element builder, for any nested element.
func (g *ASTGen) applyElementAttrs(varName string, el *ElementNode) {
	for _, attr := range el.Attrs {
		switch {
		case attr.IsEvent:
			g.emitEventHandler(varName, attr)
		case attr.IsTwoWay || attr.IsBind:
			g.emitBinding(varName, el.Tag, attr)
		case attr.IsBindThis:
			g.emitBindThisAST(varName, attr)
		case attr.IsClassDir:
			g.emitClassDir(varName, attr)
		case attr.IsStyleDir:
			g.emitStyleDir(varName, attr)
		default:
			g.body = append(g.body,
				g.setAttrStmt(varName, attr.Name, attr.Value))
		}
	}
}

// applyRootAttrs wires the root element's attributes — a thin
// alias of applyElementAttrs for $$root.
func (g *ASTGen) applyRootAttrs(el *ElementNode) {
	g.applyElementAttrs("$$root", el)
}

// emitClassDir wires `class:foo={cond}` — toggle the class `foo`
// based on the truthiness of cond.  classList.toggle preserves any
// classes set via a plain class="…" attribute, so static and
// dynamic classes coexist.  The toggle runs at initial render and
// is registered as an update against each reactive dependency.
func (g *ASTGen) emitClassDir(varName string, attr *Attr) {
	stmtSrc := classDirStmt(varName, attr.Name, attr.Value, "")
	g.initBody = append(g.initBody, jsStmt(stmtSrc))
	for _, dep := range findDeps(attrExprToJS(attr.Value), g.reactiveNames()) {
		g.addUpdate(dep, jsStmt(stmtSrc))
	}
}

// emitStyleDir wires `style:foo={expr}` — set an inline CSS
// property via style.setProperty (handles hyphenated names and
// custom properties).  Runs at initial render and on every change
// of a reactive dependency.
func (g *ASTGen) emitStyleDir(varName string, attr *Attr) {
	stmtSrc := styleDirStmt(varName, attr.Name, attr.Value, "")
	g.initBody = append(g.initBody, jsStmt(stmtSrc))
	for _, dep := range findDeps(attrExprToJS(attr.Value), g.reactiveNames()) {
		g.addUpdate(dep, jsStmt(stmtSrc))
	}
}

// emitEventHandler wires an @event attribute: an addEventListener
// in the events section and a matching removeEventListener in
// cleanup.  Three handler shapes, mirroring the legacy generator:
//   - bare identifier  → addEventListener('click', inc)
//   - handler expr     → const $$h = <arrow>; addEventListener(…, $$h)
//   - statement body   → const $$h = ($event) => { … }; …
func (g *ASTGen) emitEventHandler(varName string, attr *Attr) {
	handler := g.rewriteHandler(attr.Value)

	if isSimpleIdent(handler) {
		g.events = append(g.events,
			jsStmt(varName+".addEventListener('"+attr.Name+"', "+handler+");"))
		g.cleanup = append(g.cleanup,
			jsStmt(varName+".removeEventListener('"+attr.Name+"', "+handler+");"))
		return
	}

	hv := varName + "_" + attr.Name + "_h"
	if isHandlerExpr(handler) {
		g.body = append(g.body, jsStmt("const "+hv+" = "+handler+";"))
	} else {
		// Statement body — wrap in an ($event) => { … } arrow.
		g.body = append(g.body,
			jsStmt("const "+hv+" = ($event) => { "+handler+"; };"))
	}
	g.events = append(g.events,
		jsStmt(varName+".addEventListener('"+attr.Name+"', "+hv+");"))
	g.cleanup = append(g.cleanup,
		jsStmt(varName+".removeEventListener('"+attr.Name+"', "+hv+");"))
}

// emitBinding wires a bind:value / bind:checked attribute — both
// directions:
//   - DOM→state: an input/change listener calling $$set_X
//   - state→DOM: an initial-render write + an update registered
//     against X so the setter re-writes the DOM
func (g *ASTGen) emitBinding(varName, tag string, attr *Attr) {
	spec, ok := twoWayBindSpec(tag, attr.Name)
	sv := stripBraces(attr.Value)

	// DOM→state listener — only for a recognized (tag, attr) combo
	// bound to a bare state variable.
	if ok && g.analysis != nil && g.analysis.isStateName(sv) {
		hv := varName + "_bind_" + spec.event + "_h"
		g.body = append(g.body, jsStmt(
			"const "+hv+" = ($e) => $$set_"+sv+"($e.target."+spec.readProp+");"))
		g.events = append(g.events,
			jsStmt(varName+".addEventListener('"+spec.event+"', "+hv+");"))
		g.cleanup = append(g.cleanup,
			jsStmt(varName+".removeEventListener('"+spec.event+"', "+hv+");"))
	}

	// state→DOM write — the one-way half.
	//
	// For a two-way bind:value the write targets the DOM *property*
	// (elem.value = …), not the attribute: setAttribute('value', …)
	// only sets defaultValue and won't update a control the user
	// has already touched.  bind:checked is a boolean attr and
	// bindAttrStmt already emits elem.checked = !!(…).
	writeSrc := func() string {
		if attr.IsTwoWay && attr.Name == "value" {
			return varName + ".value = (" + stripBraces(attr.Value) + ");"
		}
		return bindAttrStmt(varName, attr.Name, attr.Value, "")
	}
	g.initBody = append(g.initBody, jsStmt(writeSrc()))
	for _, dep := range findDeps(stripBraces(attr.Value), g.reactiveNames()) {
		g.addUpdate(dep, jsStmt(writeSrc()))
	}
}

// emitBindThisAST wires bind:this={ref} — write the live element
// into the user's variable after creation, clear it on destroy.
//
// When ref names a $state variable the write goes through its
// setter ($$set_ref), so other holes reading ref react to it;
// otherwise (a plain local, or a member like refs.input) a raw
// assignment is used.  Mirrors the legacy emitBindThis.
func (g *ASTGen) emitBindThisAST(varName string, attr *Attr) {
	ref := stripBraces(attr.Value)
	if g.analysis != nil && g.analysis.isStateName(ref) {
		g.initBody = append(g.initBody,
			jsStmt("$$set_"+ref+"("+varName+");"))
		g.cleanup = append(g.cleanup,
			jsStmt("$$set_"+ref+"(null);"))
		return
	}
	g.initBody = append(g.initBody, jsStmt(ref+" = "+varName+";"))
	g.cleanup = append(g.cleanup, jsStmt(ref+" = null;"))
}

// rewriteHandler strips the {…} wrapper from a handler attribute
// value and rewrites assignments to reactive variables into their
// $$set_X form — the transform the legacy rewriteHandlerAssignments
// applies.
// rewriteHandler strips the {…} wrapper from a handler attribute
// value and rewrites assignments to reactive variables into their
// $$set_X form.
//
// This delegates to the legacy rewriteHandlerAssignments, which
// correctly handles the concise-arrow-body case (`() => n++`) by
// splitting at `=>` and treating the body as a statement — without
// that split, an update operator in expression position is never
// seen by RewriteAssignments.  Keeping one implementation avoids
// the two diverging (Stage 3 caught exactly such a divergence).
func (g *ASTGen) rewriteHandler(handler string) string {
	if g.analysis == nil {
		return stripBraces(handler)
	}
	sub := &CodeGen{analysis: g.analysis}
	return sub.rewriteHandlerAssignments(handler)
}

// stripBraces removes a single surrounding {…} wrapper that the
// template parser preserves on attribute values.
func stripBraces(s string) string {
	return unwrapBraces(s)
}

// innerHTMLStmt builds `<varName>.innerHTML = '<html>';`
func (g *ASTGen) innerHTMLStmt(varName, html string) JsStmt {
	return &JsExprStmt{
		baseStmt: baseStmt{genNode()},
		X: &JsAssignExpr{
			baseExpr: baseExpr{genNode()},
			Op:       "=",
			L: &JsMemberExpr{
				baseExpr: baseExpr{genNode()},
				Obj:      id(varName),
				Prop:     id("innerHTML"),
			},
			R: strLit(escapeJSString(html)),
		},
	}
}

// isBlankText reports whether s is empty or only whitespace.
func isBlankText(s string) bool {
	for _, r := range s {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			return false
		}
	}
	return true
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
// destroy() teardown order, matching the legacy generator:
//   1. effect cleanups        ($$cleanupN)
//   2. onMount cleanup fns     ($$mountCleanupN)
//   3. onDestroy callback bodies, inline
//   4. event-listener removal  (g.cleanup)
//   5. $$root.remove()
// set() — emitted only when the component has settable reactive
// vars — dispatches each incoming prop to its setter.
func (g *ASTGen) buildReturn() JsStmt {
	hasSettable := g.analysis != nil &&
		(len(g.analysis.StateVars) > 0 || len(g.analysis.Props) > 0)

	destroyBody := &JsBlock{baseStmt: baseStmt{genNode()}}
	// 1. Effect cleanups.
	if g.analysis != nil {
		for i := range g.analysis.Effects {
			n := itoa(i)
			destroyBody.Body = append(destroyBody.Body,
				jsStmt("if ($$cleanup"+n+") { $$cleanup"+n+"(); }"))
		}
	}
	// 2-3. onMount cleanup fns + onDestroy bodies.
	destroyBody.Body = append(destroyBody.Body, g.buildOnDestroyCleanup()...)
	// 4. Event-listener removal.
	destroyBody.Body = append(destroyBody.Body, g.cleanup...)
	// 5. Scoped-style element removal (before root removal,
	//    matching the legacy generator).
	if g.hasStyle() {
		destroyBody.Body = append(destroyBody.Body,
			jsStmt("$$style.remove();"))
	}
	// 6. Root removal.
	destroyBody.Body = append(destroyBody.Body, jsStmt("$$root.remove();"))

	destroyProp := &JsProp{
		baseNode: genNode(),
		Kind:     "init",
		Method:   true,
		Key:      id("destroy"),
		Value: &JsFuncExpr{
			baseExpr: baseExpr{genNode()},
			Body:     destroyBody,
		},
	}

	props := []*JsProp{destroyProp}

	if hasSettable {
		// set(props) { if (props.X !== undefined) $$set_X(props.X); … }
		setBody := &JsBlock{baseStmt: baseStmt{genNode()}}
		emit := func(name string) {
			setBody.Body = append(setBody.Body, jsStmt(
				"if (props."+name+" !== undefined) $$set_"+name+"(props."+name+");"))
		}
		for _, sv := range g.analysis.StateVars {
			emit(sv.Name)
		}
		for _, p := range g.analysis.Props {
			emit(p.Name)
		}
		props = append(props, &JsProp{
			baseNode: genNode(),
			Kind:     "init",
			Method:   true,
			Key:      id("set"),
			Value: &JsFuncExpr{
				baseExpr: baseExpr{genNode()},
				Params:   []JsExpr{id("props")},
				Body:     setBody,
			},
		})
	}

	return &JsReturnStmt{
		baseStmt: baseStmt{genNode()},
		Arg: &JsObjectExpr{
			baseExpr: baseExpr{genNode()},
			Props:    props,
		},
	}
}

// headerComment is the banner the legacy generator prints at the
// top of every file.  Kept identical so differential diffs aren't
// noisy.  (The printer doesn't emit comments, so the harness
// strips this from the legacy side before diffing — see
// astgen_diff_test.go.)
const headerComment = "// Generated by Vela Compiler — do not edit"
