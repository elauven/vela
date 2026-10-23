package compiler

import (
	"github.com/elauven/vela/internal/diag"
	"github.com/elauven/vela/internal/project"
	"github.com/elauven/vela/internal/ssr"

	"fmt"
	"os"
	"path/filepath"
)

// BuildSSRProject adds server and styles products around the established CSR
// project build. The caller owns user-facing progress and exit handling.
func BuildSSRProject(files []string, srcRoot, outRoot string, opts CompileOptions, parallel bool) int {
	hosts, err := ssr.LoadSSRHosts(srcRoot, files)
	if err != nil {
		fmt.Fprintf(ErrorWriter(opts), "  x  SSR project: %v\n", err)
		opts.Report.add(buildIssue(srcRoot, "SSR project: %v", err))
		return 1
	}
	if opts.SSRModule == "" || (opts.SSRModule == ssr.SSRDefaultModule && hosts.Project.Config.Module != "") {
		// vela.ssr.json sets the module unless --ssr-module overrides it.
		opts.SSRModule = hosts.Project.Config.Module
	}
	defer ssr.UseSSRRuntimeModule(hosts.Project.Config.Runtime.Module)()
	layout := ssr.NewSSRProjectLayoutWithModule(outRoot, opts.SSRModule)
	clientOpts := opts
	clientOpts.Hosts = hosts
	hosts.Modules = ssr.NewSSRModules(layout, srcRoot, hosts)
	// The client module is the ordinary CSR module; hydrate() adopts server
	// HTML with it (C-4.1).
	clientOpts.PackageImports = newPackageImports()
	errors := BuildProject(files, srcRoot, layout.ClientDir, clientOpts, parallel)
	hosts.RootModes = ssr.SSRRootModes(files, srcRoot)
	for _, problem := range hosts.Problems {
		fmt.Fprintf(ErrorWriter(opts), "  x  %s\n", problem.Error())
		opts.Report.add(&diag.Diagnostic{Sev: diag.SevError, File: cwdRelative(filepath.Join(hosts.Project.Root, filepath.FromSlash(problem.File))), Line: problem.Line, Col: 1, Code: problem.Code, Message: problem.Message})
		errors++
	}
	var suggestions []*ssr.SSRSiteDiagnostic
	var report *ssr.SSRReportCollector
	if opts.SSRReport != "" {
		report = ssr.NewSSRReportCollector()
	}
	hosts.Children = map[string][]string{}
	regions := map[string]*ssr.SSRRegion{}
	results := map[string]error{}
	for _, src := range files {
		var region *ssr.SSRRegion
		results[src] = ssr.CompileSSRProjectFileInto(src, srcRoot, layout, hosts, &region)
		regions[src] = region
	}
	for _, src := range ssr.PropagateSSRChildFailures(files, srcRoot, hosts.Children, results) {
		// The renderer was written before its child failed; without it the
		// rest of the Go package still builds.
		_ = os.Remove(layout.ServerPath(src, srcRoot))
		_ = os.Remove(layout.StylePath(src, srcRoot))
	}
	for _, src := range files {
		region, err := regions[src], results[src]
		if ssr.IsSSRClientOnly(err) {
			report.AddClientOnly(src, srcRoot)
			fmt.Fprintf(CompilerStatusWriter(opts), "  +  %-40s -> client only\n", project.RelOrBase(src, srcRoot))
			continue
		}
		if region != nil {
			for _, record := range region.Records {
				if record.Diagnostic != nil && record.Diagnostic.Host != nil {
					suggestions = append(suggestions, record.Diagnostic)
				}
			}
		}
		report.Add(src, srcRoot, region, err)
		if err != nil {
			fmt.Fprintf(ErrorWriter(opts), "  x  %s: SSR: %v\n", project.RelOrBase(src, srcRoot), ssr.SSRDiagnosticSummary(err))
			if detail := ssr.SSRDiagnosticDetail(hosts.Project.Relative(src), err); detail != "" {
				fmt.Fprintln(ErrorWriter(opts), detail)
			}
			opts.Report.add(ssr.SSRErrorDiagnostics(src, err)...)
			errors++
			continue
		}
		fmt.Fprintf(CompilerStatusWriter(opts), "  +  %-40s -> server/styles\n", project.RelOrBase(src, srcRoot))
	}
	if err := hosts.Modules.Write(); err != nil {
		fmt.Fprintf(ErrorWriter(opts), "  x  SSR modules: %v\n", err)
		opts.Report.add(buildIssue(srcRoot, "SSR modules: %v", err))
		errors++
	}
	if errors == 0 {
		if err := ssr.ValidateSSRProjectPackagesWithHosts(layout, hosts.Types); err != nil {
			fmt.Fprintf(ErrorWriter(opts), "  x  SSR package validation: %v\n", err)
			opts.Report.add(buildIssue(srcRoot, "SSR package validation: %v", err))
			errors++
		}
	}
	if err := writeSSRManifest(files, srcRoot, layout, hosts.RootModes, clientOpts.PackageImports); err != nil {
		fmt.Fprintf(ErrorWriter(opts), "  x  SSR manifest: %v\n", err)
		opts.Report.add(buildIssue(srcRoot, "SSR manifest: %v", err))
		errors++
	}
	if opts.SSRScaffoldHosts {
		for _, created := range ssr.ScaffoldSSRHosts(hosts.Project, suggestions) {
			fmt.Fprintf(CompilerStatusWriter(opts), "  +  host stub %s\n", created)
		}
	}
	if report != nil {
		if err := report.Write(opts.SSRReport); err != nil {
			fmt.Fprintf(ErrorWriter(opts), "  x  SSR report: %v\n", err)
			opts.Report.add(buildIssue(srcRoot, "SSR report: %v", err))
			errors++
		}
	}
	return errors
}
