package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type ssrCheckedBindingOracleCase struct {
	Name              string `json:"name"`
	InitialExpression string `json:"initial_expression"`
	InitialChecked    bool   `json:"initial_checked"`
	InitialText       string `json:"initial_text"`
	UpdatedExpression string `json:"updated_expression"`
	UpdatedChecked    bool   `json:"updated_checked"`
	UpdatedText       string `json:"updated_text"`
}

func loadSSRCheckedBindingOracle(t *testing.T) []ssrCheckedBindingOracleCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "ssr_checked_binding_oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []ssrCheckedBindingOracleCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("SSR checked-binding oracle must not be empty")
	}
	return cases
}

// TestSSRCheckedBindingOracle consumes the same matrix as
// tools/verify-ssr-checked-binding-oracle.mjs. Every initial value passes
// through Vela parsing and the generated Go renderer, which must write the
// checked attribute by JavaScript truthiness and the mirrored text as String()
// does. Hydration of the bound checkbox is covered by the block corpus.
func TestSSRCheckedBindingOracle(t *testing.T) {
	cases := loadSSRCheckedBindingOracle(t)

	var source strings.Builder
	source.WriteString("<script>\n")
	for _, tc := range cases {
		source.WriteString("let checked_" + tc.Name + " = $state(" + tc.InitialExpression + ");\n")
	}
	source.WriteString("</script><template><main>\n")
	for _, tc := range cases {
		source.WriteString(`<input type="checkbox" class="case-` + tc.Name + `" bind:checked={checked_` + tc.Name + `} />`)
		source.WriteString(`<p class="mirror-` + tc.Name + `">{checked_` + tc.Name + "}</p>\n")
	}
	source.WriteString(`</main></template>`)

	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	component := filepath.Join(srcRoot, "Page.vela")
	if err := os.WriteFile(component, []byte(source.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(root, "out")
	projectModule := "example.com/ssrcheckedbindingoracle"
	if errors := buildSSRProject([]string{component}, srcRoot, outRoot, CompileOptions{StatusWriter: io.Discard, SSRModule: projectModule + "/server"}, false); errors != 0 {
		t.Fatalf("SSR project errors=%d", errors)
	}
	compilerRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	module := "module " + projectModule + "\n\ngo 1.25\n\n" + ssrRuntimeRequire(compilerRoot)
	if err := os.WriteFile(filepath.Join(outRoot, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	mainSource := `package main
import (
    "os"
    components "example.com/ssrcheckedbindingoracle/server/components"
)
func main() {
    if err := components.RenderPage(os.Stdout, components.PageProps{}, components.PageSlots{}); err != nil { panic(err) }
}
`
	if err := os.WriteFile(filepath.Join(outRoot, "main.go"), []byte(mainSource), 0o644); err != nil {
		t.Fatal(err)
	}
	output, err := runSSRGeneratedProgram(outRoot)
	if err != nil {
		t.Fatal(err)
	}
	html := regexp.MustCompile(`<!--[\s\S]*?-->`).ReplaceAllString(string(output), "")
	checkedAttr := regexp.MustCompile(`\schecked(?:[\s=/>])`)
	for _, tc := range cases {
		input := regexp.MustCompile(`<input[^>]*class="case-` + regexp.QuoteMeta(tc.Name) + `"[^>]*>`).FindString(html)
		if input == "" {
			t.Fatalf("%s: checkbox missing from SSR output:\n%s", tc.Name, html)
		}
		if got := checkedAttr.MatchString(input); got != tc.InitialChecked {
			t.Errorf("%s: checked attribute = %v, want %v: %s", tc.Name, got, tc.InitialChecked, input)
		}
		mirror := regexp.MustCompile(`<p[^>]*class="mirror-` + regexp.QuoteMeta(tc.Name) + `"[^>]*>([^<]*)</p>`).FindStringSubmatch(html)
		if mirror == nil || mirror[1] != tc.InitialText {
			t.Errorf("%s: mirrored text = %q, want %q", tc.Name, mirror, tc.InitialText)
		}
	}
}
