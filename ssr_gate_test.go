package main

// ssr_gate_test.go — the SSR beta gate (task 031).
//
// Every project of testdata/ssr_gate/expected.json goes through the public
// path: the real CLI (`vela --ssr --ssr-report`), the generated Go compiled
// and run as an HTTP server, and Chromium (tools/ssr-gate-check.mjs). The
// results are compared with the frozen expectations: component status, root
// mode, the outcome of every site, the render, and the browser checks.
// Negative projects must fail with their declared diagnostics.
//
// The expectations are never regenerated silently. VELA_SSR_GATE_UPDATE=1
// rewrites expected.json only when nothing regressed; a regression (a site
// that becomes unsupported, a component that stops building, rendering or
// passing the browser checks, an interactive root that becomes static, a
// removed component) additionally needs VELA_SSR_GATE_ACCEPT="<decision>",
// which is appended with the regressions to testdata/ssr_gate/DECISIONS.md.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

const ssrGateDir = "testdata/ssr_gate"

var ssrGateServerAddr = regexp.MustCompile(`http://127\.0\.0\.1:\d+`)

type ssrGateManifest struct {
	Version  int              `json:"version"`
	Projects []ssrGateProject `json:"projects"`
}

type ssrGateProject struct {
	ID   string `json:"id"`
	Role string `json:"role"` // positive | negative
	// Root is the directory copied for the build; Src is the source root
	// inside it.
	Root string `json:"root"`
	Src  string `json:"src"`
	// Positive projects.
	Components map[string]ssrGateComponent `json:"components,omitempty"`
	// Negative projects: diagnostics the build must report.
	Diagnostics []ssrGateDiagnostic `json:"diagnostics,omitempty"`
}

type ssrGateComponent struct {
	Status  string            `json:"status"`            // ok | error | client-only
	Error   string            `json:"error,omitempty"`   // diagnostic code of an error status
	Mode    string            `json:"mode,omitempty"`    // interactive | static
	Render  string            `json:"render,omitempty"`  // ok | package-error | error:<code>
	Browser string            `json:"browser,omitempty"` // pass | failure text
	Sites   map[string]string `json:"sites,omitempty"`   // site ID → outcome
}

type ssrGateDiagnostic struct {
	Source string `json:"source"`
	Code   string `json:"code"`
}

