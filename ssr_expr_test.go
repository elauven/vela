package main

import (
	"strings"
	"testing"
)

func TestTranslateSSRExpressionUsesCachedAST(t *testing.T) {
	sfc, err := parseSFC(`<script>
export let title = "";
export let count = 0;
export let active = false;
export let items = [];
</script><template>{title + "!"}{items.length}{items[0]}{active && count > 0}</template>`)
	if err != nil {
		t.Fatalf("parseSFC: %v", err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatalf("BuildSSRRenderPlan: %v", err)
	}
	text := sfc.TemplateAST.Nodes[0].(*TextNode)
	wants := []string{
		`props.Title + "!"`,
		"vela.Length(props.Items)",
		"vela.Index(props.Items, 0.0)",
		"vela.AndLazy(func() any {\n\treturn props.Active\n}, func() any {\n\treturn vela.Number(props.Count) > vela.Number(0.0)\n})",
	}
	for i, want := range wants {
		expr, err := TranslateSSRExpression(text.Parts[i].Expr, NewSSRExprContext(plan))
		if err != nil {
			t.Fatalf("TranslateSSRExpression part %d: %v", i, err)
		}
		got, err := FormatSSRGoExpr(expr)
		if err != nil {
			t.Fatalf("FormatSSRGoExpr part %d: %v", i, err)
		}
		if got != want {
			t.Errorf("part %d = %q, want %q", i, got, want)
		}
	}
}

func TestTranslateSSRExpressionLowersNullToGoNil(t *testing.T) {
	sfc, err := parseSFC(`<template>{null}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	translated, err := TranslateSSRExpression(expr, SSRExprContext{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := FormatSSRGoExpr(translated)
	if err != nil || got != "nil" {
		t.Fatalf("null translation=%q, err=%v", got, err)
	}
}

func TestTranslateSSRExpressionReportsPositionedUnsupportedForm(t *testing.T) {
	sfc, err := parseSFC(`<template>{format(title)}</template>`)
	if err != nil {
		t.Fatalf("parseSFC: %v", err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatalf("BuildSSRRenderPlan: %v", err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	_, err = TranslateSSRExpression(expr, NewSSRExprContext(plan))
	problem, ok := err.(*SSRExpressionError)
	if !ok {
		t.Fatalf("error = %T %v, want *SSRExpressionError", err, err)
	}
	if problem.Pos != expr.Pos || problem.Error()[:len(SSRExpressionDiagnosticCode)] != SSRExpressionDiagnosticCode {
		t.Fatalf("problem = %#v", problem)
	}
}

func TestTranslateSSRExpressionExplainsLocalReactiveValue(t *testing.T) {
	sfc, err := parseSFC(`<script>let profile = $state.deep({});</script><template>{profile}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	_, err = TranslateSSRExpression(expr, NewSSRExprContext(plan))
	problem, ok := err.(*SSRExpressionError)
	if !ok || problem.Pos != expr.Pos || !strings.Contains(problem.Error(), "local reactive binding \"profile\"") {
		t.Fatalf("error=%T %v", err, err)
	}
}

func TestTranslateSSRExpressionLowersSafeLocalStateInitializer(t *testing.T) {
	sfc, err := parseSFC(`<script>let count = $state(1 + 1);</script><template>{count}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	translated, err := TranslateSSRExpression(expr, NewSSRExprContext(plan))
	if err != nil {
		t.Fatal(err)
	}
	got, err := FormatSSRGoExpr(translated)
	if err != nil || got != "vela.NumberAdd(1.0, 1.0)" {
		t.Fatalf("state initializer translation=%q, err=%v", got, err)
	}
}

func TestTranslateSSRExpressionLowersSafeLocalDerivedInitializer(t *testing.T) {
	sfc, err := parseSFC(`<script>let count = $state(2); let doubled = $derived(count * 2);</script><template>{doubled}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	translated, err := TranslateSSRExpression(expr, NewSSRExprContext(plan))
	if err != nil {
		t.Fatal(err)
	}
	got, err := FormatSSRGoExpr(translated)
	if err != nil || got != "vela.NumberMultiply(2.0, 2.0)" {
		t.Fatalf("derived initializer translation=%q, err=%v", got, err)
	}
}

func TestTranslateSSRExpressionUsesJavaScriptStringConversionForMixedPlus(t *testing.T) {
	sfc, err := parseSFC(`<script>export let count = 0;</script><template>{'item ' + count}{count + ' items'}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatal(err)
	}
	text := sfc.TemplateAST.Nodes[0].(*TextNode)
	for _, expr := range []*TemplateExpr{text.Parts[0].Expr, text.Parts[1].Expr} {
		translated, err := TranslateSSRExpression(expr, NewSSRExprContext(plan))
		if err != nil {
			t.Fatal(err)
		}
		got, err := FormatSSRGoExpr(translated)
		if err != nil || !strings.Contains(got, "vela.String(") {
			t.Fatalf("mixed plus translation=%q, err=%v", got, err)
		}
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	source, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateGeneratedSSRGo("demo.go", source); err != nil {
		t.Fatal(err)
	}
}

func TestTranslateSSRExpressionNormalizesNumericPlusAndRejectsUnknownCoercion(t *testing.T) {
	sfc, err := parseSFC(`<script>export let whole = 1; export let fraction = 0.5;</script><template>{whole + fraction}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	translated, err := TranslateSSRExpression(expr, NewSSRExprContext(plan))
	if err != nil {
		t.Fatal(err)
	}
	got, err := FormatSSRGoExpr(translated)
	if err != nil || got != "vela.NumberAdd(props.Whole, props.Fraction)" {
		t.Fatalf("numeric plus translation=%q, err=%v", got, err)
	}

	// Without a proven string or number pair, + lowers to the dynamic tier
	// (T-5.4b).
	for source, want := range map[string]string{
		`<script>export let value;</script><template>{value + 1}</template>`: "vela.Add(props.Value, 1.0)",
		`<template>{true + 1}</template>`:                                    "vela.Add(true, 1.0)",
	} {
		sfc, err := parseSFC(source)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
		if err != nil {
			t.Fatal(err)
		}
		expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
		translated, err := TranslateSSRExpression(expr, NewSSRExprContext(plan))
		if err != nil {
			t.Fatalf("unknown plus error=%v", err)
		}
		if got, _ := FormatSSRGoExpr(translated); got != want {
			t.Fatalf("unknown plus = %q, want %q", got, want)
		}
	}
}

func TestTranslateSSRExpressionUsesStrictEqualityRuntimeAndRejectsLooseEquality(t *testing.T) {
	sfc, err := parseSFC(`<template>{1 === 1.0}{null !== undefined}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	text := sfc.TemplateAST.Nodes[0].(*TextNode)
	wants := []string{
		"vela.StrictEqual(1.0, 1.0)",
		"!vela.StrictEqual(nil, vela.Undefined())",
	}
	for index, part := range text.Parts {
		translated, err := TranslateSSRExpression(part.Expr, SSRExprContext{})
		if err != nil {
			t.Fatal(err)
		}
		got, err := FormatSSRGoExpr(translated)
		if err != nil || got != wants[index] {
			t.Fatalf("strict equality %d translation=%q err=%v, want %q", index, got, err, wants[index])
		}
	}

	for _, operator := range []string{"==", "!="} {
		sfc, err := parseSFC(`<template>{1 ` + operator + ` 1}</template>`)
		if err != nil {
			t.Fatal(err)
		}
		expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
		_, err = TranslateSSRExpression(expr, SSRExprContext{})
		if err == nil || !strings.Contains(err.Error(), "loose equality") {
			t.Fatalf("operator %s error=%v", operator, err)
		}
	}
}

func TestTranslateSSRExpressionDefinesNumericAndUTF16RelationalSemantics(t *testing.T) {
	sfc, err := parseSFC(`<template>{1 < 1.5}{'𐀀' < ''}{'same' <= 'same'}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	text := sfc.TemplateAST.Nodes[0].(*TextNode)
	wants := []string{
		"vela.Number(1.0) < vela.Number(1.5)",
		`vela.StringLess("𐀀", "\ue000")`,
		`!vela.StringLess("same", "same")`,
	}
	for index, part := range text.Parts {
		translated, err := TranslateSSRExpression(part.Expr, SSRExprContext{})
		if err != nil {
			t.Fatal(err)
		}
		got, err := FormatSSRGoExpr(translated)
		if err != nil || got != wants[index] {
			t.Fatalf("relational %d translation=%q err=%v, want %q", index, got, err, wants[index])
		}
	}

	// Mixed or unproven operands use the abstract relational comparison.
	for source, want := range map[string]string{
		`<template>{'1' < 2}</template>`:                                      `vela.Less("1", 2.0)`,
		`<script>export let value;</script><template>{value >= 1}</template>`: "vela.GreaterEqual(props.Value, 1.0)",
	} {
		sfc, err := parseSFC(source)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
		if err != nil {
			t.Fatal(err)
		}
		expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
		translated, err := TranslateSSRExpression(expr, NewSSRExprContext(plan))
		if err != nil {
			t.Fatalf("dynamic relational error=%v", err)
		}
		if got, _ := FormatSSRGoExpr(translated); got != want {
			t.Fatalf("dynamic relational = %q, want %q", got, want)
		}
	}
}

func TestTranslateSSRExpressionUsesJavaScriptNumberOperations(t *testing.T) {
	sfc, err := parseSFC(`<template>{5 - 2}{3 * 4}{1 / 0}{5 % 2}{-0}{+(1 / 2)}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	text := sfc.TemplateAST.Nodes[0].(*TextNode)
	wants := []string{
		"vela.NumberSubtract(5.0, 2.0)",
		"vela.NumberMultiply(3.0, 4.0)",
		"vela.NumberDivide(1.0, 0.0)",
		"vela.NumberRemainder(5.0, 2.0)",
		"vela.NumberNegate(0.0)",
		"vela.Number(vela.NumberDivide(1.0, 2.0))",
	}
	for index, part := range text.Parts {
		translated, err := TranslateSSRExpression(part.Expr, SSRExprContext{})
		if err != nil {
			t.Fatal(err)
		}
		got, err := FormatSSRGoExpr(translated)
		if err != nil || got != wants[index] {
			t.Fatalf("number operation %d translation=%q err=%v, want %q", index, got, err, wants[index])
		}
	}

	// The Number helpers apply ToNumber to unproven operands.
	for source, want := range map[string]string{
		`<template>{true * 2}</template>`:                                 "vela.NumberMultiply(true, 2.0)",
		`<script>export let value;</script><template>{-value}</template>`: "vela.NumberNegate(props.Value)",
	} {
		sfc, err := parseSFC(source)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
		if err != nil {
			t.Fatal(err)
		}
		expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
		translated, err := TranslateSSRExpression(expr, NewSSRExprContext(plan))
		if err != nil {
			t.Fatalf("dynamic numeric error=%v", err)
		}
		if got, _ := FormatSSRGoExpr(translated); got != want {
			t.Fatalf("dynamic numeric = %q, want %q", got, want)
		}
	}
}

func TestTranslateSSRExpressionUsesSafeComputedMemberBoundary(t *testing.T) {
	sfc, err := parseSFC(`<template>{[1, 2][0]}{[1][5]}{({ title: 'ready' })['title']}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	text := sfc.TemplateAST.Nodes[0].(*TextNode)
	wants := []string{
		"vela.Index([]any{1.0, 2.0}, 0.0)",
		"vela.Index([]any{1.0}, 5.0)",
		`vela.Index(vela.NewObject().Set("title", "ready"), "title")`,
	}
	for index, part := range text.Parts {
		translated, err := TranslateSSRExpression(part.Expr, SSRExprContext{})
		if err != nil {
			t.Fatal(err)
		}
		got, err := FormatSSRGoExpr(translated)
		if err != nil || got != wants[index] {
			t.Fatalf("computed member %d translation=%q err=%v, want %q", index, got, err, wants[index])
		}
	}

	// Any key and receiver use JavaScript property-key conversion.
	for source, want := range map[string]string{
		`<template>{[1]['0']}</template>`:                                   `vela.Index([]any{1.0}, "0")`,
		`<template>{({ title: 'ready' })[0]}</template>`:                    `vela.Index(vela.NewObject().Set("title", "ready"), 0.0)`,
		`<script>export let value;</script><template>{value[0]}</template>`: "vela.Index(props.Value, 0.0)",
	} {
		sfc, err := parseSFC(source)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
		if err != nil {
			t.Fatal(err)
		}
		expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
		translated, err := TranslateSSRExpression(expr, NewSSRExprContext(plan))
		if err != nil {
			t.Fatalf("dynamic computed member error=%v", err)
		}
		if got, _ := FormatSSRGoExpr(translated); got != want {
			t.Fatalf("dynamic computed member = %q, want %q", got, want)
		}
	}
}

func TestTranslateSSRExpressionLowersNamedFormatterWithPlainConstAndDerived(t *testing.T) {
	sfc, err := parseSFC(`<script>
const prefix = 'Hello, ';
let suffix = '!';
function label(value) { return prefix + value + suffix; }
export let title = '';
let shown = $derived(label(title));
</script><template><p>{label(title)}</p><p>{shown}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatal(err)
	}
	first := sfc.TemplateAST.Nodes[0].(*ElementNode).Children[0].(*TextNode).Parts[0].Expr
	second := sfc.TemplateAST.Nodes[1].(*ElementNode).Children[0].(*TextNode).Parts[0].Expr
	for _, expr := range []*TemplateExpr{first, second} {
		translated, err := TranslateSSRExpression(expr, NewSSRExprContext(plan))
		if err != nil {
			t.Fatal(err)
		}
		got, err := FormatSSRGoExpr(translated)
		if err != nil || got != `"Hello, " + props.Title + "!"` {
			t.Fatalf("formatter translation=%q, err=%v", got, err)
		}
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	source, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateGeneratedSSRGo("demo.go", source); err != nil {
		t.Fatal(err)
	}
}

func TestTranslateSSRExpressionRejectsNonExpressionNamedFormatter(t *testing.T) {
	sfc, err := parseSFC(`<script>function label(value) { const suffix = '!'; return value + suffix; }</script><template>{label('ready')}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	_, err = TranslateSSRExpression(expr, NewSSRExprContext(plan))
	if err == nil || !strings.Contains(err.Error(), "exactly one return expression") {
		t.Fatalf("non-expression formatter error=%v", err)
	}
}

func TestTranslateSSRExpressionRejectsUnsupportedNamedFormatterCalls(t *testing.T) {
	cases := []struct {
		name    string
		source  string
		message string
	}{
		{
			name:    "external",
			source:  `<script>export let title = '';</script><template>{format(title)}</template>`,
			message: "not a supported local formatter",
		},
		{
			name:    "recursive",
			source:  `<script>function label(value) { return label(value); }</script><template>{label('ready')}</template>`,
			message: "recursively referenced",
		},
		{
			name:    "async",
			source:  `<script>async function label(value) { return value; }</script><template>{label('ready')}</template>`,
			message: "cannot be async or a generator",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sfc, err := parseSFC(tc.source)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
			if err != nil {
				t.Fatal(err)
			}
			expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
			_, err = TranslateSSRExpression(expr, NewSSRExprContext(plan))
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("formatter error=%v, want %q", err, tc.message)
			}
		})
	}
}

func TestTranslateSSRExpressionRejectsCircularLocalDerivedInitializer(t *testing.T) {
	sfc, err := parseSFC(`<script>let first = $derived(second); let second = $derived(first);</script><template>{first}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	_, err = TranslateSSRExpression(expr, NewSSRExprContext(plan))
	if err == nil || !strings.Contains(err.Error(), "circular server initializer") {
		t.Fatalf("circular derived error=%v", err)
	}
}

func TestTranslateSSRExpressionLowersTernaryThroughRuntime(t *testing.T) {
	sfc, err := parseSFC(`<script>export let count = 0;</script><template>{count ? 'many' : 'none'}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	translated, err := TranslateSSRExpression(expr, NewSSRExprContext(plan))
	if err != nil {
		t.Fatal(err)
	}
	got, err := FormatSSRGoExpr(translated)
	want := `vela.TernaryLazy(func() any {
	return props.Count
}, func() any {
	return "many"
}, func() any {
	return "none"
})`
	if err != nil || got != want {
		t.Fatalf("ternary translation=%q, err=%v", got, err)
	}
}

func TestTranslateSSRExpressionLowersLogicalOperandsThroughRuntime(t *testing.T) {
	sfc, err := parseSFC(`<script>export let title = ''; export let active = false;</script><template>{title || 'Untitled'}{active && 'active'}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatal(err)
	}
	text := sfc.TemplateAST.Nodes[0].(*TextNode)
	wants := []string{
		`vela.OrLazy(func() any {
	return props.Title
}, func() any {
	return "Untitled"
})`,
		`vela.AndLazy(func() any {
	return props.Active
}, func() any {
	return "active"
})`,
	}
	for index, want := range wants {
		translated, err := TranslateSSRExpression(text.Parts[index].Expr, NewSSRExprContext(plan))
		if err != nil {
			t.Fatal(err)
		}
		got, err := FormatSSRGoExpr(translated)
		if err != nil || got != want {
			t.Fatalf("logical expression %d=%q, err=%v", index, got, err)
		}
	}
}

func TestTranslateSSRExpressionDecodesSingleQuotedString(t *testing.T) {
	sfc, err := parseSFC(`<template>{'it\'s\nready'}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	translated, err := TranslateSSRExpression(expr, SSRExprContext{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := FormatSSRGoExpr(translated)
	if err != nil || got != `"it's\nready"` {
		t.Fatalf("single quoted translation=%q, err=%v", got, err)
	}
}

func TestTranslateSSRExpressionLowersStaticTemplateLiteral(t *testing.T) {
	sfc, err := parseSFC("<template>{`ready\\n\\`cost: \\$5`}</template>")
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	translated, err := TranslateSSRExpression(expr, SSRExprContext{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := FormatSSRGoExpr(translated)
	if err != nil || got != "\"ready\\n`cost: $5\"" {
		t.Fatalf("static template translation=%q, err=%v", got, err)
	}
}

func TestTranslateSSRExpressionLowersTypedInterpolatedTemplateLiteral(t *testing.T) {
	sfc, err := parseSFC("<script>export let title = ''; </script><template>{`hello ${title}!`}</template>")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	translated, err := TranslateSSRExpression(expr, NewSSRExprContext(plan))
	if err != nil {
		t.Fatal(err)
	}
	got, err := FormatSSRGoExpr(translated)
	if err != nil || got != `vela.TemplateString("hello ", props.Title, "!")` {
		t.Fatalf("interpolated template translation=%q, err=%v", got, err)
	}
}

func TestTranslateSSRExpressionLowersNullishInterpolatedTemplateLiteral(t *testing.T) {
	sfc, err := parseSFC("<script>export let value; </script><template>{`hello ${value}`}</template>")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	translated, err := TranslateSSRExpression(expr, NewSSRExprContext(plan))
	if err != nil {
		t.Fatal(err)
	}
	got, err := FormatSSRGoExpr(translated)
	if err != nil || got != `vela.TemplateString("hello ", props.Value, "")` {
		t.Fatalf("nullish template translation=%q, err=%v", got, err)
	}
}

func TestTranslateSSRExpressionLowersUndefinedAndMissingObjectTemplateValue(t *testing.T) {
	sfc, err := parseSFC("<script>export let profile = {}; </script><template>{`u=${undefined}; m=${profile.missing}`}</template>")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	translated, err := TranslateSSRExpression(expr, NewSSRExprContext(plan))
	if err != nil {
		t.Fatal(err)
	}
	got, err := FormatSSRGoExpr(translated)
	want := `vela.TemplateString("u=", vela.Undefined(), "; m=", vela.Get(props.Profile, "missing"), "")`
	if err != nil || got != want {
		t.Fatalf("undefined template translation=%q, err=%v", got, err)
	}
}

func TestTranslateSSRExpressionLowersArrayLiteralAndHole(t *testing.T) {
	sfc, err := parseSFC(`<template>{[1, , 'three']}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	translated, err := TranslateSSRExpression(expr, SSRExprContext{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := FormatSSRGoExpr(translated)
	if err != nil || got != `[]any{1.0, nil, "three"}` {
		t.Fatalf("array literal translation=%q, err=%v", got, err)
	}
}

func TestTranslateSSRExpressionRejectsArraySpread(t *testing.T) {
	sfc, err := parseSFC(`<script>export let items = [];</script><template>{[1, ...items]}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	_, err = TranslateSSRExpression(expr, NewSSRExprContext(plan))
	if err == nil || !strings.Contains(err.Error(), "array spread") {
		t.Fatalf("spread error=%v", err)
	}
}

func TestTranslateSSRExpressionLowersPlainObjectLiteral(t *testing.T) {
	sfc, err := parseSFC(`<template>{ { title: 'ready', selected: true } }</template>`)
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	translated, err := TranslateSSRExpression(expr, SSRExprContext{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := FormatSSRGoExpr(translated)
	if err != nil || got != `vela.NewObject().Set("title", "ready").Set("selected", true)` {
		t.Fatalf("object literal translation=%q, err=%v", got, err)
	}
}

func TestTranslateSSRExpressionKeepsLastDuplicateObjectProperty(t *testing.T) {
	sfc, err := parseSFC(`<template>{ { title: 'old', title: 'new' } }</template>`)
	if err != nil {
		t.Fatal(err)
	}
	expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
	translated, err := TranslateSSRExpression(expr, SSRExprContext{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := FormatSSRGoExpr(translated)
	if err != nil || got != `vela.NewObject().Set("title", "old").Set("title", "new")` {
		t.Fatalf("duplicate object literal translation=%q, err=%v", got, err)
	}
}

func TestTranslateSSRExpressionRejectsUnsupportedObjectForms(t *testing.T) {
	for _, source := range []string{
		`<script>export let key = '';</script><template>{ { [key]: 1 } }</template>`,
		`<template>{ { __proto__: null } }</template>`,
	} {
		sfc, err := parseSFC(source)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
		if err != nil {
			t.Fatal(err)
		}
		expr := sfc.TemplateAST.Nodes[0].(*TextNode).Parts[0].Expr
		if _, err := TranslateSSRExpression(expr, NewSSRExprContext(plan)); err == nil {
			t.Fatalf("source %q unexpectedly translated", source)
		}
	}
}

func TestTranslateSSRExpressionLowersObjectPropMember(t *testing.T) {
	sfc, err := parseSFC(`<script>export let profile = {};</script><template>{profile.name}</template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Demo", sfc, "vela-demo")
	if err != nil {
		t.Fatal(err)
	}
	text := sfc.TemplateAST.Nodes[0].(*TextNode)
	expr, err := TranslateSSRExpression(text.Parts[0].Expr, NewSSRExprContext(plan))
	if err != nil {
		t.Fatal(err)
	}
	got, err := FormatSSRGoExpr(expr)
	if err != nil {
		t.Fatal(err)
	}
	if want := `vela.Get(props.Profile, "name")`; got != want {
		t.Fatalf("member output=%q want %q", got, want)
	}
}
