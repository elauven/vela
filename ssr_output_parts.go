package main

import (
	goast "go/ast"
	"go/token"
)

func goWriteSSRValue(value goast.Expr) goast.Stmt {
	write := &goast.CallExpr{Fun: goSelector("vela", "WriteString"), Args: []goast.Expr{goast.NewIdent("w"), value}}
	assign := &goast.AssignStmt{Lhs: []goast.Expr{goast.NewIdent("err")}, Tok: token.DEFINE, Rhs: []goast.Expr{write}}
	return &goast.IfStmt{
		Init: assign,
		Cond: &goast.BinaryExpr{X: goast.NewIdent("err"), Op: token.NEQ, Y: goast.NewIdent("nil")},
		Body: &goast.BlockStmt{List: []goast.Stmt{
			&goast.ReturnStmt{Results: []goast.Expr{goast.NewIdent("err")}},
		}},
	}
}

func goWriteSSRErr(call goast.Expr) goast.Stmt {
	return &goast.IfStmt{
		Init: &goast.AssignStmt{Lhs: []goast.Expr{goast.NewIdent("err")}, Tok: token.DEFINE, Rhs: []goast.Expr{call}},
		Cond: &goast.BinaryExpr{X: goast.NewIdent("err"), Op: token.NEQ, Y: goast.NewIdent("nil")},
		Body: &goast.BlockStmt{List: []goast.Stmt{&goast.ReturnStmt{Results: []goast.Expr{goast.NewIdent("err")}}}},
	}
}
