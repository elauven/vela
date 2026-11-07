package gen

import (
	"path/filepath"
	"strings"
	"testing"
)

const testConfig = `{ "project": "src", "out": "out", "ssr": true, "ssrModule": "example.com/site/out/server" }`

const page = "<template><p>page</p></template>\n"

const loaderImports = "package p\n\nimport (\n\t\"context\"\n\n\t\"github.com/elauven/vela/app\"\n)\n\n"

// writeProject writes a project with the given files (slash paths) next to
// vela.config.json and go.mod.
func writeProject(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	base := map[string]string{"vela.config.json": testConfig, "go.mod": "module example.com/site\n\ngo 1.25.0\n"}
	for name, data := range files {
		base[name] = data
	}
	for name, data := range base {
		if data == "\x00" { // a deleted base file
			continue
		}
		writeFile(t, filepath.Join(root, filepath.FromSlash(name)), data)
	}
	return root
}

func TestParseSegment(t *testing.T) {
	for _, tc := range []struct {
		name string
		want DirSegment
		err  string
	}{
		{"users", DirSegment{Kind: SegStatic, Name: "users"}, ""},
		{"about-us.v2", DirSegment{Kind: SegStatic, Name: "about-us.v2"}, ""},
		{"[id]", DirSegment{Kind: SegParam, Name: "id"}, ""},
		{"[id=int]", DirSegment{Kind: SegParam, Name: "id", Matcher: "int"}, ""},
		{"[[lang]]", DirSegment{Kind: SegOptional, Name: "lang"}, ""},
		{"[...path]", DirSegment{Kind: SegRest, Name: "path"}, ""},
		{"(admin)", DirSegment{Kind: SegGroup, Name: "admin"}, ""},
		{"[a]-[b]", DirSegment{}, "mixed segments"},
		{"file.[ext]", DirSegment{}, "mixed segments"},
		{"[my-id]", DirSegment{}, "parameter name must be an identifier"},
		{"[id=in-t]", DirSegment{}, "matcher name must be an identifier"},
		{"a b", DirSegment{}, "invalid character"},
		{"x.param", DirSegment{}, "reserved suffix"},
	} {
		got, err := ParseSegment(tc.name)
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%s: error %v, want %q", tc.name, err, tc.err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s: %+v %v, want %+v", tc.name, got, err, tc.want)
		}
	}
}

