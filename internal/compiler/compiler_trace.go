package compiler

import (
	"github.com/elauven/vela/internal/diag"
	"github.com/elauven/vela/internal/jsast"
	"github.com/elauven/vela/internal/sfcast"

	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TraceSink receives one immutable, bounded trace record for each component
// compilation. It is optional: a nil sink has no timing, allocation, or output
// side effects beyond the normal compiler path.
type TraceSink interface {
	Record(CompilationTraceV1) error
}

// JSONLTraceSink appends one complete JSON record per line. Record is safe for
// concurrent project builds: serialization and the single underlying Write are
// both protected by a mutex, so separate records cannot interleave.
type JSONLTraceSink struct {
	mu     sync.Mutex
	writer io.Writer
	closer io.Closer
}

// OpenJSONLTraceSink opens path for append-only trace output. A path of "-"
// writes to stdout and is never closed by the sink.
func OpenJSONLTraceSink(path string) (*JSONLTraceSink, error) {
	if path == "" {
		return nil, fmt.Errorf("trace path must not be empty")
	}
	if path == "-" {
		return NewJSONLTraceSink(os.Stdout), nil
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open trace file %q: %w", path, err)
	}
	sink := NewJSONLTraceSink(file)
	sink.closer = file
	return sink, nil
}

// NewJSONLTraceSink constructs a JSONL sink around writer. It is primarily
// useful for embedding and tests; callers that pass an *os.File retain its
// lifetime unless they call Close after assigning their own closer.
func NewJSONLTraceSink(writer io.Writer) *JSONLTraceSink {
	return &JSONLTraceSink{writer: writer}
}

func (s *JSONLTraceSink) Record(trace CompilationTraceV1) error {
	if s == nil || s.writer == nil {
		return fmt.Errorf("write trace JSONL record: sink is not initialized")
	}
	data, err := json.Marshal(trace)
	if err != nil {
		return fmt.Errorf("marshal trace JSONL record: %w", err)
	}
	data = append(data, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()
	written, err := s.writer.Write(data)
	if err != nil {
		return fmt.Errorf("write trace JSONL record: %w", err)
	}
	if written != len(data) {
		return fmt.Errorf("write trace JSONL record: %w", io.ErrShortWrite)
	}
	return nil
}

// Close releases a file opened by OpenJSONLTraceSink. It intentionally leaves
// stdout and externally supplied writers alone.
func (s *JSONLTraceSink) Close() error {
	if s == nil || s.closer == nil {
		return nil
	}
	return s.closer.Close()
}

// CompilationTraceV1 is the stable, source-free observability record for one
// CompileFile invocation. It is intentionally an aggregate: raw source, AST
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
	TraceComplete = "complete"
	TraceSkipped  = "skipped"
	TraceFailed   = "failed"
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
				ID:          traceComponentIDForCompile(src, opts),
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

func traceComponentIDForCompile(src string, opts CompileOptions) string {
	if opts.TraceComponentID != "" {
		return opts.TraceComponentID
	}
	return traceComponentID(src)
}

// traceComponentID deliberately avoids absolute paths in file mode. Project
// mode supplies a project-relative ID through CompileOptions.
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
		Status: TraceComplete,
		Cache:  TraceCacheDecision{Decision: "not-applicable"},
	}
	r.trace.Stages = append(r.trace.Stages, stage)
	index := len(r.trace.Stages) - 1
	started := time.Now()
	defer func() {
		r.trace.Stages[index].DurationNS = time.Since(started).Nanoseconds()
		if panicValue := recover(); panicValue != nil {
			r.trace.Stages[index].Status = TraceFailed
			panic(panicValue)
		}
		if err != nil {
			r.trace.Stages[index].Status = TraceFailed
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
		Status: TraceSkipped,
		Cache:  TraceCacheDecision{Decision: "not-applicable"},
		Reason: reason,
	})
}

func (r *traceRecorder) finish(diags []*diag.Diagnostic, outcome string) error {
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

func traceDiagnostics(diags []*diag.Diagnostic) TraceDiagnosticCounts {
	out := TraceDiagnosticCounts{}
	for _, d := range diags {
		if d == nil {
			continue
		}
		switch d.Sev {
		case diag.SevError:
			out.Errors++
		case diag.SevWarning:
			out.Warnings++
		case diag.SevHint:
			out.Hints++
		}
		if d.Code != "" {
			if out.ByCode == nil {
				out.ByCode = map[string]int{}
			}
			out.ByCode[d.Code]++
		}
	}
	return out
}

func countJSNodes(prog *jsast.JsProgram) int {
	if prog == nil {
		return 0
	}
	count := 1
	for _, stmt := range prog.Body {
		jsast.WalkNode(stmt, func(jsast.JsNode) bool {
			count++
			return true
		})
	}
	return count
}

func countTemplateNodes(nodes []sfcast.Node) int {
	count := 0
	var visit func([]sfcast.Node)
	visit = func(items []sfcast.Node) {
		for _, node := range items {
			if node == nil {
				continue
			}
			count++
			switch n := node.(type) {
			case *sfcast.ElementNode:
				visit(n.Children)
			case *sfcast.ComponentNode:
				for _, slot := range n.Slots {
					visit(slot)
				}
			case *sfcast.SlotNode:
				visit(n.Fallback)
			case *sfcast.IfChainNode:
				for _, branch := range n.Branches {
					if branch != nil {
						visit(branch.Children)
					}
				}
			case *sfcast.EachBlockNode:
				visit(n.Children)
				visit(n.ElseChildren)
			case *sfcast.AwaitBlockNode:
				visit(n.PendingChildren)
				visit(n.ThenChildren)
				visit(n.CatchChildren)
			}
		}
	}
	visit(nodes)
	return count
}
