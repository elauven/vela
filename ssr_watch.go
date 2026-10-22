package main

// ssr_watch.go — `vela --ssr --watch` (task 064). A change of any input of
// the SSR build rebuilds the whole project: the analysis links components
// across packages, translates the modules they import and binds hosts, and
// a full build of a 27-component project takes about 0.1 s, so the rebuild
// is a clean build in place. After every successful build the stamp
// SSRReadyStamp signals that the Go server can restart.

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SSRReadyStamp is written to the output root after every successful build
// of `vela --ssr --watch`; it holds the start time of that build. A process
// watcher restarts the Go server on it (docs/SSR.md).
const SSRReadyStamp = ".vela-ready"

type inputStamp struct {
	modTime time.Time
	size    int64
}

// ssrWatchInputs are the files an SSR build reads: the components and local
// modules under srcRoot, vela.ssr.json in srcRoot or its parent, and the Go
// files of the host tree.
func ssrWatchInputs(srcRoot string) map[string]inputStamp {
	files := append(CollectVela(srcRoot), CollectJSModules(srcRoot)...)
	for _, dir := range []string{srcRoot, filepath.Dir(filepath.Clean(srcRoot))} {
		files = append(files, filepath.Join(dir, SSRConfigFile))
	}
	if project, err := LoadSSRProject(srcRoot); err == nil {
		_ = filepath.WalkDir(project.HostDir, func(path string, entry fs.DirEntry, err error) error {
			if err == nil && !entry.IsDir() && strings.HasSuffix(path, ".go") {
				files = append(files, path)
			}
			return nil
		})
	}
	inputs := map[string]inputStamp{}
	for _, file := range files {
		if info, err := os.Stat(file); err == nil && !info.IsDir() {
			inputs[file] = inputStamp{info.ModTime(), info.Size()}
		}
	}
	return inputs
}

// ssrBuildOnce builds the project and, without errors, writes the stamp.
func ssrBuildOnce(srcRoot, outRoot string, opts CompileOptions, parallel bool) (files []string, errors int) {
	start := time.Now()
	files = CollectVela(srcRoot)
	if len(files) == 0 {
		fmt.Fprintln(compilerStatusWriter(opts), "  no .vela files found")
		return nil, 0
	}
	errors = buildSSRProject(files, srcRoot, outRoot, opts, parallel)
	status := compilerStatusWriter(opts)
	if errors > 0 {
		fmt.Fprintf(status, "\n  %d error(s); the server keeps the previous build\n", errors)
		return files, errors
	}
	if err := os.WriteFile(filepath.Join(outRoot, SSRReadyStamp), []byte(start.UTC().Format(time.RFC3339Nano)+"\n"), 0o644); err != nil {
		fmt.Fprintf(errorWriter(opts), "  x  %s: %v\n", SSRReadyStamp, err)
		return files, 1
	}
	fmt.Fprintf(status, "\n  %d SSR component(s) ready in %s ✓\n", len(files), time.Since(start).Round(time.Millisecond))
	return files, 0
}

// removeSSROutputs deletes what the build wrote for a removed component:
// its client module, renderer, styles and props sidecar, unless the
// developer has edited the sidecar.
func removeSSROutputs(src, srcRoot, outRoot string, opts CompileOptions) {
	layout := NewSSRProjectLayoutWithModule(outRoot, SSRDefaultModule)
	client := MirrorPath(src, srcRoot, layout.ClientDir, ".js")
	for _, file := range []string{client, client + ".map", strings.TrimSuffix(client, ".js") + ".css", layout.ServerPath(src, srcRoot), layout.StylePath(src, srcRoot)} {
		_ = os.Remove(file)
	}
	status := compilerStatusWriter(opts)
	props := layout.PropsPath(src, srcRoot)
	if data, err := os.ReadFile(props); err == nil {
		if !SSRPropsGenerated(data) {
			fmt.Fprintf(status, "  −  %s (removed; the edited %s is yours to delete)\n", RelOrBase(src, srcRoot), props)
			return
		}
		_ = os.Remove(props)
	}
	fmt.Fprintf(status, "  −  %s (removed)\n", RelOrBase(src, srcRoot))
}

// watchSSRProject rebuilds after every change of the inputs, polling every
// interval, until done is closed.
func watchSSRProject(srcRoot, outRoot string, opts CompileOptions, parallel bool, interval time.Duration, done <-chan struct{}) {
	status := compilerStatusWriter(opts)
	inputs := ssrWatchInputs(srcRoot)
	built, _ := ssrBuildOnce(srcRoot, outRoot, opts, parallel)
	fmt.Fprintln(status, "\nWatching for changes… (Ctrl-C to stop)")
	for {
		select {
		case <-done:
			return
		case <-time.After(interval):
		}
		current := ssrWatchInputs(srcRoot)
		var changed []string
		for file, stamp := range current {
			if inputs[file] != stamp {
				changed = append(changed, file)
			}
		}
		for file := range inputs {
			if _, ok := current[file]; !ok {
				changed = append(changed, file)
			}
		}
		if len(changed) == 0 {
			continue
		}
		inputs = current
		sort.Strings(changed)
		fmt.Fprintf(status, "\n  ↻  %s\n", strings.Join(relativeAll(changed, srcRoot), ", "))
		files, _ := ssrBuildOnce(srcRoot, outRoot, opts, parallel)
		kept := map[string]bool{}
		for _, file := range files {
			kept[file] = true
		}
		for _, file := range built {
			if !kept[file] {
				removeSSROutputs(file, srcRoot, outRoot, opts)
			}
		}
		built = files
	}
}

func relativeAll(files []string, root string) []string {
	out := make([]string, len(files))
	for i, file := range files {
		out[i] = RelOrBase(file, root)
	}
	return out
}
