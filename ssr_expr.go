package main

import (
	"bytes"
	"fmt"
	goast "go/ast"
	"go/format"
	"go/token"
	"strconv"
	"strings"
)

const SSRExpressionDiagnosticCode = "VELA-SSR-EXPR"

// SSRExpressionError carries a component-template-relative position. The CLI
// integration will translate it through DiagSet; keeping it structured here
// prevents the backend from degrading errors into unpositioned strings.
type SSRExpressionError struct {
	Pos     int
	Message string
}

func (e *SSRExpressionError) Error() string {
	return SSRExpressionDiagnosticCode + ": " + e.Message
}

// SSRExprContext maps template identifiers to already-typed Go AST values.
// Props are constructed by NewSSRExprContext; each/await lowering will extend
// Bindings with lexical row and branch names rather than reparsing expressions.
type SSRExprContext struct {
	Props               map[string]SSRPlanProp
	Bindings            map[string]goast.Expr
	ReactiveNames       map[string]bool
	StateInitializers   map[string]JsExpr
	DerivedInitializers map[string]JsExpr
	PlainInitializers   map[string]JsExpr
	Functions           map[string]*JsFuncDecl
	resolving           map[string]bool
	resolvingFunctions  map[string]bool
}

func NewSSRExprContext(plan *SSRRenderPlan) SSRExprContext {
	ctx := SSRExprContext{
		Props:               map[string]SSRPlanProp{},
		Bindings:            map[string]goast.Expr{},
		ReactiveNames:       map[string]bool{},
		StateInitializers:   map[string]JsExpr{},
		DerivedInitializers: map[string]JsExpr{},
		PlainInitializers:   map[string]JsExpr{},
		Functions:           map[string]*JsFuncDecl{},
	}
	if plan == nil {
		return ctx
	}
	for _, prop := range plan.Props {
		ctx.Props[prop.SourceName] = prop
	}
	for _, name := range plan.ReactiveNames {
		ctx.ReactiveNames[name] = true
	}
	for name, initializer := range plan.StateInitializers {
		ctx.StateInitializers[name] = initializer
	}
	for name, initializer := range plan.DerivedInitializers {
		ctx.DerivedInitializers[name] = initializer
	}
	for name, initializer := range plan.PlainInitializers {
		ctx.PlainInitializers[name] = initializer
	}
	for name, function := range plan.Functions {
		ctx.Functions[name] = function
	}
	return ctx
}

// TranslateSSRExpression lowers one cached TemplateExpr into Go AST. It does
// not accept source text, so all future renderer paths share the parser and
// expression ownership of ComponentAST.
func TranslateSSRExpression(expr *TemplateExpr, ctx SSRExprContext) (goast.Expr, error) {
	if expr == nil || expr.Root() == nil {
		return nil, &SSRExpressionError{Message: "template expression has no cached AST"}
	}
	return translateSSRExpr(expr.Root(), ctx, expr.Pos)
}

