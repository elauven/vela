package main

// astgen.go — the AST-based code generator (Phase C).
//
// This is the replacement for codegen.go's string-concatenation
// approach.  Instead of writing JS text into buffers, it constructs
// a JsProgram tree and hands it to the printer (jsprinter.go).
//
// The historical differential fixtures remain as validation coverage, but
// production compilation constructs this AST directly and never reparses
// generated JavaScript.
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
// ALL 14 BLOCKS MIGRATED. Production compilation is direct AST-only;
// astCanBuildDirectly remains temporarily as migration-test classification
// until the legacy generator is removed entirely.

import (
	"sort"
	"strings"
)

// ─────────────────────────────────────────────
//  AST construction helpers
// ─────────────────────────────────────────────
//
// Hand-building AST nodes is verbose, so these helpers cover the shapes
// astgen reaches for most often. Every compiler-authored construct is made
// through typed nodes; user-authored <script> enters separately through the
// cached source-ordered ScriptPlan. Everything constructed here is marked
// OriginGenerated.

// gen marks a freshly built node as compiler-generated.
func genNode() baseNode { return baseNode{Origin: OriginGenerated} }

// id builds an identifier.
func id(name string) *JsIdent {
	return &JsIdent{baseExpr: baseExpr{genNode()}, Name: name}
}

// strLit builds a decoded compiler-generated string literal. The printer
// owns JavaScript escaping, so callers always pass the semantic string value.
func strLit(value string) *JsLiteral {
	return &JsLiteral{
		baseExpr: baseExpr{genNode()},
		Kind:     "string",
		Value:    value,
		Decoded:  true,
	}
}

func genMember(obj JsExpr, prop string) *JsMemberExpr {
	return &JsMemberExpr{baseExpr: baseExpr{genNode()}, Obj: obj, Prop: id(prop)}
}

func genCall(callee JsExpr, args ...JsExpr) *JsCallExpr {
	return &JsCallExpr{baseExpr: baseExpr{genNode()}, Callee: callee, Args: args}
}

func genExprStmt(expr JsExpr) *JsExprStmt {
	return &JsExprStmt{baseStmt: baseStmt{genNode()}, X: expr}
}

func genAssignStmt(left, right JsExpr) *JsExprStmt {
	return genExprStmt(&JsAssignExpr{baseExpr: baseExpr{genNode()}, Op: "=", L: left, R: right})
}

func genVar(kind, name string, init JsExpr) *JsVarDecl {
	return &JsVarDecl{baseStmt: baseStmt{genNode()}, Kind: kind, Decls: []*JsVarDeclarator{{Id: id(name), Init: init}}}
}

func genConst(name string, init JsExpr) *JsVarDecl { return genVar("const", name, init) }

func genLet(name string, init JsExpr) *JsVarDecl { return genVar("let", name, init) }

func genInvoke(name string, args ...JsExpr) *JsExprStmt {
	return genExprStmt(genCall(id(name), args...))
}

func genBinary(op string, left, right JsExpr) *JsBinaryExpr {
	return &JsBinaryExpr{baseExpr: baseExpr{genNode()}, Op: op, L: left, R: right}
}

func genIf(test JsExpr, then JsStmt) *JsIfStmt {
	return &JsIfStmt{baseStmt: baseStmt{genNode()}, Test: test, Then: then}
}

func genNull() *JsLiteral {
	return &JsLiteral{baseExpr: baseExpr{genNode()}, Raw: "null", Kind: "null"}
}

func numLit(value int) *JsLiteral {
	return &JsLiteral{baseExpr: baseExpr{genNode()}, Raw: itoa(value), Kind: "number"}
}

func boolLit(value bool) *JsLiteral {
	if value {
		return &JsLiteral{baseExpr: baseExpr{genNode()}, Raw: "true", Kind: "boolean"}
	}
	return &JsLiteral{baseExpr: baseExpr{genNode()}, Raw: "false", Kind: "boolean"}
}

func genComputedMember(obj, prop JsExpr) *JsMemberExpr {
	return &JsMemberExpr{baseExpr: baseExpr{genNode()}, Obj: obj, Prop: prop, Computed: true}
}

func genLogical(op string, left, right JsExpr) *JsLogicalExpr {
	return &JsLogicalExpr{baseExpr: baseExpr{genNode()}, Op: op, L: left, R: right}
}

func genUnary(op string, expr JsExpr) *JsUnaryExpr {
	return &JsUnaryExpr{baseExpr: baseExpr{genNode()}, Op: op, Prefix: true, X: expr}
}

func genNew(callee JsExpr, args ...JsExpr) *JsNewExpr {
	return &JsNewExpr{baseExpr: baseExpr{genNode()}, Callee: callee, Args: args}
}

func genArray(elems ...JsExpr) *JsArrayExpr {
	return &JsArrayExpr{baseExpr: baseExpr{genNode()}, Elems: elems}
}

func genBlock(body ...JsStmt) *JsBlock {
	return &JsBlock{baseStmt: baseStmt{genNode()}, Body: body}
}

func genReturn(arg JsExpr) *JsReturnStmt {
	return &JsReturnStmt{baseStmt: baseStmt{genNode()}, Arg: arg}
}

func genFor(init JsStmt, test, update JsExpr, body JsStmt) *JsForStmt {
	return &JsForStmt{baseStmt: baseStmt{genNode()}, Init: init, Test: test, Update: update, Body: body}
}

func genForOf(name string, iterable JsExpr, body JsStmt) *JsForInStmt {
	return &JsForInStmt{
		baseStmt: baseStmt{genNode()},
		Left:     genConst(name, nil),
		Right:    iterable,
		Body:     body,
		IsOf:     true,
	}
}

// generatedMemberPath turns a compiler-owned dotted path into a member AST.
// It must not receive user JavaScript.
func generatedMemberPath(path string) JsExpr {
	parts := strings.Split(path, ".")
	if len(parts) == 0 || parts[0] == "" {
		panic("empty compiler-generated member path")
	}
	var expr JsExpr = id(parts[0])
	for _, part := range parts[1:] {
		if part == "" {
			panic("invalid compiler-generated member path: " + path)
		}
		expr = genMember(expr, part)
	}
	return expr
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

	// domAcquireMode is the structural DOM acquisition policy. CSR always
	// creates nodes; the future hydrator walks matching server markers. Both
	// modes are driven by domAcquirePlan, assembled in this same template walk.
	domAcquireMode DOMAcquireMode
	domAcquirePlan DOMAcquirePlan

	// hydrationMarkers bind every acquired template marker to the canonical SSR
	// render-plan ID. Inactive structural alternatives still reserve IDs, so
	// hydration must not reproduce dynamic traversal order with a local counter.
	hydrationMarkers SSRHydrationMarkerIDs
	// hydrationLifecyclePlan is the typed ownership graph for this component.
	// During the incremental migration each hydration lowering site verifies its
	// matching operation here before emitted JS acquires server DOM.
	hydrationLifecyclePlan *SSRStructuralLifecyclePlan

	// hydrationKeyedRows records that the narrow keyed-#each hydration
	// lowering is present.  It keeps the helper import decision structural:
	// generated calls never need to be discovered by parsing printed JS.
	hydrationKeyedRows bool

	// hydrationStateInput enables the compiler-owned $$velaState input for
	// SSR client modules only. Ordinary CSR output keeps its established
	// initialization path and pays no additional state-restore work.
	hydrationStateInput bool

	// hydrationComponents maps local Vela component bindings to their
	// marker-aware hydrate entries. It is populated only for SSR hydration
	// clients; normal CSR components retain their ordinary factory imports.
	hydrationComponents map[string]bool

	// hydrationCSRComponents maps components which can be materialized only
	// after hydration (inside a supported structural branch) to a distinct
	// ordinary CSR factory binding. The hydrate entry and CSR factory must
	// never share the same local binding.
	hydrationCSRComponents map[string]string

	// hydrationComponentInstanceSuffixes carries the static structural part of
	// repeated child identities. The hydrate entry supplies the current parent
	// scope through $$hydrate.instanceID.
	hydrationComponentInstanceSuffixes map[SectionSpan]string
	// hydrationComponentInstanceIdentities complements static suffixes for
	// #each/#key children, whose final scope also contains a runtime value.
	hydrationComponentInstanceIdentities map[SectionSpan]SSRComponentInstanceIdentity

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
	// hydrationInitBody is the small post-adoption subset of initialization:
	// it may synchronize browser-owned form state or assign bind:this refs,
	// but must never recreate server DOM or replay normal initial writes.
	hydrationInitBody []JsStmt

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

	// updateStructuralOwners records structural reconcile functions that build
	// a given ordinary update. When such a reconcile runs in a flush, its
	// branch init already writes the final value, so the ordinary update can
	// be skipped rather than writing the newly-created node twice.
	updateStructuralOwners map[string][]string
	structuralOwnerStack   []string

	// flushPlan is assembled after template code generation and assigns a
	// stable dirty flag to every unique structural/DOM update and effect.
	// Setters only mark flags; one component-local $$flush replays them.
	flushPlan *reactiveFlushPlan

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
	nestedTeardowns []JsStmt

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

	// templateBindings redirects parser-owned template identifiers into the
	// generated lexical bindings that currently own them: an each-row's
	// mutable item cell or an await branch's resolved value cell.  Expressions
	// are rewritten through the scope-aware AST visitor at emission time;
	// template nodes themselves are never copied through source text.
	templateBindings []templateBinding

	// branchTemplateBindings are temporary lexical bindings attached to a
	// synthetic branch (currently {:then}/{:catch}). They are applied only
	// while that branch's DOM plan is built, never to sibling branches.
	branchTemplateBindings map[*IfBranch][]templateBinding
}

type templateBinding struct{ from, to string }

func (g *ASTGen) withTemplateBinding(from, to string, fn func()) {
	if from == "" || to == "" || from == to {
		fn()
		return
	}
	g.templateBindings = append(g.templateBindings, templateBinding{from: from, to: to})
	defer func() { g.templateBindings = g.templateBindings[:len(g.templateBindings)-1] }()
	fn()
}

func (g *ASTGen) withBranchTemplateBindings(branch *IfBranch, fn func()) {
	bindings := g.branchTemplateBindings[branch]
	var apply func(int)
	apply = func(i int) {
		if i == len(bindings) {
			fn()
			return
		}
		g.withTemplateBinding(bindings[i].from, bindings[i].to, func() { apply(i + 1) })
	}
	apply(0)
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
	if len(g.structuralOwnerStack) == 0 || stmt == nil {
		return
	}
	if g.updateStructuralOwners == nil {
		g.updateStructuralOwners = map[string][]string{}
	}
	key := reactiveFlushStmtKey(stmt)
	for _, owner := range g.structuralOwnerStack {
		if owner == "" {
			continue
		}
		g.updateStructuralOwners[key] = append(g.updateStructuralOwners[key], owner)
	}
}

func (g *ASTGen) withStructuralOwner(owner string, fn func()) {
	if owner == "" {
		fn()
		return
	}
	g.structuralOwnerStack = append(g.structuralOwnerStack, owner)
	defer func() {
		g.structuralOwnerStack = g.structuralOwnerStack[:len(g.structuralOwnerStack)-1]
	}()
	fn()
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

type reactiveFlushEntry struct {
	key  string
	flag string
	ran  string
	stmt JsStmt
}

type reactiveFlushPlan struct {
	entries     []reactiveFlushEntry
	structural  []int
	ordinary    []int
	flagsByKey  map[string]string
	effectFlags []string
}

func reactiveFlushStmtKey(stmt JsStmt) string {
	return PrintJS(&JsProgram{Body: []JsStmt{stmt}}, PrintMinified)
}

func (p *reactiveFlushPlan) flagFor(stmt JsStmt) string {
	if p == nil || stmt == nil {
		return ""
	}
	return p.flagsByKey[reactiveFlushStmtKey(stmt)]
}

func (g *ASTGen) flushDependencyOrder() []string {
	if g.analysis == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	for _, state := range g.analysis.StateVars {
		add(state.Name)
	}
	for _, prop := range g.analysis.Props {
		add(prop.Name)
	}
	for _, derived := range g.analysis.DerivedVars {
		add(derived.Name)
	}
	return out
}

// buildReactiveFlushPlan emits one component-local scheduler. Setters mark
// dirty flags and invoke $$scheduleFlush; the scheduled $$flush performs
// structural reconciliation, ordinary DOM work, and effects in that order.
//
// Derived values remain synchronous in buildSetterCascade: user script may
// read a derived immediately between two source-order assignments.
func (g *ASTGen) buildReactiveFlushPlan() []JsStmt {
	plan := &reactiveFlushPlan{flagsByKey: map[string]string{}}
	register := func(stmt JsStmt, structural bool) {
		if stmt == nil {
			return
		}
		key := reactiveFlushStmtKey(stmt)
		if index, exists := planIndex(plan.entries, key); exists {
			if structural && !planHasIndex(plan.structural, index) {
				plan.structural = append(plan.structural, index)
				plan.entries[index].ran = "$$ran_s" + itoa(index)
				plan.ordinary = planWithoutIndex(plan.ordinary, index)
			}
			return
		}
		prefix := "$$dirty_u"
		if structural {
			prefix = "$$dirty_s"
		}
		index := len(plan.entries)
		entry := reactiveFlushEntry{
			key:  key,
			flag: prefix + itoa(index),
			stmt: shareStmt(stmt),
		}
		if structural {
			entry.ran = "$$ran_s" + itoa(index)
		}
		plan.entries = append(plan.entries, entry)
		plan.flagsByKey[key] = entry.flag
		if structural {
			plan.structural = append(plan.structural, index)
		} else {
			plan.ordinary = append(plan.ordinary, index)
		}
	}
	for _, dep := range g.flushDependencyOrder() {
		for _, stmt := range g.structuralUpdates[dep] {
			register(stmt, true)
		}
	}
	for _, dep := range g.flushDependencyOrder() {
		for _, stmt := range g.updates[dep] {
			register(stmt, false)
		}
	}
	if g.analysis != nil {
		for i := range g.analysis.Effects {
			plan.effectFlags = append(plan.effectFlags, "$$dirty_e"+itoa(i))
		}
	}
	ownerRuns := map[string]string{}
	for _, index := range plan.structural {
		entry := plan.entries[index]
		ownerRuns[entry.key] = entry.ran
	}
	g.flushPlan = plan

	// Every component owns one liveness flag. It is set before teardown so
	// queued flushes and late user callbacks become harmless no-ops.
	var out []JsStmt
	out = append(out, genLet("$$disposed", boolLit(false)))
	if len(plan.entries) == 0 && len(plan.effectFlags) == 0 {
		return out
	}

	out = append(out, genLet("$$flushQueued", boolLit(false)))
	for _, entry := range plan.entries {
		out = append(out, genLet(entry.flag, boolLit(false)))
	}
	for _, index := range plan.structural {
		out = append(out, genLet(plan.entries[index].ran, boolLit(false)))
	}
	for _, flag := range plan.effectFlags {
		out = append(out, genLet(flag, boolLit(false)))
	}

	flushBody := []JsStmt{
		genAssignStmt(id("$$flushQueued"), boolLit(false)),
		genIf(id("$$disposed"), genReturn(nil)),
	}
	if g.analysis != nil {
		for _, state := range g.analysis.StateVars {
			if state.Deep {
				flushBody = append(flushBody,
					genAssignStmt(id("$$activeMutations_"+state.Name), id("$$pendingMutations_"+state.Name)),
					genAssignStmt(id("$$pendingMutations_"+state.Name), genArray()),
				)
			}
		}
	}
	for _, index := range plan.structural {
		flushBody = append(flushBody, genAssignStmt(id(plan.entries[index].ran), boolLit(false)))
	}
	appendEntry := func(index int, structural bool) {
		entry := plan.entries[index]
		body := []JsStmt{genAssignStmt(id(entry.flag), boolLit(false))}
		if structural {
			body = append(body, genAssignStmt(id(entry.ran), boolLit(true)), shareStmt(entry.stmt))
		} else {
			shouldRun := JsExpr(boolLit(true))
			hasOwnerRun := false
			for _, owner := range g.updateStructuralOwners[entry.key] {
				structuralKey := reactiveFlushStmtKey(genInvoke(owner))
				if ran := ownerRuns[structuralKey]; ran != "" {
					hasOwnerRun = true
					shouldRun = genLogical("&&", shouldRun, genUnary("!", id(ran)))
				}
			}
			if hasOwnerRun {
				body = append(body, genIf(shouldRun, shareStmt(entry.stmt)))
			} else {
				body = append(body, shareStmt(entry.stmt))
			}
		}
		flushBody = append(flushBody, genIf(id(entry.flag), genBlock(body...)))
	}
	for _, index := range plan.structural {
		appendEntry(index, true)
	}
	for _, index := range plan.ordinary {
		appendEntry(index, false)
	}
	for i, flag := range plan.effectFlags {
		flushBody = append(flushBody, genIf(id(flag), genBlock(
			genAssignStmt(id(flag), boolLit(false)),
			genInvoke("$$effect"+itoa(i)),
		)))
	}
	// Active mutation metadata belongs only to this flush. Mutations from an
	// effect above have already been recorded in the separate pending batch.
	if g.analysis != nil {
		for _, state := range g.analysis.StateVars {
			if state.Deep {
				flushBody = append(flushBody, genAssignStmt(id("$$activeMutations_"+state.Name), genArray()))
			}
		}
	}
	out = append(out, &JsFuncDecl{
		baseStmt: baseStmt{genNode()},
		Name:     "$$flush",
		Body:     genBlock(flushBody...),
	})
	out = append(out, &JsFuncDecl{
		baseStmt: baseStmt{genNode()},
		Name:     "$$scheduleFlush",
		Body: genBlock(
			genIf(id("$$disposed"), genReturn(nil)),
			genIf(id("$$flushQueued"), genReturn(nil)),
			genAssignStmt(id("$$flushQueued"), boolLit(true)),
			genInvoke("$$defer", id("$$flush")),
		),
	})
	return out
}

func planIndex(entries []reactiveFlushEntry, key string) (int, bool) {
	for index, entry := range entries {
		if entry.key == key {
			return index, true
		}
	}
	return 0, false
}

func planHasIndex(indices []int, wanted int) bool {
	for _, index := range indices {
		if index == wanted {
			return true
		}
	}
	return false
}

func planWithoutIndex(indices []int, unwanted int) []int {
	out := indices[:0]
	for _, index := range indices {
		if index != unwanted {
			out = append(out, index)
		}
	}
	return out
}

// capturedScope holds the statements a builder function emitted
// while the generator's component-scope buffers were redirected —
// see captureScope.
type capturedScope struct {
	body              []JsStmt            // DOM construction
	initBody          []JsStmt            // initial-render statements
	hydrationInitBody []JsStmt            // server-DOM adoption statements
	events            []JsStmt            // addEventListener calls
	cleanup           []JsStmt            // teardown statements
	updates           map[string][]JsStmt // reactive dep → update statements
	comps             []compInstance      // component instances built here

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
	fragHoisted     []JsStmt
	nestedTeardowns []JsStmt
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
	saveHydrationInit := g.hydrationInitBody
	saveEvents, saveCleanup := g.events, g.cleanup
	saveUpdates := g.updates
	saveComps := g.compInstances
	saveFragHoisted := g.fragHoisted
	saveNestedTeardowns := g.nestedTeardowns

	// Restore on the way out — via defer so a panic inside fn()
	// cannot leave the generator's buffers swapped out.  The
	// fuzzer and the test suite both recover from panics and keep
	// running; without this, a panic mid-capture would corrupt
	// every subsequent compilation with another generator's
	// partial state, masking the original failure.
	defer func() {
		g.body, g.initBody = saveBody, saveInit
		g.hydrationInitBody = saveHydrationInit
		g.events, g.cleanup = saveEvents, saveCleanup
		g.updates = saveUpdates
		g.compInstances = saveComps
		g.fragHoisted = saveFragHoisted
		g.nestedTeardowns = saveNestedTeardowns
	}()

	g.body, g.initBody = nil, nil
	g.hydrationInitBody = nil
	g.events, g.cleanup = nil, nil
	g.updates = map[string][]JsStmt{}
	g.compInstances = nil
	g.fragHoisted = nil
	g.nestedTeardowns = nil

	fn()

	return capturedScope{
		body:              g.body,
		initBody:          g.initBody,
		hydrationInitBody: g.hydrationInitBody,
		events:            g.events,
		cleanup:           g.cleanup,
		updates:           g.updates,
		comps:             g.compInstances,
		fragHoisted:       g.fragHoisted,
		nestedTeardowns:   g.nestedTeardowns,
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

// generateCodeAST is the production code generator. It constructs one
// validated JavaScript AST directly from the parsed SFC and prints it.
// The legacy string generator is not part of this path.
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
	return expr.Root()
}

// templateExpr returns the cached expression parsed from the template and
// records its absolute SFC offset on its root. Template expressions are
// deliberately re-used by initial-render and update shells: JsNode has no
// parent pointers, so sharing this immutable subtree is safe.
func templateExprIdent(expr *TemplateExpr) (string, bool) {
	ident, ok := templateExprRoot(expr).(*JsIdent)
	if !ok || ident.Name == "" {
		return "", false
	}
	return ident.Name, true
}

func templateExprIsHandler(expr *TemplateExpr) bool {
	switch templateExprRoot(expr).(type) {
	case *JsArrowFunc, *JsFuncExpr:
		return true
	default:
		return false
	}
}

func (g *ASTGen) templateExpr(expr *TemplateExpr) JsExpr {
	root := cloneJsExpr(templateExprRoot(expr))
	if root == nil {
		return id("undefined")
	}
	// The innermost template binding wins when nested each/await blocks reuse
	// a binding name. Rewriting in reverse order preserves lexical shadowing.
	for i := len(g.templateBindings) - 1; i >= 0; i-- {
		binding := g.templateBindings[i]
		root = rewriteItemRefAST(root, binding.from, binding.to)
	}
	markGeneratedNode(root)
	setTemplateOrigin(root, g.sfc.TemplateOffset+expr.Pos)
	return root
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
	return genAssignStmt(genMember(id(ref), "data"), g.textValueExpr(parts))
}

func guardedTemplateStmt(stmt JsStmt, guard string) JsStmt {
	if guard == "" {
		return stmt
	}
	return &JsIfStmt{baseStmt: baseStmt{genNode()}, Test: id(guard), Then: stmt}
}

func genBool(expr JsExpr) JsExpr {
	return &JsUnaryExpr{
		baseExpr: baseExpr{genNode()}, Op: "!", Prefix: true,
		X: &JsUnaryExpr{baseExpr: baseExpr{genNode()}, Op: "!", Prefix: true, X: expr},
	}
}

func genString(expr JsExpr) JsExpr { return genCall(id("String"), expr) }

// attrWriteStmt handles ordinary dynamic attributes. Two-way form bindings use
// bindingWriteStmt below so their live property write can be guarded.
func (g *ASTGen) attrWriteStmt(varName string, attr *Attr) JsStmt {
	expr := g.templateExpr(attr.Expr)
	if isBoolAttr(attr.Name) {
		return genAssignStmt(genMember(id(varName), attr.Name), genBool(expr))
	}
	if attr.Name == "class" {
		return genAssignStmt(genMember(id(varName), "className"), genString(expr))
	}
	return genExprStmt(genCall(genMember(id(varName), "setAttribute"), strLit(attr.Name), genString(expr)))
}

// bindingWriteStmt evaluates a two-way binding expression once, then writes the
// live DOM property only when it differs. Reusing one compiler-owned temporary
// preserves expression side effects while avoiding a redundant controlled-input
// write immediately after the browser has already applied user input.
func (g *ASTGen) bindingWriteStmt(varName string, attr *Attr) JsStmt {
	cache := varName + "_bind_" + attr.Name + "_value"
	g.declareInScope(genLet(cache, nil))
	value := g.templateExpr(attr.Expr)
	if attr.Name == "checked" {
		value = genBool(value)
	}
	target := genMember(id(varName), attr.Name)
	return genBlock(
		genAssignStmt(id(cache), value),
		genIf(genBinary("!==", target, id(cache)), genAssignStmt(target, id(cache))),
	)
}

func (g *ASTGen) classDirStmtAST(varName string, attr *Attr) JsStmt {
	return genExprStmt(genCall(genMember(genMember(id(varName), "classList"), "toggle"), strLit(attr.Name), genBool(g.templateExpr(attr.Expr))))
}

func (g *ASTGen) styleDirStmtAST(varName string, attr *Attr) JsStmt {
	return genExprStmt(genCall(genMember(genMember(id(varName), "style"), "setProperty"), strLit(attr.Name), genString(g.templateExpr(attr.Expr))))
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
	case *JsThrowStmt:
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
			// Callback props are ordinary component inputs, not CustomEvents.
			// An inline arrow can still assign Vela state and therefore needs
			// the same AST lowering as a DOM @event handler.  Without it,
			// `<Canvas onSelect={(id) => selectedId = id} />` updates the
			// local variable but bypasses the setter cascade.
			prop.Value = g.componentPropExpr(attr)
		} else {
			prop.Value = strLit(attr.Value)
		}
		obj.Props = append(obj.Props, prop)
		if attr.IsTwoWay && g.analysis != nil {
			target, isIdent := templateExprIdent(attr.Expr)
			if isIdent && g.analysis.stateSet()[target] {
				obj.Props = append(obj.Props, &JsProp{
					baseNode: genNode(), Key: id("$$onChange_" + attr.Name),
					Value: &JsArrowFunc{
						baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$v")},
						Body: genCall(id("$$set_"+target), id("$$v")),
					},
				})
			}
		}
	}
	return obj
}

