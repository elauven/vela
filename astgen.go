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
	"reflect"
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

	// structuralUpdates maps a reactive variable name → chain
	// re-render (mount/unmount) statements that must run BEFORE
	// the ordinary updates for the same dependency.  See
	// addStructuralUpdate.
	structuralUpdates map[string][]JsStmt

	// hoisted holds declarations that must ALWAYS live at component
	// scope, even when emitted while captureScope has redirected
	// the other buffers into a local scope (a slot-factory closure
	// or an {#each} fragment).  captureScope deliberately does NOT
	// swap this buffer.  Used for child-component instance vars:
	// `let $$nN_comp = null` must be visible to the slot closure's
	// reopen path, which references the instance without
	// re-running its first-open declaration — a `let` captured
	// inside the closure would throw a TDZ ReferenceError on
	// reopen.  Mirrors legacy walkComponent's instance-var hoist.
	hoisted []JsStmt

	// inSlotScope is true while building the content of a slot
	// closure.  Slot content emitted under captureScope lands in
	// the closure's first-open path; any hole-node variable it
	// captures must therefore be hoisted to component scope (and
	// null-guarded) so the setter cascade — which replays the
	// slot's reactive updates — can reach it.  buildChildren reads
	// this to pick the hoisting build path.
	inSlotScope bool

	// slotGuard, when inSlotScope, is the slot's cache variable
	// ($$scache_$$nN).  It is non-null only after the slot has
	// first opened, so it guards slot-content hole updates: a
	// reactive update that fires before the slot ever opened (the
	// captured hole vars still null) becomes a safe no-op.
	slotGuard string

	// inBranchScope is true while the recursive walker builds an
	// {#if}/{#key}/{#await} branch body under captureScope.  Like
	// inSlotScope, it makes element-creating builders hoist their
	// node variable (`let X = null;` at component scope, assigned
	// in the branch-build block) so the setter cascade — which
	// replays the branch's reactive attribute/style updates — can
	// reach the element.  A `const` trapped in the branch-build
	// block would be invisible to $$set_*.
	inBranchScope bool

	// nestedTeardowns collects teardown statements for nested
	// {#if}/{#key}/{#await} chains whose machinery was hoisted to
	// component scope.  When the enclosing outer branch
	// deactivates, the chain's content is left orphaned in the
	// DOM (the outer's bv snapshot only contained the chain's
	// anchor, not its dynamically-attached nodes).  The outer
	// chain's per-branch teardown reads the entries appended
	// during that branch's build (snapshot-length pattern) and
	// emits them so the nested chain is torn down with its
	// parent.  Doubly-nested chains propagate naturally because
	// each level appends its own teardown.
	nestedTeardowns []string

	// fragHoisted collects per-row-fragment declarations during
	// emission (companion to capturedScope.fragHoisted).
	// buildComponentNode appends here when it is BOTH in a
	// fragment AND in a branch scope; the row-fragment builder
	// prepends them to the fragment body.  captureScope swaps
	// this with a fresh slice and the inner buildIfChain re-emits
	// the captured entries so they continue bubbling up.
	fragHoisted []JsStmt

	// cssExternal selects CSS delivery.  When false (default), the
	// component injects its scoped <style> into document.head at
	// runtime (CSS-in-JS).  When true, buildStyleInjection emits
	// nothing — the scoped CSS is written to a sibling .css file
	// by the compiler, and the consumer links it.
	cssExternal bool

	// compInstances records the (instance, wrapper) variable names
	// of every component built since the last captureScope reset.
	// buildIfChain uses these to destroy + null a branch's
	// components when the branch is removed, so re-entering the
	// branch recreates them (their `if (!$$nN_comp)` guard would
	// otherwise skip re-creation, leaving an empty wrapper).
	compInstances []compInstance
}

// compInstance is one child-component invocation's bookkeeping —
// its instance handle, wrapper element variable names, and the
// `.set(propsObj)` statement that propagates new prop values to an
// already-mounted instance.  setStmt is empty for a slot-positioned
// component whose props are not reactive (the mount call alone is
// enough).
type compInstance struct {
	inst    string
	wrap    string
	setStmt JsStmt // `if (inst) inst.set({...});`  — nil if none
}

// addUpdate records that statement stmt must re-run whenever the
// reactive variable dep changes.
func (g *ASTGen) addUpdate(dep string, stmt JsStmt) {
	if g.updates == nil {
		g.updates = map[string][]JsStmt{}
	}
	g.updates[dep] = append(g.updates[dep], stmt)
}

// declare routes a top-level declaration to the scope appropriate
// for the generator's current position.  Use this for declarations
// that must outlive a branch-build closure (`let X = null;`,
// `function Y(){}`, etc.) — the scope rule is:
//
//   - inside an {#each} row fragment closure → fragment scope
//     (g.fragHoisted; consumed by the row-fragment builder so each
//     row gets its own var while the chain function declared here
//     can still read row-local `it`/`$$i`);
//   - otherwise → component scope (g.hoisted).
//
// Pair with inCapturedScope (predicate) and declareInScope (the
// "escape if captured, else emit to body" wrapper).  These three
// replaced four nearly-identical per-builder helpers — every
// builder used to compute the routing itself, which was a recipe
// for the next builder to forget.
//
// declare is for declarations that must be visible across the
// current closure.  Statements that should execute in document
// order (e.g. `anchor.appendChild(...)`) still go to g.body or
// g.initBody as before.
func (g *ASTGen) declare(stmt JsStmt) {
	if g.fragCounter >= 0 {
		g.fragHoisted = append(g.fragHoisted, stmt)
	} else {
		g.hoisted = append(g.hoisted, stmt)
	}
}

// inCapturedScope reports whether the generator is currently
// emitting into a captured g.body — that is, inside a branch's
// build closure (captured by buildIfChain) or a slot's factory
// closure (captured by buildComponentNode's slot path).  When
// true, builders must route declarations through g.declare so
// they escape the closure; when false, declarations can go to
// g.body directly because g.body IS the component scope.
//
// This is the predicate the prior per-builder helpers were
// each computing as `g.inBranchScope || g.inSlotScope`.
func (g *ASTGen) inCapturedScope() bool {
	return g.inBranchScope || g.inSlotScope
}

// declareInScope emits a declaration at the "right" scope for the
// generator's current position: g.declare (escape) when inside a
// captured scope, g.body (direct) otherwise.  This is the body
// of every builder's `decl` closure — buildIfChain, buildAwait
// Block, etc. — pulled into a single method so the next builder
// doesn't have to reinvent it.
func (g *ASTGen) declareInScope(stmt JsStmt) {
	if g.inCapturedScope() {
		g.declare(stmt)
	} else {
		g.body = append(g.body, stmt)
	}
}

// addStructuralUpdate records a STRUCTURAL re-render — an
// {#if}/{#key}/{#await} chain function or a similar
// mount/unmount operation — that must run BEFORE the ordinary
// (interior) updates for the same dependency.
//
// Why ordering matters: when a condition flips a branch off (e.g.
// `block` becomes null in `{#if block}`), the chain re-render
// tears the branch down and nulls its guard var ($$chain_X_bN).
// The branch's interior updates are all guarded by that var, so
// once the chain has run they correctly no-op.  If an interior
// update ran first, it would read now-stale state — e.g.
// `$$each_$$nN(def.fields)` where `def` just became null —
// throwing before the branch could be removed.
func (g *ASTGen) addStructuralUpdate(dep string, stmt JsStmt) {
	if g.structuralUpdates == nil {
		g.structuralUpdates = map[string][]JsStmt{}
	}
	g.structuralUpdates[dep] = append(g.structuralUpdates[dep], stmt)
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
	comps    []compInstance      // component instances built here

	// fragHoisted carries declarations that must escape any
	// enclosing branch-build closures to land at the top of the
	// nearest ENCLOSING row-fragment scope (an {#each} fragment
	// body).  Example: a child component used inside an {#if}
	// inside an {#each} row.  Hoisting to component scope is
	// wrong (every row would share one instance — the
	// `if (!compVar)` mount guard would build only the first
	// row).  The right scope is the row fragment, so each row
	// has its own var.  buildIfChain (and any other capturing
	// branch builder) re-emits inner fragHoisted from its own
	// capture, so the entries bubble up through arbitrarily
	// deep nesting until consumed.  Outside a fragment (no
	// enclosing row), buildComponentNode hoists to component
	// scope directly and this channel stays empty.
	fragHoisted []JsStmt
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
	saveComps := g.compInstances
	saveFragHoisted := g.fragHoisted

	// Restore on the way out — via defer so a panic inside fn()
	// cannot leave the generator's buffers swapped out.  The
	// fuzzer and the test suite both recover from panics and keep
	// running; without this, a panic mid-capture would corrupt
	// every subsequent compilation with another generator's
	// partial state, masking the original failure.
	defer func() {
		g.body, g.initBody = saveBody, saveInit
		g.events, g.cleanup = saveEvents, saveCleanup
		g.updates = saveUpdates
		g.compInstances = saveComps
		g.fragHoisted = saveFragHoisted
	}()

	g.body, g.initBody = nil, nil
	g.events, g.cleanup = nil, nil
	g.updates = map[string][]JsStmt{}
	g.compInstances = nil
	g.fragHoisted = nil

	fn()

	return capturedScope{
		body:        g.body,
		initBody:    g.initBody,
		events:      g.events,
		cleanup:     g.cleanup,
		updates:     g.updates,
		comps:       g.compInstances,
		fragHoisted: g.fragHoisted,
	}
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
	defer func() { g.fragCounter = saved }()
	g.fragCounter = 0
	fn()
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
	return generateCodeASTCSS(name, sfc, nodes, analysis, scopeID, false)
}

// generateCodeASTCSS is generateCodeAST with explicit CSS-delivery
// control.  cssExternal=true suppresses the runtime <style>
// injection; the compiler writes the scoped CSS to a sibling .css
// file instead.  generateCodeAST keeps the historical signature
// (inject mode) for the many existing callers and tests.
func generateCodeASTCSS(
	name string,
	sfc *SFC,
	nodes []Node,
	analysis *Analysis,
	scopeID string,
	cssExternal bool,
) string {
	prog := buildProgramCSS(name, sfc, nodes, analysis, scopeID, cssExternal)
	return headerComment + "\n\n" + PrintJS(prog, PrintPretty)
}

func templateExprRoot(expr *TemplateExpr) JsExpr {
	if expr == nil || expr.Program == nil || len(expr.Program.Body) != 1 {
		return nil
	}
	if stmt, ok := expr.Program.Body[0].(*JsExprStmt); ok {
		return stmt.X
	}
	return nil
}

// templateExpr returns the cached expression parsed from the template and
// records its absolute SFC offset on its root. Template expressions are
// deliberately re-used by initial-render and update shells: JsNode has no
// parent pointers, so sharing this immutable subtree is safe.
func (g *ASTGen) templateExpr(expr *TemplateExpr) JsExpr {
	root := templateExprRoot(expr)
	if root == nil {
		return jsExpr("undefined")
	}
	markGeneratedNode(root)
	setTemplateOrigin(root, g.sfc.TemplateOffset+expr.Pos)
	return root
}

// rewrittenTemplateExpr preserves the existing state-assignment rewrite for
// event handlers, but gives the rewritten expression its attribute's exact
// template origin instead of finding matching printed text after generation.
func (g *ASTGen) rewrittenTemplateExpr(expr *TemplateExpr, source string) JsExpr {
	root := jsExpr(source)
	if expr != nil {
		setTemplateOrigin(root, g.sfc.TemplateOffset+expr.Pos)
	}
	return root
}

func templateMarker(n int) string { return "$$vela_template_" + itoa(n) }

// templateStmt parses a compiler-authored shell and swaps its explicit marker
// identifiers for cached template AST roots. This is local AST construction,
// not a global search by printed expression signature.
func (g *ASTGen) templateStmt(src string, exprs ...JsExpr) JsStmt {
	bindings := make(map[string]JsExpr, len(exprs))
	for i, expr := range exprs {
		bindings[templateMarker(i)] = expr
	}
	stmt := jsStmt(src)
	replaceTemplateMarkers(reflect.ValueOf(stmt), bindings)
	return stmt
}

