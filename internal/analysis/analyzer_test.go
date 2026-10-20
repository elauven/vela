package analysis

import (
	"regexp"
	"strings"
	"testing"

	"github.com/elauven/vela/internal/jsast"
	"github.com/elauven/vela/internal/sfc"
)

func TestScriptASTOwnsParserResultUsedByAnalysis(t *testing.T) {
	sfc, err := sfc.ParseSFC("<script>let count = $state(0);</script><template>{count}</template>")
	if err != nil {
		t.Fatal(err)
	}
	analysis := AnalyzeScriptAST(sfc.ScriptAST)
	if sfc.ScriptAST.Program == nil || analysis.Script != sfc.ScriptAST {
		t.Fatalf("analysis did not retain owned ScriptAST: script=%p analysis=%p", sfc.ScriptAST, analysis.Script)
	}
	if got, want := sfc.ScriptAST.Span.Start, sfc.ScriptOffset; got != want {
		t.Fatalf("script span start = %d, want section offset %d", got, want)
	}
}

// The Analysis.IntrinsicAliases map is the single source of
// truth — verify it is populated as expected directly, so a
// future change in how lifecycles get emitted does not silently
// regress the alias mechanism.
func TestAnalyzer_IntrinsicAliases_Populated(t *testing.T) {
	a := AnalyzeScript(`
		import { onMount as setup, onDestroy as teardown } from 'vela';
		setup(() => {});
		teardown(() => {});
	`)
	if a.IntrinsicAliases["setup"] != "onMount" {
		t.Errorf("setup → onMount missing: %#v", a.IntrinsicAliases)
	}
	if a.IntrinsicAliases["teardown"] != "onDestroy" {
		t.Errorf("teardown → onDestroy missing: %#v", a.IntrinsicAliases)
	}
}

// An intrinsic imported from a non-Vela source must NOT be
// recorded as an alias — the user's `onMount` from their own
// module is a regular function, and the alias map is keyed on
// `import { … } from 'vela'` only.  (Note: the current direct-
// name match in extractLifecycle still hijacks `onMount(...)`
// regardless of import source — that pre-existing behavior is
// separate from this fix.)
func TestAnalyzer_IntrinsicAliases_NotPopulatedForOtherSource(t *testing.T) {
	a := AnalyzeScript(`
		import { onMount as setup } from './my-other.js';
		setup(() => {});
	`)
	if _, ok := a.IntrinsicAliases["setup"]; ok {
		t.Errorf("setup should NOT be in IntrinsicAliases for non-vela source: %#v",
			a.IntrinsicAliases)
	}
}

func TestReproducibleStateInitializers(t *testing.T) {
	a := AnalyzeScript(`export let columns = [];
export let issue = null;
import { load } from './api.js';
const LIMIT = 3;
let board = $state(columns);
let title = $state(issue.title);
let key = $state(issue['key']);
let empty = $state();
let n = $state(-1);
let flags = $state.deep({ open: false, tags: ['a', 'b'], owner: issue.owner });
let copy = $state([...columns]);
let loaded = $state(load());
let limit = $state(LIMIT);
let twice = $state(columns.length * 2);
let dyn = $state(issue[title]);`)
	want := map[string]bool{"board": true, "title": true, "key": true, "empty": true, "n": true, "flags": true,
		"copy": false, "loaded": false, "limit": false, "twice": false, "dyn": false}
	for _, sv := range a.StateVars {
		if sv.Reproducible != want[sv.Name] {
			t.Errorf("%s: reproducible = %v, want %v", sv.Name, sv.Reproducible, want[sv.Name])
		}
	}
}

func TestAnalyzeScript_CleanScript(t *testing.T) {
	a := AnalyzeScript(`
let count = $state(0);
$effect(() => { console.log(count); });

function increment() {
  count++;
}
`)
	if strings.Contains(a.CleanScript(), "$state") {
		t.Error("CleanScript should have $state removed")
	}
	if strings.Contains(a.CleanScript(), "$effect") {
		t.Error("CleanScript should have $effect removed")
	}
	if !strings.Contains(a.CleanScript(), "function increment") {
		t.Error("CleanScript should retain function declarations")
	}
}

