package main

// blocks_codegen.go — template lowering for the block DOM engine
// (docs/HYDRATION_ARCHITECTURE.md, task 039).
//
// The script side of ASTGen (props, state, setters, $derived, $effect, the
// flush plan) lives in astgen.go. This file owns the template lowering:
// every template body becomes a fragment function that
// builds DOM through a cursor and returns { p(dirty, scopeChanged) }, and
// structural constructs call the runtime blocks of runtime/runtime-blocks.js.
//
// Dirty state is an object keyed by reactive name. Setters mark the name and
// its dependent derived values in $$dirty (buildSetterCascade), one
// structural flush entry calls $$viewUpdate, and fragments guard each update
// with the names its expression reads. Each-row locals (item, index) are
// marked in the forwarded dirty object when the row's scope changes, so
// nested fragments that read them update too.

import (
	"fmt"
	"sort"
	"strings"
)

// blocksRuntimeImport is the module the generators import block helpers
// from; project builds retarget it like the other runtime modules.
const blocksRuntimeImport = "./runtime-blocks.js"

// blocksRuntimeExports lists the helpers exported by runtime-blocks.js.
var blocksRuntimeExports = runtimeExportNames(embeddedBlocksRuntime)

// blocksState is the per-component state of the block lowering.
type blocksState struct {
	counter   int
	templates []JsStmt        // module-scope $$template constants
	deps      map[string]bool // reactive names read anywhere in the template
	frag      *blocksFragment // fragment being built
	// assignRoot is set when the template is a single element, which the
	// root fragment assigns to $$root.
	assignRoot bool
	// locals are the each/await names visible at the current position.
	// Mutable locals (each item and index) change when a row is reused.
	locals []blocksLocal
	// sites is the hydration site table of development builds (V-11.3b).
	sites []JsExpr
}

type blocksLocal struct {
	name    string // local variable in the fragment
	source  string // field of the scope object $$s it comes from
	mutable bool   // refreshed when the owning block reuses the fragment
}

// blocksFragment is one generated fragment function.
type blocksFragment struct {
	name    string
	scoped  bool     // takes ($$c, $$s)
	locals  []string // mutable locals refreshed from $$s on scope change
	scope   []JsStmt // `let item = $$s.item;` declarations
	body    []JsStmt // build statements
	updates []JsStmt // statements of p()
	nested  []JsStmt // child fragment function declarations
	live    bool     // declares $$live: slot content owned by this fragment
}

func (b *blocksState) next(prefix string) string {
	name := prefix + itoa(b.counter)
	b.counter++
	return name
}

func (g *ASTGen) blocksEmit(stmts ...JsStmt) {
	g.blocks.frag.body = append(g.blocks.frag.body, stmts...)
}

// blocksGuard returns the update condition for a template expression, or nil
// when nothing it reads can change. Reactive names are recorded so the
// component flush runs the view for them.
func (g *ASTGen) blocksGuard(exprs ...*TemplateExpr) JsExpr {
	names := map[string]bool{}
	reactive := g.reactiveNames()
	for _, expr := range exprs {
		if expr == nil || expr.Root() == nil {
			continue
		}
		for _, dep := range templateExprDeps(expr, reactive) {
			names[dep] = true
			g.blocks.deps[dep] = true
		}
		idents := map[string]bool{}
		for _, ident := range CollectIdents(expr.Root()) {
			idents[ident] = true
		}
		for _, local := range g.blocks.locals {
			if local.mutable && idents[local.name] {
				names[local.name] = true
			}
		}
	}
	return blocksDirtyTest(names)
}

