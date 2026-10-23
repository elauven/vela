package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// serviceSession runs serveStdio over pipes for a test.
type serviceSession struct {
	t      *testing.T
	in     *io.PipeWriter
	out    *bufio.Reader
	done   chan error
	header serviceHandshake
}

func startService(t *testing.T, s *CompilerService) *serviceSession {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	session := &serviceSession{t: t, in: inW, out: bufio.NewReader(outR), done: make(chan error, 1)}
	go func() {
		err := serveStdio(inR, outW, s)
		_ = outW.Close()
		session.done <- err
	}()
	line, err := session.out.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(line, &session.header); err != nil {
		t.Fatalf("handshake %s: %v", line, err)
	}
	return session
}

func (s *serviceSession) send(req any) {
	s.t.Helper()
	line, err := json.Marshal(req)
	if err != nil {
		s.t.Fatal(err)
	}
	if _, err := s.in.Write(append(line, '\n')); err != nil {
		s.t.Fatal(err)
	}
}

func (s *serviceSession) receive() CompileResult {
	s.t.Helper()
	line, err := s.out.ReadBytes('\n')
	if err != nil {
		s.t.Fatal(err)
	}
	var res CompileResult
	if err := json.Unmarshal(line, &res); err != nil {
		s.t.Fatalf("response %s: %v", line, err)
	}
	return res
}

func (s *serviceSession) close() {
	s.t.Helper()
	_ = s.in.Close()
	if rest, _ := io.ReadAll(s.out); len(bytes.TrimSpace(rest)) > 0 {
		s.t.Errorf("unexpected output after the last response: %s", rest)
	}
	if err := <-s.done; err != nil {
		s.t.Fatal(err)
	}
}

func boolPtr(v bool) *bool       { return &v }
func stringPtr(v string) *string { return &v }

// serviceVariant is a set of request options and the build flags that select
// the same compilation.
type serviceVariant struct {
	name    string
	options FileOptions
	flags   []string
}

var serviceVariants = []serviceVariant{
	{"defaults", FileOptions{}, []string{"--css", "external", "--runtime-import", "bare"}},
	{"inject-relative", FileOptions{CSS: stringPtr("inject"), RuntimeImport: stringPtr("relative")}, nil},
	{"sourcemap", FileOptions{SourceMap: boolPtr(true), Target: stringPtr("browser")}, []string{"--css", "external", "--runtime-import", "bare", "--sourcemap", "--target", "browser"}},
	{"production", FileOptions{Minify: boolPtr(true), Mode: stringPtr("production")}, []string{"--css", "external", "--runtime-import", "bare", "--minify", "--mode", "production"}},
}

// buildFileOptions compiles src with the build command's flag parsing.
func buildFileOptions(t *testing.T, args []string) CompileOptions {
	t.Helper()
	fs := newTestFlagSet()
	file := AddFileFlags(fs, BuildFlagDefaults)
	if err := fs.Parse(args); err != nil {
		t.Fatal(err)
	}
	opts, err := file.Options()
	if err != nil {
		t.Fatal(err)
	}
	return opts
}