func loadSSRGateManifest(t *testing.T) (ssrGateManifest, bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(ssrGateDir, "expected.json"))
	if os.IsNotExist(err) {
		return ssrGateManifest{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	var manifest ssrGateManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest, true
}

// ssrGateOutcomeRank orders site outcomes from best to worst.
var ssrGateOutcomeRank = map[string]int{"server-native": 0, "server-host": 1, "client": 2, "excluded": 2, "unsupported": 3}

// compareSSRGateProject compares the observed project with its expectation.
// It returns the regressions and the other differences (improvements,
// additions) separately.
func compareSSRGateProject(want, got ssrGateProject) (regressions, changes []string) {
	var names []string
	for name := range want.Components {
		names = append(names, name)
	}
	for name := range got.Components {
		if _, ok := want.Components[name]; !ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		w, inWant := want.Components[name]
		g, inGot := got.Components[name]
		where := want.ID + " " + name
		switch {
		case !inGot:
			regressions = append(regressions, where+": component removed from the corpus")
			continue
		case !inWant:
			changes = append(changes, where+": new component without a declared outcome")
			continue
		}
		statusRank := map[string]int{"ok": 0, "client-only": 1, "error": 2}
		if w.Status != g.Status || w.Error != g.Error {
			diff := fmt.Sprintf("%s: status %s%s → %s%s", where, w.Status, ssrGateSuffix(w.Error), g.Status, ssrGateSuffix(g.Error))
			if statusRank[g.Status] > statusRank[w.Status] || (w.Status == g.Status && w.Error != g.Error) {
				regressions = append(regressions, diff)
			} else {
				changes = append(changes, diff)
			}
		}
		if w.Mode != g.Mode {
			diff := fmt.Sprintf("%s: root mode %q → %q", where, w.Mode, g.Mode)
			if w.Mode == "interactive" {
				regressions = append(regressions, diff)
			} else {
				changes = append(changes, diff)
			}
		}
		if w.Render != g.Render {
			diff := fmt.Sprintf("%s: render %q → %q", where, w.Render, g.Render)
			if w.Render == "ok" || (w.Render != "" && g.Render != "ok") {
				regressions = append(regressions, diff)
			} else {
				changes = append(changes, diff)
			}
		}
		if w.Browser != g.Browser {
			diff := fmt.Sprintf("%s: browser %q → %q", where, w.Browser, g.Browser)
			if w.Browser == "pass" || (w.Browser != "" && g.Browser != "pass") {
				regressions = append(regressions, diff)
			} else {
				changes = append(changes, diff)
			}
		}
		var sites []string
		for site := range w.Sites {
			sites = append(sites, site)
		}
		for site := range g.Sites {
			if _, ok := w.Sites[site]; !ok {
				sites = append(sites, site)
			}
		}
		sort.Strings(sites)
		for _, site := range sites {
			wo, inW := w.Sites[site]
			gogot, inG := g.Sites[site]
			switch {
			case !inG:
				regressions = append(regressions, fmt.Sprintf("%s: site %s (%s) removed", where, site, wo))
			case !inW:
				diff := fmt.Sprintf("%s: new site %s (%s)", where, site, gogot)
				if gogot == "unsupported" {
					regressions = append(regressions, diff)
				} else {
					changes = append(changes, diff)
				}
			case wo != gogot:
				diff := fmt.Sprintf("%s: site %s %s → %s", where, site, wo, gogot)
				if ssrGateOutcomeRank[gogot] > ssrGateOutcomeRank[wo] {
					regressions = append(regressions, diff)
				} else {
					changes = append(changes, diff)
				}
			}
		}
	}
	return regressions, changes
}

func ssrGateSuffix(code string) string {
	if code == "" {
		return ""
	}
	return " (" + code + ")"
}

// TestSSRGate runs the gate. It is a required test of the strict browser
// baseline (tools/run-ssr-browser-baseline.mjs), where a skip fails.
func TestSSRGate(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not available")
	}
	if _, err := os.Stat(filepath.Join("node_modules", "playwright")); err != nil {
		t.Skip("playwright not installed; run npm ci")
	}
	manifest, exists := loadSSRGateManifest(t)
	update := os.Getenv("VELA_SSR_GATE_UPDATE") == "1"
	if !exists && !update {
		t.Fatal("testdata/ssr_gate/expected.json is missing")
	}
	if len(manifest.Projects) == 0 && !update {
		t.Fatal("the gate selects no project")
	}
	compiler := filepath.Join(t.TempDir(), "vela")
	buildCLI := exec.Command("go", "build", "-o", compiler, ".")
	// The compiler builds without the workspace, as go install does.
	buildCLI.Env = append(os.Environ(), "GOWORK=off")
	if out, err := buildCLI.CombinedOutput(); err != nil {
		t.Fatalf("build the CLI: %v\n%s", err, out)
	}
	if !exists {
		manifest = ssrGateBootstrap()
	}
	var observed ssrGateManifest
	observed.Version = 1
	var regressions, changes []string
	totals := map[string]int{}
	for _, project := range manifest.Projects {
		var got ssrGateProject
		switch project.Role {
		case "positive":
			got = runSSRGatePositive(t, compiler, node, project)
			r, c := compareSSRGateProject(project, got)
			regressions, changes = append(regressions, r...), append(changes, c...)
			for _, component := range got.Components {
				totals["components"]++
				if component.Browser == "pass" {
					totals["browser-pass"]++
				}
				if component.Status == "ok" {
					totals["status-ok"]++
				}
			}
		case "negative":
			got = runSSRGateNegative(t, compiler, project)
			totals["negative-projects"]++
			if len(got.Diagnostics) != len(project.Diagnostics) {
				// The missing diagnostics were reported by the run.
				regressions = append(regressions, project.ID+": negative project lost its declared diagnostics")
			}
		default:
			t.Fatalf("%s: unknown role %q", project.ID, project.Role)
		}
		observed.Projects = append(observed.Projects, got)
	}
	t.Logf("gate totals: %v", totals)
	if totals["browser-pass"] == 0 {
		t.Fatal("the gate checked no component in the browser")
	}
	if update {
		writeSSRGateManifest(t, observed, regressions, !exists)
		return
	}
	for _, r := range regressions {
		t.Errorf("regression: %s", r)
	}
	for _, c := range changes {
		t.Errorf("undeclared change (record it with VELA_SSR_GATE_UPDATE=1): %s", c)
	}
}

