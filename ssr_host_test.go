package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// captureStderr returns what fn writes to os.Stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = writer
	done := make(chan string)
	go func() {
		var buffer bytes.Buffer
		_, _ = io.Copy(&buffer, reader)
		done <- buffer.String()
	}()
	fn()
	os.Stderr = saved
	writer.Close()
	return <-done
}

type ssrHostProject struct {
	t    *testing.T
	root string
}

func newSSRHostProject(t *testing.T) *ssrHostProject {
	t.Helper()
	p := &ssrHostProject{t: t, root: t.TempDir()}
	compilerRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	p.write("go.mod", "module example.com/dash\n\ngo 1.25\n\n"+ssrRuntimeRequire(compilerRoot))
	p.write("vela.ssr.json", `{"module": "example.com/dash/out/server", "host": {"module": "example.com/dash/ssrhost"}}`)
	p.write("src/format.js", `export function formatCurrency(value) {
  return new Intl.NumberFormat('ru-RU', { style: 'currency', currency: 'RUB', maximumFractionDigits: 0 }).format(value);
}
`)
	p.write("src/App.vela", `<script>
  import { formatCurrency } from './format.js';
  export let total = 0;
  let label = $derived(formatCurrency(total));
</script>
<template>
  <div class="app"><p class="total">{formatCurrency(total)}</p><p class="label">{label}|{label}</p><button class="noop" @click={() => {}}>+</button></div>
</template>
`)
	return p
}

func (p *ssrHostProject) write(name, content string) {
	p.t.Helper()
	file := filepath.Join(p.root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		p.t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		p.t.Fatal(err)
	}
}

// build runs the SSR project build with the block engine and returns the
// error count and stderr.
func (p *ssrHostProject) build(scaffold bool) (int, string) {
	p.t.Helper()
	srcRoot := filepath.Join(p.root, "src")
	var errors int
	stderr := captureStderr(p.t, func() {

		opts := CompileOptions{StatusWriter: io.Discard, SSRModule: SSRDefaultModule, SSRScaffoldHosts: scaffold}
		errors = buildSSRProject(collectVela(srcRoot), srcRoot, filepath.Join(p.root, "out"), opts, false)

	})
	return errors, stderr
}

// run renders App with total 1234 through RenderAppContext with a locale in
// the context and returns stdout.
func (p *ssrHostProject) run() string {
	p.t.Helper()
	p.write("cmd/render/main.go", `package main

import (
	"context"
	"fmt"
	"os"

	format "example.com/dash/ssrhost/src/format"
	c "example.com/dash/out/server/components"
)

func main() {
	ctx := context.WithValue(context.Background(), format.LocaleKey{}, "ru")
	err := c.RenderAppContext(ctx, os.Stdout, c.AppProps{Total: 1234, Set: map[string]bool{"total": true}}, c.AppSlots{})
	fmt.Printf("\nERR=%v\nCALLS=%d\n", err, format.Calls)
}
`)
	command := exec.Command("go", "run", "./cmd/render")
	command.Dir = p.root
	output, err := command.CombinedOutput()
	if err != nil {
		p.t.Fatalf("go run: %v\n%s", err, output)
	}
	return string(output)
}

