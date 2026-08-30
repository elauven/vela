package main

import (
	goast "go/ast"
	"go/token"
	"html"
	"strconv"
)

// BuildSSRGoFile emits the currently supported server render plan through
// standard Go AST nodes. It supports static elements, escaped text and
// attributes, and conditional template branches. Other plan node kinds keep
// producing explicit diagnostics until their renderer semantics exist.
func BuildSSRGoFile(plan *SSRRenderPlan, packageName string) (*goast.File, error) {
	file, err := BuildSSRGoSkeleton(plan, packageName)
	if err != nil {
		return nil, err
	}
	emitter := ssrGoEmitter{ctx: NewSSRExprContext(plan)}
	body, err := emitter.nodes(plan.Nodes)
	if err != nil {
		return nil, err
	}
	if emitter.needsEscaping {
		imports := file.Decls[0].(*goast.GenDecl)
		imports.Specs = append(imports.Specs,
			&goast.ImportSpec{Path: &goast.BasicLit{Kind: token.STRING, Value: `"fmt"`}},
			&goast.ImportSpec{Path: &goast.BasicLit{Kind: token.STRING, Value: `"html"`}},
		)
	}
	body = append(body, &goast.ReturnStmt{Results: []goast.Expr{goast.NewIdent("nil")}})
	file.Decls[len(file.Decls)-1].(*goast.FuncDecl).Body.List = body
	return file, nil
}

type ssrGoEmitter struct {
	ctx           SSRExprContext
	needsEscaping bool
}

func (e *ssrGoEmitter) nodes(nodes []SSRPlanNode) ([]goast.Stmt, error) {
	body := make([]goast.Stmt, 0, len(nodes))
	for _, node := range nodes {
		statements, err := e.node(node)
		if err != nil {
			return nil, err
		}
		body = append(body, statements...)
	}
	return body, nil
}

func (e *ssrGoEmitter) node(node SSRPlanNode) ([]goast.Stmt, error) {
	switch n := node.(type) {
	case *SSRPlanText:
		return e.text(n)
	case *SSRPlanElement:
		return e.element(n)
	case *SSRPlanIf:
		return e.ifBlock(n)
	default:
		return nil, ssrExprUnsupported(0, "SSR node %T is not lowered yet", node)
	}
}

func (e *ssrGoEmitter) text(node *SSRPlanText) ([]goast.Stmt, error) {
	body := make([]goast.Stmt, 0, len(node.Parts))
	for _, part := range node.Parts {
		value, err := e.outputValue(part.Static, part.Value, part.Expr)
		if err != nil {
			return nil, err
		}
		body = append(body, goWriteSSRValue(value))
	}
	return body, nil
}

func (e *ssrGoEmitter) element(node *SSRPlanElement) ([]goast.Stmt, error) {
	body := make([]goast.Stmt, 0, len(node.Attrs)+len(node.Children)+2)
	open := "<" + node.Tag
	for _, attr := range node.Attrs {
		switch attr.Role {
		case SSRAttrStatic:
			open += " " + attr.Name
			if attr.ValueKind != AttrValueMissing {
				open += "=\"" + html.EscapeString(attr.Value) + "\""
			}
		case SSRAttrDynamic:
			if attr.Expr == nil {
				return nil, ssrExprUnsupported(attr.Span.Start, "dynamic attribute %q has no cached expression", attr.Name)
			}
			open += " " + attr.Name + "=\""
			body = append(body, goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(open)}))
			value, err := e.outputValue(false, "", attr.Expr)
			if err != nil {
				return nil, err
			}
			body = append(body, goWriteSSRValue(value))
			open = "\""
		default:
			return nil, ssrExprUnsupported(attr.Span.Start, "attribute %q with role %d is not lowered yet", attr.Name, attr.Role)
		}
	}
	open += ">"
	body = append(body, goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(open)}))
	children, err := e.nodes(node.Children)
	if err != nil {
		return nil, err
	}
	body = append(body, children...)
	if !ssrVoidElement(node.Tag) {
		body = append(body, goWriteSSRValue(&goast.BasicLit{Kind: token.STRING, Value: strconv.Quote("</" + node.Tag + ">")}))
	}
	return body, nil
}

func (e *ssrGoEmitter) ifBlock(node *SSRPlanIf) ([]goast.Stmt, error) {
	var first *goast.IfStmt
	var previous *goast.IfStmt
	for _, branch := range node.Branches {
		body, err := e.nodes(branch.Children)
		if err != nil {
			return nil, err
		}
		if branch.Condition == nil {
			if previous == nil {
				return nil, ssrExprUnsupported(branch.Span.Start, "SSR if block has an unconditional first branch")
			}
			previous.Else = &goast.BlockStmt{List: body}
			continue
		}
		condition, err := TranslateSSRExpression(branch.Condition, e.ctx)
		if err != nil {
			return nil, err
		}
		current := &goast.IfStmt{Cond: condition, Body: &goast.BlockStmt{List: body}}
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
	return []goast.Stmt{first}, nil
}

func (e *ssrGoEmitter) outputValue(static bool, literal string, expr *TemplateExpr) (goast.Expr, error) {
	if static {
		return &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(literal)}, nil
	}
	value, err := TranslateSSRExpression(expr, e.ctx)
	if err != nil {
		return nil, err
	}
	e.needsEscaping = true
	return &goast.CallExpr{Fun: goSelector("html", "EscapeString"), Args: []goast.Expr{
		&goast.CallExpr{Fun: goSelector("fmt", "Sprint"), Args: []goast.Expr{value}},
	}}, nil
}