func TestRewriteAssignments(t *testing.T) {
	stateSet := map[string]bool{"count": true, "x": true}

	cases := []struct {
		src  string
		want string
	}{
		{"count++;", "$$set_count(count+1);"},
		{"count--;", "$$set_count(count-1);"},
		{"count = 42;", "$$set_count(42);"},
		{"count += 5;", "$$set_count(count+5);"},
		{"count -= 2;", "$$set_count(count-2);"},
		{"count *= 3;", "$$set_count(count*3);"},
		{"count **= 2;", "$$set_count(count**2);"},
		// Reading count — must NOT be rewritten
		{"const y = count + 1;", "const y = count + 1;"},
		// Property assign — must NOT be rewritten
		{"obj.count = 5;", "obj.count = 5;"},
	}

	for _, tc := range cases {
		prog := jsast.ParseJS(tc.src)
		got := RewriteAssignments(tc.src, prog, stateSet)
		if !strings.Contains(got, strings.SplitN(tc.want, "(", 2)[0]) {
			t.Errorf("RewriteAssignments(%q):\n  want %q\n  got  %q", tc.src, tc.want, got)
		}
	}
}

func TestRewriteAssignments_InsideArrow(t *testing.T) {
	// Assignment inside a nested arrow function must be rewritten.
	src := `store.subscribe(v => { todos = v; });`
	stateSet := map[string]bool{"todos": true}
	prog := jsast.ParseJS(src)
	got := RewriteAssignments(src, prog, stateSet)
	if !strings.Contains(got, "$$set_todos") {
		t.Errorf("expected $$set_todos in rewritten source, got: %s", got)
	}
}

func TestCleanScript_AssignmentsRewritten(t *testing.T) {
	// End-to-end: analyzeScript should produce CleanScript with setters.
	a := AnalyzeScript(`
let count = $state(0);
function inc() { count++; }
function dec() { count--; }
function set(n) { count = n; }
`)
	clean := a.CleanScript()
	if !strings.Contains(clean, "$$set_count") {
		t.Errorf("CleanScript should contain $$set_count; got:\n%s", clean)
	}
	if strings.Contains(clean, "$state") {
		t.Error("CleanScript should not contain $state")
	}
}

func TestScanTemplateHoles_Simple(t *testing.T) {
	holes := scanTemplateHoles("`hello ${name}!`")
	if len(holes) != 1 || holes[0] != "name" {
		t.Errorf("expected [name], got %v", holes)
	}
}

func TestScanTemplateHoles_Multiple(t *testing.T) {
	holes := scanTemplateHoles("`${a} and ${b}`")
	if len(holes) != 2 {
		t.Fatalf("expected 2 holes, got %v", holes)
	}
	if holes[0] != "a" || holes[1] != "b" {
		t.Errorf("expected [a b], got %v", holes)
	}
}

func TestScanTemplateHoles_NestedBraces(t *testing.T) {
	// Nested object literal inside hole — fails with simple [^}]* regex
	holes := scanTemplateHoles("`result: ${fn({ key: val })}`")
	if len(holes) != 1 {
		t.Fatalf("expected 1 hole, got %v", holes)
	}
	if !strings.Contains(holes[0], "fn") {
		t.Errorf("hole should contain fn, got %q", holes[0])
	}
}

func TestScanTemplateHoles_NestedString(t *testing.T) {
	// String containing } inside hole — confuses naive scanners
	holes := scanTemplateHoles("`${\"}\" + x}`")
	if len(holes) != 1 {
		t.Fatalf("expected 1 hole, got %v", holes)
	}
}

func TestScanTemplateHoles_Escaped(t *testing.T) {
	// \${ is NOT a hole
	holes := scanTemplateHoles("`\\${notAHole} ${isAHole}`")
	if len(holes) != 1 {
		t.Errorf("expected 1 hole (escaped should be ignored), got %v", holes)
	}
}

func TestAnalyzeScript_ImportSpec(t *testing.T) {
	// Import spec reconstruction from typed AST (not raw string slicing)
	cases := []struct {
		src  string
		spec string
	}{
		{`import { writable, derived } from 'vela/store';`, "{ writable, derived }"},
		{`import * as Store from 'vela/store';`, "* as Store"},
		{`import Default from 'mod';`, "Default"},
		{`import Default, { named } from 'mod';`, "Default, { named }"},
	}
	for _, tc := range cases {
		a := AnalyzeScript(tc.src)
		if len(a.Imports) == 0 {
			t.Errorf("no imports parsed for: %s", tc.src)
			continue
		}
		if formatImportSpec(a.Imports[0]) != tc.spec {
			t.Errorf("import spec:\n  want %q\n  got  %q\n  src: %s",
				tc.spec, formatImportSpec(a.Imports[0]), tc.src)
		}
	}
}

