package compiler

import (
	"github.com/elauven/vela/internal/codegen"
	"github.com/elauven/vela/internal/diag"
	"github.com/elauven/vela/internal/jsast"
	"github.com/elauven/vela/internal/ssr"

	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ServiceFlagDefaults: clients of the service bundle the output, so CSS is a
// separate result and the runtime is the npm package.
var ServiceFlagDefaults = fileFlagDefaults{cssMode: "external", runtimeImport: "bare"}

// ServiceRequest compiles one component. Source is the text to compile;
// without it the service reads Path.
type ServiceRequest struct {
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Path    string          `json:"path"`
	Source  *string         `json:"source,omitempty"`
	Options FileOptions     `json:"options"`
}

// CompileResult answers a ServiceRequest; `vela compile` prints one without
// an id.
type CompileResult struct {
	ID          json.RawMessage          `json:"id,omitempty"`
	OK          bool                     `json:"ok"`
	JS          string                   `json:"js,omitempty"`
	CSS         string                   `json:"css,omitempty"`
	Map         json.RawMessage          `json:"map,omitempty"`
	Diagnostics []diag.MachineDiagnostic `json:"diagnostics"`
	Imports     []ModuleImport           `json:"imports"`
	Error       string                   `json:"error,omitempty"`
}

// ModuleImport is an import of a component's <script> as written, for the
// watchers of a client. Resolved is the file of a relative specifier.
type ModuleImport struct {
	Specifier string `json:"specifier"`
	Kind      string `json:"kind"` // component, module, runtime, package or url
	Resolved  string `json:"resolved,omitempty"`
}

// sourceModuleImports lists the static imports, re-exports and literal
// import() calls of a component script.
func sourceModuleImports(src string, prog *jsast.JsProgram) []ModuleImport {
	out := []ModuleImport{}
	seen := map[string]bool{}
	add := func(spec string) {
		if seen[spec] {
			return
		}
		seen[spec] = true
		imp := ModuleImport{Specifier: spec}
		switch {
		case spec == "vela" || spec == "vela/store":
			imp.Kind = "runtime"
		case codegen.IsPackageSpecifier(spec):
			imp.Kind = "package"
		case strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") || strings.HasPrefix(spec, "/"):
			imp.Kind = "module"
			if strings.HasSuffix(spec, ".vela") {
				imp.Kind = "component"
			}
			if !strings.HasPrefix(spec, "/") {
				imp.Resolved = filepath.Join(filepath.Dir(src), filepath.FromSlash(spec))
			}
		default:
			imp.Kind = "url"
		}
		out = append(out, imp)
	}
	if prog == nil {
		return out
	}
	jsast.WalkNode(prog, func(n jsast.JsNode) bool {
		switch x := n.(type) {
		case *jsast.JsImportDecl:
			add(x.Source)
		case *jsast.JsExportDecl:
			if x.HasSource {
				add(x.Source)
			}
		case *jsast.JsCallExpr:
			if callee, ok := x.Callee.(*jsast.JsIdent); ok && callee.Name == "import" && len(x.Args) > 0 {
				if lit, ok := x.Args[0].(*jsast.JsLiteral); ok && lit.Kind == "string" && len(lit.Raw) >= 2 {
					add(lit.Raw[1 : len(lit.Raw)-1])
				}
			}
		}
		return true
	})
	return out
}

// CompilerService compiles requests against one working directory and
// configuration. It is safe for concurrent use.
type CompilerService struct {
	Cwd    string
	config *velaConfig
	// flags are the file flags given to `vela serve` or `vela compile`; they
	// override the configuration, and a request overrides them.
	flags []FlagValue
}

func NewCompilerService(configPath string, flags []FlagValue) (*CompilerService, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	config, err := LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	return &CompilerService{Cwd: cwd, config: config, flags: flags}, nil
}

// requestOptions resolves the options of a request: request, command line,
// configuration, service defaults.
func (s *CompilerService) requestOptions(req ServiceRequest) (CompileOptions, error) {
	fs := flag.NewFlagSet("request", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	file := AddFileFlags(fs, ServiceFlagDefaults)
	if err := ApplyFlagValues(fs, "request options", req.Options.flagValues()); err != nil {
		return CompileOptions{}, err
	}
	if err := ApplyFlagValues(fs, "command line", s.flags); err != nil {
		return CompileOptions{}, err
	}
	if s.config != nil {
		if err := ApplyFlagValues(fs, ConfigFileName, s.config.FileOptions.flagValues()); err != nil {
			return CompileOptions{}, err
		}
	}
	return file.Options()
}

// sourcePath is the path a build run in the working directory would use for
// the file: relative when the file is inside it. The path determines the
// scope class and names the file in diagnostics and source maps.
func (s *CompilerService) SourcePath(path string) string {
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(s.Cwd, abs)
	}
	rel, err := filepath.Rel(s.Cwd, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.Clean(abs)
	}
	return rel
}

func (s *CompilerService) Compile(req ServiceRequest) CompileResult {
	res := CompileResult{ID: req.ID, Diagnostics: []diag.MachineDiagnostic{}, Imports: []ModuleImport{}}
	if req.Method != "" && req.Method != "compile" {
		res.Error = fmt.Sprintf("unknown method %q", req.Method)
		return res
	}
	if !strings.EqualFold(filepath.Ext(req.Path), ".vela") {
		res.Error = fmt.Sprintf("path %q is not a .vela file", req.Path)
		return res
	}
	opts, err := s.requestOptions(req)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	src := s.SourcePath(req.Path)
	// The site path of a project build: relative to the parent of the
	// project directory (CompileProjectFile).
	opts.SitePath = filepath.ToSlash(src)
	if s.config != nil && s.config.Project != nil {
		if rel, err := filepath.Rel(filepath.Dir(filepath.Clean(*s.config.Project)), src); err == nil {
			opts.SitePath = filepath.ToSlash(rel)
		}
	}
	read := func() ([]byte, error) { return os.ReadFile(src) }
	if req.Source != nil {
		read = func() ([]byte, error) { return []byte(*req.Source), nil }
	}
	dest := strings.TrimSuffix(filepath.Base(src), filepath.Ext(src)) + ".js"
	var module *compiledModule
	diags, err := compileComponent(src, dest, opts, read, func(m *compiledModule) error {
		module = m
		return nil
	})
	for _, d := range diags {
		res.Diagnostics = append(res.Diagnostics, d.Machine())
	}
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.OK = true
	res.JS, res.CSS, res.Imports = module.JS, module.CSS, module.Imports
	if len(module.Map) > 0 {
		res.Map = json.RawMessage(module.Map)
	}
	return res
}

// Analyze analyzes the component at path with source under the options a
// compile request of it gets (the language server, task 065).
func (s *CompilerService) Analyze(path, source string) (*ComponentAnalysis, error) {
	opts, err := s.requestOptions(ServiceRequest{Path: path})
	if err != nil {
		return nil, err
	}
	return AnalyzeComponent(s.SourcePath(path), source, opts), nil
}

// ProjectCheck is the vela check of the configured project, with the SSR
// checks when the configuration enables SSR; ok is false without a
// configured project.
func (s *CompilerService) ProjectCheck() (target CheckTarget, ok bool, err error) {
	if s.config == nil || s.config.Project == nil {
		return CheckTarget{}, false, nil
	}
	opts, err := s.requestOptions(ServiceRequest{})
	if err != nil {
		return CheckTarget{}, false, err
	}
	opts.SSRModule = ssr.SSRDefaultModule
	if s.config.SSRModule != nil {
		opts.SSRModule = *s.config.SSRModule
	}
	return CheckTarget{SrcRoot: *s.config.Project, SSR: s.config.SSR != nil && *s.config.SSR, Opts: opts}, true, nil
}
