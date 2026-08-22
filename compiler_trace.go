package main

import (
	"path/filepath"
	"time"
)

// TraceSink receives one immutable, bounded trace record for each component
// compilation. It is optional: a nil sink has no timing, allocation, or output
// side effects beyond the normal compiler path.
type TraceSink interface {
	Record(CompilationTraceV1) error
}

// CompilationTraceV1 is the stable, source-free observability record for one
// compileFile invocation. It is intentionally an aggregate: raw source, AST
// payloads, generated output, diagnostic messages, and absolute paths are not
// retained or serialized.
type CompilationTraceV1 struct {
	SchemaVersion int                   `json:"schemaVersion"`
	Component     TraceComponent        `json:"component"`
	Outcome       string                `json:"outcome"`
	Options       TraceOptions          `json:"options"`
	Stages        []StageTrace          `json:"stages"`
	Diagnostics   TraceDiagnosticCounts `json:"diagnostics"`
	Metrics       TraceMetrics          `json:"metrics"`
}

type TraceComponent struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

type TraceOptions struct {
	Minify    bool   `json:"minify"`
	SourceMap bool   `json:"sourceMap"`
	CSSMode   string `json:"cssMode"`
	DebugAST  bool   `json:"debugAst"`
}

type StageTrace struct {
	Name       string             `json:"name"`
	Status     string             `json:"status"`
	DurationNS int64              `json:"durationNs"`
	Cache      TraceCacheDecision `json:"cache"`
	Reason     string             `json:"reason,omitempty"`
}

type TraceCacheDecision struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
}

type TraceDiagnosticCounts struct {
	Errors   int            `json:"errors"`
	Warnings int            `json:"warnings"`
	Hints    int            `json:"hints"`
	ByCode   map[string]int `json:"byCode,omitempty"`
}

type TraceMetrics struct {
	SourceBytes      int `json:"sourceBytes"`
	TemplateNodes    int `json:"templateNodes"`
	ScriptNodes      int `json:"scriptNodes"`
	CSSNodes         int `json:"cssNodes"`
	TargetNodes      int `json:"targetNodes"`
	OutputBytes      int `json:"outputBytes"`
	CSSBytes         int `json:"cssBytes"`
	SourceMapBytes   int `json:"sourceMapBytes"`
	ScriptParseCount int `json:"scriptParseCount"`
}

const (
	traceComplete = "complete"
	traceSkipped  = "skipped"
	traceFailed   = "failed"
)

type traceRecorder struct {
	sink  TraceSink
	trace CompilationTraceV1
}

func newTraceRecorder(src, componentName string, opts CompileOptions) *traceRecorder {
	if opts.TraceSink == nil {
		return nil
	}
	cssMode := "inject"
	if opts.CSSExternal {
		cssMode = "external"
	}
	return &traceRecorder{
		sink: opts.TraceSink,
		trace: CompilationTraceV1{
			SchemaVersion: 1,
			Component: TraceComponent{
				ID:          traceComponentID(src),
				DisplayName: componentName,
			},
			Options: TraceOptions{
				Minify:    opts.Minify,
				SourceMap: opts.SourceMap,
				CSSMode:   cssMode,
				DebugAST:  opts.EmitAST || opts.GenAST,
			},
		},
	}
}

// traceComponentID deliberately avoids absolute paths in file mode. Project
// mode can replace this with a project-relative ID when project ownership is
// introduced into CompileOptions.
func traceComponentID(src string) string {
	clean := filepath.ToSlash(filepath.Clean(src))
	if filepath.IsAbs(src) {
		return filepath.Base(clean)
	}
	return clean
}

func (r *traceRecorder) stage(name string, run func() error) (err error) {
	if r == nil {
		return run()
	}
	stage := StageTrace{
		Name:   name,
		Status: traceComplete,
		Cache:  TraceCacheDecision{Decision: "not-applicable"},
	}
	r.trace.Stages = append(r.trace.Stages, stage)
	index := len(r.trace.Stages) - 1
	started := time.Now()
	defer func() {
		r.trace.Stages[index].DurationNS = time.Since(started).Nanoseconds()
		if panicValue := recover(); panicValue != nil {
			r.trace.Stages[index].Status = traceFailed
			panic(panicValue)
		}
		if err != nil {
			r.trace.Stages[index].Status = traceFailed
		}
	}()
	return run()
}

func (r *traceRecorder) skipped(name, reason string) {
	if r == nil {
		return
	}
	r.trace.Stages = append(r.trace.Stages, StageTrace{
		Name:   name,
		Status: traceSkipped,
		Cache:  TraceCacheDecision{Decision: "not-applicable"},
		Reason: reason,
	})
}

func (r *traceRecorder) finish(diags []*Diagnostic, outcome string) error {
	if r == nil {
		return nil
	}
	r.trace.Outcome = outcome
	r.trace.Diagnostics = traceDiagnostics(diags)
	return r.sink.Record(r.snapshot())
}

func (r *traceRecorder) snapshot() CompilationTraceV1 {
	out := r.trace
	out.Stages = append([]StageTrace(nil), r.trace.Stages...)
	if len(r.trace.Diagnostics.ByCode) > 0 {
		out.Diagnostics.ByCode = make(map[string]int, len(r.trace.Diagnostics.ByCode))
		for code, count := range r.trace.Diagnostics.ByCode {
			out.Diagnostics.ByCode[code] = count
		}
	}
	return out
}

func traceDiagnostics(diags []*Diagnostic) TraceDiagnosticCounts {
	out := TraceDiagnosticCounts{}
	for _, diag := range diags {
		if diag == nil {
			continue
		}
		switch diag.Sev {
		case SevError:
			out.Errors++
		case SevWarning:
			out.Warnings++
		case SevHint:
			out.Hints++
		}
		if diag.Code != "" {
			if out.ByCode == nil {
				out.ByCode = map[string]int{}
			}
			out.ByCode[diag.Code]++
		}
	}
	return out
}

func countJSNodes(prog *JsProgram) int {
	if prog == nil {
		return 0
	}
	count := 1
	for _, stmt := range prog.Body {
		WalkNode(stmt, func(JsNode) bool {
			count++
			return true
		})
	}
	return count
}

func countTemplateNodes(nodes []Node) int {
	count := 0
	var visit func([]Node)
	visit = func(items []Node) {
		for _, node := range items {
			if node == nil {
				continue
			}
			count++
			switch n := node.(type) {
			case *ElementNode:
				visit(n.Children)
			case *ComponentNode:
				for _, slot := range n.Slots {
					visit(slot)
				}
			case *SlotNode:
				visit(n.Fallback)
			case *IfChainNode:
				for _, branch := range n.Branches {
					if branch != nil {
						visit(branch.Children)
					}
				}
			case *EachBlockNode:
				visit(n.Children)
				visit(n.ElseChildren)
			case *AwaitBlockNode:
				visit(n.PendingChildren)
				visit(n.ThenChildren)
				visit(n.CatchChildren)
			}
		}
	}
	visit(nodes)
	return count
}
