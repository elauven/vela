package ssr

import (
	"github.com/elauven/vela/internal/analyzer"
	"github.com/elauven/vela/internal/jsast"
	"github.com/elauven/vela/internal/sfcast"

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
		Keys   []string // "" with Spread or a computed key
		Values []SSRIRExpr
		Spread []bool
		// Computed holds the key expression of a computed property
		// ({ [k]: v }), nil for the others.
		Computed []SSRIRExpr
		// Accessor is "get" or "set" for the halves of an accessor
		// property, "" for data properties.
		Accessor []string
		// JS creates the object with the render's evaluation state, which
		// receives the exceptions of its conversions to primitives and
		// accessors; Local marks an object a translated function creates,
		// whose methods may write through this (T-6.6b).
		JS, Local bool
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
	// ssrIRHost calls a //vela:host function through vela.CallHost.
	ssrIRHost struct {
		ssrIRBase
		Binding *SSRHostBinding
		Alias   string
		Args    []SSRIRExpr
		Site    string
	}
	// ssrIROptional is an optional chain link: Rest reads Param, the
	// non-nullish Base.
	ssrIROptional struct {
		ssrIRBase
		Base  SSRIRExpr
		Param string
		Rest  SSRIRExpr
	}
	// ssrIRSeq evaluates Exprs in order for their effects and yields the
	// last (the comma operator), or undefined after all of them (void).
	ssrIRSeq struct {
		ssrIRBase
		Exprs []SSRIRExpr
		Void  bool
	}
	// ssrIROptionalMethod is an optional method call receiver.name?.(…):
	// Rest, the call on Param and the rest of the chain, runs only when
	// receiver.name is not null or undefined.
	ssrIROptionalMethod struct {
		ssrIRBase
		Receiver SSRIRExpr
		Name     string
		Param    string
		Rest     SSRIRExpr
		Site     string
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
				// A hole reads as undefined; array methods skip it.
				segment = append(segment, &goast.CallExpr{Fun: goSelector("vela", "Hole")})
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
		switch {
		case n.Local:
			object = &goast.CallExpr{Fun: goSelector("ssrJS", "LocalObject")}
		case n.JS:
			object = &goast.CallExpr{Fun: goSelector("ssrJS", "NewObject")}
		}
		// With accessors a data property is defined rather than assigned,
		// so that it replaces an accessor of the same name.
		set := "Set"
		for _, kind := range n.Accessor {
			if kind != "" {
				set = "Define"
			}
		}
		for i, value := range n.Values {
			if n.Spread[i] {
				object = &goast.CallExpr{Fun: goSelectorExpr(object, "Spread"), Args: []goast.Expr{lowerSSRIR(value)}}
				continue
			}
			key := ssrGoString(n.Keys[i])
			if n.Computed[i] != nil {
				// Go evaluates the key before the value, as JavaScript.
				key = &goast.CallExpr{Fun: goSelector("vela", "PropertyKey"), Args: []goast.Expr{lowerSSRIR(n.Computed[i])}}
			}
			method := set
			switch n.Accessor[i] {
			case "get":
				method = "DefineGetter"
			case "set":
				method = "DefineSetter"
			}
			object = &goast.CallExpr{Fun: goSelectorExpr(object, method), Args: []goast.Expr{key, lowerSSRIR(value)}}
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
		var call goast.Expr = &goast.CallExpr{Fun: goSelector("vela", n.Name), Args: args}
		// The lazy helpers return any; a typed result the facts promise is
		// converted, so that Go operators apply.
		switch {
		case n.Facts.String:
			call = &goast.CallExpr{Fun: goSelector("vela", "String"), Args: []goast.Expr{call}}
		case n.Facts.Number:
			call = &goast.CallExpr{Fun: goSelector("vela", "ToNumber"), Args: []goast.Expr{call}}
		}
		return call
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
	case *ssrIRCallFunc:
		return lowerSSRCallFunc(n)
	case *ssrIRHost:
		args := []goast.Expr{
			goast.NewIdent("ssrJS"),
			&goast.CallExpr{Fun: goSelector("vela", "ContextOf"), Args: []goast.Expr{goast.NewIdent("w")}},
			ssrGoString(n.Site), ssrGoString(n.Binding.Symbol.Name), goSelector(n.Alias, n.Binding.GoName),
		}
		for _, arg := range n.Args {
			args = append(args, lowerSSRIR(arg))
		}
		return &goast.CallExpr{Fun: goSelector("vela", "CallHost"), Args: args}
	case *ssrIROptional:
		rest := &goast.FuncLit{
			Type: &goast.FuncType{
				Params:  &goast.FieldList{List: []*goast.Field{{Names: []*goast.Ident{goast.NewIdent(n.Param)}, Type: goast.NewIdent("any")}}},
				Results: &goast.FieldList{List: []*goast.Field{{Type: goast.NewIdent("any")}}},
			},
			Body: &goast.BlockStmt{List: []goast.Stmt{&goast.ReturnStmt{Results: []goast.Expr{lowerSSRIR(n.Rest)}}}},
		}
		return &goast.CallExpr{Fun: goSelector("vela", "OptionalChain"), Args: []goast.Expr{lowerSSRIR(n.Base), rest}}
	case *ssrIRSeq:
		// Statements fix the order: Go leaves the order of a variable
		// read against a call among operands unspecified.
		var body []goast.Stmt
		last := len(n.Exprs) - 1
		if n.Void {
			last = len(n.Exprs)
		}
		for _, expr := range n.Exprs[:last] {
			switch expr.(type) {
			case *ssrIRLiteral, *ssrIRUndefined:
				continue // no effect; _ = nil would not compile
			}
			body = append(body, &goast.AssignStmt{Lhs: []goast.Expr{goast.NewIdent("_")}, Tok: token.ASSIGN, Rhs: []goast.Expr{lowerSSRIR(expr)}})
		}
		result := ssrUndefined()
		if !n.Void {
			result = lowerSSRIR(n.Exprs[last])
		}
		body = append(body, &goast.ReturnStmt{Results: []goast.Expr{result}})
		return &goast.CallExpr{Fun: &goast.FuncLit{
			Type: &goast.FuncType{Params: &goast.FieldList{}, Results: &goast.FieldList{List: []*goast.Field{{Type: goast.NewIdent("any")}}}},
			Body: &goast.BlockStmt{List: body},
		}}
	case *ssrIROptionalMethod:
		rest := &goast.FuncLit{
			Type: &goast.FuncType{
				Params:  &goast.FieldList{List: []*goast.Field{{Names: []*goast.Ident{goast.NewIdent(n.Param)}, Type: goast.NewIdent("any")}}},
				Results: &goast.FieldList{List: []*goast.Field{{Type: goast.NewIdent("any")}}},
			},
			Body: &goast.BlockStmt{List: []goast.Stmt{&goast.ReturnStmt{Results: []goast.Expr{lowerSSRIR(n.Rest)}}}},
		}
		return &goast.CallExpr{Fun: goSelector("ssrJS", "OptionalMethod"), Args: []goast.Expr{ssrGoString(n.Site), lowerSSRIR(n.Receiver), ssrGoString(n.Name), rest}}
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
	// hostImports maps the import paths of called host packages to their
	// aliases; calls records the outcome of every function call (T-2.3).
	hostImports map[string]string
	// modules maps the variables of used modules to their import names.
	modules map[string]string
	calls   map[string]ssrCallOutcome
	// funcCompiler is set while building a translated function body.
	funcCompiler *ssrFuncCompiler
}

// BuildSSRIR builds the IR of one template expression.
func BuildSSRIR(expr *sfcast.TemplateExpr, ctx SSRExprContext) (SSRIRExpr, error) {
	if expr == nil || expr.Root() == nil {
		return nil, &SSRExpressionError{Message: "template expression has no cached AST"}
	}
	return buildSSRIR(expr.Root(), ctx, expr.Pos)
}

func buildSSRIR(expr jsast.JsExpr, ctx SSRExprContext, pos int) (SSRIRExpr, error) {
	return newSSRIRBuilder(ctx).build(expr, pos)
}

func newSSRIRBuilder(ctx SSRExprContext) *ssrIRBuilder {
	return &ssrIRBuilder{ctx: ctx, resolving: map[string]bool{}, functions: map[string]bool{}, inlined: map[string]bool{}}
}

func (b *ssrIRBuilder) base(pos int, facts ssrIRFacts) ssrIRBase {
	return ssrIRBase{Pos: pos, Facts: facts}
}

func (b *ssrIRBuilder) build(expr jsast.JsExpr, pos int) (SSRIRExpr, error) {
	switch node := expr.(type) {
	case *jsast.JsIdent:
		return b.ident(node, pos)
	case *jsast.JsLiteral:
		return b.literal(node, pos)
	case *jsast.JsArrayExpr:
		elements := make([]SSRIRExpr, 0, len(node.Elems))
		var spreads []bool
		for _, element := range node.Elems {
			if element == nil {
				elements = append(elements, nil)
				continue
			}
			if spread, ok := element.(*jsast.JsSpreadExpr); ok {
				if !b.js {
					return nil, ssrExprUnsupported(pos, "array spread is not available to static SSR")
				}
				value, err := b.iterable(spread.X, pos)
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
			elements = append(elements, b.boxStored(element, value))
		}
		for spreads != nil && len(spreads) < len(elements) {
			spreads = append(spreads, false)
		}
		return &ssrIRArray{ssrIRBase: b.base(pos, ssrIRFacts{Array: true}), Elems: elements, Spread: spreads, Site: b.site}, nil
	case *jsast.JsObjectExpr:
		return b.object(node, pos)
	case *jsast.JsTemplateLit:
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
			b.converts(value)
			out.Exprs = append(out.Exprs, value)
		}
		return out, nil
	case *jsast.JsTaggedTemplate:
		// String.raw is the only admitted tag: the raw text of the
		// template (escapes kept, line terminators normalised to \n) with
		// the substitutions converted to strings.
		if b.globalName(node.Tag) != "String.raw" || node.Quasi == nil || len(node.Quasi.Quasis) != len(node.Quasi.Exprs)+1 {
			return nil, ssrExprUnsupported(pos, "tagged templates are admitted only with String.raw")
		}
		out := &ssrIRTemplate{ssrIRBase: b.base(pos, ssrIRFacts{String: true})}
		for index, quasi := range node.Quasi.Quasis {
			out.Quasis = append(out.Quasis, strings.ReplaceAll(strings.ReplaceAll(quasi, "\r\n", "\n"), "\r", "\n"))
			if index == len(node.Quasi.Exprs) {
				continue
			}
			value, err := b.build(node.Quasi.Exprs[index], pos)
			if err != nil {
				return nil, err
			}
			b.converts(value)
			out.Exprs = append(out.Exprs, value)
		}
		return out, nil
	case *jsast.JsUnaryExpr:
		if node.Op == "++" || node.Op == "--" {
			return nil, ssrImpure(pos, "update operator %q writes a binding during server rendering", node.Op)
		}
		if node.Op == "delete" {
			return b.deleteExpr(node, pos)
		}
		if node.Op == "void" {
			if _, literal := node.X.(*jsast.JsLiteral); literal {
				return &ssrIRUndefined{ssrIRBase: b.base(pos, ssrIRFacts{})}, nil
			}
		}
		x, err := b.build(node.X, pos)
		if err != nil {
			return nil, err
		}
		if node.Op == "void" {
			return &ssrIRSeq{ssrIRBase: b.base(pos, ssrIRFacts{}), Exprs: []SSRIRExpr{x}, Void: true}, nil
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
			b.converts(x)
			name := "Number"
			if node.Op == "-" {
				name = "NumberNegate"
			}
			return &ssrIRHelper{ssrIRBase: b.base(pos, ssrIRFacts{Number: true}), Name: name, Args: []SSRIRExpr{x}}, nil
		}
		return nil, ssrExprUnsupported(pos, "unary operator %q is not supported", node.Op)
	case *jsast.JsBinaryExpr:
		return b.binary(node, pos)
	case *jsast.JsSeqExpr:
		seq := &ssrIRSeq{ssrIRBase: b.base(pos, ssrIRFacts{})}
		for _, item := range node.Exprs {
			value, err := b.build(item, pos)
			if err != nil {
				return nil, err
			}
			seq.Exprs = append(seq.Exprs, value)
		}
		return seq, nil
	case *jsast.JsLogicalExpr:
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
	case *jsast.JsMemberExpr:
		return b.member(node, pos)
	case *jsast.JsConditionalExpr:
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
	case *jsast.JsCallExpr:
		return b.call(node, pos)
	case *jsast.JsNewExpr:
		return b.newError(node, pos)
	case *jsast.JsArrowFunc, *jsast.JsFuncExpr:
		return b.functionValue(node, pos)
	case *jsast.JsAwaitExpr:
		return b.await(node, pos)
	case *jsast.JsAssignExpr:
		return nil, ssrImpure(pos, "assignments are not available to static SSR")
	case *jsast.JsThisExpr:
		// this is the receiver of a method call; a plain call and code
		// outside functions have undefined (module code is strict).
		if b.funcCompiler != nil && b.funcCompiler.this != nil {
			return &ssrIRGo{ssrIRBase: b.base(pos, ssrIRFacts{}), Expr: b.funcCompiler.this}, nil
		}
		return &ssrIRUndefined{ssrIRBase: b.base(pos, ssrIRFacts{})}, nil
	default:
		return nil, ssrExprUnsupported(pos, "JavaScript expression %T is not supported", expr)
	}
}

func (b *ssrIRBuilder) ident(node *jsast.JsIdent, pos int) (SSRIRExpr, error) {
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
		facts := param.irBase().Facts
		if facts == (ssrIRFacts{}) {
			facts = ssrGoExprFacts(value, b.ctx)
		}
		facts.Bool = facts.Bool || boolProp
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
			initializers map[string]jsast.JsExpr
		}{{"reactive", b.ctx.StateInitializers}, {"reactive", b.ctx.DeepInitializers}, {"derived", b.ctx.DerivedInitializers}, {"plain", b.ctx.PlainInitializers}} {
			if initializer, ok := local.initializers[node.Name]; ok {
				return b.localRef(node.Name, local.kind, initializer, pos, boolProp)
			}
		}
	}
	for _, local := range []struct {
		kind         string
		initializers map[string]jsast.JsExpr
	}{{"reactive", b.ctx.StateInitializers}, {"derived", b.ctx.DerivedInitializers}, {"plain", b.ctx.PlainInitializers}} {
		if initializer, ok := local.initializers[node.Name]; ok {
			return b.local(node.Name, local.kind, initializer, pos, boolProp)
		}
	}
	if ref, ok := b.ctx.ModuleRefs[node.Name]; ok && b.js {
		return b.moduleIdent(ref, node.Name, pos)
	}
	if function := b.ctx.Functions[node.Name]; function != nil && b.ctx.funcs != nil && b.js {
		// A component function used as a value (a callback prop): compiled
		// here as a closure, like an arrow, with writes deferred to run time.
		value := &jsast.JsFuncExpr{Params: function.Params, Body: function.Body, Async: function.Async, Generator: function.Generator}
		value.BaseExpr.BaseNode = function.BaseStmt.BaseNode
		return b.functionValue(value, pos)
	}
	if ssrGlobalFunctionValues[node.Name] && b.js && !b.shadowedGlobal(node.Name) {
		// A global function used as a value: items.filter(Boolean).
		b.usedJS = true
		return &ssrIRGo{ssrIRBase: b.base(pos, ssrIRFacts{}), Expr: &goast.CallExpr{Fun: goSelector("ssrJS", "GlobalFunc"), Args: []goast.Expr{ssrGoString(b.site), ssrGoString(node.Name)}}}, nil
	}
	if (node.Name == "NaN" || node.Name == "Infinity") && !b.shadowedGlobal(node.Name) {
		return &ssrIRGo{ssrIRBase: b.base(pos, ssrIRFacts{Number: true}), Expr: &goast.CallExpr{Fun: goSelector("vela", "ToNumber"), Args: []goast.Expr{ssrGoString(node.Name)}}}, nil
	}
	if b.ctx.ReactiveNames[node.Name] {
		return nil, ssrExprUnsupported(pos, "local reactive binding %q has no server value; pass it as an exported prop or wait for hydration", node.Name)
	}
	return nil, ssrExprUnsupported(pos, "identifier %q is not an exported prop or server binding", node.Name)
}

