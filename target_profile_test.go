package main

import (
	"os"
	"path/filepath"
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
