package main

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// readTree returns the files under root by slash path, without skip.
func readTree(t *testing.T, root string, skip map[string]bool) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if rel = filepath.ToSlash(rel); !skip[rel] {
			data, err := os.ReadFile(path)
			files[rel] = string(data)
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// waitForBuild waits until the stamp names a build that started after since.
func waitForBuild(t *testing.T, stamp string, since time.Time, log *bytes.Buffer) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(stamp); err == nil {
			if start, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(data))); err == nil && start.After(since) {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no build after %s\n%s", since, log.String())
}

// TestSSRWatchRebuildsLikeACleanBuild changes (with a new prop), adds and
// removes components during `vela --ssr --watch`: the server and client
// outputs equal a clean build of the changed project, apart from the stamp.
func TestSSRWatchRebuildsLikeACleanBuild(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "bundler", "ssr", "src"), filepath.Join(root, "src"))
	src, out := filepath.Join(root, "src"), filepath.Join(root, "out")
	writeTestFile(t, filepath.Join(src, "Gone.vela"), "<template><p class=\"gone\">gone</p></template>\n")

	var log bytes.Buffer
	opts := CompileOptions{StatusWriter: &log, ErrWriter: &log, SSRModule: SSRDefaultModule}
	done := make(chan struct{})
	stopped := make(chan struct{})
	start := time.Now()
	go func() {
		watchSSRProject(src, out, opts, false, 20*time.Millisecond, done)
		close(stopped)
	}()
	defer func() {
		close(done)
		<-stopped
	}()
	stamp := filepath.Join(out, SSRReadyStamp)
	waitForBuild(t, stamp, start, &log)
	if _, err := os.Stat(filepath.Join(out, "server", "components", "gone.go")); err != nil {
		t.Fatalf("initial build: %v\n%s", err, log.String())
	}

	edited := time.Now()
	badge := filepath.Join(src, "widgets", "Badge.vela")
	data, err := os.ReadFile(badge)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.NewReplacer("rgb(4, 5, 6)", "rgb(7, 8, 9)", `class="badge"`, `class="badge" :title={hint}`, "export let label = '';", "export let label = '';\n  export let hint = 'edited';").Replace(string(data))
	writeTestFile(t, badge, changed)
	writeTestFile(t, filepath.Join(src, "widgets", "Added.vela"), "<script>\n  export let n = 1;\n</script>\n<template><b>{n}</b></template>\n")
	if err := os.Remove(filepath.Join(src, "Gone.vela")); err != nil {
		t.Fatal(err)
	}
	// Filesystems with coarse timestamps: the edit must look newer.
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(badge, later, later); err != nil {
		t.Fatal(err)
	}
	waitForBuild(t, stamp, edited, &log)

	clean := filepath.Join(t.TempDir(), "out")
	if errors := buildSSRProject(CollectVela(src), src, clean, CompileOptions{StatusWriter: io.Discard, ErrWriter: io.Discard, SSRModule: SSRDefaultModule}, false); errors != 0 {
		t.Fatalf("clean build: %d errors", errors)
	}
	got := readTree(t, out, map[string]bool{SSRReadyStamp: true})
	want := readTree(t, clean, nil)
	var names []string
	for name := range want {
		names = append(names, name)
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if got[name] != want[name] {
			t.Errorf("%s differs from a clean build (watch %d bytes, clean %d bytes)", name, len(got[name]), len(want[name]))
		}
	}
	// The comparison covers what the edits changed.
	if !strings.Contains(got["styles/widgets/Badge.css"], "rgb(7, 8, 9)") || !strings.Contains(got["server/components/widgets/badge.go"], "edited") || !regexp.MustCompile(`Hint\s+string`).MatchString(got["server/components/widgets/badge_props.go"]) {
		t.Errorf("the edit of Badge.vela did not reach the styles and the renderer")
	}
	if got["server/components/widgets/added.go"] == "" || got["client/widgets/Added.js"] == "" {
		t.Errorf("the added component has no renderer or client module")
	}
	for _, name := range []string{"server/components/gone.go", "server/components/gone_props.go", "client/Gone.js", "styles/Gone.css"} {
		if _, ok := got[name]; ok {
			t.Errorf("%s of the removed component is still there", name)
		}
	}
	if !strings.Contains(log.String(), "Gone.vela (removed)") {
		t.Errorf("the removal is not reported:\n%s", log.String())
	}
}
