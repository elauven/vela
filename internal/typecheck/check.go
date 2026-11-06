package typecheck

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/elauven/vela/internal/diag"
	"github.com/elauven/vela/internal/project"
)

// The type check of a project (`vela check --types`, TS-1.2, TS-1.5): one
// TypeScript program over the project's tsconfig.json, the virtual
// documents of its components and its .ts modules, with the diagnostics
// mapped to the .vela sources.

// ConfigDiagnosticCode reports a missing tsconfig.json or a setting that
// contradicts TS-10.2.
const ConfigDiagnosticCode = "VELA-TS-CONFIG"

// Checker checks a project; it keeps the helper and its program between
// runs (`vela check --types --watch`).
type Checker struct {
	Root    string // where tsconfig.json is, the working directory
	SrcRoot string // the project's components
	Options Options
	client  *Client
	docs    map[string]*Document // by virtual path
}

// NewChecker starts the helper: an error says why a check cannot run
// (no node, no usable TypeScript, TS-10.1).
func NewChecker(root, srcRoot string, opts Options) (*Checker, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	opts.Project = true
	c, err := Start(abs, opts)
	if err != nil {
		return nil, fmt.Errorf("type checking needs node and TypeScript 5.8 or 6.x (in node_modules, or --tsdk): %v", err)
	}
	return &Checker{Root: abs, SrcRoot: srcRoot, Options: opts, client: c, docs: map[string]*Document{}}, nil
}

// Close stops the helper.
func (c *Checker) Close() { c.client.Close() }

// Version is the TypeScript the check runs.
func (c *Checker) Version() string { return c.client.Version }

// Check checks the project again and returns the diagnostics of every
// component and module, with the configuration's.
func (c *Checker) Check() ([]*diag.Diagnostic, error) {
	files := project.CollectVela(c.SrcRoot)
	var docs []*Document
	seen := map[string]bool{}
	for _, file := range files {
		abs, err := filepath.Abs(file)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return nil, err
		}
		d := Build(abs, string(data))
		docs = append(docs, d)
		seen[d.VirtualPath()] = true
	}
	var removed []string
	for p := range c.docs {
		if !seen[p] {
			removed = append(removed, p)
		}
	}
	if len(removed) > 0 {
		if err := c.client.Call("update", map[string]any{"remove": removed}, nil); err != nil {
			return nil, err
		}
		for _, p := range removed {
			delete(c.client.sent, p)
		}
	}
	c.docs = map[string]*Document{}
	for _, d := range docs {
		c.docs[d.VirtualPath()] = d
	}
	if err := c.client.Update(docs); err != nil {
		return nil, err
	}
	var res struct {
		Diagnostics []helperDiagnostic `json:"diagnostics"`
	}
	if err := c.client.CallTimeout("check", nil, &res, 10*time.Minute); err != nil {
		return nil, err
	}
	out := c.configDiagnostics()
	sources := map[string]string{}
	for _, hd := range res.Diagnostics {
		if d := c.mapDiagnostic(hd, sources); d != nil {
			out = append(out, d)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Col < out[j].Col
	})
	return out, nil
}

type helperDiagnostic struct {
	File     string `json:"file"`
	Start    int    `json:"start"`
	Length   int    `json:"length"`
	Message  string `json:"message"`
	Category string `json:"category"`
	Code     int    `json:"code"`
}

func (c *Checker) configDiagnostics() []*diag.Diagnostic {
	if c.client.Config == "" {
		return []*diag.Diagnostic{{Sev: diag.SevWarning, File: "tsconfig.json", Code: ConfigDiagnosticCode,
			Message:    "no tsconfig.json in " + c.Root + ": checked with the configuration of docs/TYPESCRIPT.md TS-10.2",
			Suggestion: "write tsconfig.json as TS-10.2 shows, with \"types\": [\"@govela/runtime/ambient\"]"}}
	}
	var out []*diag.Diagnostic
	file := relative(c.client.Config)
	for _, w := range c.client.Warnings {
		out = append(out, &diag.Diagnostic{Sev: diag.SevWarning, File: file, Code: ConfigDiagnosticCode,
			Message:    fmt.Sprintf("compilerOptions.%s contradicts the configuration of a Vela TypeScript project (TS-10.2)", w.Option),
			Suggestion: fmt.Sprintf("set \"%s\": %s", w.Option, w.Want)})
	}
	return out
}

// mapDiagnostic turns a TypeScript diagnostic into a Vela one: in a
// component through its virtual document, in a module by its text.
func (c *Checker) mapDiagnostic(hd helperDiagnostic, sources map[string]string) *diag.Diagnostic {
	sev := diag.SevError
	switch hd.Category {
	case "warning":
		sev = diag.SevWarning
	case "hint":
		sev = diag.SevHint
	}
	code := "TS" + strconv.Itoa(hd.Code)
	if hd.File == "" {
		return &diag.Diagnostic{Sev: sev, Code: code, Message: hd.Message}
	}
	file := filepath.FromSlash(hd.File)
	if d := c.docs[file]; d != nil {
		vs, ve := ByteOffset(d.Text, hd.Start), ByteOffset(d.Text, hd.Start+hd.Length)
		start, end, ok := d.Locate(vs, ve)
		if !ok {
			// Generated text no anchor covers: a defect of the virtual
			// document, reported at the start of the component.
			start, end = 0, 0
			hd.Message += " (in code Vela generates for the type check)"
		}
		ds := diag.NewDiagSet(relative(d.Path), d.Source)
		ds.Report(sev, start, end, code, "", "%s", hd.Message)
		return ds.All()[0]
	}
	src, ok := sources[file]
	if !ok {
		data, err := os.ReadFile(file)
		if err != nil {
			return &diag.Diagnostic{Sev: sev, File: relative(file), Code: code, Message: hd.Message}
		}
		src = string(data)
		sources[file] = src
	}
	start, end := ByteOffset(src, hd.Start), ByteOffset(src, hd.Start+hd.Length)
	ds := diag.NewDiagSet(relative(file), src)
	ds.Report(sev, start, end, code, "", "%s", hd.Message)
	return ds.All()[0]
}

// relative is a path relative to the working directory when it is inside.
func relative(file string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return file
	}
	if rel, err := filepath.Rel(cwd, file); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return file
}
