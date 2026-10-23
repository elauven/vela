package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestSSRReportWritesSiteRecords covers --ssr-report (§10.4): one entry per
// component with its sites, outcomes, tiers, and types, written also when a
// component fails.
func TestSSRReportWritesSiteRecords(t *testing.T) {
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"Good.vela": `<script>export let name = 'x'; let n = $state(2);</script><template><p :title={name}>{name} {n + 1}</p></template>`,
		"Bad.vela":  `<script>export let items = [];</script><template><p>{items.localeCompare()}</p></template>`,
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(srcRoot, name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reportPath := filepath.Join(root, "report.json")
	opts := CompileOptions{StatusWriter: io.Discard, SSRModule: "example.com/report/server", SSRReport: reportPath}
	stderr := os.Stderr
	devNull, _ := os.Open(os.DevNull)
	os.Stderr = devNull
	errorsCount := BuildSSRProject(CollectVela(srcRoot), srcRoot, filepath.Join(root, "out"), opts, false)
	os.Stderr = stderr
	if errorsCount != 1 {
		t.Fatalf("errors = %d, want 1 (Bad.vela)", errorsCount)
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	var report SSRReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Components) != 2 || report.Components[0].Source != "Bad.vela" || report.Components[1].Source != "Good.vela" {
		t.Fatalf("components = %+v", report.Components)
	}
	bad, good := report.Components[0], report.Components[1]
	if bad.Status != "error" || len(bad.Sites) != 2 || bad.Sites[1].Kind != "text" || bad.Sites[1].Outcome != SSROutcomeUnsupported || bad.Sites[1].Diagnostic == nil || bad.Sites[1].Line != 1 {
		t.Fatalf("bad component = %+v", bad)
	}
	kinds := map[string]SSRSiteRecord{}
	for _, site := range good.Sites {
		kinds[site.Kind+":"+site.Name] = site
	}
	if site := kinds["attribute:title"]; site.Outcome != SSROutcomeNative || site.Type != "string" || site.Tier != "typed" {
		t.Fatalf("title attribute site = %+v", site)
	}
	if site := kinds["state:n"]; site.Required || site.Outcome != SSROutcomeNative || site.Type != "number" {
		t.Fatalf("state declaration site = %+v", site)
	}
	if report.Summary.Components != 2 || report.Summary.ComponentsOK != 1 || report.Summary.Outcomes[SSROutcomeUnsupported] != 1 {
		t.Fatalf("summary = %+v", report.Summary)
	}
}
