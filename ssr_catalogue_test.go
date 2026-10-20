package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSSRMatrixBuiltins keeps docs/SSR_TRANSLATION_MATRIX.md §2 generated
// from the catalogue (V-11.1a): the section matches, every admitted
// built-in belongs to an implemented row and every implemented row's
// built-ins are admitted, and every implemented row has oracle rows.
func TestSSRMatrixBuiltins(t *testing.T) {
	path := filepath.Join("docs", "SSR_TRANSLATION_MATRIX.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	start := strings.Index(text, "## 2. Built-ins\n")
	end := strings.Index(text[start:], "\n---\n")
	if start < 0 || end < 0 {
		t.Fatal("matrix §2 not found")
	}
	end += start
	want := RenderSSRCatalogue()
	if text[start:end+1] != want {
		if os.Getenv("VELA_UPDATE_MATRIX") == "1" {
			if err := os.WriteFile(path, []byte(text[:start]+want+text[end+1:]), 0o644); err != nil {
				t.Fatal(err)
			}
		} else {
			t.Errorf("matrix §2 differs from internal/ssr/ssr_catalogue.go; run VELA_UPDATE_MATRIX=1 go test -run TestSSRMatrixBuiltins .")
		}
	}

	admitted := map[string]bool{}
	for _, name := range SSRCatalogueNames() {
		admitted[name] = true
	}
	listed := map[string]bool{}
	for _, section := range SSRCatalogue {
		for _, row := range section.Rows {
			for _, name := range row.Names {
				if row.Status != "implemented" {
					t.Errorf("%s: %s is listed in a row with status %q", section.Title, name, row.Status)
				}
				if listed[name] {
					t.Errorf("%s is listed twice", name)
				}
				listed[name] = true
				if !admitted[name] {
					t.Errorf("%s is listed as implemented but not admitted by ssr_builtins.go", name)
				}
			}
		}
	}
	for name := range admitted {
		if !listed[name] {
			t.Errorf("%s is admitted by ssr_builtins.go but no matrix row lists it", name)
		}
	}

	var rows []ssrExpressionOracleRow
	oracle, err := os.ReadFile(filepath.Join("testdata", "ssr_expression_oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(oracle, &rows); err != nil {
		t.Fatal(err)
	}
	groups := map[string]bool{}
	for _, row := range rows {
		groups[row.Group] = true
	}
	for _, section := range SSRCatalogue {
		for _, row := range section.Rows {
			if row.Status != "implemented" {
				continue
			}
			if len(row.Oracles) == 0 {
				t.Errorf("%s: implemented row %s has no oracle group", section.Title, row.Builtin)
			}
			for _, group := range row.Oracles {
				if !groups[group] && !strings.HasPrefix(group, "Test") {
					t.Errorf("%s: oracle group %q of %s has no rows", section.Title, group, row.Builtin)
				}
			}
		}
	}
}