func (g *ASTGen) componentPropExpr(attr *Attr) JsExpr {
	value := g.templateExpr(attr.Expr)
	if _, isArrow := value.(*JsArrowFunc); isArrow && g.analysis != nil {
		value = RewriteHandlerAssignmentsAST(value, g.analysis.stateSet())
	}
	return value
}

// buildProgram produces the validated JsProgram for a component. It is exposed
// so tooling can inspect the structured AST without re-parsing generated code.
func buildProgram(
	name string,
	sfc *SFC,
	nodes []Node,
	analysis *Analysis,
	scopeID string,
) *JsProgram {
	return buildProgramCSS(name, sfc, nodes, analysis, scopeID, false)
}

// buildProgramCSS is buildProgram with explicit CSS-delivery control.
func buildProgramCSS(
	name string,
	sfc *SFC,
	nodes []Node,
	analysis *Analysis,
	scopeID string,
	cssExternal bool,
) *JsProgram {
	return buildProgramCSSWithHydration(name, sfc, nodes, analysis, scopeID, cssExternal, false)
}

func buildProgramCSSWithHydration(
	name string,
	sfc *SFC,
	nodes []Node,
	analysis *Analysis,
	scopeID string,
	cssExternal bool,
	hydrationStateInput bool,
) *JsProgram {
	prog, _ := buildProgramWithInfoAndHydration(name, sfc, nodes, analysis, scopeID, cssExternal, hydrationStateInput)
	return prog
}

// buildProgramWithInfo is instrumented for migration tests. The bool must
// always be false for production AST output.
func buildProgramWithInfo(
	name string,
	sfc *SFC,
	nodes []Node,
	analysis *Analysis,
	scopeID string,
	cssExternal bool,
) (*JsProgram, bool) {
	return buildProgramWithInfoAndHydration(name, sfc, nodes, analysis, scopeID, cssExternal, false)
}

func buildProgramWithInfoAndHydration(
	name string,
	sfc *SFC,
	nodes []Node,
	analysis *Analysis,
	scopeID string,
	cssExternal bool,
	hydrationStateInput bool,
) (*JsProgram, bool) {
	prog := buildProgramUnvalidatedWithHydration(name, sfc, nodes, analysis, scopeID, cssExternal, hydrationStateInput)
	validateGeneratedProgram(prog)
	return prog, false
}

// buildProgramUnvalidated constructs the complete target tree but deliberately
// does not run whole-program validation. It is used by the opt-in compiler
// trace to measure construction and validation as separate stable stages.
func buildProgramUnvalidated(
	name string,
	sfc *SFC,
	nodes []Node,
	analysis *Analysis,
	scopeID string,
	cssExternal bool,
) *JsProgram {
	return buildProgramUnvalidatedWithHydration(name, sfc, nodes, analysis, scopeID, cssExternal, false)
}

func buildProgramUnvalidatedWithHydration(
	name string,
	sfc *SFC,
	nodes []Node,
	analysis *Analysis,
	scopeID string,
	cssExternal bool,
	hydrationStateInput bool,
) *JsProgram {
	prog, _ := buildProgramWithDOMAcquirePlanAndHydration(name, sfc, nodes, analysis, scopeID, cssExternal, DOMAcquireCreate, hydrationStateInput)
	return prog
}

// buildProgramWithDOMAcquirePlan is the internal common construction path for
// CSR and the future hydrate entry point. It deliberately keeps the existing
// public compiler path in DOMAcquireCreate until the hydrate runtime has a
// complete marker walker and mismatch diagnostics.
func buildProgramWithDOMAcquirePlan(
	name string,
	sfc *SFC,
	nodes []Node,
	analysis *Analysis,
	scopeID string,
	cssExternal bool,
	mode DOMAcquireMode,
) (*JsProgram, DOMAcquirePlan) {
	return buildProgramWithDOMAcquirePlanAndHydration(name, sfc, nodes, analysis, scopeID, cssExternal, mode, false)
}

func buildProgramWithDOMAcquirePlanAndHydration(
	name string,
	sfc *SFC,
	nodes []Node,
	analysis *Analysis,
	scopeID string,
	cssExternal bool,
	mode DOMAcquireMode,
	hydrationStateInput bool,
) (*JsProgram, DOMAcquirePlan) {
	// Input validation consumes the immutable ScriptAST before this point.
	// Lower callback assignments only while assembling target code, so the
	// shared source tree cannot leak generated $$set_* names into user scopes.
	analysis.lowerReactiveCallbackBodies()
	g := &ASTGen{
		name:                name,
		scopeID:             scopeID,
		analysis:            analysis,
		sfc:                 sfc,
		fragCounter:         -1,
		cssExternal:         cssExternal,
		domAcquireMode:      mode,
		domAcquirePlan:      DOMAcquirePlan{Mode: mode},
		hydrationStateInput: hydrationStateInput,
	}
	// Low-level callers used by DOM-acquire tests may select hydration mode
	// without going through BuildSSRHydrationClientProgram. Preserve that
	// constructor contract by providing the same immutable lifecycle ownership
	// graph locally; the public SSR path still supplies its shared plan/index.
	if mode == DOMAcquireHydrate {
		g.hydrationLifecyclePlan = BuildSSRStructuralLifecyclePlan(nodes)
	}
	return g.buildModule(nodes), g.domAcquirePlan
}

func validateGeneratedProgram(prog *JsProgram) {
	if issues := ValidateProgram(prog); len(issues) > 0 {
		msg := "vela: internal codegen error — generated AST failed validation:\n"
		for _, iss := range issues {
			msg += "  [" + iss.Kind + "] " + iss.Msg + "\n"
		}
		panic(msg)
	}
}

// astCanBuildDirectly classifies historical migration coverage for tests and
// differential reporting. Production compilation always uses the direct builder.
// It will disappear with the legacy generator after the migration window.
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
	fnBody.Body = append(fnBody.Body,
		genAssignStmt(id("$$props"), genCall(genMember(id("Object"), "assign"),
			&JsObjectExpr{baseExpr: baseExpr{genNode()}}, id("$$props"))),
		genAssignStmt(id("$$slots"), &JsLogicalExpr{
			baseExpr: baseExpr{genNode()}, Op: "||", L: id("$$slots"),
			R: &JsObjectExpr{baseExpr: baseExpr{genNode()}},
		}),
		genExprStmt(&JsUnaryExpr{baseExpr: baseExpr{genNode()}, Op: "void", Prefix: true, X: id("$$slots")}),
	)
	if g.hydrationStateInput {
		fnBody.Body = append(fnBody.Body, genConst("$$velaState", genLogical("||", genMember(id("$$props"), "$$velaState"), genNull())))
	}
	if g.domAcquireMode == DOMAcquireHydrate {
		fnBody.Body = append(fnBody.Body,
			genConst("$$walker", genMember(id("$$hydrate"), "walker")),
			genConst("$$instanceID", genLogical("||", genMember(id("$$hydrate"), "instanceID"), strLit(g.scopeID))),
		)
	}

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
	// All template sites have been registered by buildDOM, so construct the
	// component-local dirty flush plan immediately before the setters that
	// mark it.
	fnBody.Body = append(fnBody.Body, g.buildReactiveFlushPlan()...)
	fnBody.Body = append(fnBody.Body, g.buildSetters()...)

	// ── Event listeners (block 7) ────────────────────────────
	// addEventListener calls for @event handlers and the input
	// listeners powering bind:.  After setters so handlers (which
	// call $$set_X) are in scope.
	fnBody.Body = append(fnBody.Body, g.events...)

	// ── Initial render / post-adoption synchronization (block 3) ──
	if g.domAcquireMode == DOMAcquireHydrate {
		fnBody.Body = append(fnBody.Body, g.hydrationInitBody...)
	} else {
		fnBody.Body = append(fnBody.Body, g.initBody...)
	}

	// ── Mount ────────────────────────────────────────────────
	// $$target may be an Element (appendChild as a child) OR a
	// Comment node (insertBefore — so the component is positioned
	// at the anchor's slot rather than appended at the end of its
	// container).  Comment-as-target is the wrapper-less mount
	// path: the caller creates an anchor comment in document
	if g.domAcquireMode != DOMAcquireHydrate {
		mount := genIf(
			genBinary("===", genMember(id("$$target"), "nodeType"), numLit(8)),
			genExprStmt(genCall(genMember(genMember(id("$$target"), "parentNode"), "insertBefore"), id("$$root"), id("$$target"))),
		)
		mount.Else = genExprStmt(genCall(genMember(id("$$target"), "appendChild"), id("$$root")))
		fnBody.Body = append(fnBody.Body, mount)
	}

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
		Params:   []JsExpr{id("$$target"), id("$$props"), id("$$slots")},
		Body:     fnBody,
	}
	if g.domAcquireMode == DOMAcquireHydrate {
		fn.Params = append(fn.Params, id("$$hydrate"))
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
	prog.Body = append(prog.Body, &JsExportDecl{
		baseStmt:    baseStmt{genNode()},
		Default:     true,
		Declaration: genExprStmt(id(g.name)),
	})

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
				sp.Imported == "$$isProxy" ||
				sp.Imported == "$$defer" {
				return
			}
		}
	}

	usesDispatch := false
	usesInsertHTML := false
	usesProxy := false
	usesIsProxy := false
	usesDefer := false
	usesHydrationElement := g.domAcquireMode == DOMAcquireHydrate
	usesHydrationText := len(g.domAcquirePlan.TextRanges) > 0
	usesHydrationStructure := len(g.domAcquirePlan.Structures) > 0
	usesHydrationKeyedRows := g.hydrationKeyedRows
	if g.analysis != nil && (len(g.analysis.StateVars) > 0 || len(g.analysis.Props) > 0) {
		usesIsProxy = true
		usesDefer = true
		if g.hasDeepState() {
			usesProxy = true
		}
	}
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
		case "$$defer":
			usesDefer = true
		}
		return true
	})

	// Named imports, deterministic order.
	var specs []JsImportSpec
	if usesDispatch {
		specs = append(specs, JsImportSpec{Imported: "$$dispatch", Local: "$$rt_dispatch"})
	}
	if usesInsertHTML {
		specs = append(specs, JsImportSpec{Imported: "$$insertHTML"})
	}
	if usesProxy {
		specs = append(specs, JsImportSpec{Imported: "$$proxy"})
		specs = append(specs, JsImportSpec{Imported: "$$eachRowsForMutations"})
	}
	if usesIsProxy {
		specs = append(specs, JsImportSpec{Imported: "$$isProxy"})
	}
	if usesDefer {
		specs = append(specs, JsImportSpec{Imported: "$$defer"})
	}
	if usesHydrationElement {
		specs = append(specs, JsImportSpec{Imported: "$$acquireHydrationElement"})
	}
	if usesHydrationText || usesHydrationStructure {
		specs = append(specs, JsImportSpec{Imported: "$$acquireHydrationRange"})
	}
	if usesHydrationText {
		specs = append(specs, JsImportSpec{Imported: "$$writeHydrationText"})
	}
	if usesHydrationStructure {
		specs = append(specs, JsImportSpec{Imported: "$$adoptHydrationRange"})
	}
	if usesHydrationKeyedRows {
		specs = append(specs,
			JsImportSpec{Imported: "$$adoptHydrationKeyedRows"},
			JsImportSpec{Imported: "$$adoptHydrationKeyedRowRanges"},
			JsImportSpec{Imported: "$$clearHydrationKeyedRowMarkers"},
			JsImportSpec{Imported: "$$reconcileHydrationKeyedRows"},
			JsImportSpec{Imported: "$$reconcileHydrationKeyedRowRanges"},
			JsImportSpec{Imported: "$$removeHydrationKeyedRowRange"},
		)
	}
	if len(specs) > 0 {
		imp := &JsImportDecl{
			baseStmt:   baseStmt{genNode()},
			Specifiers: specs,
			Source:     "./runtime.js",
		}
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
		bind := genConst("$$dispatch", &JsArrowFunc{
			baseExpr: baseExpr{genNode()},
			Params:   []JsExpr{id("name"), id("detail")},
			Body:     genCall(id("$$rt_dispatch"), id("$$root"), id("name"), id("detail")),
		})
		fnBody.Body = append([]JsStmt{bind}, fnBody.Body...)
	}
}

// buildImportDecls reconstructs user imports from the typed shape captured by
// the analyzer. Path rewriting and intrinsic filtering are applied directly to
// JsImportDecl fields; no executable import text is built or parsed again.
func (g *ASTGen) buildImportDecls() []JsStmt {
	if g.analysis == nil {
		return nil
	}
	var out []JsStmt
	for _, imp := range g.analysis.Imports {
		src := imp.Source
		defaultName := imp.DefaultName
		namespace := imp.Namespace
		specifiers := append([]JsImportSpec(nil), imp.Specifiers...)
		if src == "vela/store" || src == "vela" {
			src = "./runtime.js"
			kept := specifiers[:0]
			for _, spec := range specifiers {
				if !velaCompilerIntrinsics[spec.Imported] {
					kept = append(kept, spec)
				}
			}
			specifiers = kept
			if defaultName == "" && namespace == "" && len(specifiers) == 0 {
				continue
			}
		} else if g.domAcquireMode == DOMAcquireHydrate && g.hydrationComponents != nil && g.hydrationComponents[defaultName] {
			src = RewriteHydrationImportPath(src)
		} else {
			for _, spec := range specifiers {
				if g.domAcquireMode == DOMAcquireHydrate && g.hydrationComponents != nil && g.hydrationComponents[spec.Local] {
					src = RewriteHydrationImportPath(src)
					break
				}
			}
			if src == imp.Source {
				src = RewriteImportPath(src)
			}
		}
		if g.domAcquireMode == DOMAcquireHydrate && strings.HasSuffix(imp.Source, ".vela") && len(g.hydrationCSRComponents) > 0 {
			csr := &JsImportDecl{baseStmt: baseStmt{genNode()}, Source: RewriteImportPath(imp.Source)}
			if alias, ok := g.hydrationCSRComponents[defaultName]; ok {
				csr.DefaultName = alias
			}
			for _, spec := range specifiers {
				if alias, ok := g.hydrationCSRComponents[spec.Local]; ok {
					csr.Specifiers = append(csr.Specifiers, JsImportSpec{Imported: spec.Imported, Local: alias})
				}
			}
			if csr.DefaultName != "" || len(csr.Specifiers) > 0 {
				out = append(out, csr)
			}
		}

		out = append(out, &JsImportDecl{
			baseStmt:    baseStmt{genNode()},
			DefaultName: defaultName,
			Namespace:   namespace,
			Specifiers:  specifiers,
			Source:      src,
		})
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
				out = append(out, genInvoke("$$upd_"+derived.Name))
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
			if step.Stmt != nil {
				out = append(out, step.Stmt)
			}
		}
	}
	return out
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
		value := JsExpr(genMember(id("$$props"), p.Name))
		if p.DefaultAST != nil {
			value = &JsConditionalExpr{
				baseExpr: baseExpr{genNode()},
				Test:     genBinary("!==", genMember(id("$$props"), p.Name), id("undefined")),
				Then:     genMember(id("$$props"), p.Name),
				Else:     p.DefaultAST,
			}
		}
		out = append(out, genLet(p.Name, value))
	}
	// $$onChange handles for bindable props.
	for _, p := range g.analysis.Props {
		if p.Bindable {
			out = append(out, genConst("$$onChange_"+p.Name,
				genLogical("||", genMember(id("$$props"), "$$onChange_"+p.Name), genNull())))
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
	if g.domAcquireMode == DOMAcquireHydrate {
		return nil
	}
	if !g.hasStyle() {
		return nil
	}
	// External-CSS mode: the scoped stylesheet is written to a
	// sibling .css file by the compiler — the component emits no
	// runtime <style> injection at all.
	if g.cssExternal {
		return nil
	}
	css := scopedCSSForSFC(g.sfc, g.scopeID)
	return []JsStmt{
		genConst("$$style", genCall(genMember(id("document"), "createElement"), strLit("style"))),
		genAssignStmt(genMember(id("$$style"), "textContent"), strLit(css)),
		genExprStmt(genCall(genMember(genMember(id("document"), "head"), "appendChild"), id("$$style"))),
	}
}

// ExtractScopedCSS returns the scope-transformed stylesheet for an
// SFC, or "" when it has no <style>.  Used by the compiler in
// external-CSS mode to write a sibling .css file.
func ExtractScopedCSS(sfc *SFC, scopeID string) string {
	if sfc == nil || strings.TrimSpace(sfc.Style) == "" {
		return ""
	}
	return scopedCSSForSFC(sfc, scopeID)
}

// scopedCSSForSFC keeps the compiler's parse-once path while supporting
// embedders and focused tests that construct an SFC directly. Parsed SFCs
// always own StyleAST; only the latter compatibility boundary needs a local
// stylesheet parse before scoping.
func scopedCSSForSFC(sfc *SFC, scopeID string) string {
	if sfc == nil || strings.TrimSpace(sfc.Style) == "" {
		return ""
	}
	sheet := sfc.StyleAST
	if sheet == nil {
		sheet = newCssStylesheet(sfc.Style, sfc.StyleOffset)
	}
	return ScopeCssStylesheet(sheet, scopeID)
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
// $state.deep:   const $$mut_X = (mutation) => $$set_X_notify(mutation);
//
//	let X = $$proxy(<initial>, $$mut_X);
//
// The value is Proxy-wrapped so nested mutations route through the
// same notify path as reassignment; $$mut_X is the mutation
// callback the Proxy traps invoke.
func (g *ASTGen) buildStateDecl(sv *StateVar) []JsStmt {
	init := sv.InitialAST
	if init == nil {
		init = id("undefined")
	}
	if g.hydrationStateInput {
		hasState := genCall(generatedMemberPath("Object.prototype.hasOwnProperty.call"), id("$$velaState"), strLit(sv.Name))
		init = &JsConditionalExpr{
			baseExpr: baseExpr{genNode()},
			Test:     genLogical("&&", id("$$velaState"), hasState),
			Then:     genMember(id("$$velaState"), sv.Name),
			Else:     init,
		}
	}
	if !sv.Deep {
		return []JsStmt{genLet(sv.Name, init)}
	}
	mutName := "$$mut_" + sv.Name
	return []JsStmt{
		genConst(mutName, &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$mutation")}, Body: genCall(id("$$set_"+sv.Name+"_notify"), id("$$mutation"))}),
		genLet("$$pendingMutations_"+sv.Name, genArray()),
		genLet("$$activeMutations_"+sv.Name, genArray()),
		genLet(sv.Name, genCall(id("$$proxy"), init, id(mutName))),
	}
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
	expr := dv.ExprAST
	if expr == nil {
		expr = id("undefined")
	}
	return []JsStmt{
		genLet(dv.Name, nil),
		genConst("$$upd_"+dv.Name, &JsArrowFunc{
			baseExpr: baseExpr{genNode()},
			Body:     genBlock(genAssignStmt(id(dv.Name), expr)),
		}),
	}
}

func (g *ASTGen) buildDerivedDecls() []JsStmt {
	if g.analysis == nil {
		return nil
	}
	var out []JsStmt
	for _, dv := range g.analysis.DerivedVars {
		out = append(out, genLet(dv.Name, nil))
	}
	for _, dv := range g.analysis.DerivedVars {
		expr := dv.ExprAST
		if expr == nil {
			expr = id("undefined")
		}
		out = append(out, genConst("$$upd_"+dv.Name, &JsArrowFunc{
			baseExpr: baseExpr{genNode()},
			Body:     genBlock(genAssignStmt(id(dv.Name), expr)),
		}))
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
		out = append(out, genInvoke("$$upd_"+dv.Name))
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
		cleanup := "$$cleanup" + n
		body := eff.BodyAST
		if body == nil {
			body = genBlock()
		}
		runner := &JsArrowFunc{
			baseExpr: baseExpr{genNode()},
			Body: genBlock(
				genIf(id(cleanup), genBlock(
					genInvoke(cleanup),
					genAssignStmt(id(cleanup), genNull()),
				)),
				genAssignStmt(id(cleanup), genCall(&JsFuncExpr{
					baseExpr: baseExpr{genNode()},
					Body:     body,
				})),
			),
		}
		out = append(out, genLet(cleanup, genNull()), genConst("$$effect"+n, runner))
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
		out = append(out, genInvoke("$$effect"+itoa(i)))
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
		body := lc.BodyAST
		if body == nil {
			body = genBlock()
		}
		out = append(out, genConst("$$mountCleanup"+itoa(i), genCall(&JsFuncExpr{
			baseExpr: baseExpr{genNode()},
			Body:     body,
		})))
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
		out = append(out, genIf(
			genBinary("===", &JsUnaryExpr{baseExpr: baseExpr{genNode()}, Op: "typeof", Prefix: true, X: id(cv)}, strLit("function")),
			genInvoke(cv),
		))
	}
	// onDestroy callback bodies, inline.
	for _, lc := range g.analysis.Lifecycles {
		if lc.Kind != "onDestroy" {
			continue
		}
		if lc.BodyAST != nil {
			out = append(out, lc.BodyAST.Body...)
		}
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
// var: recompute dependent derived immediately, then mark the relevant DOM
// sites and effects for the component-local microtask flush. Shared by the
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
				out = append(out, genIf(
					id("$$onChange_"+name),
					genInvoke("$$onChange_"+name, id(name)),
				))
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
				out = append(out, genInvoke("$$upd_"+dv.Name))
			}
		}
	}
	// 2. DOM updates - direct (read name) + transitive (read a
	//    dependent derived). Structural re-renders come FIRST so a branch
	//    that closes nulls its interior handles before ordinary writes run.
	seen := map[string]bool{}
	var updates []JsStmt
	add := func(stmts []JsStmt) {
		for _, s := range stmts {
			key := reactiveFlushStmtKey(s)
			if seen[key] {
				continue
			}
			seen[key] = true
			updates = append(updates, shareStmt(s))
		}
	}
	add(g.structuralUpdates[name])
	for _, dv := range dependentDerived {
		add(g.structuralUpdates[dv.Name])
	}
	add(g.updates[name])
	for _, dv := range dependentDerived {
		add(g.updates[dv.Name])
	}

	// Setters mark component-local flags instead of enqueueing a fresh closure.
	// One $$scheduleFlush call coalesces all synchronous setter work into a
	// single $$flush with final state values.
	marked := map[string]bool{}
	mark := func(flag string) {
		if flag == "" || marked[flag] {
			return
		}
		marked[flag] = true
		out = append(out, genAssignStmt(id(flag), boolLit(true)))
	}
	for _, stmt := range updates {
		if g.flushPlan != nil {
			mark(g.flushPlan.flagFor(stmt))
		}
	}

	// 3. Re-run dependent effects in the same flush. Effects therefore see
	//    final state and derived values once per synchronous setter burst.
	if g.analysis != nil && g.flushPlan != nil {
		for i, eff := range g.analysis.Effects {
			effDirty := false
			for _, d := range eff.Deps {
				if dirty[d] {
					effDirty = true
					break
				}
			}
			if effDirty && i < len(g.flushPlan.effectFlags) {
				mark(g.flushPlan.effectFlags[i])
			}
		}
	}
	if len(marked) > 0 {
		out = append(out, genInvoke("$$scheduleFlush"))
	}
	return out
}

// buildOneSetter constructs a single $$set_<name> function for a
// plain ($state / prop) reactive variable: a guard, the
// assignment, then the reactive cascade.
func (g *ASTGen) buildOneSetter(name string) JsStmt {
	body := &JsBlock{baseStmt: baseStmt{genNode()}}
	body.Body = append(body.Body, genIf(id("$$disposed"), genReturn(nil)))

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
//	function $$set_X_notify($$mutation) { …reactive cascade… }
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
	pendingMutations := "$$pendingMutations_" + name
	mutationCapture := genIf(id("$$mutation"),
		genIf(genBinary("!==", id(pendingMutations), genNull()),
			genExprStmt(genCall(genMember(id(pendingMutations), "push"), id("$$mutation")))),
	)
	mutationCapture.Else = genAssignStmt(id(pendingMutations), genNull())

	// $$set_X_notify — the cascade body.
	notifyBody := &JsBlock{baseStmt: baseStmt{genNode()}}
	notifyBody.Body = append(notifyBody.Body, genIf(id("$$disposed"), genReturn(nil)))
	notifyBody.Body = append(notifyBody.Body, mutationCapture)
	notifyBody.Body = append(notifyBody.Body, g.buildSetterCascade(name)...)
	notify := &JsFuncDecl{
		baseStmt: baseStmt{genNode()},
		Name:     "$$set_" + name + "_notify",
		Params:   []JsExpr{id("$$mutation")},
		Body:     notifyBody,
	}

	// $$set_X — guard, re-wrap, notify.  The guard mirrors the
	// plain setter (=== plus !$$isProxy) so the two generators
	// agree; for a deep var the new value is normally raw (proxy
	// !== raw, so the guard never fired anyway), but a re-passed
	// proxy must still fall through to a re-wrap + notify.
	setBody := &JsBlock{baseStmt: baseStmt{genNode()}}
	setBody.Body = append(setBody.Body, genIf(id("$$disposed"), genReturn(nil)))
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
		genAssignStmt(id(name), genCall(id("$$proxy"), id("$$v"), id("$$mut_"+name))),
		genInvoke("$$set_"+name+"_notify"))
	set := &JsFuncDecl{
		baseStmt: baseStmt{genNode()},
		Name:     "$$set_" + name,
		Params:   []JsExpr{id("$$v")},
		Body:     setBody,
	}

	return []JsStmt{notify, set}
}