// local inlines a $state, $derived, or plain binding initializer.
func (b *ssrIRBuilder) local(name, kind string, initializer jsast.JsExpr, pos int, boolProp bool) (SSRIRExpr, error) {
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

func (b *ssrIRBuilder) literal(node *jsast.JsLiteral, pos int) (SSRIRExpr, error) {
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

// object admits data properties with identifier, string or computed keys
// and spread properties. Every value is evaluated in source order; a later duplicate
// key replaces the value and keeps the first position, as in JavaScript.
func (b *ssrIRBuilder) object(node *jsast.JsObjectExpr, pos int) (SSRIRExpr, error) {
	out := &ssrIRObject{ssrIRBase: b.base(pos, ssrIRFacts{Object: true})}
	defer func() {
		// Only an object that can have its own toString, valueOf, or
		// toJSON, or that has methods or accessors, needs the evaluation
		// state.
		for i, key := range out.Keys {
			if b.js && (out.Spread[i] || out.Computed[i] != nil || ssrConversionKeys[key] || out.Accessor[i] != "" || ssrIsFunction(node.Props[i].Value)) {
				out.JS, b.usedJS = true, true
			}
		}
		// An object a translated function creates may be written through
		// this by its methods.
		out.Local = out.JS && b.funcCompiler != nil
	}()
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
			out.Computed = append(out.Computed, nil)
			out.Accessor = append(out.Accessor, "")
			continue
		}
		accessor := ""
		if property.Kind == "get" || property.Kind == "set" {
			accessor = property.Kind
		} else if property.Kind != "init" {
			return nil, ssrExprUnsupported(pos, "object property kind %q is not supported", property.Kind)
		}
		if property.Computed {
			// { [k]: v } converts k with ToPropertyKey; a computed
			// "__proto__" is an ordinary own property.
			key, err := b.build(property.Key, pos)
			if err != nil {
				return nil, err
			}
			value, err := b.build(property.Value, pos)
			if err != nil {
				return nil, err
			}
			out.Keys = append(out.Keys, "")
			out.Values = append(out.Values, value)
			out.Spread = append(out.Spread, false)
			out.Computed = append(out.Computed, key)
			out.Accessor = append(out.Accessor, accessor)
			continue
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
		value = b.boxStored(property.Value, value)
		out.Keys = append(out.Keys, key)
		out.Values = append(out.Values, value)
		out.Spread = append(out.Spread, false)
		out.Computed = append(out.Computed, nil)
		out.Accessor = append(out.Accessor, accessor)
	}
	return out, nil
}

func (b *ssrIRBuilder) binary(node *jsast.JsBinaryExpr, pos int) (SSRIRExpr, error) {
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
		b.converts(left, right)
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
		b.converts(left, right)
		// JavaScript + concatenates when either operand is a string.
		switch {
		case l.String || r.String:
			if l.String != r.String {
				// The other operand is converted with the hint "default":
				// an object's valueOf comes before its toString.
				left = &ssrIRHelper{ssrIRBase: b.base(pos, ssrIRFacts{}), Name: ssrAddStringHelper(left), Args: []SSRIRExpr{left}}
				right = &ssrIRHelper{ssrIRBase: b.base(pos, ssrIRFacts{}), Name: ssrAddStringHelper(right), Args: []SSRIRExpr{right}}
			}
			return &ssrIRBinary{ssrIRBase: b.base(pos, ssrIRFacts{String: true}), Op: token.ADD, X: left, Y: right}, nil
		case l.Number && r.Number:
			return &ssrIRHelper{ssrIRBase: b.base(pos, ssrIRFacts{Number: true}), Name: "NumberAdd", Args: []SSRIRExpr{left, right}}, nil
		}
		// Dynamic tier: ToPrimitive, then concatenation or addition.
		return &ssrIRHelper{ssrIRBase: b.base(pos, ssrIRFacts{}), Name: "Add", Args: []SSRIRExpr{left, right}}, nil
	case "-", "*", "/", "%":
		b.converts(left, right)
		// The Number helpers apply ToNumber, so both tiers share them.
		name := map[string]string{"-": "NumberSubtract", "*": "NumberMultiply", "/": "NumberDivide", "%": "NumberRemainder"}[node.Op]
		return &ssrIRHelper{ssrIRBase: b.base(pos, ssrIRFacts{Number: true}), Name: name, Args: []SSRIRExpr{left, right}}, nil
	case "**":
		b.converts(left, right)
		return &ssrIRHelper{ssrIRBase: b.base(pos, ssrIRFacts{Number: true}), Name: "Pow", Args: []SSRIRExpr{left, right}}, nil
	case "in":
		if !b.js {
			return nil, ssrExprUnsupported(pos, "the in operator is available only in translated script code")
		}
		b.usedJS, b.throws = true, true
		return &ssrIRJS{ssrIRBase: b.base(pos, ssrIRFacts{Bool: true}), Method: "In", Args: []SSRIRExpr{left, right}, Site: b.site}, nil
	}
	return nil, ssrExprUnsupported(pos, "binary operator %q is not supported", node.Op)
}