func translateSSRExpr(expr JsExpr, ctx SSRExprContext, pos int) (goast.Expr, error) {
	switch node := expr.(type) {
	case *JsIdent:
		if node.Name == "undefined" {
			return &goast.CallExpr{Fun: goSelector("vela", "Undefined")}, nil
		}
		if binding, ok := ctx.Bindings[node.Name]; ok {
			return binding, nil
		}
		if prop, ok := ctx.Props[node.Name]; ok {
			return &goast.SelectorExpr{X: goast.NewIdent("props"), Sel: goast.NewIdent(prop.GoName)}, nil
		}
		if initializer, ok := ctx.StateInitializers[node.Name]; ok {
			return translateSSRLocalInitializer(node.Name, "reactive", initializer, ctx, pos)
		}
		if initializer, ok := ctx.DerivedInitializers[node.Name]; ok {
			return translateSSRLocalInitializer(node.Name, "derived", initializer, ctx, pos)
		}
		if initializer, ok := ctx.PlainInitializers[node.Name]; ok {
			return translateSSRLocalInitializer(node.Name, "plain", initializer, ctx, pos)
		}
		if ctx.ReactiveNames[node.Name] {
			return nil, ssrExprUnsupported(pos, "local reactive binding %q has no server value; pass it as an exported prop or wait for hydration", node.Name)
		}
		return nil, ssrExprUnsupported(pos, "identifier %q is not an exported prop or server binding", node.Name)
	case *JsLiteral:
		return translateSSRLiteral(node, pos)
	case *JsArrayExpr:
		return translateSSRArrayLiteral(node, ctx, pos)
	case *JsObjectExpr:
		return translateSSRObjectLiteral(node, ctx, pos)
	case *JsTemplateLit:
		return translateSSRTemplateLiteral(node, ctx, pos)
	case *JsUnaryExpr:
		x, err := translateSSRExpr(node.X, ctx, pos)
		if err != nil {
			return nil, err
		}
		// JavaScript's ! accepts every value and first applies JS truthiness.
		// Go's ! only accepts bool, so lowering it directly would reject valid
		// expressions such as !items.length.
		if node.Op == "!" {
			return &goast.UnaryExpr{Op: token.NOT, X: &goast.CallExpr{
				Fun:  goSelector("vela", "Truthy"),
				Args: []goast.Expr{x},
			}}, nil
		}
		op, ok := ssrUnaryToken(node.Op)
		if !ok {
			return nil, ssrExprUnsupported(pos, "unary operator %q is not supported", node.Op)
		}
		return &goast.UnaryExpr{Op: op, X: x}, nil
	case *JsBinaryExpr:
		left, err := translateSSRExpr(node.L, ctx, pos)
		if err != nil {
			return nil, err
		}
		right, err := translateSSRExpr(node.R, ctx, pos)
		if err != nil {
			return nil, err
		}
		op, ok := ssrBinaryToken(node.Op)
		if !ok {
			return nil, ssrExprUnsupported(pos, "binary operator %q is not supported", node.Op)
		}
		// JavaScript + concatenates when either operand is a string. Go would
		// reject a valid source expression such as "item " + props.Count, so
		// use the already-admitted JS text conversion on both sides whenever
		// the parser-owned expression tree proves one side string-like.
		if node.Op == "+" {
			leftString := ssrExprKnownString(node.L, ctx, map[string]bool{})
			rightString := ssrExprKnownString(node.R, ctx, map[string]bool{})
			switch {
			case leftString || rightString:
				if leftString != rightString {
					left = &goast.CallExpr{Fun: goSelector("vela", "String"), Args: []goast.Expr{left}}
					right = &goast.CallExpr{Fun: goSelector("vela", "String"), Args: []goast.Expr{right}}
				}
			case ssrExprKnownNumber(node.L, ctx, map[string]bool{}) && ssrExprKnownNumber(node.R, ctx, map[string]bool{}):
				left = &goast.CallExpr{Fun: goast.NewIdent("float64"), Args: []goast.Expr{left}}
				right = &goast.CallExpr{Fun: goast.NewIdent("float64"), Args: []goast.Expr{right}}
			default:
				return nil, ssrExprUnsupported(pos, "operator + requires two known numbers or at least one known string during SSR")
			}
		}
		return &goast.BinaryExpr{X: left, Op: op, Y: right}, nil
	case *JsLogicalExpr:
		left, err := translateSSRExpr(node.L, ctx, pos)
		if err != nil {
			return nil, err
		}
		right, err := translateSSRExpr(node.R, ctx, pos)
		if err != nil {
			return nil, err
		}
		function := ""
		switch node.Op {
		case "&&":
			function = "AndLazy"
		case "||":
			function = "OrLazy"
		default:
			return nil, ssrExprUnsupported(pos, "logical operator %q is not supported", node.Op)
		}
		return &goast.CallExpr{Fun: goSelector("vela", function), Args: []goast.Expr{
			ssrLazyExpr(left),
			ssrLazyExpr(right),
		}}, nil
	case *JsMemberExpr:
		return translateSSRMember(node, ctx, pos)
	case *JsConditionalExpr:
		test, err := translateSSRExpr(node.Test, ctx, pos)
		if err != nil {
			return nil, err
		}
		thenValue, err := translateSSRExpr(node.Then, ctx, pos)
		if err != nil {
			return nil, err
		}
		elseValue, err := translateSSRExpr(node.Else, ctx, pos)
		if err != nil {
			return nil, err
		}
		return &goast.CallExpr{Fun: goSelector("vela", "TernaryLazy"), Args: []goast.Expr{
			ssrLazyExpr(test),
			ssrLazyExpr(thenValue),
			ssrLazyExpr(elseValue),
		}}, nil
	case *JsCallExpr:
		return translateSSRNamedFunctionCall(node, ctx, pos)
	case *JsArrowFunc:
		return nil, ssrExprUnsupported(pos, "arrow functions are not available to static SSR")
	case *JsAssignExpr:
		return nil, ssrExprUnsupported(pos, "assignments are not available to static SSR")
	default:
		return nil, ssrExprUnsupported(pos, "JavaScript expression %T is not supported", expr)
	}
}