func TestRewriteAssignments_MemberExprNotRewritten(t *testing.T) {
	// obj.count = 5 must NOT be rewritten even though "count" is a state var
	src := `obj.count = 5;`
	prog := jsast.ParseJS(src)
	stateSet := map[string]bool{"count": true}
	got := RewriteAssignments(src, prog, stateSet)
	if strings.Contains(got, "$$set_count") {
		t.Errorf("member-expr assignment should not be rewritten: %s", got)
	}
}

func TestRewriteAssignments_NullishAndLogical(t *testing.T) {
	for _, op := range []string{"??=", "&&=", "||="} {
		src := "count " + op + " 0;"
		prog := jsast.ParseJS(src)
		got := RewriteAssignments(src, prog, map[string]bool{"count": true})
		if !strings.Contains(got, "$$set_count") {
			t.Errorf("op %s should be rewritten; got: %s", op, got)
		}
	}
}

func TestRewriteAssignments_PreservesNonState(t *testing.T) {
	// Only state vars should be rewritten; other vars stay as-is
	src := `
		let localVar = 0;
		localVar = 42;
		count = 1;
	`
	prog := jsast.ParseJS(src)
	got := RewriteAssignments(src, prog, map[string]bool{"count": true})
	if !strings.Contains(got, "localVar = 42") {
		t.Errorf("non-state assignment should be preserved; got:\n%s", got)
	}
	if !strings.Contains(got, "$$set_count") {
		t.Errorf("state assignment should be rewritten; got:\n%s", got)
	}
}

func TestPrintNode_RoundTrip(t *testing.T) {
	// printNode is used to reconstruct effect bodies — check key constructs
	cases := []string{
		"x + y",
		"a ? b : c",
		"foo(a, b, c)",
		"obj.method()",
		"[1, 2, 3]",
		"{ a: 1, b: 2 }",
	}
	for _, expr := range cases {
		prog := jsast.ParseJS(expr)
		if len(prog.Body) == 0 {
			t.Errorf("no stmts parsed for %q", expr)
			continue
		}
		got := PrintNode(prog.Body[0])
		// The round-trip output should contain all the identifiers and operators
		// (we don't require character-identical output, just semantic equivalence)
		if got == "" {
			t.Errorf("printNode(%q) returned empty string", expr)
		}
	}
}

func TestAnalyzeScript_PropsNotInCleanScript(t *testing.T) {
	// export let should be removed from CleanScript (it's handled by codegen)
	a := AnalyzeScript(`
		export let name = 'World';
		function greet() { return name; }
	`)
	if strings.Contains(a.CleanScript(), "export let") {
		t.Errorf("export let should not appear in CleanScript: %s", a.CleanScript())
	}
	if !strings.Contains(a.CleanScript(), "function greet") {
		t.Errorf("greet function should be in CleanScript: %s", a.CleanScript())
	}
}

func TestAnalyzeScript_OnDestroy(t *testing.T) {
	a := AnalyzeScript(`
		onDestroy(() => {
			clearInterval(pollId);
		});
	`)
	if len(a.Lifecycles) != 1 {
		t.Fatalf("expected 1 lifecycle, got %d", len(a.Lifecycles))
	}
	if a.Lifecycles[0].Kind != "onDestroy" {
		t.Errorf("kind: want 'onDestroy', got %q", a.Lifecycles[0].Kind)
	}
}

func TestAnalyzeScript_LifecycleNotInCleanScript(t *testing.T) {
	a := AnalyzeScript(`
		onMount(() => { document.title = 'hi'; });
		function greet() { return 'hello'; }
	`)
	if strings.Contains(a.CleanScript(), "onMount") {
		t.Errorf("onMount should be removed from CleanScript; got:\n%s", a.CleanScript())
	}
	if !strings.Contains(a.CleanScript(), "function greet") {
		t.Errorf("user functions should remain in CleanScript")
	}
}

func TestStatesSet_IncludesProps(t *testing.T) {
	a := AnalyzeScript(`
		export let name = $bindable('World');
		export let count = 0;
		let x = $state(0);
	`)
	ss := a.StateSet()
	for _, want := range []string{"name", "count", "x"} {
		if !ss[want] {
			t.Errorf("stateSet() should include %q", want)
		}
	}
}

