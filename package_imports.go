package main

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// packageImports collects the package specifiers that the modules of a
// project build import (vela.manifest.json, task 048). Components compile in
// parallel, so it is safe for concurrent use; a nil collector ignores adds.
type packageImports struct {
	mu      sync.Mutex
	modules map[string][]string // output file → specifiers
}

func newPackageImports() *packageImports {
	return &packageImports{modules: map[string][]string{}}
}

func (p *packageImports) add(module string, specs []string) {
	if p == nil || len(specs) == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.modules[module] = specs
}

// manifest returns the modules relative to outRoot and the package names,
// both sorted.
func (p *packageImports) manifest(outRoot string) ([]ManifestModule, []string) {
	if p == nil {
		return nil, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var modules []ManifestModule
	names := map[string]bool{}
	for file, specs := range p.modules {
		rel, err := filepath.Rel(outRoot, file)
		if err != nil {
			rel = file
		}
		sorted := append([]string(nil), specs...)
		sort.Strings(sorted)
		modules = append(modules, ManifestModule{Module: filepath.ToSlash(rel), Packages: sorted})
		for _, spec := range specs {
			names[packageName(spec)] = true
		}
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].Module < modules[j].Module })
	var packages []string
	for name := range names {
		packages = append(packages, name)
	}
	sort.Strings(packages)
	return modules, packages
}

// packageName is the npm package of a bare specifier: `date-fns/format` →
// `date-fns`, `@scope/pkg/sub` → `@scope/pkg`.
func packageName(spec string) string {
	parts := strings.SplitN(spec, "/", 3)
	if strings.HasPrefix(spec, "@") && len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}

// programPackageImports lists the package specifiers of a generated module:
// static imports, re-exports and literal import(), without the runtime
// package of --runtime-import=bare.
func programPackageImports(prog *JsProgram) []string {
	seen := map[string]bool{}
	var specs []string
	add := func(spec string) {
		if isPackageSpecifier(spec) && packageName(spec) != RuntimePackageName && !seen[spec] {
			seen[spec] = true
			specs = append(specs, spec)
		}
	}
	WalkNode(prog, func(n JsNode) bool {
		switch x := n.(type) {
		case *JsImportDecl:
			add(x.Source)
		case *JsExportDecl:
			if x.HasSource {
				add(x.Source)
			}
		case *JsCallExpr:
			if callee, ok := x.Callee.(*JsIdent); ok && callee.Name == "import" && len(x.Args) > 0 {
				if lit, ok := x.Args[0].(*JsLiteral); ok && lit.Kind == "string" && len(lit.Raw) >= 2 {
					add(lit.Raw[1 : len(lit.Raw)-1])
				}
			}
		}
		return true
	})
	return specs
}

// sourcePackageImports lists the package specifiers of a copied JavaScript
// module.
func sourcePackageImports(src string) []string {
	seen := map[string]bool{}
	var specs []string
	for _, spec := range moduleSpecifiers(src) {
		if isPackageSpecifier(spec.Value) && !seen[spec.Value] {
			seen[spec.Value] = true
			specs = append(specs, spec.Value)
		}
	}
	return specs
}
