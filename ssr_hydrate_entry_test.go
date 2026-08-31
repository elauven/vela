package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildSSRHydrateEntryAdoptsPayloadAndMarkerRanges(t *testing.T) {
	sfc, err := parseSFC(`<script>export let title="";</script><template><main>{title}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntry(plan)
	if err != nil {
		t.Fatal(err)
	}
	source := PrintJS(entry, PrintPretty)
	for _, want := range []string{
		"import { $$createHydrationWalker, $$readHydrationPayload } from './runtime.js';",
		"export function hydratePage($$root)",
		"$$createHydrationWalker($$root, 'vela-page')",
		"$$readHydrationPayload($$root, 'vela-page')",
		"return { root: $$root, props: $$payload.props, state: $$payload.state, ranges: $$walker.ranges };",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("hydrate entry misses %q:\n%s", want, source)
		}
	}
}

func TestSSRProjectWritesHydrateEntryAlongsideClientModule(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(srcRoot, "App.vela")
	if err := os.WriteFile(source, []byte(`<script>export let title="";</script><template><main>{title}</main></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	if errors := buildSSRProject([]string{source}, srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	layout := NewSSRProjectLayout(outRoot)
	entry, err := os.ReadFile(layout.hydratePath(source, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(entry), "export function hydrateApp") {
		t.Fatalf("missing hydrate entry:\n%s", entry)
	}
}

func TestGeneratedSSRHydrateEntryAdoptsExistingServerData(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is unavailable")
	}
	sfc, err := parseSFC(`<script>export let title="";</script><template><main>{title}</main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := BuildSSRHydrateEntry(plan)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Page.hydrate.js"), []byte(PrintJS(entry, PrintPretty)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "runtime.js"), embeddedRuntime, 0o644); err != nil {
		t.Fatal(err)
	}
	const runner = `import hydratePage from './Page.hydrate.js';
function assert(value, message) { if (!value) throw new Error(message); }
function marker(data) { return { data }; }
const comments = [marker('v0'), marker('/v0')];
const payload = { type: 'application/json', textContent: '{"component":"vela-page","props":{"Title":"server"},"state":{"count":1},"incomplete":false}' };
const document = {
  createTreeWalker() { let index = 0; return { nextNode() { return index < comments.length ? comments[index++] : null; } }; },
  getElementById(id) { return id === 'vela-state-vela-page' ? payload : null; },
};
const root = { ownerDocument: document };
const adopted = hydratePage(root);
assert(adopted.root === root, 'entry must retain the server root');
assert(adopted.props.Title === 'server', 'entry must expose server props');
assert(adopted.state.count === 1, 'entry must expose serialised state');
assert(adopted.ranges.get(0).start === comments[0], 'entry must expose indexed marker ranges');
console.log('OK');
`
	if err := os.WriteFile(filepath.Join(dir, "run.mjs"), []byte(runner), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(node, "run.mjs")
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("hydrate entry runner: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "OK" {
		t.Fatalf("hydrate entry output=%q", got)
	}
}