func TestServiceMatchesFileModeForConcurrentRequests(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("examples", "*.vela"))
	if err != nil || len(files) == 0 {
		t.Fatalf("examples: %v %v", files, err)
	}
	svc := &CompilerService{Cwd: mustGetwd(t)}
	session := startService(t, svc)
	if session.header.Protocol != ServiceProtocolVersion || session.header.RuntimeContract != RuntimeContractExport {
		t.Fatalf("handshake = %+v", session.header)
	}
	type key struct{ variant, file string }
	// The client reads while it sends: the service blocks on unread responses.
	sent := len(serviceVariants) * len(files)
	go func() {
		for _, v := range serviceVariants {
			for _, f := range files {
				session.send(ServiceRequest{ID: json.RawMessage(fmt.Sprintf("%q", v.name+"|"+f)), Path: f, Options: v.options})
			}
		}
	}()
	got := map[key]CompileResult{}
	for i := 0; i < sent; i++ {
		res := session.receive()
		var id string
		if err := json.Unmarshal(res.ID, &id); err != nil {
			t.Fatalf("id %s: %v", res.ID, err)
		}
		name, file, _ := strings.Cut(id, "|")
		got[key{name, file}] = res
	}
	session.close()

	for _, v := range serviceVariants {
		for _, f := range files {
			res, ok := got[key{v.name, f}]
			if !ok {
				t.Fatalf("%s %s: no response", v.name, f)
			}
			dir := t.TempDir()
			dest := filepath.Join(dir, ComponentName(f)+".js")
			opts := buildFileOptions(t, v.flags)
			opts.SitePath = filepath.ToSlash(f)
			diags, err := CompileFile(f, dest, opts)
			if (err == nil) != res.OK {
				t.Fatalf("%s %s: file mode err = %v, service ok = %v (%s)", v.name, f, err, res.OK, res.Error)
			}
			if len(diags) != len(res.Diagnostics) {
				t.Fatalf("%s %s: %d diagnostics in file mode, %d from the service", v.name, f, len(diags), len(res.Diagnostics))
			}
			if err != nil {
				continue
			}
			js := readTestFile(t, dest)
			if res.Map != nil {
				js = strings.TrimSuffix(js, "\n//# sourceMappingURL="+filepath.Base(dest)+".map\n")
				if want := readTestFile(t, dest+".map"); string(res.Map) != want {
					t.Errorf("%s %s: source map differs", v.name, f)
				}
			}
			if res.JS != js {
				t.Errorf("%s %s: js differs from file mode", v.name, f)
			}
			wantCSS, _ := os.ReadFile(strings.TrimSuffix(dest, ".js") + ".css")
			if res.CSS != string(wantCSS) {
				t.Errorf("%s %s: css = %q, want %q", v.name, f, res.CSS, wantCSS)
			}
		}
	}
}

// serviceDiagnosticCorpus covers diagnostics of every compilation stage.
var serviceDiagnosticCorpus = map[string]string{
	"template-syntax": "<template>\n{#await p then}<p>bad</p>{/await}\n</template>",
	"js-syntax":       "<script>let x = ;</script>\n<template><p>x</p></template>",
	"css-syntax":      "<template><p>x</p></template>\n<style>p { color: red; </style>",
	"event-modifier":  "<script>function go() {}</script>\n<template><button @click.prevent={go}>go</button></template>",
	"duplicate-attr":  "<template><p title=\"a\" title=\"b\">x</p></template>",
	"unknown-import":  "<script>import { nope } from 'vela';</script>\n<template><p>x</p></template>",
	"unicode-column":  "<script>\n  const s = 'привет'; const w = window.innerWidth;\n</script>\n<template><p>{s}{w}</p></template>",
	"warning-only":    "<script>\n  function f() { return 1; console.log('x'); }\n</script>\n<template><p title=\"{f()}\">x</p></template>",
}

func TestServiceDiagnosticsMatchTextDiagnostics(t *testing.T) {
	dir := t.TempDir()
	svc := &CompilerService{Cwd: mustGetwd(t)}
	opts := buildFileOptions(t, []string{"--css", "external", "--runtime-import", "bare", "--target", "worker"})
	for name, source := range serviceDiagnosticCorpus {
		src := filepath.Join(dir, "App.vela")
		if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		opts.SitePath = filepath.ToSlash(svc.SourcePath(src))
		diags, _ := CompileFile(svc.SourcePath(src), filepath.Join(dir, "App.js"), opts)
		if len(diags) == 0 {
			t.Fatalf("%s: corpus entry produces no diagnostics", name)
		}
		res := svc.Compile(ServiceRequest{Path: src, Source: &source, Options: FileOptions{Target: stringPtr("worker")}})
		if len(res.Diagnostics) != len(diags) {
			t.Fatalf("%s: %d JSON diagnostics, %d text diagnostics", name, len(res.Diagnostics), len(diags))
		}
		for i, d := range diags {
			text := d.String()
			m := res.Diagnostics[i]
			if !strings.HasPrefix(text, fmt.Sprintf("%s:%d:%d: [%s]", filepath.FromSlash(m.File), m.Line, m.Column, m.Severity)) {
				t.Errorf("%s: JSON %+v does not locate text %q", name, m, text)
			}
			if endLine, endCol := d.End(); m.Code != d.Code || m.Message != d.Message || m.EndLine != endLine || m.EndColumn != endCol {
				t.Errorf("%s: JSON %+v, text %q", name, m, text)
			}
		}
	}
}