// ssrNullishLiteral reports null or undefined written in the source.
func ssrNullishLiteral(expr jsast.JsExpr) bool {
	switch node := expr.(type) {
	case *jsast.JsLiteral:
		return node.Kind == "null"
	case *jsast.JsIdent:
		return node.Name == "undefined"
	}
	return false
}

// member builds a member chain a.b[c]: the root, then each link. A chain
// with an optional link (?.) is built by optionalChain (T-6.1b).
func (b *ssrIRBuilder) member(node *jsast.JsMemberExpr, pos int) (SSRIRExpr, error) {
	if b.js {
		if value, ok := b.builtinConstant(node, pos); ok {
			return value, nil
		}
	}
	if value, handled, err := b.optionalChain(node, pos); handled {
		return value, err
	}
	var links []*jsast.JsMemberExpr
	var root jsast.JsExpr = node
	for {
		link, ok := root.(*jsast.JsMemberExpr)
		if !ok {
			break
		}
		links = append([]*jsast.JsMemberExpr{link}, links...)
		root = link.Obj
		if ssrParenthesized(root, link) {
			// (a?.b).c: the parentheses end the optional chain.
			break
		}
	}
	base, err := b.build(root, pos)
	if err != nil {
		return nil, err
	}
	for _, link := range links {
		next, err := b.memberLink(base, link, false, pos)
		if err != nil {
			return nil, err
		}
		base = next
	}
	return base, nil
}

