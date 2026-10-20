package main

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

type ssrExpressionOracleRow struct {
	Group      string                     `json:"group"`
	Name       string                     `json:"name"`
	Expression string                     `json:"expression"`
	Functions  string                     `json:"functions"`
	Values     map[string]json.RawMessage `json:"values"`
	Expected   *string                    `json:"expected"`
	Throws     string                     `json:"throws"`
}

// TestSSRExpressionOracleBothTiers runs testdata/ssr_expression_oracle.json
// (V-11.1b, task 044). Node recomputes every stored result; then each row
// is rendered by generated Go twice: in the typed tier, with operands that
// are props whose literal defaults prove their types, and in the dynamic
// tier, with operands that are untyped props supplied at render time. Both
// must produce the JavaScript text, or the JavaScript exception kind.
func TestSSRExpressionOracleBothTiers(t *testing.T) {
	node, _ := requireJsdom(t)
	if out, err := exec.Command(node, "tools/verify-ssr-expression-oracle.mjs").CombinedOutput(); err != nil {
		t.Fatalf("JavaScript half: %v\n%s", err, out)
	}
	data, err := os.ReadFile(filepath.Join("testdata", "ssr_expression_oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rows []ssrExpressionOracleRow
	if err := json.Unmarshal(data, &rows); err != nil {
		t.Fatal(err)
	}
	for _, failure := range runSSRExpressionRows(t, rows) {
		t.Error(failure)
	}
}

// runSSRExpressionRows renders every row with generated Go in the typed and
// the dynamic tier and returns the rows whose text differs from the
// JavaScript result.
func runSSRExpressionRows(t *testing.T, rows []ssrExpressionOracleRow) []string {
	t.Helper()
	root := t.TempDir()
	srcRoot := filepath.Join(root, "src")
	if err := os.MkdirAll(srcRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	var calls strings.Builder
	for i, row := range rows {
		names := make([]string, 0, len(row.Values))
		for name := range row.Values {
			names = append(names, name)
		}
		sort.Strings(names)
		var typed, dynamic strings.Builder
		for _, name := range names {
			literal := string(row.Values[name])
			if literal == `"__undefined__"` {
				literal = "undefined"
			}
			fmt.Fprintf(&typed, "export let %s = %s;\n", name, literal)
			fmt.Fprintf(&dynamic, "export let %s;\n", name)
		}
		template := "<template><p>{" + row.Expression + "}</p></template>\n"
		if row.Functions != "" {
			typed.WriteString(row.Functions + "\n")
			dynamic.WriteString(row.Functions + "\n")
		}
		for tier, script := range map[string]string{"T": typed.String(), "D": dynamic.String()} {
			component := fmt.Sprintf("Case%s%d", tier, i)
			source := "<script>\n" + script + "</script>\n" + template
			if err := os.WriteFile(filepath.Join(srcRoot, component+".vela"), []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			values := "{}"
			if tier == "D" {
				encoded, _ := json.Marshal(row.Values)
				values = string(encoded)
			}
			fmt.Fprintf(&calls, "\tout[%q] = render(%q, c.Render%sStatic)\n", component, values, component)
		}
	}
	outRoot := filepath.Join(root, "out")
	projectModule := "example.com/ssroracle"

	opts := CompileOptions{StatusWriter: io.Discard, SSRModule: projectModule + "/server"}
	if errors := buildSSRProject(CollectVela(srcRoot), srcRoot, outRoot, opts, false); errors != 0 {
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
	main := `package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"

	vela "github.com/elauven/vela/ssr_runtime"
	c "` + projectModule + `/server/components"
)

// render sets the given props (an absent prop keeps its default) and
// renders static HTML, or reports the JavaScript exception kind.
func render[P, S any](values string, static func(io.Writer, P, S) error) string {
	var raw map[string]any
	if err := json.Unmarshal([]byte(values), &raw); err != nil {
		panic(err)
	}
	var props P
	target := reflect.ValueOf(&props).Elem()
	set := map[string]bool{}
	for name, value := range raw {
		field := target.FieldByNameFunc(func(field string) bool { return strings.EqualFold(field, name) })
		if value == "__undefined__" {
			value = vela.Undefined()
		}
		if value != nil {
			field.Set(reflect.ValueOf(value))
		}
		set[name] = true
	}
	target.FieldByName("Set").Set(reflect.ValueOf(set))
	var slots S
	var out strings.Builder
	if err := static(&out, props, slots); err != nil {
		var thrown *vela.RenderError
		if errors.As(err, &thrown) {
			return "THROW:" + thrown.Kind
		}
		return "ERROR:" + err.Error()
	}
	return out.String()
}

func main() {
	out := map[string]string{}
` + calls.String() + `	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		panic(err)
	}
}
`
	if err := os.WriteFile(filepath.Join(outRoot, "main.go"), []byte(main), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "run", ".")
	command.Dir = outRoot
	command.Stderr = os.Stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("render oracle: %v", err)
	}
	var rendered map[string]string
	if err := json.Unmarshal(output, &rendered); err != nil {
		t.Fatalf("invalid output: %v\n%s", err, output)
	}
	paragraph := regexp.MustCompile(`(?s)^<p[^>]*>(.*)</p>$`)
	var failures []string
	for i, row := range rows {
		want := "THROW:" + row.Throws
		if row.Expected != nil {
			want = *row.Expected
		}
		for _, tier := range []string{"T", "D"} {
			got := rendered[fmt.Sprintf("Case%s%d", tier, i)]
			if match := paragraph.FindStringSubmatch(got); match != nil {
				got = html.UnescapeString(match[1])
			}
			if got != want {
				name := map[string]string{"T": "typed", "D": "dynamic"}[tier]
				failures = append(failures, fmt.Sprintf("%s (%s tier): %s = %q, want %q", row.Name, name, row.Expression, got, want))
			}
		}
	}
	return failures
}
