package main

import (
	"fmt"
	goast "go/ast"
	"go/token"
	"strconv"
	"strings"
)

// Server IR (docs/SSR_TRANSLATION_CONTRACT.md §3, task 043).
//
// buildSSRIR makes every decision about a server-evaluated JavaScript
// expression: which construct it is, how it is lowered, and what is known
// about its type. lowerSSRIR only consumes the result and cannot fail or
// classify (T-3.1). The type facts reproduce the rules of the translator
// that preceded the IR, so the generated Go is unchanged (S1 parity); the
// dynamic tier of task 044 widens them.

// SSRIRExpr is one node of the Server IR.
type SSRIRExpr interface {
	irBase() *ssrIRBase
}

type ssrIRBase struct {
	// Pos is the source offset of the region site the node belongs to.
	Pos   int
	Facts ssrIRFacts
}

func (b *ssrIRBase) irBase() *ssrIRBase { return b }

// ssrIRFacts is what is proven about a value's JavaScript type. Each fact
// follows its own established rule, so several can be false for a value
// whose type is nevertheless obvious (a call result is never "known").
type ssrIRFacts struct {
	String, Number, Array, Object bool
	// Bool means the lowered Go expression has type bool, so a condition
	// needs no truthiness conversion.
	Bool bool
	// GoString means the lowered Go expression has type string although
	// no JavaScript fact is recorded (an await snapshot value). It only
	// lets a typed child prop take the value directly.
	GoString bool
}

// SemanticType names the type the facts prove (§5.2), or "unknown".
func (f ssrIRFacts) SemanticType() string {
	switch {
	case f.String:
		return "string"
	case f.Number:
		return "number"
	case f.Bool:
		return "boolean"
	case f.Array:
		return "array"
	case f.Object:
		return "object"
	}
	return "unknown"
}

// Tier is "typed" when the value has a proven type and "dynamic" when it is
// handled by JavaScript-semantics runtime helpers (§5.4).
func (f ssrIRFacts) Tier() string {
	if f.SemanticType() == "unknown" {
		return "dynamic"
	}
	return "typed"
}

type (
	// ssrIRGo is a value the renderer already holds as Go: a row item, an
	// await value, or a function argument lowered once per use.
	ssrIRGo struct {
		ssrIRBase
		Expr goast.Expr
	}
	ssrIRUndefined struct{ ssrIRBase }
	ssrIRProp      struct {
		ssrIRBase
		GoName string
	}
	ssrIRLiteral struct {
		ssrIRBase
		Kind  token.Token // STRING, FLOAT, or IDENT (true, false, nil)
		Value string      // decoded string, number spelling, or identifier
	}
	ssrIRArray struct {
		ssrIRBase
		Elems  []SSRIRExpr // nil element is a hole
		Spread []bool      // Spread[i]: Elems[i] is ...element
		Site   string
	}
	ssrIRObject struct {
		ssrIRBase
		Keys   []string // "" with Spread
		Values []SSRIRExpr
		Spread []bool
	}
	ssrIRTemplate struct {
		ssrIRBase
		Quasis []string
		Exprs  []SSRIRExpr
	}
	// ssrIRHelper calls a runtime helper vela.Name(args...), negated with !
	// when Not is set.
	ssrIRHelper struct {
		ssrIRBase
		Name string
		Args []SSRIRExpr
		Not  bool
	}
	// ssrIRLazy calls a short-circuit helper whose arguments are closures.
	ssrIRLazy struct {
		ssrIRBase
		Name string
		Args []SSRIRExpr
	}
	ssrIRBinary struct {
		ssrIRBase
		Op   token.Token
		X, Y SSRIRExpr
	}
	// ssrIRJS calls an operation of the render's evaluation state that can
	// throw (ssrJS.Get, Index, Length): Args, then the literal Strings, then
	// the site. Without an evaluation state it calls the package helper,
	// which does not throw.
	ssrIRJS struct {
		ssrIRBase
		Method  string
		Args    []SSRIRExpr
		Strings []string
		Site    string
		NoJS    bool
	}
	// ssrIROptional is an optional chain link: Rest reads Param, the
	// non-nullish Base.
	ssrIROptional struct {
		ssrIRBase
		Base  SSRIRExpr
		Param string
		Rest  SSRIRExpr
	}
	// ssrIRInline is a local binding initializer or a function body inlined
	// at its use; Name and Kind describe it for diagnostics and the report.
	ssrIRInline struct {
		ssrIRBase
		Kind, Name string
		Body       SSRIRExpr
	}
)

