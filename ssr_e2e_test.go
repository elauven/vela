package main

import (
	"os"
	"os/exec"
	"path/filepath"
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
    if err := components.RenderPage(os.Stdout, components.PageProps{Title: "world"}, components.PageSlots{}); err != nil {
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
	if got, want := string(output), "<main>Hello <!--v0-->world<!--/v0--></main><script type=\"application/json\" id=\"vela-state-vela-page\">{\"component\":\"vela-page\",\"props\":{\"Title\":\"world\"},\"state\":{},\"incomplete\":false}</script>"; got != want {
		t.Fatalf("SSR output=%q want %q", got, want)
	}
}
