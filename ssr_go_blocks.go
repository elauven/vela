package main

import (
	"fmt"
	goast "go/ast"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Block-engine Go renderers (task 040, docs/HYDRATION_ARCHITECTURE.md §5,
// §6, §8.2). A renderer writes block ranges, records its component state in
// the payload stream of the root, and has no instance-identifier API.
//
// The stream travels in the io.Writer: the first renderer called with a
// plain writer is the root, wraps the writer, and writes the payload script
// after its range; nested components and slot closures receive the wrapped
// writer through their ordinary w parameter.

func buildSSRGoBlocksFile(plan *SSRRenderPlan, packageName string, includeProps bool, region *SSRRegion) (*goast.File, error) {
	file, err := buildSSRGoBlocksSkeleton(plan, packageName, includeProps)
	if err != nil {
		return nil, err
	}
	emitter := ssrGoEmitter{
		ctx: NewSSRExprContext(plan), packageName: packageName, scopeID: plan.ScopeID, elementScope: plan.ElementScope,
		awaitOptions: goast.NewIdent("options"), componentName: plan.ComponentName,
		region: region, facts: map[JsExpr]ssrIRFacts{},
	}
	// A single root element is the component's element; any other root gets
	// the display:contents wrapper the client creates (B-3.8). Either one
	// carries the parent's scope attribute.
	if len(plan.Nodes) == 1 {
		emitter.rootElement, _ = plan.Nodes[0].(*SSRPlanElement)
	}
	emitter.ctx.funcs = newSSRFuncTable(plan, emitter.region)
	emitter.ctx.funcs.base = emitter.ctx
	if emitter.region != nil {
		emitter.region.funcs = emitter.ctx.funcs
	}
	defaults, err := emitter.propDefaults(plan.Props)
	if err != nil {
		return nil, err
	}
	if emitter.throwing() > 0 {
		defaults = append(defaults, ssrJSCheck())
	}
	templateBody, err := emitter.nodes(plan.Nodes)
	if err != nil {
		return nil, emitter.region.abort(err)
	}
	if err := emitter.region.finish(plan); err != nil {
		return nil, err
	}
	if emitter.rootElement == nil {
		wrapped := []goast.Stmt{
			goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(ssrWrapperOpen(plan.ElementScope))}),
			goWriteSSRErr(&goast.CallExpr{Fun: goSelector("vela", "WriteHostScope"), Args: []goast.Expr{goast.NewIdent("w")}}),
			goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(">")}),
		}
		wrapped = append(wrapped, templateBody...)
		templateBody = append(wrapped, goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote("</div>")}))
	}
	init := emitter.localsPrologue(plan, plan.StateNames)
	functions := emitter.ctx.funcs.declarations()
	body := ssrJSPrologue(emitter.region)
	if modules := ssrModulePrologue(emitter.region, &goast.CallExpr{Fun: goSelector("vela", "ContextOf"), Args: []goast.Expr{goast.NewIdent("w")}}); len(modules) > 0 {
		// Module initialisation can throw (ssr_module.go).
		body = append(append(body, modules...), ssrJSCheck())
	}
	body = append(body, functions...)
	body = append(body, defaults...)
	body = append(body, init.statements...)
	body = append(body, ssrBlocksBeginComponent(plan, init.evaluated)...)
	if plan.RootStatic {
		// A static root writes plain HTML (A-8.1).
		body = append([]goast.Stmt{&goast.AssignStmt{Lhs: []goast.Expr{goast.NewIdent("w")}, Tok: token.ASSIGN, Rhs: []goast.Expr{&goast.CallExpr{Fun: goSelector("vela", "StaticRoot"), Args: []goast.Expr{goast.NewIdent("w")}}}}}, body...)
	}
	body = append(body, emitter.openRange(""))
	body = append(body, templateBody...)
	body = append(body, emitter.closeRange())
	body = append(body, &goast.ReturnStmt{Results: []goast.Expr{&goast.CallExpr{
		Fun:  goSelector("vela", "EndComponent"),
		Args: []goast.Expr{goast.NewIdent("w"), goast.NewIdent("ssrRoot"), goast.NewIdent("ssrProps")},
	}}})
	render, err := ssrFuncDecl(file, "Render"+plan.ComponentName+"WithOptions")
	if err != nil {
		return nil, err
	}
	render.Body.List = body
	appendSSRGoImports(file, emitter.imports)
	appendSSRGoImports(file, emitter.region.hostImports)
	return file, nil
}

