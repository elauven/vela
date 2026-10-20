package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type memoryTraceSink struct {
	records []CompilationTraceV1
}

func (s *memoryTraceSink) Record(trace CompilationTraceV1) error {
	s.records = append(s.records, trace)
	return nil
}

func TestCompilerTraceRecordsStableSuccessfulPipeline(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "Trace.vela")
	const secret = "super-secret-template-text"
	source := `<script>
  let count = $state(0);
</script>
<template>
  <button @click={() => count++}>{count}</button>
  <p>` + secret + `</p>
</template>`
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	plainDest := filepath.Join(dir, "plain.js")
	if diags, err := compileFile(src, plainDest, CompileOptions{}); err != nil {
		t.Fatalf("plain compile failed: %v; diagnostics: %+v", err, diags)
	}

	sink := &memoryTraceSink{}
	tracedDest := filepath.Join(dir, "traced.js")
	if diags, err := compileFile(src, tracedDest, CompileOptions{TraceSink: sink}); err != nil {
		t.Fatalf("traced compile failed: %v; diagnostics: %+v", err, diags)
	}
	plain, err := os.ReadFile(plainDest)
	if err != nil {
		t.Fatal(err)
	}
	traced, err := os.ReadFile(tracedDest)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != string(traced) {
		t.Fatal("enabling TraceSink changed generated JavaScript")
	}

	if len(sink.records) != 1 {
		t.Fatalf("trace records = %d, want 1", len(sink.records))
	}
	trace := sink.records[0]
	if trace.SchemaVersion != 1 || trace.Outcome != "success" {
		t.Fatalf("unexpected trace header: %+v", trace)
	}
	if trace.Component.ID != "Trace.vela" || trace.Component.DisplayName != "Trace" {
		t.Fatalf("component identity leaked or changed: %+v", trace.Component)
	}
	if trace.Metrics.SourceBytes != len(source) || trace.Metrics.TemplateNodes == 0 ||
		trace.Metrics.ScriptNodes == 0 || trace.Metrics.TargetNodes == 0 ||
		trace.Metrics.OutputBytes != len(traced) || trace.Metrics.ScriptParseCount != 1 {
		t.Fatalf("incomplete successful trace metrics: %+v", trace.Metrics)
	}

	wantStages := []struct{ name, status string }{
		{"read-source", traceComplete},
		{"parse-sfc", traceComplete},
		{"parse-template", traceComplete},
		{"parse-script-and-analyze", traceComplete},
		{"validate-input", traceComplete},
		{"optimize", traceSkipped},
		{"build-target-ast", traceComplete},
		{"validate-target-ast", traceComplete},
		{"print-target", traceComplete},
		{"build-source-map", traceSkipped},
		{"write-output", traceComplete},
	}
	if len(trace.Stages) != len(wantStages) {
		t.Fatalf("stage count = %d, want %d: %+v", len(trace.Stages), len(wantStages), trace.Stages)
	}
	for i, want := range wantStages {
		got := trace.Stages[i]
		if got.Name != want.name || got.Status != want.status || got.DurationNS < 0 || got.Cache.Decision != "not-applicable" {
			t.Fatalf("stage %d = %+v, want name=%q status=%q", i, got, want.name, want.status)
		}
	}

	encoded, err := json.Marshal(trace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), dir) {
		t.Fatalf("trace JSON retained source text or absolute path: %s", encoded)
	}
}

