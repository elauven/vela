package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// R1: unselected branches of JavaScript ternary/logical expressions must not
// be evaluated by generated Go. In particular, props.Items[0] is unsafe for
// the empty collection but is valid in the selected non-empty branches.
func TestGeneratedSSRRendererShortCircuitsUnsafeCollectionReads(t *testing.T) {
	sfc, err := parseSFC(`<script>export let items = [];</script><template><main><p>{items.length ? items[0] : 'empty'}</p><p>{items.length && items[0]}</p><p>{!items.length || items[0]}</p><p>{items.length ? (items.length && items[0]) : (!items.length || items[0])}</p></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
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
	module := "module example.com/ssrfixture\n\ngo 1.25\n\n" + ssrRuntimeRequire(compilerRoot)
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(components, "Page.go"), renderer, 0o644); err != nil {
		t.Fatal(err)
	}
	mainSource := `package main
import (
    "fmt"
    "os"
    "example.com/ssrfixture/components"
)
func main() {
    empty := components.PageProps{Items: []any{}, Set: map[string]bool{"items": true}}
    if err := ` + ssrTestRenderCall("empty", "empty") + `; err != nil { panic(err) }
    fmt.Print("\n---\n")
    full := components.PageProps{Items: []any{"first"}, Set: map[string]bool{"items": true}}
    if err := ` + ssrTestRenderCall("full", "full") + `; err != nil { panic(err) }
}
`
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(mainSource), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "run", ".")
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go run must not evaluate an unsafe unselected index: %v\n%s", err, output)
	}
	sections := strings.Split(string(output), "\n---\n")
	if len(sections) != 2 {
		t.Fatalf("renderer output has %d sections:\n%s", len(sections), output)
	}
	for section, want := range map[int][]string{
		0: {">empty<", ">0<", ">true<", ">true<"},
		1: {">first<", ">first<", ">first<", ">first<"},
	} {
		for _, text := range want {
			if !strings.Contains(sections[section], text) {
				t.Fatalf("section %d misses %q:\n%s", section, text, sections[section])
			}
		}
	}
}

// R3: Go-rendered interpolation must retain the same visible nullish text as
// CSR, while array joining keeps JavaScript's distinct empty-field behaviour.
func TestGeneratedSSRRendererPreservesNullishTextAndArrayJoin(t *testing.T) {
	sfc, err := parseSFC(`<script>export let value = null;</script><template><main><p>{value}</p><p>{undefined}</p><p>{[1, null, undefined, [2, null], 3]}</p></main></template>`)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSSRRenderPlan("Page", sfc, "vela-page")
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
	module := "module example.com/ssrnullish\n\ngo 1.25\n\n" + ssrRuntimeRequire(compilerRoot)
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(components, "Page.go"), renderer, 0o644); err != nil {
		t.Fatal(err)
	}
	mainSource := `package main
import (
    "os"
    "example.com/ssrnullish/components"
)
func main() {
    props := components.PageProps{Value: nil, Set: map[string]bool{"value": true}}
    if err := ` + ssrTestRenderCall("props", "nullish") + `; err != nil { panic(err) }
}
`
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(mainSource), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "run", ".")
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go run: %v\n%s", err, output)
	}
	payloadNull := `"Value":null`

	payloadNull = `"value":null`

	for _, want := range []string{">null<", ">undefined<", ">1,,,2,,3<", payloadNull} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("nullish SSR output misses %q:\n%s", want, output)
		}
	}
}

// ssrTestRenderCall is the Go call that renders the Page component of a
// test program with the active DOM engine's API.
func ssrTestRenderCall(props, instance string) string {

	return "components.RenderPage(os.Stdout, " + props + ", components.PageSlots{})"

}
