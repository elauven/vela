package main

import (
	"fmt"
	"os"
)

// buildSSRProject adds server and styles products around the established CSR
// project build. The caller owns user-facing progress and exit handling.
func buildSSRProject(files []string, srcRoot, outRoot string, opts CompileOptions, parallel bool) int {
	hosts, err := LoadSSRHosts(srcRoot, files)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  x  SSR project: %v\n", err)
		return 1
	}
	if opts.SSRModule == "" || (opts.SSRModule == SSRDefaultModule && hosts.Project.Config.Module != "") {
		// vela.ssr.json sets the module unless --ssr-module overrides it.
		opts.SSRModule = hosts.Project.Config.Module
	}
	defer UseSSRRuntimeModule(hosts.Project.Config.Runtime.Module)()
	layout := NewSSRProjectLayoutWithModule(outRoot, opts.SSRModule)
	clientOpts := opts
	clientOpts.Hosts = hosts
	hosts.Modules = NewSSRModules(layout, srcRoot, hosts)
	// The client module is the ordinary CSR module; hydrate() adopts server
	// HTML with it (C-4.1).
	clientOpts.PackageImports = newPackageImports()
	errors := buildProject(files, srcRoot, layout.ClientDir, clientOpts, parallel)
	hosts.RootModes = SSRRootModes(files, srcRoot)
	for _, problem := range hosts.Problems {
		fmt.Fprintf(os.Stderr, "  x  %s\n", problem.Error())
		errors++
	}
	var suggestions []*SSRSiteDiagnostic
	var report *SSRReportCollector
	if opts.SSRReport != "" {
		report = NewSSRReportCollector()
	}
	hosts.Children = map[string][]string{}
	regions := map[string]*SSRRegion{}
	results := map[string]error{}
	for _, src := range files {
		var region *SSRRegion
		results[src] = CompileSSRProjectFileInto(src, srcRoot, layout, hosts, &region)
		regions[src] = region
	}
	for _, src := range PropagateSSRChildFailures(files, srcRoot, hosts.Children, results) {
		// The renderer was written before its child failed; without it the
		// rest of the Go package still builds.
		_ = os.Remove(layout.ServerPath(src, srcRoot))
		_ = os.Remove(layout.StylePath(src, srcRoot))
	}
	for _, src := range files {
		region, err := regions[src], results[src]
		if IsSSRClientOnly(err) {
			report.AddClientOnly(src, srcRoot)
			fmt.Fprintf(compilerStatusWriter(opts), "  +  %-40s -> client only\n", RelOrBase(src, srcRoot))
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
			fmt.Fprintf(os.Stderr, "  x  %s: SSR: %v\n", RelOrBase(src, srcRoot), SSRDiagnosticSummary(err))
			if detail := SSRDiagnosticDetail(hosts.Project.Relative(src), err); detail != "" {
				fmt.Fprintln(os.Stderr, detail)
			}
			errors++
			continue
		}
		fmt.Fprintf(compilerStatusWriter(opts), "  +  %-40s -> server/styles\n", RelOrBase(src, srcRoot))
	}
	if err := hosts.Modules.Write(); err != nil {
		fmt.Fprintf(os.Stderr, "  x  SSR modules: %v\n", err)
		errors++
	}
	if errors == 0 {
		if err := ValidateSSRProjectPackagesWithHosts(layout, hosts.Types); err != nil {
			fmt.Fprintf(os.Stderr, "  x  SSR package validation: %v\n", err)
			errors++
		}
	}
	if err := writeSSRManifest(files, srcRoot, layout, hosts.RootModes, clientOpts.PackageImports); err != nil {
		fmt.Fprintf(os.Stderr, "  x  SSR manifest: %v\n", err)
		errors++
	}
	if opts.SSRScaffoldHosts {
		for _, created := range ScaffoldSSRHosts(hosts.Project, suggestions) {
			fmt.Fprintf(compilerStatusWriter(opts), "  +  host stub %s\n", created)
		}
	}
	if report != nil {
		if err := report.Write(opts.SSRReport); err != nil {
			fmt.Fprintf(os.Stderr, "  x  SSR report: %v\n", err)
			errors++
		}
	}
	return errors
}