func replaceTemplateMarkers(v reflect.Value, bindings map[string]JsExpr) {
	if !v.IsValid() {
		return
	}
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			return
		}
		if v.CanInterface() {
			if ident, ok := v.Interface().(*JsIdent); ok {
				if replacement := bindings[ident.Name]; replacement != nil && v.CanSet() {
					v.Set(reflect.ValueOf(replacement))
					return
				}
			}
		}
		replaceTemplateMarkers(v.Elem(), bindings)
		return
	}
	switch v.Kind() {
	case reflect.Ptr:
		if !v.IsNil() {
			replaceTemplateMarkers(v.Elem(), bindings)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			field := v.Field(i)
			if field.CanSet() || field.Kind() != reflect.Interface {
				replaceTemplateMarkers(field, bindings)
			}
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			replaceTemplateMarkers(v.Index(i), bindings)
		}
	}
}

func (g *ASTGen) textValueExpr(parts []*Part) JsExpr {
	if len(parts) == 1 && !parts[0].Static {
		return &JsCallExpr{baseExpr: baseExpr{genNode()}, Callee: id("String"), Args: []JsExpr{g.templateExpr(parts[0].Expr)}}
	}
	t := &JsTemplateLit{baseExpr: baseExpr{genNode()}, Quasis: []string{""}}
	for _, part := range parts {
		if part.Static {
			t.Quasis[len(t.Quasis)-1] += escapeTL(part.Value)
			continue
		}
		t.Exprs = append(t.Exprs, g.templateExpr(part.Expr))
		t.Quasis = append(t.Quasis, "")
	}
	return t
}

func (g *ASTGen) textWriteStmt(ref string, parts []*Part) JsStmt {
	return g.templateStmt(ref+".data = "+templateMarker(0)+";", g.textValueExpr(parts))
}

func guardedTemplateStmt(stmt JsStmt, guard string) JsStmt {
	if guard == "" {
		return stmt
	}
	return &JsIfStmt{baseStmt: baseStmt{genNode()}, Test: id(guard), Then: stmt}
}

func (g *ASTGen) attrWriteStmt(varName string, attr *Attr) JsStmt {
	expr := g.templateExpr(attr.Expr)
	if attr.IsTwoWay && attr.Name == "value" {
		return g.templateStmt(varName+".value = ("+templateMarker(0)+");", expr)
	}
	if isBoolAttr(attr.Name) {
		return g.templateStmt(varName+"."+attr.Name+" = !!("+templateMarker(0)+");", expr)
	}
	if attr.Name == "class" {
		return g.templateStmt(varName+".className = String("+templateMarker(0)+");", expr)
	}
	return g.templateStmt(varName+".setAttribute('"+attr.Name+"', String("+templateMarker(0)+"));", expr)
}

func (g *ASTGen) classDirStmtAST(varName string, attr *Attr) JsStmt {
	return g.templateStmt(varName+".classList.toggle('"+attr.Name+"', !!("+templateMarker(0)+"));", g.templateExpr(attr.Expr))
}

func (g *ASTGen) styleDirStmtAST(varName string, attr *Attr) JsStmt {
	return g.templateStmt(varName+".style.setProperty('"+attr.Name+"', String("+templateMarker(0)+"));", g.templateExpr(attr.Expr))
}
func setTemplateOrigin(n JsNode, pos int) {
	switch x := n.(type) {
	case *JsIdent:
		x.SourceOrigin, x.HasSourceOrigin = pos, true
	case *JsLiteral:
		x.SourceOrigin, x.HasSourceOrigin = pos, true
	case *JsTemplateLit:
		x.SourceOrigin, x.HasSourceOrigin = pos, true
	case *JsArrayExpr:

		x.SourceOrigin, x.HasSourceOrigin = pos, true
	case *JsObjectExpr:
		x.SourceOrigin, x.HasSourceOrigin = pos, true
	case *JsFuncExpr:
		x.SourceOrigin, x.HasSourceOrigin = pos, true
	case *JsArrowFunc:
		x.SourceOrigin, x.HasSourceOrigin = pos, true
	case *JsUnaryExpr:
		x.SourceOrigin, x.HasSourceOrigin = pos, true
	case *JsBinaryExpr:
		x.SourceOrigin, x.HasSourceOrigin = pos, true
	case *JsLogicalExpr:
		x.SourceOrigin, x.HasSourceOrigin = pos, true
	case *JsAssignExpr:
		x.SourceOrigin, x.HasSourceOrigin = pos, true
	case *JsMemberExpr:
		x.SourceOrigin, x.HasSourceOrigin = pos, true
	case *JsCallExpr:
		x.SourceOrigin, x.HasSourceOrigin = pos, true
	case *JsNewExpr:
		x.SourceOrigin, x.HasSourceOrigin = pos, true
	case *JsConditionalExpr:
		x.SourceOrigin, x.HasSourceOrigin = pos, true
	case *JsSeqExpr:
		x.SourceOrigin, x.HasSourceOrigin = pos, true
	case *JsSpreadExpr:
		x.SourceOrigin, x.HasSourceOrigin = pos, true
	case *JsAwaitExpr:
		x.SourceOrigin, x.HasSourceOrigin = pos, true
	case *JsYieldExpr:
		x.SourceOrigin, x.HasSourceOrigin = pos, true
	case *JsThisExpr:
		x.SourceOrigin, x.HasSourceOrigin = pos, true
	case *JsSuperExpr:
		x.SourceOrigin, x.HasSourceOrigin = pos, true
	}
}

func (g *ASTGen) componentPropsExpr(attrs []*Attr) JsExpr {
	obj := &JsObjectExpr{baseExpr: baseExpr{genNode()}}
	for _, attr := range attrs {
		if attr.IsEvent {
			continue
		}
		prop := &JsProp{Key: id(attr.Name)}
		if attr.IsBind {
			prop.Value = g.templateExpr(attr.Expr)
		} else {
			prop.Value = strLit(escapeAttr(attr.Value))
		}
		obj.Props = append(obj.Props, prop)
		if attr.IsTwoWay && g.analysis != nil {
			target := strings.TrimSpace(unwrapBraces(attr.Value))
			if g.analysis.isStateName(target) {
				obj.Props = append(obj.Props, &JsProp{Key: id("$$onChange_" + attr.Name), Value: jsExpr("($$v) => $$set_" + target + "($$v)")})
			}
		}
	}
	return obj
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
	return buildProgramCSS(name, sfc, nodes, analysis, scopeID, false)

}

// buildProgramCSS is buildProgram with explicit CSS-delivery
// control — see generateCodeASTCSS.
func buildProgramCSS(
	name string,
	sfc *SFC,
	nodes []Node,
	analysis *Analysis,
	scopeID string,
	cssExternal bool,
) *JsProgram {
	prog, _ := buildProgramWithInfo(name, sfc, nodes, analysis, scopeID, cssExternal)
	return prog
}