func TestServiceReportsImports(t *testing.T) {
	source := `<script>
  import { onMount } from 'vela';
  import { format } from 'date-fns/format';
  import { Card } from './Card.vela';
  import { sum } from '../lib/math.js';
  const later = () => import('./Lazy.vela');
</script>
<template><Card /></template>`
	svc := &CompilerService{Cwd: mustGetwd(t)}
	res := svc.Compile(ServiceRequest{Path: filepath.Join("src", "pages", "App.vela"), Source: &source})
	if !res.OK {
		t.Fatalf("compile: %s %+v", res.Error, res.Diagnostics)
	}
	want := []ModuleImport{
		{Specifier: "vela", Kind: "runtime", Resolved: ""},
		{Specifier: "date-fns/format", Kind: "package", Resolved: ""},
		{Specifier: "./Card.vela", Kind: "component", Resolved: filepath.Join("src", "pages", "Card.vela")},
		{Specifier: "../lib/math.js", Kind: "module", Resolved: filepath.Join("src", "lib", "math.js")},
		{Specifier: "./Lazy.vela", Kind: "component", Resolved: filepath.Join("src", "pages", "Lazy.vela")},
	}
	if fmt.Sprint(res.Imports) != fmt.Sprint(want) {
		t.Fatalf("imports = %+v\nwant      %+v", res.Imports, want)
	}
}

// componentImports "vela" keeps the component specifiers for bundler
// plugins; the default points them to the compiled modules, as a build does.
func TestServiceComponentImports(t *testing.T) {
	source := `<script>
  import { Card } from './Card.vela';
  import { sum } from './math.js';
  const later = () => import('./Lazy.vela');
</script>
<template><Card :n={sum(1, 2)} /></template>`
	svc := &CompilerService{Cwd: mustGetwd(t)}
	for _, c := range []struct {
		option string
		want   []string
	}{
		{"", []string{"'./Card.js'", "'./math.js'"}},
		{"vela", []string{"'./Card.vela'", "'./math.js'", "'./Lazy.vela'"}},
	} {
		opts := FileOptions{}
		if c.option != "" {
			opts.ComponentImports = stringPtr(c.option)
		}
		res := svc.Compile(ServiceRequest{Path: "App.vela", Source: &source, Options: opts})
		if !res.OK {
			t.Fatalf("componentImports %q: %s %+v", c.option, res.Error, res.Diagnostics)
		}
		for _, want := range c.want {
			if !strings.Contains(res.JS, want) {
				t.Errorf("componentImports %q: the module misses %s:\n%s", c.option, want, res.JS)
			}
		}
	}
	if res := svc.Compile(ServiceRequest{Path: "App.vela", Source: &source, Options: FileOptions{ComponentImports: stringPtr("ts")}}); res.OK || !strings.Contains(res.Error, "invalid --component-imports value") {
		t.Fatalf("invalid componentImports: %+v", res)
	}
}

func TestServiceRejectsBadRequestsAndContinues(t *testing.T) {
	session := startService(t, &CompilerService{Cwd: mustGetwd(t)})
	source := "<template><p>x</p></template>"
	if _, err := session.in.Write([]byte("{\"id\": 1, \"path\": \"A.vela\", \"bogus\": true}\n")); err != nil {
		t.Fatal(err)
	}
	if res := session.receive(); string(res.ID) != "1" || !strings.Contains(res.Error, "invalid request") {
		t.Fatalf("unknown field response = %+v", res)
	}
	session.send(ServiceRequest{ID: json.RawMessage("2"), Method: "format", Path: "A.vela", Source: &source})
	if res := session.receive(); !strings.Contains(res.Error, "unknown method") {
		t.Fatalf("unknown method response = %+v", res)
	}
	session.send(ServiceRequest{ID: json.RawMessage("3"), Path: "A.vela", Source: &source, Options: FileOptions{CSS: stringPtr("inline")}})
	if res := session.receive(); res.OK || !strings.Contains(res.Error, "invalid --css value") {
		t.Fatalf("invalid option response = %+v", res)
	}
	session.send(ServiceRequest{ID: json.RawMessage("4"), Path: "A.vela", Source: &source})
	if res := session.receive(); !res.OK || res.JS == "" {
		t.Fatalf("valid request after errors = %+v", res)
	}
	session.close()
}