func TestCompilerTraceStopsAtFailingStageWithoutSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "Broken.vela")
	const secret = "do-not-retain-this-source-token"
	if err := os.WriteFile(src, []byte("<script>const marker = +secret+;</script>"), 0o644); err != nil {
		t.Fatal(err)
	}

	sink := &memoryTraceSink{}
	_, err := compileFile(src, filepath.Join(dir, "Broken.js"), CompileOptions{TraceSink: sink})
	if err == nil {
		t.Fatal("compile unexpectedly succeeded")
	}
	if len(sink.records) != 1 {
		t.Fatalf("trace records = %d, want 1", len(sink.records))
	}
	trace := sink.records[0]
	if trace.Outcome != "failed" || trace.Diagnostics.Errors == 0 {
		t.Fatalf("failed trace lost outcome or diagnostics: %+v", trace)
	}
	if len(trace.Stages) != 2 || trace.Stages[0].Name != "read-source" || trace.Stages[0].Status != traceComplete ||
		trace.Stages[1].Name != "parse-sfc" || trace.Stages[1].Status != traceFailed {
		t.Fatalf("failure did not stop at parse-sfc: %+v", trace.Stages)
	}
	encoded, marshalErr := json.Marshal(trace)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), dir) {
		t.Fatalf("failed trace retained source text or absolute path: %s", encoded)
	}
}

func TestCompileFileReportsScriptASTSyntaxDiagnostic(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "BrokenScript.vela")
	source := "<script>\nimport * as value from ;\n</script>\n<template><p>ok</p></template>"
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	diags, err := compileFile(src, filepath.Join(dir, "BrokenScript.js"), CompileOptions{})
	if err == nil {
		t.Fatal("compile unexpectedly succeeded")
	}
	if len(diags) != 1 {
		t.Fatalf("diagnostics = %+v, want one script syntax error", diags)
	}
	diag := diags[0]
	if diag.Code != "VELA-JS-SYNTAX" || diag.Line != 2 || diag.Col < 1 {
		t.Fatalf("script syntax diagnostic = %+v, want VELA-JS-SYNTAX on line 2", diag)
	}
}

func TestScriptASTOwnsParserResultUsedByAnalysis(t *testing.T) {
	sfc, err := ParseSFC("<script>let count = $state(0);</script><template>{count}</template>")
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	if sfc.ScriptAST.Program == nil || analysis.Script != sfc.ScriptAST {
		t.Fatalf("analysis did not retain owned ScriptAST: script=%p analysis=%p", sfc.ScriptAST, analysis.Script)
	}
	if got, want := sfc.ScriptAST.Span.Start, sfc.ScriptOffset; got != want {
		t.Fatalf("script span start = %d, want section offset %d", got, want)
	}
}

func TestSharedScriptASTDoesNotLeakLoweredSetterIntoValidation(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "Lifecycle.vela")
	source := `<script>
  import { onMount } from 'vela';
  let count = $state(0);
  onMount(() => { count++; });
</script>
<template><p>{count}</p></template>`
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	diags, err := compileFile(src, filepath.Join(dir, "Lifecycle.js"), CompileOptions{})
	if err != nil {
		t.Fatalf("compile failed: %v; diagnostics: %+v", err, diags)
	}
	for _, diag := range diags {
		if strings.Contains(diag.Message, "$$set_") {
			t.Fatalf("input validation saw generated setter name: %+v", diag)
		}
	}
}

func TestScriptPlanRetainsOwnedUserStatementUntilTargetBuild(t *testing.T) {
	sfc, err := ParseSFC(`<script>
  let count = $state(0);
  function increment() { count++; }
</script><template><button>{count}</button></template>`)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := ParseTemplate(sfc.Template)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	var user *ScriptPlanStep
	for i := range analysis.ScriptPlan {
		if analysis.ScriptPlan[i].Kind == "user" {
			user = &analysis.ScriptPlan[i]
			break
		}
	}
	if user == nil || user.Stmt != sfc.ScriptAST.Program.Body[1] {
		t.Fatalf("ScriptPlan did not retain the owned function statement: %+v", user)
	}
	if user.SourcePos != user.Stmt.NodePos() {
		t.Fatalf("user source position = %d, want AST position %d", user.SourcePos, user.Stmt.NodePos())
	}
	if strings.Contains(PrintJS(&JsProgram{Body: []JsStmt{user.Stmt}}, PrintPretty), "$$set_count") {
		t.Fatal("source ScriptAST was lowered before input validation")
	}

	target := buildProgramUnvalidated("Plan", sfc, nodes, analysis, "vela-test", false)
	if !strings.Contains(PrintJS(target, PrintPretty), "$$set_count(count + 1)") {
		t.Fatal("target build did not lower ScriptPlan state write")
	}
}

