package gen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Rendering rules of task 092: Prerender, Cache and RenderClient.

const pageLoader = "func Page(ctx context.Context, r *app.Request) (any, error) { return nil, nil }\n"

func TestRenderRulesAreGenerated(t *testing.T) {
	root := writeProject(t, map[string]string{
		"src/routes/Page.vela":        page,
		"src/routes/docs/Page.vela":   page,
		"src/routes/notes/Page.vela":  "<script>\n  let w = $state(window.innerWidth);\n</script>\n<template><p>{w}</p></template>\n",
		"server/routes/load.go":       loaderImports + pageLoader + "var Cache = app.Cache{}\n",
		"server/routes/docs/load.go":  strings.Replace(loaderImports, "package p", "package docs", 1) + pageLoader + "var Prerender = app.Prerender{}\n",
		"server/routes/notes/load.go": strings.Replace(loaderImports, "package p", "package notes", 1) + pageLoader + "const Render = app.RenderClient\n",
	})
	result, diags := Generate(root)
	if len(diags) > 0 {
		t.Fatal(Error(diags))
	}
	if len(result.Edited) != 1 || result.Edited[0] != "src/routes/notes/Page.vela" {
		t.Fatalf("edited %v", result.Edited)
	}
	page, _ := os.ReadFile(filepath.Join(root, "src", "routes", "notes", "Page.vela"))
	if !strings.HasPrefix(string(page), "<script context=\"client\">\n  let w") {
		t.Errorf("the page after the edit:\n%s", page)
	}
	table, _ := os.ReadFile(filepath.Join(root, "internal", "appgen", "routes_gen.go"))
	for _, want := range []string{
		"Prerender: &server_routes_docs.Prerender",
		"Cache: &server_routes.Cache",
		"Render: server_routes_notes.Render",
		`"routes/notes/Page": vela.ClientComponent("routes/notes/Page")`,
	} {
		if !strings.Contains(string(table), want) {
			t.Errorf("routes_gen.go misses %s:\n%s", want, table)
		}
	}
	routes, _ := os.ReadFile(filepath.Join(root, "src", "_app", "routes.js"))
	if !strings.Contains(string(routes), `"render":"client"`) {
		t.Errorf("routes.js has no client route:\n%s", routes)
	}
	// Idempotent: the second run edits nothing.
	again, diags := Generate(root)
	if len(diags) > 0 || len(again.Edited) != 0 || len(again.Written) != 0 {
		t.Errorf("second run: %+v %v", again, diags)
	}
	// Without a script the page gets an empty one.
	if got := makeClientOnly("<template><p>x</p></template>\n"); got != "<script context=\"client\"></script>\n\n<template><p>x</p></template>\n" {
		t.Errorf("makeClientOnly without a script: %q", got)
	}
	// A JSON-LD script of the template is not the component's.
	src := "<template><vela:head><script type=\"application/ld+json\">{}</script></vela:head></template>\n<script>\n</script>\n"
	if got := makeClientOnly(src); !strings.Contains(got, "</template>\n<script context=\"client\">") {
		t.Errorf("makeClientOnly after the template: %q", got)
	}
}

func TestRenderRuleDiagnostics(t *testing.T) {
	guard := "func Guard(ctx context.Context, r *app.Request) error { return nil }\n"
	actions := "func Actions() app.Actions { return nil }\n"
	for _, tc := range []struct {
		name, load, layoutLoad, want string
	}{
		{"prerender and client", pageLoader + "var Prerender = app.Prerender{}\nconst Render = app.RenderClient\n", "", "rendered only in the browser"},
		{"prerender and actions", pageLoader + actions + "var Prerender = app.Prerender{}\n", "", "has actions"},
		{"prerender and cache", pageLoader + "var Prerender = app.Prerender{}\nvar Cache = app.Cache{}\n", "", "Cache does not apply"},
		{"prerender under a guard", pageLoader + "var Prerender = app.Prerender{}\n", guard, "below the guard"},
		{"cache as a constant", pageLoader + "const Cache = 1\n", "", "must be a variable of type app.Cache"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"src/routes/Layout.vela":  page,
				"src/routes/a/Page.vela":  page,
				"server/routes/a/load.go": strings.Replace(loaderImports, "package p", "package a", 1) + tc.load,
			}
			if tc.layoutLoad != "" {
				files["server/routes/load.go"] = loaderImports + tc.layoutLoad
			}
			_, diags := Load(writeProject(t, files))
			if len(diags) == 0 || diags[0].Code != CodeRender || !strings.Contains(diags[0].Message, tc.want) {
				t.Errorf("diagnostics %v, want %s %q", diags, CodeRender, tc.want)
			}
		})
	}
}

func TestClientPageWithoutRenderClientWarns(t *testing.T) {
	root := writeProject(t, map[string]string{
		"src/routes/Page.vela": "<script context=\"client\">\n</script>\n<template><p>x</p></template>\n",
	})
	result, diags := Generate(root)
	if len(diags) > 0 {
		t.Fatal(Error(diags))
	}
	if len(result.Notes) == 0 || !strings.Contains(result.Notes[0], "[warning] [VELA-APP-RENDER]") {
		t.Errorf("notes %v", result.Notes)
	}
	if len(result.Edited) != 0 {
		t.Errorf("edited %v", result.Edited)
	}
}
