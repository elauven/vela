package main

// astgen.go — the AST-based code generator.
//
// It constructs a JsProgram tree for a component and hands it to the
// printer (jsprinter.go); production compilation never reparses generated
// JavaScript. This file owns the component module: imports, props, state,
// setters, $derived, $effect, the flush plan, lifecycle and the returned
// instance. Template lowering is done by the block engine
// (blocks_codegen.go).

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
func genNode() BaseNode { return BaseNode{Origin: OriginGenerated} }

// id builds an identifier.
func id(name string) *JsIdent {
	return &JsIdent{BaseExpr: BaseExpr{BaseNode: genNode()}, Name: name}
}

// strLit builds a decoded compiler-generated string literal. The printer
// owns JavaScript escaping, so callers always pass the semantic string value.
func strLit(value string) *JsLiteral {
	return &JsLiteral{
		BaseExpr: BaseExpr{BaseNode: genNode()},
		Kind:     "string",
		Value:    value,
		Decoded:  true,
	}
}

func genMember(obj JsExpr, prop string) *JsMemberExpr {
	return &JsMemberExpr{BaseExpr: BaseExpr{BaseNode: genNode()}, Obj: obj, Prop: id(prop)}
}

func genCall(callee JsExpr, args ...JsExpr) *JsCallExpr {
	return &JsCallExpr{BaseExpr: BaseExpr{BaseNode: genNode()}, Callee: callee, Args: args}
}

func genExprStmt(expr JsExpr) *JsExprStmt {
	return &JsExprStmt{BaseStmt: BaseStmt{BaseNode: genNode()}, X: expr}
}

func genAssignStmt(left, right JsExpr) *JsExprStmt {
	return genExprStmt(&JsAssignExpr{BaseExpr: BaseExpr{BaseNode: genNode()}, Op: "=", L: left, R: right})
}

func genVar(kind, name string, init JsExpr) *JsVarDecl {
	return &JsVarDecl{BaseStmt: BaseStmt{BaseNode: genNode()}, Kind: kind, Decls: []*JsVarDeclarator{{Id: id(name), Init: init}}}
}

func genConst(name string, init JsExpr) *JsVarDecl { return genVar("const", name, init) }

func genLet(name string, init JsExpr) *JsVarDecl { return genVar("let", name, init) }

func genInvoke(name string, args ...JsExpr) *JsExprStmt {
	return genExprStmt(genCall(id(name), args...))
}

func genBinary(op string, left, right JsExpr) *JsBinaryExpr {
	return &JsBinaryExpr{BaseExpr: BaseExpr{BaseNode: genNode()}, Op: op, L: left, R: right}
}

func genIf(test JsExpr, then JsStmt) *JsIfStmt {
	return &JsIfStmt{BaseStmt: BaseStmt{BaseNode: genNode()}, Test: test, Then: then}
}

func genNull() *JsLiteral {
	return &JsLiteral{BaseExpr: BaseExpr{BaseNode: genNode()}, Raw: "null", Kind: "null"}
}

func numLit(value int) *JsLiteral {
	return &JsLiteral{BaseExpr: BaseExpr{BaseNode: genNode()}, Raw: itoa(value), Kind: "number"}
}

func boolLit(value bool) *JsLiteral {
	if value {
		return &JsLiteral{BaseExpr: BaseExpr{BaseNode: genNode()}, Raw: "true", Kind: "boolean"}
	}
	return &JsLiteral{BaseExpr: BaseExpr{BaseNode: genNode()}, Raw: "false", Kind: "boolean"}
}

func genComputedMember(obj, prop JsExpr) *JsMemberExpr {
	return &JsMemberExpr{BaseExpr: BaseExpr{BaseNode: genNode()}, Obj: obj, Prop: prop, Computed: true}
}

func genLogical(op string, left, right JsExpr) *JsLogicalExpr {
	return &JsLogicalExpr{BaseExpr: BaseExpr{BaseNode: genNode()}, Op: op, L: left, R: right}
}

func genUnary(op string, expr JsExpr) *JsUnaryExpr {
	return &JsUnaryExpr{BaseExpr: BaseExpr{BaseNode: genNode()}, Op: op, Prefix: true, X: expr}
}

func genNew(callee JsExpr, args ...JsExpr) *JsNewExpr {
	return &JsNewExpr{BaseExpr: BaseExpr{BaseNode: genNode()}, Callee: callee, Args: args}
}

func genArray(elems ...JsExpr) *JsArrayExpr {
	return &JsArrayExpr{BaseExpr: BaseExpr{BaseNode: genNode()}, Elems: elems}
}

