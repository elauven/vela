package vite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The manifest of Vite's backend integration guide: two entries sharing a
// chunk with CSS, and a dynamic import that is not preloaded.
const manifest = `{
  "_shared-B7PI925R.js": {"file": "assets/shared-B7PI925R.js", "name": "shared", "css": ["assets/shared-ChJ_j-JJ.css"]},
  "_shared-ChJ_j-JJ.css": {"file": "assets/shared-ChJ_j-JJ.css", "src": "_shared-ChJ_j-JJ.css"},
  "baz.js": {"file": "assets/baz-B2H3sXNv.js", "name": "baz", "src": "baz.js", "isDynamicEntry": true},
  "views/bar.js": {"file": "assets/bar-gkvgaI9m.js", "name": "bar", "src": "views/bar.js", "isEntry": true, "imports": ["_shared-B7PI925R.js"], "dynamicImports": ["baz.js"]},
  "views/foo.js": {"file": "assets/foo-BRBmoGS9.js", "name": "foo", "src": "views/foo.js", "isEntry": true, "imports": ["_shared-B7PI925R.js"], "css": ["assets/foo-5UjPuW-k.css"]}
}`

func TestProductionTags(t *testing.T) {
	assets, err := Parse([]byte(manifest), "/")
	if err != nil {
		t.Fatal(err)
	}
	got, err := assets.Tags("views/foo.js")
	if err != nil {
		t.Fatal(err)
	}
	want := `<link rel="stylesheet" href="/assets/foo-5UjPuW-k.css">` +
		`<link rel="stylesheet" href="/assets/shared-ChJ_j-JJ.css">` +
		`<link rel="modulepreload" href="/assets/shared-B7PI925R.js">` +
		`<script type="module" src="/assets/foo-BRBmoGS9.js"></script>`
	if got != want {
		t.Errorf("Tags(foo) =\n%s\nwant\n%s", got, want)
	}

	// Two entries name a shared file once; a base without a trailing slash
	// is a directory.
	assets, err = Parse([]byte(manifest), "/static")
	if err != nil {
		t.Fatal(err)
	}
	got, err = assets.Tags("views/foo.js", "/views/bar.js")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(got, "shared-B7PI925R.js"); n != 1 || !strings.Contains(got, `src="/static/assets/bar-gkvgaI9m.js"`) || strings.Contains(got, "baz") {
		t.Errorf("Tags(foo, bar) = %s", got)
	}

	if _, err := assets.Tags("baz.js"); err == nil || !strings.Contains(err.Error(), "entries: views/bar.js, views/foo.js") {
		t.Errorf("a dynamic entry as entry: %v", err)
	}
}

func TestDevTags(t *testing.T) {
	got, err := Dev("http://localhost:5173/").Tags("src/client.js")
	if err != nil {
		t.Fatal(err)
	}
	want := `<script type="module" src="http://localhost:5173/@vite/client"></script>` +
		`<script type="module" src="http://localhost:5173/src/client.js"></script>`
	if got != want {
		t.Errorf("dev Tags = %s", got)
	}
}

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	if _, err := Load(path, "/"); err == nil || !strings.Contains(err.Error(), "build.manifest") {
		t.Errorf("missing manifest: %v", err)
	}
	if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if assets, err := Load(path, "/"); err != nil || len(assets.entries()) != 2 {
		t.Errorf("Load: %v", err)
	}
}
