package dev

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/elauven/vela/app/internal/gen"
)

// buildMarker marks a directory that Build wrote. Build replaces only a
// directory that has it, so -o never deletes a directory of the developer.
const buildMarker = ".vela-app-build"

// BuildOptions configure Build. A command is a program and its arguments;
// an empty command is not run.
type BuildOptions struct {
	// Dir is the project directory.
	Dir string
	// Vela is the compiler, ["vela"] by default ($VELA_BINARY when set).
	Vela []string
	// Vite builds the client into dist/: ["npx", "vite", "build"].
	Vite []string
	// Server is the Go package of the server, "./cmd/server"; "" builds
	// no server.
	Server string
	// Output is the directory of the build, relative to Dir: "build".
	Output string
	// Stdout receives the output of every step.
	Stdout io.Writer
}

// BuildDefaults returns the default build.
func BuildDefaults() BuildOptions {
	vela := os.Getenv("VELA_BINARY")
	if vela == "" {
		vela = "vela"
	}
	return BuildOptions{
		Vela:   []string{vela},
		Vite:   []string{"npx", "vite", "build"},
		Server: "./cmd/server",
		Output: "build",
	}
}

// Build is the production build of a Vela App: it generates the app
// sources, compiles them with vela, builds the client with Vite and the
// Go server with go build, and puts what the server needs at run time into
// Output with the layout of the project:
//
//	build/server[.exe]                  the server binary
//	build/dist/                         the client of vite build
//	build/out/client/vela.manifest.json the root modes of vela (R-9)
//
// The server runs in Output as it runs in the project directory, so
// Output is the directory to deploy.
func Build(ctx context.Context, opts BuildOptions) error {
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Output == "" {
		opts.Output = "build"
	}
	log := func(format string, args ...any) { fmt.Fprintf(opts.Stdout, "[build] "+format+"\n", args...) }
	output := opts.Output
	if !filepath.IsAbs(output) {
		output = filepath.Join(opts.Dir, output)
	}
	if err := checkOutput(opts.Dir, output); err != nil {
		return err
	}

	log("vela-app gen")
	result, diags := gen.Generate(opts.Dir)
	if len(diags) > 0 {
		for _, d := range diags {
			fmt.Fprintln(opts.Stdout, d.String())
		}
		return gen.Error(diags)
	}
	for _, file := range result.Written {
		log("wrote %s", file)
	}
	step := func(name string, command []string) error {
		if len(command) == 0 {
			return nil
		}
		log("%s", strings.Join(command, " "))
		if err := runProcess(ctx, opts.Dir, command, opts.Stdout); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		return nil
	}
	if err := step("vela", opts.Vela); err != nil {
		return err
	}
	if err := step("vite", opts.Vite); err != nil {
		return err
	}

	if err := os.RemoveAll(output); err != nil {
		// Windows cannot remove the binary of a running process.
		return fmt.Errorf("cannot replace the build directory (is its server still running?): %w", err)
	}
	if err := os.MkdirAll(output, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(output, buildMarker), []byte("Written by vela-app build; replaced by the next build.\n"), 0o644); err != nil {
		return err
	}
	if opts.Server != "" {
		binary := filepath.Join(output, "server"+exeSuffix())
		if err := step("go build", []string{"go", "build", "-trimpath", "-o", binary, opts.Server}); err != nil {
			return err
		}
	}
	if err := copyIfExists(filepath.Join(opts.Dir, "dist"), filepath.Join(output, "dist")); err != nil {
		return err
	}
	manifest := filepath.Join(velaOut(opts.Dir), "client", "vela.manifest.json")
	if err := copyIfExists(filepath.Join(opts.Dir, manifest), filepath.Join(output, manifest)); err != nil {
		return err
	}
	rel, err := filepath.Rel(opts.Dir, output)
	if err != nil || strings.HasPrefix(rel, "..") {
		rel = output
	}
	log("done: deploy %s and run its server there", rel)
	return nil
}

// checkOutput refuses an output directory that Build did not write, and
// one that would contain the project.
func checkOutput(dir, output string) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	absOut, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	if rel, err := filepath.Rel(absOut, absDir); err == nil && !strings.HasPrefix(rel, "..") {
		return fmt.Errorf("the output directory %s contains the project", output)
	}
	info, err := os.Stat(output)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("the output %s is not a directory", output)
	}
	entries, err := os.ReadDir(output)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	if _, err := os.Stat(filepath.Join(output, buildMarker)); err != nil {
		return fmt.Errorf("the output directory %s was not written by vela-app build (no %s); remove it or choose another with -o", output, buildMarker)
	}
	return nil
}

// exeSuffix is the suffix of executables for the GOOS go build targets.
func exeSuffix() string {
	goos := os.Getenv("GOOS")
	if goos == "" {
		goos = runtime.GOOS
	}
	if goos == "windows" {
		return ".exe"
	}
	return ""
}

// copyIfExists copies the file or directory tree from to to; a missing
// source copies nothing.
func copyIfExists(from, to string) error {
	info, err := os.Stat(from)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return copyFile(from, to, info.Mode())
	}
	return filepath.WalkDir(from, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, file)
		if err != nil {
			return err
		}
		target := filepath.Join(to, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return copyFile(file, target, info.Mode())
	})
}

func copyFile(from, to string, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
