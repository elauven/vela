// Package dev is `vela-app dev`, the development loop of a Vela App (task
// 078): it regenerates the app sources, runs `vela --watch`, restarts the
// Go server after every good build of vela and every change of the Go
// sources, and runs Vite, each with its prefix on the output.
package dev

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/elauven/vela/app/internal/gen"
)

// readyStamp is the file vela --ssr --watch writes after each build
// without errors (docs/SSR.md, development loop).
const readyStamp = ".vela-ready"

// Options configure Run. A command is a program and its arguments; an
// empty command is not run.
type Options struct {
	// Dir is the project directory.
	Dir string
	// Vela is the compiler in watch mode, ["vela", "--watch"] by default.
	Vela []string
	// Server is the Go server, restarted after every good build:
	// ["go", "run", "./cmd/server", "-dev", "http://localhost:5173"].
	Server []string
	// Vite is the Vite dev server, ["npx", "vite"]; nil does not run it.
	Vite []string
	// Interval is how often files are checked; 300 ms by default.
	Interval time.Duration
	// Stdout receives the prefixed output of every process.
	Stdout io.Writer
}

// Defaults returns the default commands. The compiler is $VELA_BINARY
// when it is set.
func Defaults() Options {
	vela := os.Getenv("VELA_BINARY")
	if vela == "" {
		vela = "vela"
	}
	return Options{
		Vela:   []string{vela, "--watch"},
		Server: []string{"go", "run", "./cmd/server", "-dev", "http://localhost:5173"},
		Vite:   []string{"npx", "vite"},
	}
}

// Run runs the loop until ctx is done or vela or Vite exits. It stops
// every process before it returns.
func Run(ctx context.Context, opts Options) error {
	if opts.Interval <= 0 {
		opts.Interval = 300 * time.Millisecond
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	out := &output{w: opts.Stdout}
	cfg, diags := gen.LoadConfig(opts.Dir)
	if len(diags) > 0 {
		return gen.Error(diags)
	}
	buildOut := velaOut(opts.Dir)
	ready := filepath.Join(opts.Dir, buildOut, readyStamp)
	// A stamp of an earlier run would start the server before vela built.
	os.Remove(ready)

	var wg sync.WaitGroup
	defer wg.Wait()
	wg.Add(1)
	go func() {
		defer wg.Done()
		log := out.prefix("gen")
		gen.Watch(ctx, opts.Dir, opts.Interval, func(result gen.Result, diags []gen.Diagnostic) {
			for _, d := range diags {
				fmt.Fprintln(log, d.String())
			}
			for _, file := range result.Written {
				fmt.Fprintln(log, "wrote "+file)
			}
		})
	}()
	start := func(name string, command []string) {
		if len(command) == 0 {
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := runProcess(ctx, opts.Dir, command, out.prefix(name))
			if ctx.Err() == nil {
				fmt.Fprintf(out.prefix("dev"), "%s exited: %v\n", name, err)
				cancel(fmt.Errorf("%s exited: %v", name, err))
			}
		}()
	}
	start("vela", opts.Vela)
	start("vite", opts.Vite)
	if len(opts.Server) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			serve(ctx, opts, ready, []string{buildOut, cfg.GoPackage, "node_modules", "dist"}, out)
		}()
	}
	<-ctx.Done()
	if err := context.Cause(ctx); err != nil && !errors.Is(err, context.Canceled) {
		cancel(nil)
		wg.Wait()
		return err
	}
	return nil
}

// serve starts the server once vela has built, and restarts it when the
// ready stamp or a Go source outside the generated directories changes.
func serve(ctx context.Context, opts Options, ready string, skip []string, out *output) {
	log := out.prefix("dev")
	last := ""
	var stop func()
	defer func() {
		if stop != nil {
			stop()
		}
	}()
	ticker := time.NewTicker(opts.Interval)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(ready); err == nil {
			if current := sources(opts.Dir, ready, skip); current != last {
				// Wait until the files settle.
				for {
					select {
					case <-ctx.Done():
						return
					case <-time.After(opts.Interval):
					}
					again := sources(opts.Dir, ready, skip)
					if again == current {
						break
					}
					current = again
				}
				if stop != nil {
					fmt.Fprintln(log, "restarting the server")
					stop()
				}
				last = current
				stop = startServer(ctx, opts.Dir, opts.Server, out.prefix("server"), log)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// startServer runs the server and returns a function that stops it and
// waits for it.
func startServer(ctx context.Context, dir string, command []string, w, log io.Writer) func() {
	serverCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		err := runProcess(serverCtx, dir, command, w)
		if serverCtx.Err() == nil {
			// The next change starts it again.
			fmt.Fprintf(log, "the server exited: %v\n", err)
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// sources fingerprints the ready stamp and the Go sources of the project.
func sources(dir, ready string, skip []string) string {
	h := sha256.New()
	stamp := func(file string) {
		if info, err := os.Stat(file); err == nil {
			fmt.Fprintf(h, "%s %d %d\n", file, info.Size(), info.ModTime().UnixNano())
		}
	}
	stamp(ready)
	stamp(filepath.Join(dir, "go.mod"))
	_ = filepath.WalkDir(dir, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, file)
		if entry.IsDir() {
			for _, s := range skip {
				if s != "" && filepath.ToSlash(rel) == strings.TrimSuffix(filepath.ToSlash(s), "/") {
					return filepath.SkipDir
				}
			}
			if strings.HasPrefix(entry.Name(), ".") && rel != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(file, ".go") {
			stamp(file)
		}
		return nil
	})
	return fmt.Sprintf("%x", h.Sum(nil))
}

// velaOut is the output directory of vela.config.json, "out" by default.
func velaOut(dir string) string {
	var cfg struct {
		Out string `json:"out"`
	}
	if data, err := os.ReadFile(filepath.Join(dir, "vela.config.json")); err == nil {
		_ = json.Unmarshal(data, &cfg)
	}
	if cfg.Out == "" {
		return "out"
	}
	return filepath.FromSlash(cfg.Out)
}

// runProcess runs a command in its own process group, so that stopping
// it also stops its children (go run starts the server as a child), and
// copies its output to w.
func runProcess(ctx context.Context, dir string, command []string, w io.Writer) error {
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = dir
	cmd.Stdout = w
	cmd.Stderr = w
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		return err
	case <-ctx.Done():
		stopProcess(cmd)
		select {
		case err := <-exited:
			return err
		case <-time.After(5 * time.Second):
			killProcess(cmd)
			return <-exited
		}
	}
}

// output writes whole lines of every process with their prefix.
type output struct {
	mu sync.Mutex
	w  io.Writer
}

func (o *output) prefix(name string) io.Writer {
	r, w := io.Pipe()
	go func() {
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 64<<10), 1<<20)
		for scanner.Scan() {
			o.mu.Lock()
			fmt.Fprintf(o.w, "[%s] %s\n", name, scanner.Text())
			o.mu.Unlock()
		}
		io.Copy(io.Discard, r)
	}()
	return w
}