func ssrExprKnownString(expr JsExpr, ctx SSRExprContext, seen map[string]bool) bool {
	switch node := expr.(type) {
	case *JsLiteral:
		return node.Kind == "string"
	case *JsTemplateLit:
		return true
	case *JsIdent:
		if seen[node.Name] {
			return false
		}
		if binding, ok := ctx.Bindings[node.Name]; ok {
			return ssrGoExprKnownString(binding, ctx)
		}
		if prop, ok := ctx.Props[node.Name]; ok {
			return prop.Type == SSRTypeString
		}
		seen[node.Name] = true
		defer delete(seen, node.Name)
		if initializer, ok := ctx.StateInitializers[node.Name]; ok {
			return ssrExprKnownString(initializer, ctx, seen)
		}
		if initializer, ok := ctx.DerivedInitializers[node.Name]; ok {
			return ssrExprKnownString(initializer, ctx, seen)
		}
		if initializer, ok := ctx.PlainInitializers[node.Name]; ok {
			return ssrExprKnownString(initializer, ctx, seen)
		}
	case *JsBinaryExpr:
		return node.Op == "+" && (ssrExprKnownString(node.L, ctx, seen) || ssrExprKnownString(node.R, ctx, seen))
	case *JsConditionalExpr:
		return ssrExprKnownString(node.Then, ctx, seen) && ssrExprKnownString(node.Else, ctx, seen)
	}
	return false
}

func ssrGoExprKnownString(expr goast.Expr, ctx SSRExprContext) bool {
	switch node := expr.(type) {
	case *goast.BasicLit:
		return node.Kind == token.STRING
	case *goast.SelectorExpr:
		base, ok := node.X.(*goast.Ident)
		if !ok || base.Name != "props" {
			return false
		}
		for _, prop := range ctx.Props {
			if prop.GoName == node.Sel.Name {
				return prop.Type == SSRTypeString
			}
		}
	case *goast.BinaryExpr:
		return node.Op == token.ADD && ssrGoExprKnownString(node.X, ctx) && ssrGoExprKnownString(node.Y, ctx)
	}
	return false
}

