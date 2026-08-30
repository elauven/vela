package main

import (
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
)

// SSRProjectLayout keeps the three output products independent: client modules
// remain consumable by the existing browser toolchain, while Go renderers and
// extracted scoped CSS never share a directory with generated JavaScript.
type SSRProjectLayout struct {
	Root      string
	ClientDir string
	ServerDir string
	StylesDir string
}

func NewSSRProjectLayout(outRoot string) SSRProjectLayout {
	root := filepath.Clean(outRoot)
	return SSRProjectLayout{
		Root:      root,
		ClientDir: filepath.Join(root, "client"),
		ServerDir: filepath.Join(root, "server", "components"),
		StylesDir: filepath.Join(root, "styles"),
	}
}

func (layout SSRProjectLayout) clientPath(src, srcRoot string) string {
	return mirrorPath(src, srcRoot, layout.ClientDir, ".js")
}

func (layout SSRProjectLayout) serverPath(src, srcRoot string) string {
	return mirrorPath(src, srcRoot, layout.ServerDir, ".go")
}

func (layout SSRProjectLayout) stylePath(src, srcRoot string) string {
	return mirrorPath(src, srcRoot, layout.StylesDir, ".css")
}

// buildSSRProject adds server and styles products around the established CSR
// project build. The caller owns user-facing progress and exit handling.
func buildSSRProject(files []string, srcRoot, outRoot string, opts CompileOptions, parallel bool) int {
	layout := NewSSRProjectLayout(outRoot)
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
	plan, err := BuildSSRRenderPlan(componentName(src), sfc, scopeID)
	if err != nil {
		return err
	}
	file, err := BuildSSRGoFile(plan, "components")
	if err != nil {
		return err
	}
	source, err := FormatSSRGoFile(file)
	if err != nil {
		return err
	}
	if err := ValidateGeneratedSSRGo(layout.serverPath(src, srcRoot), source); err != nil {
		return err
	}
	serverPath := layout.serverPath(src, srcRoot)
	if err := os.MkdirAll(filepath.Dir(serverPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(serverPath, source, 0o644); err != nil {
		return err
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
