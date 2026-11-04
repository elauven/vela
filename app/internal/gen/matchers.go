package gen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Builtins are the built-in matchers (docs/APP.md P-13): app.MatchInt and
// friends in Go, the exports of @govela/app/match in JavaScript.
var Builtins = []string{"int", "slug", "uuid"}

func builtin(name string) bool {
	for _, b := range Builtins {
		if b == name {
			return true
		}
	}
	return false
}

// CustomMatcher is a matcher of the project (P-14). Its Go half is the
// package <serverParams>/<name> with func Match(string) bool; its
// JavaScript half is <params>/<name>.js with export function match.
type CustomMatcher struct {
	Name   string
	GoDir  string // module-relative package directory, "" when absent
	JSFile string // project-relative module, "" when absent
}

var jsMatchExport = regexp.MustCompile(`(?m)^\s*export\s+(?:async\s+)?(?:function\s*\*?\s*match\s*\(|(?:const|let|var)\s+match\b)`)

// scanMatchers finds the custom matchers and checks that each has both
// halves and does not shadow a built-in one.
func (t *Tree) scanMatchers(report func(Diagnostic)) {
	t.Matchers = map[string]*CustomMatcher{}
	get := func(name string) *CustomMatcher {
		if t.Matchers[name] == nil {
			t.Matchers[name] = &CustomMatcher{Name: name}
		}
		return t.Matchers[name]
	}
	cfg := t.Config
	if entries, err := os.ReadDir(cfg.abs(cfg.ServerParams)); err == nil {
		for _, entry := range entries {
			name := entry.Name()
			rel := path.Join(cfg.ServerParams, name)
			if !entry.IsDir() {
				if strings.HasSuffix(name, ".go") {
					report(Diagnostic{File: rel, Line: 1, Col: 1, Code: CodeMatcher, Message: "a matcher is a package of its own, so that every matcher can declare func Match (P-14)", Help: fmt.Sprintf("move it to %s/%s/match.go", cfg.ServerParams, strings.TrimSuffix(name, ".go"))})
				}
				continue
			}
			if !identifier(name) {
				report(Diagnostic{File: rel + "/", Code: CodeMatcher, Message: fmt.Sprintf("matcher name %q is not an identifier", name)})
				continue
			}
			m := get(name)
			m.GoDir = rel
			if goFile, line, col, ok := goMatchFunc(cfg.abs(rel)); !ok {
				file := rel + "/"
				if goFile != "" {
					file = rel + "/" + goFile
				}
				report(Diagnostic{File: file, Line: line, Col: col, Code: CodeMatcher, Message: fmt.Sprintf("matcher %q has no func Match(segment string) bool", name), Help: "declare func Match(segment string) bool in package " + name})
			}
		}
	}
	if entries, err := os.ReadDir(cfg.abs(cfg.Params)); err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || path.Ext(name) != ".js" {
				continue
			}
			base := strings.TrimSuffix(name, ".js")
			rel := path.Join(cfg.Params, name)
			if !identifier(base) {
				report(Diagnostic{File: rel, Line: 1, Col: 1, Code: CodeMatcher, Message: fmt.Sprintf("matcher name %q is not an identifier", base)})
				continue
			}
			m := get(base)
			m.JSFile = rel
			data, err := os.ReadFile(cfg.abs(rel))
			if err == nil && !jsMatchExport.Match(data) {
				report(Diagnostic{File: rel, Line: 1, Col: 1, Code: CodeMatcher, Message: fmt.Sprintf("matcher %q does not export match", base), Help: "add export function match(segment) { … } returning a boolean"})
			}
		}
	}
	names := make([]string, 0, len(t.Matchers))
	for name := range t.Matchers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		m := t.Matchers[name]
		file := m.JSFile
		if file == "" {
			file = m.GoDir + "/"
		}
		line := 0
		if m.JSFile != "" {
			line = 1
		}
		switch {
		case builtin(name):
			report(Diagnostic{File: file, Line: line, Col: line, Code: CodeMatcher, Message: fmt.Sprintf("matcher %q shadows the built-in matcher of the same name (P-14)", name), Help: "rename the matcher"})
		case m.GoDir == "":
			report(Diagnostic{File: file, Line: line, Col: line, Code: CodeMatcher, Message: fmt.Sprintf("matcher %q has no Go half (P-14)", name), Help: fmt.Sprintf("create %s/%s/match.go with func Match(segment string) bool", cfg.ServerParams, name)})
		case m.JSFile == "":
			report(Diagnostic{File: file, Code: CodeMatcher, Message: fmt.Sprintf("matcher %q has no JavaScript half (P-14)", name), Help: fmt.Sprintf("create %s/%s.js with export function match(segment)", cfg.Params, name)})
		}
	}
	// Matchers the routes name.
	for _, d := range t.Dirs {
		matcher := d.Segment.Matcher
		if matcher == "" || builtin(matcher) || t.Matchers[matcher] != nil {
			continue
		}
		report(Diagnostic{File: t.projectRel(d.Rel) + "/", Code: CodeMatcher, Message: fmt.Sprintf("unknown matcher %q (P-13, P-14)", matcher), Help: fmt.Sprintf("use one of %s, or create %s/%s/match.go and %s/%s.js", strings.Join(Builtins, ", "), cfg.ServerParams, matcher, cfg.Params, matcher)})
	}
}

// goMatchFunc finds func Match(string) bool in a package directory. It
// returns the file and position of a Match with another signature, or
// none when there is no Match.
func goMatchFunc(dir string) (goFile string, line, col int, ok bool) {
	fset := token.NewFileSet()
	for _, name := range goFilesIn(dir) {
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		for _, decl := range file.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			if !isFunc || fn.Recv != nil || fn.Name.Name != "Match" {
				continue
			}
			params, results := fieldTypes(fn.Type.Params), fieldTypes(fn.Type.Results)
			if len(params) == 1 && len(results) == 1 && isIdent(params[0], "string") && isIdent(results[0], "bool") {
				return "", 0, 0, true
			}
			p := fset.Position(fn.Name.Pos())
			return name, p.Line, p.Column, false
		}
	}
	return "", 0, 0, false
}

func isIdent(expr ast.Expr, name string) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == name
}

func identifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		letter := r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
		if !letter && (i == 0 || r < '0' || r > '9') {
			return false
		}
	}
	return !token.Lookup(s).IsKeyword()
}