// TestSSRHostImplementationEndToEnd covers task 030: the diagnostic names
// the host file and signature, scaffolding creates a stub that fails the
// render with a positioned error, and an implementation renders through
// the render context, called exactly once per evaluation (T-6.1c).
func TestSSRHostImplementationEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not available")
	}
	p := newSSRHostProject(t)
	// The shared host package declares the context key; the
	// implementation comes later.
	p.write("ssrhost/src/format/locale.go", "package format\n\n// LocaleKey carries the locale in the render context.\ntype LocaleKey struct{}\n\n// Calls counts host calls for the test.\nvar Calls int\n")

	errors, stderr := p.build(false)
	if errors == 0 {
		t.Fatal("a call without native translation or host built")
	}
	for _, want := range []string{
		"VELA-SSR-HOST-MISSING",
		"src/App.vela:7:",
		"reason: template text → call \"formatCurrency\"",
		"ssrhost/src/format/format_currency_host.go",
		"//vela:host formatCurrency",
		"func FormatCurrency(ctx context.Context, value float64) (any, error)",
		"--ssr-scaffold-hosts",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("diagnostic misses %q:\n%s", want, stderr)
		}
	}

	if _, stderr := p.build(true); !strings.Contains(stderr, "VELA-SSR-HOST-MISSING") {
		t.Fatalf("the scaffolding build still reports the missing host:\n%s", stderr)
	}
	stub := filepath.Join(p.root, "ssrhost", "src", "format", "format_currency_host.go")
	if data, err := os.ReadFile(stub); err != nil || !strings.Contains(string(data), "vela.ErrHostNotImplemented") || !strings.Contains(string(data), "package format") {
		t.Fatalf("stub: %v\n%s", err, data)
	}
	if errors, stderr := p.build(false); errors != 0 {
		t.Fatalf("build with the stub: %d errors\n%s", errors, stderr)
	}
	// The client module names the site and its host for hydration reports
	// (V-11.3b).
	if client, err := os.ReadFile(filepath.Join(p.root, "out", "client", "App.js")); err != nil || !regexp.MustCompile(`\$\$sites = \[.*['"]src/App\.vela:7:\d+['"], ['"]formatCurrency['"]`).Match(client) {
		t.Errorf("client site table: %v\n%s", err, client)
	}
	if out := p.run(); !strings.Contains(out, "ERR=src/App.vela:4:") || !strings.Contains(out, "host formatCurrency: vela: host function is not implemented") {
		t.Fatalf("stub render:\n%s", out)
	}

	p.write("ssrhost/src/format/format_currency_host.go", `package format

import (
	"context"
	"fmt"
)

// FormatCurrency implements formatCurrency of src/format.js.
//
//vela:host formatCurrency
func FormatCurrency(ctx context.Context, value float64) (string, error) {
	Calls++
	return fmt.Sprintf("%s:%.0f RUB", ctx.Value(LocaleKey{}), value), nil
}
`)
	if errors, stderr := p.build(false); errors != 0 {
		t.Fatalf("build with the host: %d errors\n%s", errors, stderr)
	}
	out := p.run()
	for _, want := range []string{`<p vela-`, `>ru:1234 RUB</p>`, `>ru:1234 RUB|ru:1234 RUB</p>`, "ERR=<nil>", "CALLS=2"} {
		if !strings.Contains(out, want) {
			t.Errorf("host render misses %q:\n%s", want, out)
		}
	}

	// The client hydrates the host-rendered text and keeps using the
	// JavaScript function for updates (H-8.5a, task 041). This host
	// deliberately differs from the JavaScript function: development
	// verification reports the parity violation and writes the client
	// value (V-11.3b); with verification off the server text stays.
	node, jsdom := requireJsdom(t)
	clientDir := filepath.Join(p.root, "out", "client")
	p.write("out/client/package.json", `{"type":"module"}`)
	p.write("page.html", strings.SplitN(out, "\nERR=", 2)[0])
	for _, policy := range []string{"repair", "off"} {
		command := exec.Command(node, "tools/ssr-hydrate-run.mjs", jsdom, clientDir, "App.js", "App", filepath.Join(p.root, "page.html"), `[{"set":{"total":5000}}]`, policy)
		var stderr bytes.Buffer
		command.Stderr = &stderr
		result, err := command.Output()
		if err != nil {
			t.Fatalf("hydrate (%s): %v\n%s%s", policy, err, result, stderr.String())
		}
		var run struct {
			OK        bool     `json:"ok"`
			Error     string   `json:"error"`
			Snapshots []string `json:"snapshots"`
		}
		if err := json.Unmarshal(result, &run); err != nil || !run.OK || len(run.Snapshots) != 2 {
			t.Fatalf("hydrate run (%s): %v %s", policy, err, result)
		}
		// V-11.3b: position, site ID, host symbol, and both values.
		reported := strings.Contains(stderr.String(), `[vela] hydration mismatch at src/App.vela:7:38 (site 0, host formatCurrency)`) &&
			strings.Contains(stderr.String(), `server: "ru:1234 RUB"   client: "1`)
		switch policy {
		case "repair":
			if !reported || !strings.Contains(run.Snapshots[0], "₽</p>") {
				t.Errorf("repair: parity violation not reported or not repaired:\n%s\n%s", stderr.String(), run.Snapshots[0])
			}
		case "off":
			if reported || !strings.Contains(run.Snapshots[0], ">ru:1234 RUB</p>") {
				t.Errorf("off: the server text was not kept:\n%s\n%s", stderr.String(), run.Snapshots[0])
			}
		}
		if !strings.Contains(run.Snapshots[1], "5") || !strings.Contains(run.Snapshots[1], "₽") || strings.Contains(run.Snapshots[1], "RUB</p>") {
			t.Errorf("%s: the update did not use the JavaScript function: %s", policy, run.Snapshots[1])
		}
	}
}

