package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOutputCollisions(t *testing.T) {
	root := filepath.FromSlash("/p/src")
	at := func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }
	for _, c := range []struct {
		name    string
		sources []string
		second  string // "" for no collision
		output  string
		exact   bool
	}{
		{"component and module, case only", []string{"Card.vela", "card.js"}, "card.js", "card.js", false},
		{"component and module, exact", []string{"Card.vela", "Card.js"}, "Card.vela", "Card.js", true},
		{"two modules", []string{"util.ts", "util.js"}, "util.ts", "util.js", true},
		{"two components", []string{"card.vela", "Card.vela"}, "card.vela", "card.js", false},
		{"directories differ in case", []string{"Pages/Home.vela", "pages/home.js"}, "pages/home.js", "pages/home.js", false},
		{"different directories", []string{"a/Card.vela", "b/card.js"}, "", "", false},
		{"declaration file is no module", []string{"Card.vela", "card.d.ts"}, "", "", false},
		{"distinct names", []string{"Card.vela", "cards.js"}, "", "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			var sources []string
			for _, s := range c.sources {
				sources = append(sources, at(s))
			}
			got := OutputCollisions(sources, root)
			if c.second == "" {
				if len(got) != 0 {
					t.Fatalf("collisions %+v, want none", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("collisions %+v, want one", got)
			}
			if got[0].Second != at(c.second) || got[0].Output != c.output || got[0].Exact != c.exact {
				t.Errorf("got %+v, want second %s, output %s, exact %v", got[0], c.second, c.output, c.exact)
			}
		})
	}
}

func TestSameFile(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.js"), filepath.Join(dir, "b.js")
	for _, f := range []string{a, b} {
		if err := os.WriteFile(f, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if !SameFile(a, a) || SameFile(a, b) || SameFile(a, filepath.Join(dir, "missing.js")) {
		t.Error("SameFile")
	}
}