// ssrAddStringHelper converts an operand of + next to a string: String for
// a proven primitive, AddString (hint "default") for the others.
func ssrAddStringHelper(value SSRIRExpr) string {
	if ssrIRPrimitive(value) {
		return "String"
	}
	return "AddString"
}

// ssrIsFunction reports a function expression.
func ssrIsFunction(expr jsast.JsExpr) bool {
	switch expr.(type) {
	case *jsast.JsArrowFunc, *jsast.JsFuncExpr:
		return true
	}
	return false
}

// deleteExpr lowers delete o.k and delete a[i] on a value the function
// created (T-6.6b), and delete this.k on an object a translated function
// created during the render.
func (b *ssrIRBuilder) deleteExpr(node *jsast.JsUnaryExpr, pos int) (SSRIRExpr, error) {
	member, ok := node.X.(*jsast.JsMemberExpr)
	if !ok || member.Optional {
		return nil, ssrExprUnsupported(pos, "delete is admitted only on a property, as in delete o.k or delete a[i]")
	}
	c := b.funcCompiler
	root := jsast.JsExpr(member)
	for {
		inner, isMember := root.(*jsast.JsMemberExpr)
		if !isMember {
			break
		}
		root = inner.Obj
	}
	_, viaThis := member.Obj.(*jsast.JsThisExpr)
	if !viaThis {
		name := analyzer.IdentName(root)
		fresh := false
		if c != nil && name != "" && c.freshness != nil {
			_, local := c.lookup(name)
			fresh = local && c.freshness.fresh[name]
		}
		if !fresh {
			return nil, ssrImpure(pos, "delete changes a value the function did not create (T-6.6a)")
		}
	}
	object, err := b.build(member.Obj, pos)
	if err != nil {
		return nil, err
	}
	var key goast.Expr
	if member.Computed {
		value, err := b.build(member.Prop, pos)
		if err != nil {
			return nil, err
		}
		key = lowerSSRIR(value)
	} else if property, named := member.Prop.(*jsast.JsIdent); named {
		key = ssrGoString(property.Name)
	} else {
		return nil, ssrExprUnsupported(pos, "member name %T is not supported", member.Prop)
	}
	b.usedJS, b.throws = true, true
	return &ssrIRGo{ssrIRBase: b.base(pos, ssrIRFacts{Bool: true}), Expr: &goast.CallExpr{Fun: goSelector("ssrJS", "Delete"), Args: []goast.Expr{lowerSSRIR(object), key, goast.NewIdent(strconv.FormatBool(viaThis)), ssrGoString(b.site)}}}, nil
}

