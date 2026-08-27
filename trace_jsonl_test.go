package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readJSONLTraceRecords(t *testing.T, path string) []CompilationTraceV1 {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("trace %s contains no JSONL records", path)
	}
	records := make([]CompilationTraceV1, 0, len(lines))
	for index, line := range lines {
		var record CompilationTraceV1
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("trace line %d is not an independent JSON record: %v\n%s", index+1, err, line)
		}
		records = append(records, record)
	}
	return records
}

func TestJSONLTraceSinkRecordsParallelProjectWithoutSourcePaths(t *testing.T) {
	dir := t.TempDir()
	srcRoot := filepath.Join(dir, "src")
	outRoot := filepath.Join(dir, "dist")
	files := []struct {
		relative string
		source   string
	}{
		{"dashboard/Overview.vela", "<template><p>private-dashboard-token</p></template>"},
		{"settings/Profile.vela", "<template><p>private-profile-token</p></template>"},
	}
	var sourceFiles []string
	for _, file := range files {
		path := filepath.Join(srcRoot, file.relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(file.source), 0o644); err != nil {
			t.Fatal(err)
		}
		sourceFiles = append(sourceFiles, path)
	}

	tracePath := filepath.Join(dir, "trace.jsonl")
	sink, err := OpenJSONLTraceSink(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sink.Close() }()

	errors := buildProject(sourceFiles, srcRoot, outRoot, CompileOptions{
		TraceSink:    sink,
		StatusWriter: io.Discard,
	}, true)
	if errors != 0 {
		t.Fatalf("parallel project errors = %d", errors)
	}
	records := readJSONLTraceRecords(t, tracePath)
	if len(records) != len(files) {
		t.Fatalf("trace records = %d, want %d", len(records), len(files))
	}
	seen := map[string]bool{}
	for _, record := range records {
		if record.SchemaVersion != 1 || record.Outcome != "success" {
			t.Fatalf("unexpected trace record: %+v", record)
		}
		seen[record.Component.ID] = true
		encoded, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), dir) || strings.Contains(string(encoded), "private-") {
			t.Fatalf("trace leaked path or source text: %s", encoded)
		}
	}
	for _, id := range []string{"dashboard/Overview.vela", "settings/Profile.vela"} {
		if !seen[id] {
			t.Fatalf("missing project-relative trace ID %q in %#v", id, seen)
		}
	}
}

func TestConfigureTraceRoutesHumanStatusAwayFromStdoutJSONL(t *testing.T) {
	opts := CompileOptions{}
	closeTrace, err := configureTrace(&opts, "-")
	if err != nil {
		t.Fatal(err)
	}
	if opts.TraceSink == nil || opts.StatusWriter != os.Stderr {
		t.Fatalf("stdout trace configuration = %+v", opts)
	}
	if closeTrace == nil {
		t.Fatal("stdout trace configuration did not provide cleanup")
	}
	if err := closeTrace(); err != nil {
		t.Fatal(err)
	}
}

func TestJSONLTraceSinkRecordsRepeatedProjectWatchCompile(t *testing.T) {
	dir := t.TempDir()
	srcRoot := filepath.Join(dir, "src")
	src := filepath.Join(srcRoot, "nested", "App.vela")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("<template><p>first</p></template>"), 0o644); err != nil {
		t.Fatal(err)
	}
	tracePath := filepath.Join(dir, "watch.jsonl")
	sink, err := OpenJSONLTraceSink(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sink.Close() }()
	opts := CompileOptions{TraceSink: sink, StatusWriter: io.Discard}
	if !compileProjectFile(src, srcRoot, filepath.Join(dir, "dist"), opts) {
		t.Fatal("initial project compilation failed")
	}
	if err := os.WriteFile(src, []byte("<template><p>second</p></template>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !compileProjectFile(src, srcRoot, filepath.Join(dir, "dist"), opts) {
		t.Fatal("watch recompilation failed")
	}
	records := readJSONLTraceRecords(t, tracePath)
	if len(records) != 2 {
		t.Fatalf("watch trace records = %d, want 2", len(records))
	}
	for _, record := range records {
		if record.Component.ID != "nested/App.vela" || record.Outcome != "success" {
			t.Fatalf("unexpected watch trace record: %+v", record)
		}
	}
}

type failingTraceWriter struct{}

func (failingTraceWriter) Write([]byte) (int, error) {
	return 0, errors.New("trace device unavailable")
}

func TestTraceSinkFailureDoesNotCorruptGeneratedOutputOrSourceMap(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "App.vela")
	if err := os.WriteFile(src, []byte(`<script>let count = $state(0);</script><template><button @click={() => count++}>{count}</button></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "App.js")
	_, err := compileFile(src, dest, CompileOptions{
		SourceMap: true,
		TraceSink: NewJSONLTraceSink(failingTraceWriter{}),
	})
	if err == nil || !strings.Contains(err.Error(), "write compiler trace") || !strings.Contains(err.Error(), "trace device unavailable") {
		t.Fatalf("trace sink error = %v", err)
	}
	code, readErr := os.ReadFile(dest)
	if readErr != nil || !strings.Contains(string(code), "sourceMappingURL=App.js.map") {
		t.Fatalf("generated JS was lost after trace failure: err=%v code=%q", readErr, code)
	}
	if _, mapErr := os.Stat(dest + ".map"); mapErr != nil {
		t.Fatalf("generated source map was lost after trace failure: %v", mapErr)
	}
}
