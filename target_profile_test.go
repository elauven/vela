package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func compileProfileScript(t *testing.T, script string, opts CompileOptions) ([]*Diagnostic, error) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "App.vela")
	if err := os.WriteFile(src, []byte("<script>"+script+"</script><template><p>ok</p></template>"), 0o644); err != nil {
		t.Fatal(err)
	}
	return compileFile(src, filepath.Join(dir, "App.js"), opts)
}

func findDiagnostic(diags []*Diagnostic, code string) *Diagnostic {
	for _, diag := range diags {
		if diag != nil && diag.Code == code {
			return diag
		}
	}
	return nil
}

func TestTargetProfilesRejectOnlyDirectExcludedGlobals(t *testing.T) {
	tests := []struct {
		name    string
		profile TargetProfile
		script  string
		wantErr bool
	}{
		{"permissive keeps legacy globals", TargetPermissive, `document; process;`, false},
		{"browser excludes node process", TargetBrowser, `process;`, true},
		{"browser keeps document", TargetBrowser, `document.createElement('p');`, false},
		{"worker excludes document", TargetWorker, `document;`, true},
		{"worker keeps fetch", TargetWorker, `fetch('/api');`, false},
		{"node excludes document", TargetNode, `document;`, true},
		{"node keeps process", TargetNode, `process.env;`, false},
		{"ssr excludes document", TargetSSR, `document;`, true},
		{"ssr keeps process", TargetSSR, `process.env;`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			diags, err := compileProfileScript(t, tc.script, CompileOptions{TargetProfile: tc.profile})
			profileDiag := findDiagnostic(diags, "VELA-PROFILE-GLOBAL")
			if tc.wantErr {
				if err == nil || profileDiag == nil || profileDiag.Line != 1 || profileDiag.Col < 1 {
					t.Fatalf("diagnostics = %+v, err = %v; want positioned profile error", diags, err)
				}
			} else if err != nil || profileDiag != nil {
				t.Fatalf("diagnostics = %+v, err = %v; want profile-compatible compile", diags, err)
			}
		})
	}
}

func TestTargetProfileDoesNotRejectDynamicAliasesOrShadowedGlobals(t *testing.T) {
	script := `const host = globalThis; host.document; const document = {}; document.createElement('p');`
	diags, err := compileProfileScript(t, script, CompileOptions{TargetProfile: TargetWorker})
	if err != nil || findDiagnostic(diags, "VELA-PROFILE-GLOBAL") != nil {
		t.Fatalf("diagnostics = %+v, err = %v; dynamic alias or local shadow was rejected", diags, err)
	}
}

func TestUnavailableCapabilitiesRejectOnlyDirectBuiltin(t *testing.T) {
	diags, err := compileProfileScript(t, `fetch('/api');`, CompileOptions{
		TargetProfile:           TargetBrowser,
		UnavailableCapabilities: []string{"fetch"},
	})
	if err == nil || findDiagnostic(diags, "VELA-CAPABILITY-UNAVAILABLE") == nil {
		t.Fatalf("diagnostics = %+v, err = %v; want capability diagnostic", diags, err)
	}

	diags, err = compileProfileScript(t, `const request = fetch; request('/api');`, CompileOptions{TargetProfile: TargetBrowser})
	if err != nil || findDiagnostic(diags, "VELA-CAPABILITY-UNAVAILABLE") != nil {
		t.Fatalf("diagnostics = %+v, err = %v; ordinary alias should remain compatible", diags, err)
	}
}

func TestParseTargetProfileAndCapabilityList(t *testing.T) {
	if got, err := ParseTargetProfile(" Worker "); err != nil || got != TargetWorker {
		t.Fatalf("ParseTargetProfile = %q, %v; want worker", got, err)
	}
	if _, err := ParseTargetProfile("edge"); err == nil {
		t.Fatal("invalid target profile unexpectedly accepted")
	}
	if got := splitCapabilityList("fetch, indexedDB,fetch, "); len(got) != 2 || got[0] != "fetch" || got[1] != "indexedDB" {
		t.Fatalf("capability list = %#v", got)
	}
}

func TestStrictElevatesOnlyUnresolvedReads(t *testing.T) {
	diags, err := compileProfileScript(t, `missing;`, CompileOptions{})
	if err != nil || findDiagnostic(diags, "VELA-SCOPE-UNDECLARED") == nil {
		t.Fatalf("permissive diagnostics = %+v, err = %v; want warning", diags, err)
	}

	diags, err = compileProfileScript(t, `missing;`, CompileOptions{Strict: true})
	diag := findDiagnostic(diags, "VELA-SCOPE-UNDECLARED")
	if err == nil || diag == nil || diag.Sev != SevError {
		t.Fatalf("strict diagnostics = %+v, err = %v; want error", diags, err)
	}

	diags, err = compileProfileScript(t, `const value = 1; value;`, CompileOptions{Strict: true})
	if err != nil || findDiagnostic(diags, "VELA-SCOPE-UNDECLARED") != nil {
		t.Fatalf("strict known binding diagnostics = %+v, err = %v", diags, err)
	}
}

func TestProfileDiagnosticHumanJSONAndUTF16Position(t *testing.T) {
	source := "<script>🙂 document.createElement('p');</script><template><p>ok</p></template>"
	dir := t.TempDir()
	src := filepath.Join(dir, "App.vela")
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	diags, err := compileFile(src, filepath.Join(dir, "App.js"), CompileOptions{TargetProfile: TargetWorker})
	diag := findDiagnostic(diags, "VELA-PROFILE-GLOBAL")
	if err == nil || diag == nil || diag.Col != 12 {
		t.Fatalf("diagnostics = %+v, err = %v; want UTF-16 column 12", diags, err)
	}
	if text := diag.String(); !strings.Contains(text, "[VELA-PROFILE-GLOBAL]") || !strings.Contains(text, "help:") {
		t.Fatalf("human diagnostic = %q", text)
	}
	data, jsonErr := DiagnosticsJSON(diags)
	if jsonErr != nil {
		t.Fatal(jsonErr)
	}
	var machine []MachineDiagnostic
	if err := json.Unmarshal(data, &machine); err != nil {
		t.Fatal(err)
	}
	if len(machine) == 0 || machine[0].Code != "VELA-PROFILE-GLOBAL" || machine[0].Column != 12 || machine[0].Severity != "error" {
		t.Fatalf("machine diagnostics = %s", data)
	}
}

func TestTargetProfileKeepsSourceMapOutputCompatible(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "App.vela")
	source := `<script>document.title = "ready";</script><template><button @click={() => document.title = "clicked"}>Go</button></template>`
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "App.js")
	if diags, err := compileFile(src, dest, CompileOptions{TargetProfile: TargetBrowser, SourceMap: true}); err != nil {
		t.Fatalf("diagnostics = %+v, err = %v", diags, err)
	}
	data, err := os.ReadFile(dest + ".map")
	if err != nil {
		t.Fatal(err)
	}
	var mapping SourceMapV3
	if err := json.Unmarshal(data, &mapping); err != nil || mapping.Mappings == "" {
		t.Fatalf("source map = %s, err = %v", data, err)
	}
}