// TestScanErrors covers every generator rule with its position (task 074).
func TestScanErrors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  []string // substrings of the formatted diagnostics, one per diagnostic
	}{
		{"mixed segment (P-3)", map[string]string{
			"src/routes/Page.vela":             page,
			"src/routes/[a]-[b]/Page.vela":     page,
			"src/routes/a b/Page.vela":         page,
			"src/routes/files.param/Page.vela": page,
		}, []string{
			"src/routes/[a]-[b]/: [error] [VELA-APP-DIRECTORY] directory \"[a]-[b]\": mixed segments",
			"src/routes/a b/: [error] [VELA-APP-DIRECTORY] directory \"a b\": invalid character",
			"src/routes/files.param/: [error] [VELA-APP-DIRECTORY] directory \"files.param\": reserved suffix",
		}},
		{"other component (P-1)", map[string]string{
			"src/routes/Page.vela":       page,
			"src/routes/users/Card.vela": page,
			"src/routes/users/util.js":   "export const x = 1;\n",
		}, []string{"src/routes/users/Card.vela:1:1: [error] [VELA-APP-FILE] only Page.vela, Layout.vela and Error.vela"}},
		{"Go file in routes (P-17)", map[string]string{
			"src/routes/Page.vela":            page,
			"src/routes/users/[id]/Page.vela": page,
			"src/routes/users/[id]/load.go":   "package x\n",
		}, []string{"src/routes/users/[id]/load.go:1:1: [error] [VELA-APP-FILE] a Go file in the routes directory", "help: move it to the mirrored Go tree: server/routes/users/id.param/load.go"}},
		{"rest not last (P-4)", map[string]string{
			"src/routes/[...path]/Page.vela":       page,
			"src/routes/[...path]/edit/Page.vela":  page,
			"src/routes/[...path]/(g)/Layout.vela": page,
		}, []string{"src/routes/[...path]/edit/: [error] [VELA-APP-REST] [...path] must be the last directory of a path, but edit is below it"}},
		{"repeated parameter (P-5)", map[string]string{
			"src/routes/[id]/(g)/[[id]]/Page.vela": page,
		}, []string{"src/routes/[id]/(g)/[[id]]/: [error] [VELA-APP-PARAM] parameter \"id\" is already declared by src/routes/[id]"}},
		{"same rank (P-8)", map[string]string{
			"src/routes/users/[id]/Page.vela":     page,
			"src/routes/users/[slug]/x/Page.vela": page,
			"src/routes/a/[x=int]/Page.vela":      page,
			"src/routes/a/[y=int]/b/Page.vela":    page,
			"src/routes/c/[x=int]/Page.vela":      page,
			"src/routes/c/[y=slug]/Page.vela":     page,
			"src/routes/(g)/d/[p]/Page.vela":      page,
			"src/routes/(h)/d/[q]/e/Page.vela":    page,
		}, []string{
			"src/routes/(h)/d/[q]/: [error] [VELA-APP-RANK] [p] and [q] are candidates of the same rank at one level",
			"src/routes/a/[y=int]/: [error] [VELA-APP-RANK] [x=int] and [y=int]",
			"src/routes/users/[slug]/: [error] [VELA-APP-RANK] [id] and [slug]",
		}},
		{"same pattern (P-9)", map[string]string{
			"src/routes/(a)/x/Page.vela":          page,
			"src/routes/(b)/x/Page.vela":          page,
			"src/routes/about/Page.vela":          page,
			"src/routes/[[lang]]/about/Page.vela": page,
		}, []string{
			"src/routes/(b)/x/Page.vela:1:1: [error] [VELA-APP-DUPLICATE] routes /(a)/x and /(b)/x have the same URL pattern /x through a group",
			"src/routes/about/Page.vela:1:1: [error] [VELA-APP-DUPLICATE] routes /[[lang]]/about and /about have the same URL pattern /about through an optional segment",
		}},
		{"unknown matcher (P-14)", map[string]string{
			"src/routes/[id=hex]/Page.vela": page,
		}, []string{"src/routes/[id=hex]/: [error] [VELA-APP-MATCHER] unknown matcher \"hex\"", "help: use one of int, slug, uuid, or create server/params/hex/match.go and src/params/hex.js"}},
		{"matcher halves (P-14)", map[string]string{
			"src/routes/[a=onlygo]/Page.vela":   page,
			"src/routes/x/[b=onlyjs]/Page.vela": page,
			"server/params/onlygo/match.go":     "package onlygo\n\nfunc Match(s string) bool { return true }\n",
			"src/params/onlyjs.js":              "export function match(s) { return true; }\n",
			"server/params/bad/match.go":        "package bad\n\nfunc Match(s string) (bool, error) { return true, nil }\n",
			"src/params/bad.js":                 "export const other = 1;\n",
			"server/params/int/match.go":        "package int\n\nfunc Match(s string) bool { return true }\n",
			"src/params/int.js":                 "export function match(s) { return true; }\n",
			"server/params/flat.go":             "package params\n",
		}, []string{
			"server/params/bad/match.go:3:6: [error] [VELA-APP-MATCHER] matcher \"bad\" has no func Match(segment string) bool",
			"server/params/flat.go:1:1: [error] [VELA-APP-MATCHER] a matcher is a package of its own",
			"server/params/onlygo/: [error] [VELA-APP-MATCHER] matcher \"onlygo\" has no JavaScript half",
			"src/params/bad.js:1:1: [error] [VELA-APP-MATCHER] matcher \"bad\" does not export match",
			"src/params/int.js:1:1: [error] [VELA-APP-MATCHER] matcher \"int\" shadows the built-in matcher",
			"src/params/onlyjs.js:1:1: [error] [VELA-APP-MATCHER] matcher \"onlyjs\" has no Go half",
		}},
		{"stale mirrored directory (P-17)", map[string]string{
			"src/routes/users/[id]/Page.vela":      page,
			"server/routes/users/id.param/load.go": loaderImports + "func Page(ctx context.Context, r *app.Request) (any, error) { return nil, nil }\n",
			"server/routes/posts/load.go":          "package posts\n",
			"server/routes/users/bad[x]/load.go":   "package x\n",
			"server/routes/users/empty/README.md":  "no Go files\n",
		}, []string{
			"server/routes/posts/: [error] [VELA-APP-STALE] a mirrored Go directory without a route directory",
			"help: it mirrors src/routes/posts, which does not exist",
			"server/routes/users/bad[x]/: [error] [VELA-APP-STALE]",
		}},
		{"loaders (L-1, L-7)", map[string]string{
			"src/routes/users/Layout.vela":    page,
			"src/routes/users/[id]/Page.vela": page,
			"server/routes/users/load.go": loaderImports +
				"func Layout(ctx context.Context, r *app.Request) (any, error) { return nil, nil }\n\n" +
				"func Page(ctx context.Context, r *app.Request) (any, error) { return nil, nil }\n\n" +
				"const Render = app.RenderData\n",
			"server/routes/users/id.param/load.go": loaderImports +
				"func Page(r *app.Request) (any, error) { return nil, nil }\n\n" +
				"func Layout(ctx context.Context, r app.Request) (any, error) { return nil, nil }\n\n" +
				"const Deps = 1\n",
			"server/routes/users/id.param/other.go": "package p\n\nimport ctx \"context\"\nimport a \"github.com/elauven/vela/app\"\n\nvar Deps = a.Deps{}\n\nfunc helper(c ctx.Context, r *a.Request) {}\n",
		}, []string{
			"server/routes/users/id.param/load.go:9:6: [error] [VELA-APP-LOADER] loader Page has the wrong signature",
			"help: declare it as func Page(ctx context.Context, r *app.Request) (T, error)",
			"server/routes/users/id.param/load.go:11:6: [error] [VELA-APP-LOADER] loader Layout has no Layout.vela in src/routes/users/[id]",
			"server/routes/users/id.param/load.go:13:7: [error] [VELA-APP-LOADER] Deps must be a variable",
			"server/routes/users/load.go:11:6: [error] [VELA-APP-LOADER] loader Page has no Page.vela in src/routes/users",
			"server/routes/users/load.go:13:7: [error] [VELA-APP-LOADER] Render is declared without a Page.vela",
		}},
		{"actions", map[string]string{
			"src/routes/users/Layout.vela":    page,
			"src/routes/users/[id]/Page.vela": page,
			"server/routes/users/actions.go":  loaderImports + "func Actions() app.Actions { return nil }\n",
			"server/routes/users/id.param/actions.go": loaderImports +
				"func Actions(ctx context.Context) map[string]any { return nil }\n",
		}, []string{
			"server/routes/users/actions.go:9:6: [error] [VELA-APP-LOADER] Actions has no Page.vela in src/routes/users; actions belong to a page",
			"server/routes/users/id.param/actions.go:9:6: [error] [VELA-APP-LOADER] Actions has the wrong signature",
			"help: declare it as func Actions() app.Actions",
		}},
		{"guards (G-1)", map[string]string{
			"src/routes/users/Layout.vela":    page,
			"src/routes/users/[id]/Page.vela": page,
			"src/routes/(g)/x/Page.vela":      page,
			"server/routes/users/guard.go":    loaderImports + "func Guard(ctx context.Context, r *app.Request) error { return nil }\n",
			"server/routes/users/id.param/guard.go": loaderImports +
				"func Guard(ctx context.Context, r *app.Request) (any, error) { return nil, nil }\n",
			"server/routes/g.group/guard.go": loaderImports + "func Guard(ctx context.Context, r *app.Request) error { return nil }\n",
		}, []string{
			"server/routes/g.group/guard.go:9:6: [error] [VELA-APP-GUARD] Guard has no Layout.vela or Page.vela in src/routes/(g)",
			"server/routes/users/id.param/guard.go:9:6: [error] [VELA-APP-GUARD] Guard has the wrong signature",
			"help: declare it as func Guard(ctx context.Context, r *app.Request) error",
		}},
		{"Go syntax error", map[string]string{
			"src/routes/Page.vela":  page,
			"server/routes/load.go": "package routes\n\nfunc Page( {\n",
		}, []string{"server/routes/load.go:3:"}},
		{"no ssrModule", map[string]string{
			"vela.config.json":     `{ "project": "src" }`,
			"src/routes/Page.vela": page,
		}, []string{"vela.config.json: [error] [VELA-APP-CONFIG] \"ssrModule\" is not set"}},
		{"no routes", map[string]string{"src/App.vela": page}, []string{"src/routes/: [error] [VELA-APP-CONFIG] the routes directory does not exist"}},
		{"unknown config field", map[string]string{
			"vela.app.json":        `{ "route": "src/pages" }`,
			"src/routes/Page.vela": page,
		}, []string{"vela.app.json: [error] [VELA-APP-CONFIG] json: unknown field \"route\""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := writeProject(t, tc.files)
			_, diags := Load(root)
			got := Error(diags).Error()
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q in\n%s", want, got)
				}
			}
			if count := countPrefixed(tc.want); len(diags) != count {
				t.Errorf("%d diagnostics, want %d:\n%s", len(diags), count, got)
			}
		})
	}
}

