package main

import (
	goast "go/ast"
	"go/token"
)

func goWriteSSRValue(value goast.Expr) goast.Stmt {
	return goWriteSSRTo("w", value)
}

// goWriteSSRTo writes value to the named writer. An escaped value is written
// with vela.WriteEscaped, which needs no new string for text without
// characters to escape (task 060).
func goWriteSSRTo(writer string, value goast.Expr) goast.Stmt {
	function := "WriteString"
	if call, ok := value.(*goast.CallExpr); ok && len(call.Args) == 1 && ssrIsSelector(call.Fun, "vela", "EscapeString") {
		function, value = "WriteEscaped", call.Args[0]
	}
	write := &goast.CallExpr{Fun: goSelector("vela", function), Args: []goast.Expr{goast.NewIdent(writer), value}}
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
