package main

import (
	goast "go/ast"
	"go/token"
	"strconv"
)

// optimizeSSRWrites rewrites the statement lists of a generated renderer
// after it is built (task 060):
//
//   - adjacent writes of string literals to the same writer become one
//     write;
//   - an ssrJS.Err() check is dropped when only literal writes ran since
//     the previous check in the same list: they cannot raise a JavaScript
//     exception, so the error state is the one the previous check saw.
//
// The output and the error behaviour are unchanged; generated code makes
// fewer calls.
func optimizeSSRWrites(file *goast.File) {
	goast.Inspect(file, func(node goast.Node) bool {
		switch n := node.(type) {
		case *goast.BlockStmt:
			n.List = optimizeSSRWriteList(n.List)
		case *goast.CaseClause:
			n.Body = optimizeSSRWriteList(n.Body)
		}
		return true
	})
}

func optimizeSSRWriteList(list []goast.Stmt) []goast.Stmt {
	out := list[:0:0]
	checked := false
	for _, stmt := range list {
		if ssrIsJSCheck(stmt) {
			if checked {
				continue
			}
			out = append(out, stmt)
			checked = true
			continue
		}
		writer, text, literal := ssrLiteralWrite(stmt)
		if !literal {
			out = append(out, stmt)
			checked = false
			continue
		}
		if len(out) > 0 {
			if previousWriter, previous, ok := ssrLiteralWrite(out[len(out)-1]); ok && previousWriter == writer {
				out[len(out)-1] = goWriteSSRTo(writer, &goast.BasicLit{Kind: token.STRING, Value: strconv.Quote(previous + text)})
				continue
			}
		}
		out = append(out, stmt)
	}
	return out
}

// ssrLiteralWrite matches `if err := vela.WriteString(w, "text"); err != nil
// { return err }` and returns the writer name and the text.
func ssrLiteralWrite(stmt goast.Stmt) (string, string, bool) {
	call, ok := ssrErrCheckCall(stmt)
	if !ok || !ssrIsSelector(call.Fun, "vela", "WriteString") || len(call.Args) != 2 {
		return "", "", false
	}
	writer, ok := call.Args[0].(*goast.Ident)
	if !ok {
		return "", "", false
	}
	lit, ok := call.Args[1].(*goast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", "", false
	}
	text, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", "", false
	}
	return writer.Name, text, true
}

func ssrIsJSCheck(stmt goast.Stmt) bool {
	call, ok := ssrErrCheckCall(stmt)
	return ok && len(call.Args) == 0 && ssrIsSelector(call.Fun, "ssrJS", "Err")
}

// ssrErrCheckCall matches `if err := call; err != nil { return err }`, the
// shape of goWriteSSRErr.
func ssrErrCheckCall(stmt goast.Stmt) (*goast.CallExpr, bool) {
	check, ok := stmt.(*goast.IfStmt)
	if !ok || check.Else != nil || check.Body == nil || len(check.Body.List) != 1 {
		return nil, false
	}
	init, ok := check.Init.(*goast.AssignStmt)
	if !ok || init.Tok != token.DEFINE || len(init.Lhs) != 1 || len(init.Rhs) != 1 || !ssrIsIdent(init.Lhs[0], "err") {
		return nil, false
	}
	cond, ok := check.Cond.(*goast.BinaryExpr)
	if !ok || cond.Op != token.NEQ || !ssrIsIdent(cond.X, "err") || !ssrIsIdent(cond.Y, "nil") {
		return nil, false
	}
	ret, ok := check.Body.List[0].(*goast.ReturnStmt)
	if !ok || len(ret.Results) != 1 || !ssrIsIdent(ret.Results[0], "err") {
		return nil, false
	}
	call, ok := init.Rhs[0].(*goast.CallExpr)
	return call, ok
}

func ssrIsIdent(expr goast.Expr, name string) bool {
	ident, ok := expr.(*goast.Ident)
	return ok && ident.Name == name
}

func ssrIsSelector(expr goast.Expr, pkg, name string) bool {
	sel, ok := expr.(*goast.SelectorExpr)
	return ok && ssrIsIdent(sel.X, pkg) && sel.Sel.Name == name
}
