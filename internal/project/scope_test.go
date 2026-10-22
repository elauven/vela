package project

import (
	"os"
	"path/filepath"
	"testing"
)

// One component has one scope class however its path is spelled: relative,
// with ./, with native separators, or absolute inside the working directory.
func TestScopeIDIgnoresThePathSpelling(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	want := ScopeID("src/App.vela")
	for _, path := range []string{"./src/App.vela", filepath.Join("src", "App.vela"), filepath.Join(cwd, "src", "App.vela")} {
		if got := ScopeID(path); got != want {
			t.Errorf("ScopeID(%q) = %s, want %s", path, got, want)
		}
	}
	if ScopeID("src/Other.vela") == want {
		t.Error("two components share a scope class")
	}
}
