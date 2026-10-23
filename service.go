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
	goruntime "runtime"
	"sync"
)

// ServiceProtocolVersion is the version of the compiler service protocol
// (docs/TOOLING_PROTOCOL.md). It changes when a client written for the
// previous version would misread a message.
const ServiceProtocolVersion = 1

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

// serveStdio runs the service over newline-delimited JSON: the handshake,
// then one response per request line, in completion order. Requests compile
// concurrently; at the end of the input the service finishes the requests it
// has and returns.
func serveStdio(in io.Reader, out io.Writer, s *CompilerService) error {
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
			var req ServiceRequest
			if decodeErr := DecodeStrict(line, &req); decodeErr != nil {
				var probe struct {
					ID json.RawMessage `json:"id"`
				}
				_ = json.Unmarshal(line, &probe)
				write(CompileResult{ID: probe.ID, Diagnostics: []MachineDiagnostic{}, Imports: []ModuleImport{}, Error: "invalid request: " + decodeErr.Error()})
			} else {
				wg.Add(1)
				limit <- struct{}{}
				go func() {
					defer wg.Done()
					write(s.Compile(req))
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
func explicitFlags(fs *flag.FlagSet) []FlagValue {
	var out []FlagValue
	fs.Visit(func(f *flag.Flag) { out = append(out, FlagValue{Name: f.Name, Value: f.Value.String()}) })
	return out
}

// runServeCommand is `vela serve --stdio`.
func runServeCommand(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("vela serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stdio := fs.Bool("stdio", false, "serve newline-delimited JSON on stdin/stdout (the only transport)")
	protocol := fs.Int("protocol", ServiceProtocolVersion, "protocol version the client speaks")
	configPath := fs.String("config", "", "configuration file (default: "+ConfigFileName+" in the working directory, if present)")
	file := AddFileFlags(fs, ServiceFlagDefaults)
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
	if _, err := file.Options(); err != nil {
		fmt.Fprintf(stderr, "vela: %v\n", err)
		return 2
	}
	s, err := NewCompilerService(*configPath, explicitFlags(fs))
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
	file := AddFileFlags(fs, ServiceFlagDefaults)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *path == "" || fs.NArg() > 0 {
		fmt.Fprintln(stderr, "usage: vela compile --path <file.vela> [--stdin] [flags]")
		return 2
	}
	if _, err := file.Options(); err != nil {
		fmt.Fprintf(stderr, "vela: %v\n", err)
		return 2
	}
	s, err := NewCompilerService(*configPath, explicitFlags(fs))
	if err != nil {
		fmt.Fprintf(stderr, "vela: %v\n", err)
		return 2
	}
	req := ServiceRequest{Path: *path}
	if *useStdin {
		data, err := io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintf(stderr, "vela: read stdin: %v\n", err)
			return 2
		}
		source := string(data)
		req.Source = &source
	}
	res := s.Compile(req)
	line, _ := json.Marshal(res)
	fmt.Fprintf(stdout, "%s\n", line)
	if !res.OK {
		return 1
	}
	return 0
}