func ssrExprKnownNumber(expr JsExpr, ctx SSRExprContext, seen map[string]bool) bool {
	switch node := expr.(type) {
	case *JsLiteral:
		return node.Kind == "number"
	case *JsIdent:
		if seen[node.Name] {
			return false
		}
		if binding, ok := ctx.Bindings[node.Name]; ok {
			return ssrGoExprKnownNumber(binding, ctx)
		}
		if prop, ok := ctx.Props[node.Name]; ok {
			return prop.Type == SSRTypeInt || prop.Type == SSRTypeFloat
		}
		seen[node.Name] = true
		defer delete(seen, node.Name)
		for _, initializers := range []map[string]JsExpr{ctx.StateInitializers, ctx.DerivedInitializers, ctx.PlainInitializers} {
			if initializer, ok := initializers[node.Name]; ok {
				return ssrExprKnownNumber(initializer, ctx, seen)
			}
		}
	case *JsUnaryExpr:
		return (node.Op == "+" || node.Op == "-") && ssrExprKnownNumber(node.X, ctx, seen)
	case *JsBinaryExpr:
		switch node.Op {
		case "+", "-", "*", "/", "%":
			return ssrExprKnownNumber(node.L, ctx, seen) && ssrExprKnownNumber(node.R, ctx, seen)
		}
	case *JsConditionalExpr:
		return ssrExprKnownNumber(node.Then, ctx, seen) && ssrExprKnownNumber(node.Else, ctx, seen)
	}
	return false
}

func ssrGoExprKnownNumber(expr goast.Expr, ctx SSRExprContext) bool {
	switch node := expr.(type) {
	case *goast.BasicLit:
		return node.Kind == token.INT || node.Kind == token.FLOAT
	case *goast.SelectorExpr:
		base, ok := node.X.(*goast.Ident)
		if !ok || base.Name != "props" {
			return false
		}
		for _, prop := range ctx.Props {
			if prop.GoName == node.Sel.Name {
				return prop.Type == SSRTypeInt || prop.Type == SSRTypeFloat
			}
		}
	case *goast.CallExpr:
		name, ok := node.Fun.(*goast.Ident)
		return ok && name.Name == "float64"
	case *goast.BinaryExpr:
		return ssrGoExprKnownNumber(node.X, ctx) && ssrGoExprKnownNumber(node.Y, ctx)
	}
	return false
}

// translateSSRNamedFunctionCall deliberately admits a small AST-only function
// subset: top-level named, synchronous, non-generator declarations with one
// return expression and identifier parameters. It is an expression inliner,
// not a JavaScript evaluator: argument and return trees continue through the
// existing checked translator, while parameter names receive lexical Go-AST
// bindings. Calls, writes, async control flow, and external functions remain
// explicit diagnostics until their semantics have an independent contract.
func translateSSRNamedFunctionCall(call *JsCallExpr, ctx SSRExprContext, pos int) (goast.Expr, error) {
	if call.Optional {
		return nil, ssrExprUnsupported(pos, "optional JavaScript calls are not available to static SSR")
	}
	callee, ok := call.Callee.(*JsIdent)
	if !ok {
		return nil, ssrExprUnsupported(pos, "only named local formatter calls are available to static SSR")
	}
	function, ok := ctx.Functions[callee.Name]
	if !ok || function == nil {
		return nil, ssrExprUnsupported(pos, "JavaScript call %q is not a supported local formatter", callee.Name)
	}
	if function.Async || function.Generator {
		return nil, ssrExprUnsupported(pos, "local formatter %q cannot be async or a generator during SSR", callee.Name)
	}
	if len(function.Params) != len(call.Args) {
		return nil, ssrExprUnsupported(pos, "local formatter %q expects %d argument(s), got %d", callee.Name, len(function.Params), len(call.Args))
	}
	if function.Body == nil || len(function.Body.Body) != 1 {
		return nil, ssrExprUnsupported(pos, "local formatter %q must contain exactly one return expression", callee.Name)
	}
	result, ok := function.Body.Body[0].(*JsReturnStmt)
	if !ok || result.Arg == nil {
		return nil, ssrExprUnsupported(pos, "local formatter %q must contain exactly one return expression", callee.Name)
	}
	if ctx.resolvingFunctions == nil {
		ctx.resolvingFunctions = map[string]bool{}
	}
	if ctx.resolvingFunctions[callee.Name] {
		return nil, ssrExprUnsupported(pos, "local formatter %q is recursively referenced during SSR", callee.Name)
	}
	arguments := make([]goast.Expr, len(call.Args))
	for index, argument := range call.Args {
		value, err := translateSSRExpr(argument, ctx, pos)
		if err != nil {
			return nil, err
		}
		arguments[index] = value
	}
	inner := ssrContextWithBindings(ctx)
	inner.resolvingFunctions = ctx.resolvingFunctions
	inner.resolvingFunctions[callee.Name] = true
	defer delete(inner.resolvingFunctions, callee.Name)
	for index, parameter := range function.Params {
		name := identName(parameter)
		if name == "" {
			return nil, ssrExprUnsupported(pos, "local formatter %q has a non-identifier parameter", callee.Name)
		}
		inner.Bindings[name] = arguments[index]
	}
	return translateSSRExpr(result.Arg, inner, pos)
}

