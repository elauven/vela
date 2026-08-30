package main

import (
	"fmt"
	goast "go/ast"
	"go/token"
	"html"
	"strconv"
	"strings"
)

func goWriteStringLiteral(value string) goast.Stmt {
	return &goast.ExprStmt{X: &goast.CallExpr{Fun: goSelector("io", "WriteString"), Args: []goast.Expr{goast.NewIdent("w"), &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(value)}}}}
}

func BuildStaticSSRGoFile(plan *SSRRenderPlan, packageName string) (*goast.File, error) {
	file, err := BuildSSRGoSkeleton(plan, packageName)
	if err != nil {
		return nil, err
	}
	markup, err := renderStaticSSRNodes(plan.Nodes)
	if err != nil {
		return nil, err
	}
	render := file.Decls[len(file.Decls)-1].(*goast.FuncDecl)
	render.Body.List = []goast.Stmt{goWriteStringLiteral(markup), &goast.ReturnStmt{Results: []goast.Expr{goast.NewIdent("nil")}}}
	return file, nil
}
func renderStaticSSRNodes(nodes []SSRPlanNode) (string, error) {
	var out strings.Builder
	for _, n := range nodes {
		s, err := renderStaticSSRNode(n)
		if err != nil {
			return "", err
		}
		out.WriteString(s)
	}
	return out.String(), nil
}
func renderStaticSSRNode(node SSRPlanNode) (string, error) {
	switch n := node.(type) {
	case *SSRPlanText:
		var out strings.Builder
		for _, p := range n.Parts {
			if !p.Static {
				return "", ssrExprUnsupported(p.Expr.Pos, "dynamic text is not lowered yet")
			}
			out.WriteString(p.Value)
		}
		return out.String(), nil
	case *SSRPlanElement:
		var out strings.Builder
		out.WriteByte(60)
		out.WriteString(n.Tag)
		for _, a := range n.Attrs {
			if a.Role == SSRAttrEvent {
				continue
			}
			if a.Role != SSRAttrStatic {
				return "", ssrExprUnsupported(a.Span.Start, "dynamic attribute %q is not lowered yet", a.Name)
			}
			out.WriteByte(32)
			out.WriteString(a.Name)
			if a.ValueKind != AttrValueMissing {
				out.WriteByte(61)
				out.WriteByte(34)
				out.WriteString(html.EscapeString(a.Value))
				out.WriteByte(34)
			}
		}
		out.WriteByte(62)
		c, err := renderStaticSSRNodes(n.Children)
		if err != nil {
			return "", err
		}
		out.WriteString(c)
		if !ssrVoidElement(n.Tag) {
			out.WriteString("</")
			out.WriteString(n.Tag)
			out.WriteByte(62)
		}
		return out.String(), nil
	default:
		return "", fmt.Errorf("%s: node %T is not lowered yet", SSRExpressionDiagnosticCode, node)
	}
}
func ssrVoidElement(tag string) bool {
	switch strings.ToLower(tag) {
	case "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr":
		return true
	}
	return false
}

// BuildStaticSSRGoFileWithTextProps extends the static renderer with cached
// text expressions. It is intentionally narrow while element-part lowering is
// migrated; unsupported dynamic structures still fail rather than disappear.
func BuildStaticSSRGoFileWithTextProps(plan *SSRRenderPlan, packageName string) (*goast.File, error) {
	file, err := BuildSSRGoSkeleton(plan, packageName)
	if err != nil {
		return nil, err
	}
	if len(plan.Nodes) != 1 {
		return nil, ssrExprUnsupported(0, "dynamic nested output is not lowered yet")
	}
	text, ok := plan.Nodes[0].(*SSRPlanText)
	if !ok {
		return nil, ssrExprUnsupported(0, "dynamic output requires a text node")
	}
	imports := file.Decls[0].(*goast.GenDecl)
	imports.Specs = append(imports.Specs, &goast.ImportSpec{Path: &goast.BasicLit{Kind: token.STRING, Value: `"fmt"`}}, &goast.ImportSpec{Path: &goast.BasicLit{Kind: token.STRING, Value: `"html"`}})
	ctx := NewSSRExprContext(plan)
	body := make([]goast.Stmt, 0, len(text.Parts)+1)
	for _, part := range text.Parts {
		if part.Static {
			body = append(body, goWriteStringLiteral(part.Value))
			continue
		}
		x, err := TranslateSSRExpression(part.Expr, ctx)
		if err != nil {
			return nil, err
		}
		value := &goast.CallExpr{Fun: goSelector("html", "EscapeString"), Args: []goast.Expr{&goast.CallExpr{Fun: goSelector("fmt", "Sprint"), Args: []goast.Expr{x}}}}
		body = append(body, &goast.ExprStmt{X: &goast.CallExpr{Fun: goSelector("io", "WriteString"), Args: []goast.Expr{goast.NewIdent("w"), value}}})
	}
	body = append(body, &goast.ReturnStmt{Results: []goast.Expr{goast.NewIdent("nil")}})
	file.Decls[len(file.Decls)-1].(*goast.FuncDecl).Body.List = body
	return file, nil
}
