// Package e2e runs examples/app end to end: vela-app gen, vela --ssr, the
// Go server, and with node_modules installed, vite build and Chromium.
package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/elauven/vela/app/internal/gen"
	"github.com/elauven/vela/app/internal/testkit"
)

// pageCase is one route of the example with what the server must answer.
type pageCase struct {
	Path     string `json:"path"`
	Status   int    `json:"status"`
	Title    string `json:"title,omitempty"`
	Location string `json:"location,omitempty"`
	// Contains is a part of the rendered body.
	Contains string `json:"contains,omitempty"`
	// Preload is the source of a lazy page whose chunk the document
	// preloads (production build only).
	Preload string `json:"preload,omitempty"`
	// Click is a selector to click after hydration and Then the text the
	// page shows afterwards, proving that the page is interactive.
	Click string `json:"click,omitempty"`
	Then  string `json:"then,omitempty"`
}

var exampleCases = []pageCase{
	{Path: "/", Status: 200, Title: "Home · Vela App", Contains: "Welcome to Vela App.", Preload: "src/routes/Page.vela"},
	{Path: "/users", Status: 200, Title: "Users", Contains: "Grace</a>", Preload: "src/routes/users/Page.vela"},
	{Path: "/users/2", Status: 200, Title: "Grace · Users", Contains: "User #2, joined 2026-03-17", Preload: "src/routes/users/[id=int]/Page.vela"},
	{Path: "/about", Status: 200, Title: "Vela App", Contains: "About</h1>"},
	{Path: "/de/about", Status: 200, Title: "Vela App", Contains: "Über uns</h1>"},
	{Path: "/colors/00ff00", Status: 200, Title: "Vela App", Contains: "#00ff00"},
	{Path: "/settings", Status: 200, Title: "Vela App", Contains: "Admin</p>", Preload: "src/routes/(admin)/settings/Page.vela", Click: "main button", Then: "Saved"},
	{Path: "/users/9", Status: 404, Title: "Vela App", Contains: "<h1>404</h1>", Preload: "src/routes/Error.vela"},
	{Path: "/nope", Status: 404, Title: "Vela App", Contains: "<h1>404</h1>"},
	{Path: "/crash", Status: 500, Title: "Vela App", Contains: "Internal Error"},
	{Path: "/me", Status: 303, Location: "/users/1"},
	{Path: "/users/", Status: 308, Location: "/users"},
}

// buildExample generates, compiles and builds examples/app and its server.
func buildExample(t *testing.T) (root, server string) {
	t.Helper()
	testkit.RequireGo(t)
	root = testkit.CopyProject(t, filepath.Join(testkit.RepoRoot(t), "examples", "app"))
	if _, diags := gen.Generate(root); len(diags) > 0 {
		t.Fatal(gen.Error(diags))
	}
	testkit.CompileApp(t, root)
	server = filepath.Join(root, "app-server")
	if goruntime.GOOS == "windows" {
		server += ".exe"
	}
	testkit.Run(t, root, "go", "build", "-o", server, "./cmd/server")
	return root, server
}

// startServer starts the example server and returns its URL.
func startServer(t *testing.T, root, server string, args ...string) string {
	t.Helper()
	cmd := exec.Command(server, append([]string{"-addr", "127.0.0.1:0"}, args...)...)
	cmd.Dir = root
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	line := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			line <- scanner.Text()
		}
		io.Copy(io.Discard, stdout)
	}()
	select {
	case url := <-line:
		return url
	case <-time.After(30 * time.Second):
		t.Fatalf("the server did not start: %s", stderr.String())
	}
	return ""
}

var titleTag = regexp.MustCompile(`<title>([^<]*)</title>`)