// ssrBlocksBeginComponent registers the instance in the stream and, for a
// root, serializes its props before anything is written (S-6.2): a props
// error leaves the writer untouched.
//
//	w, ssrRoot := vela.BeginComponent(w, "X", `{...}`, omitted...)
//	var ssrProps []byte
//	if ssrRoot {
//		var err error
//		if ssrProps, err = vela.MarshalHydrationPayload(struct{...}{...}); err != nil {
//			return err
//		}
//	}
func ssrBlocksBeginComponent(plan *SSRRenderPlan, evaluated map[string]bool) []goast.Stmt {
	// The "s" entry carries the computed initial values of the $state and
	// $state.deep bindings without a reproducible initializer (S-6.4,
	// T-6.6d). A component without any writes no entry (nil state); the
	// client reads none. $bindable props are not written: the client takes
	// them from the props (task 059).
	state := goast.Expr(goast.NewIdent("nil"))
	if len(plan.PayloadState) > 0 {
		state = &goast.CallExpr{Fun: goSelector("vela", "NewObject")}
	}
	var omitted []goast.Expr
	for _, name := range plan.PayloadState {
		if !evaluated[name] {
			omitted = append(omitted, ssrGoString(name))
			continue
		}
		state = &goast.CallExpr{Fun: goSelectorExpr(state, "Set"), Args: []goast.Expr{ssrGoString(name), goast.NewIdent(ssrLocalName(name))}}
	}
	beginArgs := append([]goast.Expr{goast.NewIdent("w"), ssrGoString(plan.ComponentName), state}, omitted...)
	fields := make([]*goast.Field, 0, len(plan.Props))
	values := make([]goast.Expr, 0, len(plan.Props))
	for _, prop := range plan.Props {
		fields = append(fields, &goast.Field{
			Names: []*goast.Ident{goast.NewIdent(prop.GoName)},
			Type:  goast.NewIdent("any"),
			Tag:   &goast.BasicLit{Kind: token.STRING, Value: "`json:\"" + prop.SourceName + "\"`"},
		})
		values = append(values, &goast.KeyValueExpr{
			Key:   goast.NewIdent(prop.GoName),
			Value: &goast.SelectorExpr{X: goast.NewIdent("props"), Sel: goast.NewIdent(prop.GoName)},
		})
	}
	propsValue := &goast.CompositeLit{Type: &goast.StructType{Fields: &goast.FieldList{List: fields}}, Elts: values}
	errIdent := goast.NewIdent("err")
	return []goast.Stmt{
		&goast.AssignStmt{
			Lhs: []goast.Expr{goast.NewIdent("w"), goast.NewIdent("ssrRoot")},
			Tok: token.DEFINE,
			Rhs: []goast.Expr{&goast.CallExpr{Fun: goSelector("vela", "BeginComponent"), Args: beginArgs}},
		},
		&goast.DeclStmt{Decl: &goast.GenDecl{Tok: token.VAR, Specs: []goast.Spec{&goast.ValueSpec{
			Names: []*goast.Ident{goast.NewIdent("ssrProps")},
			Type:  &goast.ArrayType{Elt: goast.NewIdent("byte")},
		}}}},
		&goast.IfStmt{Cond: goast.NewIdent("ssrRoot"), Body: &goast.BlockStmt{List: []goast.Stmt{
			&goast.DeclStmt{Decl: &goast.GenDecl{Tok: token.VAR, Specs: []goast.Spec{&goast.ValueSpec{
				Names: []*goast.Ident{goast.NewIdent("err")},
				Type:  goast.NewIdent("error"),
			}}}},
			&goast.IfStmt{
				Init: &goast.AssignStmt{
					Lhs: []goast.Expr{goast.NewIdent("ssrProps"), errIdent},
					Tok: token.ASSIGN,
					Rhs: []goast.Expr{&goast.CallExpr{Fun: goSelector("vela", "MarshalHydrationPayload"), Args: []goast.Expr{propsValue}}},
				},
				Cond: &goast.BinaryExpr{X: goast.NewIdent("err"), Op: token.NEQ, Y: goast.NewIdent("nil")},
				Body: &goast.BlockStmt{List: []goast.Stmt{&goast.ReturnStmt{Results: []goast.Expr{goast.NewIdent("err")}}}},
			},
		}}},
	}
}

