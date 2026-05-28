package main

import (
	"strings"
	"testing"
)

// scope_test.go — exercise the scope graph + diagnostics emitted by
// BuildScopes and DetectUnreachable.  Each test runs a tiny script
// through the JS parser, builds scopes / runs flow analysis, and
// asserts that the expected diagnostic message appears (or doesn't).

func runScope(t *testing.T, src string) *DiagSet {
	t.Helper()
	prog := ParseJS(src)
	if prog == nil {
		t.Fatalf("ParseJS returned nil for: %q", src)
	}
	ds := NewDiagSet("test.js", src)
	BuildScopes(prog, ds)
	return ds
}

func runUnreachable(t *testing.T, src string) *DiagSet {
	t.Helper()
	prog := ParseJS(src)
	if prog == nil {
		t.Fatalf("ParseJS returned nil for: %q", src)
	}
	ds := NewDiagSet("test.js", src)
	DetectUnreachable(prog, ds)
	return ds
}

// expect asserts that at least one diagnostic message contains `sub`.
func expect(t *testing.T, ds *DiagSet, sub string) {
	t.Helper()
	for _, d := range ds.All() {
		if strings.Contains(d.Message, sub) {
			return
		}
	}
	var got []string
	for _, d := range ds.All() {
		got = append(got, d.Message)
	}
	t.Fatalf("expected a diagnostic containing %q, got: %v", sub, got)
}

// expectNone asserts that no diagnostic mentions `sub`.
func expectNone(t *testing.T, ds *DiagSet, sub string) {
	t.Helper()
	for _, d := range ds.All() {
		if strings.Contains(d.Message, sub) {
			t.Fatalf("unexpected diagnostic %q", d.Message)
		}
	}
}

// ─────────────────────────────────────────────
//  Undeclared variables
// ─────────────────────────────────────────────

func TestUndeclaredRead(t *testing.T) {
	ds := runScope(t, `console.log(foo);`)
	expect(t, ds, `"foo" is not defined`)
}

func TestUndeclaredWriteCreatesImplicitGlobal(t *testing.T) {
	ds := runScope(t, `foo = 1;`)
	expect(t, ds, "implicit global")
}

func TestBuiltinsAreSilent(t *testing.T) {
	ds := runScope(t, `console.log(Math.PI); window.alert(JSON.stringify({}));`)
	for _, d := range ds.All() {
		t.Errorf("expected no diagnostics, got: %s", d.Message)
	}
}

func TestVelaPrimitivesAreSilent(t *testing.T) {
	ds := runScope(t, `let count = $state(0); let doubled = $derived(count * 2); $effect(() => {});`)
	expectNone(t, ds, "$state")
	expectNone(t, ds, "$derived")
	expectNone(t, ds, "$effect")
}

// ─────────────────────────────────────────────
//  Declarations + scoping
// ─────────────────────────────────────────────

func TestLetIsScopedToBlock(t *testing.T) {
	src := `
		{ let x = 1; }
		console.log(x);
	`
	ds := runScope(t, src)
	expect(t, ds, `"x" is not defined`)
}

func TestVarIsHoistedToFunction(t *testing.T) {
	src := `
		function f() {
			{ var x = 1; }
			return x;  // legal — var hoists to function scope
		}
	`
	ds := runScope(t, src)
	expectNone(t, ds, `"x"`)
}

func TestHoistedReferenceBeforeVarDecl(t *testing.T) {
	// var is hoisted, so reading it before the decl is legal (returns
	// undefined at runtime, but no scope error).
	src := `
		function f() {
			console.log(x);
			var x = 1;
		}
	`
	ds := runScope(t, src)
	expectNone(t, ds, `"x"`)
}

func TestDestructuringDeclaresEachName(t *testing.T) {
	src := `
		let { a, b: c, d = 1 } = obj;
		console.log(a, c, d);
	`
	ds := runScope(t, src)
	expect(t, ds, `"obj" is not defined`)
	expectNone(t, ds, `"a"`)
	expectNone(t, ds, `"c"`)
	expectNone(t, ds, `"d"`)
}

func TestArrayDestructuringRest(t *testing.T) {
	src := `
		let [x, ...rest] = arr;
		console.log(x, rest);
	`
	ds := runScope(t, src)
	expectNone(t, ds, `"x"`)
	expectNone(t, ds, `"rest"`)
}

// ─────────────────────────────────────────────
//  TDZ
// ─────────────────────────────────────────────