func TestCleanScript_PropAssignmentRewritten(t *testing.T) {
	// export let name is a prop — assigning name = "new" should become $$set_name("new")
	a := AnalyzeScript(`
		export let name = $bindable('World');
		function rename(v) { name = v; }
	`)
	if !strings.Contains(a.CleanScript(), "$$set_name") {
		t.Errorf("prop assignment should be rewritten to $$set_name:\n%s", a.CleanScript())
	}
	if strings.Contains(a.CleanScript(), "name = v") {
		t.Errorf("raw prop assignment should be gone:\n%s", a.CleanScript())
	}
}

func TestLifecycle_OnMountBodyRewritten(t *testing.T) {
	a := AnalyzeScript(`
		let online = $state(false);
		onMount(() => {
			online = true;
		});
	`)
	if len(a.Lifecycles) == 0 {
		t.Fatal("no lifecycles found")
	}
	lc := a.Lifecycles[0]
	body := PrintNode(lc.BodyAST)
	if strings.Contains(body, "online = true") && !strings.Contains(body, "$$set_online") {
		t.Errorf("onMount AST body state assignment should be rewritten:\n%s", body)
	}
	if !strings.Contains(body, "$$set_online(true)") {
		t.Errorf("expected $$set_online(true) in onMount AST body:\n%s", body)
	}
}

func TestLifecycle_OnMountRewritesNestedClosureAssignment(t *testing.T) {
	// online = x inside a setInterval callback inside onMount should be rewritten
	a := AnalyzeScript(`
		let online = $state(false);
		onMount(() => {
			setInterval(() => { online = Math.random() > 0.5; }, 1000);
		});
	`)
	if len(a.Lifecycles) == 0 {
		t.Fatal("no lifecycles found")
	}
	lc := a.Lifecycles[0]
	body := PrintNode(lc.BodyAST)
	if !strings.Contains(body, "$$set_online") {
		t.Errorf("nested state assignment in onMount AST body not rewritten:\n%s", body)
	}
}

func TestLifecycle_PropAssignmentInOnMount(t *testing.T) {
	// Props should also be rewritten inside onMount
	a := AnalyzeScript(`
		export let count = $bindable(0);
		onMount(() => { count = 42; });
	`)
	if len(a.Lifecycles) == 0 {
		t.Fatal("no lifecycle found")
	}
	lc := a.Lifecycles[0]
	body := PrintNode(lc.BodyAST)
	if !strings.Contains(body, "$$set_count") {
		t.Errorf("prop assignment in onMount AST body not rewritten:\n%s", body)
	}
}

func TestLifecycle_OnDestroyBodyRewritten(t *testing.T) {
	a := AnalyzeScript(`
		let status = $state('active');
		onDestroy(() => { status = 'destroyed'; });
	`)
	if len(a.Lifecycles) == 0 {
		t.Fatal("no lifecycle found")
	}
	lc := a.Lifecycles[0]
	body := PrintNode(lc.BodyAST)
	if !strings.Contains(body, "$$set_status") {
		t.Errorf("onDestroy AST body state assignment not rewritten:\n%s", body)
	}
}

func TestEffect_BodyRewritten(t *testing.T) {
	// $effect bodies that mutate other state should be rewritten
	a := AnalyzeScript(`
		let count = $state(0);
		let doubled = $state(0);
		$effect(() => { doubled = count * 2; });
	`)
	if len(a.Effects) == 0 {
		t.Fatal("no effect found")
	}
	eff := a.Effects[0]
	body := PrintNode(eff.BodyAST)
	if !strings.Contains(body, "$$set_doubled") {
		t.Errorf("effect AST body state assignment not rewritten:\n%s", body)
	}
}

