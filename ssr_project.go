package main

import (
	"crypto/md5"
	"fmt"
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

func (layout SSRProjectLayout) clientPath(src, srcRoot string) string {
	return mirrorPath(src, srcRoot, layout.ClientDir, ".js")
}

func (layout SSRProjectLayout) serverPath(src, srcRoot string) string {
	return mirrorGoPath(src, srcRoot, layout.ServerDir)
}

func (layout SSRProjectLayout) propsPath(src, srcRoot string) string {
	return mirrorGoPathWithSuffix(src, srcRoot, layout.ServerDir, "_props.go")
}

func (layout SSRProjectLayout) packageName(src, srcRoot string) string {
	rel, err := filepath.Rel(srcRoot, filepath.Dir(src))
	if err != nil || rel == "." {
		return "components"
	}
	return goFileStem(filepath.Base(rel))
}

func (layout SSRProjectLayout) importPath(src, srcRoot string) string {
	rel, err := filepath.Rel(srcRoot, filepath.Dir(src))
	if err != nil || rel == "." {
		return path.Join(layout.ModulePath, "components")
	}
	return path.Join(layout.ModulePath, "components", filepath.ToSlash(rel))
}

func (layout SSRProjectLayout) stylePath(src, srcRoot string) string {
	return mirrorPath(src, srcRoot, layout.StylesDir, ".css")
}
func (layout SSRProjectLayout) eventBridgePath(src, srcRoot string) string {
	return mirrorPath(src, srcRoot, layout.ClientDir, ".ssr-events.js")
}

// buildSSRProject adds server and styles products around the established CSR
// project build. The caller owns user-facing progress and exit handling.
func buildSSRProject(files []string, srcRoot, outRoot string, opts CompileOptions, parallel bool) int {
	layout := NewSSRProjectLayoutWithModule(outRoot, opts.SSRModule)
	errors := buildProject(files, srcRoot, layout.ClientDir, opts, parallel)
	for _, src := range files {
		if err := compileSSRProjectFile(src, srcRoot, layout); err != nil {
			fmt.Fprintf(os.Stderr, "  x  %s: SSR: %v\n", relOrBase(src, srcRoot), err)
			errors++
			continue
		}
		fmt.Fprintf(compilerStatusWriter(opts), "  +  %-40s -> server/styles\n", relOrBase(src, srcRoot))
	}
	return errors
}

func compileSSRProjectFile(src, srcRoot string, layout SSRProjectLayout) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	sfc, err := parseSFC(string(data))
	if err != nil {
		return fmt.Errorf("parse component: %w", err)
	}
	hash := md5.Sum([]byte(src))
	scopeID := fmt.Sprintf("vela-%x", hash[:3])
	analysis := analyzeScriptAST(sfc.ScriptAST)
	links, err := ResolveSSRLocalComponentLinks(src, srcRoot, analysis)
	if err != nil {
		return err
	}
	links = LinkSSRComponentPackages(links, srcRoot, layout)
	plan, err := BuildSSRRenderPlanWithAnalysisAndLinks(componentName(src), sfc, analysis, scopeID, links)
	if err != nil {
		return err
	}
	bridge, err := BuildSSRStaticEventBridge(plan)
	if err != nil {
		return err
	}
	file, err := BuildSSRGoRendererFile(plan, layout.packageName(src, srcRoot))
	if err != nil {
		return err
	}
	source, err := FormatSSRGoFile(file)
	if err != nil {
		return err
	}
	serverPath := layout.serverPath(src, srcRoot)
	if err := os.MkdirAll(filepath.Dir(serverPath), 0o755); err != nil {
		return err
	}
	propsPath := layout.propsPath(src, srcRoot)
	propsSource, err := ssrProjectPropsSource(plan, propsPath, layout.packageName(src, srcRoot))
	if err != nil {
		return err
	}
	if err := ValidateGeneratedSSRGoPackage(map[string][]byte{serverPath: source, propsPath: propsSource}); err != nil {
		return err
	}
	if err := os.WriteFile(serverPath, source, 0o644); err != nil {
		return err
	}
	if bridge != nil {
		bridgePath := layout.eventBridgePath(src, srcRoot)
		if err := os.MkdirAll(filepath.Dir(bridgePath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(bridgePath, []byte(PrintJS(bridge, PrintPretty)), 0o644); err != nil {
			return err
		}
	}
	if plan.ScopedCSS == "" {
		return nil
	}
	stylePath := layout.stylePath(src, srcRoot)
	if err := os.MkdirAll(filepath.Dir(stylePath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(stylePath, []byte(plan.ScopedCSS), 0o644)
}

func ssrProjectPropsSource(plan *SSRRenderPlan, path, packageName string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		return data, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	file, err := BuildSSRGoPropsFile(plan, packageName)
	if err != nil {
		return nil, err
	}
	data, err = FormatSSRGoFile(file)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return nil, err
	}
	return data, nil
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
