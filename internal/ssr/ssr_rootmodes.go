package ssr

import (
	"github.com/elauven/vela/internal/analyzer"
	"github.com/elauven/vela/internal/codegen"
	"github.com/elauven/vela/internal/sfcast"

	"os"
	"path/filepath"
)

// Root modes (docs/HYDRATION_ARCHITECTURE.md A-8.1–A-8.7, task 041). A
// component is static as a root when nothing in its component tree can
// change after rendering: no DOM or component events, no bind:, no $effect,
// no lifecycle hook. Rendered as a root it writes plain HTML without markers
// or payload and needs no JavaScript. Inside an interactive parent it keeps
// the hydration markup, because the parent's client code creates it.

// ssrOwnInteractive reports whether a component itself needs client code.
func ssrOwnInteractive(nodes []sfcast.Node, analysis *analyzer.Analysis) bool {
	if analysis != nil && (len(analysis.Effects) > 0 || len(analysis.Lifecycles) > 0) {
		return true
	}
	return ssrTemplateInteractive(nodes)
}

func ssrTemplateInteractive(nodes []sfcast.Node) bool {
	for _, node := range nodes {
		var attrs []*sfcast.Attr
		switch n := node.(type) {
		case *sfcast.ElementNode:
			attrs = n.Attrs
		case *sfcast.ComponentNode:
			attrs = n.Attrs
		}
		for _, attr := range attrs {
			if attr.IsEvent || attr.IsTwoWay || attr.IsBindThis {
				return true
			}
		}
		interactive := false
		codegen.ForEachChildList(node, func(children []sfcast.Node) {
			interactive = interactive || ssrTemplateInteractive(children)
		})
		if interactive {
			return true
		}
	}
	return false
}

// SSRRootModes decides the root mode of every component of a project:
// interactive when the component or any component it uses is.
func SSRRootModes(files []string, srcRoot string) map[string]bool {
	own := map[string]bool{}
	children := map[string][]string{}
	for _, src := range files {
		key := canonicalPath(src)
		data, err := os.ReadFile(src)
		if err != nil {
			own[key] = true
			continue
		}
		sfc, err := sfcast.ParseSFC(string(data))
		if err != nil || sfc.TemplateAST == nil {
			own[key] = true
			continue
		}
		analysis := analyzer.AnalyzeScriptAST(sfc.ScriptAST)
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

// SSRInteractive looks up a component's mode by its path as given or
// canonical; an unknown component is interactive.
func SSRInteractive(modes map[string]bool, src string) bool {
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
func ssrHasClientBlock(nodes []sfcast.Node) bool {
	found := false
	walkTemplateNodes(nodes, func(node sfcast.Node) {
		if chain, ok := node.(*sfcast.IfChainNode); ok && chain.Client {
			found = true
		}
	})
	return found
}

// walkTemplateNodes visits every node of a template tree.
func walkTemplateNodes(nodes []sfcast.Node, visit func(sfcast.Node)) {
	for _, node := range nodes {
		visit(node)
		switch n := node.(type) {
		case *sfcast.ElementNode:
			walkTemplateNodes(n.Children, visit)
		case *sfcast.IfChainNode:
			for _, branch := range n.Branches {
				walkTemplateNodes(branch.Children, visit)
			}
		case *sfcast.EachBlockNode:
			walkTemplateNodes(n.Children, visit)
			walkTemplateNodes(n.ElseChildren, visit)
		case *sfcast.AwaitBlockNode:
			walkTemplateNodes(n.PendingChildren, visit)
			walkTemplateNodes(n.ThenChildren, visit)
			walkTemplateNodes(n.CatchChildren, visit)
		case *sfcast.ComponentNode:
			for _, slot := range n.Slots {
				walkTemplateNodes(slot, visit)
			}
		case *sfcast.SlotNode:
			walkTemplateNodes(n.Fallback, visit)
		}
	}
}