// lowerSSRIR writes the Go expression of an IR node.
func lowerSSRIR(node SSRIRExpr) goast.Expr {
	switch n := node.(type) {
	case *ssrIRGo:
		return n.Expr
	case *ssrIRUndefined:
		return &goast.CallExpr{Fun: goSelector("vela", "Undefined")}
	case *ssrIRProp:
		return &goast.SelectorExpr{X: goast.NewIdent("props"), Sel: goast.NewIdent(n.GoName)}
	case *ssrIRLiteral:
		switch n.Kind {
		case token.STRING:
			return &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(n.Value)}
		case token.FLOAT:
			return &goast.BasicLit{Kind: token.FLOAT, Value: n.Value}
		default:
			return goast.NewIdent(n.Value)
		}
	case *ssrIRArray:
		literal := func(elements []goast.Expr) goast.Expr {
			return &goast.CompositeLit{Type: &goast.ArrayType{Elt: goast.NewIdent("any")}, Elts: elements}
		}
		var parts []goast.Expr
		var segment []goast.Expr
		for i, element := range n.Elems {
			switch {
			case element == nil:
				segment = append(segment, goast.NewIdent("nil"))
			case i < len(n.Spread) && n.Spread[i]:
				if len(segment) > 0 {
					parts = append(parts, literal(segment))
					segment = nil
				}
				parts = append(parts, &goast.CallExpr{Fun: goSelector("ssrJS", "SpreadItems"), Args: []goast.Expr{lowerSSRIR(element), ssrGoString(n.Site)}})
			default:
				segment = append(segment, lowerSSRIR(element))
			}
		}
		if parts == nil {
			return literal(segment)
		}
		if len(segment) > 0 {
			parts = append(parts, literal(segment))
		}
		return &goast.CallExpr{Fun: goSelector("vela", "JoinArrays"), Args: parts}
	case *ssrIRObject:
		object := goast.Expr(&goast.CallExpr{Fun: goSelector("vela", "NewObject")})
		for i, value := range n.Values {
			if n.Spread[i] {
				object = &goast.CallExpr{Fun: goSelectorExpr(object, "Spread"), Args: []goast.Expr{lowerSSRIR(value)}}
				continue
			}
			object = &goast.CallExpr{Fun: goSelectorExpr(object, "Set"), Args: []goast.Expr{ssrGoString(n.Keys[i]), lowerSSRIR(value)}}
		}
		return object
	case *ssrIRTemplate:
		values := make([]goast.Expr, 0, len(n.Quasis)+len(n.Exprs))
		for i, quasi := range n.Quasis {
			values = append(values, &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(quasi)})
			if i < len(n.Exprs) {
				values = append(values, lowerSSRIR(n.Exprs[i]))
			}
		}
		if len(n.Exprs) == 0 {
			return values[0]
		}
		return &goast.CallExpr{Fun: goSelector("vela", "TemplateString"), Args: values}
	case *ssrIRHelper:
		args := make([]goast.Expr, len(n.Args))
		for i, arg := range n.Args {
			args[i] = lowerSSRIR(arg)
		}
		call := goast.Expr(&goast.CallExpr{Fun: goSelector("vela", n.Name), Args: args})
		if n.Not {
			call = &goast.UnaryExpr{Op: token.NOT, X: call}
		}
		return call
	case *ssrIRLazy:
		args := make([]goast.Expr, len(n.Args))
		for i, arg := range n.Args {
			args[i] = ssrLazyExpr(lowerSSRIR(arg))
		}
		return &goast.CallExpr{Fun: goSelector("vela", n.Name), Args: args}
	case *ssrIRBinary:
		return &goast.BinaryExpr{X: lowerSSRIR(n.X), Op: n.Op, Y: lowerSSRIR(n.Y)}
	case *ssrIRJS:
		args := make([]goast.Expr, 0, len(n.Args)+len(n.Strings)+1)
		for _, arg := range n.Args {
			args = append(args, lowerSSRIR(arg))
		}
		for _, text := range n.Strings {
			args = append(args, ssrGoString(text))
		}
		if n.NoJS {
			return &goast.CallExpr{Fun: goSelector("vela", n.Method), Args: args}
		}
		args = append(args, ssrGoString(n.Site))
		return &goast.CallExpr{Fun: goSelector("ssrJS", n.Method), Args: args}
	case *ssrIROptional:
		rest := &goast.FuncLit{
			Type: &goast.FuncType{
				Params:  &goast.FieldList{List: []*goast.Field{{Names: []*goast.Ident{goast.NewIdent(n.Param)}, Type: goast.NewIdent("any")}}},
				Results: &goast.FieldList{List: []*goast.Field{{Type: goast.NewIdent("any")}}},
			},
			Body: &goast.BlockStmt{List: []goast.Stmt{&goast.ReturnStmt{Results: []goast.Expr{lowerSSRIR(n.Rest)}}}},
		}
		return &goast.CallExpr{Fun: goSelector("vela", "Optional"), Args: []goast.Expr{lowerSSRIR(n.Base), rest}}
	case *ssrIRInline:
		return lowerSSRIR(n.Body)
	}
	panic(fmt.Sprintf("vela: unknown server IR node %T", node))
}

