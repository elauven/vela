package codegen

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
	"github.com/elauven/vela/internal/analyzer"
	"github.com/elauven/vela/internal/jsast"
	"github.com/elauven/vela/internal/sfcast"

	"strconv"

	"github.com/elauven/vela/runtime"

	"fmt"
	"sort"
	"strings"
)

// blocksRuntimeImport is the module the generators import block helpers
// from; project builds retarget it like the other runtime modules.
const blocksRuntimeImport = "./runtime-blocks.js"

// blocksRuntimeExports lists the helpers exported by runtime-blocks.js.
var blocksRuntimeExports = RuntimeExportNames(runtime.Blocks)

// blocksState is the per-component state of the block lowering.
type blocksState struct {
	counter   int
	templates []jsast.JsStmt  // module-scope $$template constants
	deps      map[string]bool // reactive names read anywhere in the template
	frag      *blocksFragment // fragment being built
	// assignRoot is set when the template is a single element, which the
	// root fragment assigns to $$root.
	assignRoot bool
	// locals are the each/await names visible at the current position.
	// Mutable locals (each item and index) change when a row is reused.
	locals []blocksLocal
	// sites is the hydration site table of development builds (V-11.3b).
	sites []jsast.JsExpr
}

type blocksLocal struct {
	name    string // local variable in the fragment
	source  string // field of the scope object $$s it comes from
	mutable bool   // refreshed when the owning block reuses the fragment
}

// blocksFragment is one generated fragment function.
type blocksFragment struct {
	name    string
	scoped  bool           // takes ($$c, $$s)
	locals  []string       // mutable locals refreshed from $$s on scope change
	scope   []jsast.JsStmt // `let item = $$s.item;` declarations
	body    []jsast.JsStmt // build statements
	updates []jsast.JsStmt // statements of p()
	nested  []jsast.JsStmt // child fragment function declarations
	live    bool           // declares $$live: slot content owned by this fragment
}

func (b *blocksState) next(prefix string) string {
	name := prefix + strconv.Itoa(b.counter)
	b.counter++
	return name
}

func (g *ASTGen) blocksEmit(stmts ...jsast.JsStmt) {
	g.blocks.frag.body = append(g.blocks.frag.body, stmts...)
}