func (e *ssrGoEmitter) openRange(state string) goast.Stmt {
	return e.openRangeExpr(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(state)})
}

func (e *ssrGoEmitter) openRangeExpr(state goast.Expr) goast.Stmt {
	return goWriteSSRErr(&goast.CallExpr{Fun: goSelector("vela", "OpenRange"), Args: []goast.Expr{goast.NewIdent("w"), state}})
}

func (e *ssrGoEmitter) closeRange() goast.Stmt {
	return goWriteSSRErr(&goast.CallExpr{Fun: goSelector("vela", "CloseRange"), Args: []goast.Expr{goast.NewIdent("w")}})
}

// ranged wraps statements in one block range.
func (e *ssrGoEmitter) ranged(state string, body []goast.Stmt) []goast.Stmt {
	out := make([]goast.Stmt, 0, len(body)+2)
	out = append(out, e.openRange(state))
	out = append(out, body...)
	return append(out, e.closeRange())
}

// awaitSite is the compile-time await site identifier of the payload stream
// (S-6.4): the component name and the await's dynamic ID.
func (e *ssrGoEmitter) awaitSite(id int) string {
	return e.componentName + "#" + itoa(id)
}

func buildSSRGoBlocksSkeleton(plan *SSRRenderPlan, packageName string, includeProps bool) (*goast.File, error) {
	if plan == nil {
		return nil, fmt.Errorf("ssr go AST: missing render plan")
	}
	component := strings.TrimSpace(plan.ComponentName)
	if !isGoExportedIdentifier(component) {
		return nil, fmt.Errorf("ssr go AST: component name %q is not an exported Go identifier", component)
	}
	packageName = strings.TrimSpace(packageName)
	if !isGoPackageIdentifier(packageName) {
		return nil, fmt.Errorf("ssr go AST: package name %q is not a valid Go identifier", packageName)
	}
	propsName := component + "Props"
	slotsName := component + "Slots"
	snapshotName := component + "AwaitSnapshot"
	optionsName := component + "RenderOptions"
	decls := []goast.Decl{goSSRImportDecl(), goRuntimeContractDecl()}
	if includeProps {
		decls = append(decls, goPropsTypeDecl(propsName, plan.Props))
	}
	writer := goSelector("io", "Writer")
	errResult := &goast.FieldList{List: []*goast.Field{{Type: goast.NewIdent("error")}}}
	params := func(extra ...*goast.Field) *goast.FieldList {
		list := []*goast.Field{
			{Names: []*goast.Ident{goast.NewIdent("w")}, Type: writer},
			{Names: []*goast.Ident{goast.NewIdent("props")}, Type: goast.NewIdent(propsName)},
			{Names: []*goast.Ident{goast.NewIdent("slots")}, Type: goast.NewIdent(slotsName)},
		}
		return &goast.FieldList{List: append(list, extra...)}
	}
	call := func(name string, w goast.Expr) goast.Stmt {
		return &goast.ReturnStmt{Results: []goast.Expr{&goast.CallExpr{Fun: goast.NewIdent(name), Args: []goast.Expr{
			w, goast.NewIdent("props"), goast.NewIdent("slots"), &goast.CompositeLit{Type: goast.NewIdent(optionsName)},
		}}}}
	}
	withOptions := "Render" + component + "WithOptions"
	decls = append(decls,
		goSlotsTypeDecl(slotsName, plan.SlotNames),
		goAwaitSnapshotTypeDecl(snapshotName, "any"),
		&goast.GenDecl{Tok: token.TYPE, Specs: []goast.Spec{&goast.TypeSpec{
			Name: goast.NewIdent(optionsName),
			Type: &goast.StructType{Fields: &goast.FieldList{List: []*goast.Field{
				// Await selects the branch of a root await site by its ID
				// (A-8.5, T-7.4f).
				{Names: []*goast.Ident{goast.NewIdent("Await")}, Type: &goast.MapType{Key: goast.NewIdent("int"), Value: goast.NewIdent(snapshotName)}, Tag: &goast.BasicLit{Kind: token.STRING, Value: "`json:\"await,omitempty\"`"}},
			}}},
		}}},
		&goast.FuncDecl{
			Name: goast.NewIdent(withOptions),
			Type: &goast.FuncType{Params: params(&goast.Field{Names: []*goast.Ident{goast.NewIdent("options")}, Type: goast.NewIdent(optionsName)}), Results: errResult},
			Body: &goast.BlockStmt{},
		},
		&goast.FuncDecl{
			Name: goast.NewIdent("Render" + component),
			Type: &goast.FuncType{Params: params(), Results: errResult},
			Body: &goast.BlockStmt{List: []goast.Stmt{call(withOptions, goast.NewIdent("w"))}},
		},
		goRenderContextFuncDecl(component, propsName, slotsName),
		&goast.FuncDecl{
			Name: goast.NewIdent("Render" + component + "Static"),
			Type: &goast.FuncType{Params: params(), Results: errResult},
			Body: &goast.BlockStmt{List: []goast.Stmt{call(withOptions, &goast.CallExpr{Fun: goSelector("vela", "StaticWriter"), Args: []goast.Expr{goast.NewIdent("w")}})}},
		},
		&goast.FuncDecl{
			Name: goast.NewIdent(component),
			Type: &goast.FuncType{
				Params: &goast.FieldList{List: []*goast.Field{
					{Names: []*goast.Ident{goast.NewIdent("props")}, Type: goast.NewIdent(propsName)},
					{Names: []*goast.Ident{goast.NewIdent("slots")}, Type: goast.NewIdent(slotsName)},
				}},
				Results: &goast.FieldList{List: []*goast.Field{{Type: goSelector("vela", "Renderer")}}},
			},
			Body: &goast.BlockStmt{List: []goast.Stmt{&goast.ReturnStmt{Results: []goast.Expr{&goast.CallExpr{
				Fun: goSelector("vela", "RendererFunc"),
				Args: []goast.Expr{&goast.FuncLit{
					Type: &goast.FuncType{Params: &goast.FieldList{List: []*goast.Field{{Names: []*goast.Ident{goast.NewIdent("w")}, Type: writer}}}, Results: errResult},
					Body: &goast.BlockStmt{List: []goast.Stmt{&goast.ReturnStmt{Results: []goast.Expr{&goast.CallExpr{
						Fun: goast.NewIdent("Render" + component), Args: []goast.Expr{goast.NewIdent("w"), goast.NewIdent("props"), goast.NewIdent("slots")},
					}}}}},
				}},
			}}}}},
		},
	)
	return &goast.File{Name: goast.NewIdent(packageName), Decls: decls}, nil
}