// ssrLazyExpr represents a source expression as a zero-argument Go closure.
// It is used only at JavaScript short-circuit boundaries, where Go evaluates
// function arguments eagerly but the runtime must decide which closure body
// to execute. Keeping this in AST form avoids recreating source text and
// guarantees that an unsafe unselected member/index read is never evaluated.
func ssrLazyExpr(value goast.Expr) goast.Expr {
	return &goast.FuncLit{
		Type: &goast.FuncType{
			Params:  &goast.FieldList{},
			Results: &goast.FieldList{List: []*goast.Field{{Type: goast.NewIdent("any")}}},
		},
		Body: &goast.BlockStmt{List: []goast.Stmt{
			&goast.ReturnStmt{Results: []goast.Expr{value}},
		}},
	}
}

func translateSSRLocalInitializer(name, kind string, initializer JsExpr, ctx SSRExprContext, pos int) (goast.Expr, error) {
	if ctx.resolving == nil {
		ctx.resolving = map[string]bool{}
	}
	if ctx.resolving[name] {
		return nil, ssrExprUnsupported(pos, "local %s binding %q has a circular server initializer", kind, name)
	}
	ctx.resolving[name] = true
	defer delete(ctx.resolving, name)
	value, err := translateSSRExpr(initializer, ctx, pos)
	if err != nil {
		return nil, ssrExprUnsupported(pos, "local %s binding %q has no server-safe initializer: %v", kind, name, err)
	}
	return value, nil
}

// translateSSRTemplateLiteral consumes the parser-owned quasi/expression
// structure. TemplateString owns JavaScript's null/undefined conversion, so
// every expression that is otherwise available to SSR may be interpolated.
// This keeps an interpolation on the same cached-AST lowering path as the
// equivalent standalone template expression.
func translateSSRTemplateLiteral(node *JsTemplateLit, ctx SSRExprContext, pos int) (goast.Expr, error) {
	if len(node.Quasis) != len(node.Exprs)+1 {
		return nil, ssrExprUnsupported(pos, "template literal has an invalid parser structure")
	}
	values := make([]goast.Expr, 0, len(node.Quasis)+len(node.Exprs))
	for index, quasi := range node.Quasis {
		text, err := decodeSSRTemplateQuasi(quasi)
		if err != nil {
			return nil, ssrExprUnsupported(pos, "template literal cannot be translated safely")
		}
		values = append(values, &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(text)})
		if index == len(node.Exprs) {
			continue
		}
		value, err := translateSSRExpr(node.Exprs[index], ctx, pos)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if len(node.Exprs) == 0 {
		return values[0], nil
	}
	return &goast.CallExpr{Fun: goSelector("vela", "TemplateString"), Args: values}, nil
}

