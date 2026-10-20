package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// Root modes and the client manifest (docs/HYDRATION_ARCHITECTURE.md
// A-8.1–A-8.8, task 041). A component is static as a root when nothing in
// its component tree can change after rendering: no DOM or component
// events, no bind:, no $effect, no lifecycle hook. Rendered as a root it
// writes plain HTML without markers or payload and needs no JavaScript.
// Inside an interactive parent it keeps the hydration markup, because the
// parent's client code creates it.

// ssrOwnInteractive reports whether a component itself needs client code.
func ssrOwnInteractive(nodes []Node, analysis *Analysis) bool {
	if analysis != nil && (len(analysis.Effects) > 0 || len(analysis.Lifecycles) > 0) {
		return true
	}
	return ssrTemplateInteractive(nodes)
}

func ssrTemplateInteractive(nodes []Node) bool {
	for _, node := range nodes {
		var attrs []*Attr
		switch n := node.(type) {
		case *ElementNode:
			attrs = n.Attrs
		case *ComponentNode:
			attrs = n.Attrs
		}
		for _, attr := range attrs {
			if attr.IsEvent || attr.IsTwoWay || attr.IsBindThis {
				return true
			}
		}
		interactive := false
		ForEachChildList(node, func(children []Node) {
			interactive = interactive || ssrTemplateInteractive(children)
		})
		if interactive {
			return true
		}
	}
	return false
}

// ssrRootModes decides the root mode of every component of a project:
// interactive when the component or any component it uses is.
func ssrRootModes(files []string, srcRoot string) map[string]bool {
	own := map[string]bool{}
	children := map[string][]string{}
	for _, src := range files {
		key := canonicalPath(src)
		data, err := os.ReadFile(src)
		if err != nil {
			own[key] = true
			continue
		}
		sfc, err := ParseSFC(string(data))
		if err != nil || sfc.TemplateAST == nil {
			own[key] = true
			continue
		}
		analysis := AnalyzeScriptAST(sfc.ScriptAST)
		// {#client} content and client-only components need the client.
		own[key] = sfc.ClientOnly || ssrHasClientBlock(sfc.TemplateAST.Nodes) || ssrOwnInteractive(sfc.TemplateAST.Nodes, analysis)
		links, _ := ResolveSSRLocalComponentLinks(src, srcRoot, analysis)
		for _, link := range links {
			children[key] = append(children[key], canonicalPath(link.SourcePath))
		}
	}
	interactive := map[string]bool{}
	for src, value := range own {
		interactive[src] = value
	}
	for changed := true; changed; {
		changed = false
		for src, kids := range children {
			if interactive[src] {
				continue
			}
			for _, kid := range kids {
				if interactive[kid] {
					interactive[src], changed = true, true
					break
				}
			}
		}
	}
	return interactive
}

func canonicalPath(file string) string {
	abs, err := filepath.Abs(file)
	if err != nil {
		return file
	}
	return abs
}

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
func writeSSRManifest(files []string, srcRoot string, layout SSRProjectLayout, modes map[string]bool, imports *packageImports) error {
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
		module, err := filepath.Rel(outRoot, mirrorPath(src, srcRoot, outRoot, ".js"))
		if err != nil {
			module = filepath.Base(src)
		}
		mode := ""
		if modes != nil {
			mode = "static"
			if ssrInteractive(modes, src) {
				mode = "interactive"
			}
		}
		manifest.Components = append(manifest.Components, SSRManifestComponent{
			Component: componentName(src), Source: filepath.ToSlash(rel), Mode: mode, Module: filepath.ToSlash(module),
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

// ssrInteractive looks up a component's mode by its path as given or
// canonical; an unknown component is interactive.
func ssrInteractive(modes map[string]bool, src string) bool {
	if modes == nil {
		return true
	}
	if value, ok := modes[src]; ok {
		return value
	}
	if value, ok := modes[canonicalPath(src)]; ok {
		return value
	}
	return true
}

// ssrHasClientBlock reports a {#client} block anywhere in a template.
func ssrHasClientBlock(nodes []Node) bool {
	found := false
	walkTemplateNodes(nodes, func(node Node) {
		if chain, ok := node.(*IfChainNode); ok && chain.Client {
			found = true
		}
	})
	return found
}

// walkTemplateNodes visits every node of a template tree.
func walkTemplateNodes(nodes []Node, visit func(Node)) {
	for _, node := range nodes {
		visit(node)
		switch n := node.(type) {
		case *ElementNode:
			walkTemplateNodes(n.Children, visit)
		case *IfChainNode:
			for _, branch := range n.Branches {
				walkTemplateNodes(branch.Children, visit)
			}
		case *EachBlockNode:
			walkTemplateNodes(n.Children, visit)
			walkTemplateNodes(n.ElseChildren, visit)
		case *AwaitBlockNode:
			walkTemplateNodes(n.PendingChildren, visit)
			walkTemplateNodes(n.ThenChildren, visit)
			walkTemplateNodes(n.CatchChildren, visit)
		case *ComponentNode:
			for _, slot := range n.Slots {
				walkTemplateNodes(slot, visit)
			}
		case *SlotNode:
			walkTemplateNodes(n.Fallback, visit)
		}
	}
}
