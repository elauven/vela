package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elauven/vela/app/internal/gen"
	"github.com/elauven/vela/app/internal/testkit"
)

// TestRouteTypes (task 087, docs/TYPESCRIPT.md TS-8): vela-app gen writes the
// types of every route of a TypeScript project from its Go loaders,
// `vela check --types` accepts the pages that use them, and renaming a
// field of a loader's result makes it report the page's use of it (TS-8.3).
func TestRouteTypes(t *testing.T) {
	testkit.RequireGo(t)
	repo := testkit.RepoRoot(t)
	ts := filepath.Join(repo, "node_modules", "typescript")
	if _, err := os.Stat(filepath.Join(ts, "lib", "typescript.js")); err != nil {
		t.Skip("typescript not installed (npm ci)")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}
	root := testkit.CopyProject(t, filepath.Join(repo, "app", "testdata", "ts-app"))
	testkit.UseLocalModules(t, root)
	testkit.Run(t, root, "go", "mod", "tidy")
	scope := filepath.Join(root, "node_modules", "@govela")
	if err := os.MkdirAll(scope, 0o755); err != nil {
		t.Fatal(err)
	}
	for link, target := range map[string]string{
		filepath.Join(root, "node_modules", "typescript"): ts,
		filepath.Join(scope, "runtime"):                   filepath.Join(repo, "packages", "runtime"),
		filepath.Join(scope, "app"):                       filepath.Join(repo, "packages", "app"),
	} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	if _, diags := gen.Generate(root); len(diags) > 0 {
		t.Fatal(gen.Error(diags))
	}
	read := func(rel string) string {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	user := read("src/_app/types/src/routes/users/[id=int]/$types.d.ts")
	for _, want := range []string{
		"export type Params = { id: string };",
		"export type PageData = $data.Detail;",
		"export type Form = $data.Form | null;",
		"route: Route<Params>;",
	} {
		if !strings.Contains(user, want) {
			t.Errorf("users/[id=int]/$types.d.ts misses %q:\n%s", want, user)
		}
	}
	data := read("src/_app/types/$data.d.ts")
	for _, want := range []string{"export interface Detail {\n  user: User;\n  manager: User | null;\n  tags: string[];\n}", "export interface User {\n  name: string;\n  joined: string;\n}", "field?: string;"} {
		if !strings.Contains(data, want) {
			t.Errorf("$data.d.ts misses %q:\n%s", want, data)
		}
	}
	if about := read("src/_app/types/src/routes/[[lang]]/about/$types.d.ts"); !strings.Contains(about, "export type Params = { lang?: string };") || !strings.Contains(about, "export type PageData = null;") || !strings.Contains(about, "export type Form = null;") {
		t.Errorf("[[lang]]/about:\n%s", about)
	}
	if app := read("src/_app/VelaApp.vela"); !strings.HasPrefix(app, "<script lang=\"ts\">") {
		t.Errorf("VelaApp.vela is not TypeScript:\n%s", app)
	}

	check := func() []string {
		cmd := exec.Command(testkit.VelaBinary(t), "check", "--types", "--format=json", "--project", "src")
		cmd.Dir = root
		out, _ := cmd.Output()
		var report struct {
			Diagnostics []struct {
				File, Code, Message string
				Line, Column        int
			} `json:"diagnostics"`
		}
		if err := json.Unmarshal(out, &report); err != nil {
			t.Fatalf("vela check --types: %v\n%s", err, out)
		}
		var typed []string
		for _, d := range report.Diagnostics {
			if strings.HasPrefix(d.Code, "TS") || strings.HasPrefix(d.Code, "VELA-TS") {
				typed = append(typed, d.File+" "+d.Code+" "+d.Message)
			}
		}
		return typed
	}
	if typed := check(); len(typed) > 0 {
		t.Fatalf("the TypeScript app has type errors:\n%s", strings.Join(typed, "\n"))
	}

	// A renamed Go field is a type error in the page.
	load := filepath.Join(root, "server", "routes", "users", "id.int.param", "load.go")
	src := read("server/routes/users/id.int.param/load.go")
	src = strings.Replace(src, "Name   string    `json:\"name\"`", "FullName string  `json:\"fullName\"`", 1)
	src = strings.ReplaceAll(src, "User{Name:", "User{FullName:")
	if err := os.WriteFile(load, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, diags := gen.Generate(root); len(diags) > 0 {
		t.Fatal(gen.Error(diags))
	}
	typed := check()
	joined := strings.Join(typed, "\n")
	if len(typed) == 0 || !strings.Contains(joined, "Page.vela TS2339 Property 'name' does not exist") {
		t.Errorf("the renamed field is not reported:\n%s", joined)
	}
}

// TestRouteTypesOfTheExample: the route types of examples/app, a
// JavaScript project, generated as for a TypeScript one, are declarations
// TypeScript accepts (every loader, the store's types, optional and rest
// parameters).
func TestRouteTypesOfTheExample(t *testing.T) {
	testkit.RequireGo(t)
	repo := testkit.RepoRoot(t)
	tsc := filepath.Join(repo, "node_modules", "typescript", "bin", "tsc")
	if _, err := os.Stat(tsc); err != nil {
		t.Skip("typescript not installed (npm ci)")
	}
	root := testkit.CopyProject(t, filepath.Join(repo, "examples", "app"))
	testkit.UseLocalModules(t, root)
	// The generated route table does not exist yet: -e keeps tidy going.
	testkit.Run(t, root, "go", "mod", "tidy", "-e")
	testkit.WriteFile(t, filepath.Join(root, "vela.app.json"), `{ "typescript": true }`)
	if _, diags := gen.Generate(root); len(diags) > 0 {
		t.Fatal(gen.Error(diags))
	}
	scope := filepath.Join(root, "node_modules", "@govela")
	if err := os.MkdirAll(scope, 0o755); err != nil {
		t.Fatal(err)
	}
	for link, target := range map[string]string{
		filepath.Join(scope, "runtime"): filepath.Join(repo, "packages", "runtime"),
		filepath.Join(scope, "app"):     filepath.Join(repo, "packages", "app"),
	} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	files, err := filepath.Glob(filepath.Join(root, "src", "_app", "types", "src", "routes", "*", "*", "$types.d.ts"))
	if err != nil || len(files) < 3 {
		t.Fatalf("route types: %v %v", files, err)
	}
	// preserveSymlinks: the linked packages resolve each other in this
	// node_modules.
	testkit.WriteFile(t, filepath.Join(root, "tsconfig.json"), `{ "extends": "./src/_app/tsconfig.json", "compilerOptions": { "preserveSymlinks": true }, "include": ["src/_app/types"] }`)
	cmd := exec.Command("node", tsc, "-p", ".")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("tsc over the route types: %v\n%s", err, out)
	}
	data, err := os.ReadFile(filepath.Join(root, "src", "_app", "types", "$data.d.ts"))
	if err != nil || !strings.Contains(string(data), "export interface User") {
		t.Errorf("$data.d.ts: %v\n%s", err, data)
	}
}