// translateSSRArrayLiteral preserves JavaScript array order and holes using a
// []any composite literal. Vela.String already renders nil array members as
// empty fields, so `[1, , 3]` reaches SSR text output as `1,,3`, matching
// Array.prototype.toString for the admitted non-spread subset.
func translateSSRArrayLiteral(node *JsArrayExpr, ctx SSRExprContext, pos int) (goast.Expr, error) {
	elements := make([]goast.Expr, 0, len(node.Elems))
	for _, element := range node.Elems {
		if element == nil {
			elements = append(elements, goast.NewIdent("nil"))
			continue
		}
		if _, spread := element.(*JsSpreadExpr); spread {
			return nil, ssrExprUnsupported(pos, "array spread is not available to static SSR")
		}
		value, err := translateSSRExpr(element, ctx, pos)
		if err != nil {
			return nil, err
		}
		elements = append(elements, value)
	}
	return &goast.CompositeLit{
		Type: &goast.ArrayType{Elt: goast.NewIdent("any")},
		Elts: elements,
	}, nil
}

// translateSSRObjectLiteral admits ordinary non-computed data properties with
// identifier or string keys. A map[string]any is the existing SSR boundary
// representation for JavaScript objects, and it remains readable through
// vela.Get. Spread, accessors, methods, and computed property names require
// JavaScript object semantics that must be specified before they are lowered.
func translateSSRObjectLiteral(node *JsObjectExpr, ctx SSRExprContext, pos int) (goast.Expr, error) {
	elements := make([]goast.Expr, 0, len(node.Props))
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
		value, err := translateSSRExpr(property.Value, ctx, pos)
		if err != nil {
			return nil, err
		}
		entry := &goast.KeyValueExpr{
			Key:   &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(key)},
			Value: value,
		}
		if previous, duplicate := indices[key]; duplicate {
			elements[previous] = entry
			continue
		}
		indices[key] = len(elements)
		elements = append(elements, entry)
	}
	return &goast.CompositeLit{
		Type: &goast.MapType{Key: goast.NewIdent("string"), Value: goast.NewIdent("any")},
		Elts: elements,
	}, nil
}

func translateSSRObjectKey(key JsExpr, pos int) (string, error) {
	switch node := key.(type) {
	case *JsIdent:
		return node.Name, nil
	case *JsLiteral:
		if node.Kind != "string" {
			return "", ssrExprUnsupported(pos, "only identifier and string object property names are available to static SSR")
		}
		if node.Decoded {
			return node.Value, nil
		}
		value, err := decodeSSRJSStringLiteral(node.Raw)
		if err != nil {
			return "", ssrExprUnsupported(pos, "object property name cannot be translated safely")
		}
		return value, nil
	default:
		return "", ssrExprUnsupported(pos, "only identifier and string object property names are available to static SSR")
	}
}

func translateSSRLiteral(node *JsLiteral, pos int) (goast.Expr, error) {
	switch node.Kind {
	case "string":
		if node.Decoded {
			return &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(node.Value)}, nil
		}
		value, err := decodeSSRJSStringLiteral(node.Raw)
		if err != nil {
			return nil, ssrExprUnsupported(pos, "string literal cannot be translated safely")
		}
		return &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(value)}, nil
	case "number":
		return &goast.BasicLit{Kind: token.FLOAT, Value: node.Raw}, nil
	case "boolean":
		return goast.NewIdent(node.Raw), nil
	case "null":
		return goast.NewIdent("nil"), nil
	default:
		return nil, ssrExprUnsupported(pos, "literal kind %q is not supported", node.Kind)
	}
}

// decodeSSRJSStringLiteral decodes the parser-owned token spelling without
// reparsing template/script source. Go's strconv.Unquote cannot accept a JS
// single-quoted string, so the small baseline decoder handles the shared JS
// escapes explicitly and leaves broader escape grammar for the JS capability
// matrix.
func decodeSSRJSStringLiteral(raw string) (string, error) {
	if len(raw) < 2 || (raw[0] != '\'' && raw[0] != '"') || raw[len(raw)-1] != raw[0] {
		return "", strconv.ErrSyntax
	}
	return decodeSSRJSStringContent(raw[1:len(raw)-1], false)
}

