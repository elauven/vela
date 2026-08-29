package main

import (
	"bytes"
	"fmt"
	goast "go/ast"
	"go/format"
	"go/token"
	"strconv"
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
	Props    map[string]SSRPlanProp
	Bindings map[string]goast.Expr
}

func NewSSRExprContext(plan *SSRRenderPlan) SSRExprContext {
	ctx := SSRExprContext{Props: map[string]SSRPlanProp{}, Bindings: map[string]goast.Expr{}}
	if plan == nil {
		return ctx
	}
	for _, prop := range plan.Props {
		ctx.Props[prop.SourceName] = prop
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
		return nil, ssrExprUnsupported(pos, "ternary expressions require the SSR runtime and are not enabled yet")
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
		value, err := strconv.Unquote(node.Raw)
		if err != nil {
			return nil, ssrExprUnsupported(pos, "string literal cannot be translated safely")
		}
		return &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(value)}, nil
	case "number":
		return &goast.BasicLit{Kind: token.FLOAT, Value: node.Raw}, nil
	case "boolean", "null":
		return goast.NewIdent(node.Raw), nil
	default:
		return nil, ssrExprUnsupported(pos, "literal kind %q is not supported", node.Kind)
	}
}

func translateSSRMember(node *JsMemberExpr, ctx SSRExprContext, pos int) (goast.Expr, error) {
	if !node.Computed {
		if property, ok := node.Prop.(*JsIdent); ok && property.Name == "length" {
			object, err := translateSSRExpr(node.Obj, ctx, pos)
			if err != nil {
				return nil, err
			}
			return &goast.CallExpr{Fun: goast.NewIdent("len"), Args: []goast.Expr{object}}, nil
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
