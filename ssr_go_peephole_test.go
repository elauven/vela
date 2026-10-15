package main

import (
	goast "go/ast"
	"strings"
	"testing"
)

// optimizeSSRWrites merges literal writes and drops only the ssrJS.Err()
// checks that follow nothing but literal writes (task 060).
func TestOptimizeSSRWritesMergesLiteralsAndRedundantChecks(t *testing.T) {
	lit := func(text string) goast.Expr { return ssrGoString(text) }
	dynamic := &goast.CallExpr{Fun: goSelector("vela", "String"), Args: []goast.Expr{goast.NewIdent("x")}}
	list := []goast.Stmt{
		ssrJSCheck(), // first check of the list: kept
		goWriteSSRValue(lit("<p>")),
		goWriteSSRValue(lit("a")),
		ssrJSCheck(), // only literal writes since the last check: dropped
		goWriteSSRValue(lit("b")),
		goWriteSSRTo("other", lit("c")), // another writer: not merged
		goWriteSSRValue(lit("d")),
		goWriteSSRValue(dynamic),
		ssrJSCheck(), // after an expression: kept
		ssrJSCheck(), // right after a check: dropped
		goWriteSSRValue(lit("</p>")),
	}
	file := &goast.File{Name: goast.NewIdent("p"), Decls: []goast.Decl{&goast.FuncDecl{
		Name: goast.NewIdent("f"), Type: &goast.FuncType{Params: &goast.FieldList{}},
		Body: &goast.BlockStmt{List: list},
	}}}
	optimizeSSRWrites(file)
	src, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	got := string(src)
	for _, want := range []string{`vela.WriteString(w, "<p>ab")`, `vela.WriteString(other, "c")`, `vela.WriteString(w, "d")`, `vela.WriteString(w, vela.String(x))`, `vela.WriteString(w, "</p>")`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "ssrJS.Err()"); n != 2 {
		t.Fatalf("ssrJS.Err() checks = %d, want 2:\n%s", n, got)
	}
	if escaped := goWriteSSRValue(&goast.CallExpr{Fun: goSelector("vela", "EscapeString"), Args: []goast.Expr{dynamic}}); !strings.Contains(ssrStmtText(t, escaped), "vela.WriteEscaped(w, vela.String(x))") {
		t.Fatalf("escaped write = %s", ssrStmtText(t, escaped))
	}
}

func ssrStmtText(t *testing.T, stmt goast.Stmt) string {
	t.Helper()
	file := &goast.File{Name: goast.NewIdent("p"), Decls: []goast.Decl{&goast.FuncDecl{
		Name: goast.NewIdent("f"), Type: &goast.FuncType{Params: &goast.FieldList{}},
		Body: &goast.BlockStmt{List: []goast.Stmt{stmt}},
	}}}
	src, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(src)
}