func ssrFuncDecl(file *goast.File, name string) (*goast.FuncDecl, error) {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*goast.FuncDecl)
		if ok && function.Name != nil && function.Name.Name == name {
			return function, nil
		}
	}
	return nil, fmt.Errorf("ssr go AST: missing %s declaration", name)
}

// blocksIf writes an {#if} chain as one range whose state token is the
// active branch index, or "x" when no branch renders; a {#key} block is a
// plain range (§5.1).
func (e *ssrGoEmitter) blocksIf(node *SSRPlanIf) ([]goast.Stmt, error) {
	if node.Key != nil {
		if len(node.Branches) != 1 || node.Branches[0].Condition != nil {
			return nil, ssrExprUnsupported(node.Span.Start, "SSR key block has an invalid branch shape")
		}
		children, err := e.nodes(node.Branches[0].Children)
		if err != nil {
			return nil, err
		}
		return e.ranged("", children), nil
	}
	var first, previous *goast.IfStmt
	hasElse := false
	for index, branch := range node.Branches {
		children, err := e.nodes(branch.Children)
		if err != nil {
			return nil, err
		}
		body := e.ranged(itoa(index), children)
		if branch.Condition == nil {
			if previous == nil {
				return nil, ssrExprUnsupported(branch.Span.Start, "SSR if block has an unconditional first branch")
			}
			previous.Else = &goast.BlockStmt{List: body}
			hasElse = true
			continue
		}
		condition, err := e.translate("if", "", branch.Condition, e.ctx)
		if err != nil {
			return nil, err
		}
		current := &goast.IfStmt{Cond: e.condition(branch.Condition, condition), Body: &goast.BlockStmt{List: body}}
		if first == nil {
			first = current
		} else {
			previous.Else = current
		}
		previous = current
	}
	if first == nil {
		return nil, ssrExprUnsupported(node.Span.Start, "SSR if block has no conditional branch")
	}
	if !hasElse {
		previous.Else = &goast.BlockStmt{List: e.ranged("x", nil)}
	}
	return []goast.Stmt{first}, nil
}

