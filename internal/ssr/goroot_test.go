package ssr

import (
	"go/types"
	"strings"
	"testing"
)

// panicImporter stands for the export data of another Go version, which
// go/importer cannot decode: it panics.
type panicImporter struct{ calls int }

func (p *panicImporter) Import(path string) (*types.Package, error) {
	p.calls++
	panic("export data version 4 is greater than maximum supported version 2")
}

// The type check of generated Go must not depend on the installed Go: a
// release binary of vela meets the standard library of any Go version.
func TestGeneratedGoTypeCheckDoesNotReadTheInstalledGo(t *testing.T) {
	installed := &panicImporter{}
	loader := &stdImporter{packages: map[string]*types.Package{}, real: installed}
	for _, path := range []string{"io", "context", "fmt", "encoding/json"} {
		if _, err := loader.Import(path); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	if _, err := ssrRuntimeTypesPackage(loader); err != nil {
		t.Fatalf("runtime types: %v", err)
	}
	if installed.calls != 0 {
		t.Fatalf("the installed Go was read %d times", installed.calls)
	}
	// Another package comes from the installed Go; its failure is an error.
	if _, err := loader.Import("strings"); err == nil || !strings.Contains(err.Error(), "another Go version") {
		t.Fatalf("strings from an unreadable Go: %v", err)
	}
}

// Host code reads the installed Go first and falls back to the built-in
// descriptions when it cannot.
func TestHostImporterFallsBackToBuiltInPackages(t *testing.T) {
	loader := &stdImporter{packages: map[string]*types.Package{}, real: &panicImporter{}, installedFirst: true}
	pkg, err := loader.Import("context")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.Scope().Lookup("Context") == nil {
		t.Fatal("no context.Context")
	}
}

func TestGeneratedGoValidatesWithBuiltInStandardLibrary(t *testing.T) {
	src := `package components

import (
	"context"
	"fmt"
	"io"

	vela "` + SSRRuntimeImport() + `"
)

var _ = vela.` + SSRRuntimeContract + `

func Render(ctx context.Context, w io.Writer) error {
	w = vela.WithContext(w, ctx)
	if err := vela.WriteString(w, "<p>"); err != nil {
		return fmt.Errorf("render: %w", err)
	}
	return nil
}
`
	if err := ValidateGeneratedSSRGoPackage(map[string][]byte{"app.go": []byte(src)}); err != nil {
		t.Fatal(err)
	}
}
