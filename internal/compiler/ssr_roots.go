package compiler

import (
	"github.com/elauven/vela/internal/project"
	"github.com/elauven/vela/internal/ssr"

	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// The client manifest (docs/HYDRATION_ARCHITECTURE.md A-8.8, task 041); the
// root modes it records are computed in internal/ssr.

// SSRManifest is vela.manifest.json (A-8.8): the root mode and client
// module of every component.
// SSRManifest is vela.manifest.json of a project build (A-8.8, R-12.2f):
// every component with its client module (and root mode in SSR builds),
// and the package imports of the generated and copied modules, so that an
// application or a tool can build an import map or check its
// dependencies. The compiler never resolves or bundles the packages.
type SSRManifest struct {
	Version    int                    `json:"version"`
	Components []SSRManifestComponent `json:"components"`
	Modules    []ManifestModule       `json:"modules,omitempty"`
	Packages   []string               `json:"packages,omitempty"`
}

type SSRManifestComponent struct {
	Component string `json:"component"`
	Source    string `json:"source"`
	Mode      string `json:"mode,omitempty"`
	Module    string `json:"module"`
}

// ManifestModule lists the package specifiers one output module imports.
type ManifestModule struct {
	Module   string   `json:"module"`
	Packages []string `json:"packages"`
}

// writeSSRManifest writes the manifest of an SSR build: root modes and the
// package imports collected while building the client.
func writeSSRManifest(files []string, srcRoot string, layout ssr.SSRProjectLayout, modes map[string]bool, imports *packageImports) error {
	if modes == nil {
		modes = map[string]bool{}
	}
	return writeProjectManifest(files, srcRoot, layout.ClientDir, modes, imports)
}

// writeProjectManifest writes vela.manifest.json into outRoot; modes is nil
// for CSR builds, which have no root modes.
func writeProjectManifest(files []string, srcRoot, outRoot string, modes map[string]bool, imports *packageImports) error {
	manifest := SSRManifest{Version: 1, Components: []SSRManifestComponent{}}
	for _, src := range files {
		rel, err := filepath.Rel(srcRoot, src)
		if err != nil {
			rel = filepath.Base(src)
		}
		module, err := filepath.Rel(outRoot, project.MirrorPath(src, srcRoot, outRoot, ".js"))
		if err != nil {
			module = filepath.Base(src)
		}
		mode := ""
		if modes != nil {
			mode = "static"
			if ssr.SSRInteractive(modes, src) {
				mode = "interactive"
			}
		}
		manifest.Components = append(manifest.Components, SSRManifestComponent{
			Component: project.ComponentName(src), Source: filepath.ToSlash(rel), Mode: mode, Module: filepath.ToSlash(module),
		})
	}
	sort.Slice(manifest.Components, func(i, j int) bool { return manifest.Components[i].Source < manifest.Components[j].Source })
	manifest.Modules, manifest.Packages = imports.manifest(outRoot)
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outRoot, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outRoot, "vela.manifest.json"), append(encoded, '\n'), 0o644)
}
