package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type ssrValueTextOracleCase struct {
	Name       string `json:"name"`
	Expression string `json:"expression"`
	Expected   string `json:"expected"`
}

func loadSSRValueTextOracle(t *testing.T) []ssrValueTextOracleCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "ssr_value_text_oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []ssrValueTextOracleCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("SSR value-text oracle must not be empty")
	}
	return cases
}

// TestGeneratedSSRRendererMatchesValueTextOracle makes the Go half of the
// shared R3 matrix executable. Expressions originate in the JSON corpus used
// by tools/verify-ssr-value-text-oracle.mjs, then pass through Vela parsing,
// cached AST lowering, generated Go, and the SSR runtime before HTML is read.
func TestGeneratedSSRRendererMatchesValueTextOracle(t *testing.T) {
	cases := loadSSRValueTextOracle(t)
	var template strings.Builder
	template.WriteString("<template><main>")
	for _, tc := range cases {
		template.WriteString(`<p class="case-`)
		template.WriteString(tc.Name)
		template.WriteString(`">{`)
		template.WriteString(tc.Expression)
		template.WriteString(`}</p>`)
	}
	template.WriteString("</main></template>")
	sfc, err := parseSFC(template.String())
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-value-oracle")
	if err != nil {
		t.Fatal(err)
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		t.Fatal(err)
	}
	renderer, err := FormatSSRGoFile(file)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	components := filepath.Join(root, "components")
	if err := os.MkdirAll(components, 0o755); err != nil {
		t.Fatal(err)
	}
	compilerRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	module := "module example.com/ssrvalueoracle\n\ngo 1.25\n\nrequire github.com/elauven/vela v0.0.0\n\nreplace github.com/elauven/vela => " + filepath.ToSlash(compilerRoot) + "\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(components, "page.go"), renderer, 0o644); err != nil {
		t.Fatal(err)
	}
	mainSource := `package main
import (
    "os"
    "example.com/ssrvalueoracle/components"
)
func main() {
    if err := ` + ssrTestRenderCall("components.PageProps{}", "vela-value-oracle") + `; err != nil { panic(err) }
}
`
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(mainSource), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "run", ".")
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated Go renderer: %v\n%s", err, output)
	}
	withoutMarkers := regexp.MustCompile(`<!--[^>]*-->`).ReplaceAllString(string(output), "")
	for _, tc := range cases {
		pattern := regexp.MustCompile(`<p[^>]*class="case-` + regexp.QuoteMeta(tc.Name) + `"[^>]*>(.*?)</p>`)
		match := pattern.FindStringSubmatch(withoutMarkers)
		if len(match) != 2 {
			t.Fatalf("oracle case %q has no rendered paragraph:\n%s", tc.Name, withoutMarkers)
		}
		if match[1] != tc.Expected {
			t.Errorf("oracle case %q rendered %q, want %q", tc.Name, match[1], tc.Expected)
		}
	}
}
