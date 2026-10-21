package main

import (
	"github.com/elauven/vela/runtime"

	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
)

// ServiceProtocolVersion is the version of the compiler service protocol
// (docs/TOOLING_PROTOCOL.md). It changes when a client written for the
// previous version would misread a message.
const ServiceProtocolVersion = 1

// serviceFlagDefaults: clients of the service bundle the output, so CSS is a
// separate result and the runtime is the npm package.
var serviceFlagDefaults = fileFlagDefaults{cssMode: "external", runtimeImport: "bare"}

// serviceHandshake is the first line `vela serve` writes.
type serviceHandshake struct {
	Protocol        int    `json:"protocol"`
	CompilerVersion string `json:"compilerVersion"`
	RuntimePackage  string `json:"runtimePackage"`
	RuntimeContract string `json:"runtimeContract"`
	Error           string `json:"error,omitempty"`
}

func newServiceHandshake() serviceHandshake {
	return serviceHandshake{
		Protocol:        ServiceProtocolVersion,
		CompilerVersion: runtime.Version,
		RuntimePackage:  runtime.PackageName,
		RuntimeContract: RuntimeContractExport,
	}
}

// serviceRequest compiles one component. Source is the text to compile;
// without it the service reads Path.
type serviceRequest struct {
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Path    string          `json:"path"`
	Source  *string         `json:"source,omitempty"`
	Options fileOptions     `json:"options"`
}

// compileResult answers a serviceRequest; `vela compile` prints one without
// an id.
type compileResult struct {
	ID          json.RawMessage     `json:"id,omitempty"`
	OK          bool                `json:"ok"`
	JS          string              `json:"js,omitempty"`
	CSS         string              `json:"css,omitempty"`
	Map         json.RawMessage     `json:"map,omitempty"`
	Diagnostics []MachineDiagnostic `json:"diagnostics"`
	Imports     []moduleImport      `json:"imports"`
	Error       string              `json:"error,omitempty"`
}

// moduleImport is an import of a component's <script> as written, for the
// watchers of a client. Resolved is the file of a relative specifier.
type moduleImport struct {
	Specifier string `json:"specifier"`
	Kind      string `json:"kind"` // component, module, runtime, package or url
	Resolved  string `json:"resolved,omitempty"`
}

