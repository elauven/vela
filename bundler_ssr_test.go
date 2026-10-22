package main

import (
	"github.com/elauven/vela/internal/project"

	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
)

type ssrViteRun struct {
	HTML          string `json:"html"`
	Before, After *struct {
		Counter      string `json:"counter"`
		Badge        string `json:"badge"`
		CounterColor string `json:"counterColor"`
		BadgeColor   string `json:"badgeColor"`
	}
	Removed []string `json:"removed"`
	Logs    []string `json:"logs"`
	Error   string   `json:"error"`
}

// TestSSRWithVite is the server-rendered application of docs/SSR.md with
// Vite (task 064): `vela` builds the Go renderers from vela.config.json, a
// Go server renders the page with the tags of ssr_runtime/vite, and the
// client that @govela/unplugin compiles hydrates it in Chromium, from a
// `vite build` manifest and from the Vite dev server. The scoped CSS of the
// bundle applies to the server HTML (the server and the plugin give the
// same scope classes), the client adopts the server DOM without mismatch
// reports, and the page updates.
func TestSSRWithVite(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go tool not available")
	}
	a := newBundlerApp(t, "ssr")
	compilerRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(a.root, "go.mod"), "module example.com/site\n\ngo 1.25\n\n"+ssrRuntimeRequire(compilerRoot))
	sums, err := os.ReadFile(filepath.Join("ssr_runtime", "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(a.root, "go.sum"), string(sums))

	// The build of a project directory, configured by vela.config.json.
	build := exec.Command(a.binary)
	build.Dir = a.root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("vela --ssr: %v\n%s", err, out)
	}
	server := filepath.Join(a.root, "server")
	if goruntime.GOOS == "windows" {
		server += ".exe"
	}
	goBuild := exec.Command("go", "build", "-o", server, "./cmd/server")
	goBuild.Dir = a.root
	goBuild.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod")
	if out, err := goBuild.CombinedOutput(); err != nil {
		t.Fatalf("go build of the server: %v\n%s", err, out)
	}

	for _, mode := range []string{"build", "dev"} {
		var r ssrViteRun
		a.script("ssr-vite-check.mjs", &r, mode, server)
		if r.Error != "" {
			t.Fatalf("%s: %s\n%v", mode, r.Error, r.Logs)
		}
		for _, want := range []string{`class="inc">7</button>`, `class="badge"`, project.ScopeID("src/App.vela"), project.ScopeID("src/widgets/Badge.vela")} {
			if !strings.Contains(r.HTML, want) {
				t.Errorf("%s: the server HTML misses %s:\n%s", mode, want, r.HTML)
			}
		}
		if mode == "build" && (!strings.Contains(r.HTML, `<link rel="stylesheet" href="/assets/`) || !strings.Contains(r.HTML, `<script type="module" src="/assets/`)) {
			t.Errorf("build: the page does not load the hashed files:\n%s", r.HTML)
		}
		if mode == "dev" && !strings.Contains(r.HTML, "/@vite/client") {
			t.Errorf("dev: the page does not load the Vite client:\n%s", r.HTML)
		}
		if r.Before == nil || r.Before.Counter != "7" || r.Before.Badge != "b7" || r.Before.CounterColor != "rgb(1, 2, 3)" || r.Before.BadgeColor != "rgb(4, 5, 6)" {
			t.Errorf("%s: hydrated page %+v", mode, r.Before)
		}
		if r.After == nil || r.After.Counter != "8" || r.After.Badge != "b8" {
			t.Errorf("%s: after a click %+v", mode, r.After)
		}
		if len(r.Removed) != 0 || len(r.Logs) != 0 {
			t.Errorf("%s: the client replaced %v of the server HTML; logs %v", mode, r.Removed, r.Logs)
		}
	}
}