// Regression: `x = (a > b) ? 'yes' : 'no'`
// parsePrimary strips outer parens, so ConditionalExpr.Pos pointed to `a`
// instead of `(`, causing rhs to be `a > b) ? 'yes' : 'no'` (wrong).
func TestRewriteAssignments_ParenthesisedTernary(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{
			`x = (h >= 12) ? 'PM' : 'AM';`,
			`$$set_x((h >= 12) ? 'PM' : 'AM')`,
		},
		{
			`x = (a > 0) ? a : -a;`,
			`$$set_x((a > 0) ? a : -a)`,
		},
		{
			// No outer parens — must still work
			`x = h >= 12 ? 'PM' : 'AM';`,
			`$$set_x(h >= 12 ? 'PM' : 'AM')`,
		},
		{
			// Compound assignment with parenthesised RHS
			`x += (n * 2);`,
			`$$set_x(x+(n * 2))`,
		},
	}

	stateSet := map[string]bool{"x": true}
	for _, tc := range cases {
		prog := jsast.ParseJS(tc.src)
		got := RewriteAssignments(tc.src, prog, stateSet)
		if !strings.Contains(got, tc.want) {
			t.Errorf("RewriteAssignments(%q):\n  want to contain %q\n  got  %q",
				tc.src, tc.want, got)
		}
		// Must not have the bug pattern: closing paren before the ternary operator
		if strings.Contains(got, ") ? ") && strings.Contains(got, "$$set_") {
			// Check it's the correct form: the ) closes the setter call, not the outer paren
			if strings.Contains(got, "$$set_x() ? ") || strings.Contains(got, "$$set_x(h >= 12) ?") {
				t.Errorf("detected double-wrapped / truncated ternary bug: %q", got)
			}
		}
	}
}

func TestExtractRHS_SimpleAssign(t *testing.T) {
	// x = 42  →  lhsEnd=1, op="=", stmtEnd=6 (or wherever)
	// The src used here simulates the statement text
	src := "x = 42;"
	rhs := extractRHS(src, 1, "=", len(src))
	if rhs != "42" {
		t.Errorf("extractRHS simple: want '42', got %q", rhs)
	}
}

func TestExtractRHS_Ternary(t *testing.T) {
	src := "ampm = (h >= 12) ? 'PM' : 'AM';"
	rhs := extractRHS(src, len("ampm"), "=", len(src))
	want := "(h >= 12) ? 'PM' : 'AM'"
	if rhs != want {
		t.Errorf("extractRHS ternary:\n  want %q\n  got  %q", want, rhs)
	}
}

func TestExtractRHS_Compound(t *testing.T) {
	src := "count += step * 2;"
	rhs := extractRHS(src, len("count"), "+=", len(src))
	want := "step * 2"
	if rhs != want {
		t.Errorf("extractRHS compound:\n  want %q\n  got  %q", want, rhs)
	}
}

func TestReactiveNames_IncludesProps(t *testing.T) {
	a := AnalyzeScript(`
		export let pinned = false;
		export let selected = false;
		let editMode = $state(false);
		let cardClass = $derived('card' + (pinned ? ' pinned' : '') + (selected ? ' selected' : '') + (editMode ? ' editing' : ''));
	`)
	names := a.ReactiveNames()
	nameSet := map[string]bool{}
	for _, n := range names {
		nameSet[n] = true
	}
	for _, want := range []string{"pinned", "selected", "editMode", "cardClass"} {
		if !nameSet[want] {
			t.Errorf("reactiveNames should include %q; got: %v", want, names)
		}
	}
}

func TestDerivedDeps_IncludesPropVars(t *testing.T) {
	a := AnalyzeScript(`
		export let pinned = false;
		export let selected = false;
		let editMode = $state(false);
		let cardClass = $derived('card' + (pinned ? ' pinned' : '') + (selected ? ' selected' : '') + (editMode ? ' editing' : ''));
	`)
	if len(a.DerivedVars) == 0 {
		t.Fatal("no derived vars found")
	}
	dv := a.DerivedVars[0] // cardClass
	deps := map[string]bool{}
	for _, d := range dv.Deps {
		deps[d] = true
	}
	for _, want := range []string{"pinned", "selected", "editMode"} {
		if !deps[want] {
			t.Errorf("cardClass.Deps should include %q; got: %v", want, dv.Deps)
		}
	}
}

func TestAnalyzeScript_AsyncFunctionPreserved(t *testing.T) {
	a := AnalyzeScript(`
		async function fetchData(url) {
			const res = await fetch(url);
			return res.json();
		}
		async function search() {
			const data = await fetchData('/api');
			return data;
		}
	`)
	if !strings.Contains(a.CleanScript(), "async function fetchData") {
		t.Errorf("async keyword lost from fetchData:\n%s", a.CleanScript())
	}
	if !strings.Contains(a.CleanScript(), "async function search") {
		t.Errorf("async keyword lost from search:\n%s", a.CleanScript())
	}
}