// ssrIRBuilder carries the lexical state of one IR construction: the
// function parameters in scope and the bindings and functions being
// inlined, which detect cycles.
type ssrIRBuilder struct {
	ctx       SSRExprContext
	params    map[string]SSRIRExpr
	resolving map[string]bool
	functions map[string]bool
	// inlined collects the local bindings the expression reads, for the
	// report (T-2.3).
	inlined map[string]bool
	// js is set when the renderer has an evaluation state (ssrJS): member
	// reads then throw TypeError at site. throws records that the site
	// contains an operation that can throw (T-6.5e).
	js        bool
	site      string
	throws    bool
	optionals int
	// usedJS records that the lowering references ssrJS.
	usedJS bool
	// locals makes local bindings read their Go local ssrL_<name>, which the
	// render prologue evaluates once in declaration order (T-6.6d), instead
	// of inlining the initializer at every read. reads collects the locals
	// read directly; current is the local whose initializer is being built.
	locals  bool
	reads   map[string]bool
	current string
}

// BuildSSRIR builds the IR of one template expression.
func BuildSSRIR(expr *TemplateExpr, ctx SSRExprContext) (SSRIRExpr, error) {
	if expr == nil || expr.Root() == nil {
		return nil, &SSRExpressionError{Message: "template expression has no cached AST"}
	}
	return buildSSRIR(expr.Root(), ctx, expr.Pos)
}

func buildSSRIR(expr JsExpr, ctx SSRExprContext, pos int) (SSRIRExpr, error) {
	return newSSRIRBuilder(ctx).build(expr, pos)
}

func newSSRIRBuilder(ctx SSRExprContext) *ssrIRBuilder {
	return &ssrIRBuilder{ctx: ctx, resolving: map[string]bool{}, functions: map[string]bool{}, inlined: map[string]bool{}}
}

func (b *ssrIRBuilder) base(pos int, facts ssrIRFacts) ssrIRBase {
	return ssrIRBase{Pos: pos, Facts: facts}
}

func (b *ssrIRBuilder) build(expr JsExpr, pos int) (SSRIRExpr, error) {
	switch node := expr.(type) {
	case *JsIdent:
		return b.ident(node, pos)
	case *JsLiteral:
		return b.literal(node, pos)
	case *JsArrayExpr:
		elements := make([]SSRIRExpr, 0, len(node.Elems))
		var spreads []bool
		for _, element := range node.Elems {
			if element == nil {
				elements = append(elements, nil)
				continue
			}
			if spread, ok := element.(*JsSpreadExpr); ok {
				if !b.js {
					return nil, ssrExprUnsupported(pos, "array spread is not available to static SSR")
				}
				value, err := b.build(spread.X, pos)
				if err != nil {
					return nil, err
				}
				b.usedJS = true
				// Spreading a non-iterable throws TypeError.
				if f := value.irBase().Facts; !f.Array && !f.String {
					b.throws = true
				}
				for len(spreads) < len(elements) {
					spreads = append(spreads, false)
				}
				elements = append(elements, value)
				spreads = append(spreads, true)
				continue
			}
			value, err := b.build(element, pos)
			if err != nil {
				return nil, err
			}
			elements = append(elements, value)
		}
		for spreads != nil && len(spreads) < len(elements) {
			spreads = append(spreads, false)
		}
		return &ssrIRArray{ssrIRBase: b.base(pos, ssrIRFacts{Array: true}), Elems: elements, Spread: spreads, Site: b.site}, nil
	case *JsObjectExpr:
		return b.object(node, pos)
	case *JsTemplateLit:
		if len(node.Quasis) != len(node.Exprs)+1 {
			return nil, ssrExprUnsupported(pos, "template literal has an invalid parser structure")
		}
		out := &ssrIRTemplate{ssrIRBase: b.base(pos, ssrIRFacts{String: true})}
		for index, quasi := range node.Quasis {
			text, err := decodeSSRTemplateQuasi(quasi)
			if err != nil {
				return nil, ssrExprUnsupported(pos, "template literal cannot be translated safely")
			}
			out.Quasis = append(out.Quasis, text)
			if index == len(node.Exprs) {
				continue
			}
			value, err := b.build(node.Exprs[index], pos)
			if err != nil {
				return nil, err
			}
			out.Exprs = append(out.Exprs, value)
		}
		return out, nil
	case *JsUnaryExpr:
		if node.Op == "++" || node.Op == "--" {
			return nil, ssrImpure(pos, "update operator %q writes a binding during server rendering", node.Op)
		}
		x, err := b.build(node.X, pos)
		if err != nil {
			return nil, err
		}
		// JavaScript's ! accepts every value and first applies JS truthiness.
		if node.Op == "!" {
			return &ssrIRHelper{ssrIRBase: b.base(pos, ssrIRFacts{Bool: true}), Name: "Truthy", Args: []SSRIRExpr{x}, Not: true}, nil
		}
		if node.Op == "typeof" {
			return &ssrIRHelper{ssrIRBase: b.base(pos, ssrIRFacts{String: true}), Name: "TypeOf", Args: []SSRIRExpr{x}}, nil
		}
		// Number and NumberNegate apply ToNumber, so both tiers share them.
		if node.Op == "+" || node.Op == "-" {
			name := "Number"
			if node.Op == "-" {
				name = "NumberNegate"
			}
			return &ssrIRHelper{ssrIRBase: b.base(pos, ssrIRFacts{Number: true}), Name: name, Args: []SSRIRExpr{x}}, nil
		}
		return nil, ssrExprUnsupported(pos, "unary operator %q is not supported", node.Op)
	case *JsBinaryExpr:
		return b.binary(node, pos)
	case *JsLogicalExpr:
		left, err := b.build(node.L, pos)
		if err != nil {
			return nil, err
		}
		right, err := b.build(node.R, pos)
		if err != nil {
			return nil, err
		}
		name := ""
		switch node.Op {
		case "&&":
			name = "AndLazy"
		case "||":
			name = "OrLazy"
		case "??":
			name = "NullishLazy"
		default:
			return nil, ssrExprUnsupported(pos, "logical operator %q is not supported", node.Op)
		}
		return &ssrIRLazy{ssrIRBase: b.base(pos, ssrIRFacts{}), Name: name, Args: []SSRIRExpr{left, right}}, nil
	case *JsMemberExpr:
		return b.member(node, pos)
	case *JsConditionalExpr:
		test, err := b.build(node.Test, pos)
		if err != nil {
			return nil, err
		}
		thenValue, err := b.build(node.Then, pos)
		if err != nil {
			return nil, err
		}
		elseValue, err := b.build(node.Else, pos)
		if err != nil {
			return nil, err
		}
		t, e := thenValue.irBase().Facts, elseValue.irBase().Facts
		facts := ssrIRFacts{String: t.String && e.String, Number: t.Number && e.Number, Array: t.Array && e.Array, Object: t.Object && e.Object}
		return &ssrIRLazy{ssrIRBase: b.base(pos, facts), Name: "TernaryLazy", Args: []SSRIRExpr{test, thenValue, elseValue}}, nil
	case *JsCallExpr:
		return b.call(node, pos)
	case *JsArrowFunc:
		return nil, ssrExprUnsupported(pos, "arrow functions are not available to static SSR")
	case *JsAssignExpr:
		return nil, ssrImpure(pos, "assignments are not available to static SSR")
	default:
		return nil, ssrExprUnsupported(pos, "JavaScript expression %T is not supported", expr)
	}
}

