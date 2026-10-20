package ssr

import (
	"encoding/json"
	"testing"
)

func TestSSRArtifactManifestPreservesClientBoundaryMode(t *testing.T) {
	source, err := json.Marshal(SSRArtifactManifest{
		Component: "Page",
		Mode:      SSRArtifactClientBoundary,
		Hydrate:   "Page.hydrate.js",
	})
	if err != nil {
		t.Fatal(err)
	}
	var manifest SSRArtifactManifest
	if err := json.Unmarshal(source, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Mode != SSRArtifactClientBoundary || manifest.Hydrate != "Page.hydrate.js" {
		t.Fatalf("client-boundary manifest=%#v", manifest)
	}
}

func TestGoFileStemUsesLowerSnakeCase(t *testing.T) {
	cases := map[string]string{
		"UserPage":      "user_page",
		"HTTPServer":    "http_server",
		"APIv2Client":   "ap_iv2_client",
		"kebab-case":    "kebab_case",
		"already_snake": "already_snake",
	}
	for input, want := range cases {
		if got := goFileStem(input); got != want {
			t.Errorf("goFileStem(%q)=%q want %q", input, got, want)
		}
	}
}
