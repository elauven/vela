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
	probe := "const fs=require(\"fs\"); const {chromium}=require(\"playwright\"); if (!fs.existsSync(chromium.executablePath())) process.exit(1)"
	if err := exec.Command(node, "-e", probe).Run(); err != nil {
		t.Skip("Playwright Chromium is unavailable; run npx playwright install chromium")
	}
	return node
}

func assertBrowserSourceMap(t *testing.T, raw, selector, sourceNeedle string) {
	t.Helper()
	node := requirePlaywright(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "App.vela")
	if err := os.WriteFile(src, []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := compileFile(src, filepath.Join(dir, "App.js"), CompileOptions{SourceMap: true}); err != nil {
		t.Fatalf("compile fixture: %v", err)
	}
	if err := stageRuntime(dir); err != nil {
		t.Fatalf("stage runtime: %v", err)
	}
	index := `<!doctype html><script type="module">import { App } from "./App.js"; App(document.body, {}, {});</script>`
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(index), 0644); err != nil {
		t.Fatal(err)
	}
	at := strings.Index(raw, sourceNeedle)
	if at < 0 {
		t.Fatalf("fixture source needle %q not found", sourceNeedle)
	}
	line, column := sourceLineColumn(raw, at)
	config := browserSourceMapConfig{RootDir: dir, Entry: "App.js", Map: "App.js.map", Selector: selector}
	config.Expected.Source, config.Expected.Line, config.Expected.Column = filepath.ToSlash(src), line+1, column
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

func TestBrowserSourceMapMapsScriptStatement(t *testing.T) {
	assertBrowserSourceMap(t, `<script>
  function explode() { throw new Error("script boom"); }
</script>
<template><button class="explode" @click={explode}>Boom</button></template>`, "button.explode", "new Error")
}

func TestBrowserSourceMapMapsTemplateHandler(t *testing.T) {
	assertBrowserSourceMap(t, `<script>
  let count = $state(0);
</script>
<template>
  <button class="explode" @click={() => { throw new Error("template boom"); }}>Boom</button>
</template>`, "button.explode", "() => { throw new Error")
}

func TestBrowserSourceMapMapsEachIfHandler(t *testing.T) {
	assertBrowserSourceMap(t, `<script>
  let rows = $state.deep([{ id: 1, show: true }]);
</script>
<template>
  {#each rows as row (row.id)}
    {#if row.show}
      <button class="explode" @click={() => { throw new Error("row boom"); }}>Boom</button>
    {/if}
  {/each}
</template>`, "button.explode", "() => { throw new Error")
}

func TestBrowserSourceMapMapsKeyHandler(t *testing.T) {
	assertBrowserSourceMap(t, `<script>
  let identity = $state(1);
</script>
<template>
  {#key identity}
    <button class="explode" @click={() => { throw new Error("key boom"); }}>Boom</button>
  {/key}
</template>`, "button.explode", "() => { throw new Error")
}

func TestBrowserSourceMapMapsAwaitHandler(t *testing.T) {
	assertBrowserSourceMap(t, `<script>
  let request = $state(Promise.resolve("ready"));
</script>
<template>
  {#await request}
    <p>Loading</p>
  {:then value}
    <button class="explode" @click={() => { throw new Error("await boom"); }}>{value}</button>
  {:catch error}
    <p>{error.message}</p>
  {/await}
</template>`, "button.explode", "() => { throw new Error")
}