func (b *ssrIRBuilder) ident(node *JsIdent, pos int) (SSRIRExpr, error) {
	// Bool follows the established condition rule: only a declared bool
	// prop of that name, whatever shadows it.
	boolProp := false
	if prop, ok := b.ctx.Props[node.Name]; ok && prop.Type == SSRTypeBool {
		boolProp = true
	}
	if node.Name == "undefined" {
		return &ssrIRUndefined{ssrIRBase: b.base(pos, ssrIRFacts{Bool: boolProp})}, nil
	}
	if param, ok := b.params[node.Name]; ok {
		// A parameter is the lowered argument; what is known about it is
		// what its Go form shows (parity with the inliner).
		value := lowerSSRIR(param)
		facts := ssrGoExprFacts(value, b.ctx)
		facts.Bool = boolProp
		return &ssrIRGo{ssrIRBase: b.base(pos, facts), Expr: value}, nil
	}
	if binding, ok := b.ctx.Bindings[node.Name]; ok {
		facts := ssrGoExprFacts(binding, b.ctx)
		facts.GoString = ssrAwaitSnapshotField(binding)
		facts.Bool = boolProp
		return &ssrIRGo{ssrIRBase: b.base(pos, facts), Expr: binding}, nil
	}
	if prop, ok := b.ctx.Props[node.Name]; ok {
		facts := ssrIRFacts{
			String: prop.Type == SSRTypeString,
			Number: prop.Type == SSRTypeInt || prop.Type == SSRTypeFloat,
			Array:  prop.Type == SSRTypeSlice,
			Object: prop.Type == SSRTypeObject,
			Bool:   prop.Type == SSRTypeBool,
		}
		return &ssrIRProp{ssrIRBase: b.base(pos, facts), GoName: prop.GoName}, nil
	}
	if b.locals {
		for _, local := range []struct {
			kind         string
			initializers map[string]JsExpr
		}{{"reactive", b.ctx.StateInitializers}, {"reactive", b.ctx.DeepInitializers}, {"derived", b.ctx.DerivedInitializers}, {"plain", b.ctx.PlainInitializers}} {
			if initializer, ok := local.initializers[node.Name]; ok {
				return b.localRef(node.Name, local.kind, initializer, pos, boolProp)
			}
		}
	}
	for _, local := range []struct {
		kind         string
		initializers map[string]JsExpr
	}{{"reactive", b.ctx.StateInitializers}, {"derived", b.ctx.DerivedInitializers}, {"plain", b.ctx.PlainInitializers}} {
		if initializer, ok := local.initializers[node.Name]; ok {
			return b.local(node.Name, local.kind, initializer, pos, boolProp)
		}
	}
	if b.ctx.ReactiveNames[node.Name] {
		return nil, ssrExprUnsupported(pos, "local reactive binding %q has no server value; pass it as an exported prop or wait for hydration", node.Name)
	}
	return nil, ssrExprUnsupported(pos, "identifier %q is not an exported prop or server binding", node.Name)
}

