package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elauven/vela/internal/pkgpath"
	public "github.com/elauven/vela/ssr_runtime/pkgpath"
)

// codeAfterDoc is a Go file from its package clause on.
func codeAfterDoc(t *testing.T, file string) string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	text := "\n" + string(data)
	i := strings.Index(text, "\npackage ")
	if i < 0 {
		t.Fatalf("%s has no package clause", file)
	}
	return text[i:]
}

// TestPkgpathCopiesMatch: the compiler encodes directories exactly as the
// public ssr_runtime/pkgpath the app layer uses (task 069).
func TestPkgpathCopiesMatch(t *testing.T) {
	for _, name := range []string{"pkgpath.go", "pkgpath_test.go"} {
		if codeAfterDoc(t, filepath.Join("internal", "pkgpath", name)) != codeAfterDoc(t, filepath.Join("ssr_runtime", "pkgpath", name)) {
			t.Errorf("internal/pkgpath/%s differs from ssr_runtime/pkgpath/%s; copy it", name, name)
		}
	}
	for _, name := range []string{"[id]", "[[l]]", "[...r]", "[a=b]", "(g)", "x-y", "id.param", "[a]-[b]", "$", "2x", "go"} {
		a, errA := pkgpath.Encode(name)
		b, errB := public.Encode(name)
		if a != b || (errA == nil) != (errB == nil) || pkgpath.PackageName(a) != public.PackageName(b) {
			t.Errorf("%q: %q %v / %q %v", name, a, errA, b, errB)
		}
	}
}