// sourceModuleImports lists the static imports, re-exports and literal
// import() calls of a component script.
func sourceModuleImports(src string, prog *JsProgram) []moduleImport {
	out := []moduleImport{}
	seen := map[string]bool{}
	add := func(spec string) {
		if seen[spec] {
			return
		}
		seen[spec] = true
		imp := moduleImport{Specifier: spec}
		switch {
		case spec == "vela" || spec == "vela/store":
			imp.Kind = "runtime"
		case IsPackageSpecifier(spec):
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
	WalkNode(prog, func(n JsNode) bool {
		switch x := n.(type) {
		case *JsImportDecl:
			add(x.Source)
		case *JsExportDecl:
			if x.HasSource {
				add(x.Source)
			}
		case *JsCallExpr:
			if callee, ok := x.Callee.(*JsIdent); ok && callee.Name == "import" && len(x.Args) > 0 {
				if lit, ok := x.Args[0].(*JsLiteral); ok && lit.Kind == "string" && len(lit.Raw) >= 2 {
					add(lit.Raw[1 : len(lit.Raw)-1])
				}
			}
		}
		return true
	})
	return out
}

// compilerService compiles requests against one working directory and
// configuration. It is safe for concurrent use.
type compilerService struct {
	cwd    string
	config *velaConfig
	// flags are the file flags given to `vela serve` or `vela compile`; they
	// override the configuration, and a request overrides them.
	flags []flagValue
}

func newCompilerService(configPath string, flags []flagValue) (*compilerService, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	config, err := loadConfig(configPath)
	if err != nil {
		return nil, err
	}
	return &compilerService{cwd: cwd, config: config, flags: flags}, nil
}

// requestOptions resolves the options of a request: request, command line,
// configuration, service defaults.
func (s *compilerService) requestOptions(req serviceRequest) (CompileOptions, error) {
	fs := flag.NewFlagSet("request", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	file := addFileFlags(fs, serviceFlagDefaults)
	if err := applyFlagValues(fs, "request options", req.Options.flagValues()); err != nil {
		return CompileOptions{}, err
	}
	if err := applyFlagValues(fs, "command line", s.flags); err != nil {
		return CompileOptions{}, err
	}
	if s.config != nil {
		if err := applyFlagValues(fs, ConfigFileName, s.config.fileOptions.flagValues()); err != nil {
			return CompileOptions{}, err
		}
	}
	return file.options()
}

// sourcePath is the path a build run in the working directory would use for
// the file: relative when the file is inside it. The path determines the
// scope class and names the file in diagnostics and source maps.
func (s *compilerService) sourcePath(path string) string {
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(s.cwd, abs)
	}
	rel, err := filepath.Rel(s.cwd, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.Clean(abs)
	}
	return rel
}

func (s *compilerService) compile(req serviceRequest) compileResult {
	res := compileResult{ID: req.ID, Diagnostics: []MachineDiagnostic{}, Imports: []moduleImport{}}
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
	src := s.sourcePath(req.Path)
	// The site path of a project build: relative to the parent of the
	// project directory (compileProjectFile).
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

// serveStdio runs the service over newline-delimited JSON: the handshake,
// then one response per request line, in completion order. Requests compile
// concurrently; at the end of the input the service finishes the requests it
// has and returns.
func serveStdio(in io.Reader, out io.Writer, s *compilerService) error {
	var mu sync.Mutex
	var writeErr error
	write := func(v any) {
		line, err := json.Marshal(v)
		mu.Lock()
		defer mu.Unlock()
		if err == nil && writeErr == nil {
			_, err = out.Write(append(line, '\n'))
		}
		if err != nil && writeErr == nil {
			writeErr = err
		}
	}
	write(newServiceHandshake())

	reader := bufio.NewReader(in)
	limit := make(chan struct{}, goruntime.GOMAXPROCS(0))
	var wg sync.WaitGroup
	var readErr error
	for {
		line, err := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var req serviceRequest
			if decodeErr := decodeStrict(line, &req); decodeErr != nil {
				var probe struct {
					ID json.RawMessage `json:"id"`
				}
				_ = json.Unmarshal(line, &probe)
				write(compileResult{ID: probe.ID, Diagnostics: []MachineDiagnostic{}, Imports: []moduleImport{}, Error: "invalid request: " + decodeErr.Error()})
			} else {
				wg.Add(1)
				limit <- struct{}{}
				go func() {
					defer wg.Done()
					write(s.compile(req))
					<-limit
				}()
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				readErr = err
			}
			break
		}
	}
	wg.Wait()
	if readErr != nil {
		return readErr
	}
	return writeErr
}

// explicitFlags lists the flags set on the command line.
func explicitFlags(fs *flag.FlagSet) []flagValue {
	var out []flagValue
	fs.Visit(func(f *flag.Flag) { out = append(out, flagValue{f.Name, f.Value.String()}) })
	return out
}

// runServeCommand is `vela serve --stdio`.
func runServeCommand(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vela serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stdio := fs.Bool("stdio", false, "serve newline-delimited JSON on stdin/stdout (the only transport)")
	protocol := fs.Int("protocol", ServiceProtocolVersion, "protocol version the client speaks")
	configPath := fs.String("config", "", "configuration file (default: "+ConfigFileName+" in the working directory, if present)")
	file := addFileFlags(fs, serviceFlagDefaults)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !*stdio || fs.NArg() > 0 {
		fmt.Fprintln(stderr, "usage: vela serve --stdio [flags]")
		return 2
	}
	if *protocol != ServiceProtocolVersion {
		handshake := newServiceHandshake()
		handshake.Error = fmt.Sprintf("protocol %d is not supported; this compiler speaks protocol %d", *protocol, ServiceProtocolVersion)
		line, _ := json.Marshal(handshake)
		fmt.Fprintf(stdout, "%s\n", line)
		return 2
	}
	if _, err := file.options(); err != nil {
		fmt.Fprintf(stderr, "vela: %v\n", err)
		return 2
	}
	s, err := newCompilerService(*configPath, explicitFlags(fs))
	if err != nil {
		fmt.Fprintf(stderr, "vela: %v\n", err)
		return 2
	}
	if err := serveStdio(stdin, stdout, s); err != nil {
		fmt.Fprintf(stderr, "vela: %v\n", err)
		return 1
	}
	return 0
}

// runCompileCommand is `vela compile`: one component, one JSON result on
// stdout. The exit code is 1 when the component does not compile.
func runCompileCommand(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vela compile", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("path", "", "the component's path; names it in diagnostics and source maps and sets its scope")
	useStdin := fs.Bool("stdin", false, "read the source from stdin instead of --path")
	configPath := fs.String("config", "", "configuration file (default: "+ConfigFileName+" in the working directory, if present)")
	file := addFileFlags(fs, serviceFlagDefaults)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *path == "" || fs.NArg() > 0 {
		fmt.Fprintln(stderr, "usage: vela compile --path <file.vela> [--stdin] [flags]")
		return 2
	}
	if _, err := file.options(); err != nil {
		fmt.Fprintf(stderr, "vela: %v\n", err)
		return 2
	}
	s, err := newCompilerService(*configPath, explicitFlags(fs))
	if err != nil {
		fmt.Fprintf(stderr, "vela: %v\n", err)
		return 2
	}
	req := serviceRequest{Path: *path}
	if *useStdin {
		data, err := io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintf(stderr, "vela: read stdin: %v\n", err)
			return 2
		}
		source := string(data)
		req.Source = &source
	}
	res := s.compile(req)
	line, _ := json.Marshal(res)
	fmt.Fprintf(stdout, "%s\n", line)
	if !res.OK {
		return 1
	}
	return 0
}