// local inlines a $state, $derived, or plain binding initializer.
func (b *ssrIRBuilder) local(name, kind string, initializer JsExpr, pos int, boolProp bool) (SSRIRExpr, error) {
	if b.resolving[name] {
		return nil, ssrExprUnsupported(pos, "local %s binding %q has a circular server initializer", kind, name)
	}
	b.resolving[name] = true
	defer delete(b.resolving, name)
	b.inlined[name] = true
	value, err := b.build(initializer, pos)
	if err != nil {
		b.locate(err, initializer)
		wrapped := ssrExprUnsupported(pos, "local %s binding %q has no server-safe initializer: %v", kind, name, err)
		return nil, ssrWithReason(wrapped, err, ssrLocalReason(kind, name))
	}
	facts := value.irBase().Facts
	facts.Bool = boolProp
	return &ssrIRInline{ssrIRBase: b.base(pos, facts), Kind: kind, Name: name, Body: value}, nil
}

func ssrLocalReason(kind, name string) string {
	switch kind {
	case "reactive":
		return fmt.Sprintf("$state %q", name)
	case "derived":
		return fmt.Sprintf("$derived %q", name)
	}
	return fmt.Sprintf("binding %q", name)
}

func (b *ssrIRBuilder) literal(node *JsLiteral, pos int) (SSRIRExpr, error) {
	switch node.Kind {
	case "string":
		value := node.Value
		if !node.Decoded {
			decoded, err := decodeSSRJSStringLiteral(node.Raw)
			if err != nil {
				return nil, ssrExprUnsupported(pos, "string literal cannot be translated safely")
			}
			value = decoded
		}
		return &ssrIRLiteral{ssrIRBase: b.base(pos, ssrIRFacts{String: true}), Kind: token.STRING, Value: value}, nil
	case "number":
		value, ok := ssrJSNumberLiteral(node.Raw)
		if !ok {
			return nil, ssrExprUnsupported(pos, "number literal %s is not supported", node.Raw)
		}
		return &ssrIRLiteral{ssrIRBase: b.base(pos, ssrIRFacts{Number: true}), Kind: token.FLOAT, Value: value}, nil
	case "boolean":
		return &ssrIRLiteral{ssrIRBase: b.base(pos, ssrIRFacts{Bool: true}), Kind: token.IDENT, Value: node.Raw}, nil
	case "null":
		return &ssrIRLiteral{ssrIRBase: b.base(pos, ssrIRFacts{}), Kind: token.IDENT, Value: "nil"}, nil
	}
	return nil, ssrExprUnsupported(pos, "literal kind %q is not supported", node.Kind)
}

// object admits data properties with identifier or string keys and spread
// properties. Every value is evaluated in source order; a later duplicate
// key replaces the value and keeps the first position, as in JavaScript.
func (b *ssrIRBuilder) object(node *JsObjectExpr, pos int) (SSRIRExpr, error) {
	out := &ssrIRObject{ssrIRBase: b.base(pos, ssrIRFacts{Object: true})}
	for _, property := range node.Props {
		if property == nil {
			return nil, ssrExprUnsupported(pos, "object property is missing")
		}
		if property.Spread {
			source := property.Value
			if source == nil {
				source = property.Key
			}
			value, err := b.build(source, pos)
			if err != nil {
				return nil, err
			}
			out.Keys = append(out.Keys, "")
			out.Values = append(out.Values, value)
			out.Spread = append(out.Spread, true)
			continue
		}
		if property.Computed {
			return nil, ssrExprUnsupported(pos, "computed object property names are not available to static SSR")
		}
		if property.Method || property.Kind != "init" {
			return nil, ssrExprUnsupported(pos, "object methods and accessors are not available to static SSR")
		}
		key, err := translateSSRObjectKey(property.Key, pos)
		if err != nil {
			return nil, err
		}
		if key == "__proto__" {
			return nil, ssrExprUnsupported(pos, "the __proto__ object property is not available to static SSR")
		}
		value, err := b.build(property.Value, pos)
		if err != nil {
			return nil, err
		}
		out.Keys = append(out.Keys, key)
		out.Values = append(out.Values, value)
		out.Spread = append(out.Spread, false)
	}
	return out, nil
}

