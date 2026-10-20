package codegen

import (
	"slices"
	"strings"
	"testing"

	"github.com/elauven/vela/internal/analyzer"
	"github.com/elauven/vela/internal/jsast"
	"github.com/elauven/vela/internal/sfcast"
)

func TestParseTemplate_QuotedObjectClassKeepsTypedValueKind(t *testing.T) {
	nodes, err := sfcast.ParseTemplate(`<div :class="{'active': enabled}" />`)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	attr := nodes[0].(*sfcast.ElementNode).Attrs[0]
	if attr.ValueKind != sfcast.AttrValueQuotedObjectCompat {
		t.Fatalf("value kind = %d, want quoted object compatibility", attr.ValueKind)
	}
	if _, ok := TemplateExprRoot(attr.Expr).(*jsast.JsObjectExpr); !ok {
		t.Fatalf("expression root = %T, want *JsObjectExpr", TemplateExprRoot(attr.Expr))
	}
}

func TestAnalyzeScript_DerivedVars(t *testing.T) {
	a := analyzer.AnalyzeScript(`
let count   = $state(0);
let doubled = $derived(count * 2);
let label   = $derived(count > 0 ? 'positive' : 'zero');
`)
	if len(a.DerivedVars) != 2 {
		t.Fatalf("expected 2 derived vars, got %d", len(a.DerivedVars))
	}
	d := a.DerivedVars[0]
	if d.Name != "doubled" {
		t.Errorf("name: want 'doubled', got %q", d.Name)
	}
	if !slices.Contains(d.Deps, "count") {
		t.Error("doubled should depend on count")
	}
}

func TestAnalyzeScript_Effects(t *testing.T) {
	a := analyzer.AnalyzeScript(`
let count = $state(0);
$effect(() => {
  document.title = count;
});
`)
	if len(a.Effects) != 1 {
		t.Fatalf("expected 1 effect, got %d", len(a.Effects))
	}
	if !slices.Contains(a.Effects[0].Deps, "count") {
		t.Error("effect should depend on count")
	}
}

func TestGenerateCode_ScopedStyle(t *testing.T) {
	sfc := &sfcast.SFC{
		Script:   "",
		Style:    `p { color: blue; }`,
		Template: `<p>hi</p>`,
	}
	nodes, _ := sfcast.ParseTemplate(sfc.Template)
	analysis := analyzer.AnalyzeScript(sfc.Script)
	code := GenerateCodeAST("Styled", sfc, nodes, analysis, "vela-abc")

	if !strings.Contains(code, "createElement('style')") {
		t.Error("expected style injection")
	}
	if !strings.Contains(code, "p[vela-abc]") {
		t.Error("expected scoped CSS selector")
	}
}

func TestCollectIdents_SkipsPropertyNames(t *testing.T) {
	// In obj.foo, only "obj" is a free identifier; "foo" is a property name.
	prog := jsast.ParseJS("obj.foo")
	names := jsast.CollectIdents(prog)
	if !slices.Contains(names, "obj") {
		t.Error("should contain obj")
	}
	// "foo" appears as a JsIdent in the AST member expr prop, but WalkNode
	// skips non-computed member expr properties — so it should NOT appear.
	// (This is intentional: property names are not variable references.)
	if slices.Contains(names, "foo") {
		t.Error("should not contain property name foo as a dep")
	}
}

func TestCollectIdents_TemplateInDerived(t *testing.T) {
	// $derived with template literal — deps inside ${} must be found
	prog := jsast.ParseJS("`Count: ${count} items, doubled: ${doubled}`")
	names := jsast.CollectIdents(prog)
	if !slices.Contains(names, "count") {
		t.Error("should find count inside template hole")
	}
	if !slices.Contains(names, "doubled") {
		t.Error("should find doubled inside template hole")
	}
}

func TestAnalyzeScript_DerivedDepsTransitive(t *testing.T) {
	// doubled depends on count; label depends on doubled — both must chain correctly
	a := analyzer.AnalyzeScript(`
		let count   = $state(0);
		let doubled = $derived(count * 2);
		let label   = $derived(doubled > 10 ? 'big' : 'small');
	`)
	if len(a.DerivedVars) != 2 {
		t.Fatalf("expected 2 derived vars, got %d", len(a.DerivedVars))
	}
	if !slices.Contains(a.DerivedVars[0].Deps, "count") {
		t.Error("doubled should depend on count")
	}
	if !slices.Contains(a.DerivedVars[1].Deps, "doubled") {
		t.Error("label should depend on doubled")
	}
}

func TestGenerateCode_Props(t *testing.T) {
	sfc := &sfcast.SFC{
		Script:   `export let greeting = 'Hello'; export let name;`,
		Template: `<p>{greeting}, {name}!</p>`,
	}
	nodes, _ := sfcast.ParseTemplate(sfc.Template)
	analysis := analyzer.AnalyzeScript(sfc.Script)
	code := GenerateCodeAST("Greeter", sfc, nodes, analysis, "vela-test")

	// Props initialised from $$props with fallback to default
	if !strings.Contains(code, "$$props.greeting") {
		t.Error("expected $$props.greeting initialisation")
	}
	if !strings.Contains(code, "'Hello'") {
		t.Error("expected default value 'Hello' for greeting")
	}
	// Required prop (no default)
	if !strings.Contains(code, "$$props.name") {
		t.Error("expected $$props.name initialisation")
	}
	// set() should accept both props
	if !strings.Contains(code, "$$set_greeting") {
		t.Error("expected $$set_greeting in set()")
	}
}

func TestGenerateCode_OnMount(t *testing.T) {
	sfc := &sfcast.SFC{
		Script:   `onMount(() => { document.body.classList.add('loaded'); });`,
		Template: `<div>hello</div>`,
	}
	nodes, _ := sfcast.ParseTemplate(sfc.Template)
	a := analyzer.AnalyzeScript(sfc.Script)
	code := GenerateCodeAST("App", sfc, nodes, a, "vela-test")
	if !strings.Contains(code, "document.body.classList.add") {
		t.Errorf("onMount body should appear in generated code:\n%s", code)
	}
}

func TestGenerateCode_OnDestroy(t *testing.T) {
	sfc := &sfcast.SFC{
		Script:   `onDestroy(() => { cleanup(); });`,
		Template: `<div>bye</div>`,
	}
	nodes, _ := sfcast.ParseTemplate(sfc.Template)
	a := analyzer.AnalyzeScript(sfc.Script)
	code := GenerateCodeAST("Bye", sfc, nodes, a, "vela-test")
	// onDestroy body must appear inside destroy()
	destroyStart := strings.Index(code, "destroy()")
	if destroyStart < 0 {
		t.Fatal("no destroy() in output")
	}
	afterDestroy := code[destroyStart:]
	if !strings.Contains(afterDestroy, "cleanup()") {
		t.Errorf("onDestroy body should be inside destroy(); code:\n%s", afterDestroy[:200])
	}
}