// TestExampleAppServes is the acceptance check of tasks 074 and 075 without
// a browser: every route of examples/app answers with its status, title
// and content.
func TestExampleAppServes(t *testing.T) {
	root, server := buildExample(t)
	url := startServer(t, root, server, "-dist", "none")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, c := range exampleCases {
		resp, err := client.Get(url + c.Path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != c.Status {
			t.Errorf("%s: status %d, want %d", c.Path, resp.StatusCode, c.Status)
		}
		if c.Location != "" && resp.Header.Get("Location") != c.Location {
			t.Errorf("%s: Location %q, want %q", c.Path, resp.Header.Get("Location"), c.Location)
		}
		if c.Title != "" {
			if m := titleTag.FindSubmatch(body); m == nil || string(m[1]) != c.Title {
				t.Errorf("%s: title %q, want %q", c.Path, m, c.Title)
			}
		}
		if c.Contains != "" && !strings.Contains(string(body), c.Contains) {
			t.Errorf("%s: the body misses %q:\n%s", c.Path, c.Contains, body)
		}
	}
}

// browserResult is the output of testdata/e2e.mjs.
type browserResult struct {
	Pages []struct {
		Path      string   `json:"path"`
		Status    int      `json:"status"`
		Title     string   `json:"title"`
		Location  string   `json:"location"`
		Hydrated  bool     `json:"hydrated"`
		SameDOM   bool     `json:"sameDOM"`
		Removed   []string `json:"removed"`
		Preloaded []string `json:"preloaded"`
		After     string   `json:"after"`
		Logs      []string `json:"logs"`
		Diff      string   `json:"diff"`
	} `json:"pages"`
	Navigation []struct {
		Name  string `json:"name"`
		Error string `json:"error"`
	} `json:"navigation"`
	Error string `json:"error"`
}

// TestExampleAppHydrates is the browser check of tasks 075 and 076: vite
// build of examples/app, the Go server with the build's tags, and Chromium.
// Every document hydrates without a mismatch report and without changing
// the server DOM, preloads the chunk of its lazy page, and is interactive.
// Then the router navigates: kept layouts keep their DOM and state and
// their loaders do not run, back and forward restore scroll, redirects and
// 404s stay inside the app, and without JavaScript links load documents.
// It needs the repository's node_modules (npm ci and playwright install);
// VELA_REQUIRE_APP_E2E=1 turns the skip into a failure.
func TestExampleAppHydrates(t *testing.T) {
	repo := testkit.RepoRoot(t)
	node, err := exec.LookPath("node")
	skip := func(format string, args ...any) {
		if os.Getenv("VELA_REQUIRE_APP_E2E") != "" {
			t.Fatalf(format, args...)
		}
		t.Skipf(format, args...)
	}
	if err != nil {
		skip("node not available")
	}
	for _, dep := range []string{"vite", "unplugin", "playwright"} {
		if _, err := os.Stat(filepath.Join(repo, "node_modules", dep, "package.json")); err != nil {
			skip("%s not installed; run npm ci", dep)
		}
	}
	root, server := buildExample(t)
	installPackages(t, repo, root)
	cases, _ := json.Marshal(exampleCases)
	script := filepath.Join(repo, "app", "testdata", "e2e.mjs")
	cmd := exec.Command(node, script, root, server, string(cases))
	cmd.Env = append(os.Environ(), "VELA_BINARY="+testkit.VelaBinary(t))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s%s", err, out, stderr.String())
	}
	var result browserResult
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	if len(result.Pages) != len(exampleCases) {
		t.Fatalf("%d pages checked, want %d", len(result.Pages), len(exampleCases))
	}
	for i, p := range result.Pages {
		c := exampleCases[i]
		if p.Status != c.Status || (c.Location != "" && p.Location != c.Location) {
			t.Errorf("%s: %d %q, want %d %q", c.Path, p.Status, p.Location, c.Status, c.Location)
		}
		if c.Location != "" {
			continue
		}
		if p.Title != c.Title {
			t.Errorf("%s: title %q, want %q", c.Path, p.Title, c.Title)
		}
		if !p.Hydrated || !p.SameDOM || len(p.Removed) > 0 || len(p.Logs) > 0 {
			t.Errorf("%s: hydrated %v, same DOM %v, removed %v, logs %v\n%s", c.Path, p.Hydrated, p.SameDOM, p.Removed, p.Logs, p.Diff)
		}
		if c.Preload != "" && !contains(p.Preloaded, c.Preload) {
			t.Errorf("%s: %s is not preloaded: %v", c.Path, c.Preload, p.Preloaded)
		}
		if c.Click != "" && p.After != c.Then {
			t.Errorf("%s: after clicking %s the page shows %q, want %q", c.Path, c.Click, p.After, c.Then)
		}
	}
	if len(result.Navigation) == 0 {
		t.Error("no navigation scenario ran")
	}
	for _, n := range result.Navigation {
		if n.Error != "" {
			t.Errorf("navigation: %s: %s", n.Name, n.Error)
		}
	}
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

// installPackages puts the packages of the repository into the project's
// node_modules, as npm install of examples/app/package.json would.
func installPackages(t *testing.T, repo, root string) {
	t.Helper()
	modules := filepath.Join(root, "node_modules")
	for name, dir := range map[string]string{
		"@govela/app":      "packages/app",
		"@govela/runtime":  "packages/runtime",
		"@govela/compiler": "packages/compiler",
		"@govela/unplugin": "packages/unplugin",
	} {
		copyDir(t, filepath.Join(repo, filepath.FromSlash(dir)), filepath.Join(modules, filepath.FromSlash(name)))
	}
	// The dependencies of those packages and vite come from the
	// repository's node_modules.
	for _, dep := range []string{"unplugin", "vite"} {
		if err := os.Symlink(filepath.Join(repo, "node_modules", dep), filepath.Join(modules, dep)); err != nil {
			t.Skipf("symlink %s: %v", dep, err)
		}
	}
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(file string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, file)
		if info.IsDir() {
			if info.Name() == "node_modules" || info.Name() == "bin" {
				return filepath.SkipDir
			}
			return nil
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		testkit.WriteFile(t, filepath.Join(dst, rel), string(data))
		return nil
	})
	if err != nil {
		t.Fatal(fmt.Errorf("copy %s: %w", src, err))
	}
}