func (b *ssrIRBuilder) binary(node *JsBinaryExpr, pos int) (SSRIRExpr, error) {
	left, err := b.build(node.L, pos)
	if err != nil {
		return nil, err
	}
	right, err := b.build(node.R, pos)
	if err != nil {
		return nil, err
	}
	l, r := left.irBase().Facts, right.irBase().Facts
	switch node.Op {
	case "==", "!=":
		// Only x == null and x != null are admitted (§6.2).
		var operand SSRIRExpr
		switch {
		case ssrNullishLiteral(node.R):
			operand = left
		case ssrNullishLiteral(node.L):
			operand = right
		default:
			return nil, ssrExprUnsupported(pos, "loose equality operator %q is admitted only against null or undefined; use strict equality", node.Op)
		}
		return &ssrIRHelper{ssrIRBase: b.base(pos, ssrIRFacts{Bool: true}), Name: "LooseNull", Args: []SSRIRExpr{operand}, Not: node.Op == "!="}, nil
	case "===", "!==":
		return &ssrIRHelper{ssrIRBase: b.base(pos, ssrIRFacts{Bool: true}), Name: "StrictEqual", Args: []SSRIRExpr{left, right}, Not: node.Op == "!=="}, nil
	case "<", "<=", ">", ">=":
		facts := ssrIRFacts{Bool: true}
		if l.String && r.String {
			// a > b is b < a; a <= b is !(b < a); a >= b is !(a < b).
			switch node.Op {
			case "<":
				return &ssrIRHelper{ssrIRBase: b.base(pos, facts), Name: "StringLess", Args: []SSRIRExpr{left, right}}, nil
			case ">":
				return &ssrIRHelper{ssrIRBase: b.base(pos, facts), Name: "StringLess", Args: []SSRIRExpr{right, left}}, nil
			case "<=":
				return &ssrIRHelper{ssrIRBase: b.base(pos, facts), Name: "StringLess", Args: []SSRIRExpr{right, left}, Not: true}, nil
			default:
				return &ssrIRHelper{ssrIRBase: b.base(pos, facts), Name: "StringLess", Args: []SSRIRExpr{left, right}, Not: true}, nil
			}
		}
		if l.Number && r.Number {
			op, _ := ssrBinaryToken(node.Op)
			return &ssrIRBinary{ssrIRBase: b.base(pos, facts), Op: op,
				X: &ssrIRHelper{ssrIRBase: b.base(pos, ssrIRFacts{Number: true}), Name: "Number", Args: []SSRIRExpr{left}},
				Y: &ssrIRHelper{ssrIRBase: b.base(pos, ssrIRFacts{Number: true}), Name: "Number", Args: []SSRIRExpr{right}},
			}, nil
		}
		// Dynamic tier: the abstract relational comparison.
		name := map[string]string{"<": "Less", "<=": "LessEqual", ">": "Greater", ">=": "GreaterEqual"}[node.Op]
		return &ssrIRHelper{ssrIRBase: b.base(pos, facts), Name: name, Args: []SSRIRExpr{left, right}}, nil
	case "+":
		// JavaScript + concatenates when either operand is a string.
		switch {
		case l.String || r.String:
			if l.String != r.String {
				left = &ssrIRHelper{ssrIRBase: b.base(pos, ssrIRFacts{}), Name: "String", Args: []SSRIRExpr{left}}
				right = &ssrIRHelper{ssrIRBase: b.base(pos, ssrIRFacts{}), Name: "String", Args: []SSRIRExpr{right}}
			}
			return &ssrIRBinary{ssrIRBase: b.base(pos, ssrIRFacts{String: true}), Op: token.ADD, X: left, Y: right}, nil
		case l.Number && r.Number:
			return &ssrIRHelper{ssrIRBase: b.base(pos, ssrIRFacts{Number: true}), Name: "NumberAdd", Args: []SSRIRExpr{left, right}}, nil
		}
		// Dynamic tier: ToPrimitive, then concatenation or addition.
		return &ssrIRHelper{ssrIRBase: b.base(pos, ssrIRFacts{}), Name: "Add", Args: []SSRIRExpr{left, right}}, nil
	case "-", "*", "/", "%":
		// The Number helpers apply ToNumber, so both tiers share them.
		name := map[string]string{"-": "NumberSubtract", "*": "NumberMultiply", "/": "NumberDivide", "%": "NumberRemainder"}[node.Op]
		return &ssrIRHelper{ssrIRBase: b.base(pos, ssrIRFacts{Number: true}), Name: name, Args: []SSRIRExpr{left, right}}, nil
	case "**":
		return &ssrIRHelper{ssrIRBase: b.base(pos, ssrIRFacts{Number: true}), Name: "Pow", Args: []SSRIRExpr{left, right}}, nil
	}
	return nil, ssrExprUnsupported(pos, "binary operator %q is not supported", node.Op)
}

// ssrNullishLiteral reports null or undefined written in the source.
func ssrNullishLiteral(expr JsExpr) bool {
	switch node := expr.(type) {
	case *JsLiteral:
		return node.Kind == "null"
	case *JsIdent:
		return node.Name == "undefined"
	}
	return false
}

// member builds a member chain a.b[c]?.d: the root, then each link. An
// optional link (?.) stops the rest of the chain when its object is null or
// undefined (T-6.1b).
func (b *ssrIRBuilder) member(node *JsMemberExpr, pos int) (SSRIRExpr, error) {
	var links []*JsMemberExpr
	var root JsExpr = node
	for {
		link, ok := root.(*JsMemberExpr)
		if !ok {
			break
		}
		links = append([]*JsMemberExpr{link}, links...)
		root = link.Obj
	}
	base, err := b.build(root, pos)
	if err != nil {
		return nil, err
	}
	return b.memberLinks(base, links, false, pos)
}

