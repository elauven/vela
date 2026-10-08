package jsast

// WalkNode completeness: every node that is part of the tree must be reached by
// WalkNode — otherwise identifier collection, dependency analysis and the
// reactive-assignment rewriter silently ignore whole sub-trees (this is how a
// missing `case *JsClassDecl` goes unnoticed).
//
// The expected set is computed independently, by reflection over the node
// structs, minus the edges WalkNode skips on purpose:
//
//	JsProp.Key, JsClassMember.Key      when not computed (a name, not a reference)
//	JsMemberExpr.Prop                  when not computed (a name, not a reference)
//	JsTryStmt.CatchParam               (a declaration, not a reference)
//
// JsSwitchCase, JsVarDeclarator and JsProp are pure containers: WalkNode goes
// straight to their children and never calls fn on them.

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func ctSkipEdge(typeName, field string, s reflect.Value) bool {
	switch typeName + "." + field {
	case "JsProp.Key", "JsClassMember.Key":
		return !ctBool(s, "Computed")
	case "JsMemberExpr.Prop":
		return !ctBool(s, "Computed")
	case "JsTryStmt.CatchParam":
		return true
	}
	return false
}

func ctExpectedVisits(n JsNode, out map[JsNode]int) {
	switch ctTypeName(n) {
	case "JsSwitchCase", "JsVarDeclarator", "JsProp":
	default:
		out[n]++
	}
	s, ok := ctStruct(n)
	if !ok {
		return
	}
	tn := s.Type().Name()
	for i := 0; i < s.NumField(); i++ {
		f := s.Field(i)
		if !f.CanInterface() || ctSkipEdge(tn, s.Type().Field(i).Name, s) {
			continue
		}
		visit := func(v reflect.Value) {
			if (v.Kind() == reflect.Interface || v.Kind() == reflect.Ptr) && !v.IsNil() && v.CanInterface() {
				if c, ok := v.Interface().(JsNode); ok {
					ctExpectedVisits(c, out)
				}
			}
		}
		switch f.Kind() {
		case reflect.Interface, reflect.Ptr:
			visit(f)
		case reflect.Slice:
			for j := 0; j < f.Len(); j++ {
				visit(f.Index(j))
			}
		}
	}
}

// ctWalkProblems compares WalkNode with the reflective expectation.
func ctWalkProblems(src string) []string {
	r := ctParse(src)
	if r.panic != nil || r.hang {
		return []string{fmt.Sprintf("parse failed: panic=%v hang=%v", r.panic, r.hang)}
	}
	want := map[JsNode]int{}
	ctExpectedVisits(r.prog, want)
	got := map[JsNode]int{}
	func() {
		defer func() { recover() }()
		WalkNode(r.prog, func(n JsNode) bool { got[n]++; return true })
	}()
	var problems []string
	missing := map[string]int{}
	for n := range want {
		if got[n] == 0 {
			missing[ctTypeName(n)]++
		}
	}
	var names []string
	for k := range missing {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		problems = append(problems, fmt.Sprintf("WalkNode never reaches %d × %s", missing[k], k))
	}
	for n, c := range got {
		if c > 1 {
			problems = append(problems, fmt.Sprintf("WalkNode visits one %s %d times", ctTypeName(n), c))
		}
		if want[n] == 0 {
			problems = append(problems, fmt.Sprintf("WalkNode visits %s which is not part of the tree", ctTypeName(n)))
		}
	}
	return problems
}

func TestCompatWalkNodeVisitsEveryNode(t *testing.T) {
	for _, src := range ctCorpus() {
		// Only valid programs: error recovery may build trees with shared placeholders.
		if r := ctParse(src); len(r.diags) > 0 {
			continue
		}
		src := src
		t.Run(ctShorten(strings.ReplaceAll(src, "\n", " ")), func(t *testing.T) {
			if p := ctWalkProblems(src); len(p) > 0 {
				t.Errorf("src: %q\n    %s", src, strings.Join(p, "\n    "))
			}
		})
	}
}

// Every construct the parser can produce, one per row, each of which must be
// fully walked.  Kept separate from the matrix so a failure names the construct.
func TestCompatWalkNodeConstructs(t *testing.T) {
	cases := map[string]string{
		"class declaration":      `class A extends B { m() { f(x) } static s = g(y); #p = 1; static { h(z) } get v() { return w } [k()]() {} }`,
		"class expression":       `const A = class extends mix(B) { m() { return q } }`,
		"class expression arg":   `f(class { x = r() })`,
		"tagged template":        "tag(a)`x${b}y${c(d)}`",
		"tagged template member": "obj.tag`${e}`",
		"nested tagged":          "a`${b`${c}`}`",
		"export default object":  `export default { m() { f(a) }, p: g(b), get q() { return c } }`,
		"export default class":   `export default class { m() { f(a) } }`,
		"export list":            `export { a, b as c }; export * from 'm'; export * as ns from 'm'`,
		"for await":              `async function f() { for await (const x of g(y)) h(x) }`,
		"dynamic import":         `const m = await import(p)`,
		"import.meta":            `x = import.meta.url`,
		"new with member":        `new a.b.C(d, ...e)`,
		"optional chain":         `a?.b?.[c]?.(d)`,
		"private brand check":    `class A { #x; static has(o) { return #x in o } }`,
		"arrow params":           `(a = f(b), { c = g(d) }, ...[e]) => a`,
		"destructuring default":  `const { a = f(b), c: [d = g(e)] } = h(i)`,
		"switch / labels / try":  `l: switch (a) { case b: try { c() } catch { d() } finally { e() } default: f() }`,
		"async generator":        `async function* g() { yield* h(a); await i }`,
	}
	for name, src := range cases {
		name, src := name, src
		t.Run(name, func(t *testing.T) {
			if p := ctWalkProblems(src); len(p) > 0 {
				t.Errorf("src: %q\n    %s", src, strings.Join(p, "\n    "))
			}
		})
	}
}
