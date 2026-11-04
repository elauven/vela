package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elauven/vela/app/internal/dev"
	"github.com/elauven/vela/app/internal/testkit"
)

// syncBuffer collects the output of the dev loop.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// TestDevLoop is the acceptance check of vela-app dev (task 078) without
// Vite: it starts examples/app, serves it, and serves the change after an
// edit of a component and after an edit of the Go sources. Ctrl-C (the
// context) stops every process.
func TestDevLoop(t *testing.T) {
	testkit.RequireGo(t)
	root := testkit.CopyProject(t, filepath.Join(testkit.RepoRoot(t), "examples", "app"))
	testkit.UseLocalModules(t, root)
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "-mod=mod")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()

	var out syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		finished <- dev.Run(ctx, dev.Options{
			Dir:      root,
			Vela:     []string{testkit.VelaBinary(t), "--watch"},
			Server:   []string{"go", "run", "./cmd/server", "-addr", addr, "-dist", "none"},
			Interval: 100 * time.Millisecond,
			Stdout:   &out,
		})
	}()
	page := func() string {
		resp, err := http.Get("http://" + addr + "/docs/intro")
		if err != nil {
			return ""
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return string(body)
	}
	waitFor := func(what, want string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Minute)
		for time.Now().Before(deadline) {
			select {
			case err := <-finished:
				t.Fatalf("dev stopped while waiting for %s: %v\n%s", what, err, out.String())
			default:
			}
			if strings.Contains(page(), want) {
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatalf("%s: the page never had %q\n%s", what, want, out.String())
	}
	waitFor("the first start", "<h1>Introduction</h1>")

	component := filepath.Join(root, "src", "routes", "docs", "[...path]", "Page.vela")
	edit(t, component, "<h1>{data.title}</h1>", `<h1>{data.title}</h1><p class="edited">edited</p>`)
	waitFor("a component edit", `<p class="edited">edited</p>`)

	edit(t, filepath.Join(root, "server", "store", "store.go"), `{Title: "Introduction", Paragraphs`, `{Title: "Intro, edited", Paragraphs`)
	waitFor("a Go edit", "<h1>Intro, edited</h1>")

	cancel()
	select {
	case err := <-finished:
		if err != nil {
			t.Errorf("dev: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("dev did not stop\n%s", out.String())
	}
	if page() != "" {
		t.Errorf("the server still runs after dev stopped")
	}
	for _, prefix := range []string{"[vela] ", "[gen] ", "[dev] restarting the server"} {
		if !strings.Contains(out.String(), prefix) {
			t.Errorf("the output has no %q:\n%s", prefix, out.String())
		}
	}
}

func edit(t *testing.T, file, from, to string) {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(from)) {
		t.Fatalf("%s has no %q", file, from)
	}
	if err := os.WriteFile(file, bytes.Replace(data, []byte(from), []byte(to), 1), 0o644); err != nil {
		t.Fatal(fmt.Errorf("edit: %w", err))
	}
}

// TestBuild is the acceptance check of vela-app build without Vite: the
// build directory holds the server and the vela manifest, and the server
// started in that directory, away from the project, serves the app.
func TestBuild(t *testing.T) {
	testkit.RequireGo(t)
	root := testkit.CopyProject(t, filepath.Join(testkit.RepoRoot(t), "examples", "app"))
	testkit.UseLocalModules(t, root)
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "-mod=mod")

	var out syncBuffer
	output := filepath.Join(t.TempDir(), "site")
	opts := dev.BuildDefaults()
	opts.Dir, opts.Stdout, opts.Output = root, &out, output
	opts.Vela, opts.Vite = []string{testkit.VelaBinary(t)}, nil
	if err := dev.Build(context.Background(), opts); err != nil {
		t.Fatalf("build: %v\n%s", err, out.String())
	}
	server := filepath.Join(output, "server")
	if _, err := os.Stat(server + exeSuffix()); err != nil {
		t.Fatalf("no server binary: %v\n%s", err, out.String())
	}
	if _, err := os.Stat(filepath.Join(output, "out", "client", "vela.manifest.json")); err != nil {
		t.Fatalf("no vela manifest in the build: %v", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	cmd := exec.Command(server+exeSuffix(), "-addr", addr, "-dist", "none")
	cmd.Dir = output
	var logs syncBuffer
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			cmd.Process.Kill()
			cmd.Wait()
		}
	}
	defer stop()
	deadline := time.Now().Add(30 * time.Second)
	for {
		resp, err := http.Get("http://" + addr + "/docs/intro")
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if !strings.Contains(string(body), "<h1>Introduction</h1>") {
				t.Fatalf("the built server serves:\n%s", body)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the built server does not answer: %v\n%s", err, logs.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Windows cannot remove the binary of a running process.
	stop()

	// A second build replaces the directory; a directory of the developer
	// is never replaced.
	if err := dev.Build(context.Background(), opts); err != nil {
		t.Fatalf("second build: %v", err)
	}
	foreign := t.TempDir()
	if err := os.WriteFile(filepath.Join(foreign, "keep.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	opts.Output = foreign
	if err := dev.Build(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "not written by vela-app build") {
		t.Fatalf("build into a foreign directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(foreign, "keep.txt")); err != nil {
		t.Fatalf("the foreign directory lost its file: %v", err)
	}
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}