func TestMinifyEliminatesFromOwnedScriptAST(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "Minify.vela")
	source := `<script>
  let count = $state(0);
  const unused = 42;
  function increment() { count++; }
</script>
<template><button @click={increment}>{count}</button></template>`
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	sink := &memoryTraceSink{}
	out := filepath.Join(dir, "Minify.js")
	if diags, err := compileFile(src, out, CompileOptions{Minify: true, TraceSink: sink}); err != nil {
		t.Fatalf("minify compile failed: %v; diagnostics: %+v", err, diags)
	}
	code, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(code), "unused") || !strings.Contains(string(code), "$$set_count") {
		t.Fatalf("unexpected minified output: %s", code)
	}
	if len(sink.records) != 1 || sink.records[0].Metrics.ScriptParseCount != 1 {
		t.Fatalf("minify trace did not retain one script parse: %+v", sink.records)
	}
}

func TestEmitASTUsesParsedComponentPipeline(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "Debug.vela")
	source := `<script>
  let count = $state(0);
</script>
<template><button @click={() => count++}>{count}</button></template>`
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	sink := &memoryTraceSink{}
	dest := filepath.Join(dir, "Debug.js")
	if diags, err := compileFile(src, dest, CompileOptions{EmitAST: true, TraceSink: sink}); err != nil {
		t.Fatalf("--ast compile failed: %v; diagnostics: %+v", err, diags)
	}
	astPath := filepath.Join(dir, "Debug.ast.json")
	dump, err := os.ReadFile(astPath)
	if err != nil {
		t.Fatalf("--ast did not write parsed AST dump: %v", err)
	}
	if !strings.Contains(string(dump), `"count"`) || !strings.Contains(string(dump), `"template"`) {
		t.Fatalf("unexpected AST dump: %s", dump)
	}
	if len(sink.records) != 1 || sink.records[0].Metrics.ScriptParseCount != 1 {
		t.Fatalf("--ast pipeline did not retain one script parse: %+v", sink.records)
	}
}

func TestComponentASTOwnsSectionTreesAndSpans(t *testing.T) {
	source := `<style>
  button { color: red; }
</style>
<script>
  let count = $state(0);
</script>
<template><button>{count}</button></template>`
	sfc, err := ParseSFC(source)
	if err != nil {
		t.Fatal(err)
	}
	if sfc.AST == nil || sfc.AST.Source != source {
		t.Fatalf("component AST did not retain source ownership: %+v", sfc.AST)
	}
	if sfc.AST.Script != sfc.ScriptAST || sfc.AST.Template != sfc.TemplateAST || sfc.AST.Style != sfc.StyleAST {
		t.Fatalf("section AST ownership is not canonical: %+v", sfc.AST)
	}
	for _, section := range []struct {
		name string
		span SectionSpan
		src  string
	}{
		{"script", sfc.ScriptAST.Span, sfc.Script},
		{"template", sfc.TemplateAST.Span, sfc.Template},
		{"style", sfc.StyleAST.Span, sfc.Style},
	} {
		if section.span.Start < 0 || section.span.End < section.span.Start || section.span.End > len(source) {
			t.Fatalf("%s span is outside component source: %+v", section.name, section.span)
		}
		if got := source[section.span.Start:section.span.End]; got != section.src {
			t.Fatalf("%s span source = %q, want %q", section.name, got, section.src)
		}
	}
	if len(sfc.TemplateAST.Nodes) != 1 || len(sfc.StyleAST.Rules) != 1 {
		t.Fatalf("section AST trees missing: template=%d css=%d", len(sfc.TemplateAST.Nodes), len(sfc.StyleAST.Rules))
	}
}