func TestServeRejectsUnsupportedProtocolInHandshake(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runServeCommand([]string{"--stdio", "--protocol", "2"}, strings.NewReader(""), &stdout, &stderr)
	var handshake serviceHandshake
	if err := json.Unmarshal(stdout.Bytes(), &handshake); err != nil {
		t.Fatalf("stdout %q: %v", stdout.String(), err)
	}
	if code != 2 || handshake.Protocol != ServiceProtocolVersion || !strings.Contains(handshake.Error, "protocol 2 is not supported") {
		t.Fatalf("code %d, handshake %+v", code, handshake)
	}
}

func TestServiceOptionPrecedence(t *testing.T) {
	dir := t.TempDir()
	chdirForTest(t, dir)
	writeTestFile(t, filepath.Join(dir, ConfigFileName), `{"css": "inject", "mode": "production", "minify": true, "project": "src"}`)
	source := "<template><p>x</p></template>\n<style>p { color: red; }</style>"
	writeTestFile(t, filepath.Join(dir, "src", "App.vela"), source)

	// Configuration: injected CSS, production, minified.
	var stdout, stderr bytes.Buffer
	if code := runCompileCommand([]string{"--path", "src/App.vela"}, nil, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s %s", code, stdout.String(), stderr.String())
	}
	var res CompileResult
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.CSS != "" || strings.Contains(res.JS, "\n  ") {
		t.Fatalf("configuration not applied: css %q js %q", res.CSS, res.JS)
	}

	// The command line overrides the configuration, a request overrides both.
	svc, err := NewCompilerService("", []FlagValue{{Name: "css", Value: "external"}, {Name: "minify", Value: "false"}})
	if err != nil {
		t.Fatal(err)
	}
	res = svc.Compile(ServiceRequest{Path: "src/App.vela"})
	if res.CSS == "" || !strings.Contains(res.JS, "\n  ") {
		t.Fatalf("command line not applied: %+v", res)
	}
	res = svc.Compile(ServiceRequest{Path: "src/App.vela", Options: FileOptions{CSS: stringPtr("inject")}})
	if res.CSS != "" {
		t.Fatalf("request option not applied: css %q", res.CSS)
	}

	// --stdin source with the path of a file that does not exist.
	stdout.Reset()
	if code := runCompileCommand([]string{"--stdin", "--path", "src/Other.vela"}, strings.NewReader(source), &stdout, &stderr); code != 0 {
		t.Fatalf("stdin exit %d: %s", code, stdout.String())
	}
}

func TestCompileCommandExitCodes(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runCompileCommand([]string{"--stdin", "--path", "App.vela"}, strings.NewReader("<script>let x = ;</script>"), &stdout, &stderr); code != 1 {
		t.Fatalf("broken component: exit %d", code)
	}
	var res CompileResult
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil || res.OK || len(res.Diagnostics) == 0 {
		t.Fatalf("broken component result %s: %v", stdout.String(), err)
	}
	if code := runCompileCommand(nil, nil, &stdout, &stderr); code != 2 {
		t.Fatalf("missing --path: exit %d", code)
	}
}

func TestConfigRejectsUnknownKeysAndResolvesPaths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conf", ConfigFileName)
	abs := filepath.Join(dir, "abs", "out")
	absJSON, _ := json.Marshal(abs)
	writeTestFile(t, path, `{"$schema": "x", "project": "src", "out": `+string(absJSON)+`, "targt": "browser"}`)
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), `unknown field "targt"`) {
		t.Fatalf("unknown key error = %v", err)
	}
	writeTestFile(t, path, `{"project": "src", "out": `+string(absJSON)+`}`)
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if *config.Project != filepath.Join(dir, "conf", "src") || *config.Out != abs {
		t.Fatalf("paths = %q %q", *config.Project, *config.Out)
	}
	if _, err := LoadConfig(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("an explicit missing configuration must fail")
	}
	chdirForTest(t, dir)
	if config, err := LoadConfig(""); config != nil || err != nil {
		t.Fatalf("no default configuration: %v %v", config, err)
	}
}

func newTestFlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func chdirForTest(t *testing.T, dir string) {
	t.Helper()
	prev := mustGetwd(t)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
