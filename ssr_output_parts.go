package main

import (
	goast "go/ast"
	"go/token"
	"html"
	"strconv"
	"strings"
)

// SSRRenderOutputPart is one typed write in a server renderer. Literal parts
// are already escaped markup; expression parts retain the cached template AST
// until they are translated to Go AST.
type SSRRenderOutputPart struct {
	Literal string
	Expr    *TemplateExpr
	Pos     int
}

func (part SSRRenderOutputPart) Dynamic() bool {
	return part.Expr != nil
}

// BuildSSRRenderOutputParts lowers the static HTML shape into literal writes
// and preserves dynamic text as typed expression writes. Blocks, components,
// and dynamic attributes deliberately remain diagnostics until their lowering
// semantics are implemented.
func BuildSSRRenderOutputParts(nodes []SSRPlanNode) ([]SSRRenderOutputPart, error) {
	builder := ssrOutputPartBuilder{}
	if err := builder.nodes(nodes); err != nil {
		return nil, err
	}
	return builder.parts, nil
}

type ssrOutputPartBuilder struct {
	parts []SSRRenderOutputPart
}

func (b *ssrOutputPartBuilder) literal(value string) {
	if value == "" {
		return
	}
	b.parts = append(b.parts, SSRRenderOutputPart{Literal: value})
}

func (b *ssrOutputPartBuilder) nodes(nodes []SSRPlanNode) error {
	for _, node := range nodes {
		if err := b.node(node); err != nil {
			return err
		}
	}
	return nil
}

func (b *ssrOutputPartBuilder) node(node SSRPlanNode) error {
	switch n := node.(type) {
	case *SSRPlanText:
		for _, part := range n.Parts {
			if part.Static {
				b.literal(part.Value)
				continue
			}
			b.parts = append(b.parts, SSRRenderOutputPart{Expr: part.Expr, Pos: part.Expr.Pos})
		}
		return nil
	case *SSRPlanElement:
		open, err := ssrStaticOpenTag(n)
		if err != nil {
			return err
		}
		b.literal(open)
		if err := b.nodes(n.Children); err != nil {
			return err
		}
		if !ssrVoidElement(n.Tag) {
			b.literal("</" + n.Tag + ">")
		}
		return nil
	default:
		return ssrExprUnsupported(0, "SSR node %T is not lowered yet", node)
	}
}

func ssrStaticOpenTag(node *SSRPlanElement) (string, error) {
	var out strings.Builder
	out.WriteByte('<')
	out.WriteString(node.Tag)
	for _, attr := range node.Attrs {
		if attr.Role != SSRAttrStatic {
			return "", ssrExprUnsupported(attr.Span.Start, "dynamic attribute %q is not lowered yet", attr.Name)
		}
		out.WriteByte(' ')
		out.WriteString(attr.Name)
		if attr.ValueKind != AttrValueMissing {
			out.WriteString("=\"")
			out.WriteString(html.EscapeString(attr.Value))
			out.WriteByte('"')
		}
	}
	out.WriteByte('>')
	return out.String(), nil
}

// BuildSSRGoFileWithOutputParts emits Go AST for static elements and escaped
// text interpolations. Every output write checks its io error before returning.
func BuildSSRGoFileWithOutputParts(plan *SSRRenderPlan, packageName string) (*goast.File, error) {
	parts, err := BuildSSRRenderOutputParts(plan.Nodes)
	if err != nil {
		return nil, err
	}
	file, err := BuildSSRGoSkeleton(plan, packageName)
	if err != nil {
		return nil, err
	}
	needsDynamic := false
	for _, part := range parts {
		needsDynamic = needsDynamic || part.Dynamic()
	}
	if needsDynamic {
		imports := file.Decls[0].(*goast.GenDecl)
		imports.Specs = append(imports.Specs,
			&goast.ImportSpec{Path: &goast.BasicLit{Kind: token.STRING, Value: `"fmt"`}},
			&goast.ImportSpec{Path: &goast.BasicLit{Kind: token.STRING, Value: `"html"`}},
		)
	}
	ctx := NewSSRExprContext(plan)
	body := make([]goast.Stmt, 0, len(parts)+1)
	for _, part := range parts {
		value := goast.Expr(&goast.BasicLit{Kind: token.STRING, Value: `""`})
		if part.Dynamic() {
			expr, err := TranslateSSRExpression(part.Expr, ctx)
			if err != nil {
				return nil, err
			}
			value = &goast.CallExpr{Fun: goSelector("html", "EscapeString"), Args: []goast.Expr{
				&goast.CallExpr{Fun: goSelector("fmt", "Sprint"), Args: []goast.Expr{expr}},
			}}
		} else {
			value = &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(part.Literal)}
		}
		body = append(body, goWriteSSRValue(value))
	}
	body = append(body, &goast.ReturnStmt{Results: []goast.Expr{goast.NewIdent("nil")}})
	file.Decls[len(file.Decls)-1].(*goast.FuncDecl).Body.List = body
	return file, nil
}

func goWriteSSRValue(value goast.Expr) goast.Stmt {
	write := &goast.CallExpr{Fun: goSelector("io", "WriteString"), Args: []goast.Expr{goast.NewIdent("w"), value}}
	assign := &goast.AssignStmt{Lhs: []goast.Expr{goast.NewIdent("_"), goast.NewIdent("err")}, Tok: token.DEFINE, Rhs: []goast.Expr{write}}
	return &goast.IfStmt{
		Init: assign,
		Cond: &goast.BinaryExpr{X: goast.NewIdent("err"), Op: token.NEQ, Y: goast.NewIdent("nil")},
		Body: &goast.BlockStmt{List: []goast.Stmt{
			&goast.ReturnStmt{Results: []goast.Expr{goast.NewIdent("err")}},
		}},
	}
}