func TestTDZRead(t *testing.T) {
	src := `
		function f() {
			console.log(x);
			let x = 1;
		}
	`
	ds := runScope(t, src)
	expect(t, ds, "temporal dead zone")
}

func TestTDZSelfReferenceInInit(t *testing.T) {
	// `let x = x` at runtime throws ReferenceError because the inner
	// `x` is in TDZ until the initializer completes.  Statically,
	// detecting this requires tracking "currently being initialized"
	// — a stronger property than the source-position approximation
	// used here.  Documenting current behavior: no diagnostic.  A
	// real-engine runtime error will catch it.
	src := `let x = x;`
	ds := runScope(t, src)
	// No assertion about which diagnostic — just that it doesn't crash.
	_ = ds
}

func TestTDZDoesNotFireAcrossFunctionBoundary(t *testing.T) {
	// The reference inside the inner fn can run after `x` is initialised.
	src := `
		function f() {
			function g() { return x; }
			let x = 1;
			return g();
		}
	`
	ds := runScope(t, src)
	expectNone(t, ds, "temporal dead zone")
}

func TestTemplateLiteralInterpolationPosition(t *testing.T) {
	// Regression: an identifier interpolated in a template literal
	// is parsed by structureTemplate from a DETACHED substring, so
	// without a position fix-up every node inside ${…} carried a
	// Pos relative to that substring (e.g. 0).  The TDZ check then
	// saw the interpolated ident's Pos (~0) as < the real `const`
	// declaration's Pos and reported a bogus "before
	// initialization" error.  structureTemplate now shifts the
	// sub-tree onto the original source coordinates, so a reference
	// that legitimately follows its declaration sees the right
	// ordering and no TDZ fires.
	src := "function f() { const r = 1; return `value ${r}`; }"
	ds := runScope(t, src)
	expectNone(t, ds, "temporal dead zone")

	// The interpolated ident must carry a real, non-zero position
	// past the declaration — proves the shift was applied.
	prog := ParseJS(src)
	if prog == nil {
		t.Fatalf("ParseJS returned nil")
	}
	var declPos, refPos int
	WalkNode(prog, func(n JsNode) bool {
		id, ok := n.(*JsIdent)
		if !ok || id.Name != "r" {
			return true
		}
		// The declarator id comes first in source order; the
		// interpolated reference second.
		if declPos == 0 {
			declPos = id.Pos
		} else if refPos == 0 {
			refPos = id.Pos
		}
		return true
	})
	if declPos == 0 || refPos == 0 {
		t.Fatalf("expected two 'r' idents with non-zero positions, "+
			"got decl=%d ref=%d", declPos, refPos)
	}
	if refPos <= declPos {
		t.Fatalf("interpolated ref position %d should follow the "+
			"declaration position %d", refPos, declPos)
	}
}

// ─────────────────────────────────────────────
//  const / import immutability
// ─────────────────────────────────────────────

func TestConstAssignment(t *testing.T) {
	src := `const x = 1; x = 2;`
	ds := runScope(t, src)
	expect(t, ds, "cannot assign to const")
}

func TestImportAssignment(t *testing.T) {
	src := `import { foo } from 'mod'; foo = 1;`
	ds := runScope(t, src)
	expect(t, ds, "cannot assign to import")
}

// ─────────────────────────────────────────────
//  Redeclaration
// ─────────────────────────────────────────────

func TestLetRedeclaration(t *testing.T) {
	src := `let x = 1; let x = 2;`
	ds := runScope(t, src)
	expect(t, ds, "redeclaration")
}

func TestVarOnVarIsLegal(t *testing.T) {
	src := `var x = 1; var x = 2;`
	ds := runScope(t, src)
	expectNone(t, ds, "redeclaration")
}

// ─────────────────────────────────────────────
//  Update expressions
// ─────────────────────────────────────────────

func TestUpdateOnConstFails(t *testing.T) {
	src := `const x = 1; x++;`
	ds := runScope(t, src)
	expect(t, ds, "cannot assign to const")
}

func TestUpdateOnUndeclaredIsImplicitGlobal(t *testing.T) {
	src := `foo++;`
	ds := runScope(t, src)
	expect(t, ds, "implicit global")
}

// ─────────────────────────────────────────────
//  Member expressions: property names aren't variables
// ─────────────────────────────────────────────

func TestMemberPropertyNameNotResolved(t *testing.T) {
	// `obj.foo` — foo is a property name, not a variable, so it
	// should NOT trigger an "undeclared" warning.
	src := `let obj = {}; console.log(obj.foo);`
	ds := runScope(t, src)
	expectNone(t, ds, `"foo"`)
}

