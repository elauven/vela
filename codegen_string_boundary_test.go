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

// TestASTGenStructuralStringGatewayInventory makes the forbidden structural
// JavaScript-string boundary explicit. Task 012 has removed the last approved
// gateway, so the inventory must remain empty.
func TestASTGenStructuralStringGatewayInventory(t *testing.T) {
	expected := map[string]int{}
	if actual := structuralGatewayCalls(t, "astgen.go"); !reflect.DeepEqual(actual, expected) {
		t.Fatalf("structural JS gateway inventory changed:\nwant %#v\n got %#v", expected, actual)
	}
}

// TestStructuralStringGatewaysStayInASTGen prevents a second production
// code-generation path from quietly acquiring a jsStmt/jsStmts/jsExpr helper.
// Test files are deliberately excluded because parser fixtures may use valid
// snippets for readability.
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

// TestASTGenOnlyParsesUserScriptAST permits parsing only at the boundary where
// already-authored <script> source enters the unified target program. A
// generated fragment must never be serialized and parsed back into AST.
func TestASTGenOnlyParsesUserScriptAST(t *testing.T) {
	expected := map[string]int{}
	actual := namedCallInventory(t, "astgen.go", "ParseJS", "parseJSExpr")
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("AST generator parse boundary changed:\nwant %#v\n got %#v", expected, actual)
	}
}

// TestASTGenPrinterBoundaryInventory makes the two intentional printer uses
// reviewable: scoped CSS output and deterministic keys for already-built
// update statements. Neither result is ever parsed as JavaScript.
func TestASTGenPrinterBoundaryInventory(t *testing.T) {
	expected := map[string]int{
		"generateCodeASTCSS/PrintJS":   1,
		"reactiveFlushStmtKey/PrintJS": 1,
	}
	actual := namedCallInventory(t, "astgen.go", "PrintJS", "PrintExpr")
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("AST generator printer boundary changed:\nwant %#v\n got %#v", expected, actual)
	}
}

// TestASTGenHasNoLegacyStringGatewayDefinitions blocks a wrapper that could
// hide a future parser round-trip from the call inventories above.
func TestASTGenHasNoLegacyStringGatewayDefinitions(t *testing.T) {
	src, err := os.ReadFile("astgen.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := goparser.ParseFile(token.NewFileSet(), "astgen.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	forbidden := map[string]bool{
		"jsStmt": true, "jsStmts": true, "jsExpr": true, "stmtsToText": true,
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && forbidden[fn.Name.Name] {
			t.Fatalf("astgen.go reintroduces forbidden legacy string gateway %s", fn.Name.Name)
		}
	}
}

func structuralGatewayCalls(t *testing.T, filename string) map[string]int {
	return namedCallInventory(t, filename, "jsStmt", "jsStmts", "jsExpr")
}

func namedCallInventory(t *testing.T, filename string, names ...string) map[string]int {
	t.Helper()
	src, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("read %s: %v", filename, err)
	}
	file, err := goparser.ParseFile(token.NewFileSet(), filename, src, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}
	wanted := map[string]bool{}
	for _, name := range names {
		wanted[name] = true
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
			if !ok || !wanted[ident.Name] {
				return true
			}
			calls[fn.Name.Name+"/"+ident.Name]++
			return true
		})
	}
	return calls
}