func decodeSSRTemplateQuasi(raw string) (string, error) {
	return decodeSSRJSStringContent(raw, true)
}

func decodeSSRJSStringContent(raw string, template bool) (string, error) {
	var out strings.Builder
	for index := 0; index < len(raw); index++ {
		char := raw[index]
		if char != '\\' {
			out.WriteByte(char)
			continue
		}
		index++
		if index >= len(raw) {
			return "", strconv.ErrSyntax
		}
		switch raw[index] {
		case '\'', '"', '\\':
			out.WriteByte(raw[index])
		case '`', '$':
			if !template {
				return "", strconv.ErrSyntax
			}
			out.WriteByte(raw[index])
		case 'n':
			out.WriteByte('\n')
		case 'r':
			out.WriteByte('\r')
		case 't':
			out.WriteByte('\t')
		case 'b':
			out.WriteByte('\b')
		case 'f':
			out.WriteByte('\f')
		case 'v':
			out.WriteByte('\v')
		case '0':
			out.WriteByte(0)
		default:
			return "", strconv.ErrSyntax
		}
	}
	return out.String(), nil
}

func translateSSRMember(node *JsMemberExpr, ctx SSRExprContext, pos int) (goast.Expr, error) {
	if !node.Computed {
		if property, ok := node.Prop.(*JsIdent); ok && property.Name == "length" {
			object, err := translateSSRExpr(node.Obj, ctx, pos)
			if err != nil {
				return nil, err
			}
			return &goast.CallExpr{Fun: goSelector("vela", "Length"), Args: []goast.Expr{object}}, nil
		}
		if property, ok := node.Prop.(*JsIdent); ok {
			object, err := translateSSRExpr(node.Obj, ctx, pos)
			if err != nil {
				return nil, err
			}
			return &goast.CallExpr{Fun: goSelector("vela", "Get"), Args: []goast.Expr{
				object,
				&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(property.Name)},
			}}, nil
		}
	}
	if node.Computed {
		object, err := translateSSRExpr(node.Obj, ctx, pos)
		if err != nil {
			return nil, err
		}
		index, err := translateSSRExpr(node.Prop, ctx, pos)
		if err != nil {
			return nil, err
		}
		return &goast.IndexExpr{X: object, Index: index}, nil
	}
	return nil, ssrExprUnsupported(pos, "member access is not available until the prop has a declared object type")
}

func ssrUnaryToken(op string) (token.Token, bool) {
	switch op {
	case "!":
		return token.NOT, true
	case "-":
		return token.SUB, true
	case "+":
		return token.ADD, true
	default:
		return token.ILLEGAL, false
	}
}

func ssrBinaryToken(op string) (token.Token, bool) {
	switch op {
	case "+":
		return token.ADD, true
	case "-":
		return token.SUB, true
	case "*":
		return token.MUL, true
	case "/":
		return token.QUO, true
	case "%":
		return token.REM, true
	case "<":
		return token.LSS, true
	case "<=":
		return token.LEQ, true
	case ">":
		return token.GTR, true
	case ">=":
		return token.GEQ, true
	case "==", "===":
		return token.EQL, true
	case "!=", "!==":
		return token.NEQ, true
	default:
		return token.ILLEGAL, false
	}
}

func ssrExprUnsupported(pos int, format string, args ...interface{}) error {
	return &SSRExpressionError{Pos: pos, Message: fmt.Sprintf(format, args...)}
}

func FormatSSRGoExpr(expr goast.Expr) (string, error) {
	if expr == nil {
		return "", fmt.Errorf("nil Go expression")
	}
	var out bytes.Buffer
	if err := format.Node(&out, token.NewFileSet(), expr); err != nil {
		return "", err
	}
	return out.String(), nil
}
