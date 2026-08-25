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
	sfc, err := parseSFC("<script>let count = $state(0);</script><template>{count}</template>")
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
	sfc, err := parseSFC(`<script>
  let count = $state(0);
  function increment() { count++; }
</script><template><button>{count}</button></template>`)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := parseTemplate(sfc.Template)
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
	if user.SourcePos != user.Stmt.nodePos() {
		t.Fatalf("user source position = %d, want AST position %d", user.SourcePos, user.Stmt.nodePos())
	}
	if strings.Contains(PrintJS(&JsProgram{Body: []JsStmt{user.Stmt}}, PrintPretty), "$$set_count") {
		t.Fatal("source ScriptAST was lowered before input validation")
	}

	target := buildProgramUnvalidated("Plan", sfc, nodes, analysis, "vela-test", false)
	if !strings.Contains(PrintJS(target, PrintPretty), "$$set_count(count + 1)") {
		t.Fatal("target build did not lower ScriptPlan state write")
	}
}