// ssrConversionKeys are the properties that conversions of an object call.
var ssrConversionKeys = map[string]bool{"toString": true, "valueOf": true, "toJSON": true}

// converts records that a value is converted to a primitive. Unless it is
// proven primitive, or is a prop (a Go value has no methods), the
// conversion can call a function in an own toString or valueOf property or
// throw TypeError (OrdinaryToPrimitive), so the site can throw.
func (b *ssrIRBuilder) converts(values ...SSRIRExpr) {
	if !b.js {
		return
	}
	for _, value := range values {
		if !ssrIRPrimitive(value) {
			b.usedJS, b.throws = true, true
			return
		}
	}
}

// ssrIRPrimitive reports a value whose conversion to a primitive calls no
// function and cannot throw.
func ssrIRPrimitive(value SSRIRExpr) bool {
	if f := value.irBase().Facts; f.String || f.Number || f.Bool {
		return true
	}
	switch value.(type) {
	case *ssrIRProp, *ssrIRLiteral, *ssrIRUndefined:
		return true
	}
	return false
}

// memberLink reads one property. A read on a value that is not proven to
// be an object, array, or string can throw TypeError (T-6.5a).
func (b *ssrIRBuilder) memberLink(object SSRIRExpr, link *jsast.JsMemberExpr, nonNullish bool, pos int) (SSRIRExpr, error) {
	b.usedJS = b.usedJS || b.js
	o := object.irBase().Facts
	if !nonNullish && !o.String && !o.Array && !o.Object {
		b.throws = b.throws || b.js
	}
	if !link.Computed {
		property, ok := link.Prop.(*jsast.JsIdent)
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

// ssrChainLink is one link of an optional chain: a member read or a call.
type ssrChainLink struct {
	member *jsast.JsMemberExpr
	call   *jsast.JsCallExpr
}

func (l ssrChainLink) optional() bool {
	if l.member != nil {
		return l.member.Optional
	}
	return l.call.Optional
}

// ssrParenthesized reports a child expression written in parentheses: it
// starts after its parent, as in (a?.b).c or (f)().
func ssrParenthesized(child, parent jsast.JsNode) bool {
	return child.NodePos() != parent.NodePos()
}

// ssrChain splits a chain a?.b.c(x)?.(y) into its root and links, up to a
// parenthesised part. A call whose callee is neither a link nor optional
// (f(x)?.y) is the root: it resolves as any call.
func ssrChain(expr jsast.JsExpr) (jsast.JsExpr, []ssrChainLink) {
	var links []ssrChainLink
	current := expr
	for {
		var next jsast.JsExpr
		switch node := current.(type) {
		case *jsast.JsMemberExpr:
			links = append([]ssrChainLink{{member: node}}, links...)
			next = node.Obj
		case *jsast.JsCallExpr:
			if _, method := node.Callee.(*jsast.JsMemberExpr); !method && !node.Optional {
				return current, links
			}
			links = append([]ssrChainLink{{call: node}}, links...)
			next = node.Callee
		default:
			return current, links
		}
		if ssrParenthesized(next, current) {
			return next, links
		}
		current = next
	}
}

// optionalChain builds a chain with an optional link (T-6.1b): a nullish
// value before ?. ends the whole chain, calls included, with undefined.
// Chains without one are not handled here.
func (b *ssrIRBuilder) optionalChain(expr jsast.JsExpr, pos int) (SSRIRExpr, bool, error) {
	root, links := ssrChain(expr)
	optional, calls := false, false
	for _, link := range links {
		optional = optional || link.optional()
		calls = calls || link.call != nil
	}
	if !optional {
		return nil, false, nil
	}
	if calls && !b.js {
		return nil, true, ssrExprUnsupported(pos, "calls in optional chains are available only in translated script code")
	}
	if ident, ok := root.(*jsast.JsIdent); ok && links[0].call != nil {
		_, local := b.params[ident.Name]
		_, binding := b.ctx.Bindings[ident.Name]
		if !local && !binding {
			// f?.() of a declared, host or module function: never nullish.
			plain := *links[0].call
			plain.Optional = false
			base, err := b.call(&plain, pos)
			if err != nil {
				return nil, true, err
			}
			value, err := b.chainLinks(base, links[1:], false, pos)
			return value, true, err
		}
	}
	base, err := b.build(root, pos)
	if err != nil {
		return nil, true, err
	}
	value, err := b.chainLinks(base, links, false, pos)
	return value, true, err
}

// chainLinks applies chain links to base. When entered is set, the first
// link is the optional link whose nullish check has already passed.
func (b *ssrIRBuilder) chainLinks(base SSRIRExpr, links []ssrChainLink, entered bool, pos int) (SSRIRExpr, error) {
	for i := 0; i < len(links); i++ {
		link := links[i]
		if link.optional() && !(entered && i == 0) {
			param := "ssrOpt" + strconv.Itoa(b.optionals)
			b.optionals++
			inner := &ssrIRGo{ssrIRBase: b.base(pos, ssrIRFacts{}), Expr: goast.NewIdent(param)}
			rest, err := b.chainLinks(inner, links[i:], true, pos)
			if err != nil {
				return nil, err
			}
			return &ssrIROptional{ssrIRBase: b.base(pos, ssrIRFacts{}), Base: base, Param: param, Rest: rest}, nil
		}
		nonNullish := entered && i == 0
		if link.member != nil && i+1 < len(links) && links[i+1].call != nil && links[i+1].call.Callee == link.member {
			call := links[i+1].call
			if !call.Optional {
				value, err := b.methodOn(base, link.member, call, pos)
				if err != nil {
					return nil, err
				}
				base, i = value, i+1
				continue
			}
			// a.b?.(): the call and the rest of the chain run only when
			// a.b is not nullish.
			property, named := link.member.Prop.(*jsast.JsIdent)
			if link.member.Computed || !named {
				return nil, ssrExprUnsupported(pos, "optional calls of computed members are not supported")
			}
			param := "ssrOpt" + strconv.Itoa(b.optionals)
			b.optionals++
			receiver := &ssrIRGo{ssrIRBase: b.base(pos, base.irBase().Facts), Expr: goast.NewIdent(param)}
			value, err := b.methodOn(receiver, link.member, call, pos)
			if err != nil {
				return nil, err
			}
			rest, err := b.chainLinks(value, links[i+2:], false, pos)
			if err != nil {
				return nil, err
			}
			b.usedJS, b.throws = true, true
			return &ssrIROptionalMethod{ssrIRBase: b.base(pos, ssrIRFacts{}), Receiver: base, Name: property.Name, Param: param, Rest: rest, Site: b.site}, nil
		}
		if link.member != nil {
			next, err := b.memberLink(base, link.member, nonNullish, pos)
			if err != nil {
				return nil, err
			}
			base = next
			continue
		}
		// A call of a function value: f?.(), g(x)?.(), a?.b.c?.().
		value, err := b.valueCall(base, link.call, pos)
		if err != nil {
			return nil, err
		}
		base = value
	}
	return base, nil
}

// methodOn calls a catalogue method on an already built receiver, as
// builtinCall does for receiver.name(args).
func (b *ssrIRBuilder) methodOn(receiver SSRIRExpr, member *jsast.JsMemberExpr, call *jsast.JsCallExpr, pos int) (SSRIRExpr, error) {
	name := "\x00receiver" + strconv.Itoa(b.optionals)
	b.optionals++
	if b.params == nil {
		b.params = map[string]SSRIRExpr{}
	}
	b.params[name] = receiver
	defer delete(b.params, name)
	synthetic := &jsast.JsCallExpr{
		BaseExpr: call.BaseExpr,
		Callee:   &jsast.JsMemberExpr{BaseExpr: member.BaseExpr, Obj: &jsast.JsIdent{BaseExpr: member.BaseExpr, Name: name}, Prop: member.Prop, Computed: member.Computed},
		Args:     call.Args,
	}
	value, handled, err := b.builtinCall(synthetic, pos)
	if !handled {
		if property, named := member.Prop.(*jsast.JsIdent); named && !member.Computed {
			if b.js && !ssrUnmodeledMethods[property.Name] {
				return b.objectMethodCall(receiver, property.Name, call, pos)
			}
			return nil, ssrExprUnsupported(pos, "method value.%s is not available during server rendering", property.Name)
		}
		return nil, ssrExprUnsupported(pos, "computed method calls are not supported")
	}
	return value, err
}

// ssrUnmodeledMethods are method names of built-in prototypes the server
// does not model (RegExp, Date, Function, the array iterators outside
// for…of, locale methods): a call keeps its diagnostic and host route
// instead of reaching a function stored in an object.
var ssrUnmodeledMethods = map[string]bool{
	"test": true, "exec": true, "match": true, "matchAll": true, "search": true,
	"localeCompare": true, "toLocaleUpperCase": true, "toLocaleLowerCase": true,
	"toLocaleString": true, "toLocaleDateString": true, "toLocaleTimeString": true,
	"keys": true, "entries": true, "values": true, "copyWithin": true,
	"call": true, "apply": true, "bind": true,
	"hasOwnProperty": true, "isPrototypeOf": true, "propertyIsEnumerable": true,
	// Date methods in local time and formats (§2.10).
	"getDate": true, "getDay": true, "getFullYear": true, "getHours": true,
	"getMilliseconds": true, "getMinutes": true, "getMonth": true, "getSeconds": true,
	"getTimezoneOffset": true, "getYear": true,
	"setDate": true, "setFullYear": true, "setHours": true, "setMilliseconds": true,
	"setMinutes": true, "setMonth": true, "setSeconds": true,
	"toDateString": true, "toTimeString": true, "toUTCString": true, "toGMTString": true,
}

// objectMethodCall calls receiver.name(args): a function stored in a
// property of an object, called with the receiver as this (§7.1). Any
// other receiver throws TypeError, as in JavaScript.
func (b *ssrIRBuilder) objectMethodCall(receiver SSRIRExpr, name string, call *jsast.JsCallExpr, pos int) (SSRIRExpr, error) {
	lowered := []goast.Expr{ssrGoString(b.site), lowerSSRIR(receiver), ssrGoString(name)}
	ellipsis := token.NoPos
	spread, err := b.spreadArgs(call, pos)
	if err != nil {
		return nil, err
	}
	if spread != nil {
		lowered, ellipsis = append(lowered, spread), 1
	} else {
		args, err := b.buildArgs(call, pos)
		if err != nil {
			return nil, err
		}
		for _, arg := range args {
			lowered = append(lowered, lowerSSRIR(arg))
		}
	}
	b.usedJS, b.throws = true, true
	return &ssrIRGo{ssrIRBase: b.base(pos, ssrIRFacts{}), Expr: &goast.CallExpr{Fun: goSelector("ssrJS", "Method"), Args: lowered, Ellipsis: ellipsis}}, nil
}

// call resolves a call in the order of T-4.1: a declared host
// implementation, then native translation of a top-level named function
// with one return expression (inlined), otherwise the call is unsupported
// and the diagnostic names the host implementation that would fix it.
func (b *ssrIRBuilder) call(call *jsast.JsCallExpr, pos int) (SSRIRExpr, error) {
	if value, handled, err := b.optionalChain(call, pos); handled {
		return value, err
	}
	if member, ok := call.Callee.(*jsast.JsMemberExpr); ok && b.js {
		if value, handled, err := b.promiseCall(member, call, pos); handled {
			return value, err
		}
	}
	if value, handled, err := b.builtinCall(call, pos); handled {
		return value, err
	}
	callee, ok := call.Callee.(*jsast.JsIdent)
	if !ok {
		if member, isMember := call.Callee.(*jsast.JsMemberExpr); isMember && !member.Computed {
			if property, named := member.Prop.(*jsast.JsIdent); named {
				owner := "value"
				object, global := member.Obj.(*jsast.JsIdent)
				if global && ssrGlobalNamespaces[object.Name] && !b.shadowedGlobal(object.Name) {
					owner = object.Name
				} else if b.js && !ssrUnmodeledMethods[property.Name] {
					receiver, err := b.build(member.Obj, pos)
					if err != nil {
						return nil, err
					}
					return b.objectMethodCall(receiver, property.Name, call, pos)
				}
				return nil, ssrExprUnsupported(pos, "method %s.%s is not available during server rendering", owner, property.Name)
			}
		}
		return nil, ssrExprUnsupported(pos, "only named local formatter calls are available to static SSR")
	}
	reason := fmt.Sprintf("call %q", callee.Name)
	if binding, ok := b.ctx.Hosts[callee.Name]; ok && b.js {
		return b.hostCall(binding, call, reason, pos)
	}
	if ref, ok := b.ctx.ModuleRefs[callee.Name]; ok && b.js {
		if _, local := b.params[callee.Name]; !local {
			return b.moduleCall(ref, call, reason, pos)
		}
	}
	if param, ok := b.params[callee.Name]; ok && (b.funcCompiler != nil || b.js) {
		// A function value held by a local of a translated function.
		return b.valueCall(param, call, pos)
	}
	if component := ssrComponentFunctions(b.ctx.funcsPlan())[callee.Name]; component != nil && (!ssrInlinable(component) || b.ctx.Functions[callee.Name] == nil) && b.ctx.funcs != nil && b.js {
		value, err := b.compiledCall(callee.Name, call, pos)
		if err != nil {
			if symbol, bindable := b.ctx.HostSymbols[callee.Name]; bindable {
				return nil, b.hostMissing(err, symbol, call, component, reason)
			}
			return nil, ssrWithReason(err, err, reason)
		}
		return value, nil
	}
	function, ok := b.ctx.Functions[callee.Name]
	if !ok || function == nil {
		if symbol, bindable := b.ctx.HostSymbols[callee.Name]; bindable {
			problem := ssrExprUnsupported(pos, "it is defined in %s, and functions of JavaScript modules are translated natively only with task 045", symbol.Source)
			return nil, b.hostMissing(problem, symbol, call, b.ctx.HostDecls[callee.Name], reason)
		}
		return nil, ssrExprUnsupported(pos, "JavaScript call %q is not a supported local formatter", callee.Name)
	}
	symbol, bindable := b.ctx.HostSymbols[callee.Name]
	fail := func(format string, args ...interface{}) (SSRIRExpr, error) {
		problem := ssrExprUnsupported(pos, format, args...)
		b.locate(problem, function)
		if bindable {
			return nil, b.hostMissing(problem, symbol, call, function, reason)
		}
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
	result, ok := function.Body.Body[0].(*jsast.JsReturnStmt)
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
		name := analyzer.IdentName(parameter)
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
		if bindable {
			return nil, b.hostMissing(err, symbol, call, function, reason)
		}
		return nil, ssrWithReason(err, err, reason)
	}
	b.called(callee.Name, SSROutcomeNative, "")
	// What is known about a call result is not tracked (parity).
	return &ssrIRInline{ssrIRBase: b.base(pos, ssrIRFacts{}), Kind: "call", Name: callee.Name, Body: value}, nil
}

// hostMissing turns the failure of a bindable function into
// VELA-SSR-HOST-MISSING: the function needs a host implementation, whose
// file and signature (inferred from this call) the diagnostic gives.
func (b *ssrIRBuilder) hostMissing(cause error, symbol SSRHostSymbol, call *jsast.JsCallExpr, function *jsast.JsFuncDecl, reason string) error {
	suggestion := &ssrHostSuggestion{Symbol: symbol}
	for i, argument := range call.Args {
		name := fmt.Sprintf("arg%d", i+1)
		if function != nil && i < len(function.Params) {
			if param := analyzer.IdentName(function.Params[i]); param != "" {
				name = param
			}
		}
		facts := ssrIRFacts{}
		if value, err := b.build(argument, 0); err == nil {
			facts = value.irBase().Facts
		}
		suggestion.ParamNames = append(suggestion.ParamNames, name)
		suggestion.ParamTypes = append(suggestion.ParamTypes, ssrGoTypeForFacts(facts))
	}
	b.called(symbol.Name, SSROutcomeUnsupported, "")
	wrapped := ssrWithReason(cause, cause, reason).(*SSRExpressionError)
	wrapped.Code = SSRHostMissingCode
	wrapped.Host = suggestion
	if wrapped.Cause == "" {
		wrapped.Cause = wrapped.Message
	}
	if nested, ok := cause.(*SSRExpressionError); ok && nested.bare != "" {
		wrapped.Cause = nested.bare
	}
	wrapped.bare = wrapped.Cause
	wrapped.Message = fmt.Sprintf("%s has no server-native translation: %s", symbol.Name, wrapped.Cause)
	wrapped.Cause = wrapped.Message
	return wrapped
}

// hostCall lowers a call to a //vela:host function. Arguments the
// signature cannot take are build errors (VELA-SSR-HOST-SIG, H-8.3e);
// values of unproven type are converted at run time.
func (b *ssrIRBuilder) hostCall(binding *SSRHostBinding, call *jsast.JsCallExpr, reason string, pos int) (SSRIRExpr, error) {
	signatureError := func(format string, args ...interface{}) (SSRIRExpr, error) {
		problem := ssrExprUnsupported(pos, format, args...).(*SSRExpressionError)
		problem.Code = SSRHostSignatureCode
		b.called(binding.Symbol.Name, SSROutcomeUnsupported, binding.ImportPath+"."+binding.GoName)
		return nil, ssrWithReason(problem, nil, reason)
	}
	if binding.Problem != "" {
		return signatureError("host %s.%s (%s:%d) has an unusable signature: %s", binding.Package, binding.GoName, binding.File, binding.Line, binding.Problem)
	}
	arguments := make([]SSRIRExpr, 0, len(call.Args))
	for i, argument := range call.Args {
		if _, spread := argument.(*jsast.JsSpreadExpr); spread {
			return signatureError("spread arguments to host %s are not supported", binding.Symbol.Name)
		}
		value, err := b.build(argument, pos)
		if err != nil {
			return nil, err
		}
		if param, ok := ssrHostParamAt(binding, i); ok {
			if mismatch := ssrHostArgumentMismatch(param, value.irBase().Facts); mismatch != "" {
				return signatureError("argument %d of %s is %s, but %s takes %s %s", i+1, binding.Symbol.Name, mismatch, binding.GoName, param.Name, param.Type)
			}
		}
		arguments = append(arguments, value)
	}
	for i := len(call.Args); i < len(binding.Params); i++ {
		param := binding.Params[i]
		if binding.Variadic && i == len(binding.Params)-1 {
			break
		}
		if !param.AcceptsUndefined {
			return signatureError("%s is called with %d argument(s), so %s receives undefined, which Go type %s cannot hold; use vela.Optional[%s], a pointer, or any", binding.Symbol.Name, len(call.Args), param.Name, param.Type, param.Type)
		}
	}
	if b.hostImports == nil {
		b.hostImports = map[string]string{}
	}
	alias := ssrHostAlias(binding)
	b.hostImports[binding.ImportPath] = alias
	b.usedJS, b.throws = true, true
	b.called(binding.Symbol.Name, SSROutcomeHost, binding.ImportPath+"."+binding.GoName)
	return &ssrIRHost{ssrIRBase: b.base(pos, ssrIRFacts{}), Binding: binding, Alias: alias, Args: arguments, Site: b.site}, nil
}

func ssrHostParamAt(binding *SSRHostBinding, index int) (SSRHostParam, bool) {
	if index < len(binding.Params) {
		return binding.Params[index], true
	}
	if binding.Variadic && len(binding.Params) > 0 {
		return binding.Params[len(binding.Params)-1], true
	}
	return SSRHostParam{}, false
}

// ssrHostArgumentMismatch describes a proven argument type the parameter
// cannot take, or "".
func ssrHostArgumentMismatch(param SSRHostParam, facts ssrIRFacts) string {
	proven := facts.SemanticType()
	if proven == "unknown" || param.Kind == "any" {
		return ""
	}
	want := param.Kind
	if want == "time" {
		want = "string"
	}
	if proven == want {
		return ""
	}
	return "a " + proven
}

// ssrHostAlias is the import alias of a host package in generated code.
func ssrHostAlias(binding *SSRHostBinding) string {
	sum := 0
	for _, r := range binding.ImportPath {
		sum = (sum*31 + int(r)) % 9973
	}
	return fmt.Sprintf("host_%s_%d", binding.Package, sum)
}

// called records the outcome of a function call for the report.
func (b *ssrIRBuilder) called(name, outcome, host string) {
	if b.calls == nil {
		b.calls = map[string]ssrCallOutcome{}
	}
	previous, seen := b.calls[name]
	if !seen || previous.Outcome != SSROutcomeUnsupported {
		b.calls[name] = ssrCallOutcome{Outcome: outcome, Host: host}
	}
}

type ssrCallOutcome struct {
	Outcome string
	Host    string
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
func (b *ssrIRBuilder) locate(err error, node jsast.JsNode) {
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
func (b *ssrIRBuilder) localRef(name, kind string, initializer jsast.JsExpr, pos int, boolProp bool) (SSRIRExpr, error) {
	if binding, ok := b.ctx.Hosts[name]; ok && kind == "plain" && b.js {
		// A module value provided by a parameterless host (§8.7): the
		// prologue calls it once per render.
		if len(binding.Params) > 0 || binding.Problem != "" {
			problem := ssrExprUnsupported(pos, "host %s.%s (%s:%d) for the value %q must take no arguments", binding.Package, binding.GoName, binding.File, binding.Line, name).(*SSRExpressionError)
			problem.Code = SSRHostSignatureCode
			return nil, problem
		}
		b.inlined[name] = true
		if b.reads == nil {
			b.reads = map[string]bool{}
		}
		b.reads[name] = true
		b.called(name, SSROutcomeHost, binding.ImportPath+"."+binding.GoName)
		return &ssrIRGo{ssrIRBase: b.base(pos, ssrIRFacts{}), Expr: goast.NewIdent(ssrLocalName(name))}, nil
	}
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
	if b.ctx.funcs != nil && b.ctx.funcs.captured[name] {
		// Declared as any before the functions that read it.
		facts = ssrIRFacts{}
	}
	return &ssrIRGo{ssrIRBase: b.base(pos, facts), Expr: goast.NewIdent(ssrLocalName(name))}, nil
}

// ssrComponentContext is ctx without template bindings and function
// parameters: the scope of the component script.
func ssrComponentContext(ctx SSRExprContext) SSRExprContext {
	ctx.Bindings = map[string]goast.Expr{}
	return ctx
}

// ssrGlobalNamespaces are the global objects whose functions the
// catalogue lists (docs/SSR_TRANSLATION_MATRIX.md §2).
var ssrGlobalNamespaces = map[string]bool{"Math": true, "Number": true, "Object": true, "JSON": true, "Array": true, "String": true, "Promise": true, "Intl": true, "Date": true, "Map": true, "Set": true, "localStorage": true, "sessionStorage": true, "console": true, "window": true, "document": true}