func TestCompileFileReportsTemplateASTSyntaxDiagnostic(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "BrokenTemplate.vela")
	source := "<template>\n{#await p then}<p>bad</p>{/await}\n</template>"
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	diags, err := compileFile(src, filepath.Join(dir, "BrokenTemplate.js"), CompileOptions{})
	if err == nil {
		t.Fatal("compile unexpectedly succeeded")
	}
	if len(diags) != 1 {
		t.Fatalf("diagnostics = %+v, want one template syntax error", diags)
	}
	if got := diags[0]; got.Code != "VELA-TEMPLATE-SYNTAX" || got.Line != 2 || got.Col < 1 {
		t.Fatalf("template syntax diagnostic = %+v, want VELA-TEMPLATE-SYNTAX on line 2", got)
	}
}

func TestCompileFileRejectsEmptyStructuredKeyExpressions(t *testing.T) {
	tests := []struct {
		name, template string
	}{
		{"key block", `{#key}<p>bad key</p>{/key}`},
		{"each key", `{#each rows as row ()}<p>{row}</p>{/each}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "BrokenKey.vela")
			source := "<template>" + tc.template + "</template>"
			if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			diags, err := compileFile(src, filepath.Join(dir, "BrokenKey.js"), CompileOptions{})
			if err == nil || len(diags) == 0 {
				t.Fatalf("diagnostics = %+v, err = %v; want template AST failure", diags, err)
			}
			var validation *Diagnostic
			for i := range diags {
				if diags[i].Code == "VELA-TEMPLATE-AST" {
					validation = diags[i]
					break
				}
			}
			if validation == nil || validation.Line != 1 || validation.Col < 1 {
				t.Fatalf("diagnostics = %+v, want positioned VELA-TEMPLATE-AST", diags)
			}
		})
	}
}

func TestValidateTemplateASTRejectsMissingCachedExpression(t *testing.T) {
	ast := &TemplateAST{
		Source: "{count}",
		Span:   SectionSpan{Start: 0, End: len("{count}")},
		Nodes: []Node{&TextNode{Parts: []*Part{{
			Static: false,
			Value:  "count",
			Pos:    1,
		}}}},
	}
	issues := ValidateTemplateAST(ast)
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "cached expression AST") {
		t.Fatalf("validation issues = %+v, want missing cached expression diagnostic", issues)
	}
}

func TestTemplateStructuralSpansUseComponentSource(t *testing.T) {
	source := `<template><div :title={label}>{label}</div>{#each items as item}<p>{item}</p>{/each}</template>`
	sfc, err := ParseSFC(source)
	if err != nil {
		t.Fatal(err)
	}
	div := sfc.TemplateAST.Nodes[0].(*ElementNode)
	if got := source[div.Span.Start:div.Span.End]; got != `<div :title={label}>{label}</div>` {
		t.Fatalf("element span = %q", got)
	}
	if got := source[div.Attrs[0].Span.Start:div.Attrs[0].Span.End]; got != `:title={label}` {
		t.Fatalf("attribute span = %q", got)
	}
	part := div.Children[0].(*TextNode).Parts[0]
	if got := source[part.Span.Start:part.Span.End]; got != `label` {
		t.Fatalf("interpolation part span = %q", got)
	}
	each := sfc.TemplateAST.Nodes[1].(*EachBlockNode)
	if got := source[each.Span.Start:each.Span.End]; got != `{#each items as item}<p>{item}</p>{/each}` {
		t.Fatalf("each block span = %q", got)
	}
}

func TestComponentAndSlotSpansUseComponentSource(t *testing.T) {
	source := `<template><Card :title={title}><slot name="actions" /><svelte:fragment slot="footer"><p>Footer</p></svelte:fragment></Card></template>`
	sfc, err := ParseSFC(source)
	if err != nil {
		t.Fatal(err)
	}
	card := sfc.TemplateAST.Nodes[0].(*ComponentNode)
	if got := source[card.Span.Start:card.Span.End]; got != `<Card :title={title}><slot name="actions" /><svelte:fragment slot="footer"><p>Footer</p></svelte:fragment></Card>` {
		t.Fatalf("component span = %q", got)
	}
	actions := card.Slots["default"][0].(*SlotNode)
	if got := source[actions.Span.Start:actions.Span.End]; got != `<slot name="actions" />` {
		t.Fatalf("slot span = %q", got)
	}
	footer := card.Slots["footer"][0].(*ElementNode)
	if got := source[footer.Span.Start:footer.Span.End]; got != `<p>Footer</p>` {
		t.Fatalf("fragment child span = %q", got)
	}
}

func TestIfBranchSpansUseComponentSource(t *testing.T) {
	source := `<template>{#if first}<p>first</p>{:else if second}<p>second</p>{:else}<p>last</p>{/if}</template>`
	sfc, err := ParseSFC(source)
	if err != nil {
		t.Fatal(err)
	}
	chain := sfc.TemplateAST.Nodes[0].(*IfChainNode)
	want := []string{
		`{#if first}<p>first</p>`,
		`{:else if second}<p>second</p>`,
		`{:else}<p>last</p>`,
	}
	for i, branch := range chain.Branches {
		if got := source[branch.Span.Start:branch.Span.End]; got != want[i] {
			t.Fatalf("branch %d span = %q, want %q", i, got, want[i])
		}
	}
}

func TestValidateTemplateASTRejectsEscapingStructuralSpans(t *testing.T) {
	source := `<template>{#if visible}<p>{label}</p>{/if}{#each items as item}<p>{item}</p>{/each}{#await request}<p>loading</p>{/await}</template>`
	sfc, err := ParseSFC(source)
	if err != nil {
		t.Fatal(err)
	}
	chain := sfc.TemplateAST.Nodes[0].(*IfChainNode)
	each := sfc.TemplateAST.Nodes[1].(*EachBlockNode)
	await := sfc.TemplateAST.Nodes[2].(*AwaitBlockNode)
	chain.Branches[0].Span.End = sfc.TemplateAST.Span.End + 1
	each.Span.End = sfc.TemplateAST.Span.End + 1
	await.Span.End = sfc.TemplateAST.Span.End + 1
	issues := ValidateTemplateAST(sfc.TemplateAST)
	if len(issues) != 3 {
		t.Fatalf("validation issues = %+v, want three escaping structural span diagnostics", issues)
	}
}

func TestCompileFileReportsCSSSyntaxDiagnostic(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "BrokenStyle.vela")
	source := "<style>\n.card { color: red;\n</style>\n<template><p>ok</p></template>"
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	diags, err := compileFile(src, filepath.Join(dir, "BrokenStyle.js"), CompileOptions{})
	if err == nil {
		t.Fatal("compile unexpectedly succeeded")
	}
	if len(diags) != 1 || diags[0].Code != "VELA-CSS-SYNTAX" {
		t.Fatalf("diagnostics = %+v, want one VELA-CSS-SYNTAX error", diags)
	}
}

func TestCompileFileReportsCSSSyntaxPositions(t *testing.T) {
	tests := []struct {
		name, css string
		line, col int
	}{
		{name: "unexpected closing brace", css: "}", line: 2, col: 1},
		{name: "unterminated string", css: "'", line: 2, col: 2},
		{name: "unterminated block", css: ".x {", line: 2, col: 5},
		{name: "unterminated comment", css: "/*", line: 2, col: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "BrokenStyle.vela")
			source := "<style>\n" + tc.css + "\n</style>\n<template><p>ok</p></template>"
			if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			diags, err := compileFile(src, filepath.Join(dir, "BrokenStyle.js"), CompileOptions{})
			if err == nil || len(diags) != 1 {
				t.Fatalf("diagnostics = %+v, err = %v", diags, err)
			}
			diag := diags[0]
			if diag.Code != "VELA-CSS-SYNTAX" || diag.Line != tc.line || diag.Col != tc.col {
				t.Fatalf("diagnostic = %+v, want VELA-CSS-SYNTAX at %d:%d", diag, tc.line, tc.col)
			}
		})
	}
}