// blocksAwait writes the branch selected by the root's await snapshot as a
// range with state token p, t, or c, and records the matching "a" entry
// (S-6.4). Only root render options select a snapshot: nested components
// render their pending branch.
func (e *ssrGoEmitter) blocksAwait(node *SSRPlanAwait, pending []goast.Stmt) ([]goast.Stmt, error) {
	return e.blocksAwaitBlock(node, pending)
}

// writeSSRBlocksProjectFile writes the server products of one component:
// the Go renderer, its preserved props contract, and scoped styles. The
// client module is the ordinary CSR module; there are no per-component
// hydration artifacts (A-8.8).
func writeSSRBlocksProjectFile(src, srcRoot string, layout SSRProjectLayout, plan *SSRRenderPlan, file *goast.File) error {
	source, err := FormatSSRGoFile(file)
	if err != nil {
		return err
	}
	serverPath := layout.serverPath(src, srcRoot)
	if err := os.MkdirAll(filepath.Dir(serverPath), 0o755); err != nil {
		return err
	}
	if _, err := ssrProjectPropsSource(plan, layout.propsPath(src, srcRoot), layout.packageName(src, srcRoot)); err != nil {
		return err
	}
	if err := os.WriteFile(serverPath, source, 0o644); err != nil {
		return err
	}
	if plan.ScopedCSS == "" {
		return nil
	}
	stylePath := layout.stylePath(src, srcRoot)
	if err := os.MkdirAll(filepath.Dir(stylePath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(stylePath, []byte(plan.ScopedCSS), 0o644)
}

// checkSSRBlocksNesting rejects a template whose server HTML the browser
// would parse into a different node sequence (§5.2), with a source position.
// A child component's root tag comes from its own template.
func checkSSRBlocksNesting(filename, source string, nodes []Node, links map[string]SSRComponentLink) error {
	ctx := HTMLNestingContext{ComponentRoot: func(component *ComponentNode) string {
		link, ok := links[component.Name]
		if !ok {
			return ""
		}
		data, err := os.ReadFile(link.SourcePath)
		if err != nil {
			return ""
		}
		child, err := parseSFC(string(data))
		if err != nil || child.TemplateAST == nil {
			return ""
		}
		if len(child.TemplateAST.Nodes) == 1 {
			if element, ok := child.TemplateAST.Nodes[0].(*ElementNode); ok {
				return element.Tag
			}
		}
		return "div"
	}}
	if err := checkHTMLNesting(nodes, ctx); err != nil {
		line, column := sourceLineColumn(source, err.Pos)
		return fmt.Errorf("%s:%d:%d: %w", filename, line+1, column+1, err)
	}
	return nil
}

// blocksAwaitBlock renders an {#await} block. When the server can evaluate
// the operand (T-7.4f): the operand is settled synchronously, the then or
// catch branch is rendered with the value or error, and the payload holds
// the settled snapshot, which the client adopts without re-evaluating the
// operand. A snapshot in the render options still takes precedence and
// the operand is then not evaluated. Snapshot values are any JSON value.
// Otherwise only a snapshot selects a branch and the server renders
// pending (a client boundary, T-9.1b, is task 047).
func (e *ssrGoEmitter) blocksAwaitBlock(node *SSRPlanAwait, pending []goast.Stmt) ([]goast.Stmt, error) {
	var operand goast.Expr
	if e.region != nil && node.Promise != nil {
		err := e.speculate(func() error {
			var err error
			operand, err = e.translate("await", "", node.Promise, e.ctx)
			return err
		})
		if err != nil {
			// An operand the server cannot evaluate needs a {#client} boundary
			// around the block (T-7.4f, T-9.3): a required site.
			operand = nil
			e.region.add("await", "", e.region.TemplateOffset+node.Promise.Pos, true, nil, nil, err)
		}
	}
	evaluated := operand != nil
	if evaluated {
		// Settling reads and may set the evaluation state.
		e.region.usesJS = true
	}
	id := itoa(node.DynamicID)
	snapshot, tokenVar, value := "ssrAwaitSnapshot"+id, "ssrAwaitToken"+id, "ssrAwaitValue"+id
	site := &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(e.awaitSite(node.DynamicID))}
	branch := func(nodes []SSRPlanNode, binding string) ([]goast.Stmt, error) {
		previous := e.ctx
		e.ctx = ssrContextWithBindings(e.ctx)
		if binding != "" {
			e.ctx.Bindings[binding] = goast.NewIdent(value)
		}
		defer func() { e.ctx = previous }()
		if evaluated {
			return e.nodes(nodes)
		}
		// Only a caller snapshot selects this branch: content the server
		// cannot render fails that render, not the build.
		var body []goast.Stmt
		if err := e.speculate(func() error {
			var err error
			body, err = e.nodes(nodes)
			return err
		}); err != nil {
			if e.imports == nil {
				e.imports = map[string]string{}
			}
			e.imports["fmt"] = "fmt"
			return []goast.Stmt{&goast.ReturnStmt{Results: []goast.Expr{&goast.CallExpr{Fun: goSelector("fmt", "Errorf"), Args: []goast.Expr{
				ssrGoString("SSR await snapshot " + id + " branch cannot be rendered on the server: " + ssrDiagnosticSummary(err)),
			}}}}}, nil
		}
		return body, nil
	}
	thenBody, err := branch(node.ThenChildren, node.ValueName)
	if err != nil {
		return nil, err
	}
	catchBody, err := branch(node.CatchChildren, node.ErrorName)
	if err != nil {
		return nil, err
	}
	field := func(name string) goast.Expr { return goSelectorExpr(goast.NewIdent(snapshot), name) }
	assign := func(lhs []string, tok token.Token, rhs goast.Expr) goast.Stmt {
		left := make([]goast.Expr, len(lhs))
		for i, name := range lhs {
			left[i] = goast.NewIdent(name)
		}
		return &goast.AssignStmt{Lhs: left, Tok: tok, Rhs: []goast.Expr{rhs}}
	}
	declare := func(name string, typ string) goast.Stmt {
		return &goast.DeclStmt{Decl: &goast.GenDecl{Tok: token.VAR, Specs: []goast.Spec{&goast.ValueSpec{Names: []*goast.Ident{goast.NewIdent(name)}, Type: goast.NewIdent(typ)}}}}
	}
	operandVar := "ssrAwaitOperand" + id
	var otherwise []goast.Stmt
	if evaluated {
		otherwise = []goast.Stmt{
			assign([]string{operandVar}, token.DEFINE, operand),
			ssrJSCheck(),
			assign([]string{tokenVar, value}, token.ASSIGN, &goast.CallExpr{Fun: goSelector("vela", "AwaitSettle"), Args: []goast.Expr{goast.NewIdent("w"), goast.NewIdent("ssrJS"), site, goast.NewIdent(operandVar)}}),
			ssrJSCheck(),
		}
	} else {
		// Without a snapshot the server renders pending.
		otherwise = []goast.Stmt{assign([]string{tokenVar}, token.ASSIGN, &goast.CallExpr{Fun: goSelector("vela", "AwaitEntry"), Args: []goast.Expr{goast.NewIdent("w"), site, ssrGoString(""), goast.NewIdent("nil"), goast.NewIdent("nil")}})}
	}
	return []goast.Stmt{
		assign([]string{snapshot}, token.DEFINE, &goast.IndexExpr{X: goSelectorExpr(e.awaitOptions, "Await"), Index: &goast.BasicLit{Kind: token.INT, Value: id}}),
		declare(tokenVar, "string"),
		declare(value, "any"),
		&goast.IfStmt{
			Cond: &goast.BinaryExpr{X: field("State"), Op: token.NEQ, Y: &goast.BasicLit{Kind: token.STRING, Value: `""`}},
			Body: &goast.BlockStmt{List: []goast.Stmt{
				assign([]string{tokenVar}, token.ASSIGN, &goast.CallExpr{Fun: goSelector("vela", "AwaitEntry"), Args: []goast.Expr{goast.NewIdent("w"), site, field("State"), field("Value"), field("Error")}}),
				assign([]string{value}, token.ASSIGN, field("Value")),
				&goast.IfStmt{Cond: &goast.BinaryExpr{X: goast.NewIdent(tokenVar), Op: token.EQL, Y: &goast.BasicLit{Kind: token.STRING, Value: `"c"`}}, Body: &goast.BlockStmt{List: []goast.Stmt{
					assign([]string{value}, token.ASSIGN, field("Error")),
				}}},
			}},
			Else: &goast.BlockStmt{List: otherwise},
		},
		ssrUse(value),
		e.openRangeExpr(goast.NewIdent(tokenVar)),
		&goast.SwitchStmt{Tag: goast.NewIdent(tokenVar), Body: &goast.BlockStmt{List: []goast.Stmt{
			&goast.CaseClause{List: []goast.Expr{&goast.BasicLit{Kind: token.STRING, Value: `"t"`}}, Body: thenBody},
			&goast.CaseClause{List: []goast.Expr{&goast.BasicLit{Kind: token.STRING, Value: `"c"`}}, Body: catchBody},
			&goast.CaseClause{Body: pending},
		}}},
		e.closeRange(),
	}, nil
}

// clientBlock renders a {#client} block or a client-only component: the
// fallback in a range with the state token "f" (contract §9.1). The main
// branch has no server code.
func (e *ssrGoEmitter) clientBlock(node *SSRPlanClient) ([]goast.Stmt, error) {

	fallback, err := e.nodes(node.Fallback)
	if err != nil {
		return nil, err
	}
	return e.ranged("f", fallback), nil
}

// ssrWrapperOpen opens the display:contents wrapper of a multi-root
// component as the client creates it: with the scope attribute only when the
// component has styles.
func ssrWrapperOpen(scope string) string {
	if scope == "" {
		return `<div style="display: contents;"`
	}
	return `<div ` + scope + ` style="display: contents;"`
}
