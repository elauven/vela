package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestBlocksEngineSizeReport reports the generated client bytes of the
// legacy and block engines for examples/ and test_projects (Q-10.6). It is a
// report, not a gate: run with -v to see the table. Only components that
// both engines compile are counted; runtime modules are listed separately.
func TestBlocksEngineSizeReport(t *testing.T) {
	roots := []string{
		"examples",
		filepath.Join("test_projects", "corpus"),
		filepath.Join("test_projects", "gpt-todo-vela", "src"),
		filepath.Join("test_projects", "qwen-dashboard-vela", "src"),
		filepath.Join("test_projects", "qdash", "src"),
		filepath.Join("test_projects", "qpage-builder", "src"),
	}
	sizes := func(engine DOMEngine, root string, minify bool) map[string]int {
		out := t.TempDir()
		withDOMEngine(t, engine, func() {
			buildProject(collectVela(root), root, out, CompileOptions{Minify: minify, StatusWriter: discardWriter{}}, false)
		})
		result := map[string]int{}
		_ = filepath.Walk(out, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || filepath.Ext(path) != ".js" || strings.HasPrefix(info.Name(), "runtime") {
				return nil
			}
			if strings.HasSuffix(path, ".map") {
				return nil
			}
			rel, _ := filepath.Rel(out, path)
			result[rel] = int(info.Size())
			return nil
		})
		return result
	}
	var report strings.Builder
	fmt.Fprintf(&report, "\n%-34s %6s %10s %10s %7s %10s %10s %7s\n", "project", "files", "legacy", "blocks", "delta", "legacy.min", "blocks.min", "delta")
	var totals [4]int
	files := 0
	for _, root := range roots {
		var row [4]int
		legacy, blocks := sizes(DOMEngineLegacy, root, false), sizes(DOMEngineBlocks, root, false)
		legacyMin, blocksMin := sizes(DOMEngineLegacy, root, true), sizes(DOMEngineBlocks, root, true)
		names := make([]string, 0, len(legacy))
		for name := range legacy {
			if _, ok := blocks[name]; ok {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		for _, name := range names {
			row[0] += legacy[name]
			row[1] += blocks[name]
			row[2] += legacyMin[name]
			row[3] += blocksMin[name]
		}
		for i := range totals {
			totals[i] += row[i]
		}
		files += len(names)
		fmt.Fprintf(&report, "%-34s %6d %10d %10d %6.0f%% %10d %10d %6.0f%%\n", filepath.ToSlash(root), len(names), row[0], row[1], percent(row[1], row[0]), row[2], row[3], percent(row[3], row[2]))
	}
	fmt.Fprintf(&report, "%-34s %6d %10d %10d %6.0f%% %10d %10d %6.0f%%\n", "total", files, totals[0], totals[1], percent(totals[1], totals[0]), totals[2], totals[3], percent(totals[3], totals[2]))
	fmt.Fprintf(&report, "runtime: legacy runtime.js %d bytes; blocks runtime.js + runtime-blocks.js %d bytes\n",
		len(embeddedRuntime), len(embeddedRuntime)+len(embeddedBlocksRuntime))
	t.Log(report.String())
	if files == 0 {
		t.Fatal("size report compiled no components")
	}
}

func percent(value, base int) float64 {
	if base == 0 {
		return 0
	}
	return float64(value-base) * 100 / float64(base)
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