// blocksGuard returns the update condition for a template expression, or nil
// when nothing it reads can change. Reactive names are recorded so the
// component flush runs the view for them.
func (g *ASTGen) blocksGuard(exprs ...*sfcast.TemplateExpr) jsast.JsExpr {
	names := map[string]bool{}
	reactive := g.reactiveNames()
	for _, expr := range exprs {
		if expr == nil || expr.Root() == nil {
			continue
		}
		for _, dep := range sfcast.TemplateExprDeps(expr, reactive) {
			names[dep] = true
			g.blocks.deps[dep] = true
		}
		idents := map[string]bool{}
		for _, ident := range jsast.CollectIdents(expr.Root()) {
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

func blocksDirtyTest(names map[string]bool) jsast.JsExpr {
	if len(names) == 0 {
		return nil
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	var test jsast.JsExpr
	for _, name := range sorted {
		check := jsast.JsExpr(jsast.GenMember(jsast.GenIdent("$$d"), name))
		if test == nil {
			test = check
		} else {
			test = jsast.GenLogical("||", test, check)
		}
	}
	return test
}

// blocksChanged wraps a guard into a `($$d) => …` predicate for a block.
func blocksChanged(test jsast.JsExpr) jsast.JsExpr {
	if test == nil {
		return jsast.GenNull()
	}
	return &jsast.JsArrowFunc{BaseExpr: jsast.BaseExpr{BaseNode: jsast.GenNode()}, Params: []jsast.JsExpr{jsast.GenIdent("$$d")}, Body: test}
}

func blocksArrow(params []jsast.JsExpr, body jsast.JsNode) *jsast.JsArrowFunc {
	return &jsast.JsArrowFunc{BaseExpr: jsast.BaseExpr{BaseNode: jsast.GenNode()}, Params: params, Body: body}
}

// blocksUpdate registers an update statement of the current fragment.
func (g *ASTGen) blocksUpdate(test jsast.JsExpr, stmt jsast.JsStmt) {
	if test == nil {
		return
	}
	g.blocks.frag.updates = append(g.blocks.frag.updates, jsast.GenIf(test, stmt))
}

// blocksInit runs an initial DOM write through the cursor (F-2.2) and
// re-runs it when the expression's inputs change.
func (g *ASTGen) blocksInitAndUpdate(stmt jsast.JsStmt, exprs ...*sfcast.TemplateExpr) []jsast.JsStmt {
	init := jsast.GenExprStmt(jsast.GenCall(jsast.GenMember(jsast.GenIdent("$$c"), "init"), blocksArrow(nil, jsast.GenBlock(stmt))))
	g.blocksUpdate(g.blocksGuard(exprs...), stmt)
	if len(exprs) > 0 {
		if at := g.blocksSiteStmt(exprs[0]); at != nil {
			return []jsast.JsStmt{at, init}
		}
	}
	return []jsast.JsStmt{init}
}

// buildBlocksView lowers the template of a component: it declares the root,
// the root fragment and $$viewUpdate, registers the view with the flush plan,
// and mounts the view during the initial render.
func (g *ASTGen) buildBlocksView(nodes []sfcast.Node) {
	// The server renders the same normalised template (M-5.7).
	NormalizeTableRows(nodes)
	g.blocks.deps = map[string]bool{}
	rootIsElement := len(nodes) == 1
	if rootIsElement {
		_, rootIsElement = nodes[0].(*sfcast.ElementNode)
	}
	// Multiple roots, a structural root or a text root get a
	// display:contents wrapper (B-3.8); the runtime
	// creates or adopts it ($$mountComponent).
	g.body = append(g.body, jsast.GenLet("$$root", jsast.GenNull()))
	// A single root element is assigned to $$root by the root fragment.
	g.blocks.assignRoot = rootIsElement
	root := g.buildBlocksFragment(nodes, nil)
	g.body = append(g.body, root)

	// One structural flush entry runs the view for every reactive name the
	// template reads.
	update := &jsast.JsFuncDecl{BaseStmt: jsast.BaseStmt{BaseNode: jsast.GenNode()}, Name: "$$viewUpdate", Body: jsast.GenBlock(
		jsast.GenIf(jsast.GenUnary("!", jsast.GenIdent("$$view")), jsast.GenReturn(nil)),
		jsast.GenConst("$$d", jsast.GenIdent("$$dirty")),
		jsast.GenAssignStmt(jsast.GenIdent("$$dirty"), jsast.GenCall(jsast.GenMember(jsast.GenIdent("Object"), "create"), jsast.GenNull())),
		jsast.GenExprStmt(jsast.GenCall(jsast.GenMember(jsast.GenIdent("$$view"), "p"), jsast.GenIdent("$$d"))),
	)}
	g.body = append(g.body, update)
	deps := make([]string, 0, len(g.blocks.deps))
	for dep := range g.blocks.deps {
		deps = append(deps, dep)
	}
	sort.Strings(deps)
	call := jsast.GenInvoke("$$viewUpdate")
	for _, dep := range deps {
		g.addStructuralUpdate(dep, call)
	}

	wrapper := jsast.JsExpr(jsast.GenNull())
	if !rootIsElement {
		wrapper = jsast.GenStrLit(g.elementScope)
	}
	setRoot := &jsast.JsArrowFunc{BaseExpr: jsast.BaseExpr{BaseNode: jsast.GenNode()}, Params: []jsast.JsExpr{jsast.GenIdent("$$el")}, Body: jsast.GenBlock(jsast.GenAssignStmt(jsast.GenIdent("$$root"), jsast.GenIdent("$$el")))}
	getRoot := &jsast.JsArrowFunc{BaseExpr: jsast.BaseExpr{BaseNode: jsast.GenNode()}, Body: jsast.GenIdent("$$root")}
	g.initBody = append(g.initBody, jsast.GenAssignStmt(jsast.GenIdent("$$view"), jsast.GenCall(jsast.GenIdent("$$mountComponent"), jsast.GenIdent("$$target"), jsast.GenIdent(rootFragmentName), wrapper, setRoot, getRoot)))
	g.cleanup = append(g.cleanup, jsast.GenIf(jsast.GenIdent("$$view"), jsast.GenExprStmt(jsast.GenCall(jsast.GenMember(jsast.GenIdent("$$view"), "release")))))
}

const rootFragmentName = "$$f_root"

// buildBlocksFragment builds a fragment function for nodes. scopeLocals are
// each/await names bound from $$s; mutable ones are refreshed when the
// scope changes.
func (g *ASTGen) buildBlocksFragment(nodes []sfcast.Node, scopeLocals []blocksLocal) jsast.JsStmt {
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
		frag.scope = append(frag.scope, jsast.GenLet(local.name, jsast.GenMember(jsast.GenIdent("$$s"), local.source)))
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
func (f *blocksFragment) decl(scopeLocals []blocksLocal) jsast.JsStmt {
	params := []jsast.JsExpr{jsast.GenIdent("$$c")}
	if f.scoped {
		params = append(params, jsast.GenIdent("$$s"))
	}
	body := append([]jsast.JsStmt{}, f.scope...)
	if f.live {
		body = append(body, jsast.GenConst("$$live", jsast.GenNew(jsast.GenIdent("Set"))))
	}
	body = append(body, f.nested...)
	body = append(body, f.body...)
	if len(f.updates) > 0 || len(f.locals) > 0 || f.live {
		var update []jsast.JsStmt
		if len(f.locals) > 0 {
			refresh := []jsast.JsStmt{}
			marks := &jsast.JsObjectExpr{BaseExpr: jsast.BaseExpr{BaseNode: jsast.GenNode()}}
			for _, local := range scopeLocals {
				if !local.mutable {
					continue
				}
				refresh = append(refresh, jsast.GenAssignStmt(jsast.GenIdent(local.name), jsast.GenMember(jsast.GenIdent("$$s"), local.source)))
				marks.Props = append(marks.Props, &jsast.JsProp{BaseNode: jsast.GenNode(), Key: jsast.GenIdent(local.name), Value: jsast.GenBoolLit(true)})
			}
			refresh = append(refresh, jsast.GenAssignStmt(jsast.GenIdent("$$d"), jsast.GenCall(jsast.GenMember(jsast.GenIdent("Object"), "assign"),
				jsast.GenCall(jsast.GenMember(jsast.GenIdent("Object"), "create"), jsast.GenNull()), jsast.GenIdent("$$d"), marks)))
			update = append(update, jsast.GenIf(jsast.GenIdent("$$sc"), jsast.GenBlock(refresh...)))
		}
		update = append(update, f.updates...)
		if f.live {
			// Slot content built here lives inside a child component but is
			// updated with this fragment's dirty state (B-3.7).
			update = append(update, jsast.GenForOf("$$h", jsast.GenIdent("$$live"), jsast.GenBlock(jsast.GenExprStmt(jsast.GenCall(jsast.GenMember(jsast.GenIdent("$$h"), "p"), jsast.GenIdent("$$d"))))))
		}
		method := &jsast.JsProp{
			BaseNode: jsast.GenNode(), Kind: "init", Method: true, Key: jsast.GenIdent("p"),
			Value: &jsast.JsFuncExpr{BaseExpr: jsast.BaseExpr{BaseNode: jsast.GenNode()}, Params: []jsast.JsExpr{jsast.GenIdent("$$d"), jsast.GenIdent("$$sc")}, Body: jsast.GenBlock(update...)},
		}
		body = append(body, jsast.GenReturn(&jsast.JsObjectExpr{BaseExpr: jsast.BaseExpr{BaseNode: jsast.GenNode()}, Props: []*jsast.JsProp{method}}))
	}
	return &jsast.JsFuncDecl{BaseStmt: jsast.BaseStmt{BaseNode: jsast.GenNode()}, Name: f.name, Params: params, Body: jsast.GenBlock(body...)}
}

// buildBlocksChildren lowers a child list: an all-static list with an
// element is one static subtree; otherwise runs of static nodes become
// static templates between the dynamic nodes. Whitespace was decided by the
// parser (task 059), so the server writes the same text runs (M-5.10).
func (g *ASTGen) buildBlocksChildren(nodes []sfcast.Node, isRoot bool) {
	if len(nodes) == 0 {
		return
	}
	if !isRoot && hasElement(nodes) && allStatic(nodes) {
		g.blocksStatic(nodes)
		return
	}
	// The parser keeps whitespace only where HTML renders it (one space
	// between siblings, task 059), so every static run is created.
	var run []sfcast.Node
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
		if text, ok := node.(*sfcast.TextNode); ok && len(run) > 0 {
			if last, ok := run[len(run)-1].(*sfcast.TextNode); ok && last.Preformatted == text.Preformatted {
				joined := *text
				joined.Parts = append(append([]*sfcast.Part{}, last.Parts...), text.Parts...)
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

func isElementNode(node sfcast.Node) bool {
	_, ok := node.(*sfcast.ElementNode)
	return ok
}

// blocksStatic clones a module-level template holding static nodes.
func (g *ASTGen) blocksStatic(nodes []sfcast.Node) {
	name := "$$tpl_" + g.name + "_" + strconv.Itoa(len(g.blocks.templates))
	g.blocks.templates = append(g.blocks.templates, jsast.GenConst(name, jsast.GenCall(jsast.GenIdent("$$template"), jsast.GenStrLit(emitStaticHTML(nodes, g.elementScope)))))
	g.blocksEmit(jsast.GenExprStmt(jsast.GenCall(jsast.GenMember(jsast.GenIdent("$$c"), "static"), jsast.GenIdent(name))))
}

func (g *ASTGen) buildBlocksNode(node sfcast.Node, isRoot bool) {
	switch n := node.(type) {
	case *sfcast.ElementNode:
		g.buildBlocksElement(n, isRoot)
	case *sfcast.TextNode:
		g.buildBlocksText(n)
	case *sfcast.IfChainNode:
		if n.Client {
			g.buildBlocksClient(n)
		} else if n.KeyAST != nil {
			g.buildBlocksKey(n)
		} else {
			g.buildBlocksIf(n)
		}
	case *sfcast.EachBlockNode:
		g.buildBlocksEach(n)
	case *sfcast.AwaitBlockNode:
		g.buildBlocksAwait(n)
	case *sfcast.ComponentNode:
		g.buildBlocksComponent(n)
	case *sfcast.SlotNode:
		g.buildBlocksSlot(n)
	default:
		panic(fmt.Sprintf("vela: block engine cannot lower %T", node))
	}
}

func (g *ASTGen) buildBlocksText(text *sfcast.TextNode) {
	ref := g.blocks.next("$$t")
	if textNodeAllStatic(text) {
		value := ""
		for _, part := range text.Parts {
			value += part.Value
		}
		g.blocksEmit(jsast.GenExprStmt(jsast.GenCall(jsast.GenMember(jsast.GenIdent("$$c"), "text"), jsast.GenStrLit(value))))
		return
	}
	for _, part := range text.Parts {
		if !part.Static {
			g.blocksSite(part.Expr)
			break
		}
	}
	g.blocksEmit(jsast.GenConst(ref, jsast.GenCall(jsast.GenMember(jsast.GenIdent("$$c"), "text"), g.textValueExpr(text.Parts))))
	var exprs []*sfcast.TemplateExpr
	for _, part := range text.Parts {
		if !part.Static {
			exprs = append(exprs, part.Expr)
		}
	}
	g.blocksUpdate(g.blocksGuard(exprs...), g.textWriteStmt(ref, text.Parts))
}

func (g *ASTGen) buildBlocksElement(el *sfcast.ElementNode, isRoot bool) {
	ref := g.blocks.next("$$e")
	attrs := jsast.GenArray()
	if g.elementScope != "" {
		attrs = jsast.GenArray(jsast.GenStrLit(g.elementScope), jsast.GenStrLit(""))
	}
	for _, attr := range el.Attrs {
		if attr.IsEvent || attr.IsTwoWay || attr.IsBind || attr.IsBindThis || attr.IsClassDir || attr.IsStyleDir {
			continue
		}
		attrs.Elems = append(attrs.Elems, jsast.GenStrLit(attr.Name), jsast.GenStrLit(attr.Value))
	}
	g.blocksEmit(jsast.GenConst(ref, jsast.GenCall(jsast.GenMember(jsast.GenIdent("$$c"), "element"), jsast.GenStrLit(el.Tag), attrs)))
	if isRoot {
		g.blocksEmit(jsast.GenAssignStmt(jsast.GenIdent("$$root"), jsast.GenIdent(ref)))
	}

	// Listeners first; DOM writes after the children exist, so a select's
	// value is written once its options are present.
	var writes []jsast.JsStmt
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
				expr := el.Children[0].(*sfcast.TextNode).Parts[0].Expr
				write := jsast.GenAssignStmt(jsast.GenMember(jsast.GenIdent(ref), prop), jsast.GenString(g.templateExpr(expr)))
				writes = append(writes, g.blocksInitAndUpdate(write, expr)...)
			} else if text, ok := staticRawText(el.Children); ok {
				g.blocksEmit(jsast.GenAssignStmt(jsast.GenMember(jsast.GenIdent(ref), prop), jsast.GenStrLit(text)))
			} else if text, ok := el.Children[0].(*sfcast.TextNode); ok && len(el.Children) == 1 {
				// Static text with interpolations: the property gets the whole
				// text, as the server writes it.
				var exprs []*sfcast.TemplateExpr
				for _, part := range text.Parts {
					if !part.Static {
						exprs = append(exprs, part.Expr)
					}
				}
				write := jsast.GenAssignStmt(jsast.GenMember(jsast.GenIdent(ref), prop), g.textValueExpr(text.Parts))
				writes = append(writes, g.blocksInitAndUpdate(write, exprs...)...)
			}
		} else {
			g.blocksEmit(jsast.GenExprStmt(jsast.GenCall(jsast.GenMember(jsast.GenIdent("$$c"), "enter"), jsast.GenIdent(ref))))
			g.buildBlocksChildren(el.Children, false)
			g.blocksEmit(jsast.GenExprStmt(jsast.GenCall(jsast.GenMember(jsast.GenIdent("$$c"), "exit"))))
		}
	}
	g.blocksEmit(writes...)
}

func (g *ASTGen) blocksListen(ref, event string, handler jsast.JsExpr) jsast.JsStmt {
	return jsast.GenExprStmt(jsast.GenCall(jsast.GenMember(jsast.GenIdent("$$c"), "listen"), jsast.GenIdent(ref), jsast.GenStrLit(event), handler))
}

func (g *ASTGen) blocksEventHandler(attr *sfcast.Attr) jsast.JsExpr {
	handler := g.eventHandlerExpr(attr)
	if handler == nil {
		return jsast.GenIdent("undefined")
	}
	if _, isIdent := TemplateExprIdent(attr.Expr); isIdent || templateExprIsHandler(attr.Expr) {
		return handler
	}
	return blocksArrow([]jsast.JsExpr{jsast.GenIdent("$event")}, jsast.GenBlock(jsast.GenExprStmt(handler)))
}

// blocksBinding lowers bind:value / bind:checked and one-way :attr writes.
func (g *ASTGen) blocksBinding(ref, tag string, attr *sfcast.Attr) []jsast.JsStmt {
	spec, ok := twoWayBindSpec(tag, attr.Name)
	sv, isIdent := TemplateExprIdent(attr.Expr)
	if attr.IsTwoWay && ok && isIdent && g.hasReactiveSetter(sv) {
		handler := blocksArrow([]jsast.JsExpr{jsast.GenIdent("$e")}, twoWaySetterFromEvent("$$set_"+sv, tag == "select" && attr.Name == "value", spec.readProp))
		g.blocksEmit(g.blocksListen(ref, spec.event, handler))
		if tag != "select" && (attr.Name == "value" || attr.Name == "checked") {
			// Hydration adopts a value the user entered before it (C-4.5).
			read := blocksArrow(nil, g.templateExpr(attr.Expr))
			write := blocksArrow([]jsast.JsExpr{jsast.GenIdent("$$v")}, jsast.GenCall(jsast.GenIdent("$$set_"+sv), jsast.GenIdent("$$v")))
			g.blocksEmit(jsast.GenExprStmt(jsast.GenCall(jsast.GenMember(jsast.GenIdent("$$c"), "adopt"), jsast.GenIdent(ref), jsast.GenStrLit(attr.Name), read, write)))
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
func (g *ASTGen) blocksBindingWrite(ref string, attr *sfcast.Attr, multipleAware bool) jsast.JsStmt {
	value := g.templateExpr(attr.Expr)
	if attr.Name == "checked" {
		value = jsast.GenBool(value)
	}
	target := jsast.GenMember(jsast.GenIdent(ref), attr.Name)
	if multipleAware && attr.Name == "value" {
		return jsast.GenBlock(
			jsast.GenConst("$$v", value),
			jsast.GenIf(jsast.GenMember(jsast.GenIdent(ref), "multiple"), jsast.GenExprStmt(jsast.GenCall(jsast.GenIdent("$$writeSelectValues"), jsast.GenIdent(ref), jsast.GenIdent("$$v")))),
			jsast.GenIf(jsast.GenUnary("!", jsast.GenMember(jsast.GenIdent(ref), "multiple")), jsast.GenIf(jsast.GenBinary("!==", target, jsast.GenIdent("$$v")), jsast.GenAssignStmt(target, jsast.GenIdent("$$v")))),
		)
	}
	return jsast.GenBlock(
		jsast.GenConst("$$v", value),
		jsast.GenIf(jsast.GenBinary("!==", target, jsast.GenIdent("$$v")), jsast.GenAssignStmt(target, jsast.GenIdent("$$v"))),
	)
}

// blocksBindThis assigns the element to its reference after creation and
// clears it when the fragment is released.
func (g *ASTGen) blocksBindThis(ref string, attr *sfcast.Attr) []jsast.JsStmt {
	name, isIdent := TemplateExprIdent(attr.Expr)
	var set, clear jsast.JsStmt
	if isIdent && g.analysis != nil && g.analysis.StateSet()[name] {
		set = jsast.GenInvoke("$$set_"+name, jsast.GenIdent(ref))
		clear = jsast.GenInvoke("$$set_"+name, jsast.GenNull())
	} else {
		target := g.templateExpr(attr.Expr)
		set = jsast.GenAssignStmt(target, jsast.GenIdent(ref))
		clear = jsast.GenAssignStmt(g.templateExpr(attr.Expr), jsast.GenNull())
	}
	// The reference is assigned on creation and on hydration alike (C-4.5),
	// so it is not an initial DOM write.
	return []jsast.JsStmt{
		set,
		jsast.GenExprStmt(jsast.GenCall(jsast.GenMember(jsast.GenMember(jsast.GenIdent("$$c"), "owner"), "add"), blocksArrow(nil, jsast.GenBlock(clear)))),
	}
}

// blocksBlockCall emits `const $$bN = $$c.block(kind, args...)` and forwards
// updates to it.
func (g *ASTGen) blocksBlockCall(kind string, args ...jsast.JsExpr) {
	ref := g.blocks.next("$$b")
	call := jsast.GenCall(jsast.GenMember(jsast.GenIdent("$$c"), "block"), append([]jsast.JsExpr{jsast.GenIdent(kind)}, args...)...)
	g.blocksEmit(jsast.GenConst(ref, call))
	g.blocks.frag.updates = append(g.blocks.frag.updates, jsast.GenExprStmt(jsast.GenCall(jsast.GenMember(jsast.GenIdent(ref), "p"), jsast.GenIdent("$$d"))))
}

// blocksNestedFragment builds a child fragment and declares it inside the
// current fragment, so it closes over the current locals.
func (g *ASTGen) blocksNestedFragment(nodes []sfcast.Node, locals []blocksLocal) string {
	parent := g.blocks.frag
	decl := g.buildBlocksFragment(nodes, locals)
	parent.nested = append(parent.nested, decl)
	return decl.(*jsast.JsFuncDecl).Name
}

func (g *ASTGen) buildBlocksIf(chain *sfcast.IfChainNode) {
	var exprs []*sfcast.TemplateExpr
	var fragments []jsast.JsExpr
	var select_ jsast.JsExpr = jsast.GenNumLit(-1)
	for i := len(chain.Branches) - 1; i >= 0; i-- {
		branch := chain.Branches[i]
		var name string
		g.withBranchTemplateBindings(branch, func() {
			name = g.blocksNestedFragment(branch.Children, nil)
		})
		fragments = append([]jsast.JsExpr{jsast.GenIdent(name)}, fragments...)
		if branch.Expr != nil {
			exprs = append(exprs, branch.Expr)
			select_ = &jsast.JsConditionalExpr{BaseExpr: jsast.BaseExpr{BaseNode: jsast.GenNode()}, Test: g.templateExpr(branch.Expr), Then: jsast.GenNumLit(i), Else: select_}
		} else {
			select_ = jsast.GenNumLit(i)
		}
	}
	if len(exprs) > 0 {
		g.blocksSite(exprs[len(exprs)-1])
	}
	g.blocksBlockCall("$$if", blocksArrow(nil, select_), jsast.GenArray(fragments...), blocksChanged(g.blocksGuard(exprs...)))
}

func (g *ASTGen) buildBlocksKey(chain *sfcast.IfChainNode) {
	var body []sfcast.Node
	if len(chain.Branches) > 0 {
		body = chain.Branches[0].Children
	}
	name := g.blocksNestedFragment(body, nil)
	g.blocksSite(chain.KeyAST)
	g.blocksBlockCall("$$key", blocksArrow(nil, g.templateExpr(chain.KeyAST)), jsast.GenIdent(name), blocksChanged(g.blocksGuard(chain.KeyAST)))
}

func (g *ASTGen) buildBlocksEach(each *sfcast.EachBlockNode) {
	index := each.IndexName
	if index == "" {
		index = "$$index"
	}
	locals := []blocksLocal{{name: each.ItemName, source: "item", mutable: true}, {name: index, source: "index", mutable: true}}
	row := g.blocksNestedFragment(each.Children, locals)
	key := jsast.JsExpr(jsast.GenNull())
	if each.HasKey() {
		// Keyed rows compare String(key), so 1 and "1" are one row.
		g.blocks.locals = append(g.blocks.locals, locals...)
		key = blocksArrow([]jsast.JsExpr{jsast.GenIdent(each.ItemName), jsast.GenIdent(index)}, jsast.GenString(g.templateExpr(each.KeyAST)))
		g.blocks.locals = g.blocks.locals[:len(g.blocks.locals)-len(locals)]
	}
	fallback := jsast.JsExpr(jsast.GenNull())
	if len(each.ElseChildren) > 0 {
		fallback = jsast.GenIdent(g.blocksNestedFragment(each.ElseChildren, nil))
	}
	args := []jsast.JsExpr{blocksArrow(nil, g.templateExpr(each.IterExpr)), key, jsast.GenIdent(row), fallback, blocksChanged(g.blocksGuard(each.IterExpr))}
	// A keyed each over a $state.deep collection refreshes only the rows that
	// in-place mutations touched (task 023).
	if each.HasKey() {
		if plan := g.eachIncrementalPlanFor(each); plan != nil {
			args = append(args, blocksArrow(nil, jsast.GenCall(jsast.GenIdent("$$eachRowsForMutations"), jsast.GenIdent("$$activeMutations_"+plan.stateName), jsast.GenStrLit(plan.keyField))))
		}
	}
	g.blocksSite(each.IterExpr)
	g.blocksBlockCall("$$each", args...)
}

func (g *ASTGen) buildBlocksAwait(await *sfcast.AwaitBlockNode) {
	branch := func(nodes []sfcast.Node, name, source string, present bool) jsast.JsExpr {
		if !present {
			return jsast.GenNull()
		}
		var locals []blocksLocal
		if name != "" {
			locals = []blocksLocal{{name: name, source: source}}
		}
		return jsast.GenIdent(g.blocksNestedFragment(nodes, locals))
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
func (g *ASTGen) buildBlocksComponent(comp *sfcast.ComponentNode) {
	slots := &jsast.JsObjectExpr{BaseExpr: jsast.BaseExpr{BaseNode: jsast.GenNode()}}
	names := make([]string, 0, len(comp.Slots))
	for name := range comp.Slots {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fragment := g.blocksNestedFragment(comp.Slots[name], nil)
		g.blocks.frag.live = true
		slots.Props = append(slots.Props, &jsast.JsProp{
			BaseNode: jsast.GenNode(),
			Key:      jsast.GenStrLit(name),
			Value:    jsast.GenCall(jsast.GenIdent("$$slotFactory"), jsast.GenIdent("$$c"), jsast.GenIdent("$$live"), jsast.GenIdent(fragment)),
		})
	}

	events := &jsast.JsObjectExpr{BaseExpr: jsast.BaseExpr{BaseNode: jsast.GenNode()}}
	var exprs []*sfcast.TemplateExpr
	for _, attr := range comp.Attrs {
		if attr.IsEvent {
			events.Props = append(events.Props, &jsast.JsProp{
				BaseNode: jsast.GenNode(),
				Key:      jsast.GenStrLit(strings.TrimPrefix(attr.Name, "on:")),
				Value:    g.blocksComponentEventHandler(attr),
			})
			continue
		}
		if !attr.IsBindThis && attr.Expr != nil {
			exprs = append(exprs, attr.Expr)
		}
	}
	var eventsArg jsast.JsExpr = jsast.GenNull()
	if len(events.Props) > 0 {
		eventsArg = events
	}
	props := blocksArrow(nil, g.componentPropsExpr(comp.Attrs))
	g.blocksBlockCall("$$component", jsast.GenIdent(comp.Name), props, slots, eventsArg, blocksChanged(g.blocksGuard(exprs...)), jsast.GenStrLit(g.elementScope))
}

// blocksComponentEventHandler forwards only CustomEvents dispatched by the
// child, so a native event of the same name bubbling from its DOM is not
// mistaken for a component event (as in buildComponentEvents).
func (g *ASTGen) blocksComponentEventHandler(attr *sfcast.Attr) jsast.JsExpr {
	handler := g.eventHandlerExpr(attr)
	if handler == nil {
		handler = jsast.GenIdent("undefined")
	}
	var invoke jsast.JsStmt = jsast.GenExprStmt(jsast.GenCall(handler, jsast.GenIdent("$e")))
	if _, isIdent := TemplateExprIdent(attr.Expr); !isIdent && !templateExprIsHandler(attr.Expr) {
		invoke = jsast.GenExprStmt(handler)
		if g.analysis != nil {
			invoke = analyzer.RewriteStatementAssignmentsAST(invoke, g.analysis.StateSet())
		}
		pos := g.sfc.TemplateOffset + attr.Expr.Pos
		jsast.WalkNode(invoke, func(node jsast.JsNode) bool {
			setTemplateOrigin(node, pos)
			return true
		})
	}
	return blocksArrow([]jsast.JsExpr{jsast.GenIdent("$e")}, jsast.GenBlock(
		jsast.GenIf(jsast.GenBinary("instanceof", jsast.GenIdent("$e"), jsast.GenIdent("CustomEvent")), jsast.GenBlock(invoke)),
	))
}

// buildBlocksSlot opens the parent's slot content, or mounts the fallback.
func (g *ASTGen) buildBlocksSlot(slot *sfcast.SlotNode) {
	var fallback jsast.JsExpr = jsast.GenNull()
	if len(slot.Fallback) > 0 {
		fallback = jsast.GenIdent(g.blocksNestedFragment(slot.Fallback, nil))
	}
	g.blocksBlockCall("$$slot", jsast.GenComputedMember(jsast.GenIdent("$$slots"), jsast.GenStrLit(slot.Name)), fallback)
}

func textNodeStaticValue(text *sfcast.TextNode) string {
	value := ""
	for _, part := range text.Parts {
		value += part.Value
	}
	return value
}

// hasPreformattedText reports text of a <pre>, <textarea>, or <listing>
// in a run: its whitespace is content.
func hasPreformattedText(run []sfcast.Node) bool {
	for _, node := range run {
		if text, ok := node.(*sfcast.TextNode); ok && text.Preformatted {
			return true
		}
	}
	return false
}

// buildBlocksClient lowers {#client}…{:else}…{/client} to the $$client
// block: the main branch fragment and the fallback fragment, or null for
// an empty fallback (T-9.1b).
func (g *ASTGen) buildBlocksClient(chain *sfcast.IfChainNode) {
	main := g.blocksNestedFragment(chain.Branches[0].Children, nil)
	fallback := jsast.JsExpr(jsast.GenNull())
	if len(chain.Branches) > 1 && len(chain.Branches[1].Children) > 0 {
		fallback = jsast.GenIdent(g.blocksNestedFragment(chain.Branches[1].Children, nil))
	}
	g.blocksBlockCall("$$client", jsast.GenIdent(main), fallback)
}

// blocksSite emits $$c.at(site) before an adopted site in development
// builds, so that a hydration mismatch names its source position, site
// ID, and host symbol (V-11.3b).
func (g *ASTGen) blocksSite(expr *sfcast.TemplateExpr) {
	if at := g.blocksSiteStmt(expr); at != nil {
		g.blocksEmit(at)
	}
}

func (g *ASTGen) blocksSiteStmt(expr *sfcast.TemplateExpr) jsast.JsStmt {
	if g.sfc == nil || g.sfc.SitePath == "" || expr == nil || false {
		return nil
	}
	line, column := SourceLineColumn(g.sfc.Source, g.sfc.TemplateOffset+expr.Pos)
	host := jsast.JsExpr(jsast.GenNull())
	if g.sfc.SiteHosts != nil && expr.Root() != nil {
		jsast.WalkNode(expr.Root(), func(n jsast.JsNode) bool {
			if call, ok := n.(*jsast.JsCallExpr); ok {
				if callee, ok := call.Callee.(*jsast.JsIdent); ok {
					if name, bound := g.sfc.SiteHosts(callee.Name); bound {
						host = jsast.GenStrLit(name)
						return false
					}
				}
			}
			return true
		})
	}
	site := len(g.blocks.sites)
	g.blocks.sites = append(g.blocks.sites, jsast.GenArray(jsast.GenNumLit(site), jsast.GenStrLit(fmt.Sprintf("%s:%d:%d", g.sfc.SitePath, line+1, column+1)), host))
	return jsast.GenExprStmt(jsast.GenCall(jsast.GenMember(jsast.GenIdent("$$c"), "at"), &jsast.JsMemberExpr{BaseExpr: jsast.BaseExpr{BaseNode: jsast.GenNode()}, Obj: jsast.GenIdent("$$sites"), Prop: jsast.GenNumLit(site), Computed: true}))
}
