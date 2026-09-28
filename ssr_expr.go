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
	Props             map[string]SSRPlanProp
	Bindings          map[string]goast.Expr
	ReactiveNames     map[string]bool
	StateInitializers map[string]JsExpr
}

func NewSSRExprContext(plan *SSRRenderPlan) SSRExprContext {
	ctx := SSRExprContext{
		Props:             map[string]SSRPlanProp{},
		Bindings:          map[string]goast.Expr{},
		ReactiveNames:     map[string]bool{},
		StateInitializers: map[string]JsExpr{},
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
		if binding, ok := ctx.Bindings[node.Name]; ok {
			return binding, nil
		}
		if prop, ok := ctx.Props[node.Name]; ok {
			return &goast.SelectorExpr{X: goast.NewIdent("props"), Sel: goast.NewIdent(prop.GoName)}, nil
		}
		if initializer, ok := ctx.StateInitializers[node.Name]; ok {
			value, err := translateSSRExpr(initializer, ctx, pos)
			if err != nil {
				return nil, ssrExprUnsupported(pos, "local reactive binding %q has no server-safe initializer: %v", node.Name, err)
			}
			return value, nil
		}
		if ctx.ReactiveNames[node.Name] {
			return nil, ssrExprUnsupported(pos, "local reactive binding %q has no server value; pass it as an exported prop or wait for hydration", node.Name)
		}
		return nil, ssrExprUnsupported(pos, "identifier %q is not an exported prop or server binding", node.Name)
	case *JsLiteral:
		return translateSSRLiteral(node, pos)
	case *JsUnaryExpr:
		x, err := translateSSRExpr(node.X, ctx, pos)
		if err != nil {
			return nil, err
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
		op, ok := ssrLogicalToken(node.Op)
		if !ok {
			return nil, ssrExprUnsupported(pos, "logical operator %q is not supported", node.Op)
		}
		return &goast.BinaryExpr{X: left, Op: op, Y: right}, nil
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
		return &goast.CallExpr{Fun: goSelector("vela", "Ternary"), Args: []goast.Expr{
			&goast.CallExpr{Fun: goSelector("vela", "Truthy"), Args: []goast.Expr{test}}, thenValue, elseValue,
		}}, nil
	case *JsCallExpr:
		return nil, ssrExprUnsupported(pos, "JavaScript calls are not available to static SSR")
	case *JsArrowFunc:
		return nil, ssrExprUnsupported(pos, "arrow functions are not available to static SSR")
	case *JsAssignExpr:
		return nil, ssrExprUnsupported(pos, "assignments are not available to static SSR")
	default:
		return nil, ssrExprUnsupported(pos, "JavaScript expression %T is not supported", expr)
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
	var out strings.Builder
	for index := 1; index < len(raw)-1; index++ {
		char := raw[index]
		if char != '\\' {
			out.WriteByte(char)
			continue
		}
		index++
		if index >= len(raw)-1 {
			return "", strconv.ErrSyntax
		}
		switch raw[index] {
		case '\'', '"', '\\':
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
		if object, ok := node.Obj.(*JsIdent); ok {
			if property, ok := node.Prop.(*JsIdent); ok {
				if prop, exists := ctx.Props[object.Name]; exists && prop.Type == SSRTypeObject {
					return &goast.IndexExpr{
						X:     &goast.SelectorExpr{X: goast.NewIdent("props"), Sel: goast.NewIdent(prop.GoName)},
						Index: &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(property.Name)},
					}, nil
				}
			}
		}

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

func ssrLogicalToken(op string) (token.Token, bool) {
	switch op {
	case "&&":
		return token.LAND, true
	case "||":
		return token.LOR, true
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