func TestCSSStructuralSpansUseComponentSource(t *testing.T) {
	source := `<style>@media screen { .card, .panel { color: red; } }</style><template><p>ok</p></template>`
	sfc, err := ParseSFC(source)
	if err != nil {
		t.Fatal(err)
	}
	media := sfc.StyleAST.Rules[0].(*CssAtRule)
	if got := source[media.Span.Start:media.Span.End]; got != `@media screen { .card, .panel { color: red; } }` {
		t.Fatalf("at-rule span = %q", got)
	}
	rule := media.Block[0].(*CssQualifiedRule)
	if got := source[rule.Selectors[1].Span.Start:rule.Selectors[1].Span.End]; got != `.panel` {
		t.Fatalf("selector span = %q", got)
	}
	if got := source[rule.Decls[0].Span.Start:rule.Decls[0].Span.End]; got != `color: red;` {
		t.Fatalf("declaration span = %q", got)
	}
}

func TestTemplateExprDepsRequiresCachedAST(t *testing.T) {
	if got := TemplateExprDeps(nil, []string{"count"}); len(got) != 0 {
		t.Fatalf("nil cached expression dependencies = %v, want none", got)
	}
	expr := NewTemplateExpr("count + other", 0)
	if got := TemplateExprDeps(expr, []string{"count", "other"}); len(got) != 2 || got[0] != "count" || got[1] != "other" {
		t.Fatalf("cached expression dependencies = %v", got)
	}
}

