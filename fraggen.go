package main

import (
	"fmt"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
//  FragGen — self-contained fragment code generator
//
//  A fragment is a JS function that:
//    1. Receives a parent element + optional row variables (item, index, key)
//    2. Creates its own DOM subtree
//    3. Returns a handle: { el, update(vars), destroy() }
//
//  This is used for:
//   • #each rows        →  $$frag_N(item, i)
//   • #if/#elseif/#else →  $$branch_N()
//
//  FragGen writes into its own string builders, which are then emitted as
//  inner functions inside the parent component's factory.
// ─────────────────────────────────────────────────────────────────────────────

type FragGen struct {
	name     string   // function name, e.g. "$$frag_3"
	params   []string // row-local variable names, e.g. ["item", "$$i"]
	scopeID  string
	analysis *Analysis

	// itemVar / boxVar power the row-scoped "current item" indirection.
	// At row creation the frag declares  let <boxVar> = <itemVar>;  and
	// update(<itemVar>, $$i)  refreshes  <boxVar> = <itemVar>;  at the top.
	// Event handlers captured during create close over boxVar (rewritten
	// via rewriteItemRef) so a later forEach reconciliation that calls
	// update() with a fresh item sees its click-handler reads refreshed.
	// Without this, handlers stale-close over the forEach callback param
	// and clicking a row passes the pre-update item to the handler — the
	// same bug that was fixed in the emitEachRowBody path (turn 12).
	//
	// Both fields are empty when FragGen is used outside an {#each} row
	// (e.g. fragment-capturing slot fallbacks), in which case no box is
	// emitted and handler rewriting becomes a no-op.
	itemVar string
	boxVar  string

	counter int

	// Per-fragment code sections (analogous to CodeGen but local)
	create  strings.Builder // createElement / createTextNode
	wire    strings.Builder // appendChild / insertBefore
	events  strings.Builder // addEventListener
	init    strings.Builder // initial data assignments
	update  strings.Builder // statements that run on every update(vars) call
	cleanup strings.Builder // removeEventListener, child.destroy()
}

func newFragGen(name string, params []string, scopeID string, analysis *Analysis) *FragGen {
	return &FragGen{
		name:    name,
		params:  params,
		scopeID: scopeID,
		analysis: analysis,
	}
}

func (f *FragGen) id() string {
	id := f.counter
	f.counter++
	return fmt.Sprintf("$$f%d", id)
}

// Emit produces the complete JS function definition for this fragment.
//
//   function $$frag_N(item, $$i) {
//     // create
//     const $$f0 = document.createElement('li');
//     ...
//     // wire
//     $$f0.appendChild($$f1);
//     // events
//     $$f0.addEventListener('click', handler);
//     // init
//     $$f1.data = `${item.name}`;
//     return {
//       el: $$f0,
//       update(item, $$i) { $$f1.data = `${item.name}`; },
//       destroy() { $$f0.removeEventListener('click', handler); $$f0.remove(); }
//     };
//   }
func (f *FragGen) Emit() string {
	var b strings.Builder
	params := strings.Join(f.params, ", ")
	b.WriteString(fmt.Sprintf("  function %s(%s) {\n", f.name, params))

	// Row-scoped "current item" box — see FragGen struct comment.  Declared
	// BEFORE any handler const (which may reference boxVar after rewrite) so
	// the captured closures see the correct binding.
	if f.itemVar != "" && f.boxVar != "" {
		b.WriteString(fmt.Sprintf("    let %s = %s;\n", f.boxVar, f.itemVar))
	}

	if f.create.Len() > 0 {
		b.WriteString(f.create.String())
	}
	if f.wire.Len() > 0 {
		b.WriteString(f.wire.String())
	}
	if f.events.Len() > 0 {
		b.WriteString(f.events.String())
	}
	if f.init.Len() > 0 {
		b.WriteString(f.init.String())
	}

	// Determine root element variable (always $$f0 for the first created element)
	rootVar := "$$f0"

	// update() reruns dynamic assignments with new variable values
	b.WriteString(fmt.Sprintf("    return {\n"))
	b.WriteString(fmt.Sprintf("      el: %s,\n", rootVar))
	if f.update.Len() > 0 || (f.itemVar != "" && f.boxVar != "") {
		b.WriteString(fmt.Sprintf("      update(%s) {\n", params))
		// Refresh the item box FIRST so any handler-closure read after this
		// point sees the fresh item.
		if f.itemVar != "" && f.boxVar != "" {
			b.WriteString(fmt.Sprintf("        %s = %s;\n", f.boxVar, f.itemVar))
		}
		b.WriteString(f.update.String())
		b.WriteString("      },\n")
	}
	b.WriteString("      destroy() {\n")
	if f.cleanup.Len() > 0 {
		b.WriteString(f.cleanup.String())
	}
	b.WriteString(fmt.Sprintf("        %s.remove();\n", rootVar))
	b.WriteString("      },\n")
	b.WriteString("    };\n")
	b.WriteString("  }\n")
	return b.String()
}

// ─────────────────────────────────────────────────────────────────────────────
//  Fragment walker — mirrors CodeGen's walkXxx but writes into FragGen buffers
// ─────────────────────────────────────────────────────────────────────────────

// WalkNodes walks a node list and emits into this fragment.
// rootVar is the variable name of the parent element to appendChild into.
//
// Contiguous fully-static runs (with at least one element) batch into a
// single insertAdjacentHTML call, matching the codegen.go walkChildren
// strategy.  For each-row fragments this is especially valuable because
// the savings multiply by the row count at runtime.
func (f *FragGen) WalkNodes(nodes []Node, parentVar string) {
	i := 0
	for i < len(nodes) {
		if isFullyStatic(nodes[i]) {
			j := i + 1
			for j < len(nodes) && isFullyStatic(nodes[j]) {
				j++
			}
			run := nodes[i:j]
			if hasElement(run) {
				html := emitStaticHTML(run, f.scopeID)
				f.create.WriteString(fmt.Sprintf(
					"    $$insertHTML(%s, '%s');\n",
					parentVar, escapeJSString(html)))
				i = j
				continue
			}
		}
		node := nodes[i]
		i++
		switch n := node.(type) {
		case *ElementNode:
			f.WalkElement(n, parentVar)
		case *IfChainNode:
			f.WalkIfChain(n, parentVar)
		case *ComponentNode:
			f.WalkComponent(n, parentVar)
		case *TextNode:
			f.WalkText(n, parentVar)
		}
	}
}

func (f *FragGen) WalkElement(elem *ElementNode, parentVar string) {
	if elem.EachExpr != "" {
		f.WalkEach(elem, parentVar)
		return
	}
	if elem.IfCond != "" {
		chain := &IfChainNode{Branches: []*IfBranch{{
			Cond: elem.IfCond, Children: elem.Children, Elem: elem,
		}}}
		f.WalkIfChain(chain, parentVar)
		return
	}

	v := f.id()
	f.create.WriteString(fmt.Sprintf("    const %s = document.createElement('%s');\n", v, elem.Tag))
	f.create.WriteString(fmt.Sprintf("    %s.setAttribute('%s', '');\n", v, f.scopeID))

	for _, attr := range elem.Attrs {
		switch {
		case attr.IsEvent:
			handler := attr.Value
			// Rewrite bare row-item references so the handler closes over
			// the row's mutable box instead of the stale forEach parameter.
			// No-op when f.itemVar/f.boxVar aren't set (fragment not in an
			// each-row context).
			handler = rewriteItemRef(handler, f.itemVar, f.boxVar)
			if isSimpleIdent(handler) {
				f.events.WriteString(fmt.Sprintf(
					"    %s.addEventListener('%s', %s);\n", v, attr.Name, handler))
				f.cleanup.WriteString(fmt.Sprintf(
					"        %s.removeEventListener('%s', %s);\n", v, attr.Name, handler))
			} else if isHandlerExpr(handler) {
				hv := v + "_" + attr.Name + "_h"
				f.create.WriteString(fmt.Sprintf("    const %s = %s;\n", hv, handler))
				f.events.WriteString(fmt.Sprintf(
					"    %s.addEventListener('%s', %s);\n", v, attr.Name, hv))
				f.cleanup.WriteString(fmt.Sprintf(
					"        %s.removeEventListener('%s', %s);\n", v, attr.Name, hv))
			} else {
				hv := v + "_" + attr.Name + "_h"
				f.create.WriteString(fmt.Sprintf(
					"    const %s = ($event) => { %s; };\n", hv, handler))
				f.events.WriteString(fmt.Sprintf(
					"    %s.addEventListener('%s', %s);\n", v, attr.Name, hv))
				f.cleanup.WriteString(fmt.Sprintf(
					"        %s.removeEventListener('%s', %s);\n", v, attr.Name, hv))
			}
		case attr.IsBindThis:
			// bind:this inside a fragment template (either {#each} row
			// or {#if} branch via the static-flatten path).  For each
			// rows this is intentionally skipped — see the codegen
			// each-row case for rationale.  For #if branches we
			// shouldn't even reach this path now that isFullyStatic
			// returns false for bind:this, but if we ever do, skipping
			// is safer than emitting setAttribute('this', '…') and
			// surprising the user.
			_ = attr
		case attr.IsBind:
			stmt := bindAttrStmt(v, attr.Name, attr.Value, "    ")
			if attr.IsTwoWay && attr.Name == "value" {
				stmt = fmt.Sprintf("    %s.value = (%s);", v, attr.Value)
			}
			f.init.WriteString(stmt + "\n")
			f.update.WriteString("  " + stmt + "\n")
			if attr.IsTwoWay {
				f.emitTwoWayListener(v, elem.Tag, attr)
			}
		default:
			f.create.WriteString(fmt.Sprintf(
				"    %s.setAttribute('%s', '%s');\n", v, attr.Name, escapeAttr(attr.Value)))
		}
	}

	// Static-subtree compression — mirrors the codegen.go walkElement hook.
	// Important for {#each} rows: each row instantiates the whole subtree
	// once per item, so fewer per-element DOM ops translates directly into
	// a proportional speedup as the list grows.
	if strings.ToLower(elem.Tag) != "template" &&
		len(elem.Children) > 0 &&
		hasElement(elem.Children) &&
		allStatic(elem.Children) {
		html := emitStaticHTML(elem.Children, f.scopeID)
		f.create.WriteString(fmt.Sprintf("    %s.innerHTML = '%s';\n",
			v, escapeJSString(html)))
	} else {
		f.WalkNodes(elem.Children, v)
	}
	f.wire.WriteString(fmt.Sprintf("    %s.appendChild(%s);\n", parentVar, v))
}

func (f *FragGen) WalkText(node *TextNode, parentVar string) {
	expr := buildExpr(node.Parts)
	isStatic := true
	for _, p := range node.Parts {
		if !p.Static {
			isStatic = false
			break
		}
	}
	if isStatic {
		if strings.TrimSpace(expr) == `""` || strings.TrimSpace(expr) == "``" {
			return
		}
		f.wire.WriteString(fmt.Sprintf(
			"    %s.appendChild(document.createTextNode(%s));\n", parentVar, expr))
		return
	}
	tv := f.id()
	f.create.WriteString(fmt.Sprintf("    const %s = document.createTextNode('');\n", tv))
	f.wire.WriteString(fmt.Sprintf("    %s.appendChild(%s);\n", parentVar, tv))
	f.init.WriteString(fmt.Sprintf("    %s.data = %s;\n", tv, expr))
	f.update.WriteString(fmt.Sprintf("    %s.data = %s;\n", tv, expr))
}

func (f *FragGen) WalkIfChain(chain *IfChainNode, parentVar string) {
	anchor := f.id() + "_a"
	fnName := "$$chain_" + f.id()
	elVars := make([]string, len(chain.Branches))
	for i := range chain.Branches {
		elVars[i] = fmt.Sprintf("%s_b%d", fnName, i)
	}

	f.create.WriteString(fmt.Sprintf(
		"    const %s = document.createComment('if-chain');\n", anchor))
	f.wire.WriteString(fmt.Sprintf("    %s.appendChild(%s);\n", parentVar, anchor))
	for _, ev := range elVars {
		f.create.WriteString(fmt.Sprintf("    let %s = null;\n", ev))
	}

	var fn strings.Builder
	fn.WriteString(fmt.Sprintf("    function %s() {\n", fnName))
	fn.WriteString("      let $$active = -1;\n")
	for i, branch := range chain.Branches {
		if branch.Cond == "" {
			fn.WriteString(fmt.Sprintf("      if ($$active < 0) $$active = %d;\n", i))
		} else {
			fn.WriteString(fmt.Sprintf("      if ($$active < 0 && (%s)) $$active = %d;\n", branch.Cond, i))
		}
	}
	for i, ev := range elVars {
		fn.WriteString(fmt.Sprintf(
			"      if ($$active !== %d && %s) { %s.remove(); %s = null; }\n", i, ev, ev, ev))
	}
	for i, branch := range chain.Branches {
		ev := elVars[i]
		// branch.Elem is nil for Svelte-style {#if} blocks (no wrapper element).
		// FragGen only handles simple attribute-syntax branches; use a plain div for Svelte-style.
		tag := "div"
		if branch.Elem != nil {
			tag = branch.Elem.Tag
		}
		fn.WriteString(fmt.Sprintf("      if ($$active === %d && !%s) {\n", i, ev))
		fn.WriteString(fmt.Sprintf("        %s = document.createElement('%s');\n", ev, tag))
		fn.WriteString(fmt.Sprintf("        %s.setAttribute('%s','');\n", ev, f.scopeID))
		if branch.Elem != nil {
			innerExpr := f.buildTextExpr(branch.Children)
			if innerExpr != "" {
				fn.WriteString(fmt.Sprintf("        %s.textContent = %s;\n", ev, innerExpr))
			}
		}
		fn.WriteString(fmt.Sprintf("        %s.before(%s);\n", anchor, ev))
		fn.WriteString("      }\n")
	}
	fn.WriteString("    }\n")
	f.create.WriteString(fn.String())
	f.init.WriteString(fmt.Sprintf("    %s();\n", fnName))
	f.update.WriteString(fmt.Sprintf("    %s();\n", fnName))
}

func (f *FragGen) WalkEach(elem *ElementNode, parentVar string) {
	array, itemVar, indexVar := parseEachExpr(elem.EachExpr)
	if indexVar == "" {
		indexVar = "$$i"
	}
	anchor := f.id() + "_a"
	blocks := f.id() + "_bs"
	fnName := "$$each_" + f.id()

	f.create.WriteString(fmt.Sprintf(
		"    const %s = document.createComment('#each');\n", anchor))
	f.wire.WriteString(fmt.Sprintf("    %s.appendChild(%s);\n", parentVar, anchor))
	f.create.WriteString(fmt.Sprintf("    let %s = [];\n", blocks))

	var fn strings.Builder
	fn.WriteString(fmt.Sprintf("    function %s($$arr) {\n", fnName))
	fn.WriteString(fmt.Sprintf("      %s.forEach(b => b.destroy()); %s = [];\n", blocks, blocks))
	fn.WriteString(fmt.Sprintf("      ($$arr||[]).forEach(function(%s, %s) {\n", itemVar, indexVar))
	fn.WriteString(fmt.Sprintf("        const $$el = document.createElement('%s');\n", elem.Tag))
	fn.WriteString(fmt.Sprintf("        $$el.setAttribute('%s','');\n", f.scopeID))
	// inner children
	for _, child := range elem.Children {
		f.emitInlineChild(&fn, child, "$$el", 8)
	}
	fn.WriteString(fmt.Sprintf("        %s.before($$el);\n", anchor))
	fn.WriteString(fmt.Sprintf("        %s.push({ el: $$el, destroy() { $$el.remove(); } });\n", blocks))
	fn.WriteString("      });\n    }\n")

	f.create.WriteString(fn.String())
	f.init.WriteString(fmt.Sprintf("    %s(%s);\n", fnName, array))
	f.update.WriteString(fmt.Sprintf("    %s(%s);\n", fnName, array))
}

func (f *FragGen) WalkComponent(comp *ComponentNode, parentVar string) {
	wrapVar := f.id() + "_cw"
	instVar := f.id() + "_ci"
	var props []string
	for _, attr := range comp.Attrs {
		if attr.IsBind {
			props = append(props, fmt.Sprintf("%s: %s", attr.Name, attr.Value))
		} else if !attr.IsEvent {
			props = append(props, fmt.Sprintf("%s: '%s'", attr.Name, escapeAttr(attr.Value)))
		}
	}
	propsObj := "{}"
	if len(props) > 0 {
		propsObj = "{ " + strings.Join(props, ", ") + " }"
	}
	f.create.WriteString(fmt.Sprintf("    const %s = document.createElement('div');\n", wrapVar))
	f.wire.WriteString(fmt.Sprintf("    %s.appendChild(%s);\n", parentVar, wrapVar))
	f.init.WriteString(fmt.Sprintf(
		"    const %s = %s(%s, %s);\n", instVar, comp.Name, wrapVar, propsObj))
	f.cleanup.WriteString(fmt.Sprintf("        if (%s) %s.destroy();\n", instVar, instVar))
}

// emitInlineChild emits a child node with a given indentation level
// directly into a string builder (for use inside closures).
func (f *FragGen) emitInlineChild(b *strings.Builder, node Node, parentVar string, indent int) {
	pad := strings.Repeat(" ", indent)
	switch n := node.(type) {
	case *TextNode:
		expr := buildExpr(n.Parts)
		b.WriteString(fmt.Sprintf("%s%s.appendChild(document.createTextNode(%s));\n",
			pad, parentVar, expr))
	case *ElementNode:
		sub := pad + "$$sub_" + fmt.Sprintf("%p", n)[2:8]
		b.WriteString(fmt.Sprintf("%sconst %s = document.createElement('%s');\n", pad, sub, n.Tag))
		b.WriteString(fmt.Sprintf("%s%s.setAttribute('%s','');\n", pad, sub, f.scopeID))
		for _, attr := range n.Attrs {
			if attr.IsBind {
				b.WriteString(bindAttrStmt(sub, attr.Name, attr.Value, pad) + "\n")
			} else if !attr.IsEvent {
				b.WriteString(fmt.Sprintf("%s%s.setAttribute('%s','%s');\n",
					pad, sub, attr.Name, escapeAttr(attr.Value)))
			}
		}
		for _, cc := range n.Children {
			f.emitInlineChild(b, cc, sub, indent)
		}
		b.WriteString(fmt.Sprintf("%s%s.appendChild(%s);\n", pad, parentVar, sub))
	}
}

// buildTextExpr collects all text-node expressions from children into one string.
func (f *FragGen) buildTextExpr(children []Node) string {
	var parts []*Part
	for _, child := range children {
		if t, ok := child.(*TextNode); ok {
			parts = append(parts, t.Parts...)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return buildExpr(parts)
}

// emitTwoWayListener wires the event listener that powers the "write back
// to state" half of a bind: attribute, inside a fragment.
//
// Same semantics as (*CodeGen).emitTwoWayListener but uses FragGen's own
// buffers — handler declared in .create (alongside the element), listener
// registered in .events, removal in .cleanup so the fragment's destroy()
// can remove it explicitly.  (A FragGen row tears down via destroy() on
// the row handle, which is why we emit the cleanup; this differs from the
// inline-each case where the row's $$row.remove() is all that runs.)
//
// Supported combinations and restrictions mirror twoWayBindSpec; non-state
// RHS values silently fall back to one-way (the .value/.checked write has
// already been emitted by the caller).
func (f *FragGen) emitTwoWayListener(v, tag string, attr *Attr) {
	spec, ok := twoWayBindSpec(tag, attr.Name)
	if !ok {
		return
	}
	if !f.analysis.isStateName(attr.Value) {
		return
	}
	sv := attr.Value
	hv := v + "_bind_" + spec.event + "_h"
	f.create.WriteString(fmt.Sprintf(
		"    const %s = ($e) => $$set_%s($e.target.%s);\n",
		hv, sv, spec.readProp))
	f.events.WriteString(fmt.Sprintf(
		"    %s.addEventListener('%s', %s);\n",
		v, spec.event, hv))
	f.cleanup.WriteString(fmt.Sprintf(
		"        %s.removeEventListener('%s', %s);\n",
		v, spec.event, hv))
}