func TestGenerateCode_BindableCallback(t *testing.T) {
	sfc := &sfcast.SFC{
		Script:   `export let count = $bindable(0);`,
		Template: `<span>{count}</span>`,
	}
	nodes, _ := sfcast.ParseTemplate(sfc.Template)
	a := analyzer.AnalyzeScript(sfc.Script)
	code := GenerateCodeAST("Counter", sfc, nodes, a, "vela-test")
	// Should emit $$onChange_count reference
	if !strings.Contains(code, "$$onChange_count") {
		t.Errorf("expected $$onChange_count for bindable prop; code:\n%s", code)
	}
}

func TestGenerateCode_SlotRendering(t *testing.T) {
	sfc := &sfcast.SFC{
		Script:   ``,
		Template: `<div><slot name="header">Default header</slot></div>`,
	}
	nodes, _ := sfcast.ParseTemplate(sfc.Template)
	a := analyzer.AnalyzeScript(sfc.Script)
	code := GenerateCodeAST("Card", sfc, nodes, a, "vela-test")
	if !strings.Contains(code, `$$slots["header"]`) && !strings.Contains(code, `$$slots['header']`) {
		t.Errorf("expected slot lookup in generated code:\n%s", code)
	}
	if !strings.Contains(code, "$$slots") {
		t.Error("expected $$slots parameter")
	}
}

func TestGenerateCode_ComponentPassesSlots(t *testing.T) {
	sfc := &sfcast.SFC{
		Script:   `import { Card } from './Card.js';`,
		Template: `<Card><p slot="header">My Title</p></Card>`,
	}
	nodes, _ := sfcast.ParseTemplate(sfc.Template)
	a := analyzer.AnalyzeScript(sfc.Script)
	code := GenerateCodeAST("App", sfc, nodes, a, "vela-test")
	// The component mount call should include a slots argument
	if !strings.Contains(code, "\"header\"") && !strings.Contains(code, "'header'") {
		t.Errorf("expected slot content in component call:\n%s", code)
	}
}

func TestGenerateCode_DispatchFunction(t *testing.T) {
	sfc := &sfcast.SFC{Script: ``, Template: `<div>hello</div>`}
	nodes, _ := sfcast.ParseTemplate(sfc.Template)
	code := GenerateCodeAST("MyComp", sfc, nodes, analyzer.AnalyzeScript(sfc.Script), "vela-test")
	// Runtime helpers are usage-tracked. A component without child events
	// must not import or bind dispatch machinery.
	if strings.Contains(code, "$$dispatch") {
		t.Errorf("static component unexpectedly emits dispatch wiring:\n%s", code[:min(400, len(code))])
	}
}