// memberLinks applies links to base. When entered is set, the first link is
// the optional link whose nullish check has already passed.
func (b *ssrIRBuilder) memberLinks(base SSRIRExpr, links []*JsMemberExpr, entered bool, pos int) (SSRIRExpr, error) {
	for i, link := range links {
		if link.Optional && !(entered && i == 0) {
			param := "ssrOpt" + strconv.Itoa(b.optionals)
			b.optionals++
			inner := &ssrIRGo{ssrIRBase: b.base(pos, ssrIRFacts{}), Expr: goast.NewIdent(param)}
			rest, err := b.memberLinks(inner, links[i:], true, pos)
			if err != nil {
				return nil, err
			}
			return &ssrIROptional{ssrIRBase: b.base(pos, ssrIRFacts{}), Base: base, Param: param, Rest: rest}, nil
		}
		next, err := b.memberLink(base, link, entered && i == 0, pos)
		if err != nil {
			return nil, err
		}
		base = next
	}
	return base, nil
}

// memberLink reads one property. A read on a value that is not proven to
// be an object, array, or string can throw TypeError (T-6.5a).
func (b *ssrIRBuilder) memberLink(object SSRIRExpr, link *JsMemberExpr, nonNullish bool, pos int) (SSRIRExpr, error) {
	b.usedJS = b.usedJS || b.js
	o := object.irBase().Facts
	if !nonNullish && !o.String && !o.Array && !o.Object {
		b.throws = b.throws || b.js
	}
	if !link.Computed {
		property, ok := link.Prop.(*JsIdent)
		if !ok {
			return nil, ssrExprUnsupported(pos, "member name %T is not supported (private names are not part of the server region)", link.Prop)
		}
		if property.Name == "length" {
			facts := ssrIRFacts{Number: o.String || o.Array}
			return &ssrIRJS{ssrIRBase: b.base(pos, facts), Method: "Length", Args: []SSRIRExpr{object}, Site: b.site, NoJS: !b.js}, nil
		}
		return &ssrIRJS{ssrIRBase: b.base(pos, ssrIRFacts{}), Method: "Get", Args: []SSRIRExpr{object}, Strings: []string{property.Name}, Site: b.site, NoJS: !b.js}, nil
	}
	index, err := b.build(link.Prop, pos)
	if err != nil {
		return nil, err
	}
	return &ssrIRJS{ssrIRBase: b.base(pos, ssrIRFacts{}), Method: "Index", Args: []SSRIRExpr{object, index}, Site: b.site, NoJS: !b.js}, nil
}

// call inlines a top-level named, synchronous function with one return
// expression and identifier parameters. Arguments are built in the
// caller's scope; the body sees them as parameters.
func (b *ssrIRBuilder) call(call *JsCallExpr, pos int) (SSRIRExpr, error) {
	if call.Optional {
		return nil, ssrExprUnsupported(pos, "optional JavaScript calls are not available to static SSR")
	}
	callee, ok := call.Callee.(*JsIdent)
	if !ok {
		return nil, ssrExprUnsupported(pos, "only named local formatter calls are available to static SSR")
	}
	function, ok := b.ctx.Functions[callee.Name]
	if !ok || function == nil {
		return nil, ssrExprUnsupported(pos, "JavaScript call %q is not a supported local formatter", callee.Name)
	}
	reason := fmt.Sprintf("call %q", callee.Name)
	fail := func(format string, args ...interface{}) (SSRIRExpr, error) {
		problem := ssrExprUnsupported(pos, format, args...)
		b.locate(problem, function)
		return nil, ssrWithReason(problem, nil, reason)
	}
	if function.Async || function.Generator {
		return fail("local formatter %q cannot be async or a generator during SSR", callee.Name)
	}
	if len(function.Params) != len(call.Args) {
		return fail("local formatter %q expects %d argument(s), got %d", callee.Name, len(function.Params), len(call.Args))
	}
	if function.Body == nil || len(function.Body.Body) != 1 {
		return fail("local formatter %q must contain exactly one return expression", callee.Name)
	}
	result, ok := function.Body.Body[0].(*JsReturnStmt)
	if !ok || result.Arg == nil {
		return fail("local formatter %q must contain exactly one return expression", callee.Name)
	}
	if b.functions[callee.Name] {
		return fail("local formatter %q is recursively referenced during SSR", callee.Name)
	}
	arguments := make([]SSRIRExpr, len(call.Args))
	for index, argument := range call.Args {
		value, err := b.build(argument, pos)
		if err != nil {
			return nil, err
		}
		arguments[index] = value
	}
	params := make(map[string]SSRIRExpr, len(b.params)+len(function.Params))
	for name, value := range b.params {
		params[name] = value
	}
	for index, parameter := range function.Params {
		name := identName(parameter)
		if name == "" {
			return fail("local formatter %q has a non-identifier parameter", callee.Name)
		}
		params[name] = arguments[index]
	}
	outer := b.params
	b.params = params
	b.functions[callee.Name] = true
	value, err := b.build(result.Arg, pos)
	delete(b.functions, callee.Name)
	b.params = outer
	if err != nil {
		b.locate(err, result.Arg)
		return nil, ssrWithReason(err, err, reason)
	}
	// What is known about a call result is not tracked (parity).
	return &ssrIRInline{ssrIRBase: b.base(pos, ssrIRFacts{}), Kind: "call", Name: callee.Name, Body: value}, nil
}