// shareStmt returns a registered generated statement without copying it.
//
// Update AST nodes are immutable once passed to addUpdate/addStructuralUpdate or
// saved as compInstance.setStmt: ASTGen only appends them to completed blocks,
// while validation, walking, source-map collection and printing are read-only.
// Sharing therefore avoids the former PrintJS → ParseJS clone round-trip without
// allowing one reactive path to affect another.
func shareStmt(s JsStmt) JsStmt { return s }

// buildDOM emits the DOM-construction statements for the template
// into g.body (and any initial-render statements into g.initBody).
//
// Dispatches on the shape astCanBuildDirectly already validated:
//   - all roots static  → buildStaticDOM (block 2)
//   - single root with text holes → buildHolesDOM (block 3)
func (g *ASTGen) buildDOM(nodes []Node) {
	// The executable hydration subset owns one existing root element. Route it
	// before the CSR strategy ladder: static children and structural ranges are
	// already present in server DOM and must not fall through to recursive
	// construction, anchors, or initial render calls.
	if g.domAcquireMode == DOMAcquireHydrate && len(nodes) == 1 {
		if root, ok := nodes[0].(*ElementNode); ok && root != nil {
			g.buildHydrationRootDOM(root)
			return
		}
	}

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

	// Multiple dynamic roots, and roots containing control-flow blocks, need a
	// stable wrapper. Build every sibling structurally so dynamic elements are
	// not silently dropped next to the first root.
	if len(nodes) > 1 {
		g.buildChainRootDOM(nodes)
		return
	}
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
			g.body = append(g.body,
				genAssignStmt(genMember(genMember(id(root), "style"), "display"), strLit("contents")))
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

func (g *ASTGen) buildHydrationRootDOM(root *ElementNode) {
	// Keep the historical id consumption for deterministic generated symbol
	// names in the shared expression lowerings, even though $$root is adopted
	// by a marker rather than newly created.
	_ = g.id()
	rootPlan := g.hydrationLifecycleNode(SSRLifecyclePlanElement, root.Span)
	g.body = append(g.body, g.createHydrationTemplateElementStmt("$$root", root))
	g.applyHydrationElementAttrs("$$root", root)
	g.buildHydrationRootChildren(root.Children, rootPlan.Children, "$$root")
}

// buildChainRootDOM builds DOM for a root list containing one or
// more {#if} chains (block 9), possibly mixed with fully-static
// sibling elements.  Everything goes inside a display:contents
// wrapper so the chains have a stable parent and document order is
// preserved.
func (g *ASTGen) buildChainRootDOM(nodes []Node) {
	// The wrapper <div> is named $$root literally — legacy does not
	// call id() for it, so the chain numbering must not either.
	g.body = append(g.body, g.createElementStmt("$$root", "div"))
	if g.domAcquireMode != DOMAcquireHydrate {
		g.body = append(g.body, g.setAttrStmt("$$root", g.scopeID, ""))
	}
	g.body = append(g.body,
		genAssignStmt(genMember(genMember(id("$$root"), "style"), "display"), strLit("contents")))

	// Use the common recursive builder for every sibling. Besides preserving
	// document order this keeps events, bindings, components, and dynamic
	// attributes alive on root-level elements beside a block.
	g.buildNodes(nodes, "$$root", false)
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
				res.build = append(res.build,
					genExprStmt(genCall(genMember(id(targetVar), "appendChild"),
						genCall(genMember(id("document"), "createTextNode"), strLit(text)))))
				continue
			}
			// Dynamic TextNode — one text-node hole.
			refVar := g.id()
			if hoist {
				res.decls = append(res.decls, genLet(refVar, genNull()))
				res.build = append(res.build,
					genAssignStmt(id(refVar), genCall(genMember(id("document"), "createTextNode"), strLit(""))))
				res.captured = append(res.captured, refVar)
			} else {
				res.build = append(res.build,
					genConst(refVar, genCall(genMember(id("document"), "createTextNode"), strLit(""))))
			}
			res.build = append(res.build,
				genExprStmt(genCall(genMember(id(targetVar), "appendChild"), id(refVar))))
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
		res.decls = append(res.decls, genLet(twVar, genNull()))
		res.build = append(res.build,
			genAssignStmt(id(twVar), genCall(genMember(id("document"), "createTreeWalker"), id(targetVar), numLit(128))))
		res.captured = append(res.captured, twVar)
	} else {
		res.build = append(res.build,
			genConst(twVar, genCall(genMember(id("document"), "createTreeWalker"), id(targetVar), numLit(128))))
	}

	anchors := make([]string, len(holes))
	for i := range holes {
		anchors[i] = g.id() + "_a"
		if hoist {
			res.decls = append(res.decls, genLet(anchors[i], genNull()))
			res.build = append(res.build,
				genAssignStmt(id(anchors[i]), genCall(genMember(id(twVar), "nextNode"))))
			res.captured = append(res.captured, anchors[i])
		} else {
			res.build = append(res.build,
				genConst(anchors[i], genCall(genMember(id(twVar), "nextNode"))))
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
			res.decls = append(res.decls, genLet(refVar, genNull()))
			res.build = append(res.build,
				genAssignStmt(id(refVar), genCall(genMember(id("document"), "createTextNode"), strLit(""))))
			res.captured = append(res.captured, refVar)
		} else {
			res.build = append(res.build,
				genConst(refVar, genCall(genMember(id("document"), "createTextNode"), strLit(""))))
		}
		res.build = append(res.build,
			genExprStmt(genCall(genMember(genMember(id(anchor), "parentNode"), "replaceChild"), id(refVar), id(anchor))))

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
// rewritten on the cached template AST so they close over the row's mutable
// $$item box rather than the stale forEach parameter.
//
//	rowVar  — the row element variable ($$f0)
//	itemVar — the loop binding name
//	boxVar  — the row's mutable item box ($$item_$$nN)
func (g *ASTGen) buildEachRowAttrs(
	rowElem *ElementNode, rowVar, itemVar, boxVar string,
) eachRowAttrs {
	res := eachRowAttrs{deps: map[string]bool{}}
	addDeps := func(attr *Attr) {
		for _, dep := range templateExprDeps(attr.Expr, g.reactiveNames()) {
			res.deps[dep] = true
		}
	}
	for _, attr := range rowElem.Attrs {
		switch {
		case attr.IsEvent:
			// A row-root event needs both state-assignment and row-item rewrites.
			_, isIdent := templateExprIdent(attr.Expr)
			handlerExpr := g.eachItemExpr(attr, itemVar, boxVar, true)
			if isIdent {
				res.create = append(res.create, genExprStmt(genCall(genMember(id(rowVar), "addEventListener"), strLit(attr.Name), handlerExpr)))
			} else {
				hv := rowVar + "_" + attr.Name + "_h"
				if templateExprIsHandler(attr.Expr) {
					res.create = append(res.create, genConst(hv, handlerExpr))
				} else {
					res.create = append(res.create, genConst(hv, &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$event")}, Body: genBlock(genExprStmt(handlerExpr))}))
				}
				res.create = append(res.create,
					genExprStmt(genCall(genMember(id(rowVar), "addEventListener"), strLit(attr.Name), id(hv))))
			}

		case attr.IsClassDir:
			stmt := genExprStmt(genCall(genMember(genMember(id(rowVar), "classList"), "toggle"), strLit(attr.Name), genBool(g.eachItemExpr(attr, itemVar, boxVar, false))))
			res.update = append(res.update, stmt)
			addDeps(attr)

		case attr.IsStyleDir:
			stmt := genExprStmt(genCall(genMember(genMember(id(rowVar), "style"), "setProperty"), strLit(attr.Name), genString(g.eachItemExpr(attr, itemVar, boxVar, false))))
			res.update = append(res.update, stmt)
			addDeps(attr)

		case attr.IsBind:
			valueExpr := g.eachItemExpr(attr, itemVar, boxVar, false)
			var stmt JsStmt
			switch {
			case attr.IsTwoWay && attr.Name == "value":
				stmt = genAssignStmt(genMember(id(rowVar), "value"), valueExpr)
			case isBoolAttr(attr.Name):
				stmt = genAssignStmt(genMember(id(rowVar), attr.Name), genBool(valueExpr))
			case attr.Name == "class":
				stmt = genAssignStmt(genMember(id(rowVar), "className"), genString(valueExpr))
			default:
				stmt = genExprStmt(genCall(genMember(id(rowVar), "setAttribute"), strLit(attr.Name), genString(valueExpr)))
			}
			res.update = append(res.update, stmt)
			addDeps(attr)

		case attr.IsBindThis:
			// bind:this on an each-row would overwrite the same ref
			// on every iteration — legacy skips it, so do we.

		default:
			// Plain static attribute.
			res.create = append(res.create,
				genExprStmt(genCall(genMember(id(rowVar), "setAttribute"), strLit(attr.Name), strLit(attr.Value))))
		}
	}
	return res
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
		out = append(out,
			genExprStmt(genCall(genMember(id(targetVar), "appendChild"),
				genCall(genMember(id("document"), "createTextNode"), strLit(text)))))
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
	if g.domAcquireMode != DOMAcquireHydrate {
		g.body = append(g.body, g.setAttrStmt("$$root", g.scopeID, ""))
	}
	g.applyRootAttrs(el)
	if g.domAcquireMode == DOMAcquireHydrate {
		rootPlan := g.hydrationLifecycleNode(SSRLifecyclePlanElement, el.Span)
		g.buildHydrationRootChildren(el.Children, rootPlan.Children, "$$root")
		return
	}

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
			write := genAssignStmt(genMember(id("$$root"), prop), genString(g.templateExpr(el.Children[0].(*TextNode).Parts[0].Expr)))
			g.initBody = append(g.initBody, write)
			for _, dep := range findDeps(expr, g.reactiveNames()) {
				g.addUpdate(dep, write)
			}
			return
		}
		if text, ok := staticRawText(el.Children); ok {
			g.body = append(g.body, genAssignStmt(genMember(id("$$root"), prop), strLit(text)))
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
type eachIncrementalPlan struct {
	stateName string
	keyField  string
}

// eachIncrementalPlanFor returns a deliberately narrow optimisation contract:
// a direct $state.deep list keyed by item.<field>. All other expressions use
// the existing full keyed reconciler.
func (g *ASTGen) eachIncrementalPlanFor(block *EachBlockNode) *eachIncrementalPlan {
	name, ok := templateExprIdent(block.IterExpr)
	if !ok || g.analysis == nil {
		return nil
	}
	deep := false
	for _, state := range g.analysis.StateVars {
		if state.Name == name && state.Deep {
			deep = true
			break
		}
	}
	if !deep {
		return nil
	}
	key, ok := templateExprRoot(block.KeyAST).(*JsMemberExpr)
	if !ok || key.Computed {
		return nil
	}
	item, itemOK := key.Obj.(*JsIdent)
	field, fieldOK := key.Prop.(*JsIdent)
	if !itemOK || !fieldOK || item.Name != block.ItemName || field.Name == "" {
		return nil
	}
	return &eachIncrementalPlan{stateName: name, keyField: field.Name}
}

func (g *ASTGen) buildEachBlock(block *EachBlockNode, parentVar string) {
	itemVar, indexVar := block.ItemName, block.IndexName
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
	generatedID := func(name string) JsExpr {
		return &JsIdent{baseExpr: baseExpr{genNode()}, Name: name}
	}

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
		g.declare(genLet(anchor, genNull()))
		g.body = append(g.body, genAssignStmt(
			generatedID(anchor),
			genCall(genMember(generatedID("document"), "createComment"), strLit("#each")),
		))
	} else {
		g.body = append(g.body, genConst(
			anchor,
			genCall(genMember(generatedID("document"), "createComment"), strLit("#each")),
		))
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
	rowChildren := rowElem.Children
	rowHasBlocks := !astHoleSubtreeOK(rowChildren)
	g.withFragmentIDs(func() {
		g.withTemplateBinding(itemVar, "$$item_"+id, func() {
			rowVar = g.id() // $$f0
			fragBody = []JsStmt{
				genLet("$$item_"+id, generatedID(itemVar)),
				genConst(rowVar, genCall(
					genMember(generatedID("document"), "createElement"), strLit(rowElem.Tag),
				)),
				genExprStmt(genCall(
					genMember(generatedID(rowVar), "setAttribute"), strLit(g.scopeID), strLit(""),
				)),
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
	})

	// update(item, $$i): refresh the row box, then re-run the
	// per-row directive/bind statements and the hole writes — in
	// that order, matching legacy (attr loop precedes child walk).
	updateBody := []JsStmt{genAssignStmt(generatedID("$$item_"+id), generatedID(itemVar))}
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
			updateBody = append(updateBody, shareStmt(ci.setStmt))
		}
	}
	// Nested-block rows: re-run the nested chain/each init calls so
	// the row's dynamic content refreshes when the row is reused.
	updateBody = append(updateBody, nestedUpdate...)

	// Keep the fragment structured. Rendering fragBody/updateBody to text and parsing
	// it again drops SourceOrigin from row-local template expressions.
	destroyBody := append([]JsStmt{}, rowCleanup...)
	destroyBody = append(destroyBody, genExprStmt(genCall(genMember(generatedID(rowVar), "remove"))))
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
	incrementalPlan := g.eachIncrementalPlanFor(block)
	forceFull := ""
	if incrementalPlan != nil {
		forceFull = "$$forceFull_" + id
		g.declare(genLet(forceFull, boolLit(false)))
	}
	reconcilerDecl := g.eachReconciler(
		eachFn, fragFn, anchor, blocks, keymap, keyExpr, block.KeyAST, itemVar, indexVar, incrementalPlan)

	if captured {
		g.declare(fragDecl)
		if keyExpr != "" {
			g.declare(genLet(keymap, genNull()))
			g.body = append(g.body, genAssignStmt(generatedID(keymap), &JsNewExpr{
				baseExpr: baseExpr{genNode()}, Callee: generatedID("Map"),
			}))
		}
		g.declare(genLet(blocks, &JsArrayExpr{baseExpr: baseExpr{genNode()}, Elems: []JsExpr{}}))
		g.declare(reconcilerDecl)
		// Outer-branch teardown contribution.  When the {#if} (or
		// other chain) containing this {#each} switches off, the
		// chain only captures this each's anchor in its branch
		// handle — the rows live before the anchor and would be
		// orphaned in the DOM if the outer simply removed the
		// handle.  Emit teardown statements so the outer branch's
		// teardown destroys every row, clears the bookkeeping,
		// and nulls the anchor — matching the pattern used for
		// nested {#if} chains.  Same g.nestedTeardowns channel,
		// so a doubly-nested control-flow propagates naturally.
		rowDestroy := &JsArrowFunc{
			baseExpr: baseExpr{genNode()},
			Params:   []JsExpr{generatedID("$$block")},
			Body:     genCall(genMember(generatedID("$$block"), "destroy")),
		}
		teardown := []JsStmt{
			genExprStmt(genCall(genMember(generatedID(blocks), "forEach"), rowDestroy)),
			genAssignStmt(generatedID(blocks), genArray()),
		}
		if keyExpr != "" {
			teardown = append(teardown, genAssignStmt(generatedID(keymap), genNull()))
		}
		teardown = append(teardown,
			genAssignStmt(genMember(generatedID(eachFn), "_arr"), generatedID("undefined")),
			genAssignStmt(generatedID(anchor), genNull()),
		)
		g.nestedTeardowns = append(g.nestedTeardowns, teardown...)
	} else {
		g.body = append(g.body, fragDecl)
		if keyExpr != "" {
			g.body = append(g.body, genConst(keymap, &JsNewExpr{
				baseExpr: baseExpr{genNode()}, Callee: generatedID("Map"),
			}))
		}
		g.body = append(g.body, genLet(blocks, &JsArrayExpr{baseExpr: baseExpr{genNode()}, Elems: []JsExpr{}}))
		g.body = append(g.body, reconcilerDecl)
	}
	// ── 5. Wiring ────────────────────────────────────────────
	g.body = append(g.body, genExprStmt(genCall(
		genMember(generatedID(parentVar), "appendChild"), generatedID(anchor),
	)))
	arrayExpr := g.templateExpr(block.IterExpr)
	runEachArgs := []JsExpr{arrayExpr}
	var markForceFull JsStmt
	if incrementalPlan != nil {
		mutations := JsExpr(generatedID("$$activeMutations_" + incrementalPlan.stateName))
		mutations = &JsConditionalExpr{
			baseExpr: baseExpr{genNode()},
			Test:     generatedID(forceFull),
			Then:     genNull(),
			Else:     mutations,
		}
		runEachArgs = append(runEachArgs, mutations)
		markForceFull = genAssignStmt(generatedID(forceFull), boolLit(true))
	}
	runEachBody := []JsStmt{genExprStmt(genCall(generatedID(eachFn), runEachArgs...))}
	if forceFull != "" {
		runEachBody = append(runEachBody, genAssignStmt(generatedID(forceFull), boolLit(false)))
	}
	runEach := genBlock(runEachBody...)
	g.initBody = append(g.initBody, runEach)
	for _, dep := range templateExprDeps(block.IterExpr, g.reactiveNames()) {
		g.addUpdate(dep, shareStmt(runEach))
	}
	for dep := range rowAttrs.deps {
		g.addUpdate(dep, shareStmt(runEach))
		if markForceFull != nil {
			g.addStructuralUpdate(dep, shareStmt(markForceFull))
		}
	}
	// A nested-control-flow row body reads component state through
	// fragment-local chain functions the component setter cannot
	// reach. Re-run the full reconciler when such an external dep changes.
	for dep := range rowBodyDeps {
		g.addUpdate(dep, shareStmt(runEach))
		if markForceFull != nil {
			g.addStructuralUpdate(dep, shareStmt(markForceFull))
		}
	}
	g.cleanup = append(g.cleanup, genExprStmt(genCall(
		genMember(generatedID(blocks), "forEach"),
		&JsArrowFunc{
			baseExpr: baseExpr{genNode()},
			Params:   []JsExpr{generatedID("b")},
			Body:     genCall(genMember(generatedID("b"), "destroy")),
		},
	)))
}

// eachReconciler builds a typed reconciler. The keyed path remains a temporary
// source fallback while its key-expression origin handling is migrated.
func (g *ASTGen) eachReconciler(
	eachFn, fragFn, anchor, blocks, keymap, keyExpr string, keyAST *TemplateExpr, itemVar, indexVar string, incremental *eachIncrementalPlan,
) JsStmt {
	if keyExpr != "" {
		return g.eachReconcilerKeyed(eachFn, fragFn, anchor, blocks, keymap, keyAST, itemVar, indexVar, incremental)
	}
	return g.eachReconcilerUnkeyed(eachFn, fragFn, anchor, blocks, itemVar, indexVar)
}

func (g *ASTGen) eachReconcilerUnkeyed(eachFn, fragFn, anchor, blocks, itemVar, indexVar string) JsStmt {
	arr := id("$$arr")
	blockAt := func(index JsExpr) JsExpr { return genComputedMember(id(blocks), index) }
	arrAt := func(index JsExpr) JsExpr { return genComputedMember(arr, index) }
	inc := func(name string) JsExpr {
		return &JsAssignExpr{baseExpr: baseExpr{genNode()}, Op: "=", L: id(name), R: genBinary("+", id(name), numLit(1))}
	}
	rowDestroy := func(index JsExpr) JsStmt {
		return genExprStmt(genCall(genMember(blockAt(index), "destroy")))
	}
	makeRow := func(index JsExpr) []JsStmt {
		return []JsStmt{
			genConst("$$blk", genCall(id(fragFn), arrAt(index), index)),
			genExprStmt(genCall(genMember(id(anchor), "before"), genMember(id("$$blk"), "el"))),
			genExprStmt(genCall(genMember(id(blocks), "push"), id("$$blk"))),
		}
	}

	commonLoop := genFor(
		genLet("$$i", numLit(0)),
		genBinary("<", id("$$i"), id("$$common")),
		inc("$$i"),
		genBlock(genIf(
			genMember(blockAt(id("$$i")), "update"),
			genExprStmt(genCall(genMember(blockAt(id("$$i")), "update"), arrAt(id("$$i")), id("$$i"))),
		)),
	)
	shrinkLoop := genFor(
		genLet("$$i", genMember(arr, "length")),
		genBinary("<", id("$$i"), genMember(id(blocks), "length")),
		inc("$$i"),
		genBlock(rowDestroy(id("$$i"))),
	)
	growLoop := genFor(
		genLet("$$i", id("$$common")),
		genBinary("<", id("$$i"), genMember(arr, "length")),
		inc("$$i"),
		genBlock(makeRow(id("$$i"))...),
	)
	updateExisting := genBlock(
		genConst("$$common", genCall(genMember(id("Math"), "min"), genMember(id(blocks), "length"), genMember(arr, "length"))),
		commonLoop,
		&JsIfStmt{
			baseStmt: baseStmt{genNode()},
			Test:     genBinary(">", genMember(id(blocks), "length"), genMember(arr, "length")),
			Then: genBlock(
				shrinkLoop,
				genAssignStmt(genMember(id(blocks), "length"), genMember(arr, "length")),
			),
			Else: &JsIfStmt{
				baseStmt: baseStmt{genNode()},
				Test:     genBinary("<", genMember(id(blocks), "length"), genMember(arr, "length")),
				Then:     genBlock(growLoop),
			},
		},
		genReturn(nil),
	)
	return &JsFuncDecl{
		baseStmt: baseStmt{genNode()},
		Name:     eachFn,
		Params:   []JsExpr{arr},
		Body: genBlock(
			genIf(genUnary("!", genMember(id(anchor), "parentNode")), genReturn(nil)),
			genAssignStmt(arr, genLogical("||", arr, genArray())),
			genConst("$$sameRef", genBinary("===", genMember(id(eachFn), "_arr"), arr)),
			genConst("$$stillLive", genLogical("||",
				genBinary("===", genMember(id(blocks), "length"), numLit(0)),
				genMember(genMember(blockAt(numLit(0)), "el"), "isConnected"),
			)),
			genIf(genLogical("&&", id("$$sameRef"), id("$$stillLive")), updateExisting),
			genAssignStmt(genMember(id(eachFn), "_arr"), arr),
			genExprStmt(genCall(genMember(id(blocks), "forEach"), &JsArrowFunc{
				baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("b")}, Body: genCall(genMember(id("b"), "destroy")),
			})),
			genAssignStmt(id(blocks), genArray()),
			genExprStmt(genCall(genMember(arr, "forEach"), &JsFuncExpr{
				baseExpr: baseExpr{genNode()}, Params: []JsExpr{id(itemVar), id(indexVar)},
				Body: genBlock(
					genConst("$$blk", genCall(id(fragFn), id(itemVar), id(indexVar))),
					genExprStmt(genCall(genMember(id(anchor), "before"), genMember(id("$$blk"), "el"))),
					genExprStmt(genCall(genMember(id(blocks), "push"), id("$$blk"))),
				),
			})),
		),
	}
}

func (g *ASTGen) eachReconcilerKeyed(eachFn, fragFn, anchor, blocks, keymap string, keyAST *TemplateExpr, itemVar, indexVar string, incremental *eachIncrementalPlan) JsStmt {
	arr := id("$$arr")
	key := id("$$key")
	blk := id("$$blk")
	keyExpr := g.templateExpr(keyAST)
	buildRow := genBlock(
		genConst("$$key", genCall(id("String"), keyExpr)),
		genExprStmt(genCall(genMember(id("$$newKeys"), "add"), key)),
		genLet("$$blk", genCall(genMember(id(keymap), "get"), key)),
		&JsIfStmt{
			baseStmt: baseStmt{genNode()},
			Test:     blk,
			Then: genBlock(genIf(
				genMember(blk, "update"),
				genExprStmt(genCall(genMember(blk, "update"), id(itemVar), id(indexVar))),
			)),
			Else: genBlock(
				genAssignStmt(blk, genCall(id(fragFn), id(itemVar), id(indexVar))),
				genExprStmt(genCall(genMember(id(keymap), "set"), key, blk)),
			),
		},
		genIf(
			genBinary("!==", genMember(genMember(blk, "el"), "nextSibling"), id(anchor)),
			genExprStmt(genCall(genMember(id(anchor), "before"), genMember(blk, "el"))),
		),
		genExprStmt(genCall(genMember(id("$$newBlocks"), "push"), blk)),
	)
	removeDropped := &JsArrowFunc{
		baseExpr: baseExpr{genNode()},
		Params:   []JsExpr{id("$$blk"), id("$$key")},
		Body: genBlock(genIf(
			genUnary("!", genCall(genMember(id("$$newKeys"), "has"), id("$$key"))),
			genBlock(
				genExprStmt(genCall(genMember(id("$$blk"), "destroy"))),
				genExprStmt(genCall(genMember(id(keymap), "delete"), id("$$key"))),
			),
		)),
	}
	fastPath := []JsStmt{}
	if incremental != nil {
		rows := id("$$dirtyRows")
		rowIndex := id("$$rowIndex")
		rowBlock := id("$$rowBlock")
		rowUpdate := &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{rowIndex}, Body: genBlock(
			genConst("$$rowBlock", genComputedMember(id(blocks), rowIndex)),
			genIf(genLogical("&&", rowBlock, genMember(rowBlock, "update")),
				genExprStmt(genCall(genMember(rowBlock, "update"), genComputedMember(arr, rowIndex), rowIndex))),
		)}
		canUpdateRows := genLogical("&&", rows, genLogical("&&", arr,
			genBinary("===", genMember(arr, "length"), genMember(id(blocks), "length"))))
		fastPath = append(fastPath,
			genConst("$$dirtyRows", genCall(id("$$eachRowsForMutations"), id("$$mutations"), strLit(incremental.keyField))),
			genIf(canUpdateRows, genBlock(
				genExprStmt(genCall(genMember(rows, "forEach"), rowUpdate)),
				genReturn(nil),
			)),
		)
	}
	return &JsFuncDecl{
		baseStmt: baseStmt{genNode()},
		Name:     eachFn,
		Params:   []JsExpr{arr, id("$$mutations")},
		Body: genBlock(
			genIf(genUnary("!", genMember(id(anchor), "parentNode")), genReturn(nil)),
			genBlock(fastPath...),
			// Moving an already-focused keyed row can make Chromium clear
			// document.activeElement. Remember focus only when it belongs to a
			// retained old row, then restore that exact DOM node after the
			// reconciliation has moved rows and removed dropped keys.
			genConst("$$focusTarget", genMember(id("document"), "activeElement")),
			genConst("$$restoreFocus", genLogical("&&", id("$$focusTarget"), genCall(
				genMember(genCall(generatedMemberPath("Array.from"), genCall(genMember(id(keymap), "values"))), "some"),
				&JsArrowFunc{
					baseExpr: baseExpr{genNode()},
					Params:   []JsExpr{id("$$row")},
					Body: genLogical("||",
						genBinary("===", genMember(id("$$row"), "el"), id("$$focusTarget")),
						genCall(genMember(genMember(id("$$row"), "el"), "contains"), id("$$focusTarget")),
					),
				},
			))),

			genConst("$$newKeys", genNew(id("Set"))),
			genConst("$$newBlocks", genArray()),
			genExprStmt(genCall(genMember(genLogical("||", arr, genArray()), "forEach"), &JsFuncExpr{
				baseExpr: baseExpr{genNode()},
				Params:   []JsExpr{id(itemVar), id(indexVar)},
				Body:     buildRow,
			})),
			genExprStmt(genCall(genMember(id(keymap), "forEach"), removeDropped)),
			genAssignStmt(id(blocks), id("$$newBlocks")),
			genIf(genLogical("&&", id("$$restoreFocus"), genLogical("&&",
				genMember(id("$$focusTarget"), "isConnected"),
				genBinary("===", genUnary("typeof", genMember(id("$$focusTarget"), "focus")), strLit("function")),
			)), genExprStmt(genCall(genMember(id("$$focusTarget"), "focus")))),
		),
	}
}

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
// bindings. Those bindings are redirected to the module-scope
// $$awaitVal/$$awaitErr storage through the scoped template-AST binding
// environment before the chain sees them.
func (g *ASTGen) buildAwaitBlock(block *AwaitBlockNode, parentVar string) {
	blockID := g.id()
	stateVar := "$$promState_" + blockID
	valVar := "$$awaitVal_" + blockID
	errVar := "$$awaitErr_" + blockID
	cellVar := "$$promCell_" + blockID

	// When this {#await} is built inside another branch's build
	// closure, the state-var declarations and driver function
	// must escape that closure — declareInScope handles the
	// routing.  The initial driver call still goes to g.initBody
	// so it fires when the component mounts.
	nestedHoist := g.inCapturedScope()
	declAwait := func(stmt JsStmt) { g.declareInScope(stmt) }

	// Component-scope storage.  State starts at 0 (pending).
	declAwait(genLet(stateVar, numLit(0)))
	declAwait(genLet(valVar, id("undefined")))
	declAwait(genLet(errVar, id("undefined")))
	declAwait(genLet(cellVar, genNull()))

	// Synthetic chain: pending / resolved / rejected.  The {:then}
	// and {:catch} binding names are redirected by a branch-local AST
	// context while that branch plan is built, so sibling branches retain
	// their own lexical meaning without copying template source text.
	branches := []*IfBranch{
		{
			Cond:          stateVar + " === 0",
			SyntheticCond: genBinary("===", id(stateVar), numLit(0)),
			Children:      block.PendingChildren,
		},
	}
	if block.ThenChildren != nil {
		branch := &IfBranch{
			Cond:          stateVar + " === 1",
			SyntheticCond: genBinary("===", id(stateVar), numLit(1)),
			Children:      block.ThenChildren,
		}
		branches = append(branches, branch)
		if block.ValueName != "" {
			if g.branchTemplateBindings == nil {
				g.branchTemplateBindings = map[*IfBranch][]templateBinding{}
			}
			g.branchTemplateBindings[branch] = []templateBinding{{from: block.ValueName, to: valVar}}
		}
	}
	if block.CatchChildren != nil {
		branch := &IfBranch{
			Cond:          stateVar + " === 2",
			SyntheticCond: genBinary("===", id(stateVar), numLit(2)),
			Children:      block.CatchChildren,
		}
		branches = append(branches, branch)
		if block.ErrorName != "" {
			if g.branchTemplateBindings == nil {
				g.branchTemplateBindings = map[*IfBranch][]templateBinding{}
			}
			g.branchTemplateBindings[branch] = []templateBinding{{from: block.ErrorName, to: errVar}}
		}
	}
	chainFn := g.buildIfChain(&IfChainNode{Branches: branches}, parentVar)

	// Promise driver.  Resolving the *current* promise twice is
	// guarded by the cell check: a stale settle cannot clobber a
	// fresher one.  The driver runs once at init and again whenever
	// the promise expression's reactive deps change.
	driver := "$$awaitDrive_" + blockID
	settle := func(name, target string, state int) JsExpr {
		return &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id(name)}, Body: genBlock(
			genIf(genBinary("!==", id(cellVar), id("$$p")), genReturn(nil)),
			genAssignStmt(id(target), id(name)),
			genAssignStmt(id(stateVar), numLit(state)),
			genInvoke(chainFn),
		)}
	}
	driverStmt := &JsFuncDecl{baseStmt: baseStmt{genNode()}, Name: driver, Body: genBlock(
		genConst("$$p", genCall(genMember(id("Promise"), "resolve"), g.templateExpr(block.Expr))),
		genIf(genBinary("===", id(cellVar), id("$$p")), genReturn(nil)),
		genAssignStmt(id(cellVar), id("$$p")),
		genAssignStmt(id(stateVar), numLit(0)),
		genAssignStmt(id(valVar), id("undefined")),
		genAssignStmt(id(errVar), id("undefined")),
		genInvoke(chainFn),
		genExprStmt(genCall(genMember(id("$$p"), "then"), settle("v", valVar, 1), settle("e", errVar, 2))),
	)}

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
	g.initBody = append(g.initBody, genInvoke(driver))

	// Re-run the driver when the promise expression's deps change.
	for _, dep := range templateExprDeps(block.Expr, g.reactiveNames()) {
		g.addUpdate(dep, genInvoke(driver))
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

// removeNodesStmts removes the DOM nodes cached for a branch and clears its handle.
func removeNodesStmts(nodes string) []JsStmt {
	return []JsStmt{
		genForOf("$$node", id(nodes), genExprStmt(genCall(genMember(id("$$node"), "remove")))),
		genAssignStmt(id(nodes), genNull()),
	}
}

// nullCapturedStmts clears branch-local references to detached DOM nodes.
func nullCapturedStmts(captured []string) []JsStmt {
	stmts := make([]JsStmt, 0, len(captured))
	for _, name := range captured {
		stmts = append(stmts, genAssignStmt(id(name), genNull()))
	}
	return stmts
}

// destroyCompStmts tears down child component instances and clears their handles.
func destroyCompStmts(comps []compInstance) []JsStmt {
	stmts := make([]JsStmt, 0, len(comps)*2)
	for _, comp := range comps {
		stmts = append(stmts,
			genIf(id(comp.inst), genBlock(
				genExprStmt(genCall(genMember(id(comp.inst), "destroy"))),
				genAssignStmt(id(comp.inst), genNull()),
			)),
			genAssignStmt(id(comp.wrap), genNull()),
		)
	}
	return stmts
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
		nestedTeardowns []JsStmt
	}
	plans := make([]plan, len(chain.Branches))

	captured := g.inCapturedScope()
	if captured {
		g.declare(genLet(anchor, genNull()))
		g.body = append(g.body, genAssignStmt(
			id(anchor),
			genCall(genMember(id("document"), "createComment"), strLit("if-chain")),
		))
	} else {
		g.body = append(g.body, genConst(
			anchor,
			genCall(genMember(id("document"), "createComment"), strLit("if-chain")),
		))
	}
	for i := range chain.Branches {
		decl := genLet(fn+"_b"+itoa(i), genNull())
		cleanupDecl := genLet(fn+"_b"+itoa(i)+"_cleanup", genNull())
		if captured {
			g.declare(decl)
			g.declare(cleanupDecl)
		} else {
			g.body = append(g.body, decl, cleanupDecl)
		}
	}
	runningDecl := &JsVarDecl{
		baseStmt: baseStmt{genNode()},
		Kind:     "let",
		Decls: []*JsVarDeclarator{
			{Id: id(fn + "_running"), Init: boolLit(false)},
			{Id: id(fn + "_pending"), Init: boolLit(false)},
		},
	}
	if captured {
		g.declare(runningDecl)
	} else {
		g.body = append(g.body, runningDecl)
	}
	if chain.HasKey() {
		keyDecl := genLet(fn+"_keyprev", &JsObjectExpr{baseExpr: baseExpr{genNode()}})
		if captured {
			g.declare(keyDecl)
		} else {
			g.body = append(g.body, keyDecl)
		}
	}

	for i, branch := range chain.Branches {
		g.withStructuralOwner(fn, func() {
			g.withBranchTemplateBindings(branch, func() {
				bv := fn + "_b" + itoa(i)
				frag := bv + "_frag"
				if allStatic(branch.Children) {
					html := emitStaticHTML(branch.Children, g.scopeID)
					plans[i].build = []JsStmt{genInvoke("$$insertHTML", id(frag), strLit(html))}
					return
				}
				if astHoleSubtreeOK(branch.Children) {
					sub := g.buildHoleSubtree(frag, branch.Children, true, bv, false, true)
					if captured {
						for _, decl := range sub.decls {
							g.declare(decl)
						}
					} else {
						g.body = append(g.body, sub.decls...)
					}
					plans[i].build = sub.build
					plans[i].init = sub.initUpdates
					plans[i].captured = sub.captured
					return
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
				plans[i].nestedTeardowns = append([]JsStmt{}, g.nestedTeardowns[tdMark:]...)
				g.fragHoisted = append(g.fragHoisted, cap.fragHoisted...)
				g.compInstances = append(g.compInstances, cap.comps...)
				for dep, stmts := range cap.updates {
					for _, stmt := range stmts {
						g.addUpdate(dep, guardedTemplateStmt(stmt, bv))
					}
				}
			})
		})
	}

	tryBody := []JsStmt{}
	if chain.HasKey() {
		keyNew := fn + "_keynew"
		tryBody = append(tryBody, genConst(keyNew, g.templateExpr(chain.KeyAST)))
		for i := range chain.Branches {
			bv := fn + "_b" + itoa(i)
			teardown := removeNodesStmts(bv)
			teardown = append(teardown, nullCapturedStmts(plans[i].captured)...)
			teardown = append(teardown, destroyCompStmts(plans[i].comps)...)
			tryBody = append(tryBody, genIf(
				genLogical("&&", genBinary("!==", id(fn+"_keyprev"), id(keyNew)), id(bv)),
				genBlock(teardown...),
			))
		}
		tryBody = append(tryBody, genAssignStmt(id(fn+"_keyprev"), id(keyNew)))
	}
	tryBody = append(tryBody, genLet("$$active", numLit(-1)))
	for i, branch := range chain.Branches {
		if !branch.HasCondition() {
			tryBody = append(tryBody, genIf(
				genBinary("<", id("$$active"), numLit(0)),
				genAssignStmt(id("$$active"), numLit(i)),
			))
			continue
		}
		condition := branch.SyntheticCond
		if condition == nil {
			// ValidateTemplateAST rejects a non-synthetic condition without
			// its cached TemplateExpr before codegen. Keep this fallback inert
			// rather than reparsing branch.Cond from source text.
			condition = g.templateExpr(branch.Expr)
		}
		test := genLogical("&&", genBinary("<", id("$$active"), numLit(0)), condition)
		tryBody = append(tryBody, genIf(test, genAssignStmt(id("$$active"), numLit(i))))
	}
	for i := range chain.Branches {
		bv := fn + "_b" + itoa(i)
		cleanupVar := bv + "_cleanup"
		teardown := []JsStmt{}
		if len(plans[i].cleanup) > 0 {
			teardown = append(teardown, genIf(id(cleanupVar), genBlock(
				genInvoke(cleanupVar),
				genAssignStmt(id(cleanupVar), genNull()),
			)))
		}
		teardown = append(teardown, removeNodesStmts(bv)...)
		teardown = append(teardown, nullCapturedStmts(plans[i].captured)...)
		teardown = append(teardown, destroyCompStmts(plans[i].comps)...)
		teardown = append(teardown, plans[i].nestedTeardowns...)
		tryBody = append(tryBody, genIf(
			genLogical("&&", genBinary("!==", id("$$active"), numLit(i)), id(bv)),
			genBlock(teardown...),
		))
	}
	for i := range chain.Branches {
		bv := fn + "_b" + itoa(i)
		frag := bv + "_frag"
		mountBody := []JsStmt{genConst(frag, genCall(genMember(id("document"), "createDocumentFragment")))}
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
			genAssignStmt(id(bv), genArray(&JsSpreadExpr{baseExpr: baseExpr{genNode()}, X: genMember(id(frag), "childNodes")})),
			genExprStmt(genCall(genMember(id(anchor), "before"), id(frag))),
		)
		activeBody := []JsStmt{&JsIfStmt{
			baseStmt: baseStmt{genNode()},
			Test:     genUnary("!", id(bv)),
			Then:     &JsBlock{baseStmt: baseStmt{genNode()}, Body: mountBody},
			Else: genIf(
				genLogical("&&",
					genLogical("&&", genMember(id(anchor), "parentNode"), genMember(id(bv), "length")),
					genBinary("!==", genMember(genComputedMember(id(bv), numLit(0)), "parentNode"), genMember(id(anchor), "parentNode")),
				),
				genForOf("$$node", id(bv), genExprStmt(genCall(genMember(id(anchor), "before"), id("$$node")))),
			),
		}}
		activeBody = append(activeBody, plans[i].init...)
		tryBody = append(tryBody, &JsIfStmt{
			baseStmt: baseStmt{genNode()},
			Test:     genBinary("===", id("$$active"), numLit(i)),
			Then:     &JsBlock{baseStmt: baseStmt{genNode()}, Body: activeBody},
		})
	}
	finallyBody := []JsStmt{
		genAssignStmt(id(fn+"_running"), boolLit(false)),
		genIf(id(fn+"_pending"), genBlock(
			genAssignStmt(id(fn+"_pending"), boolLit(false)),
			genInvoke(fn),
		)),
	}
	body := []JsStmt{
		// A nested chain is also registered against its own reactive
		// dependencies. Those updates may run before the enclosing branch
		// creates (or after it tears down) this chain anchor.
		genIf(genLogical("||", genUnary("!", id(anchor)), genUnary("!", genMember(id(anchor), "parentNode"))), genReturn(nil)),
		genIf(id(fn+"_running"), genBlock(
			genAssignStmt(id(fn+"_pending"), boolLit(true)),
			genReturn(nil),
		)),
		genAssignStmt(id(fn+"_running"), boolLit(true)),
		&JsTryStmt{baseStmt: baseStmt{genNode()}, Block: genBlock(tryBody...), Finalizer: genBlock(finallyBody...)},
	}
	g.declareInScope(&JsFuncDecl{baseStmt: baseStmt{genNode()}, Name: fn, Body: &JsBlock{baseStmt: baseStmt{genNode()}, Body: body}})
	g.body = append(g.body, genExprStmt(genCall(genMember(id(parentVar), "appendChild"), id(anchor))))
	g.initBody = append(g.initBody, genInvoke(fn))
	deps := map[string]bool{}
	for _, branch := range chain.Branches {
		for _, dep := range templateExprDeps(branch.Expr, g.reactiveNames()) {
			deps[dep] = true
		}
	}
	if chain.HasKey() {
		for _, dep := range templateExprDeps(chain.KeyAST, g.reactiveNames()) {
			deps[dep] = true
		}
	}
	for dep := range deps {
		if g.fragCounter >= 0 {
			g.addUpdate(dep, genInvoke(fn))
		} else {
			g.addStructuralUpdate(dep, genInvoke(fn))
		}
	}
	if captured {
		teardown := []JsStmt{}
		for i := range chain.Branches {
			bv := fn + "_b" + itoa(i)
			cleanupVar := bv + "_cleanup"
			teardown = append(teardown, genIf(id(cleanupVar), genBlock(
				genInvoke(cleanupVar),
				genAssignStmt(id(cleanupVar), genNull()),
			)))
			teardown = append(teardown, genIf(id(bv), genBlock(removeNodesStmts(bv)...)))
			teardown = append(teardown, destroyCompStmts(plans[i].comps)...)
			teardown = append(teardown, plans[i].nestedTeardowns...)
		}
		teardown = append(teardown,
			genAssignStmt(id(anchor), genNull()),
			genAssignStmt(id(fn+"_running"), boolLit(false)),
			genAssignStmt(id(fn+"_pending"), boolLit(false)),
		)
		if chain.HasKey() {
			teardown = append(teardown, genAssignStmt(id(fn+"_keyprev"), &JsObjectExpr{baseExpr: baseExpr{genNode()}}))
		}
		g.nestedTeardowns = append(g.nestedTeardowns, teardown...)
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
	g.body = append(g.body, g.createElementStmt("$$root", "div"))
	g.body = append(g.body, g.setAttrStmt("$$root", g.scopeID, ""))
	g.body = append(g.body,
		genAssignStmt(genMember(genMember(id("$$root"), "style"), "display"), strLit("contents")))
	html := emitStaticHTML(nodes, g.scopeID)
	g.body = append(g.body, &JsExprStmt{
		baseStmt: baseStmt{genNode()},
		X: &JsCallExpr{
			baseExpr: baseExpr{genNode()},
			Callee:   id("$$insertHTML"),
			Args:     []JsExpr{id("$$root"), strLit(html)},
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
	if g.domAcquireMode == DOMAcquireHydrate {
		return
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
				R: strLit(html),
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
	// When building inside a captured scope (an {#if} branch via
	// the recursive walker, or a slot closure), the element var
	// must be hoisted: a `const` in the branch-build / slot-closure
	// block is invisible to the component-scope setter cascade that
	// replays the element's reactive attribute updates.  Emit a
	// hoisted `let X = null;` plus an assignment in the body.
	if g.inCapturedScope() {
		// A branch nested in an each-row must keep its captured DOM
		// reference in that row fragment's closure. Routing straight to
		// g.hoisted made independently-built nested each fragments reuse
		// their $$fN names in component scope, producing duplicate lets.
		g.declare(genLet(varName, genNull()))
		g.body = append(g.body,
			genAssignStmt(id(varName), g.acquireElementExpr(varName, el.Tag)))
	} else {
		g.body = append(g.body, g.createElementStmt(varName, el.Tag))
	}
	g.body = append(g.body, g.setAttrStmt(varName, g.scopeID, ""))
	g.applyElementAttrs(varName, el)

	g.buildChildren(el, varName)

	if !isRoot {
		g.body = append(g.body, genExprStmt(genCall(genMember(id(parentVar), "appendChild"), id(varName))))
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
			write := genAssignStmt(genMember(id(varName), prop), genString(g.templateExpr(children[0].(*TextNode).Parts[0].Expr)))
			g.initBody = append(g.initBody, write)
			for _, dep := range findDeps(expr, g.reactiveNames()) {
				g.addUpdate(dep, shareStmt(write))
			}
			return
		}
		if text, ok := staticRawText(children); ok {
			g.body = append(g.body, genAssignStmt(genMember(id(varName), prop), strLit(text)))
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
			for _, decl := range sub.decls {
				g.declare(decl)
			}
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
		g.body = append(g.body,
			genExprStmt(genCall(id("$$insertHTML"), id(parentVar), strLit(html))))
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
		g.body = append(g.body,
			genExprStmt(genCall(genMember(id(parentVar), "appendChild"),
				genCall(genMember(id("document"), "createTextNode"), strLit(text)))))
		return
	}
	refVar := g.id()
	g.body = append(g.body,
		genConst(refVar, genCall(genMember(id("document"), "createTextNode"), strLit(""))),
		genExprStmt(genCall(genMember(id(parentVar), "appendChild"), id(refVar))))
	write := g.textWriteStmt(refVar, tn.Parts)
	g.initBody = append(g.initBody, write)
	for _, dep := range templatePartsDeps(tn.Parts, g.reactiveNames()) {
		g.addUpdate(dep, shareStmt(write))
	}
}

// buildHydrationRootTextChildren adopts direct root text interpolation ranges.
// Its eligibility is deliberately narrow: nested content is static and each
// dynamic part has exactly one paired SSR `v` marker. Initial DOM already
// contains the server value, so only the marker range is acquired here; later
// setters call $$writeHydrationText when a dependency actually changes.
func (g *ASTGen) buildHydrationRootChildren(children []Node, plans []*SSRStructuralLifecyclePlan, parentVar string) {
	if len(children) != len(plans) {
		panic("vela: hydration lifecycle plan does not match template child count")
	}
	for index, child := range children {
		plan := plans[index]
		if plan == nil {
			panic("vela: missing hydration lifecycle child plan")
		}
		switch plan.Kind {
		case SSRLifecyclePlanText:
			current, ok := child.(*TextNode)
			if !ok || current == nil {
				panic("vela: hydration lifecycle text plan does not match template node")
			}
			g.buildHydrationRootTextChildren([]Node{current})
		case SSRLifecyclePlanElement:
			current, ok := child.(*ElementNode)
			if !ok || current == nil {
				panic("vela: hydration lifecycle element plan does not match template node")
			}
			g.buildHydrationElement(current, plan)
		case SSRLifecyclePlanIf:
			current, ok := child.(*IfChainNode)
			if !ok || current == nil {
				panic("vela: hydration lifecycle if plan does not match template node")
			}
			g.buildHydrationRootIfChain(current)
		case SSRLifecyclePlanEach:
			current, ok := child.(*EachBlockNode)
			if !ok || current == nil {
				panic("vela: hydration lifecycle each plan does not match template node")
			}
			g.buildHydrationRootEach(current)
		case SSRLifecyclePlanAwait:
			current, ok := child.(*AwaitBlockNode)
			if !ok || current == nil {
				panic("vela: hydration lifecycle await plan does not match template node")
			}
			g.buildHydrationRootAwait(current)
		case SSRLifecyclePlanComponent:
			current, ok := child.(*ComponentNode)
			if !ok || current == nil {
				panic("vela: hydration lifecycle component plan does not match template node")
			}
			g.buildHydrationComponentNode(current, parentVar, plan)
		case SSRLifecyclePlanSlot:
			if _, ok := child.(*SlotNode); !ok {
				panic("vela: hydration lifecycle slot plan does not match template node")
			}
			// Static fallback or parent-projected slot content already exists in SSR DOM.
			// It is intentionally owned by neither child nor parent hydration walker.
		default:
			panic("vela: unsupported hydration lifecycle plan node")
		}
	}
}

// buildHydrationElement acquires a nested server element and recursively
// adopts its text ranges. Static attributes are already present in SSR HTML,
// so this path emits neither attribute writes nor appendChild calls during
// hydration. The normal acquire-plan therefore remains the one source of
// structural ordering for root and descendant elements alike.
func (g *ASTGen) buildHydrationElement(element *ElementNode, plan *SSRStructuralLifecyclePlan) {
	if element == nil {
		return
	}
	if plan == nil || plan.Kind != SSRLifecyclePlanElement || plan.Span != element.Span {
		panic("vela: hydration lifecycle element plan does not match template span")
	}
	variable := g.id()
	g.body = append(g.body, g.createHydrationTemplateElementStmt(variable, element))
	g.applyHydrationElementAttrs(variable, element)
	g.buildHydrationRootChildren(element.Children, plan.Children, variable)
}

// applyHydrationElementAttrs wires reactive work without replaying SSR's
// initial attributes. Events may attach during adoption, but ordinary
// attributes, class directives, and style directives are written only after a
// reactive dependency changes. DOM bindings stay outside this subset because
// their initial value must reconcile browser-owned form state first.
func (g *ASTGen) applyHydrationElementAttrs(variable string, element *ElementNode) {
	if element == nil {
		return
	}
	for _, attr := range element.Attrs {
		if attr == nil {
			continue
		}
		switch {
		case attr.IsEvent:
			g.emitEventHandler(variable, attr)
		case attr.IsTwoWay:
			g.emitHydrationBinding(variable, element.Tag, attr)
		case attr.IsBindThis:
			g.emitBindThisAST(variable, attr)
		case attr.IsClassDir:
			stmt := g.classDirStmtAST(variable, attr)
			for _, dependency := range templateExprDeps(attr.Expr, g.reactiveNames()) {
				g.addUpdate(dependency, shareStmt(stmt))
			}
		case attr.IsStyleDir:
			stmt := g.styleDirStmtAST(variable, attr)
			for _, dependency := range templateExprDeps(attr.Expr, g.reactiveNames()) {
				g.addUpdate(dependency, shareStmt(stmt))
			}
		case attr.Expr != nil:
			stmt := g.attrWriteStmt(variable, attr)
			for _, dependency := range templateExprDeps(attr.Expr, g.reactiveNames()) {
				g.addUpdate(dependency, shareStmt(stmt))
			}
		}
	}
}

// emitHydrationBinding retains browser-owned form state during adoption. The
// live property is fed through the normal generated setter once, so text typed
// between SSR paint and hydration is not erased. The guarded normal binding
// write then keeps later state-to-DOM updates free of redundant property
// assignments.
func (g *ASTGen) emitHydrationBinding(variable, tag string, attr *Attr) {
	spec, ok := twoWayBindSpec(tag, attr.Name)
	if !ok {
		return
	}
	state, isIdent := templateExprIdent(attr.Expr)
	if !isIdent || g.analysis == nil || !g.analysis.stateSet()[state] {
		return
	}
	handler := variable + "_bind_" + spec.event + "_h"
	g.body = append(g.body, genConst(handler, &JsArrowFunc{
		baseExpr: baseExpr{genNode()},
		Params:   []JsExpr{id("$e")},
		Body:     genCall(id("$$set_"+state), genMember(genMember(id("$e"), "target"), spec.readProp)),
	}))
	g.emitListenerPair(&g.events, variable, spec.event, handler, nil, "", true)
	// Adopt the live control value after listener construction. For unchanged
	// SSR DOM the setter returns immediately; for pre-hydration user input it
	// schedules the usual reactive cascade without a blind value write.
	g.hydrationInitBody = append(g.hydrationInitBody, genInvoke("$$set_"+state, genMember(id(variable), spec.readProp)))
	write := g.bindingWriteStmt(variable, attr)
	for _, dependency := range templateExprDeps(attr.Expr, g.reactiveNames()) {
		g.addUpdate(dependency, shareStmt(write))
	}
}

func (g *ASTGen) buildHydrationRootTextChildren(children []Node) {
	for _, child := range children {
		tn, ok := child.(*TextNode)
		if !ok || tn == nil {
			continue
		}
		for _, part := range tn.Parts {
			if part == nil || part.Static {
				continue
			}
			markerID := g.hydrationTextMarkerID(part)
			rangeVar := "$$h" + itoa(markerID) + "_range"
			g.domAcquirePlan.addTextRange(rangeVar, markerID)
			g.body = append(g.body, genConst(rangeVar, genCall(id("$$acquireHydrationRange"), id("$$walker"), numLit(markerID))))
			write := genExprStmt(genCall(id("$$writeHydrationText"), id(rangeVar), genString(g.templateExpr(part.Expr))))
			for _, dependency := range templateExprDeps(part.Expr, g.reactiveNames()) {
				g.addUpdate(dependency, shareStmt(write))
			}
		}
	}
}

// buildHydrationRootIfChain adopts the branch rendered by SSR and retains it
// until its condition changes. The supported shape is one conditional branch
// with an optional else, both of whose descendants are static. On a later
// change the range alone is replaced; the root and unrelated siblings stay
// intact.
func (g *ASTGen) buildHydrationRootIfChain(chain *IfChainNode) {
	if chain == nil || len(chain.Branches) == 0 {
		return
	}
	if chain.HasKey() {
		if components, ok := ssrHydrationDirectKeyComponents(g.hydrationLifecycleNode(SSRLifecyclePlanIf, chain.Span)); ok {
			g.buildHydrationRootKeyComponents(chain, components)
			return
		}
		g.buildHydrationRootKeyBlock(chain)
		return
	}
	if components, componentIndex, ok := ssrHydrationDirectIfComponents(g.hydrationLifecycleNode(SSRLifecyclePlanIf, chain.Span)); ok {
		g.buildHydrationRootIfComponentChain(chain, components, componentIndex)
		return
	}
	if each, eachIndex, ok := ssrHydrationIfNestedEachBranch(chain); ok {
		g.buildHydrationRootIfNestedEachChain(chain, each, eachIndex)
		return
	}
	markerID := g.hydrationIfMarkerID(chain)

	name := "$$hif" + itoa(markerID)
	rangeVar := name + "_range"
	nodesVar := name + "_nodes"
	activeVar := name + "_active"
	g.domAcquirePlan.addStructuralRange(rangeVar, markerID)
	g.body = append(g.body,
		genConst(rangeVar, genCall(id("$$acquireHydrationRange"), id("$$walker"), numLit(markerID))),
		genLet(nodesVar, genCall(id("$$adoptHydrationRange"), id(rangeVar))),
	)

	activeExpr := JsExpr(numLit(-1))
	for index := len(chain.Branches) - 1; index >= 0; index-- {
		branch := chain.Branches[index]
		if branch == nil {
			continue
		}
		if !branch.HasCondition() {
			activeExpr = numLit(index)
			continue
		}
		activeExpr = &JsConditionalExpr{baseExpr: baseExpr{genNode()}, Test: g.templateExpr(branch.Expr), Then: numLit(index), Else: activeExpr}
	}
	g.body = append(g.body, genLet(activeVar, activeExpr))

	updateBody := []JsStmt{genLet("$$active", activeExpr)}
	updateBody = append(updateBody, genIf(genBinary("===", id("$$active"), id(activeVar)), genReturn(nil)))
	updateBody = append(updateBody, genForOf("$$node", id(nodesVar), genExprStmt(genCall(genMember(id("$$node"), "remove")))))
	updateBody = append(updateBody, genConst("$$frag", genCall(genMember(id("document"), "createDocumentFragment"))))
	for index, branch := range chain.Branches {
		if branch == nil {
			continue
		}
		html := emitStaticHTML(branch.Children, g.scopeID)
		updateBody = append(updateBody, genIf(
			genBinary("===", id("$$active"), numLit(index)),
			genExprStmt(genCall(id("$$insertHTML"), id("$$frag"), strLit(html))),
		))
	}
	updateBody = append(updateBody,
		genAssignStmt(id(nodesVar), genCall(generatedMemberPath("Array.from"), genMember(id("$$frag"), "childNodes"))),
		genExprStmt(genCall(genMember(id(rangeVar), "end.before"), id("$$frag"))),
		genAssignStmt(id(activeVar), id("$$active")),
	)
	fn := name + "_update"
	g.body = append(g.body, &JsFuncDecl{baseStmt: baseStmt{genNode()}, Name: fn, Body: genBlock(updateBody...)})
	for _, branch := range chain.Branches {
		if branch == nil || !branch.HasCondition() {
			continue
		}
		for _, dependency := range templateExprDeps(branch.Expr, g.reactiveNames()) {
			g.addStructuralUpdate(dependency, genInvoke(fn))
		}
	}
	g.cleanup = append(g.cleanup, genIf(id(nodesVar), genBlock(removeNodesStmts(nodesVar)...)))
}

// buildHydrationRootIfNestedEachChain composes the initial marker-aware #each
// adoption with its parent conditional. SSR DOM stays untouched during setup;
// on a branch transition the conditional runs the inner ownership cleanup before
// removing its range, while a later return creates a fresh CSR list branch.
func (g *ASTGen) buildHydrationRootIfNestedEachChain(chain *IfChainNode, each *EachBlockNode, eachIndex int) {
	if chain == nil || each == nil || eachIndex < 0 || eachIndex >= len(chain.Branches) {
		panic("vela: missing nested if each hydration lifecycle")
	}
	markerID := g.hydrationIfMarkerID(chain)
	name := "$$hif" + itoa(markerID)
	rangeVar := name + "_range"
	nodesVar := name + "_nodes"
	activeVar := name + "_active"
	renderVar := name + "_render"

	plans := make([]hydrationAwaitBranchPlan, len(chain.Branches))
	for index, branch := range chain.Branches {
		if branch == nil {
			continue
		}
		plans[index] = g.buildHydrationAwaitBranch(branch.Children, "", "", name+"_b"+itoa(index)+"_frag", name+"_b"+itoa(index)+"_mounted")
	}
	adapter := g.captureHydrationStructuralAdapter(
		g.hydrationLifecycleNode(SSRLifecyclePlanEach, each.Span),
		plans[eachIndex],
		func() { g.buildHydrationRootEach(each) },
	)
	g.installHydrationStructuralAdapter(adapter, &plans[eachIndex])

	g.domAcquirePlan.addStructuralRange(rangeVar, markerID)
	g.body = append(g.body,
		genConst(rangeVar, genCall(id("$$acquireHydrationRange"), id("$$walker"), numLit(markerID))),
		genLet(nodesVar, genCall(id("$$adoptHydrationRange"), id(rangeVar))),
	)
	for _, plan := range plans {
		if plan.mounted != "" {
			g.body = append(g.body, genLet(plan.mounted, boolLit(false)))
		}
		if plan.cleanupVar != "" {
			g.body = append(g.body, genLet(plan.cleanupVar, genNull()))
		}
	}

	activeExpr := JsExpr(numLit(-1))
	for index := len(chain.Branches) - 1; index >= 0; index-- {
		branch := chain.Branches[index]
		if branch == nil {
			continue
		}
		if !branch.HasCondition() {
			activeExpr = numLit(index)
			continue
		}
		activeExpr = &JsConditionalExpr{baseExpr: baseExpr{genNode()}, Test: g.templateExpr(branch.Expr), Then: numLit(index), Else: activeExpr}
	}
	g.body = append(g.body, genLet(activeVar, activeExpr))

	renderBody := []JsStmt{
		genLet("$$active", activeExpr),
		genIf(genBinary("===", id("$$active"), id(activeVar)), genReturn(nil)),
	}
	for index, plan := range plans {
		if len(plan.cleanup) == 0 {
			continue
		}
		renderBody = append(renderBody, genIf(
			genBinary("===", id(activeVar), numLit(index)),
			genBlock(plan.cleanup...),
		))
	}
	renderBody = append(renderBody,
		genForOf("$$node", id(nodesVar), genExprStmt(genCall(genMember(id("$$node"), "remove")))),
		genConst("$$frag", genCall(genMember(id("document"), "createDocumentFragment"))),
	)
	for index, plan := range plans {
		if len(plan.build) == 0 {
			continue
		}
		renderBody = append(renderBody, genIf(
			genBinary("===", id("$$active"), numLit(index)),
			genBlock(plan.build...),
		))
	}
	renderBody = append(renderBody,
		genAssignStmt(id(nodesVar), genCall(generatedMemberPath("Array.from"), genMember(id("$$frag"), "childNodes"))),
		genExprStmt(genCall(genMember(id(rangeVar), "end.before"), id("$$frag"))),
		genAssignStmt(id(activeVar), id("$$active")),
	)
	g.body = append(g.body, &JsFuncDecl{baseStmt: baseStmt{genNode()}, Name: renderVar, Body: genBlock(renderBody...)})
	for _, branch := range chain.Branches {
		if branch == nil || !branch.HasCondition() {
			continue
		}
		for _, dependency := range templateExprDeps(branch.Expr, g.reactiveNames()) {
			g.addStructuralUpdate(dependency, genInvoke(renderVar))
		}
	}
	cleanup := []JsStmt{}
	for _, plan := range plans {
		cleanup = append(cleanup, plan.cleanup...)
	}
	cleanup = append(cleanup, genIf(id(nodesVar), genBlock(removeNodesStmts(nodesVar)...)))
	g.cleanup = append(g.cleanup, cleanup...)
}

// buildHydrationRootKeyComponents adopts every server child owned by a #key
// range. The cached key AST supplies the same runtime namespace used by Go
// SSR; changing it destroys the owned chain and rebuilds static siblings and
// local children in their source order through ordinary CSR factories.
func (g *ASTGen) buildHydrationRootKeyComponents(chain *IfChainNode, components []*ComponentNode) {
	if chain == nil || !chain.HasKey() || len(components) == 0 {
		panic("vela: missing key component lifecycle")
	}
	markerID := g.hydrationIfMarkerID(chain)
	name := "$$hkey" + itoa(markerID)
	rangeVar := name + "_range"
	nodesVar := name + "_nodes"
	keyVar := name + "_prev"
	handles := make([]hydrationIfComponentHandle, 0, len(components))
	for index, component := range components {
		instanceVar := name + "_component"
		if len(components) > 1 {
			instanceVar += "_" + itoa(index)
		}
		handles = append(handles, hydrationIfComponentHandle{
			component: component, instanceVar: instanceVar, propsExpr: g.componentPropsExpr(component.Attrs), csrFactory: g.hydrationCSRComponents[component.Name],
		})
	}
	g.domAcquirePlan.addStructuralRange(rangeVar, markerID)
	for _, handle := range handles {
		if handle.csrFactory == "" {
			panic("vela: missing key component CSR factory")
		}
		g.declareInScope(genLet(handle.instanceVar, genNull()))
	}
	g.body = append(g.body,
		genConst(rangeVar, genCall(id("$$acquireHydrationRange"), id("$$walker"), numLit(markerID))),
		genLet(nodesVar, genCall(id("$$adoptHydrationRange"), id(rangeVar))),
		genLet(keyVar, g.templateExpr(chain.KeyAST)),
	)
	for _, handle := range handles {
		g.hydrationInitBody = append(g.hydrationInitBody,
			genAssignStmt(id(handle.instanceVar), genMember(g.hydrationComponentCallAt(handle.component, id("$$root"), nil, g.hydrationKeyInstanceScope(chain)), "component")),
		)
		deps := map[string]bool{}
		for _, attr := range handle.component.Attrs {
			for _, dependency := range templateExprDeps(attr.Expr, g.reactiveNames()) {
				deps[dependency] = true
			}
		}
		setStmt := genIf(id(handle.instanceVar), genExprStmt(genCall(genMember(id(handle.instanceVar), "set"), handle.propsExpr)))
		for dependency := range deps {
			g.addUpdate(dependency, shareStmt(setStmt))
		}
	}
	updateBody := []JsStmt{
		genConst("$$key", g.templateExpr(chain.KeyAST)),
		genIf(genBinary("===", id("$$key"), id(keyVar)), genReturn(nil)),
	}
	for index := len(handles) - 1; index >= 0; index-- {
		handle := handles[index]
		updateBody = append(updateBody, genIf(id(handle.instanceVar), genBlock(
			genExprStmt(genCall(genMember(id(handle.instanceVar), "destroy"))),
			genAssignStmt(id(handle.instanceVar), genNull()),
		)))
	}
	updateBody = append(updateBody,
		genForOf("$$node", id(nodesVar), genExprStmt(genCall(genMember(id("$$node"), "remove")))),
		genConst("$$frag", genCall(genMember(id("document"), "createDocumentFragment"))),
	)
	updateBody = append(updateBody, g.hydrationIfComponentBranchMount(chain.Branches[0], handles)...)
	updateBody = append(updateBody,
		genAssignStmt(id(nodesVar), genCall(generatedMemberPath("Array.from"), genMember(id("$$frag"), "childNodes"))),
		genExprStmt(genCall(genMember(id(rangeVar), "end.before"), id("$$frag"))),
		genAssignStmt(id(keyVar), id("$$key")),
	)
	fn := name + "_update"
	g.body = append(g.body, &JsFuncDecl{baseStmt: baseStmt{genNode()}, Name: fn, Body: genBlock(updateBody...)})
	for _, dependency := range templateExprDeps(chain.KeyAST, g.reactiveNames()) {
		g.addStructuralUpdate(dependency, genInvoke(fn))
	}
	for index := len(handles) - 1; index >= 0; index-- {
		handle := handles[index]
		g.cleanup = append(g.cleanup, genIf(id(handle.instanceVar), genBlock(
			genExprStmt(genCall(genMember(id(handle.instanceVar), "destroy"))),
			genAssignStmt(id(handle.instanceVar), genNull()),
		)))
	}
	g.cleanup = append(g.cleanup, genIf(id(nodesVar), genBlock(removeNodesStmts(nodesVar)...)))
}

func (g *ASTGen) hydrationKeyInstanceScope(chain *IfChainNode) JsExpr {
	return genBinary("+",
		genBinary("+", id("$$instanceID"), strLit("--key-"+itoa(chain.Span.Start)+"-")),
		genString(g.templateExpr(chain.KeyAST)),
	)
}

// buildHydrationRootIfComponentChain owns a set of direct local children in
// one otherwise static #if branch. Every child has an independent instance
// handle and payload namespace (unique local component bindings only). The
// initial server branch is adopted through hydrate entries; later branch
// materialization mounts every child through its normal CSR factory.
func (g *ASTGen) buildHydrationRootIfComponentChain(chain *IfChainNode, components []*ComponentNode, componentIndex int) {
	if len(components) == 0 {
		panic("vela: missing structural component lifecycle")
	}
	markerID := g.hydrationIfMarkerID(chain)
	name := "$$hif" + itoa(markerID)
	rangeVar := name + "_range"
	nodesVar := name + "_nodes"
	activeVar := name + "_active"
	handles := make([]hydrationIfComponentHandle, 0, len(components))
	for index, component := range components {
		instanceVar := name + "_component"
		if len(components) > 1 {
			instanceVar += "_" + itoa(index)
		}
		handles = append(handles, hydrationIfComponentHandle{
			component: component, instanceVar: instanceVar, propsExpr: g.componentPropsExpr(component.Attrs), csrFactory: g.hydrationCSRComponents[component.Name],
		})
	}
	g.domAcquirePlan.addStructuralRange(rangeVar, markerID)
	for _, handle := range handles {
		if handle.csrFactory == "" {
			panic("vela: missing structural component CSR factory")
		}
		g.declareInScope(genLet(handle.instanceVar, genNull()))
	}
	g.body = append(g.body,
		genConst(rangeVar, genCall(id("$$acquireHydrationRange"), id("$$walker"), numLit(markerID))),
		genLet(nodesVar, genCall(id("$$adoptHydrationRange"), id(rangeVar))),
	)

	activeExpr := JsExpr(numLit(-1))
	for index := len(chain.Branches) - 1; index >= 0; index-- {
		branch := chain.Branches[index]
		if branch == nil {
			continue
		}
		if !branch.HasCondition() {
			activeExpr = numLit(index)
			continue
		}
		activeExpr = &JsConditionalExpr{baseExpr: baseExpr{genNode()}, Test: g.templateExpr(branch.Expr), Then: numLit(index), Else: activeExpr}
	}
	g.body = append(g.body, genLet(activeVar, activeExpr))

	for _, handle := range handles {
		g.hydrationInitBody = append(g.hydrationInitBody, genIf(
			genBinary("===", id(activeVar), numLit(componentIndex)),
			genAssignStmt(id(handle.instanceVar), genMember(g.hydrationComponentCall(handle.component, id("$$root"), nil), "component")),
		))
		setStmt := genIf(id(handle.instanceVar), genExprStmt(genCall(genMember(id(handle.instanceVar), "set"), handle.propsExpr)))
		deps := map[string]bool{}
		for _, attr := range handle.component.Attrs {
			for _, dependency := range templateExprDeps(attr.Expr, g.reactiveNames()) {
				deps[dependency] = true
			}
		}
		for dependency := range deps {
			g.addUpdate(dependency, shareStmt(setStmt))
		}
	}

	updateBody := []JsStmt{
		genLet("$$active", activeExpr),
		genIf(genBinary("===", id("$$active"), id(activeVar)), genReturn(nil)),
	}
	for index := len(handles) - 1; index >= 0; index-- {
		handle := handles[index]
		updateBody = append(updateBody, genIf(id(handle.instanceVar), genBlock(
			genExprStmt(genCall(genMember(id(handle.instanceVar), "destroy"))),
			genAssignStmt(id(handle.instanceVar), genNull()),
		)))
	}
	updateBody = append(updateBody,
		genForOf("$$node", id(nodesVar), genExprStmt(genCall(genMember(id("$$node"), "remove")))),
		genConst("$$frag", genCall(genMember(id("document"), "createDocumentFragment"))),
	)
	for index, branch := range chain.Branches {
		if branch == nil {
			continue
		}
		if index == componentIndex {
			mount := g.hydrationIfComponentBranchMount(branch, handles)
			updateBody = append(updateBody, genIf(genBinary("===", id("$$active"), numLit(index)), genBlock(mount...)))
			continue
		}
		updateBody = append(updateBody, genIf(
			genBinary("===", id("$$active"), numLit(index)),
			genExprStmt(genCall(id("$$insertHTML"), id("$$frag"), strLit(emitStaticHTML(branch.Children, g.scopeID)))),
		))
	}
	updateBody = append(updateBody,
		genAssignStmt(id(nodesVar), genCall(generatedMemberPath("Array.from"), genMember(id("$$frag"), "childNodes"))),
		genExprStmt(genCall(genMember(id(rangeVar), "end.before"), id("$$frag"))),
		genAssignStmt(id(activeVar), id("$$active")),
	)
	fn := name + "_update"
	g.body = append(g.body, &JsFuncDecl{baseStmt: baseStmt{genNode()}, Name: fn, Body: genBlock(updateBody...)})
	for _, branch := range chain.Branches {
		if branch == nil || !branch.HasCondition() {
			continue
		}
		for _, dependency := range templateExprDeps(branch.Expr, g.reactiveNames()) {
			g.addStructuralUpdate(dependency, genInvoke(fn))
		}
	}
	for index := len(handles) - 1; index >= 0; index-- {
		handle := handles[index]
		g.cleanup = append(g.cleanup, genIf(id(handle.instanceVar), genBlock(
			genExprStmt(genCall(genMember(id(handle.instanceVar), "destroy"))),
			genAssignStmt(id(handle.instanceVar), genNull()),
		)))
	}
	g.cleanup = append(g.cleanup, genIf(id(nodesVar), genBlock(removeNodesStmts(nodesVar)...)))
}

type hydrationIfComponentHandle struct {
	component   *ComponentNode
	instanceVar string
	propsExpr   JsExpr
	csrFactory  string
}

// hydrationIfComponentBranchMount recreates static siblings and every owned
// direct child in original template order after a branch replacement.
func (g *ASTGen) hydrationIfComponentBranchMount(branch *IfBranch, handles []hydrationIfComponentHandle) []JsStmt {
	if branch == nil || len(handles) == 0 {
		panic("vela: missing structural component branch lifecycle metadata")
	}
	var body []JsStmt
	for _, child := range branch.Children {
		if component, ok := child.(*ComponentNode); ok {
			matched := false
			for _, handle := range handles {
				if component != handle.component {
					continue
				}
				body = append(body, genAssignStmt(id(handle.instanceVar), genCall(id(handle.csrFactory), id("$$frag"), handle.propsExpr, &JsObjectExpr{baseExpr: baseExpr{genNode()}})))
				matched = true
				break
			}
			if !matched {
				panic("vela: structural component branch does not match lifecycle handles")
			}
			continue
		}
		body = append(body, genExprStmt(genCall(id("$$insertHTML"), id("$$frag"), strLit(emitStaticHTML([]Node{child}, g.scopeID)))))
	}
	return body
}

// buildHydrationRootKeyBlock adopts the rendered static {#key} child range.
// Its key is retained as the cached template AST expression, and the range is
// rebuilt only after that expression changes. Hydration itself therefore never
// recreates the server subtree.
func (g *ASTGen) buildHydrationRootKeyBlock(chain *IfChainNode) {
	markerID := g.hydrationIfMarkerID(chain)
	name := "$$hkey" + itoa(markerID)
	rangeVar := name + "_range"
	nodesVar := name + "_nodes"
	keyVar := name + "_prev"
	g.domAcquirePlan.addStructuralRange(rangeVar, markerID)
	g.body = append(g.body,
		genConst(rangeVar, genCall(id("$$acquireHydrationRange"), id("$$walker"), numLit(markerID))),
		genLet(nodesVar, genCall(id("$$adoptHydrationRange"), id(rangeVar))),
		genLet(keyVar, g.templateExpr(chain.KeyAST)),
	)
	branch := chain.Branches[0]
	updateBody := []JsStmt{
		genConst("$$key", g.templateExpr(chain.KeyAST)),
		genIf(genBinary("===", id("$$key"), id(keyVar)), genReturn(nil)),
	}
	updateBody = append(updateBody, genForOf("$$node", id(nodesVar), genExprStmt(genCall(genMember(id("$$node"), "remove")))))
	updateBody = append(updateBody,
		genConst("$$frag", genCall(genMember(id("document"), "createDocumentFragment"))),
		genExprStmt(genCall(id("$$insertHTML"), id("$$frag"), strLit(emitStaticHTML(branch.Children, g.scopeID)))),
		genAssignStmt(id(nodesVar), genCall(generatedMemberPath("Array.from"), genMember(id("$$frag"), "childNodes"))),
		genExprStmt(genCall(genMember(id(rangeVar), "end.before"), id("$$frag"))),
		genAssignStmt(id(keyVar), id("$$key")),
	)
	fn := name + "_update"
	g.body = append(g.body, &JsFuncDecl{baseStmt: baseStmt{genNode()}, Name: fn, Body: genBlock(updateBody...)})
	for _, dependency := range templateExprDeps(chain.KeyAST, g.reactiveNames()) {
		g.addStructuralUpdate(dependency, genInvoke(fn))
	}
	g.cleanup = append(g.cleanup, genIf(id(nodesVar), genBlock(removeNodesStmts(nodesVar)...)))
}

// buildHydrationRootEach adopts SSR rows for the initial unkeyed static-row
// subset. A later collection replacement clears and materializes only the
// paired range; it never remounts the already acquired component root.
func (g *ASTGen) buildHydrationRootEach(block *EachBlockNode) {
	if block == nil {
		return
	}
	if block.HasKey() {
		if components, ok := ssrHydrationDirectKeyedEachComponents(g.hydrationLifecycleNode(SSRLifecyclePlanEach, block.Span)); ok {
			g.buildHydrationRootKeyedEachComponents(block, components)
			return
		}
		g.buildHydrationRootKeyedEach(block)
		return
	}
	if components, ok := ssrHydrationDirectUnkeyedEachComponents(g.hydrationLifecycleNode(SSRLifecyclePlanEach, block.Span)); ok {
		g.buildHydrationRootEachComponents(block, components)
		return
	}
	markerID := g.hydrationEachMarkerID(block)
	name := "$$heach" + itoa(markerID)
	rangeVar := name + "_range"
	nodesVar := name + "_nodes"
	g.domAcquirePlan.addStructuralRange(rangeVar, markerID)
	g.body = append(g.body,
		genConst(rangeVar, genCall(id("$$acquireHydrationRange"), id("$$walker"), numLit(markerID))),
		genLet(nodesVar, genCall(id("$$adoptHydrationRange"), id(rangeVar))),
	)

	itemsVar := name + "_items"
	hasRowsVar := name + "_hasRows"
	updateBody := []JsStmt{
		genForOf("$$node", id(nodesVar), genExprStmt(genCall(genMember(id("$$node"), "remove")))),
		genConst("$$frag", genCall(genMember(id("document"), "createDocumentFragment"))),
		genConst(itemsVar, g.templateExpr(block.IterExpr)),
		genLet(hasRowsVar, boolLit(false)),
	}
	rowBody := g.hydrationEachRowBuild(block, "$$item", "$$frag")
	rowBody.Body = append([]JsStmt{genAssignStmt(id(hasRowsVar), boolLit(true))}, rowBody.Body...)
	updateBody = append(updateBody, genForOf("$$item", id(itemsVar), rowBody))
	if len(block.ElseChildren) > 0 {
		updateBody = append(updateBody, genIf(genUnary("!", id(hasRowsVar)),
			genExprStmt(genCall(id("$$insertHTML"), id("$$frag"), strLit(emitStaticHTML(block.ElseChildren, g.scopeID)))),
		))
	}
	updateBody = append(updateBody,
		genAssignStmt(id(nodesVar), genCall(generatedMemberPath("Array.from"), genMember(id("$$frag"), "childNodes"))),
		genExprStmt(genCall(genMember(id(rangeVar), "end.before"), id("$$frag"))),
	)
	fn := name + "_update"
	g.body = append(g.body, &JsFuncDecl{baseStmt: baseStmt{genNode()}, Name: fn, Body: genBlock(updateBody...)})
	for _, dependency := range templateExprDeps(block.IterExpr, g.reactiveNames()) {
		g.addStructuralUpdate(dependency, genInvoke(fn))
	}
	g.cleanup = append(g.cleanup, genIf(id(nodesVar), genBlock(removeNodesStmts(nodesVar)...)))
}

// buildHydrationRootEachComponents adopts a source-ordered direct child chain per unkeyed
// SSR row. The current unkeyed reconciler replaces the whole range, so its
// ownership model is explicit: destroy all adopted instances, remove the
// range, then materialize fresh CSR children in list order.
func (g *ASTGen) buildHydrationRootEachComponents(block *EachBlockNode, components []*ComponentNode) {
	if block == nil || len(components) == 0 {
		panic("vela: missing unkeyed each component lifecycle")
	}
	markerID := g.hydrationEachMarkerID(block)
	name := "$$heach" + itoa(markerID)
	rangeVar := name + "_range"
	nodesVar := name + "_nodes"
	instancesVar := name + "_components"
	itemsVar := name + "_items"
	indexVar := name + "_index"
	for _, component := range components {
		if g.hydrationCSRComponents[component.Name] == "" {
			panic("vela: missing unkeyed each component CSR factory")
		}
	}
	g.domAcquirePlan.addStructuralRange(rangeVar, markerID)
	g.body = append(g.body,
		genConst(rangeVar, genCall(id("$$acquireHydrationRange"), id("$$walker"), numLit(markerID))),
		genLet(nodesVar, genCall(id("$$adoptHydrationRange"), id(rangeVar))),
		genLet(instancesVar, genArray()),
		genConst(itemsVar, g.templateExpr(block.IterExpr)),
		genLet(indexVar, numLit(0)),
	)
	adoptScope := g.hydrationEachInstanceScope(block, id(indexVar))
	adoptBody := []JsStmt{}
	for _, component := range components {
		adoptBody = append(adoptBody, genExprStmt(genCall(genMember(id(instancesVar), "push"), genMember(g.hydrationComponentCallAt(component, id("$$root"), nil, adoptScope), "component"))))
	}
	adoptBody = append(adoptBody, genAssignStmt(id(indexVar), genBinary("+", id(indexVar), numLit(1))))
	g.hydrationInitBody = append(g.hydrationInitBody, genForOf("$$item", id(itemsVar), genBlock(adoptBody...)))
	updateBody := []JsStmt{
		genForOf("$$component", id(instancesVar), genExprStmt(genCall(genMember(id("$$component"), "destroy")))),
		genAssignStmt(id(instancesVar), genArray()),
		genForOf("$$node", id(nodesVar), genExprStmt(genCall(genMember(id("$$node"), "remove")))),
		genConst("$$frag", genCall(genMember(id("document"), "createDocumentFragment"))),
		genConst(itemsVar, g.templateExpr(block.IterExpr)),
		genLet(indexVar, numLit(0)),
	}
	mountBody := []JsStmt{}
	for index, child := range block.Children {
		component, ok := child.(*ComponentNode)
		if !ok || component == nil {
			mountBody = append(mountBody, genExprStmt(genCall(id("$$insertHTML"), id("$$frag"), strLit(emitStaticHTML([]Node{child}, g.scopeID)))))
			continue
		}
		propsExpr := g.hydrationEachComponentProps(block, component, "$$item", indexVar)
		componentVar := "$$component_" + itoa(index)
		mountBody = append(mountBody,
			genConst(componentVar, genCall(id(g.hydrationCSRComponents[component.Name]), id("$$frag"), propsExpr, &JsObjectExpr{baseExpr: baseExpr{genNode()}})),
			genExprStmt(genCall(genMember(id(instancesVar), "push"), id(componentVar))),
		)
	}
	mountBody = append(mountBody, genAssignStmt(id(indexVar), genBinary("+", id(indexVar), numLit(1))))
	updateBody = append(updateBody, genForOf("$$item", id(itemsVar), genBlock(mountBody...)))
	if len(block.ElseChildren) > 0 {
		updateBody = append(updateBody, genIf(
			genBinary("===", genMember(id(itemsVar), "length"), numLit(0)),
			genExprStmt(genCall(id("$$insertHTML"), id("$$frag"), strLit(emitStaticHTML(block.ElseChildren, g.scopeID)))),
		))
	}
	updateBody = append(updateBody,
		genAssignStmt(id(nodesVar), genCall(generatedMemberPath("Array.from"), genMember(id("$$frag"), "childNodes"))),
		genExprStmt(genCall(genMember(id(rangeVar), "end.before"), id("$$frag"))),
	)
	fn := name + "_update"
	g.body = append(g.body, &JsFuncDecl{baseStmt: baseStmt{genNode()}, Name: fn, Body: genBlock(updateBody...)})
	for _, dependency := range templateExprDeps(block.IterExpr, g.reactiveNames()) {
		g.addStructuralUpdate(dependency, genInvoke(fn))
	}
	g.cleanup = append(g.cleanup,
		genForOf("$$component", id(instancesVar), genExprStmt(genCall(genMember(id("$$component"), "destroy")))),
		genAssignStmt(id(instancesVar), genArray()),
		genIf(id(nodesVar), genBlock(removeNodesStmts(nodesVar)...)),
	)
}

func (g *ASTGen) hydrationEachComponentProps(block *EachBlockNode, component *ComponentNode, itemVar, indexVar string) JsExpr {
	props := JsExpr(&JsObjectExpr{baseExpr: baseExpr{genNode()}})
	g.withTemplateBinding(block.ItemName, itemVar, func() {
		g.withTemplateBinding(block.IndexName, indexVar, func() {
			props = g.componentPropsExpr(component.Attrs)
		})
	})
	return props
}

func (g *ASTGen) hydrationEachInstanceScope(block *EachBlockNode, index JsExpr) JsExpr {
	value := index
	if block != nil && block.HasKey() {
		g.withTemplateBinding(block.ItemName, "$$item", func() {
			g.withTemplateBinding(block.IndexName, "$$index", func() {
				value = g.templateExpr(block.KeyAST)
			})
		})
	}
	return genBinary("+",
		genBinary("+", id("$$instanceID"), strLit("--each-"+itoa(block.Span.Start)+"-")),
		genString(value),
	)
}

// buildHydrationRootKeyedEachComponent keeps the existing keyed DOM-row
// reconciler authoritative for DOM identity while a parallel map owns child
// component instances. Retained keys preserve both the row and child; removed
// keys destroy their child before the runtime removes its row; new keys mount
// one CSR child and return its root element to the reconciler.
func (g *ASTGen) buildHydrationRootKeyedEachComponents(block *EachBlockNode, components []*ComponentNode) {
	if block == nil || len(components) == 0 || !block.HasKey() {
		panic("vela: missing keyed each component lifecycle")
	}
	markerID := g.hydrationEachMarkerID(block)
	name := "$$heach" + itoa(markerID)
	rangeVar := name + "_range"
	itemsVar := name + "_items"
	keyOfVar := name + "_key"
	blocksVar := name + "_blocks"
	instancesVar := name + "_components"
	markersVar := name + "_markers"
	createVar := name + "_create"
	updateVar := name + "_row_update"
	for _, component := range components {
		if g.hydrationCSRComponents[component.Name] == "" {
			panic("vela: missing keyed each component CSR factory")
		}
	}
	g.domAcquirePlan.addStructuralRange(rangeVar, markerID)
	g.hydrationKeyedRows = true

	keyExpr := JsExpr(id("undefined"))
	g.withTemplateBinding(block.ItemName, "$$item", func() {
		g.withTemplateBinding(block.IndexName, "$$index", func() {
			keyExpr = genString(g.templateExpr(block.KeyAST))
		})
	})
	keyOf := &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$item"), id("$$index")}, Body: keyExpr}
	createBody := []JsStmt{
		genConst("$$frag", genCall(genMember(id("document"), "createDocumentFragment"))),
		genConst("$$key", genCall(id(keyOfVar), id("$$item"), id("$$index"))),
		genConst("$$start", genCall(genMember(id("document"), "createComment"), strLit("r"))),
		genExprStmt(genCall(genMember(id("$$frag"), "append"), id("$$start"))),
		genConst("$$components", genArray()),
	}
	componentIndex := 0
	for _, child := range block.Children {
		current, ok := child.(*ComponentNode)
		if !ok || current == nil {
			createBody = append(createBody, genExprStmt(genCall(id("$$insertHTML"), id("$$frag"), strLit(emitStaticHTML([]Node{child}, g.scopeID)))))
			continue
		}
		props := g.hydrationEachComponentProps(block, current, "$$item", "$$index")
		instance := "$$component_" + itoa(componentIndex)
		createBody = append(createBody,
			genConst(instance, genCall(id(g.hydrationCSRComponents[current.Name]), id("$$frag"), props, &JsObjectExpr{baseExpr: baseExpr{genNode()}})),
			genExprStmt(genCall(genMember(id("$$components"), "push"), id(instance))),
		)
		componentIndex++
	}
	createBody = append(createBody,
		genConst("$$end", genCall(genMember(id("document"), "createComment"), strLit("/r"))),
		genExprStmt(genCall(genMember(id("$$frag"), "append"), id("$$end"))),
		genExprStmt(genCall(genMember(id(instancesVar), "set"), id("$$key"), id("$$components"))),
		genReturn(&JsObjectExpr{baseExpr: baseExpr{genNode()}, Props: []*JsProp{{Key: id("start"), Value: id("$$start")}, {Key: id("end"), Value: id("$$end")}}}),
	)
	create := &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$item"), id("$$index")}, Body: genBlock(createBody...)}
	rowUpdateBody := []JsStmt{genConst("$$components", genCall(genMember(id(instancesVar), "get"), genCall(id(keyOfVar), id("$$item"), id("$$index"))))}
	for index, current := range components {
		props := g.hydrationEachComponentProps(block, current, "$$item", "$$index")
		rowUpdateBody = append(rowUpdateBody, genIf(genComputedMember(id("$$components"), numLit(index)), genExprStmt(genCall(genMember(genComputedMember(id("$$components"), numLit(index)), "set"), props))))
	}
	update := &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$row"), id("$$item"), id("$$index")}, Body: genBlock(rowUpdateBody...)}

	g.body = append(g.body,
		genConst(rangeVar, genCall(id("$$acquireHydrationRange"), id("$$walker"), numLit(markerID))),
		genConst(keyOfVar, keyOf),
		genConst(itemsVar, g.templateExpr(block.IterExpr)),
		genLet(blocksVar, genCall(id("$$adoptHydrationKeyedRowRanges"), id(rangeVar), id(itemsVar), id(keyOfVar), numLit(markerID))),
		genLet(instancesVar, genNew(id("Map"))),
		genLet(markersVar, boolLit(true)),
		genConst(createVar, create),
		genConst(updateVar, update),
	)
	initialIndex := name + "_index"
	initialScope := g.hydrationEachInstanceScope(block, id(initialIndex))
	initialBody := []JsStmt{
		genConst("$$key", genCall(id(keyOfVar), id("$$item"), id(initialIndex))),
		genConst("$$components", genArray()),
	}
	for _, current := range components {
		initialBody = append(initialBody, genExprStmt(genCall(genMember(id("$$components"), "push"), genMember(g.hydrationComponentCallAt(current, id("$$root"), nil, initialScope), "component"))))
	}
	initialBody = append(initialBody,
		genExprStmt(genCall(genMember(id(instancesVar), "set"), id("$$key"), id("$$components"))),
		genAssignStmt(id(initialIndex), genBinary("+", id(initialIndex), numLit(1))),
	)
	g.hydrationInitBody = append(g.hydrationInitBody, genLet(initialIndex, numLit(0)), genForOf("$$item", id(itemsVar), genBlock(initialBody...)))

	updateBody := []JsStmt{
		genConst("$$nextItems", g.templateExpr(block.IterExpr)),
		genConst("$$nextKeys", genNew(id("Set"))),
		genLet("$$index", numLit(0)),
		genForOf("$$item", id("$$nextItems"), genBlock(
			genExprStmt(genCall(genMember(id("$$nextKeys"), "add"), genCall(id(keyOfVar), id("$$item"), id("$$index")))),
			genAssignStmt(id("$$index"), genBinary("+", id("$$index"), numLit(1))),
		)),
		genExprStmt(genCall(genMember(id(instancesVar), "forEach"), &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$components"), id("$$key")}, Body: genBlock(
			genIf(genUnary("!", genCall(genMember(id("$$nextKeys"), "has"), id("$$key"))), genBlock(
				genForOf("$$component", genCall(genMember(genCall(genMember(id("$$components"), "slice")), "reverse")), genExprStmt(genCall(genMember(id("$$component"), "destroy")))),
				genExprStmt(genCall(genMember(id(instancesVar), "delete"), id("$$key"))),
			)),
		)})),
		genIf(id(markersVar), genBlock(
			genExprStmt(genCall(id("$$clearHydrationKeyedRowMarkers"), id(rangeVar))),
			genAssignStmt(id(markersVar), boolLit(false)),
		)),
		genAssignStmt(id(blocksVar), genCall(id("$$reconcileHydrationKeyedRowRanges"), id(rangeVar), id(blocksVar), id("$$nextItems"), id(keyOfVar), id(createVar), id(updateVar))),
	}
	fn := name + "_update"
	g.body = append(g.body, &JsFuncDecl{baseStmt: baseStmt{genNode()}, Name: fn, Body: genBlock(updateBody...)})
	for _, dependency := range templateExprDeps(block.IterExpr, g.reactiveNames()) {
		g.addStructuralUpdate(dependency, genInvoke(fn))
	}
	g.cleanup = append(g.cleanup,
		genExprStmt(genCall(genMember(id(instancesVar), "forEach"), &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$components")}, Body: genBlock(genForOf("$$component", genCall(genMember(genCall(genMember(id("$$components"), "slice")), "reverse")), genExprStmt(genCall(genMember(id("$$component"), "destroy")))))})),
		genExprStmt(genCall(genMember(id(instancesVar), "clear"))),
		genExprStmt(genCall(genMember(id(blocksVar), "forEach"), &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$row")}, Body: genBlock(genExprStmt(genCall(id("$$removeHydrationKeyedRowRange"), id("$$row"))))})),
	)
}

func (g *ASTGen) hydrationKeyedComponentRowTag(component *ComponentNode) string {
	if component == nil {
		return ""
	}
	// SSR child roots are the direct keyed rows. The hydration walker validates
	// exact element tags through component markers; an empty tag asks the
	// runtime to count every direct element inside this capability-checked row.
	return ""
}

// buildHydrationRootAwait retains the pending branch emitted by SSR, then
// drives only the paired marker range after the browser promise settles. The
// initial active state is deliberately pending, so invoking the driver during
// hydration attaches promise callbacks without recreating or rewriting the
// server DOM.
//
// CanBuildSSRHydrationClient requires a static pending branch and limits
// then/catch to static HTML plus text holes. Those holes are still lowered
// from cached TemplateAST and bind to the settled value or error.
func (g *ASTGen) buildHydrationRootAwait(block *AwaitBlockNode) {
	if block == nil {
		return
	}
	if branches, ok := ssrHydrationDirectAwaitComponents(g.hydrationLifecycleNode(SSRLifecyclePlanAwait, block.Span)); ok {
		g.buildHydrationRootAwaitComponentBranches(block, branches)
		return
	}
	markerID := g.hydrationAwaitMarkerID(block)
	name := "$$hawait" + itoa(markerID)
	rangeVar := name + "_range"
	nodesVar := name + "_nodes"
	activeVar := name + "_active"
	stateVar := name + "_state"
	valueVar := name + "_value"
	errorVar := name + "_error"
	cellVar := name + "_cell"
	renderVar := name + "_render"
	driverVar := name + "_drive"

	plans := []hydrationAwaitBranchPlan{
		g.buildHydrationAwaitBranch(block.PendingChildren, "", "", name+"_b0_frag", ""),
		g.buildHydrationAwaitBranch(block.ThenChildren, block.ValueName, valueVar, name+"_b1_frag", name+"_b1_mounted"),
		g.buildHydrationAwaitBranch(block.CatchChildren, block.ErrorName, errorVar, name+"_b2_frag", name+"_b2_mounted"),
	}
	// The initial pending branch is already present in server DOM. For the
	// narrow nested-if slice, build its marker-aware adoption lifecycle into
	// a temporary scope, then give its destroy handles to the outer await
	// range. The generic await branch builder above remains responsible for
	// recreating that same branch with CSR factories after a later promise
	// replacement.
	if pendingIf, ok := ssrHydrationPendingAwaitIfChain(block); ok {
		adapter := g.captureHydrationStructuralAdapter(g.hydrationLifecycleNode(SSRLifecyclePlanIf, pendingIf.Span), plans[0], func() {
			g.buildHydrationRootIfChain(pendingIf)
		})
		g.installHydrationStructuralAdapter(adapter, &plans[0])
	} else if pendingEach, ok := ssrHydrationPendingAwaitEachBlock(block); ok {
		adapter := g.captureHydrationStructuralAdapter(g.hydrationLifecycleNode(SSRLifecyclePlanEach, pendingEach.Span), plans[0], func() {
			g.buildHydrationRootEach(pendingEach)
		})
		g.installHydrationStructuralAdapter(adapter, &plans[0])
	}
	g.domAcquirePlan.addStructuralRange(rangeVar, markerID)
	g.body = append(g.body,
		genConst(rangeVar, genCall(id("$$acquireHydrationRange"), id("$$walker"), numLit(markerID))),
		genLet(nodesVar, genCall(id("$$adoptHydrationRange"), id(rangeVar))),
		genLet(activeVar, numLit(0)),
		genLet(stateVar, numLit(0)),
		genLet(valueVar, id("undefined")),
		genLet(errorVar, id("undefined")),
		genLet(cellVar, genNull()),
	)
	for _, plan := range plans {
		if plan.mounted != "" {
			g.body = append(g.body, genLet(plan.mounted, boolLit(false)))
		}
		if plan.cleanupVar != "" {
			g.body = append(g.body, genLet(plan.cleanupVar, genNull()))
		}
	}

	renderBody := []JsStmt{
		genIf(genBinary("===", id(stateVar), id(activeVar)), genReturn(nil)),
	}
	for state, plan := range plans {
		if len(plan.cleanup) == 0 {
			continue
		}
		renderBody = append(renderBody, genIf(
			genBinary("===", id(activeVar), numLit(state)),
			genBlock(plan.cleanup...),
		))
	}
	renderBody = append(renderBody,
		genForOf("$$node", id(nodesVar), genExprStmt(genCall(genMember(id("$$node"), "remove")))),
		genConst("$$frag", genCall(genMember(id("document"), "createDocumentFragment"))),
	)
	for state, plan := range plans {
		if len(plan.build) == 0 {
			continue
		}
		renderBody = append(renderBody, genIf(
			genBinary("===", id(stateVar), numLit(state)),
			genBlock(plan.build...),
		))
	}
	renderBody = append(renderBody,
		genAssignStmt(id(nodesVar), genCall(generatedMemberPath("Array.from"), genMember(id("$$frag"), "childNodes"))),
		genExprStmt(genCall(genMember(id(rangeVar), "end.before"), id("$$frag"))),
		genAssignStmt(id(activeVar), id(stateVar)),
	)
	g.body = append(g.body, &JsFuncDecl{baseStmt: baseStmt{genNode()}, Name: renderVar, Body: genBlock(renderBody...)})

	settle := func(param, target string, state int) JsExpr {
		return &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id(param)}, Body: genBlock(
			genIf(genBinary("!==", id(cellVar), id("$$p")), genReturn(nil)),
			genAssignStmt(id(target), id(param)),
			genAssignStmt(id(stateVar), numLit(state)),
			genInvoke(renderVar),
		)}
	}
	g.body = append(g.body, &JsFuncDecl{baseStmt: baseStmt{genNode()}, Name: driverVar, Body: genBlock(
		genConst("$$p", genCall(genMember(id("Promise"), "resolve"), g.templateExpr(block.Expr))),
		genIf(genBinary("===", id(cellVar), id("$$p")), genReturn(nil)),
		genAssignStmt(id(cellVar), id("$$p")),
		genAssignStmt(id(stateVar), numLit(0)),
		genAssignStmt(id(valueVar), id("undefined")),
		genAssignStmt(id(errorVar), id("undefined")),
		genInvoke(renderVar),
		genExprStmt(genCall(genMember(id("$$p"), "then"), settle("$$value", valueVar, 1), settle("$$error", errorVar, 2))),
	)})
	g.hydrationInitBody = append(g.hydrationInitBody, genInvoke(driverVar))
	for _, dependency := range templateExprDeps(block.Expr, g.reactiveNames()) {
		g.addStructuralUpdate(dependency, genInvoke(driverVar))
	}
	cleanup := []JsStmt{}
	for _, plan := range plans {
		cleanup = append(cleanup, plan.cleanup...)
	}
	cleanup = append(cleanup, genIf(id(nodesVar), genBlock(removeNodesStmts(nodesVar)...)))
	g.cleanup = append(g.cleanup, cleanup...)
}

// hydrationStructuralAdapter is the common ownership transfer unit for a
// server-rendered structural subtree. It owns the initial SSR adoption and
// the existing CSR re-entry plan separately, so a recursive lowerer can later
// compose adapters instead of falling back to static HTML.
type hydrationStructuralAdapter struct {
	plan          *SSRStructuralLifecyclePlan
	adopted       string
	adoptBody     []JsStmt
	adoptInitBody []JsStmt
	mountCSR      []JsStmt
	destroySSR    []JsStmt
	destroyCSR    []JsStmt
	updates       map[string][]JsStmt
}

func (g *ASTGen) captureHydrationStructuralAdapter(plan *SSRStructuralLifecyclePlan, csr hydrationAwaitBranchPlan, build func()) hydrationStructuralAdapter {
	if plan == nil || !plan.hasOperation(SSRLifecycleAdopt) || !plan.hasOperation(SSRLifecycleDestroy) || build == nil {
		panic("vela: missing structural hydration adapter lifecycle")
	}
	captured := g.captureScope(build)
	adopted := "$$hssr" + itoa(plan.Span.Start) + "_" + itoa(plan.Span.End)
	destroySSR := append([]JsStmt(nil), captured.cleanup...)
	if len(destroySSR) > 0 {
		destroySSR = []JsStmt{genIf(id(adopted), genBlock(append(destroySSR, genAssignStmt(id(adopted), boolLit(false)))...))}
	}
	return hydrationStructuralAdapter{plan: plan, adopted: adopted, adoptBody: captured.body, adoptInitBody: captured.hydrationInitBody, mountCSR: append([]JsStmt(nil), csr.build...), destroySSR: destroySSR, destroyCSR: append([]JsStmt(nil), csr.cleanup...), updates: captured.updates}
}

// installHydrationStructuralAdapter transfers a captured structural subtree to
// an await pending branch. The await owns destruction before it removes its
// outer range; reactive updates remain component-scoped and retain the child
// adapter guards emitted by the concrete lowerer.
func (g *ASTGen) installHydrationStructuralAdapter(adapter hydrationStructuralAdapter, branch *hydrationAwaitBranchPlan) {
	if adapter.plan == nil || branch == nil {
		panic("vela: missing pending await structural adapter")
	}
	g.body = append(g.body, genLet(adapter.adopted, boolLit(true)))
	g.body = append(g.body, adapter.adoptBody...)
	g.hydrationInitBody = append(g.hydrationInitBody, adapter.adoptInitBody...)
	branch.build = append(branch.build[:0], adapter.mountCSR...)
	branch.cleanup = append(branch.cleanup[:0], adapter.destroyCSR...)
	branch.cleanup = append(branch.cleanup, adapter.destroySSR...)
	for dependency, statements := range adapter.updates {
		for _, statement := range statements {
			g.addUpdate(dependency, guardedTemplateStmt(statement, adapter.adopted))
		}
	}
}

type hydrationAwaitComponentHandle struct {
	component   *ComponentNode
	instanceVar string
	propsExpr   JsExpr
	csrFactory  string
	branch      int
}

// buildHydrationRootAwaitComponentBranches owns direct local child chains in
// an await range. SSR always supplies the pending branch, so only its handles
// are adopted. Once a promise settles, every branch is materialized from the
// source-ordered typed children with ordinary CSR factories.
func (g *ASTGen) buildHydrationRootAwaitComponentBranches(block *AwaitBlockNode, branches [][]*ComponentNode) {
	if block == nil || len(branches) != 3 {
		panic("vela: missing await component lifecycle")
	}
	markerID := g.hydrationAwaitMarkerID(block)
	name := "$$hawait" + itoa(markerID)
	rangeVar := name + "_range"
	nodesVar := name + "_nodes"
	activeVar := name + "_active"
	stateVar := name + "_state"
	valueVar := name + "_value"
	errorVar := name + "_error"
	cellVar := name + "_cell"
	renderVar := name + "_render"
	driverVar := name + "_drive"
	bindingNames := []string{"", block.ValueName, block.ErrorName}
	bindingValues := []string{"", valueVar, errorVar}
	children := [][]Node{block.PendingChildren, block.ThenChildren, block.CatchChildren}
	handles := make([]hydrationAwaitComponentHandle, 0)
	for branch, components := range branches {
		for index, component := range components {
			handle := hydrationAwaitComponentHandle{
				component:   component,
				instanceVar: name + "_b" + itoa(branch) + "_component_" + itoa(index),
				csrFactory:  g.hydrationCSRComponents[component.Name],
				branch:      branch,
			}
			if handle.csrFactory == "" {
				panic("vela: missing await component CSR factory")
			}
			if bindingNames[branch] == "" {
				handle.propsExpr = g.componentPropsExpr(component.Attrs)
			} else {
				g.withTemplateBinding(bindingNames[branch], bindingValues[branch], func() {
					handle.propsExpr = g.componentPropsExpr(component.Attrs)
				})
			}
			handles = append(handles, handle)
		}
	}
	g.domAcquirePlan.addStructuralRange(rangeVar, markerID)
	for _, handle := range handles {
		g.declareInScope(genLet(handle.instanceVar, genNull()))
	}
	g.body = append(g.body,
		genConst(rangeVar, genCall(id("$$acquireHydrationRange"), id("$$walker"), numLit(markerID))),
		genLet(nodesVar, genCall(id("$$adoptHydrationRange"), id(rangeVar))),
		genLet(activeVar, numLit(0)),
		genLet(stateVar, numLit(0)),
		genLet(valueVar, id("undefined")),
		genLet(errorVar, id("undefined")),
		genLet(cellVar, genNull()),
	)
	for _, handle := range handles {
		if handle.branch != 0 {
			continue
		}
		g.hydrationInitBody = append(g.hydrationInitBody,
			genAssignStmt(id(handle.instanceVar), genMember(g.hydrationComponentCall(handle.component, id("$$root"), nil), "component")),
		)
	}
	for _, handle := range handles {
		setStmt := genIf(
			&JsLogicalExpr{baseExpr: baseExpr{genNode()}, Op: "&&", L: genBinary("===", id(activeVar), numLit(handle.branch)), R: id(handle.instanceVar)},
			genExprStmt(genCall(genMember(id(handle.instanceVar), "set"), handle.propsExpr)),
		)
		deps := map[string]bool{}
		for _, attr := range handle.component.Attrs {
			for _, dependency := range templateExprDeps(attr.Expr, g.reactiveNames()) {
				deps[dependency] = true
			}
		}
		for dependency := range deps {
			g.addUpdate(dependency, shareStmt(setStmt))
		}
	}

	renderBody := []JsStmt{
		genIf(genBinary("===", id(stateVar), id(activeVar)), genReturn(nil)),
	}
	for index := len(handles) - 1; index >= 0; index-- {
		handle := handles[index]
		renderBody = append(renderBody, genIf(id(handle.instanceVar), genBlock(
			genExprStmt(genCall(genMember(id(handle.instanceVar), "destroy"))),
			genAssignStmt(id(handle.instanceVar), genNull()),
		)))
	}
	renderBody = append(renderBody,
		genForOf("$$node", id(nodesVar), genExprStmt(genCall(genMember(id("$$node"), "remove")))),
		genConst("$$frag", genCall(genMember(id("document"), "createDocumentFragment"))),
	)
	for branch := range children {
		mount := g.hydrationAwaitComponentBranchMount(children[branch], handles, branch)
		renderBody = append(renderBody, genIf(
			genBinary("===", id(stateVar), numLit(branch)),
			genBlock(mount...),
		))
	}
	renderBody = append(renderBody,
		genAssignStmt(id(nodesVar), genCall(generatedMemberPath("Array.from"), genMember(id("$$frag"), "childNodes"))),
		genExprStmt(genCall(genMember(id(rangeVar), "end.before"), id("$$frag"))),
		genAssignStmt(id(activeVar), id(stateVar)),
	)
	g.body = append(g.body, &JsFuncDecl{baseStmt: baseStmt{genNode()}, Name: renderVar, Body: genBlock(renderBody...)})

	settle := func(param, target string, state int) JsExpr {
		return &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id(param)}, Body: genBlock(
			genIf(genBinary("!==", id(cellVar), id("$$p")), genReturn(nil)),
			genAssignStmt(id(target), id(param)),
			genAssignStmt(id(stateVar), numLit(state)),
			genInvoke(renderVar),
		)}
	}
	g.body = append(g.body, &JsFuncDecl{baseStmt: baseStmt{genNode()}, Name: driverVar, Body: genBlock(
		genConst("$$p", genCall(genMember(id("Promise"), "resolve"), g.templateExpr(block.Expr))),
		genIf(genBinary("===", id(cellVar), id("$$p")), genReturn(nil)),
		genAssignStmt(id(cellVar), id("$$p")),
		genAssignStmt(id(stateVar), numLit(0)),
		genAssignStmt(id(valueVar), id("undefined")),
		genAssignStmt(id(errorVar), id("undefined")),
		genInvoke(renderVar),
		genExprStmt(genCall(genMember(id("$$p"), "then"), settle("$$value", valueVar, 1), settle("$$error", errorVar, 2))),
	)})
	g.hydrationInitBody = append(g.hydrationInitBody, genInvoke(driverVar))
	for _, dependency := range templateExprDeps(block.Expr, g.reactiveNames()) {
		g.addStructuralUpdate(dependency, genInvoke(driverVar))
	}
	for index := len(handles) - 1; index >= 0; index-- {
		handle := handles[index]
		g.cleanup = append(g.cleanup, genIf(id(handle.instanceVar), genBlock(
			genExprStmt(genCall(genMember(id(handle.instanceVar), "destroy"))),
			genAssignStmt(id(handle.instanceVar), genNull()),
		)))
	}
	g.cleanup = append(g.cleanup, genIf(id(nodesVar), genBlock(removeNodesStmts(nodesVar)...)))
}

// hydrationAwaitComponentBranchMount recreates one await branch in exact
// source order. Static siblings are emitted through the existing typed static
// HTML printer, while components use their CSR aliases and retain handles for
// subsequent prop updates and deterministic teardown.
func (g *ASTGen) hydrationAwaitComponentBranchMount(children []Node, handles []hydrationAwaitComponentHandle, branch int) []JsStmt {
	body := make([]JsStmt, 0, len(children))
	for _, child := range children {
		component, isComponent := child.(*ComponentNode)
		if !isComponent {
			body = append(body, genExprStmt(genCall(id("$$insertHTML"), id("$$frag"), strLit(emitStaticHTML([]Node{child}, g.scopeID)))))
			continue
		}
		var handle *hydrationAwaitComponentHandle
		for index := range handles {
			current := &handles[index]
			if current.branch == branch && current.component == component {
				handle = current
				break
			}
		}
		if handle == nil {
			panic("vela: await component branch does not match lifecycle handles")
		}
		body = append(body, genAssignStmt(id(handle.instanceVar), genCall(
			id(handle.csrFactory), id("$$frag"), handle.propsExpr, &JsObjectExpr{baseExpr: baseExpr{genNode()}},
		)))
	}
	return body
}

type hydrationAwaitBranchPlan struct {
	build      []JsStmt
	cleanup    []JsStmt
	cleanupVar string
	mounted    string
}

// buildHydrationAwaitBranch creates a branch that did not exist in SSR DOM.
// Its text-hole references are component-scoped and nulled when the branch
// leaves the await range, so later parent-state flushes cannot write detached
// nodes. The branch content still originates from TemplateAST through
// buildHoleSubtree; no template text is re-parsed at code generation time.
func (g *ASTGen) buildHydrationAwaitBranch(children []Node, bindingName, bindingValue, targetVar, mountedVar string) hydrationAwaitBranchPlan {
	if len(children) == 0 {
		return hydrationAwaitBranchPlan{}
	}

	if allStatic(children) {
		return hydrationAwaitBranchPlan{build: []JsStmt{
			genExprStmt(genCall(id("$$insertHTML"), id("$$frag"), strLit(emitStaticHTML(children, g.scopeID)))),
		}}
	}

	var plan hydrationAwaitBranchPlan
	plan.cleanupVar = targetVar + "_cleanup"
	g.withTemplateBinding(bindingName, bindingValue, func() {
		wasBranchScope := g.inBranchScope
		cap := g.captureScope(func() {
			g.inBranchScope = true
			wasAcquireMode := g.domAcquireMode
			g.domAcquireMode = DOMAcquireCreate
			defer func() {
				g.inBranchScope = wasBranchScope
				g.domAcquireMode = wasAcquireMode
			}()
			g.buildChildRun(children, targetVar)
		})
		plan.mounted = mountedVar
		plan.build = append(plan.build, genConst(targetVar, id("$$frag")))
		plan.build = append(plan.build, cap.body...)
		plan.build = append(plan.build, cap.events...)
		plan.build = append(plan.build, cap.initBody...)
		if len(cap.cleanup) > 0 {
			// cap.cleanup can reference handlers declared while this branch
			// is built. Retain it as a closure rather than replaying it from
			// the later render/destroy scope, where those branch locals do
			// not exist.
			plan.build = append(plan.build, genAssignStmt(id(plan.cleanupVar), &JsArrowFunc{
				baseExpr: baseExpr{genNode()},
				Body:     genBlock(cap.cleanup...),
			}))
			plan.cleanup = append(plan.cleanup, genIf(id(plan.cleanupVar), genBlock(
				genInvoke(plan.cleanupVar),
				genAssignStmt(id(plan.cleanupVar), genNull()),
			)))
		}
		plan.cleanup = append(plan.cleanup, cap.nestedTeardowns...)
		if mountedVar != "" {
			plan.build = append(plan.build, genAssignStmt(id(mountedVar), boolLit(true)))
			plan.cleanup = append(plan.cleanup, genAssignStmt(id(mountedVar), boolLit(false)))
		}
		for dep, statements := range cap.updates {
			for _, statement := range statements {
				g.addUpdate(dep, guardedTemplateStmt(statement, mountedVar))
			}
		}
	})
	return plan
}

// buildHydrationRootKeyedEach adopts the SSR row elements into a key-to-node
// map.  It never touches those rows during hydration.  The first later list
// update removes only compiler marker comments, then the runtime reconciler
// retains, reorders, creates, updates, and removes rows by their cached
// KeyAST-derived identity.
//
// CanBuildSSRHydrationClient deliberately admits only one ordinary row
// element with direct text children and no {:else} for this path.  That makes
// a row an actual element (rather than a synthetic range) and keeps the
// identity contract small and testable before nested keyed ranges are added.
func (g *ASTGen) buildHydrationRootKeyedEach(block *EachBlockNode) {
	markerID := g.hydrationEachMarkerID(block)
	name := "$$heach" + itoa(markerID)
	rangeVar := name + "_range"
	itemsVar := name + "_items"
	keyOfVar := name + "_key"
	blocksVar := name + "_blocks"
	markersVar := name + "_markers"
	createVar := name + "_create"
	updateVar := name + "_row_update"
	g.domAcquirePlan.addStructuralRange(rangeVar, markerID)
	g.hydrationKeyedRows = true

	keyExpr := JsExpr(id("undefined"))
	g.withTemplateBinding(block.ItemName, "$$item", func() {
		g.withTemplateBinding(block.IndexName, "$$index", func() {
			keyExpr = genString(g.templateExpr(block.KeyAST))
		})
	})
	keyOf := &JsArrowFunc{
		baseExpr: baseExpr{genNode()},
		Params:   []JsExpr{id("$$item"), id("$$index")},
		Body:     keyExpr,
	}
	create := g.hydrationEachRowCreate(block)
	update := g.hydrationEachRowUpdate(block)

	g.body = append(g.body,
		genConst(rangeVar, genCall(id("$$acquireHydrationRange"), id("$$walker"), numLit(markerID))),
		genConst(keyOfVar, keyOf),
		genConst(itemsVar, g.templateExpr(block.IterExpr)),
		genLet(blocksVar, genCall(id("$$adoptHydrationKeyedRows"), id(rangeVar), id(itemsVar), id(keyOfVar), strLit(g.hydrationEachRowTag(block)))),
		genLet(markersVar, boolLit(true)),
		genConst(createVar, create),
		genConst(updateVar, update),
	)

	updateBody := []JsStmt{
		genIf(id(markersVar), genBlock(
			genExprStmt(genCall(id("$$clearHydrationKeyedRowMarkers"), id(rangeVar))),
			genAssignStmt(id(markersVar), boolLit(false)),
		)),
		genAssignStmt(id(blocksVar), genCall(
			id("$$reconcileHydrationKeyedRows"), id(rangeVar), id(blocksVar), g.templateExpr(block.IterExpr),
			id(keyOfVar), id(createVar), id(updateVar),
		)),
	}
	fn := name + "_update"
	g.body = append(g.body, &JsFuncDecl{baseStmt: baseStmt{genNode()}, Name: fn, Body: genBlock(updateBody...)})
	for _, dependency := range templateExprDeps(block.IterExpr, g.reactiveNames()) {
		g.addStructuralUpdate(dependency, genInvoke(fn))
	}
	g.cleanup = append(g.cleanup, genExprStmt(genCall(genMember(id(blocksVar), "forEach"), &JsArrowFunc{
		baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$row")},
		Body: genCall(genMember(id("$$row"), "remove")),
	})))
}

func (g *ASTGen) hydrationEachRowTag(block *EachBlockNode) string {
	row, ok := ssrCapabilityCurrentEachDynamicTextRow(block)
	if !ok || row == nil {
		return ""
	}
	return row.Tag
}

// hydrationEachRowCreate builds a single keyed row as typed JS AST.  Its
// expressions are evaluated under item/index template bindings; neither the
// key nor row content falls back to reparsing source strings.
func (g *ASTGen) hydrationEachRowCreate(block *EachBlockNode) JsExpr {
	row, _ := ssrCapabilityCurrentEachDynamicTextRow(block)
	body := []JsStmt{genConst("$$row", genCall(genMember(id("document"), "createElement"), strLit(row.Tag)))}
	body = append(body, g.setAttrStmt("$$row", g.scopeID, ""))
	for _, attr := range row.Attrs {
		if attr != nil && attr.Expr == nil {
			body = append(body, g.setAttrStmt("$$row", attr.Name, attr.Value))
		}
	}
	g.withTemplateBinding(block.ItemName, "$$item", func() {
		g.withTemplateBinding(block.IndexName, "$$index", func() {
			for _, child := range row.Children {
				tn, ok := child.(*TextNode)
				if ok && tn != nil {
					body = append(body, genExprStmt(genCall(genMember(id("$$row"), "appendChild"),
						genCall(genMember(id("document"), "createTextNode"), g.textValueExpr(tn.Parts)))))
				}
			}
		})
	})
	body = append(body, genReturn(id("$$row")))
	return &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$item"), id("$$index")}, Body: genBlock(body...)}
}

func (g *ASTGen) hydrationEachRowUpdate(block *EachBlockNode) JsExpr {
	row, _ := ssrCapabilityCurrentEachDynamicTextRow(block)
	parts := []*Part{}
	for _, child := range row.Children {
		if tn, ok := child.(*TextNode); ok && tn != nil {
			parts = append(parts, tn.Parts...)
		}
	}
	hasDynamic := false
	for _, part := range parts {
		if part != nil && !part.Static {
			hasDynamic = true
			break
		}
	}
	if !hasDynamic {
		return genNull()
	}
	value := JsExpr(id("undefined"))
	g.withTemplateBinding(block.ItemName, "$$item", func() {
		g.withTemplateBinding(block.IndexName, "$$index", func() {
			value = g.textValueExpr(parts)
		})
	})
	return &JsArrowFunc{
		baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$row"), id("$$item"), id("$$index")},
		Body: genBlock(genAssignStmt(genMember(id("$$row"), "textContent"), value)),
	}
}

// hydrationEachRowBuild creates one replacement row using typed DOM AST.
// Static rows still take the compact insertHTML path; the dynamic-text subset
// creates its element and text nodes directly so item expressions remain AST
// expressions and never become interpolated executable strings.
func (g *ASTGen) hydrationEachRowBuild(block *EachBlockNode, itemVar, fragmentVar string) *JsBlock {
	if row, ok := ssrCapabilityCurrentEachDynamicTextRow(block); ok {
		body := []JsStmt{genConst("$$row", genCall(genMember(id("document"), "createElement"), strLit(row.Tag)))}
		body = append(body, g.setAttrStmt("$$row", g.scopeID, ""))
		for _, attr := range row.Attrs {
			if attr == nil || attr.Expr != nil {
				continue
			}
			body = append(body, g.setAttrStmt("$$row", attr.Name, attr.Value))
		}
		g.withTemplateBinding(block.ItemName, itemVar, func() {
			for _, child := range row.Children {
				tn, ok := child.(*TextNode)
				if !ok || tn == nil {
					continue
				}
				body = append(body, genExprStmt(genCall(genMember(id("$$row"), "appendChild"),
					genCall(genMember(id("document"), "createTextNode"), g.textValueExpr(tn.Parts)))))
			}
		})
		body = append(body, genExprStmt(genCall(genMember(id(fragmentVar), "appendChild"), id("$$row"))))
		return genBlock(body...)
	}
	return genBlock(genExprStmt(genCall(id("$$insertHTML"), id(fragmentVar), strLit(emitStaticHTML(block.Children, g.scopeID)))))
}

// buildSlotNode projects a <slot> — an anchor comment plus an init
// block that, if the parent supplied slot content under this
// slot's name, inserts it before the anchor.  Static fallback
// content is rendered when the slot is unfilled.
func (g *ASTGen) buildSlotNode(slot *SlotNode, parentVar string) {
	slotVar := g.id() + "_slot"
	anchor := slotVar + "_a"

	g.body = append(g.body,
		genConst(anchor, genCall(genMember(id("document"), "createComment"), strLit("slot:"+slot.Name))),
		genExprStmt(genCall(genMember(id(parentVar), "appendChild"), id(anchor))))

	// The init block runs after wiring, once the anchor has a parent.  User
	// fallback expressions remain AST nodes so their original template offsets
	// reach the source map.
	showSlot := genBlock(
		genConst("$$sn", genCall(id("$$sf"))),
		genIf(id("$$sn"), genExprStmt(genCall(genMember(id(anchor), "before"), id("$$sn")))),
	)
	init := &JsBlock{baseStmt: baseStmt{genNode()}, Body: []JsStmt{
		genConst("$$sf", genComputedMember(id("$$slots"), strLit(slot.Name))),
		&JsIfStmt{baseStmt: baseStmt{genNode()}, Test: id("$$sf"), Then: showSlot},
	}}
	if len(slot.Fallback) > 0 {
		init.Body[1].(*JsIfStmt).Else = genBlock(g.slotFallbackStmts(slot.Fallback, anchor)...)
	}
	g.initBody = append(g.initBody, init)
}

// slotFallbackStmts emits fallback content for an unfilled slot. Text nodes
// keep their structured expressions; static elements use a template fragment
// like the legacy path, but now as typed target AST.
func (g *ASTGen) slotFallbackStmts(fallback []Node, anchor string) []JsStmt {
	var out []JsStmt
	for _, child := range fallback {
		switch c := child.(type) {
		case *TextNode:
			out = append(out, genExprStmt(genCall(genMember(id(anchor), "before"),
				genCall(genMember(id("document"), "createTextNode"), g.textValueExpr(c.Parts)))))
		case *ElementNode:
			html := emitStaticHTML([]Node{c}, g.scopeID)
			fallbackVar := g.id()
			out = append(out, genBlock(
				genConst(fallbackVar, genCall(genMember(id("document"), "createElement"), strLit("template"))),
				genAssignStmt(genMember(id(fallbackVar), "innerHTML"), strLit(html)),
				genExprStmt(genCall(genMember(id(anchor), "before"), genMember(id(fallbackVar), "content"))),
			))
		}
	}
	return out
}

// hydrationComponentOverridesExpr contains only bindings that must exist before
// the child factory runs. Passing the bound value again would replay a server
// value into live DOM; the child payload already owns that initial value.
func (g *ASTGen) hydrationComponentOverridesExpr(attrs []*Attr) JsExpr {
	overrides := &JsObjectExpr{baseExpr: baseExpr{genNode()}}
	for _, attr := range attrs {
		if attr == nil || !attr.IsTwoWay || g.analysis == nil {
			continue
		}
		target, isIdent := templateExprIdent(attr.Expr)
		if !isIdent || !g.analysis.stateSet()[target] {
			continue
		}
		overrides.Props = append(overrides.Props, &JsProp{
			baseNode: genNode(), Key: id("$$onChange_" + attr.Name),
			Value: &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$$v")}, Body: genCall(id("$$set_"+target), id("$$v"))},
		})
	}
	if len(overrides.Props) == 0 {
		return nil
	}
	return overrides
}

// buildHydrationComponentSlotContent adopts parent-owned dynamic text markers
// in deterministic slot-name order, matching SSR plan construction.
func (g *ASTGen) buildHydrationComponentSlotContent(slots map[string][]Node, componentPlan *SSRStructuralLifecyclePlan) {
	if componentPlan == nil || componentPlan.Kind != SSRLifecyclePlanComponent {
		panic("vela: hydration lifecycle component plan is missing slot ownership")
	}
	names := make([]string, 0, len(slots))
	for name := range slots {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		var slotPlan *SSRStructuralLifecyclePlan
		for _, child := range componentPlan.Children {
			if child == nil || child.Kind != SSRLifecyclePlanSlot || len(child.InstancePath.Segments) == 0 {
				continue
			}
			segment := child.InstancePath.Segments[len(child.InstancePath.Segments)-1]
			if segment.Kind == SSRInstanceSlot && segment.Name == name {
				slotPlan = child
				break
			}
		}
		if slotPlan == nil {
			panic("vela: hydration lifecycle component slot plan does not match template slot")
		}
		g.buildHydrationRootChildren(slots[name], slotPlan.Children, "$$root")
	}
}

// buildHydrationComponentNode adopts a local child component already emitted by
// the Go renderer. Its hydrate entry restores child payload before returning
// the live client instance, so the parent must not replay initial props or
// create a comment anchor. Parent reactive prop updates still use the
// ordinary instance .set() path after adoption.
func (g *ASTGen) buildHydrationComponentNode(comp *ComponentNode, parentVar string, plan *SSRStructuralLifecyclePlan) {
	if comp == nil || plan == nil || plan.Kind != SSRLifecyclePlanComponent || plan.Span != comp.Span {
		panic("vela: hydration lifecycle component plan does not match template span")
	}
	g.requireHydrationLifecycle(SSRLifecyclePlanComponent, comp.Span, SSRLifecycleAdopt)
	inst := g.id() + "_comp"
	g.declareInScope(genLet(inst, genNull()))

	propsExpr := g.componentPropsExpr(comp.Attrs)
	g.buildHydrationComponentSlotContent(comp.Slots, plan)
	overrides := g.hydrationComponentOverridesExpr(comp.Attrs)
	g.hydrationInitBody = append(g.hydrationInitBody, genIf(
		genUnary("!", id(inst)),
		genAssignStmt(id(inst), genMember(g.hydrationComponentCall(comp, id(parentVar), overrides), "component")),
	))

	setStmt := genIf(id(inst), genExprStmt(genCall(genMember(id(inst), "set"), propsExpr)))
	deps := map[string]bool{}
	for _, attr := range comp.Attrs {
		if attr.IsEvent || attr.IsBindThis {
			continue
		}
		for _, dep := range templateExprDeps(attr.Expr, g.reactiveNames()) {
			deps[dep] = true
		}
	}
	for dep := range deps {
		g.addUpdate(dep, setStmt)
	}
	g.buildComponentEvents(comp, inst)
	g.cleanup = append(g.cleanup, genIf(id(inst), genExprStmt(genCall(genMember(id(inst), "destroy")))))
}

// hydrationComponentCall preserves the established two-argument hydrate
// entry calls for ordinary children. Repeated static bindings additionally
// receive the parent runtime scope plus their structural suffix.
func (g *ASTGen) hydrationComponentCall(comp *ComponentNode, parent JsExpr, overrides JsExpr) JsExpr {
	return g.hydrationComponentCallAt(comp, parent, overrides, id("$$instanceID"))
}

func (g *ASTGen) hydrationComponentCallAt(comp *ComponentNode, parent JsExpr, overrides, instanceScope JsExpr) JsExpr {
	args := []JsExpr{parent}
	suffix := ""
	if comp != nil {
		suffix = g.hydrationComponentInstanceSuffixes[comp.Span]
		if identity, ok := g.hydrationComponentInstanceIdentities[comp.Span]; ok && identity.RuntimeScoped {
			suffix = ssrRuntimeComponentSuffix(identity)
		}
	}
	if overrides != nil || suffix != "" {
		if overrides == nil {
			args = append(args, genNull())
		} else {
			args = append(args, overrides)
		}
	}
	if suffix != "" {
		args = append(args, genBinary("+", instanceScope, strLit("--"+suffix)))
	}
	return genCall(id(comp.Name), args...)
}

// buildComponentNode invokes a child component: a wrapper element,
// an instance handle, the constructor call, a reactive .set() for
// prop changes, and .destroy() teardown.
//
// Slot children passed to the component are not yet struct-built —
// astComponentOK gates a component with slot content to the
// splice.
func (g *ASTGen) buildComponentNode(comp *ComponentNode, parentVar string) {
	if g.domAcquireMode == DOMAcquireHydrate && g.hydrationComponents[comp.Name] {
		g.buildHydrationComponentNode(comp, parentVar, g.hydrationLifecycleNode(SSRLifecyclePlanComponent, comp.Span))
		return
	}
	factoryName := comp.Name
	// A hydrate client imports local children from their .hydrate.js entry so
	// existing SSR DOM can be adopted. Branches materialized after hydration
	// must instead use the ordinary CSR factory, including recursively built
	// #if/#each/#await content captured by another structural lifecycle.
	if g.domAcquireMode == DOMAcquireCreate && g.hydrationCSRComponents[comp.Name] != "" {
		factoryName = g.hydrationCSRComponents[comp.Name]
	}
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
		g.fragHoisted = append(g.fragHoisted, genLet(wrap, genNull()), genLet(inst, genNull()))
	case inFragment:
		g.body = append(g.body, genLet(wrap, genNull()), genLet(inst, genNull()))
	default:
		g.hoisted = append(g.hoisted, genLet(wrap, genNull()), genLet(inst, genNull()))
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
	g.body = append(g.body,
		genAssignStmt(id(wrap), genCall(genMember(id("document"), "createComment"), strLit("comp"))),
		genExprStmt(genCall(genMember(id(parentVar), "appendChild"), id(wrap))))

	propsExpr := g.componentPropsExpr(comp.Attrs)

	// Initial mount. propsExpr and slotsObj are both typed AST, so their
	// user-origin subexpressions never cross a printer/parser boundary.
	g.initBody = append(g.initBody, genIf(genUnary("!", id(inst)),
		genAssignStmt(id(inst), genCall(id(factoryName), id(wrap), propsExpr, slotsObj))))

	g.initBody = append(g.initBody, genIf(
		&JsLogicalExpr{baseExpr: baseExpr{genNode()}, Op: "&&", L: id(inst), R: genMember(id(inst), "el")},
		genExprStmt(genCall(genMember(genMember(id(inst), "el"), "setAttribute"), strLit(g.scopeID), strLit(""))),
	))

	// Reactive prop updates. The .set() statement propagates new prop values to an already-mounted instance.
	// It is registered both in the component setter cascade and in each-row update closures.
	setStmt := genIf(id(inst), genExprStmt(genCall(genMember(id(inst), "set"), propsExpr)))
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
		for _, d := range templateExprDeps(attr.Expr, g.reactiveNames()) {
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

	g.cleanup = append(g.cleanup,
		genIf(id(inst), genExprStmt(genCall(genMember(id(inst), "destroy")))))
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
		eventName := strings.TrimPrefix(attr.Name, "on:")
		// The guard wrapper — only forward CustomEvents.  Hoisted
		// to component scope: when the component is in a slot, the
		// addEventListener call runs on every slot reopen but the
		// handler const is declared once; a `let`/`const` trapped
		// in the slot closure's first-open path would be undefined
		// on reopen.
		handlerExpr := g.eventHandlerExpr(attr)
		if handlerExpr == nil {
			handlerExpr = id("undefined")
		}
		var invoke JsStmt = genExprStmt(genCall(handlerExpr, id("$e")))
		if _, isIdent := templateExprIdent(attr.Expr); !isIdent && !templateExprIsHandler(attr.Expr) {
			invoke = genExprStmt(handlerExpr)
			if g.analysis != nil {
				invoke = RewriteStatementAssignmentsAST(invoke, g.analysis.stateSet())
			}
			pos := g.sfc.TemplateOffset + attr.Expr.Pos
			WalkNode(invoke, func(node JsNode) bool {
				setTemplateOrigin(node, pos)
				return true
			})
		}
		handlerDecl := genConst(hv, &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$e")}, Body: genBlock(
			genIf(genBinary("instanceof", id("$e"), id("CustomEvent")), genBlock(invoke)),
		)})
		if g.fragCounter >= 0 {
			g.body = append(g.body, handlerDecl)
		} else {
			g.hoisted = append(g.hoisted, handlerDecl)
		}
		attach := &g.initBody
		if g.domAcquireMode == DOMAcquireHydrate {
			attach = &g.hydrationInitBody
		} else if g.fragCounter >= 0 {
			// Row update() replays initBody; subscriptions attach only at mount.
			attach = &g.events
		}
		g.emitListenerPair(attach, inst+".el", eventName, hv, nil, inst, true)
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
func (g *ASTGen) buildSlotClosures(comp *ComponentNode) JsExpr {
	if len(comp.Slots) == 0 {
		return &JsObjectExpr{baseExpr: baseExpr{genNode()}}
	}
	// Deterministic slot order — ranging a Go map is random and
	// would make output non-reproducible.
	names := make([]string, 0, len(comp.Slots))
	for name := range comp.Slots {
		names = append(names, name)
	}
	sort.Strings(names)

	slots := &JsObjectExpr{baseExpr: baseExpr{genNode()}}
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
		// replay.  the branch teardown still fires on slot teardown via
		// the cleanup path; this re-publish only extends the
		// component's reactive-prop reach, not its lifetime.
		g.compInstances = append(g.compInstances, cap.comps...)

		// Event listeners and initial-render statements emitted by
		// slot content (e.g. a component @click handler, or a
		// child component's init call) must run inside the closure
		// too — they were being dropped, so components in a slot
		// had no event wiring.
		initStmts := append(append([]JsStmt{}, cap.events...), cap.initBody...)

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
		g.declareInScope(genLet(cacheVar, genNull()))

		newFragment := func() JsExpr {
			return genCall(genMember(id("document"), "createDocumentFragment"))
		}
		reopen := []JsStmt{
			genConst(fragVar, newFragment()),
			genForOf("$$c", id(cacheVar), genBlock(
				genIf(genBinary("!==", genMember(id("$$c"), "parentNode"), genNull()),
					genExprStmt(genCall(genMember(id(fragVar), "appendChild"), id("$$c"))),
				),
			)),
		}
		reopen = append(reopen, initStmts...)
		reopen = append(reopen, genReturn(id(fragVar)))

		firstOpen := []JsStmt{genConst(fragVar, newFragment())}
		firstOpen = append(firstOpen, cap.body...)
		firstOpen = append(firstOpen, initStmts...)
		firstOpen = append(firstOpen,
			genAssignStmt(id(cacheVar), genCall(genMember(id("Array"), "from"), genMember(id(fragVar), "childNodes"))),
			genReturn(id(fragVar)),
		)

		factory := &JsArrowFunc{
			baseExpr: baseExpr{genNode()},
			Body: genBlock(
				genIf(id(cacheVar), genBlock(reopen...)),
				genBlock(firstOpen...),
			),
		}
		slots.Props = append(slots.Props, &JsProp{
			baseNode: genNode(),
			Key:      strLit(name),
			Value:    factory,
		})
	}
	return slots
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
			if g.domAcquireMode != DOMAcquireHydrate {
				g.body = append(g.body,
					g.setAttrStmt(varName, attr.Name, attr.Value))
			}
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
// registerReactiveTemplateStmt runs a DOM write during initial render and
// reuses that exact AST statement for every reactive dependency of the source
// expression. Keeping one statement instance preserves its template origin.
func (g *ASTGen) registerReactiveTemplateStmt(stmt JsStmt, expr *TemplateExpr) {
	g.initBody = append(g.initBody, stmt)
	for _, dep := range templateExprDeps(expr, g.reactiveNames()) {
		g.addUpdate(dep, stmt)
	}
}

func (g *ASTGen) emitClassDir(varName string, attr *Attr) {
	stmt := g.classDirStmtAST(varName, attr)
	g.registerReactiveTemplateStmt(stmt, attr.Expr)
}

// emitStyleDir wires `style:foo={expr}` — set an inline CSS
// property via style.setProperty (handles hyphenated names and
// custom properties).  Runs at initial render and on every change
// of a reactive dependency.
func (g *ASTGen) emitStyleDir(varName string, attr *Attr) {
	stmt := g.styleDirStmtAST(varName, attr)
	g.registerReactiveTemplateStmt(stmt, attr.Expr)
}

// emitEventHandler wires an @event attribute: an addEventListener
// in the events section and a matching removeEventListener in
// cleanup.  Three handler shapes, mirroring the legacy generator:
//   - bare identifier  → addEventListener('click', inc)
//   - handler expr     → const $$h = <arrow>; addEventListener(…, $$h)
//   - statement body   → const $$h = ($event) => { … }; …
//
// emitListenerPair keeps listener registration and teardown structurally paired.
// When handlerExpr is present, the attach statement preserves the template
// expression origin for source maps; otherwise handler is compiler-authored.
func (g *ASTGen) emitListenerPair(attach *[]JsStmt, target, event, handler string, handlerExpr JsExpr, cleanupGuard string, addCleanup bool) {
	listenerExpr := handlerExpr
	if listenerExpr == nil {
		listenerExpr = id(handler)
	}
	*attach = append(*attach,
		genExprStmt(genCall(genMember(generatedMemberPath(target), "addEventListener"), strLit(event), listenerExpr)))
	if !addCleanup {
		return
	}
	remove := genExprStmt(genCall(genMember(generatedMemberPath(target), "removeEventListener"), strLit(event), id(handler)))
	if cleanupGuard != "" {
		g.cleanup = append(g.cleanup, genIf(id(cleanupGuard), remove))
		return
	}
	g.cleanup = append(g.cleanup, remove)
}

func (g *ASTGen) emitEventHandler(varName string, attr *Attr) {
	handler, isIdent := templateExprIdent(attr.Expr)
	handlerExpr := g.eventHandlerExpr(attr)
	if handlerExpr == nil {
		handlerExpr = id("undefined")
	}
	addCleanup := !g.inSlotScope
	if isIdent {
		g.emitListenerPair(&g.events, varName, attr.Name, handler, handlerExpr, "", addCleanup)
		return
	}
	hv := varName + "_" + attr.Name + "_h"
	decl := genConst(hv, handlerExpr)
	if !templateExprIsHandler(attr.Expr) {
		decl = genConst(hv, &JsArrowFunc{baseExpr: baseExpr{genNode()}, Params: []JsExpr{id("$event")}, Body: genBlock(genExprStmt(handlerExpr))})
	}
	g.body = append(g.body, decl)
	g.emitListenerPair(&g.events, varName, attr.Name, hv, nil, "", addCleanup)
}

// emitBinding wires a bind:value / bind:checked attribute — both
// directions:
//   - DOM→state: an input/change listener calling $$set_X
//   - state→DOM: an initial-render write + an update registered
//     against X so the setter re-writes the DOM
func (g *ASTGen) emitBinding(varName, tag string, attr *Attr) {
	spec, ok := twoWayBindSpec(tag, attr.Name)
	sv, isIdent := templateExprIdent(attr.Expr)
	if attr.IsTwoWay && ok && isIdent && g.analysis != nil && g.analysis.stateSet()[sv] {
		hv := varName + "_bind_" + spec.event + "_h"
		g.body = append(g.body, genConst(hv, &JsArrowFunc{
			baseExpr: baseExpr{genNode()},
			Params:   []JsExpr{id("$e")},
			Body:     genCall(id("$$set_"+sv), genMember(genMember(id("$e"), "target"), spec.readProp)),
		}))
		g.emitListenerPair(&g.events, varName, spec.event, hv, nil, "", true)
	}
	write := g.attrWriteStmt(varName, attr)
	if attr.IsTwoWay && ok {
		write = g.bindingWriteStmt(varName, attr)
	}
	g.registerReactiveTemplateStmt(write, attr.Expr)
}

// emitBindThisAST wires bind:this={ref} — write the live element
// into the user's variable after creation, clear it on destroy.
//
// When ref names a $state variable the write goes through its
// setter ($$set_ref), so other holes reading ref react to it;
// otherwise (a plain local, or a member like refs.input) a raw
// assignment is used.  Mirrors the legacy emitBindThis.
func (g *ASTGen) emitBindThisAST(varName string, attr *Attr) {
	ref, isIdent := templateExprIdent(attr.Expr)
	addInit := func(stmt JsStmt) {
		if g.domAcquireMode == DOMAcquireHydrate {
			g.hydrationInitBody = append(g.hydrationInitBody, stmt)
			return
		}
		g.initBody = append(g.initBody, stmt)
	}
	if isIdent && g.analysis != nil && g.analysis.stateSet()[ref] {
		addInit(genInvoke("$$set_"+ref, id(varName)))
		g.cleanup = append(g.cleanup,
			genInvoke("$$set_"+ref, genNull()))
		return
	}
	refExpr := g.templateExpr(attr.Expr)
	addInit(genAssignStmt(refExpr, id(varName)))
	g.cleanup = append(g.cleanup, genAssignStmt(refExpr, genNull()))
}

// rewriteHandler strips the {…} wrapper from a template event handler and
// rewrites assignments to reactive variables into their  form. The
// shared rewriter handles concise arrow bodies as statements, so updates such
// as `() => count++` remain reactive.
func (g *ASTGen) eventHandlerExpr(attr *Attr) JsExpr {
	if attr == nil || attr.Expr == nil {
		return nil
	}
	root := templateExprRoot(attr.Expr)
	if root == nil {
		return nil
	}
	root = g.templateExpr(attr.Expr)
	if g.analysis != nil {
		root = RewriteHandlerAssignmentsAST(root, g.analysis.stateSet())
	}
	// Browser source-map consumers may stop at a nested handler node rather
	// than the arrow root. Keep the attribute's exact origin on the complete
	// lowered subtree, matching the legacy rewritten-template path.
	pos := g.sfc.TemplateOffset + attr.Expr.Pos
	WalkNode(root, func(node JsNode) bool {
		setTemplateOrigin(node, pos)
		return true
	})
	return root
}

// eachItemExpr lowers one each-row attribute from its cached template AST.
// The item rewrite happens before the handler assignment rewrite, so both
// transformations see structured JavaScript and retain the original template
// source origin.
func (g *ASTGen) eachItemExpr(attr *Attr, itemVar, boxVar string, handler bool) JsExpr {
	if attr == nil || attr.Expr == nil || templateExprRoot(attr.Expr) == nil {
		return id("undefined")
	}
	root := rewriteItemRefAST(g.templateExpr(attr.Expr), itemVar, boxVar)
	if handler && g.analysis != nil {
		root = RewriteHandlerAssignmentsAST(root, g.analysis.stateSet())
	}
	pos := g.sfc.TemplateOffset + attr.Expr.Pos
	WalkNode(root, func(node JsNode) bool {
		setTemplateOrigin(node, pos)
		return true
	})
	return root
}

// stripBraces removes a single surrounding {…} wrapper that the
// template parser preserves on attribute values.
func stripBraces(s string) string {
	return unwrapBraces(s)
}

// innerHTMLStmt builds `<varName>.innerHTML = '<html>';`
func (g *ASTGen) innerHTMLStmt(varName, html string) JsStmt {
	return genAssignStmt(genMember(id(varName), "innerHTML"), strLit(html))
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
		genConst(tpl, genCall(genMember(id("document"), "createElement"), strLit("template"))),
		genAssignStmt(genMember(id(tpl), "innerHTML"), strLit(html)),
		genExprStmt(genCall(genMember(id(targetVar), "appendChild"), genMember(id(tpl), "content"))),
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

// createHydrationTemplateElementStmt acquires a template element by the exact
// marker ID reserved by the shared SSR render plan. Unlike the CSR plan order,
// this remains correct when a preceding static structural range owns elements
// that the client deliberately does not acquire.
func (g *ASTGen) createHydrationTemplateElementStmt(varName string, element *ElementNode) JsStmt {
	if element == nil {
		panic("vela: missing template element during hydration lowering")
	}
	g.requireHydrationLifecycle(SSRLifecyclePlanElement, element.Span, SSRLifecycleAdopt)
	hydrationID, ok := g.hydrationMarkers.Elements[element.Span]
	if !ok {
		if g.hydrationMarkers.Elements == nil {
			return g.createElementStmt(varName, element.Tag)
		}
		panic("vela: missing SSR element marker for hydration template span")
	}
	g.domAcquirePlan.addHydrationElement(varName, element.Tag, hydrationID)
	return genConst(varName, genCall(id("$$acquireHydrationElement"), id("$$walker"), numLit(hydrationID), strLit(element.Tag)))
}

// createElementStmt acquires a structural element through the component's
// selected DOM mode. It is the canonical construction primitive for ordinary
// template elements; runtime-only helper elements keep their own explicit
// creation sites.
func (g *ASTGen) createElementStmt(varName, tag string) JsStmt {
	return genConst(varName, g.acquireElementExpr(varName, tag))
}

func (g *ASTGen) acquireElementExpr(varName, tag string) JsExpr {
	hydrationID := g.domAcquirePlan.addElement(varName, tag)
	if g.domAcquireMode == DOMAcquireHydrate {
		return genCall(id("$$acquireHydrationElement"), id("$$walker"), numLit(hydrationID), strLit(tag))
	}
	return genCall(genMember(id("document"), "createElement"), strLit(tag))
}

// setAttrStmt builds `<varName>.setAttribute('<name>', '<value>');`
func (g *ASTGen) setAttrStmt(varName, name, value string) JsStmt {
	return genExprStmt(genCall(genMember(id(varName), "setAttribute"), strLit(name), strLit(value)))
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
	// Mark disposal before user cleanup. This makes repeated destroy() calls and
	// setters reached from cleanup or later callbacks harmless.
	destroyBody.Body = append(destroyBody.Body,
		genIf(id("$$disposed"), genReturn(nil)),
		genAssignStmt(id("$$disposed"), boolLit(true)),
	)
	// A shared runtime queue may still hold a component flush; it will observe disposal,
	// while clearing component-local bookkeeping releases the current batch.
	if g.flushPlan != nil && (len(g.flushPlan.entries) > 0 || len(g.flushPlan.effectFlags) > 0) {
		destroyBody.Body = append(destroyBody.Body, genAssignStmt(id("$$flushQueued"), boolLit(false)))
		for _, entry := range g.flushPlan.entries {
			destroyBody.Body = append(destroyBody.Body, genAssignStmt(id(entry.flag), boolLit(false)))
			if entry.ran != "" {
				destroyBody.Body = append(destroyBody.Body, genAssignStmt(id(entry.ran), boolLit(false)))
			}
		}
		for _, flag := range g.flushPlan.effectFlags {
			destroyBody.Body = append(destroyBody.Body, genAssignStmt(id(flag), boolLit(false)))
		}
	}
	if g.analysis != nil {
		for _, state := range g.analysis.StateVars {
			if state.Deep {
				destroyBody.Body = append(destroyBody.Body,
					genAssignStmt(id("$$pendingMutations_"+state.Name), genArray()),
					genAssignStmt(id("$$activeMutations_"+state.Name), genArray()),
				)
			}
		}
	}
	// 1. Effect cleanups.
	if g.analysis != nil {
		for i := range g.analysis.Effects {
			n := itoa(i)
			destroyBody.Body = append(destroyBody.Body,
				genIf(id("$$cleanup"+n), genInvoke("$$cleanup"+n)))
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
			genExprStmt(genCall(genMember(id("$$style"), "remove"))))
	}
	// 6. Root removal.
	destroyBody.Body = append(destroyBody.Body,
		genExprStmt(genCall(genMember(id("$$root"), "remove"))))

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
			prop := genMember(id("props"), name)
			setBody.Body = append(setBody.Body,
				genIf(genBinary("!==", prop, id("undefined")),
					genInvoke("$$set_"+name, genMember(id("props"), name))))
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