func TestFullPipeline_UserCard(t *testing.T) {
	src := `
<script>
  export let name = $bindable('World');
  export let role = 'Member';
  let greeting = $derived('Hello, ' + name + '!');
  onMount(() => { document.body.setAttribute('data-mounted', 'true'); });
  onDestroy(() => { document.body.removeAttribute('data-mounted'); });
  $effect(() => { document.title = name; });
</script>
<style>
  .card { padding: 1rem; }
  .name { font-weight: bold; }
</style>
<template>
  <div class="card">
    <h2 class="name">{name}</h2>
    <p>{greeting}</p>
    <slot name="footer"><span>Default footer</span></slot>
  </div>
</template>
`
	sfc, err := sfcast.ParseSFC(src)
	if err != nil {
		t.Fatalf("parseSFC: %v", err)
	}
	nodes, err := sfcast.ParseTemplate(sfc.Template)
	if err != nil {
		t.Fatalf("parseTemplate: %v", err)
	}
	a := analyzer.AnalyzeScript(sfc.Script)
	code := GenerateCodeAST("UserCard", sfc, nodes, a, "vela-card")

	must := []string{
		"export function UserCard(",
		"$$props, $$slots",
		"$$onChange_name",               // bindable callback
		"document.body.setAttribute",    // onMount body
		"document.body.removeAttribute", // onDestroy body
		"$$effect0",                     // $effect
		"let greeting",                  // derived
		"$$upd_greeting",                // derived updater
		`$$slots["footer"]`,             // slot lookup (or with single quotes)
		".card[vela-card]",              // scoped CSS
		"destroy()",                     // cleanup handle
	}
	// slot lookup may use single or double quotes
	for _, want := range must {
		alt := ""
		if want == `$$slots["footer"]` {
			alt = `$$slots['footer']`
		}
		if !strings.Contains(code, want) && (alt == "" || !strings.Contains(code, alt)) {
			t.Errorf("missing in output: %q\n--- first 500 chars ---\n%s", want, code[:min(500, len(code))])
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// findSetterEnd returns the byte offset just past the closing `}` of the
// top-level function definition that starts at `start` in `src`.  Scans
// brace pairs while honouring string, template-literal, and comment
// tokens so braces inside them don't throw off the counter.
//
// Used by setter-body tests that need to examine ONLY the target
// function's body — the previous approach (search for "\n  function ")
// returned -1 when the setter was the last function in the output,
// which made the slice bleed into later code and matched unrelated
// $$effect0() calls in the initial-render / mount sections.
func findSetterEnd(src string, start int) int {
	// Seek to first `{`
	i := start
	for i < len(src) && src[i] != '{' {
		i++
	}
	if i >= len(src) {
		return len(src)
	}
	depth := 0
	for i < len(src) {
		c := src[i]
		switch c {
		case '{':
			depth++
			i++
		case '}':
			depth--
			i++
			if depth == 0 {
				return i
			}
		case '"', '\'', '`':
			q := c
			i++
			for i < len(src) {
				if src[i] == '\\' && i+1 < len(src) {
					i += 2
					continue
				}
				if src[i] == q {
					i++
					break
				}
				i++
			}
		case '/':
			if i+1 < len(src) && src[i+1] == '/' {
				for i < len(src) && src[i] != '\n' {
					i++
				}
			} else if i+1 < len(src) && src[i+1] == '*' {
				i += 2
				for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
					i++
				}
				i += 2
			} else {
				i++
			}
		default:
			i++
		}
	}
	return len(src)
}

func TestGenerateCode_ArrowHandlerNotDoubleWrapped(t *testing.T) {
	sfc := &sfcast.SFC{
		Script:   `let x = $state(0); function foo() { x++; }`,
		Template: `<button @click="() => foo()">click</button>`,
	}
	nodes, _ := sfcast.ParseTemplate(sfc.Template)
	a := analyzer.AnalyzeScript(sfc.Script)
	code := GenerateCodeAST("Btn", sfc, nodes, a, "vela-test")

	// Should NOT produce: ($event) => { () => foo(); }
	if strings.Contains(code, "($event) => { () =>") {
		t.Errorf("arrow handler was double-wrapped:\n%s", code)
	}
	// Should produce the arrow directly (stored in a const for removeEventListener)
	if !strings.Contains(code, "() => foo()") {
		t.Errorf("arrow handler body lost from output:\n%s", code)
	}
}

// Regression: expression handler @input="($e) => x = $e.target.value"
// should be stored directly, not wrapped in another arrow.
func TestGenerateCode_ExprHandlerNotDoubleWrapped(t *testing.T) {
	sfc := &sfcast.SFC{
		Script:   `let v = $state('');`,
		Template: `<input @input="($e) => v = $e.target.value" />`,
	}
	nodes, _ := sfcast.ParseTemplate(sfc.Template)
	a := analyzer.AnalyzeScript(sfc.Script)
	code := GenerateCodeAST("Input", sfc, nodes, a, "vela-test")

	// Must NOT double-wrap: ($event) => { ($e) => v = $e.target.value; }
	if strings.Contains(code, "($event) => { ($e)") {
		t.Errorf("parameterised arrow handler was double-wrapped:\n%s", code)
	}
}

// isHandlerExpr unit tests
func TestIsHandlerExpr(t *testing.T) {
	yes := []string{
		"() => foo()",
		"($e) => bar($e)",
		"(e) => { e.preventDefault(); }",
		"function() { return 1; }",
		"async () => await fetch(url)",
		"e => doThing(e)",
	}
	no := []string{
		"myFunc",
		"handleClick",
		"",
		"obj.method",
	}
	for _, s := range yes {
		if !isHandlerExpr(s) {
			t.Errorf("isHandlerExpr(%q) should be true", s)
		}
	}
	for _, s := range no {
		if isHandlerExpr(s) {
			t.Errorf("isHandlerExpr(%q) should be false", s)
		}
	}
}

func TestIsBoolAttr(t *testing.T) {
	bools := []string{"checked", "disabled", "readonly", "required",
		"selected", "multiple", "hidden", "autofocus", "loop", "muted", "open"}
	nonBools := []string{"class", "id", "value", "href", "src",
		"placeholder", "type", "name", "data-x", "aria-label"}

	for _, a := range bools {
		if !IsBoolAttr(a) {
			t.Errorf("isBoolAttr(%q) should be true", a)
		}
	}
	for _, a := range nonBools {
		if IsBoolAttr(a) {
			t.Errorf("isBoolAttr(%q) should be false", a)
		}
	}
}

func TestBindAttrStmt_BooleanUsesProperty(t *testing.T) {
	stmt := bindAttrStmt("$$el", "checked", "todo.done", "  ")
	// Must use property assignment, NOT setAttribute
	if strings.Contains(stmt, "setAttribute") {
		t.Errorf("boolean attr should use property, not setAttribute: %s", stmt)
	}
	if !strings.Contains(stmt, "$$el.checked") {
		t.Errorf("expected $$el.checked property assignment: %s", stmt)
	}
	if !strings.Contains(stmt, "!!(todo.done)") {
		t.Errorf("expected !!() coercion for boolean: %s", stmt)
	}
}

func TestBindAttrStmt_ClassUsesClassName(t *testing.T) {
	stmt := bindAttrStmt("$$el", "class", "myClass", "  ")
	if !strings.Contains(stmt, "$$el.className") {
		t.Errorf("class attr should use className: %s", stmt)
	}
}

func TestBindAttrStmt_RegularUsesSetAttribute(t *testing.T) {
	stmt := bindAttrStmt("$$el", "href", "url", "  ")
	if !strings.Contains(stmt, "setAttribute('href'") {
		t.Errorf("regular attr should use setAttribute: %s", stmt)
	}
}

func TestGenerateCode_BooleanBindingCheckbox(t *testing.T) {
	sfc := &sfcast.SFC{
		Script:   `let done = $state(false);`,
		Template: `<input type="checkbox" :checked="done" />`,
	}
	nodes, _ := sfcast.ParseTemplate(sfc.Template)
	a := analyzer.AnalyzeScript(sfc.Script)
	code := GenerateCodeAST("Check", sfc, nodes, a, "vela-test")

	// Must not use setAttribute for checked
	if strings.Contains(code, "setAttribute('checked'") {
		t.Errorf("boolean :checked must not use setAttribute:\n%s", code)
	}
	// Must use property assignment with !!()
	if !strings.Contains(code, ".checked = !!done") {
		t.Errorf("expected .checked = !!done in output:\n%s", code)
	}
}

func TestGenerateCode_BooleanBindingDisabled(t *testing.T) {
	sfc := &sfcast.SFC{
		Script:   `let loading = $state(true);`,
		Template: `<button :disabled="loading">Submit</button>`,
	}
	nodes, _ := sfcast.ParseTemplate(sfc.Template)
	a := analyzer.AnalyzeScript(sfc.Script)
	code := GenerateCodeAST("Btn", sfc, nodes, a, "vela-test")

	if strings.Contains(code, "setAttribute('disabled'") {
		t.Errorf("boolean :disabled must not use setAttribute:\n%s", code)
	}
	if !strings.Contains(code, ".disabled = !!loading") {
		t.Errorf("expected .disabled = !!loading:\n%s", code)
	}
}

func TestGenerateCode_BooleanBindingHidden(t *testing.T) {
	sfc := &sfcast.SFC{
		Script:   `let visible = $state(true);`,
		Template: `<div :hidden="!visible">content</div>`,
	}
	nodes, _ := sfcast.ParseTemplate(sfc.Template)
	a := analyzer.AnalyzeScript(sfc.Script)
	code := GenerateCodeAST("Vis", sfc, nodes, a, "vela-test")

	if strings.Contains(code, "setAttribute('hidden'") {
		t.Errorf("boolean :hidden must not use setAttribute:\n%s", code)
	}
	if !strings.Contains(code, ".hidden = !!!visible") {
		t.Errorf("expected .hidden = !!!visible:\n%s", code)
	}
}

func TestGenerateCode_PropAssignmentInMethod(t *testing.T) {
	sfc := &sfcast.SFC{
		Script: `
			export let name = $bindable('World');
			let nameEdit = $state('');
			function saveEdit() { name = nameEdit; }
		`,
		Template: `<div>{name}</div>`,
	}
	nodes, _ := sfcast.ParseTemplate(sfc.Template)
	a := analyzer.AnalyzeScript(sfc.Script)
	code := GenerateCodeAST("Card", sfc, nodes, a, "vela-test")

	if !strings.Contains(code, "$$set_name") {
		t.Errorf("saveEdit should call $$set_name:\n%s", code)
	}
}

func TestFullPipeline_PropBindingAndLifecycle(t *testing.T) {
	src := `
<script>
  export let name = $bindable('World');
  let online = $state(false);
  let initials = $derived(name.split(' ').map(w => w[0]).join(''));

  onMount(() => {
    online = true;
  });
  onDestroy(() => {
    online = false;
  });

  $effect(() => {
    document.title = name;
  });

  function saveEdit(v) { name = v; }
</script>
<template>
  <div>
    <span>{initials}</span>
    <input type="text" :value="name" @input="($e) => name = $e.target.value" />
    <input type="checkbox" :checked="online" />
  </div>
</template>
`
	sfc, err := sfcast.ParseSFC(src)
	if err != nil {
		t.Fatalf("parseSFC: %v", err)
	}
	nodes, err := sfcast.ParseTemplate(sfc.Template)
	if err != nil {
		t.Fatalf("parseTemplate: %v", err)
	}
	a := analyzer.AnalyzeScript(sfc.Script)
	code := GenerateCodeAST("Profile", sfc, nodes, a, "vela-prof")

	checks := []struct{ desc, want, notWant string }{
		{"prop setter exists", "function $$set_name", ""},
		{"prop init from $$props", "$$props.name", ""},
		{"onMount body rewritten", "$$set_online(true)", ""},
		{"onDestroy body rewritten", "$$set_online(false)", ""},
		{"saveEdit uses setter", "$$set_name(v)", ""},
		{"input rewrite for name", "$$set_name", ""},
		{"checked uses property", ".checked = !!online", "setAttribute('checked'"},
		{"$$onChange_name for bindable", "$$onChange_name", ""},
	}
	for _, c := range checks {
		hasWant := c.want == "" || strings.Contains(code, c.want)
		hasNotWant := c.notWant != "" && strings.Contains(code, c.notWant)
		if !hasWant || hasNotWant {
			t.Errorf("[%s] want=%q notWant=%q\ncode excerpt:\n%s",
				c.desc, c.want, c.notWant, code[:min(600, len(code))])
		}
	}
}

// Full pipeline: the Clock component had this exact bug in its onMount body
func TestFullPipeline_ClockTernaryInOnMount(t *testing.T) {
	src := `
<script>
  let ampm = $state('');
  let hours = $state('--');
  onMount(() => {
    setInterval(() => {
      let h = new Date().getHours();
      ampm = (h >= 12) ? 'PM' : 'AM';
      hours = String(h % 12 || 12).padStart(2, '0');
    }, 1000);
  });
</script>
<template><div>{hours} {ampm}</div></template>
`
	sfc, err := sfcast.ParseSFC(src)
	if err != nil {
		t.Fatalf("parseSFC: %v", err)
	}
	nodes, err := sfcast.ParseTemplate(sfc.Template)
	if err != nil {
		t.Fatalf("parseTemplate: %v", err)
	}
	a := analyzer.AnalyzeScript(sfc.Script)

	// The onMount body should have the ternary rewritten correctly
	if len(a.Lifecycles) == 0 {
		t.Fatal("no lifecycle found")
	}
	body := analyzer.PrintNode(a.Lifecycles[0].BodyAST)

	// Must produce a valid setter call — not a truncated one
	if !strings.Contains(body, "$$set_ampm") {
		t.Errorf("ampm not rewritten in lifecycle body:\n%s", body)
	}
	// The ternary must be fully inside the setter call
	// Bad: $$set_ampm(h >= 12) ? 'PM' : 'AM')
	// Good: $$set_ampm((h >= 12) ? 'PM' : 'AM')
	if strings.Contains(body, "$$set_ampm(h >= 12) ?") {
		t.Errorf("ternary was truncated in rewrite (paren bug):\n%s", body)
	}
	if strings.Contains(body, "$$set_ampm((h >= 12) ? 'PM' : 'AM')") ||
		strings.Contains(body, "$$set_ampm(h >= 12 ? 'PM' : 'AM')") {
		// Either form is correct
	} else {
		t.Errorf("unexpected ampm rewrite form:\n%s", body)
	}

	// Codegen should succeed without syntax errors
	code := GenerateCodeAST("Clock", sfc, nodes, a, "vela-test")
	if !strings.Contains(code, "export function Clock(") {
		t.Errorf("codegen failed:\n%s", code[:min(300, len(code))])
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func TestGenerateCode_PlainComponentGuardsCustomEvent(t *testing.T) {
	// Even plain (non-#each) component wrappers must guard events.
	sfc := &sfcast.SFC{
		Script:   `import { Modal } from './Modal.js';`,
		Template: `<div><Modal @close="($e) => handleClose($e.detail)" @select="onSelect" /></div>`,
	}
	nodes, _ := sfcast.ParseTemplate(sfc.Template)
	a := analyzer.AnalyzeScript(sfc.Script)
	code := GenerateCodeAST("App", sfc, nodes, a, "vela-test")

	if !strings.Contains(code, "instanceof CustomEvent") {
		t.Errorf("plain component event handlers must guard with instanceof CustomEvent:\n%s",
			code[:min(600, len(code))])
	}
}

func TestRewriteHandlerAssignments_ConciseArrow(t *testing.T) {
	sfc := &sfcast.SFC{
		Script:   `let visible = $state(false);`,
		Template: `<input type="checkbox" @change="($e) => visible = $e.target.checked" />`,
	}
	nodes, _ := sfcast.ParseTemplate(sfc.Template)
	a := analyzer.AnalyzeScript(sfc.Script)
	code := GenerateCodeAST("Comp", sfc, nodes, a, "vela-test")

	// The handler must call $$set_visible, NOT do a raw assignment
	if strings.Contains(code, "visible = $e.target.checked") && !strings.Contains(code, "$$set_visible") {
		t.Errorf("concise arrow assignment not rewritten:\n%s", code)
	}
	if !strings.Contains(code, "$$set_visible") {
		t.Errorf("expected $$set_visible in handler:\n%s", code[:min(600, len(code))])
	}
}

func TestComponentCallbackPropInlineAssignmentIsReactive(t *testing.T) {
	sfc := &sfcast.SFC{
		Script:   `import { Canvas } from './Canvas.vela'; let selectedId = $state(null);`,
		Template: `<Canvas onSelect={(id) => selectedId = id} />`,
	}
	nodes, err := sfcast.ParseTemplate(sfc.Template)
	if err != nil {
		t.Fatal(err)
	}
	code := GenerateCodeAST("Editor", sfc, nodes, analyzer.AnalyzeScript(sfc.Script), "vela-test")
	if !strings.Contains(code, "$$set_selectedId(id)") {
		t.Fatalf("inline component callback bypassed reactive setter:\n%s", code)
	}
	if strings.Contains(code, "onSelect: (id) => selectedId = id") {
		t.Fatalf("raw state assignment remained in callback prop:\n%s", code)
	}
}

func TestRewriteHandlerAssignments_BlockBody(t *testing.T) {
	sfc := &sfcast.SFC{
		Script:   `let count = $state(0); let step = $state(1);`,
		Template: `<button @click="($e) => { count = count + step; step = 2; }">go</button>`,
	}
	nodes, _ := sfcast.ParseTemplate(sfc.Template)
	a := analyzer.AnalyzeScript(sfc.Script)
	code := GenerateCodeAST("Comp", sfc, nodes, a, "vela-test")

	if !strings.Contains(code, "$$set_count") {
		t.Errorf("block arrow count assignment not rewritten:\n%s", code[:min(500, len(code))])
	}
	if !strings.Contains(code, "$$set_step") {
		t.Errorf("block arrow step assignment not rewritten:\n%s", code[:min(500, len(code))])
	}
}

func TestRewriteHandlerAssignments_MultipleAssignments(t *testing.T) {
	sfc := &sfcast.SFC{
		Script:   `let x = $state(0); let y = $state(0);`,
		Template: `<button @click="() => { x = 1; y = 2; }">set</button>`,
	}
	nodes, _ := sfcast.ParseTemplate(sfc.Template)
	a := analyzer.AnalyzeScript(sfc.Script)
	code := GenerateCodeAST("Comp", sfc, nodes, a, "vela-test")

	if !strings.Contains(code, "$$set_x(1)") {
		t.Errorf("x not rewritten: %s", code[:min(400, len(code))])
	}
	if !strings.Contains(code, "$$set_y(2)") {
		t.Errorf("y not rewritten: %s", code[:min(400, len(code))])
	}
}

func TestRewriteHandlerAssignments_NoFalsePositive(t *testing.T) {
	// Non-state variables and method calls should not be touched
	sfc := &sfcast.SFC{
		Script:   `let count = $state(0);`,
		Template: `<button @click="($e) => { $e.preventDefault(); localVar = 5; }">go</button>`,
	}
	nodes, _ := sfcast.ParseTemplate(sfc.Template)
	a := analyzer.AnalyzeScript(sfc.Script)
	code := GenerateCodeAST("Comp", sfc, nodes, a, "vela-test")

	// localVar is not state — should NOT be rewritten
	if strings.Contains(code, "$$set_localVar") {
		t.Errorf("non-state var should not be rewritten: %s", code[:min(400, len(code))])
	}
	// preventDefault should be preserved
	if !strings.Contains(code, "preventDefault") {
		t.Errorf("preventDefault was removed from handler")
	}
}

func TestGenerateCode_PropChangeTriggersDerivedUpdate(t *testing.T) {
	// When a prop changes via set(), the derived vars that depend on it must update.
	sfc := &sfcast.SFC{
		Script: `
			export let pinned = false;
			export let selected = false;
			let editMode = $state(false);
			let cardClass = $derived('card' + (pinned ? ' card-pinned' : '') + (selected ? ' card-selected' : '') + (editMode ? ' card-editing' : ''));
		`,
		Template: `<div :class="cardClass">content</div>`,
	}
	nodes, _ := sfcast.ParseTemplate(sfc.Template)
	a := analyzer.AnalyzeScript(sfc.Script)
	code := GenerateCodeAST("Card", sfc, nodes, a, "vela-test")

	// $$set_pinned must call $$upd_cardClass
	pinSetStart := strings.Index(code, "function $$set_pinned")
	if pinSetStart < 0 {
		t.Fatal("no $$set_pinned found")
	}
	pinSetEnd := pinSetStart + strings.Index(code[pinSetStart:], "\n  function ")
	if pinSetEnd <= pinSetStart {
		pinSetEnd = len(code)
	}
	pinSetter := code[pinSetStart:pinSetEnd]
	if !strings.Contains(pinSetter, "$$upd_cardClass") {
		t.Errorf("$$set_pinned must call $$upd_cardClass; setter:\n%s", pinSetter)
	}

	// $$set_selected must also call $$upd_cardClass
	selSetStart := strings.Index(code, "function $$set_selected")
	if selSetStart < 0 {
		t.Fatal("no $$set_selected found")
	}
	selSetEnd := selSetStart + strings.Index(code[selSetStart:], "\n  function ")
	if selSetEnd <= selSetStart {
		selSetEnd = len(code)
	}
	selSetter := code[selSetStart:selSetEnd]
	if !strings.Contains(selSetter, "$$upd_cardClass") {
		t.Errorf("$$set_selected must call $$upd_cardClass; setter:\n%s", selSetter)
	}
}

func TestGenerateCode_EffectDoesNotCallItsOwnSetter(t *testing.T) {
	// When isAuthenticated is WRITTEN inside an effect,
	// $$set_isAuthenticated must NOT call $$effect0 again.
	sfc := &sfcast.SFC{
		Script: `
			let isAuthenticated = $state(false);
			$effect(() => {
				const unsub = auth.subscribe(state => {
					isAuthenticated = state.isAuthenticated;
				});
				return unsub;
			});
		`,
		Template: `{#if isAuthenticated}<div>logged in</div>{/if}`,
	}
	nodes, _ := sfcast.ParseTemplate(sfc.Template)
	a := analyzer.AnalyzeScript(sfc.Script)
	code := GenerateCodeAST("App", sfc, nodes, a, "vela-test")

	// Find $$set_isAuthenticated body
	setIdx := strings.Index(code, "function $$set_isAuthenticated")
	if setIdx < 0 {
		t.Fatal("no $$set_isAuthenticated found")
	}
	// The setter's body is a single JS function — end it at the matching
	// close brace.  Previous approach (search for "\n  function ") gave
	// -1 when the setter was the LAST function in the output, which made
	// the slice bleed into later code (initial-render / mount sections)
	// and falsely triggered the $$effect0() contains-check.
	setEnd := findSetterEnd(code, setIdx)
	setter := code[setIdx:setEnd]

	if strings.Contains(setter, "$$effect0()") {
		t.Errorf("$$set_isAuthenticated must NOT call $$effect0 — infinite recursion:\n%s", setter)
	}
}

func TestGenerateCode_EffectCleanupEmitted(t *testing.T) {
	// Effects must emit $$cleanup variables and call them before re-running
	sfc := &sfcast.SFC{
		Script: `
			let x = $state(0);
			$effect(() => {
				const unsub = store.subscribe(v => { x = v; });
				return unsub;
			});
		`,
		Template: `<div>{x}</div>`,
	}
	nodes, _ := sfcast.ParseTemplate(sfc.Template)
	a := analyzer.AnalyzeScript(sfc.Script)
	code := GenerateCodeAST("App", sfc, nodes, a, "vela-test")

	if !strings.Contains(code, "$$cleanup0") {
		t.Error("expected $$cleanup0 variable for effect cleanup")
	}
	if !strings.Contains(code, "$$cleanup0 = null") {
		t.Error("expected $$cleanup0 = null initialization")
	}
	// Cleanup called in destroy()
	destroyIdx := strings.Index(code, "destroy() {")
	if destroyIdx < 0 {
		t.Fatal("no destroy() found")
	}
	destroySection := code[destroyIdx:]
	if !strings.Contains(destroySection, "$$cleanup0") {
		t.Error("effect cleanup should be called in destroy()")
	}
}

func TestGenerateCode_EffectHasCleanup(t *testing.T) {
	sfc := &sfcast.SFC{
		Script: `
			let x = $state(0);
			$effect(() => {
				const unsub = store.subscribe(v => { x = v; });
				return unsub;
			});
		`,
		Template: `<div>{x}</div>`,
	}
	nodes, _ := sfcast.ParseTemplate(sfc.Template)
	a := analyzer.AnalyzeScript(sfc.Script)
	code := GenerateCodeAST("App", sfc, nodes, a, "vela-test")

	// Cleanup variable must be declared
	if !strings.Contains(code, "$$cleanup0") {
		t.Errorf("effect cleanup var not emitted:\n%s", code[:min(400, len(code))])
	}
	// Effect must store return value via IIFE
	if !strings.Contains(code, "$$cleanup0 = (function()") {
		t.Errorf("effect return value not captured:\n%s", code[:min(600, len(code))])
	}
	// Cleanup must be called before re-run
	if !strings.Contains(code, "if ($$cleanup0) {") || !strings.Contains(code, "$$cleanup0();") {
		t.Errorf("cleanup not called before effect re-runs:\n%s", code[:min(600, len(code))])
	}
	// Cleanup must be called in destroy()
	destroyIdx := strings.Index(code, "destroy()")
	if destroyIdx < 0 {
		t.Fatal("no destroy()")
	}
	// Clamp the slice bound — generated code length varies with changes
	// to the emitter, and hard-coding +200 will occasionally panic.
	destroyEnd := destroyIdx + 200
	if destroyEnd > len(code) {
		destroyEnd = len(code)
	}
	destroyBlock := code[destroyIdx:destroyEnd]
	if !strings.Contains(destroyBlock, "$$cleanup0") {
		t.Errorf("cleanup not called in destroy():\n%s", destroyBlock)
	}
}

func TestGenerateCode_SubscribeEffectNoInfiniteLoop(t *testing.T) {
	// The key regression: $$set_isAuthenticated must NOT call $$effect0 if
	// isAuthenticated is only written (not read) inside the effect.
	sfc := &sfcast.SFC{
		Script: `
			let isAuthenticated = $state(false);
			$effect(() => {
				const unsub = auth.subscribe(state => {
					isAuthenticated = state.isAuthenticated;
				});
				return unsub;
			});
		`,
		Template: `<div>{isAuthenticated ? 'logged in' : 'logged out'}</div>`,
	}
	nodes, _ := sfcast.ParseTemplate(sfc.Template)
	a := analyzer.AnalyzeScript(sfc.Script)
	code := GenerateCodeAST("App", sfc, nodes, a, "vela-test")

	// Find $$set_isAuthenticated
	setterStart := strings.Index(code, "function $$set_isAuthenticated")
	if setterStart < 0 {
		t.Fatal("no $$set_isAuthenticated found")
	}
	setterEnd := findSetterEnd(code, setterStart)
	setter := code[setterStart:setterEnd]

	// The setter must NOT call $$effect0 — that would cause infinite recursion
	if strings.Contains(setter, "$$effect0()") {
		t.Errorf("$$set_isAuthenticated calls $$effect0 — this causes infinite recursion!\nSetter:\n%s", setter)
	}
}

func TestRewriteItemRef_Shadowing(t *testing.T) {
	// `(u) => fn(u)`  — inner u is a shadowed parameter, leave it alone.
	src := `(u) => fn(u)`
	got := rewriteItemRef(src, "u", "$$box")
	if got != src {
		t.Errorf("shadowed param should not be rewritten;\n  src = %q\n  got = %q", src, got)
	}
}

func TestRewriteItemRef_BlockShadow(t *testing.T) {
	// `() => { const u = 99; return u; }` — block-scoped shadow.
	src := `() => { const u = 99; return u; }`
	got := rewriteItemRef(src, "u", "$$box")
	if got != src {
		t.Errorf("block-scoped shadow should not be rewritten;\n  src = %q\n  got = %q", src, got)
	}
}

func TestRewriteItemRef_MemberAccess(t *testing.T) {
	// `obj.u` — property access, don't rewrite.
	// `obj[u]` — computed index, DO rewrite the `u`.
	// `u.id` — u is the object, .id is the property — rewrite u only.
	cases := []struct{ src, want string }{
		{`obj.u`, `obj.u`},
		{`obj[u]`, `obj[$$box]`},
		{`u.id`, `$$box.id`},
		{`obj?.u`, `obj?.u`}, // optional property access also skipped
	}
	for _, tc := range cases {
		got := rewriteItemRef(tc.src, "u", "$$box")
		if got != tc.want {
			t.Errorf("rewriteItemRef(%q): want %q, got %q", tc.src, tc.want, got)
		}
	}
}

func TestRewriteItemRef_ObjectKeys(t *testing.T) {
	// `{u: x}` — u is a key, NOT a ref; leave alone.
	// `{[u]: 1}` — computed key, DO rewrite.
	// `{u}` — shorthand: key AND value are the same JsIdent; as a value-pos
	//         reference, it should be rewritten.
	cases := []struct{ src, want string }{
		{`({u: 1})`, `({u: 1})`},
		{`({[u]: 1})`, `({[$$box]: 1})`},
		{`({u})`, `({$$box})`}, // shorthand — u appears in value position
	}
	for _, tc := range cases {
		got := rewriteItemRef(tc.src, "u", "$$box")
		if got != tc.want {
			t.Errorf("rewriteItemRef(%q): want %q, got %q", tc.src, tc.want, got)
		}
	}
}

func TestRewriteItemRefAST_MatchesScopedRewriteRules(t *testing.T) {
	cases := []struct{ src, want string }{
		{`(u) => fn(u)`, `(u)=>fn(u)`},
		{`() => { const u = 99; return u; }`, `()=>{const u=99;return u;}`},
		{`obj[u]`, `obj[$$box]`},
		{`u.id`, `$$box.id`},
		{`obj.u`, `obj.u`},
		{`({[u]: 1})`, `{[$$box]:1}`},
		{`({u})`, `{u:$$box}`},
		{`({...u})`, `{...$$box}`},
		{`() => { try { return u; } catch (u) { return u; } }`, `()=>{try{return $$box;}catch(u){return u;}}`},
		{"`${u}`", "`${$$box}`"},
	}
	for _, tc := range cases {
		root := jsast.ParseJSExpr(tc.src)
		if root == nil {
			t.Fatalf("ParseJSExpr(%q) returned nil", tc.src)
		}
		got := jsast.PrintExpr(rewriteItemRefAST(root, "u", "$$box"), jsast.PrintMinified)
		if got != tc.want {
			t.Errorf("rewriteItemRefAST(%q): want %q, got %q", tc.src, tc.want, got)
		}
	}
}

func TestTwoWayBindSpec(t *testing.T) {
	cases := []struct {
		tag, attr        string
		wantEvent, wantP string
		ok               bool
	}{
		{"input", "value", "input", "value", true},
		{"textarea", "value", "input", "value", true},
		{"select", "value", "change", "value", true},
		{"input", "checked", "change", "checked", true},
		{"div", "value", "", "", false},        // value on non-form element: unsupported
		{"textarea", "checked", "", "", false}, // checked on textarea: unsupported
	}
	for _, tc := range cases {
		spec, ok := twoWayBindSpec(tc.tag, tc.attr)
		if ok != tc.ok {
			t.Errorf("<%s bind:%s>: ok=%v, want %v", tc.tag, tc.attr, ok, tc.ok)
			continue
		}
		if ok && (spec.event != tc.wantEvent || spec.readProp != tc.wantP) {
			t.Errorf("<%s bind:%s>: spec=%+v, want event=%q readProp=%q",
				tc.tag, tc.attr, spec, tc.wantEvent, tc.wantP)
		}
	}
}

func TestTwoWayBinding_OneWayBindDoesNotWireListener(t *testing.T) {
	// :value="x" is strictly one-way — no listener should be wired
	// (that's the contract after the turn-13 redesign).
	sfc := &sfcast.SFC{
		Script:   `let name = $state('');`,
		Template: `<input :value="name" />`,
	}
	nodes, _ := sfcast.ParseTemplate(sfc.Template)
	a := analyzer.AnalyzeScript(sfc.Script)
	code := GenerateCodeAST("Form", sfc, nodes, a, "vela-test")

	if strings.Contains(code, ".addEventListener('input',") {
		t.Errorf("one-way :value should NOT auto-wire an input listener; got:\n%s", code)
	}
}

func TestImports_MultilineNamed(t *testing.T) {
	script := `import {
  MultiA,
  MultiB as Aliased,
  MultiC
} from "./multi.vela";`
	recs := ParseImportRecords(script, 0)
	if len(recs) != 1 {
		t.Fatalf("expected 1 import record, got %d", len(recs))
	}
	r := recs[0]
	if r.Path != "./multi.vela" {
		t.Errorf("path: want ./multi.vela, got %q", r.Path)
	}
	want := []string{"MultiA", "Aliased", "MultiC"}
	if len(r.Names) != len(want) {
		t.Fatalf("names: want %v, got %v", want, r.Names)
	}
	for i, w := range want {
		if r.Names[i] != w {
			t.Errorf("name[%d]: want %q, got %q", i, w, r.Names[i])
		}
	}
}

func TestImports_MixedDefaultAndNamed(t *testing.T) {
	script := `import Def, { A, B as C } from './mixed.vela';`
	recs := ParseImportRecords(script, 0)
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	// Names list should include both the default and the named locals.
	wantNames := map[string]bool{"Def": true, "A": true, "C": true}
	gotNames := map[string]bool{}
	for _, n := range recs[0].Names {
		gotNames[n] = true
	}
	for n := range wantNames {
		if !gotNames[n] {
			t.Errorf("expected %q in Names; got %v", n, recs[0].Names)
		}
	}
}

func TestImports_SideEffect(t *testing.T) {
	script := `import './polyfill.vela';`
	recs := ParseImportRecords(script, 0)
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	if recs[0].Path != "./polyfill.vela" {
		t.Errorf("path: want ./polyfill.vela, got %q", recs[0].Path)
	}
	if len(recs[0].Names) != 0 {
		t.Errorf("side-effect import should have no Names; got %v", recs[0].Names)
	}
}

func TestImports_Namespace(t *testing.T) {
	script := `import * as Utils from './utils.vela';`
	recs := ParseImportRecords(script, 0)
	if len(recs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(recs))
	}
	if len(recs[0].Names) != 1 || recs[0].Names[0] != "Utils" {
		t.Errorf("namespace import names: want [Utils], got %v", recs[0].Names)
	}
}

func TestRewriteScriptImports_VelaToJs(t *testing.T) {
	// .vela paths should be rewritten to .js in every import form,
	// preserving multi-line formatting and trailing comments.
	script := `import Card from './Card.vela';
import { A, B } from "./widgets.vela";
import * as Utils from './utils.vela';
import './polyfill.vela';
import {
  Multi1,
  Multi2
} from "./multi.vela";`

	got := RewriteScriptImports(script)

	// Every .vela suffix must be gone — all rewritten to .js.
	if strings.Contains(got, ".vela") {
		t.Errorf(".vela suffix leaked through rewrite; got:\n%s", got)
	}
	// Spot-check each form emitted its .js counterpart.
	for _, want := range []string{
		`'./Card.js'`, `"./widgets.js"`, `'./utils.js'`, `'./polyfill.js'`, `"./multi.js"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %s in rewritten script; got:\n%s", want, got)
		}
	}
	// Multi-line structure must survive (sanity: line count is roughly the same).
	origLines := strings.Count(script, "\n")
	gotLines := strings.Count(got, "\n")
	if gotLines != origLines {
		t.Errorf("line count changed during rewrite (structural damage): orig=%d got=%d",
			origLines, gotLines)
	}
}

func TestRewriteScriptImports_NodeModulesUnchanged(t *testing.T) {
	// Bare specifiers (no leading ./ or /) are node-module paths and
	// must pass through unchanged — rewriting them would break imports
	// from npm packages.
	script := `import { createStore } from 'some-pkg';
import lodash from "lodash";`
	got := RewriteScriptImports(script)
	if got != script {
		t.Errorf("node-module imports should be unchanged;\n  orig: %q\n  got:  %q", script, got)
	}
}

// staticText / dynText / staticElem / dynElem are tiny helpers to build AST
// fixtures for the predicate tests below.  Keeping them in one place so each
// test reads as a sequence of clear shape assertions.
func staticText(s string) *sfcast.TextNode {
	return &sfcast.TextNode{Parts: []*sfcast.Part{{Static: true, Value: s}}}
}

func dynText(expr string) *sfcast.TextNode {
	return &sfcast.TextNode{Parts: []*sfcast.Part{{Static: false, Value: expr}}}
}

func staticElem(tag string, children ...sfcast.Node) *sfcast.ElementNode {
	return &sfcast.ElementNode{Tag: tag, Children: children}
}

func eventElem(tag, eventName, handler string, children ...sfcast.Node) *sfcast.ElementNode {
	return &sfcast.ElementNode{
		Tag:      tag,
		Attrs:    []*sfcast.Attr{{Name: eventName, Value: handler, IsEvent: true}},
		Children: children,
	}
}

func bindElem(tag, attrName, expr string, children ...sfcast.Node) *sfcast.ElementNode {
	return &sfcast.ElementNode{
		Tag:      tag,
		Attrs:    []*sfcast.Attr{{Name: attrName, Value: expr, IsBind: true}},
		Children: children,
	}
}

func twoWayElem(tag, attrName, expr string) *sfcast.ElementNode {
	return &sfcast.ElementNode{
		Tag:   tag,
		Attrs: []*sfcast.Attr{{Name: attrName, Value: expr, IsBind: true, IsTwoWay: true}},
	}
}

func TestIsFullyStatic(t *testing.T) {
	cases := []struct {
		name string
		node sfcast.Node
		want bool
	}{
		{"static-text", staticText("hello"), true},
		{"dyn-text", dynText("name"), false},
		{"empty-elem", staticElem("br"), true},
		{"elem-with-static-children", staticElem("div", staticText("hi")), true},
		{"deeply-nested-static",
			staticElem("section",
				staticElem("header",
					staticElem("h1", staticText("Title")))),
			true},
		{"elem-with-event", eventElem("button", "click", "onClick"), false},
		{"elem-with-bind", bindElem("input", "value", "name"), false},
		{"elem-with-two-way", twoWayElem("input", "value", "name"), false},
		{"static-frame-with-dyn-leaf",
			staticElem("div", eventElem("button", "click", "onClick")),
			false},
		{"static-frame-with-dyn-text",
			staticElem("p", dynText("greeting")),
			false},
		{"if-chain", &sfcast.IfChainNode{}, false},
		{"each-block", &sfcast.EachBlockNode{}, false},
		{"component", &sfcast.ComponentNode{Name: "MyComp"}, false},
		{"slot", &sfcast.SlotNode{Name: "default"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isFullyStatic(c.node); got != c.want {
				t.Errorf("isFullyStatic(%s) = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

func TestEscapeHTMLText(t *testing.T) {
	cases := map[string]string{
		"hello":      "hello",         // unchanged
		"a < b":      "a &lt; b",      // <
		"a > b":      "a &gt; b",      // >
		"x & y":      "x &amp; y",     // &
		"<&>":        "&lt;&amp;&gt;", // multiple
		"":           "",              // empty
		"plain text": "plain text",    // no special chars
	}
	for in, want := range cases {
		if got := escapeHTMLText(in); got != want {
			t.Errorf("escapeHTMLText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEscapeHTMLAttr(t *testing.T) {
	cases := map[string]string{
		"hello":        "hello",
		`a & b`:        "a &amp; b",
		`he said "hi"`: `he said &quot;hi&quot;`,
		`a"&"b`:        `a&quot;&amp;&quot;b`,
		"":             "",
		// < and > are NOT escaped inside attribute values per HTML spec.
		"a < b": "a < b",
	}
	for in, want := range cases {
		if got := escapeHTMLAttr(in); got != want {
			t.Errorf("escapeHTMLAttr(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEscapeJSString(t *testing.T) {
	// Used inside single-quoted JS string literals — must escape ', \, and
	// the line-break/whitespace characters that would break the literal.
	cases := map[string]string{
		"hello":        "hello",
		"it's":         `it\'s`,
		"a\\b":         `a\\b`,
		"line1\nline2": `line1\nline2`,
		"a\tb":         `a\tb`,
		"":             "",
		// Double quotes are NOT escaped — they're fine inside a single-
		// quoted JS string.  This is intentional: HTML attributes use ".
		`<div class="x">`: `<div class="x">`,
	}
	for in, want := range cases {
		if got := escapeJSString(in); got != want {
			t.Errorf("escapeJSString(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVoidTags_ContainsKnownVoidElements(t *testing.T) {
	// Pin the void-tag list — the HTML spec defines exactly these elements
	// as void (no closing tag, no children).  buildHoleTemplate consults
	// this map; getting it wrong produces invalid HTML and broken templates.
	mustHave := []string{"area", "base", "br", "col", "embed", "hr", "img",
		"input", "link", "meta", "param", "source", "track", "wbr"}
	for _, t2 := range mustHave {
		if !voidTags[t2] {
			t.Errorf("voidTags missing %q", t2)
		}
	}
	// Common non-void elements MUST NOT be in the list — emitting them
	// without a closing tag would produce malformed HTML.
	mustNotHave := []string{"div", "span", "p", "button", "form", "label",
		"select", "option", "textarea", "table", "tr", "td", "th"}
	for _, t2 := range mustNotHave {
		if voidTags[t2] {
			t.Errorf("voidTags must not contain %q (it's a normal element)", t2)
		}
	}
}

func TestScriptPlanInitializesDerivedBeforeFollowingUserStatement(t *testing.T) {
	script := `
		let count = $state(2);
		let doubled = $derived(count * 2);
		const captured = doubled + 1;
	`
	analysis := analyzer.AnalyzeScript(script)
	sfc, err := sfcast.ParseSFC("<script>" + script + "</script><template><p>{captured}</p></template>")
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := sfcast.ParseTemplate(sfc.Template)
	if err != nil {
		t.Fatal(err)
	}
	code := GenerateCodeAST("DerivedPlan", sfc, tmpl, analysis, "vela-000000")
	update := strings.Index(code, "();")
	captured := strings.Index(code, "const captured = doubled + 1;")
	if update < 0 || captured < 0 || update > captured {
		t.Fatalf("derived initializer must precede following user statement:\n%s", code)
	}
}
