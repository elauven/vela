package gen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/elauven/vela/app/typegen"
)

// Route types of a TypeScript project (task 087, docs/TYPESCRIPT.md TS-8):
// for each route directory, <out>/types/<directory>/$types.d.ts declares
// PageData, LayoutData, Params, Form and the props of its components, and
// <out>/tsconfig.json maps `./$types` of the directory to it with rootDirs.
// The data types come from the Go loaders, converted by package typegen in
// a program of the project's module, so a changed Go result type changes
// them (TS-8.3).

// TypesDir is the directory of the route types in the out directory.
const TypesDir = "types"

// TypegenImportPath is the package the types program calls.
const TypegenImportPath = AppImportPath + "/typegen"

// typesOutputs are the route type files and the tsconfig of a TypeScript
// project.
func (m *Model) typesOutputs() ([]Output, error) {
	loaders, forms := m.typeKeys()
	result := typegen.Result{Types: map[string]string{}}
	if len(loaders)+len(forms) > 0 {
		var err error
		if result, err = m.runTypegen(loaders, forms); err != nil {
			return nil, err
		}
	}
	marker := "// " + Marker + "\n"
	var outs []Output
	data := marker + "// The Go types of the loaders' results (docs/APP.md L-2), as JSON.\n\n" + result.Declarations
	if result.Declarations == "" {
		data += "export {};\n"
	}
	outs = append(outs, Output{Path: path.Join(m.Config.Out, TypesDir, "$data.d.ts"), Data: []byte(data)})
	for _, d := range m.typeDirs() {
		outs = append(outs, Output{Path: m.typesFile(d), Data: m.routeTypes(d, result)})
	}
	outs = append(outs, Output{Path: path.Join(m.Config.Out, "tsconfig.json"), Data: m.tsconfig()})
	return outs, nil
}

