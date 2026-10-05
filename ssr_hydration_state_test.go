package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSSRClientProgramReadsHydrationStateWithoutChangingCSRDefault(t *testing.T) {
	legacyCodeShapeTest(t)
	sfc, err := parseSFC(`<script>let count=$state(1); let settings=$state.deep({ enabled: true });</script><template><p>{count}</p></template>`)
	if err != nil {
		t.Fatal(err)
	}
	analysis := analyzeScriptAST(sfc.ScriptAST)
	ssrClient := PrintJS(buildProgramCSSWithHydration("Page", sfc, sfc.TemplateAST.Nodes, analysis, "vela-page", false, true), PrintPretty)
	for _, want := range []string{
		"const $$velaState = $$props.$$velaState || null;",
		"Object.prototype.hasOwnProperty.call($$velaState, 'count')",
		"Object.prototype.hasOwnProperty.call($$velaState, 'settings')",
		"$$proxy($$velaState && Object.prototype.hasOwnProperty.call($$velaState, 'settings') ? $$velaState.settings : { enabled: true }, $$mut_settings)",
	} {
		if !strings.Contains(ssrClient, want) {
			t.Fatalf("SSR client misses %q:\n%s", want, ssrClient)
		}
	}
	csr := PrintJS(buildProgramCSS("Page", sfc, sfc.TemplateAST.Nodes, analyzeScriptAST(sfc.ScriptAST), "vela-page", false), PrintPretty)
	if strings.Contains(csr, "$$velaState") {
		t.Fatalf("ordinary CSR unexpectedly includes hydration state support:\n%s", csr)
	}
}

func TestSSRProjectClientModuleEnablesHydrationStateInput(t *testing.T) {
	legacySSRProtocolTest(t)
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(srcRoot, "App.vela")
	if err := os.WriteFile(source, []byte(`<script>export let title='SSR'; let count=$state(1);</script><template><p>{title}</p></template>`), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	if errors := buildSSRProject([]string{source}, srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	client, err := os.ReadFile(NewSSRProjectLayout(outRoot).clientPath(source, srcRoot))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(client), "const $$velaState = $$props.$$velaState || null;") {
		t.Fatalf("SSR client module misses hydration state input:\n%s", client)
	}
}
