package ssr

import (
	"github.com/elauven/vela/internal/analyzer"
	"github.com/elauven/vela/internal/codegen"
	"github.com/elauven/vela/internal/project"
	"github.com/elauven/vela/internal/sfcast"

	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// PropagateSSRChildFailures marks a component whose renderer calls a child
// without a Go renderer (its SSR compilation failed) as failed too: its
// generated package would not build, and the report would call it ok
// (task 031). Children are matched by absolute path.
func PropagateSSRChildFailures(files []string, srcRoot string, children map[string][]string, results map[string]error) (marked []string) {
	failed := map[string]string{}
	for _, list := range children {
		sort.Strings(list)
	}
	for _, src := range files {
		if err := results[src]; err != nil && !IsSSRClientOnly(err) {
			failed[ssrAbs(src)] = src
		}
	}
	for changed := true; changed; {
		changed = false
		for _, src := range files {
			if results[src] != nil {
				continue
			}
			for _, child := range children[src] {
				if failedSrc, ok := failed[ssrAbs(child)]; ok {
					results[src] = fmt.Errorf("VELA-SSR-CHILD: child component %s has no server renderer; fix its SSR diagnostics or declare it <script context=\"client\">", filepath.ToSlash(project.RelOrBase(failedSrc, srcRoot)))
					failed[ssrAbs(src)] = src
					marked = append(marked, src)
					changed = true
					break
				}
			}
		}
	}
	return marked
}

func ssrAbs(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

func CompileSSRProjectFile(src, srcRoot string, layout SSRProjectLayout) error {
	hosts, err := LoadSSRHosts(srcRoot, project.CollectVela(srcRoot))
	if err != nil {
		return err
	}
	defer UseSSRRuntimeModule(hosts.Project.Config.Runtime.Module)()
	return CompileSSRProjectFileInto(src, srcRoot, layout, hosts, nil)
}

// CompileSSRProjectFileInto also stores the region site records of the
// component in regionOut when the render plan exists, whatever the result.
func CompileSSRProjectFileInto(src, srcRoot string, layout SSRProjectLayout, hosts *SSRHosts, regionOut **SSRRegion) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	sfc, err := sfcast.ParseSFC(string(data))
	if err != nil {
		return fmt.Errorf("parse component: %w", err)
	}
	if sfc.ClientOnly {
		// A client-only component has no Go renderer (T-9.2a).
		return errSSRClientOnly
	}
	hash := md5.Sum([]byte(src))
	scopeID := fmt.Sprintf("vela-%x", hash[:3])
	analysis := analyzer.AnalyzeScriptAST(sfc.ScriptAST)
	links, err := ResolveSSRLocalComponentLinks(src, srcRoot, analysis)
	if err != nil {
		return err
	}
	links = LinkSSRComponentPackages(links, srcRoot, layout)
	if hosts.Children != nil {
		for _, link := range links {
			if !link.ClientOnly {
				hosts.Children[src] = append(hosts.Children[src], link.SourcePath)
			}
		}
	}
	codegen.NormalizeTableRows(sfc.TemplateAST.Nodes)
	if err := checkSSRBlocksNesting(src, string(data), sfc.TemplateAST.Nodes, links); err != nil {
		return err
	}
	plan, err := BuildSSRRenderPlanWithAnalysisAndLinks(project.ComponentName(src), sfc, analysis, scopeID, links)
	if err != nil {
		return err
	}
	bindSSRHosts(plan, hosts, src, analysis)
	if plan.PropsWithoutPresence, err = ssrPropsWithoutPresence(layout.PropsPath(src, srcRoot), plan.ComponentName+"Props"); err != nil {
		return err
	}
	if hosts.RootModes != nil {
		plan.RootStatic = !SSRInteractive(hosts.RootModes, src)
	}
	// The region records are kept whether or not the renderer succeeds, so
	// the report covers every component.
	file, region, renderErr := buildSSRGoRendererWithRegion(plan, layout.PackageName(src, srcRoot), hosts.Project.Relative(src), string(data))
	region.locate(string(data))
	if regionOut != nil {
		*regionOut = region
	}
	if renderErr != nil {
		return renderErr
	}
	return writeSSRBlocksProjectFile(src, srcRoot, layout, plan, file)
}
