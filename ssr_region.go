package main

import (
	"encoding/json"
	"errors"
	"fmt"
	goast "go/ast"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Server-evaluated region records (docs/SSR_TRANSLATION_CONTRACT.md §2,
// §10, task 043). The renderer records one site for every region root it
// lowers (T-2.1) and keeps going after a failure, so a component reports
// all of its failures in one build (D-10.3a). Event handlers, effects,
// lifecycle hooks, and binding write-back are never lowered and therefore
// never recorded (T-2.2).

const (
	SSROutcomeNative      = "server-native"
	SSROutcomeUnsupported = "unsupported"
	SSROutcomeHost        = "server-host"
)

// SSRSiteRecord is the T-2.3 record of one region site.
type SSRSiteRecord struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Name    string `json:"name,omitempty"`
	Pos     int    `json:"-"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
	Outcome string `json:"outcome"`
	Tier    string `json:"tier,omitempty"`
	Type    string `json:"type,omitempty"`
	// Required is set for sites the server render evaluates. Declaration
	// sites ($state, $derived) are recorded with their own outcome; the
	// render needs them only where another site reads them (Reads).
	Required bool `json:"required"`
	// Host is the Go function of a server-host function site.
	Host       string             `json:"host,omitempty"`
	Reads      []string           `json:"reads,omitempty"`
	Diagnostic *SSRSiteDiagnostic `json:"diagnostic,omitempty"`
}

// SSRSiteDiagnostic is the §10.1 content of a failed site.
type SSRSiteDiagnostic struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Reasons []string `json:"reasons"`
	Fix     string   `json:"fix"`
	// Host is the host implementation that would fix the site.
	Host *SSRHostFix `json:"host,omitempty"`
	// Error keeps the one-line message of the translator.
	Error string `json:"-"`
	// Pos is the .vela offset of the script code the problem is in, or -1
	// when the site position applies; Line and Column locate it.
	Pos    int `json:"-"`
	Line   int `json:"line,omitempty"`
	Column int `json:"column,omitempty"`
}

// SSRRegion collects the site records of one component.
type SSRRegion struct {
	Component string
	// funcs compiles the component functions declarations call (task 045).
	funcs          *ssrFuncTable
	TemplateOffset int
	ScriptOffset   int
	Records        []SSRSiteRecord
	counters       map[string]int
	source         string
	// File names the component in RenderError sites; usesJS and throwing
	// record that lowered sites reference the evaluation state and how many
	// of them can throw.
	File     string
	usesJS   bool
	throwing int
	// project locates the host tree; hostImports collects the host packages
	// the lowered sites call; calls the outcome of every called function.
	project     *SSRProject
	hostImports map[string]string
	// modules maps the variables of the modules the region uses to their
	// import names.
	modules map[string]string
	calls   map[string]ssrCallOutcome
}

func newSSRRegion(plan *SSRRenderPlan) *SSRRegion {
	return &SSRRegion{Component: plan.ComponentName, TemplateOffset: plan.TemplateOffset, ScriptOffset: plan.ScriptOffset, counters: map[string]int{}, project: plan.HostProject, hostImports: map[string]string{}, calls: map[string]ssrCallOutcome{}}
}

// ssrSiteRoot describes a region root for the first reason of a chain.
func ssrSiteRoot(kind, name string) string {
	switch kind {
	case "text":
		return "template text"
	case "await":
		return "{#await} operand"
	case "attribute":
		return fmt.Sprintf("attribute %q", name)
	case "class":
		return "class:" + name
	case "style":
		return "style:" + name
	case "if":
		return "{#if} condition"
	case "each":
		return "{#each} iterable"
	case "each-key":
		return "{#each} key"
	case "key":
		return "{#key} expression"
	case "component-prop":
		component, prop, _ := strings.Cut(name, ".")
		return fmt.Sprintf("prop %q of <%s>", prop, component)
	case "prop-default":
		return fmt.Sprintf("default of prop %q", name)
	case "state":
		return fmt.Sprintf("$state %q", name)
	case "derived":
		return fmt.Sprintf("$derived %q", name)
	case "binding":
		return fmt.Sprintf("binding %q", name)
	case "form-value":
		return fmt.Sprintf("%s value", name)
	}
	return kind
}

// add records a site built from ir or failed with err.
func (r *SSRRegion) add(kind, name string, pos int, required bool, ir SSRIRExpr, reads map[string]bool, err error) {
	r.counters[kind]++
	record := SSRSiteRecord{ID: fmt.Sprintf("%s#%d", kind, r.counters[kind]), Kind: kind, Name: name, Pos: pos, Required: required}
	for name := range reads {
		record.Reads = append(record.Reads, name)
	}
	sort.Strings(record.Reads)
	if err != nil {
		record.Outcome = SSROutcomeUnsupported
		record.Diagnostic = ssrSiteDiagnostic(kind, name, err, r.project)
	} else {
		record.Outcome = SSROutcomeNative
		facts := ir.irBase().Facts
		record.Tier, record.Type = facts.Tier(), facts.SemanticType()
	}
	r.Records = append(r.Records, record)
}

func ssrSiteDiagnostic(kind, name string, err error, project *SSRProject) *SSRSiteDiagnostic {
	diagnostic := &SSRSiteDiagnostic{Code: SSRExpressionDiagnosticCode, Message: err.Error(), Reasons: []string{ssrSiteRoot(kind, name)}, Error: err.Error(), Pos: -1}
	if problem, ok := err.(*SSRExpressionError); ok {
		diagnostic.Code = problem.code()
		diagnostic.Message = problem.innermost()
		diagnostic.Reasons = append(diagnostic.Reasons, problem.Reasons...)
		if problem.SourceSet {
			diagnostic.Pos = problem.Source
		}
		if problem.Host != nil {
			file, directive, signature := problem.Host.describe(project)
			diagnostic.Host = &SSRHostFix{File: file, Directive: directive, Signature: signature}
		}
	}
	if kind == "prop-default" {
		diagnostic.Error = fmt.Sprintf("SSR default prop %q: %s", name, diagnostic.Error)
	}
	diagnostic.Fix = ssrSiteFix(diagnostic.Code, diagnostic.Message)
	if diagnostic.Host != nil {
		diagnostic.Fix = "implement it in Go:\n    " + diagnostic.Host.File + "\n      " + diagnostic.Host.Directive + "\n      " + diagnostic.Host.Signature + "\n    or run: vela --ssr --ssr-scaffold-hosts"
	}
	if ssrStructuralKind(kind) && diagnostic.Code != SSRImpureDiagnosticCode {
		// A value that controls structure moves into {#client} with the
		// structure it controls (T-9.3).
		structural := "this value controls the structure of the page: move the block, with the structure it controls, into {#client}…{:else}fallback{/client} (docs/SSR_TRANSLATION_CONTRACT.md T-9.3)"
		diagnostic.Fix = structural + "; or " + diagnostic.Fix
	}
	return diagnostic
}

// ssrSiteFix gives a concrete fix (§10.1). Host implementations (S3) and
// {#client} boundaries (S6) add their own fixes when they exist.
func ssrSiteFix(code, message string) string {
	switch {
	case code == SSRHostSignatureCode:
		return "change the host function or the call so that the arguments fit the Go parameters (docs/SSR_TRANSLATION_CONTRACT.md §8.3, §8.4)"
	case code == SSRImpureDiagnosticCode:
		return "move the write into an event handler, $effect, or onMount; server rendering must not change state"
	case strings.Contains(message, "has no server value"):
		return "give the binding a server-evaluable initializer, or pass the value as an exported prop"
	case strings.Contains(message, "is not linked to a local .vela import"):
		return "import the component from its .vela file in this component's <script>"
	case strings.Contains(message, "only named local formatter calls") || strings.Contains(message, "is not available during server rendering"):
		return "this built-in is not translated (docs/SSR_TRANSLATION_MATRIX.md §2); move the expression into a top-level function of the component script and implement that function in Go with //vela:host (contract H-8.2e), wrap the markup in {#client}, or pass the value as a prop"
	case strings.Contains(message, "not a supported local formatter"):
		return "a global function has no host binding; wrap the call in a top-level function of the component script and implement that function in Go with //vela:host, or pass the value as a prop"
	}
	return "rewrite the expression within the admitted subset (docs/SSR_TRANSLATION_MATRIX.md), or compute it in Go and pass it as a prop"
}

// Failures returns the required sites that failed.
func (r *SSRRegion) Failures() []SSRSiteRecord {
	var failures []SSRSiteRecord
	for _, record := range r.Records {
		if record.Required && record.Outcome == SSROutcomeUnsupported {
			failures = append(failures, record)
		}
	}
	return failures
}

// SSRRegionError reports every failed required site of a component.
type SSRRegionError struct {
	Region   *SSRRegion
	Failures []SSRSiteRecord
	// Then is a structural error that stopped the renderer after the
	// failures, if any.
	Then error
}

// Error lists the one-line message of every failure, grouped so that one
// cause inside a function or binding shared by several sites appears once
// (D-10.3b).
func (e *SSRRegionError) Error() string {
	var lines []string
	for _, group := range ssrGroupFailures(e.Failures) {
		lines = append(lines, group[0].Diagnostic.Error)
	}
	if e.Then != nil {
		lines = append(lines, e.Then.Error())
	}
	return strings.Join(lines, "\n")
}

func ssrGroupFailures(failures []SSRSiteRecord) [][]SSRSiteRecord {
	var groups [][]SSRSiteRecord
	index := map[string]int{}
	for _, failure := range failures {
		d := failure.Diagnostic
		key := d.Code + "\x00" + d.Message
		if len(d.Reasons) > 1 {
			key += "\x00" + strings.Join(d.Reasons[1:], "\x00")
		} else {
			key += "\x00" + failure.ID
		}
		if i, ok := index[key]; ok {
			groups[i] = append(groups[i], failure)
			continue
		}
		index[key] = len(groups)
		groups = append(groups, []SSRSiteRecord{failure})
	}
	return groups
}

// locate fills Line and Column from the component source.
func (r *SSRRegion) locate(source string) {
	r.source = source
	for i := range r.Records {
		if source == "" || r.Records[i].Pos < 0 || r.Records[i].Pos > len(source) {
			continue
		}
		line, column := sourceLineColumn(source, r.Records[i].Pos)
		r.Records[i].Line, r.Records[i].Column = line+1, column+1
		if d := r.Records[i].Diagnostic; d != nil && d.Pos >= 0 && d.Pos <= len(source) {
			line, column := sourceLineColumn(source, d.Pos)
			d.Line, d.Column = line+1, column+1
		}
	}
}

// FormatSSRRegionError writes the §10.1 diagnostics of a region error:
// position, code, message, reason chain, and fix, once per cause with its
// further sites.
func FormatSSRRegionError(filename string, err *SSRRegionError) string {
	var out strings.Builder
	for i, group := range ssrGroupFailures(err.Failures) {
		if i > 0 {
			out.WriteString("\n")
		}
		first := group[0]
		d := first.Diagnostic
		line, column := err.Region.position(first)
		fmt.Fprintf(&out, "%s:%d:%d: [error] %s\n  %s\n  reason: %s\n  fix: %s", filename, line, column, d.Code, d.Message, strings.Join(d.Reasons, " → "), d.Fix)
		for _, other := range group[1:] {
			line, column := err.Region.sitePosition(other)
			fmt.Fprintf(&out, "\n  also at %s:%d:%d (%s)", filename, line, column, other.Diagnostic.Reasons[0])
		}
	}
	return out.String()
}

// SSRReport is the --ssr-report document (§10.4).
type SSRReport struct {
	Version    int                  `json:"version"`
	Components []SSRComponentReport `json:"components"`
	Summary    SSRReportSummary     `json:"summary"`
}

type SSRComponentReport struct {
	Component string          `json:"component"`
	Source    string          `json:"source"`
	Status    string          `json:"status"`
	Error     string          `json:"error,omitempty"`
	Sites     []SSRSiteRecord `json:"sites"`
}

type SSRReportSummary struct {
	Components   int            `json:"components"`
	ComponentsOK int            `json:"componentsOK"`
	Sites        int            `json:"sites"`
	Outcomes     map[string]int `json:"outcomes"`
	Tiers        map[string]int `json:"tiers"`
}

// ssrReportCollector gathers component reports during one project build.
type ssrReportCollector struct {
	report SSRReport
}

func newSSRReportCollector() *ssrReportCollector {
	return &ssrReportCollector{report: SSRReport{Version: 1}}
}

func (c *ssrReportCollector) add(src, srcRoot string, region *SSRRegion, err error) {
	if c == nil {
		return
	}
	rel, relErr := filepath.Rel(srcRoot, src)
	if relErr != nil {
		rel = src
	}
	component := SSRComponentReport{Component: componentName(src), Source: filepath.ToSlash(rel), Status: "ok", Sites: []SSRSiteRecord{}}
	if region != nil {
		component.Sites = region.Records
	}
	if err != nil {
		component.Status = "error"
		component.Error = err.Error()
	}
	c.report.Components = append(c.report.Components, component)
}

func (c *ssrReportCollector) write(path string) error {
	sort.Slice(c.report.Components, func(i, j int) bool { return c.report.Components[i].Source < c.report.Components[j].Source })
	summary := SSRReportSummary{Outcomes: map[string]int{}, Tiers: map[string]int{}}
	for _, component := range c.report.Components {
		summary.Components++
		if component.Status == "ok" {
			summary.ComponentsOK++
		}
		for _, site := range component.Sites {
			summary.Sites++
			summary.Outcomes[site.Outcome]++
			if site.Tier != "" {
				summary.Tiers[site.Tier]++
			}
		}
	}
	c.report.Summary = summary
	encoded, err := json.MarshalIndent(c.report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(encoded, '\n'), 0o644)
}

// translate lowers one region root of the template through the Server IR
// and records it. Without a region (direct renderer API users) or inside a
// speculative branch, a failure is returned as before. With a region it is
// recorded and an undefined placeholder is lowered, so that every failure
// of the component is found in one pass; the renderer fails at the end.
func (e *ssrGoEmitter) translate(kind, name string, expr *TemplateExpr, ctx SSRExprContext) (goast.Expr, error) {
	if expr == nil || expr.Root() == nil {
		return nil, &SSRExpressionError{Message: "template expression has no cached AST"}
	}
	source := -1
	if e.region != nil {
		source = e.region.TemplateOffset + expr.Pos
	}
	return e.translateJS(kind, name, expr.Root(), expr.Pos, source, ctx)
}

func (e *ssrGoEmitter) translateJS(kind, name string, expr JsExpr, pos, source int, ctx SSRExprContext) (goast.Expr, error) {
	builder := newSSRIRBuilder(ctx)
	if e.region != nil {
		builder.js = true
		// Prop defaults are evaluated before the local bindings exist.
		builder.locals = kind != "prop-default"
		builder.site = e.region.siteLabel(source)
	}
	ir, err := builder.build(expr, pos)
	if e.region != nil && (err == nil || e.speculative == 0) {
		e.region.absorb(builder)
	}
	if err == nil {
		if builder.usedJS {
			e.region.usesJS = true
		}
		if builder.throws {
			e.region.throwing++
		}
		if e.facts == nil {
			e.facts = map[JsExpr]ssrIRFacts{}
		}
		e.facts[expr] = ir.irBase().Facts
		if ssrIRPrimitive(ir) {
			if e.primitive == nil {
				e.primitive = map[JsExpr]bool{}
			}
			e.primitive[expr] = true
		}
	}
	if e.region == nil || (err != nil && e.speculative > 0) {
		if err != nil {
			return nil, err
		}
		return lowerSSRIR(ir), nil
	}
	e.region.add(kind, name, source, true, ir, builder.inlined, err)
	if err != nil {
		return lowerSSRIR(&ssrIRUndefined{}), nil
	}
	return lowerSSRIR(ir), nil
}

// convertsOutput records that the value of expr is converted to text. A
// value not proven primitive can call a function in an own toString or
// throw TypeError, so the site can throw (OrdinaryToPrimitive).
func (e *ssrGoEmitter) convertsOutput(expr *TemplateExpr) {
	if e.region == nil || expr == nil || e.primitive[expr.Root()] {
		return
	}
	e.region.usesJS = true
	e.region.throwing++
}

// speculate runs emit and drops the sites it recorded when it fails: a
// speculative branch has its own fallback and its failure is not a region
// failure.
func (e *ssrGoEmitter) speculate(emit func() error) error {
	mark := 0
	if e.region != nil {
		mark = len(e.region.Records)
	}
	e.speculative++
	err := emit()
	e.speculative--
	if err != nil && e.region != nil {
		e.region.Records = e.region.Records[:mark]
	}
	return err
}

// finish records the declaration sites ($state and $derived initializers)
// and returns the region error when a required site failed.
func (r *SSRRegion) finish(plan *SSRRenderPlan) error {
	reads := map[string]bool{}
	for _, record := range r.Records {
		for _, name := range record.Reads {
			reads[name] = true
		}
	}
	ctx := ssrComponentContext(NewSSRExprContext(plan))
	ctx.funcs = r.funcs
	for _, declarations := range []struct {
		kind         string
		initializers map[string]JsExpr
	}{{"state", plan.StateInitializers}, {"state", plan.DeepInitializers}, {"derived", plan.DerivedInitializers}, {"binding", plan.PlainInitializers}} {
		names := make([]string, 0, len(declarations.initializers))
		for name := range declarations.initializers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			initializer := declarations.initializers[name]
			// As the prologue evaluates it (ssr_init.go).
			builder := newSSRIRBuilder(ctx)
			builder.locals, builder.current, builder.js = true, name, true
			builder.site = r.siteLabel(r.ScriptOffset + initializer.NodePos())
			ir, err := builder.build(initializer, 0)
			r.add(declarations.kind, name, r.ScriptOffset+initializer.NodePos(), false, ir, builder.inlined, err)
			if reads[name] {
				r.Records[len(r.Records)-1].Reads = append(r.Records[len(r.Records)-1].Reads, "(read by the render)")
			}
		}
	}
	// One record per called function, at its definition (T-2.1 root 6).
	names := make([]string, 0, len(r.calls))
	for name := range r.calls {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		call := r.calls[name]
		r.counters["function"]++
		pos := -1
		if function, ok := plan.Functions[name]; ok && function != nil {
			pos = r.ScriptOffset + function.NodePos()
		}
		r.Records = append(r.Records, SSRSiteRecord{ID: fmt.Sprintf("function#%d", r.counters["function"]), Kind: "function", Name: name, Pos: pos, Outcome: call.Outcome, Host: call.Host})
	}
	if failures := r.Failures(); len(failures) > 0 {
		return &SSRRegionError{Region: r, Failures: failures}
	}
	return nil
}

// abort combines the site failures recorded before a structural error
// stopped the renderer with that error, in traversal order.
func (r *SSRRegion) abort(err error) error {
	failures := r.Failures()
	if len(failures) == 0 {
		return err
	}
	return &SSRRegionError{Region: r, Failures: failures, Then: err}
}

// ssrDiagnosticSummary is the first line of an SSR error, as the CLI has
// always printed it.
func ssrDiagnosticSummary(err error) string {
	first, _, _ := strings.Cut(err.Error(), "\n")
	return first
}

// ssrDiagnosticDetail is the positioned §10.1 diagnostic of a region error,
// indented under the summary line, or "" for other errors.
func ssrDiagnosticDetail(src string, err error) string {
	var region *SSRRegionError
	if !errors.As(err, &region) {
		return ""
	}
	lines := strings.Split(FormatSSRRegionError(src, region), "\n")
	if region.Then != nil {
		lines = append(lines, region.Then.Error())
	}
	for i, line := range lines {
		lines[i] = "       " + line
	}
	return strings.Join(lines, "\n")
}

// position is the 1-based line and column of a record in the located
// source, or 0, 0 before locate.
func (r *SSRRegion) position(record SSRSiteRecord) (int, int) {
	if d := record.Diagnostic; d != nil && d.Pos >= 0 && r != nil && d.Pos <= len(r.source) {
		line, column := sourceLineColumn(r.source, d.Pos)
		return line + 1, column + 1
	}
	if r == nil || r.source == "" || record.Pos < 0 || record.Pos > len(r.source) {
		return 0, 0
	}
	line, column := sourceLineColumn(r.source, record.Pos)
	return line + 1, column + 1
}

// sitePosition is the 1-based position of the site itself.
func (r *SSRRegion) sitePosition(record SSRSiteRecord) (int, int) {
	record.Diagnostic = nil
	return r.position(record)
}

// siteLabel is the source position written into a RenderError: file, line,
// and column when the source is known, else the component name.
func (r *SSRRegion) siteLabel(pos int) string {
	file := r.File
	if file == "" {
		file = r.Component + ".vela"
	}
	if r.source == "" || pos < 0 || pos > len(r.source) {
		return file
	}
	line, column := sourceLineColumn(r.source, pos)
	return fmt.Sprintf("%s:%d:%d", file, line+1, column+1)
}

// throwing counts the lowered sites that can throw.
func (e *ssrGoEmitter) throwing() int {
	if e.region == nil {
		return 0
	}
	return e.region.throwing
}

// ssrJSCheck returns the first JavaScript exception of the render:
//
//	if err := ssrJS.Err(); err != nil { return err }
func ssrJSCheck() goast.Stmt {
	return goWriteSSRErr(&goast.CallExpr{Fun: goSelector("ssrJS", "Err")})
}

// ssrJSPrologue declares the evaluation state of a render function when a
// lowered site uses it.
func ssrJSPrologue(region *SSRRegion) []goast.Stmt {
	if region == nil || !region.usesJS {
		return nil
	}
	out := []goast.Stmt{
		&goast.AssignStmt{Lhs: []goast.Expr{goast.NewIdent("ssrJS")}, Tok: token.DEFINE, Rhs: []goast.Expr{&goast.CallExpr{Fun: goSelector("vela", "NewJS")}}},
		&goast.AssignStmt{Lhs: []goast.Expr{goast.NewIdent("_")}, Tok: token.ASSIGN, Rhs: []goast.Expr{goast.NewIdent("ssrJS")}},
	}
	return out
}

// SSRHostFix is where and how to implement a missing host (§10.1).
type SSRHostFix struct {
	File      string `json:"file"`
	Directive string `json:"directive"`
	Signature string `json:"signature"`
}

// absorb collects what a builder used: host imports and call outcomes.
func (r *SSRRegion) absorb(builder *ssrIRBuilder) {
	if r == nil {
		return
	}
	for path, alias := range builder.hostImports {
		r.hostImports[path] = alias
	}
	for name, alias := range builder.modules {
		if r.modules == nil {
			r.modules = map[string]string{}
		}
		r.modules[name] = alias
	}
	for name, outcome := range builder.calls {
		if previous, seen := r.calls[name]; seen && previous.Outcome == SSROutcomeUnsupported {
			continue
		}
		r.calls[name] = outcome
	}
}

// errSSRClientOnly marks a component declared <script context="client">:
// it has no Go renderer (contract §9.2).
var errSSRClientOnly = errors.New("client-only component")

// addClientOnly records a client-only component in the report.
func (c *ssrReportCollector) addClientOnly(src, srcRoot string) {
	if c == nil {
		return
	}
	rel, err := filepath.Rel(srcRoot, src)
	if err != nil {
		rel = src
	}
	c.report.Components = append(c.report.Components, SSRComponentReport{Component: componentName(src), Source: filepath.ToSlash(rel), Status: "client-only", Sites: []SSRSiteRecord{}})
}

func isSSRClientOnly(err error) bool { return errors.Is(err, errSSRClientOnly) }

// ssrStructuralKind reports the site kinds whose value controls structure
// (T-4.3): branch conditions, keys, each collections, await operands.
func ssrStructuralKind(kind string) bool {
	switch kind {
	case "if", "key", "each", "await":
		return true
	}
	return false
}