// typeDirs are the route directories with a page or a layout.
func (m *Model) typeDirs() []*Dir {
	seen := map[*Dir]bool{}
	var dirs []*Dir
	add := func(d *Dir) {
		if d != nil && !seen[d] && (d.Page != "" || d.Layout != "") {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	for _, r := range m.Routes {
		for _, l := range r.Chain {
			add(l.Dir)
		}
	}
	for _, l := range m.NotFound {
		add(l.Dir)
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Rel < dirs[j].Rel })
	return dirs
}

// typesFile mirrors the route directory under <out>/types, relative to the
// project root (as rootDirs needs).
func (m *Model) typesFile(d *Dir) string {
	dir := m.Config.Routes
	if d.Rel != "" {
		dir = path.Join(dir, d.Rel)
	}
	return path.Join(m.Config.Out, TypesDir, dir, "$types.d.ts")
}

type typeKey struct{ key, pkg, name string }

// typeKeys lists the loaders and the Form types of the model.
func (m *Model) typeKeys() (loaders, forms []typeKey) {
	seen := map[string]bool{}
	for _, d := range m.typeDirs() {
		load := d.Load
		if load == nil {
			continue
		}
		for _, l := range []struct {
			ok   bool
			name string
		}{{load.Page && d.Page != "", "Page"}, {load.Layout && d.Layout != "", "Layout"}} {
			if key := load.Dir + ":" + l.name; l.ok && !seen[key] {
				seen[key] = true
				loaders = append(loaders, typeKey{key, load.Dir, l.name})
			}
		}
		if key := load.Dir + ":Form"; load.Form && d.Page != "" && !seen[key] {
			seen[key] = true
			forms = append(forms, typeKey{key, load.Dir, "Form"})
		}
	}
	return loaders, forms
}

// runTypegen runs the types program in the project's module.
func (m *Model) runTypegen(loaders, forms []typeKey) (typegen.Result, error) {
	var result typegen.Result
	dir, err := os.MkdirTemp(m.Config.Root, ".vela-app-types-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(dir)
	aliases := map[string]string{}
	var imports []string
	alias := func(pkg string) string {
		if a, ok := aliases[pkg]; ok {
			return a
		}
		a := "p" + strconv.Itoa(len(aliases))
		aliases[pkg] = a
		imports = append(imports, "\t"+a+" "+strconv.Quote(path.Join(m.Config.GoModule, pkg)))
		return a
	}
	var b bytes.Buffer
	b.WriteString("// " + Marker + "\n\n// The types of the loaders, for vela-app gen (task 087).\npackage main\n\n")
	var body bytes.Buffer
	body.WriteString("func main() {\n\ttypegen.Main(map[string]any{\n")
	for _, l := range loaders {
		fmt.Fprintf(&body, "\t\t%q: %s.%s,\n", l.key, alias(l.pkg), l.name)
	}
	body.WriteString("\t}, map[string]reflect.Type{\n")
	for _, f := range forms {
		fmt.Fprintf(&body, "\t\t%q: reflect.TypeFor[%s.%s](),\n", f.key, alias(f.pkg), f.name)
	}
	body.WriteString("\t})\n}\n")
	b.WriteString("import (\n\t\"reflect\"\n\n\t\"" + TypegenImportPath + "\"\n" + strings.Join(imports, "\n") + "\n)\n\n")
	b.Write(body.Bytes())
	if err := os.WriteFile(filepath.Join(dir, "main.go"), b.Bytes(), 0o644); err != nil {
		return result, err
	}
	cmd := exec.Command("go", "run", "./"+filepath.Base(dir))
	cmd.Dir = m.Config.Root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return result, fmt.Errorf("the route types need the loaders to compile (go run): %v\n%s", err, strings.TrimSpace(stderr.String()))
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return result, fmt.Errorf("the types program: %v", err)
	}
	return result, nil
}

// routeTypes is the $types.d.ts of a route directory.
func (m *Model) routeTypes(d *Dir, result typegen.Result) []byte {
	var b bytes.Buffer
	b.WriteString("// " + Marker + "\n")
	fmt.Fprintf(&b, "// The types of the route components of %s (docs/TYPESCRIPT.md TS-8.2):\n", m.typesRouteDir(d))
	b.WriteString("// import type { PageProps } from './$types';\n\n")
	b.WriteString("import type { Route } from '@govela/app/client';\n")
	data := strings.Repeat("../", strings.Count(path.Dir(m.typesFile(d)), "/")-strings.Count(path.Join(m.Config.Out, TypesDir), "/"))
	if data == "" {
		data = "./"
	}
	b.WriteString("import type * as $data from '" + data + "$data';\n\n")
	b.WriteString("/** The parameters of the path (P-2): [[optional]] ones may be absent. */\n")
	b.WriteString("export type Params = " + m.paramsType(d) + ";\n")
	typeOf := func(name string, ok bool) string {
		if !ok || d.Load == nil {
			return "null"
		}
		if t, found := result.Types[d.Load.Dir+":"+name]; found {
			return t
		}
		return "unknown"
	}
	if d.Layout != "" {
		b.WriteString("\n/** The result of Layout in load.go, as JSON (L-2); null without a loader. */\n")
		b.WriteString("export type LayoutData = " + typeOf("Layout", d.Load != nil && d.Load.Layout) + ";\n")
		b.WriteString("\nexport interface LayoutProps {\n  data: LayoutData;\n  route: Route<Params>;\n}\n")
	}
	if d.Page != "" {
		b.WriteString("\n/** The result of Page in load.go, as JSON (L-2); null without a loader. */\n")
		b.WriteString("export type PageData = " + typeOf("Page", d.Load != nil && d.Load.Page) + ";\n")
		form := "unknown"
		switch {
		case d.Load != nil && d.Load.Form:
			form = element(typeOf("Form", true)) + " | null"
		case d.Load == nil || !d.Load.Actions:
			form = "null"
		}
		b.WriteString("\n/** The value an action passed to app.Fail: `type Form` of the package types it. */\n")
		b.WriteString("export type Form = " + form + ";\n")
		b.WriteString("\nexport interface PageProps {\n  data: PageData;\n  form: Form;\n  route: Route<Params>;\n}\n")
	}
	return b.Bytes()
}

func element(s string) string {
	if strings.Contains(s, " | ") {
		return "(" + s + ")"
	}
	return s
}

func (m *Model) typesRouteDir(d *Dir) string {
	if d.Rel == "" {
		return m.Config.Routes
	}
	return path.Join(m.Config.Routes, d.Rel)
}

// paramsType is the parameters of the path to d.
func (m *Model) paramsType(d *Dir) string {
	var dirs []*Dir
	for up := d; up != nil; up = up.Parent {
		dirs = append([]*Dir{up}, dirs...)
	}
	var members []string
	for _, dir := range dirs {
		switch dir.Segment.Kind {
		case SegParam, SegRest:
			members = append(members, dir.Segment.Name+": string")
		case SegOptional:
			members = append(members, dir.Segment.Name+"?: string")
		}
	}
	if len(members) == 0 {
		return "{}"
	}
	return "{ " + strings.Join(members, "; ") + " }"
}

// tsconfig is <out>/tsconfig.json: the configuration of TS-10.2 with the
// rootDirs that map `./$types` of a route directory to its types. The
// project's tsconfig.json extends it.
func (m *Model) tsconfig() []byte {
	up := strings.Repeat("../", strings.Count(m.Config.Out, "/")+1)
	config := map[string]any{
		"//": Marker + " The project's tsconfig.json extends it (docs/TYPESCRIPT.md TS-8).",
		"compilerOptions": map[string]any{
			"target":                     "ES2022",
			"module":                     "ESNext",
			"moduleResolution":           "bundler",
			"strict":                     true,
			"noEmit":                     true,
			"isolatedModules":            true,
			"verbatimModuleSyntax":       true,
			"erasableSyntaxOnly":         true,
			"allowImportingTsExtensions": true,
			"useDefineForClassFields":    true,
			"lib":                        []string{"ES2022", "DOM", "DOM.Iterable"},
			"types":                      []string{"@govela/runtime/ambient"},
			"rootDirs":                   []string{up, "./" + TypesDir},
		},
	}
	data, _ := json.MarshalIndent(config, "", "  ")
	return append(data, '\n')
}