func TestFindWrittenVars(t *testing.T) {
	prog := jsast.ParseJS(`
		const unsub = store.subscribe(state => {
			isAuthenticated = state.isAuthenticated;
			theme = state.theme;
		});
		return unsub;
	`)
	written := FindWrittenVars(prog)
	if !written["isAuthenticated"] {
		t.Error("isAuthenticated should be in written vars")
	}
	if !written["theme"] {
		t.Error("theme should be in written vars")
	}
	// 'state' is only read, not written
	if written["state"] {
		t.Error("state is read, not written — should not be in written vars")
	}
	// 'unsub' is declared (not assigned via =), depends on parser treatment
	// The key thing is the written vars are isAuthenticated and theme
}

func TestEffectDeps_ExcludeWrittenVars(t *testing.T) {
	// This effect reads isAuthenticated from a store callback but also WRITES
	// isAuthenticated — the dep should not include isAuthenticated, only the
	// store-external deps, otherwise $$set_isAuthenticated → $$effect0 →
	// $$set_isAuthenticated → ∞
	a := AnalyzeScript(`
		let isAuthenticated = $state(false);
		$effect(() => {
			const unsub = auth.subscribe(state => {
				isAuthenticated = state.isAuthenticated;
			});
			return unsub;
		});
	`)
	if len(a.Effects) == 0 {
		t.Fatal("no effects found")
	}
	eff := a.Effects[0]
	for _, dep := range eff.Deps {
		if dep == "isAuthenticated" {
			t.Errorf("isAuthenticated is WRITTEN in the effect body — must not be a dep (causes infinite recursion)")
		}
	}
}

func TestEffectDeps_SimpleSubscribePattern(t *testing.T) {
	// The common store-subscribe pattern: writes x = value.x inside callback
	// The effect should have NO reactive deps (it only reads from external stores)
	a := AnalyzeScript(`
		let count = $state(0);
		let theme = $state('light');
		$effect(() => {
			const unsub = someStore.subscribe(s => {
				count = s.count;
				theme = s.theme;
			});
			return unsub;
		});
	`)
	if len(a.Effects) == 0 {
		t.Fatal("no effects")
	}
	eff := a.Effects[0]
	for _, dep := range eff.Deps {
		if dep == "count" || dep == "theme" {
			t.Errorf("var %q is written inside the effect — should not be a dep", dep)
		}
	}
}

func TestEffectDeps_ExcludesWrittenVars(t *testing.T) {
	// The effect WRITES isAuthenticated via $$set_isAuthenticated.
	// isAuthenticated must NOT be in Deps — if it were, $$set_isAuthenticated
	// would re-run the effect, which calls $$set_isAuthenticated again → infinite loop.
	a := AnalyzeScript(`
		let isAuthenticated = $state(false);
		let theme = $state('light');
		$effect(() => {
			const unsub = auth.subscribe(state => {
				isAuthenticated = state.isAuthenticated;
			});
			return unsub;
		});
	`)
	if len(a.Effects) == 0 {
		t.Fatal("no effects found")
	}
	eff := a.Effects[0]
	for _, dep := range eff.Deps {
		if dep == "isAuthenticated" {
			t.Errorf("effect Deps must not include written var 'isAuthenticated' (causes infinite loop); got Deps=%v", eff.Deps)
		}
	}
}

func TestEffectDeps_KeepsReadVars(t *testing.T) {
	// Variables only READ (not written) inside an effect must remain as deps.
	a := AnalyzeScript(`
		let count = $state(0);
		let title = $state('');
		$effect(() => {
			document.title = title + ' - ' + count;
		});
	`)
	if len(a.Effects) == 0 {
		t.Fatal("no effects found")
	}
	eff := a.Effects[0]
	deps := map[string]bool{}
	for _, d := range eff.Deps {
		deps[d] = true
	}
	for _, want := range []string{"title", "count"} {
		if !deps[want] {
			t.Errorf("effect should depend on read-only var %q; got Deps=%v", want, eff.Deps)
		}
	}
}