func TestKeyframeOpaqueRegionUsesComponentSourceSpan(t *testing.T) {
	source := `<style>@keyframes fade { from { opacity: 0; } to { opacity: 1; } }</style><template><p>ok</p></template>`
	sfc, err := ParseSFC(source)
	if err != nil {
		t.Fatal(err)
	}
	keyframes := sfc.StyleAST.Rules[0].(*CssAtRule)
	raw := keyframes.Block[0].(*CssAtRule)
	if got := source[raw.Span.Start:raw.Span.End]; got != ` from { opacity: 0; } to { opacity: 1; } ` {
		t.Fatalf("opaque keyframe span = %q", got)
	}
	if issues := ValidateCssAST(sfc.StyleAST); len(issues) != 0 {
		t.Fatalf("validation issues = %+v", issues)
	}
}

func TestValidateCssASTRejectsEscapingSelectorTokenSpan(t *testing.T) {
	source := `<style>.card { color: red; }</style><template><p>ok</p></template>`
	sfc, err := ParseSFC(source)
	if err != nil {
		t.Fatal(err)
	}
	rule := sfc.StyleAST.Rules[0].(*CssQualifiedRule)
	rule.Selectors[0].Parts[0].End = sfc.StyleAST.Span.End + 1
	issues := ValidateCssAST(sfc.StyleAST)
	if len(issues) == 0 || !strings.Contains(issues[0].Message, "selector token") {
		t.Fatalf("issues = %+v, want escaping selector token span diagnostic", issues)
	}
}

func TestValidateTemplateASTRejectsEscapingExpressionSpan(t *testing.T) {
	expr := NewTemplateExpr("count", 1)
	expr.Span = SectionSpan{Start: 11, End: 30}
	ast := &TemplateAST{
		Source: "{count}",
		Span:   SectionSpan{Start: 10, End: 17},
		Nodes: []Node{&TextNode{Parts: []*Part{{
			Static: false,
			Value:  "count",
			Pos:    1,
			Expr:   expr,
		}}}},
	}
	issues := ValidateTemplateAST(ast)
	if len(issues) == 0 || !strings.Contains(issues[0].Message, "escapes template source") {
		t.Fatalf("issues = %+v, want escaping span diagnostic", issues)
	}
}