func TestSSRHostDirectivesAreChecked(t *testing.T) {
	p := newSSRHostProject(t)
	p.write("ssrhost/src/format/hosts.go", `package format

import "context"

//vela:host formatMoney
func FormatMoney(value float64) (string, error) { return "", nil }

//vela:host formatCurrency
func FormatCurrency(ctx context.Context, value string) (string, error) { return value, nil }
`)
	p.write("ssrhost/src/other/other.go", "package other\n\n//vela:host anything\nfunc Anything() (string, error) { return \"\", nil }\n")
	p.write("ssrhost/src/format/zz_bad.go", "package format\n\n//vela:host formatCurrency\nfunc Twice(value float64) chan int { return nil }\n")
	errors, stderr := p.build(false)
	if errors == 0 {
		t.Fatal("broken directives built")
	}
	for _, want := range []string{
		"ssrhost/src/format/hosts.go:5: [error] VELA-SSR-HOST-STALE //vela:host formatMoney: src/format.js has no function \"formatMoney\"",
		"ssrhost/src/other/other.go:3: [error] VELA-SSR-HOST-STALE //vela:host anything: no JavaScript source mirrors ssrhost/src/other",
		"VELA-SSR-HOST-STALE //vela:host formatCurrency is bound twice",
		"VELA-SSR-HOST-SIG",
		"argument 1 of formatCurrency is a number, but FormatCurrency takes value string",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("diagnostics miss %q:\n%s", want, stderr)
		}
	}
}

// TestSSRHostProvidesModuleValue covers §8.7: a module value whose
// initializer is not translatable comes from a parameterless host, called
// once per render however often it is read.
func TestSSRHostProvidesModuleValue(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not available")
	}
	p := newSSRHostProject(t)
	p.write("src/App.vela", `<script>
  const rates = JSON.parse(window.localStorage.getItem('rates'));
  export let total = 0;
</script>
<template>
  <p class="usd">{rates.usd}|{rates.usd + total}</p>
</template>
`)
	p.write("ssrhost/src/app/rates_host.go", `package app

// Calls counts host calls for the test.
var Calls int

// Rates provides the rates value of App.vela.
//
//vela:host rates
func Rates() (map[string]float64, error) {
	Calls++
	return map[string]float64{"usd": 90}, nil
}
`)
	p.write("ssrhost/src/format/locale.go", "package format\n\ntype LocaleKey struct{}\n\nvar Calls int\n")
	if errors, stderr := p.build(false); errors != 0 {
		t.Fatalf("build: %d errors\n%s", errors, stderr)
	}
	p.write("cmd/render/main.go", `package main

import (
	"fmt"
	"os"

	app "example.com/dash/ssrhost/src/app"
	c "example.com/dash/out/server/components"
)

func main() {
	err := c.RenderApp(os.Stdout, c.AppProps{Total: 10, Set: map[string]bool{"total": true}}, c.AppSlots{})
	fmt.Printf("\nERR=%v\nCALLS=%d\n", err, app.Calls)
}
`)
	command := exec.Command("go", "run", "./cmd/render")
	command.Dir = p.root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go run: %v\n%s", err, output)
	}
	for _, want := range []string{">90|100</p>", "ERR=<nil>", "CALLS=1"} {
		if !strings.Contains(string(output), want) {
			t.Errorf("output misses %q:\n%s", want, output)
		}
	}
}