func blocksDirtyTest(names map[string]bool) JsExpr {
	if len(names) == 0 {
		return nil
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	var test JsExpr
	for _, name := range sorted {
		check := JsExpr(genMember(id("$$d"), name))
		if test == nil {
			test = check
		} else {
			test = genLogical("||", test, check)
		}
	}
	return test
}

// blocksChanged wraps a guard into a `($$d) => …` predicate for a block.
func blocksChanged(test JsExpr) JsExpr {
	if test == nil {
		return genNull()
	}
	return &JsArrowFunc{BaseExpr: BaseExpr{BaseNode: genNode()}, Params: []JsExpr{id("$$d")}, Body: test}
}

func blocksArrow(params []JsExpr, body JsNode) *JsArrowFunc {
	return &JsArrowFunc{BaseExpr: BaseExpr{BaseNode: genNode()}, Params: params, Body: body}
}

// blocksUpdate registers an update statement of the current fragment.
func (g *ASTGen) blocksUpdate(test JsExpr, stmt JsStmt) {
	if test == nil {
		return
	}
	g.blocks.frag.updates = append(g.blocks.frag.updates, genIf(test, stmt))
}

// blocksInit runs an initial DOM write through the cursor (F-2.2) and
// re-runs it when the expression's inputs change.
func (g *ASTGen) blocksInitAndUpdate(stmt JsStmt, exprs ...*TemplateExpr) []JsStmt {
	init := genExprStmt(genCall(genMember(id("$$c"), "init"), blocksArrow(nil, genBlock(stmt))))
	g.blocksUpdate(g.blocksGuard(exprs...), stmt)
	if len(exprs) > 0 {
		if at := g.blocksSiteStmt(exprs[0]); at != nil {
			return []JsStmt{at, init}
		}
	}
	return []JsStmt{init}
}

// buildBlocksView lowers the template of a component: it declares the root,
// the root fragment and $$viewUpdate, registers the view with the flush plan,
// and mounts the view during the initial render.
func (g *ASTGen) buildBlocksView(nodes []Node) {
	// The server renders the same normalised template (M-5.7).
	normalizeTableRows(nodes)
	g.blocks.deps = map[string]bool{}
	rootIsElement := len(nodes) == 1
	if rootIsElement {
		_, rootIsElement = nodes[0].(*ElementNode)
	}
	// Multiple roots, a structural root or a text root get a
	// display:contents wrapper (B-3.8); the runtime
	// creates or adopts it ($$mountComponent).
	g.body = append(g.body, genLet("$$root", genNull()))
	// A single root element is assigned to $$root by the root fragment.
	g.blocks.assignRoot = rootIsElement
	root := g.buildBlocksFragment(nodes, nil)
	g.body = append(g.body, root)

	// One structural flush entry runs the view for every reactive name the
	// template reads.
	update := &JsFuncDecl{BaseStmt: BaseStmt{BaseNode: genNode()}, Name: "$$viewUpdate", Body: genBlock(
		genIf(genUnary("!", id("$$view")), genReturn(nil)),
		genConst("$$d", id("$$dirty")),
		genAssignStmt(id("$$dirty"), genCall(genMember(id("Object"), "create"), genNull())),
		genExprStmt(genCall(genMember(id("$$view"), "p"), id("$$d"))),
	)}
	g.body = append(g.body, update)
	deps := make([]string, 0, len(g.blocks.deps))
	for dep := range g.blocks.deps {
		deps = append(deps, dep)
	}
	sort.Strings(deps)
	call := genInvoke("$$viewUpdate")
	for _, dep := range deps {
		g.addStructuralUpdate(dep, call)
	}

	wrapper := JsExpr(genNull())
	if !rootIsElement {
		wrapper = strLit(g.elementScope)
	}
	setRoot := &JsArrowFunc{BaseExpr: BaseExpr{BaseNode: genNode()}, Params: []JsExpr{id("$$el")}, Body: genBlock(genAssignStmt(id("$$root"), id("$$el")))}
	getRoot := &JsArrowFunc{BaseExpr: BaseExpr{BaseNode: genNode()}, Body: id("$$root")}
	g.initBody = append(g.initBody, genAssignStmt(id("$$view"), genCall(id("$$mountComponent"), id("$$target"), id(rootFragmentName), wrapper, setRoot, getRoot)))
	g.cleanup = append(g.cleanup, genIf(id("$$view"), genExprStmt(genCall(genMember(id("$$view"), "release")))))
}

const rootFragmentName = "$$f_root"

// buildBlocksFragment builds a fragment function for nodes. scopeLocals are
// each/await names bound from $$s; mutable ones are refreshed when the
// scope changes.
func (g *ASTGen) buildBlocksFragment(nodes []Node, scopeLocals []blocksLocal) JsStmt {
	name := rootFragmentName
	if g.blocks.frag != nil {
		name = g.blocks.next("$$f")
	}
	frag := &blocksFragment{name: name, scoped: len(scopeLocals) > 0}
	parentFrag := g.blocks.frag
	parentLocals := len(g.blocks.locals)
	g.blocks.frag = frag
	for _, local := range scopeLocals {
		g.blocks.locals = append(g.blocks.locals, local)
		frag.scope = append(frag.scope, genLet(local.name, genMember(id("$$s"), local.source)))
		if local.mutable {
			frag.locals = append(frag.locals, local.name)
		}
	}
	g.buildBlocksChildren(nodes, name == rootFragmentName && g.blocks.assignRoot)
	g.blocks.frag = parentFrag
	g.blocks.locals = g.blocks.locals[:parentLocals]
	return frag.decl(scopeLocals)
}

// decl prints the fragment as a function declaration.
func (f *blocksFragment) decl(scopeLocals []blocksLocal) JsStmt {
	params := []JsExpr{id("$$c")}
	if f.scoped {
		params = append(params, id("$$s"))
	}
	body := append([]JsStmt{}, f.scope...)
	if f.live {
		body = append(body, genConst("$$live", genNew(id("Set"))))
	}
	body = append(body, f.nested...)
	body = append(body, f.body...)
	if len(f.updates) > 0 || len(f.locals) > 0 || f.live {
		var update []JsStmt
		if len(f.locals) > 0 {
			refresh := []JsStmt{}
			marks := &JsObjectExpr{BaseExpr: BaseExpr{BaseNode: genNode()}}
			for _, local := range scopeLocals {
				if !local.mutable {
					continue
				}
				refresh = append(refresh, genAssignStmt(id(local.name), genMember(id("$$s"), local.source)))
				marks.Props = append(marks.Props, &JsProp{BaseNode: genNode(), Key: id(local.name), Value: boolLit(true)})
			}
			refresh = append(refresh, genAssignStmt(id("$$d"), genCall(genMember(id("Object"), "assign"),
				genCall(genMember(id("Object"), "create"), genNull()), id("$$d"), marks)))
			update = append(update, genIf(id("$$sc"), genBlock(refresh...)))
		}
		update = append(update, f.updates...)
		if f.live {
			// Slot content built here lives inside a child component but is
			// updated with this fragment's dirty state (B-3.7).
			update = append(update, genForOf("$$h", id("$$live"), genBlock(genExprStmt(genCall(genMember(id("$$h"), "p"), id("$$d"))))))
		}
		method := &JsProp{
			BaseNode: genNode(), Kind: "init", Method: true, Key: id("p"),
			Value: &JsFuncExpr{BaseExpr: BaseExpr{BaseNode: genNode()}, Params: []JsExpr{id("$$d"), id("$$sc")}, Body: genBlock(update...)},
		}
		body = append(body, genReturn(&JsObjectExpr{BaseExpr: BaseExpr{BaseNode: genNode()}, Props: []*JsProp{method}}))
	}
	return &JsFuncDecl{BaseStmt: BaseStmt{BaseNode: genNode()}, Name: f.name, Params: params, Body: genBlock(body...)}
}

// buildBlocksChildren lowers a child list: an all-static list with an
// element is one static subtree; otherwise runs of static nodes become
// static templates between the dynamic nodes. Whitespace was decided by the
// parser (task 059), so the server writes the same text runs (M-5.10).
func (g *ASTGen) buildBlocksChildren(nodes []Node, isRoot bool) {
	if len(nodes) == 0 {
		return
	}
	if !isRoot && hasElement(nodes) && allStatic(nodes) {
		g.blocksStatic(nodes)
		return
	}
	// The parser keeps whitespace only where HTML renders it (one space
	// between siblings, task 059), so every static run is created.
	var run []Node
	flush := func() {
		if len(run) == 0 {
			return
		}
		g.blocksStatic(run)
		run = nil
	}
	for _, node := range nodes {
		if isStaticNode(node) && !(isRoot && isElementNode(node)) {
			run = append(run, node)
			continue
		}
		// A kept static text right before a text with holes is one text run
		// with it (C-4.3): the server writes them together and the HTML
		// parser joins them, so a separate client node failed hydration
		// (task 031).
		if text, ok := node.(*TextNode); ok && len(run) > 0 {
			if last, ok := run[len(run)-1].(*TextNode); ok && last.Preformatted == text.Preformatted {
				joined := *text
				joined.Parts = append(append([]*Part{}, last.Parts...), text.Parts...)
				joined.Span.Start = last.Span.Start
				run = run[:len(run)-1]
				if len(run) > 0 {
					g.blocksStatic(run)
				}
				run = nil
				g.buildBlocksNode(&joined, isRoot)
				continue
			}
		}
		flush()
		g.buildBlocksNode(node, isRoot)
	}
	flush()
}

func isElementNode(node Node) bool {
	_, ok := node.(*ElementNode)
	return ok
}

// blocksStatic clones a module-level template holding static nodes.
func (g *ASTGen) blocksStatic(nodes []Node) {
	name := "$$tpl_" + g.name + "_" + itoa(len(g.blocks.templates))
	g.blocks.templates = append(g.blocks.templates, genConst(name, genCall(id("$$template"), strLit(emitStaticHTML(nodes, g.elementScope)))))
	g.blocksEmit(genExprStmt(genCall(genMember(id("$$c"), "static"), id(name))))
}

func (g *ASTGen) buildBlocksNode(node Node, isRoot bool) {
	switch n := node.(type) {
	case *ElementNode:
		g.buildBlocksElement(n, isRoot)
	case *TextNode:
		g.buildBlocksText(n)
	case *IfChainNode:
		if n.Client {
			g.buildBlocksClient(n)
		} else if n.KeyAST != nil {
			g.buildBlocksKey(n)
		} else {
			g.buildBlocksIf(n)
		}
	case *EachBlockNode:
		g.buildBlocksEach(n)
	case *AwaitBlockNode:
		g.buildBlocksAwait(n)
	case *ComponentNode:
		g.buildBlocksComponent(n)
	case *SlotNode:
		g.buildBlocksSlot(n)
	default:
		panic(fmt.Sprintf("vela: block engine cannot lower %T", node))
	}
}

func (g *ASTGen) buildBlocksText(text *TextNode) {
	ref := g.blocks.next("$$t")
	if textNodeAllStatic(text) {
		value := ""
		for _, part := range text.Parts {
			value += part.Value
		}
		g.blocksEmit(genExprStmt(genCall(genMember(id("$$c"), "text"), strLit(value))))
		return
	}
	for _, part := range text.Parts {
		if !part.Static {
			g.blocksSite(part.Expr)
			break
		}
	}
	g.blocksEmit(genConst(ref, genCall(genMember(id("$$c"), "text"), g.textValueExpr(text.Parts))))
	var exprs []*TemplateExpr
	for _, part := range text.Parts {
		if !part.Static {
			exprs = append(exprs, part.Expr)
		}
	}
	g.blocksUpdate(g.blocksGuard(exprs...), g.textWriteStmt(ref, text.Parts))
}

func (g *ASTGen) buildBlocksElement(el *ElementNode, isRoot bool) {
	ref := g.blocks.next("$$e")
	attrs := genArray()
	if g.elementScope != "" {
		attrs = genArray(strLit(g.elementScope), strLit(""))
	}
	for _, attr := range el.Attrs {
		if attr.IsEvent || attr.IsTwoWay || attr.IsBind || attr.IsBindThis || attr.IsClassDir || attr.IsStyleDir {
			continue
		}
		attrs.Elems = append(attrs.Elems, strLit(attr.Name), strLit(attr.Value))
	}
	g.blocksEmit(genConst(ref, genCall(genMember(id("$$c"), "element"), strLit(el.Tag), attrs)))
	if isRoot {
		g.blocksEmit(genAssignStmt(id("$$root"), id(ref)))
	}

	// Listeners first; DOM writes after the children exist, so a select's
	// value is written once its options are present.
	var writes []JsStmt
	for _, attr := range el.Attrs {
		switch {
		case attr.IsEvent:
			g.blocksEmit(g.blocksListen(ref, attr.Name, g.blocksEventHandler(attr)))
		case attr.IsTwoWay || attr.IsBind:
			writes = append(writes, g.blocksBinding(ref, el.Tag, attr)...)
		case attr.IsBindThis:
			writes = append(writes, g.blocksBindThis(ref, attr)...)
		case attr.IsClassDir:
			writes = append(writes, g.blocksInitAndUpdate(g.classDirStmtAST(ref, attr), attr.Expr)...)
		case attr.IsStyleDir:
			writes = append(writes, g.blocksInitAndUpdate(g.styleDirStmtAST(ref, attr), attr.Expr)...)
		}
	}

	if len(el.Children) > 0 {
		if prop, ok := rawTextProp(el.Tag); ok {
			if _, ok := singleInterpolation(el.Children); ok {
				expr := el.Children[0].(*TextNode).Parts[0].Expr
				write := genAssignStmt(genMember(id(ref), prop), genString(g.templateExpr(expr)))
				writes = append(writes, g.blocksInitAndUpdate(write, expr)...)
			} else if text, ok := staticRawText(el.Children); ok {
				g.blocksEmit(genAssignStmt(genMember(id(ref), prop), strLit(text)))
			} else if text, ok := el.Children[0].(*TextNode); ok && len(el.Children) == 1 {
				// Static text with interpolations: the property gets the whole
				// text, as the server writes it.
				var exprs []*TemplateExpr
				for _, part := range text.Parts {
					if !part.Static {
						exprs = append(exprs, part.Expr)
					}
				}
				write := genAssignStmt(genMember(id(ref), prop), g.textValueExpr(text.Parts))
				writes = append(writes, g.blocksInitAndUpdate(write, exprs...)...)
			}
		} else {
			g.blocksEmit(genExprStmt(genCall(genMember(id("$$c"), "enter"), id(ref))))
			g.buildBlocksChildren(el.Children, false)
			g.blocksEmit(genExprStmt(genCall(genMember(id("$$c"), "exit"))))
		}
	}
	g.blocksEmit(writes...)
}

func (g *ASTGen) blocksListen(ref, event string, handler JsExpr) JsStmt {
	return genExprStmt(genCall(genMember(id("$$c"), "listen"), id(ref), strLit(event), handler))
}

func (g *ASTGen) blocksEventHandler(attr *Attr) JsExpr {
	handler := g.eventHandlerExpr(attr)
	if handler == nil {
		return id("undefined")
	}
	if _, isIdent := templateExprIdent(attr.Expr); isIdent || templateExprIsHandler(attr.Expr) {
		return handler
	}
	return blocksArrow([]JsExpr{id("$event")}, genBlock(genExprStmt(handler)))
}

// blocksBinding lowers bind:value / bind:checked and one-way :attr writes.
func (g *ASTGen) blocksBinding(ref, tag string, attr *Attr) []JsStmt {
	spec, ok := twoWayBindSpec(tag, attr.Name)
	sv, isIdent := templateExprIdent(attr.Expr)
	if attr.IsTwoWay && ok && isIdent && g.hasReactiveSetter(sv) {
		handler := blocksArrow([]JsExpr{id("$e")}, twoWaySetterFromEvent("$$set_"+sv, tag == "select" && attr.Name == "value", spec.readProp))
		g.blocksEmit(g.blocksListen(ref, spec.event, handler))
		if tag != "select" && (attr.Name == "value" || attr.Name == "checked") {
			// Hydration adopts a value the user entered before it (C-4.5).
			read := blocksArrow(nil, g.templateExpr(attr.Expr))
			write := blocksArrow([]JsExpr{id("$$v")}, genCall(id("$$set_"+sv), id("$$v")))
			g.blocksEmit(genExprStmt(genCall(genMember(id("$$c"), "adopt"), id(ref), strLit(attr.Name), read, write)))
		}
	}
	write := g.attrWriteStmt(ref, attr)
	if attr.IsTwoWay && ok {
		write = g.blocksBindingWrite(ref, attr, tag == "select")
	}
	return g.blocksInitAndUpdate(write, attr.Expr)
}

// blocksBindingWrite writes a bound control property only when the live
// value differs, like bindingWriteStmt, with block-local temporaries.
func (g *ASTGen) blocksBindingWrite(ref string, attr *Attr, multipleAware bool) JsStmt {
	value := g.templateExpr(attr.Expr)
	if attr.Name == "checked" {
		value = genBool(value)
	}
	target := genMember(id(ref), attr.Name)
	if multipleAware && attr.Name == "value" {
		return genBlock(
			genConst("$$v", value),
			genIf(genMember(id(ref), "multiple"), genExprStmt(genCall(id("$$writeSelectValues"), id(ref), id("$$v")))),
			genIf(genUnary("!", genMember(id(ref), "multiple")), genIf(genBinary("!==", target, id("$$v")), genAssignStmt(target, id("$$v")))),
		)
	}
	return genBlock(
		genConst("$$v", value),
		genIf(genBinary("!==", target, id("$$v")), genAssignStmt(target, id("$$v"))),
	)
}

// blocksBindThis assigns the element to its reference after creation and
// clears it when the fragment is released.
func (g *ASTGen) blocksBindThis(ref string, attr *Attr) []JsStmt {
	name, isIdent := templateExprIdent(attr.Expr)
	var set, clear JsStmt
	if isIdent && g.analysis != nil && g.analysis.stateSet()[name] {
		set = genInvoke("$$set_"+name, id(ref))
		clear = genInvoke("$$set_"+name, genNull())
	} else {
		target := g.templateExpr(attr.Expr)
		set = genAssignStmt(target, id(ref))
		clear = genAssignStmt(g.templateExpr(attr.Expr), genNull())
	}
	// The reference is assigned on creation and on hydration alike (C-4.5),
	// so it is not an initial DOM write.
	return []JsStmt{
		set,
		genExprStmt(genCall(genMember(genMember(id("$$c"), "owner"), "add"), blocksArrow(nil, genBlock(clear)))),
	}
}

// blocksBlockCall emits `const $$bN = $$c.block(kind, args...)` and forwards
// updates to it.
func (g *ASTGen) blocksBlockCall(kind string, args ...JsExpr) {
	ref := g.blocks.next("$$b")
	call := genCall(genMember(id("$$c"), "block"), append([]JsExpr{id(kind)}, args...)...)
	g.blocksEmit(genConst(ref, call))
	g.blocks.frag.updates = append(g.blocks.frag.updates, genExprStmt(genCall(genMember(id(ref), "p"), id("$$d"))))
}

// blocksNestedFragment builds a child fragment and declares it inside the
// current fragment, so it closes over the current locals.
func (g *ASTGen) blocksNestedFragment(nodes []Node, locals []blocksLocal) string {
	parent := g.blocks.frag
	decl := g.buildBlocksFragment(nodes, locals)
	parent.nested = append(parent.nested, decl)
	return decl.(*JsFuncDecl).Name
}

func (g *ASTGen) buildBlocksIf(chain *IfChainNode) {
	var exprs []*TemplateExpr
	var fragments []JsExpr
	var select_ JsExpr = numLit(-1)
	for i := len(chain.Branches) - 1; i >= 0; i-- {
		branch := chain.Branches[i]
		var name string
		g.withBranchTemplateBindings(branch, func() {
			name = g.blocksNestedFragment(branch.Children, nil)
		})
		fragments = append([]JsExpr{id(name)}, fragments...)
		if branch.Expr != nil {
			exprs = append(exprs, branch.Expr)
			select_ = &JsConditionalExpr{BaseExpr: BaseExpr{BaseNode: genNode()}, Test: g.templateExpr(branch.Expr), Then: numLit(i), Else: select_}
		} else {
			select_ = numLit(i)
		}
	}
	if len(exprs) > 0 {
		g.blocksSite(exprs[len(exprs)-1])
	}
	g.blocksBlockCall("$$if", blocksArrow(nil, select_), genArray(fragments...), blocksChanged(g.blocksGuard(exprs...)))
}

func (g *ASTGen) buildBlocksKey(chain *IfChainNode) {
	var body []Node
	if len(chain.Branches) > 0 {
		body = chain.Branches[0].Children
	}
	name := g.blocksNestedFragment(body, nil)
	g.blocksSite(chain.KeyAST)
	g.blocksBlockCall("$$key", blocksArrow(nil, g.templateExpr(chain.KeyAST)), id(name), blocksChanged(g.blocksGuard(chain.KeyAST)))
}

func (g *ASTGen) buildBlocksEach(each *EachBlockNode) {
	index := each.IndexName
	if index == "" {
		index = "$$index"
	}
	locals := []blocksLocal{{name: each.ItemName, source: "item", mutable: true}, {name: index, source: "index", mutable: true}}
	row := g.blocksNestedFragment(each.Children, locals)
	key := JsExpr(genNull())
	if each.HasKey() {
		// Keyed rows compare String(key), so 1 and "1" are one row.
		g.blocks.locals = append(g.blocks.locals, locals...)
		key = blocksArrow([]JsExpr{id(each.ItemName), id(index)}, genString(g.templateExpr(each.KeyAST)))
		g.blocks.locals = g.blocks.locals[:len(g.blocks.locals)-len(locals)]
	}
	fallback := JsExpr(genNull())
	if len(each.ElseChildren) > 0 {
		fallback = id(g.blocksNestedFragment(each.ElseChildren, nil))
	}
	args := []JsExpr{blocksArrow(nil, g.templateExpr(each.IterExpr)), key, id(row), fallback, blocksChanged(g.blocksGuard(each.IterExpr))}
	// A keyed each over a $state.deep collection refreshes only the rows that
	// in-place mutations touched (task 023).
	if each.HasKey() {
		if plan := g.eachIncrementalPlanFor(each); plan != nil {
			args = append(args, blocksArrow(nil, genCall(id("$$eachRowsForMutations"), id("$$activeMutations_"+plan.stateName), strLit(plan.keyField))))
		}
	}
	g.blocksSite(each.IterExpr)
	g.blocksBlockCall("$$each", args...)
}

func (g *ASTGen) buildBlocksAwait(await *AwaitBlockNode) {
	branch := func(nodes []Node, name, source string, present bool) JsExpr {
		if !present {
			return genNull()
		}
		var locals []blocksLocal
		if name != "" {
			locals = []blocksLocal{{name: name, source: source}}
		}
		return id(g.blocksNestedFragment(nodes, locals))
	}
	pending := branch(await.PendingChildren, "", "", len(await.PendingChildren) > 0)
	then := branch(await.ThenChildren, await.ValueName, "value", await.ThenChildren != nil)
	catcher := branch(await.CatchChildren, await.ErrorName, "error", await.CatchChildren != nil)
	g.blocksSite(await.Expr)
	g.blocksBlockCall("$$await", blocksArrow(nil, g.templateExpr(await.Expr)), pending, then, catcher, blocksChanged(g.blocksGuard(await.Expr)))
}

// buildBlocksComponent mounts a child component through its public factory
// (B-3.9). Slot content is built by fragments of this component, owned and
// updated here; props, component events and bind: to a $bindable prop use
// the component protocol of $component.
func (g *ASTGen) buildBlocksComponent(comp *ComponentNode) {
	slots := &JsObjectExpr{BaseExpr: BaseExpr{BaseNode: genNode()}}
	names := make([]string, 0, len(comp.Slots))
	for name := range comp.Slots {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fragment := g.blocksNestedFragment(comp.Slots[name], nil)
		g.blocks.frag.live = true
		slots.Props = append(slots.Props, &JsProp{
			BaseNode: genNode(),
			Key:      strLit(name),
			Value:    genCall(id("$$slotFactory"), id("$$c"), id("$$live"), id(fragment)),
		})
	}

	events := &JsObjectExpr{BaseExpr: BaseExpr{BaseNode: genNode()}}
	var exprs []*TemplateExpr
	for _, attr := range comp.Attrs {
		if attr.IsEvent {
			events.Props = append(events.Props, &JsProp{
				BaseNode: genNode(),
				Key:      strLit(strings.TrimPrefix(attr.Name, "on:")),
				Value:    g.blocksComponentEventHandler(attr),
			})
			continue
		}
		if !attr.IsBindThis && attr.Expr != nil {
			exprs = append(exprs, attr.Expr)
		}
	}
	var eventsArg JsExpr = genNull()
	if len(events.Props) > 0 {
		eventsArg = events
	}
	props := blocksArrow(nil, g.componentPropsExpr(comp.Attrs))
	g.blocksBlockCall("$$component", id(comp.Name), props, slots, eventsArg, blocksChanged(g.blocksGuard(exprs...)), strLit(g.elementScope))
}

// blocksComponentEventHandler forwards only CustomEvents dispatched by the
// child, so a native event of the same name bubbling from its DOM is not
// mistaken for a component event (as in buildComponentEvents).
func (g *ASTGen) blocksComponentEventHandler(attr *Attr) JsExpr {
	handler := g.eventHandlerExpr(attr)
	if handler == nil {
		handler = id("undefined")
	}
	var invoke JsStmt = genExprStmt(genCall(handler, id("$e")))
	if _, isIdent := templateExprIdent(attr.Expr); !isIdent && !templateExprIsHandler(attr.Expr) {
		invoke = genExprStmt(handler)
		if g.analysis != nil {
			invoke = RewriteStatementAssignmentsAST(invoke, g.analysis.stateSet())
		}
		pos := g.sfc.TemplateOffset + attr.Expr.Pos
		WalkNode(invoke, func(node JsNode) bool {
			setTemplateOrigin(node, pos)
			return true
		})
	}
	return blocksArrow([]JsExpr{id("$e")}, genBlock(
		genIf(genBinary("instanceof", id("$e"), id("CustomEvent")), genBlock(invoke)),
	))
}

// buildBlocksSlot opens the parent's slot content, or mounts the fallback.
func (g *ASTGen) buildBlocksSlot(slot *SlotNode) {
	var fallback JsExpr = genNull()
	if len(slot.Fallback) > 0 {
		fallback = id(g.blocksNestedFragment(slot.Fallback, nil))
	}
	g.blocksBlockCall("$$slot", genComputedMember(id("$$slots"), strLit(slot.Name)), fallback)
}

func textNodeStaticValue(text *TextNode) string {
	value := ""
	for _, part := range text.Parts {
		value += part.Value
	}
	return value
}

// hasPreformattedText reports text of a <pre>, <textarea>, or <listing>
// in a run: its whitespace is content.
func hasPreformattedText(run []Node) bool {
	for _, node := range run {
		if text, ok := node.(*TextNode); ok && text.Preformatted {
			return true
		}
	}
	return false
}

// buildBlocksClient lowers {#client}…{:else}…{/client} to the $$client
// block: the main branch fragment and the fallback fragment, or null for
// an empty fallback (T-9.1b).
func (g *ASTGen) buildBlocksClient(chain *IfChainNode) {
	main := g.blocksNestedFragment(chain.Branches[0].Children, nil)
	fallback := JsExpr(genNull())
	if len(chain.Branches) > 1 && len(chain.Branches[1].Children) > 0 {
		fallback = id(g.blocksNestedFragment(chain.Branches[1].Children, nil))
	}
	g.blocksBlockCall("$$client", id(main), fallback)
}

// blocksSite emits $$c.at(site) before an adopted site in development
// builds, so that a hydration mismatch names its source position, site
// ID, and host symbol (V-11.3b).
func (g *ASTGen) blocksSite(expr *TemplateExpr) {
	if at := g.blocksSiteStmt(expr); at != nil {
		g.blocksEmit(at)
	}
}

func (g *ASTGen) blocksSiteStmt(expr *TemplateExpr) JsStmt {
	if g.sfc == nil || g.sfc.SitePath == "" || expr == nil || false {
		return nil
	}
	line, column := sourceLineColumn(g.sfc.Source, g.sfc.TemplateOffset+expr.Pos)
	host := JsExpr(genNull())
	if g.sfc.SiteHosts != nil && expr.Root() != nil {
		WalkNode(expr.Root(), func(n JsNode) bool {
			if call, ok := n.(*JsCallExpr); ok {
				if callee, ok := call.Callee.(*JsIdent); ok {
					if name, bound := g.sfc.SiteHosts(callee.Name); bound {
						host = strLit(name)
						return false
					}
				}
			}
			return true
		})
	}
	site := len(g.blocks.sites)
	g.blocks.sites = append(g.blocks.sites, genArray(numLit(site), strLit(fmt.Sprintf("%s:%d:%d", g.sfc.SitePath, line+1, column+1)), host))
	return genExprStmt(genCall(genMember(id("$$c"), "at"), &JsMemberExpr{BaseExpr: BaseExpr{BaseNode: genNode()}, Obj: id("$$sites"), Prop: numLit(site), Computed: true}))
}