// buildProgramWithInfo is the instrumented form of buildProgramCSS. It keeps
// production output unchanged while allowing migration tests to prove which
// inputs still route through the legacy string-emitter splice.
func buildProgramWithInfo(
	name string,
	sfc *SFC,
	nodes []Node,
	analysis *Analysis,
	scopeID string,
	cssExternal bool,
) (*JsProgram, bool) {
	g := &ASTGen{
		name:        name,
		scopeID:     scopeID,
		analysis:    analysis,
		sfc:         sfc,
		fragCounter: -1,
		cssExternal: cssExternal,
	}

	usedLegacySplice := !astCanBuildDirectly(nodes, analysis)
	var prog *JsProgram
	if usedLegacySplice {
		prog = g.buildModuleSplice(name, sfc, nodes, analysis, scopeID)
	} else {
		prog = g.buildModule(nodes)
	}

	if issues := ValidateProgram(prog); len(issues) > 0 {
		msg := "vela: internal codegen error — generated AST failed validation:\n"
		for _, iss := range issues {
			msg += "  [" + iss.Kind + "] " + iss.Msg + "\n"
		}
		panic(msg)
	}

	return prog, usedLegacySplice
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

	// A standalone <slot> with a text/static fallback is handled by
	// buildSlotNode directly; unsupported dynamic fallback shapes still use
	// the legacy splice path.
	if len(nodes) == 1 {
		if slot, ok := nodes[0].(*SlotNode); ok {
			return astHoleSubtreeOK(slot.Fallback)
		}
	}

	// Standalone components without slots are direct-buildable.
	if len(nodes) == 1 {
		if comp, ok := nodes[0].(*ComponentNode); ok {
			return len(comp.Slots) == 0
		}
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
			return true
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
			// {#await} is lowered to a state-driven branch chain.
			if eachHasComponent(x.PendingChildren) || eachHasComponent(x.ThenChildren) || eachHasComponent(x.CatchChildren) {
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
			// Raw-text elements (<textarea>, <title>) cannot use
			// the innerHTML + TreeWalker hole path: their body is
			// CDATA, so a <!--$--> placeholder is stored as
			// literal text and the hole anchor comes back null.
			// An interpolated body must take the recursive-walker
			// path, which writes the .value/.textContent property.
			if _, isRaw := rawTextProp(n.Tag); isRaw {
				if _, hasInterp := singleInterpolation(n.Children); hasInterp {
					return false
				}
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

	// ── Runtime helpers ──────────────────────────────────────
	// $$dispatch, $$insertHTML and $$proxy used to be inlined in
	// full into every component.  They now live in runtime.js.
	// After the whole program is built, finalizeRuntimeHelpers
	// scans for which are referenced and prepends a single import
	// from runtime.js — plus, for $$dispatch, a thin local arrow
	// that binds the imported (target, …) form to this
	// component's own $$target.

	// $$proxy (deep-reactivity helper) is imported from runtime.js
	// when the component declares a $state.deep variable —
	// finalizeRuntimeHelpers adds the import.

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

	// ── Source-ordered component setup ─────────────────────
	// Normal top-level statements and reactive declarations keep the order
	// in which the author wrote them.
	fnBody.Body = append(fnBody.Body, g.buildScriptPlan()...)

	// ── Effects (block 6) ────────────────────────────────────
	// Each $effect becomes a $$cleanupN holder plus an $$effectN
	// runner that disposes the previous cleanup and re-runs the
	// body (an IIFE whose return value is the new cleanup).
	fnBody.Body = append(fnBody.Body, g.buildEffectDecls()...)

	// ── DOM construction (blocks 2-3) ────────────────────────
	g.buildDOM(nodes)
	// Hoisted declarations (child-component instance vars) first,
	// so a slot closure's reopen path can reference them without a
	// TDZ error — see ASTGen.hoisted.
	fnBody.Body = append(fnBody.Body, g.hoisted...)
	fnBody.Body = append(fnBody.Body, g.body...)

	// ── Reactive setters (blocks 5-6) ────────────────────────
	fnBody.Body = append(fnBody.Body, g.buildSetters()...)

	// ── Event listeners (block 7) ────────────────────────────
	// addEventListener calls for @event handlers and the input
	// listeners powering bind:.  After setters so handlers (which
	// call $$set_X) are in scope.
	fnBody.Body = append(fnBody.Body, g.events...)

	// ── Initial render (block 3) ─────────────────────────────
	fnBody.Body = append(fnBody.Body, g.initBody...)

	// ── Mount ────────────────────────────────────────────────
	// $$target may be an Element (appendChild as a child) OR a
	// Comment node (insertBefore — so the component is positioned
	// at the anchor's slot rather than appended at the end of its
	// container).  Comment-as-target is the wrapper-less mount
	// path: the caller creates an anchor comment in document
	// order, then passes the anchor here.  Without the
	// nodeType-8 branch every wrapper-less mount would land at
	// the END of the containing element, breaking position when
	// the component has any siblings rendered after it.
	fnBody.Body = append(fnBody.Body,
		jsStmt(`if ($$target.nodeType === 8) `+
			`$$target.parentNode.insertBefore($$root, $$target); `+
			`else $$target.appendChild($$root);`))

	// ── onMount callbacks (stage 1a) ─────────────────────────
	// Run after the component's DOM is in the document.  Each
	// callback body is wrapped in an IIFE so a `return fn` inside
	// it is captured as that mount's cleanup function rather than
	// returning from the component factory.
	fnBody.Body = append(fnBody.Body, g.buildOnMount()...)

	// ── Run effects once (block 6) ───────────────────────────
	// onMount may restore state from browser storage; run effects afterwards
	// so persistence observes that restored value rather than overwriting it.
	fnBody.Body = append(fnBody.Body, g.buildEffectInit()...)

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

	// Also expose the component factory as the module's DEFAULT
	// export.  Vela components compile to `export function Name`,
	// but the idiomatic component import — matching Svelte and the
	// way every multi-component Vela project is written — is a
	// default import: `import Name from './Name.vela'`.  Emitting
	// `export default Name;` makes both the default and the named
	// import resolve to the same factory.
	prog.Body = append(prog.Body, jsStmt("export default "+g.name+";"))

	// Prepend runtime-helper imports + the $$dispatch binding for
	// whichever helpers this component actually references.
	g.finalizeRuntimeHelpers(prog, fnBody)
	return prog
}

// finalizeRuntimeHelpers scans the built program for references to
// the shared runtime helpers ($$dispatch / $$insertHTML / $$proxy)
// and wires in only what is used:
//
//   - $$insertHTML / $$proxy — imported by name from runtime.js.
//   - $$dispatch — the runtime exports a (target, name, detail)
//     form; it is imported aliased as $$rt_dispatch and a thin
//     local `const $$dispatch = (name, detail) => …` is inserted
//     at the top of the component body, closing over $$target.
//
// Emitting nothing for an unused helper keeps minimal components
// free of dead imports.
func (g *ASTGen) finalizeRuntimeHelpers(prog *JsProgram, fnBody *JsBlock) {
	// The splice path re-parses legacy output, which already
	// carries the helper import and the $$dispatch binding.
	// Detect that and skip — adding them again would duplicate.
	for _, st := range prog.Body {
		imp, ok := st.(*JsImportDecl)
		if !ok || imp.Source != "./runtime.js" {
			continue
		}
		for _, sp := range imp.Specifiers {
			if sp.Local == "$$rt_dispatch" ||
				sp.Imported == "$$insertHTML" ||
				sp.Imported == "$$proxy" ||
				sp.Imported == "$$isProxy" {
				return
			}
		}
	}

	usesDispatch := false
	usesInsertHTML := false
	usesProxy := false
	usesIsProxy := false
	WalkNode(prog, func(n JsNode) bool {
		id, ok := n.(*JsIdent)
		if !ok {
			return true
		}
		switch id.Name {
		case "$$dispatch":
			usesDispatch = true
		case "$$insertHTML":
			usesInsertHTML = true
		case "$$proxy":
			usesProxy = true
		case "$$isProxy":
			usesIsProxy = true
		}
		return true
	})

	// Named imports, deterministic order.
	var names []string
	if usesDispatch {
		names = append(names, "$$dispatch as $$rt_dispatch")
	}
	if usesInsertHTML {
		names = append(names, "$$insertHTML")
	}
	if usesProxy {
		names = append(names, "$$proxy")
	}
	if usesIsProxy {
		names = append(names, "$$isProxy")
	}
	if len(names) > 0 {
		imp := jsStmt("import { " + strings.Join(names, ", ") +
			" } from './runtime.js';")
		// Helper import goes after user imports, before the body —
		// prepend to module body just ahead of the export.
		prog.Body = append([]JsStmt{imp}, prog.Body...)
	}

	// The $$dispatch local binding closes over $$root — insert
	// it at the very top of the component function body.  Note:
	// the arrow reads $$root LAZILY at call time, not at the
	// binding's evaluation; by the time a user handler calls
	// $$dispatch, $$root has been created.  Dispatching on $$root
	// (not $$target) is what makes events isolated to the
	// component: with the wrapper-less mount, $$target may be a
	// Comment anchor in the parent's DOM, and dispatching there
	// would fire on the parent — leaking to sibling components.
	if usesDispatch {
		bind := jsStmts("const $$dispatch = (name, detail) => " +
			"$$rt_dispatch($$root, name, detail);")
		fnBody.Body = append(bind, fnBody.Body...)
	}
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
		spec := imp.Spec
		if src == "vela/store" || src == "vela" {
			src = "./runtime.js"
			// onMount/onDestroy are compiler intrinsics — the
			// analyzer extracts them and the codegen wires the
			// callbacks directly into the component's mount path
			// and destroy() body.  They have no runtime existence,
			// so emitting `import { onMount } from './runtime.js'`
			// produces an ERR_MODULE_NOT_FOUND at module load.
			// Strip them; drop the statement entirely if nothing
			// real-runtime is left.
			spec = stripVelaIntrinsics(spec)
			if spec == "" && imp.Spec != "" {
				continue // every name was an intrinsic
			}
		} else {
			src = RewriteImportPath(src)
		}
		var stmtSrc string
		if spec == "" {
			stmtSrc = "import '" + src + "';"
		} else {
			stmtSrc = "import " + spec + " from '" + src + "';"
		}
		out = append(out, jsStmt(stmtSrc))
	}
	return out
}

// velaCompilerIntrinsics is the set of names that look like imports
// from 'vela' but are actually compiler intrinsics — extracted at
// analysis time and wired directly into generated code.  An import
// statement that mentions one of these must not pass the name
// through to the runtime module.
var velaCompilerIntrinsics = map[string]bool{
	"onMount":   true,
	"onDestroy": true,
}

// stripVelaIntrinsics rewrites an import specifier string (the part
// between `import` and `from`) by removing any imported name that
// is a Vela compiler intrinsic.  Returns "" when the resulting
// import would import nothing real — the caller drops the
// statement in that case.
//
// Handles the practical shapes seen on `import … from 'vela'`:
//
//	{ A, B }
//	{ A as a, B }
//	D, { A }                   (default + named)
//	* as Ns                    (namespace — passed through)
//	D                          (default only — passed through)
//
// Whitespace and aliases are preserved on the names that survive.
func stripVelaIntrinsics(spec string) string {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return ""
	}
	// Default-only or namespace import — no names to filter.
	openBrace := strings.Index(spec, "{")
	if openBrace < 0 {
		return spec
	}
	closeBrace := strings.LastIndex(spec, "}")
	if closeBrace < openBrace {
		return spec
	}
	before := strings.TrimSpace(spec[:openBrace]) // "D," or ""
	inside := spec[openBrace+1 : closeBrace]      // "A, B as b"
	before = strings.TrimSuffix(before, ",")
	before = strings.TrimSpace(before)

	// Filter the comma-separated specifier list.
	var kept []string
	for _, part := range strings.Split(inside, ",") {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		// `X as Y` — the imported name (what `vela` would export)
		// is on the LEFT of `as`; that is what identifies a Vela
		// intrinsic.  The local alias is irrelevant here.
		name := p
		if i := strings.Index(p, " as "); i > 0 {
			name = strings.TrimSpace(p[:i])
		}
		if velaCompilerIntrinsics[name] {
			continue
		}
		kept = append(kept, p)
	}

	if len(kept) == 0 && before == "" {
		return ""
	}
	if len(kept) == 0 {
		// Default-only remains — drop the now-empty braces.
		return before
	}
	named := "{ " + strings.Join(kept, ", ") + " }"
	if before == "" {
		return named
	}
	return before + ", " + named
}

// buildUserScriptParts keeps source-derived constants ahead of rune
// initializers while leaving statements that read reactive bindings after them.
// Function declarations are safe in the prelude because their bodies execute
// only when called, after the component has completed initialization.
func (g *ASTGen) buildUserScriptParts() (prelude, postlude []JsStmt) {
	if g.analysis == nil || g.analysis.CleanScript == "" {
		return nil, nil
	}
	prog := ParseJS(g.analysis.CleanScript)
	reactive := g.reactiveNames()
	for _, stmt := range prog.Body {
		if _, ok := stmt.(*JsFuncDecl); ok {
			prelude = append(prelude, stmt)
			continue
		}
		if len(FindDepsInNode(stmt, reactive)) == 0 {
			prelude = append(prelude, stmt)
		} else {
			postlude = append(postlude, stmt)
		}
	}
	return prelude, postlude
}

func (g *ASTGen) buildScriptPlan() []JsStmt {
	if g.analysis == nil {
		return nil
	}
	var out []JsStmt
	seenDerived := map[string]bool{}
	initialized := map[string]bool{}
	flushReadyDerived := func() {
		for _, derived := range g.analysis.DerivedVars {
			if !seenDerived[derived.Name] || initialized[derived.Name] {
				continue
			}
			ready := true
			for _, dep := range derived.Deps {
				if g.isDerivedName(dep) && !initialized[dep] {
					ready = false
					break
				}
			}
			if ready {
				out = append(out, jsStmt("$$upd_"+derived.Name+"();"))
				initialized[derived.Name] = true
			}
		}
	}
	for _, step := range g.analysis.ScriptPlan {
		switch step.Kind {
		case "state":
			out = append(out, g.buildStateDecl(step.State)...)
		case "derived":
			out = append(out, g.buildDerivedDecl(step.Derived)...)
			seenDerived[step.Derived.Name] = true
			flushReadyDerived()
		case "user":
			userProg := ParseJS(step.Source)
			shiftTree(userProg, step.SourcePos)
			out = append(out, userProg.Body...)
		}
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
func (g *ASTGen) isDerivedName(name string) bool {
	if g.analysis == nil {
		return false
	}
	for _, derived := range g.analysis.DerivedVars {
		if derived.Name == name {
			return true
		}
	}
	return false
}

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
	// External-CSS mode: the scoped stylesheet is written to a
	// sibling .css file by the compiler — the component emits no
	// runtime <style> injection at all.
	if g.cssExternal {
		return nil
	}
	css := ScopeCSS(g.sfc.Style, g.scopeID)
	return []JsStmt{
		jsStmt("const $$style = document.createElement('style');"),
		jsStmt("$$style.textContent = " + backtickString(css) + ";"),
		jsStmt("document.head.appendChild($$style);"),
	}
}

// ExtractScopedCSS returns the scope-transformed stylesheet for an
// SFC, or "" when it has no <style>.  Used by the compiler in
// external-CSS mode to write a sibling .css file.
func ExtractScopedCSS(sfc *SFC, scopeID string) string {
	if sfc == nil || strings.TrimSpace(sfc.Style) == "" {
		return ""
	}
	return ScopeCSS(sfc.Style, scopeID)
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
//
//	let X = $$proxy(<initial>, $$mut_X);
//
// The value is Proxy-wrapped so nested mutations route through the
// same notify path as reassignment; $$mut_X is the mutation
// callback the Proxy traps invoke.
func (g *ASTGen) buildStateDecl(sv *StateVar) []JsStmt {
	init := sv.Initial
	if init == "" {
		init = "undefined"
	}
	if sv.Deep {
		return []JsStmt{jsStmt("const $$mut_" + sv.Name + " = () => $$set_" + sv.Name + "_notify();"), jsStmt("let " + sv.Name + " = $$proxy(" + init + ", $$mut_" + sv.Name + ");")}
	}
	return []JsStmt{jsStmt("let " + sv.Name + " = " + init + ";")}
}

func (g *ASTGen) buildStateDecls() []JsStmt {
	if g.analysis == nil {
		return nil
	}
	var out []JsStmt
	for _, sv := range g.analysis.StateVars {
		out = append(out, g.buildStateDecl(sv)...)
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
func (g *ASTGen) buildDerivedDecl(dv *DerivedVar) []JsStmt {
	return []JsStmt{jsStmt("let " + dv.Name + ";"), jsStmt("const $$upd_" + dv.Name + " = () => { " + dv.Name + " = " + dv.Expr + "; };")}
}

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
//	const $$mountCleanup0 = (function() { …body… })();
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
//	function $$set_X($$v) {
//	  if (X === $$v) return;            // cycle break + no-op skip
//	  X = $$v;
//	  $$defer(() => { …DOM updates… }); // only if X drives a hole
//	}
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
	//    Structural re-renders (chain mount/unmount) come FIRST so
	//    that a branch toggled off tears down — nulling its guard
	//    var — before any of its now-stale interior updates run.
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
	// Structural first: for `name` and every transitively-dirtied
	// derived.
	add(g.structuralUpdates[name])
	for _, dv := range dependentDerived {
		add(g.structuralUpdates[dv.Name])
	}
	// Then the ordinary interior updates.
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

	// 3. Re-run dependent effects.  An effect re-runs when it
	//    reads `name` directly OR reads any derived that `name`
	//    transitively dirtied — `dirty` already holds that full
	//    closure (step 1).  Checking only `name` here would miss
	//    an effect that reads a $derived whose upstream changed
	//    (e.g. an effect reading `previewHtml = $derived(...md...)`
	//    would never re-run on a markdownText edit).
	if g.analysis != nil {
		for i, eff := range g.analysis.Effects {
			effDirty := false
			for _, d := range eff.Deps {
				if dirty[d] {
					effDirty = true
					break
				}
			}
			if effDirty {
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

	// if (<name> === $$v && !$$isProxy($$v)) return;
	//
	// Identity equality is a sound "unchanged" test only for
	// values that cannot mutate in place.  A deep-reactive proxy
	// ($state.deep) passed from a parent keeps the same reference
	// across an in-place mutation, so a bare `=== $$v` guard would
	// suppress the re-render the mutation should trigger (the
	// PageCraft symptom: editing a block field updated the data
	// but Canvas did not re-render).  $$isProxy lets an
	// identity-equal proxy through to the cascade; primitives and
	// plain objects keep the fast skip.
	body.Body = append(body.Body, &JsIfStmt{
		baseStmt: baseStmt{genNode()},
		Test: &JsBinaryExpr{
			baseExpr: baseExpr{genNode()},
			Op:       "&&",
			L: &JsBinaryExpr{
				baseExpr: baseExpr{genNode()},
				Op:       "===",
				L:        id(name),
				R:        id("$$v"),
			},
			R: &JsUnaryExpr{
				baseExpr: baseExpr{genNode()},
				Op:       "!",
				Prefix:   true,
				X: &JsCallExpr{
					baseExpr: baseExpr{genNode()},
					Callee:   id("$$isProxy"),
					Args:     []JsExpr{id("$$v")},
				},
			},
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
//	function $$set_X_notify() { …reactive cascade… }
//	function $$set_X($$v) {
//	  if (X === $$v) return;
//	  X = $$proxy($$v, $$mut_X);   // re-wrap the new value
//	  $$set_X_notify();
//	}
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

	// $$set_X — guard, re-wrap, notify.  The guard mirrors the
	// plain setter (=== plus !$$isProxy) so the two generators
	// agree; for a deep var the new value is normally raw (proxy
	// !== raw, so the guard never fired anyway), but a re-passed
	// proxy must still fall through to a re-wrap + notify.
	setBody := &JsBlock{baseStmt: baseStmt{genNode()}}
	setBody.Body = append(setBody.Body, &JsIfStmt{
		baseStmt: baseStmt{genNode()},
		Test: &JsBinaryExpr{
			baseExpr: baseExpr{genNode()},
			Op:       "&&",
			L: &JsBinaryExpr{
				baseExpr: baseExpr{genNode()},
				Op:       "===",
				L:        id(name),
				R:        id("$$v"),
			},
			R: &JsUnaryExpr{
				baseExpr: baseExpr{genNode()},
				Op:       "!",
				Prefix:   true,
				X: &JsCallExpr{
					baseExpr: baseExpr{genNode()},
					Callee:   id("$$isProxy"),
					Args:     []JsExpr{id("$$v")},
				},
			},
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

// cloneStmt deep-copies a statement so the same reactive update
// can appear in multiple setters without aliasing one node pointer
// across the tree (a later mutation of one copy would otherwise
// corrupt the others).
//
// It is implemented as a print + reparse round-trip rather than a
// hand-written deep-clone visitor.  That is a deliberate call: a
// per-node-type clone switch is a second exhaustive switch that
// silently drifts when a JsNode type is added (a missed field =
// an aliasing bug), whereas the printer/parser are already kept
// exact inverses by jsprinter_roundtrip_test.  Profiling puts this
// at <1% of compile time, so the round-trip's cost does not
// justify taking on that maintenance hazard.  Revisit only if a
// profile ever shows it mattering.
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
		case *IfChainNode, *EachBlockNode, *AwaitBlockNode, *SlotNode:
			g.buildChainRootDOM(nodes)
			return
		}
	}

	if len(nodes) == 1 {
		if comp, ok := nodes[0].(*ComponentNode); ok {
			root := "$" + "$root"
			g.body = append(g.body, g.createElementStmt(root, "div"))
			g.body = append(g.body, g.setAttrStmt(root, g.scopeID, ""))
			g.body = append(g.body, jsStmt(root+".style.display = 'contents';"))
			g.buildComponentNode(comp, root)
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
		case *SlotNode:
			g.buildSlotNode(x, "$$root")
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
//	fragTarget — true when targetVar is a DocumentFragment (the
//	            {#if}-branch case): a fragment has no innerHTML
//	            setter, so HTML is filled via a <template>.
//
// Updates are registered in g.updates against each hole's reactive
// dependencies, exactly as before.
func (g *ASTGen) buildHoleSubtree(
	targetVar string,
	children []Node,
	hoist bool,
	guard string,
	collectUpdates bool,
	fragTarget bool,
) holeSubtree {
	var res holeSubtree

	// fill emits the HTML-population statements for targetVar —
	// fragment-safe when the target is a DocumentFragment.
	fill := func(html string) []JsStmt {
		if fragTarget {
			return g.fillHTMLStmts(targetVar, html)
		}
		return []JsStmt{g.innerHTMLStmt(targetVar, html)}
	}

	html, holes := buildHoleTemplate(children, g.scopeID)
	if len(children) == 0 {
		return res
	}
	if len(holes) == 0 {
		// Fully static subtree — innerHTML for element-bearing
		// runs, direct text nodes for text-only runs.
		if hasElement(children) {
			res.build = append(res.build, fill(html)...)
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

			write := g.textWriteStmt(refVar, tn.Parts)
			hg := guard
			if hg == "$$self" {
				hg = refVar
			}
			res.initUpdates = append(res.initUpdates,
				guardedTemplateStmt(write, hg))
			if collectUpdates {
				res.updateStmts = append(res.updateStmts, write)
			} else {
				for _, dep := range templatePartsDeps(tn.Parts, g.reactiveNames()) {
					g.addUpdate(dep, guardedTemplateStmt(write, hg))
				}
			}
		}
		return res
	}

	// Element-bearing subtree with holes — the HTML carries the
	// <!--$--> anchors.  fill is fragment-safe.
	res.build = append(res.build, fill(html)...)

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

		write := g.textWriteStmt(refVar, textNode.Parts)
		hg := guard
		if hg == "$$self" {
			hg = refVar
		}

		// Initial render write — guarded too, for symmetry with the
		// setter path (a branch may be unmounted at init time).
		res.initUpdates = append(res.initUpdates,
			guardedTemplateStmt(write, hg))

		// Reactive updates.  For a chain branch they are registered
		// against the component-scope setters, mounted-guarded.
		// For an {#each} fragment (collectUpdates) the row update()
		// re-runs every write unconditionally, so collect them
		// instead — one write per hole, deduplicated across deps.
		if collectUpdates {
			res.updateStmts = append(res.updateStmts, write)
		} else {
			for _, dep := range templatePartsDeps(textNode.Parts, g.reactiveNames()) {
				g.addUpdate(dep, guardedTemplateStmt(write, hg))
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
	// deps are component-level reactive names read by row updates.
	deps map[string]bool
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
	res := eachRowAttrs{deps: map[string]bool{}}
	addDeps := func(attr *Attr, expr string) {
		for _, dep := range templateExprDeps(attr.Expr, stripBraces(expr), g.reactiveNames()) {
			res.deps[dep] = true
		}
	}
	for _, attr := range rowElem.Attrs {
		switch {
		case attr.IsEvent:
			// A row-root event needs both state-assignment and row-item rewrites.
			handler := rewriteItemRef(g.rewriteHandler(attr.Value), itemVar, boxVar)
			handlerExpr := g.rewrittenTemplateExpr(attr.Expr, handler)
			if isSimpleIdent(handler) {
				res.create = append(res.create, g.templateStmt(
					rowVar+".addEventListener('"+attr.Name+"', "+templateMarker(0)+");", handlerExpr))
			} else {
				hv := rowVar + "_" + attr.Name + "_h"
				if isHandlerExpr(handler) {
					res.create = append(res.create, g.templateStmt(
						"const "+hv+" = "+templateMarker(0)+";", handlerExpr))
				} else {
					res.create = append(res.create, g.templateStmt(
						"const "+hv+" = ($event) => { "+templateMarker(0)+"; };", handlerExpr))
				}
				res.create = append(res.create, jsStmt(
					rowVar+".addEventListener('"+attr.Name+"', "+hv+");"))
			}

		case attr.IsClassDir:
			expr := rewriteItemRef(attr.Value, itemVar, boxVar)
			stmt := g.templateStmt(
				rowVar+".classList.toggle('"+attr.Name+"', !!("+templateMarker(0)+"));",
				g.rewrittenTemplateExpr(attr.Expr, attrExprToJS(expr)))
			res.update = append(res.update, stmt)
			addDeps(attr, expr)

		case attr.IsStyleDir:
			expr := rewriteItemRef(attr.Value, itemVar, boxVar)
			stmt := g.templateStmt(
				rowVar+".style.setProperty('"+attr.Name+"', String("+templateMarker(0)+"));",
				g.rewrittenTemplateExpr(attr.Expr, attrExprToJS(expr)))
			res.update = append(res.update, stmt)
			addDeps(attr, expr)

		case attr.IsBind:
			expr := rewriteItemRef(attr.Value, itemVar, boxVar)
			valueExpr := g.rewrittenTemplateExpr(attr.Expr, unwrapBraces(expr))
			var stmt JsStmt
			switch {
			case attr.IsTwoWay && attr.Name == "value":
				stmt = g.templateStmt(rowVar+".value = ("+templateMarker(0)+");", valueExpr)
			case isBoolAttr(attr.Name):
				stmt = g.templateStmt(rowVar+"."+attr.Name+" = !!("+templateMarker(0)+");", valueExpr)
			case attr.Name == "class":
				stmt = g.templateStmt(rowVar+".className = String("+templateMarker(0)+");", valueExpr)
			default:
				stmt = g.templateStmt(rowVar+".setAttribute('"+attr.Name+"', String("+templateMarker(0)+"));", valueExpr)
			}
			res.update = append(res.update, stmt)
			addDeps(attr, expr)

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

	// 2-4. Children.
	//
	// Raw-text elements (<textarea>, <title>) have a CDATA content
	// model — their markup is NOT parsed as HTML.  Setting
	// innerHTML = '<!--$-->' on a <textarea> stores the literal
	// string as its value rather than creating a comment node, so
	// the hole-capturing TreeWalker finds nothing and the anchor
	// is null (crash: "$$nN_a is null").  Compile a single
	// interpolated child to a reactive property assignment instead
	// — textarea→value, title→textContent — which is also the
	// semantically correct mapping for these elements.
	if prop, ok := rawTextProp(el.Tag); ok {
		if expr, ok := singleInterpolation(el.Children); ok {
			write := g.templateStmt("$$root."+prop+" = String("+templateMarker(0)+");", g.templateExpr(el.Children[0].(*TextNode).Parts[0].Expr))
			g.initBody = append(g.initBody, write)
			for _, dep := range findDeps(expr, g.reactiveNames()) {
				g.addUpdate(dep, write)
			}
			return
		}
		if text, ok := staticRawText(el.Children); ok {
			g.body = append(g.body, jsStmt("$$root."+prop+" = '"+escapeJSString(text)+"';"))
			return
		}
	}

	// Children — innerHTML + TreeWalker hole capture.
	sub := g.buildHoleSubtree("$$root", el.Children, false, "", false, false)
	g.body = append(g.body, sub.build...)
	g.initBody = append(g.initBody, sub.initUpdates...)
}

// rawTextProp reports whether tag is a raw-text (CDATA) element
// whose interpolated body must be written as a DOM property rather
// than parsed as HTML, and returns that property name.
func rawTextProp(tag string) (string, bool) {
	switch tag {
	case "textarea":
		return "value", true
	case "title":
		return "textContent", true
	}
	return "", false
}

// singleInterpolation returns the JS expression of a children list
// that is exactly one dynamic text interpolation ({expr}) with no
// surrounding static text or sibling nodes.
func singleInterpolation(children []Node) (string, bool) {
	if len(children) != 1 {
		return "", false
	}
	tn, ok := children[0].(*TextNode)
	if !ok || len(tn.Parts) != 1 || tn.Parts[0].Static {
		return "", false
	}
	return tn.Parts[0].Value, true
}

// staticRawText returns the concatenated literal text of a children
// list that contains only static text parts (no interpolation, no
// elements).
func staticRawText(children []Node) (string, bool) {
	var b strings.Builder
	for _, c := range children {
		tn, ok := c.(*TextNode)
		if !ok {
			return "", false
		}
		for _, p := range tn.Parts {
			if !p.Static {
				return "", false
			}
			b.WriteString(p.Value)
		}
	}
	return b.String(), true
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
//  1. an anchor comment ($$nN_a) the rows are inserted before
//  2. a fragment builder $$frag_$$nN(item, $$i) that builds one
//     row's DOM and returns { el, update(item,$$i), destroy() }
//  3. block storage — $$nN_bs (array); $$nN_km (Map) when keyed
//  4. the reconciler $$each_$$nN($$arr): unkeyed destroys-all-
//     then-rebuilds; keyed diffs against $$nN_km, reusing matched
//     rows and destroying dropped keys
//  5. wiring — anchor appendChild, the setter $$defer re-render,
//     the init call, and the destroy-all teardown
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

	// When the {#each} is built inside a captured scope — an {#if}
	// branch via the recursive walker, or a slot closure — its
	// PERSISTENT machinery (anchor, block storage, keymap, the row
	// fragment builder, and the reconciler) must live at component
	// scope.  The setter cascade calls the reconciler ($$each_$$nN)
	// when a dependency changes; a function trapped in the branch-
	// build block would be invisible there (ReferenceError).  Only
	// the INITIAL reconciler call stays in the branch/slot body so
	// it runs when that branch mounts.
	captured := g.inCapturedScope()

	// ── 1. Anchor ────────────────────────────────────────────
	if captured {
		g.declare(jsStmt("let " + anchor + " = null;"))
		g.body = append(g.body, jsStmt(
			anchor+" = document.createComment('#each');"))
	} else {
		g.body = append(g.body, jsStmt(
			"const "+anchor+" = document.createComment('#each');"))
	}

	// ── 2. Fragment builder ──────────────────────────────────
	// Built under the fragment-local $$fN namespace: the row
	// element is $$f0 and buildHoleSubtree's captures continue
	// $$f1, $$f2, … — isolated per fragment and stable across
	// multiple {#each} blocks.
	var fragBody []JsStmt
	var sub holeSubtree
	var rowAttrs eachRowAttrs
	var nestedUpdate []JsStmt   // captured init+updates for nested-block rows
	var rowComps []compInstance // child-component instances built in this row
	var rowCleanup []JsStmt     // component/event teardown owned by this row
	// rowBodyDeps collects component-state names a nested-control-
	// flow row body reads; every such dep re-runs the whole {#each}
	// (see the rowHasBlocks branch).
	rowBodyDeps := map[string]bool{}
	rowVar := "$$f0"
	rowChildren := rewriteIdentInNodes(rowElem.Children, itemVar, "$$item_"+id)
	rowHasBlocks := !astHoleSubtreeOK(rowChildren)
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
			// fragment closure, not component scope.
			cap := g.captureScope(func() {
				g.buildChildRun(rowChildren, rowVar)
			})
			// Per-row declarations bubbled up from any nested
			// branch (a component built inside an {#if} inside
			// this row).  These must come BEFORE the row's
			// element creation/branch builds: code emitted from
			// those references the vars.  Each row gets its own
			// instance because they live in the fragment closure.
			fragBody = append(fragBody, cap.fragHoisted...)
			fragBody = append(fragBody, cap.body...)
			fragBody = append(fragBody, rowAttrs.update...)
			fragBody = append(fragBody, cap.initBody...)
			fragBody = append(fragBody, cap.events...)
			nestedUpdate = append(nestedUpdate, cap.initBody...)
			rowCleanup = append(rowCleanup, cap.cleanup...)
			// Child-component instances built within this row need
			// their .set() statements replayed in the row's update
			// closure (Session A: row reuse without destroying
			// children).
			rowComps = append(rowComps, cap.comps...)
			// The nested blocks register reactive updates against
			// component state (e.g. an {#if} on a component-scope
			// flag).  Those chain functions are fragment-local —
			// the component setter cannot call them directly.  So
			// every dep a row body reads must re-run the WHOLE
			// {#each}: re-rendering all rows re-evaluates each
			// row's nested chains.  Collect the dep names here;
			// they are registered against eachFn below.
			for dep := range cap.updates {
				rowBodyDeps[dep] = true
			}
		} else {
			sub = g.buildHoleSubtree(rowVar, rowChildren, false, "", true, false)
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
	// Re-run each child component's `.set(propsObj)` so an
	// already-mounted component receives fresh prop values when
	// the row is reused (instead of being torn down and rebuilt,
	// which would drop focus, scroll position, and any internal
	// state).  This is the keystone of Session A: the row's
	// update is now structurally equivalent to its mount.  An
	// unmounted instance (slot positions that haven't opened
	// yet) is skipped by the `if (inst)` guard inside setStmt.
	for _, ci := range rowComps {
		if ci.setStmt != nil {
			updateBody = append(updateBody, cloneStmt(ci.setStmt))
		}
	}
	// Nested-block rows: re-run the nested chain/each init calls so
	// the row's dynamic content refreshes when the row is reused.
	updateBody = append(updateBody, nestedUpdate...)

	// Keep the fragment structured. Rendering fragBody/updateBody to text and parsing
	// it again drops SourceOrigin from row-local template expressions.
	generatedID := func(name string) JsExpr {
		return &JsIdent{baseExpr: baseExpr{genNode()}, Name: name}
	}
	destroyBody := append([]JsStmt{}, rowCleanup...)
	destroyBody = append(destroyBody, jsStmt(rowVar+".remove();"))
	fragBody = append(fragBody, &JsReturnStmt{
		baseStmt: baseStmt{genNode()},
		Arg: &JsObjectExpr{
			baseExpr: baseExpr{genNode()},
			Props: []*JsProp{
				{Key: generatedID("el"), Value: generatedID(rowVar)},
				{Key: generatedID("update"), Value: &JsFuncExpr{
					baseExpr: baseExpr{genNode()},
					Params:   []JsExpr{generatedID(itemVar), generatedID(indexVar)},
					Body:     &JsBlock{baseStmt: baseStmt{genNode()}, Body: updateBody},
				}},
				{Key: generatedID("destroy"), Value: &JsFuncExpr{
					baseExpr: baseExpr{genNode()},
					Body:     &JsBlock{baseStmt: baseStmt{genNode()}, Body: destroyBody},
				}},
			},
		},
	})

	fragDecl := &JsFuncDecl{
		baseStmt: baseStmt{genNode()},
		Name:     fragFn,
		Params:   []JsExpr{generatedID(itemVar), generatedID(indexVar)},
		Body:     &JsBlock{baseStmt: baseStmt{genNode()}, Body: fragBody},
	}

	// ── 3. Block storage ─────────────────────────────────────
	// ── 4. Reconciler ────────────────────────────────────────
	// The fragment builder, block storage, keymap, and reconciler
	// are persistent: the setter cascade re-invokes the reconciler
	// across the component's whole life.  Hoist them to component
	// scope when building inside a captured branch/slot scope.
	reconcilerDecl := jsStmt(g.eachReconciler(
		eachFn, fragFn, anchor, blocks, keymap, keyExpr, itemVar, indexVar))

	if captured {
		g.declare(fragDecl)
		if keyExpr != "" {
			g.declare(jsStmt("let " + keymap + " = null;"))
			g.body = append(g.body, jsStmt(keymap+" = new Map();"))
		}
		g.declare(jsStmt("let " + blocks + " = [];"))
		g.declare(reconcilerDecl)
		// Outer-branch teardown contribution.  When the {#if} (or
		// other chain) containing this {#each} switches off, the
		// chain only captures this each's anchor in its branch
		// handle — the rows live before the anchor and would be
		// orphaned in the DOM if the outer simply removed the
		// handle.  Emit a teardown snippet so the outer branch's
		// teardown destroys every row, clears the bookkeeping,
		// and nulls the anchor — matching the pattern used for
		// nested {#if} chains.  Same g.nestedTeardowns channel,
		// so a doubly-nested control-flow propagates naturally.
		var td strings.Builder
		td.WriteString(blocks + ".forEach(b => b.destroy()); ")
		td.WriteString(blocks + " = []; ")
		if keyExpr != "" {
			td.WriteString(keymap + " = null; ")
		}
		td.WriteString(eachFn + "._arr = undefined; ")
		td.WriteString(anchor + " = null; ")
		g.nestedTeardowns = append(g.nestedTeardowns, td.String())
	} else {
		g.body = append(g.body, fragDecl)
		if keyExpr != "" {
			g.body = append(g.body, jsStmt("const "+keymap+" = new Map();"))
		}
		g.body = append(g.body, jsStmt("let "+blocks+" = [];"))
		g.body = append(g.body, reconcilerDecl)
	}

	// ── 5. Wiring ────────────────────────────────────────────
	g.body = append(g.body, jsStmt(parentVar+".appendChild("+anchor+");"))
	arrayExpr := g.templateExpr(block.IterExpr)
	g.initBody = append(g.initBody, g.templateStmt(eachFn+"("+templateMarker(0)+");", arrayExpr))
	for _, dep := range templateExprDeps(block.IterExpr, array, g.reactiveNames()) {
		g.addUpdate(dep, g.templateStmt(eachFn+"("+templateMarker(0)+");", arrayExpr))
	}
	for dep := range rowAttrs.deps {
		g.addUpdate(dep, g.templateStmt(eachFn+"("+templateMarker(0)+");", arrayExpr))
	}
	// A nested-control-flow row body reads component state through
	// fragment-local chain functions the component setter cannot
	// reach.  Re-running the whole {#each} when such a dep changes
	// rebuilds every row, re-evaluating its nested chains.
	for dep := range rowBodyDeps {
		g.addUpdate(dep, g.templateStmt(eachFn+"("+templateMarker(0)+");", arrayExpr))
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
		// Unkeyed reconciler.  When the array reference is stable
		// AND the existing rows are still in the DOM, the
		// reconciler updates positionally: rows in the overlap
		// (min(oldLen, newLen)) are updated in place via
		// $$blk.update(item, i); surplus rows are destroyed from
		// the tail; new rows are appended for any growth.
		//
		// Positional update preserves DOM identity for the
		// overlapping prefix — focus, scroll position, native
		// selection, IME composition survive — and reuses child
		// component instances (their .set(propsObj) is replayed
		// by the row's update closure).  This is the unkeyed
		// each's correct semantics: it is INDEX-based, so a
		// middle-insertion shifts every subsequent row's content
		// (the user's mental model "this is row 1, it's about
		// item B" becomes "this is row 1, it's about item C")
		// but DOM nodes are preserved.  Users who want stable
		// row identity across middle inserts/removes should use
		// a keyed {#each} — see Session C.
		//
		// The DOM-liveness check (rows[0].el.isConnected) matters
		// when an enclosing {#if} branch was previously unmounted:
		// the chain removes the branch root, which detaches the
		// rows from the document but leaves their parent
		// reference intact — so a bare .parentNode check would
		// spuriously pass and we would update orphaned blocks.
		// .isConnected reports document-tree connectedness.
		//
		// When the reference or liveness check fails we fall
		// through to full destroy + rebuild — that is still
		// required for "items = items.concat(…)" style updates
		// (different array reference, even if contents overlap)
		// and for branch-reopen.
		return "function " + eachFn + "($$arr) {" +
			"  if (!" + anchor + ".parentNode) return;" +
			"  $$arr = $$arr || [];" +
			"  const $$sameRef = (" + eachFn + "._arr === $$arr);" +
			"  const $$stillLive = (" + blocks + ".length === 0 || " +
			blocks + "[0].el.isConnected);" +
			"  if ($$sameRef && $$stillLive) {" +
			"    const $$common = Math.min(" + blocks + ".length, $$arr.length);" +
			"    for (let $$i = 0; $$i < $$common; $$i++) {" +
			"      if (" + blocks + "[$$i].update) " + blocks + "[$$i].update($$arr[$$i], $$i);" +
			"    }" +
			"    if (" + blocks + ".length > $$arr.length) {" +
			"      for (let $$i = $$arr.length; $$i < " + blocks + ".length; $$i++) {" +
			"        " + blocks + "[$$i].destroy();" +
			"      }" +
			"      " + blocks + ".length = $$arr.length;" +
			"    } else if (" + blocks + ".length < $$arr.length) {" +
			"      for (let $$i = $$common; $$i < $$arr.length; $$i++) {" +
			"        const $$blk = " + fragFn + "($$arr[$$i], $$i);" +
			"        " + anchor + ".before($$blk.el);" +
			"        " + blocks + ".push($$blk);" +
			"      }" +
			"    }" +
			"    return;" +
			"  }" +
			"  " + eachFn + "._arr = $$arr;" +
			"  " + blocks + ".forEach(b => b.destroy());" +
			"  " + blocks + " = [];" +
			"  $$arr.forEach(function(" + itemVar + ", " + indexVar + ") {" +
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

	// When this {#await} is built inside another branch's build
	// closure, the state-var declarations and driver function
	// must escape that closure — declareInScope handles the
	// routing.  The initial driver call still goes to g.initBody
	// so it fires when the component mounts.
	nestedHoist := g.inCapturedScope()
	declAwait := func(s string) { g.declareInScope(jsStmt(s)) }

	// Component-scope storage.  State starts at 0 (pending).
	declAwait("let " + stateVar + " = 0;")
	declAwait("let " + valVar + " = undefined;")
	declAwait("let " + errVar + " = undefined;")
	declAwait("let " + cellVar + " = null;")

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
	driverStmt := g.templateStmt(
		"function "+driver+"() {"+
			"  const $$p = Promise.resolve("+templateMarker(0)+");"+
			"  if ("+cellVar+" === $$p) return;"+
			"  "+cellVar+" = $$p;"+
			"  "+stateVar+" = 0;"+
			"  "+valVar+" = undefined; "+errVar+" = undefined;"+
			"  "+chainFn+"();"+
			"  $$p.then("+
			"    v => { if ("+cellVar+" !== $$p) return; "+
			valVar+" = v; "+stateVar+" = 1; "+chainFn+"(); },"+
			"    e => { if ("+cellVar+" !== $$p) return; "+
			errVar+" = e; "+stateVar+" = 2; "+chainFn+"(); }"+
			"  );"+
			"}", g.templateExpr(block.Expr))

	// buildIfChain's last appended statement is the anchor's
	// appendChild — legacy emits the driver BEFORE that wiring
	// (driver goes in the create buffer, the appendChild in the
	// wire buffer).  Insert the driver just before that trailing
	// statement so the structural order matches.  When nested,
	// the chain function declaration was hoisted (no trailing
	// appendChild remained in g.body) and the driver itself must
	// also reach component scope so the cascade can find it.
	if nestedHoist {
		g.declare(driverStmt)
	} else if n := len(g.body); n > 0 {
		g.body = append(g.body[:n-1],
			append([]JsStmt{driverStmt}, g.body[n-1])...)
	} else {
		g.body = append(g.body, driverStmt)
	}

	// Run the driver at init.
	g.initBody = append(g.initBody, jsStmt(driver+"();"))

	// Re-run the driver when the promise expression's deps change.
	for _, dep := range templateExprDeps(block.Expr, stripBraces(block.PromiseExpr), g.reactiveNames()) {
		g.addUpdate(dep, jsStmt(driver+"();"))
	}
}

// buildIfChain emits an {#if}/{:else if}/{:else} chain (block 9).
//
// The structured builder owns lifecycle, source origins, and cleanup ordering.
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

// stmtsToText renders a slice of statements to minified JS text for slot factories,
// where closure bodies are still assembled from compiler-owned text. The chain
// emitter no longer uses it; statements produced
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

// destroyComps returns the teardown statements for a removed
// branch's child components: each instance is destroyed and its
// instance + wrapper variables nulled, so re-entering the branch
// recreates the component (the `if (!$$nN_comp)` mount guard
// would otherwise skip a still-live instance).
func destroyComps(comps []compInstance) string {
	var b strings.Builder
	for _, c := range comps {
		b.WriteString(" if (" + c.inst + ") { " + c.inst +
			".destroy(); " + c.inst + " = null; }")
		b.WriteString(" " + c.wrap + " = null;")
	}
	return b.String()
}

// buildIfChainStructured emits the Phase-1 BranchPlan path. Branch body
// statements remain AST nodes all the way into the chain function; only
// compiler-authored lifecycle plumbing is parsed from fixed strings.
func (g *ASTGen) buildIfChainStructured(chain *IfChainNode, parentVar string) string {
	anchor := g.id() + "_a"
	fn := "$$chain_" + g.id()
	type plan struct {
		build           []JsStmt
		init            []JsStmt
		captured        []string
		cleanup         []JsStmt
		comps           []compInstance
		nestedTeardowns []string
	}
	plans := make([]plan, len(chain.Branches))

	g.body = append(g.body, jsStmt("const "+anchor+" = document.createComment('if-chain');"))
	for i := range chain.Branches {
		g.body = append(g.body, jsStmt("let "+fn+"_b"+itoa(i)+" = null;"))
		g.body = append(g.body, jsStmt("let "+fn+"_b"+itoa(i)+"_cleanup = null;"))
	}
	g.body = append(g.body, jsStmt("let "+fn+"_running = false, "+fn+"_pending = false;"))
	if chain.KeyExpr != "" {
		g.body = append(g.body, jsStmt("let "+fn+"_keyprev = {};"))
	}

	for i, branch := range chain.Branches {
		bv := fn + "_b" + itoa(i)
		frag := bv + "_frag"
		if allStatic(branch.Children) {
			html := emitStaticHTML(branch.Children, g.scopeID)
			plans[i].build = []JsStmt{jsStmt("$$insertHTML(" + frag + ", '" + escapeJSString(html) + "');")}
			continue
		}
		if astHoleSubtreeOK(branch.Children) {
			sub := g.buildHoleSubtree(frag, branch.Children, true, bv, false, true)
			g.body = append(g.body, sub.decls...)
			plans[i].build = sub.build
			plans[i].init = sub.initUpdates
			plans[i].captured = sub.captured
			continue
		}

		// Dynamic element branches use the ordinary recursive DOM builder,
		// captured as a branch-local AST plan.  Its element references are
		// hoisted by inBranchScope so setter cascades can safely update a
		// mounted branch and no source-bearing expression is serialized.
		tdMark := len(g.nestedTeardowns)
		wasBranchScope := g.inBranchScope
		cap := g.captureScope(func() {
			g.inBranchScope = true
			defer func() { g.inBranchScope = wasBranchScope }()
			g.buildChildRun(branch.Children, frag)
		})
		plans[i].build = append(cap.body, cap.events...)
		plans[i].init = cap.initBody
		plans[i].cleanup = cap.cleanup
		plans[i].comps = cap.comps
		plans[i].nestedTeardowns = append([]string{}, g.nestedTeardowns[tdMark:]...)
		g.fragHoisted = append(g.fragHoisted, cap.fragHoisted...)
		g.compInstances = append(g.compInstances, cap.comps...)
		for dep, stmts := range cap.updates {
			for _, stmt := range stmts {
				g.addUpdate(dep, guardedTemplateStmt(stmt, bv))
			}
		}
	}

	tryBody := []JsStmt{}
	if chain.KeyExpr != "" {
		keyNew := fn + "_keynew"
		tryBody = append(tryBody, g.templateStmt("const "+keyNew+" = "+templateMarker(0)+";", g.templateExpr(chain.KeyAST)))
		for i := range chain.Branches {
			bv := fn + "_b" + itoa(i)
			tryBody = append(tryBody, jsStmt("if ("+fn+"_keyprev !== "+keyNew+" && "+bv+") { for (const $$node of "+bv+") $$node.remove(); "+bv+" = null;"+nullCaptured(plans[i].captured)+destroyComps(plans[i].comps)+" }"))
		}
		tryBody = append(tryBody, jsStmt(fn+"_keyprev = "+keyNew+";"))
	}
	tryBody = append(tryBody, jsStmt("let $$active = -1;"))
	for i, branch := range chain.Branches {
		if branch.Cond == "" {
			tryBody = append(tryBody, jsStmt("if ($$active < 0) $$active = "+itoa(i)+";"))
			continue
		}
		if branch.Expr == nil {
			// Await lowering introduces synthetic state comparisons. Build the
			// active-selector AST directly; it must retain the exact $$active name.
			test := &JsLogicalExpr{baseExpr: baseExpr{genNode()}, Op: "&&", L: &JsBinaryExpr{baseExpr: baseExpr{genNode()}, Op: "<", L: id("$$active"), R: jsExpr("0")}, R: jsExpr(branch.Cond)}
			assign := &JsExprStmt{baseStmt: baseStmt{genNode()}, X: &JsAssignExpr{baseExpr: baseExpr{genNode()}, Op: "=", L: id("$$active"), R: jsExpr(itoa(i))}}
			tryBody = append(tryBody, &JsIfStmt{baseStmt: baseStmt{genNode()}, Test: test, Then: assign})
			continue
		}
		test := &JsLogicalExpr{baseExpr: baseExpr{genNode()}, Op: "&&", L: &JsBinaryExpr{baseExpr: baseExpr{genNode()}, Op: "<", L: id("$" + "$active"), R: jsExpr("0")}, R: g.templateExpr(branch.Expr)}
		assign := &JsExprStmt{baseStmt: baseStmt{genNode()}, X: &JsAssignExpr{baseExpr: baseExpr{genNode()}, Op: "=", L: id("$" + "$active"), R: jsExpr(itoa(i))}}
		tryBody = append(tryBody, &JsIfStmt{baseStmt: baseStmt{genNode()}, Test: test, Then: assign})
	}
	for i := range chain.Branches {
		bv := fn + "_b" + itoa(i)
		cleanupVar := bv + "_cleanup"
		cleanup := ""
		if len(plans[i].cleanup) > 0 {
			cleanup = "if (" + cleanupVar + ") { " + cleanupVar + "(); " + cleanupVar + " = null; } "
		}
		teardown := "if ($$active !== " + itoa(i) + " && " + bv + ") { " +
			cleanup + "for (const $$node of " + bv + ") $$node.remove(); " + bv + " = null;" +
			nullCaptured(plans[i].captured) + destroyComps(plans[i].comps) + strings.Join(plans[i].nestedTeardowns, " ") + " }"
		tryBody = append(tryBody, jsStmt(teardown))
	}
	for i := range chain.Branches {
		bv := fn + "_b" + itoa(i)
		frag := bv + "_frag"
		mountBody := []JsStmt{jsStmt("const " + frag + " = document.createDocumentFragment();")}
		mountBody = append(mountBody, plans[i].build...)
		if len(plans[i].cleanup) > 0 {
			mountBody = append(mountBody, &JsExprStmt{
				baseStmt: baseStmt{genNode()},
				X: &JsAssignExpr{
					baseExpr: baseExpr{genNode()}, Op: "=", L: id(bv + "_cleanup"),
					R: &JsArrowFunc{baseExpr: baseExpr{genNode()}, Body: &JsBlock{baseStmt: baseStmt{genNode()}, Body: plans[i].cleanup}},
				},
			})
		}
		mountBody = append(mountBody,
			jsStmt(bv+" = [..."+frag+".childNodes];"),
			jsStmt(anchor+".before("+frag+");"))
		activeBody := []JsStmt{&JsIfStmt{
			baseStmt: baseStmt{genNode()},
			Test:     jsExpr("!" + bv),
			Then:     &JsBlock{baseStmt: baseStmt{genNode()}, Body: mountBody},
			Else: jsStmt("if (" + anchor + ".parentNode && " + bv +
				".length && " + bv + "[0].parentNode !== " + anchor +
				".parentNode) { for (const $$node of " + bv + ") " + anchor + ".before($$node); }"),
		}}
		activeBody = append(activeBody, plans[i].init...)
		tryBody = append(tryBody, &JsIfStmt{
			baseStmt: baseStmt{genNode()},
			Test:     jsExpr("$$active === " + itoa(i)),
			Then:     &JsBlock{baseStmt: baseStmt{genNode()}, Body: activeBody},
		})
	}
	finallyBody := jsStmts(fn + "_running = false; if (" + fn + "_pending) { " + fn + "_pending = false; " + fn + "(); }")
	body := []JsStmt{
		jsStmt("if (" + fn + "_running) { " + fn + "_pending = true; return; }"),
		jsStmt(fn + "_running = true;"),
		&JsTryStmt{baseStmt: baseStmt{genNode()}, Block: &JsBlock{baseStmt: baseStmt{genNode()}, Body: tryBody}, Finalizer: &JsBlock{baseStmt: baseStmt{genNode()}, Body: finallyBody}},
	}
	g.body = append(g.body, &JsFuncDecl{baseStmt: baseStmt{genNode()}, Name: fn, Body: &JsBlock{baseStmt: baseStmt{genNode()}, Body: body}})
	g.body = append(g.body, jsStmt(parentVar+".appendChild("+anchor+");"))
	g.initBody = append(g.initBody, jsStmt(fn+"();"))
	deps := map[string]bool{}
	for _, branch := range chain.Branches {
		for _, dep := range templateExprDeps(branch.Expr, stripBraces(branch.Cond), g.reactiveNames()) {
			deps[dep] = true
		}
	}
	if chain.KeyExpr != "" {
		for _, dep := range templateExprDeps(chain.KeyAST, stripBraces(chain.KeyExpr), g.reactiveNames()) {
			deps[dep] = true
		}
	}
	for dep := range deps {
		if g.fragCounter >= 0 {
			g.addUpdate(dep, jsStmt(fn+"();"))
		} else {
			g.addStructuralUpdate(dep, jsStmt(fn+"();"))
		}
	}
	return fn
}
func (g *ASTGen) buildIfChain(chain *IfChainNode, parentVar string) string {
	return g.buildIfChainStructured(chain, parentVar)
}

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

	// When building inside a captured scope (an {#if} branch via
	// the recursive walker, or a slot closure), the element var
	// must be hoisted: a `const` in the branch-build / slot-closure
	// block is invisible to the component-scope setter cascade that
	// replays the element's reactive attribute updates.  Emit a
	// hoisted `let X = null;` plus an assignment in the body.
	if g.inCapturedScope() {
		g.hoisted = append(g.hoisted, jsStmt("let "+varName+" = null;"))
		g.body = append(g.body, jsStmt(
			varName+" = document.createElement('"+el.Tag+"');"))
	} else {
		g.body = append(g.body, g.createElementStmt(varName, el.Tag))
	}
	g.body = append(g.body, g.setAttrStmt(varName, g.scopeID, ""))
	g.applyElementAttrs(varName, el)

	g.buildChildren(el, varName)

	if !isRoot {
		g.body = append(g.body, jsStmt(parentVar+".appendChild("+varName+");"))
	}
}

// buildChildren emits an element's children using the same
// strategy ladder as legacy walkElement:
//  1. fully-static element subtree → innerHTML
//  2. hole-compatible (static frame, text holes) → buildHoleSubtree
//  3. otherwise → recurse per child, batching static runs
func (g *ASTGen) buildChildren(el *ElementNode, varName string) {
	children := el.Children
	if len(children) == 0 {
		return
	}
	isTemplate := strings.EqualFold(el.Tag, "template")

	// Raw-text elements (<textarea>, <title>) have a CDATA content
	// model: their body is not parsed as HTML, so the innerHTML +
	// TreeWalker hole mechanism produces a null anchor and crashes
	// ("$$nN_a is null").  Compile their body to the reactive DOM
	// property instead (textarea→value, title→textContent).  This
	// also handles the case nested inside an {#if} branch, where
	// the element is built via this recursive path.
	if prop, ok := rawTextProp(el.Tag); ok {
		if expr, ok := singleInterpolation(children); ok {
			writeSrc := varName + "." + prop + " = String(" + expr + ");"
			g.initBody = append(g.initBody, jsStmt(writeSrc))
			for _, dep := range findDeps(expr, g.reactiveNames()) {
				g.addUpdate(dep, jsStmt(writeSrc))
			}
			return
		}
		if text, ok := staticRawText(children); ok {
			g.body = append(g.body, jsStmt(
				varName+"."+prop+" = '"+escapeJSString(text)+"';"))
			return
		}
	}

	switch {
	case !isTemplate && hasElement(children) && allStatic(children):
		g.body = append(g.body,
			g.innerHTMLStmt(varName, emitStaticHTML(children, g.scopeID)))

	case !isTemplate && astHoleSubtreeOK(children):
		if g.inSlotScope || g.inBranchScope {
			// Inside a slot closure OR an {#if}/{#key}/{#await}
			// branch built by the recursive walker: the captured
			// hole-node vars must be hoisted to component scope
			// (decls → g.hoisted) so the setter cascade — which
			// runs at component scope — can reach them.  A var left
			// as a `const` in the branch/slot build block would be
			// invisible there (ReferenceError: $$nN is not defined).
			// Each hole's update is guarded by its own ref var
			// ($$self → the per-node ref), null until the node is
			// built, so a cascade replay before the branch mounts
			// or the slot opens is a safe no-op.
			sub := g.buildHoleSubtree(varName, children, true,
				"$$self", false, false)
			g.hoisted = append(g.hoisted, sub.decls...)
			g.body = append(g.body, sub.build...)
			g.initBody = append(g.initBody, sub.initUpdates...)
		} else {
			sub := g.buildHoleSubtree(varName, children, false, "", false, false)
			g.body = append(g.body, sub.build...)
			g.initBody = append(g.initBody, sub.initUpdates...)
		}

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
	for _, dep := range templatePartsDeps(tn.Parts, g.reactiveNames()) {
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

	// Keep a text-only fallback on the structured template-expression path.
	// Besides avoiding a second JS-expression serializer, this preserves the
	// original template location for its source-map segment.
	if len(slot.Fallback) == 1 {
		if text, ok := slot.Fallback[0].(*TextNode); ok {
			slotFactory := "$" + "$sf"
			slotNodes := "$" + "$sn"
			slots := "$" + "$slots"
			src := "{ const " + slotFactory + " = " + slots + "[" + jsonQuote(slot.Name) + "]; " +
				"if (" + slotFactory + ") { const " + slotNodes + " = " + slotFactory + "(); " +
				"if (" + slotNodes + ") " + anchor + ".before(" + slotNodes + "); } else { " +
				anchor + ".before(document.createTextNode(" + templateMarker(0) + ")); } }"
			g.initBody = append(g.initBody, g.templateStmt(src, g.textValueExpr(text.Parts)))
			return
		}
	}

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
	g.compInstances = append(g.compInstances,
		compInstance{inst: inst, wrap: wrap})

	// Build the $$slots object — one factory closure per slot.
	// Each slot's content is built by the recursive walker into a
	// captured local scope (captureScope), then assembled into a
	// fragment-factory closure: first call builds a DocumentFragment
	// and caches its children; a reopen re-adopts the cached nodes.
	slotsObj := g.buildSlotClosures(comp)

	// Wrap and instance variables.  The right declaration scope
	// depends on where this component invocation sits:
	//
	//   - inside an {#if} inside an {#each} (inFragment +
	//     branch): fragHoisted, so each row gets its own var AND
	//     the var survives the branch closure;
	//   - inside an {#each} row only (inFragment, no branch):
	//     g.body — which is the row fragment closure, so each
	//     row gets its own var naturally;
	//   - top-level or non-fragment branch: g.hoisted (component
	//     scope) — every reference site (mount, setStmt cascade,
	//     destroy) needs to see them, and a local `let` declared
	//     in a branch closure would be invisible on reopen/
	//     rebuild and unassignable when the branch-removal
	//     cleanup nulls it.
	//
	// "Captured" means inBranchScope OR inSlotScope: both place
	// the current g.body inside a closure (a branch build block,
	// or a slot factory) whose declarations would be block-
	// scoped to that closure.  An inner-slot component is the
	// motivating case for slot scope: `<Wrapper><Child v={it.v}
	// /></Wrapper>` inside an {#each} needs Child's instance var
	// to live at row-fragment scope so the row's update closure
	// can replay .set() on it.  Without this, the slot's
	// factory rebuilds Child each invocation (or fails to plumb
	// the reactive prop at all).
	inFragment := g.fragCounter >= 0
	switch {
	case inFragment && g.inCapturedScope():
		g.fragHoisted = append(g.fragHoisted, jsStmt("let "+wrap+" = null;"))
		g.fragHoisted = append(g.fragHoisted, jsStmt("let "+inst+" = null;"))
	case inFragment:
		g.body = append(g.body, jsStmt("let "+wrap+" = null;"))
		g.body = append(g.body, jsStmt("let "+inst+" = null;"))
	default:
		g.hoisted = append(g.hoisted, jsStmt("let "+wrap+" = null;"))
		g.hoisted = append(g.hoisted, jsStmt("let "+inst+" = null;"))
	}
	// The wrap variable now holds an ANCHOR COMMENT — a
	// Node.COMMENT_NODE that marks the insertion position in
	// the parent's DOM.  Previously this was a
	// `<div style="display:contents">`, which fixed layout but
	// still appeared in the DOM as an element, breaking direct-
	// child CSS selectors like `.parent > .child`.  Comments
	// are not elements, so `>` selectors and `:nth-child` skip
	// them entirely — the component's $$root becomes a true
	// direct child of the parent.
	g.body = append(g.body, jsStmt(
		wrap+" = document.createComment('comp');"))
	g.body = append(g.body, jsStmt(parentVar+".appendChild("+wrap+");"))

	propsExpr := g.componentPropsExpr(comp.Attrs)

	// Initial mount — keep dynamic props as cached template AST expressions.
	g.initBody = append(g.initBody, g.templateStmt(
		"if (!"+inst+") "+inst+" = "+comp.Name+"("+wrap+", "+templateMarker(0)+", "+slotsObj+");", propsExpr))

	// Reactive prop updates.  The .set() statement propagates new
	// prop values to an already-mounted instance — both via the
	// component-scope setter cascade (addUpdate below) AND, when
	// this component sits inside an {#each} row, via the row's
	// update closure (captureScope picks compInstances up and the
	// fragment builder replays each instance's setStmt so a row
	// reuse re-sets the child's props rather than tearing down).
	setStmt := g.templateStmt("if ("+inst+") "+inst+".set("+templateMarker(0)+");", propsExpr)
	// Back-fill the bookkeeping entry pushed above so the
	// fragment-update path can find this statement.  The entry
	// is normally the last pushed (the recursive walker hasn't
	// added anything else between the push and here), but a
	// component with a slot may emit OTHER component instances
	// during its slot's captureScope re-publish (an inner-slot
	// <Child> bubbles its comp entry up to this scope so the
	// row's update closure can replay its .set()).  Search
	// backwards by inst name so a later entry doesn't shadow
	// this component's own bookkeeping.
	for i := len(g.compInstances) - 1; i >= 0; i-- {
		if g.compInstances[i].inst == inst {
			g.compInstances[i].setStmt = setStmt
			break
		}
	}
	deps := map[string]bool{}
	for _, attr := range comp.Attrs {
		if attr.IsEvent || attr.IsBindThis {
			continue
		}
		for _, d := range templateExprDeps(attr.Expr, attrExprToJS(attr.Value), g.reactiveNames()) {
			deps[d] = true
		}
	}
	for d := range deps {
		g.addUpdate(d, setStmt)
	}

	// Component @event handlers.  A child component dispatches a
	// CustomEvent on its wrapper div; the parent listens there.
	// The handler is guarded with `instanceof CustomEvent` so a
	// native DOM event of the same name (e.g. a built-in 'select'
	// /'change' bubbling up) is not mishandled as a component
	// event.  This wiring was missing — component events silently
	// did nothing.
	g.buildComponentEvents(comp, inst)

	// Teardown.
	g.cleanup = append(g.cleanup, jsStmt(
		"if ("+inst+") "+inst+".destroy();"))
}

// buildComponentEvents wires every @event attribute of a component
// invocation: a CustomEvent-only filter handler is hoisted to
// component scope, the listener is attached to the component's
// $$root (inst.el) AFTER mount, and a matching removeEventListener
// goes on g.cleanup for branch-teardown unmount + the component-
// destroy lifecycle.
//
// Why inst.el rather than a parent-side wrapper: the bug-4 fix
// removed the parent-side wrapper div (it broke direct-child CSS
// selectors).  The component dispatches CustomEvents on $$root
// (see the $$dispatch shim — it captures $$root, not $$target);
// listening on inst.el ALWAYS receives those events.  The CustomEvent
// is also marked `bubbles: true`, so listening on an ancestor would
// work too, but inst.el keeps the listener scope tight: a sibling
// component's events of the same name reach a different inst.el and
// don't cross-fire.
//
// Why g.initBody (not g.events): the listener target inst.el does
// not exist until after the mount call.  g.events runs BEFORE
// g.initBody (handlers must be in scope when initial render fires
// their refs), so attaching listeners there would `addEventListener`
// on null.  Emitting AFTER the mount call in g.initBody is the
// natural fit — the captured scope's order is preserved (in a
// branch, plans[i].initText holds these statements after mount).
func (g *ASTGen) buildComponentEvents(comp *ComponentNode, inst string) {
	for _, attr := range comp.Attrs {
		if !attr.IsEvent {
			continue
		}
		hv := inst + "_" + sanitizeIdent(attr.Name) + "_ch"
		handler := g.rewriteHandler(attr.Value)
		eventName := strings.TrimPrefix(attr.Name, "on:")
		// The guard wrapper — only forward CustomEvents.  Hoisted
		// to component scope: when the component is in a slot, the
		// addEventListener call runs on every slot reopen but the
		// handler const is declared once; a `let`/`const` trapped
		// in the slot closure's first-open path would be undefined
		// on reopen.
		handlerDecl := g.templateStmt("const "+hv+" = ($e) => { if ($e instanceof CustomEvent) { ("+templateMarker(0)+")($e); } };", g.rewrittenTemplateExpr(attr.Expr, handler))
		if g.fragCounter >= 0 {
			g.body = append(g.body, handlerDecl)
		} else {
			g.hoisted = append(g.hoisted, handlerDecl)
		}
		listener := jsStmt(inst + ".el.addEventListener('" + eventName + "', " + hv + ");")
		if g.fragCounter >= 0 {
			// Row update() replays initBody; subscriptions attach only at mount.
			g.events = append(g.events, listener)
		} else {
			g.initBody = append(g.initBody, listener)
		}
		g.cleanup = append(g.cleanup, jsStmt(
			"if ("+inst+") "+inst+".el.removeEventListener('"+eventName+"', "+hv+");"))
	}
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
		// inSlotScope makes buildChildren hoist hole-node vars to
		// component scope so the setter cascade can reach them;
		// slotGuard guards those updates until the slot first opens.
		cap := g.captureScope(func() {
			savedSlotScope, savedSlotGuard := g.inSlotScope, g.slotGuard
			g.inSlotScope = true
			g.slotGuard = cacheVar
			defer func() {
				g.inSlotScope, g.slotGuard = savedSlotScope, savedSlotGuard
			}()
			g.buildChildRun(slotNodes, fragVar)
		})
		// fragHoisted entries collected while building the slot
		// content (component instance vars, chain functions
		// emitted by nested {#if}/{#each}/{#await}) must reach
		// the OUTER row-fragment scope, not stay trapped in the
		// slot factory.  The motivating case: an inner-slot
		// component `<Wrapper><Child v={it.v}/></Wrapper>` inside
		// an {#each} — Child's instance var has to be reachable
		// from the row's update closure (Session A's rowComps
		// replay) so .set({v: it.v}) fires on each row tick.
		// Bubbling these to g.fragHoisted lands them at the row
		// fragment top, which is the closure all of: slot
		// factory, chain functions inside the slot, AND the row
		// update closure share.  When there is no enclosing
		// fragment (the slot is at component scope), the bubble
		// is a no-op consumption — declare picks g.hoisted
		// instead, also reachable by everything.
		for _, s := range cap.fragHoisted {
			g.declare(s)
		}
		// Component instances built inside the slot must be
		// re-published to the enclosing scope's compInstances so
		// the row-update mechanism (Session A's rowComps replay)
		// fires .set() on them when the row's item changes.
		// Without this, an inner-slot component receives no
		// reactive prop updates after its initial mount.  The
		// slot factory caches its output, so the component
		// instance isn't rebuilt on subsequent slot invocations —
		// the only path for it to receive new props is the
		// replay.  destroyComps still fires on slot teardown via
		// the cleanup path; this re-publish only extends the
		// component's reactive-prop reach, not its lifetime.
		g.compInstances = append(g.compInstances, cap.comps...)

		buildText := stmtsToText(cap.body)
		// Event listeners and initial-render statements emitted by
		// slot content (e.g. a component @click handler, or a
		// child component's init call) must run inside the closure
		// too — they were being dropped, so components in a slot
		// had no event wiring.
		initText := stmtsToText(append(append([]JsStmt{},
			cap.events...), cap.initBody...))

		// Teardown emitted by slot content (component .destroy(),
		// removeEventListener) runs at component scope.
		g.cleanup = append(g.cleanup, cap.cleanup...)

		// Reactive updates emitted by slot content — a {count}
		// hole inside the slot updates the same component state as
		// anywhere else, so its update statements must join the
		// component's setter cascade.  Without this the slot's
		// holes never re-render: the setter changes the variable
		// but no DOM write replays.  The hole node vars these
		// statements reference are hoisted + null-guarded (see
		// buildHoleSubtree's slot-mode handling), so an update
		// that fires before the slot first opens is a safe no-op.
		for dep, stmts := range cap.updates {
			for _, s := range stmts {
				g.addUpdate(dep, s)
			}
		}

		// The cache var stores the slot's previously-built nodes
		// so a re-open can reuse them.  It is read from the slot
		// factory closure (stored on the child component's
		// props, invoked from inside the child's own component
		// body) — that reference lives outside the current
		// emission scope.  When this <Component> is itself
		// inside a captured scope (an {#if} branch or another
		// slot), a plain `let` in g.body would be block-scoped
		// to that closure and invisible from the factory.
		// declareInScope routes via g.declare in that case,
		// landing the var at the right enclosing scope.
		g.declareInScope(jsStmt("let " + cacheVar + " = null;"))

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
	stmt := g.classDirStmtAST(varName, attr)
	g.initBody = append(g.initBody, stmt)
	for _, dep := range templateExprDeps(attr.Expr, attrExprToJS(attr.Value), g.reactiveNames()) {
		g.addUpdate(dep, stmt)
	}
}

// emitStyleDir wires `style:foo={expr}` — set an inline CSS
// property via style.setProperty (handles hyphenated names and
// custom properties).  Runs at initial render and on every change
// of a reactive dependency.
func (g *ASTGen) emitStyleDir(varName string, attr *Attr) {
	stmt := g.styleDirStmtAST(varName, attr)
	g.initBody = append(g.initBody, stmt)
	for _, dep := range templateExprDeps(attr.Expr, attrExprToJS(attr.Value), g.reactiveNames()) {
		g.addUpdate(dep, stmt)
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
	handlerExpr := g.rewrittenTemplateExpr(attr.Expr, handler)
	addCleanup := !g.inSlotScope
	if isSimpleIdent(handler) {
		g.events = append(g.events, g.templateStmt(varName+".addEventListener('"+attr.Name+"', "+templateMarker(0)+");", handlerExpr))
		if addCleanup {
			g.cleanup = append(g.cleanup, jsStmt(varName+".removeEventListener('"+attr.Name+"', "+handler+");"))
		}
		return
	}
	hv := varName + "_" + attr.Name + "_h"
	decl := g.templateStmt("const "+hv+" = "+templateMarker(0)+";", handlerExpr)
	if !isHandlerExpr(handler) {
		decl = g.templateStmt("const "+hv+" = ($event) => { "+templateMarker(0)+"; };", handlerExpr)
	}
	g.body = append(g.body, decl)
	g.events = append(g.events, jsStmt(varName+".addEventListener('"+attr.Name+"', "+hv+");"))
	if addCleanup {
		g.cleanup = append(g.cleanup, jsStmt(varName+".removeEventListener('"+attr.Name+"', "+hv+");"))
	}
}

// emitBinding wires a bind:value / bind:checked attribute — both
// directions:
//   - DOM→state: an input/change listener calling $$set_X
//   - state→DOM: an initial-render write + an update registered
//     against X so the setter re-writes the DOM
func (g *ASTGen) emitBinding(varName, tag string, attr *Attr) {
	spec, ok := twoWayBindSpec(tag, attr.Name)
	sv := stripBraces(attr.Value)
	if ok && g.analysis != nil && g.analysis.isStateName(sv) {
		hv := varName + "_bind_" + spec.event + "_h"
		g.body = append(g.body, jsStmt("const "+hv+" = ($e) => $$set_"+sv+"($e.target."+spec.readProp+");"))
		g.events = append(g.events, jsStmt(varName+".addEventListener('"+spec.event+"', "+hv+");"))
		g.cleanup = append(g.cleanup, jsStmt(varName+".removeEventListener('"+spec.event+"', "+hv+");"))
	}
	write := g.attrWriteStmt(varName, attr)
	g.initBody = append(g.initBody, write)
	for _, dep := range templateExprDeps(attr.Expr, stripBraces(attr.Value), g.reactiveNames()) {
		g.addUpdate(dep, write)
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

// fillHTMLStmts populates targetVar with parsed HTML in a way that
// works whether targetVar is an Element OR a DocumentFragment.
// A DocumentFragment has no innerHTML setter, so the HTML is parsed
// through a <template> element and its .content appended.  The
// resulting child nodes (and the <!--$--> anchors a TreeWalker
// later visits) land directly under targetVar either way.
func (g *ASTGen) fillHTMLStmts(targetVar, html string) []JsStmt {
	tpl := targetVar + "_tpl"
	return []JsStmt{
		jsStmt("const " + tpl +
			" = document.createElement('template');"),
		jsStmt(tpl + ".innerHTML = '" + escapeJSString(html) + "';"),
		jsStmt(targetVar + ".appendChild(" + tpl + ".content);"),
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
//  1. effect cleanups        ($$cleanupN)
//  2. onMount cleanup fns     ($$mountCleanupN)
//  3. onDestroy callback bodies, inline
//  4. event-listener removal  (g.cleanup)
//  5. $$root.remove()
//
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
	//    matching the legacy generator).  Skipped in external-CSS
	//    mode — no runtime <style> was injected.
	if g.hasStyle() && !g.cssExternal {
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

	// The `el` field exposes the component's $$root to its
	// caller so the caller can attach event listeners and read
	// position information.  Required by bug-4's wrapper-less
	// mount: the parent's @event listeners now target inst.el
	// (instead of a wrapper div the parent itself controls).
	elProp := &JsProp{
		baseNode: genNode(),
		Kind:     "init",
		Key:      id("el"),
		Value:    id("$$root"),
	}

	props := []*JsProp{elProp, destroyProp}

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
