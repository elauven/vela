package ssr

import (
	"github.com/elauven/vela/internal/project"

	"bytes"
	"crypto/sha256"
	"encoding/hex"
	goast "go/ast"
	goparser "go/parser"
	"go/token"
	"go/types"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
)

// SSRProjectLayout keeps the three output products independent: client modules
// remain consumable by the existing browser toolchain, while Go renderers and
// extracted scoped CSS never share a directory with generated JavaScript.
type SSRProjectLayout struct {
	Root       string
	ModulePath string
	ClientDir  string
	ServerDir  string
	StylesDir  string
}

// SSRArtifactMode states the public runtime contract of one generated SSR
// component. A validation-only hydrate entry is deliberately not called
// executable: consumers must not assume it has a live set/destroy instance.
type SSRArtifactMode string

const (
	SSRArtifactExecutable SSRArtifactMode = "executable"
	SSRArtifactStaticOnly SSRArtifactMode = "static-only"
	// SSRArtifactClientBoundary denotes an explicitly declared client-owned
	// region. It is not an executable component controller: the structural
	// boundary lowerer supplies its deterministic server fallback in Task 030.
	SSRArtifactClientBoundary SSRArtifactMode = "client-boundary"
)

// SSRArtifactManifest is emitted next to browser artifacts so a CLI consumer
// can distinguish an executable hydrate client from static markup validation.
// Paths are relative to the client output directory.
type SSRArtifactManifest struct {
	Component         string          `json:"component"`
	Mode              SSRArtifactMode `json:"mode"`
	Hydrate           string          `json:"hydrate"`
	HydrationClient   string          `json:"hydrationClient,omitempty"`
	StaticEventBridge string          `json:"staticEventBridge,omitempty"`
}

const SSRStateDiagnosticCode = "VELA-SSR-STATE"

func NewSSRProjectLayout(outRoot string) SSRProjectLayout {
	return NewSSRProjectLayoutWithModule(outRoot, "vela/generated/server")
}

func NewSSRProjectLayoutWithModule(outRoot, modulePath string) SSRProjectLayout {
	root := filepath.Clean(outRoot)
	return SSRProjectLayout{
		Root:       root,
		ModulePath: strings.TrimSuffix(strings.TrimSpace(modulePath), "/"),
		ClientDir:  filepath.Join(root, "client"),
		ServerDir:  filepath.Join(root, "server", "components"),
		StylesDir:  filepath.Join(root, "styles"),
	}
}

func (layout SSRProjectLayout) ServerPath(src, srcRoot string) string {
	return mirrorGoPath(src, srcRoot, layout.ServerDir)
}

func (layout SSRProjectLayout) PropsPath(src, srcRoot string) string {
	return mirrorGoPathWithSuffix(src, srcRoot, layout.ServerDir, "_props.go")
}

func (layout SSRProjectLayout) PackageName(src, srcRoot string) string {
	rel := componentPackageDir(src, srcRoot)
	if rel == "." {
		return "components"
	}
	return goFileStem(filepath.Base(rel))
}

func (layout SSRProjectLayout) ImportPath(src, srcRoot string) string {
	rel := componentPackageDir(src, srcRoot)
	if rel == "." {
		return path.Join(layout.ModulePath, "components")
	}
	return path.Join(layout.ModulePath, "components", filepath.ToSlash(rel))
}

// componentPackageDir is the directory of src relative to srcRoot. Both are
// made absolute first: a child component linked by an absolute path inside
// a project given as a relative --project directory used to fail filepath.Rel
// and fall back to the root package (task 031).
func componentPackageDir(src, srcRoot string) string {
	dir, err1 := filepath.Abs(filepath.Dir(src))
	root, err2 := filepath.Abs(srcRoot)
	if err1 != nil || err2 != nil {
		return "."
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return "."
	}
	return rel
}

func (layout SSRProjectLayout) StylePath(src, srcRoot string) string {
	return project.MirrorPath(src, srcRoot, layout.StylesDir, ".css")
}

// UseSSRRuntimeModule makes generated code and host checks import the Go
// runtime from module (vela.ssr.json "runtime.module", R-12.1b) and returns
// the function that restores the default.
func UseSSRRuntimeModule(module string) func() {
	if module == "" {
		return func() {}
	}
	ssrRuntimePath = module
	return func() { ssrRuntimePath = SSRRuntimeImportPath }
}

// ValidateSSRProjectPackages checks every materialized generated-server Go
// package as a graph. Renderer files deliberately rely on their preserved
// *_props.go sibling, and components from another directory import its Go
// package, so isolated-file checking cannot catch every conflict.
func ValidateSSRProjectPackages(layout SSRProjectLayout) error {
	return ValidateSSRProjectPackagesWithHosts(layout, nil)
}