// ssrGoExprFacts reads what a Go expression the renderer holds proves about
// its type.
func ssrGoExprFacts(expr goast.Expr, ctx SSRExprContext) ssrIRFacts {
	facts := ssrIRFacts{String: ssrGoExprKnownString(expr, ctx), Number: ssrGoExprKnownNumber(expr, ctx)}
	if literal, ok := expr.(*goast.CompositeLit); ok {
		_, facts.Array = literal.Type.(*goast.ArrayType)
		_, facts.Object = literal.Type.(*goast.MapType)
	}
	return facts
}

// locate marks err with the source position of node in the component
// script, unless a deeper position is already set.
func (b *ssrIRBuilder) locate(err error, node JsNode) {
	problem, ok := err.(*SSRExpressionError)
	if !ok || problem.SourceSet || b.ctx.scriptOffset <= 0 || node == nil {
		return
	}
	problem.Source, problem.SourceSet = b.ctx.scriptOffset+node.NodePos(), true
}

// ssrAwaitSnapshotField reports a settled await branch binding: the Value
// or Error string of an await snapshot.
func ssrAwaitSnapshotField(expr goast.Expr) bool {
	selector, ok := expr.(*goast.SelectorExpr)
	if !ok || (selector.Sel.Name != "Value" && selector.Sel.Name != "Error") {
		return false
	}
	base, ok := selector.X.(*goast.Ident)
	return ok && strings.HasPrefix(base.Name, "ssrAwaitSnapshot")
}

func ssrGoString(text string) goast.Expr {
	return &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(text)}
}

// ssrJSNumberLiteral spells a JavaScript numeric literal as a Go floating
// constant, so that it is a float64 wherever it lands, also in an any
// context (T-5.3a): 0 becomes 0.0, 0xff becomes 255.0.
func ssrJSNumberLiteral(raw string) (string, bool) {
	text := strings.ReplaceAll(raw, "_", "")
	var value float64
	switch {
	case len(text) > 2 && text[0] == '0' && strings.ContainsRune("xXoObB", rune(text[1])):
		base := map[byte]int{'x': 16, 'X': 16, 'o': 8, 'O': 8, 'b': 2, 'B': 2}[text[1]]
		n, err := strconv.ParseUint(text[2:], base, 64)
		if err != nil {
			return "", false
		}
		value = float64(n)
	case len(text) > 1 && text[0] == '0' && !strings.ContainsAny(text, ".eE"):
		// Legacy octal (017) is a strict-mode syntax error.
		return "", false
	default:
		n, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return "", false
		}
		value = n
	}
	spelled := strconv.FormatFloat(value, 'g', -1, 64)
	if !strings.ContainsAny(spelled, ".eE") {
		spelled += ".0"
	}
	return spelled, true
}

// ssrLocalName is the Go local that holds a binding's initial value.
func ssrLocalName(name string) string { return "ssrL_" + name }

// localRef reads a local binding through its Go local. The initializer is
// still built here, in the component scope, for its type facts and so that
// a failure is reported at the site that reads it; the prologue lowers it
// once. A read of a binding declared later is a ReferenceError in
// JavaScript and is rejected.
func (b *ssrIRBuilder) localRef(name, kind string, initializer JsExpr, pos int, boolProp bool) (SSRIRExpr, error) {
	if b.current != "" && b.ctx.localIndex[name] > b.ctx.localIndex[b.current] {
		problem := ssrExprUnsupported(pos, "%q reads %q, which is declared later (a ReferenceError in JavaScript)", b.current, name)
		return nil, problem
	}
	if b.resolving[name] {
		return nil, ssrExprUnsupported(pos, "local %s binding %q has a circular server initializer", kind, name)
	}
	b.resolving[name] = true
	defer delete(b.resolving, name)
	b.inlined[name] = true
	if b.reads == nil {
		b.reads = map[string]bool{}
	}
	b.reads[name] = true
	inner := &ssrIRBuilder{ctx: ssrComponentContext(b.ctx), resolving: b.resolving, functions: map[string]bool{}, inlined: b.inlined, js: b.js, site: b.site, locals: true, current: name}
	value, err := inner.build(initializer, pos)
	if err != nil {
		b.locate(err, initializer)
		wrapped := ssrExprUnsupported(pos, "local %s binding %q has no server-safe initializer: %v", kind, name, err)
		return nil, ssrWithReason(wrapped, err, ssrLocalReason(kind, name))
	}
	facts := value.irBase().Facts
	facts.Bool = boolProp
	facts.GoString = false
	return &ssrIRGo{ssrIRBase: b.base(pos, facts), Expr: goast.NewIdent(ssrLocalName(name))}, nil
}

// ssrComponentContext is ctx without template bindings and function
// parameters: the scope of the component script.
func ssrComponentContext(ctx SSRExprContext) SSRExprContext {
	ctx.Bindings = map[string]goast.Expr{}
	return ctx
}