func TestComputedMemberIsResolved(t *testing.T) {
	// `obj[bar]` — bar IS a variable reference.
	src := `let obj = {}; console.log(obj[bar]);`
	ds := runScope(t, src)
	expect(t, ds, `"bar" is not defined`)
}

// ─────────────────────────────────────────────
//  Object literal shorthand
// ─────────────────────────────────────────────

func TestShorthandPropertyIsReference(t *testing.T) {
	// `{ x }` — x is a reference (not just a name).
	src := `let obj = { x };`
	ds := runScope(t, src)
	expect(t, ds, `"x" is not defined`)
}

// ─────────────────────────────────────────────
//  Catch binding
// ─────────────────────────────────────────────

func TestCatchBinding(t *testing.T) {
	src := `try { } catch (e) { console.log(e); }`
	ds := runScope(t, src)
	expectNone(t, ds, `"e"`)
}

func TestCatchBindingNotVisibleAfter(t *testing.T) {
	src := `try { } catch (e) { } console.log(e);`
	ds := runScope(t, src)
	expect(t, ds, `"e" is not defined`)
}

// ─────────────────────────────────────────────
//  Unreachable code
// ─────────────────────────────────────────────

func TestUnreachableAfterReturn(t *testing.T) {
	src := `function f() { return 1; console.log("dead"); }`
	ds := runUnreachable(t, src)
	expect(t, ds, "unreachable code")
}

func TestUnreachableAfterThrow(t *testing.T) {
	src := `function f() { throw new Error(); let x = 1; }`
	ds := runUnreachable(t, src)
	expect(t, ds, "unreachable code")
}

func TestUnreachableAfterIfElseBothReturn(t *testing.T) {
	src := `function f(c) {
		if (c) { return 1; } else { return 2; }
		console.log("dead");
	}`
	ds := runUnreachable(t, src)
	expect(t, ds, "unreachable code")
}

func TestReachableAfterIfWithNoElse(t *testing.T) {
	src := `function f(c) {
		if (c) { return 1; }
		console.log("alive");
	}`
	ds := runUnreachable(t, src)
	expectNone(t, ds, "unreachable")
}

func TestReachableAfterIfElseOneReturns(t *testing.T) {
	src := `function f(c) {
		if (c) { return 1; } else { let x = 2; }
		console.log("alive");
	}`
	ds := runUnreachable(t, src)
	expectNone(t, ds, "unreachable")
}

func TestUnreachableAfterBreak(t *testing.T) {
	src := `for (let i = 0; i < 10; i++) {
		break;
		console.log("dead");
	}`
	ds := runUnreachable(t, src)
	expect(t, ds, "unreachable code")
}

func TestNestedFunctionFlowIsIndependent(t *testing.T) {
	// The inner function body has its own flow — code after the outer
	// return is unreachable, but the inner function being declared
	// later should not itself be flagged twice.
	src := `function f() {
		return 1;
		function g() { return 2; }
	}`
	ds := runUnreachable(t, src)
	// Exactly one "unreachable" warning, on the function declaration.
	count := 0
	for _, d := range ds.All() {
		if strings.Contains(d.Message, "unreachable") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 unreachable warning, got %d", count)
	}
}

func TestTryFinallyThatReturnsTerminates(t *testing.T) {
	src := `function f() {
		try { return 1; } finally { return 2; }
		console.log("dead");
	}`
	ds := runUnreachable(t, src)
	expect(t, ds, "unreachable code")
}

// TestExportLetVisibleToEarlierClosure — an `export let` prop
// referenced by a closure that appears EARLIER in source order
// (e.g. inside an $effect declared above the prop) must resolve.
// Regression: preDeclareLexicalStmt did not unwrap JsExportDecl,
// so the prop name was absent from module scope during pass 2 and
// the reference reported a bogus "not defined".
func TestExportLetVisibleToEarlierClosure(t *testing.T) {
	// The arrow function is declared before `export let title`.
	src := `
		const log = () => title;
		export let title = 'hi';
	`
	ds := runScope(t, src)
	expectNone(t, ds, "not defined")
}

// TestDispatchBuiltinResolves — $$dispatch is wired into every
// component scope by the code generator, so a user script that
// calls it must not report "$$dispatch is not defined".
func TestDispatchBuiltinResolves(t *testing.T) {
	src := `
		function close() {
			$$dispatch('close', { reason: 'user' });
		}
	`
	ds := runScope(t, src)
	expectNone(t, ds, "not defined")
}