// countPrefixed counts the expectations that start a diagnostic (the ones
// that are not a help line or a bare position).
func countPrefixed(wants []string) int {
	n := 0
	for _, w := range wants {
		if strings.Contains(w, "[error]") || strings.HasSuffix(w, ":") {
			n++
		}
	}
	return n
}

// TestLoaderHead: a loader that calls Head(), or whose package has a helper
// that does, runs for the head of a data navigation that keeps its level
// (N-4); the other loader of the same package does not.
func TestLoaderHead(t *testing.T) {
	sig := "(ctx context.Context, r *app.Request) (any, error)"
	for _, tc := range []struct {
		name               string
		load               string
		layoutHead, pgHead bool
	}{
		{"page only", "func Layout" + sig + " { return nil, nil }\nfunc Page" + sig + " { r.Head().Title = \"x\"; return nil, nil }\n", false, true},
		{"layout only", "func Layout" + sig + " { r.Head().SetMeta(app.Meta{}); return nil, nil }\nfunc Page" + sig + " { return nil, nil }\n", true, false},
		{"helper", "func Layout" + sig + " { return nil, title(r) }\nfunc Page" + sig + " { return nil, nil }\nfunc title(r *app.Request) error { r.Head().Title = \"x\"; return nil }\n", true, true},
		{"none", "func Layout" + sig + " { return nil, nil }\nfunc Page" + sig + " { _ = r.Header(); return nil, nil }\n", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := writeProject(t, map[string]string{
				"src/routes/Layout.vela": page,
				"src/routes/Page.vela":   page,
				"server/routes/load.go":  loaderImports + tc.load,
			})
			m, diags := Load(root)
			if len(diags) > 0 {
				t.Fatal(Error(diags))
			}
			chain := m.Routes[0].Chain
			if chain[0].Head != tc.layoutHead || chain[1].Head != tc.pgHead {
				t.Errorf("Head: layout %v page %v, want %v %v", chain[0].Head, chain[1].Head, tc.layoutHead, tc.pgHead)
			}
		})
	}
}
