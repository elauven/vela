package gen

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/elauven/vela/ssr_runtime/pkgpath"
)

// Scaffold creates the mirrored directory and a load.go with the loader
// signatures of L-1 for the route given by its id (P-12) or its directory
// relative to the routes directory, or for every route directory without
// Go files when route is "". It never overwrites a file (P-18). It also
// adds the #app/* entry to the "imports" of package.json (R-11), which it
// reports as "package.json" among the created files.
func Scaffold(root, route string) ([]string, []Diagnostic) {
	cfg, diags := LoadConfig(root)
	if len(diags) > 0 {
		return nil, diags
	}
	var created []string
	if _, err := os.Stat(filepath.Join(cfg.Root, "package.json")); err == nil {
		changed, err := AddAppImport(cfg)
		if err != nil {
			return nil, []Diagnostic{{File: "package.json", Code: CodeConfig, Message: err.Error(), Help: "add " + AppImportEntry(cfg)}}
		}
		if changed {
			created = append(created, "package.json")
		}
	}
	tree, diags := Scan(cfg)
	if tree.Root == nil {
		return nil, diags
	}
	var dirs []*Dir
	if route != "" {
		rel := strings.Trim(route, "/")
		d := tree.byRel[rel]
		if d == nil || (d.Page == "" && d.Layout == "") {
			return nil, []Diagnostic{{File: tree.projectRel(rel) + "/", Code: CodeConfig, Message: fmt.Sprintf("%s is not a route directory with a Page.vela or a Layout.vela", route)}}
		}
		dirs = []*Dir{d}
	} else {
		for _, d := range tree.Dirs {
			if (d.Page != "" || d.Layout != "") && d.Load == nil {
				dirs = append(dirs, d)
			}
		}
	}
	for _, d := range dirs {
		rel := path.Join(cfg.Server, d.GoRel, "load.go")
		file := cfg.abs(rel)
		if _, err := os.Stat(file); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return created, []Diagnostic{{File: rel, Code: CodeConfig, Message: err.Error()}}
		}
		if err := os.WriteFile(file, scaffoldSource(tree, d), 0o644); err != nil {
			return created, []Diagnostic{{File: rel, Code: CodeConfig, Message: err.Error()}}
		}
		created = append(created, rel)
	}
	return created, nil
}

// LoaderPackage is the package name of a mirrored directory (L-1): the
// encoded directory name with dots replaced by underscores.
func LoaderPackage(cfg Config, goRel string) string {
	if goRel == "." {
		return pkgpath.PackageName(path.Base(cfg.Server))
	}
	return pkgpath.PackageName(path.Base(goRel))
}

func scaffoldSource(t *Tree, d *Dir) []byte {
	var b bytes.Buffer
	pkg := LoaderPackage(t.Config, d.GoRel)
	fmt.Fprintf(&b, "// Package %s holds the loaders of %s (docs/APP.md L-1).\npackage %s\n\n", pkg, t.projectRel(d.Rel), pkg)
	b.WriteString("import (\n\t\"context\"\n\n\t\"" + AppImportPath + "\"\n)\n")
	for _, loader := range []struct{ name, file string }{{"Layout", d.Layout}, {"Page", d.Page}} {
		if loader.file == "" {
			continue
		}
		fmt.Fprintf(&b, "\n// %s loads the data prop of %s. Its result reaches the\n// component as JSON.\n", loader.name, loader.file)
		fmt.Fprintf(&b, "func %s(ctx context.Context, r *app.Request) (any, error) {\n\treturn nil, nil\n}\n", loader.name)
	}
	return b.Bytes()
}