// ssrGateBootstrap lists the corpus of the first run.
func ssrGateBootstrap() ssrGateManifest {
	positive := []struct{ id, root, src string }{
		{"examples", "examples", "."},
		{"ssr-host", "examples/ssr-host", "src"},
		{"qdash", "test_projects/qdash", "src"},
		{"qpage-builder", "test_projects/qpage-builder", "src"},
		{"qwen-dashboard", "test_projects/qwen-dashboard-vela", "src"},
		{"gpt-todo", "test_projects/gpt-todo-vela", "src"},
		{"deepseek-fixed", "test_projects/corpus/deepseek-fixed", "src"},
		{"gpt-fixed", "test_projects/corpus/gpt-fixed", "src"},
		{"qwen-fixed", "test_projects/corpus/qwen-fixed", "src"},
	}
	manifest := ssrGateManifest{Version: 1}
	for _, p := range positive {
		manifest.Projects = append(manifest.Projects, ssrGateProject{ID: p.id, Role: "positive", Root: p.root, Src: p.src, Components: map[string]ssrGateComponent{}})
	}
	manifest.Projects = append(manifest.Projects,
		ssrGateProject{ID: "deepseek-todo", Role: "negative", Root: "test_projects/deepseek-todo-vela", Src: "src", Diagnostics: []ssrGateDiagnostic{
			{"App.vela", "VELA-DERIVED-CALLBACK"}, {"App.vela", "VELA-EVENT-MODIFIER"}, {"App.vela", "VELA-CLASS-OBJECT"}, {"App.vela", "VELA-BIND-MEMBER"},
		}},
		ssrGateProject{ID: "qwen-todo", Role: "negative", Root: "test_projects/qwen-todo-vela", Src: "src", Diagnostics: []ssrGateDiagnostic{
			{"App.vela", "VELA-DYNAMIC-CLASS"}, {"TodoItem.vela", "VELA-BUILTIN-IMPORT"}, {"TodoItem.vela", "VELA-DYNAMIC-CLASS"},
		}},
	)
	return manifest
}