func ValidateSSRProjectPackagesWithHosts(layout SSRProjectLayout, hostTypes map[string]*types.Package) error {
	packages := map[string]map[string][]byte{}
	if err := collectSSRGoPackages(filepath.Join(layout.Root, "server", "modules"), path.Join(layout.ModulePath, "modules"), packages); err != nil && !os.IsNotExist(err) {
		return err
	}
	err := filepath.Walk(layout.ServerDir, func(filename string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info == nil || info.IsDir() || filepath.Ext(filename) != ".go" {
			return nil
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		dir := filepath.Dir(filename)
		rel, err := filepath.Rel(layout.ServerDir, dir)
		if err != nil {
			return err
		}
		importPath := path.Join(layout.ModulePath, "components")
		if rel != "." {
			importPath = path.Join(importPath, filepath.ToSlash(rel))
		}
		if packages[importPath] == nil {
			packages[importPath] = map[string][]byte{}
		}
		packages[importPath][filename] = data
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := validateGeneratedSSRGoGraph(packages, hostTypes); err != nil {
		return err
	}
	return nil
}

// SSRHydrationBlockedError reports why a component fell back to static-only
// output before the error of that fallback, so the build names the construct
// that blocks hydration rather than an event handler the static bridge cannot
// carry. It unwraps to the fallback error.
type SSRHydrationBlockedError struct {
	Reason   string
	Fallback error
}

// SSRHydrationBlockedDiagnosticCode is reported when a component cannot use
// executable hydration and its static-only fallback is not possible either.
const SSRHydrationBlockedDiagnosticCode = "VELA-SSR-HYDRATION"

func ssrProjectPropsSource(plan *SSRRenderPlan, path, packageName string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err == nil && !SSRPropsGenerated(data) {
		return data, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	file, err := BuildSSRGoPropsFile(plan, packageName)
	if err != nil {
		return nil, err
	}
	body, err := FormatSSRGoFile(file)
	if err != nil {
		return nil, err
	}
	generated := ssrPropsFile(plan.ComponentName, body)
	if bytes.Equal(generated, data) {
		return data, nil
	}
	if err := os.WriteFile(path, generated, 0o644); err != nil {
		return nil, err
	}
	return generated, nil
}

// ssrPropsMarker begins the line of a generated props sidecar that holds the
// hash of the lines after it. While the hash matches, nobody has edited the
// file and the build rewrites it when the props change; an edited file, or
// one without the line, belongs to the developer and is never overwritten.
const ssrPropsMarker = "// vela:props "

func ssrPropsFile(component string, body []byte) []byte {
	var b bytes.Buffer
	b.WriteString("// The props of " + component + ", generated by vela. vela updates this file\n")
	b.WriteString("// when the props change. Edit it to extend the struct: from then on it is\n")
	b.WriteString("// yours and vela never overwrites it.\n")
	rest := append([]byte("\n"), body...)
	b.WriteString(ssrPropsMarker + ssrPropsHash(rest) + "\n")
	b.Write(rest)
	return b.Bytes()
}

func ssrPropsHash(rest []byte) string {
	sum := sha256.Sum256(rest)
	return hex.EncodeToString(sum[:8])
}

// SSRPropsGenerated reports a props sidecar as the build wrote it.
func SSRPropsGenerated(data []byte) bool {
	start := bytes.Index(data, []byte(ssrPropsMarker))
	if start < 0 || (start > 0 && data[start-1] != '\n') {
		return false
	}
	end := bytes.IndexByte(data[start:], '\n')
	if end < 0 {
		return false
	}
	hash := string(data[start+len(ssrPropsMarker) : start+end])
	return hash == ssrPropsHash(data[start+end+1:])
}

// mirrorGoPath preserves the component directory tree but applies Go source
// filename convention to every emitted component file.
func mirrorGoPath(src, srcRoot, outRoot string) string {
	return mirrorGoPathWithSuffix(src, srcRoot, outRoot, ".go")
}

func mirrorGoPathWithSuffix(src, srcRoot, outRoot, suffix string) string {
	rel, err := filepath.Rel(srcRoot, src)
	if err != nil {
		rel = filepath.Base(src)
	}
	dir := filepath.Dir(rel)
	stem := strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
	name := goFileStem(stem) + suffix
	if dir == "." {
		return filepath.Join(outRoot, name)
	}
	return filepath.Join(outRoot, dir, name)
}

func goFileStem(name string) string {
	runes := []rune(name)
	var out []rune
	separator := true
	for index, current := range runes {
		if !unicode.IsLetter(current) && !unicode.IsDigit(current) {
			if !separator && len(out) > 0 {
				out = append(out, rune('_'))
				separator = true
			}
			continue
		}
		upper := unicode.IsUpper(current)
		previousLowerOrDigit := index > 0 && (unicode.IsLower(runes[index-1]) || unicode.IsDigit(runes[index-1]))
		nextLower := index+1 < len(runes) && unicode.IsLower(runes[index+1])
		previousUpper := index > 0 && unicode.IsUpper(runes[index-1])
		if upper && !separator && (previousLowerOrDigit || (previousUpper && nextLower)) {
			out = append(out, rune('_'))
		}
		out = append(out, unicode.ToLower(current))
		separator = false
	}
	stem := strings.Trim(string(out), "_")
	if stem == "" {
		return "component"
	}
	return stem
}

// ssrPropsWithoutPresence reports whether the preserved props sidecar at path
// declares typeName without the Set map[string]bool presence field. A missing
// file is generated with the field, so it reports false (task 060).
func ssrPropsWithoutPresence(path, typeName string) (bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	file, err := goparser.ParseFile(token.NewFileSet(), path, data, goparser.SkipObjectResolution)
	if err != nil {
		// The Go build reports the broken sidecar; keep the reflective check.
		return true, nil
	}
	for _, decl := range file.Decls {
		gen, ok := decl.(*goast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec := spec.(*goast.TypeSpec)
			structType, ok := typeSpec.Type.(*goast.StructType)
			if typeSpec.Name.Name != typeName || !ok || typeSpec.TypeParams != nil {
				continue
			}
			for _, field := range structType.Fields.List {
				for _, name := range field.Names {
					if name.Name == "Set" {
						return !ssrIsStringBoolMap(field.Type), nil
					}
				}
			}
			return true, nil
		}
	}
	return true, nil
}

func ssrIsStringBoolMap(expr goast.Expr) bool {
	m, ok := expr.(*goast.MapType)
	if !ok {
		return false
	}
	key, keyOK := m.Key.(*goast.Ident)
	value, valueOK := m.Value.(*goast.Ident)
	return keyOK && valueOK && key.Name == "string" && value.Name == "bool"
}