func genBlock(body ...JsStmt) *JsBlock {
	return &JsBlock{BaseStmt: BaseStmt{BaseNode: genNode()}, Body: body}
}

func genReturn(arg JsExpr) *JsReturnStmt {
	return &JsReturnStmt{BaseStmt: BaseStmt{BaseNode: genNode()}, Arg: arg}
}

func genForOf(name string, iterable JsExpr, body JsStmt) *JsForInStmt {
	return &JsForInStmt{
		BaseStmt: BaseStmt{BaseNode: genNode()},
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
// interface; we reach the embedded BaseNode through the concrete
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

	// blocks is non-nil when the block DOM engine lowers this component
	// (blocks_codegen.go).
	blocks *blocksState

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

	// updateStructuralOwners records structural reconcile functions that build
	// a given ordinary update. When such a reconcile runs in a flush, its
	// branch init already writes the final value, so the ordinary update can
	// be skipped rather than writing the newly-created node twice.
	updateStructuralOwners map[string][]string

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

	// cssExternal selects CSS delivery.  When false (default), the
	// component injects its scoped <style> into document.head at
	// runtime (CSS-in-JS).  When true, buildStyleInjection emits
	// nothing — the scoped CSS is written to a sibling .css file
	// by the compiler, and the consumer links it.
	cssExternal bool

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
		BaseStmt: BaseStmt{BaseNode: genNode()},
		Name:     "$$flush",
		Body:     genBlock(flushBody...),
	})
	out = append(out, &JsFuncDecl{
		BaseStmt: BaseStmt{BaseNode: genNode()},
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
		return &JsCallExpr{BaseExpr: BaseExpr{BaseNode: genNode()}, Callee: id("String"), Args: []JsExpr{g.templateExpr(parts[0].Expr)}}
	}
	t := &JsTemplateLit{BaseExpr: BaseExpr{BaseNode: genNode()}, Quasis: []string{""}}
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

func genBool(expr JsExpr) JsExpr {
	return &JsUnaryExpr{
		BaseExpr: BaseExpr{BaseNode: genNode()}, Op: "!", Prefix: true,
		X: &JsUnaryExpr{BaseExpr: BaseExpr{BaseNode: genNode()}, Op: "!", Prefix: true, X: expr},
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

func twoWaySetterFromEvent(setter string, multipleAware bool, readProp string) JsNode {
	target := genMember(id("$e"), "target")
	scalar := genCall(id(setter), genMember(target, readProp))
	if !multipleAware {
		return scalar
	}
	return genBlock(
		genIf(genMember(target, "multiple"), genBlock(
			genExprStmt(genCall(id(setter), genCall(id("$$readSelectValues"), target))),
			genReturn(nil),
		)),
		genReturn(scalar),
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
	obj := &JsObjectExpr{BaseExpr: BaseExpr{BaseNode: genNode()}}
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
					BaseNode: genNode(), Key: id("$$onChange_" + attr.Name),
					Value: &JsArrowFunc{
						BaseExpr: BaseExpr{BaseNode: genNode()}, Params: []JsExpr{id("$$v")},
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
	prog := buildProgramUnvalidated(name, sfc, nodes, analysis, scopeID, cssExternal)
	validateGeneratedProgram(prog)
	return prog
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
	// Input validation consumes the immutable ScriptAST before this point.
	// Lower callback assignments only while assembling target code, so the
	// shared source tree cannot leak generated $$set_* names into user scopes.
	analysis.lowerReactiveCallbackBodies()
	g := &ASTGen{
		name:        name,
		scopeID:     scopeID,
		analysis:    analysis,
		sfc:         sfc,
		fragCounter: -1,
		cssExternal: cssExternal,
		blocks:      &blocksState{},
	}
	return g.buildModule(nodes)
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

	fnBody := &JsBlock{BaseStmt: BaseStmt{BaseNode: genNode()}}

	// ── Preamble: normalize props/slots ──────────────────────
	fnBody.Body = append(fnBody.Body,
		genAssignStmt(id("$$props"), genCall(genMember(id("Object"), "assign"),
			&JsObjectExpr{BaseExpr: BaseExpr{BaseNode: genNode()}}, id("$$props"))),
		genAssignStmt(id("$$slots"), &JsLogicalExpr{
			BaseExpr: BaseExpr{BaseNode: genNode()}, Op: "||", L: id("$$slots"),
			R: &JsObjectExpr{BaseExpr: BaseExpr{BaseNode: genNode()}},
		}),
		genExprStmt(&JsUnaryExpr{BaseExpr: BaseExpr{BaseNode: genNode()}, Op: "void", Prefix: true, X: id("$$slots")}),
	)

	// A hydrating instance restores its state from the payload stream
	// (S-6.8); a created one gets null.
	fnBody.Body = append(fnBody.Body, genConst("$$velaState", genCall(id("$$hydrationState"), strLit(g.name))))

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

	// Setters may run while the script initialises, before the view
	// mounts; their dirty marks are collected from the start.
	fnBody.Body = append(fnBody.Body,
		genLet("$$dirty", genCall(genMember(id("Object"), "create"), genNull())),
		genLet("$$view", genNull()),
	)

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

	g.buildBlocksView(nodes)

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

	// Make queued setters and deferred flushes inert if a later adoption
	// step fails. Listener, child-cache and lifecycle cleanup callbacks are
	// registered after this owner callback and therefore unwind first.

	// ── Event listeners (block 7) ────────────────────────────
	// addEventListener calls for @event handlers and the input
	// listeners powering bind:.  After setters so handlers (which
	// call $$set_X) are in scope.
	fnBody.Body = append(fnBody.Body, g.events...)

	// ── Initial render / post-adoption synchronization (block 3) ──

	fnBody.Body = append(fnBody.Body, g.initBody...)

	// ── Mount ────────────────────────────────────────────────
	// $$target may be an Element (appendChild as a child) OR a
	// Comment node (insertBefore — so the component is positioned
	// at the anchor's slot rather than appended at the end of its
	// container).  Comment-as-target is the wrapper-less mount
	// path: the caller creates an anchor comment in document

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
		BaseStmt: BaseStmt{BaseNode: genNode()},
		Name:     g.name,
		Params:   []JsExpr{id("$$target"), id("$$props"), id("$$slots")},
		Body:     fnBody,
	}

	// Static subtrees are parsed once per module.
	prog.Body = append(prog.Body, g.blocks.templates...)
	if len(g.blocks.sites) > 0 {
		prog.Body = append(prog.Body, genConst("$$sites", genArray(g.blocks.sites...)))
	}

	prog.Body = append(prog.Body, &JsExportDecl{
		BaseStmt:    BaseStmt{BaseNode: genNode()},
		Declaration: fn,
	})
	if g.sfc != nil && g.sfc.ClientOnly {
		// A client-only component (contract §9.2): $$component mounts it
		// after the root commits instead of adopting server DOM.
		prog.Body = append(prog.Body, genAssignStmt(genMember(id(g.name), "$$clientOnly"), boolLit(true)))
	}

	// Also expose the component factory as the module's DEFAULT
	// export.  Vela components compile to `export function Name`,
	// but the idiomatic component import — matching Svelte and the
	// way every multi-component Vela project is written — is a
	// default import: `import Name from './Name.vela'`.  Emitting
	// `export default Name;` makes both the default and the named
	// import resolve to the same factory.
	prog.Body = append(prog.Body, &JsExportDecl{
		BaseStmt:    BaseStmt{BaseNode: genNode()},
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
	// A program that already imports the helpers (and binds
	// $dispatch) is left alone; adding them again would duplicate.
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
	usesReadSelectValues := false
	usesSelectValuesEqual := false
	usesWriteSelectValues := false
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
		case "$$readSelectValues":
			usesReadSelectValues = true
		case "$$selectValuesEqual":
			usesSelectValuesEqual = true
		case "$$writeSelectValues":
			usesWriteSelectValues = true
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
	if usesReadSelectValues {
		specs = append(specs, JsImportSpec{Imported: "$$readSelectValues"})
	}
	if usesSelectValuesEqual {
		specs = append(specs, JsImportSpec{Imported: "$$selectValuesEqual"})
	}
	if usesWriteSelectValues {
		specs = append(specs, JsImportSpec{Imported: "$$writeSelectValues"})
	}
	{
		used := map[string]bool{}
		WalkNode(prog, func(n JsNode) bool {
			if ident, ok := n.(*JsIdent); ok && blocksRuntimeExports[ident.Name] {
				used[ident.Name] = true
			}
			return true
		})
		names := make([]string, 0, len(used))
		for name := range used {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			specs = append(specs, JsImportSpec{Imported: name})
		}
	}

	if len(specs) > 0 {
		// Helper imports go after user imports, before the body —
		// prepend to module body just ahead of the export.
		prog.Body = append(runtimeImportDecls(specs), prog.Body...)
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
			BaseExpr: BaseExpr{BaseNode: genNode()},
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
		} else {
			if src == imp.Source {
				src = RewriteImportPath(src)
			}
		}

		out = append(out, &JsImportDecl{
			BaseStmt:    BaseStmt{BaseNode: genNode()},
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
				BaseExpr: BaseExpr{BaseNode: genNode()},
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
	{

		hasState := genCall(generatedMemberPath("Object.prototype.hasOwnProperty.call"), id("$$velaState"), strLit(sv.Name))
		init = &JsConditionalExpr{
			BaseExpr: BaseExpr{BaseNode: genNode()},
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
		genConst(mutName, &JsArrowFunc{BaseExpr: BaseExpr{BaseNode: genNode()}, Params: []JsExpr{id("$$mutation")}, Body: genCall(id("$$set_"+sv.Name+"_notify"), id("$$mutation"))}),
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
			BaseExpr: BaseExpr{BaseNode: genNode()},
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
			BaseExpr: BaseExpr{BaseNode: genNode()},
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
			BaseExpr: BaseExpr{BaseNode: genNode()},
			Body: genBlock(
				genIf(id(cleanup), genBlock(
					genInvoke(cleanup),
					genAssignStmt(id(cleanup), genNull()),
				)),
				genAssignStmt(id(cleanup), genCall(&JsFuncExpr{
					BaseExpr: BaseExpr{BaseNode: genNode()},
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
		cleanup := "$$mountCleanup" + itoa(i)
		out = append(out, genConst(cleanup, genCall(&JsFuncExpr{
			BaseExpr: BaseExpr{BaseNode: genNode()},
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
			genBinary("===", &JsUnaryExpr{BaseExpr: BaseExpr{BaseNode: genNode()}, Op: "typeof", Prefix: true, X: id(cv)}, strLit("function")),
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

	// The block engine's fragments read which names changed from $$dirty:
	// the name itself and every derived value recomputed above.

	out = append(out, genAssignStmt(genMember(id("$$dirty"), name), boolLit(true)))
	for _, dv := range dependentDerived {
		out = append(out, genAssignStmt(genMember(id("$$dirty"), dv.Name), boolLit(true)))
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
	body := &JsBlock{BaseStmt: BaseStmt{BaseNode: genNode()}}
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
		BaseStmt: BaseStmt{BaseNode: genNode()},
		Test: &JsBinaryExpr{
			BaseExpr: BaseExpr{BaseNode: genNode()},
			Op:       "&&",
			L: &JsBinaryExpr{
				BaseExpr: BaseExpr{BaseNode: genNode()},
				Op:       "===",
				L:        id(name),
				R:        id("$$v"),
			},
			R: &JsUnaryExpr{
				BaseExpr: BaseExpr{BaseNode: genNode()},
				Op:       "!",
				Prefix:   true,
				X: &JsCallExpr{
					BaseExpr: BaseExpr{BaseNode: genNode()},
					Callee:   id("$$isProxy"),
					Args:     []JsExpr{id("$$v")},
				},
			},
		},
		Then: &JsReturnStmt{BaseStmt: BaseStmt{BaseNode: genNode()}},
	})

	// <name> = $$v;
	body.Body = append(body.Body, &JsExprStmt{
		BaseStmt: BaseStmt{BaseNode: genNode()},
		X: &JsAssignExpr{
			BaseExpr: BaseExpr{BaseNode: genNode()},
			Op:       "=",
			L:        id(name),
			R:        id("$$v"),
		},
	})

	body.Body = append(body.Body, g.buildSetterCascade(name)...)

	return &JsFuncDecl{
		BaseStmt: BaseStmt{BaseNode: genNode()},
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
	notifyBody := &JsBlock{BaseStmt: BaseStmt{BaseNode: genNode()}}
	notifyBody.Body = append(notifyBody.Body, genIf(id("$$disposed"), genReturn(nil)))
	notifyBody.Body = append(notifyBody.Body, mutationCapture)
	notifyBody.Body = append(notifyBody.Body, g.buildSetterCascade(name)...)
	notify := &JsFuncDecl{
		BaseStmt: BaseStmt{BaseNode: genNode()},
		Name:     "$$set_" + name + "_notify",
		Params:   []JsExpr{id("$$mutation")},
		Body:     notifyBody,
	}

	// $$set_X — guard, re-wrap, notify.  The guard mirrors the
	// plain setter (=== plus !$$isProxy) so the two generators
	// agree; for a deep var the new value is normally raw (proxy
	// !== raw, so the guard never fired anyway), but a re-passed
	// proxy must still fall through to a re-wrap + notify.
	setBody := &JsBlock{BaseStmt: BaseStmt{BaseNode: genNode()}}
	setBody.Body = append(setBody.Body, genIf(id("$$disposed"), genReturn(nil)))
	setBody.Body = append(setBody.Body, &JsIfStmt{
		BaseStmt: BaseStmt{BaseNode: genNode()},
		Test: &JsBinaryExpr{
			BaseExpr: BaseExpr{BaseNode: genNode()},
			Op:       "&&",
			L: &JsBinaryExpr{
				BaseExpr: BaseExpr{BaseNode: genNode()},
				Op:       "===",
				L:        id(name),
				R:        id("$$v"),
			},
			R: &JsUnaryExpr{
				BaseExpr: BaseExpr{BaseNode: genNode()},
				Op:       "!",
				Prefix:   true,
				X: &JsCallExpr{
					BaseExpr: BaseExpr{BaseNode: genNode()},
					Callee:   id("$$isProxy"),
					Args:     []JsExpr{id("$$v")},
				},
			},
		},
		Then: &JsReturnStmt{BaseStmt: BaseStmt{BaseNode: genNode()}},
	})
	setBody.Body = append(setBody.Body,
		genAssignStmt(id(name), genCall(id("$$proxy"), id("$$v"), id("$$mut_"+name))),
		genInvoke("$$set_"+name+"_notify"))
	set := &JsFuncDecl{
		BaseStmt: BaseStmt{BaseNode: genNode()},
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

func (g *ASTGen) hydrationKeyedComponentRowTag(component *ComponentNode) string {
	if component == nil {
		return ""
	}
	// SSR child roots are the direct keyed rows. The hydration walker validates
	// exact element tags through component markers; an empty tag asks the
	// runtime to count every direct element inside this capability-checked row.
	return ""
}

func (g *ASTGen) hasReactiveSetter(name string) bool {
	if name == "" || g.analysis == nil {
		return false
	}
	if g.analysis.stateSet()[name] {
		return true
	}
	for _, prop := range g.analysis.Props {
		if prop != nil && prop.Name == name {
			return true
		}
	}
	return false
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

// stripBraces removes a single surrounding {…} wrapper that the
// template parser preserves on attribute values.
func stripBraces(s string) string {
	return unwrapBraces(s)
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

	destroyBody := &JsBlock{BaseStmt: BaseStmt{BaseNode: genNode()}}
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
		BaseNode: genNode(),
		Kind:     "init",
		Method:   true,
		Key:      id("destroy"),
		Value: &JsFuncExpr{
			BaseExpr: BaseExpr{BaseNode: genNode()},
			Body:     destroyBody,
		},
	}

	// The `el` field exposes the component's $$root to its
	// caller so the caller can attach event listeners and read
	// position information.  Required by bug-4's wrapper-less
	// mount: the parent's @event listeners now target inst.el
	// (instead of a wrapper div the parent itself controls).
	elProp := &JsProp{
		BaseNode: genNode(),
		Kind:     "init",
		Key:      id("el"),
		Value:    id("$$root"),
	}

	props := []*JsProp{elProp, destroyProp}
	{

		// abandon releases everything but leaves the root in the document:
		// hydration rollback keeps the server DOM for a retry (B-3.4, X-7.4).
		abandonBody := &JsBlock{BaseStmt: BaseStmt{BaseNode: genNode()}, Body: destroyBody.Body[:len(destroyBody.Body)-1]}
		props = append(props, &JsProp{BaseNode: genNode(), Kind: "init", Method: true, Key: id("abandon"), Value: &JsFuncExpr{BaseExpr: BaseExpr{BaseNode: genNode()}, Body: abandonBody}})
	}

	if hasSettable {
		// set(props) { if (props.X !== undefined) $$set_X(props.X); … }
		setBody := &JsBlock{BaseStmt: BaseStmt{BaseNode: genNode()}}
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
			BaseNode: genNode(),
			Kind:     "init",
			Method:   true,
			Key:      id("set"),
			Value: &JsFuncExpr{
				BaseExpr: BaseExpr{BaseNode: genNode()},
				Params:   []JsExpr{id("props")},
				Body:     setBody,
			},
		})
	}

	return &JsReturnStmt{
		BaseStmt: BaseStmt{BaseNode: genNode()},
		Arg: &JsObjectExpr{
			BaseExpr: BaseExpr{BaseNode: genNode()},
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
