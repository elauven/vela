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
		Elems []SSRIRExpr // nil element is a hole
	}
	ssrIRObject struct {
		ssrIRBase
		Keys   []string
		Values []SSRIRExpr
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
		elements := make([]goast.Expr, len(n.Elems))
		for i, element := range n.Elems {
			if element == nil {
				elements[i] = goast.NewIdent("nil")
			} else {
				elements[i] = lowerSSRIR(element)
			}
		}
		return &goast.CompositeLit{Type: &goast.ArrayType{Elt: goast.NewIdent("any")}, Elts: elements}
	case *ssrIRObject:
		elements := make([]goast.Expr, len(n.Keys))
		for i, key := range n.Keys {
			elements[i] = &goast.KeyValueExpr{Key: &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(key)}, Value: lowerSSRIR(n.Values[i])}
		}
		return &goast.CompositeLit{Type: &goast.MapType{Key: goast.NewIdent("string"), Value: goast.NewIdent("any")}, Elts: elements}
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
		for _, element := range node.Elems {
			if element == nil {
				elements = append(elements, nil)
				continue
			}
			if _, spread := element.(*JsSpreadExpr); spread {
				return nil, ssrExprUnsupported(pos, "array spread is not available to static SSR")
			}
			value, err := b.build(element, pos)
			if err != nil {
				return nil, err
			}
			elements = append(elements, value)
		}
		return &ssrIRArray{ssrIRBase: b.base(pos, ssrIRFacts{Array: true}), Elems: elements}, nil
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
		if node.Op == "+" || node.Op == "-" {
			if !x.irBase().Facts.Number {
				return nil, ssrExprUnsupported(pos, "unary operator %q requires a known number during SSR", node.Op)
			}
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
		return &ssrIRLiteral{ssrIRBase: b.base(pos, ssrIRFacts{Number: true}), Kind: token.FLOAT, Value: node.Raw}, nil
	case "boolean":
		return &ssrIRLiteral{ssrIRBase: b.base(pos, ssrIRFacts{Bool: true}), Kind: token.IDENT, Value: node.Raw}, nil
	case "null":
		return &ssrIRLiteral{ssrIRBase: b.base(pos, ssrIRFacts{}), Kind: token.IDENT, Value: "nil"}, nil
	}
	return nil, ssrExprUnsupported(pos, "literal kind %q is not supported", node.Kind)
}

// object admits ordinary non-computed data properties with identifier or
// string keys; a later duplicate key replaces the earlier value in place.
func (b *ssrIRBuilder) object(node *JsObjectExpr, pos int) (SSRIRExpr, error) {
	out := &ssrIRObject{ssrIRBase: b.base(pos, ssrIRFacts{Object: true})}
	indices := make(map[string]int, len(node.Props))
	for _, property := range node.Props {
		if property == nil || property.Spread {
			return nil, ssrExprUnsupported(pos, "object spread is not available to static SSR")
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
		if previous, duplicate := indices[key]; duplicate {
			out.Values[previous] = value
			continue
		}
		indices[key] = len(out.Keys)
		out.Keys = append(out.Keys, key)
		out.Values = append(out.Values, value)
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
		return nil, ssrExprUnsupported(pos, "loose equality operator %q is not available to static SSR until its coercion matrix is defined; use strict equality", node.Op)
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
		return nil, ssrExprUnsupported(pos, "relational operator %q requires two known numbers or two known strings during SSR", node.Op)
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
		return nil, ssrExprUnsupported(pos, "operator + requires two known numbers or at least one known string during SSR")
	case "-", "*", "/", "%":
		if !l.Number || !r.Number {
			return nil, ssrExprUnsupported(pos, "numeric operator %q requires two known numbers during SSR", node.Op)
		}
		name := map[string]string{"-": "NumberSubtract", "*": "NumberMultiply", "/": "NumberDivide", "%": "NumberRemainder"}[node.Op]
		return &ssrIRHelper{ssrIRBase: b.base(pos, ssrIRFacts{Number: true}), Name: name, Args: []SSRIRExpr{left, right}}, nil
	}
	return nil, ssrExprUnsupported(pos, "binary operator %q is not supported", node.Op)
}

func (b *ssrIRBuilder) member(node *JsMemberExpr, pos int) (SSRIRExpr, error) {
	if !node.Computed {
		property, ok := node.Prop.(*JsIdent)
		if !ok {
			return nil, ssrExprUnsupported(pos, "member access is not available until the prop has a declared object type")
		}
		object, err := b.build(node.Obj, pos)
		if err != nil {
			return nil, err
		}
		if property.Name == "length" {
			return &ssrIRHelper{ssrIRBase: b.base(pos, ssrIRFacts{}), Name: "Length", Args: []SSRIRExpr{object}}, nil
		}
		key := &ssrIRLiteral{ssrIRBase: b.base(pos, ssrIRFacts{String: true}), Kind: token.STRING, Value: property.Name}
		return &ssrIRHelper{ssrIRBase: b.base(pos, ssrIRFacts{}), Name: "Get", Args: []SSRIRExpr{object, key}}, nil
	}
	object, err := b.build(node.Obj, pos)
	if err != nil {
		return nil, err
	}
	index, err := b.build(node.Prop, pos)
	if err != nil {
		return nil, err
	}
	o, i := object.irBase().Facts, index.irBase().Facts
	if !(o.Array && i.Number) && !(o.Object && i.String) {
		return nil, ssrExprUnsupported(pos, "computed member access requires a known array with a numeric index or a known object with a string key during SSR")
	}
	return &ssrIRHelper{ssrIRBase: b.base(pos, ssrIRFacts{}), Name: "Index", Args: []SSRIRExpr{object, index}}, nil
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