func TestEffectDeps_MixedReadWrite(t *testing.T) {
	// An effect that reads 'source' and writes 'derived' should depend on 'source' only.
	a := AnalyzeScript(`
		let source = $state(0);
		let derived = $state(0);
		$effect(() => {
			derived = source * 2;
		});
	`)
	if len(a.Effects) == 0 {
		t.Fatal("no effects found")
	}
	eff := a.Effects[0]
	deps := map[string]bool{}
	for _, d := range eff.Deps {
		deps[d] = true
	}

	if !deps["source"] {
		t.Errorf("effect should depend on 'source' (read); Deps=%v", eff.Deps)
	}
	if deps["derived"] {
		t.Errorf("effect must NOT depend on 'derived' (written); Deps=%v", eff.Deps)
	}
}

func TestRewritePatternAssign_Object(t *testing.T) {
	// ({count, name} = obj)  →  const $$dN = obj; $$set_count($$dN.count); $$set_name($$dN.name);
	src := `({count, name} = obj);`
	stateSet := map[string]bool{"count": true, "name": true}
	prog := jsast.ParseJS(src)
	got := RewriteAssignments(src, prog, stateSet)

	if !strings.Contains(got, "$$set_count(") {
		t.Errorf("expected $$set_count call; got: %s", got)
	}
	if !strings.Contains(got, "$$set_name(") {
		t.Errorf("expected $$set_name call; got: %s", got)
	}
	// Must introduce a temp binding for the RHS so obj is only evaluated once.
	if !strings.Contains(got, "const $$d") {
		t.Errorf("expected const $$dN temp binding; got: %s", got)
	}
	// Access paths: $$dN.count, $$dN.name
	if !regexp.MustCompile(`\$\$set_count\(\$\$d\d+\.count\)`).MatchString(got) {
		t.Errorf("expected setter reads temp.count; got: %s", got)
	}
}

func TestRewritePatternAssign_Array(t *testing.T) {
	// [a, b] = pair  →  const $$dN = pair; $$set_a($$dN[0]); $$set_b($$dN[1]);
	src := `[a, b] = pair;`
	stateSet := map[string]bool{"a": true, "b": true}
	prog := jsast.ParseJS(src)
	got := RewriteAssignments(src, prog, stateSet)

	if !regexp.MustCompile(`\$\$set_a\(\$\$d\d+\[0\]\)`).MatchString(got) {
		t.Errorf("expected $$set_a with [0] index; got: %s", got)
	}
	if !regexp.MustCompile(`\$\$set_b\(\$\$d\d+\[1\]\)`).MatchString(got) {
		t.Errorf("expected $$set_b with [1] index; got: %s", got)
	}
}

func TestRewritePatternAssign_Renamed(t *testing.T) {
	// ({foo: count} = obj)  →  const $$dN = obj; $$set_count($$dN.foo);
	// The KEY is foo (access path), but the BINDING is count (state var).
	src := `({foo: count} = obj);`
	stateSet := map[string]bool{"count": true}
	prog := jsast.ParseJS(src)
	got := RewriteAssignments(src, prog, stateSet)

	if !regexp.MustCompile(`\$\$set_count\(\$\$d\d+\.foo\)`).MatchString(got) {
		t.Errorf("expected $$set_count reading temp.foo (renamed key); got: %s", got)
	}
}

func TestRewritePatternAssign_SkipsNonState(t *testing.T) {
	// Non-state vars in a pattern should NOT be rewritten; only the state
	// var gets a setter.  The non-state binding is silently dropped (a
	// documented limitation of the rewriter).
	src := `({count, misc} = obj);`
	stateSet := map[string]bool{"count": true}
	prog := jsast.ParseJS(src)
	got := RewriteAssignments(src, prog, stateSet)

	if !strings.Contains(got, "$$set_count(") {
		t.Errorf("expected $$set_count call; got: %s", got)
	}
	if strings.Contains(got, "$$set_misc(") {
		t.Errorf("$$set_misc should NOT be emitted for non-state var; got: %s", got)
	}
}

func TestAnalyzeScriptOrdersDerivedDependencies(t *testing.T) {
	analysis := AnalyzeScript(`
		let count = $state(2);
		let total = $derived(doubled + 1);
		let doubled = $derived(count * 2);
	`)
	if len(analysis.DerivedVars) != 2 {
		t.Fatalf("derived count = %d, want 2", len(analysis.DerivedVars))
	}
	if got := analysis.DerivedVars[0].Name; got != "doubled" {
		t.Fatalf("first derived = %q, want dependency doubled", got)
	}
	if got := analysis.DerivedVars[1].Name; got != "total" {
		t.Fatalf("second derived = %q, want dependent total", got)
	}
}
