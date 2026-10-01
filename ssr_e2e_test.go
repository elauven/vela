package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedSSRRendererRunsInGoModule(t *testing.T) {
	sfc, err := parseSFC(`<script>export let title="";</script><template><main>Hello {title}</main></template>`)
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
	module := "module example.com/ssrfixture\n\ngo 1.22\n\nrequire github.com/elauven/vela v0.0.0\n\nreplace github.com/elauven/vela => " + filepath.ToSlash(compilerRoot) + "\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(components, "Page.go"), renderer, 0o644); err != nil {
		t.Fatal(err)
	}
	mainSource := `package main
import (
    "os"
    "example.com/ssrfixture/components"
)
func main() {
    if err := components.RenderPageAt(os.Stdout, components.PageProps{Title: "world", Set: map[string]bool{"title": true}}, components.PageSlots{}, "vela-page-instance"); err != nil {
        panic(err)
    }
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
	if got, want := string(output), "<!--e:vela-page-instance:0--><main>Hello <!--v:vela-page-instance:0-->world<!--/v:vela-page-instance:0--></main><!--/e:vela-page-instance:0--><script type=\"application/json\" id=\"vela-state-vela-page-instance\">{\"version\":1,\"component\":\"vela-page-instance\",\"props\":{\"Title\":\"world\"},\"state\":{},\"incomplete\":false}</script>"; got != want {
		t.Fatalf("SSR output=%q want %q", got, want)
	}
}

func TestGeneratedSSRRendererRunsNamedFormatterFromTemplateAndDerived(t *testing.T) {
	sfc, err := parseSFC(`<script>
const prefix = 'Hello, ';
let suffix = '!';
function label(value) { return prefix + value + suffix; }
export let title = '';
let shown = $derived(label(title));
</script><template><main><p>{label(title)}</p><p>{shown}</p></main></template>`)
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
	module := "module example.com/ssrformatter\n\ngo 1.22\n\nrequire github.com/elauven/vela v0.0.0\n\nreplace github.com/elauven/vela => " + filepath.ToSlash(compilerRoot) + "\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(components, "Page.go"), renderer, 0o644); err != nil {
		t.Fatal(err)
	}
	mainSource := `package main
import (
    "os"
    "example.com/ssrformatter/components"
)
func main() {
    if err := components.RenderPageAt(os.Stdout, components.PageProps{Title: "Vela", Set: map[string]bool{"title": true}}, components.PageSlots{}, "vela-page-instance"); err != nil {
        panic(err)
    }
}
`
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(mainSource), 0o644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "run", ".")
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("formatter Go renderer: %v\n%s", err, output)
	}
	if got := strings.Count(string(output), ">Hello, Vela!<"); got != 2 {
		t.Fatalf("formatter output count=%d want 2:\n%s", got, output)
	}
}

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
	module := "module example.com/ssrfixture\n\ngo 1.22\n\nrequire github.com/elauven/vela v0.0.0\n\nreplace github.com/elauven/vela => " + filepath.ToSlash(compilerRoot) + "\n"
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
    if err := components.RenderPageAt(os.Stdout, empty, components.PageSlots{}, "empty"); err != nil { panic(err) }
    fmt.Print("\n---\n")
    full := components.PageProps{Items: []any{"first"}, Set: map[string]bool{"items": true}}
    if err := components.RenderPageAt(os.Stdout, full, components.PageSlots{}, "full"); err != nil { panic(err) }
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
	module := "module example.com/ssrnullish\n\ngo 1.22\n\nrequire github.com/elauven/vela v0.0.0\n\nreplace github.com/elauven/vela => " + filepath.ToSlash(compilerRoot) + "\n"
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
    if err := components.RenderPageAt(os.Stdout, props, components.PageSlots{}, "nullish"); err != nil { panic(err) }
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
	for _, want := range []string{">null<", ">undefined<", ">1,,,2,,3<", `"Value":null`} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("nullish SSR output misses %q:\n%s", want, output)
		}
	}
}
