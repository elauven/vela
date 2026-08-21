package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type browserSourceMapConfig struct {
	RootDir  string `json:"rootDir"`
	Entry    string `json:"entry"`
	Map      string `json:"map"`
	Selector string `json:"selector"`
	Expected struct {
		Source string `json:"source"`
		Line   int    `json:"line"`
		Column int    `json:"column"`
	} `json:"expected"`
}

type browserSourceMapResult struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error"`
	Original struct {
		Source string `json:"source"`
		Line   int    `json:"line"`
		Column int    `json:"column"`
	} `json:"original"`
}

func requirePlaywright(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is unavailable; install Node.js and Playwright to run browser source-map tests")
	}
	if err := exec.Command(node, "-e", "require.resolve(\"playwright\")").Run(); err != nil {
		t.Skip("playwright is unavailable; run npm install --save-dev playwright and npx playwright install chromium")
	}
	return node
}

func TestBrowserSourceMapMapsTemplateHandler(t *testing.T) {
	node := requirePlaywright(t)
	dir := t.TempDir()
	const raw = `<script>
  let count = $state(0);
</script>
<template>
  <button class="explode" @click={() => { throw new Error("template boom"); }}>Boom</button>
</template>`
	src := filepath.Join(dir, "App.vela")
	if err := os.WriteFile(src, []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := compileFile(src, filepath.Join(dir, "App.js"), CompileOptions{Codegen: "ast", SourceMap: true}); err != nil {
		t.Fatalf("compile fixture: %v", err)
	}
	index := `<!doctype html><script type="module">import { App } from "./App.js"; App(document.body, {}, {});</script>`
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(index), 0644); err != nil {
		t.Fatal(err)
	}
	at := strings.Index(raw, "() => { throw new Error")
	if at < 0 {
		t.Fatal("fixture handler expression not found")
	}
	line, column := sourceLineColumn(raw, at)
	config := browserSourceMapConfig{RootDir: dir, Entry: "App.js", Map: "App.js.map", Selector: "button.explode"}
	config.Expected.Source, config.Expected.Line, config.Expected.Column = filepath.ToSlash(src), line, column
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "browser-source-map.json")
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, "tools/browser-sourcemap-harness.mjs", configPath).CombinedOutput()
	if err != nil {
		t.Fatalf("browser harness: %v\n%s", err, out)
	}
	var result browserSourceMapResult
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("invalid browser harness output: %v\n%s", err, out)
	}
	if !result.OK {
		t.Fatalf("browser source-map verification failed: %s; original=%+v", result.Error, result.Original)
	}
}
