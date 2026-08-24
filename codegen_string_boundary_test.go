package main

import (
	"go/ast"
	goparser "go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestASTGenStructuralStringGatewayInventory makes the temporary structural
// JavaScript-string boundary explicit. Task 012 must monotonically reduce this
// inventory; adding a new gateway or adding calls to an existing generator
// routine requires an intentional review and baseline update.
func TestASTGenStructuralStringGatewayInventory(t *testing.T) {
	expected := map[string]int{
		"buildDerivedDecl/jsStmt":       2,
		"buildDerivedDecls/jsStmt":      2,
		"buildEffectDecls/jsStmt":       2,
		"buildIfChainStructured/jsStmt": 1,
		"buildOnDestroyCleanup/jsStmts": 1,
		"buildOnMount/jsStmt":           1,
		"buildPropDecls/jsStmt":         2,
		"buildSlotClosures/jsStmt":      1,
		"buildSlotNode/jsStmt":          1,
		"buildStateDecl/jsStmt":         3,
		"jsStmt/jsStmts":                1,
		"rewrittenTemplateExpr/jsExpr":  1,
		"templateExpr/jsExpr":           1,
		"templateStmt/jsStmt":           1,
	}
	if actual := structuralGatewayCalls(t, "astgen.go"); !reflect.DeepEqual(actual, expected) {
		t.Fatalf("structural JS gateway inventory changed:\nwant %#v\n got %#v", expected, actual)
	}
}

// TestStructuralStringGatewaysStayInASTGen prevents a second production
// code-generation path from quietly acquiring a jsStmt/jsStmts/jsExpr helper.
// Existing ASTGen use is tracked by the inventory above until Task 012 removes
// it; test files are deliberately excluded because parser fixtures may use
// valid snippets for readability.
func TestStructuralStringGatewaysStayInASTGen(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file == "astgen.go" || strings.HasSuffix(file, "_test.go") {
			continue
		}
		if calls := structuralGatewayCalls(t, file); len(calls) != 0 {
			t.Fatalf("%s introduces structural JS gateway calls: %#v", file, calls)
		}
	}
}

func structuralGatewayCalls(t *testing.T, filename string) map[string]int {
	t.Helper()
	src, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("read %s: %v", filename, err)
	}
	file, err := goparser.ParseFile(token.NewFileSet(), filename, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}
	calls := map[string]int{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if !ok || (ident.Name != "jsStmt" && ident.Name != "jsStmts" && ident.Name != "jsExpr") {
				return true
			}
			calls[fn.Name.Name+"/"+ident.Name]++
			return true
		})
	}
	return calls
}