func writeSSRGateManifest(t *testing.T, observed ssrGateManifest, regressions []string, bootstrap bool) {
	t.Helper()
	accept := os.Getenv("VELA_SSR_GATE_ACCEPT")
	if len(regressions) > 0 && accept == "" && !bootstrap {
		for _, r := range regressions {
			t.Errorf("regression: %s", r)
		}
		t.Fatal("expected.json is not updated: regressions need VELA_SSR_GATE_ACCEPT=\"<decision>\"")
	}
	encoded, err := json.MarshalIndent(observed, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ssrGateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ssrGateDir, "expected.json"), append(encoded, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	var entry strings.Builder
	switch {
	case bootstrap:
		entry.WriteString("\n## " + time.Now().Format("2006-01-02") + ": initial expectations\n\nRecorded from the first gate run (task 031).\n")
	case len(regressions) > 0:
		entry.WriteString("\n## " + time.Now().Format("2006-01-02") + ": " + accept + "\n\nAccepted regressions:\n\n")
		for _, r := range regressions {
			entry.WriteString("- " + r + "\n")
		}
	default:
		return
	}
	log, err := os.OpenFile(filepath.Join(ssrGateDir, "DECISIONS.md"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	if _, err := log.WriteString(entry.String()); err != nil {
		t.Fatal(err)
	}
}

// copySSRGateProject copies a project into dir without build outputs and
// dependencies.
func copySSRGateProject(t *testing.T, from, to string) {
	t.Helper()
	skip := map[string]bool{"node_modules": true, "out": true, "dist": true, ".git": true}
	err := filepath.WalkDir(from, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		if d.Type()&os.ModeSymlink != 0 {
			return nil // build outputs linked into the project
		}
		if d.IsDir() {
			if rel != "." && skip[d.Name()] {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(to, rel), 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(to, rel), data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// ssrGateGoMod prepares the Go module of a copied project: its own go.mod
// with the runtime pointed at this repository, or a new module.
func ssrGateGoMod(t *testing.T, root string) (module string) {
	t.Helper()
	compilerRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	runtimeDir := filepath.ToSlash(filepath.Join(compilerRoot, "ssr_runtime"))
	file := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(file)
	if err == nil {
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if strings.HasPrefix(line, "module ") {
				module = strings.TrimSpace(strings.TrimPrefix(line, "module "))
			}
			if strings.HasPrefix(line, "replace "+SSRRuntimeImportPath) {
				lines[i] = "replace " + SSRRuntimeImportPath + " => " + runtimeDir
			}
		}
		if err := os.WriteFile(file, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
			t.Fatal(err)
		}
		return module
	}
	module = "example.com/ssrgate"
	if err := os.WriteFile(file, []byte("module "+module+"\n\ngo 1.25\n\n"+ssrRuntimeRequire(compilerRoot)), 0o644); err != nil {
		t.Fatal(err)
	}
	return module
}

type ssrGateReport struct {
	Components []struct {
		Source string `json:"source"`
		Status string `json:"status"`
		Error  string `json:"error"`
		Sites  []struct {
			ID      string `json:"id"`
			Outcome string `json:"outcome"`
		} `json:"sites"`
	} `json:"components"`
}

func runSSRGatePositive(t *testing.T, compiler, node string, project ssrGateProject) ssrGateProject {
	t.Helper()
	root := filepath.Join(t.TempDir(), "project")
	copySSRGateProject(t, project.Root, root)
	module := ssrGateGoMod(t, root)
	args := []string{"--ssr", "--project", project.Src, "--out", "out", "--ssr-report", "report.json"}
	serverModule := module + "/out/server"
	if _, err := os.Stat(filepath.Join(root, SSRConfigFile)); err != nil {
		args = append(args, "--ssr-module", serverModule)
	} else if config, err := LoadSSRProject(filepath.Join(root, project.Src)); err == nil && config.Config.Module != "" {
		serverModule = config.Config.Module
	}
	cli := exec.Command(compiler, args...)
	cli.Dir = root
	// A project with components that need hosts exits with an error; the
	// report states which, and the gate compares it.
	cliOut, _ := cli.CombinedOutput()
	reportData, err := os.ReadFile(filepath.Join(root, "report.json"))
	if err != nil {
		t.Fatalf("%s: no --ssr-report: %v\n%s", project.ID, err, cliOut)
	}
	var report ssrGateReport
	if err := json.Unmarshal(reportData, &report); err != nil {
		t.Fatal(err)
	}
	var manifest SSRManifest
	if data, err := os.ReadFile(filepath.Join(root, "out", "client", "vela.manifest.json")); err == nil {
		_ = json.Unmarshal(data, &manifest)
	}
	modes := map[string]SSRManifestComponent{}
	for _, c := range manifest.Components {
		modes[c.Source] = c
	}
	got := ssrGateProject{ID: project.ID, Role: project.Role, Root: project.Root, Src: project.Src, Components: map[string]ssrGateComponent{}}
	for _, c := range report.Components {
		component := ssrGateComponent{Status: c.Status, Sites: map[string]string{}}
		if c.Status == "error" {
			component.Error = strings.SplitN(c.Error, ":", 2)[0]
		}
		for _, site := range c.Sites {
			component.Sites[site.ID] = site.Outcome
		}
		if c.Status == "ok" {
			component.Mode = modes[c.Source].Mode
		}
		got.Components[c.Source] = component
	}
	renderSSRGateProject(t, node, root, project.Src, serverModule, got)
	return got
}

// renderSSRGateProject generates and runs the Go server of the project and
// runs the browser checks for every component with status ok.
func renderSSRGateProject(t *testing.T, node, root, src, serverModule string, project ssrGateProject) {
	t.Helper()
	layout := NewSSRProjectLayoutWithModule(filepath.Join(root, "out"), serverModule)
	srcRoot := filepath.Join(root, src)
	env := append(os.Environ(), "GOFLAGS=-mod=mod", "GONOSUMDB=*", "GOWORK=off")
	var sources []string
	for source, c := range project.Components {
		if c.Status == "ok" {
			sources = append(sources, source)
		}
	}
	sort.Strings(sources)
	// A Go package that does not build (it imports a component without a
	// renderer) leaves its components unrendered: render "package-error".
	built := map[string]bool{}
	imports := map[string]string{}
	var renderable []string
	for _, source := range sources {
		file := filepath.Join(srcRoot, filepath.FromSlash(source))
		importPath := layout.ImportPath(file, srcRoot)
		ok, seen := built[importPath]
		if !seen {
			build := exec.Command("go", "build", importPath)
			build.Dir, build.Env = root, env
			_, err := build.CombinedOutput()
			ok = err == nil
			built[importPath] = ok
		}
		c := project.Components[source]
		if !ok {
			c.Render = "package-error"
			project.Components[source] = c
			continue
		}
		if _, ok := imports[importPath]; !ok {
			imports[importPath] = fmt.Sprintf("p%d", len(imports))
		}
		renderable = append(renderable, source)
	}
	if len(renderable) == 0 {
		return
	}
	var importLines, pages strings.Builder
	var importPaths []string
	for p := range imports {
		importPaths = append(importPaths, p)
	}
	sort.Strings(importPaths)
	for _, p := range importPaths {
		fmt.Fprintf(&importLines, "\t%s %q\n", imports[p], p)
	}
	type gateEntry struct {
		ID     string `json:"id"`
		Mode   string `json:"mode"`
		Module string `json:"module"`
		Name   string `json:"name"`
	}
	var entries []gateEntry
	for _, source := range renderable {
		file := filepath.Join(srcRoot, filepath.FromSlash(source))
		alias := imports[layout.ImportPath(file, srcRoot)]
		name := ComponentName(file)
		module := strings.TrimSuffix(filepath.ToSlash(source), ".vela") + ".js"
		fmt.Fprintf(&pages, "\t%q: {%q, %q, func(w io.Writer) error { return %s.Render%s(w, %s.%sProps{Set: map[string]bool{}}, %s.%sSlots{}) }},\n",
			source, module, name, alias, name, alias, name, alias, name)
		entries = append(entries, gateEntry{ID: source, Mode: project.Components[source].Mode, Module: path.Clean(module), Name: name})
	}
	program := ssrGateServerSource(importLines.String(), pages.String())
	if err := os.MkdirAll(filepath.Join(root, "gatecmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "gatecmd", "main.go"), []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "gate-server")
	build := exec.Command("go", "build", "-o", binary, "./gatecmd")
	build.Dir, build.Env = root, env
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%s: build the gate server: %v\n%s", project.ID, err, out)
	}
	server := exec.Command(binary, filepath.Join(root, "out", "client"))
	stdout, err := server.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	server.Stderr = os.Stderr
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Process.Kill(); _ = server.Wait() }()
	addr, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("%s: gate server did not start: %v", project.ID, err)
	}
	go func() { _, _ = io.Copy(io.Discard, stdout) }()
	var list []map[string]string
	for _, e := range entries {
		list = append(list, map[string]string{"id": e.ID, "mode": e.Mode})
	}
	listJSON, _ := json.Marshal(list)
	check := exec.Command(node, "tools/ssr-gate-check.mjs", "http://"+strings.TrimSpace(addr), string(listJSON))
	out, err := check.Output()
	if err != nil {
		t.Fatalf("%s: browser checks: %v\n%s", project.ID, err, out)
	}
	var results []struct {
		ID     string `json:"id"`
		Result string `json:"result"`
	}
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatalf("%s: %v\n%s", project.ID, err, out)
	}
	if len(results) != len(entries) {
		t.Fatalf("%s: %d browser results for %d components", project.ID, len(results), len(entries))
	}
	for _, r := range results {
		c := project.Components[r.ID]
		switch {
		case strings.HasPrefix(r.Result, "render: HTTP 500 RENDER-ERROR: "):
			// The server text names the error; its code is the first token
			// after the position.
			c.Render = "error"
		default:
			c.Render = "ok"
			// The first line names the failure; the rest (stacks, ports)
			// changes between runs.
			c.Browser = ssrGateServerAddr.ReplaceAllString(strings.SplitN(r.Result, "\n", 2)[0], "")
		}
		project.Components[r.ID] = c
	}
	_ = entries
}

func ssrGateServerSource(imports, pages string) string {
	return `// Generated by TestSSRGate (task 031).
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"

` + imports + `)

type page struct {
	module, name string
	render       func(io.Writer) error
}

var pages = map[string]page{
` + pages + `}

const gateJS = ` + "`" + ssrGateBrowserScript + "`" + `

func main() {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	fmt.Println(ln.Addr().String())
	http.Handle("/client/", http.StripPrefix("/client/", http.FileServer(http.Dir(os.Args[1]))))
	http.HandleFunc("/gate.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		io.WriteString(w, gateJS)
	})
	http.HandleFunc("/c/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/c/")
		p, ok := pages[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		var html strings.Builder
		if err := p.render(&html); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, "RENDER-ERROR: "+err.Error())
			return
		}
		config, _ := json.Marshal(map[string]string{"module": p.module, "name": p.name})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, "<!doctype html><html><head><meta charset=\"utf-8\"></head><body><div id=\"ssr\">"+html.String()+
			"</div><div id=\"csr\"></div><script id=\"gate-config\" type=\"application/json\">"+string(config)+
			"</script><script type=\"module\" src=\"/gate.js\"></script></body></html>")
	})
	panic(http.Serve(ln, nil))
}
`
}

// ssrGateBrowserScript hydrates #ssr and mounts #csr (tools/ssr-gate-check.mjs
// reads window.__gate).
const ssrGateBrowserScript = `import { hydrate } from '/client/runtime-hydrate.js';
import { $$debugStats } from '/client/runtime-blocks.js';
const gate = window.__gate = { errors: [], reports: [], stats: $$debugStats };
window.addEventListener('error', (e) => gate.errors.push(String(e.message)));
window.addEventListener('unhandledrejection', (e) => gate.errors.push(String(e.reason)));
const config = JSON.parse(document.getElementById('gate-config').textContent);
const tick = () => new Promise((resolve) => setTimeout(resolve, 0));
try {
  const ssr = document.getElementById('ssr');
  const csr = document.getElementById('csr');
  const module = await import('/client/' + config.module);
  const Component = module[config.name] || module.default;
  gate.baseline = $$debugStats();
  gate.elements = [...ssr.querySelectorAll('*')].filter((el) => !(el.localName === 'script' && el.hasAttribute('data-vela-root')));
  const observer = new MutationObserver(() => {});
  observer.observe(ssr, { subtree: true, childList: true, attributes: true, characterData: true });
  if (ssr.querySelector('script[data-vela-root]')) {
    gate.ssrInstance = hydrate(Component, ssr, { policy: 'fail', onMismatch: (report) => gate.reports.push(String(report.message || JSON.stringify(report))) });
  }
  await tick();
  // Removing the payload script is the only DOM change hydration may make.
  gate.mutations = observer.takeRecords().filter((record) => !(record.type === 'childList' && record.addedNodes.length === 0 &&
    [...record.removedNodes].every((node) => node.nodeType === 1 && node.localName === 'script' && node.hasAttribute('data-vela-root')))).length;
  observer.disconnect();
  gate.csrInstance = Component(csr, {});
  await tick();
} catch (error) {
  gate.errors.push(String(error && error.stack || error));
}
gate.done = true;
`

// runSSRGateNegative builds a negative project and checks that every declared
// diagnostic is reported; it returns the diagnostics found.
func runSSRGateNegative(t *testing.T, compiler string, project ssrGateProject) ssrGateProject {
	t.Helper()
	root := filepath.Join(t.TempDir(), "project")
	copySSRGateProject(t, project.Root, root)
	module := ssrGateGoMod(t, root)
	cli := exec.Command(compiler, "--ssr", "--project", project.Src, "--out", "out", "--ssr-module", module+"/out/server")
	cli.Dir = root
	out, err := cli.CombinedOutput()
	if err == nil {
		t.Errorf("%s: negative project built without errors", project.ID)
	}
	got := project
	got.Diagnostics = nil
	for _, d := range project.Diagnostics {
		found := false
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, d.Source+":") && strings.Contains(line, "["+d.Code+"]") && strings.Contains(line, "[error]") {
				found = true
				break
			}
		}
		if found {
			got.Diagnostics = append(got.Diagnostics, d)
		} else {
			t.Errorf("%s: missing diagnostic %s in %s\n%s", project.ID, d.Code, d.Source, out)
		}
	}
	return got
}

const ssrGateSummaryFile = "docs/SSR_GATE.md"

// ssrGateSummary renders docs/SSR_GATE.md from the expectations: positive
// and negative totals apart, per project, and site outcomes.
func ssrGateSummary(manifest ssrGateManifest) string {
	var b strings.Builder
	b.WriteString("# SSR beta gate\n\n")
	b.WriteString("<!-- Generated from testdata/ssr_gate/expected.json by TestSSRGate; do not edit. -->\n\n")
	b.WriteString("`TestSSRGate` (task 031) builds every project below with the real CLI\n")
	b.WriteString("(`vela --ssr --ssr-report`), compiles and runs the generated Go as an HTTP\n")
	b.WriteString("server, and checks every rendered component in Chromium: hydration without\n")
	b.WriteString("DOM changes or mismatch reports, the hydrated DOM equal to a client-only\n")
	b.WriteString("mount, the same DOM after each click on its buttons and checkboxes, and no\n")
	b.WriteString("owners, listeners or queued work left after `destroy()`. Components are\n")
	b.WriteString("rendered with their default props. Any difference from the recorded\n")
	b.WriteString("outcomes fails CI; a regression can only be recorded with a decision in\n")
	b.WriteString("`testdata/ssr_gate/DECISIONS.md`.\n\n")
	type row struct {
		id                                           string
		components, ok, pass, clientOnly, renderFail int
		errors                                       map[string]int
	}
	var rows []row
	total := row{id: "**total**", errors: map[string]int{}}
	sites := map[string]int{}
	var negatives []ssrGateProject
	for _, p := range manifest.Projects {
		if p.Role == "negative" {
			negatives = append(negatives, p)
			continue
		}
		r := row{id: p.ID, errors: map[string]int{}}
		for _, c := range p.Components {
			r.components++
			switch c.Status {
			case "ok":
				r.ok++
				if c.Browser == "pass" {
					r.pass++
				} else {
					r.renderFail++
				}
			case "client-only":
				r.clientOnly++
			case "error":
				r.errors[c.Error]++
			}
			for _, outcome := range c.Sites {
				sites[outcome]++
			}
		}
		rows = append(rows, r)
		total.components += r.components
		total.ok += r.ok
		total.pass += r.pass
		total.clientOnly += r.clientOnly
		total.renderFail += r.renderFail
		for code, n := range r.errors {
			total.errors[code] += n
		}
	}
	rows = append(rows, total)
	b.WriteString("## Positive corpus\n\n")
	b.WriteString("| Project | Components | Server renderer | Browser checks pass | Not checked in the browser | Client only | SSR diagnostics |\n")
	b.WriteString("| --- | ---: | ---: | ---: | ---: | ---: | --- |\n")
	for _, r := range rows {
		var codes []string
		for code, n := range r.errors {
			codes = append(codes, fmt.Sprintf("%s ×%d", code, n))
		}
		sort.Strings(codes)
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %d | %s |\n", r.id, r.components, r.ok, r.pass, r.renderFail, r.clientOnly, strings.Join(codes, ", "))
	}
	b.WriteString("\nA component is not checked in the browser when its default props make\n")
	b.WriteString("the render throw (as the client would), or when its Go package cannot\n")
	b.WriteString("build. SSR diagnostics are the outcomes of `--ssr-report`; each one names\n")
	b.WriteString("the site and the host or `{#client}` route that would fix it.\n\n")
	b.WriteString("## Site outcomes\n\n| Outcome | Sites |\n| --- | ---: |\n")
	var outcomes []string
	for outcome := range sites {
		outcomes = append(outcomes, outcome)
	}
	sort.Slice(outcomes, func(i, j int) bool {
		return ssrGateOutcomeRank[outcomes[i]] < ssrGateOutcomeRank[outcomes[j]] || (ssrGateOutcomeRank[outcomes[i]] == ssrGateOutcomeRank[outcomes[j]] && outcomes[i] < outcomes[j])
	})
	for _, outcome := range outcomes {
		fmt.Fprintf(&b, "| %s | %d |\n", outcome, sites[outcome])
	}
	b.WriteString("\n## Negative corpus\n\n")
	b.WriteString("These projects use syntax outside the supported template contract. The\n")
	b.WriteString("build must fail with each listed diagnostic; rejecting them does not count\n")
	b.WriteString("as positive coverage.\n\n| Project | File | Diagnostic |\n| --- | --- | --- |\n")
	diagnostics := 0
	for _, p := range negatives {
		for _, d := range p.Diagnostics {
			fmt.Fprintf(&b, "| %s | %s | %s |\n", p.ID, d.Source, d.Code)
			diagnostics++
		}
	}
	fmt.Fprintf(&b, "\nTotals: %d positive components in %d projects (%d checked in the browser); %d negative projects with %d required diagnostics.\n",
		total.components, len(rows)-1, total.pass, len(negatives), diagnostics)
	return b.String()
}

// TestSSRGateSummaryIsCurrent fails when docs/SSR_GATE.md differs from the
// expectations (VELA_SSR_GATE_UPDATE=1 rewrites it).
func TestSSRGateSummaryIsCurrent(t *testing.T) {
	manifest, ok := loadSSRGateManifest(t)
	if !ok {
		t.Fatal("testdata/ssr_gate/expected.json is missing")
	}
	want := ssrGateSummary(manifest)
	if os.Getenv("VELA_SSR_GATE_UPDATE") == "1" {
		if err := os.WriteFile(ssrGateSummaryFile, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(ssrGateSummaryFile)
	if err != nil || string(got) != want {
		t.Fatalf("%s is stale; run VELA_SSR_GATE_UPDATE=1 go test -run TestSSRGateSummaryIsCurrent .", ssrGateSummaryFile)
	}
}
